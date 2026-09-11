package config

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/viper"
)

// Config holds all configuration for the AlgoForge application.
type Config struct {
	Database   DatabaseConfig   `mapstructure:"database"`
	Redis      RedisConfig      `mapstructure:"redis"`
	MinIO      MinIOConfig      `mapstructure:"minio"`
	Temporal   TemporalConfig   `mapstructure:"temporal"`
	Anthropic  AnthropicConfig  `mapstructure:"anthropic"`
	Embedding  EmbeddingConfig  `mapstructure:"embedding"`
	Sandbox    SandboxConfig    `mapstructure:"sandbox"`
	Outbox     OutboxConfig     `mapstructure:"outbox"`
	Provenance ProvenanceConfig `mapstructure:"provenance"`
	App        AppConfig        `mapstructure:"app"`
}

// DatabaseConfig holds PostgreSQL connection settings.
type DatabaseConfig struct {
	Host    string `mapstructure:"host"`
	Port    int    `mapstructure:"port"`
	User    string `mapstructure:"user"`
	Pass    string `mapstructure:"pass"`
	DBName  string `mapstructure:"dbname"`
	SSLMode string `mapstructure:"sslmode"`
}

// DSN returns the PostgreSQL connection string.
func (d DatabaseConfig) DSN() string {
	return fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		d.Host, d.Port, d.User, d.Pass, d.DBName, d.SSLMode,
	)
}

// RedisConfig holds Redis connection settings.
type RedisConfig struct {
	Addr     string `mapstructure:"addr"`
	Password string `mapstructure:"password"`
	DB       int    `mapstructure:"db"`
}

// MinIOConfig holds MinIO object storage settings.
type MinIOConfig struct {
	Endpoint  string `mapstructure:"endpoint"`
	AccessKey string `mapstructure:"access_key"`
	SecretKey string `mapstructure:"secret_key"`
	Bucket    string `mapstructure:"bucket"`
	UseSSL    bool   `mapstructure:"use_ssl"`
}

// TemporalConfig holds Temporal workflow engine settings.
type TemporalConfig struct {
	Host                    string        `mapstructure:"host"`
	Namespace               string        `mapstructure:"namespace"`
	TaskQueue               string        `mapstructure:"task_queue"`
	WorkerBuildID           string        `mapstructure:"worker_build_id"`
	UseBuildIDForVersioning bool          `mapstructure:"use_build_id_for_versioning"`
	WorkerConcurrency       int           `mapstructure:"worker_concurrency"`
	ProviderEffectLease     time.Duration `mapstructure:"provider_effect_lease"`
}

// AnthropicConfig holds Anthropic API settings.
// Supports direct Anthropic API or third-party proxies (e.g. CC Switch,
// OpenRouter) by allowing a custom BaseURL.
type AnthropicConfig struct {
	APIKey          string `mapstructure:"api_key"`
	BaseURL         string `mapstructure:"base_url"` // leave empty for official Anthropic API
	Model           string `mapstructure:"model"`
	Provider        string `mapstructure:"provider"`
	ReasoningEffort string `mapstructure:"reasoning_effort"`
}

// ProviderID returns the auditable provider identity. A custom endpoint cannot
// inherit "anthropic" merely because it implements the Messages protocol.
func (a AnthropicConfig) ProviderID() string {
	if provider := strings.TrimSpace(a.Provider); provider != "" {
		return provider
	}
	if strings.TrimSpace(a.BaseURL) == "" {
		return "anthropic"
	}
	return ""
}

func validateAnthropicBaseURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if strings.Contains(raw, "#") {
		return fmt.Errorf("must not contain a fragment")
	}
	endpoint, err := url.ParseRequestURI(raw)
	if err != nil || endpoint.Scheme == "" || endpoint.Host == "" {
		return fmt.Errorf("must be an absolute http(s) URL")
	}
	scheme := strings.ToLower(endpoint.Scheme)
	if scheme != "https" && scheme != "http" {
		return fmt.Errorf("must use http or https")
	}
	if endpoint.User != nil {
		return fmt.Errorf("must not contain credentials")
	}
	if endpoint.RawQuery != "" || endpoint.ForceQuery {
		return fmt.Errorf("must not contain a query")
	}
	if endpoint.Fragment != "" {
		return fmt.Errorf("must not contain a fragment")
	}
	return nil
}

// EmbeddingConfig holds OpenAI-compatible embedding API settings.
type EmbeddingConfig struct {
	APIKey                          string `mapstructure:"api_key"`
	BaseURL                         string `mapstructure:"base_url"`
	Model                           string `mapstructure:"model"`
	Dimensions                      int    `mapstructure:"dimensions"`
	TimeoutSec                      int    `mapstructure:"timeout_sec"`
	Enabled                         bool   `mapstructure:"enabled"`
	ExpectedStatementModelVersionID string `mapstructure:"expected_statement_model_version_id"`
}

// ProviderID returns a credential-free identity for the configured
// OpenAI-compatible endpoint. The endpoint path is part of the identity so a
// serving route change cannot reuse provider-effect results from another route.
func (e EmbeddingConfig) ProviderID() string {
	endpoint, err := url.ParseRequestURI(strings.TrimSpace(e.BaseURL))
	if err != nil || endpoint.Scheme == "" || endpoint.Host == "" {
		return ""
	}
	endpoint.Scheme = strings.ToLower(endpoint.Scheme)
	endpoint.Host = strings.ToLower(endpoint.Host)
	endpoint.User = nil
	endpoint.RawQuery = ""
	endpoint.ForceQuery = false
	endpoint.Fragment = ""
	return "openai-compatible:" + strings.TrimRight(endpoint.String(), "/")
}

// SandboxConfig holds code sandbox execution settings.
type SandboxConfig struct {
	Binary     string        `mapstructure:"binary"`
	ConfigPath string        `mapstructure:"config_path"`
	Timeout    time.Duration `mapstructure:"timeout"`
}

// OutboxConfig controls durable publication of transactional outbox events.
// The endpoint is process configuration, never event-controlled data.
type OutboxConfig struct {
	Enabled            bool          `mapstructure:"enabled"`
	Endpoint           string        `mapstructure:"endpoint"`
	BearerToken        string        `mapstructure:"bearer_token"`
	HMACSecret         string        `mapstructure:"hmac_secret"`
	PollInterval       time.Duration `mapstructure:"poll_interval"`
	BatchSize          int           `mapstructure:"batch_size"`
	LeaseDuration      time.Duration `mapstructure:"lease_duration"`
	MaxAttempts        int           `mapstructure:"max_attempts"`
	ConnectTimeout     time.Duration `mapstructure:"connect_timeout"`
	RequestTimeout     time.Duration `mapstructure:"request_timeout"`
	MaxRequestBytes    int64         `mapstructure:"max_request_bytes"`
	MaxResponseBytes   int64         `mapstructure:"max_response_bytes"`
	ReadinessMaxQueued int64         `mapstructure:"readiness_max_queued"`
	ReadinessMaxAge    time.Duration `mapstructure:"readiness_max_age"`
	HealthAddr         string        `mapstructure:"health_addr"`
}

// ProvenanceConfig controls the recurring retention-expiry audit run by each
// worker. Concurrent schedulers are safe because batches use SKIP LOCKED.
type ProvenanceConfig struct {
	RetentionEnabled    bool          `mapstructure:"retention_enabled"`
	RetentionInterval   time.Duration `mapstructure:"retention_interval"`
	RetentionBatchSize  int           `mapstructure:"retention_batch_size"`
	RetentionMaxBatches int           `mapstructure:"retention_max_batches"`
}

// AppConfig holds general application settings.
type AppConfig struct {
	Port                  int    `mapstructure:"port"`
	JWTSecret             string `mapstructure:"jwt_secret"`
	SettingsEncryptionKey string `mapstructure:"settings_encryption_key"`
	LogLevel              string `mapstructure:"log_level"`
	DevMode               bool   `mapstructure:"dev_mode"`
	ModelRoutingEnabled   bool   `mapstructure:"model_routing_enabled"`
}

// Load reads configuration from environment variables and an optional
// config.yaml file, returning a fully populated Config struct.
func Load() (*Config, error) {
	v := viper.New()

	// Set config file options.
	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.AddConfigPath(".")
	v.AddConfigPath("/etc/algoforge")

	// Environment variable binding with ALGOFORGE_ prefix.
	v.SetEnvPrefix("ALGOFORGE")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	// Set default values.
	setDefaults(v)

	// Attempt to read config file; it is not an error if it does not exist.
	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("reading config file: %w", err)
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshalling config: %w", err)
	}

	if err := Validate(&cfg); err != nil {
		return nil, fmt.Errorf("validating config: %w", err)
	}

	return &cfg, nil
}

// setDefaults registers default values for all configuration keys.
func setDefaults(v *viper.Viper) {
	// Database defaults.
	v.SetDefault("database.host", "localhost")
	v.SetDefault("database.port", 5432)
	v.SetDefault("database.user", "algoforge")
	v.SetDefault("database.pass", "")
	v.SetDefault("database.dbname", "algoforge")
	v.SetDefault("database.sslmode", "disable")

	// Redis defaults.
	v.SetDefault("redis.addr", "localhost:6379")
	v.SetDefault("redis.password", "")
	v.SetDefault("redis.db", 0)

	// MinIO defaults.
	v.SetDefault("minio.endpoint", "localhost:9000")
	v.SetDefault("minio.access_key", "")
	v.SetDefault("minio.secret_key", "")
	v.SetDefault("minio.bucket", "algoforge")
	v.SetDefault("minio.use_ssl", false)

	// Temporal defaults.
	v.SetDefault("temporal.host", "localhost:7233")
	v.SetDefault("temporal.namespace", "algoforge")
	v.SetDefault("temporal.task_queue", "algoforge-tasks")
	v.SetDefault("temporal.worker_build_id", "")
	v.SetDefault("temporal.use_build_id_for_versioning", false)
	v.SetDefault("temporal.worker_concurrency", 10)
	v.SetDefault("temporal.provider_effect_lease", 30*time.Minute)

	// Anthropic defaults.
	v.SetDefault("anthropic.api_key", "")
	v.SetDefault("anthropic.base_url", "") // empty = official Anthropic API; set for CC Switch / other proxies
	v.SetDefault("anthropic.model", "claude-opus-4-6")
	v.SetDefault("anthropic.provider", "")
	v.SetDefault("anthropic.reasoning_effort", "")

	// Embedding defaults.
	v.SetDefault("embedding.api_key", "")
	v.SetDefault("embedding.base_url", "")
	v.SetDefault("embedding.model", "")
	v.SetDefault("embedding.dimensions", 1536)
	v.SetDefault("embedding.timeout_sec", 30)
	v.SetDefault("embedding.enabled", false)
	v.SetDefault("embedding.expected_statement_model_version_id", "")

	// Sandbox defaults.
	v.SetDefault("sandbox.binary", "/usr/local/bin/algoforge-sandbox")
	v.SetDefault("sandbox.config_path", "/etc/algoforge/sandbox.yaml")
	v.SetDefault("sandbox.timeout", 30*time.Second)

	// Transactional outbox defaults. Delivery is explicitly disabled until a
	// fixed destination and credential are configured.
	v.SetDefault("outbox.enabled", false)
	v.SetDefault("outbox.endpoint", "")
	v.SetDefault("outbox.bearer_token", "")
	v.SetDefault("outbox.hmac_secret", "")
	v.SetDefault("outbox.poll_interval", 2*time.Second)
	v.SetDefault("outbox.batch_size", 25)
	v.SetDefault("outbox.lease_duration", 5*time.Minute)
	v.SetDefault("outbox.max_attempts", 10)
	v.SetDefault("outbox.connect_timeout", 3*time.Second)
	v.SetDefault("outbox.request_timeout", 10*time.Second)
	v.SetDefault("outbox.max_request_bytes", int64(1<<20))
	v.SetDefault("outbox.max_response_bytes", int64(64<<10))
	v.SetDefault("outbox.readiness_max_queued", int64(1000))
	v.SetDefault("outbox.readiness_max_age", 5*time.Minute)
	v.SetDefault("outbox.health_addr", ":8092")

	// Provenance retention is a worker-owned recurring safety job. It can be
	// explicitly disabled for one-shot maintenance workers only.
	v.SetDefault("provenance.retention_enabled", true)
	v.SetDefault("provenance.retention_interval", time.Hour)
	v.SetDefault("provenance.retention_batch_size", 1000)
	v.SetDefault("provenance.retention_max_batches", 100)

	// App defaults.
	v.SetDefault("app.port", 8080)
	v.SetDefault("app.jwt_secret", "")
	v.SetDefault("app.settings_encryption_key", "")
	v.SetDefault("app.log_level", "info")
	v.SetDefault("app.dev_mode", false)
	v.SetDefault("app.model_routing_enabled", false)

	// Explicit environment variable bindings so the .env names (without
	// the ALGOFORGE_ prefix) are picked up correctly by Viper.
	// Database
	v.BindEnv("database.host", "POSTGRES_HOST")
	v.BindEnv("database.port", "POSTGRES_PORT")
	v.BindEnv("database.user", "POSTGRES_USER")
	v.BindEnv("database.pass", "POSTGRES_PASSWORD")
	v.BindEnv("database.dbname", "POSTGRES_DB")

	// Redis
	v.BindEnv("redis.addr", "REDIS_ADDR")

	// MinIO
	v.BindEnv("minio.endpoint", "MINIO_ENDPOINT")
	v.BindEnv("minio.access_key", "MINIO_ACCESS_KEY")
	v.BindEnv("minio.secret_key", "MINIO_SECRET_KEY")
	v.BindEnv("minio.bucket", "MINIO_BUCKET")
	v.BindEnv("minio.use_ssl", "MINIO_USE_SSL")

	// Temporal
	v.BindEnv("temporal.host", "TEMPORAL_ADDRESS")
	v.BindEnv("temporal.namespace", "TEMPORAL_NAMESPACE")
	v.BindEnv("temporal.task_queue", "WORKER_TASK_QUEUE")
	v.BindEnv("temporal.worker_build_id", "WORKER_BUILD_ID")
	v.BindEnv("temporal.use_build_id_for_versioning", "WORKER_USE_BUILD_ID_VERSIONING")
	v.BindEnv("temporal.worker_concurrency", "WORKER_CONCURRENCY")
	v.BindEnv("temporal.provider_effect_lease", "PROVIDER_EFFECT_LEASE")

	// Anthropic
	v.BindEnv("anthropic.api_key", "ANTHROPIC_API_KEY")
	v.BindEnv("anthropic.base_url", "ANTHROPIC_BASE_URL")
	v.BindEnv("anthropic.model", "ANTHROPIC_MODEL")
	v.BindEnv("anthropic.provider", "ANTHROPIC_PROVIDER")
	v.BindEnv("anthropic.reasoning_effort", "ANTHROPIC_REASONING_EFFORT")

	// Embedding
	v.BindEnv("embedding.api_key", "ALGOFORGE_EMBEDDING_API_KEY")
	v.BindEnv("embedding.base_url", "ALGOFORGE_EMBEDDING_BASE_URL")
	v.BindEnv("embedding.model", "ALGOFORGE_EMBEDDING_MODEL")
	v.BindEnv("embedding.dimensions", "ALGOFORGE_EMBEDDING_DIMENSIONS")
	v.BindEnv("embedding.timeout_sec", "ALGOFORGE_EMBEDDING_TIMEOUT_SEC")
	v.BindEnv("embedding.enabled", "ALGOFORGE_EMBEDDING_ENABLED")
	v.BindEnv("embedding.expected_statement_model_version_id", "ALGOFORGE_EMBEDDING_EXPECTED_STATEMENT_MODEL_VERSION_ID")

	// Transactional outbox
	v.BindEnv("outbox.enabled", "OUTBOX_ENABLED")
	v.BindEnv("outbox.endpoint", "OUTBOX_ENDPOINT")
	v.BindEnv("outbox.bearer_token", "OUTBOX_BEARER_TOKEN")
	v.BindEnv("outbox.hmac_secret", "OUTBOX_HMAC_SECRET")
	v.BindEnv("outbox.poll_interval", "OUTBOX_POLL_INTERVAL")
	v.BindEnv("outbox.batch_size", "OUTBOX_BATCH_SIZE")
	v.BindEnv("outbox.lease_duration", "OUTBOX_LEASE_DURATION")
	v.BindEnv("outbox.max_attempts", "OUTBOX_MAX_ATTEMPTS")
	v.BindEnv("outbox.connect_timeout", "OUTBOX_CONNECT_TIMEOUT")
	v.BindEnv("outbox.request_timeout", "OUTBOX_REQUEST_TIMEOUT")
	v.BindEnv("outbox.max_request_bytes", "OUTBOX_MAX_REQUEST_BYTES")
	v.BindEnv("outbox.max_response_bytes", "OUTBOX_MAX_RESPONSE_BYTES")
	v.BindEnv("outbox.readiness_max_queued", "OUTBOX_READINESS_MAX_QUEUED")
	v.BindEnv("outbox.readiness_max_age", "OUTBOX_READINESS_MAX_AGE")
	v.BindEnv("outbox.health_addr", "OUTBOX_HEALTH_ADDR")

	// Provenance retention
	v.BindEnv("provenance.retention_enabled", "PROVENANCE_RETENTION_ENABLED")
	v.BindEnv("provenance.retention_interval", "PROVENANCE_RETENTION_INTERVAL")
	v.BindEnv("provenance.retention_batch_size", "PROVENANCE_RETENTION_BATCH_SIZE")
	v.BindEnv("provenance.retention_max_batches", "PROVENANCE_RETENTION_MAX_BATCHES")

	// App
	v.BindEnv("app.port", "API_PORT")
	v.BindEnv("app.jwt_secret", "JWT_SECRET")
	v.BindEnv("app.settings_encryption_key", "ALGOFORGE_SETTINGS_ENCRYPTION_KEY")
	v.BindEnv("app.log_level", "LOG_LEVEL")
	v.BindEnv("app.dev_mode", "APP_DEV_MODE")
	v.BindEnv("app.model_routing_enabled", "ALGOFORGE_QG14_MODEL_ROUTING_ENABLED")
}

// Validate checks that all required configuration values are present
// and returns an error describing any missing or invalid fields.
func Validate(cfg *Config) error {
	var errs []string

	if cfg.Database.Host == "" {
		errs = append(errs, "database.host is required")
	}
	if cfg.Database.User == "" {
		errs = append(errs, "database.user is required")
	}
	if cfg.Database.DBName == "" {
		errs = append(errs, "database.dbname is required")
	}
	if err := validateAnthropicBaseURL(cfg.Anthropic.BaseURL); err != nil {
		errs = append(errs, "anthropic.base_url "+err.Error())
	}
	if cfg.Anthropic.ProviderID() == "" {
		errs = append(errs, "anthropic.provider is required when anthropic.base_url is customized")
	}
	if effort := strings.ToLower(strings.TrimSpace(cfg.Anthropic.ReasoningEffort)); !isSupportedReasoningEffort(effort) {
		errs = append(errs, "anthropic.reasoning_effort must be one of none, minimal, low, medium, high, xhigh, or max")
	}
	if cfg.Embedding.Enabled && cfg.Embedding.ProviderID() == "" {
		errs = append(errs, "embedding.base_url must be an absolute URL when embedding is enabled")
	}
	if expected := strings.TrimSpace(cfg.Embedding.ExpectedStatementModelVersionID); expected != "" {
		if _, err := uuid.Parse(expected); err != nil {
			errs = append(errs, "embedding.expected_statement_model_version_id must be a UUID")
		}
	}
	if cfg.App.JWTSecret == "" && !cfg.App.DevMode {
		errs = append(errs, "app.jwt_secret is required")
	}
	if cfg.Temporal.Host == "" {
		errs = append(errs, "temporal.host is required")
	}
	if cfg.Temporal.UseBuildIDForVersioning && cfg.Temporal.WorkerBuildID == "" {
		errs = append(errs, "temporal.worker_build_id is required when worker versioning is enabled")
	}
	if cfg.Temporal.WorkerConcurrency <= 0 {
		errs = append(errs, "temporal.worker_concurrency must be positive")
	}
	if cfg.Temporal.ProviderEffectLease <= 35*time.Minute {
		errs = append(errs, "temporal.provider_effect_lease must exceed the 35 minute LLM activity timeout")
	}
	if cfg.MinIO.AccessKey == "" {
		errs = append(errs, "minio.access_key is required")
	}
	if cfg.MinIO.SecretKey == "" {
		errs = append(errs, "minio.secret_key is required")
	}
	if cfg.Outbox.PollInterval <= 0 {
		errs = append(errs, "outbox.poll_interval must be positive")
	}
	if cfg.Outbox.BatchSize <= 0 || cfg.Outbox.BatchSize > 1000 {
		errs = append(errs, "outbox.batch_size must be in [1,1000]")
	}
	if cfg.Outbox.LeaseDuration <= 0 {
		errs = append(errs, "outbox.lease_duration must be positive")
	}
	if cfg.Outbox.MaxAttempts <= 0 {
		errs = append(errs, "outbox.max_attempts must be positive")
	}
	if cfg.Outbox.ConnectTimeout <= 0 || cfg.Outbox.RequestTimeout <= 0 || cfg.Outbox.ConnectTimeout > cfg.Outbox.RequestTimeout {
		errs = append(errs, "outbox timeouts must be positive and connect_timeout must not exceed request_timeout")
	}
	if cfg.Outbox.BatchSize > 0 && cfg.Outbox.RequestTimeout > 0 &&
		cfg.Outbox.LeaseDuration <= time.Duration(cfg.Outbox.BatchSize)*cfg.Outbox.RequestTimeout {
		errs = append(errs, "outbox.lease_duration must exceed batch_size * request_timeout")
	}
	if cfg.Outbox.MaxRequestBytes <= 0 || cfg.Outbox.MaxResponseBytes <= 0 {
		errs = append(errs, "outbox request and response size limits must be positive")
	}
	if cfg.Outbox.ReadinessMaxQueued < 0 || cfg.Outbox.ReadinessMaxAge <= 0 {
		errs = append(errs, "outbox readiness thresholds must be non-negative with a positive max age")
	}
	if strings.TrimSpace(cfg.Outbox.HealthAddr) == "" {
		errs = append(errs, "outbox.health_addr is required")
	}
	if cfg.Outbox.Enabled {
		endpoint, err := url.ParseRequestURI(strings.TrimSpace(cfg.Outbox.Endpoint))
		if err != nil || endpoint.Host == "" || (endpoint.Scheme != "https" && !(cfg.App.DevMode && endpoint.Scheme == "http")) {
			errs = append(errs, "outbox.endpoint must be an absolute HTTPS URL (HTTP is allowed only in dev mode)")
		} else if endpoint.User != nil || endpoint.Fragment != "" {
			errs = append(errs, "outbox.endpoint must not contain user info or a fragment")
		}
		if !cfg.App.DevMode && strings.TrimSpace(cfg.Outbox.BearerToken) == "" {
			errs = append(errs, "outbox.bearer_token is required outside dev mode")
		}
		if len(cfg.Outbox.HMACSecret) < 32 {
			errs = append(errs, "outbox.hmac_secret must contain at least 32 bytes when outbox is enabled")
		}
	}
	if cfg.Provenance.RetentionInterval <= 0 {
		errs = append(errs, "provenance.retention_interval must be positive")
	}
	if cfg.Provenance.RetentionBatchSize <= 0 || cfg.Provenance.RetentionBatchSize > 10_000 {
		errs = append(errs, "provenance.retention_batch_size must be in [1,10000]")
	}
	if cfg.Provenance.RetentionMaxBatches <= 0 {
		errs = append(errs, "provenance.retention_max_batches must be positive")
	}

	if len(errs) > 0 {
		return fmt.Errorf("config validation failed:\n  - %s", strings.Join(errs, "\n  - "))
	}

	return nil
}

func isSupportedReasoningEffort(value string) bool {
	switch value {
	case "", "none", "minimal", "low", "medium", "high", "xhigh", "max":
		return true
	default:
		return false
	}
}
