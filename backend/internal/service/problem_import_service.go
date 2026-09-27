package service

import (
	"context"
	"errors"
	"fmt"
	"github.com/Gingoo-TvT/Qraft/backend/internal/access"
	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/google/uuid"
	enums "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"strings"
	"time"
)

type ProblemImportService struct {
	temporal    client.Client
	queue       string
	resolve     ProblemSetProviderResolver
	permissions *WorkflowAccess
}

func NewProblemImportService(t client.Client, queue string, resolve ProblemSetProviderResolver, permissions *WorkflowAccess) *ProblemImportService {
	return &ProblemImportService{t, queue, resolve, permissions}
}
func (s *ProblemImportService) Start(ctx context.Context, req domain.ProblemImportRequest) (string, error) {
	if e := req.Normalize(); e != nil {
		return "", fmt.Errorf("validation: %w", e)
	}
	if s == nil || s.temporal == nil || s.resolve == nil || s.queue == "" {
		return "", fmt.Errorf("import service unavailable")
	}
	actor := "local"
	if p, ok := access.FromContext(ctx); ok {
		actor = p.UserID
	} else if s.permissions != nil {
		return "", ErrWorkflowAccessDenied
	}
	providers, e := s.resolve(ctx)
	if e != nil {
		return "", e
	}
	if providers == nil {
		return "", fmt.Errorf("import model configuration unavailable")
	}
	if e = providers.Validate(); e != nil {
		return "", e
	}
	id := "problem-import-" + uuid.NewString()
	_, e = s.temporal.ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: id, TaskQueue: s.queue, WorkflowExecutionTimeout: 24 * time.Hour, WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE}, "ProblemImportWorkflow", domain.ProblemImportInput{Request: req, ProviderConfig: providers, OwnerUserID: actor, DirectDifficulty: true})
	if e != nil {
		return "", fmt.Errorf("starting import: %w", e)
	}
	return id, nil
}
func (s *ProblemImportService) Status(ctx context.Context, id string) (*domain.ProblemImportState, error) {
	if s == nil || s.temporal == nil {
		return nil, fmt.Errorf("import service unavailable")
	}
	if !strings.HasPrefix(id, "problem-import-") || len(id) > 100 {
		return nil, ErrNotFound
	}
	if s.permissions != nil {
		ok, e := s.permissions.CanAccess(ctx, id)
		if e != nil {
			return nil, e
		}
		if !ok {
			return nil, ErrNotFound
		}
	}
	desc, e := s.temporal.DescribeWorkflowExecution(ctx, id, "")
	if e != nil {
		return nil, ErrNotFound
	}
	if desc.WorkflowExecutionInfo == nil || desc.WorkflowExecutionInfo.Type == nil || desc.WorkflowExecutionInfo.Type.Name != "ProblemImportWorkflow" {
		return nil, ErrNotFound
	}
	var state domain.ProblemImportState
	var qerr error
	if desc.WorkflowExecutionInfo.Status == enums.WORKFLOW_EXECUTION_STATUS_COMPLETED {
		// A completed batch has a durable result and must remain readable after
		// its worker is retired. Pin the run described above to avoid a restart race.
		qerr = s.temporal.GetWorkflow(ctx, id, desc.WorkflowExecutionInfo.GetExecution().GetRunId()).Get(ctx, &state)
	} else {
		var value converter.EncodedValue
		value, qerr = s.temporal.QueryWorkflow(ctx, id, "", domain.ProblemImportQuery)
		if qerr == nil {
			qerr = value.Get(&state)
		}
	}
	if qerr != nil {
		return nil, fmt.Errorf("import status unavailable: %w", qerr)
	}
	switch desc.WorkflowExecutionInfo.Status {
	case enums.WORKFLOW_EXECUTION_STATUS_CANCELED:
		state.Status = "cancelled"
	case enums.WORKFLOW_EXECUTION_STATUS_FAILED, enums.WORKFLOW_EXECUTION_STATUS_TERMINATED, enums.WORKFLOW_EXECUTION_STATUS_TIMED_OUT:
		state.Status = "failed"
	}
	state.Recount()
	return &state, nil
}

// Batch imports can run for a day. Pin credentials for that bounded lifetime
// without changing the shorter default used by interactive operations.
type importRuntimeKeys interface {
	PutWithTTL(context.Context, string, time.Duration) (string, error)
}
type importKeyLifetime struct{ keys importRuntimeKeys }

func (k importKeyLifetime) Put(ctx context.Context, key string) (string, error) {
	return k.keys.PutWithTTL(ctx, key, 24*time.Hour+5*time.Minute)
}
func NewProblemImportProviderResolver(settings setSavedProviderSettings, keys importRuntimeKeys) ProblemSetProviderResolver {
	return NewProblemSetProviderResolver(settings, importKeyLifetime{keys})
}

var ErrImportStillRunning = errors.New("import is still running")

// Resume only a terminal batch. Retained rows are read from the authorized
// server-side workflow, never accepted as client-provided problem identities.
func (s *ProblemImportService) Resume(ctx context.Context, id string) (string, error) {
	state, err := s.Status(ctx, id)
	if err != nil {
		return "", err
	}
	if state.Status == "running" || state.Status == "queued" {
		return "", ErrImportStillRunning
	}
	desc, err := s.temporal.DescribeWorkflowExecution(ctx, id, "")
	if err != nil {
		return "", err
	}
	runID := desc.GetWorkflowExecutionInfo().GetExecution().GetRunId()
	history := s.temporal.GetWorkflowHistory(ctx, id, runID, false, enums.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	if !history.HasNext() {
		return "", fmt.Errorf("import input unavailable")
	}
	first, err := history.Next()
	if err != nil {
		return "", err
	}
	started := first.GetWorkflowExecutionStartedEventAttributes()
	if started == nil {
		return "", fmt.Errorf("import input unavailable")
	}
	var input domain.ProblemImportInput
	if err = converter.GetDefaultDataConverter().FromPayloads(started.Input, &input); err != nil {
		return "", err
	}
	if err = input.Request.Normalize(); err != nil {
		return "", err
	}
	if len(state.Items) != len(input.Request.Items) {
		return "", fmt.Errorf("import result count mismatch")
	}
	if s.resolve == nil {
		return "", fmt.Errorf("import model configuration unavailable")
	}
	providers, err := s.resolve(ctx)
	if err != nil {
		return "", err
	}
	if providers == nil {
		return "", fmt.Errorf("import model configuration unavailable")
	}
	if err = providers.Validate(); err != nil {
		return "", err
	}
	input.ProviderConfig = providers
	input.DirectDifficulty = true
	input.ResumeItems = state.Items
	if input.CollectionWorkflowID == "" {
		input.CollectionWorkflowID = id
	}
	for i, item := range input.ResumeItems {
		if item.ItemID != input.Request.Items[i].ItemID {
			return "", fmt.Errorf("import result identity mismatch")
		}
		// A child may finish before its parent records the completion event.
		// Reconcile that durable result before deciding to regenerate this row.
		if input.Request.Mode == "preserve_statement" && item.ProblemID == "" && item.Status != "skipped_duplicate" && item.WorkflowID == fmt.Sprintf("%s-item-%d", id, i+1) {
			child, describeErr := s.temporal.DescribeWorkflowExecution(ctx, item.WorkflowID, "")
			var missing *serviceerror.NotFound
			if describeErr != nil && !errors.As(describeErr, &missing) {
				return "", describeErr
			}
			if describeErr == nil && child.GetWorkflowExecutionInfo().GetType().GetName() == "ImportedProblemWorkflow" {
				info := child.GetWorkflowExecutionInfo()
				if info.GetStatus() == enums.WORKFLOW_EXECUTION_STATUS_RUNNING {
					return "", ErrImportStillRunning
				}
				if info.GetStatus() == enums.WORKFLOW_EXECUTION_STATUS_COMPLETED {
					var completed domain.ProblemImportItemResult
					if err = s.temporal.GetWorkflow(ctx, item.WorkflowID, info.GetExecution().GetRunId()).Get(ctx, &completed); err != nil {
						return "", err
					}
					if completed.ItemID != item.ItemID || completed.WorkflowID != item.WorkflowID {
						return "", fmt.Errorf("completed import result identity mismatch")
					}
					item = completed
					input.ResumeItems[i] = completed
				}
			}
		}
		if (item.Status == "imported" || item.Status == "assessment_failed") && item.ProblemID != "" {
			if _, err = uuid.Parse(item.ProblemID); err != nil {
				return "", fmt.Errorf("retained problem identity invalid")
			}
		} else if item.Status != "skipped_duplicate" {
			input.ResumeItems[i] = domain.ProblemImportItemResult{}
		}
	}
	nextID := "problem-import-" + uuid.NewSHA1(uuid.NameSpaceURL, []byte("qraft-import-resume:"+id+":"+runID)).String()
	_, err = s.temporal.ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: nextID, TaskQueue: s.queue, WorkflowExecutionTimeout: 24 * time.Hour, WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE}, "ProblemImportWorkflow", input)
	var exists *serviceerror.WorkflowExecutionAlreadyStarted
	if err != nil && !errors.As(err, &exists) {
		return "", err
	}
	return nextID, nil
}
