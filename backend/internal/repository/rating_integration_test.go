//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/rating"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

func ratingTestDB(t *testing.T) (*pgxpool.Pool, *RatingRepository, uuid.UUID) {
	t.Helper()
	if os.Getenv("ALGOFORGE_INTEGRATION_DISPOSABLE_DB") != "true" {
		t.Skip("requires explicitly disposable migrated DATABASE_URL")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	id := uuid.New()
	_, err = pool.Exec(ctx, "INSERT INTO problems(id,title,statement,level,difficulty,tags) VALUES($1,'Synthetic rating fixture','Compute a+b. 0<=a,b<=100.','algorithm',1200,ARRAY['math'])", id)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), "DELETE FROM problems WHERE id=$1", id) })
	return pool, NewRatingRepository(pool), id
}
func testInvitation(t *testing.T, r *RatingRepository, id uuid.UUID, key string) rating.IssuedInvitation {
	t.Helper()
	i, err := r.IssueInvitation(context.Background(), id, rating.InvitationRequest{ReviewerKey: key, WindowMinutes: 60, Context: "practice", ExpiresInDays: 1}, "test-admin")
	require.NoError(t, err)
	return i
}
func testRatingFeedback() rating.FeedbackInput {
	return rating.FeedbackInput{Outcome: "solved", IndependentMinutes: 10, ElapsedMinutes: 10, ResultSource: "self_report", FirstRoute: "enumeration", FinalRoute: "addition"}
}
func TestRatingSnapshotContentAndSemanticInvalidationIntegration(t *testing.T) {
	pool, r, id := ratingTestDB(t)
	ctx := context.Background()
	artifact := map[string][]byte{"fixture.in": []byte("1 2"), "fixture.out": []byte("3")}
	r.SetArtifactReader(func(_ context.Context, path string) ([]byte, error) { return artifact[path], nil })
	_, err := pool.Exec(ctx, "INSERT INTO testcases(id,problem_id,test_index,is_sample,input_path,output_path) VALUES($1,$2,0,true,'fixture.in','fixture.out')", uuid.New(), id)
	require.NoError(t, err)
	first, err := r.CaptureSubject(ctx, id)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, "UPDATE problems SET difficulty=1300,tags=ARRAY['greedy'],detailed_solution='changed editorial' WHERE id=$1", id)
	require.NoError(t, err)
	same, err := r.CaptureSubject(ctx, id)
	require.NoError(t, err)
	require.Equal(t, first.Hash, same.Hash, "target/editorial updates do not silently mix task versions")
	artifact["fixture.out"] = []byte("4")
	changed, err := r.CaptureSubject(ctx, id)
	require.NoError(t, err)
	require.NotEqual(t, first.Hash, changed.Hash, "same object path with changed bytes must invalidate")
	_, err = pool.Exec(ctx, "UPDATE problems SET time_limit=time_limit+1 WHERE id=$1", id)
	require.NoError(t, err)
	changedLimit, err := r.CaptureSubject(ctx, id)
	require.NoError(t, err)
	require.NotEqual(t, changed.Hash, changedLimit.Hash)
	_, err = pool.Exec(ctx, "INSERT INTO solutions(id,problem_id,solution_type,language,source_code) VALUES($1,$2,'checker','cpp','checker fixture')", uuid.New(), id)
	require.NoError(t, err)
	checker, err := r.CaptureSubject(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "unsupported_checker", checker.JudgeMode)
	require.NotEqual(t, changedLimit.Hash, checker.Hash)
}
func TestRatingInvitationDedupRevisionExpiryAndBlindPayloadIntegration(t *testing.T) {
	pool, r, id := ratingTestDB(t)
	ctx := context.Background()
	key := "fixture-" + uuid.NewString()
	one := testInvitation(t, r, id, key)
	two := testInvitation(t, r, id, key)
	require.Equal(t, one.Invitation.ReviewerID, two.Invitation.ReviewerID)
	require.NotEqual(t, one.Token, two.Token)
	var storedHash string
	require.NoError(t, pool.QueryRow(ctx, "SELECT token_hash FROM rating_invitations WHERE id=$1", one.Invitation.ID).Scan(&storedHash))
	require.NotEqual(t, one.Token, storedHash)
	require.Len(t, storedHash, 64)
	f, err := r.SubmitFeedback(ctx, one.Token, testRatingFeedback())
	require.NoError(t, err)
	edited := testRatingFeedback()
	edited.Notes = "Additional actual route detail"
	f2, err := r.SubmitFeedback(ctx, two.Token, edited)
	require.NoError(t, err)
	require.Equal(t, f.ID, f2.ID)
	require.Equal(t, 2, f2.Revision)
	fs, err := r.ListFeedback(ctx, id, f.SubjectHash)
	require.NoError(t, err)
	require.Len(t, fs, 1)
	var revisions int
	require.NoError(t, pool.QueryRow(ctx, "SELECT count(*) FROM rating_feedback_revisions WHERE feedback_id=$1", f.ID).Scan(&revisions))
	require.Equal(t, 2, revisions)
	task, err := r.ReviewTask(ctx, one.Token)
	require.NoError(t, err)
	raw, err := json.Marshal(task)
	require.NoError(t, err)
	for _, secret := range []string{"target_difficulty", "official_solution", "expected_tags", "reviewer_id", "reviewer_key"} {
		require.NotContains(t, string(raw), secret)
	}
	require.NotNil(t, task.Feedback)
	_, err = r.IssueInvitation(ctx, id, rating.InvitationRequest{ReviewerKey: key, WindowMinutes: 30, Context: "practice", ExpiresInDays: 1}, "admin")
	require.ErrorIs(t, err, rating.ErrInvalid)
	require.NoError(t, r.RevokeInvitation(ctx, id, one.Invitation.ID))
	_, err = r.SubmitFeedback(ctx, one.Token, testRatingFeedback())
	require.ErrorIs(t, err, rating.ErrUnauthorized)
	_, err = pool.Exec(ctx, "UPDATE rating_invitations SET expires_at=now()-interval '1 second' WHERE id=$1", two.Invitation.ID)
	require.NoError(t, err)
	_, err = r.ReviewTask(ctx, two.Token)
	require.ErrorIs(t, err, rating.ErrUnauthorized)
}
func TestRatingConcurrentFeedbackAndDecisionSnapshotIntegration(t *testing.T) {
	pool, r, id := ratingTestDB(t)
	ctx := context.Background()
	invite := testInvitation(t, r, id, "fixture-"+uuid.NewString())
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := r.SubmitFeedback(ctx, invite.Token, testRatingFeedback()); errs <- e }()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		require.NoError(t, e)
	}
	fs, err := r.ListFeedback(ctx, id, invite.Invitation.SubjectHash)
	require.NoError(t, err)
	require.Len(t, fs, 1)
	require.Equal(t, 1, fs[0].Revision, "identical concurrent submissions are idempotent")
	a, err := r.CreateAssessment(ctx, id, "admin")
	require.NoError(t, err)
	_, err = r.CreateAssessment(ctx, id, "admin")
	require.ErrorIs(t, err, rating.ErrConflict)
	value := 1400
	require.NoError(t, r.UpdateAssessment(ctx, a.ID, "completed", "complete", &rating.Report{SnapshotHash: a.Subject.Hash, Validity: "tested_candidates", Estimate: rating.ReferenceEstimate{Representative: &value}}, ""))
	_, err = r.SaveCalibration(ctx, rating.BuildCalibration(id, a.Subject.Hash, fs))
	require.NoError(t, err)
	c, err := r.SaveCalibration(ctx, rating.BuildCalibration(id, a.Subject.Hash, fs))
	require.NoError(t, err)
	c2, err := r.SaveCalibration(ctx, rating.BuildCalibration(id, a.Subject.Hash, fs))
	require.NoError(t, err)
	require.Equal(t, c.ID, c2.ID, "same snapshot is not new evidence")
	input := rating.DecisionInput{SubjectHash: a.Subject.Hash, FeedbackHash: rating.FeedbackSnapshotHash(fs), AssessmentID: &a.ID, Action: "accept", Reason: "Synthetic administrator review"}
	decisions := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := r.Decide(ctx, id, input, "admin"); decisions <- e }()
	}
	wg.Wait()
	close(decisions)
	success, conflicts := 0, 0
	for e := range decisions {
		if e == nil {
			success++
		} else if errors.Is(e, rating.ErrConflict) {
			conflicts++
		} else {
			require.NoError(t, e)
		}
	}
	require.Equal(t, 1, success)
	require.Equal(t, 1, conflicts)
	w, err := r.Workspace(ctx, id)
	require.NoError(t, err)
	require.NotNil(t, w.Official)
	require.Equal(t, value, w.Official.Rating)
	current, err := r.CurrentOfficial(ctx, id)
	require.NoError(t, err)
	require.NotNil(t, current)
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	verified, err := r.VerifyOfficialTx(ctx, tx, id, w.Subject.Hash, current.DecisionID)
	require.NoError(t, err)
	require.Equal(t, value, verified.Rating)
	require.NoError(t, tx.Rollback(ctx))
	tx, err = pool.Begin(ctx)
	require.NoError(t, err)
	_, err = r.VerifyOfficialTx(ctx, tx, id, w.Subject.Hash, uuid.New())
	require.ErrorIs(t, err, rating.ErrConflict)
	require.NoError(t, tx.Rollback(ctx))

	var target int
	require.NoError(t, pool.QueryRow(ctx, "SELECT difficulty FROM problems WHERE id=$1", id).Scan(&target))
	require.Equal(t, 1200, target)
	input.ExpectedDecisionID = &w.Official.DecisionID
	edited := testRatingFeedback()
	edited.Blockers = "New observation"
	_, err = r.SubmitFeedback(ctx, invite.Token, edited)
	require.NoError(t, err)
	_, err = r.Decide(ctx, id, input, "admin")
	require.ErrorIs(t, err, rating.ErrConflict, "new revision invalidates a previously loaded decision")
	_, err = pool.Exec(ctx, "UPDATE problems SET statement=statement||' New constraint.' WHERE id=$1", id)
	require.NoError(t, err)
	w, err = r.Workspace(ctx, id)
	require.NoError(t, err)
	require.True(t, w.Official.Stale)
	current, err = r.CurrentOfficial(ctx, id)
	require.NoError(t, err)
	require.Nil(t, current)

	require.Empty(t, w.Feedback, "new task version does not merge old human evidence")
}
func TestRatingAssessmentCancellationAndUnreviewedAnchorIntegration(t *testing.T) {
	_, r, id := ratingTestDB(t)
	ctx := context.Background()
	a, err := r.CreateAssessment(ctx, id, "admin")
	require.NoError(t, err)
	require.NoError(t, r.UpdateAssessment(ctx, a.ID, "cancelled", "cancelled", nil, ""))
	require.ErrorIs(t, r.UpdateAssessment(ctx, a.ID, "completed", "complete", &rating.Report{SnapshotHash: a.Subject.Hash}, ""), rating.ErrConflict)
	loaded, err := r.GetAssessment(ctx, a.ID)
	require.NoError(t, err)
	require.Equal(t, "cancelled", loaded.Status)
	_, err = r.CreateAnchor(ctx, rating.Anchor{Title: "AI guessed", Rating: 1500}, "admin")
	require.ErrorIs(t, err, rating.ErrInvalid)
	source := rating.Anchor{Title: "Synthetic reviewed anchor " + uuid.NewString(), Rating: 1500, SourceURL: "https://example.org/fixture", RatingSource: "Synthetic source", RetrievedAt: time.Now().Add(-time.Hour), StatementSummary: "synthetic problem", SolutionSummary: "synthetic solution", Population: "fixture only", Family: "fixture", SourceConfirmed: true}
	saved, err := r.CreateAnchor(ctx, source, "admin")
	require.NoError(t, err)
	require.Equal(t, "admin", saved.ReviewedBy)
	require.NotEqual(t, uuid.Nil, saved.ID)
	require.NotContains(t, strings.ToLower(string(ratingJSON(saved))), "api_key")
}

func TestRatingCaptureDigesterMatchesStreamingContentIdentityIntegration(t *testing.T) {
	pool, r, id := ratingTestDB(t)
	ctx := context.Background()
	_, err := pool.Exec(ctx, "INSERT INTO testcases(id,problem_id,test_index,is_sample,input_path,output_path) VALUES($1,$2,0,false,'digest.in','digest.out')", uuid.New(), id)
	require.NoError(t, err)
	reads := 0
	digests := 0
	data := map[string][]byte{"digest.in": []byte("1 2"), "digest.out": []byte("3")}
	r.SetArtifactReader(func(_ context.Context, path string) ([]byte, error) { reads++; return data[path], nil })
	original, err := r.CaptureSubject(ctx, id)
	require.NoError(t, err)
	require.Equal(t, 2, reads)
	r.SetArtifactDigester(func(_ context.Context, path string) (string, error) { digests++; return ratingHash(data[path]), nil })
	same, err := r.CaptureSubject(ctx, id)
	require.NoError(t, err)
	require.Equal(t, original.Hash, same.Hash)
	require.Equal(t, 2, reads, "non-samples no longer use the materializing reader")
	require.Equal(t, 2, digests)
	data["digest.out"] = []byte("4")
	changed, err := r.CaptureSubject(ctx, id)
	require.NoError(t, err)
	require.NotEqual(t, same.Hash, changed.Hash)
	r.SetArtifactDigester(func(context.Context, string) (string, error) { return "not-a-content-digest", nil })
	_, err = r.CaptureSubject(ctx, id)
	require.ErrorContains(t, err, "invalid rating input digest")
}
