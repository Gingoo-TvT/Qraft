package workflow

import (
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/workflow/activities"
	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/temporal"
)

func TestProblemGenerationAuthenticationFailureStopsBusinessRetries(t *testing.T) {
	for _, stage := range []string{"statement", "cleaning", "testdata", "solution"} {
		t.Run(stage, func(t *testing.T) {
			env, acts, fixture := newPipelineFailClosedEnvironment(t)
			authErr := temporal.NewNonRetryableApplicationError(
				"model provider authentication/access failed (HTTP 401): Invalid token fixture",
				activities.LLMAuthenticationErrorType, nil,
			)
			wantStep := domain.StepGenerateStatement
			wantActivity := "GenerateStatementActivity"
			statementErr := error(nil)
			if stage == "statement" {
				statementErr = authErr
			}
			env.OnActivity(acts.GenerateStatementActivity, mock.Anything, mock.Anything).
				Return(fixture.statement, statementErr).Once()
			if stage != "statement" {
				cleaningErr := error(nil)
				if stage == "cleaning" {
					cleaningErr = authErr
					wantActivity = "CleanStatementActivity"
				}
				env.OnActivity(acts.CleanStatementActivity, mock.Anything, mock.Anything, mock.Anything).
					Return(fixture.statement, cleaningErr).Once()
			}
			if stage == "testdata" || stage == "solution" {
				env.OnActivity(acts.PostStatementSimilarityActivity, mock.Anything, mock.Anything).
					Return(&activities.PostStatementSimilarityResult{}, nil).Once()
				testdataErr := error(nil)
				if stage == "testdata" {
					testdataErr = authErr
					wantStep, wantActivity = domain.StepGenerateTestdata, "GenerateTestDataActivity"
				}
				env.OnActivity(acts.GenerateTestDataActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return(fixture.testData, testdataErr).Once()
			}
			if stage == "solution" {
				wantStep, wantActivity = domain.StepGenerateSolution, "GenerateSolutionActivity"
				env.OnActivity(acts.GenerateSolutionActivity, mock.Anything, mock.Anything, mock.Anything).
					Return(nil, authErr).Once()
			}

			executeAndAssertPipelineFailure(t, env, wantStep, "Invalid token fixture")
			env.AssertActivityNumberOfCalls(t, wantActivity, 1)
			env.AssertActivityNumberOfCalls(t, "GenerateStatementActivity", 1)
			env.AssertActivityNumberOfCalls(t, "RepairStatementActivityV1", 0)
			env.AssertActivityNumberOfCalls(t, "RepairSolutionActivity", 0)
			env.AssertActivityNumberOfCalls(t, "StoreProblemActivity", 0)
		})
	}
}
