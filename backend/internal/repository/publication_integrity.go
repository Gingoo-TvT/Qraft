package repository

import (
	"context"
	"fmt"
	"time"
)

const (
	PublicationIntegrityDecisionGo   = "go"
	PublicationIntegrityDecisionNoGo = "no_go"
)

// PublicationIntegrityReport is the QE::T10 evidence shape for the public
// problem surface. A no_go report means a published row is missing mandatory
// artifacts, provenance policy evidence, or the active statement vector.
type PublicationIntegrityReport struct {
	GeneratedAt        time.Time                           `json:"generated_at"`
	Decision           string                              `json:"decision"`
	BlockingIssueCount int64                               `json:"blocking_issue_count"`
	PublishedCoverage  PublicationIntegrityCoverage        `json:"published_coverage"`
	Counts             PublicationIntegrityViolationCounts `json:"counts"`
}

type PublicationIntegrityCoverage struct {
	PolicyVersion         float64 `json:"policy_version"`
	DefinitionArtifact    float64 `json:"definition_artifact"`
	DetailedSolution      float64 `json:"detailed_solution"`
	MainSolution          float64 `json:"main_solution"`
	BruteSolution         float64 `json:"brute_solution"`
	RunnableTests         float64 `json:"runnable_tests"`
	ActiveStatementVector float64 `json:"active_statement_vector"`
	Freshness             float64 `json:"freshness"`
}

type PublicationIntegrityViolationCounts struct {
	PublishedProblems                         int64 `json:"published_problems"`
	PublishedMissingPublicationPolicyVersion  int64 `json:"published_missing_publication_policy_version"`
	PublishedMissingCurrentDefinitionArtifact int64 `json:"published_missing_current_definition_artifact"`
	PublishedDefinitionDeniedByPolicy         int64 `json:"published_definition_denied_by_policy"`
	PublishedPolicyVersionMismatch            int64 `json:"published_policy_version_mismatch"`
	PublishedMissingDetailedSolution          int64 `json:"published_missing_detailed_solution"`
	PublishedMissingMainSolution              int64 `json:"published_missing_main_solution"`
	PublishedMissingBruteSolution             int64 `json:"published_missing_brute_solution"`
	PublishedMissingRunnableTests             int64 `json:"published_missing_runnable_tests"`
	PublishedMissingActiveStatementVector     int64 `json:"published_missing_active_statement_vector"`
	PublishedDuplicateActiveStatementVector   int64 `json:"published_duplicate_active_statement_vector"`
	PublishedWithQuarantineRecord             int64 `json:"published_with_quarantine_record"`
	PublishedStaleProblem                     int64 `json:"published_stale_problem"`
	PublishedStaleActiveStatementVector       int64 `json:"published_stale_active_statement_vector"`
	PublishedStaleSolutions                   int64 `json:"published_stale_solutions"`
	PublishedStaleTestcases                   int64 `json:"published_stale_testcases"`
}

func (c PublicationIntegrityViolationCounts) BlockingIssues() int64 {
	return c.PublishedMissingPublicationPolicyVersion +
		c.PublishedMissingCurrentDefinitionArtifact +
		c.PublishedDefinitionDeniedByPolicy +
		c.PublishedPolicyVersionMismatch +
		c.PublishedMissingDetailedSolution +
		c.PublishedMissingMainSolution +
		c.PublishedMissingBruteSolution +
		c.PublishedMissingRunnableTests +
		c.PublishedMissingActiveStatementVector +
		c.PublishedDuplicateActiveStatementVector +
		c.PublishedWithQuarantineRecord +
		c.PublishedStaleProblem +
		c.PublishedStaleActiveStatementVector +
		c.PublishedStaleSolutions +
		c.PublishedStaleTestcases
}

func (c PublicationIntegrityViolationCounts) Coverage() PublicationIntegrityCoverage {
	return PublicationIntegrityCoverage{
		PolicyVersion:         coverage(c.PublishedProblems, c.PublishedMissingPublicationPolicyVersion),
		DefinitionArtifact:    coverage(c.PublishedProblems, c.PublishedMissingCurrentDefinitionArtifact),
		DetailedSolution:      coverage(c.PublishedProblems, c.PublishedMissingDetailedSolution),
		MainSolution:          coverage(c.PublishedProblems, c.PublishedMissingMainSolution),
		BruteSolution:         coverage(c.PublishedProblems, c.PublishedMissingBruteSolution),
		RunnableTests:         coverage(c.PublishedProblems, c.PublishedMissingRunnableTests),
		ActiveStatementVector: coverage(c.PublishedProblems, c.PublishedMissingActiveStatementVector),
		Freshness: coverage(c.PublishedProblems,
			c.PublishedStaleProblem+
				c.PublishedStaleActiveStatementVector+
				c.PublishedStaleSolutions+
				c.PublishedStaleTestcases),
	}
}

func coverage(total, missing int64) float64 {
	if total <= 0 {
		return 1.0
	}
	return float64(total-missing) / float64(total)
}

// ScanPublicationIntegrity checks the published surface without mutating rows.
// Remediation belongs to the publication gate or follow-up quarantine/recovery
// flows; this scanner is intentionally evidence-only.
func (r *ProblemRepository) ScanPublicationIntegrity(ctx context.Context) (PublicationIntegrityReport, error) {
	if r == nil || r.db == nil {
		return PublicationIntegrityReport{}, fmt.Errorf("problem repository database is required")
	}

	var counts PublicationIntegrityViolationCounts
	if err := r.db.QueryRow(ctx, scanPublicationIntegritySQL()).Scan(
		&counts.PublishedProblems,
		&counts.PublishedMissingPublicationPolicyVersion,
		&counts.PublishedMissingCurrentDefinitionArtifact,
		&counts.PublishedDefinitionDeniedByPolicy,
		&counts.PublishedPolicyVersionMismatch,
		&counts.PublishedMissingDetailedSolution,
		&counts.PublishedMissingMainSolution,
		&counts.PublishedMissingBruteSolution,
		&counts.PublishedMissingRunnableTests,
		&counts.PublishedMissingActiveStatementVector,
		&counts.PublishedDuplicateActiveStatementVector,
		&counts.PublishedWithQuarantineRecord,
		&counts.PublishedStaleProblem,
		&counts.PublishedStaleActiveStatementVector,
		&counts.PublishedStaleSolutions,
		&counts.PublishedStaleTestcases,
	); err != nil {
		return PublicationIntegrityReport{}, fmt.Errorf("scan publication integrity: %w", err)
	}

	blocking := counts.BlockingIssues()
	decision := PublicationIntegrityDecisionGo
	if blocking > 0 {
		decision = PublicationIntegrityDecisionNoGo
	}
	return PublicationIntegrityReport{
		GeneratedAt:        time.Now().UTC(),
		Decision:           decision,
		BlockingIssueCount: blocking,
		PublishedCoverage:  counts.Coverage(),
		Counts:             counts,
	}, nil
}

func scanPublicationIntegritySQL() string {
	return `
WITH published AS (
    SELECT id, title, statement, one_line_hint, detailed_solution, metadata_json
    FROM problems
    WHERE status = 'published'
),
definition_binding AS (
    SELECT
        p.id AS problem_id,
        binding.artifact_id,
        artifact.policy_version,
        CASE
            WHEN binding.artifact_id IS NULL THEN FALSE
            ELSE provenance_artifact_use_allowed(binding.artifact_id, 'public_release')
        END AS public_release_allowed
    FROM published p
    LEFT JOIN LATERAL (
        SELECT b.artifact_id
        FROM provenance_artifact_bindings b
        WHERE b.subject_type = 'problem'
          AND b.subject_id = p.id::text
          AND b.artifact_role = 'definition'
          AND b.is_current
        ORDER BY b.created_at DESC
        LIMIT 1
    ) binding ON TRUE
    LEFT JOIN provenance_artifacts artifact ON artifact.artifact_id = binding.artifact_id
),
active_statement AS (
    SELECT model_version_id
    FROM embedding_active_pointers
    WHERE embedding_kind = 'statement'
),
active_statement_rows AS (
    SELECT pe.problem_id, pe.content_hash, pe.metadata_json
    FROM problem_embeddings pe
    INNER JOIN active_statement active ON active.model_version_id = pe.model_version_id
    WHERE pe.embedding_kind = 'statement'
),
published_active_statement AS (
    SELECT
        p.id,
        COUNT(pe.problem_id) AS row_count,
        COUNT(pe.problem_id) FILTER (
            WHERE COALESCE(pe.metadata_json ->> 'stale', 'false') = 'true'
        ) AS stale_count
    FROM published p
    LEFT JOIN active_statement_rows pe
      ON pe.problem_id = p.id
     AND pe.content_hash = embedding_statement_content_hash(p.title, p.statement, p.one_line_hint)
    GROUP BY p.id
)
SELECT
    (SELECT COUNT(*) FROM published),
    (SELECT COUNT(*)
     FROM published
     WHERE btrim(COALESCE(metadata_json ->> 'publication_policy_version', '')) = ''),
    (SELECT COUNT(*)
     FROM definition_binding
     WHERE artifact_id IS NULL),
    (SELECT COUNT(*)
     FROM definition_binding
     WHERE artifact_id IS NOT NULL
       AND NOT COALESCE(public_release_allowed, FALSE)),
    (SELECT COUNT(*)
     FROM published p
     INNER JOIN definition_binding b ON b.problem_id = p.id
     WHERE b.artifact_id IS NOT NULL
       AND btrim(COALESCE(p.metadata_json ->> 'publication_policy_version', '')) <> ''
       AND p.metadata_json ->> 'publication_policy_version' IS DISTINCT FROM b.policy_version),
    (SELECT COUNT(*)
     FROM published
     WHERE btrim(COALESCE(detailed_solution, '')) = ''),
    (SELECT COUNT(*)
     FROM published p
     WHERE NOT EXISTS (
         SELECT 1
         FROM solutions s
         WHERE s.problem_id = p.id
           AND s.solution_type = 'main'
           AND s.compile_status = 'success'
           AND btrim(s.source_code) <> ''
     )),
    (SELECT COUNT(*)
     FROM published p
     WHERE NOT EXISTS (
         SELECT 1
         FROM solutions s
         WHERE s.problem_id = p.id
           AND s.solution_type = 'brute'
           AND s.compile_status = 'success'
           AND btrim(s.source_code) <> ''
     )),
    (SELECT COUNT(*)
     FROM published p
     WHERE NOT EXISTS (
         SELECT 1
         FROM testcases tc
         WHERE tc.problem_id = p.id
           AND btrim(tc.input_path) <> ''
           AND btrim(tc.output_path) <> ''
     )),
    (SELECT COUNT(*)
     FROM published p
     INNER JOIN published_active_statement active ON active.id = p.id
     WHERE active.row_count = 0),
    (SELECT COUNT(*)
     FROM published p
     INNER JOIN published_active_statement active ON active.id = p.id
     WHERE active.row_count > 1),
    (SELECT COUNT(*)
     FROM published p
     WHERE EXISTS (
         SELECT 1
         FROM problem_quarantine_records quarantine
         WHERE quarantine.problem_id = p.id
     )),
    (SELECT COUNT(*)
     FROM published
     WHERE COALESCE(metadata_json ->> 'stale', 'false') = 'true'),
    (SELECT COUNT(*)
     FROM published p
     INNER JOIN published_active_statement active ON active.id = p.id
     WHERE active.stale_count > 0),
    (SELECT COUNT(DISTINCT p.id)
     FROM published p
     INNER JOIN solutions s ON s.problem_id = p.id
     WHERE COALESCE(s.metadata_json ->> 'stale', 'false') = 'true'),
    (SELECT COUNT(DISTINCT p.id)
     FROM published p
     INNER JOIN testcases tc ON tc.problem_id = p.id
     WHERE COALESCE(tc.metadata_json ->> 'stale', 'false') = 'true')`
}
