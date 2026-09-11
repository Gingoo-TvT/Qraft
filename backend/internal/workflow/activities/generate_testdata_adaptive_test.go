package activities

import (
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
)

func adaptiveTestDataConfig() domain.TestDataConfig {
	return domain.TestDataConfig{
		NumTestCases:  20,
		AutoCaseCount: true,
		NumSamples:    2,
		BoundaryConfig: domain.BoundaryConfig{
			IncludeMinCase: true,
			IncludeMaxCase: true,
		},
	}
}

func adaptiveCases() []TestCaseData {
	return []TestCaseData{
		{Input: "1\n", Description: "minimum small exhaustive", Coverage: []string{"minimum", "small_exhaustive"}},
		{Input: "2\n", Description: "threshold adversarial", Coverage: []string{"threshold", "adversarial"}},
		{Input: "3\n", Description: "maximum-scale randomized stress", Coverage: []string{"maximum_random", "random"}},
		{Input: "4\n", Description: "random medium", Coverage: []string{"random"}},
		{Input: "5\n", Description: "zero boundary", Coverage: []string{"zero"}},
		{Input: "6\n", Description: "negative boundary", Coverage: []string{"negative"}},
		{Input: "7\n", Description: "adversarial structure", Coverage: []string{"adversarial"}},
		{Input: "8\n", Description: "metamorphic relation", Coverage: []string{"metamorphic"}},
		{Input: "9\n", Description: "random alternate", Coverage: []string{"random"}},
		{Input: "10\n", Description: "boundary alternate", Coverage: []string{"threshold"}},
	}
}

func TestAdaptivePromptRequestsSmallestSufficientCoverageSuite(t *testing.T) {
	prompt := buildTestDataPrompt("Given n numbers, compute the answer.", adaptiveTestDataConfig())
	for _, want := range []string{
		"Case-count mode: adaptive",
		"[10,20]",
		"smallest sufficient",
		"maximum-scale randomized/pressure",
		"maximum_random",
		"small_exhaustive",
		"custom cases included",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("adaptive prompt omitted %q:\n%s", want, prompt)
		}
	}
}

func TestAdaptiveMergeDoesNotSilentlyDropCornerCases(t *testing.T) {
	config := adaptiveTestDataConfig()
	config.CustomCases = []domain.CustomTestCase{{Input: "custom\n", Description: "custom threshold"}}
	merged := mergeCustomCasesForConfig(adaptiveCases(), config)
	if len(merged) != len(adaptiveCases())+1 {
		t.Fatalf("adaptive merge length = %d, want %d", len(merged), len(adaptiveCases())+1)
	}
	if merged[0].Origin != TestCaseOriginCustom || merged[1].Description != adaptiveCases()[0].Description {
		t.Fatalf("adaptive merge order = %#v", merged[:2])
	}

	explicit := config
	explicit.AutoCaseCount = false
	explicit.NumTestCases = 3
	truncated := mergeCustomCasesForConfig(adaptiveCases(), explicit)
	if len(truncated) != 3 {
		t.Fatalf("explicit merge length = %d, want 3", len(truncated))
	}
}

func TestAdaptiveCoverageGateRequiresScaleAndSmallCases(t *testing.T) {
	config := adaptiveTestDataConfig()
	if err := validateAdaptiveTestDataResult(adaptiveCases(), config); err != nil {
		t.Fatalf("valid adaptive coverage rejected: %v", err)
	}

	missing := append([]TestCaseData(nil), adaptiveCases()...)
	missing[2].Coverage = []string{"random"}
	missing[2].Description = "random medium"
	if err := validateAdaptiveTestDataResult(missing, config); err == nil || !strings.Contains(err.Error(), "maximum_random") {
		t.Fatalf("missing maximum-random coverage error = %v", err)
	}

	short := adaptiveCases()[:9]
	if err := validateAdaptiveTestDataResult(short, config); err == nil || !strings.Contains(err.Error(), "adaptive range") {
		t.Fatalf("short adaptive suite error = %v", err)
	}
}

func TestParseTestDataResponseNormalizesCoverageLabels(t *testing.T) {
	result, err := parseTestDataResponse(`{"test_cases":[{"input":"1\n","group_id":1,"is_sample":true,"description":"最大规模随机压力","coverage":["max-scale-random","random","random"]}],"generator_code":""}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.TestCases) != 1 || strings.Join(result.TestCases[0].Coverage, ",") != "maximum_random,random" {
		t.Fatalf("normalized coverage = %#v", result.TestCases)
	}
}

func TestNormalizeSampleFlagsEnforcesRequestedCardinality(t *testing.T) {
	cases := []TestCaseData{
		{Input: "a", IsSample: true},
		{Input: "b", IsSample: true},
		{Input: "c", IsSample: true},
		{Input: "d"},
	}
	normalizeSampleFlags(cases, 2)
	if cases[0].IsSample != true || cases[1].IsSample != true || cases[2].IsSample || cases[3].IsSample {
		t.Fatalf("normalized sample flags = [%v,%v,%v,%v], want [true,true,false,false]", cases[0].IsSample, cases[1].IsSample, cases[2].IsSample, cases[3].IsSample)
	}

	cases = []TestCaseData{{Input: "a"}, {Input: "b"}, {Input: "c"}}
	normalizeSampleFlags(cases, 2)
	if !cases[0].IsSample || !cases[1].IsSample || cases[2].IsSample {
		t.Fatalf("filled sample flags = [%v,%v,%v], want [true,true,false]", cases[0].IsSample, cases[1].IsSample, cases[2].IsSample)
	}

	normalizeSampleFlags(cases, 0)
	for index, testCase := range cases {
		if testCase.IsSample {
			t.Fatalf("zero requested samples left case %d marked", index)
		}
	}
}

func TestConvertCustomCasesProvidesStableDescriptionWhenOmitted(t *testing.T) {
	cases := convertCustomCases([]domain.CustomTestCase{{Input: "1\n"}})
	if len(cases) != 1 || cases[0].Description == "" || !strings.Contains(cases[0].Description, "custom case 1") {
		t.Fatalf("custom case description = %#v", cases)
	}
	if !strings.Contains(strings.Join(cases[0].Coverage, ","), "custom") {
		t.Fatalf("custom case coverage = %#v", cases[0].Coverage)
	}
}
