package activities

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	speccontract "github.com/Gingoo-TvT/Qraft/backend/internal/spec"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
)

const (
	StageAuthoringStatementSamplesPayloadVersion  = 1
	StagedAuthoringStatementSamplesSchemaV1       = "algoforge.staged-authoring-statement-samples.v1"
	StagedAuthoringStatementSamplesStatusV1       = "first_run_bound_not_final"
	stagedAuthoringStatementSamplesRuleV1         = "algoforge.statement-samples.stage-first-run.v1"
	stageAuthoringStatementSamplesContractErrorV1 = "StageAuthoringStatementSamplesContractError"
	maxStagedAuthoringStatementSamplesBytesV1     = 4 << 20
)

type StageAuthoringStatementSamplesInput struct {
	PayloadVersion                       int                         `json:"payload_version"`
	AuthoringBundleArtifact              ArtifactRef                 `json:"authoring_bundle_artifact"`
	ExpectedAuthoringBundleSHA256        string                      `json:"expected_authoring_bundle_sha256"`
	StatementDraftArtifact               ArtifactRef                 `json:"statement_draft_artifact"`
	ExpectedStatementDraftSHA256         string                      `json:"expected_statement_draft_sha256"`
	ValidatedSamplesArtifact             ArtifactRef                 `json:"validated_samples_artifact"`
	ExpectedValidatedSamplesSHA256       string                      `json:"expected_validated_samples_sha256"`
	VerifiedProgramReceiptArtifact       ArtifactRef                 `json:"verified_program_receipt_artifact"`
	ExpectedVerifiedProgramReceiptSHA256 string                      `json:"expected_verified_program_receipt_sha256"`
	FirstRun                             AuthoringSampleSandboxRunV1 `json:"first_run"`
}

type AuthoringStatementSampleRecordV1 struct {
	Index        int    `json:"index"`
	Input        string `json:"input"`
	InputSHA256  string `json:"input_sha256"`
	Output       string `json:"output"`
	OutputSHA256 string `json:"output_sha256"`
}

type StagedAuthoringStatementSamplesBundleV1 struct {
	SchemaVersion                  string                             `json:"schema_version"`
	Status                         string                             `json:"status"`
	StageInputSHA256               string                             `json:"stage_input_sha256"`
	AuthoringBundleSHA256          string                             `json:"authoring_bundle_sha256"`
	StatementDraftSHA256           string                             `json:"statement_draft_sha256"`
	StatementDraftMarkdownSHA256   string                             `json:"statement_draft_markdown_sha256"`
	ValidatedSamplesSHA256         string                             `json:"validated_samples_sha256"`
	VerifiedProgramReceiptSHA256   string                             `json:"verified_program_receipt_sha256"`
	FirstRunSHA256                 string                             `json:"first_run_sha256"`
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

type StageAuthoringStatementSamplesResult struct {
	PayloadVersion       int            `json:"payload_version"`
	Status               string         `json:"status"`
	StageInputSHA256     string         `json:"stage_input_sha256"`
	SampleCount          int            `json:"sample_count"`
	MarkdownSHA256       string         `json:"markdown_sha256"`
	StagedBundleSHA256   string         `json:"staged_bundle_sha256"`
	StagedBundleArtifact *ArtifactRef   `json:"staged_bundle_artifact"`
	SourceArtifacts      []*ArtifactRef `json:"source_artifacts"`
}

func (a *Activities) StageAuthoringStatementSamplesActivityV1(
	ctx context.Context,
	in StageAuthoringStatementSamplesInput,
) (*StageAuthoringStatementSamplesResult, error) {
	if in.PayloadVersion != StageAuthoringStatementSamplesPayloadVersion {
		return nil, nonRetryableStageAuthoringSamplesErrorV1(fmt.Errorf("unsupported stage payload version %d", in.PayloadVersion))
	}
	if a == nil || a.artifacts == nil {
		return nil, fmt.Errorf("artifact store is not configured")
	}
	stageInputSHA, err := canonicalJSONSHA256(in)
	if err != nil {
		return nil, nonRetryableStageAuthoringSamplesErrorV1(fmt.Errorf("hash stage input: %w", err))
	}
	bucket := in.AuthoringBundleArtifact.Bucket

	activity.RecordHeartbeat(ctx, "reading QG-02/QG-03 source artifacts")
	authoringBytes, err := a.readBoundSampleClosureArtifactV1(ctx, in.AuthoringBundleArtifact, in.ExpectedAuthoringBundleSHA256, bucket, "GenerateAuthoringPlanActivity", maxAuthoringPlanBundleBytes)
	if err != nil {
		return nil, err
	}
	var authoring AuthoringPlanBundleV1
	if err := decodeCanonicalSampleClosureJSONV1(authoringBytes, &authoring); err != nil {
		return nil, nonRetryableStageAuthoringSamplesErrorV1(fmt.Errorf("decode authoring bundle: %w", err))
	}
	semanticSHA, err := validateAuthoringBundleForSampleClosureV1(authoring, in.ExpectedAuthoringBundleSHA256)
	if err != nil {
		return nil, nonRetryableStageAuthoringSamplesErrorV1(err)
	}
	if err := speccontract.ValidateSampleInputProfileV1(*authoring.SemanticSpec); err != nil {
		return nil, sampleClosureQualityErrorV1("unsupported QG-03 input profile: %v", err)
	}

	draftBytes, err := a.readBoundSampleClosureArtifactV1(ctx, in.StatementDraftArtifact, in.ExpectedStatementDraftSHA256, bucket, "RenderStatementFromAuthoringBundleActivityV1", maxStatementDraftBundleBytesV1)
	if err != nil {
		return nil, err
	}
	var draft StatementDraftBundleV1
	if err := decodeCanonicalSampleClosureJSONV1(draftBytes, &draft); err != nil {
		return nil, nonRetryableStageAuthoringSamplesErrorV1(fmt.Errorf("decode statement draft: %w", err))
	}
	if err := validateStatementDraftForSampleClosureV1(draft, authoring, semanticSHA, in.ExpectedAuthoringBundleSHA256, in.ExpectedStatementDraftSHA256); err != nil {
		return nil, nonRetryableStageAuthoringSamplesErrorV1(err)
	}

	validatedBytes, err := a.readBoundSampleClosureArtifactV1(ctx, in.ValidatedSamplesArtifact, in.ExpectedValidatedSamplesSHA256, bucket, "ValidateAuthoringSampleCandidatesActivityV1", maxValidatedSampleCandidatesBundleBytesV1)
	if err != nil {
		return nil, err
	}
	var validated ValidatedSampleCandidatesBundleV1
	if err := decodeCanonicalSampleClosureJSONV1(validatedBytes, &validated); err != nil {
		return nil, nonRetryableStageAuthoringSamplesErrorV1(fmt.Errorf("decode validated samples: %w", err))
	}
	if err := validateSampleCandidatesForClosureV1(validated, authoring, semanticSHA, in.ExpectedAuthoringBundleSHA256, in.ExpectedValidatedSamplesSHA256); err != nil {
		return nil, nonRetryableStageAuthoringSamplesErrorV1(err)
	}

	receiptBytes, err := a.readBoundSampleClosureArtifactV1(ctx, in.VerifiedProgramReceiptArtifact, in.ExpectedVerifiedProgramReceiptSHA256, bucket, VerifiedProgramReceiptProducerV1, maxVerifiedProgramReceiptBytesV1)
	if err != nil {
		return nil, err
	}
	receipt, err := decodeCanonicalVerifiedProgramReceiptV1(receiptBytes)
	if err != nil {
		return nil, nonRetryableStageAuthoringSamplesErrorV1(fmt.Errorf("decode verified-program receipt: %w", err))
	}

	expectedInputSHA := make([]string, len(validated.Candidates))
	for index := range validated.Candidates {
		expectedInputSHA[index] = validated.Candidates[index].CanonicalInputSHA256
	}
	if err := validateAuthoringSampleSandboxRunV1(in.FirstRun, AuthoringSampleSandboxRunPhaseFirst, *receipt, in.ExpectedVerifiedProgramReceiptSHA256, expectedInputSHA); err != nil {
		return nil, nonRetryableStageAuthoringSamplesErrorV1(err)
	}
	outputs, err := a.resolveAuthoringSampleSandboxOutputsV1(ctx, in.FirstRun.SandboxOutput, bucket)
	if err != nil {
		return nil, nonRetryableStageAuthoringSamplesErrorV1(err)
	}
	firstRunSHA, err := canonicalJSONSHA256(in.FirstRun)
	if err != nil {
		return nil, nonRetryableStageAuthoringSamplesErrorV1(fmt.Errorf("hash first sandbox run: %w", err))
	}

	contractSamples := make([]speccontract.StatementSampleV1, len(validated.Candidates))
	records := make([]AuthoringStatementSampleRecordV1, len(validated.Candidates))
	for index, candidate := range validated.Candidates {
		contractSamples[index] = speccontract.StatementSampleV1{Input: candidate.CanonicalInput, Output: outputs[index]}
		records[index] = AuthoringStatementSampleRecordV1{
			Index: index + 1, Input: candidate.CanonicalInput,
			InputSHA256: candidate.CanonicalInputSHA256,
			Output:      outputs[index], OutputSHA256: sha256Hex([]byte(outputs[index])),
		}
	}
	markdown, err := speccontract.RenderStatementSamplesV1(draft.Markdown, contractSamples)
	if err != nil {
		return nil, sampleClosureQualityErrorV1("render staged statement samples: %v", err)
	}
	parsed, err := speccontract.ParseStatementSamplesV1(markdown)
	if err != nil || !reflect.DeepEqual(parsed, contractSamples) {
		return nil, sampleClosureQualityErrorV1("immediate sample parse-back failed: %v", err)
	}
	markdownSHA := sha256Hex([]byte(markdown))
	staged := StagedAuthoringStatementSamplesBundleV1{
		SchemaVersion: StagedAuthoringStatementSamplesSchemaV1, Status: StagedAuthoringStatementSamplesStatusV1,
		StageInputSHA256: stageInputSHA, AuthoringBundleSHA256: in.ExpectedAuthoringBundleSHA256,
		StatementDraftSHA256: in.ExpectedStatementDraftSHA256, StatementDraftMarkdownSHA256: draft.MarkdownSHA256,
		ValidatedSamplesSHA256: in.ExpectedValidatedSamplesSHA256, VerifiedProgramReceiptSHA256: in.ExpectedVerifiedProgramReceiptSHA256,
		FirstRunSHA256: firstRunSHA, ProgramSHA256: receipt.ProgramSHA256,
		IndependentOracleReceiptSHA256: receipt.IndependentOracleReceiptSHA256,
		SandboxImageDigest:             receipt.SandboxImageDigest, ToolchainManifestDigest: receipt.ToolchainManifestDigest,
		SeccompPolicyDigest: receipt.SeccompPolicyDigest, LimitProfile: receipt.LimitProfile,
		SemanticSpecSHA256: semanticSHA, InputGrammarSHA256: validated.InputGrammarSHA256,
		SampleCount: len(records), Samples: records, Markdown: markdown, MarkdownSHA256: markdownSHA,
		DerivationRule: stagedAuthoringStatementSamplesRuleV1,
	}
	stagedBytes, err := json.Marshal(staged)
	if err != nil {
		return nil, fmt.Errorf("marshal staged statement samples: %w", err)
	}
	if len(stagedBytes) > maxStagedAuthoringStatementSamplesBytesV1 {
		return nil, sampleClosureQualityErrorV1("staged statement exceeds %d bytes", maxStagedAuthoringStatementSamplesBytesV1)
	}
	metadata := artifactMetadataFromActivity(ctx)
	metadata.ArtifactType = "qg03_staged_statement_samples"
	metadata.SourceType = "derived_first_run_statement_samples"
	metadata.RetentionClass = "workflow_cas_unreviewed"
	metadata.ProvenanceMetadata, _ = json.Marshal(map[string]interface{}{
		"stage_input_sha256": stageInputSHA, "program_sha256": receipt.ProgramSHA256,
		"verified_program_receipt_sha256": in.ExpectedVerifiedProgramReceiptSHA256,
		"sample_count":                    len(records), "markdown_sha256": markdownSHA,
	})
	stagedArtifact, err := a.putArtifactWithMetadata(ctx, stagedBytes, "application/json", metadata)
	if err != nil {
		return nil, fmt.Errorf("store staged statement samples: %w", err)
	}
	stagedSHA := sha256Hex(stagedBytes)
	if stagedArtifact == nil || stagedArtifact.SHA256 != stagedSHA || stagedArtifact.SizeBytes != int64(len(stagedBytes)) || stagedArtifact.Producer != "StageAuthoringStatementSamplesActivityV1" {
		return nil, fmt.Errorf("staged statement-samples CAS identity mismatch")
	}
	authoringRef, draftRef, validatedRef, receiptRef := in.AuthoringBundleArtifact, in.StatementDraftArtifact, in.ValidatedSamplesArtifact, in.VerifiedProgramReceiptArtifact
	return &StageAuthoringStatementSamplesResult{
		PayloadVersion: StageAuthoringStatementSamplesPayloadVersion, Status: StagedAuthoringStatementSamplesStatusV1,
		StageInputSHA256: stageInputSHA, SampleCount: len(records), MarkdownSHA256: markdownSHA,
		StagedBundleSHA256: stagedSHA, StagedBundleArtifact: stagedArtifact,
		SourceArtifacts: []*ArtifactRef{&authoringRef, &draftRef, &validatedRef, &receiptRef, stagedArtifact},
	}, nil
}

func validateAuthoringBundleForSampleClosureV1(bundle AuthoringPlanBundleV1, expectedSHA string) (string, error) {
	if bundle.SchemaVersion != AuthoringPlanBundleSchemaV1 || bundle.DerivationRule != authoringPlanDerivationRuleV1 ||
		bundle.ModelDecision != AuthoringPlanDecisionAccepted || bundle.Decision != AuthoringPlanDecisionAccepted ||
		bundle.SemanticSpec == nil || bundle.AuthoringPlan == nil || bundle.LintReport == nil || !bundle.LintReport.Passed {
		return "", fmt.Errorf("sample closure requires an accepted lint-passing authoring bundle")
	}
	if _, _, _, err := authoringTestCaseBoundsV1(bundle); err != nil {
		return "", fmt.Errorf("authoring test-case contract mismatch: %w", err)
	}
	if bundle.RequestedSampleCount < 0 || bundle.RequestedSampleCount > domain.MaxGeneratedTestCases ||
		bundle.RequestedSampleCount != len(bundle.SemanticSpec.SampleInputs) {
		return "", fmt.Errorf("authoring sample count binding mismatch")
	}
	semanticSHA := speccontract.SemanticSpecSHA256V1(*bundle.SemanticSpec)
	if !statementDraftIsSHA256V1(expectedSHA) || !statementDraftIsSHA256V1(semanticSHA) ||
		bundle.AuthoringPlan.SemanticSpecSHA256 != semanticSHA || bundle.LintReport.SemanticSpecSHA256 != semanticSHA {
		return "", fmt.Errorf("authoring SemanticSpec identity mismatch")
	}
	return semanticSHA, nil
}

func validateStatementDraftForSampleClosureV1(draft StatementDraftBundleV1, authoring AuthoringPlanBundleV1, semanticSHA, expectedAuthoringSHA, expectedDraftSHA string) error {
	if draft.SchemaVersion != StatementDraftBundleSchemaV1 || draft.DocumentStatus != statementDraftDocumentStatusV1 || draft.DerivationRule != statementDraftDerivationRuleV1 {
		return fmt.Errorf("unsupported statement draft schema/status")
	}
	if draft.AuthoringBundleSHA256 != expectedAuthoringSHA ||
		draft.AuthoringInputSHA256 != authoring.InputSHA256 || draft.SemanticSpecSHA256 != semanticSHA ||
		draft.BriefSHA256 != authoring.BriefSHA256 ||
		draft.MarkdownSHA256 != sha256Hex([]byte(draft.Markdown)) || strings.Count(draft.Markdown, StatementSamplesPlaceholder) != 1 {
		return fmt.Errorf("statement draft binding or placeholder mismatch")
	}
	expectedFacts := statementFactManifestFromSemanticSpecV1(*authoring.SemanticSpec)
	expectedFactSHA, err := canonicalJSONSHA256(expectedFacts)
	if err != nil || draft.FactManifestSHA256 != expectedFactSHA || !reflect.DeepEqual(draft.FactManifest, expectedFacts) || !statementDraftIsSHA256V1(expectedDraftSHA) {
		return fmt.Errorf("statement draft fact-manifest mismatch")
	}
	return nil
}

func validateSampleCandidatesForClosureV1(validated ValidatedSampleCandidatesBundleV1, authoring AuthoringPlanBundleV1, semanticSHA, expectedAuthoringSHA, expectedValidatedSHA string) error {
	if validated.SchemaVersion != ValidatedSampleCandidatesBundleSchemaV1 || validated.Status != ValidatedSampleCandidatesStatusInputsOnlyV1 ||
		validated.DerivationRule != validatedSampleCandidatesDerivationRuleV1 || validated.ParserRuleVersion != speccontract.SampleInputParserRuleVersionV1 {
		return fmt.Errorf("unsupported validated-sample bundle schema/status")
	}
	if validated.AuthoringBundleSHA256 != expectedAuthoringSHA || validated.SemanticSpecSHA256 != semanticSHA ||
		validated.BriefSHA256 != authoring.BriefSHA256 || validated.AuthoringInputSHA256 != authoring.InputSHA256 ||
		validated.InputGrammarSHA256 != speccontract.InputGrammarSHA256V1(authoring.SemanticSpec.InputGrammar) ||
		validated.RequestedSampleCount != authoring.RequestedSampleCount || len(validated.Candidates) != authoring.RequestedSampleCount ||
		!statementDraftIsSHA256V1(expectedValidatedSHA) {
		return fmt.Errorf("validated-sample bundle ancestry/count mismatch")
	}
	authoringMin, authoringMax, authoringAdaptive, authoringCountErr := authoringTestCaseBoundsV1(authoring)
	validatedMin, validatedMax, validatedAdaptive, validatedCountErr := authoringTestCaseBoundsV1(AuthoringPlanBundleV1{
		RequestedTestCaseCount: validated.RequestedTestCaseCount,
		MinTestCaseCount:       validated.MinTestCaseCount,
		MaxTestCaseCount:       validated.MaxTestCaseCount,
		AdaptiveTestCaseCount:  validated.AdaptiveTestCaseCount,
	})
	if authoringCountErr != nil || validatedCountErr != nil ||
		validated.RequestedTestCaseCount != authoring.RequestedTestCaseCount ||
		authoringMin != validatedMin || authoringMax != validatedMax || authoringAdaptive != validatedAdaptive {
		return fmt.Errorf("validated-sample test-case contract mismatch")
	}
	for index, candidate := range validated.Candidates {
		parsed, err := speccontract.ParseSampleInputV1(*authoring.SemanticSpec, authoring.SemanticSpec.SampleInputs[index])
		if err != nil {
			return fmt.Errorf("reparse source sample %d: %w", index, err)
		}
		if candidate.SourceIndex != index || candidate.SemanticSpecSHA256 != semanticSHA ||
			candidate.RawInputSHA256 != sha256Hex([]byte(authoring.SemanticSpec.SampleInputs[index])) ||
			candidate.CanonicalInput != parsed.CanonicalInput || candidate.CanonicalInputSHA256 != parsed.CanonicalSHA256 ||
			candidate.BindingSHA256 != parsed.BindingSHA256 || !reflect.DeepEqual(candidate.Bindings, parsed.Bindings) {
			return fmt.Errorf("validated sample %d receipt mismatch", index)
		}
	}
	return nil
}

func (a *Activities) readBoundSampleClosureArtifactV1(ctx context.Context, ref ArtifactRef, expectedSHA, bucket, producer string, maxBytes int) ([]byte, error) {
	if !statementDraftIsSHA256V1(expectedSHA) || ref.SHA256 != expectedSHA || ref.Bucket != bucket ||
		ref.ContentType != "application/json" || ref.Producer != producer || ref.LLMCallReceipt != nil ||
		ref.SizeBytes <= 0 || ref.SizeBytes > int64(maxBytes) {
		return nil, nonRetryableStageAuthoringSamplesErrorV1(fmt.Errorf("artifact %s binding/type mismatch", producer))
	}
	if err := ref.Validate(bucket); err != nil {
		return nil, nonRetryableStageAuthoringSamplesErrorV1(fmt.Errorf("invalid %s artifact: %w", producer, err))
	}
	data, err := a.artifacts.Get(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("read %s artifact: %w", producer, err)
	}
	if len(data) > maxBytes || int64(len(data)) != ref.SizeBytes || sha256Hex(data) != expectedSHA {
		return nil, nonRetryableStageAuthoringSamplesErrorV1(fmt.Errorf("artifact %s bytes/identity mismatch", producer))
	}
	return data, nil
}

func (a *Activities) resolveAuthoringSampleSandboxOutputsV1(ctx context.Context, output SandboxResult, bucket string) ([]string, error) {
	resolved := make([]string, len(output.Outputs))
	for index := range output.Outputs {
		if len(output.OutputArtifacts) == 0 {
			resolved[index] = output.Outputs[index]
			continue
		}
		ref := output.OutputArtifacts[index]
		if ref == nil || output.Outputs[index] != "" || ref.Bucket != bucket || ref.Producer != "RunSandboxActivity" || ref.ContentType != "text/plain" || ref.LLMCallReceipt != nil {
			return nil, fmt.Errorf("sandbox output artifact %d binding/type mismatch", index)
		}
		if err := ref.Validate(bucket); err != nil {
			return nil, fmt.Errorf("invalid sandbox output artifact %d: %w", index, err)
		}
		data, err := a.artifacts.Get(ctx, *ref)
		if err != nil {
			return nil, fmt.Errorf("read sandbox output artifact %d: %w", index, err)
		}
		resolved[index] = string(data)
	}
	return resolved, nil
}

func nonRetryableStageAuthoringSamplesErrorV1(err error) error {
	return temporal.NewNonRetryableApplicationError(err.Error(), stageAuthoringStatementSamplesContractErrorV1, err)
}

func sampleClosureQualityErrorV1(format string, args ...interface{}) error {
	return temporal.NewNonRetryableApplicationError(fmt.Sprintf(format, args...), "QualityNotMet", nil)
}
