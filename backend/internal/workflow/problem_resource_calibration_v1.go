package workflow

import (
	"fmt"
	"strings"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/workflow/activities"
)

const (
	problemGenerationResourceCalibrationV1ChangeID = "problem-generation-resource-calibration-v1"
	// problemBruteCorrectnessBudgetV1ChangeID separates the new reference
	// execution contract from histories that already recorded the old relaxed
	// brute limits.  New runs use the problem's actual time limit; old runs
	// replay the legacy 3x/2x arguments without command-history drift.
	problemBruteCorrectnessBudgetV1ChangeID        = "problem-brute-correctness-budget-v1"
	problemGenerationResourceCalibrationStateKeyV1 = "resource_calibration_v1"
	problemResourceCalibrationSchemaVersionV1      = 1
	problemResourceCalibrationPolicyV1             = "std-peak-v1:time=max(1000ms,ceil100(max(3x,x+500ms)));memory=max(256MiB,ceil16(2x+32MiB));stack=memory;final-rerun=required"

	// These are the permanent sandbox service's configured execution ceilings.
	// Calibration intentionally benchmarks below the protocol's theoretical
	// maximum so a derived final limit is guaranteed to be deployable here.
	problemResourceBenchmarkTimeLimitMSV1 = 10_000
	problemResourceBenchmarkMemoryMBV1    = 512
	// The brute/reference solution is an internal oracle, not the submitted
	// program. Give it a stable, generous memory budget instead of deriving a
	// small limit from the generated solution's public memory limit. Keeping
	// this at the permanent sandbox ceiling also avoids starving STL-heavy
	// reference implementations on selected differential cases.
	problemResourceBruteMemoryLimitMBV1 = activities.ReferenceSolutionMemoryLimitMB
	problemResourceMinimumTimeLimitMSV1 = 1_000
	problemResourceMinimumMemoryMBV1    = 256
)

func problemResourceBenchmarkLimitsV1() activities.ExecutionLimits {
	return activities.ExecutionLimits{
		TimeLimitMs:   problemResourceBenchmarkTimeLimitMSV1,
		MemoryLimitMB: problemResourceBenchmarkMemoryMBV1,
	}
}

func deriveProblemResourceCalibrationV1(
	benchmark activities.SandboxResult,
	expectedCaseCount int,
) (activities.ResourceCalibrationV1, error) {
	if expectedCaseCount <= 0 {
		return activities.ResourceCalibrationV1{}, fmt.Errorf("benchmark expected case count must be positive, got %d", expectedCaseCount)
	}
	caseCount := len(benchmark.TimeTaken)
	if caseCount != expectedCaseCount || len(benchmark.Outputs) != expectedCaseCount {
		return activities.ResourceCalibrationV1{}, fmt.Errorf(
			"benchmark did not cover the full generated suite: time=%d outputs=%d want=%d",
			caseCount,
			len(benchmark.Outputs),
			expectedCaseCount,
		)
	}
	if len(benchmark.MemoryUsed) != caseCount {
		return activities.ResourceCalibrationV1{}, fmt.Errorf(
			"benchmark observation count mismatch: time=%d memory=%d",
			caseCount,
			len(benchmark.MemoryUsed),
		)
	}

	var maxTimeMS int64
	var maxMemoryBytes int64
	for index := 0; index < caseCount; index++ {
		elapsed := benchmark.TimeTaken[index]
		memoryBytes := benchmark.MemoryUsed[index]
		if elapsed < 0 {
			return activities.ResourceCalibrationV1{}, fmt.Errorf("benchmark case %d returned negative time %s", index+1, elapsed)
		}
		if memoryBytes < 0 {
			return activities.ResourceCalibrationV1{}, fmt.Errorf("benchmark case %d returned negative memory %d", index+1, memoryBytes)
		}
		elapsedMS := elapsed.Milliseconds()
		if elapsed > 0 && elapsedMS == 0 {
			elapsedMS = 1
		}
		if elapsedMS > maxTimeMS {
			maxTimeMS = elapsedMS
		}
		if memoryBytes > maxMemoryBytes {
			maxMemoryBytes = memoryBytes
		}
	}
	if maxTimeMS < 1 {
		maxTimeMS = 1
	}

	timeCandidate := maxInt64V1(maxTimeMS*3, maxTimeMS+500)
	timeCandidate = maxInt64V1(timeCandidate, problemResourceMinimumTimeLimitMSV1)
	finalTimeMS := roundUpInt64V1(timeCandidate, 100)
	if finalTimeMS > problemResourceBenchmarkTimeLimitMSV1 {
		return activities.ResourceCalibrationV1{}, fmt.Errorf(
			"measured std peak %dms requires derived time limit %dms above sandbox ceiling %dms",
			maxTimeMS,
			finalTimeMS,
			problemResourceBenchmarkTimeLimitMSV1,
		)
	}

	const bytesPerMiB = int64(1 << 20)
	observedMemoryMB := (maxMemoryBytes + bytesPerMiB - 1) / bytesPerMiB
	memoryCandidate := maxInt64V1(observedMemoryMB*2+32, problemResourceMinimumMemoryMBV1)
	finalMemoryMB := roundUpInt64V1(memoryCandidate, 16)
	if finalMemoryMB > problemResourceBenchmarkMemoryMBV1 {
		return activities.ResourceCalibrationV1{}, fmt.Errorf(
			"measured std peak %d bytes requires derived memory limit %dMiB above sandbox ceiling %dMiB",
			maxMemoryBytes,
			finalMemoryMB,
			problemResourceBenchmarkMemoryMBV1,
		)
	}

	finalLimits := activities.ExecutionLimits{
		TimeLimitMs:   int(finalTimeMS),
		MemoryLimitMB: int(finalMemoryMB),
	}
	return activities.ResourceCalibrationV1{
		SchemaVersion:          problemResourceCalibrationSchemaVersionV1,
		Policy:                 problemResourceCalibrationPolicyV1,
		ObservedCaseCount:      caseCount,
		BenchmarkLimits:        problemResourceBenchmarkLimitsV1(),
		ObservedMaxTimeMS:      maxTimeMS,
		ObservedMaxMemoryBytes: maxMemoryBytes,
		FinalLimits:            finalLimits,
		StackLimitMB:           finalLimits.MemoryLimitMB,
		BenchmarkAudit:         benchmark.Audit,
	}, nil
}

func finalizeProblemResourceCalibrationV1(
	calibration activities.ResourceCalibrationV1,
	finalResult activities.SandboxResult,
) (activities.ResourceCalibrationV1, error) {
	if len(finalResult.TimeTaken) != calibration.ObservedCaseCount ||
		len(finalResult.MemoryUsed) != calibration.ObservedCaseCount ||
		len(finalResult.Outputs) != calibration.ObservedCaseCount {
		return activities.ResourceCalibrationV1{}, fmt.Errorf(
			"final sandbox observation count mismatch: time=%d memory=%d outputs=%d want=%d",
			len(finalResult.TimeTaken),
			len(finalResult.MemoryUsed),
			len(finalResult.Outputs),
			calibration.ObservedCaseCount,
		)
	}
	if err := validateProblemResourceReceiptV1("benchmark", calibration.BenchmarkAudit, calibration.BenchmarkLimits); err != nil {
		return activities.ResourceCalibrationV1{}, err
	}
	if err := validateProblemResourceReceiptV1("final", finalResult.Audit, calibration.FinalLimits); err != nil {
		return activities.ResourceCalibrationV1{}, err
	}
	calibration.FinalAudit = finalResult.Audit
	return calibration, nil
}

func validateProblemResourceReceiptV1(
	name string,
	audit activities.SandboxAuditMetadata,
	limits activities.ExecutionLimits,
) error {
	if strings.TrimSpace(audit.RunID) == "" || strings.TrimSpace(audit.ManifestDigest) == "" {
		return fmt.Errorf("%s sandbox receipt is missing run identity", name)
	}
	for _, field := range []string{
		fmt.Sprintf("time_ms=%d", limits.TimeLimitMs),
		fmt.Sprintf("memory_mb=%d", limits.MemoryLimitMB),
		fmt.Sprintf("stack_mb=%d", limits.MemoryLimitMB),
	} {
		if !strings.Contains(audit.LimitProfile, field) {
			return fmt.Errorf("%s sandbox receipt limit profile %q is missing %q", name, audit.LimitProfile, field)
		}
	}
	return nil
}

func applyProblemResourceCalibrationV1(
	params domain.ProblemGenParams,
	calibration activities.ResourceCalibrationV1,
) domain.ProblemGenParams {
	params.TimeLimit = calibration.FinalLimits.TimeLimitMs
	params.MemoryLimit = calibration.FinalLimits.MemoryLimitMB
	params.MetadataExtras = cloneProblemGenerationMetadataV1(params.MetadataExtras)
	params.MetadataExtras[problemGenerationResourceCalibrationStateKeyV1] = calibration
	return params
}

// problemResourceBruteLimitsV1 is the current correctness-only oracle
// contract.  The selected brute cases must finish within the same time limit
// advertised for the problem; the fixed memory ceiling is independent of the
// contestant limit.  A brute timeout therefore identifies a defective
// reference program, never a slow main solution.
func problemResourceBruteLimitsV1(final activities.ExecutionLimits) activities.ExecutionLimits {
	timeLimit := final.TimeLimitMs
	if timeLimit < 1 {
		timeLimit = problemResourceMinimumTimeLimitMSV1
	}
	return activities.ExecutionLimits{
		TimeLimitMs:   timeLimit,
		MemoryLimitMB: problemResourceBruteMemoryLimitMBV1,
	}
}

// problemResourceBruteLimitsLegacyV0 preserves the pre-correctness-budget
// reference arguments for histories that have already recorded the old
// activity command.  At that point the resource-calibration path already used
// the fixed sandbox ceiling for memory, so keep that exact value for replay.
// It is intentionally not used by new generation or validation runs.
func problemResourceBruteLimitsLegacyV0(final activities.ExecutionLimits) activities.ExecutionLimits {
	return activities.ExecutionLimits{
		TimeLimitMs:   minIntV1(final.TimeLimitMs*3, problemResourceBenchmarkTimeLimitMSV1),
		MemoryLimitMB: problemResourceBruteMemoryLimitMBV1,
	}
}

func roundUpInt64V1(value, quantum int64) int64 {
	return ((value + quantum - 1) / quantum) * quantum
}

func maxInt64V1(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}

func minIntV1(left, right int) int {
	if left < right {
		return left
	}
	return right
}
