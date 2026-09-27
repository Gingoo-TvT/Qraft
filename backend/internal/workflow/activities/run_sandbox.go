package activities

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	remotesandbox "github.com/Gingoo-TvT/Qraft/backend/internal/sandbox"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
)

var newRemoteSandboxExecutor = func(baseURL string, timeout time.Duration) (remotesandbox.RemoteExecutor, error) {
	return remotesandbox.NewHTTPClient(baseURL, timeout)
}

// RemoteSandboxExecutorFactoryV1 is the narrow injectable constructor used by
// source-of-truth S3 activities. It lets acceptance tests execute the real
// activity code with a deterministic sandbox without changing production
// SANDBOX_URL behavior.
type RemoteSandboxExecutorFactoryV1 func(time.Duration) (remotesandbox.RemoteExecutor, error)

const (
	remoteSandboxHeartbeatInterval = 10 * time.Second
	// The sandbox compile cgroup allows 60 seconds. Keep transport and audit
	// headroom so the client never cancels a compile the service still permits.
	remoteSandboxCompileTimeout   = 75 * time.Second
	remoteSandboxMaxOutputBytes   = 8 << 20
	remoteSandboxBatchOutputBytes = 64 << 20
)

var startRemoteSandboxHeartbeat = heartbeatWhile

func (a *Activities) CompileCheckActivity(ctx context.Context, solutions []domain.Solution) (*CompileCheckResult, error) {
	if activity.IsActivity(ctx) {
		activity.GetLogger(ctx).Info("compiling solutions", "count", len(solutions))
	}

	executor, err := a.remoteSandboxExecutor(a.sandboxCompileTimeout())
	if err != nil {
		return nil, err
	}
	for i, solution := range solutions {
		heartbeatMessage := fmt.Sprintf("compiling solution %d/%d (%s)", i+1, len(solutions), solution.SolutionType)
		recordSandboxHeartbeat(ctx, heartbeatMessage)
		result, err := func() (*remotesandbox.RemoteCompileResult, error) {
			stopHeartbeat := startRemoteSandboxHeartbeat(ctx, heartbeatMessage, remoteSandboxHeartbeatInterval)
			defer stopHeartbeat()
			return executor.Compile(ctx, solution.Language, solution.SourceCode)
		}()
		if err != nil {
			return nil, fmt.Errorf("remote compile %s solution (%s): %w", solution.SolutionType, solution.Language, err)
		}
		if !result.Success {
			return &CompileCheckResult{
				AllCompiled: false,
				ErrorDetail: compileCheckErrorDetail(solution, result.Stderr),
			}, nil
		}
		if activity.IsActivity(ctx) {
			activity.GetLogger(ctx).Info("solution compiled successfully",
				"type", solution.SolutionType,
				"language", result.Language,
				"toolchain", result.Toolchain,
				"sandbox_revision", result.SandboxRevision,
				"run_id", result.Audit.RunID,
				"manifest_digest", result.Audit.ManifestDigest,
				"image_digest", result.Audit.ImageDigest,
			)
		}
	}
	return &CompileCheckResult{AllCompiled: true}, nil
}

func (a *Activities) RunSandboxActivity(
	ctx context.Context,
	solution domain.Solution,
	testCases []TestCaseData,
	limits ExecutionLimits,
) (*SandboxResult, error) {
	if activity.IsActivity(ctx) {
		activity.GetLogger(ctx).Info("running remote sandbox",
			"solution_type", solution.SolutionType,
			"language", solution.Language,
			"test_count", len(testCases),
			"time_limit_ms", limits.TimeLimitMs,
			"memory_limit_mb", limits.MemoryLimitMB,
		)
	}

	// Imported problems may legitimately print close to 8 MiB for one case.
	// Keep each declared batch within 64 MiB without reducing that case's cap.
	// Explicit output contracts and non-imported execution stay unchanged.
	const importedBatchSize = remoteSandboxBatchOutputBytes / remoteSandboxMaxOutputBytes
	if limits.UseDeploymentLimits && limits.OutputLimitBytes == 0 && len(testCases) > importedBatchSize {
		combined := &SandboxResult{PayloadVersion: ActivityPayloadVersion}
		for first := 0; first < len(testCases); first += importedBatchSize {
			end := min(first+importedBatchSize, len(testCases))
			batch, err := a.RunSandboxActivity(ctx, solution, testCases[first:end], limits)
			if err != nil {
				return nil, fmt.Errorf("imported sandbox cases %d-%d: %w", first+1, end, err)
			}
			if len(batch.Outputs) != end-first {
				return nil, fmt.Errorf("imported sandbox batch returned an incomplete result")
			}
			combined.Outputs = append(combined.Outputs, batch.Outputs...)
			refs := make([]*ArtifactRef, end-first)
			copy(refs, batch.OutputArtifacts)
			combined.OutputArtifacts = append(combined.OutputArtifacts, refs...)
			combined.TimeTaken = append(combined.TimeTaken, batch.TimeTaken...)
			combined.MemoryUsed = append(combined.MemoryUsed, batch.MemoryUsed...)
			combined.Batches = append(combined.Batches, SandboxExecutionBatch{FirstCase: first, CaseCount: end - first, Audit: batch.Audit})
		}
		return combined, nil
	}

	inputs := make([]string, len(testCases))
	for i, tc := range testCases {
		recordSandboxHeartbeat(ctx, fmt.Sprintf("resolving test case %d/%d", i+1, len(testCases)))
		switch {
		case tc.InputArtifact != nil:
			data, err := a.getArtifact(ctx, tc.InputArtifact)
			if err != nil {
				return nil, fmt.Errorf("reading input artifact %d: %w", i, err)
			}
			inputs[i] = string(data)
		case tc.Input != "":
			inputs[i] = tc.Input
		case tc.InputRef != "":
			// InputRef is a replay-only fallback for workflow histories created
			// before durable ArtifactRef payloads were introduced.
			data, err := os.ReadFile(tc.InputRef)
			if err != nil {
				return nil, fmt.Errorf("reading legacy input ref %d: %w", i, err)
			}
			inputs[i] = string(data)
		}
	}

	timeout := a.sandboxBatchTimeout(len(inputs), limits.TimeLimitMs)
	executor, err := a.remoteSandboxExecutor(timeout)
	if err != nil {
		return nil, err
	}
	recordSandboxHeartbeat(ctx, "submitting source and test cases to remote sandbox")
	remoteLimits := remotesandbox.NewRemoteLimits(limits.TimeLimitMs, limits.MemoryLimitMB)
	if limits.OutputLimitBytes > 0 {
		if limits.OutputLimitBytes > remoteSandboxMaxOutputBytes {
			return nil, fmt.Errorf("sandbox output limit %d exceeds %d", limits.OutputLimitBytes, remoteSandboxMaxOutputBytes)
		}
		remoteLimits.OutputLimitBytes = limits.OutputLimitBytes
	}
	remoteLimits.Profile = limits.Profile
	// The service limits the sum of declared case budgets, not only each case.
	// Keep explicit contracts fixed; adaptive defaults must fit the whole batch.
	maximumOutput := int64(remoteSandboxMaxOutputBytes)
	if len(inputs) > 0 && maximumOutput > remoteSandboxBatchOutputBytes/int64(len(inputs)) {
		maximumOutput = remoteSandboxBatchOutputBytes / int64(len(inputs))
	}
	if limits.OutputLimitBytes == 0 && remoteLimits.OutputLimitBytes > maximumOutput {
		remoteLimits.OutputLimitBytes = maximumOutput
	}
	remoteResult, err := func() (*remotesandbox.RemoteExecuteResult, error) {
		stopHeartbeat := startRemoteSandboxHeartbeat(ctx, "waiting for remote sandbox execution", remoteSandboxHeartbeatInterval)
		defer stopHeartbeat()
		result, err := executor.Execute(ctx, solution.Language, solution.SourceCode, inputs, remoteLimits)
		if limits.UseDeploymentLimits && lowerImportedMemoryCeiling(err, &remoteLimits) {
			recordSandboxHeartbeat(ctx, "validating imported problem under stricter deployed memory ceiling")
			result, err = executor.Execute(ctx, solution.Language, solution.SourceCode, inputs, remoteLimits)
		}
		// The default 2 MiB stdout cap can reject a correct program on a large
		// generated input. Retry the same complete batch once at the existing
		// 8 MiB ceiling, retaining one coherent execution audit. Explicit
		// caller limits (including S3 contracts) are never changed.
		if err == nil && result != nil && result.Compile.Success && limits.OutputLimitBytes == 0 && remoteLimits.OutputLimitBytes < maximumOutput {
			for _, item := range result.Results {
				if item.Verdict == remotesandbox.Verdict("OLE") {
					remoteLimits.OutputLimitBytes = maximumOutput
					recordSandboxHeartbeat(ctx, fmt.Sprintf("retrying sandbox stdout at %d bytes per case", maximumOutput))
					return executor.Execute(ctx, solution.Language, solution.SourceCode, inputs, remoteLimits)
				}
			}
		}
		return result, err
	}()
	if err != nil {
		return nil, fmt.Errorf("remote sandbox execution: %w", err)
	}
	if !remoteResult.Compile.Success {
		compileErr := fmt.Errorf("remote sandbox compilation failed: %s", boundedDiagnostic(remoteResult.Compile.Stderr))
		if solution.SolutionType == domain.SolutionTypeBrute {
			message := fmt.Sprintf("reference solution program failure (compile): %v", compileErr)
			return nil, temporal.NewNonRetryableApplicationError(message, "ReferenceSolutionFailure", compileErr)
		}
		return nil, compileErr
	}

	result := &SandboxResult{
		PayloadVersion: ActivityPayloadVersion,
		Outputs:        make([]string, 0, len(remoteResult.Results)),
		TimeTaken:      make([]time.Duration, 0, len(remoteResult.Results)),
		MemoryUsed:     make([]int64, 0, len(remoteResult.Results)),
		Audit: SandboxAuditMetadata{
			RunID: remoteResult.Audit.RunID, ManifestDigest: remoteResult.Audit.ManifestDigest,
			Seed: remoteResult.Audit.Seed, LimitProfile: remoteResult.Audit.LimitProfile,
			Profile:                 remoteResult.Audit.Profile,
			ImageDigest:             remoteResult.Audit.ImageDigest,
			ToolchainManifestDigest: remoteResult.Audit.ToolchainManifestDigest,
			SeccompPolicyDigest:     remoteResult.Audit.SeccompPolicyDigest,
		},
	}
	for i, item := range remoteResult.Results {
		recordSandboxHeartbeat(ctx, fmt.Sprintf("received sandbox result %d/%d", i+1, len(remoteResult.Results)))
		if item.Verdict != remotesandbox.VerdictOK {
			runtimeErr := fmt.Errorf("test case %d failed closed with verdict %s (exit=%d signal=%s): %s", i+1, item.Verdict, item.ExitCode, item.Signal, sandboxRuntimeDiagnostic(item, remoteLimits.OutputLimitBytes))
			if solution.SolutionType == domain.SolutionTypeBrute {
				// The brute/reference program is an internal oracle.  A TLE/MLE/RE
				// here is evidence that the oracle itself is unsuitable for the
				// selected differential case; it is not a verdict against the main
				// solution or the official test suite. It is deterministic for this
				// source/input pair, so mark it non-retryable at the activity boundary
				// and let the workflow regenerate the oracle instead of repeating the
				// same remote execution.
				message := fmt.Sprintf("reference solution program failure (runtime): %v", runtimeErr)
				return nil, temporal.NewNonRetryableApplicationError(message, "ReferenceSolutionFailure", runtimeErr)
			}
			return nil, runtimeErr
		}
		output := item.Stdout
		if !limits.PreserveOutputBytes {
			output = strings.TrimRight(output, "\n\r ")
		}
		result.Outputs = append(result.Outputs, output)
		result.TimeTaken = append(result.TimeTaken, time.Duration(item.TimeMS)*time.Millisecond)
		result.MemoryUsed = append(result.MemoryUsed, item.MemoryBytes)
	}

	const outputThreshold = 64 * 1024
	totalSize := 0
	for _, output := range result.Outputs {
		totalSize += len(output)
	}
	if totalSize > outputThreshold {
		metadata := artifactMetadataFromActivity(ctx)
		metadata.Provider = "algoforge-sandbox"
		metadata.Model = remoteResult.Compile.Toolchain
		metadata.ModelRevision = remoteResult.Compile.SandboxRevision
		result.OutputArtifacts = make([]*ArtifactRef, len(result.Outputs))
		for i, output := range result.Outputs {
			ref, err := a.putArtifactWithMetadata(ctx, []byte(output), "text/plain", metadata)
			if err != nil {
				return nil, fmt.Errorf("persisting sandbox output %d: %w", i, err)
			}
			result.OutputArtifacts[i] = ref
			result.Outputs[i] = ""
		}
		if activity.IsActivity(ctx) {
			activity.GetLogger(ctx).Info("externalized sandbox outputs to durable artifact store", "count", len(result.OutputArtifacts))
		}
	}

	if activity.IsActivity(ctx) {
		activity.GetLogger(ctx).Info("remote sandbox execution completed",
			"outputs", len(result.Outputs),
			"toolchain", remoteResult.Compile.Toolchain,
			"sandbox_revision", remoteResult.Compile.SandboxRevision,
			"run_id", remoteResult.Audit.RunID,
			"manifest_digest", remoteResult.Audit.ManifestDigest,
			"image_digest", remoteResult.Audit.ImageDigest,
		)
	}
	return result, nil
}

// Only accept the service's explicit memory ceiling rejection. Never weaken
// strict S3 profiles or reinterpret program MLE/failures as deployment limits.
func lowerImportedMemoryCeiling(err error, limits *remotesandbox.RemoteLimits) bool {
	var remote *remotesandbox.RemoteError
	if limits.Profile != "" || !errors.As(err, &remote) || remote.StatusCode != 422 || remote.Code != "deployment_limit_exceeded" {
		return false
	}
	var requested, maximum int
	if n, scanErr := fmt.Sscanf(remote.Message, "memory_limit_mb %d exceeds deployed execute maximum %d", &requested, &maximum); scanErr != nil || n != 2 || requested != limits.MemoryLimitMB || maximum < 16 || maximum >= requested {
		return false
	}
	if remote.Message != fmt.Sprintf("memory_limit_mb %d exceeds deployed execute maximum %d", requested, maximum) {
		return false
	}
	limits.MemoryLimitMB = maximum
	return true
}

func (a *Activities) remoteSandboxExecutor(timeout time.Duration) (remotesandbox.RemoteExecutor, error) {
	if a != nil && a.deps != nil && a.deps.RemoteSandboxFactory != nil {
		return a.deps.RemoteSandboxFactory(timeout)
	}
	baseURL := strings.TrimSpace(os.Getenv("SANDBOX_URL"))
	if baseURL == "" {
		return nil, fmt.Errorf("SANDBOX_URL is required; refusing host execution fallback")
	}
	executor, err := newRemoteSandboxExecutor(baseURL, timeout)
	if err != nil {
		return nil, fmt.Errorf("creating remote sandbox client: %w", err)
	}
	return executor, nil
}

func (a *Activities) sandboxTimeout() time.Duration {
	if a != nil && a.deps != nil && a.deps.SandboxCfg.Timeout > 0 {
		return a.deps.SandboxCfg.Timeout
	}
	return 30 * time.Second
}

func (a *Activities) sandboxCompileTimeout() time.Duration {
	timeout := a.sandboxTimeout()
	if timeout < remoteSandboxCompileTimeout {
		return remoteSandboxCompileTimeout
	}
	return timeout
}

func (a *Activities) sandboxBatchTimeout(caseCount, timeLimitMS int) time.Duration {
	const maxBatchTimeout = 5 * time.Minute
	timeout := a.sandboxTimeout()
	if caseCount < 1 || timeLimitMS < 1 {
		return timeout
	}
	worstCase := remoteSandboxCompileTimeout + time.Duration(caseCount)*time.Duration(timeLimitMS+500)*time.Millisecond
	if worstCase > timeout {
		timeout = worstCase
	}
	if timeout > maxBatchTimeout {
		return maxBatchTimeout
	}
	return timeout
}

func boundedDiagnostic(value string) string {
	const maxDiagnosticBytes = 4096
	value = strings.TrimSpace(value)
	if value == "" {
		return "no diagnostic provided"
	}
	if len(value) <= maxDiagnosticBytes {
		return value
	}
	return value[:maxDiagnosticBytes] + "..."
}

func sandboxRuntimeDiagnostic(item remotesandbox.RemoteCaseResult, outputLimitBytes int64) string {
	if item.Verdict == remotesandbox.Verdict("OLE") {
		captured := int64(len(item.Stdout) + len(item.Stderr))
		if outputLimitBytes > 0 {
			return fmt.Sprintf("output limit exceeded (captured at least %d bytes; limit %d bytes)", captured, outputLimitBytes)
		}
		return fmt.Sprintf("output limit exceeded (captured at least %d bytes)", captured)
	}
	return boundedDiagnostic(item.Stderr)
}

func compileCheckErrorDetail(solution domain.Solution, stderr string) string {
	detail := fmt.Sprintf("%s solution (%s) compilation failed: %s",
		solution.SolutionType, solution.Language, boundedDiagnostic(stderr))
	if solution.SolutionType == domain.SolutionTypeBrute {
		return fmt.Sprintf("%s (compile): %s", ReferenceSolutionFailurePrefix, detail)
	}
	return detail
}

// IsReferenceSolutionFailure reports whether an activity error was explicitly
// classified as a brute/reference-program failure.  Temporal wraps activity
// errors when they cross the workflow boundary, so the helper intentionally
// checks the stable prefix in addition to the local error chain.
func IsReferenceSolutionFailure(err error) bool {
	if err == nil {
		return false
	}
	return IsReferenceSolutionFailureText(err.Error())
}

// IsReferenceSolutionFailureText is the string form used for aggregate
// activity results (for example CompileCheckResult.ErrorDetail), where no Go
// error chain is available yet.
func IsReferenceSolutionFailureText(value string) bool {
	return strings.Contains(strings.ToLower(value), ReferenceSolutionFailurePrefix)
}

func recordSandboxHeartbeat(ctx context.Context, details string) {
	if activity.IsActivity(ctx) {
		activity.RecordHeartbeat(ctx, details)
	}
}

func languageExtension(language string) string {
	switch strings.ToLower(strings.TrimSpace(language)) {
	case "cpp", "c++", "cc":
		return ".cpp"
	case "c":
		return ".c"
	case "java":
		return ".java"
	case "python3", "python", "py":
		return ".py"
	case "go", "golang":
		return ".go"
	case "rust", "rs":
		return ".rs"
	default:
		return ".txt"
	}
}
