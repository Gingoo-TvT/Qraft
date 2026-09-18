package workflow

import (
	"fmt"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/rating"
	"github.com/Gingoo-TvT/Qraft/backend/internal/workflow/activities"
	"github.com/google/uuid"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// RatingWorkflow bounds deliberation to two independent solves, an analysis,
// actual candidate execution, and at most one targeted follow-up. Evidence
// travels as immutable CAS references, not code/test bytes in workflow history.
func RatingWorkflow(ctx workflow.Context, in rating.WorkflowInput) (state *domain.WorkflowState, err error) {
	now := workflow.Now(ctx)
	state = &domain.WorkflowState{Status: domain.WorkflowStatusRunning, StartedAt: &now, CurrentStep: "rating_snapshot"}
	if err = workflow.SetQueryHandler(ctx, domain.WorkflowStateQueryName, func() (domain.WorkflowStateQuery, error) { return domain.WorkflowStateQuery{State: *state}, nil }); err != nil {
		return state, err
	}
	opts := workflow.ActivityOptions{StartToCloseTimeout: 15 * time.Minute, HeartbeatTimeout: time.Minute, WaitForCancellation: true, RetryPolicy: &temporal.RetryPolicy{MaximumAttempts: 2, InitialInterval: time.Second, NonRetryableErrorTypes: []string{"InvalidParameterError"}}}
	activityCtx := workflow.WithActivityOptions(ctx, opts)
	activityCtx, cancelActivities := workflow.WithCancel(activityCtx)
	defer cancelActivities()
	fastOpts := workflow.ActivityOptions{StartToCloseTimeout: 2 * time.Minute, RetryPolicy: &temporal.RetryPolicy{MaximumAttempts: 3, InitialInterval: time.Second}}
	fastCtx := workflow.WithActivityOptions(ctx, fastOpts)
	defer func() {
		if err == nil {
			return
		}
		cancelActivities()
		publicMessage := "题目评估未完成，请检查当前阶段的服务配置和执行证据。"
		state.MarkFailedAt(publicMessage, workflow.Now(ctx))
		status := "failed"
		if temporal.IsCanceledError(err) || ctx.Err() != nil {
			state.Status = domain.WorkflowStatusCancelled
			status = "cancelled"
			err = temporal.NewCanceledError()
		} else {
			err = temporal.NewNonRetryableApplicationError(publicMessage, "RatingAssessmentFailed", nil)
		}
		disconnected, _ := workflow.NewDisconnectedContext(ctx)
		cleanupCtx := workflow.WithActivityOptions(disconnected, fastOpts)
		if e := workflow.ExecuteActivity(cleanupCtx, "RatingStateActivity", activities.RatingStateInput{AssessmentID: in.AssessmentID, Status: status, Phase: string(state.CurrentStep), Error: publicMessage}).Get(cleanupCtx, nil); e != nil {
			workflow.GetLogger(ctx).Error("rating terminal state persistence failed", "error", e)
		}
	}()
	if in.AssessmentID == uuid.Nil || in.ProblemID == uuid.Nil || in.SnapshotHash == "" {
		err = temporal.NewNonRetryableApplicationError("rating assessment identity is required", "InvalidParameterError", nil)
		return state, err
	}
	phase := func(name string, progress int) error {
		state.CurrentStep = domain.WorkflowStep(name)
		state.Progress = progress
		return workflow.ExecuteActivity(fastCtx, "RatingStateActivity", activities.RatingStateInput{AssessmentID: in.AssessmentID, Status: "running", Phase: name}).Get(ctx, nil)
	}
	var snapshot activities.ArtifactRef
	if err = workflow.ExecuteActivity(fastCtx, "RatingLoadActivity", in).Get(ctx, &snapshot); err != nil {
		return state, err
	}
	if err = phase("rating_blind_solving", 10); err != nil {
		return state, err
	}
	futures := []workflow.Future{
		workflow.ExecuteActivity(activityCtx, "RatingBlindSolveActivity", activities.RatingBlindInput{Snapshot: snapshot, Role: "blind_a", Runtime: in.BlindA}),
		workflow.ExecuteActivity(activityCtx, "RatingBlindSolveActivity", activities.RatingBlindInput{Snapshot: snapshot, Role: "blind_b", Runtime: in.BlindB}),
	}
	blinds := make([]activities.ArtifactRef, 2)
	for i, f := range futures {
		if err = f.Get(ctx, &blinds[i]); err != nil {
			return state, err
		}
	}
	if err = phase("rating_kc_analysis", 35); err != nil {
		return state, err
	}
	var report activities.ArtifactRef
	if err = workflow.ExecuteActivity(activityCtx, "RatingAnalyzeActivity", activities.RatingAnalyzeInput{Snapshot: snapshot, Blinds: blinds, Round: 0, Runtime: in.Review}).Get(ctx, &report); err != nil {
		return state, err
	}
	if err = phase("rating_validation", 55); err != nil {
		return state, err
	}
	var verified activities.RatingReportResult
	if err = workflow.ExecuteActivity(activityCtx, "RatingVerifyActivity", activities.RatingVerifyInput{Snapshot: snapshot, Report: report}).Get(ctx, &verified); err != nil {
		return state, err
	}
	if verified.NeedsAdditionalRound {
		if err = phase("rating_targeted_review", 70); err != nil {
			return state, err
		}
		if err = workflow.ExecuteActivity(activityCtx, "RatingAnalyzeActivity", activities.RatingAnalyzeInput{Snapshot: snapshot, Blinds: blinds, Previous: &verified.Report, Round: 1, Runtime: in.Review}).Get(ctx, &report); err != nil {
			return state, err
		}
		if err = phase("rating_revalidation", 85); err != nil {
			return state, err
		}
		if err = workflow.ExecuteActivity(activityCtx, "RatingVerifyActivity", activities.RatingVerifyInput{Snapshot: snapshot, Report: report}).Get(ctx, &verified); err != nil {
			return state, err
		}
	}
	if err = phase("rating_finalize", 95); err != nil {
		return state, err
	}
	if err = workflow.ExecuteActivity(fastCtx, "RatingFinalizeActivity", activities.RatingFinalizeInput{AssessmentID: in.AssessmentID, Report: verified.Report}).Get(ctx, nil); err != nil {
		return state, err
	}
	state.CurrentStep = "rating_completed"
	state.MarkCompletedAt(workflow.Now(ctx))
	state.Steps = []domain.StepResult{{Step: "rating_completed", Status: domain.WorkflowStatusCompleted, Output: fmt.Sprintf("assessment=%s report=%s", in.AssessmentID, verified.Report.SHA256)}}
	return state, nil
}
