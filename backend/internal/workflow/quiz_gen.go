package workflow

import (
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/workflow/activities"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// QuizGenerationWorkflow generates objective quiz drafts and persists them.
func QuizGenerationWorkflow(ctx workflow.Context, input activities.QuizGenerateInput) (*activities.QuizStoreResult, error) {
	logger := workflow.GetLogger(ctx)
	payloadPatchVersion := workflow.GetVersion(
		ctx,
		"quiz-generation-activity-payload-v1",
		workflow.DefaultVersion,
		activities.ActivityPayloadVersion,
	)
	logger.Info("QuizGenerationWorkflow started",
		"subject", input.Subject,
		"type", input.Type,
		"difficulty", input.Difficulty,
		"count", input.Count,
	)

	llmCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Minute,
		HeartbeatTimeout:    60 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    10 * time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    2 * time.Minute,
			MaximumAttempts:    3,
			NonRetryableErrorTypes: []string{
				"InvalidQuizType",
				"InvalidParameterError",
			},
		},
	})

	storeCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    30 * time.Second,
			MaximumAttempts:    5,
		},
	})

	var generated activities.QuizGenerateResult
	if err := workflow.ExecuteActivity(llmCtx, "GenerateQuizActivity", input).Get(ctx, &generated); err != nil {
		return nil, err
	}

	var stored activities.QuizStoreResult
	storeInput := activities.QuizStoreInput{
		Drafts:            generated.Drafts,
		Params:            input,
		KnowledgePointIDs: input.KnowledgePointIDs,
		SourceArtifacts:   generated.SourceArtifacts,
	}
	if payloadPatchVersion >= activities.ActivityPayloadVersion {
		storeInput.PayloadVersion = activities.ActivityPayloadVersion
		storeInput.IdempotencyKey = workflow.GetInfo(ctx).WorkflowExecution.ID + "/store-quiz/v1"
	}
	err := workflow.ExecuteActivity(storeCtx, "StoreQuizActivity", storeInput).Get(ctx, &stored)
	if err != nil {
		return nil, err
	}

	logger.Info("QuizGenerationWorkflow completed", "inserted", len(stored.InsertedIDs))
	return &stored, nil
}
