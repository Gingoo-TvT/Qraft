package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	algoworkflow "github.com/Gingoo-TvT/Qraft/backend/internal/workflow"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func mixedSetFixture() *domain.ProblemSet {
	return &domain.ProblemSet{ID: uuid.New(), Title: "期末题集", DesiredItemCount: 4,
		GenerationConfig: &domain.ProblemSetGenerationConfig{Mode: "mixed", Requirements: "覆盖不同概念", Distribution: []domain.ProblemSetTypeQuota{
			{Type: domain.QuizTypeProgramming, Count: 1, Score: 100},
			{Type: domain.QuizTypeChoice, Count: 1, Score: 2},
			{Type: domain.QuizTypeFillBlank, Count: 1, Score: 5},
			{Type: domain.QuizTypeJudge, Count: 1, Score: 2},
		}},
	}
}

func TestSetGenerationExactQuotas(t *testing.T) {
	s := mixedSetFixture()
	require.NoError(t, s.GenerationConfig.Validate(4))
	state, err := buildSetGenerationState(s)
	require.NoError(t, err)
	require.Len(t, state.Slots, 4)
	for i, q := range s.GenerationConfig.Distribution {
		require.Equal(t, i+1, state.Slots[i].Position)
		require.Equal(t, q.Type, state.Slots[i].Type)
		require.Equal(t, q.Score, state.Slots[i].Score)
	}
	for _, test := range []struct {
		name string
		edit func(*domain.ProblemSetGenerationConfig)
	}{
		{"pure rejects objective", func(c *domain.ProblemSetGenerationConfig) { c.Mode = "programming" }},
		{"count drift", func(c *domain.ProblemSetGenerationConfig) { c.Distribution[0].Count++ }},
		{"duplicate type", func(c *domain.ProblemSetGenerationConfig) { c.Distribution[1].Type = c.Distribution[0].Type }},
		{"unknown type", func(c *domain.ProblemSetGenerationConfig) { c.Distribution[1].Type = "essay" }},
		{"negative score", func(c *domain.ProblemSetGenerationConfig) { c.Distribution[1].Score = -1 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			c := mixedSetFixture().GenerationConfig
			test.edit(c)
			require.Error(t, c.Validate(4))
		})
	}
	for _, n := range []int{1, 1000} {
		c := &domain.ProblemSetGenerationConfig{Mode: "programming", Distribution: []domain.ProblemSetTypeQuota{{Type: domain.QuizTypeProgramming, Count: n, Score: 100}}}
		require.NoError(t, c.Validate(n))
	}
}

func TestSetGenerationRetryPreservesSuccessAndRecoversInterruptedStore(t *testing.T) {
	s := mixedSetFixture()
	state, err := buildSetGenerationState(s)
	require.NoError(t, err)
	state.Brief = "已保存整套规划"
	state.Status = "partial"
	retained := uuid.New()
	s.Items = []domain.ProblemSetItem{{Position: 1, Score: 100, ProblemID: &retained, Problem: &domain.Problem{ID: retained, Title: "已完成", Statement: "existing"}}}
	recovered := uuid.New()
	state.Slots[0].Status = "succeeded"
	state.Slots[1].Status = "failed"
	state.Slots[1].ChildID = "old-child"
	state.Slots[1].QuizID = &recovered
	state.Slots[1].Brief = "保留的分题任务"
	state.Slots[2].Status = "failed"
	state.Slots[2].Regenerate = true
	state.Slots[2].QuizID = &recovered
	state.Slots[2].ChildID = "rejected-child"
	s.Generation = state
	next, err := buildSetGenerationState(s)
	require.NoError(t, err)
	require.NotEqual(t, state.ID, next.ID)
	require.Equal(t, state.Brief, next.Brief)
	require.Equal(t, "succeeded", next.Slots[0].Status)
	require.Equal(t, &retained, next.Slots[0].ProblemID)
	require.Equal(t, &recovered, next.Slots[1].QuizID)
	require.Equal(t, "old-child", next.Slots[1].ChildID)
	require.Equal(t, 2, next.Slots[1].Attempt)
	require.Equal(t, "pending", next.Slots[1].Status)
	require.Nil(t, next.Slots[2].QuizID)
	require.Empty(t, next.Slots[2].ChildID)
	s.Items = nil
	replacement, err := buildSetGenerationState(s)
	require.NoError(t, err)
	require.Nil(t, replacement.Slots[0].ProblemID, "a removed successful item must not be resurrected")
	s.GenerationConfig.Requirements = "新的需求"
	changed, err := buildSetGenerationState(s)
	require.NoError(t, err)
	require.Empty(t, changed.Brief)
	require.Nil(t, changed.Slots[1].QuizID)
}

func TestSetGenerationExistingItemsCannotOverfillOrOverlap(t *testing.T) {
	s := mixedSetFixture()
	qid := uuid.New()
	item := domain.ProblemSetItem{Position: 2, QuizID: &qid, Quiz: &domain.QuizProblem{Type: domain.QuizTypeChoice, Statement: "choice"}}
	s.Items = []domain.ProblemSetItem{item, item}
	_, err := buildSetGenerationState(s)
	require.Error(t, err)
	s.Items[1].Position = 3
	_, err = buildSetGenerationState(s)
	require.ErrorContains(t, err, "超过配额")
	s.Items = []domain.ProblemSetItem{item}
	s.Items[0].Position = 5
	_, err = buildSetGenerationState(s)
	require.ErrorContains(t, err, "排序")
}

func TestSetBatchPlannerRejectsModelTypeAndCoverageDrift(t *testing.T) {
	targets := []domain.ProblemSetGenerationSlot{{Position: 1, Type: domain.QuizTypeProgramming}, {Position: 2, Type: domain.QuizTypeJudge}}
	catalog := []domain.TagCategory{{TagName: "binary_search", Level: domain.LevelAlgorithm, MinDifficulty: 1000, MaxDifficulty: 2000}}
	valid := []setPlanEntry{
		{Position: 1, Type: domain.QuizTypeProgramming, Title: "边界定位", Brief: "为单调判定设计一个需要推导边界的题目，必须辨析临界状态而非机械套用模板。", Tags: []string{"binary_search"}, Level: domain.LevelAlgorithm, Difficulty: 1500},
		{Position: 2, Type: domain.QuizTypeJudge, Title: "不变量", Brief: "围绕循环不变量编写判断题，给出完整前提以保证结论唯一，并解释典型误区。", Tags: []string{"不变量"}, QuizDifficulty: domain.QuizDifficultyMedium},
	}
	require.NoError(t, validateSetBatchPlan(valid, targets, nil, catalog))
	for _, tc := range []struct {
		name string
		edit func([]setPlanEntry)
	}{
		{"wrong type", func(p []setPlanEntry) { p[1].Type = domain.QuizTypeChoice }},
		{"duplicate position", func(p []setPlanEntry) { p[1].Position = 1 }},
		{"duplicate task", func(p []setPlanEntry) { p[1].Brief = p[0].Brief }},
		{"invented tag", func(p []setPlanEntry) { p[0].Tags = []string{"invented"} }},
		{"out of range", func(p []setPlanEntry) { p[0].Difficulty = 3000 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plans := append([]setPlanEntry(nil), valid...)
			tc.edit(plans)
			require.Error(t, validateSetBatchPlan(plans, targets, nil, catalog))
		})
	}
	require.Error(t, validateSetBatchPlan(valid[:1], targets, nil, catalog))
}

type setStateOnlyStore struct {
	problemSetGenerationStore
	state *domain.ProblemSetGenerationState
}

func (s *setStateOnlyStore) ChangeGeneration(_ context.Context, _ domain.ProblemSetGenerationRef, f func(*domain.ProblemSetGenerationState) error) error {
	return f(s.state)
}

func TestSetGenerationFinishesPartialAndPreservesCompletedOnCancel(t *testing.T) {
	for _, status := range []string{"failed", "pending", "succeeded"} {
		state := &domain.ProblemSetGenerationState{Status: "generating", Slots: []domain.ProblemSetGenerationSlot{{Status: "succeeded"}, {Status: status}}}
		svc := &ProblemSetGenerationService{repo: &setStateOnlyStore{state: state}}
		more, err := svc.FinishProblemSetGenerationActivity(context.Background(), algoworkflow.ProblemSetFinishInput{})
		require.NoError(t, err)
		require.Equal(t, status == "pending", more)
		require.Equal(t, map[string]string{"failed": "partial", "pending": "planning", "succeeded": "completed"}[status], state.Status)
	}
	state := &domain.ProblemSetGenerationState{Status: "generating", Slots: []domain.ProblemSetGenerationSlot{{Status: "succeeded"}, {Status: "running"}}}
	svc := &ProblemSetGenerationService{repo: &setStateOnlyStore{state: state}}
	_, err := svc.FinishProblemSetGenerationActivity(context.Background(), algoworkflow.ProblemSetFinishInput{Cancelled: true})
	require.NoError(t, err)
	require.Equal(t, "cancelled", state.Status)
	require.Equal(t, "succeeded", state.Slots[0].Status)
}

type setSettingsFixture struct{ g, v, r *domain.LLMRuntimeConfig }

func (s setSettingsFixture) EffectiveRuntimeConfig(context.Context, string) (*domain.LLMRuntimeConfig, error) {
	return s.g, nil
}
func (s setSettingsFixture) SavedRuntimeConfig(_ context.Context, p string) (*domain.LLMRuntimeConfig, bool, error) {
	x := s.v
	if p == "review" {
		x = s.r
	}
	return x, x != nil, nil
}

type setKeysFixture struct{ values []string }

func (s *setKeysFixture) Put(_ context.Context, k string) (string, error) {
	s.values = append(s.values, k)
	return fmt.Sprintf("runtime:test-key-%d", len(s.values)), nil
}
func TestSetProviderFallbackDoesNotPutRawKeysInWorkflow(t *testing.T) {
	g := &domain.LLMRuntimeConfig{Model: "generator", Provider: "openai", Protocol: "openai-chat", BaseURL: "https://example.test/v1", APIKey: "test-secret"}
	keys := &setKeysFixture{}
	cfg, err := NewProblemSetProviderResolver(setSettingsFixture{g: g}, keys)(context.Background())
	require.NoError(t, err)
	for _, r := range []*domain.LLMRuntimeConfig{cfg.Statement, cfg.Verification, cfg.Review} {
		require.Empty(t, r.APIKey)
		require.NotEmpty(t, r.APIKeyRef)
		require.Equal(t, g.Model, r.Model)
	}
	require.Equal(t, "test-secret", g.APIKey)
}
