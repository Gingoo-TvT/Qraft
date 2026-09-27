package service

import (
	"context"
	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	workflowpb "go.temporal.io/api/workflow/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	temporalmocks "go.temporal.io/sdk/mocks"
	"testing"
)

type importStateFixture struct{ state domain.ProblemImportState }

func (v importStateFixture) HasValue() bool { return true }
func (v importStateFixture) Get(out interface{}) error {
	*out.(*domain.ProblemImportState) = v.state
	return nil
}
func TestResumeImportPreservesStoredRowsAndRefreshesConfiguration(t *testing.T) {
	t.Run("unfinished child", func(t *testing.T) { testResumeImport(t, false) })
	t.Run("child completed before parent observed result", func(t *testing.T) { testResumeImport(t, true) })
}
func testResumeImport(t *testing.T, completedChild bool) {
	tc := temporalmocks.NewClient(t)
	id := "problem-import-fixture"
	runID := "original-run"
	pid := uuid.New().String()
	desc := &workflowservice.DescribeWorkflowExecutionResponse{WorkflowExecutionInfo: &workflowpb.WorkflowExecutionInfo{Type: &commonpb.WorkflowType{Name: "ProblemImportWorkflow"}, Execution: &commonpb.WorkflowExecution{WorkflowId: id, RunId: runID}, Status: enums.WORKFLOW_EXECUTION_STATUS_CANCELED}}
	tc.On("DescribeWorkflowExecution", mock.Anything, id, "").Return(desc, nil).Twice()
	state := domain.ProblemImportState{Status: "running", Items: []domain.ProblemImportItemResult{{ItemID: "a", Status: "imported", ProblemID: pid}, {ItemID: "b", Status: "running"}}}
	tc.On("QueryWorkflow", mock.Anything, id, "", domain.ProblemImportQuery).Return(importStateFixture{state}, nil).Once()
	input := domain.ProblemImportInput{OwnerUserID: "owner", Request: domain.ProblemImportRequest{Mode: "preserve_statement", Items: []domain.SourceProblem{{ItemID: "a", Title: "A", Statement: "a"}, {ItemID: "b", Title: "B", Statement: "b"}}}}
	secondID := uuid.New().String()
	if completedChild {
		childID := id + "-item-2"
		state.Items[1].WorkflowID = childID
		child := &workflowservice.DescribeWorkflowExecutionResponse{WorkflowExecutionInfo: &workflowpb.WorkflowExecutionInfo{Type: &commonpb.WorkflowType{Name: "ImportedProblemWorkflow"}, Execution: &commonpb.WorkflowExecution{WorkflowId: childID, RunId: "child-run"}, Status: enums.WORKFLOW_EXECUTION_STATUS_COMPLETED}}
		tc.On("DescribeWorkflowExecution", mock.Anything, childID, "").Return(child, nil).Once()
		run := temporalmocks.NewWorkflowRun(t)
		run.On("Get", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
			*args.Get(1).(*domain.ProblemImportItemResult) = domain.ProblemImportItemResult{ItemID: "b", Title: "B", WorkflowID: childID, Status: "imported", ProblemID: secondID}
		}).Return(nil).Once()
		tc.On("GetWorkflow", mock.Anything, childID, "child-run").Return(run).Once()
	}
	payload, err := converter.GetDefaultDataConverter().ToPayloads(input)
	require.NoError(t, err)
	history := temporalmocks.NewHistoryEventIterator(t)
	history.On("HasNext").Return(true).Once()
	history.On("Next").Return(&historypb.HistoryEvent{Attributes: &historypb.HistoryEvent_WorkflowExecutionStartedEventAttributes{WorkflowExecutionStartedEventAttributes: &historypb.WorkflowExecutionStartedEventAttributes{Input: payload}}}, nil).Once()
	tc.On("GetWorkflowHistory", mock.Anything, id, runID, false, enums.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT).Return(history).Once()
	expected := "problem-import-" + uuid.NewSHA1(uuid.NameSpaceURL, []byte("qraft-import-resume:"+id+":"+runID)).String()
	tc.On("ExecuteWorkflow", mock.Anything, mock.MatchedBy(func(o client.StartWorkflowOptions) bool { return o.ID == expected }), "ProblemImportWorkflow", mock.MatchedBy(func(i domain.ProblemImportInput) bool {
		return i.DirectDifficulty && i.CollectionWorkflowID == id && i.OwnerUserID == "owner" && i.ResumeItems[0].ProblemID == pid && ((!completedChild && i.ResumeItems[1].Status == "") || (completedChild && i.ResumeItems[1].Status == "imported" && i.ResumeItems[1].ProblemID == secondID)) && i.ProviderConfig != nil
	})).Return(temporalmocks.NewWorkflowRun(t), nil).Once()
	s := NewProblemImportService(tc, "fixture", func(context.Context) (*domain.ProviderRuntimeConfig, error) {
		return &domain.ProviderRuntimeConfig{}, nil
	}, nil)
	got, err := s.Resume(context.Background(), id)
	require.NoError(t, err)
	require.Equal(t, expected, got)
}
func TestResumeImportRejectsAnotherOwner(t *testing.T) {
	raw := &ownershipTemporal{}
	owners := &memoryWorkflowOwners{owners: map[string]string{"problem-import-fixture": "owner"}}
	s := NewProblemImportService(raw, "fixture", nil, NewWorkflowAccess(raw, owners))
	_, err := s.Resume(ownerContext("someone-else", "member"), "problem-import-fixture")
	require.ErrorIs(t, err, ErrNotFound)
	require.Zero(t, raw.starts)
	require.Zero(t, raw.describes)
}
