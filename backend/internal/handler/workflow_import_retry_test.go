package handler

import (
	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	"go.temporal.io/api/workflowservice/v1"
	temporalmocks "go.temporal.io/sdk/mocks"
	"net/http"
	"testing"
)

func importTask(id int64, kind enums.EventType) *historypb.HistoryEvent {
	return &historypb.HistoryEvent{EventId: id, EventType: kind}
}
func importScheduled(id, taskID int64, name string) *historypb.HistoryEvent {
	return &historypb.HistoryEvent{EventId: id, EventType: enums.EVENT_TYPE_ACTIVITY_TASK_SCHEDULED, Attributes: &historypb.HistoryEvent_ActivityTaskScheduledEventAttributes{ActivityTaskScheduledEventAttributes: &historypb.ActivityTaskScheduledEventAttributes{ActivityType: &commonpb.ActivityType{Name: name}, WorkflowTaskCompletedEventId: taskID}}}
}
func importCompleted(id, scheduled int64) *historypb.HistoryEvent {
	return &historypb.HistoryEvent{EventId: id, EventType: enums.EVENT_TYPE_ACTIVITY_TASK_COMPLETED, Attributes: &historypb.HistoryEvent_ActivityTaskCompletedEventAttributes{ActivityTaskCompletedEventAttributes: &historypb.ActivityTaskCompletedEventAttributes{ScheduledEventId: scheduled}}}
}

func TestImportedProblemResetPointPreservesGeneratedArtifacts(t *testing.T) {
	first := importTask(4, enums.EVENT_TYPE_WORKFLOW_TASK_COMPLETED)
	prepare := importScheduled(5, 4, "PrepareImportedStatementActivity")
	prepared := importCompleted(6, 5)
	storeTask := importTask(10, enums.EVENT_TYPE_WORKFLOW_TASK_COMPLETED)
	store := importScheduled(11, 10, "StoreProblemActivity")
	tests := []struct {
		name   string
		events []*historypb.HistoryEvent
		want   int64
		bad    bool
	}{
		{"partial store failure", []*historypb.HistoryEvent{first, prepare, prepared, storeTask, store, importTask(12, enums.EVENT_TYPE_ACTIVITY_TASK_FAILED), importTask(15, enums.EVENT_TYPE_WORKFLOW_TASK_COMPLETED)}, 10, false},
		{"cancelled while store running", []*historypb.HistoryEvent{first, prepare, prepared, storeTask, store, importTask(12, enums.EVENT_TYPE_WORKFLOW_EXECUTION_CANCELED)}, 10, false},
		{"store completed before later workflow failure", []*historypb.HistoryEvent{first, prepare, prepared, storeTask, store, importCompleted(12, 11), importTask(14, enums.EVENT_TYPE_WORKFLOW_TASK_STARTED), importTask(15, enums.EVENT_TYPE_WORKFLOW_TASK_FAILED)}, 14, false},
		{"completed store without later safe point", []*historypb.HistoryEvent{first, storeTask, store, importCompleted(12, 11)}, 0, true},
		{"unknown store scheduling task must not restart from beginning", []*historypb.HistoryEvent{first, importScheduled(11, 9, "StoreProblemActivity")}, 0, true},
		{"program failure retries last activity not original statement", []*historypb.HistoryEvent{first, prepare, prepared, storeTask, importScheduled(11, 10, "GenerateSolutionActivity")}, 10, false},
		{"no activities uses first task", []*historypb.HistoryEvent{first}, 4, false},
		{"no safe task", []*historypb.HistoryEvent{importTask(1, enums.EVENT_TYPE_WORKFLOW_EXECUTION_STARTED)}, 0, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, e := importedProblemResetPoint(tc.events)
			if tc.bad {
				require.Error(t, e)
				require.Zero(t, got)
			} else {
				require.NoError(t, e)
				require.Equal(t, tc.want, got)
			}
		})
	}
}
func TestHandleRetryImportedProblemResetsStoreSchedulingTask(t *testing.T) {
	tc := temporalmocks.NewClient(t)
	h := NewWorkflowHandler(tc, "default")
	tc.On("DescribeWorkflowExecution", mock.Anything, testWorkflowID, "").Return(workflowDescription("ImportedProblemWorkflow", enums.WORKFLOW_EXECUTION_STATUS_FAILED), nil).Once()
	events := []*historypb.HistoryEvent{importTask(4, enums.EVENT_TYPE_WORKFLOW_TASK_COMPLETED), importScheduled(5, 4, "GenerateSolutionActivity"), importCompleted(8, 5), importTask(10, enums.EVENT_TYPE_WORKFLOW_TASK_COMPLETED), importScheduled(11, 10, "StoreProblemActivity"), importTask(15, enums.EVENT_TYPE_WORKFLOW_TASK_COMPLETED)}
	history := temporalmocks.NewHistoryEventIterator(t)
	for _, event := range events {
		history.On("HasNext").Return(true).Once()
		history.On("Next").Return(event, nil).Once()
	}
	history.On("HasNext").Return(false).Once()
	tc.On("GetWorkflowHistory", mock.Anything, testWorkflowID, testRunID, false, enums.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT).Return(history).Once()
	tc.On("ResetWorkflowExecution", mock.Anything, mock.MatchedBy(func(r *workflowservice.ResetWorkflowExecutionRequest) bool {
		return r.WorkflowTaskFinishEventId == 10 && r.GetWorkflowExecution().GetRunId() == testRunID && r.ResetReapplyType == enums.RESET_REAPPLY_TYPE_NONE
	})).Return(&workflowservice.ResetWorkflowExecutionResponse{RunId: "recovered"}, nil).Once()
	response := invokeWorkflowHandler(t, http.MethodPost, "/retry", "", h.HandleRetry)
	require.Equal(t, http.StatusOK, response.Code)
}
func TestHandleRetryImportedProblemRefusesUnsafeCompletedStoreReset(t *testing.T) {
	tc := temporalmocks.NewClient(t)
	h := NewWorkflowHandler(tc, "default")
	tc.On("DescribeWorkflowExecution", mock.Anything, testWorkflowID, "").Return(workflowDescription("ImportedProblemWorkflow", enums.WORKFLOW_EXECUTION_STATUS_FAILED), nil).Once()
	events := []*historypb.HistoryEvent{importTask(4, enums.EVENT_TYPE_WORKFLOW_TASK_COMPLETED), importScheduled(5, 4, "StoreProblemActivity"), importCompleted(6, 5)}
	history := temporalmocks.NewHistoryEventIterator(t)
	for _, event := range events {
		history.On("HasNext").Return(true).Once()
		history.On("Next").Return(event, nil).Once()
	}
	history.On("HasNext").Return(false).Once()
	tc.On("GetWorkflowHistory", mock.Anything, testWorkflowID, testRunID, false, enums.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT).Return(history).Once()
	response := invokeWorkflowHandler(t, http.MethodPost, "/retry", "", h.HandleRetry)
	require.Equal(t, http.StatusConflict, response.Code)
	tc.AssertNotCalled(t, "ResetWorkflowExecution", mock.Anything, mock.Anything)
}
func TestHandleGetImportedProblemExposesRunningState(t *testing.T) {
	tc := temporalmocks.NewClient(t)
	h := NewWorkflowHandler(tc, "default")
	tc.On("DescribeWorkflowExecution", mock.Anything, testWorkflowID, "").Return(runningWorkflowDescription("ImportedProblemWorkflow"), nil).Once()
	query := domain.WorkflowStateQuery{State: domain.WorkflowState{Status: domain.WorkflowStatusRunning, CurrentStep: domain.StepStore, ProblemID: "saved-problem"}}
	tc.On("QueryWorkflow", mock.Anything, testWorkflowID, testRunID, domain.WorkflowStateQueryName).Return(encodedQueryValue(t, query), nil).Once()
	response := invokeWorkflowHandler(t, http.MethodGet, "", "", h.HandleGet)
	require.Equal(t, http.StatusOK, response.Code)
	require.Contains(t, response.Body.String(), `"problem_id":"saved-problem"`)
	require.Contains(t, response.Body.String(), `"current_step":"store"`)
}
func TestHandleGetImportedProblemExposesPersistedResult(t *testing.T) {
	tc := temporalmocks.NewClient(t)
	h := NewWorkflowHandler(tc, "default")
	tc.On("DescribeWorkflowExecution", mock.Anything, testWorkflowID, "").Return(workflowDescription("ImportedProblemWorkflow", enums.WORKFLOW_EXECUTION_STATUS_COMPLETED), nil).Once()
	run := temporalmocks.NewWorkflowRun(t)
	run.On("Get", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		*args.Get(1).(*domain.ProblemImportItemResult) = domain.ProblemImportItemResult{Status: "imported", ProblemID: "saved-problem", StatementChanged: true, ClarificationReason: "explicit ambiguity"}
	}).Return(nil).Once()
	tc.On("GetWorkflow", mock.Anything, testWorkflowID, "").Return(run).Once()
	response := invokeWorkflowHandler(t, http.MethodGet, "", "", h.HandleGet)
	require.Equal(t, http.StatusOK, response.Code)
	require.Contains(t, response.Body.String(), `"problem_id":"saved-problem"`)
	require.Contains(t, response.Body.String(), `"import_result"`)
	require.Contains(t, response.Body.String(), `"statement_changed":true`)
}
