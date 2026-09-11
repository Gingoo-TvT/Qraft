package activities

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/llm"
	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
)

type providerCallCountingLLM struct {
	calls int
}

func (l *providerCallCountingLLM) CompleteWithRetry(context.Context, *llm.Request, int) (*llm.Response, error) {
	l.calls++
	return nil, errors.New("provider must not be called while effect lease is busy")
}

func TestRequiredProviderEffectErrorPreservesTemporalRetryMetadata(t *testing.T) {
	busy := temporal.NewApplicationErrorWithOptions(
		"provider effect is busy",
		providerEffectBusyErrorType,
		temporal.ApplicationErrorOptions{NextRetryDelay: 37 * time.Second},
	)
	got := wrapRequiredProviderEffectError("llm call for fixture", busy)
	if got != busy {
		t.Fatalf("busy application error was replaced: got %T %v", got, got)
	}
	var applicationErr *temporal.ApplicationError
	if !errors.As(got, &applicationErr) {
		t.Fatalf("busy error type = %T, want *temporal.ApplicationError", got)
	}
	if applicationErr.Type() != providerEffectBusyErrorType {
		t.Fatalf("busy error type = %q, want %q", applicationErr.Type(), providerEffectBusyErrorType)
	}
	if applicationErr.NextRetryDelay() != 37*time.Second {
		t.Fatalf("next retry delay = %s, want 37s", applicationErr.NextRetryDelay())
	}

	providerErr := errors.New("provider unavailable")
	got = wrapRequiredProviderEffectError("llm call for fixture", providerErr)
	if !errors.Is(got, providerErr) || !strings.Contains(got.Error(), "llm call for fixture") {
		t.Fatalf("ordinary provider error lost context or cause: %v", got)
	}
}

func TestGenerateTestDataActivityReturnsBusyProviderEffectToTemporal(t *testing.T) {
	leasedUntil := time.Now().Add(time.Minute)
	store := &fakeProviderEffectStore{claim: repository.ProviderEffectClaim{
		State: repository.ProviderEffectBusy, LeasedUntil: leasedUntil,
	}}
	provider := &providerCallCountingLLM{}
	acts := New(&Dependencies{
		LLM: provider, LLMProvider: "fixture-provider", LLMModel: "fixture-model",
		ProviderEffects: store, ProviderEffectLease: time.Minute,
	})

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(acts.GenerateTestDataActivity)
	_, err := env.ExecuteActivity(
		acts.GenerateTestDataActivity,
		"Given an integer, print it.",
		domain.TestDataConfig{NumTestCases: 1, NumSamples: 1},
		domain.DefaultProblemGenParams(),
	)
	var applicationErr *temporal.ApplicationError
	if !errors.As(err, &applicationErr) {
		t.Fatalf("GenerateTestData busy error = %T %v, want *temporal.ApplicationError", err, err)
	}
	if applicationErr.Type() != providerEffectBusyErrorType || applicationErr.NonRetryable() {
		t.Fatalf("application error type=%q non_retryable=%v", applicationErr.Type(), applicationErr.NonRetryable())
	}
	if delay := applicationErr.NextRetryDelay(); delay <= time.Until(leasedUntil) {
		t.Fatalf("next retry delay = %s, leased until %s", delay, leasedUntil)
	}
	if provider.calls != 0 {
		t.Fatalf("provider calls while lease busy = %d, want 0", provider.calls)
	}
	if store.acquired != 1 || store.completed != 0 || store.failed != 0 {
		t.Fatalf("provider effect store calls: %+v", store)
	}
}
