package workflow

import (
	"errors"
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/workflow/activities"
	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/testsuite"
)

func TestProblemGenerationAuthoringWorkflowV1AcceptedTerminatesAfterOneActivity(t *testing.T) {
	env, acts, input := newAuthoringWorkflowEnvironment(t)
	want := acceptedAuthoringWorkflowResult(t, input)
	env.OnActivity(acts.GenerateAuthoringPlanActivity, mock.Anything, input).
		Return(want, nil).
		Once()

	env.ExecuteWorkflow(ProblemGenerationAuthoringWorkflowV1, input)
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("authoring workflow failed: %v", err)
	}
	var got activities.GenerateAuthoringPlanResult
	if err := env.GetWorkflowResult(&got); err != nil {
		t.Fatalf("decode authoring workflow result: %v", err)
	}
	if got.Decision != activities.AuthoringPlanDecisionAccepted || got.BundleSHA256 != want.BundleSHA256 {
		t.Fatalf("unexpected accepted result: %+v", got)
	}
	assertAuthoringWorkflowCallsOnlyFormalizer(t, env)
}

func TestProblemGenerationAuthoringWorkflowV1RejectedOutcomesTerminateWithoutLegacyFallback(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		result func(*testing.T, activities.GenerateAuthoringPlanInput) *activities.GenerateAuthoringPlanResult
		gate   string
	}{
		{name: "model rejected", result: modelRejectedAuthoringWorkflowResult, gate: activities.AuthoringPlanGateReasonModelRejected},
		{name: "spec lint failed", result: lintRejectedAuthoringWorkflowResult, gate: activities.AuthoringPlanGateReasonSpecLintFailed},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			env, acts, input := newAuthoringWorkflowEnvironment(t)
			want := testCase.result(t, input)
			env.OnActivity(acts.GenerateAuthoringPlanActivity, mock.Anything, input).
				Return(want, nil).
				Once()

			env.ExecuteWorkflow(ProblemGenerationAuthoringWorkflowV1, input)
			if err := env.GetWorkflowError(); err != nil {
				t.Fatalf("rejected stage outcome failed the workflow: %v", err)
			}
			var got activities.GenerateAuthoringPlanResult
			if err := env.GetWorkflowResult(&got); err != nil {
				t.Fatalf("decode rejected authoring result: %v", err)
			}
			if got.Decision != activities.AuthoringPlanDecisionRejected || got.GateReasonCode != testCase.gate {
				t.Fatalf("unexpected rejected result: %+v", got)
			}
			assertAuthoringWorkflowCallsOnlyFormalizer(t, env)
		})
	}
}

func TestProblemGenerationAuthoringWorkflowV1FailsClosedOnActivityError(t *testing.T) {
	env, acts, input := newAuthoringWorkflowEnvironment(t)
	activityErr := errors.New("authoring provider unavailable")
	env.OnActivity(acts.GenerateAuthoringPlanActivity, mock.Anything, input).
		Return(nil, activityErr).
		Times(3)

	env.ExecuteWorkflow(ProblemGenerationAuthoringWorkflowV1, input)
	workflowErr := env.GetWorkflowError()
	if workflowErr == nil || !strings.Contains(workflowErr.Error(), activityErr.Error()) {
		t.Fatalf("workflow error = %v, want %q", workflowErr, activityErr)
	}
	env.AssertActivityNumberOfCalls(t, "GenerateAuthoringPlanActivity", 3)
	assertNoLegacyAuthoringWorkflowCalls(t, env)
}

func TestProblemGenerationAuthoringWorkflowV1RejectsInvalidInputBeforeActivity(t *testing.T) {
	env, _, input := newAuthoringWorkflowEnvironment(t)
	input.CanonicalBriefSHA256 = strings.Repeat("f", 64)

	env.ExecuteWorkflow(ProblemGenerationAuthoringWorkflowV1, input)
	workflowErr := env.GetWorkflowError()
	if workflowErr == nil || !strings.Contains(workflowErr.Error(), authoringInputContractErrorTypeV1) {
		t.Fatalf("workflow error = %v, want %s", workflowErr, authoringInputContractErrorTypeV1)
	}
	env.AssertActivityNumberOfCalls(t, "GenerateAuthoringPlanActivity", 0)
	assertNoLegacyAuthoringWorkflowCalls(t, env)
}

func TestProblemGenerationAuthoringWorkflowV1RejectsMalformedActivityResult(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		mutate func(*activities.GenerateAuthoringPlanResult)
	}{
		{
			name: "bundle identity mismatch",
			mutate: func(result *activities.GenerateAuthoringPlanResult) {
				result.BundleArtifact.SHA256 = strings.Repeat("f", 64)
			},
		},
		{
			name: "bundle reference mismatch",
			mutate: func(result *activities.GenerateAuthoringPlanResult) {
				listedBundle := *result.SourceArtifacts[1]
				listedBundle.Key = "different-key"
				result.SourceArtifacts[1] = &listedBundle
			},
		},
		{
			name: "accepted without lint pass",
			mutate: func(result *activities.GenerateAuthoringPlanResult) {
				result.LintPassed = false
				result.LintErrorCount = 1
			},
		},
		{
			name: "unknown gate reason",
			mutate: func(result *activities.GenerateAuthoringPlanResult) {
				result.Decision = activities.AuthoringPlanDecisionRejected
				result.GateReasonCode = "unknown_gate"
				result.RejectionReason = "unknown gate"
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			env, acts, input := newAuthoringWorkflowEnvironment(t)
			result := acceptedAuthoringWorkflowResult(t, input)
			testCase.mutate(result)
			env.OnActivity(acts.GenerateAuthoringPlanActivity, mock.Anything, input).
				Return(result, nil).
				Once()

			env.ExecuteWorkflow(ProblemGenerationAuthoringWorkflowV1, input)
			workflowErr := env.GetWorkflowError()
			if workflowErr == nil || !strings.Contains(workflowErr.Error(), authoringGateContractErrorTypeV1) {
				t.Fatalf("workflow error = %v, want %s", workflowErr, authoringGateContractErrorTypeV1)
			}
			assertAuthoringWorkflowCallsOnlyFormalizer(t, env)
		})
	}
}

func newAuthoringWorkflowEnvironment(
	t *testing.T,
) (*testsuite.TestWorkflowEnvironment, *activities.Activities, activities.GenerateAuthoringPlanInput) {
	t.Helper()
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(ProblemGenerationAuthoringWorkflowV1)
	acts := activities.New(nil)
	params := domain.DefaultProblemGenParams()
	params.Tags = []string{"prefix-sum"}
	brief := "Given a sequence, compute its aggregate exactly once."
	input := activities.GenerateAuthoringPlanInput{
		PayloadVersion:       activities.GenerateAuthoringPlanPayloadVersion,
		Params:               params,
		CanonicalBrief:       brief,
		CanonicalBriefSHA256: authoringWorkflowSHA256HexV1([]byte(brief)),
	}
	return env, acts, input
}

func acceptedAuthoringWorkflowResult(
	t *testing.T,
	input activities.GenerateAuthoringPlanInput,
) *activities.GenerateAuthoringPlanResult {
	t.Helper()
	inputSHA, err := authoringWorkflowInputSHA256V1(input)
	if err != nil {
		t.Fatal(err)
	}
	raw := &activities.ArtifactRef{
		SHA256: strings.Repeat("a", 64),
		LLMCallReceipt: &activities.LLMCallReceipt{
			RequestSHA256: strings.Repeat("d", 64),
		},
	}
	bundle := &activities.ArtifactRef{SHA256: strings.Repeat("b", 64)}
	return &activities.GenerateAuthoringPlanResult{
		PayloadVersion:     activities.GenerateAuthoringPlanPayloadVersion,
		ModelDecision:      activities.AuthoringPlanDecisionAccepted,
		Decision:           activities.AuthoringPlanDecisionAccepted,
		InputSHA256:        inputSHA,
		BriefSHA256:        input.CanonicalBriefSHA256,
		SemanticSpecSHA256: strings.Repeat("c", 64),
		BundleSHA256:       bundle.SHA256,
		BundleArtifact:     bundle,
		SourceArtifacts:    []*activities.ArtifactRef{raw, bundle},
		LintPassed:         true,
	}
}

func modelRejectedAuthoringWorkflowResult(
	t *testing.T,
	input activities.GenerateAuthoringPlanInput,
) *activities.GenerateAuthoringPlanResult {
	t.Helper()
	result := acceptedAuthoringWorkflowResult(t, input)
	result.ModelDecision = activities.AuthoringPlanDecisionRejected
	result.Decision = activities.AuthoringPlanDecisionRejected
	result.GateReasonCode = activities.AuthoringPlanGateReasonModelRejected
	result.RejectionReason = "the frozen brief is contradictory"
	result.SemanticSpecSHA256 = ""
	result.LintPassed = false
	return result
}

func lintRejectedAuthoringWorkflowResult(
	t *testing.T,
	input activities.GenerateAuthoringPlanInput,
) *activities.GenerateAuthoringPlanResult {
	t.Helper()
	result := acceptedAuthoringWorkflowResult(t, input)
	result.Decision = activities.AuthoringPlanDecisionRejected
	result.GateReasonCode = activities.AuthoringPlanGateReasonSpecLintFailed
	result.RejectionReason = "deterministic spec lint failed"
	result.LintPassed = false
	result.LintErrorCount = 1
	return result
}

func assertAuthoringWorkflowCallsOnlyFormalizer(t *testing.T, env *testsuite.TestWorkflowEnvironment) {
	t.Helper()
	env.AssertActivityNumberOfCalls(t, "GenerateAuthoringPlanActivity", 1)
	assertNoLegacyAuthoringWorkflowCalls(t, env)
}

func assertNoLegacyAuthoringWorkflowCalls(t *testing.T, env *testsuite.TestWorkflowEnvironment) {
	t.Helper()
	for _, activityName := range []string{
		"SimilarityCheckActivity",
		"GenerateStatementActivity",
		"GenerateSolutionActivity",
		"GenerateTestDataActivity",
		"StoreProblemActivity",
	} {
		env.AssertActivityNumberOfCalls(t, activityName, 0)
	}
}
