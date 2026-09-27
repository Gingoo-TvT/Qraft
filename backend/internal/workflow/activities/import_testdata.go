package activities

import (
	"context"
	"errors"
	"fmt"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
)

// Validate executable output inside the same bounded repair loop as JSON. Only
// accept a complete suite; a failed attempt never leaks partially filled cases.
func (a *Activities) prepareImportedTestData(ctx context.Context, result *TestDataResult, config domain.TestDataConfig) error {
	result.TestCases = mergeCustomCasesForConfig(result.TestCases, config)
	if err := config.ValidateGeneratedTestCaseCount(len(result.TestCases)); err != nil {
		return err
	}
	if result.DeclaredTestCaseCount > 0 && result.DeclaredTestCaseCount != len(result.TestCases) {
		return fmt.Errorf("declared %d final test cases but produced %d", result.DeclaredTestCaseCount, len(result.TestCases))
	}
	if config.NumSamples > len(result.TestCases) {
		return fmt.Errorf("requested samples exceed the number of test cases")
	}
	normalizeSampleFlags(result.TestCases, config.NumSamples)
	if config.IsAdaptive() {
		if err := validateAdaptiveTestDataResult(result.TestCases, config); err != nil {
			return err
		}
	}
	if result.GeneratorCode != "" {
		execution, err := a.executeGeneratorWithEvidence(ctx, result.GeneratorCode, result.TestCases)
		if err != nil {
			var invalid *invalidGeneratedTestArtifactError
			if errors.As(err, &invalid) {
				return err
			}
			return &importValidationUnavailable{cause: err}
		}
		result.TestCases, result.GeneratorBatches = execution.TestCases, execution.Batches
	}
	return validateGeneratedTestInputs(result.TestCases)
}
