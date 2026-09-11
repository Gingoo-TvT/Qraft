package llm

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestConfiguredHTTPTimeout(t *testing.T) {
	t.Setenv("ALGOFORGE_LLM_REQUEST_TIMEOUT", "17m")
	if got := configuredHTTPTimeout(); got != 17*time.Minute {
		t.Fatalf("duration timeout = %v, want 17m", got)
	}
	t.Setenv("ALGOFORGE_LLM_REQUEST_TIMEOUT", "901")
	if got := configuredHTTPTimeout(); got != 901*time.Second {
		t.Fatalf("seconds timeout = %v, want 901s", got)
	}
	t.Setenv("ALGOFORGE_LLM_REQUEST_TIMEOUT", "invalid")
	if got := configuredHTTPTimeout(); got != defaultTimeout {
		t.Fatalf("invalid timeout = %v, want default %v", got, defaultTimeout)
	}
}

func TestCompleteWithRetryRecordsActualNetworkRetries(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			http.Error(w, `{"type":"error","error":{"type":"api_error","message":"retry"}}`, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"msg-1","type":"message","role":"assistant","model":"fixture-model","network_retries":99,"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":3,"output_tokens":1}}`)
	}))
	defer server.Close()

	client := &Client{apiKey: "fixture", baseURL: server.URL, model: "fixture-model", httpClient: server.Client()}
	response, err := client.CompleteWithRetry(t.Context(), &Request{
		Model: "fixture-model", MaxTokens: 8,
		Messages: []Message{{Role: "user", Content: "ping"}},
	}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if response.NetworkRetries != 1 || calls.Load() != 2 {
		t.Fatalf("retry audit = %d, provider calls = %d", response.NetworkRetries, calls.Load())
	}
}

func TestCompleteWithRetryOverwritesProviderSuppliedRetryField(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"msg-1","type":"message","role":"assistant","model":"fixture-model","network_retries":99,"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":3,"output_tokens":1}}`)
	}))
	defer server.Close()

	client := &Client{apiKey: "fixture", baseURL: server.URL, model: "fixture-model", httpClient: server.Client()}
	response, err := client.CompleteWithRetry(t.Context(), &Request{
		Model: "fixture-model", MaxTokens: 8,
		Messages: []Message{{Role: "user", Content: "ping"}},
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if response.NetworkRetries != 0 {
		t.Fatalf("provider-controlled retry count survived: %d", response.NetworkRetries)
	}
}
