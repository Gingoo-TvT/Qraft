package activities

import (
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
)

func TestGeneratedAndCustomInputCapacityBoundariesFailClosed(t *testing.T) {
	exactCase := strings.Repeat("x", int(generatorMaxOutputBytes))
	if err := validateGeneratedTestInputs([]TestCaseData{{Input: exactCase}}); err != nil {
		t.Fatalf("exact per-case limit rejected: %v", err)
	}
	if err := validateGeneratedTestInputs([]TestCaseData{{Input: exactCase + "x"}}); err == nil {
		t.Fatal("per-case limit +1 was accepted")
	}

	exactBatch := []TestCaseData{
		{Input: exactCase},
		{Input: exactCase},
		{Input: exactCase},
		{Input: exactCase},
	}
	if err := validateGeneratedTestInputs(exactBatch); err != nil {
		t.Fatalf("exact batch limit rejected: %v", err)
	}
	if err := validateGeneratedTestInputs(append(exactBatch, TestCaseData{Input: "x"})); err == nil {
		t.Fatal("batch limit +1 was accepted")
	}

	config := domain.TestDataConfig{
		NumTestCases: 1,
		CustomCases: []domain.CustomTestCase{{
			Input: exactCase + "x",
		}},
	}
	if _, err := mergeCustomCasesAndValidate([]TestCaseData{{Input: "1\n"}}, config); err == nil {
		t.Fatal("oversized prepended custom case bypassed merged input validation")
	}
	if generatorMaxBatchCases != 1 {
		t.Fatalf("generator batch cases = %d, want 1 for worst-case JSON escaping", generatorMaxBatchCases)
	}
}

func TestTestDataExternalizationUsesAggregateTemporalPayloadSize(t *testing.T) {
	exactSingle := []TestCaseData{{
		Input:    strings.Repeat("x", temporalInlineTestDataThreshold),
		IsSample: true,
	}}
	if shouldExternalizeTestInputs(exactSingle) {
		t.Fatal("single input at inline threshold was externalized")
	}

	many := make([]TestCaseData, 32)
	for index := range many {
		many[index] = TestCaseData{
			Input:    strings.Repeat("x", temporalInlineTestDataThreshold-1),
			IsSample: index == 0,
		}
	}
	if !shouldExternalizeTestInputs(many) {
		t.Fatal("aggregate multi-case Temporal payload was left inline")
	}
	if !shouldExternalizeTestInputs([]TestCaseData{{Input: strings.Repeat("x", temporalInlineTestDataThreshold+1)}}) {
		t.Fatal("single input over inline threshold was left inline")
	}
}
