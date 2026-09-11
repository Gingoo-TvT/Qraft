package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/labstack/echo/v4"
)

type fakeRuntimeKeyStore struct {
	lastKey string
	ref     string
	err     error
	lastTTL time.Duration
	puts    int
}

type fakePermanentProviderSettings struct {
	statement    *domain.LLMRuntimeConfig
	verification *domain.LLMRuntimeConfig
	review       *domain.LLMRuntimeConfig
	calls        []string
}

func (s *fakePermanentProviderSettings) EffectiveRuntimeConfig(
	_ context.Context,
	purpose string,
) (*domain.LLMRuntimeConfig, error) {
	s.calls = append(s.calls, purpose)
	switch purpose {
	case "statement":
		return s.statement, nil
	case "verification":
		return s.verification, nil
	case "review":
		return s.review, nil
	default:
		return nil, nil
	}
}

func (s *fakePermanentProviderSettings) SavedRuntimeConfig(
	_ context.Context,
	purpose string,
) (*domain.LLMRuntimeConfig, bool, error) {
	s.calls = append(s.calls, purpose)
	var config *domain.LLMRuntimeConfig
	switch purpose {
	case "statement":
		config = s.statement
	case "verification":
		config = s.verification
	case "review":
		config = s.review
	}
	return config, config != nil, nil
}

func (s *fakeRuntimeKeyStore) Put(_ context.Context, apiKey string) (string, error) {
	s.puts++
	s.lastKey = apiKey
	if s.err != nil {
		return "", s.err
	}
	if s.ref == "" {
		return "runtime:test-token", nil
	}
	return s.ref, nil
}

func (s *fakeRuntimeKeyStore) PutWithTTL(ctx context.Context, apiKey string, ttl time.Duration) (string, error) {
	s.lastTTL = ttl
	return s.Put(ctx, apiKey)
}

func TestPrepareProviderRuntimeConfigReplacesRawKeysWithRuntimeRefs(t *testing.T) {
	store := &fakeRuntimeKeyStore{}
	handler := NewProblemHandler(nil, nil, store)
	params := domain.DefaultProblemGenParams()
	params.ProviderConfig = &domain.ProviderRuntimeConfig{
		Statement: &domain.LLMRuntimeConfig{
			Model:    "statement-model",
			APIKey:   "  sk-statement  ",
			BaseURL:  "https://llm.example.com",
			Provider: "anthropic-compatible",
		},
		Verification: &domain.LLMRuntimeConfig{
			Model:     "review-model",
			APIKeyRef: "env:ALGOFORGE_REVIEW_KEY",
		},
	}

	if err := handler.prepareProviderRuntimeConfig(context.Background(), params.ProviderConfig); err != nil {
		t.Fatalf("prepareProviderRuntimeConfig() error = %v", err)
	}
	if store.lastKey != "sk-statement" {
		t.Fatalf("stored key = %q, want trimmed raw key", store.lastKey)
	}
	if params.ProviderConfig.Statement.APIKey != "" {
		t.Fatal("raw statement key was not cleared")
	}
	if params.ProviderConfig.Statement.APIKeyRef != "runtime:test-token" {
		t.Fatalf("statement key ref = %q, want runtime ref", params.ProviderConfig.Statement.APIKeyRef)
	}
	if params.ProviderConfig.Statement.BaseURL != "https://llm.example.com" {
		t.Fatalf("statement base url = %q, want preserved override", params.ProviderConfig.Statement.BaseURL)
	}
	if params.ProviderConfig.Statement.Provider != "anthropic-compatible" {
		t.Fatalf("statement provider = %q, want preserved provider", params.ProviderConfig.Statement.Provider)
	}
	if err := params.Validate(); err != nil {
		t.Fatalf("normalized params failed validation: %v", err)
	}
}

func TestPrepareProviderRuntimeConfigRejectsAmbiguousKeyInputs(t *testing.T) {
	handler := NewProblemHandler(nil, nil, &fakeRuntimeKeyStore{})
	cfg := &domain.ProviderRuntimeConfig{
		Statement: &domain.LLMRuntimeConfig{
			APIKey:    "sk-statement",
			APIKeyRef: "env:ALGOFORGE_STATEMENT_KEY",
		},
	}
	err := handler.prepareProviderRuntimeConfig(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "cannot be combined") {
		t.Fatalf("ambiguous runtime config error = %v", err)
	}
}

func TestPrepareProviderRuntimeConfigRequiresStoreForRawKeys(t *testing.T) {
	handler := NewProblemHandler(nil, nil)
	cfg := &domain.ProviderRuntimeConfig{
		Statement: &domain.LLMRuntimeConfig{APIKey: "sk-statement"},
	}
	err := handler.prepareProviderRuntimeConfig(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "runtime key store") {
		t.Fatalf("missing runtime key store error = %v", err)
	}
}

func TestPermanentProviderSettingsMergeAndReplaceStoredKeyWithRuntimeRef(t *testing.T) {
	keyStore := &fakeRuntimeKeyStore{}
	handler := NewProblemHandler(nil, nil, keyStore)
	settings := &fakePermanentProviderSettings{
		statement: &domain.LLMRuntimeConfig{
			Model:    "saved-statement-model",
			APIKey:   "saved-secret",
			BaseURL:  "https://saved.example.com",
			Provider: "saved-provider",
		},
		verification: &domain.LLMRuntimeConfig{
			Model:     "saved-verification-model",
			APIKeyRef: "env:ANTHROPIC_API_KEY",
			BaseURL:   "https://saved.example.com",
			Provider:  "saved-provider",
		},
		review: &domain.LLMRuntimeConfig{
			Model:     "saved-review-model",
			APIKeyRef: "env:ANTHROPIC_API_KEY",
			BaseURL:   "https://review.example.com",
			Provider:  "review-provider",
		},
	}
	handler.SetPermanentProviderSettings(settings, true)
	config := &domain.ProviderRuntimeConfig{
		Statement: &domain.LLMRuntimeConfig{Model: "one-request-model"},
	}

	if err := handler.applyPermanentProviderConfig(context.Background(), &config); err != nil {
		t.Fatalf("applyPermanentProviderConfig() error = %v", err)
	}
	if config.Statement.Model != "one-request-model" || config.Statement.BaseURL != "https://saved.example.com" {
		t.Fatalf("merged statement config = %+v", config.Statement)
	}
	if config.Verification == nil || config.Verification.Model != "saved-verification-model" {
		t.Fatalf("merged verification config = %+v", config.Verification)
	}
	if config.Review == nil || config.Review.Model != "saved-review-model" || config.Review.BaseURL != "https://review.example.com" {
		t.Fatalf("merged review config = %+v", config.Review)
	}
	if got := strings.Join(settings.calls, ","); got != "statement,verification,review" {
		t.Fatalf("settings calls = %q", got)
	}
	if err := handler.prepareProviderRuntimeConfig(context.Background(), config); err != nil {
		t.Fatalf("prepareProviderRuntimeConfig() error = %v", err)
	}
	if keyStore.lastKey != "saved-secret" || config.Statement.APIKeyRef != "runtime:test-token" || config.Statement.APIKey != "" {
		t.Fatalf("prepared statement config = %+v stored=%q", config.Statement, keyStore.lastKey)
	}
	if err := config.Validate(); err != nil {
		t.Fatalf("merged config failed validation: %v", err)
	}
}

func TestPermanentProviderSettingsInheritGToVToRBeforeRequestOverrides(t *testing.T) {
	handler := NewProblemHandler(nil, nil, &fakeRuntimeKeyStore{})
	settings := &fakePermanentProviderSettings{
		statement: &domain.LLMRuntimeConfig{
			Model:     "saved-g-model",
			APIKeyRef: "env:ANTHROPIC_API_KEY",
			BaseURL:   "https://g.example/v1",
			Provider:  "saved-g-provider",
			Protocol:  "anthropic-messages",
		},
	}
	handler.SetPermanentProviderSettings(settings, true)
	config := &domain.ProviderRuntimeConfig{
		Statement:    &domain.LLMRuntimeConfig{Model: "request-g-model"},
		Verification: &domain.LLMRuntimeConfig{Model: "request-v-model"},
		Review:       &domain.LLMRuntimeConfig{Model: "request-r-model"},
	}

	if err := handler.applyPermanentProviderConfig(context.Background(), &config); err != nil {
		t.Fatalf("applyPermanentProviderConfig() error = %v", err)
	}
	if config.Statement.Model != "request-g-model" || config.Statement.BaseURL != "https://g.example/v1" {
		t.Fatalf("statement config = %+v", config.Statement)
	}
	if config.Verification.Model != "request-v-model" || config.Verification.Provider != config.Statement.Provider ||
		config.Verification.BaseURL != config.Statement.BaseURL || config.Verification.APIKeyRef != config.Statement.APIKeyRef {
		t.Fatalf("verification config = %+v statement=%+v", config.Verification, config.Statement)
	}
	if config.Review.Model != "request-r-model" || config.Review.Provider != config.Verification.Provider ||
		config.Review.BaseURL != config.Verification.BaseURL || config.Review.APIKeyRef != config.Verification.APIKeyRef {
		t.Fatalf("review config = %+v verification=%+v", config.Review, config.Verification)
	}
	if config.Statement == config.Verification || config.Verification == config.Review {
		t.Fatal("inherited roles must be independent runtime config values")
	}
	if got := strings.Join(settings.calls, ","); got != "statement,verification,review" {
		t.Fatalf("settings calls = %q", got)
	}
}

func TestPermanentProviderSettingsRejectTransportIdentityChangeWithoutRequestKey(t *testing.T) {
	tests := []struct {
		name     string
		override *domain.LLMRuntimeConfig
	}{
		{name: "base_url", override: &domain.LLMRuntimeConfig{BaseURL: "https://other.example/v1"}},
		{name: "provider", override: &domain.LLMRuntimeConfig{Provider: "other-provider"}},
		{name: "protocol", override: &domain.LLMRuntimeConfig{Protocol: "openai-chat"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			keyStore := &fakeRuntimeKeyStore{}
			handler := NewProblemHandler(nil, nil, keyStore)
			handler.SetPermanentProviderSettings(&fakePermanentProviderSettings{
				statement: &domain.LLMRuntimeConfig{
					Model: "saved-model", APIKey: "saved-secret", BaseURL: "https://saved.example/v1",
					Provider: "saved-provider", Protocol: "anthropic-messages",
				},
			}, true)
			config := &domain.ProviderRuntimeConfig{Statement: test.override}

			err := handler.applyPermanentProviderConfig(context.Background(), &config)
			if err == nil || !strings.Contains(err.Error(), "must supply api_key or api_key_ref") {
				t.Fatalf("transport override error = %v", err)
			}
			if keyStore.puts != 0 || keyStore.lastKey != "" {
				t.Fatalf("rejected transport override reached runtime key store: puts=%d key=%q", keyStore.puts, keyStore.lastKey)
			}
		})
	}
}

func TestPermanentProviderSettingsAllowEquivalentTransportAndModelOnlyWithoutRequestKey(t *testing.T) {
	handler := NewProblemHandler(nil, nil, &fakeRuntimeKeyStore{})
	handler.SetPermanentProviderSettings(&fakePermanentProviderSettings{
		statement: &domain.LLMRuntimeConfig{
			Model: "saved-model", APIKey: "saved-secret", BaseURL: "https://saved.example/v1",
			Provider: "saved-provider", Protocol: "openai-chat",
		},
	}, true)
	config := &domain.ProviderRuntimeConfig{Statement: &domain.LLMRuntimeConfig{
		Model: "request-model", BaseURL: " HTTPS://SAVED.EXAMPLE/v1/ ",
		Provider: " saved-provider ", Protocol: "openai-compatible",
	}}

	if err := handler.applyPermanentProviderConfig(context.Background(), &config); err != nil {
		t.Fatalf("equivalent transport/model-only override rejected: %v", err)
	}
	if config.Statement.Model != "request-model" || config.Statement.APIKey != "saved-secret" {
		t.Fatalf("merged equivalent override = %+v", config.Statement)
	}
}

func TestPermanentProviderSettingsAllowTransportChangeWithRequestKeyRef(t *testing.T) {
	handler := NewProblemHandler(nil, nil, &fakeRuntimeKeyStore{})
	handler.SetPermanentProviderSettings(&fakePermanentProviderSettings{
		statement: &domain.LLMRuntimeConfig{
			Model: "saved-model", APIKey: "saved-secret", BaseURL: "https://saved.example/v1",
			Provider: "saved-provider", Protocol: "anthropic-messages",
		},
	}, true)
	config := &domain.ProviderRuntimeConfig{Statement: &domain.LLMRuntimeConfig{
		BaseURL: "https://other.example/v1", Provider: "other-provider",
		Protocol: "openai-chat", APIKeyRef: "env:OTHER_PROVIDER_KEY",
	}}

	if err := handler.applyPermanentProviderConfig(context.Background(), &config); err != nil {
		t.Fatalf("keyed transport override rejected: %v", err)
	}
	if config.Statement.APIKey != "" || config.Statement.APIKeyRef != "env:OTHER_PROVIDER_KEY" {
		t.Fatalf("keyed transport override did not replace inherited key: %+v", config.Statement)
	}
}

func TestGenerateHandlersReturnBadRequestBeforeRuntimeKeyPutForUnsafeTransportOverride(t *testing.T) {
	for _, test := range []struct {
		name   string
		handle func(*ProblemHandler, echo.Context) error
	}{
		{name: "single", handle: func(h *ProblemHandler, c echo.Context) error { return h.HandleGenerate(c) }},
		{name: "gplt", handle: func(h *ProblemHandler, c echo.Context) error { return h.HandleGPLTGenerate(c) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			keyStore := &fakeRuntimeKeyStore{}
			handler := NewProblemHandler(nil, nil, keyStore)
			handler.SetPermanentProviderSettings(&fakePermanentProviderSettings{
				statement: &domain.LLMRuntimeConfig{
					Model: "saved-model", APIKey: "saved-secret", BaseURL: "https://saved.example/v1",
					Provider: "saved-provider", Protocol: "anthropic-messages",
				},
			}, true)
			e := echo.New()
			request := httptest.NewRequest(
				http.MethodPost,
				"/api/v1/problems/generate",
				strings.NewReader(`{"provider_config":{"statement":{"base_url":"https://other.example/v1"}}}`),
			)
			request.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			recorder := httptest.NewRecorder()

			if err := test.handle(handler, e.NewContext(request, recorder)); err != nil {
				t.Fatalf("handler error = %v", err)
			}
			if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "INVALID_PARAMS") {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			if keyStore.puts != 0 || keyStore.lastKey != "" {
				t.Fatalf("rejected request reached runtime key store: puts=%d key=%q", keyStore.puts, keyStore.lastKey)
			}
		})
	}
}

func TestPermanentProviderSettingsFlagOffPreservesLegacyPath(t *testing.T) {
	settings := &fakePermanentProviderSettings{
		statement: &domain.LLMRuntimeConfig{Model: "saved-statement-model"},
		review:    &domain.LLMRuntimeConfig{Model: "saved-review-model"},
	}
	handler := NewProblemHandler(nil, nil, &fakeRuntimeKeyStore{})
	handler.SetPermanentProviderSettings(settings, false)
	config := &domain.ProviderRuntimeConfig{
		Statement: &domain.LLMRuntimeConfig{Model: "request-model", APIKeyRef: "env:ANTHROPIC_API_KEY"},
	}

	if err := handler.applyPermanentProviderConfig(context.Background(), &config); err != nil {
		t.Fatalf("applyPermanentProviderConfig() error = %v", err)
	}
	if len(settings.calls) != 0 {
		t.Fatalf("disabled routing read saved settings: %v", settings.calls)
	}
	if config.Statement.Model != "request-model" || config.Review != nil || config.Verification != nil {
		t.Fatalf("disabled routing changed request config: %+v", config)
	}
}
