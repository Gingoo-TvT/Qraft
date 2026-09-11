package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/config"
	"github.com/rs/zerolog/log"
)

const (
	// defaultAnthropicAPIURL is the base URL for the Anthropic Messages API.
	// Can be overridden via config to support third-party proxies (CC Switch, etc.).
	defaultAnthropicAPIURL = "https://api.anthropic.com/v1/messages"

	// anthropicAPIVersion is the API version header value.
	anthropicAPIVersion = "2023-06-01"

	// defaultMaxTokens is the default maximum number of tokens to generate
	// when the caller does not specify one.
	defaultMaxTokens = 4096

	// defaultTimeout is the HTTP client timeout for non-streaming requests.
	// Code-generation calls can take several minutes on Anthropic-compatible
	// proxy providers because response headers may not arrive until generation
	// finishes.
	defaultTimeout = 30 * time.Minute

	// defaultStreamTimeout is the HTTP client timeout for streaming requests.
	defaultStreamTimeout = 30 * time.Minute

	// Provider responses are persisted into governed CAS artifacts. Capping the
	// in-memory envelope prevents a malformed endpoint from exhausting a worker.
	maxProviderResponseBytes = 16 << 20
)

// configuredHTTPTimeout returns the per-request transport timeout. It accepts
// Go duration syntax (for example, 30m) or a positive number of seconds.
// An invalid value falls back to the safe 30-minute default and is logged.
func configuredHTTPTimeout() time.Duration {
	raw := strings.TrimSpace(os.Getenv("ALGOFORGE_LLM_REQUEST_TIMEOUT"))
	if raw == "" {
		return defaultTimeout
	}
	if duration, err := time.ParseDuration(raw); err == nil && duration > 0 {
		return duration
	}
	if seconds, err := strconv.ParseInt(raw, 10, 64); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	log.Warn().Str("value", raw).Msg("invalid LLM request timeout; using default")
	return defaultTimeout
}

// Client dispatches requests to Anthropic Messages, Gemini Native, OpenAI
// Responses, or OpenAI-compatible Chat Completions per request.
type Client struct {
	apiKey          string
	baseURL         string
	model           string
	reasoningEffort string
	httpClient      *http.Client
	apiKeyResolver  APIKeyResolver
}

// NewClient creates a protocol-dispatching client. AnthropicConfig remains the
// deployment fallback for backwards compatibility; runtime settings may route
// an individual request to another protocol.
func NewClient(cfg config.AnthropicConfig) *Client {
	return &Client{
		apiKey:          cfg.APIKey,
		baseURL:         strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/"),
		model:           cfg.Model,
		reasoningEffort: strings.ToLower(strings.TrimSpace(cfg.ReasoningEffort)),
		httpClient: &http.Client{
			Timeout: configuredHTTPTimeout(),
		},
	}
}

func (c *Client) SetAPIKeyResolver(resolver APIKeyResolver) {
	c.apiKeyResolver = resolver
}

// Complete sends a prompt using the selected protocol and normalizes the
// provider response into Response.
//
// If req.Model is empty the client's default model is used.  If
// req.MaxTokens is zero the default (4096) is applied.
func (c *Client) Complete(ctx context.Context, req *Request) (*Response, error) {
	if req != nil && req.Runtime == nil && strings.TrimSpace(c.reasoningEffort) != "" {
		req.Runtime = &RuntimeConfig{ReasoningEffort: c.reasoningEffort}
	}
	// Apply defaults.
	if req.Model == "" {
		req.Model = c.model
	}
	if req.MaxTokens == 0 {
		req.MaxTokens = defaultMaxTokens
	}

	// Force non-streaming for this method.
	req.Stream = false

	baseURL, apiKey, err := c.requestTransport(ctx, req)
	if err != nil {
		return nil, err
	}
	protocol, err := ResolveProtocol(req.Runtime, req.Model)
	if err != nil {
		return nil, err
	}
	log.Debug().
		Str("model", req.Model).
		Str("protocol", string(protocol)).
		Msg("sending LLM API request")
	return c.completeProtocol(ctx, req, baseURL, apiKey, protocol)
}

// CompleteStream sends a prompt to Claude with streaming enabled and calls
// onChunk for each text delta received.  The final assembled Response is
// returned when the stream ends.
func (c *Client) CompleteStream(ctx context.Context, req *Request, onChunk func(text string)) (*Response, error) {
	if req != nil && req.Runtime == nil && strings.TrimSpace(c.reasoningEffort) != "" {
		req.Runtime = &RuntimeConfig{ReasoningEffort: c.reasoningEffort}
	}
	if req.Model == "" {
		req.Model = c.model
	}
	if req.MaxTokens == 0 {
		req.MaxTokens = defaultMaxTokens
	}

	req.Stream = true

	baseURL, apiKey, err := c.requestTransport(ctx, req)
	if err != nil {
		return nil, err
	}
	protocol, err := ResolveProtocol(req.Runtime, req.Model)
	if err != nil {
		return nil, err
	}
	if protocol != ProtocolAnthropic {
		return nil, fmt.Errorf("streaming is not implemented for protocol %q", protocol)
	}
	endpoint := normalizeMessagesURL(baseURL)

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshalling request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating http request: %w", err)
	}
	c.setHeaders(httpReq, apiKey)

	// Use a longer timeout for streaming.
	streamClient := &http.Client{Timeout: configuredHTTPTimeout()}
	resp, err := streamClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("executing streaming http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, c.parseErrorResponse(resp)
	}

	return c.consumeStream(resp.Body, onChunk)
}

// CompleteWithRetry wraps Complete with exponential backoff retry logic.
// It retries on transient errors (5xx, 429) up to maxRetries times.  Rate
// limit errors respect the Retry-After header when present.
func (c *Client) CompleteWithRetry(ctx context.Context, req *Request, maxRetries int) (*Response, error) {
	var lastErr error

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			backoff := c.calculateBackoff(attempt, lastErr)

			log.Warn().
				Int("attempt", attempt).
				Dur("backoff", backoff).
				Err(lastErr).
				Msg("retrying LLM api request")

			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("context cancelled during retry backoff: %w", ctx.Err())
			case <-time.After(backoff):
			}
		}

		resp, err := c.Complete(ctx, req)
		if err == nil {
			// Provider response bodies are not allowed to self-report retry
			// usage. CompleteWithRetry owns this audit field and overwrites it
			// with the actual number of preceding retryable failures.
			resp.NetworkRetries = attempt
			if attempt > 0 {
				log.Info().
					Int("attempt", attempt).
					Msg("LLM api request succeeded after retry")
			}
			return resp, nil
		}

		// Check if the error is retryable.
		if apiErr, ok := err.(*APIError); ok && !apiErr.IsRetryable() {
			return nil, err
		}
		if _, ok := err.(*RateLimitError); ok {
			lastErr = err
			continue
		}
		if apiErr, ok := err.(*APIError); ok && apiErr.IsRetryable() {
			lastErr = err
			continue
		}

		// Non-API errors (network issues, etc.) are also retryable.
		lastErr = err
	}

	return nil, fmt.Errorf("LLM api request failed after %d retries: %w", maxRetries, lastErr)
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

func normalizeMessagesURL(baseURL string) string {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return defaultAnthropicAPIURL
	}
	baseURL = strings.TrimRight(baseURL, "/")
	if strings.HasSuffix(baseURL, "/v1/messages") {
		return baseURL
	}
	if strings.HasSuffix(baseURL, "/v1") {
		return baseURL + "/messages"
	}
	return baseURL + "/v1/messages"
}

func (c *Client) requestTransport(ctx context.Context, req *Request) (string, string, error) {
	endpoint := c.baseURL
	apiKey := c.apiKey
	if req != nil && req.Runtime != nil {
		if strings.TrimSpace(req.Runtime.BaseURL) != "" {
			endpoint = strings.TrimRight(strings.TrimSpace(req.Runtime.BaseURL), "/")
		}
		if strings.TrimSpace(req.Runtime.APIKeyRef) != "" {
			resolved, err := c.resolveAPIKeyRef(ctx, req.Runtime.APIKeyRef)
			if err != nil {
				return "", "", err
			}
			apiKey = resolved
		}
	}
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return "", "", fmt.Errorf("LLM api key is required")
	}
	return endpoint, apiKey, nil
}

func (c *Client) resolveAPIKeyRef(ctx context.Context, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	const envPrefix = "env:"
	if strings.HasPrefix(ref, envPrefix) {
		name := strings.TrimPrefix(ref, envPrefix)
		value := strings.TrimSpace(os.Getenv(name))
		if value == "" {
			return "", fmt.Errorf("LLM api_key_ref %q resolved to an empty environment variable", ref)
		}
		return value, nil
	}
	if c.apiKeyResolver == nil {
		return "", fmt.Errorf("unsupported LLM api_key_ref %q: use env:NAME or configure a runtime key resolver", ref)
	}
	return c.apiKeyResolver.ResolveAPIKey(ctx, ref)
}

// setHeaders applies the required Anthropic API headers to the request.
func (c *Client) setHeaders(req *http.Request, apiKey string) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", anthropicAPIVersion)
}

// handleResponse reads and parses the HTTP response body.  On success it
// returns the parsed Response; on error it returns an appropriate APIError or
// RateLimitError.
func (c *Client) handleResponse(resp *http.Response) (*Response, error) {
	if resp.StatusCode != http.StatusOK {
		return nil, c.parseErrorResponse(resp)
	}

	respBody, truncated, err := readProviderResponse(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}
	if truncated {
		return nil, &ResponseDecodeError{Body: respBody, Truncated: true}
	}

	var result Response
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, &ResponseDecodeError{Cause: err, Body: respBody}
	}
	result.Protocol = string(ProtocolAnthropic)
	result.ModelObserved = strings.TrimSpace(result.Model) != ""

	log.Debug().
		Str("id", result.ID).
		Str("stop_reason", result.StopReason).
		Int("input_tokens", result.Usage.InputTokens).
		Int("output_tokens", result.Usage.OutputTokens).
		Msg("LLM api response received")

	return &result, nil
}

// parseErrorResponse converts a non-200 HTTP response into the appropriate
// error type.
func (c *Client) parseErrorResponse(resp *http.Response) error {
	respBody, truncated, err := readProviderResponse(resp.Body)
	if err != nil {
		return &APIError{
			StatusCode: resp.StatusCode,
			Type:       "read_error",
			Message:    fmt.Sprintf("failed to read error response body: %v", err),
		}
	}
	if truncated {
		return &APIError{
			StatusCode:                resp.StatusCode,
			Type:                      "response_too_large",
			Message:                   "provider error response exceeded the configured body limit",
			responseEvidence:          respBody,
			responseEvidenceTruncated: true,
		}
	}

	var errResp apiErrorResponse
	if err := json.Unmarshal(respBody, &errResp); err != nil {
		return &APIError{
			StatusCode:       resp.StatusCode,
			Type:             "parse_error",
			Message:          "provider returned an unparseable error response",
			responseEvidence: respBody,
		}
	}

	apiErr := APIError{
		StatusCode:       resp.StatusCode,
		Type:             errResp.Error.Type,
		Message:          errResp.Error.Message,
		responseEvidence: respBody,
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		rateLimitErr := &RateLimitError{APIError: apiErr}
		if retryAfter := resp.Header.Get("Retry-After"); retryAfter != "" {
			if seconds, err := strconv.ParseFloat(retryAfter, 64); err == nil {
				rateLimitErr.RetryAfterSeconds = seconds
			}
		}
		return rateLimitErr
	}

	return &apiErr
}

func readProviderResponse(body io.Reader) ([]byte, bool, error) {
	data, err := io.ReadAll(io.LimitReader(body, maxProviderResponseBytes+1))
	if err != nil {
		return nil, false, err
	}
	if len(data) > maxProviderResponseBytes {
		return data[:maxProviderResponseBytes], true, nil
	}
	return data, false, nil
}

// calculateBackoff returns the duration to wait before the next retry.
// For rate limit errors with a Retry-After header, that value is used.
// Otherwise exponential backoff with jitter is applied.
func (c *Client) calculateBackoff(attempt int, lastErr error) time.Duration {
	// Honour Retry-After from rate limit errors.
	if rlErr, ok := lastErr.(*RateLimitError); ok && rlErr.RetryAfterSeconds > 0 {
		return time.Duration(rlErr.RetryAfterSeconds*1000) * time.Millisecond
	}

	// Exponential backoff: 1s, 2s, 4s, 8s, ... capped at 60s.
	backoffSeconds := math.Min(math.Pow(2, float64(attempt-1)), 60)
	return time.Duration(backoffSeconds) * time.Second
}

// ---------------------------------------------------------------------------
// Streaming helpers
// ---------------------------------------------------------------------------

// streamEvent represents a single server-sent event from the streaming API.
type streamEvent struct {
	Type  string `json:"type"`
	Index int    `json:"index,omitempty"`
	Delta *struct {
		Type       string `json:"type,omitempty"`
		Text       string `json:"text,omitempty"`
		StopReason string `json:"stop_reason,omitempty"`
	} `json:"delta,omitempty"`
	Message *Response `json:"message,omitempty"`
	Usage   *Usage    `json:"usage,omitempty"`
}

// consumeStream reads a server-sent event stream and assembles the final
// response, calling onChunk for each text delta.
func (c *Client) consumeStream(body io.Reader, onChunk func(string)) (*Response, error) {
	decoder := json.NewDecoder(body)
	raw := make([]byte, 0, 4096)

	var assembled Response
	var fullText string

	// Read the raw body and parse SSE lines.
	buf, err := io.ReadAll(body)
	if err != nil {
		return nil, fmt.Errorf("reading stream body: %w", err)
	}
	_ = decoder // We switch to manual SSE parsing below.

	raw = append(raw, buf...)

	// Parse server-sent events from the raw bytes.
	lines := bytes.Split(raw, []byte("\n"))
	for _, line := range lines {
		line = bytes.TrimSpace(line)
		if !bytes.HasPrefix(line, []byte("data: ")) {
			continue
		}
		data := bytes.TrimPrefix(line, []byte("data: "))
		if string(data) == "[DONE]" {
			break
		}

		var event streamEvent
		if err := json.Unmarshal(data, &event); err != nil {
			log.Warn().Err(err).Str("data", string(data)).Msg("failed to parse stream event")
			continue
		}

		switch event.Type {
		case "message_start":
			if event.Message != nil {
				assembled.ID = event.Message.ID
				assembled.Type = event.Message.Type
				assembled.Role = event.Message.Role
				assembled.Model = event.Message.Model
				assembled.ModelObserved = strings.TrimSpace(event.Message.Model) != ""
				assembled.Usage = event.Message.Usage
			}
		case "content_block_delta":
			if event.Delta != nil && event.Delta.Text != "" {
				fullText += event.Delta.Text
				if onChunk != nil {
					onChunk(event.Delta.Text)
				}
			}
		case "message_delta":
			if event.Delta != nil && event.Delta.StopReason != "" {
				assembled.StopReason = event.Delta.StopReason
			}
			if event.Usage != nil {
				assembled.Usage.OutputTokens = event.Usage.OutputTokens
			}
		}
	}

	assembled.Content = []ContentBlock{{Type: "text", Text: fullText}}
	return &assembled, nil
}
