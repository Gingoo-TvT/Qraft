package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	EmbeddingSwitchDecisionGo   = "go"
	EmbeddingSwitchDecisionNoGo = "no_go"

	EmbeddingSwitchOperationCutover  = "cutover"
	EmbeddingSwitchOperationRollback = "rollback"
)

// ActivePointerSwitchOptions describes one QE::T04 active pointer transition.
// The old and new vector spaces are retained; switching only changes the DB
// active pointer after the target model has complete current-content coverage.
type ActivePointerSwitchOptions struct {
	Kind                       string
	Operation                  string
	ToModelVersionID           uuid.UUID
	ExpectedFromModelVersionID *uuid.UUID
	Actor                      string
	Reason                     string
	DatasetReportSHA256        string
	ShadowReadCount            int64
	ShadowErrorCount           int64
	DryRun                     bool
}

type ActivePointerSwitchReport struct {
	GeneratedAt         time.Time                    `json:"generated_at"`
	Decision            string                       `json:"decision"`
	Operation           string                       `json:"operation"`
	DryRun              bool                         `json:"dry_run"`
	Committed           bool                         `json:"committed"`
	EmbeddingKind       string                       `json:"embedding_kind"`
	OldModelVersionID   uuid.UUID                    `json:"old_model_version_id"`
	NewModelVersionID   uuid.UUID                    `json:"new_model_version_id"`
	Actor               string                       `json:"actor"`
	Reason              string                       `json:"reason"`
	DatasetReportSHA256 string                       `json:"dataset_report_sha256"`
	AuditEventID        int64                        `json:"audit_event_id,omitempty"`
	Preflight           ActivePointerSwitchPreflight `json:"preflight"`
	BlockingIssues      []string                     `json:"blocking_issues,omitempty"`
}

type ActivePointerSwitchPreflight struct {
	EligibleProblemCount              int64   `json:"eligible_problem_count"`
	TargetCurrentVectorCount          int64   `json:"target_current_vector_count"`
	TargetMissingCurrentVectorCount   int64   `json:"target_missing_current_vector_count"`
	TargetDuplicateCurrentVectorCount int64   `json:"target_duplicate_current_vector_count"`
	ShadowReadCount                   int64   `json:"shadow_read_count"`
	ShadowErrorCount                  int64   `json:"shadow_error_count"`
	ShadowErrorRate                   float64 `json:"shadow_error_rate"`
}

func (p ActivePointerSwitchPreflight) Coverage() float64 {
	if p.EligibleProblemCount <= 0 {
		return 1.0
	}
	return float64(p.EligibleProblemCount-p.TargetMissingCurrentVectorCount) / float64(p.EligibleProblemCount)
}

func (p ActivePointerSwitchPreflight) BlockingIssues() []string {
	var issues []string
	if p.TargetMissingCurrentVectorCount > 0 {
		issues = append(issues, fmt.Sprintf("target active-vector coverage is below 100%%: missing=%d", p.TargetMissingCurrentVectorCount))
	}
	if p.TargetDuplicateCurrentVectorCount > 0 {
		issues = append(issues, fmt.Sprintf("target active-vector has duplicate current rows: duplicates=%d", p.TargetDuplicateCurrentVectorCount))
	}
	if p.ShadowReadCount > 0 && p.ShadowErrorRate >= 0.001 {
		issues = append(issues, fmt.Sprintf("shadow error rate %.6f exceeds threshold 0.001", p.ShadowErrorRate))
	}
	return issues
}

// SwitchActiveEmbeddingPointer performs a QE::T04 active pointer transition in
// one serializable transaction. If DryRun is true, the transaction is rolled
// back after all checks and audit insertion have been exercised.
func (r *VectorRepository) SwitchActiveEmbeddingPointer(
	ctx context.Context,
	options ActivePointerSwitchOptions,
) (ActivePointerSwitchReport, error) {
	if r == nil || r.db == nil {
		return ActivePointerSwitchReport{}, fmt.Errorf("vector repository database is required")
	}
	if err := validateActivePointerSwitchOptions(options); err != nil {
		return ActivePointerSwitchReport{}, err
	}

	kind := normalizeEmbeddingKind(options.Kind)
	operation := normalizeSwitchOperation(options.Operation)
	report := ActivePointerSwitchReport{
		GeneratedAt:         time.Now().UTC(),
		Decision:            EmbeddingSwitchDecisionNoGo,
		Operation:           operation,
		DryRun:              options.DryRun,
		EmbeddingKind:       kind,
		NewModelVersionID:   options.ToModelVersionID,
		Actor:               strings.TrimSpace(options.Actor),
		Reason:              strings.TrimSpace(options.Reason),
		DatasetReportSHA256: strings.TrimSpace(options.DatasetReportSHA256),
	}

	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return report, fmt.Errorf("beginning active pointer switch transaction: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback on committed tx is a no-op

	oldModelVersionID, err := lockActivePointer(ctx, tx, kind)
	if err != nil {
		return report, err
	}
	report.OldModelVersionID = oldModelVersionID
	if options.ExpectedFromModelVersionID != nil && *options.ExpectedFromModelVersionID != oldModelVersionID {
		return report, fmt.Errorf(
			"active pointer expectation mismatch for %q: database=%s expected=%s",
			kind,
			oldModelVersionID,
			*options.ExpectedFromModelVersionID,
		)
	}

	targetDimensions, err := lockTargetModelVersion(ctx, tx, options.ToModelVersionID)
	if err != nil {
		return report, err
	}

	preflight, err := scanActivePointerSwitchPreflight(
		ctx,
		tx,
		options.ToModelVersionID,
		kind,
		options.ShadowReadCount,
		options.ShadowErrorCount,
	)
	if err != nil {
		return report, err
	}
	report.Preflight = preflight
	report.BlockingIssues = preflight.BlockingIssues()
	if len(report.BlockingIssues) > 0 {
		return report, nil
	}

	if _, err := tx.Exec(ctx, `
		UPDATE embedding_model_versions
		SET status='active', updated_at=NOW()
		WHERE id=$1`, options.ToModelVersionID); err != nil {
		return report, fmt.Errorf("promoting target embedding model version: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE embedding_active_pointers
		SET model_version_id=$2,
		    expected_dimensions=$3,
		    updated_by=$4,
		    reason=$5,
		    updated_at=NOW()
		WHERE embedding_kind=$1`,
		kind,
		options.ToModelVersionID,
		targetDimensions,
		report.Actor,
		report.Reason,
	); err != nil {
		return report, fmt.Errorf("updating active embedding pointer: %w", err)
	}

	eventType := "embedding_active_pointer_" + operation
	if err := tx.QueryRow(ctx, `
		INSERT INTO provenance_audit_events (
			event_type, actor, reason, details
		) VALUES (
			$1, $2, $3,
			jsonb_build_object(
				'embedding_kind',$4::text,
				'old_model_version_id',$5::text,
				'new_model_version_id',$6::text,
				'dataset_report_sha256',$7::text,
				'shadow_read_count',$8::bigint,
				'shadow_error_count',$9::bigint,
				'dry_run',$10::boolean
			)
		)
		RETURNING event_id`,
		eventType,
		report.Actor,
		report.Reason,
		kind,
		oldModelVersionID.String(),
		options.ToModelVersionID.String(),
		report.DatasetReportSHA256,
		options.ShadowReadCount,
		options.ShadowErrorCount,
		options.DryRun,
	).Scan(&report.AuditEventID); err != nil {
		return report, fmt.Errorf("recording active pointer switch audit event: %w", err)
	}

	report.Decision = EmbeddingSwitchDecisionGo
	if options.DryRun {
		if err := tx.Rollback(ctx); err != nil {
			return report, fmt.Errorf("rolling back dry-run active pointer switch: %w", err)
		}
		return report, nil
	}
	if err := tx.Commit(ctx); err != nil {
		return report, fmt.Errorf("committing active pointer switch: %w", err)
	}
	report.Committed = true
	return report, nil
}

func validateActivePointerSwitchOptions(options ActivePointerSwitchOptions) error {
	if err := validateEmbeddingKind(normalizeEmbeddingKind(options.Kind)); err != nil {
		return err
	}
	if options.ToModelVersionID == uuid.Nil {
		return fmt.Errorf("to_model_version_id is required")
	}
	if operation := normalizeSwitchOperation(options.Operation); operation == "" {
		return fmt.Errorf("operation must be %q or %q", EmbeddingSwitchOperationCutover, EmbeddingSwitchOperationRollback)
	}
	if strings.TrimSpace(options.Actor) == "" {
		return fmt.Errorf("actor is required")
	}
	if strings.TrimSpace(options.Reason) == "" {
		return fmt.Errorf("reason is required")
	}
	if err := validateContentHash(strings.TrimSpace(options.DatasetReportSHA256)); err != nil {
		return fmt.Errorf("dataset_report_sha256: %w", err)
	}
	if options.ShadowReadCount < 0 {
		return fmt.Errorf("shadow_read_count must be non-negative")
	}
	if options.ShadowErrorCount < 0 {
		return fmt.Errorf("shadow_error_count must be non-negative")
	}
	if options.ShadowErrorCount > options.ShadowReadCount && options.ShadowReadCount > 0 {
		return fmt.Errorf("shadow_error_count cannot exceed shadow_read_count")
	}
	return nil
}

func normalizeSwitchOperation(operation string) string {
	switch strings.ToLower(strings.TrimSpace(operation)) {
	case EmbeddingSwitchOperationCutover:
		return EmbeddingSwitchOperationCutover
	case EmbeddingSwitchOperationRollback:
		return EmbeddingSwitchOperationRollback
	default:
		return ""
	}
}

func lockActivePointer(ctx context.Context, tx pgx.Tx, kind string) (uuid.UUID, error) {
	var oldModelVersionID uuid.UUID
	err := tx.QueryRow(ctx, `
		SELECT model_version_id
		FROM embedding_active_pointers
		WHERE embedding_kind=$1
		FOR UPDATE`, kind).Scan(&oldModelVersionID)
	if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, sql.ErrNoRows) {
		return uuid.Nil, fmt.Errorf("active embedding pointer for kind %q not found: %w", kind, sql.ErrNoRows)
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("locking active embedding pointer for kind %q: %w", kind, err)
	}
	return oldModelVersionID, nil
}

func lockTargetModelVersion(ctx context.Context, tx pgx.Tx, id uuid.UUID) (int, error) {
	var dimensions int
	err := tx.QueryRow(ctx, `
		SELECT dimensions
		FROM embedding_model_versions
		WHERE id=$1
		FOR UPDATE`, id).Scan(&dimensions)
	if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("target embedding model version %s not found: %w", id, sql.ErrNoRows)
	}
	if err != nil {
		return 0, fmt.Errorf("locking target embedding model version %s: %w", id, err)
	}
	return dimensions, nil
}

func scanActivePointerSwitchPreflight(
	ctx context.Context,
	tx pgx.Tx,
	targetModelVersionID uuid.UUID,
	kind string,
	shadowReadCount int64,
	shadowErrorCount int64,
) (ActivePointerSwitchPreflight, error) {
	preflight := ActivePointerSwitchPreflight{
		ShadowReadCount:  shadowReadCount,
		ShadowErrorCount: shadowErrorCount,
	}
	if shadowReadCount > 0 {
		preflight.ShadowErrorRate = float64(shadowErrorCount) / float64(shadowReadCount)
	}
	if err := tx.QueryRow(ctx, activePointerSwitchPreflightSQL(), targetModelVersionID, kind).Scan(
		&preflight.EligibleProblemCount,
		&preflight.TargetCurrentVectorCount,
		&preflight.TargetMissingCurrentVectorCount,
		&preflight.TargetDuplicateCurrentVectorCount,
	); err != nil {
		return preflight, fmt.Errorf("scanning active pointer switch preflight: %w", err)
	}
	return preflight, nil
}

func activePointerSwitchPreflightSQL() string {
	return `
WITH eligible AS (
    SELECT id, title, statement, one_line_hint
    FROM problems p
    WHERE p.status = 'published'
      AND NOT EXISTS (
          SELECT 1
          FROM problem_quarantine_records quarantine
          WHERE quarantine.problem_id = p.id
      )
),
target_rows AS (
    SELECT p.id, COUNT(pe.problem_id) AS row_count
    FROM eligible p
    LEFT JOIN problem_embeddings pe
      ON pe.problem_id = p.id
     AND pe.model_version_id = $1
     AND pe.embedding_kind = $2
     AND pe.content_hash = embedding_statement_content_hash(p.title, p.statement, p.one_line_hint)
    GROUP BY p.id
)
SELECT
    (SELECT COUNT(*) FROM eligible),
    (SELECT COUNT(*) FROM target_rows WHERE row_count = 1),
    (SELECT COUNT(*) FROM target_rows WHERE row_count = 0),
    (SELECT COUNT(*) FROM target_rows WHERE row_count > 1)`
}
