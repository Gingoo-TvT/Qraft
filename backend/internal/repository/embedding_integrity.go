package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

const (
	EmbeddingIntegrityDecisionGo   = "go"
	EmbeddingIntegrityDecisionNoGo = "no_go"
)

// EmbeddingIntegrityReport is the shared evidence shape for QE::T02/T10 active
// vector scans. Counts are intentionally narrow and DB-derived so the same
// report can be used by CLI evidence, API preflight, and later publication
// gates without another interpretation layer.
type EmbeddingIntegrityReport struct {
	GeneratedAt                time.Time                         `json:"generated_at"`
	Decision                   string                            `json:"decision"`
	BlockingIssueCount         int64                             `json:"blocking_issue_count"`
	PublishedStatementCoverage float64                           `json:"published_statement_coverage"`
	ActivePointers             []EmbeddingActivePointerSummary   `json:"active_pointers"`
	Counts                     EmbeddingIntegrityViolationCounts `json:"counts"`
}

type EmbeddingActivePointerSummary struct {
	EmbeddingKind      string    `json:"embedding_kind"`
	ModelVersionID     uuid.UUID `json:"model_version_id"`
	Provider           string    `json:"provider"`
	ModelID            string    `json:"model_id"`
	Revision           string    `json:"revision"`
	WeightsHash        string    `json:"weights_hash"`
	Dimensions         int       `json:"dimensions"`
	ExpectedDimensions int       `json:"expected_dimensions"`
	Status             string    `json:"status"`
}

type EmbeddingIntegrityViolationCounts struct {
	MissingStatementActivePointer                 int64 `json:"missing_statement_active_pointer"`
	MissingSolutionActivePointer                  int64 `json:"missing_solution_active_pointer"`
	ActivePointerInactiveModels                   int64 `json:"active_pointer_inactive_models"`
	ActivePointerDimensionMismatches              int64 `json:"active_pointer_dimension_mismatches"`
	OrphanProblemEmbeddings                       int64 `json:"orphan_problem_embeddings"`
	OrphanModelEmbeddings                         int64 `json:"orphan_model_embeddings"`
	EmbeddingDimensionMismatches                  int64 `json:"embedding_dimension_mismatches"`
	ActiveStatementCurrentRows                    int64 `json:"active_statement_current_rows"`
	ActiveStatementDuplicateCurrentRows           int64 `json:"active_statement_duplicate_current_rows"`
	ActiveStatementStaleRows                      int64 `json:"active_statement_stale_rows"`
	PublishedProblems                             int64 `json:"published_problems"`
	PublishedMissingActiveStatementVector         int64 `json:"published_missing_active_statement_vector"`
	LegacyProjectionPresent                       int64 `json:"legacy_projection_present"`
	LegacyProjectionWithoutCurrentStatementVector int64 `json:"legacy_projection_without_current_statement_vector"`
}

func (c EmbeddingIntegrityViolationCounts) BlockingIssues() int64 {
	return c.MissingStatementActivePointer +
		c.MissingSolutionActivePointer +
		c.ActivePointerInactiveModels +
		c.ActivePointerDimensionMismatches +
		c.OrphanProblemEmbeddings +
		c.OrphanModelEmbeddings +
		c.EmbeddingDimensionMismatches +
		c.ActiveStatementDuplicateCurrentRows +
		c.PublishedMissingActiveStatementVector +
		c.LegacyProjectionWithoutCurrentStatementVector
}

// ScanEmbeddingIntegrity checks that the active embedding space is internally
// consistent and that every published problem has a current active statement
// vector. It does not mutate rows; remediation belongs to T11/T10 flows.
func (r *VectorRepository) ScanEmbeddingIntegrity(ctx context.Context) (EmbeddingIntegrityReport, error) {
	if r == nil || r.db == nil {
		return EmbeddingIntegrityReport{}, fmt.Errorf("vector repository database is required")
	}

	pointers, err := r.listActiveEmbeddingPointers(ctx)
	if err != nil {
		return EmbeddingIntegrityReport{}, err
	}

	var counts EmbeddingIntegrityViolationCounts
	if err := r.db.QueryRow(ctx, scanEmbeddingIntegritySQL()).Scan(
		&counts.MissingStatementActivePointer,
		&counts.MissingSolutionActivePointer,
		&counts.ActivePointerInactiveModels,
		&counts.ActivePointerDimensionMismatches,
		&counts.OrphanProblemEmbeddings,
		&counts.OrphanModelEmbeddings,
		&counts.EmbeddingDimensionMismatches,
		&counts.ActiveStatementCurrentRows,
		&counts.ActiveStatementDuplicateCurrentRows,
		&counts.ActiveStatementStaleRows,
		&counts.PublishedProblems,
		&counts.PublishedMissingActiveStatementVector,
		&counts.LegacyProjectionPresent,
		&counts.LegacyProjectionWithoutCurrentStatementVector,
	); err != nil {
		return EmbeddingIntegrityReport{}, fmt.Errorf("scan embedding integrity: %w", err)
	}

	blocking := counts.BlockingIssues()
	decision := EmbeddingIntegrityDecisionGo
	if blocking > 0 {
		decision = EmbeddingIntegrityDecisionNoGo
	}

	coverage := 1.0
	if counts.PublishedProblems > 0 {
		coverage = float64(counts.PublishedProblems-counts.PublishedMissingActiveStatementVector) / float64(counts.PublishedProblems)
	}

	return EmbeddingIntegrityReport{
		GeneratedAt:                time.Now().UTC(),
		Decision:                   decision,
		BlockingIssueCount:         blocking,
		PublishedStatementCoverage: coverage,
		ActivePointers:             pointers,
		Counts:                     counts,
	}, nil
}

func (r *VectorRepository) listActiveEmbeddingPointers(ctx context.Context) ([]EmbeddingActivePointerSummary, error) {
	rows, err := r.db.Query(ctx, `
		SELECT
			ap.embedding_kind,
			ap.model_version_id,
			mv.provider,
			mv.model_id,
			mv.revision,
			mv.weights_hash,
			mv.dimensions,
			ap.expected_dimensions,
			mv.status
		FROM embedding_active_pointers ap
		INNER JOIN embedding_model_versions mv ON mv.id = ap.model_version_id
		ORDER BY ap.embedding_kind`)
	if err != nil {
		return nil, fmt.Errorf("query active embedding pointers: %w", err)
	}
	defer rows.Close()

	var pointers []EmbeddingActivePointerSummary
	for rows.Next() {
		var pointer EmbeddingActivePointerSummary
		if err := rows.Scan(
			&pointer.EmbeddingKind,
			&pointer.ModelVersionID,
			&pointer.Provider,
			&pointer.ModelID,
			&pointer.Revision,
			&pointer.WeightsHash,
			&pointer.Dimensions,
			&pointer.ExpectedDimensions,
			&pointer.Status,
		); err != nil {
			return nil, fmt.Errorf("scan active embedding pointer: %w", err)
		}
		pointers = append(pointers, pointer)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate active embedding pointers: %w", err)
	}
	return pointers, nil
}

func scanEmbeddingIntegritySQL() string {
	return `
WITH active_statement AS (
    SELECT model_version_id
    FROM embedding_active_pointers
    WHERE embedding_kind = 'statement'
),
active_solution AS (
    SELECT model_version_id
    FROM embedding_active_pointers
    WHERE embedding_kind = 'solution'
),
current_problem_content AS (
    SELECT
        id,
        status,
        embedding AS legacy_embedding,
        embedding_statement_content_hash(title, statement, one_line_hint) AS statement_content_hash
    FROM problems
),
active_statement_rows AS (
    SELECT pe.problem_id, pe.content_hash
    FROM problem_embeddings pe
    INNER JOIN active_statement active ON active.model_version_id = pe.model_version_id
    WHERE pe.embedding_kind = 'statement'
),
current_active_statement AS (
    SELECT p.id, COUNT(pe.problem_id) AS row_count
    FROM current_problem_content p
    LEFT JOIN active_statement_rows pe
      ON pe.problem_id = p.id
     AND pe.content_hash = p.statement_content_hash
    GROUP BY p.id
)
SELECT
    (SELECT CASE WHEN COUNT(*) = 1 THEN 0 ELSE 1 END FROM active_statement),
    (SELECT CASE WHEN COUNT(*) = 1 THEN 0 ELSE 1 END FROM active_solution),
    (SELECT COUNT(*)
     FROM embedding_active_pointers ap
     INNER JOIN embedding_model_versions mv ON mv.id = ap.model_version_id
     WHERE mv.status <> 'active'),
    (SELECT COUNT(*)
     FROM embedding_active_pointers ap
     INNER JOIN embedding_model_versions mv ON mv.id = ap.model_version_id
     WHERE ap.expected_dimensions <> mv.dimensions),
    (SELECT COUNT(*)
     FROM problem_embeddings pe
     LEFT JOIN problems p ON p.id = pe.problem_id
     WHERE p.id IS NULL),
    (SELECT COUNT(*)
     FROM problem_embeddings pe
     LEFT JOIN embedding_model_versions mv ON mv.id = pe.model_version_id
     WHERE mv.id IS NULL),
    (SELECT COUNT(*)
     FROM problem_embeddings pe
     INNER JOIN embedding_model_versions mv ON mv.id = pe.model_version_id
     WHERE vector_dims(pe.embedding) <> mv.dimensions),
    (SELECT COUNT(*) FROM current_active_statement WHERE row_count = 1),
    (SELECT COUNT(*) FROM current_active_statement WHERE row_count > 1),
    (SELECT COUNT(*)
     FROM active_statement_rows pe
     INNER JOIN current_problem_content p ON p.id = pe.problem_id
     WHERE pe.content_hash <> p.statement_content_hash),
    (SELECT COUNT(*) FROM current_problem_content WHERE status = 'published'),
    (SELECT COUNT(*)
     FROM current_problem_content p
     INNER JOIN current_active_statement cur ON cur.id = p.id
     WHERE p.status = 'published' AND cur.row_count = 0),
    (SELECT COUNT(*) FROM current_problem_content WHERE legacy_embedding IS NOT NULL),
    (SELECT COUNT(*)
     FROM current_problem_content p
     INNER JOIN current_active_statement cur ON cur.id = p.id
     WHERE p.legacy_embedding IS NOT NULL AND cur.row_count = 0)`
}
