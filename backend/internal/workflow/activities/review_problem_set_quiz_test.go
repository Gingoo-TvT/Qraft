package activities

import (
	"context"
	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/llm"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
	"testing"
)

func TestSetQuizReviewRejectsBrokenObjectiveShape(t *testing.T) {
	valid := QuizDraft{Title: "单调性", Statement: "哪个结论成立？", Explanation: "完整推导", Answers: []string{"A"}, Options: []domain.QuizOption{{Label: "A", Content: "结论一"}, {Label: "B", Content: "结论二"}, {Label: "C", Content: "结论三"}}}
	require.NoError(t, validateSetQuizDraft(domain.QuizTypeChoice, valid))
	repeated := valid
	repeated.Options = append([]domain.QuizOption(nil), valid.Options...)
	repeated.Options[1].Content = repeated.Options[0].Content
	require.Error(t, validateSetQuizDraft(domain.QuizTypeChoice, repeated))
	missing := valid
	missing.Answers = []string{"D"}
	require.Error(t, validateSetQuizDraft(domain.QuizTypeChoice, missing))
	fill := QuizDraft{Title: "填空", Statement: "先 ___ 后 ___。", Answers: []string{"x", "y"}, Explanation: "按顺序"}
	require.NoError(t, validateSetQuizDraft(domain.QuizTypeFillBlank, fill))
	fill.Answers = fill.Answers[:1]
	require.Error(t, validateSetQuizDraft(domain.QuizTypeFillBlank, fill))
	judge := QuizDraft{Title: "判断", Statement: "完整命题", Answers: []string{"true"}, Explanation: "原因"}
	require.Error(t, validateSetQuizDraft(domain.QuizTypeJudge, judge))
	judge.Answers = []string{"错"}
	require.NoError(t, validateSetQuizDraft(domain.QuizTypeJudge, judge))
}
func TestSetQuizReviewAnswerComparisonRetainsBlankOrder(t *testing.T) {
	require.True(t, sameSetQuizAnswers(domain.QuizTypeChoice, []string{"A", "C"}, []string{"C", "A"}))
	require.False(t, sameSetQuizAnswers(domain.QuizTypeChoice, []string{"A"}, []string{"A", "A"}))
	require.False(t, sameSetQuizAnswers(domain.QuizTypeFillBlank, []string{"x", "y"}, []string{"y", "x"}))
	require.True(t, sameSetQuizAnswers(domain.QuizTypeFillBlank, []string{" x "}, []string{"x"}))
}

type setReviewCaptureLLM struct{ request *llm.Request }

func (f *setReviewCaptureLLM) CompleteWithRetry(_ context.Context, r *llm.Request, _ int) (*llm.Response, error) {
	f.request = r
	return &llm.Response{Content: []llm.ContentBlock{{Type: "text", Text: `{"answers":["对"],"ambiguous":false,"reason":"根据题干完整条件可以推出结论。"}`}}}, nil
}
func TestSetQuizIndependentReviewNeverReceivesGeneratedAnswerOrExplanation(t *testing.T) {
	model := &setReviewCaptureLLM{}
	acts := New(&Dependencies{LLM: model, ArtifactStore: &captureArtifactStore{}, ProvenanceRecorder: &captureProvenanceRecorder{}})
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(acts.ReviewProblemSetQuizActivity)
	result, err := env.ExecuteActivity(acts.ReviewProblemSetQuizActivity, ProblemSetQuizReviewInput{Type: domain.QuizTypeJudge, Runtime: &domain.LLMRuntimeConfig{Model: "fixture-model", APIKeyRef: "runtime:fixture_token", BaseURL: "https://example.test/v1", Provider: "fixture-provider", Protocol: "openai-responses"}, Draft: QuizDraft{Title: "title", Statement: "一个具有完整条件的独立判断命题", Answers: []string{"对"}, Explanation: "DO_NOT_LEAK_EXPLANATION"}})
	require.NoError(t, err)
	var review ProblemSetQuizReviewResult
	require.NoError(t, result.Get(&review))
	require.True(t, review.Approved)
	require.NotNil(t, model.request)
	require.NotContains(t, model.request.Messages[0].Content, "answers")
	require.NotContains(t, model.request.Messages[0].Content, "DO_NOT_LEAK_EXPLANATION")
	require.Contains(t, model.request.Messages[0].Content, "独立判断命题")
}
