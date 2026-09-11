package embeddingruntime

import (
	"context"
	"errors"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/config"
	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
)

type fakeSettingsStore struct {
	record repository.EmbeddingProviderSettingRecord
	err    error
}

func (s fakeSettingsStore) GetEmbeddingProviderSetting(context.Context) (repository.EmbeddingProviderSettingRecord, error) {
	return s.record, s.err
}

type fakeCipher struct {
	plaintext string
	err       error
}

func (c fakeCipher) Open(string) (string, error) { return c.plaintext, c.err }

func TestAPIKeyResolverUsesMatchingStoredKey(t *testing.T) {
	resolver := NewAPIKeyResolver(fakeSettingsStore{record: repository.EmbeddingProviderSettingRecord{
		BaseURL: "https://api.example.com/v1/", Model: "text-embedding-3-large", Dimensions: 1536,
		EncryptedAPIKey: "sealed",
	}}, fakeCipher{plaintext: "stored-key"}, config.EmbeddingConfig{APIKey: "environment-key"})

	got, err := resolver.ResolveEmbeddingAPIKey(context.Background(), "https://api.example.com/v1", "text-embedding-3-large", 1536)
	if err != nil {
		t.Fatalf("ResolveEmbeddingAPIKey: %v", err)
	}
	if got != "stored-key" {
		t.Fatalf("key = %q, want stored key", got)
	}
}

func TestAPIKeyResolverFallsBackForMissingOrMismatchedSetting(t *testing.T) {
	tests := []fakeSettingsStore{
		{err: repository.ErrEmbeddingProviderSettingNotFound},
		{record: repository.EmbeddingProviderSettingRecord{
			BaseURL: "https://other.example/v1", Model: "text-embedding-3-large", Dimensions: 1536,
			EncryptedAPIKey: "sealed",
		}},
	}
	for _, store := range tests {
		resolver := NewAPIKeyResolver(store, fakeCipher{plaintext: "stored-key"}, config.EmbeddingConfig{APIKey: "environment-key"})
		got, err := resolver.ResolveEmbeddingAPIKey(context.Background(), "https://api.example.com/v1", "text-embedding-3-large", 1536)
		if err != nil {
			t.Fatalf("ResolveEmbeddingAPIKey: %v", err)
		}
		if got != "environment-key" {
			t.Fatalf("key = %q, want environment fallback", got)
		}
	}
}

func TestAPIKeyResolverFailsClosedOnDecryptError(t *testing.T) {
	resolver := NewAPIKeyResolver(fakeSettingsStore{record: repository.EmbeddingProviderSettingRecord{
		BaseURL: "https://api.example.com/v1", Model: "text-embedding-3-large", Dimensions: 1536,
		EncryptedAPIKey: "broken",
	}}, fakeCipher{err: errors.New("bad ciphertext")}, config.EmbeddingConfig{APIKey: "environment-key"})

	if _, err := resolver.ResolveEmbeddingAPIKey(context.Background(), "https://api.example.com/v1", "text-embedding-3-large", 1536); err == nil {
		t.Fatal("ResolveEmbeddingAPIKey returned nil error")
	}
}
