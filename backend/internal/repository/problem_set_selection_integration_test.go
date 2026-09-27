//go:build integration

package repository_test

import (
	"context"
	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"sync"
	"testing"
)

func TestManualSelectionAtomicAndConcurrentAppendIntegration(t *testing.T) {
	pool, repo, set := setGenerationDB(t)
	ctx := context.Background()
	ids := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
	for _, id := range ids {
		_, err := pool.Exec(ctx, `INSERT INTO quiz_problems(id,code,type,subject,title,statement,answers,explanation,difficulty,visibility) VALUES($1::uuid,($1::uuid)::text,'judge','data_structure_algorithm','Selection fixture','statement',$2,'reason','medium','private')`, id, []string{"对"})
		require.NoError(t, err)
	}
	missing := uuid.New()
	require.Error(t, repo.AddItems(ctx, set.ID, []domain.ProblemSetItem{{QuizID: &ids[0], Score: 5}, {QuizID: &missing, Score: 5}}))
	got, err := repo.GetByID(ctx, set.ID)
	require.NoError(t, err)
	require.Empty(t, got.Items)
	require.NoError(t, repo.AddItems(ctx, set.ID, []domain.ProblemSetItem{{QuizID: &ids[1], Score: 7}, {QuizID: &ids[0], Score: 3}, {QuizID: &ids[1], Score: 7}}))
	got, err = repo.GetByID(ctx, set.ID)
	require.NoError(t, err)
	require.Len(t, got.Items, 2)
	require.Equal(t, 10, got.TotalScore)
	require.Equal(t, ids[1], *got.Items[0].QuizID)
	require.Equal(t, 1, got.Items[0].Position)
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- repo.AddItems(ctx, set.ID, []domain.ProblemSetItem{{QuizID: &ids[2], Score: 6}})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	got, err = repo.GetByID(ctx, set.ID)
	require.NoError(t, err)
	require.Len(t, got.Items, 3)
	require.Equal(t, 16, got.TotalScore)
	require.Equal(t, 3, got.Items[2].Position)
	state := &domain.ProblemSetGenerationState{ID: uuid.NewString(), Status: "queued"}
	_, err = repo.ReserveGeneration(ctx, set.ID, got.UpdatedAt, state)
	require.NoError(t, err)
	require.ErrorIs(t, repo.AddItems(ctx, set.ID, []domain.ProblemSetItem{{QuizID: &ids[0]}}), repository.ErrProblemSetGenerationActive)
}

func TestManualSelectionCreateRollsBackWholeSetIntegration(t *testing.T) {
	pool, repo, set := setGenerationDB(t)
	ctx := context.Background()
	missing := uuid.New()
	set.ID = uuid.New()
	set.Code = "ROLLBACK-" + uuid.NewString()
	set.Items = []domain.ProblemSetItem{{QuizID: &missing}}
	require.Error(t, repo.Create(ctx, set))
	var count int
	require.NoError(t, pool.QueryRow(ctx, `SELECT COUNT(*) FROM problem_sets WHERE id=$1`, set.ID).Scan(&count))
	require.Zero(t, count)
}

func TestManualSelectionLibrarySearchAndPaginationIntegration(t *testing.T) {
	pool, _, _ := setGenerationDB(t)
	ctx := context.Background()
	repo := repository.NewProblemRepository(pool)
	prefix := "SelectionSearch-" + uuid.NewString()
	titles := []string{prefix + " B2009 addition", prefix + " B2033 product", prefix + " 100% literal"}
	for _, title := range titles {
		p := &domain.Problem{ID: uuid.New(), Title: title, Statement: "synthetic", Level: domain.LevelAlgorithm, Difficulty: 800, Status: domain.ProblemStatus("draft"), Tags: []string{}}
		require.NoError(t, repo.Create(ctx, p))
	}
	rows, total, err := repo.List(ctx, repository.ProblemFilter{Search: prefix + " B2009", Limit: 10})
	require.NoError(t, err)
	require.Equal(t, 1, total)
	require.Equal(t, titles[0], rows[0].Title)
	rows, total, err = repo.List(ctx, repository.ProblemFilter{Search: prefix, OrderBy: "title", OrderDirection: "asc", Limit: 1, Offset: 1})
	require.NoError(t, err)
	require.Equal(t, 3, total)
	require.Equal(t, titles[0], rows[0].Title)
	rows, total, err = repo.List(ctx, repository.ProblemFilter{Search: prefix + " 100%", Limit: 10})
	require.NoError(t, err)
	require.Equal(t, 1, total)
	require.Equal(t, titles[2], rows[0].Title)
	_, total, err = repo.List(ctx, repository.ProblemFilter{Search: prefix + "_%", OrderBy: "title; DROP TABLE problems", Limit: 10})
	require.NoError(t, err)
	require.Zero(t, total)
}
