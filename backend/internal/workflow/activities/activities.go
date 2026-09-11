// Package activities implements the Temporal activity functions for the
// AlgoForge problem generation workflow. Each activity encapsulates a discrete
// unit of work (LLM call, sandbox execution, database operation, etc.) that
// can be independently retried and monitored.
package activities

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/config"
	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/llm"
	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
)

// Dependencies bundles all external dependencies that activities need. It is
// constructed once at worker startup and shared across all activity executions.
type Dependencies struct {
	LLM                     LLMCompleter
	LLMProvider             string
	LLMModel                string
	LLMBaseURL              string
	Embedding               llm.Embedder
	EmbeddingEnabled        bool
	EmbeddingProvider       string
	EmbeddingModel          string
	EmbeddingModelVersionID uuid.UUID
	MinIO                   *minio.Client
	MinioBucket             string
	ProblemRepo             *repository.ProblemRepository
	ProblemEditRefreshRepo  ProblemEditRefreshStore
	ProblemTitleLookup      ProblemTitleLookup
	ReviewSettings          *repository.ReviewSettingsRepository
	TestCaseRepo            *repository.TestCaseRepository
	VectorRepo              ProblemVectorStore
	QuizRepo                *repository.QuizRepository
	KPRepo                  *repository.KnowledgePointRepository
	OperationLedger         OperationLedger
	ProviderEffects         ProviderEffectStore
	ProviderEffectLease     time.Duration
	OutboxQueue             OutboxQueue
	OutboxPublisher         OutboxPublisher
	OutboxDispatch          OutboxDispatchOptions
	ProvenanceRecorder      ProvenanceArtifactRecorder
	// ArtifactStore is an additive injection seam for deterministic workflow
	// acceptance tests and non-MinIO deployments. Production keeps using MinIO
	// when this field is nil.
	ArtifactStore            ArtifactStore
	RemoteSandboxFactory     RemoteSandboxExecutorFactoryV1
	S3SandboxIdentityPolicy  *S3SandboxIdentityPolicyV1
	HiddenSuiteResolver      HiddenSuiteResolverV1
	HiddenRegressionExecutor HiddenRegressionExecutorV1
	RepairRevisionGenerator  RepairRevisionGeneratorV1
	SandboxCfg               config.SandboxConfig
}

type LLMCompleter interface {
	CompleteWithRetry(context.Context, *llm.Request, int) (*llm.Response, error)
}

type ProviderEffectStore interface {
	Acquire(context.Context, string, string, string, time.Duration) (repository.ProviderEffectClaim, error)
	Complete(context.Context, string, string, string, uuid.UUID, json.RawMessage) error
	Fail(context.Context, string, string, string, uuid.UUID, string) error
}

// ProblemVectorStore is the narrow vector dependency used by generation
// activities. Keeping it as an interface lets fail-closed behavior be tested
// without a live pgvector database.
type ProblemVectorStore interface {
	FindSimilarForVersion(
		context.Context,
		[]float32,
		uuid.UUID,
		string,
		int,
		float64,
	) ([]*domain.Problem, []float64, error)
	UpdateEmbeddingForVersion(context.Context, repository.EmbeddingWrite) error
}

// ProblemEditRefreshStore is the narrow repository contract used by the
// validation workflow after an edited problem passes differential execution.
// Keeping this seam separate from ProblemRepo lets the refresh activity be
// exercised without a live database while production still uses the same
// repository implementation.
type ProblemEditRefreshStore interface {
	GetByID(context.Context, uuid.UUID) (*domain.Problem, error)
	CompleteProblemEditRefresh(context.Context, repository.ProblemEditRefreshOptions) (repository.ProblemEditRefreshReport, error)
}

// ProblemTitleLookup is the narrow exact-title dependency used by dedup. The
// concrete repository remains the production default while acceptance tests
// can exercise the real activity without a database.
type ProblemTitleLookup interface {
	FindByTitle(context.Context, string, int) ([]*domain.Problem, error)
}

// ValidateProviderEffectConfiguration is called before a production worker
// registers activities. It prevents a partially wired worker from silently
// bypassing the durable provider-effect cache or hashing an unresolved model.
func (d *Dependencies) ValidateProviderEffectConfiguration() error {
	if d == nil {
		return fmt.Errorf("activity dependencies are required")
	}
	if d.ProviderEffects == nil {
		return fmt.Errorf("provider effect store is required")
	}
	if d.ProviderEffectLease <= 0 {
		return fmt.Errorf("provider effect lease must be positive")
	}
	identities := map[string]string{
		"LLM provider": d.LLMProvider,
		"LLM model":    d.LLMModel,
	}
	if d.EmbeddingEnabled {
		identities["embedding provider"] = d.EmbeddingProvider
		identities["embedding model"] = d.EmbeddingModel
		if d.EmbeddingModelVersionID == uuid.Nil {
			return fmt.Errorf("configured statement model version is required for provider effect caching")
		}
	}
	for name, value := range identities {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s identity is required for provider effect caching", name)
		}
	}
	return nil
}

func (d *Dependencies) configuredStatementModelVersion() (uuid.UUID, error) {
	if d == nil || d.EmbeddingModelVersionID == uuid.Nil {
		return uuid.Nil, repository.ErrConfiguredStatementModelVersionRequired
	}
	return d.EmbeddingModelVersionID, nil
}

// Activities holds a reference to the shared dependencies and exposes
// activity methods. Each method is registered with the Temporal worker.
type Activities struct {
	deps      *Dependencies
	artifacts ArtifactStore
}

// StatementSamplesPlaceholder is replaced only after the structured sample
// inputs have passed main/brute differential validation.
const StatementSamplesPlaceholder = "<!-- ALGOFORGE_SAMPLES -->"

const ActivityPayloadVersion = 1

// ReferenceSolutionMemoryLimitMB is the fixed memory ceiling used by the
// legacy brute/reference oracle.  The oracle is a correctness witness only;
// its budget must not be derived from (or multiplied from) the contestant
// memory limit.  Keep this below the permanent sandbox ceiling so every
// selected differential case has the same reproducible resource contract.
const ReferenceSolutionMemoryLimitMB = 512

// MaxReferenceDifferentialInputBytes is only a transport/admission guard for
// newly generated brute-check cases. Input size is not a proof of semantic
// complexity (a huge scalar bound can serialize to a handful of bytes), so
// workflow selection must also come from public samples or an explicit small
// BruteCheck group. The actual time-limit verdict remains authoritative and is
// classified as a reference failure when the oracle still cannot finish.
const MaxReferenceDifferentialInputBytes int64 = 16 << 10

// ReferenceSolutionFailurePrefix is the stable error prefix used when the
// brute/reference program itself cannot compile or run.  Callers use this
// marker to avoid turning an oracle defect into a rejection of the main
// solution or of the official test suite.
const ReferenceSolutionFailurePrefix = "reference solution program failure"

// MaxReferenceDifferentialCases bounds the legacy brute oracle workload.  A
// manifest or generation config may contain a much larger official suite, but
// the correctness-only oracle is never allowed to silently become a full-suite
// performance job.
const MaxReferenceDifferentialCases = 32

// StoreProblemPayloadVersion is separate because review quarantine is a
// publication-critical field. A v1 worker rejects this version before any
// side effects instead of silently dropping the unknown JSON field.
const StoreProblemPayloadVersion = 2

// StoreProblemTestManifestPayloadVersion makes the derived quality manifest
// publication-critical: older workers reject v3 before performing side effects.
const StoreProblemTestManifestPayloadVersion = 3

// StoreProblemKnowledgePointCombinationPayloadVersion makes the jobs v1
// knowledge-point contract publication-critical. Older workers reject v4
// before side effects instead of silently dropping the nested contract.
const StoreProblemKnowledgePointCombinationPayloadVersion = 4

// StoreProblemStandardEvidencePayloadVersion makes the downloadable standard
// evidence receipt publication-critical. Older workers reject v5 before any
// side effects instead of silently dropping the request.
const StoreProblemStandardEvidencePayloadVersion = 5

// StoreProblemS3QualityDraftPayloadVersion persists the canonical S3
// TestManifest v2 and a complete nine-gate PASS artifact binding as an
// editable draft. It deliberately does not invoke any publication policy.
const StoreProblemS3QualityDraftPayloadVersion = 6

const KnowledgePointConformanceMetadataKey = "knowledge_point_conformance"

const SimilarityParamLevelThreshold = 0.82
const SimilarityStatementWarnThreshold = 0.76
const SimilarityStatementRejectThreshold = 0.84
const SimilarityParamTopK = 8
const SimilarityStatementTopK = 5
const DedupReportSchemaVersion = "algoforge.workflow.dedup.report.v1"

const (
	DedupStagePreGeneration = "pre_generation"
	DedupStagePostStatement = "post_statement"

	DedupDecisionPass        = "pass"
	DedupDecisionWarn        = "warn"
	DedupDecisionRejected    = "rejected"
	DedupDecisionCheckFailed = "check_failed"
	// DedupDecisionProviderUnavailable is retained for source compatibility
	// with older callers. New reports use DedupDecisionCheckFailed.
	DedupDecisionProviderUnavailable = "provider_unavailable"
)

// New creates a new Activities instance with the given dependencies.
func New(deps *Dependencies) *Activities {
	activities := &Activities{deps: deps}
	if deps != nil && deps.ArtifactStore != nil {
		activities.artifacts = deps.ArtifactStore
	} else if deps != nil && deps.MinIO != nil && deps.MinioBucket != "" {
		activities.artifacts = NewMinIOArtifactStore(deps.MinIO, deps.MinioBucket)
	}
	return activities
}

// buildEmbeddingText returns the canonical text fed to the embedding model.
// Store and post-statement similarity must share this formula.
func buildEmbeddingText(title, statement, oneLineHint string) string {
	return title + "\n\n" + statement + "\n\n" + oneLineHint
}

// ---------------------------------------------------------------------------
// Shared result types
// ---------------------------------------------------------------------------

// SimilarityCheckResult contains the outcome of the similarity check activity.
type SimilarityCheckResult struct {
	// TooSimilar is true if the number of similar problems exceeds the limit.
	TooSimilar bool `json:"too_similar"`

	// SimilarProblems lists the IDs and similarity scores of matching problems.
	SimilarProblems []SimilarProblem `json:"similar_problems,omitempty"`

	// Neighbors lists top similar problems for prompt injection.
	Neighbors []NeighborInfo `json:"neighbors,omitempty"`

	// Report is the auditable E2 dedup decision payload.
	Report *DedupReport `json:"report,omitempty"`
}

// SimilarProblem records a single similar problem found during the check.
type SimilarProblem struct {
	ID         uuid.UUID `json:"id"`
	Title      string    `json:"title"`
	Similarity float64   `json:"similarity"`
}

// PostStatementSimilarityResult contains the post-generation similarity check.
type PostStatementSimilarityResult struct {
	MaxSimilarity float64        `json:"max_similarity"`
	Neighbors     []NeighborInfo `json:"neighbors,omitempty"`
	HardReject    bool           `json:"hard_reject"`
	Warning       bool           `json:"warning"`
	Report        *DedupReport   `json:"report,omitempty"`
}

// DedupReport is the stable, compact evidence payload emitted by generation
// dedup checks and persisted with stored candidates.
type DedupReport struct {
	SchemaVersion   string  `json:"schema_version"`
	Stage           string  `json:"stage"`
	ModelVersion    string  `json:"model_version,omitempty"`
	Kind            string  `json:"kind"`
	ContentHash     string  `json:"content_hash"`
	TopK            int     `json:"top_k"`
	Threshold       float64 `json:"threshold"`
	RejectThreshold float64 `json:"reject_threshold,omitempty"`
	Decision        string  `json:"decision"`
	// NeighborCount is present only after a real similarity query succeeds.
	// A non-nil pointer to zero therefore means "checked, no neighbors" while
	// nil means the check never produced a neighbor count.
	NeighborCount *int    `json:"neighbor_count,omitempty"`
	MaxSimilarity float64 `json:"max_similarity,omitempty"`
	SimilarLimit  int     `json:"similar_limit,omitempty"`
	Reason        string  `json:"reason,omitempty"`
}

// NeighborInfo records a nearby existing problem for dedup prompts.
type NeighborInfo struct {
	ID          uuid.UUID `json:"id"`
	Title       string    `json:"title"`
	OneLineHint string    `json:"one_line_hint"`
	Tags        []string  `json:"tags"`
	Similarity  float64   `json:"similarity"`
}

// StatementResult contains the generated problem statement and metadata.
type StatementResult struct {
	SourceArtifacts         []*ArtifactRef                  `json:"source_artifacts,omitempty"`
	Title                   string                          `json:"title"`
	Statement               string                          `json:"statement"`
	Tags                    []string                        `json:"tags"`
	OneLineHint             string                          `json:"one_line_hint"`
	DifficultyJustification string                          `json:"difficulty_justification"`
	MarkdownDiagnostics     []StatementMarkdownDiagnosticV1 `json:"markdown_diagnostics,omitempty"`
}

// GenerateStatementInput bundles statement generation params and neighbors.
type GenerateStatementInput struct {
	Params                domain.ProblemGenParams   `json:"params"`
	Neighbors             []NeighborInfo            `json:"neighbors,omitempty"`
	UseStructuredSamples  bool                      `json:"use_structured_samples,omitempty"`
	RetryFeedback         *StatementRetryFeedbackV1 `json:"retry_feedback,omitempty"`
	EnableStatementRepair bool                      `json:"enable_statement_repair,omitempty"`
	// StrictMarkdownQuality opts new workflow histories into the current
	// statement contract. It is version-gated so older histories retain their
	// original validator semantics during replay.
	StrictMarkdownQuality bool `json:"strict_markdown_quality,omitempty"`
}

// SolutionResult contains the generated main and brute-force solutions.
type SolutionResult struct {
	SourceArtifacts    []*ArtifactRef             `json:"source_artifacts,omitempty"`
	MainSolution       domain.Solution            `json:"main_solution"`
	BruteSolution      domain.Solution            `json:"brute_solution"`
	OracleIndependence *OracleIndependenceReceipt `json:"oracle_independence,omitempty"`
}

// CompileCheckResult contains the outcome of compiling solutions.
type CompileCheckResult struct {
	AllCompiled bool   `json:"all_compiled"`
	ErrorDetail string `json:"error_detail,omitempty"`
}

// TestCaseData represents a single test case's input and metadata before it
// is persisted to the database.
type TestCaseOrigin string

const (
	TestCaseOriginLLMInline TestCaseOrigin = "llm_inline"
	TestCaseOriginGenerator TestCaseOrigin = "generator"
	TestCaseOriginCustom    TestCaseOrigin = "custom"
)

type TestCaseData struct {
	Input         string       `json:"input"`
	InputArtifact *ArtifactRef `json:"input_artifact,omitempty"`
	// InputRef is retained only so in-flight legacy workflow histories can replay.
	// New activity results must use InputArtifact for cross-activity data.
	InputRef    string `json:"input_ref,omitempty"`
	GroupID     int    `json:"group_id"`
	IsSample    bool   `json:"is_sample"`
	Description string `json:"description,omitempty"`
	// Coverage contains normalized intent labels supplied by the generator.
	// They are advisory metadata: input parsing, sandbox execution, and
	// differential checks remain the authoritative correctness gates.
	Coverage                  []string       `json:"coverage,omitempty"`
	Origin                    TestCaseOrigin `json:"origin,omitempty"`
	GeneratorOutputLimitBytes int64          `json:"generator_output_limit_bytes,omitempty"`
	GeneratorSeed             int64          `json:"generator_seed,omitempty"`
	GeneratorCaseIndex        *int           `json:"generator_case_index,omitempty"`
	GeneratorBatchIndex       *int           `json:"generator_batch_index,omitempty"`
}

type GeneratorBatchAudit struct {
	BatchIndex           int                  `json:"batch_index"`
	TestIndexes          []int                `json:"test_indexes"`
	GeneratorCaseIndexes []int                `json:"generator_case_indexes"`
	Audit                SandboxAuditMetadata `json:"audit"`
}

// TestDataResult contains the generated test data.
type TestDataResult struct {
	// SelectedTestCaseCount is server-derived after custom/generator merging.
	SelectedTestCaseCount int `json:"selected_test_case_count,omitempty"`
	// DeclaredTestCaseCount is the optional count emitted by the LLM. The
	// activity checks it against the final merged list before returning.
	DeclaredTestCaseCount int                   `json:"declared_test_case_count,omitempty"`
	PayloadVersion        int                   `json:"payload_version"`
	SourceArtifacts       []*ArtifactRef        `json:"source_artifacts,omitempty"`
	TestCases             []TestCaseData        `json:"test_cases"`
	GeneratorCode         string                `json:"generator_code,omitempty"`
	GeneratorSHA256       string                `json:"generator_sha256,omitempty"`
	GeneratorBatches      []GeneratorBatchAudit `json:"generator_batches,omitempty"`
}

// ExecutionLimits specifies resource constraints for sandbox execution.
type ExecutionLimits struct {
	TimeLimitMs   int `json:"time_limit_ms"`
	MemoryLimitMB int `json:"memory_limit_mb"`
	// OutputLimitBytes is additive. S3 uses a fixed value across differently
	// sized oracle and sample batches so their trusted limit identity matches.
	OutputLimitBytes int64 `json:"output_limit_bytes,omitempty"`
	// PreserveOutputBytes keeps stdout byte-exact for S3 oracle/sample gates.
	// Legacy executions retain their historical trailing-space normalization.
	PreserveOutputBytes bool `json:"preserve_output_bytes,omitempty"`
	// Profile is additive and omitted for every legacy/default execution.
	Profile string `json:"profile,omitempty"`
}

// SandboxResult contains the outputs and resource usage from running a
// solution against test cases in the sandbox.
type SandboxResult struct {
	PayloadVersion  int            `json:"payload_version"`
	Outputs         []string       `json:"outputs"`
	OutputArtifacts []*ArtifactRef `json:"output_artifacts,omitempty"`
	// OutputRefs is retained only so in-flight legacy workflow histories can
	// replay. New activity results must use OutputArtifacts.
	OutputRefs []string             `json:"output_refs,omitempty"`
	TimeTaken  []time.Duration      `json:"time_taken"`
	MemoryUsed []int64              `json:"memory_used"`
	Audit      SandboxAuditMetadata `json:"audit"`
}

type SandboxAuditMetadata struct {
	RunID                   string `json:"run_id"`
	ManifestDigest          string `json:"manifest_digest"`
	Seed                    int64  `json:"seed"`
	LimitProfile            string `json:"limit_profile"`
	Profile                 string `json:"profile,omitempty"`
	ImageDigest             string `json:"image_digest"`
	ToolchainManifestDigest string `json:"toolchain_manifest_digest"`
	SeccompPolicyDigest     string `json:"seccomp_policy_digest"`
}

// ResourceCalibrationV1 records how the model solution's measured resource
// usage was converted into the limits that are ultimately stored with the
// problem. The final sandbox receipt proves that the exact model solution and
// generated test suite passed under those derived limits.
type ResourceCalibrationV1 struct {
	SchemaVersion          int                  `json:"schema_version"`
	Policy                 string               `json:"policy"`
	ObservedCaseCount      int                  `json:"observed_case_count"`
	BenchmarkLimits        ExecutionLimits      `json:"benchmark_limits"`
	ObservedMaxTimeMS      int64                `json:"observed_max_time_ms"`
	ObservedMaxMemoryBytes int64                `json:"observed_max_memory_bytes"`
	FinalLimits            ExecutionLimits      `json:"final_limits"`
	StackLimitMB           int                  `json:"stack_limit_mb"`
	BenchmarkAudit         SandboxAuditMetadata `json:"benchmark_audit"`
	FinalAudit             SandboxAuditMetadata `json:"final_audit"`
}

// ValidationResult contains the outcome of cross-validating main vs brute
// solution outputs.
type ValidationResult struct {
	AllPassed  bool       `json:"all_passed"`
	Mismatches []Mismatch `json:"mismatches,omitempty"`
}

// Mismatch records a test case where the main and brute solutions produced
// different outputs.
type Mismatch struct {
	TestIndex   int    `json:"test_index"`
	MainOutput  string `json:"main_output"`
	BruteOutput string `json:"brute_output"`
}

// ReviewResult contains the outcome of the LLM review activity.
type ReviewResult struct {
	SourceArtifacts     []*ArtifactRef  `json:"source_artifacts,omitempty"`
	Approved            bool            `json:"approved"`
	Issues              []string        `json:"issues,omitempty"`
	Suggestions         []string        `json:"suggestions,omitempty"`
	Confidence          float64         `json:"confidence"`
	EstimatedDifficulty int             `json:"estimated_difficulty,omitempty"`
	IsDuplicate         bool            `json:"is_duplicate"`
	DuplicateOf         string          `json:"duplicate_of,omitempty"`
	DuplicateReason     string          `json:"duplicate_reason,omitempty"`
	ReviewDetails       json.RawMessage `json:"review_details,omitempty"`
	FullText            string          `json:"full_text,omitempty"`
}

// LLMReviewInput bundles all data needed for LLM review.
type LLMReviewInput struct {
	Statement           StatementResult         `json:"statement"`
	Solutions           SolutionResult          `json:"solutions"`
	TestCases           []TestCaseData          `json:"test_cases"`
	Params              domain.ProblemGenParams `json:"params"`
	Neighbors           []NeighborInfo          `json:"neighbors,omitempty"`
	ResourceCalibration *ResourceCalibrationV1  `json:"resource_calibration,omitempty"`
}

// FinalizeStatementSamplesInput binds the public statement samples to the
// already validated structured test cases and main-solution outputs.
type FinalizeStatementSamplesInput struct {
	PayloadVersion        int             `json:"payload_version"`
	Statement             StatementResult `json:"statement"`
	TestCases             []TestCaseData  `json:"test_cases"`
	SandboxOutput         SandboxResult   `json:"sandbox_output"`
	Locale                string          `json:"locale,omitempty"`
	ExpectedSampleCount   int             `json:"expected_sample_count,omitempty"`
	EnforceSampleCount    bool            `json:"enforce_sample_count,omitempty"`
	StrictMarkdownQuality bool            `json:"strict_markdown_quality,omitempty"`
}

// StoreInput bundles all the data needed to persist a problem.
type StoreInput struct {
	PayloadVersion   int                           `json:"payload_version"`
	IdempotencyKey   string                        `json:"idempotency_key"`
	WorkflowID       string                        `json:"workflow_id"`
	SourceArtifacts  []*ArtifactRef                `json:"source_artifacts,omitempty"`
	Statement        StatementResult               `json:"statement"`
	Solutions        SolutionResult                `json:"solutions"`
	TestCases        []TestCaseData                `json:"test_cases"`
	SandboxOutput    SandboxResult                 `json:"sandbox_output"`
	Params           domain.ProblemGenParams       `json:"params"`
	Editorial        string                        `json:"editorial,omitempty"`
	DedupReports     []DedupReport                 `json:"dedup_reports,omitempty"`
	ReviewQuarantine *ReviewQuarantineEvidence     `json:"review_quarantine,omitempty"`
	TestManifest     *TestManifestV1               `json:"test_manifest,omitempty"`
	TestManifestV2   *TestManifestV2               `json:"test_manifest_v2,omitempty"`
	QualityPassDraft *S3QualityPassDraftEvidenceV1 `json:"quality_pass_draft,omitempty"`
}

// ReviewQuarantineEvidence is the immutable review verdict and execution
// lineage that forces a stored candidate into quarantine.
type ReviewQuarantineEvidence struct {
	Reason             string         `json:"reason"`
	WorkflowRunID      string         `json:"workflow_run_id"`
	ReviewGateChangeID string         `json:"review_gate_change_id"`
	ReviewGateVersion  int            `json:"review_gate_version"`
	ReviewResultSHA256 string         `json:"review_result_sha256"`
	ReviewResult       ReviewResult   `json:"review_result"`
	SourceAncestry     []*ArtifactRef `json:"source_ancestry"`
}

// StoreResult contains the identifiers of the stored problem.
type StoreResult struct {
	ProblemID        uuid.UUID                                   `json:"problem_id"`
	SerialNumber     string                                      `json:"serial_number"`
	Status           domain.ProblemStatus                        `json:"status"`
	QuarantineReason string                                      `json:"quarantine_reason,omitempty"`
	StandardEvidence *domain.GenerationStandardEvidenceReference `json:"standard_evidence,omitempty"`
}

// FeasibilityResult is the outcome of AssessProblemFeasibilityActivity.
type FeasibilityResult struct {
	SourceArtifacts []*ArtifactRef `json:"source_artifacts,omitempty"`
	// Feasible is true when the problem is well-formed and the validation
	// mismatch is attributable to buggy solution code rather than the problem.
	Feasible bool `json:"feasible"`

	// Reason is a concise explanation of the assessment.
	Reason string `json:"reason"`
}

// EditorialResult contains the generated editorial text.
type EditorialResult struct {
	SourceArtifacts []*ArtifactRef `json:"source_artifacts,omitempty"`
	Editorial       string         `json:"editorial"`
}

type QuizGenerateInput struct {
	Subject             string                `json:"subject"`
	Type                domain.QuizType       `json:"type"`
	Difficulty          domain.QuizDifficulty `json:"difficulty"`
	KnowledgePointCodes []string              `json:"knowledge_point_codes"`
	KnowledgePointNames []string              `json:"knowledge_point_names,omitempty"`
	KnowledgePointIDs   []uuid.UUID           `json:"knowledge_point_ids,omitempty"`
	Count               int                   `json:"count"`
	Tags                []string              `json:"tags,omitempty"`
	Visibility          domain.QuizVisibility `json:"visibility"`
	IsVIP               bool                  `json:"is_vip"`
	CustomPrompt        string                `json:"custom_prompt,omitempty"`
	// LLMRuntime is resolved from the durable statement-model settings by the
	// API before a workflow starts. It contains only a runtime key reference.
	LLMRuntime *domain.LLMRuntimeConfig `json:"llm_runtime,omitempty"`
}

type QuizDraft struct {
	Title       string              `json:"title"`
	Statement   string              `json:"statement"`
	Options     []domain.QuizOption `json:"options,omitempty"`
	Answers     []string            `json:"answers"`
	Explanation string              `json:"explanation,omitempty"`
}

type QuizGenerateResult struct {
	SourceArtifacts []*ArtifactRef `json:"source_artifacts,omitempty"`
	Drafts          []QuizDraft    `json:"drafts"`
}

type QuizStoreInput struct {
	PayloadVersion    int               `json:"payload_version"`
	IdempotencyKey    string            `json:"idempotency_key"`
	SourceArtifacts   []*ArtifactRef    `json:"source_artifacts,omitempty"`
	Drafts            []QuizDraft       `json:"drafts"`
	Params            QuizGenerateInput `json:"params"`
	KnowledgePointIDs []uuid.UUID       `json:"kp_ids"`
}

type QuizStoreResult struct {
	SourceArtifacts []*ArtifactRef `json:"source_artifacts,omitempty"`
	InsertedIDs     []uuid.UUID    `json:"inserted_ids"`
	Codes           []string       `json:"codes"`
}
