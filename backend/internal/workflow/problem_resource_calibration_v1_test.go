package workflow

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/workflow/activities"
)

func TestDeriveProblemResourceCalibrationV1UsesMeasuredPeaks(t *testing.T) {
	benchmarkLimits := problemResourceBenchmarkLimitsV1()
	benchmark := activities.SandboxResult{
		Outputs:    []string{"a", "b"},
		TimeTaken:  []time.Duration{240 * time.Millisecond, 1501 * time.Millisecond},
		MemoryUsed: []int64{10 << 20, 100 << 20},
		Audit:      sandboxReceiptFixtureV1("benchmark", benchmarkLimits),
	}

	calibration, err := deriveProblemResourceCalibrationV1(benchmark, 2)
	if err != nil {
		t.Fatal(err)
	}
	if calibration.ObservedCaseCount != 2 || calibration.ObservedMaxTimeMS != 1501 || calibration.ObservedMaxMemoryBytes != 100<<20 {
		t.Fatalf("unexpected observed peaks: %+v", calibration)
	}
	if calibration.FinalLimits.TimeLimitMs != 4600 || calibration.FinalLimits.MemoryLimitMB != 256 || calibration.StackLimitMB != 256 {
		t.Fatalf("unexpected derived limits: %+v", calibration)
	}

	finalResult := activities.SandboxResult{
		Outputs:    []string{"a", "b"},
		TimeTaken:  []time.Duration{200 * time.Millisecond, 1400 * time.Millisecond},
		MemoryUsed: []int64{10 << 20, 99 << 20},
		Audit:      sandboxReceiptFixtureV1("final", calibration.FinalLimits),
	}
	calibration, err = finalizeProblemResourceCalibrationV1(calibration, finalResult)
	if err != nil {
		t.Fatal(err)
	}
	if calibration.FinalAudit.RunID != "final-run" {
		t.Fatalf("final receipt was not bound: %+v", calibration.FinalAudit)
	}

	params := domain.DefaultProblemGenParams()
	params = applyProblemResourceCalibrationV1(params, calibration)
	if params.TimeLimit != 4600 || params.MemoryLimit != 256 {
		t.Fatalf("stored params did not receive final limits: %+v", params)
	}
	if _, ok := params.MetadataExtras[problemGenerationResourceCalibrationStateKeyV1]; !ok {
		t.Fatalf("stored params omitted calibration evidence: %+v", params.MetadataExtras)
	}
}

func TestDeriveProblemResourceCalibrationV1AppliesSafetyFloors(t *testing.T) {
	calibration, err := deriveProblemResourceCalibrationV1(activities.SandboxResult{
		Outputs:    []string{"ok"},
		TimeTaken:  []time.Duration{0},
		MemoryUsed: []int64{0},
	}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if calibration.FinalLimits.TimeLimitMs != 1000 || calibration.FinalLimits.MemoryLimitMB != 256 {
		t.Fatalf("floor limits = %+v", calibration.FinalLimits)
	}
}

func TestDeriveProblemResourceCalibrationV1RequiresFullSuiteCoverage(t *testing.T) {
	_, err := deriveProblemResourceCalibrationV1(activities.SandboxResult{
		Outputs:    []string{"only-one"},
		TimeTaken:  []time.Duration{time.Millisecond},
		MemoryUsed: []int64{1 << 20},
	}, 2)
	if err == nil || !strings.Contains(err.Error(), "full generated suite") {
		t.Fatalf("partial-suite error = %v", err)
	}
}

func TestDeriveProblemResourceCalibrationV1RejectsUndeployableLimit(t *testing.T) {
	_, err := deriveProblemResourceCalibrationV1(activities.SandboxResult{
		Outputs:    []string{"ok"},
		TimeTaken:  []time.Duration{4 * time.Second},
		MemoryUsed: []int64{16 << 20},
	}, 1)
	if err == nil || !strings.Contains(err.Error(), "above sandbox ceiling") {
		t.Fatalf("time ceiling error = %v", err)
	}

	_, err = deriveProblemResourceCalibrationV1(activities.SandboxResult{
		Outputs:    []string{"ok"},
		TimeTaken:  []time.Duration{100 * time.Millisecond},
		MemoryUsed: []int64{250 << 20},
	}, 1)
	if err == nil || !strings.Contains(err.Error(), "above sandbox ceiling") {
		t.Fatalf("memory ceiling error = %v", err)
	}
}

func TestProblemResourceBruteLimitsV1StayWithinPermanentSandbox(t *testing.T) {
	tests := []struct {
		name       string
		final      activities.ExecutionLimits
		wantTime   int
		wantMemory int
	}{
		{
			name:       "small public memory still gets fixed oracle budget",
			final:      activities.ExecutionLimits{TimeLimitMs: 1000, MemoryLimitMB: 64},
			wantTime:   1000,
			wantMemory: problemResourceBruteMemoryLimitMBV1,
		},
		{
			name:       "time remains capped at benchmark ceiling",
			final:      activities.ExecutionLimits{TimeLimitMs: 4600, MemoryLimitMB: 320},
			wantTime:   4600,
			wantMemory: problemResourceBruteMemoryLimitMBV1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			limits := problemResourceBruteLimitsV1(tt.final)
			if limits.TimeLimitMs != tt.wantTime || limits.MemoryLimitMB != tt.wantMemory {
				t.Fatalf("brute limits = %+v, want time=%d memory=%d", limits, tt.wantTime, tt.wantMemory)
			}
		})
	}
}

func TestProblemResourceBruteLimitsLegacyV0PreservesRecordedRelaxedTime(t *testing.T) {
	limits := problemResourceBruteLimitsLegacyV0(activities.ExecutionLimits{TimeLimitMs: 4600, MemoryLimitMB: 240})
	if limits.TimeLimitMs != problemResourceBenchmarkTimeLimitMSV1 || limits.MemoryLimitMB != problemResourceBruteMemoryLimitMBV1 {
		t.Fatalf("legacy brute limits = %+v, want time=%d memory=%d", limits, problemResourceBenchmarkTimeLimitMSV1, problemResourceBruteMemoryLimitMBV1)
	}
}

func sandboxReceiptFixtureV1(name string, limits activities.ExecutionLimits) activities.SandboxAuditMetadata {
	return activities.SandboxAuditMetadata{
		RunID:          name + "-run",
		ManifestDigest: "sha256:" + strings.Repeat("a", 64),
		LimitProfile: strings.Join([]string{
			"execute-v1",
			"time_ms=" + intStringV1(limits.TimeLimitMs),
			"memory_mb=" + intStringV1(limits.MemoryLimitMB),
			"stack_mb=" + intStringV1(limits.MemoryLimitMB),
		}, ":"),
	}
}

func intStringV1(value int) string {
	return fmt.Sprintf("%d", value)
}
