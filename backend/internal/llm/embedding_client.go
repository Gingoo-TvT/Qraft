package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Gingoo-TvT/Qraft/backend/internal/config"
)

// Embedder converts text into vector embeddings.
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
}

// EmbeddingAPIKeyResolver supplies the credential for the configured endpoint
// at call time. This lets an API deployment persist a rotated key without
// requiring the Temporal worker process to restart.
type EmbeddingAPIKeyResolver interface {
	ResolveEmbeddingAPIKey(ctx context.Context, baseURL, model string, dimensions int) (string, error)
}

// baseBackoff is retry base interval; tests can override EmbeddingClient.baseBackoff.
const defaultBaseBackoff = 500 * time.Millisecond

// ErrEmbeddingDisabled indicates embedding calls are intentionally disabled.
var ErrEmbeddingDisabled = fmt.Errorf("embedding disabled")

// EmbeddingClient calls an OpenAI-compatible /v1/embeddings API.
type EmbeddingClient struct {
	apiKey      string
	baseURL     string
	model       string
	dimensions  int
	http        *http.Client
	baseBackoff time.Duration
	keyResolver EmbeddingAPIKeyResolver
}

type embedReqBody struct {
	Model          string   `json:"model"`
	Input          []string `json:"input"`
	Dimensions     int      `json:"dimensions"`
	EncodingFormat string   `json:"encoding_format"`
}

type embedRespItem struct {
	Index     int       `json:"index"`
	Embedding []float32 `json:"embedding"`
}

type embedRespBody struct {
	Data  []embedRespItem `json:"data"`
	Model string          `json:"model"`
	Usage struct {
		PromptTokens int `json:"prompt_tokens"`
		TotalTokens  int `json:"total_tokens"`
	} `json:"usage"`
}

// NewEmbeddingClient creates an OpenAI-compatible embedding client.
func NewEmbeddingClient(cfg config.EmbeddingConfig) (*EmbeddingClient, error) {
	return newEmbeddingClient(cfg, nil)
}

// NewEmbeddingClientWithResolver creates a client whose credential may come
// from an encrypted persistent settings store. The deployment key remains a
// fallback when the store has no matching runtime record.
func NewEmbeddingClientWithResolver(
	cfg config.EmbeddingConfig,
	resolver EmbeddingAPIKeyResolver,
) (*EmbeddingClient, error) {
	if resolver == nil {
		return nil, fmt.Errorf("embedding: api key resolver is required")
	}
	return newEmbeddingClient(cfg, resolver)
}

func newEmbeddingClient(
	cfg config.EmbeddingConfig,
	resolver EmbeddingAPIKeyResolver,
) (*EmbeddingClient, error) {
	if cfg.APIKey == "" && resolver == nil {
		return nil, fmt.Errorf("embedding: api_key is required")
	}
	if cfg.Dimensions != 1536 {
		return nil, fmt.Errorf("embedding: only dimensions=1536 supported (got %d)", cfg.Dimensions)
	}
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("embedding: base_url is required")
	}
	if cfg.Model == "" {
		return nil, fmt.Errorf("embedding: model is required")
	}

	timeout := time.Duration(cfg.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	return &EmbeddingClient{
		apiKey:      cfg.APIKey,
		baseURL:     strings.TrimRight(cfg.BaseURL, "/"),
		model:       cfg.Model,
		dimensions:  cfg.Dimensions,
		http:        &http.Client{Timeout: timeout},
		baseBackoff: defaultBaseBackoff,
		keyResolver: resolver,
	}, nil
}

// NewNoopEmbedder returns an Embedder that always returns ErrEmbeddingDisabled.
// Callers should treat this as "feature disabled" and skip deduplication.
func NewNoopEmbedder() Embedder { return &noopEmbedder{} }

type noopEmbedder struct{}

func (n *noopEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	return nil, ErrEmbeddingDisabled
}

func (n *noopEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	return nil, ErrEmbeddingDisabled
}

// Embed embeds a single text.
func (c *EmbeddingClient) Embed(ctx context.Context, text string) ([]float32, error) {
	vecs, err := c.EmbedBatch(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	return vecs[0], nil
}

// EmbedBatch embeds a batch of texts.
func (c *EmbeddingClient) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, fmt.Errorf("embedding: empty input")
	}
	apiKey := strings.TrimSpace(c.apiKey)
	if c.keyResolver != nil {
		resolved, err := c.keyResolver.ResolveEmbeddingAPIKey(ctx, c.baseURL, c.model, c.dimensions)
		if err != nil {
			return nil, fmt.Errorf("embedding: resolve api key: %w", err)
		}
		apiKey = strings.TrimSpace(resolved)
	}
	if apiKey == "" {
		return nil, fmt.Errorf("embedding: api_key is required")
	}

	cleaned := make([]string, len(texts))
	for i, t := range texts {
		cleaned[i] = truncateUTF8(t, 24000)
	}

	body, err := json.Marshal(embedReqBody{
		Model:          c.model,
		Input:          cleaned,
		Dimensions:     c.dimensions,
		EncodingFormat: "float",
	})
	if err != nil {
		return nil, fmt.Errorf("embedding: marshal request: %w", err)
	}

	var lastErr error
	const maxAttempts = 3
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			wait := c.baseBackoff * (1 << uint(attempt-1))
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(wait):
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/embeddings", bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+apiKey)
		req.Header.Set("Content-Type", "application/json")

		resp, err := c.http.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
			lastErr = fmt.Errorf("embedding api retryable status=%d body=%s", resp.StatusCode, string(respBody))
			continue
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("embedding api status=%d body=%s", resp.StatusCode, string(respBody))
		}

		var er embedRespBody
		if err := json.Unmarshal(respBody, &er); err != nil {
			return nil, fmt.Errorf("embedding api decode: %w", err)
		}
		if strings.TrimSpace(er.Model) != "" && er.Model != c.model {
			return nil, fmt.Errorf("embedding api model mismatch got %q want %q", er.Model, c.model)
		}
		out, err := validateAndNormalizeEmbeddings(er.Data, len(texts), c.dimensions)
		if err != nil {
			return nil, fmt.Errorf("embedding api: %w", err)
		}
		return out, nil
	}

	return nil, fmt.Errorf("embedding api failed after %d attempts: %w", maxAttempts, lastErr)
}

func validateAndNormalizeEmbeddings(items []embedRespItem, expected, dimensions int) ([][]float32, error) {
	out := make([][]float32, expected)
	seen := make([]bool, expected)
	for _, item := range items {
		if item.Index < 0 || item.Index >= expected {
			return nil, fmt.Errorf("invalid index %d", item.Index)
		}
		if seen[item.Index] {
			return nil, fmt.Errorf("duplicate index %d", item.Index)
		}

		embedding, err := truncateAndNormalizeEmbedding(item.Embedding, dimensions)
		if err != nil {
			return nil, fmt.Errorf("index %d: %w", item.Index, err)
		}
		out[item.Index] = embedding
		seen[item.Index] = true
	}

	for index, present := range seen {
		if !present {
			return nil, fmt.Errorf("missing index %d", index)
		}
	}
	return out, nil
}

func truncateAndNormalizeEmbedding(embedding []float32, dimensions int) ([]float32, error) {
	if len(embedding) < dimensions {
		return nil, fmt.Errorf("dim mismatch want at least %d got %d", dimensions, len(embedding))
	}

	var squaredNorm float64
	for index, component := range embedding {
		value := float64(component)
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return nil, fmt.Errorf("non-finite component at dimension %d", index)
		}
		if index < dimensions {
			squaredNorm += value * value
		}
	}
	if squaredNorm == 0 {
		return nil, fmt.Errorf("zero norm after truncation to %d dimensions", dimensions)
	}

	norm := math.Sqrt(squaredNorm)
	out := make([]float32, dimensions)
	for index := range out {
		out[index] = float32(float64(embedding[index]) / norm)
	}
	return out, nil
}

// truncateUTF8 returns a string no longer than maxBytes without cutting a rune.
func truncateUTF8(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}
