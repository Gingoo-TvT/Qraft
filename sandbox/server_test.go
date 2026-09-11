package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type fakeEngine struct {
	compileFunc func(context.Context, compileRequest) (compileResponse, error)
	executeFunc func(context.Context, executeRequest) (executeResponse, error)
	health      healthResponse
}

func (f *fakeEngine) Compile(ctx context.Context, req compileRequest) (compileResponse, error) {
	if f.compileFunc == nil {
		panic("unexpected Compile call")
	}
	return f.compileFunc(ctx, req)
}

func (f *fakeEngine) Execute(ctx context.Context, req executeRequest) (executeResponse, error) {
	if f.executeFunc == nil {
		panic("unexpected Execute call")
	}
	return f.executeFunc(ctx, req)
}

func (f *fakeEngine) Health(context.Context) healthResponse { return f.health }

func TestCompileAcceptsFiveLanguagesAndAliases(t *testing.T) {
	for _, language := range []string{"c", "cpp", "c++", "python3", "python", "java", "go", "golang"} {
		t.Run(language, func(t *testing.T) {
			engine := &fakeEngine{compileFunc: func(_ context.Context, req compileRequest) (compileResponse, error) {
				canonical, _ := normalizeLanguage(req.Language)
				return compileResponse{
					Version: apiVersion, Language: canonical, Success: true,
					Toolchain: canonical + " fixture", SandboxRevision: "test",
				}, nil
			}}
			body := `{"version":"v1","language":"` + language + `","source":"main"}`
			resp := performRequest(newServer(engine, 1), http.MethodPost, "/v1/compile", body)
			if resp.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
			}
		})
	}
}

func TestCompileRejectsUnsupportedLanguageWithoutCallingEngine(t *testing.T) {
	engine := &fakeEngine{compileFunc: func(context.Context, compileRequest) (compileResponse, error) {
		t.Fatal("engine must not receive unsupported language")
		return compileResponse{}, nil
	}}
	resp := performRequest(newServer(engine, 1), http.MethodPost, "/v1/compile", `{"version":"v1","language":"rust","source":"fn main(){}"}`)
	assertAPIError(t, resp, http.StatusUnprocessableEntity, "unsupported_language")
}

func TestExecuteReturnsStructuredBatch(t *testing.T) {
	engine := &fakeEngine{executeFunc: func(_ context.Context, req executeRequest) (executeResponse, error) {
		if len(req.Inputs) != 2 || req.Inputs[1] != "b" {
			t.Fatalf("unexpected inputs: %#v", req.Inputs)
		}
		compile := compileResponse{Version: apiVersion, Language: "cpp", Success: true, Toolchain: "g++ fixture", SandboxRevision: "test"}
		return executeResponse{Version: apiVersion, Compile: compile, Results: []caseResult{
			{Index: 0, Verdict: verdictOK, Stdout: "a", ExitCode: 0},
			{Index: 1, Verdict: verdictOK, Stdout: "b", ExitCode: 0},
		}}, nil
	}}
	body := `{"version":"v1","language":"cpp","source":"main","inputs":["a","b"],"limits":{"time_limit_ms":1000,"memory_limit_mb":64,"output_limit_bytes":1024,"max_processes":1}}`
	resp := performRequest(newServer(engine, 1), http.MethodPost, "/v1/execute", body)
	if resp.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
	}
}

func TestRequestValidationRejectsMalformedAndPathFields(t *testing.T) {
	handler := newServer(&fakeEngine{}, 1)
	tests := []struct {
		name      string
		body      string
		code      int
		errorCode string
	}{
		{name: "malformed", body: `{`, code: http.StatusBadRequest, errorCode: "invalid_request"},
		{name: "path field", body: `{"version":"v1","language":"cpp","source":"x","source_path":"/etc/passwd"}`, code: http.StatusBadRequest, errorCode: "invalid_request"},
		{name: "wrong version", body: `{"version":"v2","language":"cpp","source":"x"}`, code: http.StatusBadRequest, errorCode: "unsupported_version"},
		{name: "empty source", body: `{"version":"v1","language":"cpp","source":""}`, code: http.StatusBadRequest, errorCode: "invalid_request"},
		{name: "trailing value", body: `{"version":"v1","language":"cpp","source":"x"} {}`, code: http.StatusBadRequest, errorCode: "invalid_request"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := performRequest(handler, http.MethodPost, "/v1/compile", tt.body)
			assertAPIError(t, resp, tt.code, tt.errorCode)
		})
	}
}

func TestExecuteRequestLimits(t *testing.T) {
	handler := newServer(&fakeEngine{}, 1)
	tooManyInputs, _ := json.Marshal(executeRequest{
		Version: "v1", Language: "cpp", Source: "x", Inputs: make([]string, maxCases+1),
		Limits: executionLimits{TimeLimitMS: 1000, MemoryLimitMB: 64, OutputLimitBytes: 1024, MaxProcesses: 1},
	})
	resp := performRequest(handler, http.MethodPost, "/v1/execute", string(tooManyInputs))
	assertAPIError(t, resp, http.StatusBadRequest, "invalid_request")

	oversizedSource := strings.Repeat("x", maxSourceBytes+1)
	body, _ := json.Marshal(compileRequest{Version: "v1", Language: "cpp", Source: oversizedSource})
	resp = performRequest(handler, http.MethodPost, "/v1/compile", string(body))
	assertAPIError(t, resp, http.StatusRequestEntityTooLarge, "invalid_request")

	badLimits := `{"version":"v1","language":"cpp","source":"x","inputs":[""],"limits":{"time_limit_ms":0,"memory_limit_mb":1,"output_limit_bytes":0,"max_processes":0}}`
	resp = performRequest(handler, http.MethodPost, "/v1/execute", badLimits)
	assertAPIError(t, resp, http.StatusBadRequest, "invalid_request")

	overBudget, _ := json.Marshal(executeRequest{
		Version: "v1", Language: "cpp", Source: "x", Inputs: make([]string, 256),
		Limits: executionLimits{TimeLimitMS: 1000, MemoryLimitMB: 64, OutputLimitBytes: 1024, MaxProcesses: 1},
	})
	resp = performRequest(handler, http.MethodPost, "/v1/execute", string(overBudget))
	assertAPIError(t, resp, http.StatusBadRequest, "invalid_request")

	overOutputBudget, _ := json.Marshal(executeRequest{
		Version: "v1", Language: "cpp", Source: "x", Inputs: make([]string, 65),
		Limits: executionLimits{TimeLimitMS: 1000, MemoryLimitMB: 64, OutputLimitBytes: 1 << 20, MaxProcesses: 1},
	})
	resp = performRequest(handler, http.MethodPost, "/v1/execute", string(overOutputBudget))
	assertAPIError(t, resp, http.StatusBadRequest, "invalid_request")
}

func TestBatchOutputBudgetAllowsThirtyTwoBackendDefaultCases(t *testing.T) {
	var calls atomic.Int32
	engine := &fakeEngine{executeFunc: func(context.Context, executeRequest) (executeResponse, error) {
		calls.Add(1)
		return executeResponse{Version: apiVersion}, nil
	}}
	body, err := json.Marshal(executeRequest{
		Version: "v1", Language: "cpp", Source: "x", Inputs: make([]string, 32),
		Limits: executionLimits{TimeLimitMS: 1000, MemoryLimitMB: 64, OutputLimitBytes: 1 << 20, MaxProcesses: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp := performRequest(newServer(engine, 1), http.MethodPost, "/v1/execute", string(body))
	if resp.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
	}
	if calls.Load() != 1 {
		t.Fatalf("engine calls=%d, want=1", calls.Load())
	}
}

func TestDeploymentExecuteLimitsRejectBeforeEngine(t *testing.T) {
	var calls atomic.Int32
	engine := &fakeEngine{executeFunc: func(context.Context, executeRequest) (executeResponse, error) {
		calls.Add(1)
		return executeResponse{}, nil
	}}
	handler := newServerWithExecuteLimits(engine, 1, nil, executeLimitCeiling{
		TimeLimitMS:   5_000,
		MemoryLimitMB: 256,
	})

	tests := []struct {
		name   string
		limits executionLimits
		field  string
	}{
		{
			name: "time",
			limits: executionLimits{
				TimeLimitMS: 5_001, MemoryLimitMB: 256, OutputLimitBytes: 1024, MaxProcesses: 1,
			},
			field: "time_limit_ms 5001 exceeds deployed execute maximum 5000",
		},
		{
			name: "memory",
			limits: executionLimits{
				TimeLimitMS: 5_000, MemoryLimitMB: 257, OutputLimitBytes: 1024, MaxProcesses: 1,
			},
			field: "memory_limit_mb 257 exceeds deployed execute maximum 256",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, err := json.Marshal(executeRequest{
				Version: "v1", Language: "cpp", Source: "x", Inputs: []string{""}, Limits: tt.limits,
			})
			if err != nil {
				t.Fatal(err)
			}
			resp := performRequest(handler, http.MethodPost, "/v1/execute", string(body))
			assertAPIError(t, resp, http.StatusUnprocessableEntity, "deployment_limit_exceeded")
			if !strings.Contains(resp.Body.String(), tt.field) {
				t.Fatalf("missing deployment ceiling detail: %s", resp.Body.String())
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatalf("engine calls=%d, want=0", calls.Load())
	}
}

func TestDeploymentExecuteLimitsAllowExactCeiling(t *testing.T) {
	var calls atomic.Int32
	engine := &fakeEngine{executeFunc: func(_ context.Context, req executeRequest) (executeResponse, error) {
		calls.Add(1)
		if req.Limits.TimeLimitMS != 5_000 || req.Limits.MemoryLimitMB != 256 {
			t.Fatalf("limits=%+v", req.Limits)
		}
		return executeResponse{Version: apiVersion}, nil
	}}
	handler := newServerWithExecuteLimits(engine, 1, nil, executeLimitCeiling{
		TimeLimitMS:   5_000,
		MemoryLimitMB: 256,
	})
	body := `{"version":"v1","language":"cpp","source":"x","inputs":[""],"limits":{"time_limit_ms":5000,"memory_limit_mb":256,"output_limit_bytes":1024,"max_processes":1}}`
	resp := performRequest(handler, http.MethodPost, "/v1/execute", body)
	if resp.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
	}
	if calls.Load() != 1 {
		t.Fatalf("engine calls=%d, want=1", calls.Load())
	}
}

func TestLoadDeploymentExecuteLimits(t *testing.T) {
	lookup := func(values map[string]string) func(string) (string, bool) {
		return func(name string) (string, bool) {
			value, ok := values[name]
			return value, ok
		}
	}

	limits, err := loadDeploymentExecuteLimits(lookup(map[string]string{
		"SANDBOX_TIME_LIMIT": "10", "SANDBOX_MEMORY_LIMIT": "512",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if limits.TimeLimitMS != 10_000 || limits.MemoryLimitMB != 512 {
		t.Fatalf("limits=%+v", limits)
	}

	invalid := []map[string]string{
		{"SANDBOX_MEMORY_LIMIT": "512"},
		{"SANDBOX_TIME_LIMIT": "10"},
		{"SANDBOX_TIME_LIMIT": " 10", "SANDBOX_MEMORY_LIMIT": "512"},
		{"SANDBOX_TIME_LIMIT": "+10", "SANDBOX_MEMORY_LIMIT": "512"},
		{"SANDBOX_TIME_LIMIT": "0", "SANDBOX_MEMORY_LIMIT": "512"},
		{"SANDBOX_TIME_LIMIT": "31", "SANDBOX_MEMORY_LIMIT": "512"},
		{"SANDBOX_TIME_LIMIT": "10", "SANDBOX_MEMORY_LIMIT": "15"},
		{"SANDBOX_TIME_LIMIT": "10", "SANDBOX_MEMORY_LIMIT": "4097"},
	}
	for _, values := range invalid {
		if _, err := loadDeploymentExecuteLimits(lookup(values)); err == nil {
			t.Fatalf("invalid deployment limits accepted: %#v", values)
		}
	}
}

func TestConcurrencyLimitReturnsBusy(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	engine := &fakeEngine{compileFunc: func(context.Context, compileRequest) (compileResponse, error) {
		close(entered)
		<-release
		return compileResponse{Version: apiVersion, Language: "cpp", Success: true, Toolchain: "g++", SandboxRevision: "test"}, nil
	}}
	handler := newServer(engine, 1)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- performRequest(handler, http.MethodPost, "/v1/compile", `{"version":"v1","language":"cpp","source":"x"}`)
	}()
	<-entered
	resp := performRequest(handler, http.MethodPost, "/v1/compile", `{"version":"v1","language":"cpp","source":"x"}`)
	assertAPIError(t, resp, http.StatusTooManyRequests, "sandbox_busy")
	close(release)
	if first := <-done; first.Code != http.StatusOK {
		t.Fatalf("first request status=%d body=%s", first.Code, first.Body.String())
	}
}

func TestMissingNsjailAndToolchainFailClosed(t *testing.T) {
	t.Run("nsjail", func(t *testing.T) {
		engine := newProductionEngine("missing-nsjail", "test")
		engine.lookPath = func(string) (string, error) { return "", errors.New("missing") }
		resp := performRequest(newServer(engine, 1), http.MethodPost, "/v1/compile", `{"version":"v1","language":"cpp","source":"x"}`)
		assertAPIError(t, resp, http.StatusServiceUnavailable, "sandbox_unavailable")
	})
	t.Run("toolchain", func(t *testing.T) {
		var calls atomic.Int32
		engine := newProductionEngine("nsjail", "test")
		engine.lookPath = func(name string) (string, error) {
			calls.Add(1)
			if name == "nsjail" {
				return "/bin/true", nil
			}
			return "", errors.New("missing")
		}
		resp := performRequest(newServer(engine, 1), http.MethodPost, "/v1/compile", `{"version":"v1","language":"cpp","source":"x"}`)
		assertAPIError(t, resp, http.StatusServiceUnavailable, "toolchain_unavailable")
		if calls.Load() < 2 {
			t.Fatal("expected nsjail and toolchain probes")
		}
	})
}

func TestHealthFailsWhenDependenciesAreMissing(t *testing.T) {
	engine := newProductionEngine("missing-nsjail", "test")
	engine.lookPath = func(string) (string, error) { return "", errors.New("missing") }
	resp := performRequest(newServer(engine, 1), http.MethodGet, "/healthz", "")
	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
	}
}

func TestOutputBudgetCapsCombinedStreamsAndCancels(t *testing.T) {
	cancelled := false
	budget := newOutputBudget(5, func() { cancelled = true })
	var stdout, stderr bytes.Buffer
	_, _ = budget.writer(&stdout).Write([]byte("abcd"))
	_, _ = budget.writer(&stderr).Write([]byte("efgh"))
	if stdout.String()+stderr.String() != "abcde" {
		t.Fatalf("unexpected captured output %q + %q", stdout.String(), stderr.String())
	}
	if !budget.exceededOutput() || !cancelled {
		t.Fatal("expected output budget to cancel execution")
	}
}

func TestRunJailedAllowsNilCompileStdin(t *testing.T) {
	trueBinary, err := exec.LookPath("true")
	if err != nil {
		t.Skip("true binary unavailable")
	}
	engine := newProductionEngine(trueBinary, "test")
	engine.cgroupRoot = t.TempDir()
	if err := os.Mkdir(filepath.Join(engine.cgroupRoot, cgroupRunsDirectory), 0o700); err != nil {
		t.Fatal(err)
	}
	artifact := &compiledArtifact{workspace: t.TempDir(), root: t.TempDir()}
	result, err := engine.runJailed(context.Background(), artifact, true, nil, executionLimits{
		TimeLimitMS: 1000, MemoryLimitMB: 64, OutputLimitBytes: 1024, MaxProcesses: 1,
	}, []string{"/bin/true"})
	if err != nil || result.exitCode != 0 {
		t.Fatalf("nil compile stdin failed: result=%+v err=%v", result, err)
	}
}

func TestNsjailArgsKeepNetworkNamespaceAndMinimalFilesystem(t *testing.T) {
	artifact := &compiledArtifact{workspace: "/tmp/work", root: "/tmp/root"}
	args := strings.Join(buildNsjailArgs(artifact, false, executionLimits{
		TimeLimitMS: 1000, MemoryLimitMB: 64, OutputLimitBytes: 1024, MaxProcesses: 1,
	}, "/tmp/log", "/sys/fs/cgroup/algoforge-runs/run-1"), " ")
	if strings.Contains(args, "disable_clone_newnet") {
		t.Fatal("network namespace was disabled instead of isolated")
	}
	if strings.Contains(args, "--bindmount_ro /:/") || strings.Contains(args, "--bindmount /:/") {
		t.Fatal("host root must not be mounted into the jail")
	}
	if !strings.Contains(args, "--user 65534:65534:1") || !strings.Contains(args, "--group 65534:65534:1") {
		t.Fatalf("jail uid/gid must map to global nobody: %s", args)
	}
	if !strings.Contains(args, "--bindmount_ro /tmp/work:/workspace") {
		t.Fatalf("runtime workspace must be read-only: %s", args)
	}
	if !strings.Contains(args, "--detect_cgroupv2") || !strings.Contains(args, "--cgroup_mem_max 67108864") {
		t.Fatalf("memory cgroup is missing: %s", args)
	}
	if !strings.Contains(args, "--rlimit_stack 64") {
		t.Fatalf("stack limit must track the requested memory limit: %s", args)
	}
	if !strings.Contains(args, "--cgroup_mem_swap_max 0") {
		t.Fatalf("swap limit is missing: %s", args)
	}
	if !strings.Contains(args, "--cgroupv2_mount /sys/fs/cgroup/algoforge-runs/run-1") {
		t.Fatalf("per-run cgroup is missing: %s", args)
	}
	if !strings.Contains(args, "--seccomp_string") || !strings.Contains(args, "DEFAULT ALLOW") {
		t.Fatalf("seccomp policy is missing: %s", args)
	}
	if !strings.Contains(args, "--time_limit 2") || strings.Contains(args, "--time_limit 301") {
		t.Fatalf("nsjail wall-clock cushion must stay bounded to one second: %s", args)
	}
	if !strings.Contains(args, "--rlimit_cpu 2") {
		t.Fatalf("CPU limit must remain tied to the requested one-second budget: %s", args)
	}
}

func TestPrepareCgroupV2DelegatesMemoryAndPids(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "cgroup.controllers"), []byte("cpu memory pids"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "cgroup.subtree_control"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := prepareCgroupV2(root, 123); err != nil {
		t.Fatalf("prepare cgroup: %v", err)
	}
	pid, err := os.ReadFile(filepath.Join(root, "algoforge-service", "cgroup.procs"))
	if err != nil || string(pid) != "123" {
		t.Fatalf("service pid=%q err=%v", pid, err)
	}
	enabled, _ := os.ReadFile(filepath.Join(root, "cgroup.subtree_control"))
	if string(enabled) != "+memory +pids" {
		t.Fatalf("enabled controllers=%q", enabled)
	}
	runControllers, err := os.ReadFile(filepath.Join(root, cgroupRunsDirectory, "cgroup.subtree_control"))
	if err != nil || string(runControllers) != "+memory +pids" {
		t.Fatalf("run controllers=%q err=%v", runControllers, err)
	}
}

func TestEnsureRunCgroupV2RecreatesMissingParent(t *testing.T) {
	root := t.TempDir()
	runsGroup := filepath.Join(root, cgroupRunsDirectory)
	if err := os.MkdirAll(runsGroup, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(runsGroup); err != nil {
		t.Fatal(err)
	}
	if err := ensureRunCgroupV2(root); err != nil {
		t.Fatalf("ensure run cgroup: %v", err)
	}
	enabled, err := os.ReadFile(filepath.Join(runsGroup, "cgroup.subtree_control"))
	if err != nil {
		t.Fatal(err)
	}
	for _, controller := range []string{"memory", "pids"} {
		if !containsCgroupController(enabled, controller) {
			t.Fatalf("controller %s not delegated: %q", controller, enabled)
		}
	}
}

func TestCreateJailRootIsTraversableByMappedNobody(t *testing.T) {
	root, err := createJailRoot()
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	info, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("root mode=%o want=755", info.Mode().Perm())
	}
	for _, name := range []string{"null", "zero", "urandom", "random"} {
		if _, err := os.Stat(filepath.Join(root, "dev", name)); err != nil {
			t.Fatalf("device mount target %s: %v", name, err)
		}
	}
}

func TestCompileFailureSummaryIsBoundedAndClassified(t *testing.T) {
	tests := []struct {
		result processResult
		want   string
	}{
		{result: processResult{timedOut: true}, want: "compiler exceeded sandbox time limit"},
		{result: processResult{memoryExceeded: true}, want: "compiler exceeded sandbox memory limit"},
		{result: processResult{signal: "SIGKILL"}, want: "compiler terminated by sandbox signal SIGKILL"},
		{result: processResult{}, want: "compiler exited without diagnostics"},
	}
	for _, test := range tests {
		if got := compileFailureSummary(test.result); got != test.want {
			t.Fatalf("summary = %q, want %q", got, test.want)
		}
	}
}

func TestPrepareCgroupV2RejectsMissingController(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "cgroup.controllers"), []byte("cpu memory"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := prepareCgroupV2(root, 123); err == nil || !strings.Contains(err.Error(), "pids") {
		t.Fatalf("expected missing pids error, got %v", err)
	}
}

func TestReadOOMKillCount(t *testing.T) {
	root := t.TempDir()
	content := "low 0\nhigh 1\nmax 3\noom 2\noom_kill 4\noom_group_kill 1\n"
	if err := os.WriteFile(filepath.Join(root, "memory.events"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	count, ok := readOOMKillCount(root)
	if !ok || count != 5 {
		t.Fatalf("count=%d ok=%v", count, ok)
	}
}

func TestReadMemoryPeak(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "memory.peak"), []byte("123456\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := readMemoryPeak(root); got != 123456 {
		t.Fatalf("memory peak=%d", got)
	}
}

func TestParseNsjailSignalAndBoundLog(t *testing.T) {
	logText := "prefix\n[I] pid=7 killed by signal: SIGKILL\n"
	if got := parseNsjailSignal([]byte(logText)); got != "SIGKILL" {
		t.Fatalf("signal=%q", got)
	}
	terminated := "[I] pid=7 run time >= time limit (1 >= 1). Killing it\n[I] pid=7 terminated with signal: SIGKILL"
	if got := parseNsjailSignal([]byte(terminated)); got != "SIGKILL" {
		t.Fatalf("terminated signal=%q", got)
	}
	if !nsjailTimedOut([]byte(terminated)) {
		t.Fatal("explicit nsjail timeout was not recognized")
	}
	if nsjailTimedOut([]byte("pid=7 terminated with signal: SIGKILL")) {
		t.Fatal("bare SIGKILL must not be classified as TLE")
	}
	if got := boundedSandboxLog("123456", 4); got != "...3456" {
		t.Fatalf("bounded log=%q", got)
	}
}

func TestDeploymentIdentityRejectsMutableOrMissingValues(t *testing.T) {
	validImage := "sha256:" + strings.Repeat("a", 64)
	if err := validateDeploymentIdentity("0123456789abcdef", validImage); err != nil {
		t.Fatalf("valid identity rejected: %v", err)
	}
	for _, fixture := range []struct{ revision, image string }{
		{"dev", validImage}, {"unknown", validImage}, {"short", validImage}, {"0123456789abcdef", "latest"},
	} {
		if err := validateDeploymentIdentity(fixture.revision, fixture.image); err == nil {
			t.Fatalf("mutable identity accepted: %+v", fixture)
		}
	}
}

func TestAuditSinkPersistsVersionedRecordAndFailsClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit", "sandbox.jsonl")
	sink, err := openAuditSink(path)
	if err != nil {
		t.Fatal(err)
	}
	meta := auditMetadata{
		RunID: "run_0123456789abcdef0123456789abcdef", ManifestDigest: "sha256:" + strings.Repeat("a", 64),
		LimitProfile: "execute-v1:test", ImageDigest: "sha256:" + strings.Repeat("b", 64),
		ToolchainManifestDigest: "sha256:" + strings.Repeat("c", 64), SeccompPolicyDigest: "sha256:" + strings.Repeat("d", 64),
	}
	resp := executeResponse{
		Version: apiVersion,
		Compile: compileResponse{Version: apiVersion, Language: "cpp", Success: true, Toolchain: "g++", SandboxRevision: "revision", Audit: meta},
		Results: []caseResult{{Index: 0, Verdict: verdictOK}}, Audit: meta,
	}
	attempt := auditAttempt{
		RunID: meta.RunID, Operation: "execute", Language: "cpp",
		SourceDigest: "sha256:" + strings.Repeat("e", 64), InputDigests: []string{"sha256:" + strings.Repeat("f", 64)},
		Limits:       executionLimits{TimeLimitMS: 1000, MemoryLimitMB: 64, OutputLimitBytes: 1024, MaxProcesses: 1},
		LimitProfile: meta.LimitProfile,
	}
	if err := sink.recordAttempt(attempt); err != nil {
		t.Fatal(err)
	}
	if err := sink.recordExecuteTerminal(attempt, resp, nil); err != nil {
		t.Fatal(err)
	}
	records := readAuditRecords(t, path)
	if len(records) != 2 {
		t.Fatalf("records=%d, want=2", len(records))
	}
	if records[0].Phase != auditPhaseAttempt || records[0].Status != auditStatusStarted {
		t.Fatalf("unexpected attempt record: %+v", records[0])
	}
	record := records[1]
	if record.SchemaVersion != auditSchemaVersion || record.Phase != auditPhaseTerminal || record.Status != auditStatusSuccess || record.RunID != meta.RunID || record.Verdicts[verdictOK] != 1 {
		t.Fatalf("unexpected terminal record: %+v", record)
	}
	if err := sink.close(); err != nil {
		t.Fatal(err)
	}
	if err := sink.recordAttempt(attempt); err == nil {
		t.Fatal("closed audit sink must fail instead of silently dropping an attempt")
	}
}

func TestAuditAttemptIsDurableBeforeEngineInvocation(t *testing.T) {
	tests := []struct {
		name      string
		path      string
		body      string
		operation string
		engine    func(*testing.T, string) *fakeEngine
	}{
		{
			name: "compile", path: "/v1/compile", operation: "compile",
			body: `{"version":"v1","language":"cpp","source":"sensitive compile source","seed":7}`,
			engine: func(t *testing.T, auditPath string) *fakeEngine {
				return &fakeEngine{compileFunc: func(_ context.Context, req compileRequest) (compileResponse, error) {
					assertAttemptReadableDuringEngine(t, auditPath, req.RunID, "compile", "sensitive compile source", nil, 7, executionLimits{
						TimeLimitMS: int(compileTimeLimit / time.Millisecond), MemoryLimitMB: compileMemoryLimitMB,
						OutputLimitBytes: compileOutputLimit, MaxProcesses: 32,
					})
					return compileResponse{
						Version: apiVersion, Language: "cpp", Success: true, Toolchain: "g++ fixture", SandboxRevision: "revision",
						Audit: testAuditMetadata(req.RunID),
					}, nil
				}}
			},
		},
		{
			name: "execute", path: "/v1/execute", operation: "execute",
			body: `{"version":"v1","language":"cpp","source":"sensitive execute source","inputs":["secret input"],"limits":{"time_limit_ms":1000,"memory_limit_mb":64,"output_limit_bytes":1024,"max_processes":1},"seed":9}`,
			engine: func(t *testing.T, auditPath string) *fakeEngine {
				return &fakeEngine{executeFunc: func(_ context.Context, req executeRequest) (executeResponse, error) {
					assertAttemptReadableDuringEngine(t, auditPath, req.RunID, "execute", "sensitive execute source", []string{"secret input"}, 9, executionLimits{
						TimeLimitMS: 1000, MemoryLimitMB: 64, OutputLimitBytes: 1024, MaxProcesses: 1,
					})
					meta := testAuditMetadata(req.RunID)
					return executeResponse{
						Version: apiVersion,
						Compile: compileResponse{Version: apiVersion, Language: "cpp", Success: true, Toolchain: "g++ fixture", SandboxRevision: "revision", Audit: meta},
						Results: []caseResult{{Index: 0, Verdict: verdictOK}}, Audit: meta,
					}, nil
				}}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "audit.jsonl")
			sink, err := openAuditSink(path)
			if err != nil {
				t.Fatal(err)
			}
			defer sink.close()
			resp := performRequest(newServerWithAudit(tt.engine(t, path), 1, sink), http.MethodPost, tt.path, tt.body)
			if resp.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
			}
			records := readAuditRecords(t, path)
			if len(records) != 2 || records[1].Phase != auditPhaseTerminal || records[1].Status != auditStatusSuccess || records[1].Operation != tt.operation {
				t.Fatalf("unexpected audit lifecycle: %+v", records)
			}
			if records[0].RunID != records[1].RunID {
				t.Fatalf("attempt run_id=%q terminal run_id=%q", records[0].RunID, records[1].RunID)
			}
		})
	}
}

func TestClosedAuditSinkRejectsBeforeEngineInvocation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	sink, err := openAuditSink(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := sink.close(); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	engine := &fakeEngine{
		compileFunc: func(context.Context, compileRequest) (compileResponse, error) {
			calls.Add(1)
			return compileResponse{}, nil
		},
		executeFunc: func(context.Context, executeRequest) (executeResponse, error) {
			calls.Add(1)
			return executeResponse{}, nil
		},
	}
	handler := newServerWithAudit(engine, 1, sink)
	compileResp := performRequest(handler, http.MethodPost, "/v1/compile", `{"version":"v1","language":"cpp","source":"x"}`)
	assertAPIError(t, compileResp, http.StatusServiceUnavailable, "audit_unavailable")
	executeResp := performRequest(handler, http.MethodPost, "/v1/execute", `{"version":"v1","language":"cpp","source":"x","inputs":[""],"limits":{"time_limit_ms":1000,"memory_limit_mb":64,"output_limit_bytes":1024,"max_processes":1}}`)
	assertAPIError(t, executeResp, http.StatusServiceUnavailable, "audit_unavailable")
	if calls.Load() != 0 {
		t.Fatalf("engine calls=%d, want=0", calls.Load())
	}
}

func TestEngineErrorsAndCancellationWriteSameRunIDTerminal(t *testing.T) {
	tests := []struct {
		name           string
		path           string
		body           string
		engine         *fakeEngine
		wantHTTPStatus int
		wantErrorCode  string
		wantAuditState string
		wantResults    int
	}{
		{
			name: "compile infrastructure error", path: "/v1/compile",
			body: `{"version":"v1","language":"cpp","source":"x"}`,
			engine: &fakeEngine{compileFunc: func(context.Context, compileRequest) (compileResponse, error) {
				return compileResponse{}, errors.New(strings.Repeat("infrastructure failure ", 100))
			}},
			wantHTTPStatus: http.StatusServiceUnavailable, wantErrorCode: "sandbox_failure", wantAuditState: auditStatusInfrastructure,
		},
		{
			name: "execute cancellation", path: "/v1/execute",
			body: `{"version":"v1","language":"cpp","source":"x","inputs":[""],"limits":{"time_limit_ms":1000,"memory_limit_mb":64,"output_limit_bytes":1024,"max_processes":1}}`,
			engine: &fakeEngine{executeFunc: func(_ context.Context, req executeRequest) (executeResponse, error) {
				meta := testAuditMetadata(req.RunID)
				return executeResponse{
					Version: apiVersion,
					Compile: compileResponse{Version: apiVersion, Language: "cpp", Success: true, Toolchain: "g++ fixture", SandboxRevision: "revision", Audit: meta},
					Results: []caseResult{{Index: 0, Verdict: verdictOK}},
				}, newServiceError("request_cancelled", http.StatusRequestTimeout, "sandbox request cancelled")
			}},
			wantHTTPStatus: http.StatusRequestTimeout, wantErrorCode: "request_cancelled", wantAuditState: auditStatusCancelled, wantResults: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "audit.jsonl")
			sink, err := openAuditSink(path)
			if err != nil {
				t.Fatal(err)
			}
			defer sink.close()
			resp := performRequest(newServerWithAudit(tt.engine, 1, sink), http.MethodPost, tt.path, tt.body)
			assertAPIError(t, resp, tt.wantHTTPStatus, tt.wantErrorCode)
			records := readAuditRecords(t, path)
			if len(records) != 2 {
				t.Fatalf("records=%d, want=2: %+v", len(records), records)
			}
			attempt, terminal := records[0], records[1]
			if attempt.RunID == "" || terminal.RunID != attempt.RunID || terminal.Phase != auditPhaseTerminal || terminal.Status != tt.wantAuditState {
				t.Fatalf("unexpected audit lifecycle: %+v", records)
			}
			if terminal.ErrorCode != tt.wantErrorCode || len(terminal.ErrorMessage) > maxAuditErrorMessageBytes {
				t.Fatalf("unexpected bounded error: %+v", terminal)
			}
			if terminal.ResultCount != tt.wantResults {
				t.Fatalf("result_count=%d, want=%d", terminal.ResultCount, tt.wantResults)
			}
		})
	}
}

func TestCompileErrorWritesTerminalRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	sink, err := openAuditSink(path)
	if err != nil {
		t.Fatal(err)
	}
	defer sink.close()
	engine := &fakeEngine{compileFunc: func(_ context.Context, req compileRequest) (compileResponse, error) {
		return compileResponse{
			Version: apiVersion, Language: "cpp", Success: false, Toolchain: "g++ fixture", SandboxRevision: "revision",
			Audit: testAuditMetadata(req.RunID),
		}, nil
	}}
	resp := performRequest(newServerWithAudit(engine, 1, sink), http.MethodPost, "/v1/compile", `{"version":"v1","language":"cpp","source":"broken"}`)
	if resp.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
	}
	records := readAuditRecords(t, path)
	if len(records) != 2 || records[1].Status != auditStatusCompileError || records[1].CompileSuccess {
		t.Fatalf("unexpected CE lifecycle: %+v", records)
	}
}

func TestProductionAuditMetadataPreservesServerRunID(t *testing.T) {
	engine := newProductionEngine("nsjail", "0123456789abcdef")
	engine.imageDigest = "sha256:" + strings.Repeat("b", 64)
	engine.toolchainManifestDigest = "sha256:" + strings.Repeat("c", 64)
	limits := executionLimits{TimeLimitMS: 1000, MemoryLimitMB: 64, OutputLimitBytes: 1024, MaxProcesses: 1}
	firstID := "run_0123456789abcdef0123456789abcdef"
	secondID := "run_fedcba9876543210fedcba9876543210"
	first, err := engine.newAuditMetadata(firstID, "execute", "cpp", "source", []string{"input"}, limits, 42, "g++ fixture")
	if err != nil {
		t.Fatal(err)
	}
	second, err := engine.newAuditMetadata(secondID, "execute", "cpp", "source", []string{"input"}, limits, 42, "g++ fixture")
	if err != nil {
		t.Fatal(err)
	}
	if first.RunID != firstID || second.RunID != secondID || first.ManifestDigest != second.ManifestDigest {
		t.Fatalf("server run IDs or deterministic manifest were not preserved: first=%+v second=%+v", first, second)
	}
	if _, err := engine.newAuditMetadata("", "execute", "cpp", "source", nil, limits, 0, "g++ fixture"); err == nil {
		t.Fatal("production audit metadata accepted an empty server run_id")
	}
}

func TestWireSizeRejectsEscapedResponseBeforeWritingSuccess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	sink, err := openAuditSink(path)
	if err != nil {
		t.Fatal(err)
	}
	defer sink.close()

	largeEscapedOutput := strings.Repeat("\x00\"", maxHTTPResponseBytes/8+1)
	var calls atomic.Int32
	engine := &fakeEngine{executeFunc: func(_ context.Context, req executeRequest) (executeResponse, error) {
		calls.Add(1)
		meta := testAuditMetadata(req.RunID)
		return executeResponse{
			Version: apiVersion,
			Compile: compileResponse{Version: apiVersion, Language: "cpp", Success: true, Toolchain: "g++ fixture", SandboxRevision: "revision", Audit: meta},
			Results: []caseResult{{Index: 0, Verdict: verdictOK, Stdout: largeEscapedOutput}}, Audit: meta,
		}, nil
	}}
	body := `{"version":"v1","language":"cpp","source":"x","inputs":[""],"limits":{"time_limit_ms":1000,"memory_limit_mb":64,"output_limit_bytes":8388610,"max_processes":1}}`
	resp := performRequest(newServerWithAudit(engine, 1, sink), http.MethodPost, "/v1/execute", body)
	assertAPIError(t, resp, http.StatusInternalServerError, "response_too_large")
	if calls.Load() != 1 {
		t.Fatalf("engine calls=%d, want=1", calls.Load())
	}
	if resp.Body.Len() > 1024 {
		t.Fatalf("oversize error response was not bounded: %d bytes", resp.Body.Len())
	}
	records := readAuditRecords(t, path)
	if len(records) != 2 || records[1].RunID != records[0].RunID || records[1].Status != auditStatusInfrastructure || records[1].ErrorCode != "response_too_large" {
		t.Fatalf("unexpected oversize audit lifecycle: %+v", records)
	}
}

func TestSuccessJSONSizeMatchesControlQuoteAndSignalEncoding(t *testing.T) {
	meta := testAuditMetadata("run_0123456789abcdef0123456789abcdef")
	resp := executeResponse{
		Version: apiVersion,
		Compile: compileResponse{
			Version: apiVersion, Language: "cpp<&", Success: true, Stdout: "\x00\"\\\n\u2028", Stderr: string([]byte{0xff}),
			Toolchain: "g++", SandboxRevision: "revision", Audit: meta,
		},
		Results: []caseResult{{Index: 0, Verdict: verdictOK, Stdout: "\x01\"<&", Stderr: "\t", Signal: "SIGKILL"}},
		Audit:   meta,
	}
	want, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	got, err := successJSONSize(resp)
	if err != nil {
		t.Fatal(err)
	}
	if got != int64(len(want)) {
		t.Fatalf("predicted JSON size=%d, encoded=%d", got, len(want))
	}
	resp.Results = []caseResult{}
	want, err = json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	got, err = successJSONSize(resp)
	if err != nil {
		t.Fatal(err)
	}
	if got != int64(len(want)) {
		t.Fatalf("predicted empty-results JSON size=%d, encoded=%d", got, len(want))
	}
}

func assertAttemptReadableDuringEngine(t *testing.T, path, runID, operation, source string, inputs []string, seed int64, limits executionLimits) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("attempt is not readable during engine invocation: %v", err)
	}
	if bytes.Contains(data, []byte(source)) {
		t.Fatalf("audit contains raw source: %s", data)
	}
	for _, input := range inputs {
		if input != "" && bytes.Contains(data, []byte(input)) {
			t.Fatalf("audit contains raw input: %s", data)
		}
	}
	records := decodeAuditRecords(t, data)
	if len(records) != 1 {
		t.Fatalf("records visible before engine=%d, want=1: %s", len(records), data)
	}
	record := records[0]
	if record.Phase != auditPhaseAttempt || record.Status != auditStatusStarted || record.RunID != runID || record.Operation != operation {
		t.Fatalf("unexpected pre-engine attempt: %+v", record)
	}
	if record.SourceDigest != sha256String(source) || len(record.InputDigests) != len(inputs) {
		t.Fatalf("unexpected request digests: %+v", record)
	}
	if record.Seed != seed || record.Limits != limits || record.LimitProfile != canonicalLimitProfile(operation, limits) {
		t.Fatalf("unexpected seed/limits: %+v", record)
	}
	for i, input := range inputs {
		if record.InputDigests[i] != sha256String(input) {
			t.Fatalf("input digest %d=%q, want=%q", i, record.InputDigests[i], sha256String(input))
		}
	}
}

func testAuditMetadata(runID string) auditMetadata {
	return auditMetadata{
		RunID: runID, ManifestDigest: "sha256:" + strings.Repeat("a", 64), LimitProfile: "execute-v1:test",
		ImageDigest: "sha256:" + strings.Repeat("b", 64), ToolchainManifestDigest: "sha256:" + strings.Repeat("c", 64),
		SeccompPolicyDigest: "sha256:" + strings.Repeat("d", 64),
	}
}

func readAuditRecords(t *testing.T, path string) []auditRecord {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return decodeAuditRecords(t, data)
}

func decodeAuditRecords(t *testing.T, data []byte) []auditRecord {
	t.Helper()
	lines := bytes.Split(bytes.TrimSpace(data), []byte{'\n'})
	if len(lines) == 1 && len(lines[0]) == 0 {
		return nil
	}
	records := make([]auditRecord, 0, len(lines))
	for _, line := range lines {
		var record auditRecord
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatalf("decode audit record: %v line=%s", err, line)
		}
		records = append(records, record)
	}
	return records
}

func performRequest(handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder
}

func assertAPIError(t *testing.T, recorder *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if recorder.Code != status {
		t.Fatalf("status=%d, want=%d body=%s", recorder.Code, status, recorder.Body.String())
	}
	var response errorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if response.Error.Code != code {
		t.Fatalf("error code=%q, want=%q body=%s", response.Error.Code, code, recorder.Body.String())
	}
}

func TestFakeEngineErrorIsFailClosed(t *testing.T) {
	engine := &fakeEngine{compileFunc: func(context.Context, compileRequest) (compileResponse, error) {
		return compileResponse{}, errors.New("unexpected infrastructure failure")
	}}
	resp := performRequest(newServer(engine, 1), http.MethodPost, "/v1/compile", `{"version":"v1","language":"cpp","source":"x"}`)
	assertAPIError(t, resp, http.StatusServiceUnavailable, "sandbox_failure")
}

func TestMethodAndContentTypeAreRestricted(t *testing.T) {
	handler := newServer(&fakeEngine{}, 1)
	resp := performRequest(handler, http.MethodGet, "/v1/compile", "")
	assertAPIError(t, resp, http.StatusMethodNotAllowed, "method_not_allowed")

	req := httptest.NewRequest(http.MethodPost, "/v1/compile", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "text/plain")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	assertAPIError(t, recorder, http.StatusUnsupportedMediaType, "invalid_request")
}

func TestServiceErrorPreservesTypedStatus(t *testing.T) {
	err := newServiceError("toolchain_unavailable", http.StatusServiceUnavailable, "missing")
	var typed *serviceError
	if !errors.As(err, &typed) || typed.code != "toolchain_unavailable" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCompileRequestRespectsContextCancellation(t *testing.T) {
	engine := &fakeEngine{compileFunc: func(ctx context.Context, _ compileRequest) (compileResponse, error) {
		<-ctx.Done()
		return compileResponse{}, newServiceError("request_cancelled", http.StatusRequestTimeout, "cancelled")
	}}
	handler := newServer(engine, 1)
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	req := httptest.NewRequest(http.MethodPost, "/v1/compile", strings.NewReader(`{"version":"v1","language":"cpp","source":"x"}`)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusRequestTimeout {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
