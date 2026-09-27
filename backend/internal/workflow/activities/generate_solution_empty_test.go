package activities

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/llm"
	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
	"github.com/google/uuid"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
)

type emptySolutionLLM struct {
	mu             sync.Mutex
	calls          map[string]int
	bruteResponses []*llm.Response
	bruteError     error
	changedBudget  bool
}

func syntheticSolutionResponse(text, reason string) *llm.Response {
	return &llm.Response{Model: "fixture-model", ModelObserved: true, StopReason: reason, Content: []llm.ContentBlock{{Type: "text", Text: text}}}
}

const syntheticSolutionJSON = `{"source_code":"int main(){return 0;}","language":"cpp","complexity_time":"O(1)","complexity_space":"O(1)","explanation":"synthetic"}`

func (m *emptySolutionLLM) CompleteWithRetry(_ context.Context, req *llm.Request, _ int) (*llm.Response, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	role := "main"
	if strings.Contains(req.Messages[0].Content, "brute-force") {
		role = "brute"
	}
	m.calls[role]++
	m.changedBudget = m.changedBudget || req.MaxTokens != solutionMaxTokens
	if role == "main" {
		return syntheticSolutionResponse(syntheticSolutionJSON, "end_turn"), nil
	}
	if m.bruteError != nil {
		return nil, m.bruteError
	}
	index := m.calls[role] - 1
	if index >= len(m.bruteResponses) {
		return nil, fmt.Errorf("unexpected additional brute call %d", index+1)
	}
	return m.bruteResponses[index], nil
}

type keyedSolutionEffects struct {
	mu     sync.Mutex
	stores map[string]*durableFakeProviderEffectStore
}

func (s *keyedSolutionEffects) store(key string) *durableFakeProviderEffectStore {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stores[key] == nil {
		s.stores[key] = &durableFakeProviderEffectStore{token: uuid.New()}
	}
	return s.stores[key]
}
func (s *keyedSolutionEffects) Acquire(ctx context.Context, key, kind, hash string, lease time.Duration) (repository.ProviderEffectClaim, error) {
	return s.store(key).Acquire(ctx, key, kind, hash, lease)
}
func (s *keyedSolutionEffects) Complete(ctx context.Context, key, kind, hash string, token uuid.UUID, result json.RawMessage) error {
	return s.store(key).Complete(ctx, key, kind, hash, token, result)
}
func (s *keyedSolutionEffects) Fail(ctx context.Context, key, kind, hash string, token uuid.UUID, message string) error {
	return s.store(key).Fail(ctx, key, kind, hash, token, message)
}

func TestGenerateSolutionEmptyBodyRecoversOnceAndReusesBothEffects(t *testing.T) {
	model := &emptySolutionLLM{calls: map[string]int{}, bruteResponses: []*llm.Response{syntheticSolutionResponse("  ", "end_turn"), syntheticSolutionResponse(syntheticSolutionJSON, "end_turn")}}
	effects := &keyedSolutionEffects{stores: map[string]*durableFakeProviderEffectStore{}}
	acts := New(&Dependencies{LLM: model, LLMProvider: "fixture-provider", LLMModel: "fixture-model", ArtifactStore: immutableSolutionArtifactStore{}, ProvenanceRecorder: noopSolutionProvenanceRecorder{}, ProviderEffects: effects, ProviderEffectLease: time.Minute})
	// Reenter the real activity with the same durable activity identity. All
	// three completed effects must be read, including the initial empty one.
	callTwice := func(ctx context.Context) (*SolutionResult, error) {
		first, err := acts.GenerateSolutionActivity(ctx, "Print the input integer.", domain.DefaultProblemGenParams())
		if err != nil {
			return nil, err
		}
		second, err := acts.GenerateSolutionActivity(ctx, "Print the input integer.", domain.DefaultProblemGenParams())
		if err == nil && (len(first.SourceArtifacts) != 3 || len(second.SourceArtifacts) != 3) {
			return nil, errors.New("empty response evidence was lost")
		}
		return second, err
	}
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(callTwice)
	encoded, err := env.ExecuteActivity(callTwice)
	if err != nil {
		t.Fatal(err)
	}
	var out SolutionResult
	if err = encoded.Get(&out); err != nil {
		t.Fatal(err)
	}
	if out.MainSolution.SourceCode == "" || out.BruteSolution.SourceCode == "" || len(out.SourceArtifacts) != 3 {
		t.Fatalf("incomplete result: %+v", out)
	}
	for _, ref := range out.SourceArtifacts {
		if ref == nil || ref.LLMCallReceipt == nil {
			t.Fatal("missing original/recovery receipt")
		}
	}
	if out.SourceArtifacts[1].SHA256 == out.SourceArtifacts[2].SHA256 {
		t.Fatal("empty response artifact replaced with retry")
	}
	if model.calls["main"] != 1 || model.calls["brute"] != 2 || model.changedBudget {
		t.Fatalf("calls=%v changedBudget=%v", model.calls, model.changedBudget)
	}
	if len(effects.stores) != 3 {
		t.Fatalf("effect identities=%d, want 3", len(effects.stores))
	}
	hashes := map[string]int{}
	for _, store := range effects.stores {
		if len(store.completeCalls) != 1 || len(store.acquireCalls) != 2 {
			t.Fatal("redelivery repeated an effect")
		}
		hashes[store.completeCalls[0].requestSHA256]++
	}
	if len(hashes) != 2 {
		t.Fatal("recovery must preserve original request identity")
	}
	pair := false
	for _, count := range hashes {
		pair = pair || count == 2
	}
	if !pair {
		t.Fatal("empty and recovery request hashes differ")
	}
}

func TestGenerateSolutionEmptyBodyRecoveryHasExplicitBounds(t *testing.T) {
	cases := []struct {
		name        string
		responses   []*llm.Response
		providerErr error
		wantCalls   int
		wantType    string
	}{
		{"still_empty", []*llm.Response{syntheticSolutionResponse("", "end_turn"), syntheticSolutionResponse("", "end_turn")}, nil, 2, "EmptyLLMResponse"},
		{"refusal", []*llm.Response{syntheticSolutionResponse("", "refusal")}, nil, 1, "EmptyLLMResponse"},
		{"cancelled", []*llm.Response{syntheticSolutionResponse("", "cancelled")}, nil, 1, "EmptyLLMResponse"},
		{"unknown_stop", []*llm.Response{syntheticSolutionResponse("", "")}, nil, 1, "EmptyLLMResponse"},
		{"truncated", []*llm.Response{syntheticSolutionResponse("", "max_tokens")}, nil, 1, "TruncatedLLMResponse"},
		{"auth", nil, &llm.APIError{StatusCode: http.StatusUnauthorized, Message: "synthetic unauthorized"}, 1, LLMAuthenticationErrorType},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			model := &emptySolutionLLM{calls: map[string]int{}, bruteResponses: tc.responses, bruteError: tc.providerErr}
			acts := New(&Dependencies{LLM: model, LLMProvider: "fixture-provider", LLMModel: "fixture-model", ArtifactStore: immutableSolutionArtifactStore{}, ProvenanceRecorder: noopSolutionProvenanceRecorder{}})
			var suite testsuite.WorkflowTestSuite
			env := suite.NewTestActivityEnvironment()
			env.RegisterActivity(acts.GenerateSolutionActivity)
			_, err := env.ExecuteActivity(acts.GenerateSolutionActivity, "Print the input integer.", domain.DefaultProblemGenParams())
			var appErr *temporal.ApplicationError
			if !errors.As(err, &appErr) || appErr.Type() != tc.wantType || !appErr.NonRetryable() {
				t.Fatalf("error=%v, want terminal %s", err, tc.wantType)
			}
			if model.calls["brute"] != tc.wantCalls {
				t.Fatalf("brute calls=%d want%d", model.calls["brute"], tc.wantCalls)
			}
		})
	}
}
