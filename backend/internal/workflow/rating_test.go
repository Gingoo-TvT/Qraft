package workflow

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/rating"
	"github.com/Gingoo-TvT/Qraft/backend/internal/workflow/activities"
	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
)

func ratingWorkflowFixture(t *testing.T) (*testsuite.TestWorkflowEnvironment, rating.WorkflowInput, chan activities.RatingStateInput, *atomic.Int32, *atomic.Int32) {
	t.Helper()
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(RatingWorkflow)
	ref := activities.ArtifactRef{SHA256: strings.Repeat("a", 64), Key: "synthetic-artifact"}
	statuses := make(chan activities.RatingStateInput, 40)
	analyzes, verifies := &atomic.Int32{}, &atomic.Int32{}
	env.RegisterActivityWithOptions(func(context.Context, rating.WorkflowInput) (*activities.ArtifactRef, error) { return &ref, nil }, activity.RegisterOptions{Name: "RatingLoadActivity"})
	env.RegisterActivityWithOptions(func(context.Context, activities.RatingBlindInput) (*activities.ArtifactRef, error) { return &ref, nil }, activity.RegisterOptions{Name: "RatingBlindSolveActivity"})
	env.RegisterActivityWithOptions(func(_ context.Context, in activities.RatingAnalyzeInput) (*activities.ArtifactRef, error) {
		analyzes.Add(1)
		if in.Round > 1 {
			t.Error("more than one added round")
		}
		return &ref, nil
	}, activity.RegisterOptions{Name: "RatingAnalyzeActivity"})
	env.RegisterActivityWithOptions(func(context.Context, activities.RatingVerifyInput) (*activities.RatingReportResult, error) {
		verifies.Add(1)
		return &activities.RatingReportResult{Report: ref, NeedsAdditionalRound: true}, nil
	}, activity.RegisterOptions{Name: "RatingVerifyActivity"})
	env.RegisterActivityWithOptions(func(context.Context, activities.RatingFinalizeInput) error { return nil }, activity.RegisterOptions{Name: "RatingFinalizeActivity"})
	env.RegisterActivityWithOptions(func(_ context.Context, in activities.RatingStateInput) error { statuses <- in; return nil }, activity.RegisterOptions{Name: "RatingStateActivity"})
	return env, rating.WorkflowInput{AssessmentID: uuid.New(), ProblemID: uuid.New(), SnapshotHash: "snapshot"}, statuses, analyzes, verifies
}
func TestRatingWorkflowBoundsDisagreementAndReturnsCompactState(t *testing.T) {
	env, in, _, analyzes, verifies := ratingWorkflowFixture(t)
	env.ExecuteWorkflow(RatingWorkflow, in)
	if err := env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}
	if analyzes.Load() != 2 || verifies.Load() != 2 {
		t.Fatalf("analysis=%d verifies=%d", analyzes.Load(), verifies.Load())
	}
	var state domain.WorkflowState
	if err := env.GetWorkflowResult(&state); err != nil {
		t.Fatal(err)
	}
	if state.Status != domain.WorkflowStatusCompleted || state.Progress != 100 {
		t.Fatalf("%+v", state)
	}
	size, err := temporalPayloadSize(state)
	if err != nil || size > 4096 {
		t.Fatalf("large history output %d: %v", size, err)
	}
}
func TestRatingWorkflowFailurePersistsTerminalStatus(t *testing.T) {
	env, in, statuses, _, _ := ratingWorkflowFixture(t)
	env.OnActivity("RatingAnalyzeActivity", mock.Anything, mock.Anything).Return((*activities.ArtifactRef)(nil), temporal.NewNonRetryableApplicationError("synthetic secret-sk-never-expose", "InvalidParameterError", nil))
	env.ExecuteWorkflow(RatingWorkflow, in)
	if env.GetWorkflowError() == nil {
		t.Fatal("failed activity swallowed")
	}
	if strings.Contains(env.GetWorkflowError().Error(), "secret-sk-never-expose") {
		t.Fatal("workflow error leaks provider text")
	}
	found := false
	for len(statuses) > 0 {
		if (<-statuses).Status == "failed" {
			found = true
		}
	}
	if !found {
		t.Fatal("terminal failed state not persisted")
	}
}
func TestRatingWorkflowCancellationQueryableAndPersisted(t *testing.T) {
	env, in, statuses, _, _ := ratingWorkflowFixture(t)
	env.OnActivity("RatingBlindSolveActivity", mock.Anything, mock.Anything).Return(&activities.ArtifactRef{}, nil).After(time.Hour)
	queried := false
	env.RegisterDelayedCallback(func() {
		value, err := env.QueryWorkflow(domain.WorkflowStateQueryName)
		if err != nil {
			t.Error(err)
			return
		}
		var state domain.WorkflowStateQuery
		if err := value.Get(&state); err != nil {
			t.Error(err)
			return
		}
		queried = state.State.Status == domain.WorkflowStatusRunning && state.State.CurrentStep == "rating_blind_solving"
		env.CancelWorkflow()
	}, time.Second)
	env.ExecuteWorkflow(RatingWorkflow, in)
	if env.GetWorkflowError() == nil {
		t.Fatal("cancellation swallowed")
	}
	if !queried {
		t.Fatal("workflow state not observable while solving")
	}
	found := false
	for len(statuses) > 0 {
		if (<-statuses).Status == "cancelled" {
			found = true
		}
	}
	if !found {
		t.Fatal("terminal cancelled state not persisted")
	}
}
