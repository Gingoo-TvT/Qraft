package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/rating"
	qraftworkflow "github.com/Gingoo-TvT/Qraft/backend/internal/workflow"
	"github.com/google/uuid"
	enums "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
)

// ErrRatingStartUncertain means the server may have accepted execution even
// though the transport failed. Keep the reserved assessment pending; failing it
// here would race the already running worker. Explicit cancel remains available.
var ErrRatingStartUncertain = errors.New("assessment start outcome is uncertain; refresh its status or cancel before retrying")

type RatingRuntimeService struct {
	temporal client.Client
	store    rating.Store
	queue    string
	resolve  ProblemSetProviderResolver
}

func NewRatingRuntimeService(temporalClient client.Client, store rating.Store, queue string, resolve ProblemSetProviderResolver) *RatingRuntimeService {
	return &RatingRuntimeService{temporal: temporalClient, store: store, queue: queue, resolve: resolve}
}
func (s *RatingRuntimeService) available() error {
	if s == nil || s.temporal == nil || s.store == nil {
		return fmt.Errorf("rating workflow service is unavailable")
	}
	return nil
}
func (s *RatingRuntimeService) Start(ctx context.Context, id uuid.UUID) error {
	if err := s.available(); err != nil {
		return err
	}
	if s.resolve == nil || s.queue == "" {
		return fmt.Errorf("rating workflow configuration is incomplete")
	}
	a, err := s.store.GetAssessment(ctx, id)
	if err != nil {
		return err
	}
	if a.Status != "pending" {
		return rating.ErrConflict
	}
	providers, err := s.resolve(ctx)
	if err != nil || providers == nil {
		return fmt.Errorf("rating model configuration could not be resolved")
	}
	_, err = s.temporal.ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: a.WorkflowID, TaskQueue: s.queue, WorkflowExecutionTimeout: 45 * time.Minute, WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE}, qraftworkflow.RatingWorkflow, rating.WorkflowInput{
		AssessmentID: id, ProblemID: a.ProblemID, SnapshotHash: a.Subject.Hash, BlindA: providers.Statement, BlindB: providers.Verification, Review: providers.Review,
	})
	if err == nil {
		return nil
	}
	var duplicate *serviceerror.WorkflowExecutionAlreadyStarted
	if errors.As(err, &duplicate) {
		return nil
	}
	// A positive Describe receipt proves that the ambiguous start was accepted.
	// Absence or an unavailable Describe is not proof that execution never began.
	probeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	response, describeErr := s.temporal.DescribeWorkflowExecution(probeCtx, a.WorkflowID, "")
	if describeErr == nil && response != nil && response.WorkflowExecutionInfo != nil {
		return nil
	}
	var invalid *serviceerror.InvalidArgument
	var denied *serviceerror.PermissionDenied
	var missingNamespace *serviceerror.NamespaceNotFound
	if errors.As(err, &invalid) || errors.As(err, &denied) || errors.As(err, &missingNamespace) {
		return fmt.Errorf("rating workflow start was rejected; check service configuration")
	}
	return ErrRatingStartUncertain
}
func ratingAssessmentTerminal(status string) bool {
	return status == "completed" || status == "failed" || status == "cancelled"
}
func (s *RatingRuntimeService) persistTerminal(ctx context.Context, a rating.Assessment, status, reason string) error {
	err := s.store.UpdateAssessment(ctx, a.ID, status, "reconciled", nil, reason)
	if errors.Is(err, rating.ErrConflict) {
		current, e := s.store.GetAssessment(ctx, a.ID)
		if e == nil && ratingAssessmentTerminal(current.Status) {
			return nil
		}
	}
	return err
}
func (s *RatingRuntimeService) reconcileResponse(ctx context.Context, a rating.Assessment, response *workflowservice.DescribeWorkflowExecutionResponse) error {
	if response == nil || response.WorkflowExecutionInfo == nil {
		return fmt.Errorf("rating workflow status is unavailable")
	}
	switch response.WorkflowExecutionInfo.Status {
	case enums.WORKFLOW_EXECUTION_STATUS_RUNNING:
		return nil
	case enums.WORKFLOW_EXECUTION_STATUS_CANCELED:
		return s.persistTerminal(ctx, a, "cancelled", "Workflow cancellation was confirmed by the execution service.")
	case enums.WORKFLOW_EXECUTION_STATUS_FAILED, enums.WORKFLOW_EXECUTION_STATUS_TIMED_OUT, enums.WORKFLOW_EXECUTION_STATUS_TERMINATED:
		return s.persistTerminal(ctx, a, "failed", "Workflow ended without a completed assessment; review the service status and retry.")
	case enums.WORKFLOW_EXECUTION_STATUS_COMPLETED:
		// The worker normally commits the report before returning. A completed
		// execution with no database report is an evidence gap, never a fake PASS.
		current, err := s.store.GetAssessment(ctx, a.ID)
		if err != nil {
			return err
		}
		if ratingAssessmentTerminal(current.Status) {
			return nil
		}
		return s.persistTerminal(ctx, current, "failed", "Workflow completed without a persisted assessment report; manual review or a fresh assessment is required.")
	default:
		return nil
	}
}
func (s *RatingRuntimeService) Reconcile(ctx context.Context, id uuid.UUID) error {
	if err := s.available(); err != nil {
		return err
	}
	a, err := s.store.GetAssessment(ctx, id)
	if err != nil {
		return err
	}
	if ratingAssessmentTerminal(a.Status) {
		return nil
	}
	response, err := s.temporal.DescribeWorkflowExecution(ctx, a.WorkflowID, "")
	if err != nil {
		var missing *serviceerror.NotFound
		if errors.As(err, &missing) {
			return nil
		} // May still be between reservation and Start.
		return fmt.Errorf("rating workflow status could not be checked")
	}
	return s.reconcileResponse(ctx, a, response)
}
func (s *RatingRuntimeService) Cancel(ctx context.Context, id uuid.UUID) error {
	if err := s.available(); err != nil {
		return err
	}
	a, err := s.store.GetAssessment(ctx, id)
	if err != nil {
		return err
	}
	if ratingAssessmentTerminal(a.Status) {
		return nil
	}
	err = s.temporal.CancelWorkflow(ctx, a.WorkflowID, "")
	if err == nil {
		return s.persistTerminal(ctx, a, "cancelled", "Cancellation requested by the administrator.")
	}
	response, describeErr := s.temporal.DescribeWorkflowExecution(ctx, a.WorkflowID, "")
	if describeErr == nil {
		if response != nil && response.WorkflowExecutionInfo != nil && response.WorkflowExecutionInfo.Status != enums.WORKFLOW_EXECUTION_STATUS_RUNNING {
			return s.reconcileResponse(ctx, a, response)
		}
		return fmt.Errorf("workflow cancellation was not confirmed; retry later")
	}
	var missing *serviceerror.NotFound
	if errors.As(describeErr, &missing) {
		// Unlike passive polling, the administrator explicitly asked to release this
		// reservation. A racing worker cannot promote the now terminal database row.
		return s.persistTerminal(ctx, a, "cancelled", "Cancelled before an execution could be found.")
	}
	return fmt.Errorf("workflow cancellation status could not be confirmed; no assessment state was changed")
}
