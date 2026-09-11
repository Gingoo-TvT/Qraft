package activities

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
	"github.com/google/uuid"
)

type refreshRepoFixture struct {
	problem     *domain.Problem
	report      repository.ProblemEditRefreshReport
	options     repository.ProblemEditRefreshOptions
	completeErr error
}

func (r *refreshRepoFixture) GetByID(context.Context, uuid.UUID) (*domain.Problem, error) {
	if r.problem == nil {
		return nil, errors.New("problem missing")
	}
	copy := *r.problem
	copy.MetadataJSON = append([]byte(nil), r.problem.MetadataJSON...)
	return &copy, nil
}

func (r *refreshRepoFixture) CompleteProblemEditRefresh(_ context.Context, options repository.ProblemEditRefreshOptions) (repository.ProblemEditRefreshReport, error) {
	r.options = options
	if r.completeErr != nil {
		return repository.ProblemEditRefreshReport{}, r.completeErr
	}
	if r.report.Decision == "" {
		r.report.Decision = repository.ProblemEditRefreshDecisionGo
	}
	if r.report.ValidationReportSHA256 == "" {
		r.report.ValidationReportSHA256 = options.ValidationReportSHA256
	}
	return r.report, nil
}

type refreshVectorFixture struct {
	writes []repository.EmbeddingWrite
}

func (r *refreshVectorFixture) FindSimilarForVersion(context.Context, []float32, uuid.UUID, string, int, float64) ([]*domain.Problem, []float64, error) {
	return nil, nil, nil
}

func (r *refreshVectorFixture) UpdateEmbeddingForVersion(_ context.Context, write repository.EmbeddingWrite) error {
	write.Embedding = append([]float32(nil), write.Embedding...)
	r.writes = append(r.writes, write)
	return nil
}

func TestProblemRequiresEditRefresh(t *testing.T) {
	problem := domain.Problem{MetadataJSON: []byte(`{"stale":true}`)}
	if !ProblemRequiresEditRefresh(problem) {
		t.Fatal("stale metadata was not detected")
	}
	problem.MetadataJSON = []byte(`{"stale":false}`)
	if ProblemRequiresEditRefresh(problem) {
		t.Fatal("fresh metadata was marked stale")
	}
	problem.MetadataJSON = []byte(`not-json`)
	if ProblemRequiresEditRefresh(problem) {
		t.Fatal("invalid metadata was marked stale")
	}
}

func TestRefreshEditedProblemActivityReembedsAndCompletesGate(t *testing.T) {
	problemID := uuid.New()
	updatedAt := time.Date(2026, 8, 29, 1, 2, 3, 456000000, time.UTC)
	problem := &domain.Problem{
		ID:           problemID,
		Title:        "Edited title",
		Statement:    "Edited statement",
		OneLineHint:  "Edited hint",
		UpdatedAt:    updatedAt,
		MetadataJSON: []byte(`{"stale":true}`),
	}
	repo := &refreshRepoFixture{problem: problem}
	vector := &refreshVectorFixture{}
	embedder := &recordingEmbedder{result: []float32{0.25, 0.75}}
	artifactStore := &captureArtifactStore{}
	modelID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	acts := New(&Dependencies{
		Embedding:               embedder,
		EmbeddingProvider:       "linkapi",
		EmbeddingModel:          "text-embedding-3-large",
		EmbeddingModelVersionID: modelID,
		ProblemEditRefreshRepo:  repo,
		VectorRepo:              vector,
		ArtifactStore:           artifactStore,
	})

	result, err := acts.RefreshEditedProblemActivity(context.Background(), RefreshEditedProblemInput{
		ProblemID:           problemID,
		ExpectedUpdatedAt:   updatedAt,
		ExpectedContentHash: ProblemEmbeddingContentHash(*problem),
		ValidationMode:      "differential",
		TestCaseCount:       3,
		MainAudit:           SandboxAuditMetadata{RunID: "main-run"},
		ReferenceAudit:      &SandboxAuditMetadata{RunID: "brute-run"},
	})
	if err != nil {
		t.Fatalf("refresh activity failed: %v", err)
	}
	if result == nil || result.ValidationReport == nil {
		t.Fatalf("refresh result missing receipt: %+v", result)
	}
	if result.Report.Decision != repository.ProblemEditRefreshDecisionGo {
		t.Fatalf("decision = %q, want go", result.Report.Decision)
	}
	if len(vector.writes) != 1 || vector.writes[0].ProblemID != problemID || vector.writes[0].ModelVersionID != modelID {
		t.Fatalf("embedding writes = %+v", vector.writes)
	}
	if embedder.callCount() != 1 {
		t.Fatalf("embedding provider calls = %d, want 1", embedder.callCount())
	}
	if repo.options.ValidationReportSHA256 != result.ValidationReport.SHA256 || repo.options.Actor != "problem-validation-workflow" {
		t.Fatalf("refresh options = %+v, receipt = %+v", repo.options, result.ValidationReport)
	}
	if !strings.Contains(repo.options.OperationKey, problemID.String()) {
		t.Fatalf("operation key %q does not bind problem", repo.options.OperationKey)
	}
	var receipt problemEditRefreshReceiptV1
	if err := json.Unmarshal(artifactStore.data, &receipt); err != nil {
		t.Fatalf("decode receipt: %v", err)
	}
	if receipt.ProblemID != problemID.String() || receipt.ValidationMode != "differential" || receipt.TestCaseCount != 3 {
		t.Fatalf("receipt = %+v", receipt)
	}
	if receipt.Embedding.ContentHash != vector.writes[0].ContentHash || receipt.Embedding.ModelVersionID != modelID.String() {
		t.Fatalf("receipt embedding = %+v, write = %+v", receipt.Embedding, vector.writes[0])
	}
}

func TestRefreshEditedProblemActivityFailsClosedOnConcurrentEdit(t *testing.T) {
	problem := &domain.Problem{
		ID:           uuid.New(),
		Title:        "title",
		Statement:    "statement",
		UpdatedAt:    time.Now().UTC(),
		MetadataJSON: []byte(`{"stale":true}`),
	}
	repo := &refreshRepoFixture{problem: problem}
	vector := &refreshVectorFixture{}
	acts := New(&Dependencies{
		Embedding:               &recordingEmbedder{result: []float32{1}},
		EmbeddingProvider:       "linkapi",
		EmbeddingModel:          "model",
		EmbeddingModelVersionID: uuid.New(),
		ProblemEditRefreshRepo:  repo,
		VectorRepo:              vector,
		ArtifactStore:           &captureArtifactStore{},
	})
	_, err := acts.RefreshEditedProblemActivity(context.Background(), RefreshEditedProblemInput{
		ProblemID:         problem.ID,
		ExpectedUpdatedAt: problem.UpdatedAt.Add(-time.Second),
		ValidationMode:    "differential",
		TestCaseCount:     1,
	})
	if err == nil || !strings.Contains(err.Error(), "changed during validation") {
		t.Fatalf("error = %v, want concurrent-edit failure", err)
	}
	if len(vector.writes) != 0 {
		t.Fatalf("concurrent edit wrote embedding: %+v", vector.writes)
	}
}
