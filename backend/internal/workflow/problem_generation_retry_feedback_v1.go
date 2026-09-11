package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/workflow/activities"
	"go.temporal.io/sdk/temporal"
)

const problemGenerationRetryFeedbackV1ChangeID = "problem-generation-retry-feedback-v1"

const testDataRetryFeedbackMarkerV1 = "[AlgoForge test-data generator retry feedback v1]"

// testDataFailureCanReuseStatementV1 limits the same-statement fast retry to
// candidate defects.  Provider/transport failures must continue through the
// normal outer policy so a transient outage is not turned into another long
// LLM request with no new information.
func testDataFailureCanReuseStatementV1(err error) bool {
	if err == nil {
		return false
	}
	var applicationErr *temporal.ApplicationError
	if errors.As(err, &applicationErr) {
		switch applicationErr.Type() {
		case "InvalidGeneratedTestData", "QualityNotMet", "TruncatedLLMResponse":
			return true
		case "ProviderEffectBusy":
			return false
		}
	}
	// Temporal's activity wrapper can hide the concrete application error in
	// older SDK payloads.  Keep a narrow textual fallback for those histories,
	// while deliberately excluding provider-busy/transport diagnostics.
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "providereffectbusy") ||
		strings.Contains(message, "provider unavailable") ||
		strings.Contains(message, "context deadline exceeded") {
		return false
	}
	return strings.Contains(message, "invalidgeneratedtestdata") ||
		strings.Contains(message, "generator batch") ||
		strings.Contains(message, "generated test") ||
		strings.Contains(message, "truncated at max_tokens")
}

func newSolutionRetryFeedbackV1(
	statementAttempt int,
	solutionAttempt int,
	stage string,
	diagnostic string,
	solutions activities.SolutionResult,
	mismatches []activities.Mismatch,
	failingCases []activities.TestCaseData,
	feasibilityReason string,
) *activities.SolutionRetryFeedbackV1 {
	feedback := &activities.SolutionRetryFeedbackV1{
		SchemaVersion:       activities.GenerationRetryFeedbackSchemaVersionV1,
		StatementAttempt:    statementAttempt,
		SolutionAttempt:     solutionAttempt,
		FailureStage:        boundedGenerationRetryTextV1(stage, 100),
		Diagnostic:          boundedGenerationRetryTextV1(diagnostic, 4000),
		FeasibilityReason:   boundedGenerationRetryTextV1(feasibilityReason, 2000),
		Mismatches:          boundedGenerationRetryMismatchesV1(mismatches),
		FailingTestCases:    boundedGenerationRetryCasesV1(failingCases),
		PreviousMainSource:  boundedGenerationRetryTextV1(solutions.MainSolution.SourceCode, 16000),
		PreviousBruteSource: boundedGenerationRetryTextV1(solutions.BruteSolution.SourceCode, 16000),
	}
	if strings.TrimSpace(feedback.Diagnostic) == "" {
		feedback.Diagnostic = "previous attempt failed without a diagnostic"
	}
	return feedback
}

func newStatementRetryFeedbackV1(
	statementAttempt int,
	previousTitle string,
	stage string,
	diagnostic string,
	feasibilityReason string,
	mismatches []activities.Mismatch,
) *activities.StatementRetryFeedbackV1 {
	if strings.TrimSpace(diagnostic) == "" {
		diagnostic = "previous statement pipeline did not produce a validated solution pair"
	}
	return &activities.StatementRetryFeedbackV1{
		SchemaVersion:     activities.GenerationRetryFeedbackSchemaVersionV1,
		StatementAttempt:  statementAttempt,
		PreviousTitle:     boundedGenerationRetryTextV1(previousTitle, 200),
		FailureStage:      boundedGenerationRetryTextV1(stage, 100),
		Diagnostic:        boundedGenerationRetryTextV1(diagnostic, 4000),
		FeasibilityReason: boundedGenerationRetryTextV1(feasibilityReason, 2000),
		Mismatches:        boundedGenerationRetryMismatchesV1(mismatches),
	}
}

func statementRetryFeedbackFromSolutionV1(
	statementAttempt int,
	previousTitle string,
	feedback *activities.SolutionRetryFeedbackV1,
) *activities.StatementRetryFeedbackV1 {
	if feedback == nil {
		return newStatementRetryFeedbackV1(
			statementAttempt,
			previousTitle,
			"solution_pipeline",
			"all bounded solution attempts were exhausted",
			"",
			nil,
		)
	}
	return newStatementRetryFeedbackV1(
		statementAttempt,
		previousTitle,
		feedback.FailureStage,
		feedback.Diagnostic,
		feedback.FeasibilityReason,
		feedback.Mismatches,
	)
}

// problemGenerationTestDataParamsWithRetryFeedbackV1 carries the exact
// generator-side failure into the next outer statement attempt.  The
// statement and test-data activities intentionally keep their stable
// signatures; the feedback is appended to a copy of CustomPrompt so old
// histories and user-authored instructions remain untouched.
func problemGenerationTestDataParamsWithRetryFeedbackV1(
	params domain.ProblemGenParams,
	feedback *activities.StatementRetryFeedbackV1,
) domain.ProblemGenParams {
	if feedback == nil || feedback.SchemaVersion != activities.GenerationRetryFeedbackSchemaVersionV1 {
		return params
	}
	stage := strings.ToLower(strings.TrimSpace(feedback.FailureStage))
	if stage != "generate_testdata" && !strings.HasPrefix(stage, "structured_sample") {
		return params
	}

	var builder strings.Builder
	if strings.TrimSpace(params.CustomPrompt) != "" {
		builder.WriteString(strings.TrimSpace(params.CustomPrompt))
		builder.WriteString("\n\n")
	}
	builder.WriteString(testDataRetryFeedbackMarkerV1)
	builder.WriteString("\nThe previous test-data artifact failed in the remote sandbox. Repair the generator itself before returning a new JSON artifact; do not merely rename the branch or weaken the problem constraints.\n")
	fmt.Fprintf(&builder, "Previous statement attempt: %d; previous title: %s\n",
		feedback.StatementAttempt,
		boundedGenerationRetryTextV1(feedback.PreviousTitle, 200),
	)
	fmt.Fprintf(&builder, "Failure stage: %s\n", boundedGenerationRetryTextV1(feedback.FailureStage, 100))
	fmt.Fprintf(&builder, "Exact sandbox diagnostic: %s\n", boundedGenerationRetryTextV1(feedback.Diagnostic, 4000))
	builder.WriteString("Re-check C++ string escaping, compile the generator mentally, and ensure every configured test_index reads exactly one triple and emits one complete instance with no empty output, EOF loop, debug stdout, or unbounded retry loop. Keep samples and BruteCheck cases small and preserve the statement's exact grammar.\n")
	params.CustomPrompt = boundedGenerationRetryTextV1(builder.String(), 7000)
	return params
}

func failingCasesForMismatchesV1(
	mismatches []activities.Mismatch,
	comparedCases []activities.TestCaseData,
) []activities.TestCaseData {
	result := make([]activities.TestCaseData, 0, len(mismatches))
	for _, mismatch := range mismatches {
		if len(result) >= 5 || mismatch.TestIndex < 0 || mismatch.TestIndex >= len(comparedCases) {
			continue
		}
		result = append(result, comparedCases[mismatch.TestIndex])
	}
	return result
}

func solutionRetryFingerprintV1(feedback *activities.SolutionRetryFeedbackV1) string {
	if feedback == nil {
		return ""
	}
	material := struct {
		Stage      string                `json:"stage"`
		Diagnostic string                `json:"diagnostic"`
		Mismatches []activities.Mismatch `json:"mismatches"`
	}{
		Stage:      feedback.FailureStage,
		Diagnostic: feedback.Diagnostic,
		Mismatches: feedback.Mismatches,
	}
	encoded, _ := json.Marshal(material)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func boundedGenerationRetryMismatchesV1(values []activities.Mismatch) []activities.Mismatch {
	if len(values) > 5 {
		values = values[:5]
	}
	result := make([]activities.Mismatch, 0, len(values))
	for _, value := range values {
		result = append(result, activities.Mismatch{
			TestIndex:   value.TestIndex,
			MainOutput:  boundedGenerationRetryTextV1(value.MainOutput, 500),
			BruteOutput: boundedGenerationRetryTextV1(value.BruteOutput, 500),
		})
	}
	return result
}

func boundedGenerationRetryCasesV1(values []activities.TestCaseData) []activities.TestCaseData {
	if len(values) > 5 {
		values = values[:5]
	}
	result := make([]activities.TestCaseData, 0, len(values))
	for _, value := range values {
		item := value
		item.Input = boundedGenerationRetryTextV1(item.Input, 4000)
		item.InputRef = ""
		result = append(result, item)
	}
	return result
}

func boundedGenerationRetryTextV1(value string, maxRunes int) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	return string(runes[:maxRunes]) + "..."
}
