package service

import (
	"context"
	"errors"
	"fmt"
	"github.com/Gingoo-TvT/Qraft/backend/internal/access"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
)

var ErrWorkflowAccessDenied = errors.New("workflow not found")

type WorkflowOwnershipStore interface {
	Owner(context.Context, string) (string, error)
	Reserve(context.Context, string, string) error
}
type WorkflowAccess struct {
	temporal client.Client
	store    WorkflowOwnershipStore
}

func NewWorkflowAccess(raw client.Client, store WorkflowOwnershipStore) *WorkflowAccess {
	return &WorkflowAccess{temporal: raw, store: store}
}

// PersistOwner resolves a workflow's server-side parent chain and records the
// inherited owner for child executions. Workers call this without request
// identity; the owner is always recovered from the durable root reservation.
func (a *WorkflowAccess) PersistOwner(ctx context.Context, id string) error {
	if a == nil || a.store == nil || id == "" {
		return nil
	}
	_, err := a.owner(ctx, id, map[string]bool{})
	return err
}

// CanAccess follows only server-provided Temporal parent links, never caller
// prefixes. Child ownership is saved once resolved so retention cannot remove it.
func (a *WorkflowAccess) CanAccess(ctx context.Context, id string) (bool, error) {
	p, ok := access.FromContext(ctx)
	if !ok || a == nil || a.store == nil || id == "" {
		return false, nil
	}
	if p.IsAdmin() {
		return true, nil
	}
	owner, err := a.owner(ctx, id, map[string]bool{})
	return err == nil && owner != "" && owner == p.UserID, err
}
func (a *WorkflowAccess) owner(ctx context.Context, id string, seen map[string]bool) (string, error) {
	if id == "" || seen[id] || len(seen) >= 32 {
		return "", nil
	}
	seen[id] = true
	owner, err := a.store.Owner(ctx, id)
	if err != nil || owner != "" {
		return owner, err
	}
	if a.temporal == nil {
		return "", nil
	}
	desc, err := a.temporal.DescribeWorkflowExecution(ctx, id, "")
	var missing *serviceerror.NotFound
	if errors.As(err, &missing) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if desc == nil || desc.WorkflowExecutionInfo == nil || desc.WorkflowExecutionInfo.ParentExecution == nil {
		return "", nil
	}
	parent := desc.WorkflowExecutionInfo.ParentExecution.WorkflowId
	owner, err = a.owner(ctx, parent, seen)
	if err != nil || owner == "" {
		return owner, err
	}
	if err = a.store.Reserve(ctx, id, owner); err != nil {
		return "", err
	}
	return owner, nil
}

type ownedWorkflowClient struct {
	client.Client
	permissions *WorkflowAccess
}

func NewOwnedWorkflowClient(raw client.Client, permissions *WorkflowAccess) client.Client {
	return &ownedWorkflowClient{Client: raw, permissions: permissions}
}
func (c *ownedWorkflowClient) ExecuteWorkflow(ctx context.Context, opts client.StartWorkflowOptions, workflow interface{}, args ...interface{}) (client.WorkflowRun, error) {
	p, ok := access.FromContext(ctx)
	if !ok || c.permissions == nil || c.permissions.store == nil || opts.ID == "" {
		return nil, ErrWorkflowAccessDenied
	}
	owner, err := c.permissions.store.Owner(ctx, opts.ID)
	if err != nil {
		return nil, fmt.Errorf("checking workflow ownership: %w", err)
	}
	if owner != "" {
		if owner != p.UserID && !p.IsAdmin() {
			return nil, ErrWorkflowAccessDenied
		}
	} else {
		// Do not let a user claim pre-authentication executions by reusing their ID.
		_, probeErr := c.Client.DescribeWorkflowExecution(ctx, opts.ID, "")
		var missing *serviceerror.NotFound
		if probeErr == nil {
			if !p.IsAdmin() {
				return nil, ErrWorkflowAccessDenied
			}
			// A historical task remains ownerless even when an admin restarts it.
			return c.Client.ExecuteWorkflow(ctx, opts, workflow, args...)
		}
		if !errors.As(probeErr, &missing) {
			return nil, fmt.Errorf("checking existing workflow before ownership reservation: %w", probeErr)
		}
		if err = c.permissions.store.Reserve(ctx, opts.ID, p.UserID); err != nil {
			return nil, fmt.Errorf("reserving workflow owner: %w", err)
		}
	}
	// Keep reservation after uncertain starts; retry cannot transfer ownership.
	return c.Client.ExecuteWorkflow(ctx, opts, workflow, args...)
}

func (c *ownedWorkflowClient) DescribeWorkflowExecution(ctx context.Context, id, run string) (*workflowservice.DescribeWorkflowExecutionResponse, error) {
	allowed, err := c.permissions.CanAccess(ctx, id)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, serviceerror.NewNotFound("workflow not found")
	}
	return c.Client.DescribeWorkflowExecution(ctx, id, run)
}
