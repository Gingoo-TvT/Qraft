package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const (
	EmbeddingBackfillRunStatusPlanned   = "planned"
	EmbeddingBackfillRunStatusRunning   = "running"
	EmbeddingBackfillRunStatusCompleted = "completed"
	EmbeddingBackfillRunStatusFailed    = "failed"
)

type EmbeddingBackfillPlanOptions struct {
	FromModelVersionID *uuid.UUID `json:"from_model_version_id,omitempty"`
	ToModelVersionID   uuid.UUID  `json:"to_model_version_id"`
	Kind               string     `json:"embedding_kind"`
	AllStale           bool       `json:"all_stale"`
	Limit              int        `json:"limit"`
	AfterProblemID     *uuid.UUID `json:"after_problem_id,omitempty"`
}

type EmbeddingBackfillCandidate struct {
	ProblemID        uuid.UUID `json:"problem_id"`
	ContentHash      string    `json:"content_hash"`
	Text             string    `json:"text"`
	HasSourceCurrent bool      `json:"has_source_current"`
	HasTargetAny     bool      `json:"has_target_any"`
	HasTargetCurrent bool      `json:"has_target_current"`
}

type EmbeddingBackfillPlanReport struct {
	GeneratedAt        time.Time                    `json:"generated_at"`
	FromModelVersionID *uuid.UUID                   `json:"from_model_version_id,omitempty"`
	ToModelVersionID   uuid.UUID                    `json:"to_model_version_id"`
	EmbeddingKind      string                       `json:"embedding_kind"`
	AllStale           bool                         `json:"all_stale"`
	Limit              int                          `json:"limit"`
	AfterProblemID     *uuid.UUID                   `json:"after_problem_id,omitempty"`
	CandidateCount     int                          `json:"candidate_count"`
	Candidates         []EmbeddingBackfillCandidate `json:"candidates"`
}

type EmbeddingBackfillRunInput struct {
	RunKey             string
	FromModelVersionID *uuid.UUID
	ToModelVersionID   uuid.UUID
	Kind               string
	AllStale           bool
	DryRun             bool
	Limit              int
	RequestSHA256      string
}

type EmbeddingBackfillRunRecord struct {
	RunKey             string     `json:"run_key"`
	FromModelVersionID *uuid.UUID `json:"from_model_version_id,omitempty"`
	ToModelVersionID   uuid.UUID  `json:"to_model_version_id"`
	EmbeddingKind      string     `json:"embedding_kind"`
	AllStale           bool       `json:"all_stale"`
	DryRun             bool       `json:"dry_run"`
	Status             string     `json:"status"`
	Limit              int        `json:"limit"`
	RequestSHA256      string     `json:"request_sha256"`
	LastProblemID      *uuid.UUID `json:"last_problem_id,omitempty"`
	ScannedCount       int64      `json:"scanned_count"`
	EmbeddedCount      int64      `json:"embedded_count"`
	SkippedCount       int64      `json:"skipped_count"`
	FailedCount        int64      `json:"failed_count"`
	ErrorMessage       string     `json:"error_message,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
	CompletedAt        *time.Time `json:"completed_at,omitempty"`
}

type EmbeddingBackfillFailureInput struct {
	RunKey       string
	ProblemID    uuid.UUID
	ContentHash  string
	Kind         string
	ErrorMessage string
}

// PlanEmbeddingBackfill returns the deterministic candidate set for a shadow
// backfill run. It is read-only and does not advance checkpoints.
func (r *VectorRepository) PlanEmbeddingBackfill(
	ctx context.Context,
	options EmbeddingBackfillPlanOptions,
) (EmbeddingBackfillPlanReport, error) {
	if r == nil || r.db == nil {
		return EmbeddingBackfillPlanReport{}, fmt.Errorf("vector repository database is required")
	}
	kind := normalizeEmbeddingKind(options.Kind)
	if err := validateEmbeddingBackfillOptions(options, kind); err != nil {
		return EmbeddingBackfillPlanReport{}, err
	}

	limit := options.Limit
	if limit <= 0 {
		limit = 2_147_483_647
	}

	var fromModel any
	if options.FromModelVersionID != nil {
		fromModel = *options.FromModelVersionID
	}
	var afterProblem any
	if options.AfterProblemID != nil {
		afterProblem = *options.AfterProblemID
	}
	query, err := backfillCandidatesSQL(kind)
	if err != nil {
		return EmbeddingBackfillPlanReport{}, err
	}
	rows, err := r.db.Query(
		ctx,
		query,
		options.ToModelVersionID,
		kind,
		fromModel,
		options.AllStale,
		afterProblem,
		limit,
	)
	if err != nil {
		return EmbeddingBackfillPlanReport{}, fmt.Errorf("query embedding backfill candidates: %w", err)
	}
	defer rows.Close()

	var candidates []EmbeddingBackfillCandidate
	for rows.Next() {
		var candidate EmbeddingBackfillCandidate
		if err := rows.Scan(
			&candidate.ProblemID,
			&candidate.ContentHash,
			&candidate.Text,
			&candidate.HasSourceCurrent,
			&candidate.HasTargetAny,
			&candidate.HasTargetCurrent,
		); err != nil {
			return EmbeddingBackfillPlanReport{}, fmt.Errorf("scan embedding backfill candidate: %w", err)
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return EmbeddingBackfillPlanReport{}, fmt.Errorf("iterate embedding backfill candidates: %w", err)
	}

	return EmbeddingBackfillPlanReport{
		GeneratedAt:        time.Now().UTC(),
		FromModelVersionID: options.FromModelVersionID,
		ToModelVersionID:   options.ToModelVersionID,
		EmbeddingKind:      kind,
		AllStale:           options.AllStale,
		Limit:              options.Limit,
		AfterProblemID:     options.AfterProblemID,
		CandidateCount:     len(candidates),
		Candidates:         candidates,
	}, nil
}

// BeginEmbeddingBackfillRun creates a resumable ledger record or resumes an
// existing run with the same immutable request shape. A request mismatch is
// reported as an error so a reused run key cannot mix vector spaces.
func (r *VectorRepository) BeginEmbeddingBackfillRun(
	ctx context.Context,
	input EmbeddingBackfillRunInput,
) (EmbeddingBackfillRunRecord, error) {
	if r == nil || r.db == nil {
		return EmbeddingBackfillRunRecord{}, fmt.Errorf("vector repository database is required")
	}
	kind := normalizeEmbeddingKind(input.Kind)
	if err := validateEmbeddingBackfillRunInput(input, kind); err != nil {
		return EmbeddingBackfillRunRecord{}, err
	}

	var fromModel any
	if input.FromModelVersionID != nil {
		fromModel = *input.FromModelVersionID
	}
	var limitRows any
	if input.Limit > 0 {
		limitRows = input.Limit
	}

	record, err := scanEmbeddingBackfillRun(r.db.QueryRow(ctx, `
		INSERT INTO embedding_backfill_runs (
			run_key,
			from_model_version_id,
			to_model_version_id,
			embedding_kind,
			all_stale,
			dry_run,
			status,
			limit_rows,
			request_sha256
		)
		VALUES ($1, $2, $3, $4, $5, $6, 'running', $7, $8)
		ON CONFLICT (run_key) DO UPDATE SET
			status = CASE
				WHEN embedding_backfill_runs.status = 'completed' THEN embedding_backfill_runs.status
				ELSE 'running'
			END,
			error_message = NULL,
			updated_at = NOW()
		WHERE embedding_backfill_runs.request_sha256 = EXCLUDED.request_sha256
		  AND embedding_backfill_runs.from_model_version_id IS NOT DISTINCT FROM EXCLUDED.from_model_version_id
		  AND embedding_backfill_runs.to_model_version_id = EXCLUDED.to_model_version_id
		  AND embedding_backfill_runs.embedding_kind = EXCLUDED.embedding_kind
		  AND embedding_backfill_runs.all_stale = EXCLUDED.all_stale
		  AND embedding_backfill_runs.dry_run = EXCLUDED.dry_run
		  AND embedding_backfill_runs.limit_rows IS NOT DISTINCT FROM EXCLUDED.limit_rows
		RETURNING
			run_key,
			from_model_version_id,
			to_model_version_id,
			embedding_kind,
			all_stale,
			dry_run,
			status,
			COALESCE(limit_rows, 0),
			request_sha256,
			last_problem_id,
			scanned_count,
			embedded_count,
			skipped_count,
			failed_count,
			COALESCE(error_message, ''),
			created_at,
			updated_at,
			completed_at`,
		input.RunKey,
		fromModel,
		input.ToModelVersionID,
		kind,
		input.AllStale,
		input.DryRun,
		limitRows,
		input.RequestSHA256,
	))
	if err != nil {
		if err == pgx.ErrNoRows {
			return EmbeddingBackfillRunRecord{}, fmt.Errorf("embedding backfill run %q already exists with different immutable parameters", input.RunKey)
		}
		return EmbeddingBackfillRunRecord{}, fmt.Errorf("begin embedding backfill run %q: %w", input.RunKey, err)
	}
	return record, nil
}

func (r *VectorRepository) RecordEmbeddingBackfillProgress(
	ctx context.Context,
	runKey string,
	lastProblemID uuid.UUID,
	scannedDelta int64,
	embeddedDelta int64,
	skippedDelta int64,
) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("vector repository database is required")
	}
	if runKey == "" {
		return fmt.Errorf("run_key is required")
	}
	if lastProblemID == uuid.Nil {
		return fmt.Errorf("last_problem_id is required")
	}
	if scannedDelta < 0 || embeddedDelta < 0 || skippedDelta < 0 {
		return fmt.Errorf("embedding backfill progress deltas must be non-negative")
	}

	tag, err := r.db.Exec(ctx, `
		UPDATE embedding_backfill_runs
		SET
			status = 'running',
			last_problem_id = $2,
			scanned_count = scanned_count + $3,
			embedded_count = embedded_count + $4,
			skipped_count = skipped_count + $5,
			error_message = NULL,
			updated_at = NOW()
		WHERE run_key = $1
		  AND status <> 'completed'`,
		runKey,
		lastProblemID,
		scannedDelta,
		embeddedDelta,
		skippedDelta,
	)
	if err != nil {
		return fmt.Errorf("record embedding backfill progress for %q: %w", runKey, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("embedding backfill run %q not found or already completed", runKey)
	}
	return nil
}

func (r *VectorRepository) RecordEmbeddingBackfillFailure(ctx context.Context, input EmbeddingBackfillFailureInput) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("vector repository database is required")
	}
	kind := normalizeEmbeddingKind(input.Kind)
	if input.RunKey == "" {
		return fmt.Errorf("run_key is required")
	}
	if input.ProblemID == uuid.Nil {
		return fmt.Errorf("problem ID is required")
	}
	if err := validateEmbeddingKind(kind); err != nil {
		return err
	}
	if err := validateContentHash(input.ContentHash); err != nil {
		return err
	}
	if input.ErrorMessage == "" {
		return fmt.Errorf("error_message is required")
	}

	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin embedding backfill failure transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `
		INSERT INTO embedding_backfill_failures (
			run_key,
			problem_id,
			content_hash,
			embedding_kind,
			error_message
		)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (run_key, problem_id, content_hash, embedding_kind)
		DO UPDATE SET
			error_message = EXCLUDED.error_message,
			attempts = embedding_backfill_failures.attempts + 1,
			last_failed_at = NOW()`,
		input.RunKey,
		input.ProblemID,
		input.ContentHash,
		kind,
		input.ErrorMessage,
	); err != nil {
		return fmt.Errorf("upsert embedding backfill failure for %q problem %s: %w", input.RunKey, input.ProblemID, err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE embedding_backfill_runs
		SET failed_count = failed_count + 1,
		    error_message = $2,
		    updated_at = NOW()
		WHERE run_key = $1`,
		input.RunKey,
		input.ErrorMessage,
	); err != nil {
		return fmt.Errorf("update embedding backfill failure count for %q: %w", input.RunKey, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit embedding backfill failure transaction: %w", err)
	}
	return nil
}

func (r *VectorRepository) CompleteEmbeddingBackfillRun(ctx context.Context, runKey string) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("vector repository database is required")
	}
	if runKey == "" {
		return fmt.Errorf("run_key is required")
	}
	tag, err := r.db.Exec(ctx, `
		UPDATE embedding_backfill_runs
		SET
			status = 'completed',
			error_message = NULL,
			completed_at = COALESCE(completed_at, NOW()),
			updated_at = NOW()
		WHERE run_key = $1`,
		runKey,
	)
	if err != nil {
		return fmt.Errorf("complete embedding backfill run %q: %w", runKey, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("embedding backfill run %q not found", runKey)
	}
	return nil
}

func (r *VectorRepository) FailEmbeddingBackfillRun(ctx context.Context, runKey, errorMessage string) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("vector repository database is required")
	}
	if runKey == "" {
		return fmt.Errorf("run_key is required")
	}
	if errorMessage == "" {
		return fmt.Errorf("error_message is required")
	}
	tag, err := r.db.Exec(ctx, `
		UPDATE embedding_backfill_runs
		SET
			status = 'failed',
			error_message = $2,
			updated_at = NOW()
		WHERE run_key = $1
		  AND status <> 'completed'`,
		runKey,
		errorMessage,
	)
	if err != nil {
		return fmt.Errorf("fail embedding backfill run %q: %w", runKey, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("embedding backfill run %q not found or already completed", runKey)
	}
	return nil
}

func validateEmbeddingBackfillOptions(options EmbeddingBackfillPlanOptions, kind string) error {
	if options.ToModelVersionID == uuid.Nil {
		return fmt.Errorf("to_model_version_id is required")
	}
	if err := validateEmbeddingKind(kind); err != nil {
		return err
	}
	if options.Limit < 0 {
		return fmt.Errorf("limit must be non-negative")
	}
	if options.FromModelVersionID != nil && *options.FromModelVersionID == options.ToModelVersionID {
		return fmt.Errorf("from_model_version_id and to_model_version_id must differ")
	}
	return nil
}

func validateEmbeddingBackfillRunInput(input EmbeddingBackfillRunInput, kind string) error {
	if input.RunKey == "" {
		return fmt.Errorf("run_key is required")
	}
	if err := validateEmbeddingBackfillOptions(EmbeddingBackfillPlanOptions{
		FromModelVersionID: input.FromModelVersionID,
		ToModelVersionID:   input.ToModelVersionID,
		Kind:               kind,
		AllStale:           input.AllStale,
		Limit:              input.Limit,
	}, kind); err != nil {
		return err
	}
	if err := validateContentHash(input.RequestSHA256); err != nil {
		return fmt.Errorf("request_sha256 must be a lowercase sha256 hex digest")
	}
	return nil
}

func scanEmbeddingBackfillRun(row pgx.Row) (EmbeddingBackfillRunRecord, error) {
	var record EmbeddingBackfillRunRecord
	var fromModel pgtype.UUID
	var lastProblem pgtype.UUID
	var completedAt sql.NullTime
	if err := row.Scan(
		&record.RunKey,
		&fromModel,
		&record.ToModelVersionID,
		&record.EmbeddingKind,
		&record.AllStale,
		&record.DryRun,
		&record.Status,
		&record.Limit,
		&record.RequestSHA256,
		&lastProblem,
		&record.ScannedCount,
		&record.EmbeddedCount,
		&record.SkippedCount,
		&record.FailedCount,
		&record.ErrorMessage,
		&record.CreatedAt,
		&record.UpdatedAt,
		&completedAt,
	); err != nil {
		return EmbeddingBackfillRunRecord{}, err
	}
	if fromModel.Valid {
		id := uuid.UUID(fromModel.Bytes)
		record.FromModelVersionID = &id
	}
	if lastProblem.Valid {
		id := uuid.UUID(lastProblem.Bytes)
		record.LastProblemID = &id
	}
	if completedAt.Valid {
		record.CompletedAt = &completedAt.Time
	}
	return record, nil
}

func backfillCandidatesSQL(kind string) (string, error) {
	switch kind {
	case EmbeddingKindStatement:
		return statementBackfillCandidatesSQL(), nil
	case EmbeddingKindSolution:
		return solutionBackfillCandidatesSQL(), nil
	default:
		return "", fmt.Errorf("unsupported embedding_kind %q", kind)
	}
}

func statementBackfillCandidatesSQL() string {
	return `
WITH current_statement AS (
    SELECT
        p.id AS problem_id,
        embedding_statement_content_hash(p.title, p.statement, p.one_line_hint) AS content_hash,
        p.title || E'\n\n' || p.statement || E'\n\n' || COALESCE(p.one_line_hint, '') AS embedding_text
    FROM problems p
    WHERE p.status <> 'quarantined'
      AND ($5::uuid IS NULL OR p.id > $5::uuid)
      AND NOT EXISTS (
          SELECT 1
          FROM problem_quarantine_records quarantine
          WHERE quarantine.problem_id = p.id
      )
),
candidate_state AS (
    SELECT
        current_statement.problem_id,
        current_statement.content_hash,
        current_statement.embedding_text,
        CASE
            WHEN $3::uuid IS NULL THEN TRUE
            ELSE EXISTS (
                SELECT 1
                FROM problem_embeddings source
                WHERE source.problem_id = current_statement.problem_id
                  AND source.model_version_id = $3::uuid
                  AND source.embedding_kind = $2
                  AND source.content_hash = current_statement.content_hash
            )
        END AS has_source_current,
        EXISTS (
            SELECT 1
            FROM problem_embeddings target_any
            WHERE target_any.problem_id = current_statement.problem_id
              AND target_any.model_version_id = $1
              AND target_any.embedding_kind = $2
        ) AS has_target_any,
        EXISTS (
            SELECT 1
            FROM problem_embeddings target_current
            WHERE target_current.problem_id = current_statement.problem_id
              AND target_current.model_version_id = $1
              AND target_current.embedding_kind = $2
              AND target_current.content_hash = current_statement.content_hash
        ) AS has_target_current
    FROM current_statement
)
SELECT
    problem_id,
    content_hash,
    embedding_text,
    has_source_current,
    has_target_any,
    has_target_current
FROM candidate_state
WHERE has_source_current
  AND CASE
      WHEN $4 THEN NOT has_target_current
      ELSE NOT has_target_any
  END
ORDER BY problem_id
LIMIT $6`
}

func solutionBackfillCandidatesSQL() string {
	return `
WITH current_solution AS (
    SELECT
        p.id AS problem_id,
        encode(digest(convert_to(
            s.solution_type || E'\n\n' || s.language || E'\n\n' || s.source_code,
            'UTF8'
        ), 'sha256'), 'hex') AS content_hash,
        s.solution_type || E'\n\n' || s.language || E'\n\n' || s.source_code AS embedding_text
    FROM problems p
    INNER JOIN LATERAL (
        SELECT solution_type, language, source_code, created_at, id
        FROM solutions s
        WHERE s.problem_id = p.id
          AND s.solution_type = 'main'
          AND s.compile_status = 'success'
          AND btrim(s.source_code) <> ''
          AND COALESCE(s.metadata_json ->> 'stale', 'false') <> 'true'
        ORDER BY s.created_at DESC, s.id DESC
        LIMIT 1
    ) s ON TRUE
    WHERE p.status <> 'quarantined'
      AND ($5::uuid IS NULL OR p.id > $5::uuid)
      AND NOT EXISTS (
          SELECT 1
          FROM problem_quarantine_records quarantine
          WHERE quarantine.problem_id = p.id
      )
),
candidate_state AS (
    SELECT
        current_solution.problem_id,
        current_solution.content_hash,
        current_solution.embedding_text,
        CASE
            WHEN $3::uuid IS NULL THEN TRUE
            ELSE EXISTS (
                SELECT 1
                FROM problem_embeddings source
                WHERE source.problem_id = current_solution.problem_id
                  AND source.model_version_id = $3::uuid
                  AND source.embedding_kind = $2
                  AND source.content_hash = current_solution.content_hash
            )
        END AS has_source_current,
        EXISTS (
            SELECT 1
            FROM problem_embeddings target_any
            WHERE target_any.problem_id = current_solution.problem_id
              AND target_any.model_version_id = $1
              AND target_any.embedding_kind = $2
        ) AS has_target_any,
        EXISTS (
            SELECT 1
            FROM problem_embeddings target_current
            WHERE target_current.problem_id = current_solution.problem_id
              AND target_current.model_version_id = $1
              AND target_current.embedding_kind = $2
              AND target_current.content_hash = current_solution.content_hash
        ) AS has_target_current
    FROM current_solution
)
SELECT
    problem_id,
    content_hash,
    embedding_text,
    has_source_current,
    has_target_any,
    has_target_current
FROM candidate_state
WHERE has_source_current
  AND CASE
      WHEN $4 THEN NOT has_target_current
      ELSE NOT has_target_any
  END
ORDER BY problem_id
LIMIT $6`
}
