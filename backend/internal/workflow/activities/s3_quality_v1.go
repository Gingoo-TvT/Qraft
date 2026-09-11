package activities

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/llm"
	qualitygate "github.com/Gingoo-TvT/Qraft/backend/internal/qualitygate/v1"
	remotesandbox "github.com/Gingoo-TvT/Qraft/backend/internal/sandbox"
	speccontract "github.com/Gingoo-TvT/Qraft/backend/internal/spec"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
)

const (
	S3QualityPayloadVersionV1           = 1
	ExtractSemanticSpecArtifactSchemaV1 = "algoforge.semantic-spec-artifact.v1"
	GenerateMainSolutionSchemaV1        = "algoforge.main-solution.v1"
	S3OracleGateReceiptSchemaV1         = "algoforge.s3-oracle-gate-receipt.v1"
	S3CasePlanSchemaV1                  = "algoforge.s3-case-plan.v1"
	S3SanitizerExecutionSchemaV1        = "algoforge.s3-sanitizer-execution.v1"
	S3SanitizerReceiptSchemaV1          = "algoforge.s3-sanitizer-receipt.v1"
	S3SanitizerProfileCAndCPPV1         = "sanitizer-c-cpp-v1"
	S3HiddenSuiteSchemaV1               = "algoforge.hidden-suite-ref.v1"
	S3HiddenExecutorResponseSchemaV1    = "algoforge.hidden-executor-response.v1"
	S3HiddenReceiptSchemaV1             = "algoforge.hidden-regression-receipt.v1"
	S3RepairRevisionSchemaV1            = "algoforge.s3-repair-revision.v1"
	maxS3SemanticSpecBytesV1            = 1 << 20
	maxS3ManifestBytesV1                = 8 << 20
	maxS3ReviewVisibleBytesV1           = 16 << 20
	maxS3RepairAssetBytesV1             = 8 << 20
	maxS3RepairRevisionBytesV1          = 64 << 10
	maxS3SanitizerCaseBytesV1           = 64 << 10
	maxS3SanitizerSuiteBytesV1          = 256 << 10
	maxS3SanitizerCasesV1               = 32
	S3StableOutputLimitBytesV1          = 8 << 20
	s3QualityContractErrorTypeV1        = "S3QualityContractError"
	s3QualityNotMetErrorTypeV1          = "QualityNotMet"
)

// HiddenRegressionExecutorV1 is deliberately narrower than the workflow
// input. Implementations resolve the opaque suite outside Temporal history;
// seed, input, expected-output, and mutant bytes can never be returned through
// this interface.
type HiddenRegressionExecutorV1 interface {
	ExecuteHiddenRegressionV1(context.Context, HiddenRegressionExecutionRequestV1) (HiddenRegressionExecutionResponseV1, error)
}

// HiddenSuiteResolverV1 selects one deployment-owned opaque suite for the
// current subject/spec identity. The suite body and seeds remain outside the
// worker and Temporal history.
type HiddenSuiteResolverV1 interface {
	ResolveHiddenSuiteV1(context.Context, HiddenSuiteResolutionRequestV1) (HiddenSuiteRefV1, error)
}

// RepairRevisionGeneratorV1 receives only assets named by recomputed blockers.
// It cannot access the hidden suite or the full generation bundle through this
// contract.
type RepairRevisionGeneratorV1 interface {
	GenerateRepairRevisionV1(context.Context, RepairRevisionProviderRequestV1) (RepairRevisionProviderResponseV1, error)
}

// S3SandboxIdentityPolicyV1 freezes the deployment-approved environment.
// Matching two runs is insufficient: both must also match this configured
// image/toolchain/seccomp identity.
type S3SandboxIdentityPolicyV1 struct {
	ImageDigest             string `json:"image_digest"`
	ToolchainManifestDigest string `json:"toolchain_manifest_digest"`
	SeccompPolicyDigest     string `json:"seccomp_policy_digest"`
}

type S3RepairPolicyResultV1 struct {
	PayloadVersion int    `json:"payload_version"`
	Enabled        bool   `json:"enabled"`
	MaxRounds      int    `json:"max_rounds"`
	ReasonCode     string `json:"reason_code"`
}

// ResolveS3RepairPolicyActivityV1 is intentionally server-owned and disabled
// until the frozen T34 calibration evidence exists. A workflow caller cannot
// opt itself into model-driven repair.
func (a *Activities) ResolveS3RepairPolicyActivityV1(context.Context) (*S3RepairPolicyResultV1, error) {
	return &S3RepairPolicyResultV1{PayloadVersion: S3QualityPayloadVersionV1, Enabled: false, MaxRounds: qualitygate.MaxRepairRoundsV1, ReasonCode: "t34_calibration_not_frozen"}, nil
}

type ExtractSemanticSpecArtifactInputV1 struct {
	PayloadVersion             int         `json:"payload_version"`
	AuthoringBundleArtifact    ArtifactRef `json:"authoring_bundle_artifact"`
	ExpectedAuthoringBundleSHA string      `json:"expected_authoring_bundle_sha256"`
	ExpectedAuthoringInputSHA  string      `json:"expected_authoring_input_sha256"`
	ExpectedBriefSHA           string      `json:"expected_brief_sha256"`
	ExpectedSemanticSpecSHA    string      `json:"expected_semantic_spec_sha256"`
}

type ExtractSemanticSpecArtifactResultV1 struct {
	PayloadVersion        int          `json:"payload_version"`
	SchemaVersion         string       `json:"schema_version"`
	SemanticSpecSHA256    string       `json:"semantic_spec_sha256"`
	SemanticSpecArtifact  *ArtifactRef `json:"semantic_spec_artifact"`
	AuthoringBundleSHA256 string       `json:"authoring_bundle_sha256"`
}

// ExtractSemanticSpecArtifactActivityV1 is the D-side projection that proves
// V receives a standalone canonical SemanticSpec CAS, never the authoring plan
// or rendered statement.
func (a *Activities) ExtractSemanticSpecArtifactActivityV1(ctx context.Context, in ExtractSemanticSpecArtifactInputV1) (*ExtractSemanticSpecArtifactResultV1, error) {
	if in.PayloadVersion != S3QualityPayloadVersionV1 ||
		!isManifestSHA256(in.ExpectedAuthoringBundleSHA) ||
		!isManifestSHA256(in.ExpectedAuthoringInputSHA) ||
		!isManifestSHA256(in.ExpectedBriefSHA) ||
		!isManifestSHA256(in.ExpectedSemanticSpecSHA) {
		return nil, s3ContractErrorV1("invalid SemanticSpec extraction identity")
	}
	bundleBytes, err := a.readS3ArtifactV1(ctx, in.AuthoringBundleArtifact, in.ExpectedAuthoringBundleSHA, "application/json", maxAuthoringPlanBundleBytes)
	if err != nil {
		return nil, err
	}
	bundle, err := decodeCanonicalAuthoringBundleV1(bundleBytes)
	if err != nil {
		return nil, s3ContractErrorV1("decode accepted authoring bundle: %v", err)
	}
	semanticSHA, err := validateAuthoringBundleForSampleClosureV1(*bundle, in.ExpectedAuthoringBundleSHA)
	if err != nil {
		return nil, s3ContractErrorV1("validate accepted authoring bundle: %v", err)
	}
	if bundle.InputSHA256 != in.ExpectedAuthoringInputSHA || bundle.BriefSHA256 != in.ExpectedBriefSHA || semanticSHA != in.ExpectedSemanticSpecSHA {
		return nil, s3ContractErrorV1("authoring bundle identity does not match extraction input")
	}
	semanticBytes, err := json.Marshal(bundle.SemanticSpec)
	if err != nil {
		return nil, fmt.Errorf("marshal standalone SemanticSpec: %w", err)
	}
	if len(semanticBytes) > maxS3SemanticSpecBytesV1 || speccontract.SemanticSpecSHA256V1(*bundle.SemanticSpec) != sha256Hex(semanticBytes) || sha256Hex(semanticBytes) != semanticSHA {
		return nil, s3ContractErrorV1("standalone SemanticSpec is not canonical or exceeds its size limit")
	}
	metadata := artifactMetadataFromActivity(ctx)
	metadata.ArtifactType = "qg04_semantic_spec_only"
	metadata.SourceType = "deterministic_projection_from_accepted_authoring_bundle"
	metadata.RetentionClass = "workflow_cas_unreviewed"
	metadata.ProvenanceMetadata, _ = json.Marshal(map[string]string{
		"schema_version":          ExtractSemanticSpecArtifactSchemaV1,
		"authoring_bundle_sha256": in.ExpectedAuthoringBundleSHA,
		"semantic_spec_sha256":    semanticSHA,
	})
	ref, err := a.putArtifactWithMetadata(ctx, semanticBytes, "application/json", metadata)
	if err != nil {
		return nil, fmt.Errorf("store standalone SemanticSpec: %w", err)
	}
	if ref == nil || ref.SHA256 != semanticSHA || ref.Producer != "ExtractSemanticSpecArtifactActivityV1" || ref.LLMCallReceipt != nil {
		return nil, fmt.Errorf("standalone SemanticSpec CAS identity mismatch")
	}
	return &ExtractSemanticSpecArtifactResultV1{
		PayloadVersion: S3QualityPayloadVersionV1, SchemaVersion: ExtractSemanticSpecArtifactSchemaV1,
		SemanticSpecSHA256: semanticSHA, SemanticSpecArtifact: ref,
		AuthoringBundleSHA256: in.ExpectedAuthoringBundleSHA,
	}, nil
}

type BuildS3SpecLintGateInputV1 struct {
	PayloadVersion                int         `json:"payload_version"`
	AuthoringBundleArtifact       ArtifactRef `json:"authoring_bundle_artifact"`
	ExpectedAuthoringBundleSHA256 string      `json:"expected_authoring_bundle_sha256"`
	ExpectedAuthoringInputSHA256  string      `json:"expected_authoring_input_sha256"`
	ExpectedBriefSHA256           string      `json:"expected_brief_sha256"`
	ExpectedSemanticSpecSHA256    string      `json:"expected_semantic_spec_sha256"`
	ExpectedDifficulty            int         `json:"expected_difficulty"`
	RequiredKnowledgePoints       []string    `json:"required_knowledge_points"`
}

type BuildS3SpecLintGateResultV1 struct {
	PayloadVersion  int                        `json:"payload_version"`
	Gate            qualitygate.GateEvidenceV1 `json:"gate"`
	ReceiptSHA256   string                     `json:"receipt_sha256"`
	ReceiptArtifact *ArtifactRef               `json:"receipt_artifact"`
}

// BuildS3SpecLintGateActivityV1 independently reloads the accepted QG-02
// bundle and recomputes the deterministic lint report. The workflow never
// fabricates a PASS from an ArtifactRef alone.
func (a *Activities) BuildS3SpecLintGateActivityV1(ctx context.Context, in BuildS3SpecLintGateInputV1) (*BuildS3SpecLintGateResultV1, error) {
	if in.PayloadVersion != S3QualityPayloadVersionV1 ||
		!isManifestSHA256(in.ExpectedAuthoringBundleSHA256) ||
		!isManifestSHA256(in.ExpectedAuthoringInputSHA256) ||
		!isManifestSHA256(in.ExpectedBriefSHA256) ||
		!isManifestSHA256(in.ExpectedSemanticSpecSHA256) {
		return nil, s3ContractErrorV1("invalid S3 spec-lint gate input")
	}
	bundleBytes, err := a.readS3ArtifactV1(ctx, in.AuthoringBundleArtifact, in.ExpectedAuthoringBundleSHA256, "application/json", maxAuthoringPlanBundleBytes)
	if err != nil {
		return nil, err
	}
	bundle, err := decodeCanonicalAuthoringBundleV1(bundleBytes)
	if err != nil {
		return nil, s3ContractErrorV1("decode S3 spec-lint authoring bundle: %v", err)
	}
	semanticSHA, err := validateAuthoringBundleForSampleClosureV1(*bundle, in.ExpectedAuthoringBundleSHA256)
	if err != nil || bundle.InputSHA256 != in.ExpectedAuthoringInputSHA256 || bundle.BriefSHA256 != in.ExpectedBriefSHA256 || semanticSHA != in.ExpectedSemanticSpecSHA256 {
		return nil, s3ContractErrorV1("S3 spec-lint bundle ancestry mismatch")
	}
	recomputed := speccontract.LintV1(speccontract.LintInputV1{
		SemanticSpec: *bundle.SemanticSpec, AuthoringPlan: *bundle.AuthoringPlan,
		ExpectedBriefSHA256: in.ExpectedBriefSHA256, ExpectedDifficulty: in.ExpectedDifficulty,
		RequiredKnowledgePoints: append([]string(nil), in.RequiredKnowledgePoints...),
	})
	recomputedBytes, recomputedSHA, err := canonicalJSONBytesAndSHA256(recomputed)
	if err != nil {
		return nil, fmt.Errorf("canonicalize recomputed S3 spec lint: %w", err)
	}
	_, storedSHA, err := canonicalJSONBytesAndSHA256(bundle.LintReport)
	if err != nil || !recomputed.Passed || storedSHA != recomputedSHA || recomputed.SemanticSpecSHA256 != in.ExpectedSemanticSpecSHA256 {
		return nil, s3QualityErrorV1("S3 spec lint did not reproduce the accepted bundle report")
	}
	ref, err := a.putS3ReceiptV1(ctx, recomputedBytes, "qg02_spec_lint_receipt")
	if err != nil {
		return nil, err
	}
	if ref.SHA256 != recomputedSHA || ref.Producer != "BuildS3SpecLintGateActivityV1" {
		return nil, fmt.Errorf("S3 spec-lint receipt CAS identity mismatch")
	}
	return &BuildS3SpecLintGateResultV1{PayloadVersion: S3QualityPayloadVersionV1, Gate: qualitygate.GateEvidenceV1{Status: qualitygate.GateStatusPass, Evidence: s3EvidenceAssetV1(*ref), Blockers: []qualitygate.ReviewBlockerV1{}}, ReceiptSHA256: ref.SHA256, ReceiptArtifact: ref}, nil
}

type BuildS3SampleOutputGateInputV1 struct {
	PayloadVersion                       int         `json:"payload_version"`
	FinalStatementArtifact               ArtifactRef `json:"final_statement_artifact"`
	ExpectedFinalStatementSHA256         string      `json:"expected_final_statement_sha256"`
	ExpectedAuthoringBundleSHA256        string      `json:"expected_authoring_bundle_sha256"`
	ExpectedStatementDraftSHA256         string      `json:"expected_statement_draft_sha256"`
	ExpectedSemanticSpecSHA256           string      `json:"expected_semantic_spec_sha256"`
	VerifiedProgramReceiptArtifact       ArtifactRef `json:"verified_program_receipt_artifact"`
	ExpectedVerifiedProgramReceiptSHA256 string      `json:"expected_verified_program_receipt_sha256"`
	OracleReceiptArtifact                ArtifactRef `json:"oracle_receipt_artifact"`
	ExpectedOracleReceiptSHA256          string      `json:"expected_oracle_receipt_sha256"`
}

type BuildS3SampleOutputGateResultV1 struct {
	PayloadVersion  int                        `json:"payload_version"`
	Gate            qualitygate.GateEvidenceV1 `json:"gate"`
	ReceiptSHA256   string                     `json:"receipt_sha256"`
	ReceiptArtifact *ArtifactRef               `json:"receipt_artifact"`
}

type s3SampleOutputBindingReceiptV1 struct {
	SchemaVersion                string `json:"schema_version"`
	FinalStatementSHA256         string `json:"final_statement_sha256"`
	AuthoringBundleSHA256        string `json:"authoring_bundle_sha256"`
	StatementDraftSHA256         string `json:"statement_draft_sha256"`
	SemanticSpecSHA256           string `json:"semantic_spec_sha256"`
	VerifiedProgramReceiptSHA256 string `json:"verified_program_receipt_sha256"`
	FirstRunSHA256               string `json:"first_run_sha256"`
	SecondRunSHA256              string `json:"second_run_sha256"`
	SampleCount                  int    `json:"sample_count"`
	MarkdownSHA256               string `json:"markdown_sha256"`
}

// BuildS3SampleOutputGateActivityV1 reloads the final QG-03 bundle and its
// server-produced verified-program receipt, then binds both sandbox runs and
// every sample byte identity into a separate gate receipt.
func (a *Activities) BuildS3SampleOutputGateActivityV1(ctx context.Context, in BuildS3SampleOutputGateInputV1) (*BuildS3SampleOutputGateResultV1, error) {
	if in.PayloadVersion != S3QualityPayloadVersionV1 ||
		!isManifestSHA256(in.ExpectedFinalStatementSHA256) ||
		!isManifestSHA256(in.ExpectedAuthoringBundleSHA256) ||
		!isManifestSHA256(in.ExpectedStatementDraftSHA256) ||
		!isManifestSHA256(in.ExpectedSemanticSpecSHA256) ||
		!isManifestSHA256(in.ExpectedVerifiedProgramReceiptSHA256) ||
		!isManifestSHA256(in.ExpectedOracleReceiptSHA256) ||
		in.FinalStatementArtifact.SHA256 != in.ExpectedFinalStatementSHA256 ||
		in.FinalStatementArtifact.Producer != "FinalizeAuthoringStatementSamplesActivityV1" ||
		in.VerifiedProgramReceiptArtifact.SHA256 != in.ExpectedVerifiedProgramReceiptSHA256 ||
		in.VerifiedProgramReceiptArtifact.Producer != VerifiedProgramReceiptProducerV1 ||
		in.OracleReceiptArtifact.SHA256 != in.ExpectedOracleReceiptSHA256 ||
		in.OracleReceiptArtifact.Producer != "VerifyProgramAgainstIndependentOracleActivityV1" {
		return nil, s3ContractErrorV1("invalid S3 sample-output gate input")
	}
	finalBytes, err := a.readS3ArtifactV1(ctx, in.FinalStatementArtifact, in.ExpectedFinalStatementSHA256, "application/json", maxFinalAuthoringStatementSamplesBytesV1)
	if err != nil {
		return nil, err
	}
	var final FinalAuthoringStatementSamplesBundleV1
	if err := decodeCanonicalSampleClosureJSONV1(finalBytes, &final); err != nil {
		return nil, s3ContractErrorV1("decode final S3 sample bundle: %v", err)
	}
	if final.SchemaVersion != FinalAuthoringStatementSamplesSchemaV1 || final.Status != FinalAuthoringStatementSamplesStatusV1 ||
		final.AuthoringBundleSHA256 != in.ExpectedAuthoringBundleSHA256 || final.StatementDraftSHA256 != in.ExpectedStatementDraftSHA256 ||
		final.SemanticSpecSHA256 != in.ExpectedSemanticSpecSHA256 || final.VerifiedProgramReceiptSHA256 != in.ExpectedVerifiedProgramReceiptSHA256 ||
		!isManifestSHA256(final.FirstRunSHA256) || !isManifestSHA256(final.SecondRunSHA256) || final.FirstRunSHA256 == final.SecondRunSHA256 ||
		final.SampleCount <= 0 || final.SampleCount != len(final.Samples) || final.MarkdownSHA256 != sha256Hex([]byte(final.Markdown)) {
		return nil, s3ContractErrorV1("final S3 sample bundle ancestry or two-run identity mismatch")
	}
	for index, sample := range final.Samples {
		if sample.Index != index+1 || sample.InputSHA256 != sha256Hex([]byte(sample.Input)) || sample.OutputSHA256 != sha256Hex([]byte(sample.Output)) {
			return nil, s3ContractErrorV1("final S3 sample %d byte identity mismatch", index+1)
		}
	}
	verifiedBytes, err := a.readS3ArtifactV1(ctx, in.VerifiedProgramReceiptArtifact, in.ExpectedVerifiedProgramReceiptSHA256, "application/json", maxVerifiedProgramReceiptBytesV1)
	if err != nil {
		return nil, err
	}
	verified, err := decodeCanonicalVerifiedProgramReceiptV1(verifiedBytes)
	if err != nil || verified.ProgramSHA256 != final.ProgramSHA256 || verified.IndependentOracleReceiptSHA256 != final.IndependentOracleReceiptSHA256 ||
		verified.SandboxImageDigest != final.SandboxImageDigest || verified.ToolchainManifestDigest != final.ToolchainManifestDigest || verified.SeccompPolicyDigest != final.SeccompPolicyDigest {
		return nil, s3ContractErrorV1("verified-program receipt does not bind the final S3 sample bundle")
	}
	oracleBytes, err := a.readS3ArtifactV1(ctx, in.OracleReceiptArtifact, in.ExpectedOracleReceiptSHA256, "application/json", maxS3ManifestBytesV1)
	if err != nil {
		return nil, err
	}
	var oracle S3OracleGateReceiptV1
	if err := decodeCanonicalSampleClosureJSONV1(oracleBytes, &oracle); err != nil ||
		oracle.SchemaVersion != S3OracleGateReceiptSchemaV1 || oracle.Status != qualitygate.GateStatusPass ||
		oracle.SemanticSpecSHA256 != in.ExpectedSemanticSpecSHA256 || verified.IndependentOracleReceiptSHA256 != in.ExpectedOracleReceiptSHA256 {
		return nil, s3ContractErrorV1("independent-oracle receipt does not bind the final S3 sample bundle")
	}
	receipt := s3SampleOutputBindingReceiptV1{
		SchemaVersion: "algoforge.s3-sample-output-binding-receipt.v1", FinalStatementSHA256: in.ExpectedFinalStatementSHA256,
		AuthoringBundleSHA256: in.ExpectedAuthoringBundleSHA256, StatementDraftSHA256: in.ExpectedStatementDraftSHA256,
		SemanticSpecSHA256: in.ExpectedSemanticSpecSHA256, VerifiedProgramReceiptSHA256: in.ExpectedVerifiedProgramReceiptSHA256,
		FirstRunSHA256: final.FirstRunSHA256, SecondRunSHA256: final.SecondRunSHA256, SampleCount: final.SampleCount, MarkdownSHA256: final.MarkdownSHA256,
	}
	encoded, receiptSHA, err := canonicalJSONBytesAndSHA256(receipt)
	if err != nil {
		return nil, fmt.Errorf("canonicalize S3 sample-output receipt: %w", err)
	}
	ref, err := a.putS3ReceiptV1(ctx, encoded, "qg03_sample_output_binding_receipt")
	if err != nil {
		return nil, err
	}
	if ref.SHA256 != receiptSHA || ref.Producer != "BuildS3SampleOutputGateActivityV1" {
		return nil, fmt.Errorf("S3 sample-output receipt CAS identity mismatch")
	}
	return &BuildS3SampleOutputGateResultV1{PayloadVersion: S3QualityPayloadVersionV1, Gate: qualitygate.GateEvidenceV1{Status: qualitygate.GateStatusPass, Evidence: s3EvidenceAssetV1(*ref), Blockers: []qualitygate.ReviewBlockerV1{}}, ReceiptSHA256: ref.SHA256, ReceiptArtifact: ref}, nil
}

type GenerateMainSolutionInputV1 struct {
	PayloadVersion             int                      `json:"payload_version"`
	AuthoringBundleArtifact    ArtifactRef              `json:"authoring_bundle_artifact"`
	ExpectedAuthoringBundleSHA string                   `json:"expected_authoring_bundle_sha256"`
	StatementDraftArtifact     ArtifactRef              `json:"statement_draft_artifact"`
	ExpectedStatementDraftSHA  string                   `json:"expected_statement_draft_sha256"`
	ExpectedSemanticSpecSHA    string                   `json:"expected_semantic_spec_sha256"`
	Language                   string                   `json:"language"`
	MainRuntime                *domain.LLMRuntimeConfig `json:"main_runtime,omitempty"`
}

type GenerateMainSolutionResultV1 struct {
	PayloadVersion           int          `json:"payload_version"`
	SchemaVersion            string       `json:"schema_version"`
	AuthoringBundleSHA256    string       `json:"authoring_bundle_sha256"`
	StatementDraftSHA256     string       `json:"statement_draft_sha256"`
	SemanticSpecSHA256       string       `json:"semantic_spec_sha256"`
	Language                 string       `json:"language"`
	CandidateSourceSHA256    string       `json:"candidate_source_sha256"`
	CandidateSourceArtifact  *ArtifactRef `json:"candidate_source_artifact"`
	ProviderResponseArtifact *ArtifactRef `json:"provider_response_artifact"`
}

// GenerateMainSolutionActivityV1 is the G-side-only program generator. It
// reads the accepted plan and presentation shell inside the activity. The V
// route is never used here and remains isolated in GenerateOracleCandidate.
func (a *Activities) GenerateMainSolutionActivityV1(ctx context.Context, in GenerateMainSolutionInputV1) (*GenerateMainSolutionResultV1, error) {
	if in.PayloadVersion != S3QualityPayloadVersionV1 || !isManifestSHA256(in.ExpectedAuthoringBundleSHA) || !isManifestSHA256(in.ExpectedStatementDraftSHA) || !isManifestSHA256(in.ExpectedSemanticSpecSHA) || strings.TrimSpace(in.Language) == "" {
		return nil, s3ContractErrorV1("invalid main-solution input")
	}
	bundleBytes, err := a.readS3ArtifactV1(ctx, in.AuthoringBundleArtifact, in.ExpectedAuthoringBundleSHA, "application/json", maxAuthoringPlanBundleBytes)
	if err != nil {
		return nil, err
	}
	bundle, err := decodeCanonicalAuthoringBundleV1(bundleBytes)
	if err != nil || bundle.SemanticSpec == nil || bundle.AuthoringPlan == nil || bundle.Decision != AuthoringPlanDecisionAccepted || speccontract.SemanticSpecSHA256V1(*bundle.SemanticSpec) != in.ExpectedSemanticSpecSHA {
		return nil, s3ContractErrorV1("main solution requires the bound accepted authoring bundle")
	}
	draftBytes, err := a.readS3ArtifactV1(ctx, in.StatementDraftArtifact, in.ExpectedStatementDraftSHA, "application/json", maxStatementDraftBundleBytesV1)
	if err != nil {
		return nil, err
	}
	var draft StatementDraftBundleV1
	if err := decodeCanonicalSampleClosureJSONV1(draftBytes, &draft); err != nil || validateStatementDraftForSampleClosureV1(draft, *bundle, in.ExpectedSemanticSpecSHA, in.ExpectedAuthoringBundleSHA, in.ExpectedStatementDraftSHA) != nil {
		return nil, s3ContractErrorV1("main solution requires the bound statement draft")
	}
	prompt, err := json.Marshal(struct {
		Language          string      `json:"language"`
		SemanticSpec      interface{} `json:"semantic_spec"`
		AuthoringPlan     interface{} `json:"authoring_plan"`
		StatementMarkdown string      `json:"statement_markdown"`
	}{in.Language, bundle.SemanticSpec, bundle.AuthoringPlan, draft.Markdown})
	if err != nil {
		return nil, fmt.Errorf("marshal main-solution prompt: %w", err)
	}
	temperature := 0.0
	req := &llm.Request{MaxTokens: 16000, System: s3MainSolutionSystemPromptV1, Temperature: &temperature, Messages: []llm.Message{{Role: "user", Content: string(prompt)}}}
	if in.MainRuntime != nil {
		copyRuntime := *in.MainRuntime
		if err := copyRuntime.Validate("main_runtime"); err != nil {
			return nil, s3ContractErrorV1("invalid main runtime: %v", err)
		}
		applyLLMRuntime(req, &copyRuntime)
	}
	response, providerArtifact, err := a.completeLLMWithProvenance(ctx, "s3_main_solution_v1", req, 2)
	if err != nil {
		return nil, wrapRequiredProviderEffectError("S3 main solution", err)
	}
	if response == nil || providerArtifact == nil || providerArtifact.LLMCallReceipt == nil || response.StopReason == "max_tokens" {
		return nil, s3ContractErrorV1("main-solution provider response is missing identity or truncated")
	}
	parsed, err := parseOracleCandidateResponseV1(response.Text())
	if err != nil || parsed.Language != in.Language {
		return nil, s3ContractErrorV1("strict main-solution response: %v", err)
	}
	sourceBytes := []byte(parsed.SourceCode)
	metadata := artifactMetadataFromActivity(ctx)
	metadata.ArtifactType = "qg04_main_solution_source"
	metadata.SourceType = "generation_model_candidate"
	metadata.RetentionClass = "workflow_cas_unreviewed"
	ref, err := a.putArtifactWithMetadata(ctx, sourceBytes, oracleSourceContentTypeV1(in.Language), metadata)
	if err != nil {
		return nil, fmt.Errorf("store main-solution source: %w", err)
	}
	if ref == nil || ref.SHA256 != sha256Hex(sourceBytes) || ref.Producer != "GenerateMainSolutionActivityV1" {
		return nil, fmt.Errorf("main-solution CAS identity mismatch")
	}
	return &GenerateMainSolutionResultV1{PayloadVersion: S3QualityPayloadVersionV1, SchemaVersion: GenerateMainSolutionSchemaV1, AuthoringBundleSHA256: in.ExpectedAuthoringBundleSHA, StatementDraftSHA256: in.ExpectedStatementDraftSHA, SemanticSpecSHA256: in.ExpectedSemanticSpecSHA, Language: in.Language, CandidateSourceSHA256: ref.SHA256, CandidateSourceArtifact: ref, ProviderResponseArtifact: providerArtifact}, nil
}

const s3MainSolutionSystemPromptV1 = `You are the G implementation stage for AlgoForge S3. Implement the accepted SemanticSpec, AuthoringPlan, and statement shell exactly in the requested language. Return exactly one compact JSON object {"source_code":"full source","language":"requested language"}. Do not emit Markdown, commentary, tests, oracle source, or hidden material.`

type S3ExecutableCaseV1 struct {
	TestID        string      `json:"test_id"`
	InputArtifact ArtifactRef `json:"input_artifact"`
}

type GenerateS3TestDataInputV1 struct {
	PayloadVersion             int                     `json:"payload_version"`
	AuthoringBundleArtifact    ArtifactRef             `json:"authoring_bundle_artifact"`
	ExpectedAuthoringBundleSHA string                  `json:"expected_authoring_bundle_sha256"`
	StatementDraftArtifact     ArtifactRef             `json:"statement_draft_artifact"`
	ExpectedStatementDraftSHA  string                  `json:"expected_statement_draft_sha256"`
	ExpectedSemanticSpecSHA    string                  `json:"expected_semantic_spec_sha256"`
	Config                     domain.TestDataConfig   `json:"config"`
	Params                     domain.ProblemGenParams `json:"params"`
}

// GenerateS3TestDataActivityV1 is a server-side adapter around the legacy test
// generator. It deliberately uses the bound QG-02 statement shell, before
// sample output closure, so case generation does not depend on the later
// verified-program receipt.
func (a *Activities) GenerateS3TestDataActivityV1(ctx context.Context, in GenerateS3TestDataInputV1) (*TestDataResult, error) {
	if in.PayloadVersion != S3QualityPayloadVersionV1 || !isManifestSHA256(in.ExpectedAuthoringBundleSHA) || !isManifestSHA256(in.ExpectedStatementDraftSHA) || !isManifestSHA256(in.ExpectedSemanticSpecSHA) || in.StatementDraftArtifact.Producer != "RenderStatementFromAuthoringBundleActivityV1" {
		return nil, s3ContractErrorV1("invalid statement-shell identity for S3 test generation")
	}
	bundleBytes, err := a.readS3ArtifactV1(ctx, in.AuthoringBundleArtifact, in.ExpectedAuthoringBundleSHA, "application/json", maxAuthoringPlanBundleBytes)
	if err != nil {
		return nil, err
	}
	bundle, err := decodeCanonicalAuthoringBundleV1(bundleBytes)
	if err != nil || bundle.SemanticSpec == nil || speccontract.SemanticSpecSHA256V1(*bundle.SemanticSpec) != in.ExpectedSemanticSpecSHA {
		return nil, s3ContractErrorV1("S3 test generation requires the accepted authoring bundle")
	}
	draftBytes, err := a.readS3ArtifactV1(ctx, in.StatementDraftArtifact, in.ExpectedStatementDraftSHA, "application/json", maxStatementDraftBundleBytesV1)
	if err != nil {
		return nil, err
	}
	var draft StatementDraftBundleV1
	if err := decodeCanonicalSampleClosureJSONV1(draftBytes, &draft); err != nil || validateStatementDraftForSampleClosureV1(draft, *bundle, in.ExpectedSemanticSpecSHA, in.ExpectedAuthoringBundleSHA, in.ExpectedStatementDraftSHA) != nil {
		return nil, s3ContractErrorV1("S3 test generation statement shell has invalid ancestry")
	}
	params, err := enrichS3TestDataParamsV1(in.Params, *bundle.SemanticSpec, *bundle.AuthoringPlan)
	if err != nil {
		return nil, s3ContractErrorV1("build typed S3 test-data context: %v", err)
	}
	return a.GenerateTestDataActivity(ctx, draft.Markdown, in.Config, params)
}

const maxS3TypedTestDataContextBytesV1 = 96 << 10

// enrichS3TestDataParamsV1 keeps the legacy generator/parser implementation
// while giving the model the machine-readable contract that the S3 case-plan
// materializer will enforce. The typed context is advisory to the model but
// authoritative to the server after generation.
func enrichS3TestDataParamsV1(params domain.ProblemGenParams, spec domain.SemanticSpecV1, plan domain.AuthoringPlanV1) (domain.ProblemGenParams, error) {
	contextPayload := struct {
		SchemaVersion string                          `json:"schema_version"`
		SemanticSpec  domain.SemanticSpecV1           `json:"semantic_spec"`
		TestIntents   []domain.AuthoringTestIntentV1  `json:"test_intents"`
		FailureModes  []domain.AuthoringFailureModeV1 `json:"failure_modes"`
	}{"algoforge.s3-quality.v1", spec, append([]domain.AuthoringTestIntentV1(nil), plan.TestIntents...), append([]domain.AuthoringFailureModeV1(nil), plan.FailureModes...)}
	encoded, err := json.Marshal(contextPayload)
	if err != nil {
		return domain.ProblemGenParams{}, err
	}
	if len(encoded) > maxS3TypedTestDataContextBytesV1 {
		return domain.ProblemGenParams{}, fmt.Errorf("typed test-data context exceeds %d bytes", maxS3TypedTestDataContextBytesV1)
	}
	const prefix = "SERVER-AUTHORED TYPED TEST-DATA CONTRACT (authoritative; do not override with prose):\n"
	const suffix = "\nUse test_intents and failure_modes to choose distinct cases. Every generated input must satisfy semantic_spec exactly; the server will reparse and reject any mismatch."
	params.CustomPrompt = strings.TrimSpace(params.CustomPrompt)
	if params.CustomPrompt != "" {
		params.CustomPrompt += "\n\n"
	}
	params.CustomPrompt += prefix + string(encoded) + suffix
	return params, nil
}

type S3CasePlanBundleV1 struct {
	SchemaVersion          string                 `json:"schema_version"`
	SemanticSpecSHA256     string                 `json:"semantic_spec_sha256"`
	AuthoringBundleSHA256  string                 `json:"authoring_bundle_sha256"`
	TestDataIdentitySHA256 string                 `json:"test_data_identity_sha256"`
	Cases                  []S3ManifestCasePlanV1 `json:"cases"`
	SanitizerCaseIDs       []string               `json:"sanitizer_case_ids"`
	SanitizerSuiteSHA256   string                 `json:"sanitizer_suite_sha256"`
}

type MaterializeS3CasePlanInputV1 struct {
	PayloadVersion             int            `json:"payload_version"`
	AuthoringBundleArtifact    ArtifactRef    `json:"authoring_bundle_artifact"`
	ExpectedAuthoringBundleSHA string         `json:"expected_authoring_bundle_sha256"`
	TestData                   TestDataResult `json:"test_data"`
}

type MaterializeS3CasePlanResultV1 struct {
	PayloadVersion       int          `json:"payload_version"`
	TestCount            int          `json:"test_count"`
	CasePlanSHA256       string       `json:"case_plan_sha256"`
	CasePlanArtifact     *ArtifactRef `json:"case_plan_artifact"`
	SanitizerSuiteSHA256 string       `json:"sanitizer_suite_sha256"`
	SanitizerCaseCount   int          `json:"sanitizer_case_count"`
}

// MaterializeS3CasePlanActivityV1 distrusts free-text case descriptions. It
// reparses every actual input with SemanticSpec, derives labels from observed
// bindings and accepted test intents, and externalizes every input to CAS.
func (a *Activities) MaterializeS3CasePlanActivityV1(ctx context.Context, in MaterializeS3CasePlanInputV1) (*MaterializeS3CasePlanResultV1, error) {
	if in.PayloadVersion != S3QualityPayloadVersionV1 || !isManifestSHA256(in.ExpectedAuthoringBundleSHA) || in.TestData.PayloadVersion != ActivityPayloadVersion || len(in.TestData.TestCases) == 0 {
		return nil, s3ContractErrorV1("invalid S3 case-plan materialization input")
	}
	bundleBytes, err := a.readS3ArtifactV1(ctx, in.AuthoringBundleArtifact, in.ExpectedAuthoringBundleSHA, "application/json", maxAuthoringPlanBundleBytes)
	if err != nil {
		return nil, err
	}
	bundle, err := decodeCanonicalAuthoringBundleV1(bundleBytes)
	if err != nil || bundle.Decision != AuthoringPlanDecisionAccepted || bundle.SemanticSpec == nil || bundle.AuthoringPlan == nil {
		return nil, s3ContractErrorV1("case-plan materialization requires an accepted authoring bundle")
	}
	minTestCases, maxTestCases, adaptiveTestCases, boundsErr := authoringTestCaseBoundsV1(*bundle)
	if boundsErr != nil {
		return nil, s3ContractErrorV1("case-plan test-case contract is invalid: %v", boundsErr)
	}
	if adaptiveTestCases && (len(in.TestData.TestCases) < minTestCases || len(in.TestData.TestCases) > maxTestCases) {
		return nil, s3QualityErrorV1("adaptive test-data count %d is outside [%d,%d]", len(in.TestData.TestCases), minTestCases, maxTestCases)
	}
	if !adaptiveTestCases && len(in.TestData.TestCases) != bundle.RequestedTestCaseCount {
		return nil, s3QualityErrorV1("explicit test-data count %d does not match requested %d", len(in.TestData.TestCases), bundle.RequestedTestCaseCount)
	}
	semanticSHA := speccontract.SemanticSpecSHA256V1(*bundle.SemanticSpec)
	if bundle.AuthoringPlan.SemanticSpecSHA256 != semanticSHA {
		return nil, s3ContractErrorV1("case-plan authoring ancestry mismatch")
	}
	if _, err := DeriveIntegerBoundaryCasePlanV1(*bundle.SemanticSpec, *bundle.AuthoringPlan); err != nil {
		return nil, s3QualityErrorV1("derive case-plan obligations: %v", err)
	}
	testIdentity, err := canonicalJSONSHA256(in.TestData)
	if err != nil {
		return nil, fmt.Errorf("hash test-data activity result: %w", err)
	}
	plan := S3CasePlanBundleV1{SchemaVersion: S3CasePlanSchemaV1, SemanticSpecSHA256: semanticSHA, AuthoringBundleSHA256: in.ExpectedAuthoringBundleSHA, TestDataIdentitySHA256: testIdentity, Cases: make([]S3ManifestCasePlanV1, len(in.TestData.TestCases)), SanitizerCaseIDs: []string{}}
	sanitizerIdentities := make([]struct {
		TestID      string `json:"test_id"`
		InputSHA256 string `json:"input_sha256"`
	}, 0, maxS3SanitizerCasesV1)
	sanitizerBytes := 0
	for index, sourceCase := range in.TestData.TestCases {
		testID := fmt.Sprintf("case-%06d", index)
		var raw []byte
		var inputRef ArtifactRef
		switch {
		case sourceCase.InputArtifact != nil && sourceCase.Input == "" && sourceCase.InputRef == "":
			inputRef = *sourceCase.InputArtifact
			raw, err = a.readS3ArtifactV1(ctx, inputRef, inputRef.SHA256, inputRef.ContentType, maxS3ManifestBytesV1)
		case sourceCase.InputArtifact == nil && sourceCase.Input != "" && sourceCase.InputRef == "":
			raw = []byte(sourceCase.Input)
			metadata := artifactMetadataFromActivity(ctx)
			metadata.ArtifactType = "qg06_test_input"
			metadata.SourceType = "materialized_test_data_activity_output"
			metadata.RetentionClass = "workflow_cas_unreviewed"
			stored, putErr := a.putArtifactWithMetadata(ctx, raw, "text/plain", metadata)
			if putErr != nil {
				return nil, fmt.Errorf("store case %q input: %w", testID, putErr)
			}
			inputRef = *stored
		default:
			return nil, s3ContractErrorV1("case %q must contain exactly one durable or inline input", testID)
		}
		parsed, parseErr := speccontract.ParseSampleInputV1(*bundle.SemanticSpec, string(raw))
		if parseErr != nil || parsed.CanonicalInput != string(raw) {
			return nil, s3QualityErrorV1("case %q is not canonical SemanticSpec input: %v", testID, parseErr)
		}
		bindings := integerCoverageBindingMapV1(parsed.Bindings)
		boundaryRefs := make([]string, 0)
		boundaryHit := map[string]bool{}
		zeroHit := false
		for _, boundary := range bundle.SemanticSpec.Boundaries {
			if boundary.Allowed && integerBoundaryHitV1(boundary, bindings) {
				boundaryRefs = append(boundaryRefs, boundary.ID)
				boundaryHit[boundary.ID] = true
				zeroHit = zeroHit || boundary.Value == 0
			}
		}
		sort.Strings(boundaryRefs)
		minHit, maxHit := false, false
		for _, constraint := range bundle.SemanticSpec.Constraints {
			if constraint.Min != nil {
				minHit = minHit || integerConstraintEdgeHitV1(constraint, *constraint.Min, bindings)
			}
			if constraint.Max != nil {
				maxHit = maxHit || integerConstraintEdgeHitV1(constraint, *constraint.Max, bindings)
			}
		}
		region := "unassigned"
		for _, intent := range bundle.AuthoringPlan.TestIntents {
			matched := true
			for _, ref := range intent.BoundaryRefs {
				matched = matched && boundaryHit[ref]
			}
			if matched {
				region = intent.ConstraintRegion
				break
			}
		}
		purpose := TestManifestPurposeRandom
		switch {
		case sourceCase.IsSample:
			purpose = TestManifestPurposeSample
		case len(boundaryRefs) > 0:
			purpose = TestManifestPurposeBoundary
		case maxHit:
			purpose = TestManifestPurposeExtreme
		case minHit || zeroHit:
			purpose = TestManifestPurposeTiny
		}
		seed := sourceCase.GeneratorSeed
		if sourceCase.Origin != TestCaseOriginGenerator {
			digest := sha256.Sum256(raw)
			seed = int64(binary.BigEndian.Uint64(digest[:8]) & uint64(^uint64(0)>>1))
		}
		plan.Cases[index] = S3ManifestCasePlanV1{TestID: testID, Purpose: purpose, ConstraintRegion: region, BoundaryRefs: boundaryRefs, Seed: seed, InputArtifact: inputRef, KilledWrongIDs: []string{}, KilledWrongIDsRetentionReason: "not measured by S3 source-of-truth case materializer v1"}
		if len(raw) <= maxS3SanitizerCaseBytesV1 && len(sanitizerIdentities) < maxS3SanitizerCasesV1 && sanitizerBytes+len(raw) <= maxS3SanitizerSuiteBytesV1 {
			plan.SanitizerCaseIDs = append(plan.SanitizerCaseIDs, testID)
			sanitizerIdentities = append(sanitizerIdentities, struct {
				TestID      string `json:"test_id"`
				InputSHA256 string `json:"input_sha256"`
			}{testID, inputRef.SHA256})
			sanitizerBytes += len(raw)
		}
	}
	if len(plan.SanitizerCaseIDs) == 0 {
		return nil, s3QualityErrorV1("no generated case satisfies the fixed small sanitizer-suite limits")
	}
	_, suiteSHA, err := canonicalJSONBytesAndSHA256(sanitizerIdentities)
	if err != nil {
		return nil, fmt.Errorf("hash sanitizer suite identity: %w", err)
	}
	plan.SanitizerSuiteSHA256 = suiteSHA
	encoded, planSHA, err := canonicalJSONBytesAndSHA256(plan)
	if err != nil {
		return nil, fmt.Errorf("canonicalize S3 case plan: %w", err)
	}
	ref, err := a.putS3ReceiptV1(ctx, encoded, "qg06_source_case_plan")
	if err != nil {
		return nil, err
	}
	if ref.SHA256 != planSHA || ref.Producer != "MaterializeS3CasePlanActivityV1" {
		return nil, fmt.Errorf("S3 case-plan CAS identity mismatch")
	}
	return &MaterializeS3CasePlanResultV1{PayloadVersion: S3QualityPayloadVersionV1, TestCount: len(plan.Cases), CasePlanSHA256: planSHA, CasePlanArtifact: ref, SanitizerSuiteSHA256: suiteSHA, SanitizerCaseCount: len(plan.SanitizerCaseIDs)}, nil
}

type S3OracleGateInputV1 struct {
	PayloadVersion             int                             `json:"payload_version"`
	ExpectedSemanticSpecSHA256 string                          `json:"expected_semantic_spec_sha256"`
	MainCandidate              GenerateMainSolutionResultV1    `json:"main_candidate"`
	OracleCandidate            GenerateOracleCandidateResultV1 `json:"oracle_candidate"`
	CasePlanArtifact           ArtifactRef                     `json:"case_plan_artifact"`
	Limits                     ExecutionLimits                 `json:"limits"`
}

type S3OracleGateReceiptV1 struct {
	SchemaVersion       string                    `json:"schema_version"`
	Status              string                    `json:"status"`
	SemanticSpecSHA256  string                    `json:"semantic_spec_sha256"`
	CandidateSHA256     string                    `json:"candidate_sha256"`
	DifferentialCaseIDs []string                  `json:"differential_case_ids"`
	OrderedInputSHA256  []string                  `json:"ordered_input_sha256"`
	MainRunSHA256       string                    `json:"main_run_sha256,omitempty"`
	OracleRunSHA256     string                    `json:"oracle_run_sha256,omitempty"`
	SandboxIdentity     SandboxAuditMetadata      `json:"sandbox_identity"`
	FirstMismatchCaseID string                    `json:"first_mismatch_case_id,omitempty"`
	Promotion           *OraclePromotionReceiptV1 `json:"promotion,omitempty"`
}

type S3OracleGateResultV1 struct {
	PayloadVersion                 int                        `json:"payload_version"`
	Gate                           qualitygate.GateEvidenceV1 `json:"gate"`
	ReceiptSHA256                  string                     `json:"receipt_sha256"`
	ReceiptArtifact                *ArtifactRef               `json:"receipt_artifact"`
	VerifiedProgramReceiptSHA256   string                     `json:"verified_program_receipt_sha256,omitempty"`
	VerifiedProgramReceiptArtifact *ArtifactRef               `json:"verified_program_receipt_artifact,omitempty"`
	MainRun                        *SandboxResult             `json:"main_run,omitempty"`
	OracleRun                      *SandboxResult             `json:"oracle_run,omitempty"`
}

// VerifyProgramAgainstIndependentOracleActivityV1 is the only producer of the
// verified-program receipt consumed by QG-03. It runs both CAS-bound programs
// in the server sandbox, performs byte-exact differential comparison, enforces
// provider independence, and only then signs the receipt.
func (a *Activities) VerifyProgramAgainstIndependentOracleActivityV1(ctx context.Context, in S3OracleGateInputV1) (*S3OracleGateResultV1, error) {
	if in.PayloadVersion != S3QualityPayloadVersionV1 || !isManifestSHA256(in.ExpectedSemanticSpecSHA256) || in.Limits.TimeLimitMs <= 0 || in.Limits.MemoryLimitMB <= 0 {
		return nil, s3ContractErrorV1("invalid server oracle-verification input")
	}
	if in.MainCandidate.SchemaVersion != GenerateMainSolutionSchemaV1 || in.MainCandidate.SemanticSpecSHA256 != in.ExpectedSemanticSpecSHA256 || in.MainCandidate.CandidateSourceArtifact == nil || in.MainCandidate.CandidateSourceArtifact.Producer != "GenerateMainSolutionActivityV1" || in.MainCandidate.ProviderResponseArtifact == nil || in.MainCandidate.ProviderResponseArtifact.LLMCallReceipt == nil || in.OracleCandidate.CandidateSourceArtifact == nil || in.OracleCandidate.CandidateSourceArtifact.Producer != "GenerateOracleCandidateActivityV1" || in.OracleCandidate.SourceBinding.SemanticSpecSHA256 != in.ExpectedSemanticSpecSHA256 {
		return nil, s3ContractErrorV1("main/oracle candidate binding is incomplete")
	}
	casePlan, _, err := a.loadS3CasePlanV1(ctx, in.CasePlanArtifact)
	if err != nil {
		return nil, err
	}
	if casePlan.SemanticSpecSHA256 != in.ExpectedSemanticSpecSHA256 {
		return nil, s3ContractErrorV1("oracle case plan belongs to a different SemanticSpec")
	}
	caseIDs := make([]string, len(casePlan.Cases))
	inputSHA := make([]string, len(casePlan.Cases))
	testCases := make([]TestCaseData, len(casePlan.Cases))
	for index, testCase := range casePlan.Cases {
		caseIDs[index] = testCase.TestID
		inputSHA[index] = testCase.InputArtifact.SHA256
		inputRef := testCase.InputArtifact
		testCases[index] = TestCaseData{InputArtifact: &inputRef, Origin: TestCaseOriginCustom}
	}
	mainSource, err := a.readS3ArtifactV1(ctx, *in.MainCandidate.CandidateSourceArtifact, in.MainCandidate.CandidateSourceSHA256, in.MainCandidate.CandidateSourceArtifact.ContentType, maxOracleCandidateSourceBytesV1)
	if err != nil {
		return nil, err
	}
	oracleSource, err := a.readS3ArtifactV1(ctx, *in.OracleCandidate.CandidateSourceArtifact, in.OracleCandidate.CandidateSourceSHA256, in.OracleCandidate.CandidateSourceArtifact.ContentType, maxOracleCandidateSourceBytesV1)
	if err != nil {
		return nil, err
	}
	mainSolution := domain.Solution{Language: in.MainCandidate.Language, SourceCode: string(mainSource), SolutionType: "main"}
	oracleSolution := domain.Solution{Language: in.OracleCandidate.Language, SourceCode: string(oracleSource), SolutionType: domain.SolutionTypeBrute}
	stableLimits := in.Limits
	stableLimits.OutputLimitBytes = S3StableOutputLimitBytesV1
	stableLimits.Profile = ""
	stableLimits.PreserveOutputBytes = true
	mainRun, err := a.RunSandboxActivity(ctx, mainSolution, testCases, stableLimits)
	if err != nil {
		return nil, fmt.Errorf("run main candidate for independent verification: %w", err)
	}
	oracleRun, err := a.RunSandboxActivity(ctx, oracleSolution, testCases, stableLimits)
	if err != nil {
		return nil, fmt.Errorf("run oracle candidate for independent verification: %w", err)
	}
	mainOutputs, err := a.resolveS3SandboxOutputsV1(ctx, *mainRun)
	if err != nil {
		return nil, err
	}
	oracleOutputs, err := a.resolveS3SandboxOutputsV1(ctx, *oracleRun)
	if err != nil {
		return nil, err
	}
	if len(mainOutputs) != len(caseIDs) || len(oracleOutputs) != len(caseIDs) {
		return nil, s3ContractErrorV1("differential sandbox output count mismatch")
	}
	if !sameS3SandboxIdentityV1(mainRun.Audit, oracleRun.Audit) {
		return nil, s3ContractErrorV1("main and independent oracle ran under different sandbox identities")
	}
	if err := a.validateS3SandboxIdentityPolicyV1(mainRun.Audit); err != nil {
		return nil, err
	}
	_, mainRunSHA, err := canonicalJSONBytesAndSHA256(*mainRun)
	if err != nil {
		return nil, fmt.Errorf("hash main sandbox run: %w", err)
	}
	_, oracleRunSHA, err := canonicalJSONBytesAndSHA256(*oracleRun)
	if err != nil {
		return nil, fmt.Errorf("hash oracle sandbox run: %w", err)
	}
	mismatches := make([]OracleDifferentialMismatchV1, 0)
	for index := range caseIDs {
		if mainOutputs[index] != oracleOutputs[index] {
			mismatches = append(mismatches, OracleDifferentialMismatchV1{CaseID: caseIDs[index], MainOutputSHA256: sha256Hex([]byte(mainOutputs[index])), OracleOutputSHA256: sha256Hex([]byte(oracleOutputs[index]))})
		}
	}
	promotionInput := ValidateOraclePromotionInputV1{PayloadVersion: OracleCandidatePayloadVersionV1, ExpectedSemanticSpecSHA256: in.ExpectedSemanticSpecSHA256, MainProviderArtifact: *in.MainCandidate.ProviderResponseArtifact, Candidate: in.OracleCandidate, CandidateCompiled: true, DifferentialCaseIDs: caseIDs, Mismatches: mismatches}
	receipt, validationErr := ValidateAndPromoteOracleCandidateV1(promotionInput)
	status := qualitygate.GateStatusPass
	code := ""
	firstMismatch := ""
	if validationErr != nil {
		status = qualitygate.GateStatusBlocked
		switch {
		case len(mismatches) > 0:
			code = "oracle.differential_mismatch"
			firstMismatch = mismatches[0].CaseID
		default:
			code = "oracle.promotion_failed"
		}
	}
	gateReceipt := S3OracleGateReceiptV1{
		SchemaVersion: S3OracleGateReceiptSchemaV1, Status: status,
		SemanticSpecSHA256:  in.ExpectedSemanticSpecSHA256,
		CandidateSHA256:     in.MainCandidate.CandidateSourceSHA256,
		DifferentialCaseIDs: caseIDs, OrderedInputSHA256: inputSHA,
		MainRunSHA256: mainRunSHA, OracleRunSHA256: oracleRunSHA, SandboxIdentity: mainRun.Audit,
		FirstMismatchCaseID: firstMismatch, Promotion: receipt,
	}
	encoded, err := json.Marshal(gateReceipt)
	if err != nil {
		return nil, fmt.Errorf("marshal oracle gate receipt: %w", err)
	}
	ref, err := a.putS3ReceiptV1(ctx, encoded, "qg04_oracle_gate_receipt")
	if err != nil {
		return nil, err
	}
	asset := s3EvidenceAssetV1(*ref)
	blockers := []qualitygate.ReviewBlockerV1{}
	if status == qualitygate.GateStatusBlocked {
		fixture := asset.Ref
		if firstMismatch != "" {
			fixture = firstMismatch
		}
		blockers = []qualitygate.ReviewBlockerV1{s3BlockerV1(code, s3ArtifactRefURI(in.MainCandidate.CandidateSourceArtifact), "algoforge.oracle-promotion.v1", fixture, "candidate must compile, be independent, and match the oracle on every differential case")}
		return &S3OracleGateResultV1{PayloadVersion: S3QualityPayloadVersionV1, Gate: qualitygate.GateEvidenceV1{Status: status, Evidence: asset, Blockers: blockers}, ReceiptSHA256: ref.SHA256, ReceiptArtifact: ref, MainRun: mainRun, OracleRun: oracleRun}, nil
	}
	verified := VerifiedProgramReceiptV1{SchemaVersion: VerifiedProgramReceiptSchemaV1, Status: VerifiedProgramReceiptStatusV1, ProgramSHA256: in.MainCandidate.CandidateSourceSHA256, IndependentOracleReceiptSHA256: ref.SHA256, SandboxImageDigest: mainRun.Audit.ImageDigest, ToolchainManifestDigest: mainRun.Audit.ToolchainManifestDigest, SeccompPolicyDigest: mainRun.Audit.SeccompPolicyDigest, LimitProfile: mainRun.Audit.LimitProfile}
	if err := validateVerifiedProgramReceiptV1(verified); err != nil {
		return nil, s3ContractErrorV1("server verified-program receipt: %v", err)
	}
	verifiedBytes, err := json.Marshal(verified)
	if err != nil {
		return nil, fmt.Errorf("marshal verified-program receipt: %w", err)
	}
	verifiedRef, err := a.putS3ReceiptV1(ctx, verifiedBytes, "qg03_verified_program_receipt")
	if err != nil {
		return nil, err
	}
	if verifiedRef.Producer != VerifiedProgramReceiptProducerV1 {
		return nil, fmt.Errorf("verified-program receipt producer mismatch")
	}
	return &S3OracleGateResultV1{PayloadVersion: S3QualityPayloadVersionV1, Gate: qualitygate.GateEvidenceV1{Status: status, Evidence: asset, Blockers: blockers}, ReceiptSHA256: ref.SHA256, ReceiptArtifact: ref, VerifiedProgramReceiptSHA256: verifiedRef.SHA256, VerifiedProgramReceiptArtifact: verifiedRef, MainRun: mainRun, OracleRun: oracleRun}, nil
}

type RunVerifiedAuthoringSamplesInputV1 struct {
	PayloadVersion                 int             `json:"payload_version"`
	Phase                          string          `json:"phase"`
	ProgramArtifact                ArtifactRef     `json:"program_artifact"`
	Language                       string          `json:"language"`
	VerifiedProgramReceiptArtifact ArtifactRef     `json:"verified_program_receipt_artifact"`
	SourceArtifact                 ArtifactRef     `json:"source_artifact"`
	Limits                         ExecutionLimits `json:"limits"`
}

// RunVerifiedAuthoringSamplesActivityV1 constructs AuthoringSampleSandboxRunV1
// only from CAS bytes and a server-produced verified-program receipt. Callers
// cannot inject a pre-signed SandboxResult.
func (a *Activities) RunVerifiedAuthoringSamplesActivityV1(ctx context.Context, in RunVerifiedAuthoringSamplesInputV1) (*AuthoringSampleSandboxRunV1, error) {
	if in.PayloadVersion != S3QualityPayloadVersionV1 || (in.Phase != AuthoringSampleSandboxRunPhaseFirst && in.Phase != AuthoringSampleSandboxRunPhaseReparse) || in.Limits.TimeLimitMs <= 0 || in.Limits.MemoryLimitMB <= 0 {
		return nil, s3ContractErrorV1("invalid verified authoring-sample run input")
	}
	receiptBytes, err := a.readS3ArtifactV1(ctx, in.VerifiedProgramReceiptArtifact, in.VerifiedProgramReceiptArtifact.SHA256, "application/json", maxVerifiedProgramReceiptBytesV1)
	if err != nil {
		return nil, err
	}
	if in.VerifiedProgramReceiptArtifact.Producer != VerifiedProgramReceiptProducerV1 {
		return nil, s3ContractErrorV1("verified-program receipt has an untrusted producer")
	}
	receipt, err := decodeCanonicalVerifiedProgramReceiptV1(receiptBytes)
	if err != nil {
		return nil, s3ContractErrorV1("decode verified-program receipt: %v", err)
	}
	if in.ProgramArtifact.SHA256 != receipt.ProgramSHA256 {
		return nil, s3ContractErrorV1("sample program does not match verified-program receipt")
	}
	programBytes, err := a.readS3ArtifactV1(ctx, in.ProgramArtifact, receipt.ProgramSHA256, in.ProgramArtifact.ContentType, maxOracleCandidateSourceBytesV1)
	if err != nil {
		return nil, err
	}
	inputs := []string{}
	inputSHA := []string{}
	switch in.Phase {
	case AuthoringSampleSandboxRunPhaseFirst:
		data, err := a.readS3ArtifactV1(ctx, in.SourceArtifact, in.SourceArtifact.SHA256, "application/json", maxValidatedSampleCandidatesBundleBytesV1)
		if err != nil {
			return nil, err
		}
		var bundle ValidatedSampleCandidatesBundleV1
		if err := decodeCanonicalSampleClosureJSONV1(data, &bundle); err != nil || bundle.Status != ValidatedSampleCandidatesStatusInputsOnlyV1 {
			return nil, s3ContractErrorV1("invalid validated sample source")
		}
		inputs = make([]string, len(bundle.Candidates))
		inputSHA = make([]string, len(bundle.Candidates))
		for index, candidate := range bundle.Candidates {
			inputs[index] = candidate.CanonicalInput
			inputSHA[index] = candidate.CanonicalInputSHA256
		}
	case AuthoringSampleSandboxRunPhaseReparse:
		data, err := a.readS3ArtifactV1(ctx, in.SourceArtifact, in.SourceArtifact.SHA256, "application/json", maxStagedAuthoringStatementSamplesBytesV1)
		if err != nil {
			return nil, err
		}
		var staged StagedAuthoringStatementSamplesBundleV1
		if err := decodeCanonicalSampleClosureJSONV1(data, &staged); err != nil {
			return nil, s3ContractErrorV1("decode staged statement samples: %v", err)
		}
		parsed, stagedReceipt, err := validateAndParseStagedAuthoringSamplesV1(staged)
		if err != nil || stagedReceipt.ProgramSHA256 != receipt.ProgramSHA256 || staged.VerifiedProgramReceiptSHA256 != in.VerifiedProgramReceiptArtifact.SHA256 {
			return nil, s3ContractErrorV1("staged statement receipt ancestry mismatch")
		}
		inputs = make([]string, len(parsed))
		inputSHA = make([]string, len(parsed))
		for index, sample := range parsed {
			inputs[index] = sample.Input
			inputSHA[index] = sha256Hex([]byte(sample.Input))
		}
	}
	testCases := make([]TestCaseData, len(inputs))
	for index, input := range inputs {
		testCases[index] = TestCaseData{Input: input, IsSample: true, Origin: TestCaseOriginCustom}
	}
	stableLimits := in.Limits
	stableLimits.OutputLimitBytes = S3StableOutputLimitBytesV1
	stableLimits.Profile = ""
	stableLimits.PreserveOutputBytes = true
	run, err := a.RunSandboxActivity(ctx, domain.Solution{Language: in.Language, SourceCode: string(programBytes), SolutionType: domain.SolutionTypeMain}, testCases, stableLimits)
	if err != nil {
		return nil, fmt.Errorf("run verified authoring samples: %w", err)
	}
	resolved, err := a.resolveS3SandboxOutputsV1(ctx, *run)
	if err != nil {
		return nil, err
	}
	// Keep the QG-03 run envelope self-contained even when the nested sandbox
	// externalized large output; the final QG-03 artifact remains bounded.
	run.Outputs = resolved
	run.OutputArtifacts = nil
	if run.Audit.ImageDigest != receipt.SandboxImageDigest || run.Audit.ToolchainManifestDigest != receipt.ToolchainManifestDigest || run.Audit.SeccompPolicyDigest != receipt.SeccompPolicyDigest || run.Audit.LimitProfile != receipt.LimitProfile {
		return nil, s3QualityErrorV1("sample sandbox environment differs from verified-program receipt")
	}
	return &AuthoringSampleSandboxRunV1{SchemaVersion: AuthoringSampleSandboxRunSchemaV1, Phase: in.Phase, VerifiedProgramReceiptSHA256: in.VerifiedProgramReceiptArtifact.SHA256, ProgramSHA256: receipt.ProgramSHA256, IndependentOracleReceiptSHA256: receipt.IndependentOracleReceiptSHA256, InputSHA256: inputSHA, SandboxOutput: *run}, nil
}

type S3SanitizerExecutionV1 struct {
	SchemaVersion                string               `json:"schema_version"`
	Profile                      string               `json:"profile"`
	Language                     string               `json:"language"`
	SemanticSpecSHA256           string               `json:"semantic_spec_sha256"`
	CandidateSHA256              string               `json:"candidate_sha256"`
	VerifiedProgramReceiptSHA256 string               `json:"verified_program_receipt_sha256"`
	SmallSuiteSHA256             string               `json:"small_suite_sha256"`
	CaseIDs                      []string             `json:"case_ids"`
	Passed                       bool                 `json:"passed"`
	FindingCodes                 []string             `json:"finding_codes"`
	Audit                        SandboxAuditMetadata `json:"audit"`
}

type S3SanitizerReceiptV1 struct {
	SchemaVersion string                 `json:"schema_version"`
	Execution     S3SanitizerExecutionV1 `json:"execution"`
}

type S3SanitizerGateInputV1 struct {
	PayloadVersion                 int             `json:"payload_version"`
	CandidateArtifact              ArtifactRef     `json:"candidate_artifact"`
	Language                       string          `json:"language"`
	VerifiedProgramReceiptArtifact ArtifactRef     `json:"verified_program_receipt_artifact"`
	CasePlanArtifact               ArtifactRef     `json:"case_plan_artifact"`
	ExpectedSmallSuiteSHA256       string          `json:"expected_small_suite_sha256"`
	Limits                         ExecutionLimits `json:"limits"`
}

type S3SanitizerGateResultV1 struct {
	PayloadVersion  int                        `json:"payload_version"`
	Gate            qualitygate.GateEvidenceV1 `json:"gate"`
	ReceiptSHA256   string                     `json:"receipt_sha256"`
	ReceiptArtifact *ArtifactRef               `json:"receipt_artifact"`
}

func (a *Activities) RunSanitizerGateActivityV1(ctx context.Context, in S3SanitizerGateInputV1) (*S3SanitizerGateResultV1, error) {
	if in.PayloadVersion != S3QualityPayloadVersionV1 || !isManifestSHA256(in.ExpectedSmallSuiteSHA256) || in.Limits.TimeLimitMs <= 0 || in.Limits.MemoryLimitMB <= 0 {
		return nil, s3ContractErrorV1("invalid sanitizer gate input")
	}
	if err := in.CandidateArtifact.Validate(in.CandidateArtifact.Bucket); err != nil {
		return nil, s3ContractErrorV1("sanitizer candidate binding is invalid")
	}
	if in.CandidateArtifact.Producer != "GenerateMainSolutionActivityV1" || in.VerifiedProgramReceiptArtifact.Producer != VerifiedProgramReceiptProducerV1 {
		return nil, s3ContractErrorV1("sanitizer candidate or verified receipt has an untrusted producer")
	}
	if in.Language != "c" && in.Language != "cpp" && in.Language != "c++" && in.Language != "cc" {
		return nil, s3ContractErrorV1("sanitizer-c-cpp-v1 requires C or C++")
	}
	receiptBytes, err := a.readS3ArtifactV1(ctx, in.VerifiedProgramReceiptArtifact, in.VerifiedProgramReceiptArtifact.SHA256, "application/json", maxVerifiedProgramReceiptBytesV1)
	if err != nil {
		return nil, err
	}
	verified, err := decodeCanonicalVerifiedProgramReceiptV1(receiptBytes)
	if err != nil || verified.ProgramSHA256 != in.CandidateArtifact.SHA256 {
		return nil, s3ContractErrorV1("sanitizer candidate is not the server-verified program")
	}
	if err := a.validateS3SandboxIdentityPolicyV1(SandboxAuditMetadata{ImageDigest: verified.SandboxImageDigest, ToolchainManifestDigest: verified.ToolchainManifestDigest, SeccompPolicyDigest: verified.SeccompPolicyDigest, LimitProfile: verified.LimitProfile}); err != nil {
		return nil, err
	}
	casePlan, _, err := a.loadS3CasePlanV1(ctx, in.CasePlanArtifact)
	if err != nil {
		return nil, err
	}
	if casePlan.SanitizerSuiteSHA256 != in.ExpectedSmallSuiteSHA256 || len(casePlan.SanitizerCaseIDs) == 0 || len(casePlan.SanitizerCaseIDs) > maxS3SanitizerCasesV1 {
		return nil, s3ContractErrorV1("sanitizer suite identity does not match the D-side case plan")
	}
	caseByID := make(map[string]S3ManifestCasePlanV1, len(casePlan.Cases))
	for _, item := range casePlan.Cases {
		caseByID[item.TestID] = item
	}
	caseIDs := append([]string(nil), casePlan.SanitizerCaseIDs...)
	inputs := make([]string, len(caseIDs))
	totalInputBytes := 0
	for index, caseID := range caseIDs {
		testCase, exists := caseByID[caseID]
		if !exists {
			return nil, s3ContractErrorV1("sanitizer suite references an absent case")
		}
		data, err := a.readS3ArtifactV1(ctx, testCase.InputArtifact, testCase.InputArtifact.SHA256, testCase.InputArtifact.ContentType, maxS3SanitizerCaseBytesV1)
		if err != nil {
			return nil, err
		}
		totalInputBytes += len(data)
		if totalInputBytes > maxS3SanitizerSuiteBytesV1 {
			return nil, s3ContractErrorV1("sanitizer suite exceeds its fixed small-suite byte limit")
		}
		inputs[index] = string(data)
	}
	source, err := a.readS3ArtifactV1(ctx, in.CandidateArtifact, in.CandidateArtifact.SHA256, in.CandidateArtifact.ContentType, maxOracleCandidateSourceBytesV1)
	if err != nil {
		return nil, err
	}
	execution := S3SanitizerExecutionV1{SchemaVersion: S3SanitizerExecutionSchemaV1, Profile: remotesandbox.SanitizerCAndCPPProfileV1, Language: in.Language, SemanticSpecSHA256: casePlan.SemanticSpecSHA256, CandidateSHA256: in.CandidateArtifact.SHA256, VerifiedProgramReceiptSHA256: in.VerifiedProgramReceiptArtifact.SHA256, SmallSuiteSHA256: in.ExpectedSmallSuiteSHA256, CaseIDs: caseIDs, FindingCodes: []string{}}
	status := qualitygate.GateStatusCheckFailed
	executor, executorErr := a.remoteSandboxExecutor(a.sandboxBatchTimeout(len(inputs), in.Limits.TimeLimitMs))
	if executorErr == nil {
		limits := remotesandbox.NewRemoteLimits(in.Limits.TimeLimitMs, in.Limits.MemoryLimitMB)
		limits.OutputLimitBytes = S3StableOutputLimitBytesV1
		limits.Profile = remotesandbox.SanitizerCAndCPPProfileV1
		remoteResult, executeErr := executor.Execute(ctx, in.Language, string(source), inputs, limits)
		if executeErr == nil && remoteResult != nil {
			execution.Audit = SandboxAuditMetadata{RunID: remoteResult.Audit.RunID, ManifestDigest: remoteResult.Audit.ManifestDigest, Seed: remoteResult.Audit.Seed, LimitProfile: remoteResult.Audit.LimitProfile, Profile: remoteResult.Audit.Profile, ImageDigest: remoteResult.Audit.ImageDigest, ToolchainManifestDigest: remoteResult.Audit.ToolchainManifestDigest, SeccompPolicyDigest: remoteResult.Audit.SeccompPolicyDigest}
			if remoteResult.Audit.Profile != remotesandbox.SanitizerCAndCPPProfileV1 || !strings.HasSuffix(remoteResult.Audit.LimitProfile, ",profile="+remotesandbox.SanitizerCAndCPPProfileV1) || remoteResult.Audit.ImageDigest != verified.SandboxImageDigest || remoteResult.Audit.ToolchainManifestDigest != verified.ToolchainManifestDigest || remoteResult.Audit.SeccompPolicyDigest != verified.SeccompPolicyDigest || len(remoteResult.Results) != len(inputs) {
				return nil, s3ContractErrorV1("sanitizer sandbox omitted the requested profile, trusted environment identity, or case results")
			}
			status = qualitygate.GateStatusPass
			if !remoteResult.Compile.Success {
				status = qualitygate.GateStatusBlocked
				execution.FindingCodes = []string{"sanitizer.compile_failed"}
			}
			for _, item := range remoteResult.Results {
				if item.Verdict != remotesandbox.VerdictOK {
					status = qualitygate.GateStatusBlocked
					execution.FindingCodes = []string{"sanitizer.runtime_finding"}
					break
				}
			}
		}
	}
	execution.Passed = status == qualitygate.GateStatusPass
	receipt := S3SanitizerReceiptV1{SchemaVersion: S3SanitizerReceiptSchemaV1, Execution: execution}
	encoded, err := json.Marshal(receipt)
	if err != nil {
		return nil, fmt.Errorf("marshal sanitizer receipt: %w", err)
	}
	ref, err := a.putS3ReceiptV1(ctx, encoded, "qg05_sanitizer_receipt")
	if err != nil {
		return nil, err
	}
	blockers := []qualitygate.ReviewBlockerV1{}
	if status == qualitygate.GateStatusBlocked {
		blockers = []qualitygate.ReviewBlockerV1{s3BlockerV1("sanitizer.finding", s3ArtifactRefURI(&in.CandidateArtifact), "algoforge.sanitizer-c-cpp.v1", s3ArtifactRefURI(ref), "sanitizer finding_codes must be empty on the fixed small-case suite")}
	}
	return &S3SanitizerGateResultV1{PayloadVersion: S3QualityPayloadVersionV1, Gate: qualitygate.GateEvidenceV1{Status: status, Evidence: s3EvidenceAssetV1(*ref), Blockers: blockers}, ReceiptSHA256: ref.SHA256, ReceiptArtifact: ref}, nil
}

type S3ManifestCasePlanV1 struct {
	TestID                        string      `json:"test_id"`
	Purpose                       string      `json:"purpose"`
	ConstraintRegion              string      `json:"constraint_region"`
	BoundaryRefs                  []string    `json:"boundary_refs"`
	Seed                          int64       `json:"seed"`
	InputArtifact                 ArtifactRef `json:"input_artifact"`
	KilledWrongIDs                []string    `json:"killed_wrong_ids"`
	KilledWrongIDsRetentionReason string      `json:"killed_wrong_ids_retention_reason"`
}

type S3ManifestGateInputV1 struct {
	PayloadVersion                 int           `json:"payload_version"`
	SemanticSpecArtifact           ArtifactRef   `json:"semantic_spec_artifact"`
	AuthoringBundleArtifact        ArtifactRef   `json:"authoring_bundle_artifact"`
	CasePlanArtifact               ArtifactRef   `json:"case_plan_artifact"`
	MainRun                        SandboxResult `json:"main_run"`
	OracleReceiptArtifact          ArtifactRef   `json:"oracle_receipt_artifact"`
	VerifiedProgramReceiptArtifact ArtifactRef   `json:"verified_program_receipt_artifact"`
	SanitizerReceiptArtifact       ArtifactRef   `json:"sanitizer_receipt_artifact"`
}

type S3ManifestGateResultV1 struct {
	PayloadVersion          int                        `json:"payload_version"`
	BoundaryCoverageGate    qualitygate.GateEvidenceV1 `json:"boundary_coverage_gate"`
	TestManifestGate        qualitygate.GateEvidenceV1 `json:"test_manifest_gate"`
	BoundaryReceiptSHA256   string                     `json:"boundary_receipt_sha256"`
	BoundaryReceiptArtifact *ArtifactRef               `json:"boundary_receipt_artifact"`
	TestManifestSHA256      string                     `json:"test_manifest_sha256"`
	TestManifestArtifact    *ArtifactRef               `json:"test_manifest_artifact"`
}

// BuildS3TestManifestActivityV1 is the D-side source of truth for the final
// TestManifest v2. The workflow supplies raw case-plan inputs plus the actual
// server sandbox result; callers never supply a pre-approved manifest.
func (a *Activities) BuildS3TestManifestActivityV1(ctx context.Context, in S3ManifestGateInputV1) (*S3ManifestGateResultV1, error) {
	if in.PayloadVersion != S3QualityPayloadVersionV1 || in.MainRun.PayloadVersion != ActivityPayloadVersion {
		return nil, s3ContractErrorV1("invalid manifest gate input")
	}
	casePlan, _, err := a.loadS3CasePlanV1(ctx, in.CasePlanArtifact)
	if err != nil {
		return nil, err
	}
	specBytes, err := a.readS3ArtifactV1(ctx, in.SemanticSpecArtifact, in.SemanticSpecArtifact.SHA256, "application/json", maxS3SemanticSpecBytesV1)
	if err != nil {
		return nil, err
	}
	var semanticSpec structPlaceholderSemanticSpecV1
	if err := json.Unmarshal(specBytes, &semanticSpec); err != nil {
		return nil, s3ContractErrorV1("decode standalone SemanticSpec preflight: %v", err)
	}
	// Re-decode with the canonical domain type through the same strict path V
	// uses. This also prevents an authoring plan from being smuggled into the
	// standalone artifact.
	_, specValue, err := validateOracleCandidateInputV1(ctx, a, GenerateOracleCandidateInputV1{
		PayloadVersion:             OracleCandidatePayloadVersionV1,
		SemanticSpecArtifact:       in.SemanticSpecArtifact,
		ExpectedSemanticSpecSHA256: in.SemanticSpecArtifact.SHA256,
		Language:                   "cpp",
	})
	if err != nil {
		return nil, s3ContractErrorV1("validate standalone SemanticSpec: %v", err)
	}
	bundleBytes, err := a.readS3ArtifactV1(ctx, in.AuthoringBundleArtifact, in.AuthoringBundleArtifact.SHA256, "application/json", maxAuthoringPlanBundleBytes)
	if err != nil {
		return nil, err
	}
	bundle, err := decodeCanonicalAuthoringBundleV1(bundleBytes)
	if err != nil || bundle.AuthoringPlan == nil || bundle.SemanticSpec == nil || bundle.Decision != AuthoringPlanDecisionAccepted {
		return nil, s3ContractErrorV1("manifest gate requires an accepted authoring bundle")
	}
	if speccontract.SemanticSpecSHA256V1(specValue) != speccontract.SemanticSpecSHA256V1(*bundle.SemanticSpec) || casePlan.SemanticSpecSHA256 != in.SemanticSpecArtifact.SHA256 || casePlan.AuthoringBundleSHA256 != in.AuthoringBundleArtifact.SHA256 {
		return nil, s3ContractErrorV1("manifest SemanticSpec ancestry mismatch")
	}
	oracleBytes, err := a.readS3ArtifactV1(ctx, in.OracleReceiptArtifact, in.OracleReceiptArtifact.SHA256, "application/json", maxS3ManifestBytesV1)
	if err != nil {
		return nil, err
	}
	if in.OracleReceiptArtifact.Producer != VerifiedProgramReceiptProducerV1 {
		return nil, s3ContractErrorV1("oracle receipt has an untrusted producer")
	}
	var oracleReceipt S3OracleGateReceiptV1
	if err := decodeCanonicalSampleClosureJSONV1(oracleBytes, &oracleReceipt); err != nil || oracleReceipt.Status != qualitygate.GateStatusPass || oracleReceipt.Promotion == nil || !oracleReceipt.Promotion.Promoted || oracleReceipt.SemanticSpecSHA256 != in.SemanticSpecArtifact.SHA256 {
		return nil, s3ContractErrorV1("oracle receipt is not a canonical promoted server result")
	}
	if len(oracleReceipt.DifferentialCaseIDs) != len(casePlan.Cases) || len(oracleReceipt.OrderedInputSHA256) != len(casePlan.Cases) {
		return nil, s3ContractErrorV1("oracle receipt case lineage count differs from the D-side case plan")
	}
	for index, item := range casePlan.Cases {
		if oracleReceipt.DifferentialCaseIDs[index] != item.TestID || oracleReceipt.OrderedInputSHA256[index] != item.InputArtifact.SHA256 {
			return nil, s3ContractErrorV1("oracle receipt case lineage differs from the D-side case plan")
		}
	}
	_, actualMainRunSHA, err := canonicalJSONBytesAndSHA256(in.MainRun)
	if err != nil || actualMainRunSHA != oracleReceipt.MainRunSHA256 || !sameS3SandboxIdentityV1(in.MainRun.Audit, oracleReceipt.SandboxIdentity) {
		return nil, s3ContractErrorV1("main sandbox run was spliced from a different oracle verification")
	}
	verifiedBytes, err := a.readS3ArtifactV1(ctx, in.VerifiedProgramReceiptArtifact, in.VerifiedProgramReceiptArtifact.SHA256, "application/json", maxVerifiedProgramReceiptBytesV1)
	if err != nil {
		return nil, err
	}
	if in.VerifiedProgramReceiptArtifact.Producer != VerifiedProgramReceiptProducerV1 {
		return nil, s3ContractErrorV1("verified-program receipt has an untrusted producer")
	}
	verified, err := decodeCanonicalVerifiedProgramReceiptV1(verifiedBytes)
	if err != nil || verified.ProgramSHA256 != oracleReceipt.CandidateSHA256 || verified.IndependentOracleReceiptSHA256 != in.OracleReceiptArtifact.SHA256 {
		return nil, s3ContractErrorV1("verified-program receipt does not bind this oracle gate")
	}
	sanitizerBytes, err := a.readS3ArtifactV1(ctx, in.SanitizerReceiptArtifact, in.SanitizerReceiptArtifact.SHA256, "application/json", maxS3ManifestBytesV1)
	if err != nil {
		return nil, err
	}
	if in.SanitizerReceiptArtifact.Producer != "RunSanitizerGateActivityV1" {
		return nil, s3ContractErrorV1("sanitizer receipt has an untrusted producer")
	}
	var sanitizerReceipt S3SanitizerReceiptV1
	if err := decodeCanonicalSampleClosureJSONV1(sanitizerBytes, &sanitizerReceipt); err != nil || sanitizerReceipt.SchemaVersion != S3SanitizerReceiptSchemaV1 || !sanitizerReceipt.Execution.Passed || sanitizerReceipt.Execution.SemanticSpecSHA256 != in.SemanticSpecArtifact.SHA256 || sanitizerReceipt.Execution.CandidateSHA256 != oracleReceipt.CandidateSHA256 || sanitizerReceipt.Execution.VerifiedProgramReceiptSHA256 != in.VerifiedProgramReceiptArtifact.SHA256 || sanitizerReceipt.Execution.SmallSuiteSHA256 != casePlan.SanitizerSuiteSHA256 {
		return nil, s3ContractErrorV1("sanitizer receipt is not a canonical passing server result")
	}
	planSHA, err := canonicalJSONSHA256(*bundle.AuthoringPlan)
	if err != nil {
		return nil, fmt.Errorf("hash AuthoringPlan: %w", err)
	}
	mainOutputs, err := a.resolveS3SandboxOutputsV1(ctx, in.MainRun)
	if err != nil {
		return nil, err
	}
	if len(mainOutputs) != len(casePlan.Cases) || len(in.MainRun.TimeTaken) != len(casePlan.Cases) || len(in.MainRun.MemoryUsed) != len(casePlan.Cases) {
		return nil, s3ContractErrorV1("main sandbox result count does not match manifest case plan")
	}
	manifest := TestManifestV2{SchemaVersion: TestManifestSchemaVersionV2, SemanticSpecSHA256: in.SemanticSpecArtifact.SHA256, AuthoringPlanSHA256: planSHA, OraclePromotionReceiptSHA256: in.OracleReceiptArtifact.SHA256, SanitizerReceiptSHA256: in.SanitizerReceiptArtifact.SHA256, BoundaryCoverageReceiptSHA256: "", TestCount: len(casePlan.Cases), Cases: make([]TestManifestCaseV2, len(casePlan.Cases))}
	resolved := make([]IntegerBoundaryCoverageCaseInputV1, len(casePlan.Cases))
	for index, planCase := range casePlan.Cases {
		if planCase.TestID == "" || planCase.TestID != strings.TrimSpace(planCase.TestID) || (index > 0 && casePlan.Cases[index-1].TestID >= planCase.TestID) {
			return nil, s3ContractErrorV1("manifest case plans must be sorted and unique")
		}
		inputBytes, readErr := a.readS3ArtifactV1(ctx, planCase.InputArtifact, planCase.InputArtifact.SHA256, planCase.InputArtifact.ContentType, maxS3ManifestBytesV1)
		if readErr != nil {
			return nil, s3QualityErrorV1("resolve manifest case %q input: %v", planCase.TestID, readErr)
		}
		outputMetadata := artifactMetadataFromActivity(ctx)
		outputMetadata.ArtifactType = "qg06_test_output"
		outputMetadata.SourceType = "verified_main_sandbox_output"
		outputMetadata.RetentionClass = "workflow_cas_unreviewed"
		outputRef, putErr := a.putArtifactWithMetadata(ctx, []byte(mainOutputs[index]), "text/plain", outputMetadata)
		if putErr != nil {
			return nil, fmt.Errorf("store manifest output %q: %w", planCase.TestID, putErr)
		}
		seed := planCase.Seed
		inputRef := planCase.InputArtifact
		manifest.Cases[index] = TestManifestCaseV2{TestIndex: index, TestID: planCase.TestID, Purpose: planCase.Purpose, ConstraintRegion: planCase.ConstraintRegion, BoundaryRefs: append([]string(nil), planCase.BoundaryRefs...), Seed: &seed, InputArtifact: &inputRef, InputSHA256: inputRef.SHA256, OutputArtifact: outputRef, OutputSHA256: outputRef.SHA256, KilledWrongIDs: copyPresentS3StringsV1(planCase.KilledWrongIDs), KilledWrongIDsRetentionReason: planCase.KilledWrongIDsRetentionReason}
		resolved[index] = IntegerBoundaryCoverageCaseInputV1{TestID: planCase.TestID, Input: string(inputBytes)}
	}
	coverage, err := ValidateIntegerBoundaryCoverageV1(specValue, *bundle.AuthoringPlan, manifest, resolved)
	if err != nil {
		return nil, s3QualityErrorV1("validate automatic boundary coverage: %v", err)
	}
	coverageBytes, coverageSHA, err := CanonicalIntegerBoundaryCoverageReceiptV1(*coverage)
	if err != nil {
		return nil, s3ContractErrorV1("canonicalize boundary coverage receipt: %v", err)
	}
	coverageRef, err := a.putS3ReceiptV1(ctx, coverageBytes, "qg06_boundary_coverage_receipt")
	if err != nil {
		return nil, err
	}
	if coverageRef.SHA256 != coverageSHA {
		return nil, fmt.Errorf("boundary coverage CAS identity mismatch")
	}
	manifest.BoundaryCoverageReceiptSHA256 = coverageSHA
	manifestBytes, manifestSHA, err := CanonicalTestManifestV2JSON(manifest)
	if err != nil {
		return nil, s3QualityErrorV1("build canonical TestManifest v2: %v", err)
	}
	manifestRef, err := a.putS3ReceiptV1(ctx, manifestBytes, "qg06_test_manifest_v2")
	if err != nil {
		return nil, err
	}
	if manifestRef.SHA256 != manifestSHA || manifestRef.Producer != "BuildS3TestManifestActivityV1" {
		return nil, fmt.Errorf("TestManifest v2 CAS identity mismatch")
	}
	return &S3ManifestGateResultV1{
		PayloadVersion:        S3QualityPayloadVersionV1,
		BoundaryCoverageGate:  qualitygate.GateEvidenceV1{Status: qualitygate.GateStatusPass, Evidence: s3EvidenceAssetV1(*coverageRef), Blockers: []qualitygate.ReviewBlockerV1{}},
		TestManifestGate:      qualitygate.GateEvidenceV1{Status: qualitygate.GateStatusPass, Evidence: s3EvidenceAssetV1(*manifestRef), Blockers: []qualitygate.ReviewBlockerV1{}},
		BoundaryReceiptSHA256: coverageSHA, BoundaryReceiptArtifact: coverageRef,
		TestManifestSHA256: manifestRef.SHA256, TestManifestArtifact: manifestRef,
	}, nil
}

// structPlaceholderSemanticSpecV1 is intentionally empty and is used only to
// prove the artifact contains a JSON object before the strict canonical decode.
type structPlaceholderSemanticSpecV1 struct{}

type S3DedupGateInputV1 struct {
	PayloadVersion               int         `json:"payload_version"`
	FinalStatementArtifact       ArtifactRef `json:"final_statement_artifact"`
	ExpectedFinalStatementSHA256 string      `json:"expected_final_statement_sha256"`
	StatementDraftArtifact       ArtifactRef `json:"statement_draft_artifact"`
	ExpectedStatementDraftSHA256 string      `json:"expected_statement_draft_sha256"`
	ExpectedMarkdownSHA256       string      `json:"expected_markdown_sha256"`
}

type S3DedupGateResultV1 struct {
	PayloadVersion  int                         `json:"payload_version"`
	Gate            qualitygate.DedupEvidenceV1 `json:"gate"`
	ReceiptSHA256   string                      `json:"receipt_sha256"`
	ReceiptArtifact *ArtifactRef                `json:"receipt_artifact"`
}

func (a *Activities) S3DedupGateActivityV1(ctx context.Context, in S3DedupGateInputV1) (*S3DedupGateResultV1, error) {
	if in.PayloadVersion != S3QualityPayloadVersionV1 ||
		!isManifestSHA256(in.ExpectedFinalStatementSHA256) ||
		!isManifestSHA256(in.ExpectedStatementDraftSHA256) ||
		!isManifestSHA256(in.ExpectedMarkdownSHA256) ||
		in.FinalStatementArtifact.SHA256 != in.ExpectedFinalStatementSHA256 ||
		in.FinalStatementArtifact.Producer != "FinalizeAuthoringStatementSamplesActivityV1" ||
		in.StatementDraftArtifact.SHA256 != in.ExpectedStatementDraftSHA256 ||
		in.StatementDraftArtifact.Producer != "RenderStatementFromAuthoringBundleActivityV1" {
		return nil, s3ContractErrorV1("invalid S3 dedup input")
	}
	statementBytes, err := a.readS3ArtifactV1(ctx, in.FinalStatementArtifact, in.ExpectedFinalStatementSHA256, "application/json", maxS3ManifestBytesV1)
	if err != nil {
		return nil, err
	}
	var final FinalAuthoringStatementSamplesBundleV1
	if err := json.Unmarshal(statementBytes, &final); err != nil {
		return nil, s3ContractErrorV1("decode final statement bundle: %v", err)
	}
	if final.SchemaVersion != FinalAuthoringStatementSamplesSchemaV1 ||
		final.Status != FinalAuthoringStatementSamplesStatusV1 ||
		final.StatementDraftSHA256 != in.ExpectedStatementDraftSHA256 ||
		final.MarkdownSHA256 != in.ExpectedMarkdownSHA256 ||
		sha256Hex([]byte(final.Markdown)) != final.MarkdownSHA256 {
		return nil, s3ContractErrorV1("final statement markdown binding mismatch")
	}
	draftBytes, err := a.readS3ArtifactV1(ctx, in.StatementDraftArtifact, in.ExpectedStatementDraftSHA256, "application/json", maxStatementDraftBundleBytesV1)
	if err != nil {
		return nil, err
	}
	var draft StatementDraftBundleV1
	if err := json.Unmarshal(draftBytes, &draft); err != nil {
		return nil, s3ContractErrorV1("decode bound statement draft: %v", err)
	}
	if draft.SchemaVersion != StatementDraftBundleSchemaV1 ||
		draft.DocumentStatus != statementDraftDocumentStatusV1 ||
		strings.TrimSpace(draft.Title) == "" ||
		draft.MarkdownSHA256 != sha256Hex([]byte(draft.Markdown)) {
		return nil, s3ContractErrorV1("bound statement draft identity mismatch")
	}
	statement := StatementResult{Title: draft.Title, Statement: final.Markdown, SourceArtifacts: []*ArtifactRef{&in.FinalStatementArtifact, &in.StatementDraftArtifact}}
	dedup, dedupErr := a.PostStatementSimilarityActivity(ctx, statement)
	if dedupErr != nil {
		dedup = func() *PostStatementSimilarityResult {
			value := NewPostStatementDedupCheckFailedResult(statement, dedupErr)
			return &value
		}()
	}
	if dedup == nil || dedup.Report == nil {
		return nil, fmt.Errorf("dedup activity returned no report")
	}
	reportBytes, err := json.Marshal(dedup.Report)
	if err != nil {
		return nil, fmt.Errorf("marshal dedup report: %w", err)
	}
	ref, err := a.putS3ReceiptV1(ctx, reportBytes, "qg08_dedup_receipt")
	if err != nil {
		return nil, err
	}
	status := qualitygate.GateStatusPass
	blockers := []qualitygate.ReviewBlockerV1{}
	switch dedup.Report.Decision {
	case DedupDecisionPass, DedupDecisionWarn:
	case DedupDecisionRejected:
		status = qualitygate.GateStatusBlocked
		blockers = []qualitygate.ReviewBlockerV1{s3BlockerV1("dedup.similar_candidate", s3ArtifactRefURI(&in.FinalStatementArtifact), "algoforge.dedup.v1", s3ArtifactRefURI(ref), "candidate must stay below the hard similarity threshold")}
	case DedupDecisionCheckFailed, DedupDecisionProviderUnavailable:
		status = qualitygate.GateStatusCheckFailed
	default:
		return nil, s3ContractErrorV1("unsupported dedup decision %q", dedup.Report.Decision)
	}
	neighborCount := dedup.Report.NeighborCount
	if status == qualitygate.GateStatusCheckFailed {
		neighborCount = nil
	}
	return &S3DedupGateResultV1{PayloadVersion: S3QualityPayloadVersionV1, Gate: qualitygate.DedupEvidenceV1{Status: status, Evidence: s3EvidenceAssetV1(*ref), NeighborCount: neighborCount, Blockers: blockers}, ReceiptSHA256: ref.SHA256, ReceiptArtifact: ref}, nil
}

type S3ReviewerAssetV1 struct {
	Role           string      `json:"role"`
	Artifact       ArtifactRef `json:"artifact"`
	ExpectedSHA256 string      `json:"expected_sha256"`
}

type S3StrictReviewerInputV1 struct {
	PayloadVersion       int                      `json:"payload_version"`
	SubjectID            string                   `json:"subject_id"`
	SubjectRevision      string                   `json:"subject_revision"`
	TestManifestArtifact ArtifactRef              `json:"test_manifest_artifact"`
	VisibleAssets        []S3ReviewerAssetV1      `json:"visible_assets"`
	ReviewRuntime        *domain.LLMRuntimeConfig `json:"review_runtime,omitempty"`
}

type S3StrictReviewerResultV1 struct {
	PayloadVersion     int                            `json:"payload_version"`
	Reviewer           qualitygate.ReviewerEvidenceV1 `json:"reviewer"`
	AssessmentArtifact *ArtifactRef                   `json:"assessment_artifact"`
	ProviderArtifact   *ArtifactRef                   `json:"provider_artifact"`
}

func (a *Activities) S3StrictReviewerActivityV1(ctx context.Context, in S3StrictReviewerInputV1) (*S3StrictReviewerResultV1, error) {
	if in.PayloadVersion != S3QualityPayloadVersionV1 || strings.TrimSpace(in.SubjectID) == "" || !isManifestSHA256(in.SubjectRevision) {
		return nil, s3ContractErrorV1("invalid strict reviewer identity")
	}
	manifestBytes, err := a.readS3ArtifactV1(ctx, in.TestManifestArtifact, in.TestManifestArtifact.SHA256, "application/json", maxS3ManifestBytesV1)
	if err != nil {
		return nil, err
	}
	manifest, err := ParseTestManifestV2JSON(manifestBytes)
	if err != nil || manifest.TestCount != len(manifest.Cases) {
		return nil, s3ContractErrorV1("strict reviewer requires the complete canonical TestManifest v2")
	}
	assets := append([]S3ReviewerAssetV1(nil), in.VisibleAssets...)
	sort.Slice(assets, func(i, j int) bool { return assets[i].Role < assets[j].Role })
	seen := map[string]bool{}
	visible := make([]struct {
		Role          string `json:"role"`
		ArtifactRef   string `json:"artifact_ref"`
		SHA256        string `json:"sha256"`
		ContentBase64 string `json:"content_base64"`
	}, 0, len(assets))
	total := len(manifestBytes)
	for _, asset := range assets {
		if !validS3ReviewerRoleV1(asset.Role) || seen[asset.Role] || asset.ExpectedSHA256 != asset.Artifact.SHA256 {
			return nil, s3ContractErrorV1("reviewer asset role or identity is invalid")
		}
		seen[asset.Role] = true
		data, readErr := a.readS3ArtifactV1(ctx, asset.Artifact, asset.ExpectedSHA256, asset.Artifact.ContentType, maxS3ReviewVisibleBytesV1)
		if readErr != nil {
			return nil, readErr
		}
		total += len(data)
		if total > maxS3ReviewVisibleBytesV1 {
			return nil, s3ContractErrorV1("reviewer visible assets exceed %d bytes", maxS3ReviewVisibleBytesV1)
		}
		visible = append(visible, struct {
			Role          string `json:"role"`
			ArtifactRef   string `json:"artifact_ref"`
			SHA256        string `json:"sha256"`
			ContentBase64 string `json:"content_base64"`
		}{asset.Role, s3ArtifactRefURI(&asset.Artifact), asset.ExpectedSHA256, base64.StdEncoding.EncodeToString(data)})
	}
	for _, required := range []string{"semantic_spec", "final_statement", "oracle_promotion", "sanitizer", "boundary_coverage"} {
		if !seen[required] {
			return nil, s3ContractErrorV1("reviewer visible asset %q is required", required)
		}
	}
	promptPayload := struct {
		SubjectID       string          `json:"subject_id"`
		SubjectRevision string          `json:"subject_revision"`
		TestManifest    json.RawMessage `json:"test_manifest"`
		VisibleAssets   interface{}     `json:"visible_assets"`
	}{in.SubjectID, in.SubjectRevision, json.RawMessage(manifestBytes), visible}
	prompt, err := json.Marshal(promptPayload)
	if err != nil {
		return nil, fmt.Errorf("marshal strict reviewer prompt: %w", err)
	}
	temperature := 0.0
	req := &llm.Request{MaxTokens: 8192, System: s3StrictReviewerSystemPromptV1, Temperature: &temperature, Messages: []llm.Message{{Role: "user", Content: string(prompt)}}}
	if in.ReviewRuntime != nil {
		copyRuntime := *in.ReviewRuntime
		if err := copyRuntime.Validate("review_runtime"); err != nil {
			return nil, s3ContractErrorV1("invalid strict reviewer runtime: %v", err)
		}
		applyLLMRuntime(req, &copyRuntime)
	}
	activity.RecordHeartbeat(ctx, "reviewing complete S3 manifest and visible assets")
	response, providerArtifact, err := a.completeLLMWithProvenance(ctx, "s3_strict_reviewer_v1", req, 2)
	if err != nil {
		return nil, wrapRequiredProviderEffectError("strict S3 reviewer", err)
	}
	if response == nil || providerArtifact == nil || providerArtifact.LLMCallReceipt == nil || response.StopReason == "max_tokens" {
		return nil, s3ContractErrorV1("strict reviewer provider response is missing identity or truncated")
	}
	assessment, err := qualitygate.DecodeReviewAssessmentV1([]byte(strings.TrimSpace(response.Text())))
	if err != nil {
		return nil, s3ContractErrorV1("strict reviewer response: %v", err)
	}
	canonical, assessmentSHA, err := qualitygate.CanonicalReviewAssessmentV1(assessment)
	if err != nil {
		return nil, s3ContractErrorV1("canonicalize strict reviewer assessment: %v", err)
	}
	assessmentRef, err := a.putS3ReceiptV1(ctx, canonical, "qg07_strict_reviewer_assessment")
	if err != nil {
		return nil, err
	}
	if assessmentRef.SHA256 != assessmentSHA {
		return nil, fmt.Errorf("review assessment CAS identity mismatch")
	}
	return &S3StrictReviewerResultV1{PayloadVersion: S3QualityPayloadVersionV1, Reviewer: qualitygate.ReviewerEvidenceV1{Evidence: s3EvidenceAssetV1(*assessmentRef), AssessmentSHA256: assessmentSHA, Assessment: assessment}, AssessmentArtifact: assessmentRef, ProviderArtifact: providerArtifact}, nil
}

const s3StrictReviewerSystemPromptV1 = `You are the isolated R reviewer for AlgoForge S3. Review every TestManifest v2 entry and every supplied visible asset. Hidden-regression inputs are intentionally unavailable.
Reconstruct the executable contract from the SemanticSpec and final statement, then audit the manifest case intents, boundary coverage, sanitizer receipt, oracle promotion, and statement sample binding.
For correctness, actively search for counterexamples: empty and singleton inputs, equal and extreme values, disconnected or degenerate topology, repeated operations, no-solution and multiple-solution cases, integer overflow, parser ambiguity, and any mismatch between grammar, objective, output normalization, and code.
For complexity and difficulty, derive the intended and plausible wrong complexities from the constraints. Flag a rating mismatch when the key observation, implementation burden, or required technique is materially outside the target band.
For test coverage, check whether every declared wrong-solution family and every high-risk boundary fact has a manifest intent and whether the suite can actually distinguish it. Treat a missing witness as a defect even when the case count is large.
Only report a blocker when it is grounded in a supplied asset or manifest entry. Each blocker must name code, responsible_asset, and a bounded witness object {runner,fixture_ref,assertion}; responsible_asset must exactly equal the artifact_ref of one supplied visible asset. Never invent or rewrite a reference.
Return exactly one JSON object matching algoforge.quality-review-assessment.v1 with approved, five required dimensions (clarity, correctness, test_coverage, difficulty_calibration, tag_accuracy), and an explicit blockers array. Do not emit Markdown, extra fields, duplicate keys, comments, or trailing data. The server recomputes the verdict; approved is advisory only.`

type HiddenSuiteRefV1 struct {
	SchemaVersion  string `json:"schema_version"`
	SuiteID        string `json:"suite_id"`
	RevisionSHA256 string `json:"revision_sha256"`
}

type HiddenSuiteResolutionRequestV1 struct {
	SchemaVersion      string `json:"schema_version"`
	SubjectID          string `json:"subject_id"`
	SemanticSpecSHA256 string `json:"semantic_spec_sha256"`
}

type ResolveS3HiddenSuiteInputV1 struct {
	PayloadVersion     int    `json:"payload_version"`
	SubjectID          string `json:"subject_id"`
	SemanticSpecSHA256 string `json:"semantic_spec_sha256"`
}

type ResolveS3HiddenSuiteResultV1 struct {
	PayloadVersion int               `json:"payload_version"`
	Available      bool              `json:"available"`
	Suite          *HiddenSuiteRefV1 `json:"suite,omitempty"`
	FailureCode    string            `json:"failure_code,omitempty"`
}

// ResolveS3HiddenSuiteActivityV1 keeps suite selection server-owned. Missing
// configuration or registry failure is represented as a minimal unavailable
// result so the hidden gate can persist check_failed without exposing bytes.
func (a *Activities) ResolveS3HiddenSuiteActivityV1(ctx context.Context, in ResolveS3HiddenSuiteInputV1) (*ResolveS3HiddenSuiteResultV1, error) {
	if in.PayloadVersion != S3QualityPayloadVersionV1 || !safeOpaqueS3TokenV1(in.SubjectID, 128) || !isManifestSHA256(in.SemanticSpecSHA256) {
		return nil, s3ContractErrorV1("invalid hidden-suite resolution identity")
	}
	unavailable := &ResolveS3HiddenSuiteResultV1{PayloadVersion: S3QualityPayloadVersionV1, Available: false, FailureCode: "hidden.suite_unavailable"}
	if a == nil || a.deps == nil || a.deps.HiddenSuiteResolver == nil {
		return unavailable, nil
	}
	suite, err := a.deps.HiddenSuiteResolver.ResolveHiddenSuiteV1(ctx, HiddenSuiteResolutionRequestV1{SchemaVersion: S3HiddenSuiteSchemaV1, SubjectID: in.SubjectID, SemanticSpecSHA256: in.SemanticSpecSHA256})
	if err != nil {
		return unavailable, nil
	}
	if suite.SchemaVersion != S3HiddenSuiteSchemaV1 || !safeOpaqueS3TokenV1(suite.SuiteID, 128) || !isManifestSHA256(suite.RevisionSHA256) {
		return unavailable, nil
	}
	return &ResolveS3HiddenSuiteResultV1{PayloadVersion: S3QualityPayloadVersionV1, Available: true, Suite: &suite}, nil
}

type HiddenRegressionExecutionRequestV1 struct {
	SchemaVersion     string           `json:"schema_version"`
	Suite             HiddenSuiteRefV1 `json:"suite"`
	CandidateArtifact ArtifactRef      `json:"candidate_artifact"`
}

type HiddenRegressionExecutionResponseV1 struct {
	SchemaVersion    string `json:"schema_version"`
	Passed           bool   `json:"passed"`
	ExposureDetected bool   `json:"exposure_detected"`
	ExecutedCount    int    `json:"executed_count"`
	FailureCode      string `json:"failure_code,omitempty"`
}

type S3HiddenGateInputV1 struct {
	PayloadVersion    int                          `json:"payload_version"`
	Resolution        ResolveS3HiddenSuiteResultV1 `json:"resolution"`
	CandidateArtifact ArtifactRef                  `json:"candidate_artifact"`
}

type S3HiddenGateResultV1 struct {
	PayloadVersion  int                                    `json:"payload_version"`
	Gate            qualitygate.HiddenRegressionEvidenceV1 `json:"gate"`
	ReceiptSHA256   string                                 `json:"receipt_sha256"`
	ReceiptArtifact *ArtifactRef                           `json:"receipt_artifact"`
}

func (a *Activities) S3HiddenRegressionActivityV1(ctx context.Context, in S3HiddenGateInputV1) (*S3HiddenGateResultV1, error) {
	if in.PayloadVersion != S3QualityPayloadVersionV1 || in.Resolution.PayloadVersion != S3QualityPayloadVersionV1 {
		return nil, s3ContractErrorV1("invalid hidden-suite resolution")
	}
	if err := in.CandidateArtifact.Validate(in.CandidateArtifact.Bucket); err != nil {
		return nil, s3ContractErrorV1("invalid hidden candidate artifact: %v", err)
	}
	response := HiddenRegressionExecutionResponseV1{SchemaVersion: S3HiddenExecutorResponseSchemaV1, FailureCode: "hidden.suite_unavailable"}
	status := qualitygate.GateStatusCheckFailed
	var suite HiddenSuiteRefV1
	if in.Resolution.Available {
		if in.Resolution.Suite == nil || in.Resolution.FailureCode != "" {
			return nil, s3ContractErrorV1("available hidden-suite resolution is incomplete")
		}
		suite = *in.Resolution.Suite
		if suite.SchemaVersion != S3HiddenSuiteSchemaV1 || !safeOpaqueS3TokenV1(suite.SuiteID, 128) || !isManifestSHA256(suite.RevisionSHA256) {
			return nil, s3ContractErrorV1("invalid opaque hidden-suite reference")
		}
		response.FailureCode = "hidden.executor_unavailable"
		request := HiddenRegressionExecutionRequestV1{SchemaVersion: S3HiddenSuiteSchemaV1, Suite: suite, CandidateArtifact: in.CandidateArtifact}
		if a != nil && a.deps != nil && a.deps.HiddenRegressionExecutor != nil {
			actual, executeErr := a.deps.HiddenRegressionExecutor.ExecuteHiddenRegressionV1(ctx, request)
			if executeErr == nil {
				response = actual
				if response.SchemaVersion != S3HiddenExecutorResponseSchemaV1 || response.ExecutedCount < 0 || (response.Passed && (response.ExposureDetected || response.ExecutedCount == 0 || response.FailureCode != "")) || (!response.Passed && !validS3HiddenFailureCodeV1(response.FailureCode)) {
					return nil, s3ContractErrorV1("hidden executor returned an invalid minimal response")
				}
				status = qualitygate.GateStatusBlocked
				if response.Passed {
					status = qualitygate.GateStatusPass
				}
			}
		}
	} else if in.Resolution.Suite != nil || in.Resolution.FailureCode != "hidden.suite_unavailable" {
		return nil, s3ContractErrorV1("unavailable hidden-suite resolution is not minimal")
	}
	receipt := struct {
		SchemaVersion    string                              `json:"schema_version"`
		SuiteID          string                              `json:"suite_id,omitempty"`
		SuiteRevisionSHA string                              `json:"suite_revision_sha256"`
		CandidateSHA256  string                              `json:"candidate_sha256"`
		Response         HiddenRegressionExecutionResponseV1 `json:"response"`
	}{S3HiddenReceiptSchemaV1, suite.SuiteID, suite.RevisionSHA256, in.CandidateArtifact.SHA256, response}
	encoded, err := json.Marshal(receipt)
	if err != nil {
		return nil, fmt.Errorf("marshal hidden receipt: %w", err)
	}
	ref, err := a.putS3ReceiptV1(ctx, encoded, "qg09_hidden_regression_receipt")
	if err != nil {
		return nil, err
	}
	blockers := []qualitygate.ReviewBlockerV1{}
	if status == qualitygate.GateStatusBlocked {
		code := response.FailureCode
		if response.ExposureDetected {
			code = "hidden.exposure_detected"
		}
		blockers = []qualitygate.ReviewBlockerV1{s3BlockerV1(code, s3ArtifactRefURI(&in.CandidateArtifact), "algoforge.hidden-executor.v1", s3ArtifactRefURI(ref), "candidate must pass the opaque hidden suite without exposing hidden material")}
	}
	return &S3HiddenGateResultV1{PayloadVersion: S3QualityPayloadVersionV1, Gate: qualitygate.HiddenRegressionEvidenceV1{Status: status, Evidence: s3EvidenceAssetV1(*ref), ExposureDetected: response.ExposureDetected, Blockers: blockers}, ReceiptSHA256: ref.SHA256, ReceiptArtifact: ref}, nil
}

type RepairResponsibleAssetV1 struct {
	Role     string      `json:"role"`
	Artifact ArtifactRef `json:"artifact"`
}

type S3RepairRevisionInputV1 struct {
	PayloadVersion       int                           `json:"payload_version"`
	ParentRevisionSHA256 string                        `json:"parent_revision_sha256"`
	Round                int                           `json:"round"`
	BlockerCodes         []string                      `json:"blocker_codes"`
	Blockers             []qualitygate.ReviewBlockerV1 `json:"blockers"`
	ResponsibleAssets    []RepairResponsibleAssetV1    `json:"responsible_assets"`
}

type RepairRevisionProviderAssetV1 struct {
	Role          string `json:"role"`
	SHA256        string `json:"sha256"`
	ContentBase64 string `json:"content_base64"`
}

type RepairRevisionProviderRequestV1 struct {
	SchemaVersion        string                          `json:"schema_version"`
	ParentRevisionSHA256 string                          `json:"parent_revision_sha256"`
	Round                int                             `json:"round"`
	BlockerCodes         []string                        `json:"blocker_codes"`
	ResponsibleAssets    []RepairRevisionProviderAssetV1 `json:"responsible_assets"`
}

type RepairRevisionProviderResponseV1 struct {
	PayloadVersion int                             `json:"payload_version"`
	FrozenConcept  string                          `json:"frozen_concept"`
	RequiredFacts  []CanonicalAuthoringBriefFactV1 `json:"required_facts"`
}

type S3RepairRevisionResultV1 struct {
	PayloadVersion       int                               `json:"payload_version"`
	SchemaVersion        string                            `json:"schema_version"`
	ParentRevisionSHA256 string                            `json:"parent_revision_sha256"`
	Round                int                               `json:"round"`
	NewRevisionSHA256    string                            `json:"new_revision_sha256"`
	NewRevisionArtifact  *ArtifactRef                      `json:"new_revision_artifact"`
	NewAuthoringInput    BuildCanonicalAuthoringBriefInput `json:"new_authoring_input"`
	NewCanonicalBrief    string                            `json:"new_canonical_brief"`
}

func (a *Activities) S3RepairRevisionActivityV1(ctx context.Context, in S3RepairRevisionInputV1) (*S3RepairRevisionResultV1, error) {
	if in.PayloadVersion != S3QualityPayloadVersionV1 || !isManifestSHA256(in.ParentRevisionSHA256) || in.Round < 1 || in.Round > qualitygate.MaxRepairRoundsV1 || !sortedUniqueNonEmptyS3V1(in.BlockerCodes) || len(in.Blockers) == 0 || len(in.ResponsibleAssets) == 0 {
		return nil, s3ContractErrorV1("invalid repair revision input")
	}
	assets := append([]RepairResponsibleAssetV1(nil), in.ResponsibleAssets...)
	sort.Slice(assets, func(i, j int) bool { return assets[i].Role < assets[j].Role })
	providerAssets := make([]RepairRevisionProviderAssetV1, 0, len(assets))
	seen := map[string]bool{}
	allowedRefs := make(map[string]bool, len(in.Blockers))
	seenCodes := make([]string, 0, len(in.Blockers))
	for _, blocker := range in.Blockers {
		if blocker.Code == "" || blocker.ResponsibleAsset == "" || strings.Contains(strings.ToLower(blocker.ResponsibleAsset), "hidden") {
			return nil, s3ContractErrorV1("repair blocker has an invalid responsible asset")
		}
		allowedRefs[blocker.ResponsibleAsset] = true
		seenCodes = append(seenCodes, blocker.Code)
	}
	sort.Strings(seenCodes)
	seenCodes = compactSortedS3StringsV1(seenCodes)
	if strings.Join(seenCodes, "\x00") != strings.Join(in.BlockerCodes, "\x00") {
		return nil, s3ContractErrorV1("repair blocker codes are not server-derived from canonical blockers")
	}
	total := 0
	for _, asset := range assets {
		if strings.TrimSpace(asset.Role) == "" || strings.Contains(strings.ToLower(asset.Role), "hidden") || seen[asset.Role] || !allowedRefs[s3ArtifactRefURI(&asset.Artifact)] {
			return nil, s3ContractErrorV1("repair responsible asset role is invalid")
		}
		seen[asset.Role] = true
		data, err := a.readS3ArtifactV1(ctx, asset.Artifact, asset.Artifact.SHA256, asset.Artifact.ContentType, maxS3RepairAssetBytesV1)
		if err != nil {
			return nil, err
		}
		total += len(data)
		if total > maxS3RepairAssetBytesV1 {
			return nil, s3ContractErrorV1("repair responsible assets exceed %d bytes", maxS3RepairAssetBytesV1)
		}
		providerAssets = append(providerAssets, RepairRevisionProviderAssetV1{Role: asset.Role, SHA256: asset.Artifact.SHA256, ContentBase64: base64.StdEncoding.EncodeToString(data)})
	}
	if len(providerAssets) != len(allowedRefs) {
		return nil, s3ContractErrorV1("repair assets do not exactly match blocker responsible assets")
	}
	request := RepairRevisionProviderRequestV1{SchemaVersion: S3RepairRevisionSchemaV1, ParentRevisionSHA256: in.ParentRevisionSHA256, Round: in.Round, BlockerCodes: append([]string(nil), in.BlockerCodes...), ResponsibleAssets: providerAssets}
	var response RepairRevisionProviderResponseV1
	var err error
	if a != nil && a.deps != nil && a.deps.RepairRevisionGenerator != nil {
		response, err = a.deps.RepairRevisionGenerator.GenerateRepairRevisionV1(ctx, request)
		if err != nil {
			return nil, fmt.Errorf("generate S3 repair revision: %w", err)
		}
	} else {
		prompt, marshalErr := json.Marshal(request)
		if marshalErr != nil {
			return nil, fmt.Errorf("marshal S3 repair request: %w", marshalErr)
		}
		temperature := 0.0
		llmResponse, _, completeErr := a.completeLLMWithProvenance(ctx, "s3_repair_revision_v1", &llm.Request{MaxTokens: 4096, System: s3RepairRevisionSystemPromptV1, Temperature: &temperature, Messages: []llm.Message{{Role: "user", Content: string(prompt)}}}, 2)
		if completeErr != nil {
			return nil, wrapRequiredProviderEffectError("S3 repair revision", completeErr)
		}
		if llmResponse == nil || llmResponse.StopReason == "max_tokens" {
			return nil, s3ContractErrorV1("repair provider response is missing or truncated")
		}
		var parsed RepairRevisionProviderResponseV1
		decoder := json.NewDecoder(strings.NewReader(strings.TrimSpace(llmResponse.Text())))
		decoder.DisallowUnknownFields()
		if decodeErr := decoder.Decode(&parsed); decodeErr != nil || ensureJSONEOF(decoder) != nil {
			return nil, s3ContractErrorV1("strict repair response is invalid")
		}
		response = parsed
	}
	builderInput := BuildCanonicalAuthoringBriefInput{
		PayloadVersion: response.PayloadVersion,
		FrozenConcept:  response.FrozenConcept,
		RequiredFacts:  append([]CanonicalAuthoringBriefFactV1(nil), response.RequiredFacts...),
	}
	built, err := a.BuildCanonicalAuthoringBriefActivityV1(ctx, builderInput)
	if err != nil {
		return nil, s3ContractErrorV1("repair output cannot be rebuilt by the canonical authoring brief builder: %v", err)
	}
	if len(built.CanonicalBrief) == 0 || len(built.CanonicalBrief) > maxS3RepairRevisionBytesV1 || built.CanonicalBriefSHA256 == in.ParentRevisionSHA256 {
		return nil, s3QualityErrorV1("repair must create a new immutable revision")
	}
	ref, err := a.putS3ReceiptContentTypeV1(ctx, []byte(built.CanonicalBrief), "application/json", "qg09_repair_revision")
	if err != nil {
		return nil, err
	}
	if ref.SHA256 != built.CanonicalBriefSHA256 {
		return nil, fmt.Errorf("repair canonical-brief CAS identity mismatch")
	}
	return &S3RepairRevisionResultV1{PayloadVersion: S3QualityPayloadVersionV1, SchemaVersion: S3RepairRevisionSchemaV1, ParentRevisionSHA256: in.ParentRevisionSHA256, Round: in.Round, NewRevisionSHA256: ref.SHA256, NewRevisionArtifact: ref, NewAuthoringInput: builderInput, NewCanonicalBrief: built.CanonicalBrief}, nil
}

const s3RepairRevisionSystemPromptV1 = `You are the isolated AlgoForge R repair stage. You receive only server-selected visible CAS assets and canonical blocker witnesses. Return exactly one JSON object {"payload_version":1,"frozen_concept":"revised concept","required_facts":[{"key":"stable_key","value":"required fact"}]}. Do not mention hidden tests, emit source code, Markdown, extra fields, or commentary. The server will rebuild this structured response through the canonical authoring brief builder.`

type S3VerdictInputV1 struct {
	PayloadVersion int                      `json:"payload_version"`
	Evidence       qualitygate.S3EvidenceV1 `json:"evidence"`
}

type S3VerdictResultV1 struct {
	PayloadVersion int                 `json:"payload_version"`
	Audit          qualitygate.AuditV1 `json:"audit"`
	AuditSHA256    string              `json:"audit_sha256"`
	AuditArtifact  *ArtifactRef        `json:"audit_artifact"`
}

func (a *Activities) RecomputeS3VerdictActivityV1(ctx context.Context, in S3VerdictInputV1) (*S3VerdictResultV1, error) {
	if in.PayloadVersion != S3QualityPayloadVersionV1 {
		return nil, s3ContractErrorV1("unsupported verdict payload version %d", in.PayloadVersion)
	}
	auditValue, err := qualitygate.RecomputeVerdictV1(in.Evidence)
	if err != nil {
		return nil, s3ContractErrorV1("recompute S3 verdict: %v", err)
	}
	auditBytes, auditSHA, err := qualitygate.CanonicalAuditV1(auditValue)
	if err != nil {
		return nil, s3ContractErrorV1("canonicalize S3 audit: %v", err)
	}
	ref, err := a.putS3ReceiptV1(ctx, auditBytes, "qg07_server_recomputed_audit")
	if err != nil {
		return nil, err
	}
	if ref.SHA256 != auditSHA {
		return nil, fmt.Errorf("S3 audit CAS identity mismatch")
	}
	return &S3VerdictResultV1{PayloadVersion: S3QualityPayloadVersionV1, Audit: auditValue, AuditSHA256: auditSHA, AuditArtifact: ref}, nil
}

func (a *Activities) readS3ArtifactV1(ctx context.Context, ref ArtifactRef, expectedSHA, contentType string, maxBytes int) ([]byte, error) {
	if a == nil || a.artifacts == nil {
		return nil, fmt.Errorf("artifact store is not configured")
	}
	if !isManifestSHA256(expectedSHA) || ref.SHA256 != expectedSHA || (contentType != "" && ref.ContentType != contentType) || ref.SizeBytes < 0 || ref.SizeBytes > int64(maxBytes) {
		return nil, s3ContractErrorV1("artifact identity, media type, or size is invalid")
	}
	if err := ref.Validate(ref.Bucket); err != nil {
		return nil, s3ContractErrorV1("invalid artifact ref: %v", err)
	}
	data, err := a.artifacts.Get(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("read S3 artifact %s: %w", ref.SHA256, err)
	}
	if len(data) > maxBytes || int64(len(data)) != ref.SizeBytes || sha256Hex(data) != expectedSHA {
		return nil, s3ContractErrorV1("artifact bytes do not match immutable ref")
	}
	return data, nil
}

func (a *Activities) loadS3CasePlanV1(ctx context.Context, ref ArtifactRef) (*S3CasePlanBundleV1, []byte, error) {
	if ref.Producer != "MaterializeS3CasePlanActivityV1" || ref.LLMCallReceipt != nil {
		return nil, nil, s3ContractErrorV1("S3 case plan has an untrusted producer")
	}
	data, err := a.readS3ArtifactV1(ctx, ref, ref.SHA256, "application/json", maxS3ManifestBytesV1)
	if err != nil {
		return nil, nil, err
	}
	var plan S3CasePlanBundleV1
	if err := decodeCanonicalSampleClosureJSONV1(data, &plan); err != nil || plan.SchemaVersion != S3CasePlanSchemaV1 || !isManifestSHA256(plan.SemanticSpecSHA256) || !isManifestSHA256(plan.AuthoringBundleSHA256) || !isManifestSHA256(plan.TestDataIdentitySHA256) || !isManifestSHA256(plan.SanitizerSuiteSHA256) || len(plan.Cases) == 0 || len(plan.SanitizerCaseIDs) == 0 || len(plan.SanitizerCaseIDs) > maxS3SanitizerCasesV1 {
		return nil, nil, s3ContractErrorV1("S3 case plan is not canonical or complete")
	}
	caseByID := make(map[string]S3ManifestCasePlanV1, len(plan.Cases))
	for index, item := range plan.Cases {
		if item.TestID == "" || item.TestID != strings.TrimSpace(item.TestID) || (index > 0 && plan.Cases[index-1].TestID >= item.TestID) || !validTestManifestPurposeV2(item.Purpose) || item.ConstraintRegion == "" || item.ConstraintRegion != strings.TrimSpace(item.ConstraintRegion) || item.BoundaryRefs == nil || !sortedUniqueCanonicalStringsV2(item.BoundaryRefs) || item.KilledWrongIDs == nil || !sortedUniqueCanonicalStringsV2(item.KilledWrongIDs) || strings.TrimSpace(item.KilledWrongIDsRetentionReason) == "" || item.InputArtifact.SHA256 == "" {
			return nil, nil, s3ContractErrorV1("S3 case plan contains invalid derived metadata")
		}
		if err := item.InputArtifact.Validate(item.InputArtifact.Bucket); err != nil {
			return nil, nil, s3ContractErrorV1("S3 case-plan input artifact is invalid: %v", err)
		}
		caseByID[item.TestID] = item
	}
	if !sortedUniqueCanonicalStringsV2(plan.SanitizerCaseIDs) {
		return nil, nil, s3ContractErrorV1("S3 sanitizer case IDs are not sorted and unique")
	}
	suiteIdentities := make([]struct {
		TestID      string `json:"test_id"`
		InputSHA256 string `json:"input_sha256"`
	}, len(plan.SanitizerCaseIDs))
	totalBytes := int64(0)
	for index, caseID := range plan.SanitizerCaseIDs {
		item, exists := caseByID[caseID]
		if !exists || item.InputArtifact.SizeBytes > maxS3SanitizerCaseBytesV1 {
			return nil, nil, s3ContractErrorV1("S3 sanitizer suite references an absent or oversized case")
		}
		totalBytes += item.InputArtifact.SizeBytes
		suiteIdentities[index] = struct {
			TestID      string `json:"test_id"`
			InputSHA256 string `json:"input_sha256"`
		}{caseID, item.InputArtifact.SHA256}
	}
	if totalBytes > maxS3SanitizerSuiteBytesV1 {
		return nil, nil, s3ContractErrorV1("S3 sanitizer suite is oversized")
	}
	_, suiteSHA, err := canonicalJSONBytesAndSHA256(suiteIdentities)
	if err != nil || suiteSHA != plan.SanitizerSuiteSHA256 {
		return nil, nil, s3ContractErrorV1("S3 sanitizer suite identity is invalid")
	}
	return &plan, data, nil
}

func canonicalJSONBytesAndSHA256(value interface{}) ([]byte, string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, "", err
	}
	return data, sha256Hex(data), nil
}

func sameS3SandboxIdentityV1(left, right SandboxAuditMetadata) bool {
	return left.ImageDigest != "" && left.ToolchainManifestDigest != "" && left.SeccompPolicyDigest != "" && left.LimitProfile != "" &&
		left.ImageDigest == right.ImageDigest && left.ToolchainManifestDigest == right.ToolchainManifestDigest && left.SeccompPolicyDigest == right.SeccompPolicyDigest && left.LimitProfile == right.LimitProfile && left.Profile == right.Profile
}

func (a *Activities) validateS3SandboxIdentityPolicyV1(actual SandboxAuditMetadata) error {
	if a == nil || a.deps == nil || a.deps.S3SandboxIdentityPolicy == nil {
		return s3ContractErrorV1("S3 sandbox identity policy is not configured")
	}
	policy := a.deps.S3SandboxIdentityPolicy
	if !validS3DigestV1(policy.ImageDigest) || !validS3DigestV1(policy.ToolchainManifestDigest) || !validS3DigestV1(policy.SeccompPolicyDigest) {
		return s3ContractErrorV1("S3 sandbox identity policy is invalid")
	}
	if actual.ImageDigest != policy.ImageDigest || actual.ToolchainManifestDigest != policy.ToolchainManifestDigest || actual.SeccompPolicyDigest != policy.SeccompPolicyDigest {
		return s3ContractErrorV1("sandbox audit does not match the deployment-approved S3 identity")
	}
	return nil
}

func validS3DigestV1(value string) bool {
	return strings.HasPrefix(value, "sha256:") && isManifestSHA256(strings.TrimPrefix(value, "sha256:"))
}

func (a *Activities) resolveS3SandboxOutputsV1(ctx context.Context, run SandboxResult) ([]string, error) {
	if run.PayloadVersion != ActivityPayloadVersion || len(run.OutputRefs) != 0 {
		return nil, s3ContractErrorV1("S3 sandbox result is legacy or has an unsupported payload version")
	}
	if len(run.OutputArtifacts) == 0 {
		return append([]string(nil), run.Outputs...), nil
	}
	if len(run.OutputArtifacts) != len(run.Outputs) {
		return nil, s3ContractErrorV1("S3 sandbox output artifact count mismatch")
	}
	resolved := make([]string, len(run.Outputs))
	for index, ref := range run.OutputArtifacts {
		if ref == nil || run.Outputs[index] != "" {
			return nil, s3ContractErrorV1("S3 sandbox output mixes inline and artifact bytes")
		}
		data, err := a.readS3ArtifactV1(ctx, *ref, ref.SHA256, ref.ContentType, remoteSandboxMaxOutputBytes)
		if err != nil {
			return nil, err
		}
		resolved[index] = string(data)
	}
	return resolved, nil
}

func (a *Activities) putS3ReceiptV1(ctx context.Context, data []byte, artifactType string) (*ArtifactRef, error) {
	return a.putS3ReceiptContentTypeV1(ctx, data, "application/json", artifactType)
}

func (a *Activities) putS3ReceiptContentTypeV1(ctx context.Context, data []byte, contentType, artifactType string) (*ArtifactRef, error) {
	metadata := artifactMetadataFromActivity(ctx)
	metadata.ArtifactType = artifactType
	metadata.SourceType = "server_recomputed_s3_receipt"
	metadata.RetentionClass = "workflow_cas_unreviewed"
	ref, err := a.putArtifactWithMetadata(ctx, data, contentType, metadata)
	if err != nil {
		return nil, fmt.Errorf("store %s: %w", artifactType, err)
	}
	if ref == nil || ref.SHA256 != sha256Hex(data) || ref.LLMCallReceipt != nil {
		return nil, fmt.Errorf("%s CAS identity mismatch", artifactType)
	}
	return ref, nil
}

func s3ArtifactRefURI(ref *ArtifactRef) string {
	if ref == nil {
		return "cas://missing"
	}
	return "cas://" + ref.Bucket + "/" + ref.Key
}

func s3EvidenceAssetV1(ref ArtifactRef) qualitygate.EvidenceAssetV1 {
	return qualitygate.EvidenceAssetV1{Ref: s3ArtifactRefURI(&ref), SHA256: ref.SHA256}
}

func s3BlockerV1(code, asset, runner, fixture, assertion string) qualitygate.ReviewBlockerV1 {
	return qualitygate.ReviewBlockerV1{Code: code, ResponsibleAsset: asset, Witness: qualitygate.ExecutableWitnessV1{Runner: runner, FixtureRef: fixture, Assertion: assertion}}
}

func sortedUniqueNonEmptyS3V1(values []string) bool {
	return len(values) > 0 && sortedUniqueNonEmptyAllowEmptyS3V1(values)
}

func sortedUniqueNonEmptyAllowEmptyS3V1(values []string) bool {
	if values == nil {
		return false
	}
	for index, value := range values {
		if value == "" || value != strings.TrimSpace(value) || (index > 0 && values[index-1] >= value) {
			return false
		}
	}
	return true
}

func validS3ReviewerRoleV1(role string) bool {
	switch role {
	case "semantic_spec", "final_statement", "oracle_promotion", "sanitizer", "boundary_coverage":
		return true
	default:
		return false
	}
}

func safeOpaqueS3TokenV1(value string, maxBytes int) bool {
	if value == "" || len(value) > maxBytes || value != strings.TrimSpace(value) || strings.Contains(value, "..") {
		return false
	}
	for _, ch := range value {
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '-' || ch == '_' || ch == '.' || ch == ':' {
			continue
		}
		return false
	}
	return true
}

func validS3HiddenFailureCodeV1(value string) bool {
	switch value {
	case "hidden.assertion_failed", "hidden.exposure_detected", "hidden.execution_failed", "hidden.executor_unavailable", "hidden.suite_unavailable":
		return true
	default:
		return false
	}
}

func compactSortedS3StringsV1(values []string) []string {
	if len(values) < 2 {
		return values
	}
	result := values[:1]
	for _, value := range values[1:] {
		if value != result[len(result)-1] {
			result = append(result, value)
		}
	}
	return result
}

func copyPresentS3StringsV1(values []string) []string {
	if values == nil {
		return nil
	}
	return append([]string{}, values...)
}

func s3ContractErrorV1(format string, args ...interface{}) error {
	return temporal.NewNonRetryableApplicationError(fmt.Sprintf(format, args...), s3QualityContractErrorTypeV1, nil)
}

func s3QualityErrorV1(format string, args ...interface{}) error {
	return temporal.NewNonRetryableApplicationError(fmt.Sprintf(format, args...), s3QualityNotMetErrorTypeV1, nil)
}
