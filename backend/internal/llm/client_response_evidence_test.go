package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/config"
)

func TestHandleResponseRetainsInvalidJSONAsBoundedEvidence(t *testing.T) {
	body := []byte(`{"content":`)
	client := &Client{}
	_, err := client.handleResponse(&http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader(body)),
	})
	if err == nil {
		t.Fatal("invalid response unexpectedly decoded")
	}
	evidence, truncated, ok := ResponseEvidence(err)
	if !ok || truncated || !bytes.Equal(evidence, body) {
		t.Fatalf("evidence=%q truncated=%v ok=%v", evidence, truncated, ok)
	}
	if strings.Contains(err.Error(), string(body)) {
		t.Fatal("response evidence leaked through the error string")
	}
}

func TestHandleResponseCapsOversizedEvidence(t *testing.T) {
	body := bytes.Repeat([]byte("x"), maxProviderResponseBytes+1)
	client := &Client{}
	_, err := client.handleResponse(&http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader(body)),
	})
	var decodeErr *ResponseDecodeError
	if !errors.As(err, &decodeErr) {
		t.Fatalf("error=%T %v, want ResponseDecodeError", err, err)
	}
	evidence, truncated, ok := ResponseEvidence(err)
	if !ok || !truncated || len(evidence) != maxProviderResponseBytes {
		t.Fatalf("evidence bytes=%d truncated=%v ok=%v", len(evidence), truncated, ok)
	}
}

func TestParseErrorResponseRetainsBodyWithoutLoggingIt(t *testing.T) {
	body := []byte("not-json-private-provider-body")
	client := &Client{}
	err := client.parseErrorResponse(&http.Response{
		StatusCode: http.StatusBadGateway,
		Body:       io.NopCloser(bytes.NewReader(body)),
	})
	evidence, truncated, ok := ResponseEvidence(err)
	if !ok || truncated || !bytes.Equal(evidence, body) {
		t.Fatalf("evidence=%q truncated=%v ok=%v", evidence, truncated, ok)
	}
	if strings.Contains(err.Error(), string(body)) {
		t.Fatal("provider error body leaked through the error string")
	}
}

func TestCompleteUsesRuntimeAPIKeyRefWithoutSendingRuntimeFields(t *testing.T) {
	t.Setenv("ALGOFORGE_RUNTIME_LLM_KEY", "runtime-secret")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Fatalf("request path = %q, want /v1/messages", r.URL.Path)
		}
		if got := r.Header.Get("x-api-key"); got != "runtime-secret" {
			t.Fatalf("x-api-key = %q, want runtime-secret", got)
		}
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(raw, []byte("api_key_ref")) || bytes.Contains(raw, []byte("Runtime")) {
			t.Fatalf("runtime metadata leaked into provider body: %s", raw)
		}
		var payload map[string]interface{}
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatal(err)
		}
		if got := payload["model"]; got != "runtime-model" {
			t.Fatalf("model = %v, want runtime-model", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"msg-fixture",
			"type":"message",
			"role":"assistant",
			"content":[{"type":"text","text":"ok"}],
			"model":"runtime-model",
			"stop_reason":"end_turn",
			"usage":{"input_tokens":1,"output_tokens":1}
		}`))
	}))
	defer server.Close()

	client := NewClient(config.AnthropicConfig{
		APIKey:  "default-secret",
		BaseURL: server.URL,
		Model:   "default-model",
	})
	resp, err := client.Complete(context.Background(), &Request{
		Model:     "runtime-model",
		MaxTokens: 10,
		Messages:  []Message{{Role: "user", Content: "hello"}},
		Runtime:   &RuntimeConfig{APIKeyRef: "env:ALGOFORGE_RUNTIME_LLM_KEY"},
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if resp.Model != "runtime-model" || resp.Text() != "ok" {
		t.Fatalf("response = %+v", resp)
	}
}

func TestCompleteRejectsMissingRuntimeAPIKeyRef(t *testing.T) {
	client := &Client{apiKey: "fallback", baseURL: "http://127.0.0.1/v1/messages", model: "model"}
	_, err := client.Complete(context.Background(), &Request{
		MaxTokens: 1,
		Messages:  []Message{{Role: "user", Content: "hello"}},
		Runtime:   &RuntimeConfig{APIKeyRef: "env:ALGOFORGE_MISSING_RUNTIME_KEY"},
	})
	if err == nil || !strings.Contains(err.Error(), "resolved to an empty environment variable") {
		t.Fatalf("missing env ref error = %v", err)
	}
}

func TestCompleteFailsClosedBeforeNetworkWithoutAPIKey(t *testing.T) {
	client := &Client{baseURL: "http://127.0.0.1:1/v1/messages", model: "model"}
	_, err := client.Complete(context.Background(), &Request{
		MaxTokens: 1,
		Messages:  []Message{{Role: "user", Content: "hello"}},
	})
	if err == nil || !strings.Contains(err.Error(), "api key is required") {
		t.Fatalf("missing API key error = %v", err)
	}
}

type staticAPIKeyResolver struct {
	key string
	ref string
}

func (r *staticAPIKeyResolver) ResolveAPIKey(_ context.Context, ref string) (string, error) {
	r.ref = ref
	return r.key, nil
}

func TestCompleteUsesRuntimeAPIKeyResolver(t *testing.T) {
	resolver := &staticAPIKeyResolver{key: "resolved-runtime-secret"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("x-api-key"); got != "resolved-runtime-secret" {
			t.Fatalf("x-api-key = %q, want resolved-runtime-secret", got)
		}
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(raw, []byte("runtime:test-token")) {
			t.Fatalf("runtime ref leaked into provider body: %s", raw)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"msg-fixture",
			"type":"message",
			"role":"assistant",
			"content":[{"type":"text","text":"ok"}],
			"model":"runtime-model",
			"stop_reason":"end_turn",
			"usage":{"input_tokens":1,"output_tokens":1}
		}`))
	}))
	defer server.Close()

	client := NewClient(config.AnthropicConfig{
		APIKey:  "default-secret",
		BaseURL: server.URL,
		Model:   "default-model",
	})
	client.SetAPIKeyResolver(resolver)
	resp, err := client.Complete(context.Background(), &Request{
		Model:     "runtime-model",
		MaxTokens: 10,
		Messages:  []Message{{Role: "user", Content: "hello"}},
		Runtime:   &RuntimeConfig{APIKeyRef: "runtime:test-token"},
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if resolver.ref != "runtime:test-token" {
		t.Fatalf("resolver ref = %q, want runtime:test-token", resolver.ref)
	}
	if resp.Text() != "ok" {
		t.Fatalf("response text = %q, want ok", resp.Text())
	}
}
