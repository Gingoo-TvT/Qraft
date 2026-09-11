package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	"go.temporal.io/api/serviceerror"
	workflowpb "go.temporal.io/api/workflow/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	temporalmocks "go.temporal.io/sdk/mocks"
)

const (
	testWorkflowID = "problem-generation-test"
	testRunID      = "run-current"
	testToken      = "review-token-current"
)

func TestHandleApproveSignalsExactReviewRun(t *testing.T) {
	tc := temporalmocks.NewClient(t)
	h := NewWorkflowHandler(tc, "default")
	expectReviewQuery(t, tc, waitingReviewQuery())
	tc.On(
		"SignalWorkflow",
		mock.Anything,
		testWorkflowID,
		testRunID,
		domain.ReviewSignalChannelName,
		domain.ReviewSignal{
			Token: testToken,
			Decision: domain.ReviewDecision{
				Approved: true,
				Feedback: "ship it",
			},
		},
	).Return(nil).Once()
	expectReviewAcknowledgement(t, tc, domain.ReviewDecision{
		Approved: true,
		Feedback: "ship it",
	})

	recorder := invokeWorkflowHandler(t, http.MethodPost, "/approve", reviewBody("ship it"), h.HandleApprove)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"run_id":"run-current"`)
}

func TestHandleRejectSignalsExactReviewRun(t *testing.T) {
	tc := temporalmocks.NewClient(t)
	h := NewWorkflowHandler(tc, "default")
	expectReviewQuery(t, tc, waitingReviewQuery())
	tc.On(
		"SignalWorkflow",
		mock.Anything,
		testWorkflowID,
		testRunID,
		domain.ReviewSignalChannelName,
		domain.ReviewSignal{
			Token: testToken,
			Decision: domain.ReviewDecision{
				Approved: false,
				Feedback: "needs correction",
			},
		},
	).Return(nil).Once()
	expectReviewAcknowledgement(t, tc, domain.ReviewDecision{
		Approved: false,
		Feedback: "needs correction",
	})

	recorder := invokeWorkflowHandler(t, http.MethodPost, "/reject", reviewBody("needs correction"), h.HandleReject)
	require.Equal(t, http.StatusOK, recorder.Code)
}

func TestHandleApproveRejectStaleOrConflictingReviewReturns409(t *testing.T) {
	tests := []struct {
		name   string
		body   string
		mutate func(*domain.WorkflowStateQuery)
	}{
		{name: "stale token", body: `{"token":"stale","run_id":"run-current","review_attempt":1}`},
		{name: "stale run", body: `{"token":"review-token-current","run_id":"run-old","review_attempt":1}`},
		{name: "stale attempt", body: `{"token":"review-token-current","run_id":"run-current","review_attempt":2}`},
		{name: "missing reference", body: `{}`},
		{
			name: "not waiting",
			body: reviewBody(""),
			mutate: func(query *domain.WorkflowStateQuery) {
				query.State.Status = domain.WorkflowStatusRunning
			},
		},
		{
			name: "already decided",
			body: reviewBody(""),
			mutate: func(query *domain.WorkflowStateQuery) {
				query.ReviewRequest.Decision = &domain.ReviewDecision{Approved: false, Feedback: "first decision"}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tc := temporalmocks.NewClient(t)
			h := NewWorkflowHandler(tc, "default")
			query := waitingReviewQuery()
			if tt.mutate != nil {
				tt.mutate(&query)
			}
			if tt.name == "stale run" {
				tc.On("DescribeWorkflowExecution", mock.Anything, testWorkflowID, "").
					Return(runningWorkflowDescription(problemGenerationWorkflowType), nil).Once()
				tc.On("QueryWorkflowWithOptions", mock.Anything, reviewAckQueryRequestForRun("run-old")).
					Return(nil, serviceerror.NewNotFound("stale run")).Once()
			} else {
				expectReviewQuery(t, tc, query)
			}

			recorder := invokeWorkflowHandler(t, http.MethodPost, "/approve", tt.body, h.HandleApprove)
			require.Equal(t, http.StatusConflict, recorder.Code)
		})
	}
}

func TestHandleApproveLegacyReviewAllowsTokenlessRequest(t *testing.T) {
	tc := temporalmocks.NewClient(t)
	h := NewWorkflowHandler(tc, "default")
	query := waitingReviewQuery()
	query.ReviewRequest.TokenRequired = false
	query.ReviewRequest.ReviewAttempt = 0
	expectReviewQuery(t, tc, query)
	tc.On(
		"SignalWorkflow",
		mock.Anything,
		testWorkflowID,
		testRunID,
		domain.ReviewSignalChannelName,
		domain.ReviewSignal{Token: testToken, Decision: domain.ReviewDecision{Approved: true}},
	).Return(nil).Once()
	acknowledgement := waitingReviewQuery()
	acknowledgement.State.Status = domain.WorkflowStatusRunning
	acknowledgement.ReviewRequest.TokenRequired = false
	acknowledgement.ReviewRequest.ReviewAttempt = 0
	acknowledgement.ReviewRequest.Decision = &domain.ReviewDecision{Approved: true}
	tc.On("QueryWorkflowWithOptions", mock.Anything, reviewAckQueryRequest()).
		Return(&client.QueryWorkflowWithOptionsResponse{QueryResult: encodedQueryValue(t, acknowledgement)}, nil).Once()

	recorder := invokeWorkflowHandler(t, http.MethodPost, "/approve", "", h.HandleApprove)
	require.Equal(t, http.StatusOK, recorder.Code)
}

func TestHandleApproveConflictingAcknowledgementReturns409(t *testing.T) {
	tc := temporalmocks.NewClient(t)
	h := NewWorkflowHandler(tc, "default")
	expectReviewQuery(t, tc, waitingReviewQuery())
	tc.On(
		"SignalWorkflow",
		mock.Anything,
		testWorkflowID,
		testRunID,
		domain.ReviewSignalChannelName,
		mock.Anything,
	).Return(nil).Once()
	expectReviewAcknowledgement(t, tc, domain.ReviewDecision{
		Approved: false,
		Feedback: "another reviewer rejected it",
	})

	recorder := invokeWorkflowHandler(t, http.MethodPost, "/approve", reviewBody("ship it"), h.HandleApprove)
	require.Equal(t, http.StatusConflict, recorder.Code)
	require.Contains(t, recorder.Body.String(), "conflicts with the accepted decision")
}

func TestHandleApproveMissingAcknowledgementReturns409(t *testing.T) {
	tc := temporalmocks.NewClient(t)
	h := NewWorkflowHandler(tc, "default")
	expectReviewQuery(t, tc, waitingReviewQuery())
	tc.On(
		"SignalWorkflow",
		mock.Anything,
		testWorkflowID,
		testRunID,
		domain.ReviewSignalChannelName,
		mock.Anything,
	).Return(nil).Once()
	tc.On("QueryWorkflowWithOptions", mock.Anything, reviewAckQueryRequest()).
		Return(&client.QueryWorkflowWithOptionsResponse{QueryResult: encodedQueryValue(t, waitingReviewQuery())}, nil).Once()

	recorder := invokeWorkflowHandler(t, http.MethodPost, "/approve", reviewBody("ship it"), h.HandleApprove)
	require.Equal(t, http.StatusConflict, recorder.Code)
	require.Contains(t, recorder.Body.String(), "was not accepted")
}

func TestHandleApproveAcknowledgementQueryErrorsDoNotReturnSuccess(t *testing.T) {
	tests := []struct {
		name       string
		queryError error
		wantStatus int
	}{
		{
			name:       "closed run lookup failure is indeterminate",
			queryError: serviceerror.NewNotFound("workflow no longer available"),
			wantStatus: http.StatusInternalServerError,
		},
		{
			name:       "query failure is internal error",
			queryError: serviceerror.NewUnavailable("query unavailable"),
			wantStatus: http.StatusServiceUnavailable,
		},
		{
			name:       "query deadline is gateway timeout",
			queryError: serviceerror.NewDeadlineExceeded("query deadline"),
			wantStatus: http.StatusGatewayTimeout,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tc := temporalmocks.NewClient(t)
			h := NewWorkflowHandler(tc, "default")
			expectReviewQuery(t, tc, waitingReviewQuery())
			tc.On(
				"SignalWorkflow",
				mock.Anything,
				testWorkflowID,
				testRunID,
				domain.ReviewSignalChannelName,
				mock.Anything,
			).Return(nil).Once()
			tc.On("QueryWorkflowWithOptions", mock.Anything, reviewAckQueryRequest()).
				Return(nil, tt.queryError).Once()

			recorder := invokeWorkflowHandler(t, http.MethodPost, "/approve", reviewBody("ship it"), h.HandleApprove)
			require.Equal(t, tt.wantStatus, recorder.Code)
		})
	}
}

func TestHandleApproveRetryConfirmsDecisionAfterAcknowledgementFailure(t *testing.T) {
	tc := temporalmocks.NewClient(t)
	h := NewWorkflowHandler(tc, "default")
	expectReviewQuery(t, tc, waitingReviewQuery())
	tc.On(
		"SignalWorkflow",
		mock.Anything,
		testWorkflowID,
		testRunID,
		domain.ReviewSignalChannelName,
		mock.Anything,
	).Return(nil).Once()
	tc.On("QueryWorkflowWithOptions", mock.Anything, reviewAckQueryRequest()).
		Return(nil, serviceerror.NewUnavailable("temporary acknowledgement failure")).Once()

	first := invokeWorkflowHandler(t, http.MethodPost, "/approve", reviewBody("ship it"), h.HandleApprove)
	require.Equal(t, http.StatusServiceUnavailable, first.Code)

	tc.On("DescribeWorkflowExecution", mock.Anything, testWorkflowID, "").
		Return(workflowDescription(problemGenerationWorkflowType, enums.WORKFLOW_EXECUTION_STATUS_COMPLETED), nil).Once()
	expectReviewAcknowledgement(t, tc, domain.ReviewDecision{Approved: true, Feedback: "ship it"})

	retry := invokeWorkflowHandler(t, http.MethodPost, "/approve", reviewBody("ship it"), h.HandleApprove)
	require.Equal(t, http.StatusOK, retry.Code)
}

func TestHandleApproveQueryClosedRunReturns409(t *testing.T) {
	tc := temporalmocks.NewClient(t)
	h := NewWorkflowHandler(tc, "default")
	tc.On("DescribeWorkflowExecution", mock.Anything, testWorkflowID, "").
		Return(runningWorkflowDescription(problemGenerationWorkflowType), nil).Once()
	tc.On("QueryWorkflow", mock.Anything, testWorkflowID, testRunID, domain.WorkflowStateQueryName).
		Return(nil, serviceerror.NewNotFound("workflow closed")).Once()

	recorder := invokeWorkflowHandler(t, http.MethodPost, "/approve", reviewBody(""), h.HandleApprove)
	require.Equal(t, http.StatusConflict, recorder.Code)
}

func TestHandleRejectRequiresFeedback(t *testing.T) {
	tc := temporalmocks.NewClient(t)
	h := NewWorkflowHandler(tc, "default")
	recorder := invokeWorkflowHandler(t, http.MethodPost, "/reject", reviewBody(""), h.HandleReject)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
}

func TestHandleApproveRejectMalformedBodyReturns400(t *testing.T) {
	tc := temporalmocks.NewClient(t)
	h := NewWorkflowHandler(tc, "default")
	recorder := invokeWorkflowHandler(t, http.MethodPost, "/approve", `{`, h.HandleApprove)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
}

func TestHandleGetQueriesRunningProblemGenerationState(t *testing.T) {
	tc := temporalmocks.NewClient(t)
	h := NewWorkflowHandler(tc, "default")
	tc.On("DescribeWorkflowExecution", mock.Anything, testWorkflowID, "").
		Return(runningWorkflowDescription(problemGenerationWorkflowType), nil).Once()
	tc.On("QueryWorkflow", mock.Anything, testWorkflowID, testRunID, domain.WorkflowStateQueryName).
		Return(encodedQueryValue(t, waitingReviewQuery()), nil).Once()

	recorder := invokeWorkflowHandler(t, http.MethodGet, "", "", h.HandleGet)
	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Data map[string]interface{} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.Equal(t, string(domain.WorkflowStatusWaitingReview), response.Data["status"])
	require.Equal(t, "Running", response.Data["execution_status"])
	require.NotNil(t, response.Data["review_request"])
}

func TestHandleGetDoesNotQueryOtherRunningWorkflowTypes(t *testing.T) {
	tc := temporalmocks.NewClient(t)
	h := NewWorkflowHandler(tc, "default")
	tc.On("DescribeWorkflowExecution", mock.Anything, testWorkflowID, "").
		Return(runningWorkflowDescription("QuizGenerationWorkflow"), nil).Once()

	recorder := invokeWorkflowHandler(t, http.MethodGet, "", "", h.HandleGet)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"status":"Running"`)
}

func TestHandleGetSurfacesRejectedQuarantinedCompletedResult(t *testing.T) {
	tc := temporalmocks.NewClient(t)
	h := NewWorkflowHandler(tc, "default")
	tc.On("DescribeWorkflowExecution", mock.Anything, testWorkflowID, "").
		Return(workflowDescription(problemGenerationWorkflowType, enums.WORKFLOW_EXECUTION_STATUS_COMPLETED), nil).Once()
	run := temporalmocks.NewWorkflowRun(t)
	run.On("Get", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			state := args.Get(1).(*domain.WorkflowState)
			state.Status = domain.WorkflowStatusRejectedQuarantined
			state.Progress = 100
		}).
		Return(nil).
		Once()
	tc.On("GetWorkflow", mock.Anything, testWorkflowID, "").Return(run).Once()

	recorder := invokeWorkflowHandler(t, http.MethodGet, "", "", h.HandleGet)
	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Data map[string]interface{} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.Equal(t, string(domain.WorkflowStatusRejectedQuarantined), response.Data["status"])
	require.Equal(t, "Completed", response.Data["execution_status"])
}

func TestHandleGetPreservesTemporalStatusForNormallyCompletedResult(t *testing.T) {
	tc := temporalmocks.NewClient(t)
	h := NewWorkflowHandler(tc, "default")
	tc.On("DescribeWorkflowExecution", mock.Anything, testWorkflowID, "").
		Return(workflowDescription(problemGenerationWorkflowType, enums.WORKFLOW_EXECUTION_STATUS_COMPLETED), nil).Once()
	run := temporalmocks.NewWorkflowRun(t)
	run.On("Get", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			state := args.Get(1).(*domain.WorkflowState)
			state.Status = domain.WorkflowStatusCompleted
			state.Progress = 100
		}).
		Return(nil).
		Once()
	tc.On("GetWorkflow", mock.Anything, testWorkflowID, "").Return(run).Once()

	recorder := invokeWorkflowHandler(t, http.MethodGet, "", "", h.HandleGet)
	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Data map[string]interface{} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.Equal(t, "Completed", response.Data["status"])
	require.Equal(t, "Completed", response.Data["execution_status"])
}

func TestHandleRetryResetsFailedWorkflow(t *testing.T) {
	tc := temporalmocks.NewClient(t)
	h := NewWorkflowHandler(tc, "default")
	tc.On("DescribeWorkflowExecution", mock.Anything, testWorkflowID, "").
		Return(workflowDescription(problemGenerationWorkflowType, enums.WORKFLOW_EXECUTION_STATUS_FAILED), nil).
		Once()
	history := temporalmocks.NewHistoryEventIterator(t)
	history.On("HasNext").Return(true).Once()
	history.On("Next").Return(&historypb.HistoryEvent{
		EventId:   4,
		EventType: enums.EVENT_TYPE_WORKFLOW_TASK_COMPLETED,
	}, nil).Once()
	tc.On(
		"GetWorkflowHistory",
		mock.Anything,
		testWorkflowID,
		testRunID,
		false,
		enums.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT,
	).Return(history).Once()
	tc.On("ResetWorkflowExecution", mock.Anything, mock.MatchedBy(func(request *workflowservice.ResetWorkflowExecutionRequest) bool {
		return request != nil &&
			request.Namespace == "default" &&
			request.GetWorkflowExecution().GetWorkflowId() == testWorkflowID &&
			request.GetWorkflowExecution().GetRunId() == testRunID &&
			request.WorkflowTaskFinishEventId == 4 &&
			request.RequestId == workflowRetryRequestID("default", testWorkflowID, testRunID) &&
			request.ResetReapplyType == enums.RESET_REAPPLY_TYPE_NONE
	})).Return(&workflowservice.ResetWorkflowExecutionResponse{RunId: "run-restarted"}, nil).Once()

	recorder := invokeWorkflowHandler(t, http.MethodPost, "/retry", "", h.HandleRetry)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"action":"restarted"`)
	require.Contains(t, recorder.Body.String(), `"old_run_id":"`+testRunID+`"`)
	require.Contains(t, recorder.Body.String(), `"run_id":"run-restarted"`)
}

func TestHandleRetryRejectsRunningAndCompletedWorkflows(t *testing.T) {
	for _, test := range []struct {
		name       string
		status     enums.WorkflowExecutionStatus
		wantDetail string
	}{
		{name: "running", status: enums.WORKFLOW_EXECUTION_STATUS_RUNNING, wantDetail: "still running"},
		{name: "completed", status: enums.WORKFLOW_EXECUTION_STATUS_COMPLETED, wantDetail: "duplicate output"},
	} {
		t.Run(test.name, func(t *testing.T) {
			tc := temporalmocks.NewClient(t)
			h := NewWorkflowHandler(tc, "default")
			tc.On("DescribeWorkflowExecution", mock.Anything, testWorkflowID, "").
				Return(workflowDescription(problemGenerationWorkflowType, test.status), nil).
				Once()

			recorder := invokeWorkflowHandler(t, http.MethodPost, "/retry", "", h.HandleRetry)
			require.Equal(t, http.StatusConflict, recorder.Code)
			require.Contains(t, recorder.Body.String(), test.wantDetail)
		})
	}
}

func TestHandleRetryRejectsWorkflowWithoutResetPoint(t *testing.T) {
	tc := temporalmocks.NewClient(t)
	h := NewWorkflowHandler(tc, "default")
	tc.On("DescribeWorkflowExecution", mock.Anything, testWorkflowID, "").
		Return(workflowDescription(problemGenerationWorkflowType, enums.WORKFLOW_EXECUTION_STATUS_TIMED_OUT), nil).
		Once()
	history := temporalmocks.NewHistoryEventIterator(t)
	history.On("HasNext").Return(true).Once()
	history.On("Next").Return(&historypb.HistoryEvent{
		EventId:   1,
		EventType: enums.EVENT_TYPE_WORKFLOW_EXECUTION_STARTED,
	}, nil).Once()
	history.On("HasNext").Return(false).Once()
	tc.On(
		"GetWorkflowHistory",
		mock.Anything,
		testWorkflowID,
		testRunID,
		false,
		enums.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT,
	).Return(history).Once()

	recorder := invokeWorkflowHandler(t, http.MethodPost, "/retry", "", h.HandleRetry)
	require.Equal(t, http.StatusConflict, recorder.Code)
	require.Contains(t, recorder.Body.String(), "no completed workflow task")
}

func expectReviewQuery(t *testing.T, tc *temporalmocks.Client, query domain.WorkflowStateQuery) {
	t.Helper()
	tc.On("DescribeWorkflowExecution", mock.Anything, testWorkflowID, "").
		Return(runningWorkflowDescription(problemGenerationWorkflowType), nil).Once()
	tc.On("QueryWorkflow", mock.Anything, testWorkflowID, testRunID, domain.WorkflowStateQueryName).
		Return(encodedQueryValue(t, query), nil).Once()
}

func expectReviewAcknowledgement(t *testing.T, tc *temporalmocks.Client, decision domain.ReviewDecision) {
	t.Helper()
	query := waitingReviewQuery()
	if decision.Approved {
		query.State.Status = domain.WorkflowStatusRunning
	} else {
		query.State.Status = domain.WorkflowStatusFailed
	}
	query.ReviewRequest.Decision = &decision
	tc.On("QueryWorkflowWithOptions", mock.Anything, reviewAckQueryRequest()).
		Return(&client.QueryWorkflowWithOptionsResponse{QueryResult: encodedQueryValue(t, query)}, nil).Once()
}

func reviewAckQueryRequest() interface{} {
	return reviewAckQueryRequestForRun(testRunID)
}

func reviewAckQueryRequestForRun(runID string) interface{} {
	return mock.MatchedBy(func(request *client.QueryWorkflowWithOptionsRequest) bool {
		return request != nil &&
			request.WorkflowID == testWorkflowID &&
			request.RunID == runID &&
			request.QueryType == domain.WorkflowStateQueryName &&
			request.QueryRejectCondition == enums.QUERY_REJECT_CONDITION_NONE
	})
}

func runningWorkflowDescription(workflowType string) *workflowservice.DescribeWorkflowExecutionResponse {
	return workflowDescription(workflowType, enums.WORKFLOW_EXECUTION_STATUS_RUNNING)
}

func workflowDescription(
	workflowType string,
	status enums.WorkflowExecutionStatus,
) *workflowservice.DescribeWorkflowExecutionResponse {
	return &workflowservice.DescribeWorkflowExecutionResponse{
		WorkflowExecutionInfo: &workflowpb.WorkflowExecutionInfo{
			Execution: &commonpb.WorkflowExecution{WorkflowId: testWorkflowID, RunId: testRunID},
			Type:      &commonpb.WorkflowType{Name: workflowType},
			Status:    status,
		},
	}
}

func waitingReviewQuery() domain.WorkflowStateQuery {
	return domain.WorkflowStateQuery{
		State: domain.WorkflowState{
			Status:      domain.WorkflowStatusWaitingReview,
			CurrentStep: domain.StepHumanReview,
		},
		ReviewRequest: &domain.ReviewRequest{
			Token:         testToken,
			RunID:         testRunID,
			ReviewAttempt: 1,
			TokenRequired: true,
		},
	}
}

func encodedQueryValue(t *testing.T, query domain.WorkflowStateQuery) converter.EncodedValue {
	t.Helper()
	payloads, err := converter.GetDefaultDataConverter().ToPayloads(query)
	require.NoError(t, err)
	return client.NewValue(payloads)
}

func reviewBody(feedback string) string {
	body, err := json.Marshal(reviewDecisionRequest{
		Token:         testToken,
		RunID:         testRunID,
		ReviewAttempt: 1,
		Feedback:      feedback,
	})
	if err != nil {
		panic(err)
	}
	return string(body)
}

func invokeWorkflowHandler(
	t *testing.T,
	method string,
	suffix string,
	body string,
	handler echo.HandlerFunc,
) *httptest.ResponseRecorder {
	t.Helper()
	e := echo.New()
	request := httptest.NewRequest(method, "/workflows/"+testWorkflowID+suffix, strings.NewReader(body))
	request.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	recorder := httptest.NewRecorder()
	ctx := e.NewContext(request, recorder)
	ctx.SetPath("/workflows/:id" + suffix)
	ctx.SetParamNames("id")
	ctx.SetParamValues(testWorkflowID)
	require.NoError(t, handler(ctx))
	return recorder
}
