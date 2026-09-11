package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/rs/zerolog/log"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
)

// ---------------------------------------------------------------------------
// WorkflowHandler
// ---------------------------------------------------------------------------

// WorkflowHandler exposes HTTP endpoints for managing and monitoring Temporal
// workflows. It provides listing, detail views, SSE event streaming, and
// lifecycle control (approve, reject, retry, cancel).
type WorkflowHandler struct {
	temporalClient client.Client
	namespace      string
}

const (
	problemGenerationWorkflowType = "ProblemGenerationWorkflow"
	problemValidationWorkflowType = "ProblemValidationWorkflow"
	reviewAcknowledgementTimeout  = 10 * time.Second
)

type reviewDecisionRequest struct {
	Token         string `json:"token"`
	RunID         string `json:"run_id"`
	ReviewAttempt int    `json:"review_attempt"`
	Feedback      string `json:"feedback"`
}

// NewWorkflowHandler creates a new WorkflowHandler with the given Temporal
// client.
func NewWorkflowHandler(tc client.Client, namespace string) *WorkflowHandler {
	return &WorkflowHandler{temporalClient: tc, namespace: namespace}
}

// ---------------------------------------------------------------------------
// HandleList: GET /workflows
// ---------------------------------------------------------------------------

// workflowSummary is a lightweight representation of a workflow returned by
// list and detail endpoints.
type workflowSummary struct {
	WorkflowID string `json:"workflow_id"`
	RunID      string `json:"run_id"`
	Status     string `json:"status"`
	StartTime  string `json:"start_time,omitempty"`
	CloseTime  string `json:"close_time,omitempty"`
}

// HandleList returns recent AlgoForge workflows. It supports an
// optional ?status= query parameter to filter by workflow status
// (running, completed, failed, cancelled).
func (h *WorkflowHandler) HandleList(c echo.Context) error {
	ctx := c.Request().Context()

	// Build a visibility query to find AlgoForge workflows.
	query := `(WorkflowType = "ProblemGenerationWorkflow" OR WorkflowType = "ProblemValidationWorkflow" OR WorkflowType = "GPLTBatchGenerationWorkflow" OR WorkflowType = "QuizGenerationWorkflow")`
	if status := c.QueryParam("status"); status != "" {
		query += fmt.Sprintf(` AND ExecutionStatus = "%s"`, mapStatusFilter(status))
	}

	limit := intQueryParam(c, "size", 20)

	request := &workflowservice.ListWorkflowExecutionsRequest{
		Namespace: h.namespace,
		Query:     query,
		PageSize:  int32(limit),
	}

	resp, err := h.temporalClient.ListWorkflow(ctx, request)
	if err != nil {
		log.Error().Err(err).Msg("failed to list workflows")
		return internalError(c, "failed to list workflows")
	}

	summaries := make([]workflowSummary, 0, len(resp.Executions))
	for _, exec := range resp.Executions {
		ws := workflowSummary{
			WorkflowID: exec.Execution.WorkflowId,
			RunID:      exec.Execution.RunId,
			Status:     exec.Status.String(),
		}
		if exec.StartTime != nil {
			ws.StartTime = exec.StartTime.AsTime().Format(time.RFC3339)
		}
		if exec.CloseTime != nil {
			ws.CloseTime = exec.CloseTime.AsTime().Format(time.RFC3339)
		}
		summaries = append(summaries, ws)
	}

	return ok(c, summaries)
}

// ---------------------------------------------------------------------------
// HandleGet: GET /workflows/:id
// ---------------------------------------------------------------------------

// HandleGet returns the details of a single workflow, including its current
// state. The workflow ID is the Temporal workflow ID (not the run ID).
func (h *WorkflowHandler) HandleGet(c echo.Context) error {
	workflowID := c.Param("id")
	if workflowID == "" {
		return badRequest(c, "MISSING_PARAM", "workflow id is required")
	}

	ctx := c.Request().Context()

	desc, err := h.temporalClient.DescribeWorkflowExecution(ctx, workflowID, "")
	if err != nil {
		log.Error().Err(err).Str("workflow_id", workflowID).Msg("failed to describe workflow")
		return notFound(c, "workflow not found")
	}

	info := desc.WorkflowExecutionInfo
	result := map[string]interface{}{
		"workflow_id": info.Execution.WorkflowId,
		"run_id":      info.Execution.RunId,
		"status":      info.Status.String(),
	}

	if info.StartTime != nil {
		result["start_time"] = info.StartTime.AsTime().Format(time.RFC3339)
	}
	if info.CloseTime != nil {
		result["close_time"] = info.CloseTime.AsTime().Format(time.RFC3339)
	}

	if info.Status == enums.WORKFLOW_EXECUTION_STATUS_RUNNING &&
		(info.GetType().GetName() == problemGenerationWorkflowType ||
			info.GetType().GetName() == problemValidationWorkflowType) {
		queryState, err := h.queryWorkflowState(ctx, workflowID, info.Execution.RunId)
		if err != nil {
			// Older validation runs were created before the state query was
			// registered. Keep their basic execution status readable while new
			// runs expose step-level progress.
			if info.GetType().GetName() == problemValidationWorkflowType {
				result["execution_status"] = info.Status.String()
				return ok(c, result)
			}
			log.Error().Err(err).Str("workflow_id", workflowID).Msg("failed to query running workflow state")
			return internalError(c, "failed to query workflow state")
		}
		result["execution_status"] = info.Status.String()
		result["status"] = queryState.State.Status
		result["state"] = queryState.State
		if queryState.ReviewRequest != nil {
			result["review_request"] = queryState.ReviewRequest
		}
	}

	// If the workflow is completed, try to read its result.
	if info.Status == enums.WORKFLOW_EXECUTION_STATUS_COMPLETED {
		run := h.temporalClient.GetWorkflow(ctx, workflowID, "")
		var state domain.WorkflowState
		if err := run.Get(ctx, &state); err == nil {
			result["execution_status"] = info.Status.String()
			if state.Status == domain.WorkflowStatusRejectedQuarantined {
				result["status"] = state.Status
			}
			result["state"] = state
		}
	}

	// If the workflow failed, extract the failure reason from Temporal
	// history so it is visible in the UI.
	if info.Status == enums.WORKFLOW_EXECUTION_STATUS_FAILED ||
		info.Status == enums.WORKFLOW_EXECUTION_STATUS_TIMED_OUT {
		run := h.temporalClient.GetWorkflow(ctx, workflowID, "")
		var state domain.WorkflowState
		if err := run.Get(ctx, &state); err != nil {
			// The Get() call for failed workflows returns an error whose
			// message contains the failure reason.
			result["failure_reason"] = err.Error()
		} else {
			result["state"] = state
		}
	}

	return ok(c, result)
}

// ---------------------------------------------------------------------------
// HandleEvents: GET /workflows/:id/events (SSE)
// ---------------------------------------------------------------------------

// HandleEvents streams workflow history events as Server-Sent Events. The
// connection stays open until the workflow reaches a terminal state or the
// client disconnects. Events are derived from Temporal history so that the
// frontend can display step-level progress.
func (h *WorkflowHandler) HandleEvents(c echo.Context) error {
	workflowID := c.Param("id")
	if workflowID == "" {
		return badRequest(c, "MISSING_PARAM", "workflow id is required")
	}

	ctx := c.Request().Context()

	// Verify the workflow exists.
	_, err := h.temporalClient.DescribeWorkflowExecution(ctx, workflowID, "")
	if err != nil {
		return notFound(c, "workflow not found")
	}

	// Set SSE headers.
	c.Response().Header().Set(echo.HeaderContentType, "text/event-stream")
	c.Response().Header().Set("Cache-Control", "no-cache")
	c.Response().Header().Set("Connection", "keep-alive")
	c.Response().Header().Set("X-Accel-Buffering", "no")
	c.Response().WriteHeader(http.StatusOK)

	flusher, ok := c.Response().Writer.(http.Flusher)
	if !ok {
		return internalError(c, "streaming not supported")
	}

	// scheduledActivities maps ScheduledEventId → activity type name so we
	// can resolve names for completed/failed events.
	scheduledActivities := make(map[int64]string)
	var lastEventID int64

	// sendHistoryEvents fetches the full workflow history and sends any new
	// events (eventId > lastEventID) to the SSE stream. Returns true when
	// the workflow has reached a terminal state.
	sendHistoryEvents := func() (bool, error) {
		iter := h.temporalClient.GetWorkflowHistory(
			ctx, workflowID, "", false,
			enums.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT,
		)

		terminal := false
		for iter.HasNext() {
			he, err := iter.Next()
			if err != nil {
				return false, err
			}

			// Always record scheduled activity names (needed for lookups).
			if he.GetEventType() == enums.EVENT_TYPE_ACTIVITY_TASK_SCHEDULED {
				if attrs := he.GetActivityTaskScheduledEventAttributes(); attrs != nil && attrs.GetActivityType() != nil {
					scheduledActivities[he.GetEventId()] = attrs.GetActivityType().GetName()
				}
			}

			// Skip events we've already sent.
			if he.GetEventId() <= lastEventID {
				continue
			}
			lastEventID = he.GetEventId()

			we := convertHistoryEvent(workflowID, he, scheduledActivities)
			if we == nil {
				continue
			}

			data, _ := json.Marshal(we)
			fmt.Fprintf(c.Response().Writer, "data: %s\n\n", data)
			flusher.Flush()

			if we.Type == "workflow_completed" || we.Type == "workflow_failed" {
				terminal = true
			}
		}

		return terminal, nil
	}

	// Send existing history events immediately.
	terminal, err := sendHistoryEvents()
	if err != nil {
		log.Error().Err(err).Str("workflow_id", workflowID).Msg("failed to read initial workflow history")
	}
	if terminal {
		return nil
	}

	// Poll for new events until terminal or disconnect.
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			terminal, err := sendHistoryEvents()
			if err != nil {
				log.Warn().Err(err).Str("workflow_id", workflowID).Msg("failed to poll workflow history")
				errEvent := sseEvent{
					Type:       "workflow_failed",
					WorkflowID: workflowID,
					Message:    "读取工作流历史失败: " + err.Error(),
					Timestamp:  time.Now().Format(time.RFC3339),
				}
				data, _ := json.Marshal(errEvent)
				fmt.Fprintf(c.Response().Writer, "data: %s\n\n", data)
				flusher.Flush()
				return nil
			}
			if terminal {
				return nil
			}
		}
	}
}

// ---------------------------------------------------------------------------
// HandleApprove: POST /workflows/:id/approve
// ---------------------------------------------------------------------------

// HandleApprove sends an approval signal to a workflow that is waiting for
// human review. An optional feedback field can be included in the request body.
func (h *WorkflowHandler) HandleApprove(c echo.Context) error {
	workflowID := c.Param("id")
	if workflowID == "" {
		return badRequest(c, "MISSING_PARAM", "workflow id is required")
	}

	var body reviewDecisionRequest
	if err := c.Bind(&body); err != nil && !errors.Is(err, io.EOF) {
		return badRequest(c, "INVALID_BODY", "failed to parse request body: "+err.Error())
	}

	decision := domain.ReviewDecision{
		Approved: true,
		Feedback: body.Feedback,
	}
	return h.signalReviewDecision(c, workflowID, body, decision, "approved")
}

// ---------------------------------------------------------------------------
// HandleReject: POST /workflows/:id/reject
// ---------------------------------------------------------------------------

// HandleReject sends a rejection signal to a workflow that is waiting for
// human review. A feedback field explaining the rejection reason is required.
func (h *WorkflowHandler) HandleReject(c echo.Context) error {
	workflowID := c.Param("id")
	if workflowID == "" {
		return badRequest(c, "MISSING_PARAM", "workflow id is required")
	}

	var body reviewDecisionRequest
	if err := c.Bind(&body); err != nil && !errors.Is(err, io.EOF) {
		return badRequest(c, "INVALID_BODY", "failed to parse request body: "+err.Error())
	}
	body.Feedback = strings.TrimSpace(body.Feedback)
	if body.Feedback == "" {
		return badRequest(c, "MISSING_FIELD", "feedback is required when rejecting")
	}

	decision := domain.ReviewDecision{
		Approved: false,
		Feedback: body.Feedback,
	}

	return h.signalReviewDecision(c, workflowID, body, decision, "rejected")
}

func (h *WorkflowHandler) queryWorkflowState(
	ctx context.Context,
	workflowID string,
	runID string,
) (*domain.WorkflowStateQuery, error) {
	value, err := h.temporalClient.QueryWorkflow(
		ctx,
		workflowID,
		runID,
		domain.WorkflowStateQueryName,
	)
	if err != nil {
		return nil, err
	}
	var state domain.WorkflowStateQuery
	if err := value.Get(&state); err != nil {
		return nil, fmt.Errorf("decode workflow state query: %w", err)
	}
	return &state, nil
}

func (h *WorkflowHandler) queryWorkflowStateAllowClosed(
	ctx context.Context,
	workflowID string,
	runID string,
) (*domain.WorkflowStateQuery, error) {
	response, err := h.temporalClient.QueryWorkflowWithOptions(ctx, &client.QueryWorkflowWithOptionsRequest{
		WorkflowID:           workflowID,
		RunID:                runID,
		QueryType:            domain.WorkflowStateQueryName,
		QueryRejectCondition: enums.QUERY_REJECT_CONDITION_NONE,
	})
	if err != nil {
		return nil, err
	}
	if response == nil || response.QueryRejected != nil || response.QueryResult == nil {
		return nil, fmt.Errorf("workflow state query was rejected")
	}
	var state domain.WorkflowStateQuery
	if err := response.QueryResult.Get(&state); err != nil {
		return nil, fmt.Errorf("decode workflow state query: %w", err)
	}
	return &state, nil
}

func (h *WorkflowHandler) signalReviewDecision(
	c echo.Context,
	workflowID string,
	request reviewDecisionRequest,
	decision domain.ReviewDecision,
	action string,
) error {
	ctx := c.Request().Context()
	desc, err := h.temporalClient.DescribeWorkflowExecution(ctx, workflowID, "")
	if err != nil {
		var notFoundErr *serviceerror.NotFound
		if errors.As(err, &notFoundErr) {
			return conflict(c, "workflow is not waiting for review")
		}
		return internalError(c, "failed to read workflow state")
	}
	info := desc.WorkflowExecutionInfo
	if info == nil || info.Execution == nil || info.GetType().GetName() != problemGenerationWorkflowType {
		return conflict(c, "workflow is not waiting for review")
	}

	runID := info.Execution.RunId
	if info.Status != enums.WORKFLOW_EXECUTION_STATUS_RUNNING ||
		(request.RunID != "" && request.RunID != runID) {
		return h.confirmExistingReviewDecision(c, workflowID, request, decision, action)
	}
	queryState, err := h.queryWorkflowState(ctx, workflowID, runID)
	if err != nil {
		var notFoundErr *serviceerror.NotFound
		if errors.As(err, &notFoundErr) {
			return conflict(c, "workflow review state changed")
		}
		log.Error().Err(err).Str("workflow_id", workflowID).Msg("failed to query review state")
		return internalError(c, "failed to query review state")
	}
	review := queryState.ReviewRequest
	if review == nil || review.RunID != runID {
		return conflict(c, "workflow review run is stale")
	}

	if review.TokenRequired {
		if request.Token == "" || request.RunID == "" || request.ReviewAttempt <= 0 {
			return conflict(c, "workflow review reference is required")
		}
	}
	if request.Token != "" && request.Token != review.Token {
		return conflict(c, "workflow review token is stale")
	}
	if request.RunID != "" && request.RunID != review.RunID {
		return conflict(c, "workflow review run is stale")
	}
	if request.ReviewAttempt != 0 && request.ReviewAttempt != review.ReviewAttempt {
		return conflict(c, "workflow review attempt is stale")
	}
	if review.Decision != nil {
		return reviewDecisionAcknowledgementResponse(
			c,
			workflowID,
			runID,
			review.Token,
			review.ReviewAttempt,
			review,
			decision,
			action,
		)
	}
	if queryState.State.Status != domain.WorkflowStatusWaitingReview {
		return conflict(c, "workflow is not waiting for an undecided review")
	}

	err = h.temporalClient.SignalWorkflow(
		ctx,
		workflowID,
		runID,
		domain.ReviewSignalChannelName,
		domain.ReviewSignal{Token: review.Token, Decision: decision},
	)
	if err != nil {
		var notFoundErr *serviceerror.NotFound
		if errors.As(err, &notFoundErr) {
			return conflict(c, "workflow review state changed")
		}
		log.Error().Err(err).Str("workflow_id", workflowID).Msg("failed to signal review decision")
		return internalError(c, "failed to signal review decision")
	}

	ackCtx, cancelAck := context.WithTimeout(ctx, reviewAcknowledgementTimeout)
	defer cancelAck()
	acknowledgedState, err := h.queryWorkflowStateAllowClosed(ackCtx, workflowID, runID)
	if err != nil {
		return reviewAcknowledgementError(c, workflowID, err)
	}
	return reviewDecisionAcknowledgementResponse(
		c,
		workflowID,
		runID,
		review.Token,
		review.ReviewAttempt,
		acknowledgedState.ReviewRequest,
		decision,
		action,
	)
}

func (h *WorkflowHandler) confirmExistingReviewDecision(
	c echo.Context,
	workflowID string,
	request reviewDecisionRequest,
	decision domain.ReviewDecision,
	action string,
) error {
	if request.Token == "" || request.RunID == "" || request.ReviewAttempt <= 0 {
		return conflict(c, "workflow is not waiting for review")
	}
	ctx, cancel := context.WithTimeout(c.Request().Context(), reviewAcknowledgementTimeout)
	defer cancel()
	state, err := h.queryWorkflowStateAllowClosed(ctx, workflowID, request.RunID)
	if err != nil {
		var notFoundErr *serviceerror.NotFound
		if errors.As(err, &notFoundErr) {
			return conflict(c, "workflow review run is stale")
		}
		return reviewAcknowledgementError(c, workflowID, err)
	}
	return reviewDecisionAcknowledgementResponse(
		c,
		workflowID,
		request.RunID,
		request.Token,
		request.ReviewAttempt,
		state.ReviewRequest,
		decision,
		action,
	)
}

func reviewDecisionAcknowledgementResponse(
	c echo.Context,
	workflowID string,
	runID string,
	token string,
	reviewAttempt int,
	review *domain.ReviewRequest,
	decision domain.ReviewDecision,
	action string,
) error {
	if review == nil ||
		review.Token != token ||
		review.RunID != runID ||
		review.ReviewAttempt != reviewAttempt ||
		review.Decision == nil {
		return conflict(c, "workflow review decision was not accepted")
	}
	if *review.Decision != decision {
		return conflict(c, "workflow review decision conflicts with the accepted decision")
	}
	return ok(c, map[string]interface{}{
		"workflow_id":    workflowID,
		"run_id":         runID,
		"review_attempt": reviewAttempt,
		"action":         action,
	})
}

func reviewAcknowledgementError(c echo.Context, workflowID string, err error) error {
	log.Error().Err(err).Str("workflow_id", workflowID).Msg("failed to confirm review decision")
	var unavailableErr *serviceerror.Unavailable
	var deadlineErr *serviceerror.DeadlineExceeded
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &deadlineErr):
		return gatewayTimeout(c, "timed out confirming review decision")
	case errors.As(err, &unavailableErr):
		return serviceUnavailable(c, "review decision confirmation is unavailable")
	default:
		return internalError(c, "failed to confirm review decision")
	}
}

// ---------------------------------------------------------------------------
// HandleRetry: POST /workflows/:id/retry
// ---------------------------------------------------------------------------

// HandleRetry resets an unsuccessful workflow to its first completed workflow
// task. Temporal reuses the original input and creates a real new run.
func (h *WorkflowHandler) HandleRetry(c echo.Context) error {
	workflowID := c.Param("id")
	if workflowID == "" {
		return badRequest(c, "MISSING_PARAM", "workflow id is required")
	}

	ctx := c.Request().Context()

	desc, err := h.temporalClient.DescribeWorkflowExecution(ctx, workflowID, "")
	if err != nil {
		var notFoundErr *serviceerror.NotFound
		if errors.As(err, &notFoundErr) {
			return notFound(c, "workflow not found")
		}
		log.Error().Err(err).Str("workflow_id", workflowID).Msg("failed to describe workflow for retry")
		return internalError(c, "failed to inspect workflow before retry")
	}
	info := desc.GetWorkflowExecutionInfo()
	if info == nil || info.GetExecution() == nil || info.GetExecution().GetRunId() == "" {
		return internalError(c, "workflow description is missing its current run")
	}
	if info.GetStatus() == enums.WORKFLOW_EXECUTION_STATUS_RUNNING {
		return conflict(c, "workflow is still running; cancel it first before retrying")
	}
	if info.GetStatus() == enums.WORKFLOW_EXECUTION_STATUS_COMPLETED {
		return conflict(c, "workflow completed successfully; retry would create duplicate output")
	}
	if !isRetryableWorkflowStatus(info.GetStatus()) {
		return conflict(c, fmt.Sprintf("workflow status %s cannot be retried", info.GetStatus().String()))
	}

	runID := info.GetExecution().GetRunId()
	history := h.temporalClient.GetWorkflowHistory(
		ctx,
		workflowID,
		runID,
		false,
		enums.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT,
	)
	var resetEventID int64
	for history.HasNext() {
		event, historyErr := history.Next()
		if historyErr != nil {
			log.Error().Err(historyErr).
				Str("workflow_id", workflowID).
				Str("run_id", runID).
				Msg("failed to read workflow history for retry")
			return internalError(c, "failed to read workflow history before retry")
		}
		if event.GetEventType() == enums.EVENT_TYPE_WORKFLOW_TASK_COMPLETED {
			resetEventID = event.GetEventId()
			break
		}
	}
	if resetEventID == 0 {
		return conflict(c, "workflow has no completed workflow task to reset to")
	}

	resetResponse, err := h.temporalClient.ResetWorkflowExecution(ctx, &workflowservice.ResetWorkflowExecutionRequest{
		Namespace: h.namespace,
		WorkflowExecution: &commonpb.WorkflowExecution{
			WorkflowId: workflowID,
			RunId:      runID,
		},
		Reason:                    "operator requested retry of unsuccessful workflow",
		WorkflowTaskFinishEventId: resetEventID,
		RequestId:                 workflowRetryRequestID(h.namespace, workflowID, runID),
		ResetReapplyType:          enums.RESET_REAPPLY_TYPE_NONE,
	})
	if err != nil {
		return h.workflowRetryError(c, workflowID, runID, err)
	}
	if resetResponse == nil || resetResponse.GetRunId() == "" {
		return internalError(c, "Temporal reset did not return a new run id")
	}

	return ok(c, map[string]string{
		"workflow_id": workflowID,
		"old_run_id":  runID,
		"run_id":      resetResponse.GetRunId(),
		"action":      "restarted",
	})
}

func workflowRetryRequestID(namespace, workflowID, runID string) string {
	material := namespace + "\x00" + workflowID + "\x00" + runID
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(material)).String()
}

func (h *WorkflowHandler) workflowRetryError(c echo.Context, workflowID, runID string, err error) error {
	log.Error().Err(err).
		Str("workflow_id", workflowID).
		Str("run_id", runID).
		Msg("failed to reset workflow for retry")
	var notFoundErr *serviceerror.NotFound
	var failedPreconditionErr *serviceerror.FailedPrecondition
	var invalidArgumentErr *serviceerror.InvalidArgument
	var unavailableErr *serviceerror.Unavailable
	var deadlineErr *serviceerror.DeadlineExceeded
	switch {
	case errors.As(err, &notFoundErr):
		return notFound(c, "workflow run no longer exists")
	case errors.As(err, &failedPreconditionErr), errors.As(err, &invalidArgumentErr):
		return conflict(c, "workflow can no longer be reset from the selected run")
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &deadlineErr):
		return gatewayTimeout(c, "timed out restarting workflow")
	case errors.As(err, &unavailableErr):
		return serviceUnavailable(c, "workflow retry is temporarily unavailable")
	default:
		return internalError(c, "failed to restart workflow")
	}
}

// ---------------------------------------------------------------------------
// HandleCancel: DELETE /workflows/:id
// ---------------------------------------------------------------------------

// HandleCancel terminates a running workflow.
func (h *WorkflowHandler) HandleCancel(c echo.Context) error {
	workflowID := c.Param("id")
	if workflowID == "" {
		return badRequest(c, "MISSING_PARAM", "workflow id is required")
	}

	err := h.temporalClient.CancelWorkflow(
		c.Request().Context(),
		workflowID,
		"",
	)
	if err != nil {
		log.Error().Err(err).Str("workflow_id", workflowID).Msg("failed to cancel workflow")
		return internalError(c, "failed to cancel workflow: "+err.Error())
	}

	return ok(c, map[string]string{
		"workflow_id": workflowID,
		"action":      "cancelled",
	})
}

// ---------------------------------------------------------------------------
// SSE event helpers
// ---------------------------------------------------------------------------

// sseEvent mirrors the frontend WorkflowEvent interface.
type sseEvent struct {
	Type       string `json:"type"`
	WorkflowID string `json:"workflow_id"`
	StepName   string `json:"step_name,omitempty"`
	Message    string `json:"message,omitempty"`
	Timestamp  string `json:"timestamp"`
}

// activityToStepName maps a Temporal activity type name to a frontend step
// name (matching the domain.WorkflowStep constants).
func activityToStepName(activityType string) string {
	m := map[string]string{
		"SimilarityCheckActivity":          string(domain.StepSimilarityCheck),
		"FetchProblemDataActivity":         "fetch_data",
		"GenerateStatementActivity":        string(domain.StepGenerateStatement),
		"CleanStatementActivity":           string(domain.StepGenerateStatement),
		"PostStatementSimilarityActivity":  string(domain.StepPostStatementSimilarity),
		"GenerateSolutionActivity":         string(domain.StepGenerateSolution),
		"CompileCheckActivity":             string(domain.StepCompileCheck),
		"GenerateTestDataActivity":         string(domain.StepGenerateTestdata),
		"RunSandboxActivity":               string(domain.StepRunSandbox),
		"ValidateActivity":                 string(domain.StepValidate),
		"RefreshEditedProblemActivity":     "edit_refresh",
		"AssessProblemFeasibilityActivity": string(domain.StepAssessFeasibility),
		"LLMReviewActivity":                string(domain.StepLLMReview),
		"StoreProblemActivity":             string(domain.StepStore),
		"GenerateEditorialActivity":        "generate_editorial",
	}
	if step, ok := m[activityType]; ok {
		return step
	}
	// Fallback: convert "FooBarActivity" → "foo_bar"
	name := strings.TrimSuffix(activityType, "Activity")
	var result []rune
	for i, r := range name {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				result = append(result, '_')
			}
			result = append(result, r+32)
		} else {
			result = append(result, r)
		}
	}
	return string(result)
}

// stepLabel returns a Chinese display label for a workflow step.
func stepLabel(stepName string) string {
	m := map[string]string{
		"similarity_check":          "相似度检查",
		"fetch_data":                "加载题目数据",
		"generate_statement":        "生成题目描述",
		"post_statement_similarity": "题面相似度精筛",
		"generate_solution":         "生成解法",
		"compile_check":             "编译检查",
		"generate_testdata":         "生成测试数据",
		"run_sandbox":               "沙箱执行",
		"validate":                  "验证输出",
		"edit_refresh":              "重嵌入并刷新门禁",
		"assess_feasibility":        "可行性评估",
		"llm_review":                "LLM 审核",
		"human_review":              "人工审核",
		"store":                     "存储题目",
		"generate_editorial":        "生成题解",
	}
	if label, ok := m[stepName]; ok {
		return label
	}
	return stepName
}

// convertHistoryEvent converts a Temporal HistoryEvent into an sseEvent for
// the frontend. Returns nil for internal Temporal events that should not be
// displayed (e.g. WorkflowTaskScheduled/Started/Completed).
func convertHistoryEvent(workflowID string, he *historypb.HistoryEvent, scheduledActivities map[int64]string) *sseEvent {
	ts := ""
	if he.GetEventTime() != nil {
		ts = he.GetEventTime().AsTime().Format(time.RFC3339)
	}

	switch he.GetEventType() {
	case enums.EVENT_TYPE_WORKFLOW_EXECUTION_STARTED:
		return &sseEvent{
			Type:       "log",
			WorkflowID: workflowID,
			Message:    "工作流已启动",
			Timestamp:  ts,
		}

	case enums.EVENT_TYPE_ACTIVITY_TASK_SCHEDULED:
		attrs := he.GetActivityTaskScheduledEventAttributes()
		if attrs == nil || attrs.GetActivityType() == nil {
			return nil
		}
		stepName := activityToStepName(attrs.GetActivityType().GetName())
		return &sseEvent{
			Type:       "step_started",
			WorkflowID: workflowID,
			StepName:   stepName,
			Message:    stepLabel(stepName),
			Timestamp:  ts,
		}

	case enums.EVENT_TYPE_ACTIVITY_TASK_COMPLETED:
		attrs := he.GetActivityTaskCompletedEventAttributes()
		if attrs == nil {
			return nil
		}
		actName := scheduledActivities[attrs.GetScheduledEventId()]
		stepName := activityToStepName(actName)
		return &sseEvent{
			Type:       "step_completed",
			WorkflowID: workflowID,
			StepName:   stepName,
			Message:    stepLabel(stepName) + " 完成",
			Timestamp:  ts,
		}

	case enums.EVENT_TYPE_ACTIVITY_TASK_FAILED:
		attrs := he.GetActivityTaskFailedEventAttributes()
		if attrs == nil {
			return nil
		}
		actName := scheduledActivities[attrs.GetScheduledEventId()]
		stepName := activityToStepName(actName)
		msg := stepLabel(stepName) + " 失败"
		if f := attrs.GetFailure(); f != nil && f.GetMessage() != "" {
			msg += ": " + f.GetMessage()
		}
		return &sseEvent{
			Type:       "step_failed",
			WorkflowID: workflowID,
			StepName:   stepName,
			Message:    msg,
			Timestamp:  ts,
		}

	case enums.EVENT_TYPE_ACTIVITY_TASK_TIMED_OUT:
		attrs := he.GetActivityTaskTimedOutEventAttributes()
		if attrs == nil {
			return nil
		}
		actName := scheduledActivities[attrs.GetScheduledEventId()]
		stepName := activityToStepName(actName)
		return &sseEvent{
			Type:       "step_failed",
			WorkflowID: workflowID,
			StepName:   stepName,
			Message:    stepLabel(stepName) + " 超时",
			Timestamp:  ts,
		}

	case enums.EVENT_TYPE_WORKFLOW_EXECUTION_COMPLETED:
		return &sseEvent{
			Type:       "workflow_completed",
			WorkflowID: workflowID,
			Message:    "工作流已完成",
			Timestamp:  ts,
		}

	case enums.EVENT_TYPE_WORKFLOW_EXECUTION_FAILED:
		msg := "工作流失败"
		if attrs := he.GetWorkflowExecutionFailedEventAttributes(); attrs != nil {
			if f := attrs.GetFailure(); f != nil && f.GetMessage() != "" {
				msg += ": " + f.GetMessage()
			}
		}
		return &sseEvent{
			Type:       "workflow_failed",
			WorkflowID: workflowID,
			Message:    msg,
			Timestamp:  ts,
		}

	case enums.EVENT_TYPE_WORKFLOW_EXECUTION_CANCELED,
		enums.EVENT_TYPE_WORKFLOW_EXECUTION_TIMED_OUT,
		enums.EVENT_TYPE_WORKFLOW_EXECUTION_TERMINATED:
		return &sseEvent{
			Type:       "workflow_failed",
			WorkflowID: workflowID,
			Message:    "工作流已终止",
			Timestamp:  ts,
		}
	}

	return nil
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// writeSSE writes a Server-Sent Event to the response.
func writeSSE(c echo.Context, event, data string) {
	fmt.Fprintf(c.Response().Writer, "event: %s\ndata: %s\n\n", event, data)
}

// isRetryableWorkflowStatus permits only unsuccessful terminal executions.
func isRetryableWorkflowStatus(status enums.WorkflowExecutionStatus) bool {
	switch status {
	case enums.WORKFLOW_EXECUTION_STATUS_FAILED,
		enums.WORKFLOW_EXECUTION_STATUS_CANCELED,
		enums.WORKFLOW_EXECUTION_STATUS_TERMINATED,
		enums.WORKFLOW_EXECUTION_STATUS_TIMED_OUT:
		return true
	default:
		return false
	}
}

// mapStatusFilter maps a user-friendly status string to the Temporal
// execution status name used in visibility queries.
func mapStatusFilter(status string) string {
	switch status {
	case "running":
		return "Running"
	case "completed":
		return "Completed"
	case "failed":
		return "Failed"
	case "cancelled":
		return "Canceled"
	case "timed_out":
		return "TimedOut"
	case "terminated":
		return "Terminated"
	default:
		return status
	}
}
