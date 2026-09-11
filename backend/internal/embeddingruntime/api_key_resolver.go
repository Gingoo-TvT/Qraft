// Package embeddingruntime connects the encrypted embedding settings store to
// the long-running worker embedding client.
package embeddingruntime

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Gingoo-TvT/Qraft/backend/internal/config"
	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
)

type SettingsStore interface {
	GetEmbeddingProviderSetting(context.Context) (repository.EmbeddingProviderSettingRecord, error)
}

type SecretCipher interface {
	Open(encoded string) (string, error)
}

// APIKeyResolver returns a stored key only when its endpoint identity matches
// the worker runtime. A deployment for a different vector space therefore
// cannot silently redirect an already-running worker.
type APIKeyResolver struct {
	store    SettingsStore
	cipher   SecretCipher
	fallback config.EmbeddingConfig
}

func NewAPIKeyResolver(
	store SettingsStore,
	cipher SecretCipher,
	fallback config.EmbeddingConfig,
) *APIKeyResolver {
	return &APIKeyResolver{store: store, cipher: cipher, fallback: fallback}
}

func (r *APIKeyResolver) ResolveEmbeddingAPIKey(
	ctx context.Context,
	baseURL, model string,
	dimensions int,
) (string, error) {
	if r == nil || r.store == nil || r.cipher == nil {
		return "", fmt.Errorf("persistent embedding key resolver is unavailable")
	}
	record, err := r.store.GetEmbeddingProviderSetting(ctx)
	if errors.Is(err, repository.ErrEmbeddingProviderSettingNotFound) {
		return r.fallbackKey()
	}
	if err != nil {
		return "", err
	}
	if normalizeBaseURL(record.BaseURL) != normalizeBaseURL(baseURL) ||
		strings.TrimSpace(record.Model) != strings.TrimSpace(model) ||
		record.Dimensions != dimensions {
		return r.fallbackKey()
	}
	key, err := r.cipher.Open(record.EncryptedAPIKey)
	if err != nil {
		return "", fmt.Errorf("decrypt persistent embedding key: %w", err)
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return "", fmt.Errorf("persistent embedding key is empty")
	}
	return key, nil
}

func (r *APIKeyResolver) fallbackKey() (string, error) {
	key := strings.TrimSpace(r.fallback.APIKey)
	if key == "" {
		return "", fmt.Errorf("no matching persistent embedding key and deployment key is empty")
	}
	return key, nil
}

func normalizeBaseURL(value string) string {
	return strings.TrimRight(strings.TrimSpace(value), "/")
}
