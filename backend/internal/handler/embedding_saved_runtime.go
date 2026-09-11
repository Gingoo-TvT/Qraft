package handler

import (
	"errors"
	"github.com/Gingoo-TvT/Qraft/backend/internal/config"
	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"strings"
)

type savedEmbeddingRuntimeView struct {
	Configured     bool   `json:"configured"`
	BaseURL        string `json:"base_url,omitempty"`
	Model          string `json:"model,omitempty"`
	Dimensions     int    `json:"dimensions,omitempty"`
	TimeoutSec     int    `json:"timeout_sec,omitempty"`
	ModelVersionID string `json:"model_version_id,omitempty"`
}

// HandleSavedRuntimeSettings returns only the identity already tested and saved
// by embedding administration. It does not decrypt keys, select an active pointer,
// backfill data or change the running API/worker. Deployment remains explicit.
func (h *EmbeddingHandler) HandleSavedRuntimeSettings(c echo.Context) error {
	if h == nil || h.settingsStore == nil || h.adminRepo == nil {
		return internalError(c, "embedding settings store is unavailable")
	}
	record, err := h.settingsStore.GetEmbeddingProviderSetting(c.Request().Context())
	if errors.Is(err, repository.ErrEmbeddingProviderSettingNotFound) {
		return ok(c, savedEmbeddingRuntimeView{})
	}
	if err != nil {
		return internalError(c, "failed to read saved embedding settings")
	}
	provider := (config.EmbeddingConfig{BaseURL: record.BaseURL}).ProviderID()
	var matches []repository.EmbeddingModelVersionRecord
	if selected := strings.TrimSpace(c.QueryParam("model_version_id")); selected != "" {
		id, err := uuid.Parse(selected)
		if err != nil {
			return badRequest(c, "INVALID_MODEL_VERSION_ID", "invalid embedding model version")
		}
		version, err := h.adminRepo.GetEmbeddingModelVersion(c.Request().Context(), id)
		if err != nil {
			return badRequest(c, "EMBEDDING_MODEL_NOT_FOUND", "selected embedding model is unavailable")
		}
		if embeddingModelVersionMatchesIdentity(version, provider, record.Model, record.Dimensions) {
			matches = append(matches, version)
		}
	} else {
		versions, err := h.adminRepo.ListEmbeddingModelVersions(c.Request().Context(), 100)
		if err != nil {
			return internalError(c, "failed to read embedding model identities")
		}
		for _, version := range versions {
			if embeddingModelVersionMatchesIdentity(version, provider, record.Model, record.Dimensions) {
				matches = append(matches, version)
			}
		}
	}
	if len(matches) != 1 {
		return badRequest(c, "EMBEDDING_SELECTION_REQUIRED", "saved embedding settings need an explicit matching model_version_id; choose the version registered by the deployment page")
	}
	return ok(c, savedEmbeddingRuntimeView{Configured: true, BaseURL: record.BaseURL, Model: record.Model, Dimensions: record.Dimensions, TimeoutSec: record.TimeoutSec, ModelVersionID: matches[0].ID.String()})
}
