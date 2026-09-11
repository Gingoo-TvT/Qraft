package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/config"
	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
	"github.com/Gingoo-TvT/Qraft/backend/internal/workflow/activities"
	"github.com/google/uuid"
)

type workerProviderEffectStore struct{}

func (workerProviderEffectStore) Acquire(context.Context, string, string, string, time.Duration) (repository.ProviderEffectClaim, error) {
	return repository.ProviderEffectClaim{}, nil
}

func (workerProviderEffectStore) Complete(context.Context, string, string, string, uuid.UUID, json.RawMessage) error {
	return nil
}

func (workerProviderEffectStore) Fail(context.Context, string, string, string, uuid.UUID, string) error {
	return nil
}

func TestConfigureProviderEffectDependenciesWiresProductionIdentity(t *testing.T) {
	deps := &activities.Dependencies{}
	cfg := &config.Config{
		Anthropic: config.AnthropicConfig{Provider: "provider", Model: "llm-model", BaseURL: "https://llm.example/v1"},
		Embedding: config.EmbeddingConfig{BaseURL: "https://embedding.example/v1", Model: "embedding-model", Enabled: true},
		Temporal:  config.TemporalConfig{ProviderEffectLease: 30 * time.Minute},
	}
	store := workerProviderEffectStore{}
	resolvedID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	if err := configureProviderEffectDependencies(deps, store, cfg, resolvedID); err != nil {
		t.Fatalf("configure provider effects: %v", err)
	}
	if deps.ProviderEffects == nil || deps.ProviderEffectLease != 30*time.Minute {
		t.Fatalf("provider effect wiring = %+v", deps)
	}
	if deps.LLMProvider != "provider" || deps.LLMModel != "llm-model" || deps.LLMBaseURL != "https://llm.example/v1" ||
		deps.EmbeddingProvider != "openai-compatible:https://embedding.example/v1" ||
		deps.EmbeddingModel != "embedding-model" ||
		deps.EmbeddingModelVersionID != resolvedID {
		t.Fatalf("provider identities = %+v", deps)
	}
}

func TestConfigureProviderEffectDependenciesRejectsDisabledEmbedding(t *testing.T) {
	deps := &activities.Dependencies{}
	cfg := &config.Config{
		Anthropic: config.AnthropicConfig{Provider: "provider", Model: "llm-model"},
		Embedding: config.EmbeddingConfig{Enabled: false},
		Temporal:  config.TemporalConfig{ProviderEffectLease: 30 * time.Minute},
	}
	err := configureProviderEffectDependencies(deps, workerProviderEffectStore{}, cfg, uuid.Nil)
	if err == nil || !strings.Contains(err.Error(), "embedding must be enabled") {
		t.Fatalf("disabled embedding error = %v", err)
	}
}

func TestValidateWorkerConfigurationRejectsNilConfig(t *testing.T) {
	if err := validateWorkerConfiguration(nil); err == nil {
		t.Fatal("nil worker config was accepted")
	}
}

func TestConfigureProviderEffectDependenciesRejectsPartialWiring(t *testing.T) {
	cfg := &config.Config{
		Anthropic: config.AnthropicConfig{Provider: "provider", Model: "llm-model"},
		Embedding: config.EmbeddingConfig{BaseURL: "https://embedding.example/v1", Model: "embedding-model", Enabled: true, ExpectedStatementModelVersionID: "11111111-1111-1111-1111-111111111111"},
		Temporal:  config.TemporalConfig{ProviderEffectLease: 30 * time.Minute},
	}
	err := configureProviderEffectDependencies(
		&activities.Dependencies{}, nil, cfg,
		uuid.MustParse("11111111-1111-1111-1111-111111111111"),
	)
	if err == nil || !strings.Contains(err.Error(), "store") {
		t.Fatalf("missing store error = %v", err)
	}
}

func TestValidateWorkerConfigurationAllowsOptionalStatementModelVersionPin(t *testing.T) {
	cfg := &config.Config{Embedding: config.EmbeddingConfig{Enabled: true}}
	if err := validateWorkerConfiguration(cfg); err != nil {
		t.Fatalf("optional configured statement model version pin: %v", err)
	}
}

func TestConfigureProviderEffectDependenciesRequiresResolvedStatementVersion(t *testing.T) {
	cfg := &config.Config{
		Anthropic: config.AnthropicConfig{Provider: "provider", Model: "llm-model"},
		Embedding: config.EmbeddingConfig{BaseURL: "https://embedding.example/v1", Model: "embedding-model", Enabled: true},
		Temporal:  config.TemporalConfig{ProviderEffectLease: 30 * time.Minute},
	}
	err := configureProviderEffectDependencies(&activities.Dependencies{}, workerProviderEffectStore{}, cfg, uuid.Nil)
	if err == nil || !strings.Contains(err.Error(), "configured statement model version") {
		t.Fatalf("missing resolved statement model version error = %v", err)
	}
}
