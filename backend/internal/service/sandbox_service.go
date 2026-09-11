package service

import (
	"context"
	"fmt"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/sandbox"
	"github.com/rs/zerolog/log"
)

// ---------------------------------------------------------------------------
// SandboxService
// ---------------------------------------------------------------------------

// SandboxService wraps the sandbox executor to provide high-level code
// compilation and execution operations. It manages resource limits and
// provides convenient methods for running code against test cases.
type SandboxService struct {
	executor sandbox.Executor
}

// NewSandboxService creates a new SandboxService with the given sandbox
// executor.
func NewSandboxService(executor sandbox.Executor) *SandboxService {
	return &SandboxService{executor: executor}
}

// ---------------------------------------------------------------------------
// CompileCode
// ---------------------------------------------------------------------------

// CompileCode compiles source code in the specified language inside the
// sandbox. It returns the compilation result, which includes the path to
// the compiled binary on success.
func (s *SandboxService) CompileCode(ctx context.Context, language string, code string) (*sandbox.CompileResult, error) {
	log.Debug().
		Str("language", language).
		Int("code_len", len(code)).
		Msg("compiling code in sandbox")

	result, err := s.executor.Compile(ctx, code, language)
	if err != nil {
		return nil, fmt.Errorf("compiling %s code: %w", language, err)
	}

	if result.Success {
		log.Info().
			Str("language", language).
			Dur("duration", result.Duration).
			Msg("compilation succeeded")
	} else {
		log.Warn().
			Str("language", language).
			Str("stderr", result.Stderr).
			Msg("compilation failed")
	}

	return result, nil
}

// ---------------------------------------------------------------------------
// RunWithInput
// ---------------------------------------------------------------------------

// ExecutionLimits specifies resource constraints for a sandbox execution,
// using human-friendly units.
type ExecutionLimits struct {
	// TimeLimitMs is the time limit in milliseconds.
	TimeLimitMs int `json:"time_limit_ms"`

	// MemoryLimitMB is the memory limit in megabytes.
	MemoryLimitMB int `json:"memory_limit_mb"`
}

// toResourceLimits converts ExecutionLimits to sandbox.ResourceLimits.
func (el ExecutionLimits) toResourceLimits() sandbox.ResourceLimits {
	limits := sandbox.DefaultLimits()

	if el.TimeLimitMs > 0 {
		limits.TimeLimit = time.Duration(el.TimeLimitMs) * time.Millisecond
	}
	if el.MemoryLimitMB > 0 {
		limits.MemoryLimit = int64(el.MemoryLimitMB) * 1024 * 1024
	}

	return limits
}

// RunWithInput compiles the given source code and runs it with the provided
// standard input inside the sandbox, enforcing the specified resource limits.
// It returns the execution result including stdout, stderr, verdict, and
// resource usage.
func (s *SandboxService) RunWithInput(ctx context.Context, language string, code string, input string, limits ExecutionLimits) (*sandbox.ExecutionResult, error) {
	resLimits := limits.toResourceLimits()

	log.Debug().
		Str("language", language).
		Int("input_len", len(input)).
		Int("time_limit_ms", limits.TimeLimitMs).
		Int("memory_limit_mb", limits.MemoryLimitMB).
		Msg("running code with input in sandbox")

	result, err := s.executor.CompileAndRun(ctx, code, language, input, resLimits)
	if err != nil {
		return nil, fmt.Errorf("running %s code: %w", language, err)
	}

	log.Info().
		Str("language", language).
		Str("verdict", string(result.Verdict)).
		Dur("time_taken", result.TimeTaken).
		Int64("memory_used", result.MemoryUsed).
		Msg("sandbox execution completed")

	return result, nil
}

// ---------------------------------------------------------------------------
// RunAllTestCases
// ---------------------------------------------------------------------------

// TestCaseResult holds the execution result for a single test case along with
// its index.
type TestCaseResult struct {
	// Index of the test case.
	Index int `json:"index"`

	// Verdict from the sandbox execution.
	Verdict sandbox.Verdict `json:"verdict"`

	// Captured standard output.
	Stdout string `json:"stdout"`

	// Captured standard error.
	Stderr string `json:"stderr,omitempty"`

	// Wall-clock time taken.
	TimeTaken time.Duration `json:"time_taken"`

	// Peak memory usage in bytes.
	MemoryUsed int64 `json:"memory_used"`
}

// TestCaseInput represents a single test case's input for batch execution.
type TestCaseInput struct {
	Index int    `json:"index"`
	Input string `json:"input"`
}

// RunAllTestCases compiles the given source code once and then runs it against
// all provided test case inputs, collecting results for each. Execution stops
// early if a compilation error occurs, but runtime errors on individual test
// cases do not halt the batch.
func (s *SandboxService) RunAllTestCases(ctx context.Context, language string, code string, testCases []TestCaseInput, limits ExecutionLimits) ([]TestCaseResult, error) {
	resLimits := limits.toResourceLimits()

	log.Debug().
		Str("language", language).
		Int("num_testcases", len(testCases)).
		Msg("running code against all test cases")

	// Compile once.
	compileResult, err := s.executor.Compile(ctx, code, language)
	if err != nil {
		return nil, fmt.Errorf("compiling %s code: %w", language, err)
	}

	if !compileResult.Success {
		// Return a single CE result for all test cases.
		results := make([]TestCaseResult, len(testCases))
		for i, tc := range testCases {
			results[i] = TestCaseResult{
				Index:   tc.Index,
				Verdict: sandbox.VerdictCE,
				Stderr:  compileResult.Stderr,
			}
		}
		return results, nil
	}

	// Run against each test case.
	results := make([]TestCaseResult, 0, len(testCases))
	for _, tc := range testCases {
		execResult, err := s.executor.Run(ctx, compileResult.BinaryPath, tc.Input, resLimits)
		if err != nil {
			log.Warn().
				Err(err).
				Int("test_index", tc.Index).
				Msg("sandbox execution error for test case")

			results = append(results, TestCaseResult{
				Index:   tc.Index,
				Verdict: sandbox.VerdictRE,
				Stderr:  err.Error(),
			})
			continue
		}

		results = append(results, TestCaseResult{
			Index:      tc.Index,
			Verdict:    execResult.Verdict,
			Stdout:     execResult.Stdout,
			Stderr:     execResult.Stderr,
			TimeTaken:  execResult.TimeTaken,
			MemoryUsed: execResult.MemoryUsed,
		})
	}

	passed := 0
	for _, r := range results {
		if r.Verdict == sandbox.VerdictOK {
			passed++
		}
	}

	log.Info().
		Str("language", language).
		Int("total", len(testCases)).
		Int("passed", passed).
		Msg("batch execution completed")

	return results, nil
}
