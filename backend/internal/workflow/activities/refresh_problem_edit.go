package activities

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
	"github.com/google/uuid"
	"go.temporal.io/sdk/activity"
)

// ProblemEditRefreshReceiptSchemaVersion identifies the durable receipt that
// binds a successful validation, the exact edited problem revision, and the
// embedding written before the publication gate is re-entered.
const ProblemEditRefreshReceiptSchemaVersion = 1

// RefreshEditedProblemInput is intentionally compact. The large test inputs
// and outputs have already been externalized by FetchProblemDataActivity; only
// their count and the sandbox audit identities are needed for the receipt.
type RefreshEditedProblemInput struct {
	ProblemID           uuid.UUID             `json:"problem_id"`
	ExpectedUpdatedAt   time.Time             `json:"expected_updated_at"`
	ExpectedContentHash string                `json:"expected_content_hash"`
	ValidationMode      string                `json:"validation_mode"`
	TestCaseCount       int                   `json:"test_case_count"`
	MainAudit           SandboxAuditMetadata  `json:"main_audit"`
	ReferenceAudit      *SandboxAuditMetadata `json:"reference_audit,omitempty"`
}

// RefreshEditedProblemResult is returned to the workflow as a small,
// history-safe summary. The receipt itself remains in CAS and is referenced by
// its content hash.
type RefreshEditedProblemResult struct {
	ValidationReport     *ArtifactRef                        `json:"validation_report"`
	Report               repository.ProblemEditRefreshReport `json:"report"`
	EmbeddingModelID     uuid.UUID                           `json:"embedding_model_id"`
	EmbeddingContentHash string                              `json:"embedding_content_hash"`
}

type problemEditRefreshReceiptV1 struct {
	SchemaVersion      int                           `json:"schema_version"`
	ProblemID          string                        `json:"problem_id"`
	ProblemUpdatedAt   string                        `json:"problem_updated_at"`
	ValidationWorkflow string                        `json:"validation_workflow_id"`
	ValidationRun      string                        `json:"validation_run_id"`
	ValidationMode     string                        `json:"validation_mode"`
	TestCaseCount      int                           `json:"test_case_count"`
	MainAudit          SandboxAuditMetadata          `json:"main_audit"`
	ReferenceAudit     *SandboxAuditMetadata         `json:"reference_audit,omitempty"`
	Embedding          problemEditRefreshEmbeddingV1 `json:"embedding"`
}

type problemEditRefreshEmbeddingV1 struct {
	Provider        string `json:"provider"`
	Model           string `json:"model"`
	ModelVersionID  string `json:"model_version_id"`
	ContentHash     string `json:"content_hash"`
	EmbeddingSHA256 string `json:"embedding_sha256"`
}

// ProblemRequiresEditRefresh reports whether a problem carries the edit
// invalidation marker. Invalid or absent metadata is treated as not stale so
// legacy validation workflows remain replay-compatible.
func ProblemRequiresEditRefresh(problem domain.Problem) bool {
	if len(problem.MetadataJSON) == 0 {
		return false
	}
	var metadata map[string]interface{}
	if err := json.Unmarshal(problem.MetadataJSON, &metadata); err != nil {
		return false
	}
	value, ok := metadata["stale"]
	stale, ok := value.(bool)
	return ok && stale
}

// ProblemEmbeddingContentHash returns the canonical content identity used by
// both storage and similarity search.
func ProblemEmbeddingContentHash(problem domain.Problem) string {
	return sha256Bytes([]byte(buildEmbeddingText(problem.Title, problem.Statement, problem.OneLineHint)))
}

// RefreshEditedProblemActivity re-embeds the exact revision that was fetched
// for validation, persists a CAS validation receipt, and atomically asks the
// repository to clear stale markers and re-enter the publication gate.
//
// The operation is safe to retry: provider-effect caching protects the remote
// embedding call, vector writes are content-addressed, the receipt is
// content-addressed, and CompleteProblemEditRefresh has its own operation
// ledger/idempotency key.
func (a *Activities) RefreshEditedProblemActivity(
	ctx context.Context,
	input RefreshEditedProblemInput,
) (*RefreshEditedProblemResult, error) {
	if input.ProblemID == uuid.Nil {
		return nil, fmt.Errorf("problem ID is required")
	}
	if input.ExpectedUpdatedAt.IsZero() {
		return nil, fmt.Errorf("expected problem updated_at is required")
	}
	if input.TestCaseCount <= 0 {
		return nil, fmt.Errorf("at least one validated testcase is required")
	}
	input.ValidationMode = strings.TrimSpace(input.ValidationMode)
	if input.ValidationMode == "" {
		return nil, fmt.Errorf("validation mode is required")
	}
	if a == nil || a.deps == nil {
		return nil, fmt.Errorf("activity dependencies are required")
	}

	repo := a.deps.ProblemEditRefreshRepo
	if repo == nil {
		repo = a.deps.ProblemRepo
	}
	if repo == nil {
		return nil, fmt.Errorf("problem edit refresh repository is not configured")
	}
	problem, err := repo.GetByID(ctx, input.ProblemID)
	if err != nil {
		return nil, fmt.Errorf("reloading problem before edit refresh: %w", err)
	}
	if problem == nil {
		return nil, fmt.Errorf("reloading problem before edit refresh returned nil")
	}
	if !problem.UpdatedAt.Equal(input.ExpectedUpdatedAt) {
		return nil, fmt.Errorf("problem changed during validation: expected updated_at %s, got %s", input.ExpectedUpdatedAt.UTC().Format(time.RFC3339Nano), problem.UpdatedAt.UTC().Format(time.RFC3339Nano))
	}
	contentHash := ProblemEmbeddingContentHash(*problem)
	if expected := strings.TrimSpace(input.ExpectedContentHash); expected != "" && expected != contentHash {
		return nil, fmt.Errorf("problem content changed during validation: expected content hash %s, got %s", expected, contentHash)
	}

	modelVersionID, err := a.deps.configuredStatementModelVersion()
	if err != nil {
		return nil, fmt.Errorf("resolving configured statement embedding model: %w", err)
	}
	if a.deps.Embedding == nil {
		return nil, fmt.Errorf("embedding client is not configured")
	}
	embeddingText := buildEmbeddingText(problem.Title, problem.Statement, problem.OneLineHint)
	var embedding []float32
	if activity.IsActivity(ctx) {
		embedding, err = a.cachedTemporalProblemEmbedding(ctx, "problem-edit-refresh", embeddingText)
	} else {
		embedding, err = a.deps.Embedding.Embed(ctx, embeddingText)
	}
	if err != nil {
		return nil, fmt.Errorf("generating edited problem embedding: %w", err)
	}
	if len(embedding) == 0 {
		return nil, fmt.Errorf("generated edited problem embedding is empty")
	}
	embeddingHash, err := sha256JSON(embedding)
	if err != nil {
		return nil, fmt.Errorf("hashing edited problem embedding: %w", err)
	}
	if a.deps.VectorRepo == nil {
		return nil, fmt.Errorf("vector repository is not configured")
	}
	if err := a.deps.VectorRepo.UpdateEmbeddingForVersion(ctx, repository.EmbeddingWrite{
		ProblemID:      input.ProblemID,
		ModelVersionID: modelVersionID,
		Kind:           repository.EmbeddingKindStatement,
		ContentHash:    contentHash,
		Embedding:      embedding,
	}); err != nil {
		return nil, fmt.Errorf("storing edited problem embedding: %w", err)
	}

	workflowID, runID := validationActivityIdentity(ctx)
	provider := strings.TrimSpace(a.deps.EmbeddingProvider)
	if provider == "" {
		provider = "algoforge-embedding"
	}
	model := strings.TrimSpace(a.deps.EmbeddingModel)
	if model == "" {
		model = "configured-embedding"
	}
	receipt := problemEditRefreshReceiptV1{
		SchemaVersion:      ProblemEditRefreshReceiptSchemaVersion,
		ProblemID:          input.ProblemID.String(),
		ProblemUpdatedAt:   problem.UpdatedAt.UTC().Format(time.RFC3339Nano),
		ValidationWorkflow: workflowID,
		ValidationRun:      runID,
		ValidationMode:     input.ValidationMode,
		TestCaseCount:      input.TestCaseCount,
		MainAudit:          input.MainAudit,
		ReferenceAudit:     input.ReferenceAudit,
		Embedding: problemEditRefreshEmbeddingV1{
			Provider:        provider,
			Model:           model,
			ModelVersionID:  modelVersionID.String(),
			ContentHash:     contentHash,
			EmbeddingSHA256: embeddingHash,
		},
	}
	receiptJSON, err := json.Marshal(receipt)
	if err != nil {
		return nil, fmt.Errorf("encoding edit refresh validation receipt: %w", err)
	}
	artifact, err := a.putArtifactWithMetadata(ctx, receiptJSON, "application/json", ArtifactMetadata{
		Producer:       "RefreshEditedProblemActivity",
		Provider:       provider,
		Model:          model,
		ModelRevision:  modelVersionID.String(),
		WorkflowID:     workflowID,
		PayloadVersion: ActivityPayloadVersion,
		ArtifactType:   "problem_validation_receipt",
		SourceType:     "problem_validation",
		ArtifactRole:   "problem:" + input.ProblemID.String() + ":edit-refresh",
		RetentionClass: "workflow_cas_reviewed",
	})
	if err != nil {
		return nil, fmt.Errorf("persisting edit refresh validation receipt: %w", err)
	}

	operationKey := "problem-edit-refresh:" + input.ProblemID.String() + ":" + artifact.SHA256[:16]
	report, err := repo.CompleteProblemEditRefresh(ctx, repository.ProblemEditRefreshOptions{
		ProblemID:              input.ProblemID,
		OperationKey:           operationKey,
		Actor:                  "problem-validation-workflow",
		ValidationReportSHA256: artifact.SHA256,
	})
	if err != nil {
		return nil, fmt.Errorf("completing edited problem refresh: %w", err)
	}

	return &RefreshEditedProblemResult{
		ValidationReport:     artifact,
		Report:               report,
		EmbeddingModelID:     modelVersionID,
		EmbeddingContentHash: contentHash,
	}, nil
}

func validationActivityIdentity(ctx context.Context) (string, string) {
	if activity.IsActivity(ctx) {
		info := activity.GetInfo(ctx)
		workflowID := strings.TrimSpace(info.WorkflowExecution.ID)
		runID := strings.TrimSpace(info.WorkflowExecution.RunID)
		if workflowID != "" && runID != "" {
			return workflowID, runID
		}
	}
	return "local-validation", "local-validation"
}
