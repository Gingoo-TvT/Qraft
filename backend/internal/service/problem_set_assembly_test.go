package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func assemblyFixture(typ domain.QuizType, name, tag string, rating int) domain.ProblemSetAssemblyCandidate {
	return domain.ProblemSetAssemblyCandidate{
		ProblemSetAssemblyRef: domain.ProblemSetAssemblyRef{ID: uuid.NewSHA1(uuid.NameSpaceURL, []byte(name)), Type: typ, UpdatedAt: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)},
		Title:                 name, Tags: []string{tag}, Difficulty: rating, QuizDifficulty: domain.QuizDifficultyMedium, Fingerprint: name,
	}
}
func TestAssemblySelectsExactQuotasDeduplicatesAndOrders(t *testing.T) {
	req := ProblemSetAssemblyRequest{Config: domain.ProblemSetGenerationConfig{Mode: "mixed", Distribution: []domain.ProblemSetTypeQuota{
		{Type: domain.QuizTypeProgramming, Count: 2, Score: 50}, {Type: domain.QuizTypeJudge, Count: 3, Score: 0},
	}}, Filter: domain.ProblemSetAssemblyFilter{Seed: "stable"}}
	_, _, err := normalizeAssemblyRequest(&req)
	require.NoError(t, err)
	candidates := []domain.ProblemSetAssemblyCandidate{
		assemblyFixture(domain.QuizTypeProgramming, "hard", "图论", 2200),
		assemblyFixture(domain.QuizTypeProgramming, "easy", "动态规划", 1000),
		assemblyFixture(domain.QuizTypeJudge, "judge", "图论", 0),
		assemblyFixture(domain.QuizTypeChoice, "not-requested", "其他", 0),
	}
	duplicate := candidates[2]
	duplicate.ID = uuid.New()
	candidates = append(candidates, duplicate)
	got := selectAssemblyCandidates(req, candidates)
	require.Len(t, got.Items, 3)
	require.Equal(t, 100, got.TotalScore)
	require.Equal(t, 2, got.MissingCount)
	require.Equal(t, 1000, got.Items[0].Difficulty)
	require.Equal(t, 2200, got.Items[1].Difficulty)
	require.Zero(t, got.Items[2].Score)
	require.Equal(t, 1, got.Distribution[1].Available)
	require.Equal(t, 2, got.Distribution[1].Missing)
	// Database order must not affect a seeded preview.
	for i, j := 0, len(candidates)-1; i < j; i, j = i+1, j-1 {
		candidates[i], candidates[j] = candidates[j], candidates[i]
	}
	require.Equal(t, got, selectAssemblyCandidates(req, candidates))
}

func TestAssemblyPrefersDifferentCoverageAndRefreshCanVary(t *testing.T) {
	req := ProblemSetAssemblyRequest{Config: domain.ProblemSetGenerationConfig{Mode: "programming", Distribution: []domain.ProblemSetTypeQuota{{Type: domain.QuizTypeProgramming, Count: 2, Score: 100}}}, Filter: domain.ProblemSetAssemblyFilter{Seed: "first"}}
	candidates := []domain.ProblemSetAssemblyCandidate{}
	for i := 0; i < 30; i++ {
		candidates = append(candidates, assemblyFixture(domain.QuizTypeProgramming, fmt.Sprint(i), fmt.Sprint(i%2), 1000))
	}
	first := selectAssemblyCandidates(req, candidates)
	require.NotEqual(t, first.Items[0].Tags, first.Items[1].Tags)
	req.Filter.Seed = "second"
	require.NotEqual(t, first.Items, selectAssemblyCandidates(req, candidates).Items)
}

func TestAssemblyValidatesCountsAndFilters(t *testing.T) {
	valid := func() ProblemSetAssemblyRequest {
		return ProblemSetAssemblyRequest{Config: domain.ProblemSetGenerationConfig{Mode: "programming", Distribution: []domain.ProblemSetTypeQuota{{Type: domain.QuizTypeProgramming, Count: 1000, Score: 100}}}}
	}
	req := valid()
	total, _, err := normalizeAssemblyRequest(&req)
	require.NoError(t, err)
	require.Equal(t, 1000, total)
	require.Equal(t, 800, req.Filter.MinDifficulty)
	require.Equal(t, 3500, req.Filter.MaxDifficulty)
	for _, mutate := range []func(*ProblemSetAssemblyRequest){
		func(r *ProblemSetAssemblyRequest) { r.Config.Distribution[0].Count = 1001 },
		func(r *ProblemSetAssemblyRequest) { r.Filter.MinDifficulty = 850 },
		func(r *ProblemSetAssemblyRequest) { r.Filter.MinDifficulty = 2500; r.Filter.MaxDifficulty = 2000 },
		func(r *ProblemSetAssemblyRequest) { r.Filter.ExcludeRecentSets = 51 },
		func(r *ProblemSetAssemblyRequest) { r.Filter.QuizDifficulty = "unexpected" },
	} {
		req := valid()
		mutate(&req)
		_, _, err := normalizeAssemblyRequest(&req)
		require.ErrorContains(t, err, "validation:")
	}
	req = valid()
	req.Filter.Tags = []string{" DP ", "dp", "", "图论"}
	_, _, err = normalizeAssemblyRequest(&req)
	require.NoError(t, err)
	require.Equal(t, []string{"dp", "图论"}, req.Filter.Tags)
}

type assemblyValidationStore struct {
	problemSetStore
	candidates []domain.ProblemSetAssemblyCandidate
	writes     int
}

func (f *assemblyValidationStore) ListAssemblyCandidates(context.Context, domain.ProblemSetAssemblyFilter, []domain.QuizType) ([]domain.ProblemSetAssemblyCandidate, error) {
	return f.candidates, nil
}
func (f *assemblyValidationStore) CreateAssembled(context.Context, *domain.ProblemSet, []domain.ProblemSetAssemblyRef) error {
	f.writes++
	return nil
}

func TestAssemblyRejectsStaleDuplicateAndWrongTypeWithoutWriting(t *testing.T) {
	c := assemblyFixture(domain.QuizTypeProgramming, "p", "图", 1000)
	fake := &assemblyValidationStore{candidates: []domain.ProblemSetAssemblyCandidate{c}}
	svc := NewProblemSetServiceWithDeps(fake, nil, nil, nil, nil)
	req := ProblemSetAssembleRequest{Title: "paper", ProblemSetAssemblyRequest: ProblemSetAssemblyRequest{Config: domain.ProblemSetGenerationConfig{Mode: "programming", Distribution: []domain.ProblemSetTypeQuota{{Type: domain.QuizTypeProgramming, Count: 2, Score: 100}}}}}
	req.Items = []domain.ProblemSetAssemblyRef{c.ProblemSetAssemblyRef}
	req.Items[0].UpdatedAt = req.Items[0].UpdatedAt.Add(time.Second)
	_, err := svc.Assemble(context.Background(), req)
	require.ErrorIs(t, err, ErrConflict)
	req.Items = []domain.ProblemSetAssemblyRef{c.ProblemSetAssemblyRef, c.ProblemSetAssemblyRef}
	_, err = svc.Assemble(context.Background(), req)
	require.ErrorContains(t, err, "重复")
	req.Items = []domain.ProblemSetAssemblyRef{{ID: c.ID, Type: domain.QuizTypeJudge, UpdatedAt: c.UpdatedAt}}
	_, err = svc.Assemble(context.Background(), req)
	require.ErrorIs(t, err, ErrConflict)
	req.Items = nil
	_, err = svc.Assemble(context.Background(), req)
	require.ErrorContains(t, err, "预览")
	require.Zero(t, fake.writes)
}
