package workflow

import (
	"errors"
	"fmt"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/workflow/activities"
	"github.com/google/uuid"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const ProblemSetBatchSize = 12
const ProblemSetConcurrency = 4

type ProblemSetPreparedSlot struct {
	Slot          domain.ProblemSetGenerationSlot  `json:"slot"`
	Programming   *ProblemGenerationQualityInputV1 `json:"programming,omitempty"`
	Quiz          *activities.QuizGenerateInput    `json:"quiz,omitempty"`
	ReviewRuntime *domain.LLMRuntimeConfig         `json:"review_runtime,omitempty"`
}
type ProblemSetBatch struct {
	Slots []ProblemSetPreparedSlot `json:"slots"`
}
type ProblemSetSlotInput struct {
	Ref  domain.ProblemSetGenerationRef  `json:"ref"`
	Slot domain.ProblemSetGenerationSlot `json:"slot"`
}
type ProblemSetFinishInput struct {
	Ref       domain.ProblemSetGenerationRef `json:"ref"`
	Error     string                         `json:"error,omitempty"`
	Cancelled bool                           `json:"cancelled"`
}

// One run handles a small page. Durable slot checkpoints and ContinueAsNew keep
// a 1000-item set's history bounded; no web request has to remain connected.
func ProblemSetGenerationWorkflow(ctx workflow.Context, ref domain.ProblemSetGenerationRef) (retErr error) {
	fast := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: 5 * time.Minute, RetryPolicy: &temporal.RetryPolicy{InitialInterval: time.Second, MaximumAttempts: 5}})
	defer func() {
		if retErr == nil || workflow.IsContinueAsNewError(retErr) {
			return
		}
		clean, _ := workflow.NewDisconnectedContext(ctx)
		clean = workflow.WithActivityOptions(clean, workflow.ActivityOptions{StartToCloseTimeout: time.Minute, RetryPolicy: &temporal.RetryPolicy{MaximumAttempts: 5}})
		_ = workflow.ExecuteActivity(clean, "FinishProblemSetGenerationActivity", ProblemSetFinishInput{Ref: ref, Error: retErr.Error(), Cancelled: temporal.IsCanceledError(retErr) || ctx.Err() != nil}).Get(clean, nil)
	}()
	planCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: 15 * time.Minute, HeartbeatTimeout: 60 * time.Second, RetryPolicy: &temporal.RetryPolicy{InitialInterval: 2 * time.Second, MaximumAttempts: 3, NonRetryableErrorTypes: []string{"InvalidSetPlan", "SetGenerationChanged"}}})
	var batch ProblemSetBatch
	if err := workflow.ExecuteActivity(planCtx, "PrepareProblemSetBatchActivity", ref).Get(planCtx, &batch); err != nil {
		return err
	}
	next, finished := 0, 0
	var checkpointErr error
	for worker := 0; worker < ProblemSetConcurrency && worker < len(batch.Slots); worker++ {
		workflow.Go(ctx, func(ctx workflow.Context) {
			slotCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: 5 * time.Minute, RetryPolicy: &temporal.RetryPolicy{InitialInterval: time.Second, MaximumAttempts: 5}})
			for next < len(batch.Slots) && ctx.Err() == nil {
				i := next
				next++
				if err := runProblemSetSlot(ctx, slotCtx, ref, batch.Slots[i]); err != nil && checkpointErr == nil {
					checkpointErr = err
				}
				finished++
			}
		})
	}
	if err := workflow.Await(ctx, func() bool { return finished == len(batch.Slots) }); err != nil {
		return err
	}
	if checkpointErr != nil {
		return checkpointErr
	}
	var more bool
	if err := workflow.ExecuteActivity(fast, "FinishProblemSetGenerationActivity", ProblemSetFinishInput{Ref: ref}).Get(fast, &more); err != nil {
		return err
	}
	if more {
		return workflow.NewContinueAsNewError(ctx, ProblemSetGenerationWorkflow, ref)
	}
	return nil
}

func runProblemSetSlot(ctx, fast workflow.Context, ref domain.ProblemSetGenerationRef, in ProblemSetPreparedSlot) error {
	slot := in.Slot
	slot.Status = "running"
	if err := workflow.ExecuteActivity(fast, "RecordProblemSetSlotActivity", ProblemSetSlotInput{ref, slot}).Get(fast, nil); err != nil {
		return err
	}
	childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{WorkflowID: slot.ChildID, WorkflowExecutionTimeout: 6 * time.Hour, ParentClosePolicy: enumspb.PARENT_CLOSE_POLICY_TERMINATE, WaitForCancellation: true})
	var err error
	if slot.ProblemID == nil && slot.QuizID == nil {
		if slot.Type == domain.QuizTypeProgramming {
			if in.Programming == nil {
				err = fmt.Errorf("编程题生成参数缺失")
			} else {
				var result ProblemGenerationQualityResultV1
				err = workflow.ExecuteChildWorkflow(childCtx, ProblemGenerationQualityWorkflowV1, *in.Programming).Get(childCtx, &result)
				if err == nil {
					var id uuid.UUID
					id, err = uuid.Parse(result.StoredProblemID)
					if err == nil && (result.Decision != "pass" || id == uuid.Nil) {
						err = fmt.Errorf("编程题未通过质量校验")
					}
					if err == nil {
						slot.ProblemID = &id
					}
				}
			}
		} else {
			if in.Quiz == nil {
				err = fmt.Errorf("客观题生成参数缺失")
			} else {
				var result activities.QuizStoreResult
				err = workflow.ExecuteChildWorkflow(childCtx, ProblemSetQuizGenerationWorkflow, ProblemSetQuizInput{*in.Quiz, in.ReviewRuntime}).Get(childCtx, &result)
				if err == nil && (len(result.InsertedIDs) != 1 || result.InsertedIDs[0] == uuid.Nil) {
					err = fmt.Errorf("客观题返回数量不正确")
				}
				if err == nil {
					slot.QuizID = &result.InsertedIDs[0]
				}
			}
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err == nil {
		slot.Status = "generated"
		if err = workflow.ExecuteActivity(fast, "RecordProblemSetSlotActivity", ProblemSetSlotInput{ref, slot}).Get(fast, nil); err != nil {
			return err
		}
		err = workflow.ExecuteActivity(fast, "AttachProblemSetSlotActivity", ProblemSetSlotInput{ref, slot}).Get(fast, nil)
	}
	if err != nil {
		slot.Status = "failed"
		var rejected *temporal.ApplicationError
		slot.Regenerate = errors.As(err, &rejected) && rejected.Type() == "ProblemSetSlotRejected"
		slot.Error = err.Error()
		return workflow.ExecuteActivity(fast, "RecordProblemSetSlotActivity", ProblemSetSlotInput{ref, slot}).Get(fast, nil)
	}
	return nil
}

type ProblemSetQuizInput struct {
	Generate      activities.QuizGenerateInput `json:"generate"`
	ReviewRuntime *domain.LLMRuntimeConfig     `json:"review_runtime"`
}

func ProblemSetQuizGenerationWorkflow(ctx workflow.Context, in ProblemSetQuizInput) (*activities.QuizStoreResult, error) {
	actCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: 15 * time.Minute, HeartbeatTimeout: 60 * time.Second, RetryPolicy: &temporal.RetryPolicy{InitialInterval: 2 * time.Second, MaximumAttempts: 3}})
	for attempt := 0; attempt < 2; attempt++ {
		var generated activities.QuizGenerateResult
		if err := workflow.ExecuteActivity(actCtx, "GenerateQuizActivity", in.Generate).Get(actCtx, &generated); err != nil {
			return nil, err
		}
		var review activities.ProblemSetQuizReviewResult
		if len(generated.Drafts) != 1 {
			review.Feedback = "必须恰好输出一道完整客观题"
		} else if err := workflow.ExecuteActivity(actCtx, "ReviewProblemSetQuizActivity", activities.ProblemSetQuizReviewInput{Type: in.Generate.Type, Draft: generated.Drafts[0], Runtime: in.ReviewRuntime}).Get(actCtx, &review); err != nil {
			return nil, err
		}
		if !review.Approved {
			in.Generate.CustomPrompt += "\n上一版未通过复核，重新生成并修正：" + review.Feedback
			if attempt == 1 {
				return nil, temporal.NewNonRetryableApplicationError(review.Feedback, "QuizQualityNotMet", nil)
			}
			continue
		}
		var stored activities.QuizStoreResult
		input := activities.QuizStoreInput{
			PayloadVersion: activities.ActivityPayloadVersion, IdempotencyKey: workflow.GetInfo(ctx).WorkflowExecution.ID + "/store-quiz/v1",
			Drafts: generated.Drafts, Params: in.Generate, KnowledgePointIDs: in.Generate.KnowledgePointIDs,
			SourceArtifacts: append(generated.SourceArtifacts, review.SourceArtifacts...),
		}
		if err := workflow.ExecuteActivity(actCtx, "StoreQuizActivity", input).Get(actCtx, &stored); err != nil {
			return nil, err
		}
		return &stored, nil
	}
	return nil, fmt.Errorf("客观题复核失败")
}
