package workflow

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/workflow/activities"
	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/testsuite"
	sdkworkflow "go.temporal.io/sdk/workflow"
)

func TestProblemGenerationWorkflowStopsBeforeSandboxReviewAndStoreWhenPostDedupToolFails(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(ProblemGenerationWorkflow)
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
		Title:       "fail closed fixture",
		Statement:   "Given one integer, print it.\n\n" + activities.StatementSamplesPlaceholder,
		OneLineHint: "identity",
	}
	env.OnActivity(acts.GenerateStatementActivity, mock.Anything, mock.Anything).
		Return(statement, nil).
		Once()
	env.OnActivity(acts.CleanStatementActivity, mock.Anything, mock.Anything, mock.Anything).
		Return(statement, nil).
		Once()
	env.OnActivity(acts.PostStatementSimilarityActivity, mock.Anything, mock.Anything).
		Return(nil, errors.New("embedding dependency unavailable")).
		Times(3)

	params := domain.DefaultProblemGenParams()
	params.SimilarLimit = 0
	params.GenerateEditorial = false
	params.RequireReview = false
	env.ExecuteWorkflow(ProblemGenerationWorkflow, params)

	workflowErr := env.GetWorkflowError()
	if workflowErr == nil || !strings.Contains(workflowErr.Error(), "embedding dependency unavailable") {
		t.Fatalf("workflow error = %v", workflowErr)
	}
	assertPostDedupCheckFailedStep(t, env, activities.DedupFailureDependencyUnavailable)
	env.AssertActivityNumberOfCalls(t, "GenerateTestDataActivity", 0)
	env.AssertActivityNumberOfCalls(t, "RunSandboxActivity", 0)
	env.AssertActivityNumberOfCalls(t, "LLMReviewActivity", 0)
	env.AssertActivityNumberOfCalls(t, "StoreProblemActivity", 0)
}

func TestProblemGenerationWorkflowNeverCompletesStructuredDedupCheckFailed(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(ProblemGenerationWorkflow)
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
		Title:       "structured fail-closed fixture",
		Statement:   "Given one integer, print it.\n\n" + activities.StatementSamplesPlaceholder,
		OneLineHint: "identity",
	}
	env.OnActivity(acts.GenerateStatementActivity, mock.Anything, mock.Anything).
		Return(statement, nil).
		Once()
	env.OnActivity(acts.CleanStatementActivity, mock.Anything, mock.Anything, mock.Anything).
		Return(statement, nil).
		Once()
	env.OnActivity(acts.PostStatementSimilarityActivity, mock.Anything, mock.Anything).
		Return(&activities.PostStatementSimilarityResult{Report: &activities.DedupReport{
			SchemaVersion: activities.DedupReportSchemaVersion,
			Stage:         activities.DedupStagePostStatement,
			Decision:      activities.DedupDecisionCheckFailed,
			Reason:        activities.DedupFailureEmbeddingUnavailable,
		}}, nil).
		Once()

	params := domain.DefaultProblemGenParams()
	params.SimilarLimit = 0
	params.GenerateEditorial = false
	params.RequireReview = false
	env.ExecuteWorkflow(ProblemGenerationWorkflow, params)

	workflowErr := env.GetWorkflowError()
	if workflowErr == nil || !strings.Contains(workflowErr.Error(), "check_failed") {
		t.Fatalf("workflow error = %v, want check_failed", workflowErr)
	}
	assertPostDedupCheckFailedStep(t, env, activities.DedupFailureEmbeddingUnavailable)
	env.AssertActivityNumberOfCalls(t, "GenerateTestDataActivity", 0)
	env.AssertActivityNumberOfCalls(t, "RunSandboxActivity", 0)
	env.AssertActivityNumberOfCalls(t, "LLMReviewActivity", 0)
	env.AssertActivityNumberOfCalls(t, "StoreProblemActivity", 0)
}

func assertPostDedupCheckFailedStep(
	t *testing.T,
	env *testsuite.TestWorkflowEnvironment,
	wantReason string,
) {
	t.Helper()
	encodedState, err := env.QueryWorkflow(domain.WorkflowStateQueryName)
	if err != nil {
		t.Fatalf("query failed workflow state: %v", err)
	}
	var query domain.WorkflowStateQuery
	if err := encodedState.Get(&query); err != nil {
		t.Fatalf("decode failed workflow state: %v", err)
	}
	for _, step := range query.State.Steps {
		if step.Step != domain.StepPostStatementSimilarity {
			continue
		}
		if step.Status != domain.WorkflowStatusFailed {
			t.Fatalf("post-dedup status = %q, want failed", step.Status)
		}
		raw, err := json.Marshal(step.Output)
		if err != nil {
			t.Fatalf("marshal post-dedup output: %v", err)
		}
		var result activities.PostStatementSimilarityResult
		if err := json.Unmarshal(raw, &result); err != nil {
			t.Fatalf("decode post-dedup output: %v", err)
		}
		if result.Report == nil || result.Report.Decision != activities.DedupDecisionCheckFailed ||
			result.Report.Reason != wantReason || result.Report.NeighborCount != nil {
			t.Fatalf("post-dedup report = %+v, want check_failed/%s without count", result.Report, wantReason)
		}
		if strings.Contains(string(raw), "neighbor_count") {
			t.Fatalf("failed post-dedup output exposed a neighbor count: %s", raw)
		}
		return
	}
	t.Fatal("failed workflow has no post-statement similarity step")
}
