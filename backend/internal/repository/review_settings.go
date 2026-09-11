package repository

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const reviewSettingsKey = "global"

type ReviewSettingsRecord struct {
	AutoApprovePublicRelease bool
	UpdatedBy                string
	CreatedAt                time.Time
	UpdatedAt                time.Time
}

type ReviewSettingsRepository struct {
	db *pgxpool.Pool
}

func NewReviewSettingsRepository(db *pgxpool.Pool) *ReviewSettingsRepository {
	return &ReviewSettingsRepository{db: db}
}

func (r *ReviewSettingsRepository) GetReviewSettings(
	ctx context.Context,
) (ReviewSettingsRecord, error) {
	if r == nil || r.db == nil {
		return ReviewSettingsRecord{}, fmt.Errorf("review settings database is required")
	}

	var record ReviewSettingsRecord
	err := r.db.QueryRow(ctx, `
		SELECT auto_approve_public_release, updated_by, created_at, updated_at
		FROM review_settings
		WHERE setting_key = $1`, reviewSettingsKey).Scan(
		&record.AutoApprovePublicRelease,
		&record.UpdatedBy,
		&record.CreatedAt,
		&record.UpdatedAt,
	)
	if err != nil {
		return ReviewSettingsRecord{}, fmt.Errorf("read review settings: %w", err)
	}
	return record, nil
}

func (r *ReviewSettingsRepository) UpdateReviewSettings(
	ctx context.Context,
	autoApprovePublicRelease bool,
	actor string,
) (ReviewSettingsRecord, error) {
	if r == nil || r.db == nil {
		return ReviewSettingsRecord{}, fmt.Errorf("review settings database is required")
	}
	actor = strings.TrimSpace(actor)
	if actor == "" {
		actor = "api"
	}

	var record ReviewSettingsRecord
	err := r.db.QueryRow(ctx, `
		INSERT INTO review_settings (
			setting_key, auto_approve_public_release, updated_by
		)
		VALUES ($1, $2, $3)
		ON CONFLICT (setting_key) DO UPDATE SET
			auto_approve_public_release = EXCLUDED.auto_approve_public_release,
			updated_by = EXCLUDED.updated_by,
			updated_at = NOW()
		RETURNING auto_approve_public_release, updated_by, created_at, updated_at`,
		reviewSettingsKey,
		autoApprovePublicRelease,
		actor,
	).Scan(
		&record.AutoApprovePublicRelease,
		&record.UpdatedBy,
		&record.CreatedAt,
		&record.UpdatedAt,
	)
	if err != nil {
		return ReviewSettingsRecord{}, fmt.Errorf("update review settings: %w", err)
	}
	return record, nil
}
