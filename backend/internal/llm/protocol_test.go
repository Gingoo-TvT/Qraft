package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/config"
)

func TestResolveProtocolAuto(t *testing.T) {
	tests := []struct {
		model string
		want  Protocol
	}{
		{"gemini-3.7-flash", ProtocolGemini},
		{"gpt-5.6-sol", ProtocolOpenAIResponses},
		{"claude-opus-5", ProtocolAnthropic},
		{"deepseek-v4-flash", ProtocolOpenAIChat},
	}
	for _, test := range tests {
		got, err := ResolveProtocol(&RuntimeConfig{Provider: "linkapi", Protocol: "auto"}, test.model)
		if err != nil || got != test.want {
			t.Fatalf("model %q protocol=%q err=%v want=%q", test.model, got, err, test.want)
		}
	}
}

func TestGeminiNativeDropsTerminalAssistantPrefill(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v1beta/models/gemini-3.7-flash:generateContent") {
			t.Fatalf("path=%q", r.URL.Path)
		}
		if r.Header.Get("x-goog-api-key") != "secret" {
			t.Fatalf("missing Gemini key")
		}
		var payload struct {
			Contents []geminiContent `json:"contents"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if len(payload.Contents) != 1 || payload.Contents[0].Role != "user" {
			t.Fatalf("contents=%+v; terminal model prefill was not removed", payload.Contents)
		}
		_, _ = io.WriteString(w, `{"responseId":"g1","modelVersion":"gemini-3.7-flash","candidates":[{"content":{"role":"model","parts":[{"text":"{\"ok\":true}"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":4}}`)
	}))
	defer server.Close()

	client := NewClient(config.AnthropicConfig{APIKey: "secret"})
	resp, err := client.Complete(context.Background(), &Request{
		Model: "gemini-3.7-flash", MaxTokens: 20,
		Messages: []Message{{Role: "user", Content: "json"}, {Role: "assistant", Content: "{"}},
		Runtime:  &RuntimeConfig{BaseURL: server.URL, Provider: "linkapi", Protocol: "auto"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Protocol != string(ProtocolGemini) || resp.Text() != `{"ok":true}` || !resp.ModelObserved {
		t.Fatalf("response=%+v", resp)
	}
}

func TestOpenAIResponsesUsesResponsesProtocol(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			t.Fatalf("path=%q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Fatalf("authorization=%q", r.Header.Get("Authorization"))
		}
		var payload openAIResponsesRequest
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if len(payload.Input) != 1 || payload.Input[0].Role != "user" {
			t.Fatalf("input=%+v", payload.Input)
		}
		if payload.Reasoning == nil || payload.Reasoning.Effort != "high" {
			t.Fatalf("reasoning=%+v", payload.Reasoning)
		}
		_, _ = io.WriteString(w, `{"id":"r1","model":"gpt-5.6-sol","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"{\"approved\":true}"}]}],"usage":{"input_tokens":5,"output_tokens":6}}`)
	}))
	defer server.Close()

	client := NewClient(config.AnthropicConfig{APIKey: "secret"})
	resp, err := client.Complete(context.Background(), &Request{
		Model: "gpt-5.6-sol", MaxTokens: 20,
		Messages: []Message{{Role: "user", Content: "review"}, {Role: "assistant", Content: "{"}},
		Runtime:  &RuntimeConfig{BaseURL: server.URL, Provider: "linkapi", ReasoningEffort: "high"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Protocol != string(ProtocolOpenAIResponses) || resp.Text() != `{"approved":true}` || !resp.ModelObserved {
		t.Fatalf("response=%+v", resp)
	}
}

func TestOpenAIChatAutoForOtherLinkAPIModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path=%q", r.URL.Path)
		}
		var payload openAIChatRequest
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload.ReasoningEffort != "low" {
			t.Fatalf("reasoning_effort=%q", payload.ReasoningEffort)
		}
		_, _ = io.WriteString(w, `{"id":"c1","model":"deepseek-v4-flash","choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`)
	}))
	defer server.Close()

	client := NewClient(config.AnthropicConfig{APIKey: "secret"})
	resp, err := client.Complete(context.Background(), &Request{
		Model: "deepseek-v4-flash", Messages: []Message{{Role: "user", Content: "hi"}},
		Runtime: &RuntimeConfig{BaseURL: server.URL, Provider: "linkapi", ReasoningEffort: "low"},
	})
	if err != nil || resp.Text() != "ok" || !resp.ModelObserved {
		t.Fatalf("response=%+v err=%v", resp, err)
	}
}

func TestAnthropicMessagesMarksReturnedModelObserved(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"id":"a1","type":"message","role":"assistant","model":"claude-returned","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
	}))
	defer server.Close()

	client := NewClient(config.AnthropicConfig{APIKey: "secret"})
	resp, err := client.Complete(context.Background(), &Request{
		Model: "claude-requested", Messages: []Message{{Role: "user", Content: "hi"}},
		Runtime: &RuntimeConfig{BaseURL: server.URL, Provider: "anthropic", Protocol: "anthropic-messages"},
	})
	if err != nil || resp.Model != "claude-returned" || !resp.ModelObserved {
		t.Fatalf("response=%+v err=%v", resp, err)
	}
}

func TestProtocolParsersKeepCompatibilityModelButMarkItUnobserved(t *testing.T) {
	tests := []struct {
		name      string
		model     string
		protocol  string
		body      string
		wantModel string
	}{
		{
			name: "anthropic", model: "claude-requested", protocol: "anthropic-messages",
			body: `{"id":"a1","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`,
		},
		{
			name: "gemini", model: "gemini-requested", protocol: "gemini-native", wantModel: "gemini-requested",
			body: `{"responseId":"g1","candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`,
		},
		{
			name: "openai responses", model: "gpt-requested", protocol: "openai-responses", wantModel: "gpt-requested",
			body: `{"id":"r1","status":"completed","output_text":"ok"}`,
		},
		{
			name: "openai chat", model: "chat-requested", protocol: "openai-chat", wantModel: "chat-requested",
			body: `{"id":"c1","choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, test.body)
			}))
			defer server.Close()

			client := NewClient(config.AnthropicConfig{APIKey: "secret"})
			resp, err := client.Complete(context.Background(), &Request{
				Model: test.model, Messages: []Message{{Role: "user", Content: "hi"}},
				Runtime: &RuntimeConfig{BaseURL: server.URL, Provider: "fixture", Protocol: test.protocol},
			})
			if err != nil {
				t.Fatal(err)
			}
			if resp.Model != test.wantModel || resp.ModelObserved {
				t.Fatalf("model=%q observed=%v want_model=%q", resp.Model, resp.ModelObserved, test.wantModel)
			}
		})
	}
}
