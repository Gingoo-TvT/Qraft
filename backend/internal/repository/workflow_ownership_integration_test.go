//go:build integration

package repository_test

import (
	"context"
	"github.com/Gingoo-TvT/Qraft/backend/internal/access"
	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"os"
	"sync"
	"testing"
)

func ownershipDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if os.Getenv("ALGOFORGE_INTEGRATION_DISPOSABLE_DB") != "true" {
		t.Skip("requires disposable migrated database")
	}
	pool, err := pgxpool.New(context.Background(), os.Getenv("DATABASE_URL"))
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return pool
}
func TestWorkflowOwnerReservationCannotBeReassignedIntegration(t *testing.T) {
	pool := ownershipDB(t)
	repo := repository.NewWorkflowOwnershipRepository(pool)
	ctx := context.Background()
	id := "owner-test-" + uuid.NewString()
	t.Cleanup(func() {
		_, err := pool.Exec(ctx, "DELETE FROM workflow_ownership WHERE workflow_id=$1", id)
		require.NoError(t, err)
	})
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, owner := range []string{"a", "b"} {
		wg.Add(1)
		go func(o string) { defer wg.Done(); results <- repo.Reserve(ctx, id, o) }(owner)
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		} else {
			require.ErrorIs(t, err, repository.ErrWorkflowOwnerConflict)
		}
	}
	require.Equal(t, 1, success)
	first, err := repo.Owner(ctx, id)
	require.NoError(t, err)
	require.NotEmpty(t, first)
	require.ErrorIs(t, repo.Reserve(ctx, id, "administrator"), repository.ErrWorkflowOwnerConflict)
	after, err := repo.Owner(ctx, id)
	require.NoError(t, err)
	require.Equal(t, first, after)
}
func TestSetAndQuizSharedListsRespectTrustedOwnersIntegration(t *testing.T) {
	pool := ownershipDB(t)
	ctx := context.Background()
	a := access.WithPrincipal(ctx, access.Principal{UserID: uuid.NewString(), Role: "member"})
	b := access.WithPrincipal(ctx, access.Principal{UserID: uuid.NewString(), Role: "member"})
	admin := access.WithPrincipal(ctx, access.Principal{UserID: uuid.NewString(), Role: "admin"})
	prefix := "owner-fixture-" + uuid.NewString()
	sets := repository.NewProblemSetRepository(pool)
	ids := []uuid.UUID{}
	t.Cleanup(func() {
		for _, id := range ids {
			_, err := pool.Exec(ctx, "DELETE FROM problem_sets WHERE id=$1", id)
			require.NoError(t, err)
		}
	})
	for _, actor := range []context.Context{a, b, admin} {
		set := &domain.ProblemSet{ID: uuid.New(), Code: "OWN-" + uuid.NewString(), Title: prefix, Kind: domain.ProblemSetKindContest, Visibility: domain.ProblemSetVisibilityPublic, CreatedBy: "spoofed", OwnerUserID: "spoofed"}
		require.NoError(t, set.NormalizeProblemSet())
		require.NoError(t, sets.Create(actor, set))
		ids = append(ids, set.ID)
		p, _ := access.FromContext(actor)
		require.Equal(t, p.UserID, set.OwnerUserID)
		require.Equal(t, p.UserID, set.CreatedBy)
		if !p.IsAdmin() {
			require.Equal(t, domain.ProblemSetVisibilityPrivate, set.Visibility)
		}
	}
	for _, actor := range []context.Context{a, b} {
		got, total, err := sets.List(actor, repository.ProblemSetListFilter{Search: prefix})
		require.NoError(t, err)
		require.Equal(t, 2, total)
		p, _ := access.FromContext(actor)
		for _, set := range got {
			require.True(t, set.Visibility == domain.ProblemSetVisibilityPublic || set.OwnerUserID == p.UserID)
		}
	}
	_, total, err := sets.List(admin, repository.ProblemSetListFilter{Search: prefix})
	require.NoError(t, err)
	require.Equal(t, 3, total)
	quizzes := repository.NewQuizRepository(pool)
	subject := prefix
	q := &domain.QuizProblem{ID: uuid.New(), Type: domain.QuizTypeJudge, Statement: "synthetic statement", Answers: []string{"true"}, Explanation: "synthetic", Subject: subject, Difficulty: domain.QuizDifficultyMedium, Visibility: domain.QuizVisibilityPrivate}
	t.Cleanup(func() {
		_, err := pool.Exec(ctx, "DELETE FROM quiz_problems WHERE id=$1", q.ID)
		require.NoError(t, err)
	})
	workflowID := "quiz-owner-" + uuid.NewString()
	require.NoError(t, quizzes.CreateGeneratedBatch(ctx, workflowID+"/store-quiz/v1", q.Type, []*domain.QuizProblem{q}, nil))
	stored, err := quizzes.WorkflowID(ctx, q.ID)
	require.NoError(t, err)
	require.Equal(t, workflowID, stored)
	_, total, err = quizzes.List(a, repository.QuizListFilter{Subject: &subject}, 1, 20)
	require.NoError(t, err)
	require.Zero(t, total)
	_, total, err = quizzes.List(admin, repository.QuizListFilter{Subject: &subject}, 1, 20)
	require.NoError(t, err)
	require.Equal(t, 1, total)
	q.Visibility = domain.QuizVisibilityPublic
	require.NoError(t, quizzes.Update(admin, q))
	_, total, err = quizzes.List(b, repository.QuizListFilter{Subject: &subject}, 1, 20)
	require.NoError(t, err)
	require.Equal(t, 1, total)
}
