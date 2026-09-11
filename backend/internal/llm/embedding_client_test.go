package llm

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Gingoo-TvT/Qraft/backend/internal/config"
)

func TestEmbedding_Normal(t *testing.T) {
	var seenAuth string
	var seenReq embedReqBody
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&seenReq); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		writeEmbeddingResponse(t, w, []int{1536})
	}))
	defer srv.Close()

	client := newTestEmbeddingClient(t, srv.URL)
	vec, err := client.Embed(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Embed returned error: %v", err)
	}
	if len(vec) != 1536 {
		t.Fatalf("len(vec) = %d, want 1536", len(vec))
	}
	if seenAuth != "Bearer test-key" {
		t.Fatalf("Authorization header = %q", seenAuth)
	}
	if seenReq.Model != "text-embedding-3-large" || seenReq.Dimensions != 1536 || seenReq.EncodingFormat != "float" {
		t.Fatalf("unexpected request body: %+v", seenReq)
	}
	if len(seenReq.Input) != 1 || seenReq.Input[0] != "hello" {
		t.Fatalf("unexpected input: %+v", seenReq.Input)
	}
}

type fixedEmbeddingKeyResolver struct {
	key string
	err error
}

func (r fixedEmbeddingKeyResolver) ResolveEmbeddingAPIKey(context.Context, string, string, int) (string, error) {
	return r.key, r.err
}

func TestEmbedding_UsesRuntimeResolvedAPIKey(t *testing.T) {
	var seenAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAuth = r.Header.Get("Authorization")
		writeEmbeddingResponse(t, w, []int{1536})
	}))
	defer srv.Close()

	client, err := NewEmbeddingClientWithResolver(config.EmbeddingConfig{
		BaseURL: srv.URL, Model: "text-embedding-3-large", Dimensions: 1536, TimeoutSec: 5,
	}, fixedEmbeddingKeyResolver{key: "stored-key"})
	if err != nil {
		t.Fatalf("NewEmbeddingClientWithResolver: %v", err)
	}
	if _, err := client.Embed(context.Background(), "hello"); err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if seenAuth != "Bearer stored-key" {
		t.Fatalf("Authorization header = %q, want stored key", seenAuth)
	}
}

func TestEmbedding_DimMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeEmbeddingResponse(t, w, []int{1024})
	}))
	defer srv.Close()

	client := newTestEmbeddingClient(t, srv.URL)
	_, err := client.Embed(context.Background(), "hello")
	if err == nil || !strings.Contains(err.Error(), "dim mismatch") {
		t.Fatalf("error = %v, want dim mismatch", err)
	}
}

func TestEmbedding_Truncates4096AndNormalizes1536(t *testing.T) {
	native := make([]float32, 4096)
	native[0] = 3
	native[1] = 4
	native[1536] = 100
	srv := newEmbeddingServer(t, []embedRespItem{{Index: 0, Embedding: native}})
	defer srv.Close()

	client := newTestEmbeddingClient(t, srv.URL)
	vec, err := client.Embed(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Embed returned error: %v", err)
	}
	if len(vec) != 1536 {
		t.Fatalf("len(vec) = %d, want 1536", len(vec))
	}
	if !closeFloat32(vec[0], 0.6, 1e-6) || !closeFloat32(vec[1], 0.8, 1e-6) {
		t.Fatalf("normalized prefix = [%g, %g], want [0.6, 0.8]", vec[0], vec[1])
	}
	if got := vectorNorm(vec); math.Abs(got-1) > 1e-6 {
		t.Fatalf("L2 norm = %.9f, want 1", got)
	}
}

func TestEmbedding_Normalizes1536Response(t *testing.T) {
	serverVector := make([]float32, 1536)
	serverVector[7] = 5
	serverVector[11] = 12
	srv := newEmbeddingServer(t, []embedRespItem{{Index: 0, Embedding: serverVector}})
	defer srv.Close()

	client := newTestEmbeddingClient(t, srv.URL)
	vec, err := client.Embed(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Embed returned error: %v", err)
	}
	if !closeFloat32(vec[7], 5.0/13.0, 1e-6) || !closeFloat32(vec[11], 12.0/13.0, 1e-6) {
		t.Fatalf("normalized components = [%g, %g]", vec[7], vec[11])
	}
	if got := vectorNorm(vec); math.Abs(got-1) > 1e-6 {
		t.Fatalf("L2 norm = %.9f, want 1", got)
	}
}

func TestEmbedding_BatchRestoresOutOfOrderIndexes(t *testing.T) {
	items := []embedRespItem{
		{Index: 2, Embedding: oneHotEmbedding(4096, 2, 9)},
		{Index: 0, Embedding: oneHotEmbedding(1536, 0, 3)},
		{Index: 1, Embedding: oneHotEmbedding(4096, 1, 7)},
	}
	srv := newEmbeddingServer(t, items)
	defer srv.Close()

	client := newTestEmbeddingClient(t, srv.URL)
	vectors, err := client.EmbedBatch(context.Background(), []string{"zero", "one", "two"})
	if err != nil {
		t.Fatalf("EmbedBatch returned error: %v", err)
	}
	for index, vector := range vectors {
		if len(vector) != 1536 || vector[index] != 1 {
			t.Fatalf("vector %d was not restored by response index", index)
		}
	}
}

func TestEmbedding_ResponseValidationFailsClosed(t *testing.T) {
	valid := oneHotEmbedding(1536, 0, 1)
	tests := []struct {
		name     string
		items    []embedRespItem
		expected int
		want     string
	}{
		{name: "short vector", items: []embedRespItem{{Index: 0, Embedding: make([]float32, 1535)}}, expected: 1, want: "dim mismatch"},
		{name: "zero norm", items: []embedRespItem{{Index: 0, Embedding: make([]float32, 1536)}}, expected: 1, want: "zero norm"},
		{name: "nan", items: []embedRespItem{{Index: 0, Embedding: embeddingWithComponent(math.NaN())}}, expected: 1, want: "non-finite"},
		{name: "positive infinity", items: []embedRespItem{{Index: 0, Embedding: embeddingWithComponent(math.Inf(1))}}, expected: 1, want: "non-finite"},
		{name: "negative infinity", items: []embedRespItem{{Index: 0, Embedding: embeddingWithComponent(math.Inf(-1))}}, expected: 1, want: "non-finite"},
		{name: "duplicate index", items: []embedRespItem{{Index: 0, Embedding: valid}, {Index: 0, Embedding: valid}}, expected: 2, want: "duplicate index 0"},
		{name: "missing index", items: []embedRespItem{{Index: 0, Embedding: valid}}, expected: 2, want: "missing index 1"},
		{name: "negative index", items: []embedRespItem{{Index: -1, Embedding: valid}}, expected: 1, want: "invalid index -1"},
		{name: "high index", items: []embedRespItem{{Index: 1, Embedding: valid}}, expected: 1, want: "invalid index 1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := validateAndNormalizeEmbeddings(tt.items, tt.expected, 1536)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestEmbedding_ResponseModelMismatchFailsClosed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeEmbeddingItemsWithModel(t, w, "unexpected-embedding-model", []embedRespItem{
			{Index: 0, Embedding: oneHotEmbedding(1536, 0, 1)},
		})
	}))
	defer srv.Close()

	client := newTestEmbeddingClient(t, srv.URL)
	_, err := client.Embed(context.Background(), "hello")
	if err == nil || !strings.Contains(err.Error(), "model mismatch") {
		t.Fatalf("error = %v, want model mismatch", err)
	}
}

func TestEmbedding_ClientAndServerMRLCosineParity(t *testing.T) {
	native := make([]float32, 4096)
	for index := range native {
		native[index] = float32((index%29)-14) / 17
	}
	serverMRL := referenceNormalize(native[:1536])

	nativeServer := newEmbeddingServer(t, []embedRespItem{{Index: 0, Embedding: native}})
	defer nativeServer.Close()
	serverMRLServer := newEmbeddingServer(t, []embedRespItem{{Index: 0, Embedding: serverMRL}})
	defer serverMRLServer.Close()

	clientMRL, err := newTestEmbeddingClient(t, nativeServer.URL).Embed(context.Background(), "fixed fixture")
	if err != nil {
		t.Fatalf("client MRL: %v", err)
	}
	serverSideMRL, err := newTestEmbeddingClient(t, serverMRLServer.URL).Embed(context.Background(), "fixed fixture")
	if err != nil {
		t.Fatalf("server MRL: %v", err)
	}
	if difference := math.Abs(1 - cosineSimilarity(clientMRL, serverSideMRL)); difference > 1e-5 {
		t.Fatalf("client/server MRL cosine difference = %.9g, want <= 1e-5", difference)
	}
}

func TestEmbedding_Auth(t *testing.T) {
	var requests int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requests, 1)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer srv.Close()

	client := newTestEmbeddingClient(t, srv.URL)
	_, err := client.Embed(context.Background(), "hello")
	if err == nil || !strings.Contains(err.Error(), "status=401") {
		t.Fatalf("error = %v, want status=401", err)
	}
	if got := atomic.LoadInt32(&requests); got != 1 {
		t.Fatalf("requests = %d, want 1", got)
	}
}

func TestEmbedding_Retry5xx(t *testing.T) {
	var requests int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requests, 1)
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	client := newTestEmbeddingClient(t, srv.URL)
	_, err := client.Embed(context.Background(), "hello")
	if err == nil || !strings.Contains(err.Error(), "failed after 3 attempts") {
		t.Fatalf("error = %v, want retry failure", err)
	}
	if got := atomic.LoadInt32(&requests); got != 3 {
		t.Fatalf("requests = %d, want 3", got)
	}
}

func TestEmbedding_Retry429(t *testing.T) {
	var requests int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&requests, 1) < 3 {
			http.Error(w, "rate limited", http.StatusTooManyRequests)
			return
		}
		writeEmbeddingResponse(t, w, []int{1536})
	}))
	defer srv.Close()

	client := newTestEmbeddingClient(t, srv.URL)
	vec, err := client.Embed(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Embed returned error: %v", err)
	}
	if len(vec) != 1536 {
		t.Fatalf("len(vec) = %d, want 1536", len(vec))
	}
	if got := atomic.LoadInt32(&requests); got != 3 {
		t.Fatalf("requests = %d, want 3", got)
	}
}

func TestEmbedding_CtxCancel(t *testing.T) {
	var requests int32
	ctx, cancel := context.WithCancel(context.Background())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requests, 1)
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		cancel()
	}))
	defer srv.Close()

	client := newTestEmbeddingClient(t, srv.URL)

	_, err := client.Embed(ctx, "hello")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if got := atomic.LoadInt32(&requests); got != 1 {
		t.Fatalf("requests = %d, want 1", got)
	}
}

func TestEmbedding_TruncateUTF8(t *testing.T) {
	input := strings.Repeat("测试", 100)
	out := truncateUTF8(input, 17)
	if len(out) > 17 {
		t.Fatalf("len(out) = %d, want <= 17", len(out))
	}
	if !utf8.ValidString(out) {
		t.Fatalf("output is not valid UTF-8")
	}
}

func TestEmbedding_NewEmbeddingClientValidation(t *testing.T) {
	valid := config.EmbeddingConfig{
		APIKey:     "test-key",
		BaseURL:    "http://example.test/v1",
		Model:      "text-embedding-3-large",
		Dimensions: 1536,
		TimeoutSec: 30,
	}

	tests := []struct {
		name string
		edit func(*config.EmbeddingConfig)
	}{
		{name: "api key", edit: func(c *config.EmbeddingConfig) { c.APIKey = "" }},
		{name: "dimensions", edit: func(c *config.EmbeddingConfig) { c.Dimensions = 3072 }},
		{name: "base url", edit: func(c *config.EmbeddingConfig) { c.BaseURL = "" }},
		{name: "model", edit: func(c *config.EmbeddingConfig) { c.Model = "" }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := valid
			tt.edit(&cfg)
			if _, err := NewEmbeddingClient(cfg); err == nil {
				t.Fatalf("NewEmbeddingClient returned nil error")
			}
		})
	}
}

func newTestEmbeddingClient(t *testing.T, baseURL string) *EmbeddingClient {
	t.Helper()
	client, err := NewEmbeddingClient(config.EmbeddingConfig{
		APIKey:     "test-key",
		BaseURL:    baseURL,
		Model:      "text-embedding-3-large",
		Dimensions: 1536,
		TimeoutSec: 5,
	})
	if err != nil {
		t.Fatalf("NewEmbeddingClient: %v", err)
	}
	client.baseBackoff = time.Millisecond
	return client
}

func writeEmbeddingResponse(t *testing.T, w http.ResponseWriter, dims []int) {
	t.Helper()
	resp := embedRespBody{Data: make([]embedRespItem, len(dims))}
	for i, dim := range dims {
		resp.Data[i] = embedRespItem{Index: i, Embedding: oneHotEmbedding(dim, 0, 1)}
	}
	writeEmbeddingItems(t, w, resp.Data)
}

func newEmbeddingServer(t *testing.T, items []embedRespItem) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeEmbeddingItems(t, w, items)
	}))
}

func writeEmbeddingItems(t *testing.T, w http.ResponseWriter, items []embedRespItem) {
	t.Helper()
	writeEmbeddingItemsWithModel(t, w, "", items)
}

func writeEmbeddingItemsWithModel(t *testing.T, w http.ResponseWriter, model string, items []embedRespItem) {
	t.Helper()
	resp := embedRespBody{Data: items, Model: model}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		t.Fatalf("encode response: %v", err)
	}
}

func oneHotEmbedding(dimensions, index int, value float32) []float32 {
	embedding := make([]float32, dimensions)
	if index >= 0 && index < dimensions {
		embedding[index] = value
	}
	return embedding
}

func embeddingWithComponent(value float64) []float32 {
	embedding := oneHotEmbedding(1536, 0, 1)
	embedding[7] = float32(value)
	return embedding
}

func vectorNorm(vector []float32) float64 {
	var squared float64
	for _, component := range vector {
		value := float64(component)
		squared += value * value
	}
	return math.Sqrt(squared)
}

func referenceNormalize(vector []float32) []float32 {
	norm := vectorNorm(vector)
	out := make([]float32, len(vector))
	for index, component := range vector {
		out[index] = float32(float64(component) / norm)
	}
	return out
}

func cosineSimilarity(left, right []float32) float64 {
	var dot float64
	for index := range left {
		dot += float64(left[index]) * float64(right[index])
	}
	return dot / (vectorNorm(left) * vectorNorm(right))
}

func closeFloat32(got float32, want float64, tolerance float64) bool {
	return math.Abs(float64(got)-want) <= tolerance
}
