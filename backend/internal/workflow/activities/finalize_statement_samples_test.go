package activities

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"go.temporal.io/sdk/temporal"
)

func TestFinalizeStatementSamplesUsesValidatedMainOutputs(t *testing.T) {
	activities := New(&Dependencies{})
	activities.artifacts = &sandboxArtifactStore{getData: []byte("7")}
	artifact := &ArtifactRef{}

	result, err := activities.FinalizeStatementSamplesActivity(context.Background(), FinalizeStatementSamplesInput{
		PayloadVersion: ActivityPayloadVersion,
		Statement: StatementResult{
			Title:     "Addition",
			Statement: "Add the numbers.\\n\\n" + StatementSamplesPlaceholder,
		},
		TestCases: []TestCaseData{
			{Input: "1 2\n", IsSample: true},
			{Input: "3 4\n", IsSample: true},
			{Input: "10 20\n"},
		},
		SandboxOutput: SandboxResult{
			PayloadVersion:  ActivityPayloadVersion,
			Outputs:         []string{"3", "", "30"},
			OutputArtifacts: []*ArtifactRef{nil, artifact, nil},
		},
		Locale:              "zh-CN",
		ExpectedSampleCount: 2,
		EnforceSampleCount:  true,
	})
	if err != nil {
		t.Fatalf("finalize statement samples: %v", err)
	}
	if strings.Contains(result.Statement, StatementSamplesPlaceholder) {
		t.Fatalf("placeholder remained in finalized statement: %s", result.Statement)
	}
	for _, want := range []string{
		"### 样例",
		"#### 样例 1",
		"```text\n1 2\n```",
		"```text\n3\n```",
		"#### 样例 2",
		"```text\n3 4\n```",
		"```text\n7\n```",
	} {
		if !strings.Contains(result.Statement, want) {
			t.Fatalf("finalized statement is missing %q:\n%s", want, result.Statement)
		}
	}
	if strings.Contains(result.Statement, "10 20") || strings.Contains(result.Statement, "30") {
		t.Fatalf("hidden test leaked into public samples: %s", result.Statement)
	}
}

func TestFinalizeStatementSamplesSupportsExplicitZeroSamples(t *testing.T) {
	result, err := New(&Dependencies{}).FinalizeStatementSamplesActivity(context.Background(), FinalizeStatementSamplesInput{
		PayloadVersion: ActivityPayloadVersion,
		Statement: StatementResult{
			Title:     "No public samples",
			Statement: "Solve the task.\n\n" + StatementSamplesPlaceholder,
		},
		TestCases: []TestCaseData{{Input: "1\n"}},
		SandboxOutput: SandboxResult{
			PayloadVersion: ActivityPayloadVersion,
			Outputs:        []string{"1"},
		},
		ExpectedSampleCount: 0,
		EnforceSampleCount:  true,
	})
	if err != nil {
		t.Fatalf("finalize zero samples: %v", err)
	}
	if result.Statement != "Solve the task." || strings.Contains(result.Statement, StatementSamplesPlaceholder) || strings.Contains(result.Statement, "Samples") {
		t.Fatalf("zero-sample statement = %q", result.Statement)
	}
}

func TestFinalizeStatementSamplesEnforcesExactCountOnlyForV2(t *testing.T) {
	base := FinalizeStatementSamplesInput{
		PayloadVersion: ActivityPayloadVersion,
		Statement:      StatementResult{Statement: "Solve.\n\n" + StatementSamplesPlaceholder},
		TestCases:      []TestCaseData{{Input: "1\n", IsSample: true}},
		SandboxOutput:  SandboxResult{PayloadVersion: ActivityPayloadVersion, Outputs: []string{"1"}},
	}

	legacy, err := New(&Dependencies{}).FinalizeStatementSamplesActivity(context.Background(), base)
	if err != nil || strings.Contains(legacy.Statement, StatementSamplesPlaceholder) {
		t.Fatalf("legacy v1 finalization = %+v err=%v", legacy, err)
	}

	for _, expected := range []int{0, 2} {
		input := base
		input.EnforceSampleCount = true
		input.ExpectedSampleCount = expected
		_, err := New(&Dependencies{}).FinalizeStatementSamplesActivity(context.Background(), input)
		var applicationErr *temporal.ApplicationError
		if !errors.As(err, &applicationErr) || applicationErr.Type() != "QualityNotMet" || !applicationErr.NonRetryable() {
			t.Fatalf("expected=%d error=%T %v, want QualityNotMet", expected, err, err)
		}
	}
}

func TestFinalizeStatementSamplesFailsAsQualityNotMetWithoutPlaceholder(t *testing.T) {
	_, err := New(&Dependencies{}).FinalizeStatementSamplesActivity(context.Background(), FinalizeStatementSamplesInput{
		PayloadVersion: ActivityPayloadVersion,
		Statement:      StatementResult{Statement: "Statement with an invented sample output: 4"},
		TestCases:      []TestCaseData{{Input: "1 2\n", IsSample: true}},
		SandboxOutput: SandboxResult{
			PayloadVersion: ActivityPayloadVersion,
			Outputs:        []string{"3"},
		},
	})
	var applicationErr *temporal.ApplicationError
	if !errors.As(err, &applicationErr) || applicationErr.Type() != "QualityNotMet" || !applicationErr.NonRetryable() {
		t.Fatalf("error = %T %v, want non-retryable QualityNotMet", err, err)
	}
}

func TestFinalizeStatementSamplesStrictGateRejectsDuplicateHeadingAndInlineTeX(t *testing.T) {
	statement := "题目。\n\n## 输入格式\n输入 `w_1,\\ldots,w_n`。\n\n## 输出格式\n输出答案。\n\n### 输出格式\n输出答案。\n\n## 约束\n- 1 <= n <= 10。\n\n" + StatementSamplesPlaceholder
	_, err := New(&Dependencies{}).FinalizeStatementSamplesActivity(context.Background(), FinalizeStatementSamplesInput{
		PayloadVersion: ActivityPayloadVersion,
		Statement: StatementResult{
			Title:     "strict quality",
			Statement: statement,
		},
		TestCases: []TestCaseData{{Input: "1\n", IsSample: true}},
		SandboxOutput: SandboxResult{
			PayloadVersion: ActivityPayloadVersion,
			Outputs:        []string{"1"},
		},
		StrictMarkdownQuality: true,
	})
	var applicationErr *temporal.ApplicationError
	if !errors.As(err, &applicationErr) || applicationErr.Type() != "QualityNotMet" || !strings.Contains(err.Error(), "repeats") {
		t.Fatalf("strict finalization error = %T %v, want duplicate-heading QualityNotMet", err, err)
	}
}

func TestStructuredStatementPromptRequiresSingleSampleMarker(t *testing.T) {
	params := domain.DefaultProblemGenParams()
	prompt := buildStatementPromptWithOptions(params, nil, true)
	system := statementSystemPromptForInput(true)
	if strings.Count(prompt, StatementSamplesPlaceholder) != 1 {
		t.Fatalf("structured user prompt marker count = %d", strings.Count(prompt, StatementSamplesPlaceholder))
	}
	if strings.Count(system, StatementSamplesPlaceholder) != 1 {
		t.Fatalf("structured system prompt marker count = %d", strings.Count(system, StatementSamplesPlaceholder))
	}
	if statementSystemPromptForInput(false) != statementSystemPrompt {
		t.Fatal("legacy statement system prompt changed when structured samples are disabled")
	}
	legacyPrompt := buildStatementPrompt(params, nil)
	if strings.Contains(legacyPrompt, StatementSamplesPlaceholder) {
		t.Fatalf("legacy statement prompt unexpectedly contains marker: %s", legacyPrompt)
	}
}

func TestRenderStatementSamplesUsesFenceLongerThanSampleContent(t *testing.T) {
	rendered := renderStatementSamples([]statementSample{{
		input:  "before\n```go\nx := 1\n```\nafter",
		output: "````",
	}}, "en")

	for _, want := range []string{
		"````text\nbefore\n```go\nx := 1\n```\nafter\n````",
		"`````text\n````\n`````",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered samples are missing %q:\n%s", want, rendered)
		}
	}
}
