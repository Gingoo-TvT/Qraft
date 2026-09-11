package repository

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestCaseRepository provides data access operations for problem test cases
// stored in PostgreSQL.
type TestCaseRepository struct {
	db *pgxpool.Pool
}

// NewTestCaseRepository creates a new TestCaseRepository backed by the given
// connection pool.
func NewTestCaseRepository(db *pgxpool.Pool) *TestCaseRepository {
	return &TestCaseRepository{db: db}
}

// Create inserts a single test case into the database. The test case's ID and
// timestamps must already be populated.
func (r *TestCaseRepository) Create(ctx context.Context, tc *domain.TestCase) error {
	query := `
		INSERT INTO testcases (
			id, problem_id, input_path, output_path,
			description, is_sample, test_index,
			group_id, score,
			created_at
		) VALUES (
			$1, $2, $3, $4,
			$5, $6, $7,
			$8, $9,
			$10
		)`

	_, err := r.db.Exec(ctx, query,
		tc.ID,
		tc.ProblemID,
		tc.InputPath,
		tc.OutputPath,
		tc.Description,
		tc.IsSample,
		tc.TestIndex,
		tc.GroupID,
		tc.Score,
		tc.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("inserting test case %s for problem %s: %w", tc.ID, tc.ProblemID, err)
	}

	return nil
}

// CreateBatch inserts multiple test cases in a single database round-trip using
// a batch insert. This is significantly more efficient than calling Create in a
// loop when importing test cases from an LLM generation step.
//
// The operation is atomic: either all test cases are inserted or none are.
func (r *TestCaseRepository) CreateBatch(ctx context.Context, testCases []*domain.TestCase) error {
	if len(testCases) == 0 {
		return nil
	}

	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("beginning transaction for batch insert: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback on committed tx is a no-op

	query := `
		INSERT INTO testcases (
			id, problem_id, input_path, output_path,
			description, is_sample, test_index,
			group_id, score,
			created_at
		) VALUES (
			$1, $2, $3, $4,
			$5, $6, $7,
			$8, $9,
			$10
		)`

	for i, tc := range testCases {
		_, err := tx.Exec(ctx, query,
			tc.ID,
			tc.ProblemID,
			tc.InputPath,
			tc.OutputPath,
			tc.Description,
			tc.IsSample,
			tc.TestIndex,
			tc.GroupID,
			tc.Score,
			tc.CreatedAt,
		)
		if err != nil {
			return fmt.Errorf("inserting test case %d (%s) in batch: %w", i, tc.ID, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("committing batch insert of %d test cases: %w", len(testCases), err)
	}

	return nil
}

// CreateBatchIdempotent inserts a deterministic activity batch. Re-delivering
// the same batch has no effect; callers must ensure IDs and payload identity
// were derived from a stable idempotency key before using this method.
func (r *TestCaseRepository) CreateBatchIdempotent(ctx context.Context, testCases []*domain.TestCase) error {
	if len(testCases) == 0 {
		return nil
	}

	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("beginning transaction for idempotent batch insert: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback on committed tx is a no-op

	query := `
		INSERT INTO testcases (
			id, problem_id, input_path, output_path,
			description, is_sample, test_index,
			group_id, score,
			created_at
		) VALUES (
			$1, $2, $3, $4,
			$5, $6, $7,
			$8, $9,
			$10
		)
		ON CONFLICT (problem_id, test_index) DO NOTHING`

	for i, tc := range testCases {
		if _, err := tx.Exec(ctx, query,
			tc.ID,
			tc.ProblemID,
			tc.InputPath,
			tc.OutputPath,
			tc.Description,
			tc.IsSample,
			tc.TestIndex,
			tc.GroupID,
			tc.Score,
			tc.CreatedAt,
		); err != nil {
			return fmt.Errorf("inserting idempotent test case %d (%s): %w", i, tc.ID, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("committing idempotent batch insert of %d test cases: %w", len(testCases), err)
	}
	return nil
}

// GetByProblemID retrieves all test cases for a given problem, ordered by
// sort_order ascending. Sample test cases (is_sample = true) appear first
// within the same sort_order.
func (r *TestCaseRepository) GetByProblemID(ctx context.Context, problemID uuid.UUID) ([]*domain.TestCase, error) {
	query := `
		SELECT
			id, problem_id, input_path, output_path,
			description, is_sample, test_index,
			group_id, score,
			created_at
		FROM testcases
		WHERE problem_id = $1
		ORDER BY test_index ASC, is_sample DESC`

	rows, err := r.db.Query(ctx, query, problemID)
	if err != nil {
		return nil, fmt.Errorf("querying test cases for problem %s: %w", problemID, err)
	}
	defer rows.Close()

	var testCases []*domain.TestCase
	for rows.Next() {
		tc := &domain.TestCase{}
		if err := rows.Scan(
			&tc.ID,
			&tc.ProblemID,
			&tc.InputPath,
			&tc.OutputPath,
			&tc.Description,
			&tc.IsSample,
			&tc.TestIndex,
			&tc.GroupID,
			&tc.Score,
			&tc.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning test case row: %w", err)
		}
		testCases = append(testCases, tc)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating test case rows: %w", err)
	}

	return testCases, nil
}

// GetByID retrieves a single test case by its unique identifier. Returns
// sql.ErrNoRows wrapped in a descriptive error if no matching test case exists.
func (r *TestCaseRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.TestCase, error) {
	query := `
		SELECT
			id, problem_id, input_path, output_path,
			description, is_sample, test_index,
			group_id, score,
			created_at
		FROM testcases
		WHERE id = $1`

	tc := &domain.TestCase{}
	err := r.db.QueryRow(ctx, query, id).Scan(
		&tc.ID,
		&tc.ProblemID,
		&tc.InputPath,
		&tc.OutputPath,
		&tc.Description,
		&tc.IsSample,
		&tc.TestIndex,
		&tc.GroupID,
		&tc.Score,
		&tc.CreatedAt,
	)
	if err != nil {
		if err.Error() == "no rows in result set" {
			return nil, fmt.Errorf("test case %s not found: %w", id, sql.ErrNoRows)
		}
		return nil, fmt.Errorf("querying test case %s: %w", id, err)
	}

	return tc, nil
}

// DeleteByProblemID removes all test cases belonging to a given problem. This
// is typically called before regenerating test cases or when deleting a problem.
// It is not an error if no test cases exist for the given problem.
func (r *TestCaseRepository) DeleteByProblemID(ctx context.Context, problemID uuid.UUID) error {
	query := `DELETE FROM testcases WHERE problem_id = $1`

	_, err := r.db.Exec(ctx, query, problemID)
	if err != nil {
		return fmt.Errorf("deleting test cases for problem %s: %w", problemID, err)
	}

	return nil
}
