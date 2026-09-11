package config

import (
	"strings"
	"testing"

	"github.com/spf13/viper"
)

func TestAnthropicProviderID(t *testing.T) {
	if got := (AnthropicConfig{}).ProviderID(); got != "anthropic" {
		t.Fatalf("official endpoint provider = %q", got)
	}
	if got := (AnthropicConfig{BaseURL: "https://proxy.example"}).ProviderID(); got != "" {
		t.Fatalf("unidentified proxy provider = %q, want empty", got)
	}
	if got := (AnthropicConfig{BaseURL: "https://proxy.example", Provider: " proxy-vendor "}).ProviderID(); got != "proxy-vendor" {
		t.Fatalf("explicit proxy provider = %q", got)
	}
}

func TestValidateAllowsCredentialFreeBootstrap(t *testing.T) {
	cfg := validOutboxConfig()
	cfg.Anthropic.APIKey = ""
	if err := Validate(cfg); err != nil {
		t.Fatalf("credential-free bootstrap rejected: %v", err)
	}
}

func TestModelRoutingFeatureFlagDefaultsOffAndUsesExplicitEnv(t *testing.T) {
	v := viper.New()
	setDefaults(v)
	if v.GetBool("app.model_routing_enabled") {
		t.Fatal("model routing default must be disabled")
	}

	t.Setenv("ALGOFORGE_QG14_MODEL_ROUTING_ENABLED", "true")
	v = viper.New()
	setDefaults(v)
	if !v.GetBool("app.model_routing_enabled") {
		t.Fatal("explicit QG-14 model routing environment flag was not bound")
	}
}

func TestValidateRejectsUnsafeAnthropicBaseURL(t *testing.T) {
	for _, baseURL := range []string{
		"https://user:secret@llm.example.com/v1",
		"https://llm.example.com/v1?token=secret",
		"https://llm.example.com/v1#fragment",
	} {
		t.Run(baseURL, func(t *testing.T) {
			cfg := validOutboxConfig()
			cfg.Anthropic = AnthropicConfig{BaseURL: baseURL, Provider: "provider"}
			if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "anthropic.base_url") {
				t.Fatalf("unsafe base_url error = %v", err)
			}
		})
	}
}

func TestEmbeddingProviderIDIsStableAndCredentialFree(t *testing.T) {
	cfg := EmbeddingConfig{BaseURL: "HTTPS://user:secret@Embeddings.Example:443/v1/?token=private"}
	if got, want := cfg.ProviderID(), "openai-compatible:https://embeddings.example:443/v1"; got != want {
		t.Fatalf("embedding provider = %q, want %q", got, want)
	}
	if got := (EmbeddingConfig{BaseURL: "relative/path"}).ProviderID(); got != "" {
		t.Fatalf("relative endpoint provider = %q, want empty", got)
	}
}

func TestValidateEmbeddingExpectedStatementModelVersionID(t *testing.T) {
	cfg := validOutboxConfig()
	cfg.Embedding.ExpectedStatementModelVersionID = "not-a-uuid"
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "embedding.expected_statement_model_version_id") {
		t.Fatalf("invalid expected statement model version error = %v", err)
	}

	cfg.Embedding.ExpectedStatementModelVersionID = "11111111-1111-1111-1111-111111111111"
	if err := Validate(cfg); err != nil {
		t.Fatalf("valid expected statement model version: %v", err)
	}
}
