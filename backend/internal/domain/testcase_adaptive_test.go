package domain

import (
	"strings"
	"testing"
)

func TestAdaptiveTestDataConfigUsesBoundedRange(t *testing.T) {
	config := DefaultTestDataConfig()
	config.AutoCaseCount = true
	min, max := config.AdaptiveCaseCountRange()
	if min != AdaptiveMinTestCases || max != AdaptiveMaxTestCases {
		t.Fatalf("adaptive range = [%d,%d], want [%d,%d]", min, max, AdaptiveMinTestCases, AdaptiveMaxTestCases)
	}
	if err := config.Validate(); err != nil {
		t.Fatalf("valid adaptive config rejected: %v", err)
	}

	config.NumTestCases = AdaptiveMinTestCases - 1
	if err := config.Validate(); err == nil || !strings.Contains(err.Error(), "capacity") {
		t.Fatalf("undersized adaptive capacity error = %v", err)
	}

	config = DefaultTestDataConfig()
	config.AutoCaseCount = true
	config.CustomCases = make([]CustomTestCase, AdaptiveMaxTestCases+1)
	if err := config.Validate(); err == nil || !strings.Contains(err.Error(), "custom case count") {
		t.Fatalf("oversized adaptive custom cases error = %v", err)
	}

	config = DefaultTestDataConfig()
	config.AutoCaseCount = true
	config.NumSamples = 14
	min, max = config.AdaptiveCaseCountRange()
	if min != 14 || max != AdaptiveMaxTestCases {
		t.Fatalf("sample-aware adaptive range = [%d,%d], want [14,%d]", min, max, AdaptiveMaxTestCases)
	}
	config.NumTestCases = 13
	if err := config.Validate(); err == nil || !strings.Contains(err.Error(), "range is empty") {
		t.Fatalf("sample-infeasible adaptive config error = %v", err)
	}
}

func TestExplicitTestDataConfigRetainsLegacyCountValidation(t *testing.T) {
	config := TestDataConfig{NumTestCases: 3, NumSamples: 3}
	if err := config.Validate(); err != nil {
		t.Fatalf("explicit config unexpectedly failed adaptive-only validation: %v", err)
	}
}

func TestZeroCountNormalizesToAdaptiveContract(t *testing.T) {
	config := TestDataConfig{NumTestCases: 0, NumSamples: 2}
	if err := config.NormalizeForGeneration(); err != nil {
		t.Fatalf("zero-count adaptive config rejected: %v", err)
	}
	if !config.IsAdaptive() || config.MinTestCases != MinAdaptiveTestCases || config.MaxTestCases != MaxAdaptiveTestCases {
		t.Fatalf("normalized adaptive config = %+v", config)
	}
	for _, count := range []int{MinAdaptiveTestCases, MaxAdaptiveTestCases} {
		if err := config.ValidateGeneratedTestCaseCount(count); err != nil {
			t.Fatalf("adaptive count %d rejected: %v", count, err)
		}
	}
	for _, count := range []int{MinAdaptiveTestCases - 1, MaxAdaptiveTestCases + 1} {
		if err := config.ValidateGeneratedTestCaseCount(count); err == nil {
			t.Fatalf("out-of-range adaptive count %d accepted", count)
		}
	}
}

func TestLegacyAdaptiveCapacityDoesNotSilentlyClamp(t *testing.T) {
	config := TestDataConfig{NumTestCases: MaxAdaptiveTestCases + 1, AutoCaseCount: true}
	if err := config.NormalizeForGeneration(); err == nil || !strings.Contains(err.Error(), "capacity") {
		t.Fatalf("out-of-range legacy adaptive capacity error = %v", err)
	}
}
