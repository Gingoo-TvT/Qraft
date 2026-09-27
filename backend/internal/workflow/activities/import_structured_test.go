package activities

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Gingoo-TvT/Qraft/backend/internal/llm"
	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"strings"
	"testing"
	"time"
)

type importResponseSequence struct {
	observe func(*llm.Request)
	calls   int
	texts   []string
	wait    bool
}

func (s *importResponseSequence) CompleteWithRetry(ctx context.Context, r *llm.Request, _ int) (*llm.Response, error) {
	s.calls++
	if s.observe != nil {
		s.observe(r)
	}
	if s.wait {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if s.calls > len(s.texts) {
		return nil, fmt.Errorf("unexpected repeated provider call")
	}
	return &llm.Response{Model: "fixture", StopReason: "end_turn", Content: []llm.ContentBlock{{Type: "text", Text: s.texts[s.calls-1]}}}, nil
}

type importEffectFixture struct {
	values map[string]json.RawMessage
	hashes map[string]string
}

func (s *importEffectFixture) Acquire(_ context.Context, key, typ, hash string, _ time.Duration) (repository.ProviderEffectClaim, error) {
	if previous, ok := s.hashes[key]; ok && previous != hash {
		return repository.ProviderEffectClaim{}, fmt.Errorf("effect identity changed")
	}
	s.hashes[key] = hash
	if v, ok := s.values[key]; ok {
		return repository.ProviderEffectClaim{State: repository.ProviderEffectCompleted, ResultJSON: v}, nil
	}
	return repository.ProviderEffectClaim{State: repository.ProviderEffectAcquired, LeaseToken: uuid.New()}, nil
}
func (s *importEffectFixture) Complete(_ context.Context, key, typ, hash string, _ uuid.UUID, v json.RawMessage) error {
	s.values[key] = append(json.RawMessage(nil), v...)
	return nil
}
func (s *importEffectFixture) Fail(context.Context, string, string, string, uuid.UUID, string) error {
	return nil
}
func TestImportedStructuredRepairUsesDistinctDurableCalls(t *testing.T) {
	llmClient := &importResponseSequence{texts: []string{"```cpp\nint main(){}\n```", `{"difficulty":800,"reason":"arithmetic"}`}}
	effects := &importEffectFixture{values: map[string]json.RawMessage{}, hashes: map[string]string{}}
	a := &Activities{deps: &Dependencies{LLM: llmClient, LLMProvider: "fixture", LLMModel: "fixture", ProviderEffects: effects, ProviderEffectLease: time.Minute, ProvenanceRecorder: &captureProvenanceRecorder{}}, artifacts: &captureArtifactStore{}}
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestActivityEnvironment()
	fn := func(ctx context.Context) error {
		for i := 0; i < 2; i++ {
			response, _, refs, err := a.completeImportedStructured(ctx, "fixture", &llm.Request{System: "Return difficulty and reason as JSON", MaxTokens: 100, Messages: []llm.Message{{Role: "user", Content: "synthetic"}, {Role: "assistant", Content: "{"}}}, func(r *llm.Response) error { _, e := decodeImportDifficulty(r); return e })
			if err != nil {
				return err
			}
			if len(refs) != 2 || !strings.Contains(response.Text(), "800") {
				return fmt.Errorf("repair evidence not retained")
			}
		}
		return nil
	}
	env.RegisterActivity(fn)
	_, err := env.ExecuteActivity(fn)
	require.NoError(t, err)
	require.Equal(t, 2, llmClient.calls, "redelivery must reuse both the rejected and corrected response")
	require.Len(t, effects.values, 2)
}
func TestImportedStructuredRepairsAreBounded(t *testing.T) {
	client := &importResponseSequence{texts: []string{"wrong", "still wrong", "wrong again"}}
	a := &Activities{deps: &Dependencies{LLM: client, LLMProvider: "fixture", LLMModel: "fixture"}, artifacts: &captureArtifactStore{}}
	_, _, _, err := a.completeImportedStructured(context.Background(), "fixture", &llm.Request{System: "JSON", MaxTokens: 100}, func(r *llm.Response) error { _, e := decodeImportDifficulty(r); return e })
	var application *temporal.ApplicationError
	require.True(t, errors.As(err, &application))
	require.True(t, application.NonRetryable())
	require.Equal(t, "InvalidImportModelResponse", application.Type())
	require.Equal(t, 3, client.calls)
}
func TestImportedStructuredTimeoutDoesNotWaitForActivityDeadline(t *testing.T) {
	client := &importResponseSequence{wait: true}
	a := &Activities{deps: &Dependencies{LLM: client, LLMProvider: "fixture", LLMModel: "fixture"}, artifacts: &captureArtifactStore{}}
	start := time.Now()
	_, _, _, err := a.completeImportedStructuredWithin(context.Background(), "fixture", &llm.Request{System: "JSON", MaxTokens: 100}, func(*llm.Response) error { return nil }, 20*time.Millisecond)
	require.Less(t, time.Since(start), time.Second)
	var application *temporal.ApplicationError
	require.True(t, errors.As(err, &application))
	require.Equal(t, "ImportModelTimeout", application.Type())
	require.False(t, application.NonRetryable(), "bounded activity policy may retry a transport timeout once")
	require.Equal(t, 1, client.calls)
}
func TestTestDataRejectsInnerObjectAndWrongField(t *testing.T) {
	for _, text := range []string{
		`{"cases":[{"input":"1"}]}`,
		`{"test_cases":[{"input":"1"}],"generator_recipe":{"version":"algoforge.testdata.v1","code":"void generate(){}"}`,
		`{"test_cases":null}`, `{"test_cases":[]}`, `{"version":"algoforge.testdata.v1","code":"ignored"}`,
	} {
		_, err := parseTestDataResponse(text)
		require.Error(t, err, text)
	}
	good, err := parseTestDataResponse(`{"test_cases":[{"input":"1\n","is_sample":true}]}`)
	require.NoError(t, err)
	require.Len(t, good.TestCases, 1)
}
func TestDirectImportDifficultyRequiresUsableEstimate(t *testing.T) {
	for _, text := range []string{`{"difficulty":1501,"reason":"bad step"}`, `{"difficulty":0,"reason":"unknown"}`, `{"difficulty":900}`, `{"difficulty":900,"reason":""}`} {
		_, err := decodeImportDifficulty(&llm.Response{Content: []llm.ContentBlock{{Type: "text", Text: text}}})
		require.Error(t, err)
	}
}

func TestImportedDataBudgetDoesNotMutateCallerOrSolverRuntime(t *testing.T) {
	for _, step := range []string{"testdata", "import_source_analysis", "import_duplicate_comparison", "solution_main"} {
		t.Run(step, func(t *testing.T) {
			seen := ""
			model := &importResponseSequence{texts: []string{`{}`}, observe: func(req *llm.Request) {
				seen = req.Runtime.ReasoningEffort
			}}
			request := &llm.Request{Model: "fixture", Runtime: &llm.RuntimeConfig{ReasoningEffort: "high"}}
			a := &Activities{deps: &Dependencies{LLM: model, LLMProvider: "fixture", LLMModel: "fixture"}, artifacts: &captureArtifactStore{}}
			_, _, _, err := a.completeImportedStructured(context.Background(), step, request, func(*llm.Response) error { return nil })
			require.NoError(t, err)
			want := "high"
			if step != "solution_main" {
				want = "medium"
			}
			require.Equal(t, want, seen)
			require.Equal(t, "high", request.Runtime.ReasoningEffort)
		})
	}
}
