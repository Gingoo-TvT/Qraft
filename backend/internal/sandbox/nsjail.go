package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog"
)

// ---------------------------------------------------------------------------
// Language helpers
// ---------------------------------------------------------------------------

// languageExt maps language identifiers to source file extensions.
var languageExt = map[string]string{
	"cpp":     ".cpp",
	"c":       ".c",
	"python":  ".py",
	"python3": ".py",
	"java":    ".java",
	"go":      ".go",
}

// compileCommand returns the compiler arguments for the given language. The
// returned slice represents the full command (binary + args). srcPath is the
// source file and outPath is the desired output binary path.
func compileCommand(language, srcPath, outPath string) ([]string, error) {
	switch language {
	case "cpp":
		return []string{
			"g++", "-std=c++17", "-O2", "-Wall", "-Wextra",
			"-DONLINE_JUDGE",
			"-o", outPath,
			srcPath,
		}, nil
	case "c":
		return []string{
			"gcc", "-std=c17", "-O2", "-Wall", "-Wextra",
			"-DONLINE_JUDGE",
			"-o", outPath,
			srcPath,
		}, nil
	case "java":
		return []string{
			"javac", "-d", filepath.Dir(outPath), srcPath,
		}, nil
	case "go":
		return []string{
			"go", "build", "-o", outPath, srcPath,
		}, nil
	case "python", "python3":
		// Python is interpreted; we "compile" by syntax-checking only.
		return []string{
			"python3", "-m", "py_compile", srcPath,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported language: %q", language)
	}
}

// runCommand returns the execution arguments for the given language. binary is
// the path produced by the compile step.
func runCommand(language, binary string) []string {
	switch language {
	case "python", "python3":
		return []string{"python3", binary}
	case "java":
		// For Java, binary is the directory containing .class files. We
		// assume the main class is named "Main".
		return []string{"java", "-cp", binary, "Main"}
	default:
		return []string{binary}
	}
}

// ---------------------------------------------------------------------------
// NsjailExecutor
// ---------------------------------------------------------------------------

// NsjailExecutor implements the Executor interface using nsjail for process
// sandboxing.
type NsjailExecutor struct {
	// Path to the nsjail binary.
	nsjailBinary string

	// Path to the nsjail configuration file.
	nsjailConfig string

	// Base directory for temporary files. Each Compile/Run cycle creates
	// its own subdirectory here.
	workDir string

	// Logger scoped to sandbox operations.
	logger zerolog.Logger
}

// NsjailOption is a functional option for configuring NsjailExecutor.
type NsjailOption func(*NsjailExecutor)

// WithLogger sets a custom logger.
func WithLogger(l zerolog.Logger) NsjailOption {
	return func(e *NsjailExecutor) {
		e.logger = l
	}
}

// WithWorkDir overrides the temporary work directory base path.
func WithWorkDir(dir string) NsjailOption {
	return func(e *NsjailExecutor) {
		e.workDir = dir
	}
}

// NewNsjailExecutor creates a new NsjailExecutor with the given nsjail binary
// and configuration file paths. If no work directory is specified via options,
// a temporary directory is created under os.TempDir().
func NewNsjailExecutor(binary, configPath string, opts ...NsjailOption) (*NsjailExecutor, error) {
	e := &NsjailExecutor{
		nsjailBinary: binary,
		nsjailConfig: configPath,
		logger:       zerolog.Nop(),
	}

	for _, opt := range opts {
		opt(e)
	}

	// Verify the nsjail binary exists.
	if _, err := exec.LookPath(binary); err != nil {
		return nil, fmt.Errorf("nsjail binary not found at %q: %w", binary, err)
	}

	// Create work directory if not set.
	if e.workDir == "" {
		dir, err := os.MkdirTemp("", "algoforge-sandbox-*")
		if err != nil {
			return nil, fmt.Errorf("creating work directory: %w", err)
		}
		e.workDir = dir
	}

	e.logger.Info().
		Str("binary", binary).
		Str("config", configPath).
		Str("work_dir", e.workDir).
		Msg("nsjail executor initialised")

	return e, nil
}

// ---------------------------------------------------------------------------
// Compile
// ---------------------------------------------------------------------------

// Compile compiles the given source code in the specified language. On success
// the CompileResult.BinaryPath field contains the path to the compiled binary.
func (e *NsjailExecutor) Compile(ctx context.Context, source string, language string) (*CompileResult, error) {
	start := time.Now()

	ext, ok := languageExt[language]
	if !ok {
		return nil, fmt.Errorf("unsupported language: %q", language)
	}

	// Create an isolated subdirectory for this compilation.
	compileDir, err := os.MkdirTemp(e.workDir, "compile-*")
	if err != nil {
		return nil, fmt.Errorf("creating compile dir: %w", err)
	}

	srcFile := filepath.Join(compileDir, "source"+ext)
	if err := os.WriteFile(srcFile, []byte(source), 0644); err != nil {
		return nil, fmt.Errorf("writing source file: %w", err)
	}

	outFile := filepath.Join(compileDir, "binary")
	if language == "java" {
		// For Java the "binary" is the output directory.
		outFile = compileDir
	}

	cmdArgs, err := compileCommand(language, srcFile, outFile)
	if err != nil {
		return nil, err
	}

	e.logger.Debug().
		Str("language", language).
		Strs("cmd", cmdArgs).
		Msg("compiling source")

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, cmdArgs[0], cmdArgs[1:]...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Dir = compileDir

	runErr := cmd.Run()
	duration := time.Since(start)

	result := &CompileResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		Duration: duration,
	}

	if runErr != nil {
		e.logger.Warn().
			Err(runErr).
			Str("stderr", result.Stderr).
			Msg("compilation failed")
		result.Success = false
		return result, nil
	}

	// For Python, the "binary" is the original source file.
	if language == "python" || language == "python3" {
		outFile = srcFile
	}

	result.Success = true
	result.BinaryPath = outFile

	e.logger.Info().
		Str("language", language).
		Dur("duration", duration).
		Msg("compilation succeeded")

	return result, nil
}

// ---------------------------------------------------------------------------
// Run
// ---------------------------------------------------------------------------

// Run executes a binary inside the nsjail sandbox with the given input and
// resource limits.
func (e *NsjailExecutor) Run(ctx context.Context, binary string, input string, limits ResourceLimits) (*ExecutionResult, error) {
	if err := limits.Validate(); err != nil {
		return nil, fmt.Errorf("invalid resource limits: %w", err)
	}

	start := time.Now()

	// Build nsjail arguments.
	args := []string{
		"--mode", "o",
		"--config", e.nsjailConfig,
	}
	args = append(args, limits.ToNsjailArgs()...)

	// Determine the actual language for the run command. We look at the
	// binary extension/path to decide.
	runArgs := e.buildRunArgs(binary)
	args = append(args, "--")
	args = append(args, runArgs...)

	e.logger.Debug().
		Str("binary", binary).
		Strs("args", args).
		Msg("running in sandbox")

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, e.nsjailBinary, args...)
	cmd.Stdin = strings.NewReader(input)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	duration := time.Since(start)

	result := &ExecutionResult{
		Stdout:    stdout.String(),
		Stderr:    stderr.String(),
		TimeTaken: duration,
	}

	// Parse nsjail metadata from stderr for resource usage.
	e.parseNsjailOutput(result, stderr.String())

	// Determine verdict.
	if runErr != nil {
		if ctx.Err() == context.DeadlineExceeded {
			result.Verdict = VerdictTLE
			result.ExitCode = -1
		} else if exitErr, ok := runErr.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
			result.Signal = parseSignalFromExitError(exitErr)
			result.Verdict = classifyExitError(result, limits)
		} else {
			return nil, fmt.Errorf("executing sandbox: %w", runErr)
		}
	} else {
		result.ExitCode = 0
		result.Verdict = VerdictOK
	}

	// Post-hoc TLE detection: if nsjail killed the process because of the
	// time limit, the duration might be close to the limit.
	if result.Verdict == VerdictOK && duration > limits.TimeLimit {
		result.Verdict = VerdictTLE
	}

	e.logger.Info().
		Str("verdict", string(result.Verdict)).
		Int("exit_code", result.ExitCode).
		Dur("time_taken", result.TimeTaken).
		Int64("memory_used", result.MemoryUsed).
		Msg("sandbox execution completed")

	return result, nil
}

// ---------------------------------------------------------------------------
// CompileAndRun
// ---------------------------------------------------------------------------

// CompileAndRun is a convenience method that compiles the source and, on
// success, runs the resulting binary. If compilation fails the returned
// ExecutionResult carries VerdictCE.
func (e *NsjailExecutor) CompileAndRun(ctx context.Context, source string, language string, input string, limits ResourceLimits) (*ExecutionResult, error) {
	compileResult, err := e.Compile(ctx, source, language)
	if err != nil {
		return nil, fmt.Errorf("compile phase: %w", err)
	}

	if !compileResult.Success {
		return &ExecutionResult{
			ExitCode:  -1,
			Stdout:    compileResult.Stdout,
			Stderr:    compileResult.Stderr,
			TimeTaken: compileResult.Duration,
			Verdict:   VerdictCE,
		}, nil
	}

	return e.Run(ctx, compileResult.BinaryPath, input, limits)
}

// ---------------------------------------------------------------------------
// Cleanup
// ---------------------------------------------------------------------------

// Cleanup removes all temporary files created by this executor.
func (e *NsjailExecutor) Cleanup(_ context.Context) error {
	if e.workDir == "" {
		return nil
	}

	e.logger.Info().Str("work_dir", e.workDir).Msg("cleaning up sandbox work directory")

	if err := os.RemoveAll(e.workDir); err != nil {
		return fmt.Errorf("removing work directory %q: %w", e.workDir, err)
	}

	e.workDir = ""
	return nil
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// buildRunArgs determines how to invoke the binary based on file extension.
func (e *NsjailExecutor) buildRunArgs(binary string) []string {
	ext := filepath.Ext(binary)
	switch ext {
	case ".py":
		return []string{"python3", binary}
	case ".class", "":
		// Heuristic: if the path looks like a directory containing .class
		// files, use java. Otherwise, execute directly.
		info, err := os.Stat(binary)
		if err == nil && info.IsDir() {
			return []string{"java", "-cp", binary, "Main"}
		}
		return []string{binary}
	default:
		return []string{binary}
	}
}

// nsjailTimeRegex matches nsjail's log lines reporting resource usage.
var (
	nsjailTimeRegex   = regexp.MustCompile(`\[S\]\[.*\] pid=\d+ \(.+\) exited with status: (\d+), (.*?elapsed: (\d+(?:\.\d+)?) ms)`)
	nsjailMemRegex    = regexp.MustCompile(`\[S\]\[.*\] pid=\d+ \(.+\) max_rss: (\d+) kB`)
	nsjailSignalRegex = regexp.MustCompile(`\[S\]\[.*\] pid=\d+ \(.+\) killed by signal: (\w+)`)
)

// parseNsjailOutput scans nsjail's stderr for resource usage metadata and
// populates the result fields accordingly.
func (e *NsjailExecutor) parseNsjailOutput(result *ExecutionResult, nsjailStderr string) {
	// Parse elapsed time.
	if matches := nsjailTimeRegex.FindStringSubmatch(nsjailStderr); len(matches) >= 4 {
		if ms, err := strconv.ParseFloat(matches[3], 64); err == nil {
			result.TimeTaken = time.Duration(ms * float64(time.Millisecond))
		}
	}

	// Parse peak memory (RSS in kB -> bytes).
	if matches := nsjailMemRegex.FindStringSubmatch(nsjailStderr); len(matches) >= 2 {
		if kb, err := strconv.ParseInt(matches[1], 10, 64); err == nil {
			result.MemoryUsed = kb * 1024
		}
	}

	// Parse signal if the process was killed.
	if matches := nsjailSignalRegex.FindStringSubmatch(nsjailStderr); len(matches) >= 2 {
		result.Signal = matches[1]
	}
}

// parseSignalFromExitError attempts to extract a signal name from an
// exec.ExitError. On Linux/Darwin we can inspect ProcessState.
func parseSignalFromExitError(exitErr *exec.ExitError) string {
	// The signal is typically encoded in the exit code on Unix systems.
	// Exit codes 128+N indicate signal N.
	code := exitErr.ExitCode()
	if code > 128 {
		sigNum := code - 128
		signalNames := map[int]string{
			9:  "SIGKILL",
			11: "SIGSEGV",
			6:  "SIGABRT",
			8:  "SIGFPE",
			14: "SIGALRM",
			15: "SIGTERM",
			24: "SIGXCPU",
			25: "SIGXFSZ",
		}
		if name, ok := signalNames[sigNum]; ok {
			return name
		}
		return fmt.Sprintf("SIG%d", sigNum)
	}
	return ""
}

// classifyExitError determines the Verdict based on the exit error and
// resource limits.
func classifyExitError(result *ExecutionResult, limits ResourceLimits) Verdict {
	// SIGKILL or SIGXCPU typically indicate TLE when the time limit was
	// close to being exceeded.
	switch result.Signal {
	case "SIGKILL", "SIGXCPU", "SIGALRM":
		// Check if memory was the culprit.
		if result.MemoryUsed > 0 && result.MemoryUsed >= limits.MemoryLimit {
			return VerdictMLE
		}
		return VerdictTLE
	case "SIGXFSZ":
		// Output limit exceeded is classified as RE.
		return VerdictRE
	}

	// MLE: check if memory usage exceeded the limit.
	if result.MemoryUsed > 0 && result.MemoryUsed >= limits.MemoryLimit {
		return VerdictMLE
	}

	// Everything else is a Runtime Error.
	return VerdictRE
}

// Compile-time interface check.
var _ Executor = (*NsjailExecutor)(nil)
