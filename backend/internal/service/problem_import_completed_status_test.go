package service

import (
	"context"
	"errors"
	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/api/enums/v1"
	workflowpb "go.temporal.io/api/workflow/v1"
	"go.temporal.io/api/workflowservice/v1"
	temporalmocks "go.temporal.io/sdk/mocks"
	"testing"
)

func TestCompletedImportStatusSurvivesRetiredWorker(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "result readable", true: "durable read fails"}[failed], func(t *testing.T) {
			tc := temporalmocks.NewClient(t)
			id := "problem-import-completed"
			rid := "frozen-completed-run"
			desc := &workflowservice.DescribeWorkflowExecutionResponse{WorkflowExecutionInfo: &workflowpb.WorkflowExecutionInfo{Type: &commonpb.WorkflowType{Name: "ProblemImportWorkflow"}, Execution: &commonpb.WorkflowExecution{WorkflowId: id, RunId: rid}, Status: enums.WORKFLOW_EXECUTION_STATUS_COMPLETED}}
			tc.On("DescribeWorkflowExecution", mock.Anything, id, "").Return(desc, nil).Once()
			run := temporalmocks.NewWorkflowRun(t)
			tc.On("GetWorkflow", mock.Anything, id, rid).Return(run).Once()
			var readErr error
			if failed {
				readErr = errors.New("durable result unavailable")
			}
			run.On("Get", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
				if !failed {
					*args.Get(1).(*domain.ProblemImportState) = domain.ProblemImportState{Status: "completed", Items: []domain.ProblemImportItemResult{{ItemID: "one", Status: "imported", ProblemID: "saved-problem"}}}
				}
			}).Return(readErr).Once()
			state, err := NewProblemImportService(tc, "offline-worker-queue", nil, nil).Status(context.Background(), id)
			if failed {
				require.ErrorContains(t, err, "durable result unavailable")
			} else {
				require.NoError(t, err)
				require.Equal(t, "completed", state.Status)
				require.Equal(t, 1, state.Counts.Imported)
				require.Zero(t, state.Counts.Failed)
			}
			tc.AssertNotCalled(t, "QueryWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
		})
	}
}
