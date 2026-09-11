package workflow

import (
	"testing"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/workflow/activities"
	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/temporal"
)

func TestProviderEffectBusyRetriesTestDataWithoutRegeneratingStatement(t *testing.T) {
	env, acts, fixture := newPipelineFailClosedEnvironment(t)
	registerPipelineStatementActivities(env, acts, fixture, 1)

	busy := temporal.NewApplicationErrorWithOptions(
		"test-data provider effect lease is still active",
		"ProviderEffectBusy",
		temporal.ApplicationErrorOptions{NextRetryDelay: time.Minute},
	)
	env.OnActivity(acts.GenerateTestDataActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, busy).
		Twice()
	env.OnActivity(acts.GenerateTestDataActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(fixture.testData, nil).
		Once()
	env.OnActivity(acts.GenerateSolutionActivity, mock.Anything, mock.Anything, mock.Anything).
		Return(fixture.solutions, nil).
		Once()
	env.OnActivity(acts.CompileCheckActivity, mock.Anything, mock.Anything).
		Return(&activities.CompileCheckResult{AllCompiled: true}, nil).
		Once()
	env.OnActivity(acts.RunSandboxActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(fixture.sandbox, nil).
		Twice()
	env.OnActivity(acts.ValidateActivity, mock.Anything, mock.Anything, mock.Anything).
		Return(&activities.ValidationResult{AllPassed: true}, nil).
		Once()
	env.OnActivity(acts.LLMReviewActivity, mock.Anything, mock.Anything).
		Return(fixture.review, nil).
		Once()
	stopErr := temporal.NewNonRetryableApplicationError(
		"fixture stopped after provider-effect retry assertion",
		"ProviderEffectRetryFixtureStop",
		nil,
	)
	env.OnActivity(acts.StoreProblemActivity, mock.Anything, mock.Anything).
		Return(nil, stopErr).
		Once()

	executeAndAssertPipelineFailure(t, env, domain.StepStore, stopErr.Error())
	env.AssertActivityNumberOfCalls(t, "GenerateStatementActivity", 1)
	env.AssertActivityNumberOfCalls(t, "GenerateTestDataActivity", 3)
	env.AssertActivityNumberOfCalls(t, "GenerateSolutionActivity", 1)
}
