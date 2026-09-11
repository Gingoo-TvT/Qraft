package handler

import (
	"context"
	"encoding/json"
	"github.com/Gingoo-TvT/Qraft/backend/internal/config"
	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"net/http/httptest"
	"strings"
	"testing"
)

type savedRuntimeAdmin struct {
	fakeEmbeddingAdminStore
	versions []repository.EmbeddingModelVersionRecord
}

func (s *savedRuntimeAdmin) ListEmbeddingModelVersions(context.Context, int) ([]repository.EmbeddingModelVersionRecord, error) {
	return s.versions, nil
}
func TestSavedEmbeddingIdentityHasNoSecretsAndDoesNotActivate(t *testing.T) {
	id := uuid.New()
	endpoint := "http://127.0.0.1:19000/v1"
	version := repository.EmbeddingModelVersionRecord{ID: id, Provider: (config.EmbeddingConfig{BaseURL: endpoint}).ProviderID(), ModelID: "fixture", Dimensions: 1536}
	admin := &savedRuntimeAdmin{versions: []repository.EmbeddingModelVersionRecord{version}}
	store := &fakeEmbeddingSettingsStore{}
	h := NewEmbeddingHandler(admin)
	h.SetPersistentRuntimeSettings(store, fakeEmbeddingSettingsCipher{})
	invoke := func() (*httptest.ResponseRecorder, map[string]any) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/embedding/saved-runtime-settings", nil)
		if err := h.HandleSavedRuntimeSettings(echo.New().NewContext(req, rec)); err != nil {
			t.Fatal(err)
		}
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return rec, body
	}
	_, body := invoke()
	if body["data"].(map[string]any)["configured"] != false {
		t.Fatal(body)
	}
	store.record = &repository.EmbeddingProviderSettingRecord{BaseURL: endpoint, Model: "fixture", Dimensions: 1536, TimeoutSec: 30, EncryptedAPIKey: "sealed:private-fixture-key"}
	rec, body := invoke()
	if rec.Code != 200 || body["data"].(map[string]any)["model_version_id"] != id.String() {
		t.Fatal(rec.Code, body)
	}
	if strings.Contains(rec.Body.String(), "private-fixture-key") || strings.Contains(rec.Body.String(), "encrypted_api_key") {
		t.Fatal("secret exposed")
	}
	if len(admin.switchCalls) != 0 || len(admin.planCalls) != 0 || admin.registerInput != nil {
		t.Fatal("read mutated vector lifecycle")
	}
	admin.versions = append(admin.versions, version)
	rec, _ = invoke()
	if rec.Code != 400 {
		t.Fatal("ambiguous model silently selected")
	}
}
