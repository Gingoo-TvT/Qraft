package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrConcurrentProblemEdit      = errors.New("problem update conflict")
	ErrProblemStatusNotEditable   = errors.New("problem status does not allow editing")
	ErrProblemEditNoMutableFields = errors.New("problem update has no mutable fields")
	ErrProblemEditRefreshBlocked  = errors.New("problem edit refresh blocked")
)

// ProblemEditPatch is the narrow S2.6 edit contract. System-managed fields
// such as status, source and workflow_id are deliberately absent.
type ProblemEditPatch struct {
	ProblemID         uuid.UUID
	ExpectedUpdatedAt time.Time
	Actor             string

	Title            *string
	Statement        *string
	Level            *domain.ProblemLevel
	Difficulty       *int
	OneLineHint      *string
	DetailedSolution *string
	Tags             *[]string
	TimeLimit        *int
	MemoryLimit      *int
	MetadataJSON     *json.RawMessage
}

type ProblemEditRefreshOptions struct {
	ProblemID              uuid.UUID
	OperationKey           string
	Actor                  string
	ValidationReportSHA256 string
}

type ProblemEditRefreshReport struct {
	GeneratedAt             time.Time                   `json:"generated_at"`
	Decision                string                      `json:"decision"`
	ProblemID               uuid.UUID                   `json:"problem_id"`
	OperationKey            string                      `json:"operation_key"`
	ValidationReportSHA256  string                      `json:"validation_report_sha256"`
	Preflight               ProblemEditRefreshPreflight `json:"preflight"`
	BlockingIssues          []string                    `json:"blocking_issues,omitempty"`
	ReleaseStatus           domain.ProblemStatus        `json:"release_status,omitempty"`
	ReleaseQuarantineReason string                      `json:"release_quarantine_reason,omitempty"`
}

type ProblemEditRefreshPreflight struct {
	ProblemStale                      bool  `json:"problem_stale"`
	CurrentActiveStatementVectorCount int64 `json:"current_active_statement_vector_count"`
	CurrentActiveStatementVectorStale int64 `json:"current_active_statement_vector_stale"`
	SuccessfulMainSolutionCount       int64 `json:"successful_main_solution_count"`
	SuccessfulBruteSolutionCount      int64 `json:"successful_brute_solution_count"`
	RunnableTestcaseCount             int64 `json:"runnable_testcase_count"`
	StaleSolutionCount                int64 `json:"stale_solution_count"`
	StaleTestcaseCount                int64 `json:"stale_testcase_count"`
}

func (p ProblemEditRefreshPreflight) BlockingIssues() []string {
	var issues []string
	if p.CurrentActiveStatementVectorCount != 1 {
		issues = append(issues, fmt.Sprintf("current active statement vector count is %d, want 1", p.CurrentActiveStatementVectorCount))
	}
	if p.CurrentActiveStatementVectorStale > 0 {
		issues = append(issues, "current active statement vector is still stale")
	}
	if p.SuccessfulMainSolutionCount == 0 {
		issues = append(issues, "successful main solution is required")
	}
	if p.SuccessfulBruteSolutionCount == 0 {
		issues = append(issues, "successful brute solution is required")
	}
	if p.RunnableTestcaseCount == 0 {
		issues = append(issues, "runnable testcase is required")
	}
	return issues
}

const ProblemEditRefreshDecisionGo = "go"
const ProblemEditRefreshDecisionNoGo = "no_go"

// Edit applies a user-editable patch in a serializable transaction. If the row
// was published and any mutable field changes, it atomically leaves the public
// surface by returning to draft and marks dependent vectors/solutions/tests as
// stale so the problem must be re-embedded and revalidated before publishing.
func (r *ProblemRepository) Edit(ctx context.Context, patch ProblemEditPatch) (*domain.Problem, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("problem repository database is required")
	}
	if err := validateProblemEditPatch(patch); err != nil {
		return nil, err
	}

	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return nil, fmt.Errorf("beginning problem edit transaction: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback on committed tx is a no-op

	current, err := lockProblemForEdit(ctx, tx, patch.ProblemID)
	if err != nil {
		return nil, err
	}
	if !current.UpdatedAt.Equal(patch.ExpectedUpdatedAt) {
		return nil, fmt.Errorf("%w: updated_at changed", ErrConcurrentProblemEdit)
	}
	if current.Status == domain.ProblemStatusGenerating || current.Status == domain.ProblemStatusReview {
		return nil, fmt.Errorf("%w: status %q", ErrProblemStatusNotEditable, current.Status)
	}

	next := cloneProblemForEdit(current)
	if err := applyProblemEditPatch(next, patch); err != nil {
		return nil, err
	}
	changed := problemEditableFieldsChanged(current, next)
	if !changed {
		return current, nil
	}

	wasPublished := current.Status == domain.ProblemStatusPublished
	if wasPublished {
		next.Status = domain.ProblemStatusDraft
	}
	now := time.Now().UTC()
	next.UpdatedAt = now
	next.MetadataJSON, err = problemEditMetadata(next.MetadataJSON, wasPublished, now)
	if err != nil {
		return nil, err
	}
	if err := next.Validate(); err != nil {
		return nil, fmt.Errorf("validation: %w", err)
	}

	updated, err := updateProblemEditableFields(ctx, tx, next)
	if err != nil {
		return nil, err
	}
	if err := markProblemEditDependentsStale(ctx, tx, patch.ProblemID, patch.Actor, now, wasPublished); err != nil {
		return nil, err
	}
	if err := recordProblemEditAudit(ctx, tx, current, updated, patch.Actor, now); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		if isProblemEditSerializationFailure(err) {
			return nil, fmt.Errorf("%w: serialization failure", ErrConcurrentProblemEdit)
		}
		return nil, fmt.Errorf("committing problem edit: %w", err)
	}
	return updated, nil
}

func (r *ProblemRepository) CompleteProblemEditRefresh(
	ctx context.Context,
	options ProblemEditRefreshOptions,
) (ProblemEditRefreshReport, error) {
	if r == nil || r.db == nil {
		return ProblemEditRefreshReport{}, fmt.Errorf("problem repository database is required")
	}
	if err := validateProblemEditRefreshOptions(options); err != nil {
		return ProblemEditRefreshReport{}, err
	}
	options.OperationKey = strings.TrimSpace(options.OperationKey)
	options.Actor = strings.TrimSpace(options.Actor)
	options.ValidationReportSHA256 = strings.TrimSpace(options.ValidationReportSHA256)
	payloadHash, err := problemEditRefreshPayloadHash(options)
	if err != nil {
		return ProblemEditRefreshReport{}, err
	}

	ledger := NewWorkflowOperationRepository(r.db)
	operation, err := ledger.Begin(ctx, options.OperationKey, "problem_edit_refresh/v1", payloadHash)
	if err != nil {
		return ProblemEditRefreshReport{}, err
	}
	if operation.Status == OperationStatusCompleted {
		var cached ProblemEditRefreshReport
		if err := json.Unmarshal(operation.ResultJSON, &cached); err != nil {
			return ProblemEditRefreshReport{}, fmt.Errorf("decoding cached problem edit refresh result: %w", err)
		}
		return cached, nil
	}

	report, err := r.completeProblemEditRefreshOnce(ctx, options)
	if err != nil {
		_ = ledger.Fail(ctx, options.OperationKey, payloadHash, err.Error())
		return report, err
	}
	if report.Decision != ProblemEditRefreshDecisionGo {
		_ = ledger.Fail(ctx, options.OperationKey, payloadHash, strings.Join(report.BlockingIssues, "; "))
		return report, nil
	}

	resultJSON, err := json.Marshal(report)
	if err != nil {
		_ = ledger.Fail(ctx, options.OperationKey, payloadHash, err.Error())
		return report, fmt.Errorf("encoding problem edit refresh result: %w", err)
	}
	if err := ledger.Complete(ctx, options.OperationKey, payloadHash, resultJSON); err != nil {
		return report, err
	}
	return report, nil
}

func (r *ProblemRepository) completeProblemEditRefreshOnce(
	ctx context.Context,
	options ProblemEditRefreshOptions,
) (ProblemEditRefreshReport, error) {
	report := ProblemEditRefreshReport{
		GeneratedAt:            time.Now().UTC(),
		Decision:               ProblemEditRefreshDecisionNoGo,
		ProblemID:              options.ProblemID,
		OperationKey:           options.OperationKey,
		ValidationReportSHA256: options.ValidationReportSHA256,
	}

	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return report, fmt.Errorf("beginning problem edit refresh transaction: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback on committed tx is a no-op

	preflight, err := scanProblemEditRefreshPreflight(ctx, tx, options.ProblemID)
	if err != nil {
		return report, err
	}
	report.Preflight = preflight
	report.BlockingIssues = preflight.BlockingIssues()
	if len(report.BlockingIssues) > 0 {
		return report, nil
	}

	if err := clearProblemEditStale(ctx, tx, options); err != nil {
		return report, err
	}
	if err := recordProblemEditRefreshAudit(ctx, tx, options, report.GeneratedAt); err != nil {
		return report, err
	}
	if err := tx.Commit(ctx); err != nil {
		if isProblemEditSerializationFailure(err) {
			return report, fmt.Errorf("%w: serialization failure", ErrConcurrentProblemEdit)
		}
		return report, fmt.Errorf("committing problem edit refresh: %w", err)
	}

	status, reason, err := r.ApplyPublicReleaseGate(ctx, options.ProblemID, options.OperationKey+":release_gate")
	if err != nil {
		return report, err
	}
	report.ReleaseStatus = status
	report.ReleaseQuarantineReason = reason
	if status != domain.ProblemStatusPublished {
		report.BlockingIssues = append(report.BlockingIssues, reason)
		return report, nil
	}
	report.Decision = ProblemEditRefreshDecisionGo
	return report, nil
}

func validateProblemEditRefreshOptions(options ProblemEditRefreshOptions) error {
	if options.ProblemID == uuid.Nil {
		return fmt.Errorf("problem ID is required")
	}
	if strings.TrimSpace(options.OperationKey) == "" {
		return fmt.Errorf("operation key is required")
	}
	if strings.TrimSpace(options.Actor) == "" {
		return fmt.Errorf("actor is required")
	}
	if err := validateContentHash(strings.TrimSpace(options.ValidationReportSHA256)); err != nil {
		return fmt.Errorf("validation_report_sha256: %w", err)
	}
	return nil
}

func problemEditRefreshPayloadHash(options ProblemEditRefreshOptions) (string, error) {
	payload, err := json.Marshal(map[string]string{
		"problem_id":               options.ProblemID.String(),
		"operation_key":            strings.TrimSpace(options.OperationKey),
		"actor":                    strings.TrimSpace(options.Actor),
		"validation_report_sha256": strings.TrimSpace(options.ValidationReportSHA256),
	})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

func scanProblemEditRefreshPreflight(ctx context.Context, tx pgx.Tx, problemID uuid.UUID) (ProblemEditRefreshPreflight, error) {
	var preflight ProblemEditRefreshPreflight
	if err := tx.QueryRow(ctx, problemEditRefreshPreflightSQL(), problemID).Scan(
		&preflight.ProblemStale,
		&preflight.CurrentActiveStatementVectorCount,
		&preflight.CurrentActiveStatementVectorStale,
		&preflight.SuccessfulMainSolutionCount,
		&preflight.SuccessfulBruteSolutionCount,
		&preflight.RunnableTestcaseCount,
		&preflight.StaleSolutionCount,
		&preflight.StaleTestcaseCount,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return preflight, fmt.Errorf("problem %s not found for refresh: %w", problemID, sql.ErrNoRows)
		}
		return preflight, fmt.Errorf("scanning problem edit refresh preflight: %w", err)
	}
	return preflight, nil
}

func problemEditRefreshPreflightSQL() string {
	return `
WITH problem_row AS (
    SELECT id, title, statement, one_line_hint, metadata_json
    FROM problems
    WHERE id = $1
    FOR UPDATE
),
current_active_statement AS (
    SELECT pe.problem_id, pe.metadata_json
    FROM problem_row p
    INNER JOIN embedding_active_pointers active ON active.embedding_kind = 'statement'
    INNER JOIN problem_embeddings pe
      ON pe.problem_id = p.id
     AND pe.model_version_id = active.model_version_id
     AND pe.embedding_kind = active.embedding_kind
     AND pe.content_hash = embedding_statement_content_hash(p.title, p.statement, p.one_line_hint)
)
SELECT
    COALESCE((SELECT metadata_json ->> 'stale' = 'true' FROM problem_row), false),
    (SELECT COUNT(*) FROM current_active_statement),
    (SELECT COUNT(*) FROM current_active_statement WHERE COALESCE(metadata_json ->> 'stale', 'false') = 'true'),
    (SELECT COUNT(*) FROM solutions WHERE problem_id=$1 AND solution_type='main' AND compile_status='success' AND btrim(source_code) <> ''),
    (SELECT COUNT(*) FROM solutions WHERE problem_id=$1 AND solution_type='brute' AND compile_status='success' AND btrim(source_code) <> ''),
    (SELECT COUNT(*) FROM testcases WHERE problem_id=$1 AND btrim(input_path) <> '' AND btrim(output_path) <> ''),
    (SELECT COUNT(*) FROM solutions WHERE problem_id=$1 AND COALESCE(metadata_json ->> 'stale', 'false') = 'true'),
    (SELECT COUNT(*) FROM testcases WHERE problem_id=$1 AND COALESCE(metadata_json ->> 'stale', 'false') = 'true')`
}

func clearProblemEditStale(ctx context.Context, tx pgx.Tx, options ProblemEditRefreshOptions) error {
	if _, err := tx.Exec(
		ctx,
		clearProblemEditStaleSQL(),
		options.ProblemID,
		options.ValidationReportSHA256,
		options.Actor,
		time.Now().UTC(),
	); err != nil {
		return fmt.Errorf("clearing problem edit stale markers: %w", err)
	}
	return nil
}

func clearProblemEditStaleSQL() string {
	return `
WITH fresh AS (
    SELECT jsonb_build_object(
        'last_edit_refresh_validation_sha256', $2::text,
        'last_edit_refresh_actor', $3::text,
        'last_edit_refresh_at', $4::timestamptz
    ) AS metadata
),
problem_row AS (
    SELECT id, title, statement, one_line_hint
    FROM problems
    WHERE id = $1
),
clear_problem AS (
    UPDATE problems p
    SET metadata_json = jsonb_strip_nulls(
            (COALESCE(p.metadata_json, '{}'::jsonb)
                - 'stale'
                - 'stale_reason'
                - 'stale_at'
                - 'stale_source'
                - 'stale_actor'
                - 'edit_requires_republish'
                - 'edit_gate_version') || fresh.metadata
        ),
        updated_at = NOW()
    FROM fresh
    WHERE p.id = $1
    RETURNING p.id
),
clear_current_embedding AS (
    UPDATE problem_embeddings pe
    SET metadata_json = jsonb_strip_nulls(
            (COALESCE(pe.metadata_json, '{}'::jsonb)
                - 'stale'
                - 'stale_reason'
                - 'stale_at'
                - 'stale_source'
                - 'stale_actor'
                - 'edit_requires_republish'
                - 'edit_gate_version') || fresh.metadata
        ),
        updated_at = NOW()
    FROM fresh, problem_row p, embedding_active_pointers active
    WHERE pe.problem_id = p.id
      AND active.embedding_kind = 'statement'
      AND pe.model_version_id = active.model_version_id
      AND pe.embedding_kind = active.embedding_kind
      AND pe.content_hash = embedding_statement_content_hash(p.title, p.statement, p.one_line_hint)
    RETURNING pe.problem_id
),
clear_solutions AS (
    UPDATE solutions s
    SET metadata_json = jsonb_strip_nulls(
            (COALESCE(s.metadata_json, '{}'::jsonb)
                - 'stale'
                - 'stale_reason'
                - 'stale_at'
                - 'stale_source'
                - 'stale_actor'
                - 'edit_requires_republish'
                - 'edit_gate_version') || fresh.metadata
        )
    FROM fresh
    WHERE s.problem_id = $1
    RETURNING s.problem_id
),
clear_testcases AS (
    UPDATE testcases tc
    SET metadata_json = jsonb_strip_nulls(
            (COALESCE(tc.metadata_json, '{}'::jsonb)
                - 'stale'
                - 'stale_reason'
                - 'stale_at'
                - 'stale_source'
                - 'stale_actor'
                - 'edit_requires_republish'
                - 'edit_gate_version') || fresh.metadata
        )
    FROM fresh
    WHERE tc.problem_id = $1
    RETURNING tc.problem_id
)
SELECT
    (SELECT COUNT(*) FROM clear_problem),
    (SELECT COUNT(*) FROM clear_current_embedding),
    (SELECT COUNT(*) FROM clear_solutions),
    (SELECT COUNT(*) FROM clear_testcases)`
}

func recordProblemEditRefreshAudit(ctx context.Context, tx pgx.Tx, options ProblemEditRefreshOptions, refreshedAt time.Time) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO provenance_audit_events (
			event_type, actor, reason, details
		) VALUES (
			'problem_edit_refresh_completed',
			$1,
			'edited problem was re-embedded and revalidated before release gate',
			jsonb_build_object(
				'problem_id',$2::text,
				'operation_key',$3::text,
				'validation_report_sha256',$4::text,
				'refreshed_at',$5::timestamptz
			)
		)`,
		options.Actor,
		options.ProblemID.String(),
		options.OperationKey,
		options.ValidationReportSHA256,
		refreshedAt,
	)
	if err != nil {
		return fmt.Errorf("recording problem edit refresh audit event: %w", err)
	}
	return nil
}

func validateProblemEditPatch(patch ProblemEditPatch) error {
	if patch.ProblemID == uuid.Nil {
		return fmt.Errorf("problem ID is required")
	}
	if patch.ExpectedUpdatedAt.IsZero() {
		return fmt.Errorf("expected_updated_at is required")
	}
	if strings.TrimSpace(patch.Actor) == "" {
		return fmt.Errorf("actor is required")
	}
	if !patch.hasMutableField() {
		return ErrProblemEditNoMutableFields
	}
	if patch.MetadataJSON != nil {
		if !json.Valid(*patch.MetadataJSON) {
			return fmt.Errorf("metadata_json must be valid JSON")
		}
		var value interface{}
		if err := json.Unmarshal(*patch.MetadataJSON, &value); err != nil {
			return fmt.Errorf("metadata_json: %w", err)
		}
		if _, ok := value.(map[string]interface{}); !ok {
			return fmt.Errorf("metadata_json must be a JSON object")
		}
	}
	return nil
}

func (p ProblemEditPatch) hasMutableField() bool {
	return p.Title != nil ||
		p.Statement != nil ||
		p.Level != nil ||
		p.Difficulty != nil ||
		p.OneLineHint != nil ||
		p.DetailedSolution != nil ||
		p.Tags != nil ||
		p.TimeLimit != nil ||
		p.MemoryLimit != nil ||
		p.MetadataJSON != nil
}

func lockProblemForEdit(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.Problem, error) {
	query := `SELECT ` + problemSelectColumns + ` FROM problems WHERE id=$1 FOR UPDATE`
	problem := &domain.Problem{}
	err := scanProblemRow(tx.QueryRow(ctx, query, id), problem)
	if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("problem %s not found for edit: %w", id, sql.ErrNoRows)
	}
	if err != nil {
		if isProblemEditSerializationFailure(err) {
			return nil, fmt.Errorf("%w: serialization failure", ErrConcurrentProblemEdit)
		}
		return nil, fmt.Errorf("locking problem %s for edit: %w", id, err)
	}
	return problem, nil
}

func isProblemEditSerializationFailure(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "40001"
}

func cloneProblemForEdit(current *domain.Problem) *domain.Problem {
	next := *current
	next.Tags = append([]string(nil), current.Tags...)
	next.MetadataJSON = append(json.RawMessage(nil), current.MetadataJSON...)
	return &next
}

func applyProblemEditPatch(problem *domain.Problem, patch ProblemEditPatch) error {
	if patch.Title != nil {
		problem.Title = strings.TrimSpace(*patch.Title)
	}
	if patch.Statement != nil {
		problem.Statement = strings.TrimSpace(*patch.Statement)
	}
	if patch.Level != nil {
		problem.Level = *patch.Level
	}
	if patch.Difficulty != nil {
		problem.Difficulty = *patch.Difficulty
	}
	if patch.OneLineHint != nil {
		problem.OneLineHint = strings.TrimSpace(*patch.OneLineHint)
	}
	if patch.DetailedSolution != nil {
		problem.DetailedSolution = strings.TrimSpace(*patch.DetailedSolution)
	}
	if patch.Tags != nil {
		problem.Tags = append([]string(nil), (*patch.Tags)...)
	}
	if patch.TimeLimit != nil {
		problem.TimeLimit = *patch.TimeLimit
	}
	if patch.MemoryLimit != nil {
		problem.MemoryLimit = *patch.MemoryLimit
	}
	if patch.MetadataJSON != nil {
		problem.MetadataJSON = append(json.RawMessage(nil), (*patch.MetadataJSON)...)
	}
	return nil
}

func problemEditableFieldsChanged(before, after *domain.Problem) bool {
	return before.Title != after.Title ||
		before.Statement != after.Statement ||
		before.Level != after.Level ||
		before.Difficulty != after.Difficulty ||
		before.OneLineHint != after.OneLineHint ||
		before.DetailedSolution != after.DetailedSolution ||
		!reflect.DeepEqual(before.Tags, after.Tags) ||
		before.TimeLimit != after.TimeLimit ||
		before.MemoryLimit != after.MemoryLimit ||
		string(before.MetadataJSON) != string(after.MetadataJSON)
}

func problemEditMetadata(base json.RawMessage, wasPublished bool, editedAt time.Time) (json.RawMessage, error) {
	var metadata map[string]interface{}
	if len(base) == 0 {
		metadata = map[string]interface{}{}
	} else if err := json.Unmarshal(base, &metadata); err != nil {
		return nil, fmt.Errorf("decoding problem metadata_json: %w", err)
	}
	if metadata == nil {
		metadata = map[string]interface{}{}
	}
	// A mutable edit invalidates the previous release decision. Remove the
	// old gate outcome and receipt so API/UI consumers cannot mistake the
	// stale draft for a still-published revision.
	for _, key := range []string{
		"publication_gate_status",
		"publication_quarantine_reason",
		"last_edit_refresh_validation_sha256",
		"last_edit_refresh_actor",
		"last_edit_refresh_at",
	} {
		delete(metadata, key)
	}
	metadata["stale"] = true
	metadata["stale_reason"] = "problem edited; revalidation and re-embedding required"
	metadata["stale_at"] = editedAt.Format(time.RFC3339Nano)
	metadata["stale_source"] = "problem_edit"
	metadata["edit_requires_republish"] = wasPublished
	metadata["edit_gate_version"] = "2026-08-17.s2_6"
	return json.Marshal(metadata)
}

func updateProblemEditableFields(ctx context.Context, tx pgx.Tx, problem *domain.Problem) (*domain.Problem, error) {
	query := `UPDATE problems SET
		title=$2,
		statement=$3,
		level=$4,
		difficulty=$5,
		one_line_hint=$6,
		detailed_solution=$7,
		tags=$8,
		time_limit=$9,
		memory_limit=$10,
		status=$11,
		metadata_json=$12,
		updated_at=$13
	WHERE id=$1
	RETURNING ` + problemSelectColumns
	updated := &domain.Problem{}
	if err := scanProblemRow(tx.QueryRow(
		ctx,
		query,
		problem.ID,
		problem.Title,
		problem.Statement,
		problem.Level,
		problem.Difficulty,
		problem.OneLineHint,
		problem.DetailedSolution,
		problem.Tags,
		problem.TimeLimit,
		problem.MemoryLimit,
		problem.Status,
		problem.MetadataJSON,
		problem.UpdatedAt,
	), updated); err != nil {
		return nil, fmt.Errorf("updating editable problem fields: %w", err)
	}
	return updated, nil
}

func markProblemEditDependentsStale(ctx context.Context, tx pgx.Tx, problemID uuid.UUID, actor string, editedAt time.Time, wasPublished bool) error {
	if _, err := tx.Exec(ctx, markProblemEditDependentsStaleSQL(), problemID, actor, editedAt, wasPublished); err != nil {
		return fmt.Errorf("marking problem edit dependents stale: %w", err)
	}
	return nil
}

func markProblemEditDependentsStaleSQL() string {
	return `
WITH stale AS (
    SELECT jsonb_build_object(
        'stale', true,
        'stale_reason', 'problem edited; revalidation and re-embedding required',
        'stale_source', 'problem_edit',
        'stale_actor', $2::text,
        'stale_at', $3::timestamptz,
        'edit_requires_republish', $4::boolean
    ) AS metadata
),
stale_embeddings AS (
    UPDATE problem_embeddings pe
    SET metadata_json = COALESCE(pe.metadata_json, '{}'::jsonb) || stale.metadata,
        updated_at = NOW()
    FROM stale
    WHERE pe.problem_id = $1
      AND EXISTS (
          SELECT 1
          FROM embedding_active_pointers active
          WHERE active.embedding_kind = pe.embedding_kind
            AND active.model_version_id = pe.model_version_id
      )
    RETURNING pe.problem_id
),
stale_solutions AS (
    UPDATE solutions s
    SET metadata_json = COALESCE(s.metadata_json, '{}'::jsonb) || stale.metadata
    FROM stale
    WHERE s.problem_id = $1
    RETURNING s.problem_id
),
stale_testcases AS (
    UPDATE testcases tc
    SET metadata_json = COALESCE(tc.metadata_json, '{}'::jsonb) || stale.metadata
    FROM stale
    WHERE tc.problem_id = $1
    RETURNING tc.problem_id
)
SELECT
    (SELECT COUNT(*) FROM stale_embeddings),
    (SELECT COUNT(*) FROM stale_solutions),
    (SELECT COUNT(*) FROM stale_testcases)`
}

func recordProblemEditAudit(ctx context.Context, tx pgx.Tx, before, after *domain.Problem, actor string, editedAt time.Time) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO provenance_audit_events (
			event_type, actor, reason, details
		) VALUES (
			'problem_edit_stale_marked',
			$1,
			'problem edited; public release requires regeneration gate',
			jsonb_build_object(
				'problem_id',$2::text,
				'old_status',$3::text,
				'new_status',$4::text,
				'old_updated_at',$5::timestamptz,
				'new_updated_at',$6::timestamptz,
				'edited_at',$7::timestamptz
			)
		)`,
		actor,
		before.ID.String(),
		string(before.Status),
		string(after.Status),
		before.UpdatedAt,
		after.UpdatedAt,
		editedAt,
	)
	if err != nil {
		return fmt.Errorf("recording problem edit audit event: %w", err)
	}
	return nil
}
