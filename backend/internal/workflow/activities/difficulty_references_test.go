package activities

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/rating"
	"github.com/Gingoo-TvT/Qraft/backend/internal/sources"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

type difficultyFetcherStub struct {
	doc   *sources.Document
	err   error
	calls int
}

func (s *difficultyFetcherStub) Fetch(context.Context, string) (*sources.Document, error) {
	s.calls++
	return s.doc, s.err
}
func sourceFixtureMetadata(statement string) json.RawMessage {
	source := domain.SourceProblem{ItemID: "synthetic", Title: "Synthetic", Statement: statement, SourceURL: "https://codeforces.com/problemset/problem/900001/A"}
	raw, _ := json.Marshal(map[string]any{"import_source": domain.ImportSourceEvidence{Original: source, OriginalSHA256: source.Hash(), FinalSHA256: source.Hash()}})
	return raw
}
func TestSourceDifficultyRequiresWorkerFetchAndMatchingStatement(t *testing.T) {
	statement := "Synthetic add two values."
	doc := &sources.Document{Kind: sources.KindProblem, Items: []sources.Item{{Statement: statement, StatementSHA256: rating.Digest([]byte(statement)), Difficulty: &domain.SourceDifficulty{Platform: "codeforces", Scale: "codeforces_rating", Value: "900", Label: "Codeforces 900", SourceURL: "https://codeforces.com/problemset/problem/900001/A", FetchedAt: time.Unix(100, 0)}}}}
	fetcher := &difficultyFetcherStub{doc: doc}
	a := New(&Dependencies{SourceFetcher: fetcher})
	got := a.resolveSourceDifficulty(context.Background(), sourceFixtureMetadata(statement), statement)
	require.Equal(t, "verified", got.Status)
	n, ok := rating.NativeRating(got)
	require.True(t, ok)
	require.Equal(t, 900, n)
	doc.Items[0].Statement = "Different task"
	require.Equal(t, "source_changed", a.resolveSourceDifficulty(context.Background(), sourceFixtureMetadata(statement), statement).Status)
	fetcher.err = errors.New("unavailable")
	require.Equal(t, "unavailable", a.resolveSourceDifficulty(context.Background(), sourceFixtureMetadata(statement), statement).Status)
	before := fetcher.calls
	require.Equal(t, "stale", a.resolveSourceDifficulty(context.Background(), sourceFixtureMetadata(statement), "modified").Status)
	require.Equal(t, before, fetcher.calls, "modified local task must not adopt upstream score")
}
