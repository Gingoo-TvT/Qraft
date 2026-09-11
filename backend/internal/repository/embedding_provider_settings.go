package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrEmbeddingProviderSettingNotFound = errors.New("embedding provider setting not found")

const EmbeddingProviderSettingRuntime = "runtime"

// EmbeddingProviderSettingRecord is the encrypted, durable runtime credential
// for the OpenAI-compatible embedding endpoint. The immutable vector-space
// identity remains governed by embedding_model_versions.
type EmbeddingProviderSettingRecord struct {
	SettingKey      string
	BaseURL         string
	Model           string
	Dimensions      int
	TimeoutSec      int
	EncryptedAPIKey string
	UpdatedBy       string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type EmbeddingProviderSettingUpsert struct {
	BaseURL         string
	Model           string
	Dimensions      int
	TimeoutSec      int
	EncryptedAPIKey string
	UpdatedBy       string
}

type EmbeddingProviderSettingsRepository struct {
	db *pgxpool.Pool
}

func NewEmbeddingProviderSettingsRepository(db *pgxpool.Pool) *EmbeddingProviderSettingsRepository {
	return &EmbeddingProviderSettingsRepository{db: db}
}

func (r *EmbeddingProviderSettingsRepository) GetEmbeddingProviderSetting(
	ctx context.Context,
) (EmbeddingProviderSettingRecord, error) {
	if r == nil || r.db == nil {
		return EmbeddingProviderSettingRecord{}, fmt.Errorf("embedding provider settings database is required")
	}
	var record EmbeddingProviderSettingRecord
	err := r.db.QueryRow(ctx, `
		SELECT setting_key, base_url, model, dimensions, timeout_sec,
		       encrypted_api_key, updated_by, created_at, updated_at
		FROM embedding_provider_settings
		WHERE setting_key = $1`, EmbeddingProviderSettingRuntime).Scan(
		&record.SettingKey,
		&record.BaseURL,
		&record.Model,
		&record.Dimensions,
		&record.TimeoutSec,
		&record.EncryptedAPIKey,
		&record.UpdatedBy,
		&record.CreatedAt,
		&record.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return EmbeddingProviderSettingRecord{}, ErrEmbeddingProviderSettingNotFound
	}
	if err != nil {
		return EmbeddingProviderSettingRecord{}, fmt.Errorf("read embedding provider setting: %w", err)
	}
	return record, nil
}

func (r *EmbeddingProviderSettingsRepository) UpsertEmbeddingProviderSetting(
	ctx context.Context,
	input EmbeddingProviderSettingUpsert,
) (EmbeddingProviderSettingRecord, error) {
	if r == nil || r.db == nil {
		return EmbeddingProviderSettingRecord{}, fmt.Errorf("embedding provider settings database is required")
	}
	var record EmbeddingProviderSettingRecord
	err := r.db.QueryRow(ctx, `
		INSERT INTO embedding_provider_settings (
			setting_key, base_url, model, dimensions, timeout_sec,
			encrypted_api_key, updated_by
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (setting_key) DO UPDATE SET
			base_url = EXCLUDED.base_url,
			model = EXCLUDED.model,
			dimensions = EXCLUDED.dimensions,
			timeout_sec = EXCLUDED.timeout_sec,
			encrypted_api_key = EXCLUDED.encrypted_api_key,
			updated_by = EXCLUDED.updated_by,
			updated_at = NOW()
		RETURNING setting_key, base_url, model, dimensions, timeout_sec,
		          encrypted_api_key, updated_by, created_at, updated_at`,
		EmbeddingProviderSettingRuntime,
		input.BaseURL,
		input.Model,
		input.Dimensions,
		input.TimeoutSec,
		input.EncryptedAPIKey,
		input.UpdatedBy,
	).Scan(
		&record.SettingKey,
		&record.BaseURL,
		&record.Model,
		&record.Dimensions,
		&record.TimeoutSec,
		&record.EncryptedAPIKey,
		&record.UpdatedBy,
		&record.CreatedAt,
		&record.UpdatedAt,
	)
	if err != nil {
		return EmbeddingProviderSettingRecord{}, fmt.Errorf("upsert embedding provider setting: %w", err)
	}
	return record, nil
}
