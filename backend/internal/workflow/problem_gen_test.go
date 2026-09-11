package workflow

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/workflow/activities"
	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	sdkworkflow "go.temporal.io/sdk/workflow"
)

func TestProblemGenerationWorkflowReviewGateVersions(t *testing.T) {
	tests := []struct {
		name              string
		version           sdkworkflow.Version
		llmApproved       bool
		llmIsDuplicate    bool
		requireReview     bool
		humanDecision     *domain.ReviewDecision
		sendStaleFirst    bool
		wantStoreCalls    int
		wantStatus        domain.WorkflowStatus
		wantQuarantine    bool
		wantNoTimers      bool
		wantWorkflowError string
		wantErrorType     string
	}{
		{
			name:           "default rejected without required review preserves legacy store",
			version:        sdkworkflow.DefaultVersion,
			llmApproved:    false,
			wantStoreCalls: 1,
			wantStatus:     domain.WorkflowStatusCompleted,
		},
		{
			name:              "duplicate remains a hard failure before review gate",
			version:           2,
			llmApproved:       false,
			llmIsDuplicate:    true,
			wantStoreCalls:    0,
			wantStatus:        domain.WorkflowStatusFailed,
			wantWorkflowError: "LLM reviewer judged as duplicate",
			wantErrorType:     "DuplicateProblem",
		},
		{
			name:           "default required review accepts tokenless approval",
			version:        sdkworkflow.DefaultVersion,
			llmApproved:    true,
			requireReview:  true,
			humanDecision:  &domain.ReviewDecision{Approved: true},
			wantStoreCalls: 1,
			wantStatus:     domain.WorkflowStatusCompleted,
		},
		{
			name:              "default invalid rejection preserves legacy validation failure",
			version:           sdkworkflow.DefaultVersion,
			llmApproved:       true,
			requireReview:     true,
			humanDecision:     &domain.ReviewDecision{Approved: false},
			wantStoreCalls:    0,
			wantStatus:        domain.WorkflowStatusFailed,
			wantWorkflowError: "feedback is required when rejecting a problem",
		},
		{
			name:           "v1 rejected review ignores stale token then stores after approval",
			version:        1,
			llmApproved:    false,
			humanDecision:  &domain.ReviewDecision{Approved: true},
			sendStaleFirst: true,
			wantStoreCalls: 1,
			wantStatus:     domain.WorkflowStatusCompleted,
		},
		{
			name:              "v1 rejected review stops after human rejection",
			version:           1,
			llmApproved:       false,
			humanDecision:     &domain.ReviewDecision{Approved: false, Feedback: "quality issues remain"},
			wantStoreCalls:    0,
			wantStatus:        domain.WorkflowStatusFailed,
			wantWorkflowError: "human reviewer rejected: quality issues remain",
		},
		{
			name:           "v1 approved review still waits when required",
			version:        1,
			llmApproved:    true,
			requireReview:  true,
			humanDecision:  &domain.ReviewDecision{Approved: true},
			wantStoreCalls: 1,
			wantStatus:     domain.WorkflowStatusCompleted,
		},
		{
			name:           "v1 approved without required review stores normally",
			version:        1,
			llmApproved:    true,
			wantStoreCalls: 1,
			wantStatus:     domain.WorkflowStatusCompleted,
		},
		{
			name:           "v2 rejected review stores quarantined without timer",
			version:        2,
			llmApproved:    false,
			wantStoreCalls: 1,
			wantStatus:     domain.WorkflowStatusRejectedQuarantined,
			wantQuarantine: true,
			wantNoTimers:   true,
		},
		{
			name:           "v2 approved review stores normally",
			version:        2,
			llmApproved:    true,
			wantStoreCalls: 1,
			wantStatus:     domain.WorkflowStatusCompleted,
		},
		{
			name:           "v2 operational review switch still waits",
			version:        2,
			llmApproved:    false,
			requireReview:  true,
			humanDecision:  &domain.ReviewDecision{Approved: true},
			wantStoreCalls: 1,
			wantStatus:     domain.WorkflowStatusCompleted,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env, storeCapture := newProblemGenerationReviewTestEnvironment(
				t,
				tt.version,
				tt.llmApproved,
				tt.llmIsDuplicate,
				tt.wantQuarantine,
				0,
			)
			params := reviewGateTestParams(tt.requireReview)
			timersScheduled := 0
			env.SetOnTimerScheduledListener(func(string, time.Duration) {
				timersScheduled++
			})

			var callbackErr error
			var firstRequest *domain.ReviewRequest
			if tt.humanDecision != nil {
				env.RegisterDelayedCallback(func() {
					query, err := queryReviewGateState(env)
					if err != nil {
						callbackErr = err
						return
					}
					if err := assertWaitingReviewQuery(query, tt.version, tt.llmApproved); err != nil {
						callbackErr = err
						return
					}
					firstRequest = query.ReviewRequest
					if tt.sendStaleFirst {
						env.SignalWorkflow(domain.ReviewSignalChannelName, domain.ReviewSignal{
							Token:    "stale-review-token",
							Decision: *tt.humanDecision,
						})
						return
					}
					env.SignalWorkflow(domain.ReviewSignalChannelName, reviewSignalForVersion(tt.version, query.ReviewRequest, *tt.humanDecision))
				}, time.Second)

				if tt.sendStaleFirst {
					env.RegisterDelayedCallback(func() {
						query, err := queryReviewGateState(env)
						if err != nil {
							callbackErr = err
							return
						}
						if query.State.Status != domain.WorkflowStatusWaitingReview {
							callbackErr = fmt.Errorf("state after stale token = %q, want %q", query.State.Status, domain.WorkflowStatusWaitingReview)
							return
						}
						if firstRequest == nil || query.ReviewRequest == nil || query.ReviewRequest.Token != firstRequest.Token {
							callbackErr = fmt.Errorf("review request changed after stale token: first=%+v second=%+v", firstRequest, query.ReviewRequest)
							return
						}
						env.SignalWorkflow(domain.ReviewSignalChannelName, reviewSignalForVersion(tt.version, query.ReviewRequest, *tt.humanDecision))
					}, 2*time.Second)
				}
			}

			env.ExecuteWorkflow(ProblemGenerationWorkflow, params)
			if callbackErr != nil {
				t.Fatal(callbackErr)
			}
			if !env.IsWorkflowCompleted() {
				t.Fatal("workflow did not complete")
			}

			workflowErr := env.GetWorkflowError()
			if tt.wantWorkflowError == "" {
				if workflowErr != nil {
					t.Fatalf("workflow failed: %v", workflowErr)
				}
				var state domain.WorkflowState
				if err := env.GetWorkflowResult(&state); err != nil {
					t.Fatalf("get workflow result: %v", err)
				}
				if state.Status != tt.wantStatus {
					t.Fatalf("workflow status = %q, want %q", state.Status, tt.wantStatus)
				}
			} else if workflowErr == nil || !strings.Contains(workflowErr.Error(), tt.wantWorkflowError) {
				t.Fatalf("workflow error = %v, want containing %q", workflowErr, tt.wantWorkflowError)
			}
			if tt.wantErrorType != "" {
				var applicationErr *temporal.ApplicationError
				if !errors.As(workflowErr, &applicationErr) {
					t.Fatalf("workflow error = %T, want Temporal application error", workflowErr)
				}
				if applicationErr.Type() != tt.wantErrorType || !applicationErr.NonRetryable() {
					t.Fatalf(
						"application error type = %q, non-retryable = %t; want type %q and non-retryable",
						applicationErr.Type(),
						applicationErr.NonRetryable(),
						tt.wantErrorType,
					)
				}
			}

			env.AssertActivityNumberOfCalls(t, "StoreProblemActivity", tt.wantStoreCalls)
			if tt.wantNoTimers && timersScheduled != 0 {
				t.Fatalf("v2 automated quarantine scheduled %d timer(s), want 0", timersScheduled)
			}
			if storeCapture.input != nil {
				assertCurrentManifestStoreInput(t, storeCapture.input)
			}
			if tt.wantQuarantine {
				assertV2ReviewQuarantineInput(t, storeCapture.input)
			} else if storeCapture.input != nil && storeCapture.input.ReviewQuarantine != nil {
				t.Fatalf("non-quarantine path included review quarantine evidence: %+v", storeCapture.input.ReviewQuarantine)
			}
		})
	}
}

func TestProblemGenerationFinalStatementSimilarityV1HardRejectRegeneratesOuterAttempt(t *testing.T) {
	env, storeCapture := newProblemGenerationReviewTestEnvironment(
		t,
		sdkworkflow.Version(2),
		true,
		false,
		false,
		1,
	)

	env.ExecuteWorkflow(ProblemGenerationWorkflow, reviewGateTestParams(false))
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow failed after second final statement passed similarity: %v", err)
	}
	env.AssertActivityNumberOfCalls(t, "GenerateStatementActivity", 2)
	env.AssertActivityNumberOfCalls(t, "FinalizeStatementSamplesActivity", 2)
	env.AssertActivityNumberOfCalls(t, "PostStatementSimilarityActivity", 2)
	env.AssertActivityNumberOfCalls(t, "LLMReviewActivity", 1)
	env.AssertActivityNumberOfCalls(t, "StoreProblemActivity", 1)
	if storeCapture.input == nil {
		t.Fatal("successful retry did not reach Store")
	}
}

func TestProblemGenerationWorkflowCalibratesAndPersistsFinalResourceLimits(t *testing.T) {
	env, storeCapture := newProblemGenerationReviewTestEnvironment(
		t,
		sdkworkflow.Version(2),
		true,
		false,
		false,
		0,
		sdkworkflow.DefaultVersion,
		sdkworkflow.Version(1),
	)

	env.ExecuteWorkflow(ProblemGenerationWorkflow, reviewGateTestParams(false))
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("resource-calibrated workflow failed: %v", err)
	}
	env.AssertActivityNumberOfCalls(t, "RunSandboxActivity", 3)
	if storeCapture.input == nil {
		t.Fatal("resource-calibrated workflow did not reach Store")
	}
	if storeCapture.input.Params.TimeLimit != 4600 || storeCapture.input.Params.MemoryLimit != 256 {
		t.Fatalf("stored limits = %dms/%dMiB, want 4600ms/256MiB",
			storeCapture.input.Params.TimeLimit,
			storeCapture.input.Params.MemoryLimit,
		)
	}
	if storeCapture.input.SandboxOutput.Audit.RunID != "final-run" {
		t.Fatalf("stored sandbox output is not the final-limit rerun: %+v", storeCapture.input.SandboxOutput.Audit)
	}
}

func TestProblemGenerationWorkflowRepairsSolutionsWithBoundFailureContext(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.SetStartWorkflowOptions(client.StartWorkflowOptions{ID: "problem-generation-retry-feedback-test"})
	env.RegisterWorkflow(ProblemGenerationWorkflow)
	env.OnGetVersion(problemGenerationReviewGateV2ChangeID, sdkworkflow.DefaultVersion, 2).
		Return(sdkworkflow.DefaultVersion).
		Once()
	env.OnGetVersion(problemGenerationDifferentialV1ChangeID, sdkworkflow.DefaultVersion, 1).
		Return(sdkworkflow.Version(1)).
		Once()
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
	env.OnGetVersion(problemGenerationReviewRepairV1ChangeID, sdkworkflow.DefaultVersion, 1).
		Return(sdkworkflow.DefaultVersion).
		Once()
	env.OnGetVersion(problemGenerationResourceCalibrationV1ChangeID, sdkworkflow.DefaultVersion, 1).
		Return(sdkworkflow.DefaultVersion).
		Once()
	env.OnGetVersion(problemGenerationRetryFeedbackV1ChangeID, sdkworkflow.DefaultVersion, 1).
		Return(sdkworkflow.Version(1)).
		Once()
	env.OnGetVersion(problemGenerationReviewGateV1ChangeID, sdkworkflow.DefaultVersion, 1).
		Return(sdkworkflow.DefaultVersion).
		Once()
	env.OnGetVersion(problemGenerationStatementRepairV1ChangeID, sdkworkflow.DefaultVersion, 1).
		Return(sdkworkflow.DefaultVersion).
		Once()
	env.OnGetVersion(problemGenerationStatementQualityGateV1ChangeID, sdkworkflow.DefaultVersion, 1).
		Return(sdkworkflow.DefaultVersion).
		Once()

	acts := activities.New(nil)
	statement := &activities.StatementResult{
		Title:       "Retry feedback fixture",
		Statement:   "Read one integer n and print n.",
		OneLineHint: "Identity",
	}
	testData := &activities.TestDataResult{
		PayloadVersion: activities.ActivityPayloadVersion,
		TestCases: []activities.TestCaseData{{
			Input:       "7\n",
			GroupID:     1,
			IsSample:    true,
			Description: "single invocation",
		}},
	}
	badSolutions := &activities.SolutionResult{
		MainSolution:  domain.Solution{SolutionType: domain.SolutionTypeMain, Language: "cpp", SourceCode: "// bad main reads until EOF"},
		BruteSolution: domain.Solution{SolutionType: domain.SolutionTypeBrute, Language: "cpp", SourceCode: "// bad brute"},
	}
	goodSolutions := &activities.SolutionResult{
		MainSolution:  domain.Solution{SolutionType: domain.SolutionTypeMain, Language: "cpp", SourceCode: "// corrected main"},
		BruteSolution: domain.Solution{SolutionType: domain.SolutionTypeBrute, Language: "cpp", SourceCode: "// corrected brute"},
	}
	wrongMain := &activities.SandboxResult{PayloadVersion: activities.ActivityPayloadVersion, Outputs: []string{"7 8"}}
	right := &activities.SandboxResult{PayloadVersion: activities.ActivityPayloadVersion, Outputs: []string{"7"}}

	env.OnActivity(acts.GenerateStatementActivity, mock.Anything, mock.Anything).Return(statement, nil).Once()
	env.OnActivity(acts.CleanStatementActivity, mock.Anything, mock.Anything, mock.Anything).Return(statement, nil).Once()
	env.OnActivity(acts.PostStatementSimilarityActivity, mock.Anything, mock.Anything).
		Return(&activities.PostStatementSimilarityResult{}, nil).Once()
	env.OnActivity(acts.GenerateTestDataActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(testData, nil).Once()
	env.OnActivity(acts.GenerateSolutionActivity, mock.Anything, mock.Anything, mock.Anything).
		Return(badSolutions, nil).Once()
	env.OnActivity(acts.CompileCheckActivity, mock.Anything, mock.Anything).
		Return(&activities.CompileCheckResult{AllCompiled: true}, nil).Twice()
	env.OnActivity(acts.RunSandboxActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(wrongMain, nil).Once()
	env.OnActivity(acts.RunSandboxActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(right, nil).Once()
	env.OnActivity(acts.ValidateActivity, mock.Anything, mock.Anything, mock.Anything).
		Return(&activities.ValidationResult{Mismatches: []activities.Mismatch{{TestIndex: 0, MainOutput: "7 8", BruteOutput: "7"}}}, nil).Once()
	env.OnActivity(acts.AssessProblemFeasibilityActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&activities.FeasibilityResult{Feasible: true, Reason: "the output loop is incorrect"}, nil).Once()
	env.OnActivity(acts.RepairSolutionActivity, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			input := args.Get(1).(activities.GenerateSolutionRepairInput)
			if input.Feedback.FailureStage != "differential_validation" || len(input.Feedback.Mismatches) != 1 {
				t.Errorf("repair input omitted mismatch evidence: %+v", input.Feedback)
			}
			if input.Feedback.PreviousMainSource != badSolutions.MainSolution.SourceCode ||
				len(input.Feedback.FailingTestCases) != 1 || input.Feedback.FailingTestCases[0].Input != "7" {
				t.Errorf("repair input omitted previous source or exact failing input: %+v", input.Feedback)
			}
		}).
		Return(goodSolutions, nil).Once()
	env.OnActivity(acts.RunSandboxActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(right, nil).Twice()
	env.OnActivity(acts.ValidateActivity, mock.Anything, mock.Anything, mock.Anything).
		Return(&activities.ValidationResult{AllPassed: true}, nil).Once()
	env.OnActivity(acts.LLMReviewActivity, mock.Anything, mock.Anything).
		Return(&activities.ReviewResult{Approved: true, Confidence: 0.9, EstimatedDifficulty: reviewGateTestParams(false).Difficulty}, nil).Once()
	env.OnActivity(acts.StoreProblemActivity, mock.Anything, mock.Anything).
		Return(&activities.StoreResult{
			ProblemID:    uuid.MustParse("22222222-2222-2222-2222-222222222222"),
			SerialNumber: "AF-RETRY-1",
			Status:       domain.ProblemStatusDraft,
		}, nil).Once()

	params := reviewGateTestParams(false)
	params.TestDataConfig.NumSamples = 0
	env.ExecuteWorkflow(ProblemGenerationWorkflow, params)
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow failed instead of repairing the second solution attempt: %v", err)
	}
	env.AssertActivityNumberOfCalls(t, "GenerateSolutionActivity", 1)
	env.AssertActivityNumberOfCalls(t, "RepairSolutionActivity", 1)
}

func TestProblemGenerationWorkflowRejectedReviewContinuesAsNewWithRepairEvidence(t *testing.T) {
	env, _ := newProblemGenerationReviewTestEnvironment(
		t,
		sdkworkflow.Version(2),
		false,
		false,
		false,
		0,
		sdkworkflow.Version(1),
	)
	params := reviewGateTestParams(false)
	params.CustomPrompt = "Keep the requested graph topic."

	env.ExecuteWorkflow(ProblemGenerationWorkflow, params)
	workflowErr := env.GetWorkflowError()
	var continueErr *sdkworkflow.ContinueAsNewError
	if !errors.As(workflowErr, &continueErr) {
		t.Fatalf("workflow error = %v, want ContinueAsNewError", workflowErr)
	}
	if continueErr.WorkflowType == nil || continueErr.WorkflowType.Name != "ProblemGenerationWorkflow" {
		t.Fatalf("continue-as-new workflow type = %+v", continueErr.WorkflowType)
	}
	var nextParams domain.ProblemGenParams
	if err := converter.GetDefaultDataConverter().FromPayloads(continueErr.Input, &nextParams); err != nil {
		t.Fatalf("decode continue-as-new params: %v", err)
	}
	state, found, err := loadProblemGenerationReviewRepairStateV1(nextParams)
	if err != nil {
		t.Fatal(err)
	}
	if !found || state.RepairRounds != 1 {
		t.Fatalf("repair state = %+v, found=%v", state, found)
	}
	if !strings.Contains(nextParams.CustomPrompt, "Keep the requested graph topic.") ||
		!strings.Contains(nextParams.CustomPrompt, "fixture review issue") {
		t.Fatalf("next custom prompt did not preserve base prompt and feedback: %q", nextParams.CustomPrompt)
	}
	env.AssertActivityNumberOfCalls(t, "LLMReviewActivity", 1)
	env.AssertActivityNumberOfCalls(t, "StoreProblemActivity", 0)
}

func TestProblemGenerationWorkflowDifficultyMismatchTriggersRepair(t *testing.T) {
	env, _ := newProblemGenerationReviewTestEnvironment(
		t,
		sdkworkflow.Version(2),
		true,
		false,
		false,
		0,
		sdkworkflow.Version(1),
	)
	params := reviewGateTestParams(false)
	params.Difficulty = 2000

	env.ExecuteWorkflow(ProblemGenerationWorkflow, params)
	workflowErr := env.GetWorkflowError()
	var continueErr *sdkworkflow.ContinueAsNewError
	if !errors.As(workflowErr, &continueErr) {
		t.Fatalf("workflow error = %v, want ContinueAsNewError", workflowErr)
	}
	var nextParams domain.ProblemGenParams
	if err := converter.GetDefaultDataConverter().FromPayloads(continueErr.Input, &nextParams); err != nil {
		t.Fatalf("decode continue-as-new params: %v", err)
	}
	if !strings.Contains(nextParams.CustomPrompt, "difficulty mismatch: target=2000, estimated=1500") {
		t.Fatalf("repair prompt omitted difficulty mismatch: %q", nextParams.CustomPrompt)
	}
	env.AssertActivityNumberOfCalls(t, "StoreProblemActivity", 0)
}

func TestReviewTokenV1Stable(t *testing.T) {
	token, reviewHash, err := deriveReviewTokenV1("run-123", 1, activities.ReviewResult{
		Approved:            false,
		Issues:              []string{"issue"},
		Suggestions:         []string{"suggest"},
		Confidence:          0.9,
		EstimatedDifficulty: 1700,
	})
	if err != nil {
		t.Fatal(err)
	}
	if reviewHash != "af05dc60c5f83949b034ebb824c25be115534ee5c8c225ac12ccad7b36178612" {
		t.Fatalf("review result hash = %s", reviewHash)
	}
	if token != "f7f8d3b22fc13355f1386666b98a785911118df14a627d17cbd1755b62a969b0" {
		t.Fatalf("review token = %s", token)
	}
}

type reviewGateStoreCapture struct {
	input *activities.StoreInput
}

func newProblemGenerationReviewTestEnvironment(
	t *testing.T,
	reviewGateVersion sdkworkflow.Version,
	llmApproved bool,
	llmIsDuplicate bool,
	wantQuarantine bool,
	finalSimilarityHardRejects int,
	reviewRepairVersions ...sdkworkflow.Version,
) (*testsuite.TestWorkflowEnvironment, *reviewGateStoreCapture) {
	t.Helper()

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.SetStartWorkflowOptions(client.StartWorkflowOptions{ID: "problem-generation-review-gate-test"})
	env.RegisterWorkflow(ProblemGenerationWorkflow)
	v2Version := sdkworkflow.DefaultVersion
	if reviewGateVersion >= 2 {
		v2Version = 2
	}
	env.OnGetVersion(problemGenerationReviewGateV2ChangeID, sdkworkflow.DefaultVersion, 2).
		Return(v2Version).
		Once()
	env.OnGetVersion(problemGenerationDifferentialV1ChangeID, sdkworkflow.DefaultVersion, 1).
		Return(sdkworkflow.Version(1)).
		Once()
	env.OnGetVersion(problemGenerationStructuredSamplesV1ChangeID, sdkworkflow.DefaultVersion, 1).
		Return(sdkworkflow.Version(1)).
		Once()
	env.OnGetVersion(problemGenerationStructuredSamplesV2ChangeID, sdkworkflow.DefaultVersion, 1).
		Return(sdkworkflow.Version(1)).
		Once()
	env.OnGetVersion(problemGenerationTestManifestV1ChangeID, sdkworkflow.DefaultVersion, 1).
		Return(sdkworkflow.Version(1)).
		Once()
	env.OnGetVersion(problemGenerationFinalStatementSimilarityV1ChangeID, sdkworkflow.DefaultVersion, 1).
		Return(sdkworkflow.Version(1)).
		Once()
	reviewRepairVersion := sdkworkflow.DefaultVersion
	if len(reviewRepairVersions) > 0 {
		reviewRepairVersion = reviewRepairVersions[0]
	}
	env.OnGetVersion(problemGenerationReviewRepairV1ChangeID, sdkworkflow.DefaultVersion, 1).
		Return(reviewRepairVersion).
		Once()
	resourceCalibrationVersion := sdkworkflow.DefaultVersion
	if len(reviewRepairVersions) > 1 {
		resourceCalibrationVersion = reviewRepairVersions[1]
	}
	env.OnGetVersion(problemGenerationResourceCalibrationV1ChangeID, sdkworkflow.DefaultVersion, 1).
		Return(resourceCalibrationVersion).
		Once()
	env.OnGetVersion(problemGenerationStatementRepairV1ChangeID, sdkworkflow.DefaultVersion, 1).
		Return(sdkworkflow.DefaultVersion).
		Once()
	env.OnGetVersion(problemGenerationStatementQualityGateV1ChangeID, sdkworkflow.DefaultVersion, 1).
		Return(sdkworkflow.DefaultVersion).
		Once()
	if !llmIsDuplicate && reviewGateVersion < 2 {
		legacyVersion := reviewGateVersion
		if reviewGateVersion >= 1 {
			legacyVersion = 1
		}
		env.OnGetVersion(problemGenerationReviewGateV1ChangeID, sdkworkflow.DefaultVersion, 1).
			Return(legacyVersion).
			Once()
	}

	acts := activities.New(nil)
	statement := &activities.StatementResult{
		Title:       "Review gate fixture",
		Statement:   "Given one integer, print it.\n\n" + activities.StatementSamplesPlaceholder,
		Tags:        []string{"dp"},
		OneLineHint: "Identity",
	}
	solutions := &activities.SolutionResult{
		MainSolution: domain.Solution{
			SolutionType: domain.SolutionTypeMain,
			Language:     "cpp",
			SourceCode:   "int main() {}",
		},
		BruteSolution: domain.Solution{
			SolutionType: domain.SolutionTypeBrute,
			Language:     "cpp",
			SourceCode:   "int main() {}",
		},
	}
	testData := &activities.TestDataResult{
		PayloadVersion: activities.ActivityPayloadVersion,
		TestCases: []activities.TestCaseData{{
			Input:       "1\n",
			GroupID:     1,
			IsSample:    true,
			Description: "fixture",
			Origin:      activities.TestCaseOriginLLMInline,
		}},
	}
	sandboxResult := &activities.SandboxResult{
		PayloadVersion: activities.ActivityPayloadVersion,
		Outputs:        []string{"1\n"},
	}
	benchmarkLimits := problemResourceBenchmarkLimitsV1()
	benchmarkSandboxResult := &activities.SandboxResult{
		PayloadVersion: activities.ActivityPayloadVersion,
		Outputs:        []string{"1\n"},
		TimeTaken:      []time.Duration{1501 * time.Millisecond},
		MemoryUsed:     []int64{100 << 20},
		Audit:          sandboxReceiptFixtureV1("benchmark", benchmarkLimits),
	}
	calibratedLimits := activities.ExecutionLimits{TimeLimitMs: 4600, MemoryLimitMB: 256}
	calibratedSandboxResult := &activities.SandboxResult{
		PayloadVersion: activities.ActivityPayloadVersion,
		Outputs:        []string{"1\n"},
		TimeTaken:      []time.Duration{1400 * time.Millisecond},
		MemoryUsed:     []int64{99 << 20},
		Audit:          sandboxReceiptFixtureV1("final", calibratedLimits),
	}
	calibratedBruteLimits := problemResourceBruteLimitsV1(calibratedLimits)
	calibratedBruteResult := &activities.SandboxResult{
		PayloadVersion: activities.ActivityPayloadVersion,
		Outputs:        []string{"1\n"},
		TimeTaken:      []time.Duration{10 * time.Millisecond},
		MemoryUsed:     []int64{8 << 20},
		Audit:          sandboxReceiptFixtureV1("brute", calibratedBruteLimits),
	}
	manifest := &activities.TestManifestV1{
		SchemaVersion:            activities.TestManifestSchemaVersion,
		ComparisonMode:           activities.TestManifestComparisonMode,
		TestCount:                1,
		DifferentialCheckedCount: 1,
		MainSolutionSHA256:       strings.Repeat("b", 64),
		BruteSolutionSHA256:      strings.Repeat("c", 64),
		MainSandbox: activities.TestManifestSandboxIdentity{
			ManifestDigest:          "sha256:" + strings.Repeat("d", 64),
			ImageDigest:             "sha256:" + strings.Repeat("e", 64),
			LimitProfile:            "workflow-fixture-main",
			ToolchainManifestDigest: "sha256:" + strings.Repeat("5", 64),
			SeccompPolicyDigest:     "sha256:" + strings.Repeat("6", 64),
		},
		BruteSandbox: activities.TestManifestSandboxIdentity{
			ManifestDigest:          "sha256:" + strings.Repeat("f", 64),
			ImageDigest:             "sha256:" + strings.Repeat("1", 64),
			LimitProfile:            "workflow-fixture-brute",
			ToolchainManifestDigest: "sha256:" + strings.Repeat("7", 64),
			SeccompPolicyDigest:     "sha256:" + strings.Repeat("8", 64),
		},
		Cases: []activities.TestManifestCase{{
			TestIndex:            0,
			GroupID:              1,
			IsSample:             true,
			Purpose:              "fixture",
			Origin:               activities.TestCaseOriginLLMInline,
			InputSHA256:          strings.Repeat("2", 64),
			ExpectedOutputSHA256: strings.Repeat("3", 64),
			DifferentialChecked:  true,
			DifferentialMatch:    true,
			BruteOutputSHA256:    strings.Repeat("4", 64),
		}},
	}
	finalizedStatement := *statement
	finalizedStatement.Statement = "Given one integer, print it.\n\n### Samples\n\n#### Sample 1\n\n```text\n1\n```\n\nTime limit: 4600 ms\nMemory limit: 256 MB"

	pipelineAttempts := finalSimilarityHardRejects + 1
	env.OnActivity(acts.GenerateStatementActivity, mock.Anything, mock.Anything).
		Return(statement, nil).
		Times(pipelineAttempts)
	env.OnActivity(acts.CleanStatementActivity, mock.Anything, mock.Anything, mock.Anything).
		Return(statement, nil).
		Times(pipelineAttempts)
	assertFinalSimilarityInput := func(args mock.Arguments) {
		input := args.Get(1).(activities.StatementResult)
		if input.Statement != finalizedStatement.Statement {
			t.Errorf("final-statement similarity received %q, want %q", input.Statement, finalizedStatement.Statement)
		}
	}
	for i := 0; i < finalSimilarityHardRejects; i++ {
		env.OnActivity(acts.PostStatementSimilarityActivity, mock.Anything, mock.Anything).
			Run(assertFinalSimilarityInput).
			Return(&activities.PostStatementSimilarityResult{
				MaxSimilarity: 1,
				HardReject:    true,
			}, nil).
			Once()
	}
	env.OnActivity(acts.PostStatementSimilarityActivity, mock.Anything, mock.Anything).
		Run(assertFinalSimilarityInput).
		Return(&activities.PostStatementSimilarityResult{}, nil).
		Once()
	env.OnActivity(acts.GenerateTestDataActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(testData, nil).
		Times(pipelineAttempts)
	env.OnActivity(acts.GenerateSolutionActivity, mock.Anything, mock.Anything, mock.Anything).
		Return(solutions, nil).
		Times(pipelineAttempts)
	env.OnActivity(acts.CompileCheckActivity, mock.Anything, mock.Anything).
		Return(&activities.CompileCheckResult{AllCompiled: true}, nil).
		Times(pipelineAttempts)
	if resourceCalibrationVersion >= 1 {
		env.OnActivity(acts.RunSandboxActivity, mock.Anything, mock.Anything, mock.Anything, benchmarkLimits).
			Return(benchmarkSandboxResult, nil).
			Times(pipelineAttempts)
		env.OnActivity(acts.RunSandboxActivity, mock.Anything, mock.Anything, mock.Anything, calibratedLimits).
			Return(calibratedSandboxResult, nil).
			Times(pipelineAttempts)
		env.OnActivity(acts.RunSandboxActivity, mock.Anything, mock.Anything, mock.Anything, calibratedBruteLimits).
			Return(calibratedBruteResult, nil).
			Times(pipelineAttempts)
	} else {
		env.OnActivity(acts.RunSandboxActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(sandboxResult, nil).
			Times(2 * pipelineAttempts)
	}
	env.OnActivity(acts.ValidateActivity, mock.Anything, mock.Anything, mock.Anything).
		Return(&activities.ValidationResult{AllPassed: true}, nil).
		Times(pipelineAttempts)
	env.OnActivity(acts.BuildTestManifestActivity, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			input := args.Get(1).(activities.BuildTestManifestInput)
			if len(input.TestCases) != 1 || len(input.BruteIndices) != 1 || input.BruteIndices[0] != 0 {
				t.Errorf("unexpected test manifest input: %+v", input)
			}
			if input.MainSolution.SourceCode != solutions.MainSolution.SourceCode || input.BruteSolution.SourceCode != solutions.BruteSolution.SourceCode {
				t.Errorf("test manifest received different solutions: %+v", input)
			}
		}).
		Return(manifest, nil).
		Once()
	env.OnActivity(acts.FinalizeStatementSamplesActivity, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			input := args.Get(1).(activities.FinalizeStatementSamplesInput)
			if input.Statement.Statement != statement.Statement || len(input.TestCases) != 1 || input.SandboxOutput.Outputs[0] != "1\n" {
				t.Errorf("unexpected structured sample input: %+v", input)
			}
			if !input.EnforceSampleCount || input.ExpectedSampleCount != 1 {
				t.Errorf("unexpected structured sample v2 count contract: %+v", input)
			}
		}).
		Return(&finalizedStatement, nil).
		Times(pipelineAttempts)
	reviewSource := &activities.ArtifactRef{
		SchemaVersion:  activities.ArtifactRefSchemaVersion,
		PayloadVersion: activities.ActivityPayloadVersion,
		Bucket:         "review-fixture",
		Key:            "workflow-artifacts/v1/sha256/review",
		SHA256:         strings.Repeat("a", 64),
		SizeBytes:      128,
		ContentType:    "application/json",
		Producer:       "llm-review",
		Provider:       "fixture-provider",
		Model:          "fixture-model",
		ModelRevision:  "fixture-revision",
		WorkflowID:     "problem-generation-review-gate-test",
	}
	env.OnActivity(acts.LLMReviewActivity, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			input := args.Get(1).(activities.LLMReviewInput)
			if input.Statement.Statement != finalizedStatement.Statement {
				t.Errorf("review received unfinalized statement: %q", input.Statement.Statement)
			}
			if resourceCalibrationVersion >= 1 {
				if input.ResourceCalibration == nil || input.ResourceCalibration.FinalAudit.RunID != "final-run" {
					t.Errorf("review omitted finalized resource calibration: %+v", input.ResourceCalibration)
				}
				if input.Params.TimeLimit != calibratedLimits.TimeLimitMs || input.Params.MemoryLimit != calibratedLimits.MemoryLimitMB {
					t.Errorf("review params did not use calibrated limits: %+v", input.Params)
				}
			}
		}).
		Return(&activities.ReviewResult{
			SourceArtifacts:     []*activities.ArtifactRef{reviewSource},
			Approved:            llmApproved,
			Confidence:          0.9,
			Issues:              []string{"fixture review issue"},
			Suggestions:         []string{"fix the issue"},
			EstimatedDifficulty: 1500,
			IsDuplicate:         llmIsDuplicate,
			DuplicateOf:         "existing fixture",
			DuplicateReason:     "same construction",
			FullText:            `{"approved":false,"issues":["fixture review issue"],"review_details":{"clarity":{"score":4,"notes":"ambiguous"}}}`,
		}, nil).
		Once()
	storeCapture := &reviewGateStoreCapture{}
	storeStatus := domain.ProblemStatusDraft
	quarantineReason := ""
	if wantQuarantine {
		storeStatus = domain.ProblemStatusQuarantined
		quarantineReason = reviewQuarantineReason
	}
	env.OnActivity(acts.StoreProblemActivity, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			input := args.Get(1).(activities.StoreInput)
			storeCapture.input = &input
			if input.Statement.Statement != finalizedStatement.Statement {
				t.Errorf("store received unfinalized statement: %q", input.Statement.Statement)
			}
			if resourceCalibrationVersion >= 1 {
				if input.Params.TimeLimit != calibratedLimits.TimeLimitMs || input.Params.MemoryLimit != calibratedLimits.MemoryLimitMB {
					t.Errorf("store params did not use calibrated limits: %+v", input.Params)
				}
				if _, ok := input.Params.MetadataExtras[problemGenerationResourceCalibrationStateKeyV1]; !ok {
					t.Errorf("store params omitted resource calibration evidence: %+v", input.Params.MetadataExtras)
				}
			}
		}).
		Return(&activities.StoreResult{
			ProblemID:        uuid.MustParse("11111111-1111-1111-1111-111111111111"),
			SerialNumber:     "AF-REVIEW-1",
			Status:           storeStatus,
			QuarantineReason: quarantineReason,
			StandardEvidence: &domain.GenerationStandardEvidenceReference{
				SchemaVersion: domain.GenerationStandardEvidenceSchemaV1,
				SHA256:        strings.Repeat("c", 64),
				Path:          "problems/11111111-1111-1111-1111-111111111111/generation_standard_evidence.v1.json",
			},
		}, nil).
		Maybe()

	return env, storeCapture
}

func assertCurrentManifestStoreInput(t *testing.T, input *activities.StoreInput) {
	t.Helper()
	if input.PayloadVersion != activities.StoreProblemTestManifestPayloadVersion {
		t.Fatalf("Store payload version = %d, want %d", input.PayloadVersion, activities.StoreProblemTestManifestPayloadVersion)
	}
	if input.TestManifest == nil || input.TestManifest.SchemaVersion != activities.TestManifestSchemaVersion {
		t.Fatalf("Store omitted current test manifest: %+v", input.TestManifest)
	}
	if err := input.TestManifest.Validate(len(input.TestCases)); err != nil {
		t.Fatalf("Store received invalid current test manifest: %v", err)
	}
	if len(input.TestManifest.Cases) != len(input.TestCases) || !input.TestManifest.Cases[0].DifferentialChecked {
		t.Fatalf("Store test manifest coverage = %+v", input.TestManifest)
	}
}

func assertV2ReviewQuarantineInput(t *testing.T, input *activities.StoreInput) {
	t.Helper()
	if input == nil || input.ReviewQuarantine == nil {
		t.Fatal("v2 rejection did not send review quarantine evidence to Store")
	}
	evidence := input.ReviewQuarantine
	if evidence.ReviewGateChangeID != problemGenerationReviewGateV2ChangeID || evidence.ReviewGateVersion != 2 {
		t.Fatalf("review gate lineage = %q/%d", evidence.ReviewGateChangeID, evidence.ReviewGateVersion)
	}
	if input.PayloadVersion != activities.StoreProblemTestManifestPayloadVersion {
		t.Fatalf("v2 review Store payload version = %d, want current manifest payload %d", input.PayloadVersion, activities.StoreProblemTestManifestPayloadVersion)
	}
	if evidence.WorkflowRunID == "" || evidence.Reason != reviewQuarantineReason || evidence.ReviewResult.FullText == "" {
		t.Fatalf("incomplete quarantine evidence: %+v", evidence)
	}
	encoded, digest, err := activities.CanonicalReviewResultJSON(evidence.ReviewResult)
	if err != nil {
		t.Fatal(err)
	}
	if digest != evidence.ReviewResultSHA256 {
		t.Fatalf("review hash = %s, want %s", evidence.ReviewResultSHA256, digest)
	}
	for _, field := range []string{`"approved"`, `"issues"`, `"suggestions"`, `"confidence"`, `"estimated_difficulty"`, `"is_duplicate"`, `"duplicate_of"`, `"duplicate_reason"`, `"full_text"`} {
		if !strings.Contains(string(encoded), field) {
			t.Fatalf("canonical review JSON omitted %s: %s", field, encoded)
		}
	}
	if len(evidence.SourceAncestry) != 1 || evidence.SourceAncestry[0].ModelRevision != "fixture-revision" {
		t.Fatalf("review source ancestry = %+v", evidence.SourceAncestry)
	}
}

func reviewGateTestParams(requireReview bool) domain.ProblemGenParams {
	params := domain.DefaultProblemGenParams()
	params.Tags = []string{"dp"}
	params.SimilarLimit = 0
	params.RequireReview = requireReview
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

func TestProblemGenerationWorkflowKnowledgePointCombinationUsesStoreV4(t *testing.T) {
	env, storeCapture := newProblemGenerationReviewTestEnvironment(
		t,
		sdkworkflow.Version(2),
		true,
		false,
		false,
		0,
	)
	params := reviewGateTestParams(false)
	params.KnowledgePointCombination = &domain.KnowledgePointCombinationContract{
		SchemaVersion: domain.KnowledgePointCombinationSchemaV1,
		Mode:          domain.KnowledgePointCombinationSingle,
		MaxConcepts:   1,
	}

	env.ExecuteWorkflow(ProblemGenerationWorkflow, params)
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow failed: %v", err)
	}
	if storeCapture.input == nil {
		t.Fatal("knowledge-point workflow did not reach Store")
	}
	if storeCapture.input.PayloadVersion != activities.StoreProblemKnowledgePointCombinationPayloadVersion {
		t.Fatalf("Store payload version = %d, want %d", storeCapture.input.PayloadVersion, activities.StoreProblemKnowledgePointCombinationPayloadVersion)
	}
	if storeCapture.input.Params.KnowledgePointCombination == nil {
		t.Fatal("Store input lost the knowledge-point combination contract")
	}
	if storeCapture.input.TestManifest == nil {
		t.Fatal("Store v4 input lost the test manifest")
	}
}

func TestProblemGenerationWorkflowStandardEvidenceUsesStoreV5(t *testing.T) {
	env, storeCapture := newProblemGenerationReviewTestEnvironment(
		t,
		sdkworkflow.Version(2),
		true,
		false,
		false,
		0,
	)
	params := reviewGateTestParams(false)
	params.KnowledgePointCombination = &domain.KnowledgePointCombinationContract{
		SchemaVersion: domain.KnowledgePointCombinationSchemaV1,
		Mode:          domain.KnowledgePointCombinationSingle,
		MaxConcepts:   1,
	}
	params.GenerationEvidence = &domain.GenerationEvidenceContract{
		SchemaVersion:                   domain.GenerationEvidenceContractSchemaV1,
		EvidenceLevel:                   domain.GenerationStandardEvidenceLevel,
		JobContractVersion:              "algoforge.generation-job.v1",
		GenerationArm:                   "A",
		GenerationArmMode:               "baseline_passthrough",
		GenerationBehavior:              "unchanged",
		ReviewerProfile:                 "algoforge.reviewer.v1",
		ReviewerProfileDescriptorSHA256: strings.Repeat("a", 64),
		EvidenceProfile:                 "algoforge.review-evidence.v0",
		EvidenceProfileDescriptorSHA256: strings.Repeat("b", 64),
		OutcomeTaxonomy:                 "algoforge.generation-outcome-taxonomy.v1",
		IncludeEditorial:                true,
		IncludeSolutions:                true,
		IncludeTestData:                 true,
	}

	env.ExecuteWorkflow(ProblemGenerationWorkflow, params)
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow failed: %v", err)
	}
	if storeCapture.input == nil {
		t.Fatal("standard evidence workflow did not reach Store")
	}
	if storeCapture.input.PayloadVersion != activities.StoreProblemStandardEvidencePayloadVersion {
		t.Fatalf("Store payload version = %d, want %d", storeCapture.input.PayloadVersion, activities.StoreProblemStandardEvidencePayloadVersion)
	}
	if storeCapture.input.Params.GenerationEvidence == nil || storeCapture.input.TestManifest == nil || storeCapture.input.Params.KnowledgePointCombination == nil {
		t.Fatalf("Store v5 lost a required contract: %+v", storeCapture.input)
	}
}

func queryReviewGateState(env *testsuite.TestWorkflowEnvironment) (domain.WorkflowStateQuery, error) {
	value, err := env.QueryWorkflow(domain.WorkflowStateQueryName)
	if err != nil {
		return domain.WorkflowStateQuery{}, fmt.Errorf("query workflow state: %w", err)
	}
	var query domain.WorkflowStateQuery
	if err := value.Get(&query); err != nil {
		return domain.WorkflowStateQuery{}, fmt.Errorf("decode workflow state query: %w", err)
	}
	return query, nil
}

func assertWaitingReviewQuery(
	query domain.WorkflowStateQuery,
	version sdkworkflow.Version,
	llmApproved bool,
) error {
	if query.State.Status != domain.WorkflowStatusWaitingReview {
		return fmt.Errorf("query status = %q, want %q", query.State.Status, domain.WorkflowStatusWaitingReview)
	}
	if query.State.CurrentStep != domain.StepHumanReview {
		return fmt.Errorf("query step = %q, want %q", query.State.CurrentStep, domain.StepHumanReview)
	}
	if query.ReviewRequest == nil {
		return fmt.Errorf("query omitted review request")
	}
	if query.ReviewRequest.ReviewResult.Approved != llmApproved {
		return fmt.Errorf("query review approved = %t, want %t", query.ReviewRequest.ReviewResult.Approved, llmApproved)
	}
	if version >= 1 {
		if !query.ReviewRequest.TokenRequired || query.ReviewRequest.ReviewAttempt != 1 || len(query.ReviewRequest.Token) != 64 {
			return fmt.Errorf("unexpected v1 review request: %+v", query.ReviewRequest)
		}
	} else if query.ReviewRequest.TokenRequired || query.ReviewRequest.ReviewAttempt != 0 {
		return fmt.Errorf("unexpected default-version review request: %+v", query.ReviewRequest)
	}
	return nil
}

func reviewSignalForVersion(
	version sdkworkflow.Version,
	request *domain.ReviewRequest,
	decision domain.ReviewDecision,
) domain.ReviewSignal {
	signal := domain.ReviewSignal{Decision: decision}
	if version >= 1 {
		signal.Token = request.Token
	}
	return signal
}

func TestProblemGenerationWorkflowDifferentialGateFailsClosedOnEmptySelection(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(ProblemGenerationWorkflow)
	env.OnGetVersion(problemGenerationReviewGateV2ChangeID, sdkworkflow.DefaultVersion, 2).
		Return(sdkworkflow.Version(2)).
		Once()
	env.OnGetVersion(problemGenerationDifferentialV1ChangeID, sdkworkflow.DefaultVersion, 1).
		Return(sdkworkflow.Version(1)).
		Once()
	env.OnGetVersion(problemGenerationStructuredSamplesV1ChangeID, sdkworkflow.DefaultVersion, 1).
		Return(sdkworkflow.Version(1)).
		Once()
	env.OnGetVersion(problemGenerationStructuredSamplesV2ChangeID, sdkworkflow.DefaultVersion, 1).
		Return(sdkworkflow.Version(1)).
		Once()
	env.OnGetVersion(problemGenerationTestManifestV1ChangeID, sdkworkflow.DefaultVersion, 1).
		Return(sdkworkflow.Version(1)).
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

	acts := activities.New(nil)
	statement := &activities.StatementResult{
		Title:       "Differential gate fixture",
		Statement:   "Given one integer, print it.\n\n" + activities.StatementSamplesPlaceholder,
		OneLineHint: "Identity",
	}
	env.OnActivity(acts.GenerateStatementActivity, mock.Anything, mock.Anything).
		Return(statement, nil).
		Once()
	env.OnActivity(acts.CleanStatementActivity, mock.Anything, mock.Anything, mock.Anything).
		Return(statement, nil).
		Once()
	env.OnActivity(acts.PostStatementSimilarityActivity, mock.Anything, mock.Anything).
		Return(&activities.PostStatementSimilarityResult{}, nil).
		Once()
	env.OnActivity(acts.GenerateTestDataActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&activities.TestDataResult{
			PayloadVersion: activities.ActivityPayloadVersion,
			TestCases: []activities.TestCaseData{{
				Input:   "1\n",
				GroupID: 1,
			}},
		}, nil).
		Once()

	params := domain.DefaultProblemGenParams()
	params.SimilarLimit = 0
	params.GenerateEditorial = false
	params.TestDataConfig = domain.TestDataConfig{
		NumTestCases: 1,
		Groups: []domain.TestGroup{{
			GroupID:    2,
			NumCases:   1,
			Score:      100,
			BruteCheck: true,
		}},
	}
	env.ExecuteWorkflow(ProblemGenerationWorkflow, params)

	workflowErr := env.GetWorkflowError()
	var applicationErr *temporal.ApplicationError
	if !errors.As(workflowErr, &applicationErr) {
		t.Fatalf("workflow error = %T %v, want Temporal application error", workflowErr, workflowErr)
	}
	if applicationErr.Type() != "QualityNotMet" || !applicationErr.NonRetryable() ||
		!strings.Contains(applicationErr.Error(), "selected no test cases") {
		t.Fatalf("application error = %v, type = %q, non-retryable = %t", applicationErr, applicationErr.Type(), applicationErr.NonRetryable())
	}
	for _, activityName := range []string{"GenerateSolutionActivity", "CompileCheckActivity", "RunSandboxActivity", "ValidateActivity", "BuildTestManifestActivity", "FinalizeStatementSamplesActivity", "LLMReviewActivity", "StoreProblemActivity"} {
		env.AssertActivityNumberOfCalls(t, activityName, 0)
	}
}

func TestSelectBruteCheckCasesDefaultsToSamplesAndSmallInlineInputs(t *testing.T) {
	cases := []activities.TestCaseData{
		{Input: "1\n", IsSample: true},
		{Input: "2\n"},
		{InputRef: "/tmp/large.in"},
		{Input: makeString(5000)},
	}

	selected, indices := selectBruteCheckCases(cases, domain.TestDataConfig{})
	if len(selected) != 2 {
		t.Fatalf("expected 2 selected brute cases, got %d", len(selected))
	}
	if indices[0] != 0 || indices[1] != 1 {
		t.Fatalf("expected indices [0 1], got %v", indices)
	}
}

func TestSelectBruteCheckCasesHonorsExplicitBruteGroups(t *testing.T) {
	cases := []activities.TestCaseData{
		{InputRef: "/tmp/large-1.in", GroupID: 1},
		{InputRef: "/tmp/large-2.in", GroupID: 2},
	}
	cfg := domain.TestDataConfig{
		Groups: []domain.TestGroup{
			{GroupID: 2, BruteCheck: true},
		},
	}

	selected, indices := selectBruteCheckCases(cases, cfg)
	if len(selected) != 1 {
		t.Fatalf("expected 1 selected brute case, got %d", len(selected))
	}
	if selected[0].GroupID != 2 || indices[0] != 1 {
		t.Fatalf("expected group 2 at index 1, got group %d indices %v", selected[0].GroupID, indices)
	}
}

func TestSelectBruteCheckCasesV1IncludesSamplesInSourceOrderWithoutDuplicates(t *testing.T) {
	cases := []activities.TestCaseData{
		{Input: "sample-and-brute\n", GroupID: 2, IsSample: true},
		{Input: "sample-only\n", GroupID: 1, IsSample: true},
		{Input: "brute-only\n", GroupID: 2},
		{Input: "neither\n", GroupID: 1},
	}
	cfg := domain.TestDataConfig{
		Groups: []domain.TestGroup{{GroupID: 2, BruteCheck: true}},
	}

	selected, indices := selectBruteCheckCasesV1(cases, cfg)
	if len(selected) != 3 {
		t.Fatalf("expected 3 selected brute cases, got %d", len(selected))
	}
	wantIndices := []int{0, 1, 2}
	for i, want := range wantIndices {
		if indices[i] != want || selected[i].Input != cases[want].Input {
			t.Fatalf("selection[%d] = index %d input %q, want index %d input %q", i, indices[i], selected[i].Input, want, cases[want].Input)
		}
	}
}

func TestSelectBruteCheckCasesV1PreservesLegacyFallback(t *testing.T) {
	cases := []activities.TestCaseData{
		{Input: "sample\n", IsSample: true},
		{Input: "small-inline\n"},
		{InputArtifact: &activities.ArtifactRef{}},
	}

	legacySelected, legacyIndices := selectBruteCheckCases(cases, domain.TestDataConfig{})
	v1Selected, v1Indices := selectBruteCheckCasesV1(cases, domain.TestDataConfig{})
	if fmt.Sprint(v1Indices) != fmt.Sprint(legacyIndices) || len(v1Selected) != len(legacySelected) {
		t.Fatalf("v1 fallback indices=%v count=%d, want legacy indices=%v count=%d", v1Indices, len(v1Selected), legacyIndices, len(legacySelected))
	}
}

func TestSelectBruteCheckCasesV1RejectsOversizedSamplesAndGroups(t *testing.T) {
	large := activities.TestCaseData{
		InputArtifact: &activities.ArtifactRef{SizeBytes: activities.MaxReferenceDifferentialInputBytes + 1},
		GroupID:       2,
		IsSample:      true,
	}
	cases := []activities.TestCaseData{
		large,
		{Input: "small\n", GroupID: 2},
		{Input: "also-small\n", GroupID: 1, IsSample: true},
	}
	selected, indices := selectBruteCheckCasesV1(cases, domain.TestDataConfig{
		Groups: []domain.TestGroup{{GroupID: 2, BruteCheck: true}},
	})
	if fmt.Sprint(indices) != "[1 2]" || len(selected) != 2 {
		t.Fatalf("oversized case was admitted: indices=%v selected=%+v", indices, selected)
	}
}

func TestSelectBruteCheckCasesV2DoesNotTreatShortScalarAsSmall(t *testing.T) {
	cases := []activities.TestCaseData{
		{Input: "1\n", IsSample: true},
		// This is only 17 bytes, but its semantic value is near the maximum
		// scale for an enumerating reference implementation.
		{Input: "100000000000000\n", GroupID: 2},
		{Input: "999999999999999999\n", GroupID: 3},
	}
	selected, indices := selectBruteCheckCasesV2(cases, domain.TestDataConfig{})
	if fmt.Sprint(indices) != "[0]" || len(selected) != 1 || selected[0].Input != "1\n" {
		t.Fatalf("short scalar cases were admitted to brute subset: indices=%v selected=%+v", indices, selected)
	}
}

func TestSelectBruteCheckCasesV2UsesOnlyExplicitGroups(t *testing.T) {
	cases := []activities.TestCaseData{
		{Input: "sample\n", GroupID: 1, IsSample: true},
		{Input: "small-brute\n", GroupID: 2},
		{Input: "other\n", GroupID: 3, IsSample: true},
	}
	selected, indices := selectBruteCheckCasesV2(cases, domain.TestDataConfig{
		Groups: []domain.TestGroup{{GroupID: 2, BruteCheck: true}},
	})
	if fmt.Sprint(indices) != "[1]" || len(selected) != 1 || selected[0].GroupID != 2 {
		t.Fatalf("non-BruteCheck samples/groups were admitted: indices=%v selected=%+v", indices, selected)
	}
}

func TestStructuredSamplesVersionRoutingPreservesV1AndEnablesV2ZeroSamples(t *testing.T) {
	tests := []struct {
		name        string
		v1          sdkworkflow.Version
		v2          sdkworkflow.Version
		sampleCount int
		want        bool
	}{
		{name: "pre-v1", v1: sdkworkflow.DefaultVersion, v2: sdkworkflow.DefaultVersion, sampleCount: 1, want: false},
		{name: "v1 zero samples", v1: 1, v2: sdkworkflow.DefaultVersion, sampleCount: 0, want: false},
		{name: "v1 positive samples", v1: 1, v2: sdkworkflow.DefaultVersion, sampleCount: 1, want: true},
		{name: "v2 zero samples", v1: 1, v2: 1, sampleCount: 0, want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := structuredSamplesEnabled(test.v1, test.v2, test.sampleCount); got != test.want {
				t.Fatalf("structuredSamplesEnabled(%d,%d,%d) = %t, want %t", test.v1, test.v2, test.sampleCount, got, test.want)
			}
		})
	}
}

func makeString(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'x'
	}
	return string(b)
}

func TestMergeNeighborsDeduplicatesByID(t *testing.T) {
	id1 := uuid.New()
	id2 := uuid.New()

	merged := mergeNeighbors(
		[]activities.NeighborInfo{{ID: id1, Title: "A"}},
		[]activities.NeighborInfo{{ID: id1, Title: "A again"}, {ID: id2, Title: "B"}},
	)

	if len(merged) != 2 {
		t.Fatalf("expected 2 merged neighbors, got %d", len(merged))
	}
	if merged[0].ID != id1 || merged[1].ID != id2 {
		t.Fatalf("unexpected merge order or IDs: %+v", merged)
	}
}
