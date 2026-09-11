package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/config"
	authmw "github.com/Gingoo-TvT/Qraft/backend/internal/handler/middleware"
	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
	"github.com/labstack/echo/v4"
)

type fakeLLMSettingsStore struct {
	records     map[string]repository.LLMProviderSettingRecord
	upsertCalls int
	deleteCalls []string
	deleteErr   error
}

func (s *fakeLLMSettingsStore) GetLLMProviderSetting(
	_ context.Context,
	purpose string,
) (repository.LLMProviderSettingRecord, error) {
	record, ok := s.records[purpose]
	if !ok {
		return repository.LLMProviderSettingRecord{}, repository.ErrLLMProviderSettingNotFound
	}
	return record, nil
}

func (s *fakeLLMSettingsStore) DeleteLLMProviderSetting(_ context.Context, purpose string) error {
	s.deleteCalls = append(s.deleteCalls, purpose)
	if s.deleteErr != nil {
		return s.deleteErr
	}
	delete(s.records, purpose)
	return nil
}

func (s *fakeLLMSettingsStore) UpsertLLMProviderSetting(
	_ context.Context,
	input repository.LLMProviderSettingUpsert,
) (repository.LLMProviderSettingRecord, error) {
	s.upsertCalls++
	if s.records == nil {
		s.records = make(map[string]repository.LLMProviderSettingRecord)
	}
	now := time.Now().UTC()
	record := repository.LLMProviderSettingRecord{
		Purpose:         input.Purpose,
		Model:           input.Model,
		BaseURL:         input.BaseURL,
		Provider:        input.Provider,
		Protocol:        input.Protocol,
		ReasoningEffort: input.ReasoningEffort,
		APIKeySource:    input.APIKeySource,
		EncryptedAPIKey: input.EncryptedAPIKey,
		UpdatedBy:       input.UpdatedBy,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	s.records[input.Purpose] = record
	return record, nil
}

type fakeSettingsCipher struct {
	values map[string]string
}

type fakeLLMSettingsConnectionProber struct {
	input llmProviderConnectionProbe
	err   error
	calls int
}

func (p *fakeLLMSettingsConnectionProber) Probe(_ context.Context, input llmProviderConnectionProbe) error {
	p.calls++
	p.input = input
	return p.err
}

func (c *fakeSettingsCipher) Seal(plaintext string) (string, error) {
	if c.values == nil {
		c.values = make(map[string]string)
	}
	sealed := "ciphertext-" + time.Now().UTC().Format("150405.000000000")
	c.values[sealed] = plaintext
	return sealed, nil
}

func (c *fakeSettingsCipher) Open(encoded string) (string, error) {
	return c.values[encoded], nil
}

func TestLLMSettingsGetReportsUnconfiguredWithoutDeploymentFallback(t *testing.T) {
	h := NewLLMSettingsHandler(
		&fakeLLMSettingsStore{},
		&fakeSettingsCipher{},
		config.AnthropicConfig{
			APIKey:   "sk-must-not-leak",
			BaseURL:  "https://llm.example.com/",
			Model:    "statement-model",
			Provider: "example-provider",
		},
	)
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/settings/llm", nil)
	rec := httptest.NewRecorder()

	if err := h.HandleGet(e.NewContext(req, rec)); err != nil {
		t.Fatalf("HandleGet() error = %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "sk-must-not-leak") {
		t.Fatal("settings response leaked deployment API key")
	}
	if !strings.Contains(rec.Body.String(), `"api_key_configured":false`) ||
		!strings.Contains(rec.Body.String(), `"source":"unconfigured"`) ||
		!strings.Contains(rec.Body.String(), `"override_configured":false`) ||
		!strings.Contains(rec.Body.String(), `"inherited_from":"statement"`) ||
		!strings.Contains(rec.Body.String(), `"inherited_from":"verification"`) {
		t.Fatalf("unexpected response: %s", rec.Body.String())
	}
}

func TestLLMSettingsEffectiveRuntimeRejectsMissingStatementConfig(t *testing.T) {
	h := NewLLMSettingsHandler(
		&fakeLLMSettingsStore{},
		&fakeSettingsCipher{},
		config.AnthropicConfig{APIKey: "deployment-secret", Model: "deployment-model", BaseURL: "https://deployment.example"},
	)
	if _, err := h.EffectiveRuntimeConfig(context.Background(), repository.LLMProviderPurposeStatement); err == nil || !strings.Contains(err.Error(), "saved statement LLM configuration is required") {
		t.Fatalf("expected missing durable statement configuration error, got %v", err)
	}
}

func TestLLMSettingsGetReportsSavedAndInheritedRolesCredentialFree(t *testing.T) {
	encrypted := "ciphertext-must-not-leak"
	store := &fakeLLMSettingsStore{records: map[string]repository.LLMProviderSettingRecord{
		repository.LLMProviderPurposeStatement: {
			Purpose: repository.LLMProviderPurposeStatement, Model: "saved-g",
			BaseURL: "https://g.example/v1", Provider: "g-provider", Protocol: "openai-chat",
			APIKeySource: repository.LLMAPIKeySourceStored, EncryptedAPIKey: &encrypted,
			UpdatedAt: time.Now().UTC(), UpdatedBy: "user:admin",
		},
	}}
	h := NewLLMSettingsHandler(store, &fakeSettingsCipher{}, config.AnthropicConfig{APIKey: "env-secret"})
	e := echo.New()
	rec := httptest.NewRecorder()
	if err := h.HandleGet(e.NewContext(httptest.NewRequest(http.MethodGet, "/api/v1/settings/llm", nil), rec)); err != nil {
		t.Fatalf("HandleGet() error = %v", err)
	}
	var response struct {
		Data llmProviderSettingsView `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v body=%s", err, rec.Body.String())
	}
	if !response.Data.Statement.OverrideConfigured || response.Data.Statement.InheritedFrom != "" {
		t.Fatalf("statement view = %+v", response.Data.Statement)
	}
	if response.Data.Verification.OverrideConfigured || response.Data.Verification.InheritedFrom != "statement" || response.Data.Verification.Model != "saved-g" {
		t.Fatalf("verification view = %+v", response.Data.Verification)
	}
	if response.Data.Review.OverrideConfigured || response.Data.Review.InheritedFrom != "verification" || response.Data.Review.Model != "saved-g" {
		t.Fatalf("review view = %+v", response.Data.Review)
	}
	if strings.Contains(rec.Body.String(), encrypted) || strings.Contains(rec.Body.String(), "env-secret") {
		t.Fatalf("credential material leaked: %s", rec.Body.String())
	}
}

func TestLLMSettingsUpdateDerivesStableProviderAndProtocol(t *testing.T) {
	tests := []struct {
		name         string
		body         string
		wantBaseURL  string
		wantProvider string
		wantProtocol string
	}{
		{
			name:        "known model derives protocol",
			body:        `{"model":"gemini-2.5-pro","base_url":"HTTPS://LLM.Example.COM/v1/","use_environment_key":true}`,
			wantBaseURL: "https://llm.example.com/v1", wantProvider: "llm-endpoint:https://llm.example.com/v1",
			wantProtocol: "gemini-native",
		},
		{
			name:        "unknown custom defaults to chat",
			body:        `{"model":"private-model","base_url":"https://gateway.example/v1","use_environment_key":true}`,
			wantBaseURL: "https://gateway.example/v1", wantProvider: "llm-endpoint:https://gateway.example/v1",
			wantProtocol: "openai-chat",
		},
		{
			name:        "explicit legacy fields win",
			body:        `{"model":"private-model","base_url":"https://gateway.example/v1","provider":"legacy-provider","protocol":"openai-responses","reasoning_effort":"high","use_environment_key":true}`,
			wantBaseURL: "https://gateway.example/v1", wantProvider: "legacy-provider",
			wantProtocol: "openai-responses",
		},
		{
			name:         "empty endpoint uses official anthropic",
			body:         `{"model":"claude-sonnet","use_environment_key":true}`,
			wantProvider: "anthropic", wantProtocol: "anthropic-messages",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &fakeLLMSettingsStore{}
			prober := &fakeLLMSettingsConnectionProber{}
			h := NewLLMSettingsHandler(store, &fakeSettingsCipher{}, config.AnthropicConfig{APIKey: "env-key"}, prober)
			rec := invokeLLMSettingsUpdateBody(t, h, repository.LLMProviderPurposeStatement, test.body)
			if rec.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			record := store.records[repository.LLMProviderPurposeStatement]
			if record.BaseURL != test.wantBaseURL || record.Provider != test.wantProvider || record.Protocol != test.wantProtocol {
				t.Fatalf("derived record = %+v", record)
			}
			if test.name == "explicit legacy fields win" && record.ReasoningEffort != "high" {
				t.Fatalf("reasoning effort = %q", record.ReasoningEffort)
			}
			if prober.calls != 1 || prober.input.BaseURL != test.wantBaseURL || prober.input.Provider != test.wantProvider || prober.input.Protocol != test.wantProtocol {
				t.Fatalf("probe = calls=%d input=%+v", prober.calls, prober.input)
			}
		})
	}
}

func TestLLMSettingsUpdateRejectsUnsafeURLBeforeDerivationOrProbe(t *testing.T) {
	for _, baseURL := range []string{
		"https://user:secret@llm.example/v1",
		"https://llm.example/v1?token=secret",
		"https://llm.example/v1#secret",
	} {
		t.Run(baseURL, func(t *testing.T) {
			store := &fakeLLMSettingsStore{}
			prober := &fakeLLMSettingsConnectionProber{}
			h := NewLLMSettingsHandler(store, &fakeSettingsCipher{}, config.AnthropicConfig{APIKey: "env-key"}, prober)
			body, _ := json.Marshal(map[string]interface{}{
				"model": "private-model", "base_url": baseURL, "use_environment_key": true,
			})
			rec := invokeLLMSettingsUpdateBody(t, h, repository.LLMProviderPurposeStatement, string(body))
			if rec.Code != http.StatusBadRequest || prober.calls != 0 || len(store.records) != 0 {
				t.Fatalf("status=%d probe=%d records=%v body=%s", rec.Code, prober.calls, store.records, rec.Body.String())
			}
		})
	}
}

func TestLLMSettingsUpdateRejectsStoredKeyReuseAcrossEndpointBeforeProbe(t *testing.T) {
	encrypted := "ciphertext-old-provider"
	store := &fakeLLMSettingsStore{records: map[string]repository.LLMProviderSettingRecord{
		repository.LLMProviderPurposeStatement: {
			Purpose: repository.LLMProviderPurposeStatement, Model: "old-model",
			BaseURL: "https://old.example/v1", Provider: "old-provider", Protocol: "openai-chat",
			APIKeySource: repository.LLMAPIKeySourceStored, EncryptedAPIKey: &encrypted,
		},
	}}
	cipher := &fakeSettingsCipher{values: map[string]string{encrypted: "old-provider-secret"}}
	prober := &fakeLLMSettingsConnectionProber{}
	h := NewLLMSettingsHandler(store, cipher, config.AnthropicConfig{APIKey: "env-key"}, prober)
	rec := invokeLLMSettingsUpdateBody(
		t,
		h,
		repository.LLMProviderPurposeStatement,
		`{"model":"new-model","base_url":"https://new.example/v1","provider":"new-provider","protocol":"openai-chat"}`,
	)

	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"code":"INVALID_SETTINGS"`) ||
		!strings.Contains(rec.Body.String(), "changing base_url requires") {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if prober.calls != 0 || store.upsertCalls != 0 {
		t.Fatalf("rejected endpoint change reached probe/upsert: probe=%d upsert=%d", prober.calls, store.upsertCalls)
	}
	if store.records[repository.LLMProviderPurposeStatement].BaseURL != "https://old.example/v1" {
		t.Fatalf("stored row changed after rejection: %+v", store.records[repository.LLMProviderPurposeStatement])
	}
	if strings.Contains(rec.Body.String(), "old-provider-secret") || strings.Contains(rec.Body.String(), encrypted) {
		t.Fatalf("rejection leaked credential material: %s", rec.Body.String())
	}
}

func TestLLMSettingsUpdateReusesStoredKeyOnlyForEquivalentEndpoint(t *testing.T) {
	encrypted := "ciphertext-same-provider"
	store := &fakeLLMSettingsStore{records: map[string]repository.LLMProviderSettingRecord{
		repository.LLMProviderPurposeStatement: {
			Purpose: repository.LLMProviderPurposeStatement, Model: "old-model",
			BaseURL: "HTTPS://LLM.Example.COM/v1/", Provider: "old-provider", Protocol: "anthropic-messages",
			APIKeySource: repository.LLMAPIKeySourceStored, EncryptedAPIKey: &encrypted,
		},
	}}
	cipher := &fakeSettingsCipher{values: map[string]string{encrypted: "same-endpoint-secret"}}
	prober := &fakeLLMSettingsConnectionProber{}
	h := NewLLMSettingsHandler(store, cipher, config.AnthropicConfig{}, prober)
	rec := invokeLLMSettingsUpdateBody(
		t,
		h,
		repository.LLMProviderPurposeStatement,
		`{"model":"new-model","base_url":"https://llm.example.com/v1/","provider":"new-explicit-provider","protocol":"openai-responses"}`,
	)

	if rec.Code != http.StatusOK || prober.calls != 1 || store.upsertCalls != 1 {
		t.Fatalf("status=%d probe=%d upsert=%d body=%s", rec.Code, prober.calls, store.upsertCalls, rec.Body.String())
	}
	if prober.input.APIKey != "same-endpoint-secret" || prober.input.BaseURL != "https://llm.example.com/v1" {
		t.Fatalf("same-endpoint probe = %+v", prober.input)
	}
	record := store.records[repository.LLMProviderPurposeStatement]
	if record.Provider != "new-explicit-provider" || record.Protocol != "openai-responses" || record.EncryptedAPIKey == nil || *record.EncryptedAPIKey != encrypted {
		t.Fatalf("same-endpoint update = %+v", record)
	}
	if strings.Contains(rec.Body.String(), "same-endpoint-secret") || strings.Contains(rec.Body.String(), encrypted) {
		t.Fatalf("same-endpoint response leaked credential material: %s", rec.Body.String())
	}
}

func TestLLMSettingsUpdateAllowsExplicitCredentialChoiceForEndpointChange(t *testing.T) {
	tests := []struct {
		name          string
		body          string
		fallbackKey   string
		wantProbeKey  string
		wantKeySource string
	}{
		{
			name: "new stored key", body: `{"model":"new-model","base_url":"https://new.example/v1","api_key":"new-secret"}`,
			wantProbeKey: "new-secret", wantKeySource: repository.LLMAPIKeySourceStored,
		},
		{
			name: "explicit environment key", body: `{"model":"new-model","base_url":"https://new.example/v1","use_environment_key":true}`,
			fallbackKey: "new-env-secret", wantProbeKey: "new-env-secret", wantKeySource: repository.LLMAPIKeySourceEnvironment,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			encrypted := "ciphertext-old"
			store := &fakeLLMSettingsStore{records: map[string]repository.LLMProviderSettingRecord{
				repository.LLMProviderPurposeStatement: {
					Purpose: repository.LLMProviderPurposeStatement, Model: "old-model",
					BaseURL: "https://old.example/v1", Provider: "old-provider", Protocol: "openai-chat",
					APIKeySource: repository.LLMAPIKeySourceStored, EncryptedAPIKey: &encrypted,
				},
			}}
			cipher := &fakeSettingsCipher{values: map[string]string{encrypted: "old-secret"}}
			prober := &fakeLLMSettingsConnectionProber{}
			h := NewLLMSettingsHandler(store, cipher, config.AnthropicConfig{APIKey: test.fallbackKey}, prober)
			rec := invokeLLMSettingsUpdateBody(t, h, repository.LLMProviderPurposeStatement, test.body)
			if rec.Code != http.StatusOK || prober.calls != 1 || store.upsertCalls != 1 {
				t.Fatalf("status=%d probe=%d upsert=%d body=%s", rec.Code, prober.calls, store.upsertCalls, rec.Body.String())
			}
			if prober.input.APIKey != test.wantProbeKey || store.records[repository.LLMProviderPurposeStatement].APIKeySource != test.wantKeySource {
				t.Fatalf("probe=%+v record=%+v", prober.input, store.records[repository.LLMProviderPurposeStatement])
			}
		})
	}
}

func TestLLMSettingsUpdateEncryptsKeyAndEffectiveConfigDecryptsIt(t *testing.T) {
	store := &fakeLLMSettingsStore{}
	cipher := &fakeSettingsCipher{}
	prober := &fakeLLMSettingsConnectionProber{}
	h := NewLLMSettingsHandler(store, cipher, config.AnthropicConfig{APIKey: "env-key"}, prober)
	body, _ := json.Marshal(map[string]interface{}{
		"model":    "gemini-test",
		"base_url": "https://llm.example.com/",
		"provider": "example-provider",
		"protocol": "gemini-native",
		"api_key":  "sk-persisted-secret",
	})
	e := echo.New()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings/llm/statement", bytes.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetPath("/api/v1/settings/llm/:purpose")
	ctx.SetParamNames("purpose")
	ctx.SetParamValues("statement")

	if err := h.HandleUpdate(ctx); err != nil {
		t.Fatalf("HandleUpdate() error = %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if prober.calls != 1 || prober.input.APIKey != "sk-persisted-secret" || prober.input.Protocol != "gemini-native" {
		t.Fatalf("connection probe = calls=%d input=%+v", prober.calls, prober.input)
	}
	if strings.Contains(rec.Body.String(), "sk-persisted-secret") {
		t.Fatal("settings update response leaked API key")
	}
	record := store.records[repository.LLMProviderPurposeStatement]
	if record.APIKeySource != repository.LLMAPIKeySourceStored || record.EncryptedAPIKey == nil {
		t.Fatalf("stored record = %+v", record)
	}
	if *record.EncryptedAPIKey == "sk-persisted-secret" {
		t.Fatal("repository received plaintext API key")
	}

	effective, err := h.EffectiveRuntimeConfig(context.Background(), repository.LLMProviderPurposeStatement)
	if err != nil {
		t.Fatalf("EffectiveRuntimeConfig() error = %v", err)
	}
	if effective.APIKey != "sk-persisted-secret" || effective.APIKeyRef != "" {
		t.Fatalf("effective key fields = %+v", effective)
	}
	if effective.BaseURL != "https://llm.example.com" {
		t.Fatalf("effective base_url = %q", effective.BaseURL)
	}
	if effective.Protocol != "gemini-native" {
		t.Fatalf("effective protocol = %q", effective.Protocol)
	}
}

func TestLLMSettingsUpdateRejectsUnsupportedReasoningEffort(t *testing.T) {
	store := &fakeLLMSettingsStore{}
	prober := &fakeLLMSettingsConnectionProber{}
	h := NewLLMSettingsHandler(store, &fakeSettingsCipher{}, config.AnthropicConfig{APIKey: "env-key"}, prober)
	rec := invokeLLMSettingsUpdateBody(t, h, repository.LLMProviderPurposeStatement,
		`{"model":"gpt-5.6-sol","base_url":"https://gateway.example/v1","reasoning_effort":"extreme","use_environment_key":true}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "reasoning_effort") {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if prober.calls != 0 || store.upsertCalls != 0 {
		t.Fatalf("unsupported effort reached probe/upsert: probe=%d upsert=%d", prober.calls, store.upsertCalls)
	}
}

func TestLLMSettingsUpdateCanSwitchBackToEnvironmentKey(t *testing.T) {
	store := &fakeLLMSettingsStore{}
	prober := &fakeLLMSettingsConnectionProber{}
	h := NewLLMSettingsHandler(store, &fakeSettingsCipher{}, config.AnthropicConfig{APIKey: "env-key"}, prober)
	body := bytes.NewBufferString(`{
		"model":"claude-test",
		"base_url":"https://llm.example.com",
		"provider":"example-provider",
		"use_environment_key":true
	}`)
	e := echo.New()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings/llm/verification", body)
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames("purpose")
	ctx.SetParamValues("verification")

	if err := h.HandleUpdate(ctx); err != nil {
		t.Fatalf("HandleUpdate() error = %v", err)
	}
	record := store.records[repository.LLMProviderPurposeVerification]
	if record.APIKeySource != repository.LLMAPIKeySourceEnvironment || record.EncryptedAPIKey != nil {
		t.Fatalf("stored record = %+v", record)
	}
	effective, err := h.EffectiveRuntimeConfig(context.Background(), repository.LLMProviderPurposeVerification)
	if err != nil {
		t.Fatalf("EffectiveRuntimeConfig() error = %v", err)
	}
	if effective.APIKeyRef != "env:ANTHROPIC_API_KEY" || effective.APIKey != "" {
		t.Fatalf("effective key fields = %+v", effective)
	}
	if prober.calls != 1 || prober.input.APIKey != "env-key" {
		t.Fatalf("connection probe = calls=%d input=%+v", prober.calls, prober.input)
	}
}

func TestLLMSettingsSupportsIndependentReviewPurpose(t *testing.T) {
	store := &fakeLLMSettingsStore{}
	prober := &fakeLLMSettingsConnectionProber{}
	h := NewLLMSettingsHandler(store, &fakeSettingsCipher{}, config.AnthropicConfig{APIKey: "env-key"}, prober)
	body := bytes.NewBufferString(`{
		"model":"review-model",
		"base_url":"https://review.example.com",
		"provider":"review-provider",
		"protocol":"openai-chat",
		"use_environment_key":true
	}`)
	e := echo.New()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings/llm/review", body)
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames("purpose")
	ctx.SetParamValues("review")

	if err := h.HandleUpdate(ctx); err != nil {
		t.Fatalf("HandleUpdate() error = %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	record, ok := store.records[repository.LLMProviderPurposeReview]
	if !ok || record.Model != "review-model" || prober.calls != 1 {
		t.Fatalf("review record=%+v ok=%v probe_calls=%d", record, ok, prober.calls)
	}
}

func TestLLMSettingsConnectionFailureDoesNotPersist(t *testing.T) {
	store := &fakeLLMSettingsStore{}
	prober := &fakeLLMSettingsConnectionProber{err: errors.New("connection refused and echoed new-secret")}
	h := NewLLMSettingsHandler(store, &fakeSettingsCipher{}, config.AnthropicConfig{}, prober)
	body := bytes.NewBufferString(`{
		"model":"review-model",
		"base_url":"https://review.example.com",
		"provider":"review-provider",
		"protocol":"openai-chat",
		"api_key":"new-secret"
	}`)
	e := echo.New()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings/llm/review", body)
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames("purpose")
	ctx.SetParamValues("review")

	if err := h.HandleUpdate(ctx); err != nil {
		t.Fatalf("HandleUpdate() error = %v", err)
	}
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "LLM_CONNECTION_FAILED") {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "new-secret") || !strings.Contains(rec.Body.String(), "[redacted]") {
		t.Fatalf("connection error did not redact API key: %s", rec.Body.String())
	}
	if prober.calls != 1 || len(store.records) != 0 {
		t.Fatalf("probe_calls=%d persisted=%v", prober.calls, store.records)
	}
}

func TestLiveLLMSettingsConnectionProbeRunsBeforePersist(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.URL.Path != "/v1/messages" || r.Header.Get("x-api-key") != "probe-secret" {
			t.Fatalf("probe request path=%q api_key=%q", r.URL.Path, r.Header.Get("x-api-key"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"probe-response","type":"message","role":"assistant",
			"content":[{"type":"text","text":"OK"}],
			"model":"probe-model","stop_reason":"end_turn",
			"usage":{"input_tokens":1,"output_tokens":1}
		}`))
	}))
	defer server.Close()

	store := &fakeLLMSettingsStore{}
	h := NewLLMSettingsHandler(store, &fakeSettingsCipher{}, config.AnthropicConfig{})
	body, _ := json.Marshal(map[string]interface{}{
		"model": "probe-model", "base_url": server.URL, "provider": "probe-provider",
		"protocol": "anthropic-messages", "api_key": "probe-secret",
	})
	e := echo.New()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings/llm/review", bytes.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames("purpose")
	ctx.SetParamValues("review")

	if err := h.HandleUpdate(ctx); err != nil {
		t.Fatalf("HandleUpdate() error = %v", err)
	}
	if rec.Code != http.StatusOK || !called {
		t.Fatalf("status=%d called=%v body=%s", rec.Code, called, rec.Body.String())
	}
	if _, ok := store.records[repository.LLMProviderPurposeReview]; !ok {
		t.Fatal("successful connection probe did not persist review settings")
	}
}

func TestLLMSettingsEnvironmentKeyMustExistBeforePersist(t *testing.T) {
	store := &fakeLLMSettingsStore{}
	prober := &fakeLLMSettingsConnectionProber{}
	h := NewLLMSettingsHandler(store, &fakeSettingsCipher{}, config.AnthropicConfig{}, prober)
	body := bytes.NewBufferString(`{
		"model":"review-model","base_url":"https://review.example.com",
		"provider":"review-provider","use_environment_key":true
	}`)
	e := echo.New()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings/llm/review", body)
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames("purpose")
	ctx.SetParamValues("review")

	if err := h.HandleUpdate(ctx); err != nil {
		t.Fatalf("HandleUpdate() error = %v", err)
	}
	if rec.Code != http.StatusBadRequest || prober.calls != 0 || len(store.records) != 0 {
		t.Fatalf("status=%d probe_calls=%d records=%v body=%s", rec.Code, prober.calls, store.records, rec.Body.String())
	}
}

func TestLLMSettingsDeleteResetsOptionalOverrideToEffectiveParent(t *testing.T) {
	tests := []struct {
		purpose       string
		wantModel     string
		wantInherited string
	}{
		{purpose: repository.LLMProviderPurposeVerification, wantModel: "statement-model", wantInherited: "statement"},
		{purpose: repository.LLMProviderPurposeReview, wantModel: "verification-model", wantInherited: "verification"},
	}
	for _, test := range tests {
		t.Run(test.purpose, func(t *testing.T) {
			store := &fakeLLMSettingsStore{records: map[string]repository.LLMProviderSettingRecord{
				repository.LLMProviderPurposeStatement: {
					Purpose: repository.LLMProviderPurposeStatement, Model: "statement-model",
					Provider: "statement-provider", Protocol: "anthropic-messages",
					APIKeySource: repository.LLMAPIKeySourceEnvironment, UpdatedAt: time.Now().UTC(),
				},
				repository.LLMProviderPurposeVerification: {
					Purpose: repository.LLMProviderPurposeVerification, Model: "verification-model",
					Provider: "verification-provider", Protocol: "openai-chat",
					APIKeySource: repository.LLMAPIKeySourceEnvironment, UpdatedAt: time.Now().UTC(),
				},
				repository.LLMProviderPurposeReview: {
					Purpose: repository.LLMProviderPurposeReview, Model: "review-model",
					Provider: "review-provider", Protocol: "openai-chat",
					APIKeySource: repository.LLMAPIKeySourceEnvironment, UpdatedAt: time.Now().UTC(),
				},
			}}
			h := NewLLMSettingsHandler(store, &fakeSettingsCipher{}, config.AnthropicConfig{APIKey: "env-secret"})
			rec := invokeLLMSettingsDelete(t, h, test.purpose, nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			if len(store.deleteCalls) != 1 || store.deleteCalls[0] != test.purpose {
				t.Fatalf("delete calls = %v", store.deleteCalls)
			}
			if _, exists := store.records[test.purpose]; exists {
				t.Fatalf("override %q was not deleted", test.purpose)
			}
			var response struct {
				Data llmProviderSettingView `json:"data"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if response.Data.Model != test.wantModel || response.Data.OverrideConfigured || response.Data.InheritedFrom != test.wantInherited {
				t.Fatalf("reset view = %+v", response.Data)
			}
			if strings.Contains(rec.Body.String(), "env-secret") {
				t.Fatalf("reset response leaked key: %s", rec.Body.String())
			}
		})
	}
}

func TestLLMSettingsDeleteRejectsStatementRoot(t *testing.T) {
	store := &fakeLLMSettingsStore{records: map[string]repository.LLMProviderSettingRecord{
		repository.LLMProviderPurposeStatement: {Purpose: repository.LLMProviderPurposeStatement, Model: "g"},
	}}
	h := NewLLMSettingsHandler(store, &fakeSettingsCipher{}, config.AnthropicConfig{})
	rec := invokeLLMSettingsDelete(t, h, repository.LLMProviderPurposeStatement, nil)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "RESET_NOT_ALLOWED") {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(store.deleteCalls) != 0 {
		t.Fatalf("statement reset reached repository: %v", store.deleteCalls)
	}
}

func invokeLLMSettingsUpdateBody(
	t *testing.T,
	handler *LLMSettingsHandler,
	purpose string,
	body string,
) *httptest.ResponseRecorder {
	t.Helper()
	e := echo.New()
	request := httptest.NewRequest(http.MethodPut, "/api/v1/settings/llm/"+purpose, strings.NewReader(body))
	request.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	recorder := httptest.NewRecorder()
	ctx := e.NewContext(request, recorder)
	ctx.SetParamNames("purpose")
	ctx.SetParamValues(purpose)
	if err := handler.HandleUpdate(ctx); err != nil {
		t.Fatalf("HandleUpdate() error = %v", err)
	}
	return recorder
}

func invokeLLMSettingsDelete(
	t *testing.T,
	handler *LLMSettingsHandler,
	purpose string,
	claims *authmw.JWTClaims,
) *httptest.ResponseRecorder {
	t.Helper()
	e := echo.New()
	request := httptest.NewRequest(http.MethodDelete, "/api/v1/settings/llm/"+purpose, nil)
	recorder := httptest.NewRecorder()
	ctx := e.NewContext(request, recorder)
	ctx.SetParamNames("purpose")
	ctx.SetParamValues(purpose)
	if claims != nil {
		ctx.Set("user", claims)
	}
	if err := handler.HandleDelete(ctx); err != nil {
		t.Fatalf("HandleDelete() error = %v", err)
	}
	return recorder
}
