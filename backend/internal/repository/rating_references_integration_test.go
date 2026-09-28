//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/rating"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestReferencePoolUsesSharedSourcesAndCurrentHumanDecisionsIntegration(t *testing.T) {
	pool, r, id := ratingTestDB(t)
	ctx := context.Background()
	source := &rating.SourceReference{Status: "verified", StatementSHA256: rating.Digest([]byte("Compute a+b. 0<=a,b<=100.")), Difficulty: &domain.SourceDifficulty{Platform: "codeforces", Scale: "codeforces_rating", Value: "800", Label: "Codeforces 800", SourceURL: "https://codeforces.com/problemset/problem/900001/A", FetchedAt: time.Now().UTC()}}
	raw, _ := json.Marshal(map[string]any{"source_reference": source, "method": "external_native_v1", "difficulty": 800})
	require.NoError(t, NewProblemRepository(pool).SetImportedDifficulty(ctx, id, "Compute a+b. 0<=a,b<=100.", 800, raw))
	list, err := r.ListReferenceAnchors(ctx)
	require.NoError(t, err)
	for _, a := range list {
		require.NotEqual(t, id, a.ID, "private drafts must not become shared teaching material")
	}
	_, err = pool.Exec(ctx, "UPDATE problems SET status='published' WHERE id=$1", id)
	require.NoError(t, err)
	find := func() rating.Anchor {
		list, e := r.ListReferenceAnchors(ctx)
		require.NoError(t, e)
		for _, a := range list {
			if a.ID == id {
				return a
			}
		}
		return rating.Anchor{}
	}
	require.Equal(t, 800, find().Rating)
	require.Equal(t, "external_source", find().Basis)
	assessment, err := r.CreateAssessment(ctx, id, "synthetic-admin")
	require.NoError(t, err)
	value := 1000
	require.NoError(t, r.UpdateAssessment(ctx, assessment.ID, "completed", "complete", &rating.Report{SnapshotHash: assessment.Subject.Hash, Validity: "tested_candidates", Estimate: rating.ReferenceEstimate{Representative: &value}}, ""))
	_, err = r.Decide(ctx, id, rating.DecisionInput{SubjectHash: assessment.Subject.Hash, FeedbackHash: rating.FeedbackSnapshotHash(nil), AssessmentID: &assessment.ID, Action: "accept", Reason: "Synthetic reviewed correction"}, "synthetic-admin")
	require.NoError(t, err)
	require.Equal(t, 1000, find().Rating)
	require.Equal(t, "admin_decision", find().Basis)
	// Re-estimating an import cannot overwrite human decisions or feedback.
	invite := testInvitation(t, r, id, "reference-test-"+uuid.NewString())
	_, err = r.SubmitFeedback(ctx, invite.Token, testRatingFeedback())
	require.NoError(t, err)
	require.NoError(t, NewProblemRepository(pool).SetImportedDifficulty(ctx, id, "Compute a+b. 0<=a,b<=100.", 800, raw))
	require.Equal(t, 1000, find().Rating)
	w, err := r.Workspace(ctx, id)
	require.NoError(t, err)
	require.Equal(t, 1000, w.Official.Rating)
	require.Len(t, w.Feedback, 1)
	_, err = pool.Exec(ctx, "UPDATE problems SET statement='Different task' WHERE id=$1", id)
	require.NoError(t, err)
	require.Equal(t, uuid.Nil, find().ID, "stale decision and source snapshot must both leave the pool")
}
