package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrLLMProviderSettingNotFound = errors.New("LLM provider setting not found")

const (
	LLMProviderPurposeStatement    = "statement"
	LLMProviderPurposeVerification = "verification"
	LLMProviderPurposeReview       = "review"
	LLMAPIKeySourceEnvironment     = "environment"
	LLMAPIKeySourceStored          = "stored"
)

type LLMProviderSettingRecord struct {
	Purpose         string
	Model           string
	BaseURL         string
	Provider        string
	Protocol        string
	ReasoningEffort string
	APIKeySource    string
	EncryptedAPIKey *string
	UpdatedBy       string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type LLMProviderSettingUpsert struct {
	Purpose         string
	Model           string
	BaseURL         string
	Provider        string
	Protocol        string
	ReasoningEffort string
	APIKeySource    string
	EncryptedAPIKey *string
	UpdatedBy       string
}

type LLMProviderSettingsRepository struct {
	db *pgxpool.Pool
}

func NewLLMProviderSettingsRepository(db *pgxpool.Pool) *LLMProviderSettingsRepository {
	return &LLMProviderSettingsRepository{db: db}
}

func (r *LLMProviderSettingsRepository) GetLLMProviderSetting(
	ctx context.Context,
	purpose string,
) (LLMProviderSettingRecord, error) {
	if r == nil || r.db == nil {
		return LLMProviderSettingRecord{}, fmt.Errorf("LLM provider settings database is required")
	}
	var record LLMProviderSettingRecord
	err := r.db.QueryRow(ctx, `
		SELECT purpose, model, base_url, provider, protocol, reasoning_effort, api_key_source,
		       encrypted_api_key, updated_by, created_at, updated_at
		FROM llm_provider_settings
		WHERE purpose = $1`, purpose).Scan(
		&record.Purpose,
		&record.Model,
		&record.BaseURL,
		&record.Provider,
		&record.Protocol,
		&record.ReasoningEffort,
		&record.APIKeySource,
		&record.EncryptedAPIKey,
		&record.UpdatedBy,
		&record.CreatedAt,
		&record.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return LLMProviderSettingRecord{}, ErrLLMProviderSettingNotFound
	}
	if err != nil {
		return LLMProviderSettingRecord{}, fmt.Errorf("read LLM provider setting %q: %w", purpose, err)
	}
	return record, nil
}

func (r *LLMProviderSettingsRepository) UpsertLLMProviderSetting(
	ctx context.Context,
	input LLMProviderSettingUpsert,
) (LLMProviderSettingRecord, error) {
	if r == nil || r.db == nil {
		return LLMProviderSettingRecord{}, fmt.Errorf("LLM provider settings database is required")
	}
	var record LLMProviderSettingRecord
	err := r.db.QueryRow(ctx, `
		INSERT INTO llm_provider_settings (
			purpose, model, base_url, provider, protocol, reasoning_effort,
			api_key_source, encrypted_api_key, updated_by
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT (purpose) DO UPDATE SET
			model = EXCLUDED.model,
			base_url = EXCLUDED.base_url,
			provider = EXCLUDED.provider,
			protocol = EXCLUDED.protocol,
			reasoning_effort = EXCLUDED.reasoning_effort,
			api_key_source = EXCLUDED.api_key_source,
			encrypted_api_key = EXCLUDED.encrypted_api_key,
			updated_by = EXCLUDED.updated_by,
			updated_at = NOW()
		RETURNING purpose, model, base_url, provider, protocol, reasoning_effort, api_key_source,
		          encrypted_api_key, updated_by, created_at, updated_at`,
		input.Purpose,
		input.Model,
		input.BaseURL,
		input.Provider,
		input.Protocol,
		input.ReasoningEffort,
		input.APIKeySource,
		input.EncryptedAPIKey,
		input.UpdatedBy,
	).Scan(
		&record.Purpose,
		&record.Model,
		&record.BaseURL,
		&record.Provider,
		&record.Protocol,
		&record.ReasoningEffort,
		&record.APIKeySource,
		&record.EncryptedAPIKey,
		&record.UpdatedBy,
		&record.CreatedAt,
		&record.UpdatedAt,
	)
	if err != nil {
		return LLMProviderSettingRecord{}, fmt.Errorf("upsert LLM provider setting %q: %w", input.Purpose, err)
	}
	return record, nil
}

// DeleteLLMProviderSetting removes a saved role override. The operation is
// intentionally idempotent so a reset request remains safe across retries.
func (r *LLMProviderSettingsRepository) DeleteLLMProviderSetting(
	ctx context.Context,
	purpose string,
) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("LLM provider settings database is required")
	}
	if _, err := r.db.Exec(ctx, `DELETE FROM llm_provider_settings WHERE purpose = $1`, purpose); err != nil {
		return fmt.Errorf("delete LLM provider setting %q: %w", purpose, err)
	}
	return nil
}
