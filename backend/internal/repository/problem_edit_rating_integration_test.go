//go:build integration

package repository_test

import (
	"context"
	"testing"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestProblemEditPreservesLowImportedAlgorithmRatingIntegration(t *testing.T) {
	pool, _, _ := setGenerationDB(t)
	repo := repository.NewProblemRepository(pool)
	ctx := context.Background()
	p := &domain.Problem{ID: uuid.New(), Title: "Synthetic imported problem", Statement: "Compute a value.", Level: domain.LevelAlgorithm, Difficulty: 1000, Status: domain.ProblemStatusDraft, TimeLimit: 1000, MemoryLimit: 128, Tags: []string{}, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	require.NoError(t, repo.Create(ctx, p))
	current, err := repo.GetByID(ctx, p.ID)
	require.NoError(t, err)
	title := "Edited synthetic title"
	updated, err := repo.Edit(ctx, repository.ProblemEditPatch{ProblemID: p.ID, ExpectedUpdatedAt: current.UpdatedAt, Actor: "synthetic-editor", Title: &title})
	require.NoError(t, err)
	require.Equal(t, 1000, updated.Difficulty)
	require.Equal(t, domain.LevelAlgorithm, updated.Level)
	require.Equal(t, title, updated.Title)
	require.NotNil(t, updated.Tags)
	require.Empty(t, updated.Tags)
	_, err = repo.Edit(ctx, repository.ProblemEditPatch{ProblemID: p.ID, ExpectedUpdatedAt: current.UpdatedAt, Actor: "synthetic-editor", Title: &title})
	require.ErrorIs(t, err, repository.ErrConcurrentProblemEdit)
	// Removing the last tag must persist an empty array, never SQL NULL.
	tags := []string{"synthetic"}
	updated, err = repo.Edit(ctx, repository.ProblemEditPatch{ProblemID: p.ID, ExpectedUpdatedAt: updated.UpdatedAt, Actor: "synthetic-editor", Tags: &tags})
	require.NoError(t, err)
	tags = []string{}
	updated, err = repo.Edit(ctx, repository.ProblemEditPatch{ProblemID: p.ID, ExpectedUpdatedAt: updated.UpdatedAt, Actor: "synthetic-editor", Tags: &tags})
	require.NoError(t, err)
	require.NotNil(t, updated.Tags)
	require.Empty(t, updated.Tags)
}
