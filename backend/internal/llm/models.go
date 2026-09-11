package llm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
)

// ---------------------------------------------------------------------------
// Request / Response types for the Anthropic Messages API
// ---------------------------------------------------------------------------

// Request represents a request to the Anthropic Messages API.
type Request struct {
	// Model identifier (e.g. "claude-sonnet-4-20250514").
	Model string `json:"model"`

	// Maximum number of tokens to generate.
	MaxTokens int `json:"max_tokens"`

	// System prompt (optional).
	System string `json:"system,omitempty"`

	// Conversation messages.
	Messages []Message `json:"messages"`

	// Sampling temperature in [0, 1].  Lower values produce more
	// deterministic output.
	Temperature *float64 `json:"temperature,omitempty"`

	// Whether to use streaming mode.
	Stream bool `json:"stream,omitempty"`

	// Stop sequences that cause the model to stop generating.
	StopSequences []string `json:"stop_sequences,omitempty"`

	// Runtime carries local routing and credential metadata. It is deliberately
	// excluded from provider JSON bodies so key references never reach the LLM
	// endpoint as arbitrary request fields.
	Runtime *RuntimeConfig `json:"-"`

	// PromptID and PromptVersion identify the server-owned prompt contract.
	// They are excluded from provider JSON and are bound into provenance.
	PromptID      string `json:"-"`
	PromptVersion string `json:"-"`
}

// RuntimeConfig carries per-request provider routing. APIKeyRef currently
// supports env:NAME references; raw API keys are intentionally unsupported.
type RuntimeConfig struct {
	APIKeyRef       string
	BaseURL         string
	Provider        string
	Protocol        string
	ReasoningEffort string
}

type APIKeyResolver interface {
	ResolveAPIKey(context.Context, string) (string, error)
}

// Message represents a single message in the conversation.
type Message struct {
	// Role is either "user" or "assistant".
	Role string `json:"role"`

	// Content is the text payload of the message.
	Content string `json:"content"`
}

// Response represents the parsed response from the Anthropic Messages API.
type Response struct {
	// Unique identifier assigned by the API.
	ID string `json:"id"`

	// Type of the response object (e.g. "message").
	Type string `json:"type"`

	// Role of the responder (always "assistant").
	Role string `json:"role"`

	// Protocol records the wire protocol used for this normalized response.
	Protocol string `json:"protocol,omitempty"`

	// Content blocks returned by the model.
	Content []ContentBlock `json:"content"`

	// Model identifier that handled the request.
	Model string `json:"model"`

	// ModelObserved reports whether the provider response explicitly carried
	// Model. Model may still contain the requested-model compatibility fallback.
	ModelObserved bool `json:"model_observed,omitempty"`

	// Reason the model stopped generating.  Common values: "end_turn",
	// "max_tokens", "stop_sequence".
	StopReason string `json:"stop_reason"`

	// Token usage statistics.
	Usage Usage `json:"usage"`

	// NetworkRetries is the number of retryable provider attempts completed
	// before this successful response. It is server-authored by
	// CompleteWithRetry and is never trusted from a provider response.
	NetworkRetries int `json:"network_retries,omitempty"`
}

// Text returns the concatenated text of all text-type content blocks.
func (r *Response) Text() string {
	var text string
	for _, block := range r.Content {
		if block.Type == "text" {
			text += block.Text
		}
	}
	return text
}

// ContentBlock represents a single block within the response content array.
type ContentBlock struct {
	// Type of the content block (e.g. "text").
	Type string `json:"type"`

	// Text payload (present when Type == "text").
	Text string `json:"text,omitempty"`
}

// Usage reports the number of tokens consumed by the request and response.
type Usage struct {
	// Number of tokens in the input (prompt).
	InputTokens int `json:"input_tokens"`

	// Number of tokens generated in the output.
	OutputTokens int `json:"output_tokens"`
}

// ---------------------------------------------------------------------------
// Error types
// ---------------------------------------------------------------------------

// APIError represents a non-retryable error returned by the Anthropic API.
type APIError struct {
	// HTTP status code.
	StatusCode int `json:"status_code"`

	// Error type string from the API (e.g. "invalid_request_error").
	Type string `json:"type"`

	// Human-readable error message.
	Message string `json:"message"`

	// Protocol identifies the provider wire protocol that returned the error.
	Protocol string `json:"protocol,omitempty"`

	responseEvidence          []byte
	responseEvidenceTruncated bool
}

// Error implements the error interface.
func (e *APIError) Error() string {
	protocol := e.Protocol
	if protocol == "" {
		protocol = "provider"
	}
	return fmt.Sprintf("%s api error (status %d, type %q): %s",
		protocol, e.StatusCode, e.Type, e.Message)
}

// IsRetryable reports whether the error is potentially transient and the
// request may succeed if retried.
func (e *APIError) IsRetryable() bool {
	switch e.StatusCode {
	case http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

// ProviderResponseEvidence exposes bounded response bytes for quarantined
// provenance recording. Error strings deliberately never include these bytes.
type ProviderResponseEvidence interface {
	ProviderResponseEvidence() (body []byte, truncated bool)
}

func (e *APIError) ProviderResponseEvidence() ([]byte, bool) {
	if e == nil {
		return nil, false
	}
	return append([]byte(nil), e.responseEvidence...), e.responseEvidenceTruncated
}

// ResponseDecodeError retains a bounded provider response that could not be
// decoded. This lets the workflow persist evidence before returning failure.
type ResponseDecodeError struct {
	Cause     error
	Body      []byte
	Truncated bool
}

func (e *ResponseDecodeError) Error() string {
	if e == nil {
		return "provider response decode failed"
	}
	if e.Truncated {
		return "provider response exceeded the configured body limit"
	}
	return fmt.Sprintf("decoding provider response: %v", e.Cause)
}

func (e *ResponseDecodeError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func (e *ResponseDecodeError) ProviderResponseEvidence() ([]byte, bool) {
	if e == nil {
		return nil, false
	}
	return append([]byte(nil), e.Body...), e.Truncated
}

func ResponseEvidence(err error) (body []byte, truncated bool, ok bool) {
	var evidence ProviderResponseEvidence
	if !errors.As(err, &evidence) {
		return nil, false, false
	}
	body, truncated = evidence.ProviderResponseEvidence()
	return body, truncated, len(body) > 0 || truncated
}

// RateLimitError is a specialised APIError returned when the caller exceeds
// the Anthropic API rate limit (HTTP 429).
type RateLimitError struct {
	APIError

	// RetryAfterSeconds is the number of seconds the caller should wait
	// before retrying, as indicated by the Retry-After header.  Zero means
	// the header was absent.
	RetryAfterSeconds float64 `json:"retry_after_seconds,omitempty"`
}

// Error implements the error interface.
func (e *RateLimitError) Error() string {
	protocol := e.Protocol
	if protocol == "" {
		protocol = "provider"
	}
	if e.RetryAfterSeconds > 0 {
		return fmt.Sprintf("%s rate limit exceeded (retry after %.1fs): %s",
			protocol, e.RetryAfterSeconds, e.Message)
	}
	return fmt.Sprintf("%s rate limit exceeded: %s", protocol, e.Message)
}

// apiErrorResponse is the raw JSON structure returned by the Anthropic API
// when an error occurs.
type apiErrorResponse struct {
	Type  string `json:"type"`
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}
