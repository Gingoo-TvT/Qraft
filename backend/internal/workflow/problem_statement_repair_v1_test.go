package workflow

import (
	"strings"
	"testing"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/workflow/activities"
	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/testsuite"
	sdkworkflow "go.temporal.io/sdk/workflow"
)

func statementRepairHarnessWorkflowV1(
	ctx sdkworkflow.Context,
	candidate activities.StatementResult,
	params domain.ProblemGenParams,
	useStructuredSamples bool,
) (activities.StatementResult, error) {
	llmCtx := sdkworkflow.WithActivityOptions(ctx, sdkworkflow.ActivityOptions{
		StartToCloseTimeout: time.Minute,
	})
	return repairGeneratedStatementV1(ctx, llmCtx, candidate, params, useStructuredSamples)
}

func TestRepairGeneratedStatementV1CarriesExactCandidateAndAdvancesAfterCleanRepair(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(statementRepairHarnessWorkflowV1)
	acts := activities.New(nil)

	candidate := statementRepairCandidateFixtureV1()
	repaired := candidate
	repaired.Statement = "题目。\n\n## 输入格式\n每行包含三个整数 $u,v,c$。\n\n## 输出格式\n输出答案。\n\n## 约束\n- $n \\le 10$。\n\n" + activities.StatementSamplesPlaceholder
	repaired.MarkdownDiagnostics = nil

	env.OnActivity(acts.RepairStatementActivityV1, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			input := args.Get(1).(activities.RepairStatementInputV1)
			if input.RepairAttempt != 1 || input.Candidate.Statement != candidate.Statement {
				t.Errorf("repair input did not preserve exact candidate: %+v", input)
			}
			if len(input.Diagnostics) != 1 || input.Diagnostics[0].Code != "integer_field_count_mismatch" {
				t.Errorf("repair input omitted deterministic diagnostic: %+v", input.Diagnostics)
			}
			if !input.UseStructuredSamples {
				t.Error("repair input lost structured sample contract")
			}
		}).
		Return(&repaired, nil).
		Once()

	env.ExecuteWorkflow(statementRepairHarnessWorkflowV1, candidate, domain.ProblemGenParams{}, true)
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("bounded statement repair failed: %v", err)
	}
	var got activities.StatementResult
	if err := env.GetWorkflowResult(&got); err != nil {
		t.Fatalf("get repair result: %v", err)
	}
	if got.Statement != repaired.Statement || len(got.MarkdownDiagnostics) != 0 {
		t.Fatalf("repair result = %+v, want clean repaired candidate", got)
	}
	env.AssertActivityNumberOfCalls(t, "RepairStatementActivityV1", 1)
}

func TestRepairGeneratedStatementV1StopsOnRepeatedDiagnostics(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(statementRepairHarnessWorkflowV1)
	acts := activities.New(nil)

	candidate := statementRepairCandidateFixtureV1()
	first := candidate
	first.Statement += "\nfirst repair"
	first.MarkdownDiagnostics = []activities.StatementMarkdownDiagnosticV1{{
		Code:    "missing_constraints_heading",
		Message: "line 4 statement is missing a standalone constraints heading",
		Line:    4,
	}}
	second := first
	second.Statement += "\nsecond repair"
	second.MarkdownDiagnostics = []activities.StatementMarkdownDiagnosticV1{{
		Code:    " MISSING_CONSTRAINTS_HEADING ",
		Message: " line 9 Statement is missing a standalone   constraints heading ",
		Line:    9,
	}}

	env.OnActivity(acts.RepairStatementActivityV1, mock.Anything, mock.Anything).
		Return(&first, nil).
		Once()
	env.OnActivity(acts.RepairStatementActivityV1, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			input := args.Get(1).(activities.RepairStatementInputV1)
			if input.RepairAttempt != 2 || input.Candidate.Statement != first.Statement {
				t.Errorf("second repair did not receive first repair output: %+v", input)
			}
		}).
		Return(&second, nil).
		Once()

	env.ExecuteWorkflow(statementRepairHarnessWorkflowV1, candidate, domain.ProblemGenParams{}, false)
	err := env.GetWorkflowError()
	if err == nil || !strings.Contains(err.Error(), "repeated the same diagnostics") || !strings.Contains(err.Error(), "QualityNotMet") {
		t.Fatalf("workflow error = %v, want repeated-diagnostic QualityNotMet", err)
	}
	env.AssertActivityNumberOfCalls(t, "RepairStatementActivityV1", 2)
}

func TestValidateStatementRepairIdentityV1RejectsSemanticEnvelopeChanges(t *testing.T) {
	before := statementRepairCandidateFixtureV1()
	after := before
	after.Title = "different problem"
	if err := validateStatementRepairIdentityV1(before, after); err == nil || !strings.Contains(err.Error(), "title") {
		t.Fatalf("identity validation error = %v, want title change", err)
	}

	after = before
	after.Tags = []string{"greedy"}
	if err := validateStatementRepairIdentityV1(before, after); err == nil || !strings.Contains(err.Error(), "tags") {
		t.Fatalf("identity validation error = %v, want tags change", err)
	}
}

func statementRepairCandidateFixtureV1() activities.StatementResult {
	return activities.StatementResult{
		Title:                   "树题",
		Statement:               "题目。\n\n## 输入格式\n每行包含四个整数 u,v,c。\n\n## 输出格式\n输出答案。",
		Tags:                    []string{"dp-tree"},
		OneLineHint:             "tree DP",
		DifficultyJustification: "target 2000",
		MarkdownDiagnostics: []activities.StatementMarkdownDiagnosticV1{{
			Code:    "integer_field_count_mismatch",
			Message: "line 4 claims 4 integers but lists 3 fields: u,v,c",
			Line:    4,
		}},
	}
}
