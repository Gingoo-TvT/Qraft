package activities

import (
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/llm"
)

func TestParseSolutionResponseAcceptsRestoredJSONPrefill(t *testing.T) {
	response := restoreJSONPrefill(`"source_code":"#include <bits/stdc++.h>\nusing namespace std;\nint main(){return 0;}\n","language":"cpp","complexity_time":"O(1)","complexity_space":"O(1)","explanation":"constant"}`)

	sol, err := parseSolutionResponse(response, domain.SolutionTypeMain, "cpp")
	if err != nil {
		t.Fatalf("parseSolutionResponse returned error: %v", err)
	}
	if sol.Language != "cpp" {
		t.Fatalf("expected cpp language, got %q", sol.Language)
	}
	if !strings.Contains(sol.SourceCode, "int main()") {
		t.Fatalf("expected source code to be parsed, got %q", sol.SourceCode)
	}
}

func TestSolutionPromptRequiresCompactJSONObject(t *testing.T) {
	assertContains(t, solutionSystemPrompt, "Return exactly one compact JSON object and nothing else")
	assertContains(t, solutionSystemPrompt, `The first output characters after the assistant prefill must be "source_code"`)
}

func TestSolutionPromptsPinSingleSandboxInvocationContract(t *testing.T) {
	params := domain.DefaultProblemGenParams()
	for name, prompt := range map[string]string{
		"main":  buildMainSolutionPrompt("read n and print n", "cpp", params),
		"brute": buildBruteSolutionPrompt("read n and print n", "cpp", params),
	} {
		t.Run(name, func(t *testing.T) {
			assertContains(t, prompt, "Each generated test case is executed in a separate process")
			assertContains(t, prompt, "process exactly one instance and print exactly one answer")
			assertContains(t, prompt, "Never read repeated instances until EOF")
			assertContains(t, prompt, "recursion depth")
			assertContains(t, prompt, "cross-check every symbol")
		})
	}
}

func TestSolutionRepairPromptCarriesBoundFailureEvidence(t *testing.T) {
	feedback := &SolutionRetryFeedbackV1{
		SchemaVersion:       GenerationRetryFeedbackSchemaVersionV1,
		StatementAttempt:    2,
		SolutionAttempt:     1,
		FailureStage:        "differential_validation",
		Diagnostic:          "main and brute outputs differed",
		FeasibilityReason:   "the statement is feasible; the implementation is wrong",
		Mismatches:          []Mismatch{{TestIndex: 0, MainOutput: "1 2 3", BruteOutput: "6"}},
		FailingTestCases:    []TestCaseData{{Input: "3\n1 2 3\n"}},
		PreviousMainSource:  "int main(){ /* old main */ }",
		PreviousBruteSource: "int main(){ /* old brute */ }",
	}
	input := GenerateSolutionRepairInput{
		PayloadVersion: ActivityPayloadVersion,
		Statement:      "Given three numbers, print their sum.",
		Params:         domain.DefaultProblemGenParams(),
		Feedback:       *feedback,
	}
	if err := input.Validate(); err != nil {
		t.Fatalf("valid repair input rejected: %v", err)
	}

	common := buildSolutionRetryCommonSection(feedback, []string{"3\n1 2 3"})
	assertContains(t, common, "main and brute outputs differed")
	assertContains(t, common, `main="1 2 3"; brute="6"`)
	assertContains(t, common, "Exact failing input 1")
	assertContains(t, common, "Treat this as a repair task, not a fresh blind sample")

	mainSection := common + buildPreviousRoleSourceSection("main", feedback.PreviousMainSource)
	assertContains(t, mainSection, "old main")
	if strings.Contains(mainSection, "old brute") {
		t.Fatal("main repair prompt leaked the brute implementation")
	}
}

func TestStatementRetryPromptRequiresRedesign(t *testing.T) {
	feedback := &StatementRetryFeedbackV1{
		SchemaVersion:    GenerationRetryFeedbackSchemaVersionV1,
		StatementAttempt: 1,
		PreviousTitle:    "Old Tree Problem",
		FailureStage:     "differential_validation",
		Diagnostic:       "same output-shape mismatch repeated",
	}
	var builder strings.Builder
	appendStatementRetryFeedback(&builder, feedback)
	prompt := builder.String()
	assertContains(t, prompt, "Mandatory redesign feedback")
	assertContains(t, prompt, "Do not merely rename the previous problem")
	assertContains(t, prompt, "exactly one output per declared input instance")
}

func TestBruteSolutionUsesVerificationRole(t *testing.T) {
	request := &llm.Request{}
	params := domain.ProblemGenParams{ProviderConfig: &domain.ProviderRuntimeConfig{
		Statement:    &domain.LLMRuntimeConfig{Model: "generation-model", APIKeyRef: "runtime:generation"},
		Verification: &domain.LLMRuntimeConfig{Model: "oracle-model", APIKeyRef: "runtime:oracle"},
	}}

	applyBruteSolutionLLMRuntime(request, params)
	if request.Model != "oracle-model" || request.Runtime == nil || request.Runtime.APIKeyRef != "runtime:oracle" {
		t.Fatalf("brute/oracle runtime=%+v request=%+v", request.Runtime, request)
	}
}

func TestParseTestDataResponseAcceptsRestoredJSONPrefill(t *testing.T) {
	response := restoreJSONPrefill(`"test_cases":[{"input":"1\n","group_id":0,"is_sample":true,"description":"minimum case"},{"input":"","group_id":1,"is_sample":false,"description":"max random"}],"generator_code":"#include <bits/stdc++.h>\nusing namespace std;\nint main(){cout << 1 << '\\n';}"}`)

	result, err := parseTestDataResponse(response)
	if err != nil {
		t.Fatalf("parseTestDataResponse returned error: %v", err)
	}
	if len(result.TestCases) != 2 {
		t.Fatalf("expected 2 test cases, got %d", len(result.TestCases))
	}
	if result.GeneratorCode == "" {
		t.Fatal("expected generator code to be parsed")
	}
}

func TestTestDataPromptRequiresCompactJSONObject(t *testing.T) {
	assertContains(t, testDataSystemPrompt, "Return exactly one compact JSON object and nothing else")
	assertContains(t, testDataSystemPrompt, `The first output characters after the assistant prefill must be "test_case_count"`)
	assertContains(t, testDataSystemPrompt, "test_index group_id seed")
	assertContains(t, testDataSystemPrompt, "from standard input")
	assertContains(t, testDataSystemPrompt, "print one complete, parseable problem instance")
	assertContains(t, testDataSystemPrompt, "Never return early")
	assertContains(t, testDataSystemPrompt, "GENERATOR ACCEPTANCE CHECKLIST")
	if strings.Contains(testDataSystemPrompt, "argc/argv") || strings.Contains(testDataSystemPrompt, "command-line arguments") {
		t.Fatal("test data generator prompt still permits argv execution")
	}
}

func TestEmptyGeneratedInputWithoutGeneratorIsRejected(t *testing.T) {
	result, err := parseTestDataResponse(`{"test_cases":[{"input":"","group_id":1,"is_sample":false,"description":"placeholder"}],"generator_code":""}`)
	if err != nil {
		t.Fatalf("parse test data: %v", err)
	}
	if err := validateGeneratedTestInputs(result.TestCases); err == nil {
		t.Fatal("empty placeholder without generator must fail closed")
	}
}

func TestParseStatementResponseRepairsInvalidLatexEscapes(t *testing.T) {
	response := `{"title":"能量核心","statement":"给定 n，满足 1 \le n \le 2 \cdot 10^5。\n输出答案。","tags":["greedy"],"one_line_hint":"排序后贪心。","difficulty_justification":"(1) key; (2) O(n); (3) brute; (4) greedy; (5) fit; (6) needed; (7) attacks."}`

	stmt, err := parseStatementResponse(response)
	if err != nil {
		t.Fatalf("parseStatementResponse returned error: %v", err)
	}
	if !strings.Contains(stmt.Statement, "≤") {
		t.Fatalf("expected bare LaTeX comparison to be normalized, got %q", stmt.Statement)
	}
	if !strings.Contains(stmt.Statement, "\n## 输出格式\n\n答案") {
		t.Fatalf("expected JSON newline and unambiguous output label to be normalized, got %q", stmt.Statement)
	}
}
