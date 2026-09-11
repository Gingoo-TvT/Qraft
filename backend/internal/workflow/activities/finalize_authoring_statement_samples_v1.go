package activities

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	speccontract "github.com/Gingoo-TvT/Qraft/backend/internal/spec"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
)

const (
	FinalizeAuthoringStatementSamplesPayloadVersion  = 1
	FinalAuthoringStatementSamplesSchemaV1           = "algoforge.final-authoring-statement-samples.v1"
	FinalAuthoringStatementSamplesStatusV1           = "parseback_second_run_verified_final"
	finalAuthoringStatementSamplesRuleV1             = "algoforge.statement-samples.parseback-second-run.v1"
	finalizeAuthoringStatementSamplesContractErrorV1 = "FinalizeAuthoringStatementSamplesContractError"
	maxFinalAuthoringStatementSamplesBytesV1         = 4 << 20
)

type FinalizeAuthoringStatementSamplesInput struct {
	PayloadVersion             int                         `json:"payload_version"`
	StagedBundleArtifact       ArtifactRef                 `json:"staged_bundle_artifact"`
	ExpectedStagedBundleSHA256 string                      `json:"expected_staged_bundle_sha256"`
	SecondRun                  AuthoringSampleSandboxRunV1 `json:"second_run"`
}

type FinalAuthoringStatementSamplesBundleV1 struct {
	SchemaVersion                  string                             `json:"schema_version"`
	Status                         string                             `json:"status"`
	FinalizeInputSHA256            string                             `json:"finalize_input_sha256"`
	StagedBundleSHA256             string                             `json:"staged_bundle_sha256"`
	StageInputSHA256               string                             `json:"stage_input_sha256"`
	AuthoringBundleSHA256          string                             `json:"authoring_bundle_sha256"`
	StatementDraftSHA256           string                             `json:"statement_draft_sha256"`
	ValidatedSamplesSHA256         string                             `json:"validated_samples_sha256"`
	VerifiedProgramReceiptSHA256   string                             `json:"verified_program_receipt_sha256"`
	FirstRunSHA256                 string                             `json:"first_run_sha256"`
	SecondRunSHA256                string                             `json:"second_run_sha256"`
	ProgramSHA256                  string                             `json:"program_sha256"`
	IndependentOracleReceiptSHA256 string                             `json:"independent_oracle_receipt_sha256"`
	SandboxImageDigest             string                             `json:"sandbox_image_digest"`
	ToolchainManifestDigest        string                             `json:"toolchain_manifest_digest"`
	SeccompPolicyDigest            string                             `json:"seccomp_policy_digest"`
	LimitProfile                   string                             `json:"limit_profile"`
	SemanticSpecSHA256             string                             `json:"semantic_spec_sha256"`
	InputGrammarSHA256             string                             `json:"input_grammar_sha256"`
	SampleCount                    int                                `json:"sample_count"`
	Samples                        []AuthoringStatementSampleRecordV1 `json:"samples"`
	Markdown                       string                             `json:"markdown"`
	MarkdownSHA256                 string                             `json:"markdown_sha256"`
	DerivationRule                 string                             `json:"derivation_rule"`
}

type FinalizeAuthoringStatementSamplesResult struct {
	PayloadVersion      int            `json:"payload_version"`
	Status              string         `json:"status"`
	FinalizeInputSHA256 string         `json:"finalize_input_sha256"`
	SampleCount         int            `json:"sample_count"`
	MarkdownSHA256      string         `json:"markdown_sha256"`
	FinalBundleSHA256   string         `json:"final_bundle_sha256"`
	FinalBundleArtifact *ArtifactRef   `json:"final_bundle_artifact"`
	SourceArtifacts     []*ArtifactRef `json:"source_artifacts"`
}

func (a *Activities) FinalizeAuthoringStatementSamplesActivityV1(
	ctx context.Context,
	in FinalizeAuthoringStatementSamplesInput,
) (*FinalizeAuthoringStatementSamplesResult, error) {
	if in.PayloadVersion != FinalizeAuthoringStatementSamplesPayloadVersion {
		return nil, nonRetryableFinalizeAuthoringSamplesErrorV1(fmt.Errorf("unsupported finalize payload version %d", in.PayloadVersion))
	}
	if a == nil || a.artifacts == nil {
		return nil, fmt.Errorf("artifact store is not configured")
	}
	if !statementDraftIsSHA256V1(in.ExpectedStagedBundleSHA256) ||
		in.StagedBundleArtifact.SHA256 != in.ExpectedStagedBundleSHA256 ||
		in.StagedBundleArtifact.ContentType != "application/json" ||
		in.StagedBundleArtifact.Producer != "StageAuthoringStatementSamplesActivityV1" ||
		in.StagedBundleArtifact.LLMCallReceipt != nil || in.StagedBundleArtifact.SizeBytes <= 0 ||
		in.StagedBundleArtifact.SizeBytes > maxStagedAuthoringStatementSamplesBytesV1 {
		return nil, nonRetryableFinalizeAuthoringSamplesErrorV1(fmt.Errorf("staged bundle artifact binding/type mismatch"))
	}
	if err := in.StagedBundleArtifact.Validate(in.StagedBundleArtifact.Bucket); err != nil {
		return nil, nonRetryableFinalizeAuthoringSamplesErrorV1(fmt.Errorf("invalid staged bundle artifact: %w", err))
	}

	activity.RecordHeartbeat(ctx, "reading staged statement and parsing samples back")
	stagedBytes, err := a.artifacts.Get(ctx, in.StagedBundleArtifact)
	if err != nil {
		return nil, fmt.Errorf("read staged statement samples: %w", err)
	}
	if len(stagedBytes) > maxStagedAuthoringStatementSamplesBytesV1 ||
		int64(len(stagedBytes)) != in.StagedBundleArtifact.SizeBytes || sha256Hex(stagedBytes) != in.ExpectedStagedBundleSHA256 {
		return nil, nonRetryableFinalizeAuthoringSamplesErrorV1(fmt.Errorf("staged bundle bytes/identity mismatch"))
	}
	var staged StagedAuthoringStatementSamplesBundleV1
	if err := decodeCanonicalSampleClosureJSONV1(stagedBytes, &staged); err != nil {
		return nil, nonRetryableFinalizeAuthoringSamplesErrorV1(fmt.Errorf("decode staged statement samples: %w", err))
	}
	parsed, receipt, err := validateAndParseStagedAuthoringSamplesV1(staged)
	if err != nil {
		return nil, nonRetryableFinalizeAuthoringSamplesErrorV1(err)
	}

	expectedInputSHA := make([]string, len(parsed))
	for index := range parsed {
		expectedInputSHA[index] = sha256Hex([]byte(parsed[index].Input))
	}
	if err := validateAuthoringSampleSandboxRunV1(in.SecondRun, AuthoringSampleSandboxRunPhaseReparse, receipt, staged.VerifiedProgramReceiptSHA256, expectedInputSHA); err != nil {
		return nil, nonRetryableFinalizeAuthoringSamplesErrorV1(err)
	}
	secondOutputs, err := a.resolveAuthoringSampleSandboxOutputsV1(ctx, in.SecondRun.SandboxOutput, in.StagedBundleArtifact.Bucket)
	if err != nil {
		return nil, nonRetryableFinalizeAuthoringSamplesErrorV1(err)
	}
	for index := range parsed {
		if secondOutputs[index] != parsed[index].Output {
			return nil, sampleClosureQualityErrorV1("sample %d output differs between first and parse-back sandbox runs", index+1)
		}
	}
	secondRunSHA, err := canonicalJSONSHA256(in.SecondRun)
	if err != nil {
		return nil, nonRetryableFinalizeAuthoringSamplesErrorV1(fmt.Errorf("hash second sandbox run: %w", err))
	}
	finalizeInputSHA, err := canonicalJSONSHA256(in)
	if err != nil {
		return nil, nonRetryableFinalizeAuthoringSamplesErrorV1(fmt.Errorf("hash finalize input: %w", err))
	}
	finalBundle := FinalAuthoringStatementSamplesBundleV1{
		SchemaVersion: FinalAuthoringStatementSamplesSchemaV1, Status: FinalAuthoringStatementSamplesStatusV1,
		FinalizeInputSHA256: finalizeInputSHA, StagedBundleSHA256: in.ExpectedStagedBundleSHA256,
		StageInputSHA256: staged.StageInputSHA256, AuthoringBundleSHA256: staged.AuthoringBundleSHA256,
		StatementDraftSHA256: staged.StatementDraftSHA256, ValidatedSamplesSHA256: staged.ValidatedSamplesSHA256,
		VerifiedProgramReceiptSHA256: staged.VerifiedProgramReceiptSHA256,
		FirstRunSHA256:               staged.FirstRunSHA256, SecondRunSHA256: secondRunSHA,
		ProgramSHA256: staged.ProgramSHA256, IndependentOracleReceiptSHA256: staged.IndependentOracleReceiptSHA256,
		SandboxImageDigest: staged.SandboxImageDigest, ToolchainManifestDigest: staged.ToolchainManifestDigest,
		SeccompPolicyDigest: staged.SeccompPolicyDigest, LimitProfile: staged.LimitProfile,
		SemanticSpecSHA256: staged.SemanticSpecSHA256, InputGrammarSHA256: staged.InputGrammarSHA256,
		SampleCount: staged.SampleCount, Samples: cloneAuthoringStatementSampleRecordsV1(staged.Samples),
		Markdown: staged.Markdown, MarkdownSHA256: staged.MarkdownSHA256,
		DerivationRule: finalAuthoringStatementSamplesRuleV1,
	}
	finalBytes, err := json.Marshal(finalBundle)
	if err != nil {
		return nil, fmt.Errorf("marshal final statement samples: %w", err)
	}
	if len(finalBytes) > maxFinalAuthoringStatementSamplesBytesV1 {
		return nil, sampleClosureQualityErrorV1("final statement exceeds %d bytes", maxFinalAuthoringStatementSamplesBytesV1)
	}
	metadata := artifactMetadataFromActivity(ctx)
	metadata.ArtifactType = "qg03_final_statement_samples"
	metadata.SourceType = "derived_parseback_verified_statement"
	metadata.RetentionClass = "workflow_cas_unreviewed"
	metadata.ProvenanceMetadata, _ = json.Marshal(map[string]interface{}{
		"finalize_input_sha256": finalizeInputSHA, "staged_bundle_sha256": in.ExpectedStagedBundleSHA256,
		"verified_program_receipt_sha256": staged.VerifiedProgramReceiptSHA256,
		"first_run_sha256":                staged.FirstRunSHA256, "second_run_sha256": secondRunSHA,
		"sample_count": staged.SampleCount, "markdown_sha256": staged.MarkdownSHA256,
	})
	finalArtifact, err := a.putArtifactWithMetadata(ctx, finalBytes, "application/json", metadata)
	if err != nil {
		return nil, fmt.Errorf("store final statement samples: %w", err)
	}
	finalSHA := sha256Hex(finalBytes)
	if finalArtifact == nil || finalArtifact.SHA256 != finalSHA || finalArtifact.SizeBytes != int64(len(finalBytes)) || finalArtifact.Producer != "FinalizeAuthoringStatementSamplesActivityV1" {
		return nil, fmt.Errorf("final statement-samples CAS identity mismatch")
	}
	stagedRef := in.StagedBundleArtifact
	return &FinalizeAuthoringStatementSamplesResult{
		PayloadVersion: FinalizeAuthoringStatementSamplesPayloadVersion, Status: FinalAuthoringStatementSamplesStatusV1,
		FinalizeInputSHA256: finalizeInputSHA, SampleCount: staged.SampleCount, MarkdownSHA256: staged.MarkdownSHA256,
		FinalBundleSHA256: finalSHA, FinalBundleArtifact: finalArtifact,
		SourceArtifacts: []*ArtifactRef{&stagedRef, finalArtifact},
	}, nil
}

func validateAndParseStagedAuthoringSamplesV1(staged StagedAuthoringStatementSamplesBundleV1) ([]speccontract.StatementSampleV1, VerifiedProgramReceiptV1, error) {
	if staged.SchemaVersion != StagedAuthoringStatementSamplesSchemaV1 || staged.Status != StagedAuthoringStatementSamplesStatusV1 || staged.DerivationRule != stagedAuthoringStatementSamplesRuleV1 {
		return nil, VerifiedProgramReceiptV1{}, fmt.Errorf("unsupported staged statement schema/status")
	}
	for name, value := range map[string]string{
		"stage input": staged.StageInputSHA256, "authoring bundle": staged.AuthoringBundleSHA256,
		"statement draft": staged.StatementDraftSHA256, "draft markdown": staged.StatementDraftMarkdownSHA256,
		"validated samples": staged.ValidatedSamplesSHA256, "verified program receipt": staged.VerifiedProgramReceiptSHA256,
		"first run": staged.FirstRunSHA256, "program": staged.ProgramSHA256,
		"independent oracle": staged.IndependentOracleReceiptSHA256, "semantic spec": staged.SemanticSpecSHA256,
		"input grammar": staged.InputGrammarSHA256, "markdown": staged.MarkdownSHA256,
	} {
		if !statementDraftIsSHA256V1(value) {
			return nil, VerifiedProgramReceiptV1{}, fmt.Errorf("staged %s SHA-256 is invalid", name)
		}
	}
	if staged.SampleCount < 0 || staged.SampleCount > domain.MaxGeneratedTestCases || staged.SampleCount != len(staged.Samples) || staged.MarkdownSHA256 != sha256Hex([]byte(staged.Markdown)) {
		return nil, VerifiedProgramReceiptV1{}, fmt.Errorf("staged sample count/markdown binding mismatch")
	}
	parsed, err := speccontract.ParseStatementSamplesV1(staged.Markdown)
	if err != nil {
		return nil, VerifiedProgramReceiptV1{}, fmt.Errorf("parse staged samples: %w", err)
	}
	if len(parsed) != staged.SampleCount {
		return nil, VerifiedProgramReceiptV1{}, fmt.Errorf("parsed sample count %d does not match staged count %d", len(parsed), staged.SampleCount)
	}
	expected := make([]speccontract.StatementSampleV1, staged.SampleCount)
	for index, record := range staged.Samples {
		if record.Index != index+1 || record.InputSHA256 != sha256Hex([]byte(record.Input)) || record.OutputSHA256 != sha256Hex([]byte(record.Output)) {
			return nil, VerifiedProgramReceiptV1{}, fmt.Errorf("staged sample record %d binding mismatch", index)
		}
		expected[index] = speccontract.StatementSampleV1{Input: record.Input, Output: record.Output}
	}
	if !reflect.DeepEqual(parsed, expected) {
		return nil, VerifiedProgramReceiptV1{}, fmt.Errorf("staged Markdown samples differ from first-run records")
	}
	receipt := VerifiedProgramReceiptV1{
		SchemaVersion: VerifiedProgramReceiptSchemaV1, Status: VerifiedProgramReceiptStatusV1,
		ProgramSHA256: staged.ProgramSHA256, IndependentOracleReceiptSHA256: staged.IndependentOracleReceiptSHA256,
		SandboxImageDigest: staged.SandboxImageDigest, ToolchainManifestDigest: staged.ToolchainManifestDigest,
		SeccompPolicyDigest: staged.SeccompPolicyDigest, LimitProfile: staged.LimitProfile,
	}
	if err := validateVerifiedProgramReceiptV1(receipt); err != nil {
		return nil, VerifiedProgramReceiptV1{}, err
	}
	return parsed, receipt, nil
}

func cloneAuthoringStatementSampleRecordsV1(values []AuthoringStatementSampleRecordV1) []AuthoringStatementSampleRecordV1 {
	cloned := make([]AuthoringStatementSampleRecordV1, len(values))
	copy(cloned, values)
	return cloned
}

func nonRetryableFinalizeAuthoringSamplesErrorV1(err error) error {
	return temporal.NewNonRetryableApplicationError(err.Error(), finalizeAuthoringStatementSamplesContractErrorV1, err)
}
