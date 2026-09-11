package config

import (
	"strings"
	"testing"
	"time"
)

func validOutboxConfig() *Config {
	return &Config{
		Database:  DatabaseConfig{Host: "db", User: "user", DBName: "db"},
		MinIO:     MinIOConfig{AccessKey: "key", SecretKey: "secret"},
		Temporal:  TemporalConfig{Host: "temporal:7233", WorkerConcurrency: 1, ProviderEffectLease: 45 * time.Minute},
		Anthropic: AnthropicConfig{APIKey: "key"},
		App:       AppConfig{JWTSecret: "secret"},
		Outbox: OutboxConfig{
			PollInterval:       time.Second,
			BatchSize:          2,
			LeaseDuration:      21 * time.Second,
			MaxAttempts:        3,
			ConnectTimeout:     time.Second,
			RequestTimeout:     10 * time.Second,
			MaxRequestBytes:    1024,
			MaxResponseBytes:   1024,
			ReadinessMaxQueued: 10,
			ReadinessMaxAge:    time.Minute,
			HealthAddr:         ":8092",
			HMACSecret:         strings.Repeat("h", 32),
		},
		Provenance: ProvenanceConfig{
			RetentionEnabled:    true,
			RetentionInterval:   time.Hour,
			RetentionBatchSize:  1000,
			RetentionMaxBatches: 100,
		},
	}
}

func TestValidateOutboxProductionEndpoint(t *testing.T) {
	cfg := validOutboxConfig()
	cfg.Outbox.Enabled = true
	cfg.Outbox.Endpoint = "https://events.example.test/deliver"
	cfg.Outbox.BearerToken = "token"
	if err := Validate(cfg); err != nil {
		t.Fatalf("valid outbox config: %v", err)
	}

	cfg.Outbox.Endpoint = "http://events.example.test/deliver"
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "absolute HTTPS") {
		t.Fatalf("insecure production endpoint error = %v", err)
	}

	cfg.App.DevMode = true
	cfg.App.JWTSecret = ""
	if err := Validate(cfg); err != nil {
		t.Fatalf("dev HTTP endpoint: %v", err)
	}
}

func TestValidateOutboxRequiresCredentialAndSafeLease(t *testing.T) {
	cfg := validOutboxConfig()
	cfg.Outbox.Enabled = true
	cfg.Outbox.Endpoint = "https://events.example.test/deliver"
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "bearer_token") {
		t.Fatalf("missing credential error = %v", err)
	}

	cfg.Outbox.BearerToken = "token"
	cfg.Outbox.LeaseDuration = 20 * time.Second
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "batch_size * request_timeout") {
		t.Fatalf("unsafe lease error = %v", err)
	}
}

func TestValidateOutboxRequiresStrongHMACSecret(t *testing.T) {
	cfg := validOutboxConfig()
	cfg.Outbox.Enabled = true
	cfg.Outbox.Endpoint = "https://events.example.test/deliver"
	cfg.Outbox.BearerToken = "token"
	cfg.Outbox.HMACSecret = "too-short"
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "at least 32 bytes") {
		t.Fatalf("weak HMAC secret error = %v", err)
	}
}

func TestValidateDisabledOutboxStillRequiresMonitorSettings(t *testing.T) {
	cfg := validOutboxConfig()
	if err := Validate(cfg); err != nil {
		t.Fatalf("explicit disabled outbox: %v", err)
	}
	cfg.Outbox.PollInterval = 0
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "poll_interval") {
		t.Fatalf("invalid monitor settings error = %v", err)
	}
}
