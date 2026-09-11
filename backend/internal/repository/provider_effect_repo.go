package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ProviderEffectState string

const (
	ProviderEffectAcquired  ProviderEffectState = "acquired"
	ProviderEffectBusy      ProviderEffectState = "busy"
	ProviderEffectCompleted ProviderEffectState = "completed"
)

type ProviderEffectClaim struct {
	State       ProviderEffectState
	LeaseToken  uuid.UUID
	LeasedUntil time.Time
	ResultJSON  json.RawMessage
}

// ProviderEffectRepository serializes billable provider calls by a stable
// logical effect key and retains completed responses for activity redelivery.
type ProviderEffectRepository struct {
	db *pgxpool.Pool
}

func NewProviderEffectRepository(db *pgxpool.Pool) *ProviderEffectRepository {
	return &ProviderEffectRepository{db: db}
}

func (r *ProviderEffectRepository) Acquire(
	ctx context.Context,
	effectKey, effectType, requestSHA256 string,
	lease time.Duration,
) (ProviderEffectClaim, error) {
	if err := validateProviderEffectIdentity(effectKey, effectType, requestSHA256); err != nil {
		return ProviderEffectClaim{}, err
	}
	if lease <= 0 {
		return ProviderEffectClaim{}, fmt.Errorf("provider effect lease must be positive")
	}

	tx, err := r.db.Begin(ctx)
	if err != nil {
		return ProviderEffectClaim{}, fmt.Errorf("begin provider effect acquisition: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback after commit is a no-op

	leaseToken := uuid.New()
	tag, err := tx.Exec(ctx, `
		INSERT INTO provider_effects (
			effect_key, effect_type, request_sha256, lease_token, leased_until
		) VALUES ($1,$2,$3,$4,clock_timestamp() + ($5 * INTERVAL '1 millisecond'))
		ON CONFLICT (effect_key) DO NOTHING`,
		effectKey, effectType, requestSHA256, leaseToken, lease.Milliseconds())
	if err != nil {
		return ProviderEffectClaim{}, fmt.Errorf("create provider effect %q: %w", effectKey, err)
	}

	var (
		storedType    string
		storedRequest string
		status        string
		storedToken   *uuid.UUID
		leasedUntil   *time.Time
		result        json.RawMessage
		leaseExpired  bool
		resultIntact  bool
	)
	if err := tx.QueryRow(ctx, `
		SELECT effect_type, request_sha256, status, lease_token, leased_until,
		       COALESCE(result_json, 'null'::jsonb),
		       COALESCE(leased_until <= clock_timestamp(), FALSE),
		       COALESCE(
		           result_sha256 = encode(
		               digest(convert_to(result_json::text, 'UTF8'), 'sha256'),
		               'hex'
		           ),
		           FALSE
		       )
		FROM provider_effects WHERE effect_key=$1 FOR UPDATE`, effectKey).Scan(
		&storedType, &storedRequest, &status, &storedToken, &leasedUntil,
		&result, &leaseExpired, &resultIntact,
	); err != nil {
		return ProviderEffectClaim{}, fmt.Errorf("read provider effect %q: %w", effectKey, err)
	}
	if storedType != effectType || storedRequest != requestSHA256 {
		return ProviderEffectClaim{}, fmt.Errorf("provider effect key %q was already used with a different type or request", effectKey)
	}
	if status == "completed" {
		if !resultIntact {
			return ProviderEffectClaim{}, fmt.Errorf("provider effect %q cached result failed integrity validation", effectKey)
		}
		if err := tx.Commit(ctx); err != nil {
			return ProviderEffectClaim{}, fmt.Errorf("commit cached provider effect %q read: %w", effectKey, err)
		}
		return ProviderEffectClaim{State: ProviderEffectCompleted, ResultJSON: result}, nil
	}

	if tag.RowsAffected() == 0 && !leaseExpired {
		claim := ProviderEffectClaim{State: ProviderEffectBusy}
		if storedToken != nil {
			claim.LeaseToken = *storedToken
		}
		if leasedUntil != nil {
			claim.LeasedUntil = *leasedUntil
		}
		if err := tx.Commit(ctx); err != nil {
			return ProviderEffectClaim{}, fmt.Errorf("commit busy provider effect %q read: %w", effectKey, err)
		}
		return claim, nil
	}

	if tag.RowsAffected() == 0 {
		if _, err := tx.Exec(ctx, `
			UPDATE provider_effects
			SET lease_token=$2,
			    leased_until=clock_timestamp() + ($3 * INTERVAL '1 millisecond'),
			    attempt_count=attempt_count+1,
			    last_error=NULL
			WHERE effect_key=$1`, effectKey, leaseToken, lease.Milliseconds()); err != nil {
			return ProviderEffectClaim{}, fmt.Errorf("reacquire provider effect %q: %w", effectKey, err)
		}
	}
	if err := tx.QueryRow(ctx, `SELECT leased_until FROM provider_effects WHERE effect_key=$1`, effectKey).Scan(&leasedUntil); err != nil {
		return ProviderEffectClaim{}, fmt.Errorf("read provider effect %q lease: %w", effectKey, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return ProviderEffectClaim{}, fmt.Errorf("commit provider effect %q acquisition: %w", effectKey, err)
	}
	claim := ProviderEffectClaim{State: ProviderEffectAcquired, LeaseToken: leaseToken}
	if leasedUntil != nil {
		claim.LeasedUntil = *leasedUntil
	}
	return claim, nil
}

func (r *ProviderEffectRepository) Complete(
	ctx context.Context,
	effectKey, effectType, requestSHA256 string,
	leaseToken uuid.UUID,
	result json.RawMessage,
) error {
	if err := validateProviderEffectIdentity(effectKey, effectType, requestSHA256); err != nil {
		return err
	}
	if leaseToken == uuid.Nil {
		return fmt.Errorf("provider effect lease token is required")
	}
	if len(result) == 0 || !json.Valid(result) {
		return fmt.Errorf("provider effect result must be valid non-empty JSON")
	}
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin provider effect completion: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback after commit is a no-op

	var storedType, storedRequest, status string
	var storedToken *uuid.UUID
	var storedResultSHA *string
	var resultSHA256 string
	if err := tx.QueryRow(ctx, `
		SELECT effect_type, request_sha256, status, lease_token, result_sha256,
		       encode(
		           digest(convert_to($2::jsonb::text, 'UTF8'), 'sha256'),
		           'hex'
		       )
		FROM provider_effects WHERE effect_key=$1 FOR UPDATE`, effectKey, result).Scan(
		&storedType, &storedRequest, &status, &storedToken, &storedResultSHA,
		&resultSHA256,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("provider effect %q does not exist", effectKey)
		}
		return fmt.Errorf("read provider effect %q for completion: %w", effectKey, err)
	}
	if storedType != effectType || storedRequest != requestSHA256 {
		return fmt.Errorf("provider effect key %q changed type or request before completion", effectKey)
	}
	if status == "completed" {
		if storedResultSHA == nil || *storedResultSHA != resultSHA256 {
			return fmt.Errorf("provider effect %q completed with a different result", effectKey)
		}
		return tx.Commit(ctx)
	}
	if storedToken == nil || *storedToken != leaseToken {
		return fmt.Errorf("provider effect %q lease was lost before completion", effectKey)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE provider_effects
		SET status='completed', result_json=$2, result_sha256=$3,
		    lease_token=NULL, leased_until=NULL, last_error=NULL,
		    completed_at=clock_timestamp()
		WHERE effect_key=$1`, effectKey, result, resultSHA256); err != nil {
		return fmt.Errorf("complete provider effect %q: %w", effectKey, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit provider effect %q completion: %w", effectKey, err)
	}
	return nil
}

func (r *ProviderEffectRepository) Fail(
	ctx context.Context,
	effectKey, effectType, requestSHA256 string,
	leaseToken uuid.UUID,
	failure string,
) error {
	if err := validateProviderEffectIdentity(effectKey, effectType, requestSHA256); err != nil {
		return err
	}
	if leaseToken == uuid.Nil {
		return fmt.Errorf("provider effect lease token is required")
	}
	tag, err := r.db.Exec(ctx, `
		UPDATE provider_effects
		SET leased_until=clock_timestamp(), last_error=$5
		WHERE effect_key=$1 AND effect_type=$2 AND request_sha256=$3
		  AND status='in_progress' AND lease_token=$4`,
		effectKey, effectType, requestSHA256, leaseToken, truncateDatabaseText(failure, 16*1024))
	if err != nil {
		return fmt.Errorf("release failed provider effect %q: %w", effectKey, err)
	}
	if tag.RowsAffected() == 1 {
		return nil
	}

	var storedType, storedRequest, status string
	if err := r.db.QueryRow(ctx, `
		SELECT effect_type, request_sha256, status
		FROM provider_effects WHERE effect_key=$1`, effectKey).Scan(&storedType, &storedRequest, &status); err != nil {
		return fmt.Errorf("verify failed provider effect %q release: %w", effectKey, err)
	}
	if storedType != effectType || storedRequest != requestSHA256 {
		return fmt.Errorf("provider effect key %q changed type or request before failure release", effectKey)
	}
	if status == "completed" {
		return nil
	}
	return fmt.Errorf("provider effect %q lease was lost before failure release", effectKey)
}

func validateProviderEffectIdentity(effectKey, effectType, requestSHA256 string) error {
	if strings.TrimSpace(effectKey) == "" || len(effectKey) > 160 {
		return fmt.Errorf("provider effect key is empty or too long")
	}
	if strings.TrimSpace(effectType) == "" || len(effectType) > 80 {
		return fmt.Errorf("provider effect type is empty or too long")
	}
	if !isLowerHexSHA256(requestSHA256) {
		return fmt.Errorf("invalid provider effect request sha256")
	}
	return nil
}
