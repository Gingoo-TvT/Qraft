package activities

import (
	"context"
	"fmt"
	"strings"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
)

const GenerationRetryFeedbackSchemaVersionV1 = 1

// StatementRetryFeedbackV1 explains why the previous statement pipeline was
// abandoned. It is intentionally compact so it can remain in Temporal history.
type StatementRetryFeedbackV1 struct {
	SchemaVersion     int        `json:"schema_version"`
	StatementAttempt  int        `json:"statement_attempt"`
	PreviousTitle     string     `json:"previous_title,omitempty"`
	FailureStage      string     `json:"failure_stage"`
	Diagnostic        string     `json:"diagnostic"`
	FeasibilityReason string     `json:"feasibility_reason,omitempty"`
	Mismatches        []Mismatch `json:"mismatches,omitempty"`
}

// SolutionRetryFeedbackV1 binds a repair request to the exact failed programs,
// failing inputs, and differential outputs from the preceding attempt.
type SolutionRetryFeedbackV1 struct {
	SchemaVersion       int            `json:"schema_version"`
	StatementAttempt    int            `json:"statement_attempt"`
	SolutionAttempt     int            `json:"solution_attempt"`
	FailureStage        string         `json:"failure_stage"`
	Diagnostic          string         `json:"diagnostic"`
	FeasibilityReason   string         `json:"feasibility_reason,omitempty"`
	Mismatches          []Mismatch     `json:"mismatches,omitempty"`
	FailingTestCases    []TestCaseData `json:"failing_test_cases,omitempty"`
	PreviousMainSource  string         `json:"previous_main_source,omitempty"`
	PreviousBruteSource string         `json:"previous_brute_source,omitempty"`
}

// GenerateSolutionRepairInput is used only after a failed solution attempt.
// Keeping a separate activity payload preserves replay of legacy histories.
type GenerateSolutionRepairInput struct {
	PayloadVersion int                     `json:"payload_version"`
	Statement      string                  `json:"statement"`
	Params         domain.ProblemGenParams `json:"params"`
	Feedback       SolutionRetryFeedbackV1 `json:"feedback"`
}

func (in GenerateSolutionRepairInput) Validate() error {
	if in.PayloadVersion != ActivityPayloadVersion {
		return fmt.Errorf("unsupported solution repair payload version %d", in.PayloadVersion)
	}
	if strings.TrimSpace(in.Statement) == "" {
		return fmt.Errorf("solution repair statement is required")
	}
	feedback := in.Feedback
	if feedback.SchemaVersion != GenerationRetryFeedbackSchemaVersionV1 {
		return fmt.Errorf("unsupported solution retry feedback schema version %d", feedback.SchemaVersion)
	}
	if feedback.StatementAttempt < 1 || feedback.SolutionAttempt < 1 {
		return fmt.Errorf("solution retry attempts must be positive")
	}
	if strings.TrimSpace(feedback.FailureStage) == "" || strings.TrimSpace(feedback.Diagnostic) == "" {
		return fmt.Errorf("solution retry failure stage and diagnostic are required")
	}
	if len(feedback.Mismatches) > 5 || len(feedback.FailingTestCases) > 5 {
		return fmt.Errorf("solution retry feedback exceeds five bounded failing cases")
	}
	if len(feedback.Diagnostic) > 8192 || len(feedback.FeasibilityReason) > 4096 ||
		len(feedback.PreviousMainSource) > 32768 || len(feedback.PreviousBruteSource) > 32768 {
		return fmt.Errorf("solution retry feedback exceeds bounded text limits")
	}
	return nil
}

func appendStatementRetryFeedback(builder *strings.Builder, feedback *StatementRetryFeedbackV1) {
	if feedback == nil || feedback.SchemaVersion != GenerationRetryFeedbackSchemaVersionV1 {
		return
	}

	builder.WriteString("\n\n### Mandatory redesign feedback from the previous failed pipeline\n")
	fmt.Fprintf(builder, "Previous statement attempt: %d\n", feedback.StatementAttempt)
	if feedback.PreviousTitle != "" {
		fmt.Fprintf(builder, "Previous title: %s\n", boundedRetryPromptText(feedback.PreviousTitle, 200))
	}
	fmt.Fprintf(builder, "Failure stage: %s\n", boundedRetryPromptText(feedback.FailureStage, 100))
	fmt.Fprintf(builder, "Observed diagnostic: %s\n", boundedRetryPromptText(feedback.Diagnostic, 2000))
	if feedback.FeasibilityReason != "" {
		fmt.Fprintf(builder, "Assessment: %s\n", boundedRetryPromptText(feedback.FeasibilityReason, 1000))
	}
	for i, mismatch := range feedback.Mismatches {
		fmt.Fprintf(builder, "Mismatch %d: main=%q, brute=%q\n", i+1,
			boundedRetryPromptText(mismatch.MainOutput, 300),
			boundedRetryPromptText(mismatch.BruteOutput, 300),
		)
	}
	builder.WriteString("Do not merely rename the previous problem. Redesign any ambiguous input/output contract or fragile construction that caused the failure, while preserving the requested tags and target difficulty. The replacement must make it straightforward for two independent implementations to agree on exactly one output per declared input instance.\n")
	builder.WriteString("For statement-surface failures, the replacement must also use one standalone Input Format, Output Format, and Constraints heading, contain no literal \\n escape or TeX command inside inline code, define every symbol exactly once, and keep the structured-sample marker contract unchanged when it is enabled.\n")
}

func (a *Activities) buildSolutionRetryPromptSections(
	ctx context.Context,
	feedback *SolutionRetryFeedbackV1,
) (string, string, error) {
	if feedback == nil {
		return "", "", nil
	}

	inputs := make([]string, 0, len(feedback.FailingTestCases))
	for i, testCase := range feedback.FailingTestCases {
		var input string
		switch {
		case testCase.InputArtifact != nil:
			data, err := a.getArtifact(ctx, testCase.InputArtifact)
			if err != nil {
				return "", "", fmt.Errorf("reading solution repair input artifact %d: %w", i, err)
			}
			input = string(data)
		case testCase.Input != "":
			input = testCase.Input
		case testCase.InputRef != "":
			return "", "", fmt.Errorf("solution repair input %d uses forbidden worker-local ref", i)
		default:
			input = "<input unavailable>"
		}
		inputs = append(inputs, boundedRetryPromptText(input, 2000))
	}

	common := buildSolutionRetryCommonSection(feedback, inputs)
	main := common + buildPreviousRoleSourceSection("main", feedback.PreviousMainSource)
	brute := common + buildPreviousRoleSourceSection("brute-force", feedback.PreviousBruteSource)
	return main, brute, nil
}

func buildSolutionRetryCommonSection(feedback *SolutionRetryFeedbackV1, inputs []string) string {
	var builder strings.Builder
	builder.WriteString("\n\n### Mandatory repair evidence from the preceding attempt\n")
	fmt.Fprintf(&builder, "Failed statement attempt: %d; failed solution attempt: %d\n", feedback.StatementAttempt, feedback.SolutionAttempt)
	fmt.Fprintf(&builder, "Failure stage: %s\n", boundedRetryPromptText(feedback.FailureStage, 100))
	fmt.Fprintf(&builder, "Observed diagnostic: %s\n", boundedRetryPromptText(feedback.Diagnostic, 2000))
	if feedback.FeasibilityReason != "" {
		fmt.Fprintf(&builder, "Independent assessment: %s\n", boundedRetryPromptText(feedback.FeasibilityReason, 1000))
	}
	for i, mismatch := range feedback.Mismatches {
		fmt.Fprintf(&builder, "Failing comparison %d: main=%q; brute=%q\n", i+1,
			boundedRetryPromptText(mismatch.MainOutput, 500),
			boundedRetryPromptText(mismatch.BruteOutput, 500),
		)
		if i < len(inputs) {
			fmt.Fprintf(&builder, "Exact failing input %d:\n```text\n%s\n```\n", i+1, inputs[i])
		}
	}
	builder.WriteString("Treat this as a repair task, not a fresh blind sample. Explain the corrected invariant internally, replace the faulty implementation, and verify it against every supplied failing input before returning JSON. Do not repeat the previous source unchanged.\n")
	return builder.String()
}

func buildPreviousRoleSourceSection(role, source string) string {
	if strings.TrimSpace(source) == "" {
		return ""
	}
	return fmt.Sprintf("\nPrevious %s source that must be diagnosed and replaced:\n```cpp\n%s\n```\nRepair only this role independently; do not copy the other implementation.\n",
		role,
		boundedRetryPromptText(source, 16000),
	)
}

func boundedRetryPromptText(value string, maxRunes int) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	return string(runes[:maxRunes]) + "..."
}
