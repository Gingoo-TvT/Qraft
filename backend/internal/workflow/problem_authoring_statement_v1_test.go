package workflow

import (
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/workflow/activities"
	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/testsuite"
)

func TestProblemGenerationAuthoringStatementWorkflowV1AcceptedRunsExactlyTwoNewActivities(t *testing.T) {
	env, acts, input := newAuthoringStatementWorkflowEnvironment(t)
	authoring := acceptedAuthoringWorkflowResult(t, input)
	rendererInput := expectedAuthoringStatementRendererInputV1(input, *authoring)
	rendered := acceptedAuthoringStatementWorkflowResultV1(t, rendererInput)
	env.OnActivity(acts.GenerateAuthoringPlanActivity, mock.Anything, input).
		Return(authoring, nil).
		Once()
	env.OnActivity(acts.RenderStatementFromAuthoringBundleActivityV1, mock.Anything, rendererInput).
		Return(rendered, nil).
		Once()

	env.ExecuteWorkflow(ProblemGenerationAuthoringStatementWorkflowV1, input)
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("authoring statement workflow failed: %v", err)
	}
	var got ProblemGenerationAuthoringStatementResultV1
	if err := env.GetWorkflowResult(&got); err != nil {
		t.Fatalf("decode authoring statement workflow result: %v", err)
	}
	if got.Decision != activities.AuthoringPlanDecisionAccepted ||
		got.AuthoringResult == nil || got.StatementDraftResult == nil ||
		got.StatementDraftResult.StatementDraftSHA256 != rendered.StatementDraftSHA256 {
		t.Fatalf("unexpected accepted terminal result: %+v", got)
	}
	env.AssertActivityNumberOfCalls(t, "GenerateAuthoringPlanActivity", 1)
	env.AssertActivityNumberOfCalls(t, "RenderStatementFromAuthoringBundleActivityV1", 1)
	assertNoLegacyAuthoringWorkflowCalls(t, env)
}

func TestProblemGenerationAuthoringStatementWorkflowV1RejectedRunsNoRendererOrLegacyActivity(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		result func(*testing.T, activities.GenerateAuthoringPlanInput) *activities.GenerateAuthoringPlanResult
		gate   string
	}{
		{name: "model rejected", result: modelRejectedAuthoringWorkflowResult, gate: activities.AuthoringPlanGateReasonModelRejected},
		{name: "spec lint failed", result: lintRejectedAuthoringWorkflowResult, gate: activities.AuthoringPlanGateReasonSpecLintFailed},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			env, acts, input := newAuthoringStatementWorkflowEnvironment(t)
			authoring := testCase.result(t, input)
			env.OnActivity(acts.GenerateAuthoringPlanActivity, mock.Anything, input).
				Return(authoring, nil).
				Once()

			env.ExecuteWorkflow(ProblemGenerationAuthoringStatementWorkflowV1, input)
			if err := env.GetWorkflowError(); err != nil {
				t.Fatalf("rejected authoring statement workflow failed: %v", err)
			}
			var got ProblemGenerationAuthoringStatementResultV1
			if err := env.GetWorkflowResult(&got); err != nil {
				t.Fatalf("decode rejected terminal result: %v", err)
			}
			if got.Decision != activities.AuthoringPlanDecisionRejected ||
				got.GateReasonCode != testCase.gate || got.StatementDraftResult != nil {
				t.Fatalf("unexpected rejected terminal result: %+v", got)
			}
			env.AssertActivityNumberOfCalls(t, "GenerateAuthoringPlanActivity", 1)
			env.AssertActivityNumberOfCalls(t, "RenderStatementFromAuthoringBundleActivityV1", 0)
			assertNoLegacyAuthoringWorkflowCalls(t, env)
		})
	}
}

func TestProblemGenerationAuthoringStatementWorkflowV1FailsBeforeActivitiesForUnsupportedLocale(t *testing.T) {
	env, _, input := newAuthoringStatementWorkflowEnvironment(t)
	input.Params.Locale = "fr"

	env.ExecuteWorkflow(ProblemGenerationAuthoringStatementWorkflowV1, input)
	err := env.GetWorkflowError()
	if err == nil || !strings.Contains(err.Error(), authoringInputContractErrorTypeV1) {
		t.Fatalf("workflow error = %v, want %s", err, authoringInputContractErrorTypeV1)
	}
	env.AssertActivityNumberOfCalls(t, "GenerateAuthoringPlanActivity", 0)
	env.AssertActivityNumberOfCalls(t, "RenderStatementFromAuthoringBundleActivityV1", 0)
	assertNoLegacyAuthoringWorkflowCalls(t, env)
}

func TestProblemGenerationAuthoringStatementWorkflowV1FailsClosedOnMalformedRendererResult(t *testing.T) {
	env, acts, input := newAuthoringStatementWorkflowEnvironment(t)
	authoring := acceptedAuthoringWorkflowResult(t, input)
	rendererInput := expectedAuthoringStatementRendererInputV1(input, *authoring)
	rendered := acceptedAuthoringStatementWorkflowResultV1(t, rendererInput)
	rendered.AuthoringInputSHA256 = strings.Repeat("0", 64)
	env.OnActivity(acts.GenerateAuthoringPlanActivity, mock.Anything, input).
		Return(authoring, nil).
		Once()
	env.OnActivity(acts.RenderStatementFromAuthoringBundleActivityV1, mock.Anything, rendererInput).
		Return(rendered, nil).
		Once()

	env.ExecuteWorkflow(ProblemGenerationAuthoringStatementWorkflowV1, input)
	err := env.GetWorkflowError()
	if err == nil || !strings.Contains(err.Error(), authoringStatementGateContractErrorTypeV1) {
		t.Fatalf("workflow error = %v, want %s", err, authoringStatementGateContractErrorTypeV1)
	}
	env.AssertActivityNumberOfCalls(t, "GenerateAuthoringPlanActivity", 1)
	env.AssertActivityNumberOfCalls(t, "RenderStatementFromAuthoringBundleActivityV1", 1)
	assertNoLegacyAuthoringWorkflowCalls(t, env)
}

func TestValidateAuthoringStatementWorkflowResultV1ReportsHashErrorsDeterministically(t *testing.T) {
	_, _, input := newAuthoringStatementWorkflowEnvironment(t)
	authoring := acceptedAuthoringWorkflowResult(t, input)
	rendererInput := expectedAuthoringStatementRendererInputV1(input, *authoring)
	rendered := acceptedAuthoringStatementWorkflowResultV1(t, rendererInput)
	rendered.FactManifestSHA256 = "invalid-fact"
	rendered.MarkdownSHA256 = "invalid-markdown"
	rendered.StatementDraftSHA256 = "invalid-draft"

	const expected = "statement draft result fact_manifest_sha256 is invalid"
	for iteration := 0; iteration < 100; iteration++ {
		err := validateAuthoringStatementWorkflowResultV1(rendererInput, rendered)
		if err == nil || err.Error() != expected {
			t.Fatalf("iteration %d error = %v, want %q", iteration, err, expected)
		}
	}
}

func TestProblemGenerationAuthoringWorkflowV1NeverCallsStatementRenderer(t *testing.T) {
	env, acts, input := newAuthoringWorkflowEnvironment(t)
	authoring := acceptedAuthoringWorkflowResult(t, input)
	env.OnActivity(acts.GenerateAuthoringPlanActivity, mock.Anything, input).
		Return(authoring, nil).
		Once()

	env.ExecuteWorkflow(ProblemGenerationAuthoringWorkflowV1, input)
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("legacy authoring-only workflow failed: %v", err)
	}
	env.AssertActivityNumberOfCalls(t, "GenerateAuthoringPlanActivity", 1)
	env.AssertActivityNumberOfCalls(t, "RenderStatementFromAuthoringBundleActivityV1", 0)
	assertNoLegacyAuthoringWorkflowCalls(t, env)
}

func newAuthoringStatementWorkflowEnvironment(
	t *testing.T,
) (*testsuite.TestWorkflowEnvironment, *activities.Activities, activities.GenerateAuthoringPlanInput) {
	t.Helper()
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(ProblemGenerationAuthoringStatementWorkflowV1)
	_, acts, input := newAuthoringWorkflowEnvironment(t)
	return env, acts, input
}

func expectedAuthoringStatementRendererInputV1(
	input activities.GenerateAuthoringPlanInput,
	authoring activities.GenerateAuthoringPlanResult,
) activities.RenderStatementFromAuthoringBundleInput {
	bundle := *authoring.BundleArtifact
	return activities.RenderStatementFromAuthoringBundleInput{
		PayloadVersion:               activities.RenderStatementFromAuthoringBundlePayloadVersion,
		BundleArtifact:               bundle,
		ExpectedBundleSHA256:         authoring.BundleSHA256,
		ExpectedAuthoringInputSHA256: authoring.InputSHA256,
		ExpectedBriefSHA256:          authoring.BriefSHA256,
		ExpectedSemanticSpecSHA256:   authoring.SemanticSpecSHA256,
		ExpectedDifficulty:           input.Params.Difficulty,
		RequiredKnowledgePoints:      append([]string(nil), input.Params.Tags...),
		PresentationLocale:           "en",
		StatementRuntime:             copyAuthoringStatementRuntimeV1(input.Params.ProviderConfig),
	}
}

func acceptedAuthoringStatementWorkflowResultV1(
	t *testing.T,
	input activities.RenderStatementFromAuthoringBundleInput,
) *activities.RenderStatementFromAuthoringBundleResult {
	t.Helper()
	inputSHA, err := authoringStatementRendererInputSHA256V1(input)
	if err != nil {
		t.Fatal(err)
	}
	draftSHA := strings.Repeat("e", 64)
	draft := &activities.ArtifactRef{
		SchemaVersion:  activities.ArtifactRefSchemaVersion,
		PayloadVersion: activities.ActivityPayloadVersion,
		Bucket:         "workflow-artifacts",
		Key:            "workflow-artifacts/v1/sha256/ee/" + draftSHA,
		SHA256:         draftSHA,
		SizeBytes:      1024,
		ContentType:    "application/json",
		Producer:       "RenderStatementFromAuthoringBundleActivityV1",
		Provider:       "fixture-provider",
		Model:          "fixture-model",
		ModelRevision:  "fixture-revision",
		WorkflowID:     "fixture-workflow",
	}
	bundle := input.BundleArtifact
	raw := &activities.ArtifactRef{
		SHA256: strings.Repeat("a", 64),
		LLMCallReceipt: &activities.LLMCallReceipt{
			RequestSHA256: strings.Repeat("d", 64),
		},
	}
	return &activities.RenderStatementFromAuthoringBundleResult{
		PayloadVersion:         activities.RenderStatementFromAuthoringBundlePayloadVersion,
		RendererInputSHA256:    inputSHA,
		AuthoringBundleSHA256:  input.ExpectedBundleSHA256,
		AuthoringInputSHA256:   input.ExpectedAuthoringInputSHA256,
		BriefSHA256:            input.ExpectedBriefSHA256,
		SemanticSpecSHA256:     input.ExpectedSemanticSpecSHA256,
		FactManifestSHA256:     strings.Repeat("f", 64),
		MarkdownSHA256:         strings.Repeat("9", 64),
		StatementDraftSHA256:   draftSHA,
		StatementDraftArtifact: draft,
		SourceArtifacts:        []*activities.ArtifactRef{&bundle, raw, draft},
	}
}
