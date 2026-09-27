package repository

import (
	"context"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrWorkflowOwnerConflict = errors.New("workflow belongs to another account")

type WorkflowOwnershipRepository struct{ db *pgxpool.Pool }

func NewWorkflowOwnershipRepository(db *pgxpool.Pool) *WorkflowOwnershipRepository {
	return &WorkflowOwnershipRepository{db: db}
}
func (r *WorkflowOwnershipRepository) Owner(ctx context.Context, workflowID string) (string, error) {
	var owner string
	err := r.db.QueryRow(ctx, "SELECT owner_user_id FROM workflow_ownership WHERE workflow_id=$1", workflowID).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return owner, err
}

// Reserve never changes an existing owner, including an administrator retry.
func (r *WorkflowOwnershipRepository) Reserve(ctx context.Context, workflowID, owner string) error {
	if workflowID == "" || owner == "" {
		return fmt.Errorf("workflow identity and owner are required")
	}
	_, err := r.db.Exec(ctx, "INSERT INTO workflow_ownership(workflow_id,owner_user_id) VALUES($1,$2) ON CONFLICT(workflow_id) DO NOTHING", workflowID, owner)
	if err != nil {
		return err
	}
	actual, err := r.Owner(ctx, workflowID)
	if err != nil {
		return err
	}
	if actual != owner {
		return ErrWorkflowOwnerConflict
	}
	return nil
}
func (r *QuizRepository) WorkflowID(ctx context.Context, id uuid.UUID) (string, error) {
	var value string
	err := r.db.QueryRow(ctx, "SELECT workflow_id FROM quiz_workflow_ownership WHERE quiz_id=$1", id).Scan(&value)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return value, err
}
