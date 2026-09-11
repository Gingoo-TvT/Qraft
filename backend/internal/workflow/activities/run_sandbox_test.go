package activities

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/config"
	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	remotesandbox "github.com/Gingoo-TvT/Qraft/backend/internal/sandbox"
)

type fakeRemoteSandbox struct {
	compileFunc func(context.Context, string, string) (*remotesandbox.RemoteCompileResult, error)
	executeFunc func(context.Context, string, string, []string, remotesandbox.RemoteLimits) (*remotesandbox.RemoteExecuteResult, error)
}

func (f *fakeRemoteSandbox) Compile(ctx context.Context, language, source string) (*remotesandbox.RemoteCompileResult, error) {
	if f.compileFunc == nil {
		panic("unexpected Compile call")
	}
	return f.compileFunc(ctx, language, source)
}

func (f *fakeRemoteSandbox) Execute(ctx context.Context, language, source string, inputs []string, limits remotesandbox.RemoteLimits) (*remotesandbox.RemoteExecuteResult, error) {
	if f.executeFunc == nil {
		panic("unexpected Execute call")
	}
	return f.executeFunc(ctx, language, source, inputs, limits)
}

func TestCompileCheckUsesRemoteSandbox(t *testing.T) {
	t.Setenv("SANDBOX_URL", "http://sandbox:8090")
	called := 0
	fake := &fakeRemoteSandbox{compileFunc: func(_ context.Context, language, source string) (*remotesandbox.RemoteCompileResult, error) {
		called++
		if language != "cpp" || source == "" {
			t.Fatalf("unexpected compile input language=%q source=%q", language, source)
		}
		return remoteCompileResult("cpp", true, ""), nil
	}}
	restoreRemoteFactory(t, fake)
	activities := New(&Dependencies{SandboxCfg: config.SandboxConfig{Timeout: time.Second}})
	result, err := activities.CompileCheckActivity(context.Background(), []domain.Solution{
		{SolutionType: domain.SolutionTypeMain, Language: "cpp", SourceCode: "int main(){}"},
		{SolutionType: domain.SolutionTypeBrute, Language: "cpp", SourceCode: "int main(){}"},
	})
	if err != nil || !result.AllCompiled || called != 2 {
		t.Fatalf("result=%+v called=%d err=%v", result, called, err)
	}
}

func TestCompileCheckReturnsCompilerErrorButInfrastructureErrorFailsActivity(t *testing.T) {
	t.Setenv("SANDBOX_URL", "http://sandbox:8090")
	t.Run("compiler error", func(t *testing.T) {
		fake := &fakeRemoteSandbox{compileFunc: func(context.Context, string, string) (*remotesandbox.RemoteCompileResult, error) {
			return remoteCompileResult("java", false, "syntax error"), nil
		}}
		restoreRemoteFactory(t, fake)
		result, err := New(&Dependencies{}).CompileCheckActivity(context.Background(), []domain.Solution{{SolutionType: domain.SolutionTypeMain, Language: "java", SourceCode: "bad"}})
		if err != nil || result.AllCompiled || !strings.Contains(result.ErrorDetail, "syntax error") {
			t.Fatalf("result=%+v err=%v", result, err)
		}
	})
	t.Run("brute compiler error is classified as reference failure", func(t *testing.T) {
		fake := &fakeRemoteSandbox{compileFunc: func(context.Context, string, string) (*remotesandbox.RemoteCompileResult, error) {
			return remoteCompileResult("cpp", false, "syntax error"), nil
		}}
		restoreRemoteFactory(t, fake)
		result, err := New(&Dependencies{}).CompileCheckActivity(context.Background(), []domain.Solution{{SolutionType: domain.SolutionTypeBrute, Language: "cpp", SourceCode: "bad"}})
		if err != nil || result == nil || result.AllCompiled || !IsReferenceSolutionFailureText(result.ErrorDetail) {
			t.Fatalf("result=%+v err=%v", result, err)
		}
	})
	t.Run("transport error", func(t *testing.T) {
		fake := &fakeRemoteSandbox{compileFunc: func(context.Context, string, string) (*remotesandbox.RemoteCompileResult, error) {
			return nil, errors.New("connection refused")
		}}
		restoreRemoteFactory(t, fake)
		result, err := New(&Dependencies{}).CompileCheckActivity(context.Background(), []domain.Solution{{SolutionType: domain.SolutionTypeMain, Language: "cpp", SourceCode: "x"}})
		if err == nil || result != nil {
			t.Fatalf("expected activity failure, result=%+v err=%v", result, err)
		}
	})
}

func TestRunSandboxRemoteBatchAndLegacyInputRef(t *testing.T) {
	t.Setenv("SANDBOX_URL", "http://sandbox:8090")
	legacy := filepath.Join(t.TempDir(), "input.txt")
	if err := os.WriteFile(legacy, []byte("legacy"), 0o600); err != nil {
		t.Fatal(err)
	}
	fake := &fakeRemoteSandbox{executeFunc: func(_ context.Context, language, source string, inputs []string, limits remotesandbox.RemoteLimits) (*remotesandbox.RemoteExecuteResult, error) {
		if language != "python3" || source == "" || len(inputs) != 2 || inputs[0] != "inline" || inputs[1] != "legacy" {
			t.Fatalf("unexpected request: language=%s inputs=%#v", language, inputs)
		}
		if limits.TimeLimitMS != 250 || limits.MemoryLimitMB != 64 || limits.OutputLimitBytes != 2<<20 || limits.Profile != "" {
			t.Fatalf("legacy limits changed: %+v", limits)
		}
		return &remotesandbox.RemoteExecuteResult{
			Version: remotesandbox.ProtocolVersion,
			Compile: *remoteCompileResult("python3", true, ""),
			Results: []remotesandbox.RemoteCaseResult{
				{Index: 0, Verdict: remotesandbox.VerdictOK, Stdout: "one\n", ExitCode: 0, TimeMS: 3, MemoryBytes: 100},
				{Index: 1, Verdict: remotesandbox.VerdictOK, Stdout: "two\n", ExitCode: 0, TimeMS: 4, MemoryBytes: 200},
			},
		}, nil
	}}
	restoreRemoteFactory(t, fake)
	result, err := New(&Dependencies{}).RunSandboxActivity(context.Background(), domain.Solution{
		SolutionType: domain.SolutionTypeMain, Language: "python3", SourceCode: "print(input())",
	}, []TestCaseData{{Input: "inline"}, {InputRef: legacy}}, ExecutionLimits{TimeLimitMs: 250, MemoryLimitMB: 64})
	if err != nil {
		t.Fatalf("run sandbox: %v", err)
	}
	if result.PayloadVersion != ActivityPayloadVersion || strings.Join(result.Outputs, ",") != "one,two" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if len(result.TimeTaken) != 2 || result.MemoryUsed[1] != 200 || len(result.OutputRefs) != 0 {
		t.Fatalf("unexpected metrics/legacy refs: %+v", result)
	}
}

func TestRunSandboxFailsClosedOnNonOKVerdict(t *testing.T) {
	t.Setenv("SANDBOX_URL", "http://sandbox:8090")
	fake := &fakeRemoteSandbox{executeFunc: func(context.Context, string, string, []string, remotesandbox.RemoteLimits) (*remotesandbox.RemoteExecuteResult, error) {
		return &remotesandbox.RemoteExecuteResult{
			Version: remotesandbox.ProtocolVersion,
			Compile: *remoteCompileResult("cpp", true, ""),
			Results: []remotesandbox.RemoteCaseResult{{Index: 0, Verdict: remotesandbox.VerdictTLE, ExitCode: -1, Stderr: "timed out"}},
		}, nil
	}}
	restoreRemoteFactory(t, fake)
	result, err := New(&Dependencies{}).RunSandboxActivity(context.Background(), domain.Solution{Language: "cpp", SourceCode: "x"}, []TestCaseData{{Input: "x"}}, ExecutionLimits{TimeLimitMs: 10, MemoryLimitMB: 64})
	if err == nil || result != nil || !strings.Contains(err.Error(), "test case 1 failed closed with verdict TLE") || strings.Contains(err.Error(), "test case 0") {
		t.Fatalf("expected fail-closed TLE, result=%+v err=%v", result, err)
	}
}

func TestRunSandboxReportsOutputLimitForOLE(t *testing.T) {
	t.Setenv("SANDBOX_URL", "http://sandbox:8090")
	fake := &fakeRemoteSandbox{executeFunc: func(context.Context, string, string, []string, remotesandbox.RemoteLimits) (*remotesandbox.RemoteExecuteResult, error) {
		return &remotesandbox.RemoteExecuteResult{
			Version: remotesandbox.ProtocolVersion,
			Compile: *remoteCompileResult("cpp", true, ""),
			Results: []remotesandbox.RemoteCaseResult{{
				Index: 0, Verdict: remotesandbox.Verdict("OLE"), ExitCode: -1,
				Signal: "SIGKILL", Stdout: strings.Repeat("x", 2<<20),
			}},
		}, nil
	}}
	restoreRemoteFactory(t, fake)
	result, err := New(&Dependencies{}).RunSandboxActivity(context.Background(), domain.Solution{Language: "cpp", SourceCode: "x"}, []TestCaseData{{Input: "x"}}, ExecutionLimits{TimeLimitMs: 1000, MemoryLimitMB: 256})
	if err == nil || result != nil || !strings.Contains(err.Error(), "output limit exceeded") || !strings.Contains(err.Error(), "2097152 bytes") {
		t.Fatalf("expected explicit OLE diagnostic, result=%+v err=%v", result, err)
	}
}

func TestRunSandboxClassifiesReferenceRuntimeVerdictAsProgramFailure(t *testing.T) {
	t.Setenv("SANDBOX_URL", "http://sandbox:8090")
	fake := &fakeRemoteSandbox{executeFunc: func(context.Context, string, string, []string, remotesandbox.RemoteLimits) (*remotesandbox.RemoteExecuteResult, error) {
		return &remotesandbox.RemoteExecuteResult{
			Version: remotesandbox.ProtocolVersion,
			Compile: *remoteCompileResult("cpp", true, ""),
			Results: []remotesandbox.RemoteCaseResult{{Index: 0, Verdict: remotesandbox.VerdictRE, ExitCode: 139, Signal: "SIGSEGV", Stderr: "segmentation fault"}},
		}, nil
	}}
	restoreRemoteFactory(t, fake)
	result, err := New(&Dependencies{}).RunSandboxActivity(context.Background(), domain.Solution{
		SolutionType: domain.SolutionTypeBrute, Language: "cpp", SourceCode: "x",
	}, []TestCaseData{{Input: "x"}}, ExecutionLimits{TimeLimitMs: 1000, MemoryLimitMB: 512})
	if err == nil || result != nil || !IsReferenceSolutionFailure(err) ||
		!strings.Contains(err.Error(), "verdict RE") || !strings.Contains(err.Error(), "SIGSEGV") {
		t.Fatalf("expected classified reference RE, result=%+v err=%v", result, err)
	}
}

func TestRunSandboxUsesInputArtifactAndDurableOutputArtifacts(t *testing.T) {
	t.Setenv("SANDBOX_URL", "http://sandbox:8090")
	store := &sandboxArtifactStore{getData: []byte("artifact input")}
	fake := &fakeRemoteSandbox{executeFunc: func(_ context.Context, _ string, _ string, inputs []string, _ remotesandbox.RemoteLimits) (*remotesandbox.RemoteExecuteResult, error) {
		if len(inputs) != 2 || inputs[0] != "artifact input" {
			t.Fatalf("artifact input was not resolved: %#v", inputs)
		}
		large := strings.Repeat("x", 40*1024)
		return &remotesandbox.RemoteExecuteResult{
			Version: remotesandbox.ProtocolVersion,
			Compile: *remoteCompileResult("go", true, ""),
			Results: []remotesandbox.RemoteCaseResult{
				{Index: 0, Verdict: remotesandbox.VerdictOK, Stdout: large, ExitCode: 0},
				{Index: 1, Verdict: remotesandbox.VerdictOK, Stdout: large, ExitCode: 0},
			},
		}, nil
	}}
	restoreRemoteFactory(t, fake)
	activities := New(&Dependencies{})
	activities.artifacts = store
	inputRef := &ArtifactRef{SchemaVersion: 1}
	result, err := activities.RunSandboxActivity(context.Background(), domain.Solution{Language: "go", SourceCode: "package main"}, []TestCaseData{
		{Input: "must not win", InputArtifact: inputRef}, {Input: "inline"},
	}, ExecutionLimits{TimeLimitMs: 1000, MemoryLimitMB: 64})
	if err != nil {
		t.Fatalf("run sandbox: %v", err)
	}
	if len(result.OutputArtifacts) != 2 || result.Outputs[0] != "" || len(result.OutputRefs) != 0 || store.puts != 2 {
		t.Fatalf("outputs were not durably externalized: result=%+v puts=%d", result, store.puts)
	}
	if store.lastMetadata.Provider != "algoforge-sandbox" || store.lastMetadata.ModelRevision != "sandbox-test" || store.lastMetadata.Model != "go fixture" {
		t.Fatalf("sandbox provenance missing: %+v", store.lastMetadata)
	}
}

func TestMissingSandboxURLNeverConstructsExecutor(t *testing.T) {
	t.Setenv("SANDBOX_URL", "")
	original := newRemoteSandboxExecutor
	newRemoteSandboxExecutor = func(string, time.Duration) (remotesandbox.RemoteExecutor, error) {
		t.Fatal("client factory must not run without SANDBOX_URL")
		return nil, nil
	}
	t.Cleanup(func() { newRemoteSandboxExecutor = original })
	result, err := New(&Dependencies{}).CompileCheckActivity(context.Background(), []domain.Solution{{Language: "cpp", SourceCode: "x"}})
	if err == nil || result != nil || !strings.Contains(err.Error(), "refusing host execution fallback") {
		t.Fatalf("expected missing URL failure, result=%+v err=%v", result, err)
	}
}

func TestRemoteSandboxCallsStartAndStopPeriodicHeartbeats(t *testing.T) {
	t.Setenv("SANDBOX_URL", "http://sandbox:8090")
	fake := &fakeRemoteSandbox{
		compileFunc: func(context.Context, string, string) (*remotesandbox.RemoteCompileResult, error) {
			return remoteCompileResult("cpp", true, ""), nil
		},
		executeFunc: func(context.Context, string, string, []string, remotesandbox.RemoteLimits) (*remotesandbox.RemoteExecuteResult, error) {
			return &remotesandbox.RemoteExecuteResult{
				Version: remotesandbox.ProtocolVersion,
				Compile: *remoteCompileResult("cpp", true, ""),
				Results: []remotesandbox.RemoteCaseResult{{Index: 0, Verdict: remotesandbox.VerdictOK, Stdout: "ok\n"}},
			}, nil
		},
	}
	restoreRemoteFactory(t, fake)

	originalHeartbeat := startRemoteSandboxHeartbeat
	started, stopped := 0, 0
	startRemoteSandboxHeartbeat = func(_ context.Context, message string, interval time.Duration) context.CancelFunc {
		started++
		if message == "" || interval != remoteSandboxHeartbeatInterval {
			t.Fatalf("heartbeat message=%q interval=%s", message, interval)
		}
		called := false
		return func() {
			if called {
				t.Fatal("heartbeat stop called more than once")
			}
			called = true
			stopped++
		}
	}
	t.Cleanup(func() { startRemoteSandboxHeartbeat = originalHeartbeat })

	activities := New(&Dependencies{})
	if _, err := activities.CompileCheckActivity(context.Background(), []domain.Solution{{Language: "cpp", SourceCode: "int main(){}"}}); err != nil {
		t.Fatalf("compile check: %v", err)
	}
	if _, err := activities.RunSandboxActivity(
		context.Background(),
		domain.Solution{Language: "cpp", SourceCode: "int main(){}"},
		[]TestCaseData{{Input: "input\n"}},
		ExecutionLimits{TimeLimitMs: 1000, MemoryLimitMB: 64},
	); err != nil {
		t.Fatalf("run sandbox: %v", err)
	}
	if started != 2 || stopped != 2 {
		t.Fatalf("heartbeat lifecycle started=%d stopped=%d, want 2/2", started, stopped)
	}
}

func TestSandboxBatchTimeoutIsBounded(t *testing.T) {
	activities := New(&Dependencies{})
	if got := activities.sandboxBatchTimeout(256, 30_000); got != 5*time.Minute {
		t.Fatalf("timeout=%v want=5m", got)
	}
	if got := activities.sandboxBatchTimeout(5, 1000); got != 82*time.Second+500*time.Millisecond {
		t.Fatalf("timeout=%v want=1m22.5s", got)
	}
	if got := activities.sandboxCompileTimeout(); got != remoteSandboxCompileTimeout {
		t.Fatalf("compile timeout=%v want=%v", got, remoteSandboxCompileTimeout)
	}
	configured := New(&Dependencies{SandboxCfg: config.SandboxConfig{Timeout: 2 * time.Minute}})
	if got := configured.sandboxCompileTimeout(); got != 2*time.Minute {
		t.Fatalf("configured compile timeout=%v want=2m", got)
	}
}

func restoreRemoteFactory(t *testing.T, executor remotesandbox.RemoteExecutor) {
	t.Helper()
	original := newRemoteSandboxExecutor
	newRemoteSandboxExecutor = func(string, time.Duration) (remotesandbox.RemoteExecutor, error) { return executor, nil }
	t.Cleanup(func() { newRemoteSandboxExecutor = original })
}

func remoteCompileResult(language string, success bool, stderr string) *remotesandbox.RemoteCompileResult {
	return &remotesandbox.RemoteCompileResult{
		Version: remotesandbox.ProtocolVersion, Language: language, Success: success,
		Stderr: stderr, Toolchain: language + " fixture", SandboxRevision: "sandbox-test",
	}
}

type sandboxArtifactStore struct {
	getData      []byte
	puts         int
	lastMetadata ArtifactMetadata
}

func (s *sandboxArtifactStore) Put(_ context.Context, data []byte, contentType string, metadata ArtifactMetadata) (ArtifactRef, error) {
	s.puts++
	s.lastMetadata = metadata
	digest := sha256.Sum256(data)
	digestHex := hex.EncodeToString(digest[:])
	return ArtifactRef{
		SchemaVersion: ArtifactRefSchemaVersion, PayloadVersion: ActivityPayloadVersion,
		Bucket: "fixture", Key: artifactKey(digestHex), SHA256: digestHex,
		SizeBytes: int64(len(data)), ContentType: contentType,
		Producer: metadata.Producer, Provider: metadata.Provider, Model: metadata.Model,
		ModelRevision: metadata.ModelRevision, WorkflowID: metadata.WorkflowID,
	}, nil
}

func (s *sandboxArtifactStore) Get(context.Context, ArtifactRef) ([]byte, error) {
	return append([]byte(nil), s.getData...), nil
}
