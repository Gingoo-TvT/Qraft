package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/config"
	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

type fakeEmbeddingStatusStore struct {
	statuses []repository.ActiveEmbeddingModelStatus
	err      error
}

type fakeEmbeddingSettingsStore struct {
	record *repository.EmbeddingProviderSettingRecord
	getErr error
	upsert repository.EmbeddingProviderSettingUpsert
}

func (s *fakeEmbeddingSettingsStore) GetEmbeddingProviderSetting(context.Context) (repository.EmbeddingProviderSettingRecord, error) {
	if s.getErr != nil {
		return repository.EmbeddingProviderSettingRecord{}, s.getErr
	}
	if s.record != nil {
		return *s.record, nil
	}
	return repository.EmbeddingProviderSettingRecord{}, repository.ErrEmbeddingProviderSettingNotFound
}

func (s *fakeEmbeddingSettingsStore) UpsertEmbeddingProviderSetting(
	_ context.Context,
	input repository.EmbeddingProviderSettingUpsert,
) (repository.EmbeddingProviderSettingRecord, error) {
	s.upsert = input
	return repository.EmbeddingProviderSettingRecord{}, nil
}

type fakeEmbeddingSettingsCipher struct{}

func (fakeEmbeddingSettingsCipher) Seal(plaintext string) (string, error) {
	return "sealed:" + plaintext, nil
}

func (fakeEmbeddingSettingsCipher) Open(encoded string) (string, error) {
	return strings.TrimPrefix(encoded, "sealed:"), nil
}

func (s fakeEmbeddingStatusStore) ActiveEmbeddingModelStatuses(context.Context) ([]repository.ActiveEmbeddingModelStatus, error) {
	return s.statuses, s.err
}

type fakeEmbeddingAdminStore struct {
	fakeEmbeddingStatusStore
	modelVersion  repository.EmbeddingModelVersionRecord
	getErr        error
	registerInput *repository.EmbeddingModelVersionRegistration
	planCalls     []repository.EmbeddingBackfillPlanOptions
	updateCalls   []repository.EmbeddingWrite
	switchCalls   []repository.ActivePointerSwitchOptions
}

func (s *fakeEmbeddingAdminStore) RegisterEmbeddingModelVersion(
	_ context.Context,
	input repository.EmbeddingModelVersionRegistration,
) (repository.EmbeddingModelVersionRecord, error) {
	s.registerInput = &input
	record := s.modelVersion
	if record.ID == uuid.Nil {
		record.ID = uuid.MustParse("22222222-2222-2222-2222-222222222222")
	}
	record.Provider = input.Provider
	record.ModelID = input.ModelID
	record.Dimensions = input.Dimensions
	return record, nil
}

func (s *fakeEmbeddingAdminStore) GetEmbeddingModelVersion(
	_ context.Context,
	_ uuid.UUID,
) (repository.EmbeddingModelVersionRecord, error) {
	return s.modelVersion, s.getErr
}

func (s *fakeEmbeddingAdminStore) ListEmbeddingModelVersions(
	context.Context,
	int,
) ([]repository.EmbeddingModelVersionRecord, error) {
	return nil, nil
}

func (s *fakeEmbeddingAdminStore) PlanEmbeddingBackfill(
	_ context.Context,
	options repository.EmbeddingBackfillPlanOptions,
) (repository.EmbeddingBackfillPlanReport, error) {
	s.planCalls = append(s.planCalls, options)
	return repository.EmbeddingBackfillPlanReport{}, nil
}

func (s *fakeEmbeddingAdminStore) UpdateEmbeddingForVersion(
	_ context.Context,
	input repository.EmbeddingWrite,
) error {
	s.updateCalls = append(s.updateCalls, input)
	return nil
}

func (s *fakeEmbeddingAdminStore) SwitchActiveEmbeddingPointer(
	_ context.Context,
	options repository.ActivePointerSwitchOptions,
) (repository.ActivePointerSwitchReport, error) {
	s.switchCalls = append(s.switchCalls, options)
	return repository.ActivePointerSwitchReport{}, nil
}

func TestEmbeddingStatusHandlerReturnsActivePointers(t *testing.T) {
	modelID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	updatedAt := time.Date(2026, 8, 18, 15, 30, 0, 0, time.UTC)
	handler := NewEmbeddingHandler(fakeEmbeddingStatusStore{
		statuses: []repository.ActiveEmbeddingModelStatus{
			{
				EmbeddingKind:      repository.EmbeddingKindStatement,
				ExpectedDimensions: 1536,
				UpdatedBy:          "migration-019",
				Reason:             "seed legacy statement active pointer",
				UpdatedAt:          updatedAt,
				ModelVersion: repository.EmbeddingModelVersionRecord{
					ID:            modelID,
					Provider:      "openai-compatible",
					ModelID:       "legacy-openai-compatible-1536",
					Revision:      "pre-t02-legacy",
					WeightsHash:   strings.Repeat("a", 64),
					Dimensions:    1536,
					Normalization: "provider-default",
					Quantization:  "float32",
					IndexParams:   json.RawMessage(`{"index":"ivfflat"}`),
					Status:        repository.EmbeddingModelStatusActive,
					CreatedAt:     updatedAt,
					UpdatedAt:     updatedAt,
				},
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/embedding/status", nil)
	c := echo.New().NewContext(req, rec)

	if err := handler.HandleStatus(c); err != nil {
		t.Fatalf("HandleStatus() error = %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var body struct {
		Success bool                                    `json:"success"`
		Data    []repository.ActiveEmbeddingModelStatus `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !body.Success || len(body.Data) != 1 {
		t.Fatalf("response = %+v, want one successful status", body)
	}
	if body.Data[0].EmbeddingKind != repository.EmbeddingKindStatement ||
		body.Data[0].ModelVersion.ID != modelID {
		t.Fatalf("status data = %+v", body.Data[0])
	}
}

func TestEmbeddingStatusHandlerSurfacesStoreErrors(t *testing.T) {
	handler := NewEmbeddingHandler(fakeEmbeddingStatusStore{err: errors.New("db unavailable")})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/embedding/status", nil)
	c := echo.New().NewContext(req, rec)

	if err := handler.HandleStatus(c); err != nil {
		t.Fatalf("HandleStatus() error = %v", err)
	}
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	if !strings.Contains(rec.Body.String(), "db unavailable") {
		t.Fatalf("response body = %s, want store error", rec.Body.String())
	}
}

func TestEmbeddingLocalTestAcceptsLoopbackOpenAICompatibleEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			t.Fatalf("path = %s, want /v1/embeddings", r.URL.Path)
		}
		var body struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body.Model != "local-embedding" || len(body.Input) != 1 {
			t.Fatalf("request = %+v", body)
		}
		vector := make([]float32, 1536)
		vector[0] = 1
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": "local-embedding",
			"data": []map[string]any{
				{"index": 0, "embedding": vector},
			},
		})
	}))
	defer server.Close()

	handler := NewEmbeddingHandler(fakeEmbeddingStatusStore{})
	payload := map[string]any{
		"base_url": server.URL + "/v1",
		"model":    "local-embedding",
		"api_key":  "local-key",
	}
	body, _ := json.Marshal(payload)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/embedding/local/test", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	c := echo.New().NewContext(req, rec)

	if err := handler.HandleTestLocal(c); err != nil {
		t.Fatalf("HandleTestLocal() error = %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"dimensions":1536`) {
		t.Fatalf("response body = %s, want dimensions", rec.Body.String())
	}
}

func TestEmbeddingLocalTestRejectsPublicEndpoint(t *testing.T) {
	t.Setenv(embeddingUIAllowPublicEnv, "false")
	handler := NewEmbeddingHandler(fakeEmbeddingStatusStore{})
	payload := map[string]any{
		"base_url":   "https://example.com/v1",
		"model":      "public-model",
		"api_key":    "key",
		"dimensions": 1536,
	}
	body, _ := json.Marshal(payload)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/embedding/local/test", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	c := echo.New().NewContext(req, rec)

	if err := handler.HandleTestLocal(c); err != nil {
		t.Fatalf("HandleTestLocal() error = %v", err)
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if !strings.Contains(rec.Body.String(), "not local/private") {
		t.Fatalf("response body = %s, want local/private rejection", rec.Body.String())
	}
}

func TestNormalizeLocalEmbeddingBaseURLAllowsPublicWhenExplicitlyEnabled(t *testing.T) {
	t.Setenv(embeddingUIAllowPublicEnv, "true")

	got, err := normalizeLocalEmbeddingBaseURL("https://api.example.com/v1?ignored=yes#fragment")
	if err != nil {
		t.Fatalf("normalizeLocalEmbeddingBaseURL() error = %v", err)
	}
	if got != "https://api.example.com/v1" {
		t.Fatalf("base URL = %q, want %q", got, "https://api.example.com/v1")
	}
}

func TestEmbeddingDeployPersistsEncryptedRuntimeKey(t *testing.T) {
	store := &fakeEmbeddingSettingsStore{}
	handler := &EmbeddingHandler{
		settingsStore:  store,
		settingsCipher: fakeEmbeddingSettingsCipher{},
	}
	saved, err := handler.persistRuntimeSettings(context.Background(), localEmbeddingDeployRequest{
		Endpoint: localEmbeddingEndpointConfig{
			BaseURL: "http://127.0.0.1:8000/v1/", Model: "local-embedding",
			APIKey: "new-key",
		},
	})
	if err != nil {
		t.Fatalf("persistRuntimeSettings: %v", err)
	}
	if !saved {
		t.Fatal("persistRuntimeSettings saved = false")
	}
	if store.upsert.EncryptedAPIKey != "sealed:new-key" {
		t.Fatalf("encrypted key = %q", store.upsert.EncryptedAPIKey)
	}
	if store.upsert.BaseURL != "http://127.0.0.1:8000/v1" ||
		store.upsert.Model != "local-embedding" || store.upsert.Dimensions != 1536 ||
		store.upsert.TimeoutSec != 30 || store.upsert.UpdatedBy != "embedding-web-admin" {
		t.Fatalf("upsert = %+v", store.upsert)
	}
}

func TestEmbeddingMinimalEndpointUsesMatchingDeploymentKey(t *testing.T) {
	runtimeConfig := config.EmbeddingConfig{
		APIKey: "deployment-key", BaseURL: "http://127.0.0.1:8000/v1",
		Model: "local-embedding", Dimensions: 1536, Enabled: true,
	}
	handler := NewEmbeddingHandler(fakeEmbeddingStatusStore{}, runtimeConfig)
	endpoint, err := handler.endpointWithPersistentKey(context.Background(), localEmbeddingEndpointConfig{
		BaseURL: runtimeConfig.BaseURL,
		Model:   runtimeConfig.Model,
	})
	if err != nil {
		t.Fatalf("endpointWithPersistentKey() error = %v", err)
	}
	if endpoint.APIKey != runtimeConfig.APIKey {
		t.Fatal("minimal endpoint did not use the matching deployment key")
	}
}

func TestEmbeddingRuntimeSettingsKeepsDeploymentIdentity(t *testing.T) {
	configured := config.EmbeddingConfig{
		BaseURL: "http://127.0.0.1:9000/v1/", Model: "configured-model",
		Dimensions: 3, TimeoutSec: 17, Enabled: true,
		ExpectedStatementModelVersionID: "44444444-4444-4444-4444-444444444444",
	}
	updatedAt := time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC)
	store := &fakeEmbeddingSettingsStore{record: &repository.EmbeddingProviderSettingRecord{
		BaseURL: "http://127.0.0.1:8000/v1", Model: "stale-model",
		Dimensions: 1536, TimeoutSec: 60, EncryptedAPIKey: "sealed:stale-key",
		UpdatedBy: "old-operator", UpdatedAt: updatedAt,
	}}
	handler := NewEmbeddingHandler(fakeEmbeddingStatusStore{}, configured)
	handler.settingsStore = store

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/embedding/runtime-settings", nil)
	if err := handler.HandleRuntimeSettings(echo.New().NewContext(req, rec)); err != nil {
		t.Fatalf("HandleRuntimeSettings() error = %v", err)
	}
	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	data, ok := body["data"].(map[string]any)
	if rec.Code != http.StatusOK || !ok {
		t.Fatalf("response = %d %v", rec.Code, body)
	}
	if data["base_url"] != "http://127.0.0.1:9000/v1" ||
		data["provider_id"] != configured.ProviderID() ||
		data["model"] != "configured-model" ||
		data["statement_model_version_id"] != configured.ExpectedStatementModelVersionID {
		t.Fatalf("runtime settings = %+v, want deployment identity", data)
	}
	if data["api_key_configured"] != false || data["source"] != "deployment" {
		t.Fatalf("runtime settings = %+v, stale saved key must not contribute", data)
	}
}

func TestEmbeddingRuntimeSettingsUsesOnlyMatchingSavedKey(t *testing.T) {
	configured := config.EmbeddingConfig{
		BaseURL: "http://127.0.0.1:9000/v1", Model: "configured-model",
		Dimensions: 3, TimeoutSec: 17, Enabled: true,
	}
	store := &fakeEmbeddingSettingsStore{record: &repository.EmbeddingProviderSettingRecord{
		BaseURL: "http://127.0.0.1:9000/v1/", Model: "configured-model",
		Dimensions: 3, TimeoutSec: 60, EncryptedAPIKey: "sealed:matching-key",
		UpdatedBy: "operator", UpdatedAt: time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC),
	}}
	handler := NewEmbeddingHandler(fakeEmbeddingStatusStore{}, configured)
	handler.settingsStore = store

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/embedding/runtime-settings", nil)
	if err := handler.HandleRuntimeSettings(echo.New().NewContext(req, rec)); err != nil {
		t.Fatalf("HandleRuntimeSettings() error = %v", err)
	}
	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	data := body["data"].(map[string]any)
	if data["api_key_configured"] != true || data["updated_by"] != "operator" {
		t.Fatalf("runtime settings = %+v, matching saved key should contribute", data)
	}
	if data["base_url"] != configured.BaseURL || data["model"] != configured.Model ||
		data["timeout_sec"] != float64(configured.TimeoutSec) {
		t.Fatalf("runtime settings = %+v, saved row must not replace deployment identity", data)
	}
}

func TestEmbeddingDeploymentIdentityComesFromTestedEndpoint(t *testing.T) {
	tested := localEmbeddingTestResult{
		ProviderID: "openai-compatible:http://127.0.0.1:8000/v1",
		Model:      "served-model", BaseURL: "http://127.0.0.1:8000/v1", Dimensions: 3,
	}
	if err := validateLocalDeploymentIdentity(localEmbeddingDeployRequest{
		Provider: "local-openai-compatible",
	}, tested); err == nil {
		t.Fatal("provider override should be rejected")
	}
	if err := validateLocalDeploymentIdentity(localEmbeddingDeployRequest{
		ModelID: "local-embedding",
	}, tested); err == nil {
		t.Fatal("model_id override should be rejected")
	}
	registration := normalizeLocalDeploymentRegistration(localEmbeddingDeployRequest{}, tested)
	if registration.Provider != tested.ProviderID ||
		registration.ModelID != tested.Model ||
		registration.Dimensions != tested.Dimensions {
		t.Fatalf("registration = %+v, want tested endpoint identity", registration)
	}
	if registration.Revision != "local-runtime" ||
		len(registration.WeightsHash) != 64 ||
		registration.Normalization != "mrl-1536+l2" ||
		registration.Quantization != "float32" ||
		registration.InstructionTemplate == "" ||
		len(registration.IndexParams) == 0 {
		t.Fatalf("registration defaults = %+v", registration)
	}
}

func TestEmbeddingBackfillDryRunRejectsTargetIdentityWithoutCallingEndpoint(t *testing.T) {
	modelVersionID := uuid.MustParse("33333333-3333-3333-3333-333333333333")
	endpoint := localEmbeddingEndpointConfig{
		BaseURL: "http://127.0.0.1:1/v1", Model: "configured-model",
		APIKey: "key",
	}
	endpointProvider := (config.EmbeddingConfig{BaseURL: endpoint.BaseURL}).ProviderID()
	admin := &fakeEmbeddingAdminStore{modelVersion: repository.EmbeddingModelVersionRecord{
		ID: modelVersionID, Provider: endpointProvider, ModelID: "different-model", Dimensions: 1536,
	}}
	handler := NewEmbeddingHandler(admin)
	payload := map[string]any{
		"endpoint": endpoint, "model_version_id": modelVersionID.String(),
	}
	encoded, _ := json.Marshal(payload)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/embedding/local/backfill", strings.NewReader(string(encoded)))
	req.Header.Set("Content-Type", "application/json")
	if err := handler.HandleBackfillLocal(echo.New().NewContext(req, rec)); err != nil {
		t.Fatalf("HandleBackfillLocal() error = %v", err)
	}
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "EMBEDDING_IDENTITY_MISMATCH") {
		t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
	}
	if len(admin.planCalls) != 0 || len(admin.updateCalls) != 0 {
		t.Fatalf("backfill advanced after mismatch: plans=%d writes=%d", len(admin.planCalls), len(admin.updateCalls))
	}
}

func TestEmbeddingActivateCannotBypassConfiguredModelVersion(t *testing.T) {
	expectedID := uuid.MustParse("44444444-4444-4444-4444-444444444444")
	targetID := uuid.MustParse("55555555-5555-5555-5555-555555555555")
	runtimeConfig := config.EmbeddingConfig{
		BaseURL: "http://127.0.0.1:8000/v1", Model: "configured-model", Dimensions: 3,
		Enabled: true, ExpectedStatementModelVersionID: expectedID.String(),
	}
	admin := &fakeEmbeddingAdminStore{modelVersion: repository.EmbeddingModelVersionRecord{
		ID: targetID, Provider: runtimeConfig.ProviderID(),
		ModelID: runtimeConfig.Model, Dimensions: runtimeConfig.Dimensions,
	}}
	handler := NewEmbeddingHandler(admin, runtimeConfig)
	payload := map[string]any{
		"model_version_id": targetID.String(), "embedding_kinds": []string{"statement"},
		"dry_run": false, "allow_runtime_mismatch": true,
	}
	encoded, _ := json.Marshal(payload)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/embedding/local/activate", strings.NewReader(string(encoded)))
	req.Header.Set("Content-Type", "application/json")
	if err := handler.HandleActivateLocal(echo.New().NewContext(req, rec)); err != nil {
		t.Fatalf("HandleActivateLocal() error = %v", err)
	}
	if rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), "\"runtime_matches\":false") ||
		!strings.Contains(rec.Body.String(), "\"reports\":[]") ||
		!strings.Contains(rec.Body.String(), "activation_blocked_reason") {
		t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
	}
	if len(admin.switchCalls) != 0 {
		t.Fatalf("switch called %d times despite configured version mismatch", len(admin.switchCalls))
	}
}

func TestEmbeddingActivateSwitchesExactConfiguredModelVersion(t *testing.T) {
	targetID := uuid.MustParse("66666666-6666-6666-6666-666666666666")
	runtimeConfig := config.EmbeddingConfig{
		BaseURL: "http://127.0.0.1:8000/v1", Model: "configured-model", Dimensions: 3,
		Enabled: true, ExpectedStatementModelVersionID: targetID.String(),
	}
	admin := &fakeEmbeddingAdminStore{modelVersion: repository.EmbeddingModelVersionRecord{
		ID: targetID, Provider: runtimeConfig.ProviderID(),
		ModelID: runtimeConfig.Model, Dimensions: runtimeConfig.Dimensions,
	}}
	handler := NewEmbeddingHandler(admin, runtimeConfig)
	payload := map[string]any{
		"model_version_id": targetID.String(), "embedding_kinds": []string{"statement"}, "dry_run": false,
	}
	encoded, _ := json.Marshal(payload)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/embedding/local/activate", strings.NewReader(string(encoded)))
	req.Header.Set("Content-Type", "application/json")
	if err := handler.HandleActivateLocal(echo.New().NewContext(req, rec)); err != nil {
		t.Fatalf("HandleActivateLocal() error = %v", err)
	}
	if rec.Code != http.StatusOK || len(admin.switchCalls) != 1 {
		t.Fatalf("response = %d %s, switch calls = %d", rec.Code, rec.Body.String(), len(admin.switchCalls))
	}
}
