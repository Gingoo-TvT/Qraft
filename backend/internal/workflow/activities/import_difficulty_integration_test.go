//go:build integration

package activities

import (
	"context"
	"encoding/json"
	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/rating"
	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
	"github.com/Gingoo-TvT/Qraft/backend/internal/sources"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestImportedSourceRatingBypassesModelGuessAndPersistsEvidenceIntegration(t *testing.T) {
	pool := storeIntegrationPool(t)
	ctx := context.Background()
	id := uuid.New()
	statement := "Synthetic sum."
	_, err := pool.Exec(ctx, `INSERT INTO problems(id,title,statement,level,difficulty,tags,metadata_json) VALUES($1,'Synthetic source',$2,'algorithm',1800,ARRAY[]::text[],$3)`, id, statement, sourceFixtureMetadata(statement))
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), "DELETE FROM problems WHERE id=$1", id) })
	model := &capturingAuthoringLLM{}
	fetcher := &difficultyFetcherStub{doc: &sources.Document{Kind: sources.KindProblem, Items: []sources.Item{{Statement: statement, StatementSHA256: rating.Digest([]byte(statement)), Difficulty: &domain.SourceDifficulty{Platform: "codeforces", Scale: "codeforces_rating", Value: "800", Label: "Codeforces 800", SourceURL: "https://codeforces.com/problemset/problem/900001/A", FetchedAt: time.Now().UTC()}}}}}
	repo := repository.NewProblemRepository(pool)
	a := New(&Dependencies{ProblemRepo: repo, SourceFetcher: fetcher, LLM: model})
	result, err := a.EstimateImportedDifficultyActivity(ctx, ImportDifficultyInput{ProblemID: id})
	require.NoError(t, err)
	require.Equal(t, 800, result.Difficulty)
	require.Zero(t, model.calls, "upstream rating must not be overridden by an LLM")
	p, err := repo.GetByID(ctx, id)
	require.NoError(t, err)
	require.Equal(t, 800, p.Difficulty)
	source := rating.SourceFromMetadata(p.MetadataJSON, p.Statement)
	require.NotNil(t, source)
	require.Equal(t, "verified", source.Status)
	var evidence map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(p.MetadataJSON, &evidence))
	require.Contains(t, string(evidence["import_difficulty"]), "external_native_v1")
	_, err = a.EstimateImportedDifficultyActivity(ctx, ImportDifficultyInput{ProblemID: id})
	require.NoError(t, err)
	require.Equal(t, 1, fetcher.calls, "retry reuses frozen verified evidence")
	// A source rating outside the product range is preserved, never clamped or
	// replaced by a model guess presented as an upstream value.
	_, err = pool.Exec(ctx, "UPDATE problems SET metadata_json=$2 WHERE id=$1", id, sourceFixtureMetadata(statement))
	require.NoError(t, err)
	fetcher.doc.Items[0].Difficulty.Value = "4000"
	result, err = a.EstimateImportedDifficultyActivity(ctx, ImportDifficultyInput{ProblemID: id})
	require.NoError(t, err)
	require.Equal(t, 800, result.Difficulty)
	require.Contains(t, result.Reason, "未调整")
	require.Zero(t, model.calls)
	p, err = repo.GetByID(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "4000", rating.SourceFromMetadata(p.MetadataJSON, p.Statement).Difficulty.Value)

}
