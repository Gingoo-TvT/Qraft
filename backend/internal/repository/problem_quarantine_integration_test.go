//go:build integration

package repository_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
	"github.com/Gingoo-TvT/Qraft/backend/internal/workflow/activities"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestApplyReviewQuarantinePersistsCompleteEvidenceExactlyOnceIntegration(t *testing.T) {
	if os.Getenv("ALGOFORGE_INTEGRATION_DISPOSABLE_DB") != "true" {
		t.Skip("set ALGOFORGE_INTEGRATION_DISPOSABLE_DB=true with a disposable migrated DATABASE_URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	problemRepo := repository.NewProblemRepository(pool)
	problem := &domain.Problem{
		ID:           uuid.New(),
		Title:        "Review quarantine integration fixture",
		Statement:    "Given an integer, print it.",
		Level:        domain.LevelAlgorithm,
		Difficulty:   1500,
		Tags:         []string{"integration"},
		TimeLimit:    1000,
		MemoryLimit:  64,
		Source:       "integration_fixture",
		Status:       domain.ProblemStatusGenerating,
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
		MetadataJSON: json.RawMessage(`{"fixture":true}`),
	}
	if err := problemRepo.Create(ctx, problem); err != nil {
		t.Fatal(err)
	}
	operationKey := "integration/review-quarantine/" + uuid.NewString()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM workflow_outbox WHERE operation_key=$1`, operationKey)
	})

	ref := &activities.ArtifactRef{ModelRevision: "review-model-r1", SHA256: strings.Repeat("a", 64)}
	review := activities.ReviewResult{
		SourceArtifacts:     []*activities.ArtifactRef{ref},
		Approved:            false,
		Issues:              []string{"ambiguous boundary"},
		Suggestions:         []string{"clarify the bound"},
		Confidence:          0.91,
		EstimatedDifficulty: 1500,
		FullText:            `{"approved":false,"issues":["ambiguous boundary"],"review_details":{"clarity":{"score":4,"notes":"bound omitted"}}}`,
	}
	reviewJSON, reviewHash, err := activities.CanonicalReviewResultJSON(review)
	if err != nil {
		t.Fatal(err)
	}
	ancestryJSON, err := json.Marshal(review.SourceArtifacts)
	if err != nil {
		t.Fatal(err)
	}
	record := repository.ReviewQuarantineRecord{
		ProblemID:          problem.ID,
		OperationKey:       operationKey,
		Reason:             "automated LLM review rejected candidate",
		WorkflowID:         "problem-generation-integration",
		WorkflowRunID:      "run-integration",
		ReviewGateChangeID: "problem-generation-review-gate-v2",
		ReviewGateVersion:  2,
		ReviewResultSHA256: reviewHash,
		ReviewResultJSON:   reviewJSON,
		ReviewText:         review.FullText,
		SourceAncestryJSON: ancestryJSON,
	}
	for delivery := 1; delivery <= 2; delivery++ {
		status, reason, err := problemRepo.ApplyReviewQuarantine(ctx, record)
		if err != nil {
			t.Fatalf("delivery %d: %v", delivery, err)
		}
		if status != domain.ProblemStatusQuarantined || reason != record.Reason {
			t.Fatalf("delivery %d result = %q/%q", delivery, status, reason)
		}
	}

	var status domain.ProblemStatus
	var records, outbox int
	var storedText, workflowID, runID, changeID, storedHash string
	var storedVersion int
	var storedReview, storedAncestry json.RawMessage
	err = pool.QueryRow(ctx, `
		SELECT p.status,
		       (SELECT COUNT(*) FROM problem_quarantine_records WHERE operation_key=$2),
		       (SELECT COUNT(*) FROM workflow_outbox WHERE operation_key=$2 AND event_type='problem.review_quarantined'),
		       q.review_text, q.workflow_id, q.workflow_run_id,
		       q.review_gate_change_id, q.review_gate_version, q.review_result_sha256,
		       q.review_result, q.source_ancestry
		FROM problems p
		JOIN problem_quarantine_records q ON q.problem_id=p.id
		WHERE p.id=$1 AND q.operation_key=$2`, problem.ID, operationKey).Scan(
		&status, &records, &outbox, &storedText, &workflowID, &runID,
		&changeID, &storedVersion, &storedHash, &storedReview, &storedAncestry,
	)
	if err != nil {
		t.Fatal(err)
	}
	if status != domain.ProblemStatusQuarantined || records != 1 || outbox != 1 ||
		storedText != review.FullText || workflowID != record.WorkflowID || runID != record.WorkflowRunID ||
		changeID != record.ReviewGateChangeID || storedVersion != 2 || storedHash != reviewHash {
		t.Fatalf("stored quarantine evidence mismatch status=%s records=%d outbox=%d text=%q workflow=%s run=%s change=%s/%d hash=%s",
			status, records, outbox, storedText, workflowID, runID, changeID, storedVersion, storedHash)
	}
	if !json.Valid(storedReview) || !json.Valid(storedAncestry) || !strings.Contains(string(storedReview), `"suggestions"`) || !strings.Contains(string(storedAncestry), "review-model-r1") {
		t.Fatalf("stored full review/ancestry invalid: review=%s ancestry=%s", storedReview, storedAncestry)
	}
	if err := problemRepo.Delete(ctx, problem.ID); err == nil {
		t.Fatal("problem deletion removed immutable review quarantine evidence")
	}
	problem.Status = domain.ProblemStatusPublished
	problem.UpdatedAt = time.Now().UTC()
	if err := problemRepo.Update(ctx, problem); !errors.Is(err, repository.ErrReviewQuarantineProtected) {
		t.Fatalf("full problem update escaped review quarantine: %v", err)
	}
	if err := problemRepo.UpdateStatus(ctx, problem.ID, string(domain.ProblemStatusPublished)); !errors.Is(err, repository.ErrReviewQuarantineProtected) {
		t.Fatalf("status update escaped review quarantine: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE problem_quarantine_records SET review_text='tampered' WHERE operation_key=$1`, operationKey); err == nil {
		t.Fatal("review quarantine evidence allowed an in-place update")
	}
	if _, err := pool.Exec(ctx, `DELETE FROM problem_quarantine_records WHERE operation_key=$1`, operationKey); err == nil {
		t.Fatal("review quarantine evidence allowed deletion")
	}

	collision := record
	collision.WorkflowRunID = "different-run"
	if _, _, err := problemRepo.ApplyReviewQuarantine(ctx, collision); err == nil || !strings.Contains(err.Error(), "different evidence") {
		t.Fatalf("operation-key collision error = %v", err)
	}
}
