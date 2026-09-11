package activities

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
	"github.com/google/uuid"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	temporalworkflow "go.temporal.io/sdk/workflow"
)

var providerEffectModelVersionID = uuid.MustParse("11111111-1111-1111-1111-111111111111")

type fakeProviderEffectStore struct {
	claim       repository.ProviderEffectClaim
	acquireErr  error
	completeErr error
	failErr     error
	acquired    int
	completed   int
	failed      int
	result      json.RawMessage
}

func (s *fakeProviderEffectStore) Acquire(context.Context, string, string, string, time.Duration) (repository.ProviderEffectClaim, error) {
	s.acquired++
	return s.claim, s.acquireErr
}

func (s *fakeProviderEffectStore) Complete(_ context.Context, _, _, _ string, _ uuid.UUID, result json.RawMessage) error {
	s.completed++
	s.result = append(json.RawMessage(nil), result...)
	return s.completeErr
}

func (s *fakeProviderEffectStore) Fail(context.Context, string, string, string, uuid.UUID, string) error {
	s.failed++
	return s.failErr
}

func TestCachedProviderEffectAcquiresCompletesAndReusesResult(t *testing.T) {
	token := uuid.New()
	store := &fakeProviderEffectStore{claim: repository.ProviderEffectClaim{
		State: repository.ProviderEffectAcquired, LeaseToken: token,
	}}
	acts := New(&Dependencies{ProviderEffects: store, ProviderEffectLease: time.Minute})
	invoked := 0
	result, err := acts.cachedProviderEffect(context.Background(), providerEffectInvocation{
		Key: "operation:key", Type: "fixture/v1", RequestSHA256: strings.Repeat("a", 64),
	}, func() (json.RawMessage, error) {
		invoked++
		return json.RawMessage(`{"value":1}`), nil
	})
	if err != nil || string(result) != `{"value":1}` {
		t.Fatalf("result=%s err=%v", result, err)
	}
	if invoked != 1 || store.acquired != 1 || store.completed != 1 || store.failed != 0 {
		t.Fatalf("invoke=%d store=%+v", invoked, store)
	}

	store.claim = repository.ProviderEffectClaim{
		State: repository.ProviderEffectCompleted, ResultJSON: json.RawMessage(`{"value":1}`),
	}
	result, err = acts.cachedProviderEffect(context.Background(), providerEffectInvocation{
		Key: "operation:key", Type: "fixture/v1", RequestSHA256: strings.Repeat("a", 64),
	}, func() (json.RawMessage, error) {
		invoked++
		return nil, errors.New("cached result should skip provider")
	})
	if err != nil || string(result) != `{"value":1}` || invoked != 1 {
		t.Fatalf("cached result=%s err=%v invoke=%d", result, err, invoked)
	}
}

func TestCachedProviderEffectDoesNotInvokeWhileLeaseIsBusy(t *testing.T) {
	leasedUntil := time.Now().Add(time.Minute)
	store := &fakeProviderEffectStore{claim: repository.ProviderEffectClaim{
		State: repository.ProviderEffectBusy, LeasedUntil: leasedUntil,
	}}
	acts := New(&Dependencies{ProviderEffects: store, ProviderEffectLease: time.Minute})
	invoked := false
	_, err := acts.cachedProviderEffect(context.Background(), providerEffectInvocation{
		Key: "operation:key", Type: "fixture/v1", RequestSHA256: strings.Repeat("a", 64),
	}, func() (json.RawMessage, error) {
		invoked = true
		return json.RawMessage(`{}`), nil
	})
	var applicationErr *temporal.ApplicationError
	if err == nil || !errors.As(err, &applicationErr) || !strings.Contains(err.Error(), "leased until") || invoked {
		t.Fatalf("err=%v invoked=%v", err, invoked)
	}
	if applicationErr.Type() != providerEffectBusyErrorType || applicationErr.NonRetryable() {
		t.Fatalf("application error type=%q non_retryable=%v", applicationErr.Type(), applicationErr.NonRetryable())
	}
	if delay := applicationErr.NextRetryDelay(); delay <= time.Until(leasedUntil) || delay > time.Minute+providerEffectLeaseRetryBuffer {
		t.Fatalf("next retry delay = %s, leased until %s", delay, leasedUntil)
	}
}

func TestCachedProviderEffectBusyUsesPositiveMinimumRetryDelay(t *testing.T) {
	store := &fakeProviderEffectStore{claim: repository.ProviderEffectClaim{
		State: repository.ProviderEffectBusy, LeasedUntil: time.Now().Add(-2 * providerEffectLeaseRetryBuffer),
	}}
	acts := New(&Dependencies{ProviderEffects: store, ProviderEffectLease: time.Minute})
	invoked := false
	_, err := acts.cachedProviderEffect(context.Background(), providerEffectInvocation{
		Key: "operation:key", Type: "fixture/v1", RequestSHA256: strings.Repeat("a", 64),
	}, func() (json.RawMessage, error) {
		invoked = true
		return json.RawMessage(`{}`), nil
	})
	var applicationErr *temporal.ApplicationError
	if !errors.As(err, &applicationErr) {
		t.Fatalf("busy error = %T %v", err, err)
	}
	if delay := applicationErr.NextRetryDelay(); delay != providerEffectMinimumRetryDelay {
		t.Fatalf("next retry delay = %s, want %s", delay, providerEffectMinimumRetryDelay)
	}
	if invoked {
		t.Fatal("provider was invoked while the effect lease was busy")
	}
}

func TestCachedProviderEffectReleasesLeaseAfterProviderFailure(t *testing.T) {
	providerErr := errors.New("injected provider failure")
	store := &fakeProviderEffectStore{claim: repository.ProviderEffectClaim{
		State: repository.ProviderEffectAcquired, LeaseToken: uuid.New(),
	}}
	acts := New(&Dependencies{ProviderEffects: store, ProviderEffectLease: time.Minute})
	_, err := acts.cachedProviderEffect(context.Background(), providerEffectInvocation{
		Key: "operation:key", Type: "fixture/v1", RequestSHA256: strings.Repeat("a", 64),
	}, func() (json.RawMessage, error) { return nil, providerErr })
	if !errors.Is(err, providerErr) || store.failed != 1 || store.completed != 0 {
		t.Fatalf("err=%v store=%+v", err, store)
	}
}

func TestValidateProviderEffectConfiguration(t *testing.T) {
	store := &fakeProviderEffectStore{}
	valid := &Dependencies{
		LLMProvider: "provider", LLMModel: "model",
		EmbeddingEnabled: true, EmbeddingProvider: "embedding-provider", EmbeddingModel: "embedding-model",
		EmbeddingModelVersionID: providerEffectModelVersionID,
		ProviderEffects:         store, ProviderEffectLease: time.Minute,
	}
	if err := valid.ValidateProviderEffectConfiguration(); err != nil {
		t.Fatalf("valid dependencies: %v", err)
	}
	invalid := *valid
	invalid.LLMModel = ""
	if err := invalid.ValidateProviderEffectConfiguration(); err == nil || !strings.Contains(err.Error(), "LLM model") {
		t.Fatalf("missing model error = %v", err)
	}
	invalid = *valid
	invalid.ProviderEffects = nil
	if err := invalid.ValidateProviderEffectConfiguration(); err == nil || !strings.Contains(err.Error(), "store") {
		t.Fatalf("missing store error = %v", err)
	}
	disabled := *valid
	disabled.EmbeddingEnabled = false
	disabled.EmbeddingProvider = ""
	disabled.EmbeddingModel = ""
	if err := disabled.ValidateProviderEffectConfiguration(); err != nil {
		t.Fatalf("disabled embedding configuration: %v", err)
	}
}

type recordingEmbedder struct {
	mu     sync.Mutex
	result []float32
	err    error
	calls  []string
}

func (e *recordingEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls = append(e.calls, text)
	if e.err != nil {
		return nil, e.err
	}
	return append([]float32(nil), e.result...), nil
}

func (e *recordingEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	result := make([][]float32, len(texts))
	for i, text := range texts {
		embedding, err := e.Embed(ctx, text)
		if err != nil {
			return nil, err
		}
		result[i] = embedding
	}
	return result, nil
}

func (e *recordingEmbedder) callCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.calls)
}

type recordedProviderEffectCall struct {
	key           string
	effectType    string
	requestSHA256 string
	lease         time.Duration
}

type durableFakeProviderEffectStore struct {
	mu            sync.Mutex
	token         uuid.UUID
	completed     bool
	result        json.RawMessage
	acquireErr    error
	acquireCalls  []recordedProviderEffectCall
	completeCalls []recordedProviderEffectCall
	failCalls     int
}

func (s *durableFakeProviderEffectStore) Acquire(
	_ context.Context,
	key, effectType, requestSHA256 string,
	lease time.Duration,
) (repository.ProviderEffectClaim, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.acquireCalls = append(s.acquireCalls, recordedProviderEffectCall{
		key: key, effectType: effectType, requestSHA256: requestSHA256, lease: lease,
	})
	if s.acquireErr != nil {
		return repository.ProviderEffectClaim{}, s.acquireErr
	}
	if s.completed {
		return repository.ProviderEffectClaim{
			State: repository.ProviderEffectCompleted, ResultJSON: append(json.RawMessage(nil), s.result...),
		}, nil
	}
	return repository.ProviderEffectClaim{State: repository.ProviderEffectAcquired, LeaseToken: s.token}, nil
}

func (s *durableFakeProviderEffectStore) Complete(
	_ context.Context,
	key, effectType, requestSHA256 string,
	_ uuid.UUID,
	result json.RawMessage,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.completeCalls = append(s.completeCalls, recordedProviderEffectCall{
		key: key, effectType: effectType, requestSHA256: requestSHA256,
	})
	s.completed = true
	s.result = append(json.RawMessage(nil), result...)
	return nil
}

func (s *durableFakeProviderEffectStore) Fail(context.Context, string, string, string, uuid.UUID, string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failCalls++
	return nil
}

func cachedEmbeddingRedeliveryWorkflow(ctx temporalworkflow.Context, text string) error {
	ctx = temporalworkflow.WithActivityOptions(ctx, temporalworkflow.ActivityOptions{
		StartToCloseTimeout: time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval: time.Millisecond,
			MaximumAttempts: 2,
		},
	})
	return temporalworkflow.ExecuteActivity(ctx, "cachedEmbeddingRedeliveryActivity", text).Get(ctx, nil)
}

func TestCachedTemporalProblemEmbeddingUsesStableIdentityOnRedelivery(t *testing.T) {
	const (
		provider = "embedding-provider-fixture"
		model    = "embedding-model-fixture"
		text     = "stable embedding input"
		lease    = 3 * time.Minute
	)
	embedder := &recordingEmbedder{result: []float32{0.25, 0.75}}
	store := &durableFakeProviderEffectStore{token: uuid.New()}
	acts := New(&Dependencies{
		Embedding: embedder, EmbeddingProvider: provider, EmbeddingModel: model,
		EmbeddingModelVersionID: providerEffectModelVersionID,
		ProviderEffects:         store, ProviderEffectLease: lease,
	})
	retryActivity := func(ctx context.Context, input string) error {
		embedding, err := acts.cachedTemporalProblemEmbedding(ctx, "redelivery-fixture", input)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(embedding, []float32{0.25, 0.75}) {
			return errors.New("unexpected cached embedding")
		}
		if activity.GetInfo(ctx).Attempt == 1 {
			return errors.New("force activity redelivery after provider completion")
		}
		return nil
	}

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.SetStartWorkflowOptions(client.StartWorkflowOptions{ID: "provider-effect-redelivery-fixture"})
	env.RegisterWorkflow(cachedEmbeddingRedeliveryWorkflow)
	env.RegisterActivityWithOptions(retryActivity, activity.RegisterOptions{Name: "cachedEmbeddingRedeliveryActivity"})
	env.ExecuteWorkflow(cachedEmbeddingRedeliveryWorkflow, text)
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("redelivery workflow failed: %v", err)
	}

	store.mu.Lock()
	acquires := append([]recordedProviderEffectCall(nil), store.acquireCalls...)
	completes := append([]recordedProviderEffectCall(nil), store.completeCalls...)
	fails := store.failCalls
	store.mu.Unlock()
	if len(acquires) != 2 || len(completes) != 1 || fails != 0 {
		t.Fatalf("acquires=%d completes=%d fails=%d", len(acquires), len(completes), fails)
	}
	if embedder.callCount() != 1 {
		t.Fatalf("provider embedding calls = %d, want 1", embedder.callCount())
	}
	if acquires[0].key != acquires[1].key || acquires[0].key == "" || !strings.HasPrefix(acquires[0].key, "temporal:") {
		t.Fatalf("redelivery effect keys = %q, %q", acquires[0].key, acquires[1].key)
	}
	expectedHash, err := providerRequestSHA256(problemEmbeddingRequest{
		SchemaVersion: 2, Provider: provider, Model: model,
		ModelVersionID: providerEffectModelVersionID.String(), Text: text,
	})
	if err != nil {
		t.Fatal(err)
	}
	for i, call := range acquires {
		if call.effectType != embeddingProviderEffectType || call.requestSHA256 != expectedHash || call.lease != lease {
			t.Fatalf("acquire call %d = %+v, expected hash %s and lease %s", i, call, expectedHash, lease)
		}
	}
	if completes[0].key != acquires[0].key || completes[0].effectType != embeddingProviderEffectType || completes[0].requestSHA256 != expectedHash {
		t.Fatalf("complete call = %+v, acquire = %+v", completes[0], acquires[0])
	}
}

func TestProblemEmbeddingRequestIdentityIncludesExactModelVersion(t *testing.T) {
	base := problemEmbeddingRequest{
		SchemaVersion:  2,
		Provider:       "openai-compatible:https://embedding.example/v1",
		Model:          "embedding-model",
		ModelVersionID: "11111111-1111-1111-1111-111111111111",
		Text:           "same statement",
	}
	first, err := providerRequestSHA256(base)
	if err != nil {
		t.Fatal(err)
	}
	changed := base
	changed.ModelVersionID = "22222222-2222-2222-2222-222222222222"
	second, err := providerRequestSHA256(changed)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("provider-effect request hash did not change with exact model version")
	}
}

func TestCachedTemporalProblemEmbeddingRequiresProviderEffectStore(t *testing.T) {
	embedder := &recordingEmbedder{result: []float32{1}}
	acts := New(&Dependencies{
		Embedding: embedder, EmbeddingProvider: "provider", EmbeddingModel: "model",
		EmbeddingModelVersionID: providerEffectModelVersionID,
		ProviderEffectLease:     time.Minute,
	})
	testActivity := func(ctx context.Context, text string) ([]float32, error) {
		return acts.cachedTemporalProblemEmbedding(ctx, "missing-store", text)
	}

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivityWithOptions(testActivity, activity.RegisterOptions{Name: "missingProviderEffectStoreActivity"})
	_, err := env.ExecuteActivity(testActivity, "must not reach provider")
	if err == nil || !strings.Contains(err.Error(), "provider effect store is required") {
		t.Fatalf("missing store error = %v", err)
	}
	if embedder.callCount() != 0 {
		t.Fatalf("provider embedding calls = %d, want 0", embedder.callCount())
	}
}

func TestSimilarityActivitiesFailClosedOnEmbeddingFailure(t *testing.T) {
	embedder := &recordingEmbedder{err: errors.New("embedding provider unavailable")}
	store := &durableFakeProviderEffectStore{token: uuid.New()}
	acts := New(&Dependencies{
		Embedding: embedder, EmbeddingProvider: "provider", EmbeddingModel: "model",
		EmbeddingModelVersionID: providerEffectModelVersionID,
		ProblemRepo:             repository.NewProblemRepository(nil),
		ProviderEffects:         store, ProviderEffectLease: time.Minute,
	})

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(acts.SimilarityCheckActivity)
	env.RegisterActivity(acts.PostStatementSimilarityActivity)
	_, err := env.ExecuteActivity(acts.SimilarityCheckActivity, domain.ProblemGenParams{})
	if err == nil || !strings.Contains(err.Error(), "required pre-generation similarity embedding") {
		t.Fatalf("pre-generation similarity error = %v", err)
	}

	_, err = env.ExecuteActivity(acts.PostStatementSimilarityActivity, StatementResult{
		Statement: "statement", OneLineHint: "hint",
	})
	if err == nil || !strings.Contains(err.Error(), "required post-statement similarity embedding") {
		t.Fatalf("post-statement similarity error = %v", err)
	}
	if embedder.callCount() != 2 {
		t.Fatalf("provider embedding calls = %d, want 2", embedder.callCount())
	}
	store.mu.Lock()
	acquireCount := len(store.acquireCalls)
	completeCount := len(store.completeCalls)
	failCount := store.failCalls
	store.mu.Unlock()
	if acquireCount != 2 || completeCount != 0 || failCount != 2 {
		t.Fatalf("provider effect calls: acquire=%d complete=%d fail=%d", acquireCount, completeCount, failCount)
	}
}

func TestSimilarityActivitiesReturnBusyProviderEffectToTemporal(t *testing.T) {
	leasedUntil := time.Now().Add(time.Minute)
	embedder := &recordingEmbedder{result: []float32{1}}
	store := &fakeProviderEffectStore{claim: repository.ProviderEffectClaim{
		State: repository.ProviderEffectBusy, LeasedUntil: leasedUntil,
	}}
	acts := New(&Dependencies{
		Embedding: embedder, EmbeddingProvider: "provider", EmbeddingModel: "model",
		EmbeddingModelVersionID: providerEffectModelVersionID,
		ProblemRepo:             repository.NewProblemRepository(nil),
		ProviderEffects:         store, ProviderEffectLease: time.Minute,
	})

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(acts.SimilarityCheckActivity)
	env.RegisterActivity(acts.PostStatementSimilarityActivity)
	for name, activityFn := range map[string]interface{}{
		"parameter similarity":      acts.SimilarityCheckActivity,
		"post-statement similarity": acts.PostStatementSimilarityActivity,
	} {
		var err error
		if name == "parameter similarity" {
			_, err = env.ExecuteActivity(activityFn, domain.ProblemGenParams{})
		} else {
			_, err = env.ExecuteActivity(activityFn, StatementResult{Statement: "statement", OneLineHint: "hint"})
		}
		var applicationErr *temporal.ApplicationError
		if !errors.As(err, &applicationErr) || applicationErr.Type() != providerEffectBusyErrorType {
			t.Fatalf("%s busy error = %T %v", name, err, err)
		}
		if applicationErr.NextRetryDelay() <= time.Until(leasedUntil) {
			t.Fatalf("%s next retry delay = %s", name, applicationErr.NextRetryDelay())
		}
	}
	if embedder.callCount() != 0 {
		t.Fatalf("provider embedding calls = %d, want 0", embedder.callCount())
	}
}

func TestSimilarityActivityReturnsProviderEffectControlPlaneFailures(t *testing.T) {
	tests := []struct {
		name              string
		provider          string
		store             *fakeProviderEffectStore
		wantError         string
		wantProviderCalls int
	}{
		{
			name: "acquire database failure", provider: "provider",
			store:     &fakeProviderEffectStore{acquireErr: errors.New("acquire database unavailable")},
			wantError: "required pre-generation similarity embedding is unavailable",
		},
		{
			name: "missing provider identity",
			store: &fakeProviderEffectStore{claim: repository.ProviderEffectClaim{
				State: repository.ProviderEffectAcquired, LeaseToken: uuid.New(),
			}},
			wantError: "required pre-generation similarity embedding is unavailable",
		},
		{
			name: "invalid cached result", provider: "provider",
			store: &fakeProviderEffectStore{claim: repository.ProviderEffectClaim{
				State: repository.ProviderEffectCompleted, ResultJSON: json.RawMessage(`{`),
			}},
			wantError: "required pre-generation similarity embedding is unavailable",
		},
		{
			name: "complete database failure", provider: "provider",
			store: &fakeProviderEffectStore{
				claim: repository.ProviderEffectClaim{
					State: repository.ProviderEffectAcquired, LeaseToken: uuid.New(),
				},
				completeErr: errors.New("complete database unavailable"),
			},
			wantError: "required pre-generation similarity embedding is unavailable", wantProviderCalls: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			embedder := &recordingEmbedder{result: []float32{1}}
			acts := New(&Dependencies{
				Embedding: embedder, EmbeddingProvider: tt.provider, EmbeddingModel: "model",
				EmbeddingModelVersionID: providerEffectModelVersionID,
				ProviderEffects:         tt.store, ProviderEffectLease: time.Minute,
			})
			var suite testsuite.WorkflowTestSuite
			env := suite.NewTestActivityEnvironment()
			env.RegisterActivity(acts.SimilarityCheckActivity)
			_, err := env.ExecuteActivity(acts.SimilarityCheckActivity, domain.ProblemGenParams{})
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("control-plane error = %v, want %q", err, tt.wantError)
			}
			report, ok := DedupCheckFailedReportFromError(err)
			if !ok || report.Decision != DedupDecisionCheckFailed ||
				report.Reason != DedupFailureEmbeddingUnavailable || report.NeighborCount != nil {
				t.Fatalf("control-plane report = %+v ok=%t", report, ok)
			}
			if calls := embedder.callCount(); calls != tt.wantProviderCalls {
				t.Fatalf("provider calls = %d, want %d", calls, tt.wantProviderCalls)
			}
		})
	}
}
