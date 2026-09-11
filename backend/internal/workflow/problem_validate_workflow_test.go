package workflow

import (
	"errors"
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/workflow/activities"
	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	sdkworkflow "go.temporal.io/sdk/workflow"
)

func TestProblemValidationWorkflowRunsBruteOnlyOnManifestSubset(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.SetStartWorkflowOptions(client.StartWorkflowOptions{ID: "problem-validation-subset-test"})
	env.RegisterWorkflow(ProblemValidationWorkflow)
	configureProblemValidationVersions(env, 1)
	acts := activities.New(nil)

	problemID := uuid.New()
	fetch := validationWorkflowFetchFixture(problemID, []int{0, 2})
	env.OnActivity(acts.FetchProblemDataActivity, mock.Anything, problemID).Return(fetch, nil).Once()
	env.OnActivity(acts.CompileCheckActivity, mock.Anything, mock.Anything).
		Return(&activities.CompileCheckResult{AllCompiled: true}, nil).Once()
	env.OnActivity(acts.RunSandboxActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			solution := args.Get(1).(domain.Solution)
			cases := args.Get(2).([]activities.TestCaseData)
			limits := args.Get(3).(activities.ExecutionLimits)
			switch solution.SolutionType {
			case domain.SolutionTypeMain:
				if len(cases) != 3 || limits.TimeLimitMs != 1000 || limits.MemoryLimitMB != 256 {
					t.Errorf("main run cases/limits = %d/%+v", len(cases), limits)
				}
			case domain.SolutionTypeBrute:
				if len(cases) != 2 || limits.TimeLimitMs != 1000 || limits.MemoryLimitMB != problemResourceBruteMemoryLimitMBV1 {
					t.Errorf("brute run cases/limits = %d/%+v", len(cases), limits)
				}
			}
		}).
		Return(&activities.SandboxResult{PayloadVersion: activities.ActivityPayloadVersion, Outputs: []string{"a", "b", "c"}}, nil).
		Once()
	env.OnActivity(acts.RunSandboxActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&activities.SandboxResult{PayloadVersion: activities.ActivityPayloadVersion, Outputs: []string{"a", "c"}}, nil).
		Once()
	env.OnActivity(acts.ValidateActivity, mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			main := args.Get(1).(activities.SandboxResult)
			other := args.Get(2).(activities.SandboxResult)
			if len(main.Outputs) == 3 && len(other.Outputs) != 3 {
				t.Errorf("full-suite validation compared %d outputs", len(other.Outputs))
			}
			if len(main.Outputs) == 2 && len(other.Outputs) != 2 {
				t.Errorf("subset validation compared %d outputs", len(other.Outputs))
			}
		}).
		Return(&activities.ValidationResult{AllPassed: true}, nil).
		Twice()

	env.ExecuteWorkflow(ProblemValidationWorkflow, problemID)
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("validation workflow failed: %v", err)
	}
	var state domain.WorkflowState
	if err := env.GetWorkflowResult(&state); err != nil {
		t.Fatal(err)
	}
	if state.Status != domain.WorkflowStatusCompleted {
		t.Fatalf("state status = %s, want completed; error=%s", state.Status, state.Error)
	}
	env.AssertActivityNumberOfCalls(t, "RunSandboxActivity", 2)
	env.AssertActivityNumberOfCalls(t, "ValidateActivity", 2)
}

func TestProblemValidationWorkflowLabelsReferenceRuntimeFailure(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.SetStartWorkflowOptions(client.StartWorkflowOptions{ID: "problem-validation-reference-failure-test"})
	env.RegisterWorkflow(ProblemValidationWorkflow)
	configureProblemValidationVersions(env, 1)
	acts := activities.New(nil)

	problemID := uuid.New()
	fetch := validationWorkflowFetchFixture(problemID, []int{0})
	env.OnActivity(acts.FetchProblemDataActivity, mock.Anything, problemID).Return(fetch, nil).Once()
	env.OnActivity(acts.CompileCheckActivity, mock.Anything, mock.Anything).
		Return(&activities.CompileCheckResult{AllCompiled: true}, nil).Once()
	env.OnActivity(acts.RunSandboxActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&activities.SandboxResult{PayloadVersion: activities.ActivityPayloadVersion, Outputs: []string{"a", "b", "c"}}, nil).
		Once()
	env.OnActivity(acts.ValidateActivity, mock.Anything, mock.Anything, mock.Anything).
		Return(&activities.ValidationResult{AllPassed: true}, nil).Once()
	env.OnActivity(acts.RunSandboxActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, errors.New("reference solution program failure (runtime): test case 1 failed closed with verdict TLE")).
		Once()

	env.ExecuteWorkflow(ProblemValidationWorkflow, problemID)
	workflowErr := env.GetWorkflowError()
	if workflowErr == nil {
		t.Fatal("workflow unexpectedly succeeded after reference TLE")
	}
	var applicationErr *temporal.ApplicationError
	if !errors.As(workflowErr, &applicationErr) || applicationErr.Type() != "ReferenceSolutionFailure" || !applicationErr.NonRetryable() {
		t.Fatalf("workflow error = %T %v, want non-retryable ReferenceSolutionFailure", workflowErr, workflowErr)
	}
	if !strings.Contains(applicationErr.Error(), "main solution was not rejected") || !strings.Contains(applicationErr.Error(), "verdict TLE") {
		t.Fatalf("reference failure message = %v", applicationErr)
	}
}

func configureProblemValidationVersions(env *testsuite.TestWorkflowEnvironment, differentialVersion sdkworkflow.Version) {
	env.OnGetVersion("problem-validation-activity-payload-v1", sdkworkflow.DefaultVersion, activities.ActivityPayloadVersion).
		Return(sdkworkflow.Version(activities.ActivityPayloadVersion)).Once()
	env.OnGetVersion("workflow-state-step-output-compaction-v1", sdkworkflow.DefaultVersion, 1).
		Return(sdkworkflow.Version(1)).Once()
	env.OnGetVersion("problem-validation-edit-refresh-v1", sdkworkflow.DefaultVersion, 1).
		Return(sdkworkflow.DefaultVersion).Once()
	env.OnGetVersion(problemValidationBruteMemoryV1ChangeID, sdkworkflow.DefaultVersion, 1).
		Return(sdkworkflow.Version(1)).Once()
	env.OnGetVersion(problemBruteCorrectnessBudgetV1ChangeID, sdkworkflow.DefaultVersion, 1).
		Return(sdkworkflow.Version(1)).Once()
	env.OnGetVersion(problemValidationDifferentialSubsetV1ChangeID, sdkworkflow.DefaultVersion, 1).
		Return(differentialVersion).Once()
	env.OnGetVersion(problemValidationMemoryFloorV1ChangeID, sdkworkflow.DefaultVersion, 1).
		Return(sdkworkflow.Version(1)).Once()
}

func validationWorkflowFetchFixture(problemID uuid.UUID, bruteIndices []int) *activities.FetchProblemDataResult {
	return &activities.FetchProblemDataResult{
		Problem: domain.Problem{
			ID: problemID, Title: "validation fixture", Statement: "fixture",
			TimeLimit: 1000, MemoryLimit: 64, Status: domain.ProblemStatusDraft,
		},
		MainSolution:  domain.Solution{SolutionType: domain.SolutionTypeMain, Language: "cpp", SourceCode: "main"},
		BruteSolution: domain.Solution{SolutionType: domain.SolutionTypeBrute, Language: "cpp", SourceCode: "brute"},
		TestCases: []activities.TestCaseData{
			{Input: "a\n", IsSample: true},
			{Input: "b\n"},
			{Input: "c\n"},
		},
		Outputs:            []string{"a", "b", "c"},
		BruteIndices:       bruteIndices,
		TestManifestSchema: activities.TestManifestSchemaVersion,
	}
}
