package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/Gingoo-TvT/Qraft/backend/internal/sandbox"
	"github.com/rs/zerolog/log"
)

// ---------------------------------------------------------------------------
// ValidatorService
// ---------------------------------------------------------------------------

// ValidatorService handles problem validation by running solutions against
// test cases in the sandbox and comparing outputs between the main and
// brute-force solutions.
type ValidatorService struct {
	sandboxSvc *SandboxService
}

// NewValidatorService creates a new ValidatorService with the given sandbox
// service.
func NewValidatorService(sandboxSvc *SandboxService) *ValidatorService {
	return &ValidatorService{sandboxSvc: sandboxSvc}
}

// ---------------------------------------------------------------------------
// ValidateSolution
// ---------------------------------------------------------------------------

// SolutionValidationResult contains the outcome of running a solution against
// test cases.
type SolutionValidationResult struct {
	// AllPassed is true when every test case received VerdictOK.
	AllPassed bool `json:"all_passed"`

	// Passed is the count of test cases with VerdictOK.
	Passed int `json:"passed"`

	// Total is the total number of test cases executed.
	Total int `json:"total"`

	// Results contains the detailed result for each test case.
	Results []TestCaseResult `json:"results"`

	// FailedIndices lists the indices of test cases that did not pass.
	FailedIndices []int `json:"failed_indices,omitempty"`
}

// ValidateSolution runs the given solution code against all provided test case
// inputs and returns a structured result. Each test case is executed
// independently; a failure on one test case does not prevent execution of
// subsequent cases.
func (v *ValidatorService) ValidateSolution(
	ctx context.Context,
	code string,
	language string,
	testCases []TestCaseInput,
	limits ExecutionLimits,
) (*SolutionValidationResult, error) {
	log.Info().
		Str("language", language).
		Int("num_testcases", len(testCases)).
		Msg("validating solution against test cases")

	results, err := v.sandboxSvc.RunAllTestCases(ctx, language, code, testCases, limits)
	if err != nil {
		return nil, fmt.Errorf("running test cases: %w", err)
	}

	passed := 0
	var failedIndices []int
	for _, r := range results {
		if r.Verdict == sandbox.VerdictOK {
			passed++
		} else {
			failedIndices = append(failedIndices, r.Index)
		}
	}

	result := &SolutionValidationResult{
		AllPassed:     passed == len(testCases),
		Passed:        passed,
		Total:         len(testCases),
		Results:       results,
		FailedIndices: failedIndices,
	}

	log.Info().
		Bool("all_passed", result.AllPassed).
		Int("passed", passed).
		Int("total", len(testCases)).
		Msg("solution validation completed")

	return result, nil
}

// ---------------------------------------------------------------------------
// CompareOutputs
// ---------------------------------------------------------------------------

// OutputComparison contains the result of comparing outputs from two solutions.
type OutputComparison struct {
	// AllMatch is true when every pair of outputs matches.
	AllMatch bool `json:"all_match"`

	// Matched is the count of matching output pairs.
	Matched int `json:"matched"`

	// Total is the total number of output pairs compared.
	Total int `json:"total"`

	// Mismatches describes each output pair that did not match.
	Mismatches []OutputMismatch `json:"mismatches,omitempty"`
}

// OutputMismatch records a single case where two solutions produced different
// output.
type OutputMismatch struct {
	// Index of the test case.
	Index int `json:"index"`

	// MainOutput is the output from the main solution.
	MainOutput string `json:"main_output"`

	// BruteOutput is the output from the brute-force solution.
	BruteOutput string `json:"brute_output"`
}

// CompareOutputs compares the output of the main solution against the
// brute-force solution for each test case. Outputs are compared after
// trimming trailing whitespace from each line and ignoring trailing
// newlines.
func (v *ValidatorService) CompareOutputs(
	ctx context.Context,
	mainOutputs []string,
	bruteOutputs []string,
) (*OutputComparison, error) {
	if len(mainOutputs) != len(bruteOutputs) {
		return nil, fmt.Errorf("output count mismatch: main=%d, brute=%d",
			len(mainOutputs), len(bruteOutputs))
	}

	log.Debug().
		Int("num_outputs", len(mainOutputs)).
		Msg("comparing main vs brute-force outputs")

	matched := 0
	var mismatches []OutputMismatch

	for i := range mainOutputs {
		mainNorm := normalizeOutput(mainOutputs[i])
		bruteNorm := normalizeOutput(bruteOutputs[i])

		if mainNorm == bruteNorm {
			matched++
		} else {
			mismatches = append(mismatches, OutputMismatch{
				Index:       i,
				MainOutput:  truncateStr(mainOutputs[i], 500),
				BruteOutput: truncateStr(bruteOutputs[i], 500),
			})
		}
	}

	result := &OutputComparison{
		AllMatch:   matched == len(mainOutputs),
		Matched:    matched,
		Total:      len(mainOutputs),
		Mismatches: mismatches,
	}

	log.Info().
		Bool("all_match", result.AllMatch).
		Int("matched", matched).
		Int("total", len(mainOutputs)).
		Int("mismatches", len(mismatches)).
		Msg("output comparison completed")

	return result, nil
}

// ---------------------------------------------------------------------------
// FullValidation
// ---------------------------------------------------------------------------

// FullValidationResult contains the complete outcome of validating a problem,
// including individual solution results and output comparison.
type FullValidationResult struct {
	// MainResult is the validation result for the main solution.
	MainResult *SolutionValidationResult `json:"main_result"`

	// BruteResult is the validation result for the brute-force solution.
	BruteResult *SolutionValidationResult `json:"brute_result,omitempty"`

	// Comparison is the output comparison between main and brute solutions.
	Comparison *OutputComparison `json:"comparison,omitempty"`

	// Valid is true when the main solution passes all tests and outputs
	// match the brute-force solution (if available).
	Valid bool `json:"valid"`
}

// FullValidationInput contains all the data needed for a complete problem
// validation.
type FullValidationInput struct {
	// MainCode is the main solution source code.
	MainCode string `json:"main_code"`

	// MainLanguage is the programming language of the main solution.
	MainLanguage string `json:"main_language"`

	// BruteCode is the brute-force solution source code (optional).
	BruteCode string `json:"brute_code,omitempty"`

	// BruteLanguage is the programming language of the brute-force solution.
	BruteLanguage string `json:"brute_language,omitempty"`

	// TestCases are the test case inputs to validate against.
	TestCases []TestCaseInput `json:"test_cases"`

	// Limits are the execution resource limits.
	Limits ExecutionLimits `json:"limits"`
}

// FullValidation runs the complete validation pipeline for a problem:
//  1. Run the main solution against all test cases.
//  2. If brute-force code is provided, run it against the same test cases.
//  3. Compare outputs between main and brute-force solutions.
func (v *ValidatorService) FullValidation(
	ctx context.Context,
	input FullValidationInput,
) (*FullValidationResult, error) {
	log.Info().
		Str("main_language", input.MainLanguage).
		Bool("has_brute", input.BruteCode != "").
		Int("num_testcases", len(input.TestCases)).
		Msg("running full validation pipeline")

	result := &FullValidationResult{}

	// Step 1: Validate main solution.
	mainResult, err := v.ValidateSolution(
		ctx, input.MainCode, input.MainLanguage,
		input.TestCases, input.Limits,
	)
	if err != nil {
		return nil, fmt.Errorf("validating main solution: %w", err)
	}
	result.MainResult = mainResult

	if !mainResult.AllPassed {
		result.Valid = false
		log.Warn().
			Int("failed", len(mainResult.FailedIndices)).
			Msg("main solution failed some test cases")
		return result, nil
	}

	// Step 2: Validate brute-force solution (if provided).
	if input.BruteCode != "" {
		// Use relaxed limits for brute-force: 3x time, 2x memory.
		bruteLimits := ExecutionLimits{
			TimeLimitMs:   input.Limits.TimeLimitMs * 3,
			MemoryLimitMB: input.Limits.MemoryLimitMB * 2,
		}

		bruteResult, err := v.ValidateSolution(
			ctx, input.BruteCode, input.BruteLanguage,
			input.TestCases, bruteLimits,
		)
		if err != nil {
			return nil, fmt.Errorf("validating brute solution: %w", err)
		}
		result.BruteResult = bruteResult

		if !bruteResult.AllPassed {
			result.Valid = false
			log.Warn().
				Int("failed", len(bruteResult.FailedIndices)).
				Msg("brute-force solution failed some test cases")
			return result, nil
		}

		// Step 3: Compare outputs.
		mainOutputs := make([]string, len(mainResult.Results))
		bruteOutputs := make([]string, len(bruteResult.Results))
		for i, r := range mainResult.Results {
			mainOutputs[i] = r.Stdout
		}
		for i, r := range bruteResult.Results {
			bruteOutputs[i] = r.Stdout
		}

		comparison, err := v.CompareOutputs(ctx, mainOutputs, bruteOutputs)
		if err != nil {
			return nil, fmt.Errorf("comparing outputs: %w", err)
		}
		result.Comparison = comparison
		result.Valid = comparison.AllMatch
	} else {
		// Without a brute-force solution, validity is determined by the
		// main solution passing all tests.
		result.Valid = mainResult.AllPassed
	}

	log.Info().
		Bool("valid", result.Valid).
		Msg("full validation pipeline completed")

	return result, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// normalizeOutput trims trailing whitespace from each line and removes
// trailing empty lines, producing a canonical form suitable for comparison.
func normalizeOutput(s string) string {
	lines := strings.Split(s, "\n")

	// Trim trailing whitespace from each line.
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t\r")
	}

	// Remove trailing empty lines.
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	return strings.Join(lines, "\n")
}

// truncateStr shortens s to at most maxLen characters, appending "..." if
// truncated. This is used to keep mismatch messages readable.
func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
