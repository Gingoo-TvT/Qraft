// Package sandbox provides a sandboxed code execution environment for
// compiling and running untrusted contestant and generator code. It supports
// multiple languages and enforces strict resource limits.
package sandbox

import (
	"context"
	"time"
)

// ---------------------------------------------------------------------------
// Verdict
// ---------------------------------------------------------------------------

// Verdict classifies the outcome of a sandboxed execution.
type Verdict string

const (
	// VerdictOK means the program executed successfully within limits.
	VerdictOK Verdict = "OK"
	// VerdictTLE means the program exceeded its time limit.
	VerdictTLE Verdict = "TLE"
	// VerdictMLE means the program exceeded its memory limit.
	VerdictMLE Verdict = "MLE"
	// VerdictRE means the program crashed at runtime.
	VerdictRE Verdict = "RE"
	// VerdictCE means the source code failed to compile.
	VerdictCE Verdict = "CE"
)

// IsValid reports whether the verdict is one of the known values.
func (v Verdict) IsValid() bool {
	switch v {
	case VerdictOK, VerdictTLE, VerdictMLE, VerdictRE, VerdictCE:
		return true
	default:
		return false
	}
}

// ---------------------------------------------------------------------------
// CompileResult
// ---------------------------------------------------------------------------

// CompileResult holds the output of a compilation step.
type CompileResult struct {
	// Path to the compiled binary (empty on failure).
	BinaryPath string `json:"binary_path,omitempty"`

	// Whether compilation succeeded.
	Success bool `json:"success"`

	// Compiler stdout output.
	Stdout string `json:"stdout"`

	// Compiler stderr output (warnings, errors).
	Stderr string `json:"stderr"`

	// Wall-clock duration of the compilation.
	Duration time.Duration `json:"duration"`
}

// ---------------------------------------------------------------------------
// ExecutionResult
// ---------------------------------------------------------------------------

// ExecutionResult holds the output of a sandboxed code execution.
type ExecutionResult struct {
	// Process exit code.
	ExitCode int `json:"exit_code"`

	// Captured standard output.
	Stdout string `json:"stdout"`

	// Captured standard error.
	Stderr string `json:"stderr"`

	// Wall-clock time taken by the execution.
	TimeTaken time.Duration `json:"time_taken"`

	// Peak memory usage in bytes.
	MemoryUsed int64 `json:"memory_used"`

	// Signal name if the process was killed by a signal (e.g. "SIGKILL").
	Signal string `json:"signal,omitempty"`

	// Verdict classifying the execution outcome.
	Verdict Verdict `json:"verdict"`
}

// ---------------------------------------------------------------------------
// Executor interface
// ---------------------------------------------------------------------------

// Executor runs code in a sandboxed environment. Implementations must be safe
// for concurrent use from multiple goroutines.
type Executor interface {
	// Compile compiles the given source code in the specified language and
	// returns a CompileResult. The binary path in the result can be passed
	// to Run for execution.
	Compile(ctx context.Context, source string, language string) (*CompileResult, error)

	// Run executes a previously compiled binary with the given standard input
	// and resource limits. The binary parameter is a path returned from a
	// prior Compile call.
	Run(ctx context.Context, binary string, input string, limits ResourceLimits) (*ExecutionResult, error)

	// CompileAndRun is a convenience method that compiles the source and, if
	// compilation succeeds, immediately runs the resulting binary. If
	// compilation fails, the returned ExecutionResult will carry VerdictCE.
	CompileAndRun(ctx context.Context, source string, language string, input string, limits ResourceLimits) (*ExecutionResult, error)

	// Cleanup removes any temporary files and directories created by this
	// executor instance. It should be called when the executor is no longer
	// needed.
	Cleanup(ctx context.Context) error
}
