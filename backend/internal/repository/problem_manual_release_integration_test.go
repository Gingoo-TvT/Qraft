//go:build integration

package repository_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/access"
	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
	"github.com/Gingoo-TvT/Qraft/backend/internal/workflow/activities"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

func manualReleaseFixture(t *testing.T) (*pgxpool.Pool, *repository.ProblemRepository, *domain.Problem) {
	t.Helper()
	pool, _, _ := setGenerationDB(t)
	repo := repository.NewProblemRepository(pool)
	p := &domain.Problem{ID: uuid.New(), Title: "Manual review synthetic " + uuid.NewString(), Statement: "Read an integer and print it.", Level: domain.LevelAlgorithm, Difficulty: 1000, Tags: []string{}, TimeLimit: 1000, MemoryLimit: 128, Status: domain.ProblemStatusQuarantined, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(), MetadataJSON: json.RawMessage(`{"synthetic":true}`)}
	require.NoError(t, repo.Create(context.Background(), p))
	p, err := repo.GetByID(context.Background(), p.ID)
	require.NoError(t, err)
	return pool, repo, p
}

func approveManualFixture(t *testing.T, repo *repository.ProblemRepository, id uuid.UUID) repository.PublicReleaseApprovalReport {
	t.Helper()
	p, err := repo.GetByID(context.Background(), id)
	require.NoError(t, err)
	report, err := repo.ApprovePublicReleaseWithQualityOverride(context.Background(), id, "synthetic-admin", repository.ManualReleaseOptions{ExpectedUpdatedAt: p.UpdatedAt, Note: "Reviewed the current synthetic statement."})
	require.NoError(t, err)
	require.Equal(t, domain.ProblemStatusPublished, report.ReleaseStatus)
	require.NotNil(t, report.ManualReview)
	return report
}

func syntheticReviewRecord(t *testing.T, id uuid.UUID) repository.ReviewQuarantineRecord {
	t.Helper()
	review := activities.ReviewResult{Approved: false, Issues: []string{"synthetic review disagreement"}, Suggestions: []string{"human review"}, Confidence: 0.8, EstimatedDifficulty: 1000, FullText: `{"approved":false}`, SourceArtifacts: []*activities.ArtifactRef{{ModelRevision: "synthetic-review", SHA256: strings.Repeat("a", 64)}}}
	raw, hash, err := activities.CanonicalReviewResultJSON(review)
	require.NoError(t, err)
	ancestry, err := json.Marshal(review.SourceArtifacts)
	require.NoError(t, err)
	return repository.ReviewQuarantineRecord{ProblemID: id, OperationKey: "manual-review-test/" + uuid.NewString(), Reason: "automated LLM review rejected candidate", WorkflowID: "synthetic-workflow", WorkflowRunID: uuid.NewString(), ReviewGateChangeID: "synthetic-review-v2", ReviewGateVersion: 2, ReviewResultSHA256: hash, ReviewResultJSON: raw, ReviewText: review.FullText, SourceAncestryJSON: ancestry}
}

func TestProblemManualReleaseMissingEditorialAndReevaluationIntegration(t *testing.T) {
	pool, repo, p := manualReleaseFixture(t)
	ctx := context.Background()
	_, err := repo.ApprovePublicRelease(ctx, p.ID, "automatic-setting")
	require.ErrorIs(t, err, repository.ErrPublicReleaseApprovalConflict)
	require.ErrorContains(t, err, "missing detailed solution")
	report := approveManualFixture(t, repo, p.ID)
	require.Contains(t, report.ManualReview.OverriddenChecks, "missing detailed solution")
	current, err := repo.GetByID(ctx, p.ID)
	require.NoError(t, err)
	require.Empty(t, current.DetailedSolution, "Approval must not invent editorial content")
	var allowed, manual bool
	require.NoError(t, pool.QueryRow(ctx, `SELECT provenance_artifact_use_allowed($1,'public_release'),problem_manual_release_approved($2::uuid)`, report.ArtifactID, p.ID).Scan(&allowed, &manual))
	require.True(t, allowed)
	require.True(t, manual)
	status, reason, err := repo.ApplyPublicReleaseGate(ctx, p.ID, "synthetic-reevaluate/"+uuid.NewString())
	require.NoError(t, err)
	require.Equal(t, domain.ProblemStatusPublished, status)
	require.Empty(t, reason)
	second := approveManualFixture(t, repo, p.ID)
	require.Equal(t, report.ManualReview.ApprovalID, second.ManualReview.ApprovalID)
	var count int
	require.NoError(t, pool.QueryRow(ctx, `SELECT COUNT(*) FROM problem_manual_release_approvals WHERE problem_id=$1`, p.ID).Scan(&count))
	require.Equal(t, 1, count)
	_, err = repo.ApprovePublicReleaseWithQualityOverride(ctx, p.ID, "stale-admin", repository.ManualReleaseOptions{ExpectedUpdatedAt: p.UpdatedAt})
	require.ErrorIs(t, err, repository.ErrPublicReleaseApprovalConflict)
	_, err = pool.Exec(ctx, `UPDATE problem_manual_release_approvals SET approved_by='forged' WHERE problem_id=$1`, p.ID)
	require.Error(t, err)
	_, err = pool.Exec(ctx, `DELETE FROM problem_manual_release_approvals WHERE problem_id=$1`, p.ID)
	require.Error(t, err)
}

func TestProblemManualReleaseReviewEvidenceRemainsAndMemberCanFindItIntegration(t *testing.T) {
	pool, repo, p := manualReleaseFixture(t)
	ctx := context.Background()
	record := syntheticReviewRecord(t, p.ID)
	_, _, err := repo.ApplyReviewQuarantine(ctx, record)
	require.NoError(t, err)
	report := approveManualFixture(t, repo, p.ID)
	require.Contains(t, report.ManualReview.OverriddenChecks, record.Reason)
	var original string
	require.NoError(t, pool.QueryRow(ctx, `SELECT review_result_sha256 FROM problem_quarantine_records WHERE operation_key=$1`, record.OperationKey).Scan(&original))
	require.Equal(t, record.ReviewResultSHA256, original)
	member := access.WithPrincipal(ctx, access.Principal{UserID: "synthetic-member", Role: "member"})
	list, total, err := repo.List(member, repository.ProblemFilter{Search: p.Title, ExcludeQuarantined: true, ExcludeRejected: true, Limit: 20})
	require.NoError(t, err)
	require.Equal(t, 1, total)
	require.Equal(t, p.ID, list[0].ID)
	matches, err := repo.FindByTitle(ctx, p.Title, 10)
	require.NoError(t, err)
	require.Len(t, matches, 1)
	require.Error(t, repo.Delete(ctx, p.ID))
	_, err = pool.Exec(ctx, `DELETE FROM problem_quarantine_records WHERE problem_id=$1`, p.ID)
	require.Error(t, err)
	next := syntheticReviewRecord(t, p.ID)
	_, _, err = repo.ApplyReviewQuarantine(ctx, next)
	require.NoError(t, err)
	current, err := repo.GetByID(ctx, p.ID)
	require.NoError(t, err)
	require.Equal(t, domain.ProblemStatusQuarantined, current.Status)
	list, total, err = repo.List(member, repository.ProblemFilter{Search: p.Title, ExcludeQuarantined: true, Limit: 20})
	require.NoError(t, err)
	require.Zero(t, total)
	require.Empty(t, list)
}

func TestProblemManualReleaseChangesInvalidateApprovalIntegration(t *testing.T) {
	for _, kind := range []string{"statement", "solution", "testcase"} {
		t.Run(kind, func(t *testing.T) {
			pool, repo, p := manualReleaseFixture(t)
			ctx := context.Background()
			approveManualFixture(t, repo, p.ID)
			var err error
			switch kind {
			case "statement":
				current, e := repo.GetByID(ctx, p.ID)
				require.NoError(t, e)
				value := "Different synthetic statement."
				_, err = repo.Edit(ctx, repository.ProblemEditPatch{ProblemID: p.ID, ExpectedUpdatedAt: current.UpdatedAt, Actor: "synthetic-editor", Statement: &value})
			case "solution":
				_, err = pool.Exec(ctx, `INSERT INTO solutions(problem_id,solution_type,language,source_code,compile_status) VALUES($1,'main','cpp','int main(){}','success')`, p.ID)
			case "testcase":
				_, err = pool.Exec(ctx, `INSERT INTO testcases(problem_id,test_index,input_path,output_path) VALUES($1,1,'synthetic/in','synthetic/out')`, p.ID)
			}
			require.NoError(t, err)
			current, err := repo.GetByID(ctx, p.ID)
			require.NoError(t, err)
			require.Equal(t, domain.ProblemStatusQuarantined, current.Status)
			var valid bool
			require.NoError(t, pool.QueryRow(ctx, `SELECT problem_manual_release_approved($1::uuid)`, p.ID).Scan(&valid))
			require.False(t, valid)
		})
	}
}

func TestProblemManualReleaseCannotOverrideRemovedRightsIntegration(t *testing.T) {
	pool, repo, p := manualReleaseFixture(t)
	ctx := context.Background()
	_, err := pool.Exec(ctx, `UPDATE provenance_artifacts a SET takedown_status='removed' FROM provenance_artifact_bindings b WHERE b.artifact_id=a.artifact_id AND b.subject_type='problem' AND b.subject_id=$1 AND b.is_current`, p.ID.String())
	require.NoError(t, err)
	_, err = repo.ApprovePublicReleaseWithQualityOverride(ctx, p.ID, "synthetic-admin", repository.ManualReleaseOptions{ExpectedUpdatedAt: p.UpdatedAt})
	require.ErrorIs(t, err, repository.ErrPublicReleaseApprovalConflict)
	var count int
	require.NoError(t, pool.QueryRow(ctx, `SELECT COUNT(*) FROM problem_manual_release_approvals WHERE problem_id=$1`, p.ID).Scan(&count))
	require.Zero(t, count)
	current, err := repo.GetByID(ctx, p.ID)
	require.NoError(t, err)
	require.Equal(t, domain.ProblemStatusQuarantined, current.Status)
}
