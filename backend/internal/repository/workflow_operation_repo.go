package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	OperationStatusInProgress = "in_progress"
	OperationStatusFailed     = "failed"
	OperationStatusCompleted  = "completed"
)

type WorkflowOperation struct {
	Key           string
	Type          string
	PayloadSHA256 string
	Status        string
	ResultJSON    json.RawMessage
	AttemptCount  int
}

type WorkflowOperationRepository struct {
	db *pgxpool.Pool
}

func NewWorkflowOperationRepository(db *pgxpool.Pool) *WorkflowOperationRepository {
	return &WorkflowOperationRepository{db: db}
}

// Begin creates or reopens an operation while rejecting reuse of a stable key
// for a different activity type or payload.
func (r *WorkflowOperationRepository) Begin(ctx context.Context, key, operationType, payloadSHA256 string) (WorkflowOperation, error) {
	if err := validateOperationIdentity(key, operationType, payloadSHA256); err != nil {
		return WorkflowOperation{}, err
	}
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return WorkflowOperation{}, fmt.Errorf("begin workflow operation: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback after commit is a no-op

	tag, err := tx.Exec(ctx, `
		INSERT INTO workflow_operations (operation_key, operation_type, payload_sha256)
		VALUES ($1,$2,$3)
		ON CONFLICT (operation_key) DO NOTHING`, key, operationType, payloadSHA256)
	if err != nil {
		return WorkflowOperation{}, fmt.Errorf("creating workflow operation %q: %w", key, err)
	}

	operation, err := getWorkflowOperationTx(ctx, tx, key, true)
	if err != nil {
		return WorkflowOperation{}, err
	}
	if operation.Type != operationType || operation.PayloadSHA256 != payloadSHA256 {
		return WorkflowOperation{}, fmt.Errorf("operation key %q was already used with a different type or payload", key)
	}
	if tag.RowsAffected() == 0 && operation.Status != OperationStatusCompleted {
		if _, err := tx.Exec(ctx, `
			UPDATE workflow_operations
			SET status='in_progress', attempt_count=attempt_count+1, last_error=NULL
			WHERE operation_key=$1`, key); err != nil {
			return WorkflowOperation{}, fmt.Errorf("reopening workflow operation %q: %w", key, err)
		}
		operation.Status = OperationStatusInProgress
		operation.AttemptCount++
	}
	if err := tx.Commit(ctx); err != nil {
		return WorkflowOperation{}, fmt.Errorf("commit workflow operation begin %q: %w", key, err)
	}
	return operation, nil
}

func (r *WorkflowOperationRepository) Complete(ctx context.Context, key, payloadSHA256 string, result json.RawMessage) error {
	if len(result) == 0 || !json.Valid(result) {
		return fmt.Errorf("workflow operation result must be valid non-empty JSON")
	}
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin workflow operation completion: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback after commit is a no-op

	operation, err := getWorkflowOperationTx(ctx, tx, key, true)
	if err != nil {
		return err
	}
	if operation.PayloadSHA256 != payloadSHA256 {
		return fmt.Errorf("operation key %q payload hash changed before completion", key)
	}
	if operation.Status == OperationStatusCompleted {
		if !jsonEqual(operation.ResultJSON, result) {
			return fmt.Errorf("operation key %q completed with a different result", key)
		}
		return tx.Commit(ctx)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE workflow_operations
		SET status='completed', result_json=$2, last_error=NULL, completed_at=NOW()
		WHERE operation_key=$1`, key, result); err != nil {
		return fmt.Errorf("completing workflow operation %q: %w", key, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit workflow operation completion %q: %w", key, err)
	}
	return nil
}

func (r *WorkflowOperationRepository) Fail(ctx context.Context, key, payloadSHA256, failure string) error {
	tag, err := r.db.Exec(ctx, `
		UPDATE workflow_operations
		SET status='failed', last_error=$3, completed_at=NULL
		WHERE operation_key=$1 AND payload_sha256=$2 AND status <> 'completed'`,
		key, payloadSHA256, truncateDatabaseText(failure, 16*1024))
	if err != nil {
		return fmt.Errorf("marking workflow operation %q failed: %w", key, err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("workflow operation %q was not mutable or its payload hash changed", key)
	}
	return nil
}

func (r *WorkflowOperationRepository) StepCompleted(ctx context.Context, operationKey, stepKey, effectSHA256 string) (bool, error) {
	var storedHash string
	err := r.db.QueryRow(ctx, `
		SELECT effect_sha256 FROM workflow_operation_steps
		WHERE operation_key=$1 AND step_key=$2`, operationKey, stepKey).Scan(&storedHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("checking workflow operation step %q: %w", stepKey, err)
	}
	if storedHash != effectSHA256 {
		return false, fmt.Errorf("workflow operation step %q was already completed with different content", stepKey)
	}
	return true, nil
}

func (r *WorkflowOperationRepository) CompleteStep(ctx context.Context, operationKey, stepKey, effectSHA256 string) error {
	if strings.TrimSpace(stepKey) == "" || len(stepKey) > 768 {
		return fmt.Errorf("workflow operation step key is empty or too long")
	}
	if !isLowerHexSHA256(effectSHA256) {
		return fmt.Errorf("invalid workflow operation step effect sha256")
	}
	if _, err := r.db.Exec(ctx, `
		INSERT INTO workflow_operation_steps (operation_key, step_key, effect_sha256)
		VALUES ($1,$2,$3)
		ON CONFLICT (operation_key, step_key) DO NOTHING`, operationKey, stepKey, effectSHA256); err != nil {
		return fmt.Errorf("completing workflow operation step %q: %w", stepKey, err)
	}
	completed, err := r.StepCompleted(ctx, operationKey, stepKey, effectSHA256)
	if err != nil {
		return err
	}
	if !completed {
		return fmt.Errorf("workflow operation step %q was not persisted", stepKey)
	}
	return nil
}

func getWorkflowOperationTx(ctx context.Context, tx pgx.Tx, key string, forUpdate bool) (WorkflowOperation, error) {
	query := `SELECT operation_key, operation_type, payload_sha256, status,
		COALESCE(result_json, 'null'::jsonb), attempt_count
		FROM workflow_operations WHERE operation_key=$1`
	if forUpdate {
		query += ` FOR UPDATE`
	}
	var operation WorkflowOperation
	if err := tx.QueryRow(ctx, query, key).Scan(
		&operation.Key, &operation.Type, &operation.PayloadSHA256,
		&operation.Status, &operation.ResultJSON, &operation.AttemptCount,
	); err != nil {
		return WorkflowOperation{}, fmt.Errorf("reading workflow operation %q: %w", key, err)
	}
	return operation, nil
}

func validateOperationIdentity(key, operationType, payloadSHA256 string) error {
	if strings.TrimSpace(key) == "" || len(key) > 512 {
		return fmt.Errorf("workflow operation key is empty or too long")
	}
	if strings.TrimSpace(operationType) == "" || len(operationType) > 100 {
		return fmt.Errorf("workflow operation type is empty or too long")
	}
	if !isLowerHexSHA256(payloadSHA256) {
		return fmt.Errorf("invalid workflow operation payload sha256")
	}
	return nil
}

func isLowerHexSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, ch := range value {
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
			return false
		}
	}
	return true
}

func jsonEqual(left, right json.RawMessage) bool {
	var leftValue, rightValue interface{}
	if json.Unmarshal(left, &leftValue) != nil || json.Unmarshal(right, &rightValue) != nil {
		return bytes.Equal(left, right)
	}
	return reflect.DeepEqual(leftValue, rightValue)
}

func truncateDatabaseText(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}

type OutboxEvent struct {
	EventID       uuid.UUID
	OperationKey  string
	EventType     string
	AggregateType string
	AggregateID   string
	PayloadJSON   json.RawMessage
	AttemptCount  int
	LockToken     uuid.UUID
}

type OutboxRepository struct {
	db *pgxpool.Pool
}

type OutboxBacklogStats struct {
	Pending              int64         `json:"pending"`
	Retry                int64         `json:"retry"`
	Processing           int64         `json:"processing"`
	Dead                 int64         `json:"dead"`
	OldestUndeliveredAge time.Duration `json:"oldest_undelivered_age"`
}

func (s OutboxBacklogStats) Queued() int64 {
	return s.Pending + s.Retry + s.Processing
}

func NewOutboxRepository(db *pgxpool.Pool) *OutboxRepository {
	return &OutboxRepository{db: db}
}

func (r *OutboxRepository) BacklogStats(ctx context.Context) (OutboxBacklogStats, error) {
	var stats OutboxBacklogStats
	var oldestAgeMS int64
	err := r.db.QueryRow(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE status='pending'),
			COUNT(*) FILTER (WHERE status='retry'),
			COUNT(*) FILTER (WHERE status='processing'),
			COUNT(*) FILTER (WHERE status='dead'),
			COALESCE(EXTRACT(EPOCH FROM (
				NOW() - MIN(created_at) FILTER (
					WHERE status IN ('pending','retry','processing','dead')
				)
			)) * 1000, 0)::BIGINT
		FROM workflow_outbox`).Scan(
		&stats.Pending, &stats.Retry, &stats.Processing, &stats.Dead, &oldestAgeMS,
	)
	if err != nil {
		return OutboxBacklogStats{}, fmt.Errorf("reading workflow outbox backlog: %w", err)
	}
	if oldestAgeMS > 0 {
		stats.OldestUndeliveredAge = time.Duration(oldestAgeMS) * time.Millisecond
	}
	return stats, nil
}

// ClaimBatch leases ready messages with SKIP LOCKED so multiple dispatchers can
// safely drain the same outbox.
func (r *OutboxRepository) ClaimBatch(ctx context.Context, limit int, lease time.Duration) ([]OutboxEvent, error) {
	if limit <= 0 || limit > 1000 {
		return nil, fmt.Errorf("outbox claim limit must be in [1,1000]")
	}
	if lease <= 0 {
		return nil, fmt.Errorf("outbox lease must be positive")
	}
	lockToken := uuid.New()
	rows, err := r.db.Query(ctx, `
		WITH ready AS (
			SELECT event_id FROM workflow_outbox
			WHERE (status IN ('pending','retry') AND available_at <= NOW())
			   OR (status='processing' AND locked_until <= NOW())
			ORDER BY created_at, event_id
			FOR UPDATE SKIP LOCKED
			LIMIT $1
		)
		UPDATE workflow_outbox event
		SET status='processing', lock_token=$2,
		    locked_until=NOW() + ($3 * INTERVAL '1 millisecond'),
		    attempt_count=attempt_count+1
		FROM ready WHERE event.event_id=ready.event_id
		RETURNING event.event_id, event.operation_key, event.event_type,
		          event.aggregate_type, event.aggregate_id, event.payload_json,
		          event.attempt_count, event.lock_token`, limit, lockToken, lease.Milliseconds())
	if err != nil {
		return nil, fmt.Errorf("claiming workflow outbox: %w", err)
	}
	defer rows.Close()
	var events []OutboxEvent
	for rows.Next() {
		var event OutboxEvent
		if err := rows.Scan(
			&event.EventID, &event.OperationKey, &event.EventType,
			&event.AggregateType, &event.AggregateID, &event.PayloadJSON,
			&event.AttemptCount, &event.LockToken,
		); err != nil {
			return nil, fmt.Errorf("scanning claimed outbox event: %w", err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating claimed outbox events: %w", err)
	}
	return events, nil
}

func (r *OutboxRepository) MarkDelivered(ctx context.Context, eventID, lockToken uuid.UUID) error {
	tag, err := r.db.Exec(ctx, `
		UPDATE workflow_outbox
		SET status='delivered', delivered_at=NOW(), lock_token=NULL,
		    locked_until=NULL, last_error=NULL
		WHERE event_id=$1 AND status='processing' AND lock_token=$2`, eventID, lockToken)
	if err != nil {
		return fmt.Errorf("marking outbox event %s delivered: %w", eventID, err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("outbox event %s lease was lost before delivery acknowledgement", eventID)
	}
	return nil
}

func (r *OutboxRepository) MarkFailed(ctx context.Context, eventID, lockToken uuid.UUID, failure string, retryAt time.Time, maxAttempts int) error {
	if maxAttempts <= 0 {
		return fmt.Errorf("outbox max attempts must be positive")
	}
	tag, err := r.db.Exec(ctx, `
		UPDATE workflow_outbox
		SET status=CASE WHEN attempt_count >= $5 THEN 'dead' ELSE 'retry' END,
		    available_at=$4, lock_token=NULL, locked_until=NULL, last_error=$3
		WHERE event_id=$1 AND status='processing' AND lock_token=$2`,
		eventID, lockToken, truncateDatabaseText(failure, 16*1024), retryAt, maxAttempts)
	if err != nil {
		return fmt.Errorf("marking outbox event %s failed: %w", eventID, err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("outbox event %s lease was lost before failure acknowledgement", eventID)
	}
	return nil
}
