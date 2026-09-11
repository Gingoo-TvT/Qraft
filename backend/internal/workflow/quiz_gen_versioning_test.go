package workflow

import (
	"context"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/workflow/activities"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/testsuite"
)

func TestQuizGenerationWorkflowUsesVersionedStorePayload(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.SetStartWorkflowOptions(client.StartWorkflowOptions{ID: "quiz-workflow-fixture"})
	env.RegisterWorkflow(QuizGenerationWorkflow)

	generate := func(context.Context, activities.QuizGenerateInput) (*activities.QuizGenerateResult, error) {
		return &activities.QuizGenerateResult{Drafts: []activities.QuizDraft{{
			Title:     "fixture",
			Statement: "statement",
			Answers:   []string{"A"},
		}}}, nil
	}
	storeInputs := make(chan activities.QuizStoreInput, 1)
	store := func(_ context.Context, input activities.QuizStoreInput) (*activities.QuizStoreResult, error) {
		storeInputs <- input
		return &activities.QuizStoreResult{}, nil
	}
	env.RegisterActivityWithOptions(generate, activity.RegisterOptions{Name: "GenerateQuizActivity"})
	env.RegisterActivityWithOptions(store, activity.RegisterOptions{Name: "StoreQuizActivity"})

	env.ExecuteWorkflow(QuizGenerationWorkflow, activities.QuizGenerateInput{
		Subject:    "go",
		Type:       domain.QuizTypeChoice,
		Difficulty: domain.QuizDifficultyEasy,
		Visibility: domain.QuizVisibilityPublic,
		Count:      1,
	})
	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow did not complete")
	}
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow failed: %v", err)
	}

	input := <-storeInputs
	if input.PayloadVersion != activities.ActivityPayloadVersion {
		t.Fatalf("payload version = %d, want %d", input.PayloadVersion, activities.ActivityPayloadVersion)
	}
	if input.IdempotencyKey != "quiz-workflow-fixture/store-quiz/v1" {
		t.Fatalf("unexpected idempotency key %q", input.IdempotencyKey)
	}
}
