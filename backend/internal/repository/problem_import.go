package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type ImportedDuplicateError struct{ ProblemID uuid.UUID }

type ImportedIncompleteError struct {
	ProblemID  uuid.UUID
	WorkflowID string
}

func (e *ImportedIncompleteError) Error() string {
	return "相同原题的导入尚未完整保存，请恢复原任务：" + e.WorkflowID
}

func (e *ImportedDuplicateError) Error() string {
	return "imported statement already exists: " + e.ProblemID.String()
}

// Exact bytes are intentional: do not normalize formulae, code or whitespace.
// Only identifiers leave this lookup; unpublished content is not disclosed.
func (r *ProblemRepository) FindImportedDuplicate(ctx context.Context, statement, hash string) (uuid.UUID, error) {
	var id uuid.UUID
	err := r.db.QueryRow(ctx, `SELECT id FROM problems WHERE statement=$1 OR metadata_json->'import_source'->>'original_sha256'=$2 ORDER BY created_at,id LIMIT 1`, statement, hash).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, nil
	}
	if err == nil {
		err = r.ImportDuplicateReady(ctx, id)
	}
	return id, err
}

// A reserved draft is not proof of a completed import. Never skip a failed
// partial store as though a usable problem/test suite already existed.
func (r *ProblemRepository) ImportDuplicateReady(ctx context.Context, id uuid.UUID) error {
	var incomplete bool
	var workflowID string
	err := r.db.QueryRow(ctx, `SELECT (COALESCE(p.source,'')='qraft_import' AND COALESCE(o.status,'')<>'completed'),COALESCE(p.workflow_id,'') FROM problems p LEFT JOIN workflow_operations o ON o.operation_key=p.metadata_json->>'store_idempotency_key' WHERE p.id=$1`, id).Scan(&incomplete, &workflowID)
	if err != nil {
		return err
	}
	if incomplete {
		return &ImportedIncompleteError{ProblemID: id, WorkflowID: workflowID}
	}
	return nil
}

// A transaction-scoped content lock closes the check/insert race between
// simultaneous batches. It does not lock or change unrelated problem rows.
func (r *ProblemRepository) CreateImportedProblem(ctx context.Context, p *domain.Problem, source domain.ImportSourceEvidence) error {
	if p.Tags == nil {
		p.Tags = []string{}
	}
	tx, e := r.db.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "qraft-import:"+source.OriginalSHA256); e != nil {
		return e
	}
	var existing uuid.UUID
	e = tx.QueryRow(ctx, `SELECT id FROM problems WHERE id<>$1 AND (statement=$2 OR metadata_json->'import_source'->>'original_sha256'=$3) ORDER BY created_at,id LIMIT 1`, p.ID, p.Statement, source.OriginalSHA256).Scan(&existing)
	if e == nil {
		var incomplete bool
		var workflowID string
		if e = tx.QueryRow(ctx, `SELECT (COALESCE(p.source,'')='qraft_import' AND COALESCE(o.status,'')<>'completed'),COALESCE(p.workflow_id,'') FROM problems p LEFT JOIN workflow_operations o ON o.operation_key=p.metadata_json->>'store_idempotency_key' WHERE p.id=$1`, existing).Scan(&incomplete, &workflowID); e != nil {
			return e
		}
		if incomplete {
			return &ImportedIncompleteError{ProblemID: existing, WorkflowID: workflowID}
		}
		return &ImportedDuplicateError{existing}
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		return e
	}
	var serial int
	e = tx.QueryRow(ctx, createProblemQuery, p.ID, p.Title, p.Statement, p.Level, p.Difficulty, p.OneLineHint, p.DetailedSolution, p.Tags, p.TimeLimit, p.MemoryLimit, p.Source, p.Status, nullableString(p.WorkflowID), p.MetadataJSON, p.CreatedAt, p.UpdatedAt).Scan(&serial)
	if e != nil {
		return e
	}
	p.SerialNumber = fmt.Sprint(serial)
	return tx.Commit(ctx)
}

// Update only the reference difficulty, guarded by the statement snapshot.
// The independent calibrated rating tables are not modified by this update.
func (r *ProblemRepository) SetImportedDifficulty(ctx context.Context, id uuid.UUID, statement string, difficulty int, evidence json.RawMessage) error {
	if difficulty < 800 || difficulty > 3500 || difficulty%100 != 0 || !json.Valid(evidence) {
		return fmt.Errorf("invalid imported difficulty estimate")
	}
	tag, err := r.db.Exec(ctx, `UPDATE problems SET difficulty=$3,
 metadata_json=jsonb_set(COALESCE(metadata_json,'{}'::jsonb),'{import_difficulty}',$4::jsonb),updated_at=NOW()
 WHERE id=$1 AND statement=$2`, id, statement, difficulty, evidence)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("problem statement changed before difficulty estimate was saved")
	}
	return nil
}
