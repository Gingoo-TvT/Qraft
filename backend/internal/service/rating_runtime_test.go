package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/rating"
	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	enums "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	workflowpb "go.temporal.io/api/workflow/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/mocks"
)

type ratingRuntimeStoreStub struct {
	rating.Store
	assessment        rating.Assessment
	updates           int
	conflictCompleted bool
}

func (s *ratingRuntimeStoreStub) GetAssessment(context.Context, uuid.UUID) (rating.Assessment, error) {
	return s.assessment, nil
}
func (s *ratingRuntimeStoreStub) UpdateAssessment(_ context.Context, _ uuid.UUID, status, phase string, report *rating.Report, message string) error {
	if s.conflictCompleted {
		s.assessment.Status = "completed"
		return rating.ErrConflict
	}
	s.updates++
	s.assessment.Status = status
	s.assessment.Phase = phase
	s.assessment.Error = message
	s.assessment.Report = report
	return nil
}
func runtimeFixture() (*RatingRuntimeService, *mocks.Client, *ratingRuntimeStoreStub) {
	id := uuid.New()
	store := &ratingRuntimeStoreStub{assessment: rating.Assessment{ID: id, ProblemID: uuid.New(), WorkflowID: "rating-" + id.String(), Status: "pending", Subject: rating.Subject{Hash: "frozen"}}}
	c := &mocks.Client{}
	s := NewRatingRuntimeService(c, store, "queue", func(context.Context) (*domain.ProviderRuntimeConfig, error) {
		return &domain.ProviderRuntimeConfig{Statement: &domain.LLMRuntimeConfig{Model: "a", APIKeyRef: "runtime:a"}, Verification: &domain.LLMRuntimeConfig{Model: "b", APIKeyRef: "runtime:b"}}, nil
	})
	return s, c, store
}
func describeRating(status enums.WorkflowExecutionStatus) *workflowservice.DescribeWorkflowExecutionResponse {
	return &workflowservice.DescribeWorkflowExecutionResponse{WorkflowExecutionInfo: &workflowpb.WorkflowExecutionInfo{Status: status}}
}
func TestRatingRuntimeConfirmedTerminalReleasesReservation(t *testing.T) {
	for _, status := range []enums.WorkflowExecutionStatus{enums.WORKFLOW_EXECUTION_STATUS_FAILED, enums.WORKFLOW_EXECUTION_STATUS_TIMED_OUT, enums.WORKFLOW_EXECUTION_STATUS_TERMINATED, enums.WORKFLOW_EXECUTION_STATUS_CANCELED, enums.WORKFLOW_EXECUTION_STATUS_COMPLETED} {
		t.Run(status.String(), func(t *testing.T) {
			s, c, store := runtimeFixture()
			c.On("DescribeWorkflowExecution", mock.Anything, store.assessment.WorkflowID, "").Return(describeRating(status), nil).Once()
			if err := s.Reconcile(context.Background(), store.assessment.ID); err != nil {
				t.Fatal(err)
			}
			want := "failed"
			if status == enums.WORKFLOW_EXECUTION_STATUS_CANCELED {
				want = "cancelled"
			}
			if store.assessment.Status != want || store.updates != 1 || store.assessment.Report != nil {
				t.Fatalf("%+v", store)
			}
			if err := s.Reconcile(context.Background(), store.assessment.ID); err != nil {
				t.Fatal(err)
			}
			if store.updates != 1 {
				t.Fatal("terminal reconciliation not idempotent")
			}
			c.AssertExpectations(t)
		})
	}
}
func TestRatingRuntimePollingNeverFailsPendingStartOrRunningWork(t *testing.T) {
	for _, tc := range []struct {
		name   string
		result *workflowservice.DescribeWorkflowExecutionResponse
		err    error
	}{
		{"not yet created", nil, serviceerror.NewNotFound("not started")},
		{"network unavailable", nil, serviceerror.NewUnavailable("synthetic secret")},
		{"running", describeRating(enums.WORKFLOW_EXECUTION_STATUS_RUNNING), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, c, store := runtimeFixture()
			c.On("DescribeWorkflowExecution", mock.Anything, store.assessment.WorkflowID, "").Return(tc.result, tc.err)
			_ = s.Reconcile(context.Background(), store.assessment.ID)
			if store.updates != 0 || store.assessment.Status != "pending" {
				t.Fatal("uncertain status modified database")
			}
		})
	}
}
func TestRatingRuntimeCancelAbsentOrAlreadyEndedExecution(t *testing.T) {
	for _, tc := range []struct {
		name     string
		response *workflowservice.DescribeWorkflowExecutionResponse
		err      error
		want     string
	}{
		{"explicit absent", nil, serviceerror.NewNotFound("no execution"), "cancelled"},
		{"timed out", describeRating(enums.WORKFLOW_EXECUTION_STATUS_TIMED_OUT), nil, "failed"},
		{"unconfirmed", nil, serviceerror.NewUnavailable("network"), "pending"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, c, store := runtimeFixture()
			c.On("CancelWorkflow", mock.Anything, store.assessment.WorkflowID, "").Return(serviceerror.NewNotFound("ended"))
			c.On("DescribeWorkflowExecution", mock.Anything, store.assessment.WorkflowID, "").Return(tc.response, tc.err)
			_ = s.Cancel(context.Background(), store.assessment.ID)
			if store.assessment.Status != tc.want {
				t.Fatal(store.assessment.Status)
			}
		})
	}
}
func TestRatingRuntimeReconcileCannotOverwriteConcurrentCompletion(t *testing.T) {
	s, c, store := runtimeFixture()
	store.conflictCompleted = true
	c.On("DescribeWorkflowExecution", mock.Anything, store.assessment.WorkflowID, "").Return(describeRating(enums.WORKFLOW_EXECUTION_STATUS_FAILED), nil)
	if err := s.Reconcile(context.Background(), store.assessment.ID); err != nil {
		t.Fatal(err)
	}
	if store.assessment.Status != "completed" {
		t.Fatal("overwrote completed report")
	}
}
func TestRatingRuntimeStartsWithFrozenSubjectAndScopedModelKeys(t *testing.T) {
	s, c, store := runtimeFixture()
	c.On("ExecuteWorkflow", mock.Anything, mock.MatchedBy(func(o client.StartWorkflowOptions) bool {
		return o.ID == store.assessment.WorkflowID && o.TaskQueue == "queue" && o.WorkflowIDReusePolicy == enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE
	}), mock.Anything, mock.MatchedBy(func(in rating.WorkflowInput) bool {
		return in.AssessmentID == store.assessment.ID && in.SnapshotHash == "frozen" && in.BlindA.APIKey == "" && in.BlindA.APIKeyRef == "runtime:a" && in.BlindB.Model == "b"
	})).Return(&mocks.WorkflowRun{}, nil)
	if err := s.Start(context.Background(), store.assessment.ID); err != nil {
		t.Fatal(err)
	}
	c.AssertExpectations(t)
}
func TestRatingRuntimeAmbiguousStartDoesNotFreeReservedRow(t *testing.T) {
	s, c, store := runtimeFixture()
	c.On("ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, serviceerror.NewUnavailable("may have been accepted"))
	c.On("DescribeWorkflowExecution", mock.Anything, store.assessment.WorkflowID, "").Return(nil, serviceerror.NewUnavailable("cannot confirm"))
	err := s.Start(context.Background(), store.assessment.ID)
	if !errors.Is(err, ErrRatingStartUncertain) || store.updates != 0 {
		t.Fatalf("%v %+v", err, store)
	}
}
