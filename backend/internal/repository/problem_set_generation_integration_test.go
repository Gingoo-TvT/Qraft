//go:build integration

package repository_test

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

func setGenerationDB(t *testing.T) (*pgxpool.Pool, *repository.ProblemSetRepository, *domain.ProblemSet) {
	t.Helper()
	if os.Getenv("ALGOFORGE_INTEGRATION_DISPOSABLE_DB") != "true" {
		t.Skip("requires an explicitly disposable migrated DATABASE_URL")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	repo := repository.NewProblemSetRepository(pool)
	s := &domain.ProblemSet{Kind: domain.ProblemSetKindContest, Visibility: domain.ProblemSetVisibilityPrivate, Status: domain.ProblemSetStatusDraft, CreatedBy: "test", ID: uuid.New(), Title: "Set orchestration fixture", Code: "TEST-" + uuid.NewString(), DesiredItemCount: 2, GenerationConfig: &domain.ProblemSetGenerationConfig{Mode: "mixed", Distribution: []domain.ProblemSetTypeQuota{{Type: domain.QuizTypeJudge, Count: 2, Score: 2}}}}
	require.NoError(t, s.NormalizeProblemSet())
	require.NoError(t, repo.Create(ctx, s))
	read, err := repo.GetByID(ctx, s.ID)
	require.NoError(t, err)
	require.Equal(t, s.GenerationConfig, read.GenerationConfig)
	return pool, repo, read
}

func TestProblemSetGenerationAtomicReservationAndCompletionIntegration(t *testing.T) {
	pool, repo, s := setGenerationDB(t)
	ctx := context.Background()
	slots := []domain.ProblemSetGenerationSlot{}
	for i := 1; i <= 2; i++ {
		qid := uuid.New()
		_, err := pool.Exec(ctx, `INSERT INTO quiz_problems(id,code,type,subject,title,statement,answers,explanation,difficulty,visibility) VALUES($1::uuid,($1::uuid)::text,'judge','data_structure_algorithm','fixture','statement',$2,'reason','medium','private')`, qid, []string{"对"})
		require.NoError(t, err)
		slots = append(slots, domain.ProblemSetGenerationSlot{Position: i, Type: domain.QuizTypeJudge, Score: 2, Status: "generated", QuizID: &qid, ChildID: uuid.NewString(), Fingerprint: uuid.NewString()})
	}
	var wg sync.WaitGroup
	reserved := make(chan *domain.ProblemSetGenerationState, 8)
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			state, err := repo.ReserveGeneration(ctx, s.ID, s.UpdatedAt, &domain.ProblemSetGenerationState{ID: uuid.NewString(), Status: "queued", Slots: slots, StartedAt: time.Now()})
			reserved <- state
			errs <- err
		}()
	}
	wg.Wait()
	close(reserved)
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	var run string
	for state := range reserved {
		if run == "" {
			run = state.ID
		}
		require.Equal(t, run, state.ID)
	}
	ref := domain.ProblemSetGenerationRef{SetID: s.ID, RunID: run}
	require.ErrorIs(t, repo.Update(ctx, s), repository.ErrProblemSetGenerationActive)
	require.ErrorIs(t, repo.Delete(ctx, s.ID), repository.ErrProblemSetGenerationActive)
	require.ErrorIs(t, repo.AddItem(ctx, &domain.ProblemSetItem{SetID: s.ID, QuizID: slots[0].QuizID}), repository.ErrProblemSetGenerationActive)
	errs = make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(index int) { defer wg.Done(); errs <- repo.CompleteGenerationSlot(ctx, ref, slots[index%2]) }(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	got, err := repo.GetByID(ctx, s.ID)
	require.NoError(t, err)
	require.Len(t, got.Items, 2)
	require.Equal(t, 4, got.TotalScore)
	for _, slot := range got.Generation.Slots {
		require.Equal(t, "succeeded", slot.Status)
	}
	require.ErrorIs(t, repo.CompleteGenerationSlot(ctx, domain.ProblemSetGenerationRef{SetID: s.ID, RunID: "stale"}, slots[0]), repository.ErrProblemSetGenerationChanged)
}

func TestProblemSetGenerationManualMutationInvalidatesStaleStartIntegration(t *testing.T) {
	pool, repo, s := setGenerationDB(t)
	ctx := context.Background()
	qid := uuid.New()
	_, err := pool.Exec(ctx, `INSERT INTO quiz_problems(id,code,type,subject,title,statement,answers,explanation,difficulty,visibility) VALUES($1::uuid,($1::uuid)::text,'judge','data_structure_algorithm','fixture','statement',$2,'reason','medium','private')`, qid, []string{"对"})
	require.NoError(t, err)
	require.NoError(t, repo.AddItem(ctx, &domain.ProblemSetItem{SetID: s.ID, QuizID: &qid, Score: 2}))
	next := &domain.ProblemSetGenerationState{ID: uuid.NewString(), Status: "queued"}
	_, err = repo.ReserveGeneration(ctx, s.ID, s.UpdatedAt, next)
	require.ErrorIs(t, err, repository.ErrProblemSetGenerationChanged)
	require.ErrorIs(t, repo.Update(ctx, s), repository.ErrProblemSetGenerationChanged)
	current, err := repo.GetByID(ctx, s.ID)
	require.NoError(t, err)
	require.Equal(t, 2, current.TotalScore)
	require.NoError(t, repo.RemoveItem(ctx, s.ID, current.Items[0].ID))
	_, err = repo.ReserveGeneration(ctx, s.ID, current.UpdatedAt, next)
	require.ErrorIs(t, err, repository.ErrProblemSetGenerationChanged)
}

func TestProblemSetGenerationRejectsDuplicateAndStoppedSlotIntegration(t *testing.T) {
	pool, repo, s := setGenerationDB(t)
	ctx := context.Background()
	first, second := uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{first, second} {
		_, err := pool.Exec(ctx, `INSERT INTO quiz_problems(id,code,type,subject,title,statement,answers,explanation,difficulty,visibility) VALUES($1::uuid,($1::uuid)::text,'judge','data_structure_algorithm','fixture','statement',$2,'reason','medium','private')`, id, []string{"对"})
		require.NoError(t, err)
	}
	slots := []domain.ProblemSetGenerationSlot{
		{Position: 1, Type: domain.QuizTypeJudge, Score: 2, Status: "generated", QuizID: &first, ChildID: "first", Fingerprint: "same-content"},
		{Position: 2, Type: domain.QuizTypeJudge, Score: 2, Status: "generated", QuizID: &second, ChildID: "second", Fingerprint: "same-content"},
	}
	state, err := repo.ReserveGeneration(ctx, s.ID, s.UpdatedAt, &domain.ProblemSetGenerationState{ID: uuid.NewString(), Status: "generating", Slots: slots})
	require.NoError(t, err)
	ref := domain.ProblemSetGenerationRef{SetID: s.ID, RunID: state.ID}
	require.NoError(t, repo.CompleteGenerationSlot(ctx, ref, slots[0]))
	require.ErrorIs(t, repo.CompleteGenerationSlot(ctx, ref, slots[1]), repository.ErrProblemSetDuplicateContent)
	require.NoError(t, repo.ChangeGeneration(ctx, ref, func(state *domain.ProblemSetGenerationState) error { state.Status = "cancelled"; return nil }))
	err = repo.CompleteGenerationSlot(ctx, ref, slots[1])
	require.True(t, errors.Is(err, repository.ErrProblemSetGenerationChanged))
	got, err := repo.GetByID(ctx, s.ID)
	require.NoError(t, err)
	require.Len(t, got.Items, 1)
}
