package activities

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/config"
	"github.com/Gingoo-TvT/Qraft/backend/internal/llm"
	"go.temporal.io/sdk/temporal"
)

func TestLLMActivityAuthenticationFailureIsTerminalAndKeepsEvidence(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Path != "/v1/responses" || r.Header.Get("Authorization") != "Bearer fixture-key" {
					t.Errorf("unexpected provider request path or credentials")
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				fmt.Fprint(w, `{"error":{"type":"authentication_error","message":"Invalid token fixture"}}`)
			}))
			defer server.Close()

			store := &captureArtifactStore{}
			acts := &Activities{deps: &Dependencies{
				LLM: llm.NewClient(config.AnthropicConfig{APIKey: "fixture-key"}),
			}, artifacts: store}
			_, ref, err := acts.completeLLMWithProvenance(context.Background(), "statement", &llm.Request{
				Model:    "gpt-fixture",
				Messages: []llm.Message{{Role: "user", Content: "fixture"}},
				Runtime:  &llm.RuntimeConfig{BaseURL: server.URL, Provider: "fixture-provider", Protocol: "openai-responses"},
			}, 2)

			var appErr *temporal.ApplicationError
			if !errors.As(err, &appErr) || !appErr.NonRetryable() || !IsLLMAuthenticationError(err) {
				t.Fatalf("authentication error must be terminal: %T %v", err, err)
			}
			var apiErr *llm.APIError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != status || !strings.Contains(err.Error(), "Invalid token fixture") {
				t.Fatalf("original provider diagnostic lost: %v", err)
			}
			if calls.Load() != 1 {
				t.Fatalf("provider calls = %d, want 1", calls.Load())
			}
			if ref == nil || !strings.Contains(string(store.data), "Invalid token fixture") || !strings.Contains(string(store.data), "response_body_sha256") {
				t.Fatalf("provider response evidence was not retained")
			}
			wrapped := fmt.Errorf("activity boundary: %w", err)
			if !IsLLMAuthenticationError(wrapped) {
				t.Fatal("wrapped authentication error was not recognized")
			}
		})
	}
}

func TestLLMActivityTransientProviderFailuresStillRetry(t *testing.T) {
	for _, status := range []int{http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if calls.Add(1) == 1 {
					w.Header().Set("Retry-After", "0.001")
					w.WriteHeader(status)
					fmt.Fprint(w, `{"error":{"type":"temporary","message":"retry fixture"}}`)
					return
				}
				fmt.Fprint(w, `{"id":"response-fixture","model":"gpt-fixture","status":"completed","output_text":"ok"}`)
			}))
			defer server.Close()
			acts := &Activities{deps: &Dependencies{
				LLM: llm.NewClient(config.AnthropicConfig{APIKey: "fixture-key"}),
			}, artifacts: &captureArtifactStore{}}
			resp, _, err := acts.completeLLMWithProvenance(context.Background(), "statement", &llm.Request{
				Model:    "gpt-fixture",
				Messages: []llm.Message{{Role: "user", Content: "fixture"}},
				Runtime:  &llm.RuntimeConfig{BaseURL: server.URL, Provider: "fixture-provider", Protocol: "openai-responses"},
			}, 1)
			if err != nil || resp == nil || calls.Load() != 2 || resp.NetworkRetries != 1 {
				t.Fatalf("transient retry changed: calls=%d response=%+v error=%v", calls.Load(), resp, err)
			}
		})
	}
}

func TestClassifyLLMActivityErrorPreservesOtherFailures(t *testing.T) {
	for _, err := range []error{
		&llm.APIError{StatusCode: http.StatusBadRequest, Message: "invalid request"},
		&llm.APIError{StatusCode: http.StatusTooManyRequests, Message: "limited"},
		&llm.APIError{StatusCode: http.StatusServiceUnavailable, Message: "unavailable"},
		errors.New("connection failed"),
		temporal.NewNonRetryableApplicationError("candidate defect", "QualityNotMet", nil),
	} {
		if got := classifyLLMActivityError(err); got != err || IsLLMAuthenticationError(got) {
			t.Fatalf("unrelated error classification changed: %v", got)
		}
	}
}
