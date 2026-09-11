package workflow

import (
	"errors"
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/workflow/activities"
	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	sdkworkflow "go.temporal.io/sdk/workflow"
)

func TestProblemGenerationWorkflowFailsClosedWhenGenerateTestDataFails(t *testing.T) {
	env, acts, fixture := newPipelineFailClosedEnvironment(t)
	registerPipelineStatementActivities(env, acts, fixture, 3)

	testDataErr := errors.New("test-data provider unavailable")
	env.OnActivity(acts.GenerateTestDataActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, testDataErr).
		Times(9)

	executeAndAssertPipelineFailure(t, env, domain.StepGenerateTestdata, testDataErr.Error())
	env.AssertActivityNumberOfCalls(t, "GenerateTestDataActivity", 9)
	env.AssertActivityNumberOfCalls(t, "RunSandboxActivity", 0)
	env.AssertActivityNumberOfCalls(t, "LLMReviewActivity", 0)
	env.AssertActivityNumberOfCalls(t, "StoreProblemActivity", 0)
}

func TestProblemGenerationWorkflowRetriesCandidateTestDataOnSameStatement(t *testing.T) {
	env, acts, fixture := newPipelineFailClosedEnvironmentVersion(t, sdkworkflow.Version(1))
	registerPipelineStatementActivities(env, acts, fixture, 1)

	invalidArtifactErr := temporal.NewNonRetryableApplicationError(
		"remote generator execution failed closed: generator batch 1/2 compilation failed",
		"InvalidGeneratedTestData",
		nil,
	)
	env.OnActivity(acts.GenerateTestDataActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, invalidArtifactErr).
		Once()
	env.OnActivity(acts.GenerateTestDataActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			input := args.Get(3).(domain.ProblemGenParams)
			if !strings.Contains(input.CustomPrompt, testDataRetryFeedbackMarkerV1) {
				t.Errorf("same-statement retry did not carry generator feedback: %q", input.CustomPrompt)
			}
		}).
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
	env.OnActivity(acts.StoreProblemActivity, mock.Anything, mock.Anything).
		Return(&activities.StoreResult{
			ProblemID:    uuid.New(),
			SerialNumber: "AF-RETRY-DATA",
			Status:       domain.ProblemStatusDraft,
		}, nil).
		Once()

	params := pipelineFailClosedParams()
	params.CustomPrompt = "base prompt"
	env.ExecuteWorkflow(ProblemGenerationWorkflow, params)
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow failed after same-statement test-data retry: %v", err)
	}
	env.AssertActivityNumberOfCalls(t, "GenerateStatementActivity", 1)
	env.AssertActivityNumberOfCalls(t, "GenerateTestDataActivity", 2)
	env.AssertActivityNumberOfCalls(t, "GenerateSolutionActivity", 1)
}

func TestProblemGenerationWorkflowRepairsStatementBeforeGeneratingTestData(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(ProblemGenerationWorkflow)
	env.OnGetVersion(problemGenerationStatementRepairV1ChangeID, sdkworkflow.DefaultVersion, 1).
		Return(sdkworkflow.Version(1)).
		Once()
	env.OnGetVersion(problemGenerationStatementQualityGateV1ChangeID, sdkworkflow.DefaultVersion, 1).
		Return(sdkworkflow.Version(1)).
		Once()
	env.OnGetVersion(problemGenerationStatementMarkdownStrictV1ChangeID, sdkworkflow.DefaultVersion, 1).
		Return(sdkworkflow.Version(1)).
		Once()
	env.OnGetVersion(problemGenerationStructuredSamplesV1ChangeID, sdkworkflow.DefaultVersion, 1).
		Return(sdkworkflow.DefaultVersion).
		Once()
	env.OnGetVersion(problemGenerationStructuredSamplesV2ChangeID, sdkworkflow.DefaultVersion, 1).
		Return(sdkworkflow.DefaultVersion).
		Once()
	acts := activities.New(nil)

	malformed := &activities.StatementResult{
		Title:       "quality gate fixture",
		Statement:   "Read one integer n and print n.",
		Tags:        []string{"implementation"},
		OneLineHint: "identity",
	}
	malformed.MarkdownDiagnostics = activities.DiagnoseGeneratedStatementMarkdownV1(malformed.Statement, false)
	repaired := *malformed
	repaired.Statement = "Read one integer n and print n.\n\n## Input Format\nThe input contains one integer n.\n\n## Output Format\nPrint n.\n\n## Constraints\n- 1 <= n <= 10."
	repaired.MarkdownDiagnostics = nil

	repairedSeen := false
	qualityReady := false
	env.OnActivity(acts.GenerateStatementActivity, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			input := args.Get(1).(activities.GenerateStatementInput)
			if !input.StrictMarkdownQuality {
				t.Error("new statement generation did not receive strict markdown quality flag")
			}
		}).
		Return(malformed, nil).
		Times(3)
	env.OnActivity(acts.RepairStatementActivityV1, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			repairedSeen = true
			input := args.Get(1).(activities.RepairStatementInputV1)
			if len(input.Diagnostics) == 0 {
				t.Error("statement repair received no deterministic diagnostics")
			}
			if !input.StrictMarkdownQuality {
				t.Error("statement repair did not receive strict markdown quality flag")
			}
		}).
		Return(&repaired, nil).
		Times(3)
	env.OnActivity(acts.CleanStatementActivity, mock.Anything, mock.Anything, mock.Anything).
		Run(func(mock.Arguments) {
			if !repairedSeen {
				t.Error("clean statement was scheduled before targeted statement repair")
			}
			qualityReady = true
		}).
		Return(&repaired, nil).
		Times(3)
	env.OnActivity(acts.PostStatementSimilarityActivity, mock.Anything, mock.Anything).
		Return(&activities.PostStatementSimilarityResult{}, nil).
		Times(3)
	env.OnActivity(acts.GenerateTestDataActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Run(func(mock.Arguments) {
			if !qualityReady {
				t.Error("test data was scheduled before the statement quality gate completed")
			}
		}).
		Return(nil, errors.New("test-data provider unavailable")).
		Times(9)

	params := pipelineFailClosedParams()
	params.TestDataConfig.NumSamples = 0
	env.ExecuteWorkflow(ProblemGenerationWorkflow, params)
	if err := env.GetWorkflowError(); err == nil || !strings.Contains(err.Error(), "test-data provider unavailable") {
		t.Fatalf("workflow error = %v, want test-data provider failure after quality gate", err)
	}

	env.AssertActivityNumberOfCalls(t, "RepairStatementActivityV1", 3)
	env.AssertActivityNumberOfCalls(t, "GenerateTestDataActivity", 9)
	env.AssertActivityNumberOfCalls(t, "GenerateSolutionActivity", 0)
	env.AssertActivityNumberOfCalls(t, "RunSandboxActivity", 0)
	if !qualityReady {
		t.Fatal("quality gate did not complete before test-data generation")
	}
}

func TestProblemGenerationWorkflowRechecksCleanerOutputBeforeGeneratingTestData(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(ProblemGenerationWorkflow)
	env.OnGetVersion(problemGenerationStatementRepairV1ChangeID, sdkworkflow.DefaultVersion, 1).
		Return(sdkworkflow.Version(1)).
		Once()
	env.OnGetVersion(problemGenerationStatementQualityGateV1ChangeID, sdkworkflow.DefaultVersion, 1).
		Return(sdkworkflow.Version(1)).
		Once()
	env.OnGetVersion(problemGenerationStructuredSamplesV1ChangeID, sdkworkflow.DefaultVersion, 1).
		Return(sdkworkflow.DefaultVersion).
		Once()
	env.OnGetVersion(problemGenerationStructuredSamplesV2ChangeID, sdkworkflow.DefaultVersion, 1).
		Return(sdkworkflow.DefaultVersion).
		Once()
	acts := activities.New(nil)

	valid := &activities.StatementResult{
		Title:       "cleaner output fixture",
		Statement:   "Read one integer n and print n.\n\n## Input Format\nThe input contains one integer n.\n\n## Output Format\nPrint n.\n\n## Constraints\n- 1 <= n <= 10.",
		Tags:        []string{"implementation"},
		OneLineHint: "identity",
	}
	malformedClean := *valid
	malformedClean.Statement = "Read one integer n and print n.\n\n## Input Format\nThe input contains one integer n.\n\n## Output Format\nPrint n."
	malformedClean.MarkdownDiagnostics = nil

	var repairCalls int
	var qualityReady bool
	env.OnActivity(acts.GenerateStatementActivity, mock.Anything, mock.Anything).
		Return(valid, nil).
		Times(3)
	env.OnActivity(acts.CleanStatementActivity, mock.Anything, mock.Anything, mock.Anything).
		Return(&malformedClean, nil).
		Times(3)
	env.OnActivity(acts.RepairStatementActivityV1, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			repairCalls++
			input := args.Get(1).(activities.RepairStatementInputV1)
			if len(input.Diagnostics) != 1 || input.Diagnostics[0].Code != "missing_constraints_heading" {
				t.Errorf("cleaner defect was not passed to targeted repair: %+v", input.Diagnostics)
			}
			qualityReady = true
		}).
		Return(valid, nil).
		Times(3)
	env.OnActivity(acts.PostStatementSimilarityActivity, mock.Anything, mock.Anything).
		Return(&activities.PostStatementSimilarityResult{}, nil).
		Times(3)
	env.OnActivity(acts.GenerateTestDataActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Run(func(mock.Arguments) {
			if !qualityReady {
				t.Error("test data was scheduled before cleaner output repair")
			}
		}).
		Return(nil, errors.New("test-data provider unavailable")).
		Times(9)

	params := pipelineFailClosedParams()
	params.TestDataConfig.NumSamples = 0
	env.ExecuteWorkflow(ProblemGenerationWorkflow, params)
	if err := env.GetWorkflowError(); err == nil || !strings.Contains(err.Error(), "test-data provider unavailable") {
		t.Fatalf("workflow error = %v, want test-data provider failure after cleaner repair", err)
	}
	if repairCalls != 3 {
		t.Fatalf("targeted repair calls = %d, want 3", repairCalls)
	}
	env.AssertActivityNumberOfCalls(t, "GenerateTestDataActivity", 9)
	env.AssertActivityNumberOfCalls(t, "GenerateSolutionActivity", 0)
	env.AssertActivityNumberOfCalls(t, "RunSandboxActivity", 0)
}

func TestProblemGenerationWorkflowDoesNotBlindlyRetryInvalidGeneratedArtifact(t *testing.T) {
	env, acts, fixture := newPipelineFailClosedEnvironment(t)
	registerPipelineStatementActivities(env, acts, fixture, 3)

	invalidArtifactErr := temporal.NewNonRetryableApplicationError(
		"remote generator execution failed closed: generator case 9 produced empty input",
		"InvalidGeneratedTestData",
		nil,
	)
	env.OnActivity(acts.GenerateTestDataActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, invalidArtifactErr).
		Times(3)

	executeAndAssertPipelineFailure(t, env, domain.StepGenerateTestdata, "generator case 9 produced empty input")
	// One attempt per newly generated statement. The same immutable LLM output
	// is no longer retried three times inside each activity invocation.
	env.AssertActivityNumberOfCalls(t, "GenerateTestDataActivity", 3)
}

func TestProblemGenerationWorkflowFailsClosedWhenRunSandboxFails(t *testing.T) {
	env, acts, fixture := newPipelineFailClosedEnvironment(t)
	registerPipelineStatementActivities(env, acts, fixture, 3)
	env.OnActivity(acts.GenerateTestDataActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(fixture.testData, nil).
		Times(3)
	env.OnActivity(acts.GenerateSolutionActivity, mock.Anything, mock.Anything, mock.Anything).
		Return(fixture.solutions, nil).
		Times(6)
	env.OnActivity(acts.CompileCheckActivity, mock.Anything, mock.Anything).
		Return(&activities.CompileCheckResult{AllCompiled: true}, nil).
		Times(6)

	sandboxErr := errors.New("sandbox infrastructure unavailable")
	env.OnActivity(acts.RunSandboxActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, sandboxErr).
		Times(12)

	executeAndAssertPipelineFailure(t, env, domain.StepRunSandbox, "failed to produce valid solutions")
	// Two identical infrastructure failures abandon each statement early;
	// every scheduled sandbox activity still performs its two policy retries.
	env.AssertActivityNumberOfCalls(t, "RunSandboxActivity", 12)
	env.AssertActivityNumberOfCalls(t, "ValidateActivity", 0)
	env.AssertActivityNumberOfCalls(t, "LLMReviewActivity", 0)
	env.AssertActivityNumberOfCalls(t, "StoreProblemActivity", 0)
}

func TestProblemGenerationWorkflowFailsClosedWhenLLMReviewFails(t *testing.T) {
	tests := []struct {
		name      string
		reviewErr error
	}{
		{
			name:      "verification provider failure",
			reviewErr: errors.New("verification provider unavailable"),
		},
		{
			name:      "verification response decode failure",
			reviewErr: errors.New("decode verification response: invalid JSON"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env, acts, fixture := newPipelineFailClosedEnvironment(t)
			registerValidatedPipelineCandidate(env, acts, fixture)
			env.OnActivity(acts.LLMReviewActivity, mock.Anything, mock.Anything).
				Return(nil, tt.reviewErr).
				Times(3)

			executeAndAssertPipelineFailure(t, env, domain.StepLLMReview, tt.reviewErr.Error())
			env.AssertActivityNumberOfCalls(t, "LLMReviewActivity", 3)
			env.AssertActivityNumberOfCalls(t, "StoreProblemActivity", 0)
		})
	}
}

func TestProblemGenerationWorkflowHasNoTerminalResultWhenStoreProblemFails(t *testing.T) {
	env, acts, fixture := newPipelineFailClosedEnvironment(t)
	registerValidatedPipelineCandidate(env, acts, fixture)
	env.OnActivity(acts.LLMReviewActivity, mock.Anything, mock.Anything).
		Return(fixture.review, nil).
		Once()

	storeErr := errors.New("durable problem store unavailable")
	env.OnActivity(acts.StoreProblemActivity, mock.Anything, mock.Anything).
		Return(nil, storeErr).
		Times(5)

	state := executeAndAssertPipelineFailure(t, env, domain.StepStore, storeErr.Error())
	env.AssertActivityNumberOfCalls(t, "StoreProblemActivity", 5)
	for _, step := range state.Steps {
		if step.Step == domain.StepStore && step.Status == domain.WorkflowStatusCompleted {
			t.Fatalf("Store step completed after every StoreProblem attempt failed: %+v", step)
		}
	}
	var terminal domain.WorkflowState
	if err := env.GetWorkflowResult(&terminal); err == nil {
		t.Fatalf("failed Store workflow exposed a successful terminal result: %+v", terminal)
	}
}

type pipelineFailClosedFixture struct {
	statement *activities.StatementResult
	testData  *activities.TestDataResult
	solutions *activities.SolutionResult
	sandbox   *activities.SandboxResult
	review    *activities.ReviewResult
}

func newPipelineFailClosedEnvironment(
	t *testing.T,
) (*testsuite.TestWorkflowEnvironment, *activities.Activities, pipelineFailClosedFixture) {
	return newPipelineFailClosedEnvironmentVersion(t, sdkworkflow.DefaultVersion)
}

func newPipelineFailClosedEnvironmentVersion(
	t *testing.T,
	retryFeedbackVersion sdkworkflow.Version,
) (*testsuite.TestWorkflowEnvironment, *activities.Activities, pipelineFailClosedFixture) {
	t.Helper()
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(ProblemGenerationWorkflow)
	env.OnGetVersion(problemGenerationStructuredSamplesV1ChangeID, sdkworkflow.DefaultVersion, 1).
		Return(sdkworkflow.DefaultVersion).
		Once()
	env.OnGetVersion(problemGenerationStructuredSamplesV2ChangeID, sdkworkflow.DefaultVersion, 1).
		Return(sdkworkflow.DefaultVersion).
		Once()
	env.OnGetVersion(problemGenerationTestManifestV1ChangeID, sdkworkflow.DefaultVersion, 1).
		Return(sdkworkflow.DefaultVersion).
		Once()
	env.OnGetVersion(problemGenerationFinalStatementSimilarityV1ChangeID, sdkworkflow.DefaultVersion, 1).
		Return(sdkworkflow.DefaultVersion).
		Once()
	env.OnGetVersion(problemGenerationResourceCalibrationV1ChangeID, sdkworkflow.DefaultVersion, 1).
		Return(sdkworkflow.DefaultVersion).
		Once()
	env.OnGetVersion(problemGenerationStatementRepairV1ChangeID, sdkworkflow.DefaultVersion, 1).
		Return(sdkworkflow.DefaultVersion).
		Once()
	env.OnGetVersion(problemGenerationStatementQualityGateV1ChangeID, sdkworkflow.DefaultVersion, 1).
		Return(sdkworkflow.DefaultVersion).
		Once()
	// These fixtures assert the legacy fail-closed command sequence. New
	// retry-feedback behavior is covered by its dedicated workflow test.
	env.OnGetVersion(problemGenerationRetryFeedbackV1ChangeID, sdkworkflow.DefaultVersion, 1).
		Return(retryFeedbackVersion).
		Once()
	acts := activities.New(nil)
	fixture := pipelineFailClosedFixture{
		statement: &activities.StatementResult{
			Title:       "Fail-closed pipeline fixture",
			Statement:   "Given one integer, print it.",
			OneLineHint: "Use the identity function.",
			Tags:        []string{"implementation"},
		},
		testData: &activities.TestDataResult{
			PayloadVersion: activities.ActivityPayloadVersion,
			TestCases: []activities.TestCaseData{{
				Input:       "1\n",
				GroupID:     1,
				IsSample:    true,
				Description: "single deterministic fixture",
			}},
		},
		solutions: &activities.SolutionResult{
			MainSolution: domain.Solution{
				SolutionType: domain.SolutionTypeMain,
				Language:     "cpp",
				SourceCode:   "int main() { return 0; }",
			},
			BruteSolution: domain.Solution{
				SolutionType: domain.SolutionTypeBrute,
				Language:     "cpp",
				SourceCode:   "int main() { return 0; }",
			},
		},
		sandbox: &activities.SandboxResult{
			PayloadVersion: activities.ActivityPayloadVersion,
			Outputs:        []string{"1\n"},
		},
		review: &activities.ReviewResult{
			Approved:            true,
			Confidence:          0.95,
			EstimatedDifficulty: 1500,
		},
	}
	return env, acts, fixture
}

func registerPipelineStatementActivities(
	env *testsuite.TestWorkflowEnvironment,
	acts *activities.Activities,
	fixture pipelineFailClosedFixture,
	calls int,
) {
	env.OnActivity(acts.GenerateStatementActivity, mock.Anything, mock.Anything).
		Return(fixture.statement, nil).
		Times(calls)
	env.OnActivity(acts.CleanStatementActivity, mock.Anything, mock.Anything, mock.Anything).
		Return(fixture.statement, nil).
		Times(calls)
	env.OnActivity(acts.PostStatementSimilarityActivity, mock.Anything, mock.Anything).
		Return(&activities.PostStatementSimilarityResult{}, nil).
		Times(calls)
}

func registerValidatedPipelineCandidate(
	env *testsuite.TestWorkflowEnvironment,
	acts *activities.Activities,
	fixture pipelineFailClosedFixture,
) {
	registerPipelineStatementActivities(env, acts, fixture, 1)
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
}

func executeAndAssertPipelineFailure(
	t *testing.T,
	env *testsuite.TestWorkflowEnvironment,
	wantStep domain.WorkflowStep,
	wantError string,
) domain.WorkflowState {
	t.Helper()
	env.ExecuteWorkflow(ProblemGenerationWorkflow, pipelineFailClosedParams())
	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow did not reach a terminal failure")
	}
	workflowErr := env.GetWorkflowError()
	if workflowErr == nil || !strings.Contains(workflowErr.Error(), wantError) {
		t.Fatalf("workflow error = %v, want containing %q", workflowErr, wantError)
	}

	encoded, err := env.QueryWorkflow(domain.WorkflowStateQueryName)
	if err != nil {
		t.Fatalf("query failed workflow state: %v", err)
	}
	var query domain.WorkflowStateQuery
	if err := encoded.Get(&query); err != nil {
		t.Fatalf("decode failed workflow state: %v", err)
	}
	if query.State.Status != domain.WorkflowStatusFailed {
		t.Fatalf("workflow state status = %q, want %q", query.State.Status, domain.WorkflowStatusFailed)
	}
	if query.State.CurrentStep != wantStep {
		t.Fatalf("workflow current step = %q, want %q", query.State.CurrentStep, wantStep)
	}
	if query.State.CompletedAt == nil {
		t.Fatal("failed workflow did not record a terminal timestamp")
	}
	if query.State.Progress == 100 {
		t.Fatalf("failed workflow reported terminal success progress: %+v", query.State)
	}
	return query.State
}

func pipelineFailClosedParams() domain.ProblemGenParams {
	params := domain.DefaultProblemGenParams()
	params.SimilarLimit = 0
	params.RequireReview = false
	params.GenerateEditorial = false
	params.TestDataConfig = domain.TestDataConfig{
		NumTestCases: 1,
		NumSamples:   1,
		Groups: []domain.TestGroup{{
			GroupID:    1,
			NumCases:   1,
			Score:      100,
			BruteCheck: true,
		}},
	}
	return params
}
