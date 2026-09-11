package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	// Compile limits are fixed independently of the deployment execute ceilings.
	compileTimeLimit     = 60 * time.Second
	compileMemoryLimitMB = 2048
	compileOutputLimit   = 2 << 20
	cgroupRunsDirectory  = "algoforge-runs"
	// The outer Go context uses monotonic time and remains the primary wall-time
	// limit. nsjail gets only a one-second cushion for integer-second rounding
	// and minor container scheduling jitter. This is a safety cushion, not a
	// second execution budget: a multi-minute allowance here would let a killed
	// child survive after the request context has already expired.
	nsjailWallClockSkewAllowance = 1 * time.Second
)

// The jail already uses user, mount, PID, IPC, UTS, cgroup, and network
// namespaces. This denylist is defense in depth for syscalls that submitted
// programs never need in AlgoForge's compile/run contract. EPERM keeps escape
// probes observable instead of turning every blocked call into an opaque RE.
const seccompPolicy = `ERRNO(1) {
	mount,
	umount,
	pivot_root,
	move_mount,
	open_tree,
	fsopen,
	fsconfig,
	fsmount,
	fspick,
	mount_setattr,
	ptrace,
	process_vm_readv,
	process_vm_writev,
	kcmp,
	bpf,
	perf_event_open,
	kexec_load,
	kexec_file_load,
	init_module,
	finit_module,
	delete_module,
	setns,
	unshare,
	reboot,
	swapon,
	swapoff,
	name_to_handle_at,
	open_by_handle_at,
	userfaultfd,
	keyctl,
	add_key,
	request_key,
	io_uring_setup,
	io_uring_enter,
	io_uring_register,
	connect,
	bind,
	listen,
	accept,
	accept4,
	sendto,
	sendmsg,
	recvfrom,
	recvmsg,
	shutdown
}
DEFAULT ALLOW`

type healthResponse struct {
	Status                  string                     `json:"status"`
	Ready                   bool                       `json:"ready"`
	NsjailReady             bool                       `json:"nsjail_ready"`
	CgroupV2Ready           bool                       `json:"cgroup_v2_ready"`
	Languages               map[string]toolchainHealth `json:"languages"`
	SandboxRevision         string                     `json:"sandbox_revision"`
	ImageDigest             string                     `json:"image_digest"`
	ToolchainManifestDigest string                     `json:"toolchain_manifest_digest"`
	SeccompPolicyDigest     string                     `json:"seccomp_policy_digest"`
	Error                   string                     `json:"error,omitempty"`
}

type toolchainHealth struct {
	Ready   bool   `json:"ready"`
	Version string `json:"version,omitempty"`
	Error   string `json:"error,omitempty"`
}

type languageSpec struct {
	name          string
	extension     string
	compiler      string
	runtime       string
	versionArgs   []string
	versionPrefix string
}

var languageSpecs = map[string]languageSpec{
	"c": {
		name: "c", extension: ".c", compiler: "gcc", versionArgs: []string{"-dumpfullversion", "-dumpversion"},
	},
	"cpp": {
		name: "cpp", extension: ".cpp", compiler: "g++", versionArgs: []string{"-dumpfullversion", "-dumpversion"},
	},
	"python3": {
		name: "python3", extension: ".py", compiler: "python3", runtime: "python3", versionArgs: []string{"--version"}, versionPrefix: "Python 3.12",
	},
	"java": {
		name: "java", extension: ".java", compiler: "javac", runtime: "java", versionArgs: []string{"-version"}, versionPrefix: "javac 21",
	},
	"go": {
		name: "go", extension: ".go", compiler: "go", versionArgs: []string{"version"}, versionPrefix: "go version go1.23",
	},
}

type resolvedToolchain struct {
	spec         languageSpec
	compilerPath string
	runtimePath  string
	version      string
}

type productionEngine struct {
	nsjailBinary            string
	revision                string
	lookPath                func(string) (string, error)
	cgroupErr               error
	cgroupRoot              string
	imageDigest             string
	toolchainManifestDigest string
	seccompPolicyDigest     string
}

func newProductionEngine(nsjailBinary, revision string) *productionEngine {
	if strings.TrimSpace(nsjailBinary) == "" {
		nsjailBinary = "nsjail"
	}
	if strings.TrimSpace(revision) == "" {
		revision = "unknown"
	}
	return &productionEngine{
		nsjailBinary:            nsjailBinary,
		revision:                revision,
		lookPath:                exec.LookPath,
		cgroupRoot:              "/sys/fs/cgroup",
		imageDigest:             "unverified",
		toolchainManifestDigest: "unverified",
		seccompPolicyDigest:     sha256String(seccompPolicy),
	}
}

func normalizeLanguage(language string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(language)) {
	case "c":
		return "c", nil
	case "cpp", "c++", "cc":
		return "cpp", nil
	case "python3", "python", "py":
		return "python3", nil
	case "java":
		return "java", nil
	case "go", "golang":
		return "go", nil
	default:
		return "", newServiceError("unsupported_language", http.StatusUnprocessableEntity, "unsupported language %q", language)
	}
}

func (e *productionEngine) Health(ctx context.Context) healthResponse {
	_, nsjailErr := e.lookPath(e.nsjailBinary)
	cgroupErr := e.cgroupErr
	if cgroupErr == nil {
		// Docker Desktop/WSL can discard an empty delegated child cgroup while
		// leaving the long-running service cgroup intact. Repair that drift here
		// so a healthy response continues to mean the next run can be admitted.
		cgroupErr = ensureRunCgroupV2(e.cgroupRoot)
	}
	resp := healthResponse{
		Status:                  "ok",
		Ready:                   nsjailErr == nil,
		NsjailReady:             nsjailErr == nil,
		CgroupV2Ready:           cgroupErr == nil,
		Languages:               make(map[string]toolchainHealth, len(languageSpecs)),
		SandboxRevision:         e.revision,
		ImageDigest:             e.imageDigest,
		ToolchainManifestDigest: e.toolchainManifestDigest,
		SeccompPolicyDigest:     e.seccompPolicyDigest,
	}
	if cgroupErr != nil {
		resp.Ready = false
		resp.Status = "unavailable"
		resp.Error = cgroupErr.Error()
	}
	for _, language := range []string{"c", "cpp", "python3", "java", "go"} {
		tc, err := e.resolveToolchain(ctx, language)
		status := toolchainHealth{Ready: err == nil}
		if err != nil {
			status.Error = err.Error()
			resp.Ready = false
		} else {
			status.Version = tc.version
		}
		resp.Languages[language] = status
	}
	if !resp.Ready {
		resp.Status = "unavailable"
	}
	return resp
}

func (e *productionEngine) Compile(ctx context.Context, req compileRequest) (compileResponse, error) {
	language, _ := normalizeLanguage(req.Language)
	artifact, result, err := e.compile(ctx, language, req.Source, req.Seed, req.RunID, req.Profile)
	if artifact != nil {
		defer artifact.cleanup()
	}
	return result, err
}

func (e *productionEngine) Execute(ctx context.Context, req executeRequest) (executeResponse, error) {
	language, _ := normalizeLanguage(req.Language)
	artifact, compileResult, err := e.compile(ctx, language, req.Source, req.Seed, req.RunID, req.Profile)
	resp := executeResponse{Version: apiVersion, Compile: compileResult, Results: []caseResult{}}
	if err != nil {
		return resp, err
	}
	if artifact != nil {
		defer artifact.cleanup()
	}
	audit, auditErr := e.newAuditMetadata(req.RunID, "execute", language, req.Source, req.Inputs, req.Limits, req.Seed, compileResult.Toolchain, req.Profile)
	if auditErr != nil {
		return resp, newServiceError("sandbox_failure", http.StatusServiceUnavailable, "creating execution audit metadata: %v", auditErr)
	}
	resp.Audit = audit
	if !compileResult.Success {
		return resp, nil
	}

	resp.Results = make([]caseResult, 0, len(req.Inputs))
	for i, input := range req.Inputs {
		result, runErr := e.runCase(ctx, artifact, i, input, req.Limits)
		if runErr != nil {
			return resp, runErr
		}
		resp.Results = append(resp.Results, result)
	}
	return resp, nil
}

type compiledArtifact struct {
	workspace   string
	root        string
	language    string
	binaryPath  string
	runtimePath string
	seed        int64
	profile     string
}

func (a *compiledArtifact) cleanup() {
	_ = os.RemoveAll(a.workspace)
	_ = os.RemoveAll(a.root)
}

func (e *productionEngine) compile(ctx context.Context, language, source string, seed int64, runID, profile string) (*compiledArtifact, compileResponse, error) {
	response := compileResponse{Version: apiVersion, Language: language, SandboxRevision: e.revision}
	if e.cgroupErr != nil {
		return nil, response, newServiceError("sandbox_unavailable", http.StatusServiceUnavailable, "cgroup v2 delegation unavailable: %v", e.cgroupErr)
	}
	if _, err := e.lookPath(e.nsjailBinary); err != nil {
		return nil, response, newServiceError("sandbox_unavailable", http.StatusServiceUnavailable, "nsjail unavailable: %v", err)
	}
	tc, err := e.resolveToolchain(ctx, language)
	if err != nil {
		return nil, response, err
	}
	response.Toolchain = tc.version
	audit, auditErr := e.newAuditMetadata(runID, "compile", language, source, nil, executionLimits{
		TimeLimitMS: int(compileTimeLimit / time.Millisecond), MemoryLimitMB: compileMemoryLimitMB,
		OutputLimitBytes: compileOutputLimit, MaxProcesses: 32,
	}, seed, tc.version, profile)
	if auditErr != nil {
		return nil, response, newServiceError("sandbox_failure", http.StatusServiceUnavailable, "creating compile audit metadata: %v", auditErr)
	}
	response.Audit = audit

	workspace, err := os.MkdirTemp("", "algoforge-sandbox-work-*")
	if err != nil {
		return nil, response, newServiceError("sandbox_failure", http.StatusServiceUnavailable, "creating sandbox workspace: %v", err)
	}
	root, err := createJailRoot()
	if err != nil {
		_ = os.RemoveAll(workspace)
		return nil, response, newServiceError("sandbox_failure", http.StatusServiceUnavailable, "creating jail root: %v", err)
	}
	artifact := &compiledArtifact{workspace: workspace, root: root, language: language, runtimePath: tc.runtimePath, seed: seed, profile: profile}
	if err := os.Chmod(workspace, 0o777); err != nil {
		artifact.cleanup()
		return nil, response, newServiceError("sandbox_failure", http.StatusServiceUnavailable, "preparing sandbox workspace: %v", err)
	}

	sourceName := "Main" + tc.spec.extension
	if language == "go" || language == "python3" {
		sourceName = "main" + tc.spec.extension
	}
	sourcePath := filepath.Join(workspace, sourceName)
	if err := os.WriteFile(sourcePath, []byte(source), 0o444); err != nil {
		artifact.cleanup()
		return nil, response, newServiceError("sandbox_failure", http.StatusServiceUnavailable, "writing source: %v", err)
	}

	compileCommand, binaryPath := buildCompileCommand(tc, sourcePath, workspace, profile)
	artifact.binaryPath = binaryPath
	limits := executionLimits{
		TimeLimitMS:      int(compileTimeLimit / time.Millisecond),
		MemoryLimitMB:    compileMemoryLimitMB,
		OutputLimitBytes: compileOutputLimit,
		MaxProcesses:     32,
	}
	start := time.Now()
	processResult, runErr := e.runJailed(ctx, artifact, true, nil, limits, compileCommand)
	response.DurationMS = time.Since(start).Milliseconds()
	response.Stdout = processResult.stdout
	response.Stderr = processResult.stderr
	if processResult.outputExceeded {
		response.Stderr += "\ncompiler output exceeded sandbox limit"
	}
	if runErr != nil {
		artifact.cleanup()
		return nil, response, runErr
	}
	if processResult.exitCode != 0 || processResult.outputExceeded {
		response.Success = false
		if strings.TrimSpace(response.Stderr) == "" {
			response.Stderr = compileFailureSummary(processResult)
		}
		artifact.cleanup()
		return nil, response, nil
	}

	if err := verifyArtifact(artifact); err != nil {
		artifact.cleanup()
		return nil, response, newServiceError("sandbox_failure", http.StatusServiceUnavailable, "compiler reported success without a runnable artifact: %v", err)
	}
	response.Success = true
	return artifact, response, nil
}

func prepareCgroupV2(root string, pid int) error {
	controllersData, err := os.ReadFile(filepath.Join(root, "cgroup.controllers"))
	if err != nil {
		return fmt.Errorf("reading cgroup v2 controllers: %w", err)
	}
	for _, controller := range []string{"memory", "pids"} {
		if !containsCgroupController(controllersData, controller) {
			return fmt.Errorf("required %s controller is not delegated", controller)
		}
	}

	serviceGroup := filepath.Join(root, "algoforge-service")
	if err := os.MkdirAll(serviceGroup, 0o755); err != nil {
		return fmt.Errorf("creating service cgroup: %w", err)
	}
	if err := os.WriteFile(filepath.Join(serviceGroup, "cgroup.procs"), []byte(strconv.Itoa(pid)), 0o644); err != nil {
		return fmt.Errorf("moving sandbox service into child cgroup: %w", err)
	}
	if err := os.WriteFile(filepath.Join(root, "cgroup.subtree_control"), []byte("+memory +pids"), 0o644); err != nil {
		return fmt.Errorf("enabling cgroup v2 controllers: %w", err)
	}
	if err := ensureRunCgroupV2(root); err != nil {
		return err
	}

	enabledData, err := os.ReadFile(filepath.Join(root, "cgroup.subtree_control"))
	if err != nil {
		return fmt.Errorf("verifying cgroup v2 controllers: %w", err)
	}
	for _, controller := range []string{"memory", "pids"} {
		if !containsCgroupController(enabledData, controller) {
			return fmt.Errorf("required %s controller was not enabled", controller)
		}
	}
	return nil
}

func ensureRunCgroupV2(root string) error {
	runsGroup := filepath.Join(root, cgroupRunsDirectory)
	if err := os.MkdirAll(runsGroup, 0o700); err != nil {
		return fmt.Errorf("creating sandbox runs cgroup: %w", err)
	}
	if err := os.WriteFile(filepath.Join(runsGroup, "cgroup.subtree_control"), []byte("+memory +pids"), 0o644); err != nil {
		return fmt.Errorf("delegating sandbox run controllers: %w", err)
	}

	enabledData, err := os.ReadFile(filepath.Join(runsGroup, "cgroup.subtree_control"))
	if err != nil {
		return fmt.Errorf("verifying sandbox run controllers: %w", err)
	}
	for _, controller := range []string{"memory", "pids"} {
		if !containsCgroupController(enabledData, controller) {
			return fmt.Errorf("required %s controller was not delegated to sandbox runs", controller)
		}
	}
	return nil
}

func containsCgroupController(data []byte, controller string) bool {
	for _, field := range strings.Fields(string(data)) {
		if strings.TrimPrefix(field, "+") == controller {
			return true
		}
	}
	return false
}

func buildCompileCommand(tc resolvedToolchain, sourcePath, workspace string, profiles ...string) ([]string, string) {
	profile := optionalExecutionProfile(profiles)
	binaryPath := filepath.Join(workspace, "program")
	jailSourcePath := filepath.Join("/workspace", filepath.Base(sourcePath))
	jailBinaryPath := "/workspace/program"
	switch tc.spec.name {
	case "c":
		if profile == sanitizerCAndCPPProfileV1 {
			return []string{tc.compilerPath, "-std=c17", "-O1", "-g", "-fno-omit-frame-pointer", "-fsanitize=address,undefined", "-fno-sanitize-recover=all", "-pipe", "-Wall", "-Wextra", "-DONLINE_JUDGE", "-o", jailBinaryPath, jailSourcePath}, binaryPath
		}
		return []string{tc.compilerPath, "-std=c17", "-O2", "-pipe", "-Wall", "-Wextra", "-DONLINE_JUDGE", "-o", jailBinaryPath, jailSourcePath}, binaryPath
	case "cpp":
		if profile == sanitizerCAndCPPProfileV1 {
			return []string{tc.compilerPath, "-std=c++20", "-O1", "-g", "-fno-omit-frame-pointer", "-fsanitize=address,undefined", "-fno-sanitize-recover=all", "-D_GLIBCXX_ASSERTIONS", "-pipe", "-Wall", "-Wextra", "-DONLINE_JUDGE", "-o", jailBinaryPath, jailSourcePath}, binaryPath
		}
		return []string{tc.compilerPath, "-std=c++20", "-O2", "-pipe", "-Wall", "-Wextra", "-DONLINE_JUDGE", "-o", jailBinaryPath, jailSourcePath}, binaryPath
	case "python3":
		return []string{tc.compilerPath, "-I", "-m", "py_compile", jailSourcePath}, sourcePath
	case "java":
		return []string{
			tc.compilerPath,
			"-J-Xms16m", "-J-Xmx512m",
			"-J-XX:+UseSerialGC",
			"-J-XX:CompressedClassSpaceSize=64m",
			"-J-XX:MaxMetaspaceSize=256m",
			"-J-XX:ReservedCodeCacheSize=64m",
			"-encoding", "UTF-8", "-d", "/workspace", jailSourcePath,
		}, filepath.Join(workspace, "Main.class")
	case "go":
		return []string{tc.compilerPath, "build", "-trimpath", "-o", jailBinaryPath, jailSourcePath}, binaryPath
	default:
		panic("validated language has no compile command")
	}
}

func compileFailureSummary(result processResult) string {
	switch {
	case result.outputExceeded:
		return "compiler output exceeded sandbox limit"
	case result.timedOut:
		return "compiler exceeded sandbox time limit"
	case result.memoryExceeded:
		return "compiler exceeded sandbox memory limit"
	case strings.TrimSpace(result.signal) != "":
		return "compiler terminated by sandbox signal " + strings.TrimSpace(result.signal)
	default:
		return "compiler exited without diagnostics"
	}
}

func verifyArtifact(a *compiledArtifact) error {
	info, err := os.Stat(a.binaryPath)
	if err != nil {
		return err
	}
	if a.language == "java" {
		if info.IsDir() {
			return fmt.Errorf("Main.class is a directory")
		}
		return nil
	}
	if info.IsDir() {
		return fmt.Errorf("artifact is a directory")
	}
	if a.language != "python3" {
		if err := os.Chmod(a.binaryPath, 0o555); err != nil {
			return err
		}
	}
	return nil
}

func (e *productionEngine) runCase(ctx context.Context, artifact *compiledArtifact, index int, input string, limits executionLimits) (caseResult, error) {
	runCommand := buildRunCommandForArtifact(artifact, limits.MemoryLimitMB)
	start := time.Now()
	processResult, err := e.runJailed(ctx, artifact, false, strings.NewReader(input), limits, runCommand)
	stderr := processResult.stderr
	if processResult.exitCode != 0 && !processResult.outputExceeded && strings.TrimSpace(stderr) == "" {
		stderr = boundedSandboxLog(processResult.sandboxLog, 4096)
	}
	result := caseResult{
		Index:       index,
		Stdout:      processResult.stdout,
		Stderr:      stderr,
		ExitCode:    processResult.exitCode,
		TimeMS:      time.Since(start).Milliseconds(),
		MemoryBytes: processResult.memoryBytes,
		Signal:      processResult.signal,
	}
	if err != nil {
		return caseResult{}, err
	}
	switch {
	case processResult.outputExceeded:
		result.Verdict = verdictOLE
	case processResult.timedOut:
		result.Verdict = verdictTLE
	case processResult.memoryExceeded:
		result.Verdict = verdictMLE
	case processResult.exitCode != 0:
		result.Verdict = verdictRE
	default:
		result.Verdict = verdictOK
	}
	return result, nil
}

func buildRunCommandForArtifact(a *compiledArtifact, memoryLimitMB int) []string {
	switch a.language {
	case "python3":
		return []string{a.runtimePath, "-I", "/workspace/" + filepath.Base(a.binaryPath)}
	case "java":
		heapMB := memoryLimitMB / 2
		if heapMB < 16 {
			heapMB = 16
		}
		return []string{
			a.runtimePath,
			"-Xms16m", fmt.Sprintf("-Xmx%dm", heapMB),
			"-XX:+UseSerialGC",
			"-XX:CompressedClassSpaceSize=32m",
			"-XX:MaxMetaspaceSize=96m",
			"-XX:ReservedCodeCacheSize=32m",
			"-cp", "/workspace", "Main",
		}
	default:
		return []string{"/workspace/" + filepath.Base(a.binaryPath)}
	}
}

type processResult struct {
	stdout         string
	stderr         string
	exitCode       int
	signal         string
	memoryBytes    int64
	timedOut       bool
	memoryExceeded bool
	outputExceeded bool
	duration       time.Duration
	sandboxLog     string
}

func (e *productionEngine) runJailed(ctx context.Context, artifact *compiledArtifact, writableWorkspace bool, stdin io.Reader, limits executionLimits, command []string) (processResult, error) {
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(limits.TimeLimitMS)*time.Millisecond+250*time.Millisecond)
	defer cancel()
	if err := ensureRunCgroupV2(e.cgroupRoot); err != nil {
		return processResult{}, newServiceError("sandbox_unavailable", http.StatusServiceUnavailable, "preparing per-run cgroup parent: %v", err)
	}
	runCgroup, err := os.MkdirTemp(filepath.Join(e.cgroupRoot, cgroupRunsDirectory), "run-")
	if err != nil {
		return processResult{}, newServiceError("sandbox_unavailable", http.StatusServiceUnavailable, "creating per-run cgroup: %v", err)
	}
	defer cleanupRunCgroup(runCgroup)

	logFile := filepath.Join(artifact.workspace, fmt.Sprintf("nsjail-%d.log", time.Now().UnixNano()))
	args := buildNsjailArgs(artifact, writableWorkspace, limits, logFile, runCgroup)
	args = append(args, "--")
	args = append(args, command...)

	budget := newOutputBudget(limits.OutputLimitBytes, cancel)
	var stdout, stderr bytes.Buffer
	cmd := exec.Command(e.nsjailBinary, args...)
	if stdin != nil {
		cmd.Stdin = stdin
	}
	cmd.Stdout = budget.writer(&stdout)
	cmd.Stderr = budget.writer(&stderr)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	startedAt := time.Now()
	startErr := cmd.Start()
	if startErr != nil {
		return processResult{}, newServiceError("sandbox_unavailable", http.StatusServiceUnavailable, "starting nsjail: %v", startErr)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	var waitErr error
	select {
	case waitErr = <-done:
	case <-runCtx.Done():
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		waitErr = <-done
	}

	logData, _ := os.ReadFile(logFile)
	_ = os.Remove(logFile)
	if infrastructureFailure(logData) {
		return processResult{}, newServiceError("sandbox_failure", http.StatusServiceUnavailable, "nsjail isolation failed: %s", firstErrorLine(logData))
	}

	memoryBytes := parseMemoryBytes(logData)
	if peak := readMemoryPeak(runCgroup); peak > memoryBytes {
		memoryBytes = peak
	}
	result := processResult{
		stdout:         stdout.String(),
		stderr:         stderr.String(),
		exitCode:       exitCode(waitErr),
		signal:         exitSignal(waitErr),
		memoryBytes:    memoryBytes,
		outputExceeded: budget.exceededOutput(),
		duration:       time.Since(startedAt),
		sandboxLog:     string(logData),
	}
	if result.signal == "" {
		result.signal = parseNsjailSignal(logData)
	}
	result.timedOut = nsjailTimedOut(logData)
	if runCtx.Err() != nil && !result.outputExceeded {
		if ctx.Err() != nil {
			return processResult{}, newServiceError("request_cancelled", http.StatusRequestTimeout, "sandbox request cancelled: %v", ctx.Err())
		}
		result.timedOut = true
	}
	oomKills, haveOOMKills := readOOMKillCount(runCgroup)
	if haveOOMKills && oomKills > 0 && result.exitCode != 0 {
		result.memoryExceeded = true
	}
	result.memoryExceeded = result.memoryExceeded || classifyMemoryExceeded(result, limits, logData)
	return result, nil
}

func buildNsjailArgs(artifact *compiledArtifact, writableWorkspace bool, limits executionLimits, logFile, runCgroup string) []string {
	cpuSeconds := int(math.Ceil(float64(limits.TimeLimitMS) / 1000))
	if cpuSeconds < 1 {
		cpuSeconds = 1
	}
	timeSeconds := cpuSeconds + int(nsjailWallClockSkewAllowance/time.Second)
	fileLimitMB := int(math.Ceil(float64(limits.OutputLimitBytes) / float64(1<<20)))
	if fileLimitMB < 1 {
		fileLimitMB = 1
	}
	if writableWorkspace {
		// Compiler archives and binaries are larger than captured diagnostics.
		// The workspace is request-scoped and the source/body limits remain fixed.
		fileLimitMB = 128
	}
	addressSpaceLimit := strconv.Itoa(limits.MemoryLimitMB)
	if artifact.language == "java" || artifact.language == "go" || artifact.profile == sanitizerCAndCPPProfileV1 {
		// Both runtimes reserve large virtual address ranges. Physical memory is
		// still enforced by the cgroup below.
		addressSpaceLimit = "inf"
	}
	args := []string{
		"--mode", "o",
		"--chroot", artifact.root,
		"--cwd", "/workspace",
		"--user", "65534:65534:1",
		"--group", "65534:65534:1",
		"--hostname", "sandbox",
		"--time_limit", strconv.Itoa(timeSeconds),
		"--rlimit_cpu", strconv.Itoa(cpuSeconds + 1),
		"--rlimit_as", addressSpaceLimit,
		"--rlimit_stack", strconv.Itoa(limits.MemoryLimitMB),
		"--rlimit_fsize", strconv.Itoa(fileLimitMB),
		"--rlimit_nofile", "64",
		"--rlimit_nproc", strconv.Itoa(limits.MaxProcesses),
		"--max_cpus", "1",
		"--detect_cgroupv2",
		"--cgroupv2_mount", runCgroup,
		"--cgroup_mem_max", strconv.FormatInt(int64(limits.MemoryLimitMB)*1024*1024, 10),
		"--cgroup_mem_swap_max", "0",
		"--cgroup_pids_max", strconv.Itoa(limits.MaxProcesses),
		"--seccomp_string", seccompPolicy,
		"--log", logFile,
		"--env", "PATH=/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin",
		"--env", "HOME=/tmp",
		"--env", "LANG=C.UTF-8",
		"--env", "LC_ALL=C.UTF-8",
		"--env", "TZ=UTC",
		"--env", "TMPDIR=/tmp",
		"--env", "GOCACHE=/tmp/go-cache",
		"--env", "CGO_ENABLED=0",
		"--env", "ALGOFORGE_SEED=" + strconv.FormatInt(artifact.seed, 10),
		"--mount", "none:/tmp:tmpfs:size=268435456",
	}
	if artifact.profile == sanitizerCAndCPPProfileV1 {
		args = append(args,
			"--env", "ASAN_OPTIONS=abort_on_error=1:detect_leaks=0:symbolize=0",
			"--env", "UBSAN_OPTIONS=halt_on_error=1:print_stacktrace=0",
		)
	}
	for _, mount := range []string{"/usr", "/bin", "/lib", "/lib64"} {
		if _, err := os.Stat(mount); err == nil {
			args = append(args, "--bindmount_ro", mount+":"+mount)
		}
	}
	for _, mount := range []string{"/dev/null", "/dev/zero", "/dev/urandom", "/dev/random", "/etc/alternatives", "/etc/java-21-openjdk"} {
		if _, err := os.Stat(mount); err == nil {
			args = append(args, "--bindmount_ro", mount+":"+mount)
		}
	}
	workspaceMount := "--bindmount_ro"
	if writableWorkspace {
		workspaceMount = "--bindmount"
	}
	args = append(args, workspaceMount, artifact.workspace+":/workspace")
	return args
}

func createJailRoot() (string, error) {
	root, err := os.MkdirTemp("", "algoforge-jail-root-*")
	if err != nil {
		return "", err
	}
	if err := os.Chmod(root, 0o755); err != nil {
		_ = os.RemoveAll(root)
		return "", err
	}
	dirs := []string{"usr", "bin", "lib", "lib64", "workspace", "tmp", "proc", "dev", "etc", "etc/alternatives", "etc/java-21-openjdk"}
	for _, dir := range dirs {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			_ = os.RemoveAll(root)
			return "", err
		}
	}
	for _, name := range []string{"null", "zero", "urandom", "random"} {
		if err := os.WriteFile(filepath.Join(root, "dev", name), nil, 0o666); err != nil {
			_ = os.RemoveAll(root)
			return "", err
		}
	}
	if err := os.WriteFile(filepath.Join(root, "etc", "passwd"), []byte("nobody:x:65534:65534:nobody:/tmp:/usr/sbin/nologin\n"), 0o644); err != nil {
		_ = os.RemoveAll(root)
		return "", err
	}
	if err := os.WriteFile(filepath.Join(root, "etc", "group"), []byte("nogroup:x:65534:\n"), 0o644); err != nil {
		_ = os.RemoveAll(root)
		return "", err
	}
	return root, nil
}

func (e *productionEngine) resolveToolchain(ctx context.Context, language string) (resolvedToolchain, error) {
	spec, ok := languageSpecs[language]
	if !ok {
		return resolvedToolchain{}, newServiceError("unsupported_language", http.StatusUnprocessableEntity, "unsupported language %q", language)
	}
	compilerPath, err := e.lookPath(spec.compiler)
	if err != nil {
		return resolvedToolchain{}, newServiceError("toolchain_unavailable", http.StatusServiceUnavailable, "%s compiler unavailable: %v", language, err)
	}
	runtimePath := compilerPath
	if spec.runtime != "" && spec.runtime != spec.compiler {
		runtimePath, err = e.lookPath(spec.runtime)
		if err != nil {
			return resolvedToolchain{}, newServiceError("toolchain_unavailable", http.StatusServiceUnavailable, "%s runtime unavailable: %v", language, err)
		}
	}
	versionCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(versionCtx, compilerPath, spec.versionArgs...)
	output, versionErr := cmd.CombinedOutput()
	version := strings.TrimSpace(firstLine(string(output)))
	if versionErr != nil || version == "" {
		return resolvedToolchain{}, newServiceError("toolchain_unavailable", http.StatusServiceUnavailable, "cannot identify %s toolchain: %v", language, versionErr)
	}
	if spec.versionPrefix != "" && !strings.HasPrefix(version, spec.versionPrefix) {
		return resolvedToolchain{}, newServiceError("toolchain_unavailable", http.StatusServiceUnavailable, "%s requires %s, found %s", language, spec.versionPrefix, version)
	}
	return resolvedToolchain{spec: spec, compilerPath: compilerPath, runtimePath: runtimePath, version: version}, nil
}

func firstLine(value string) string {
	if i := strings.IndexByte(value, '\n'); i >= 0 {
		return value[:i]
	}
	return value
}

func infrastructureFailure(logData []byte) bool {
	logText := string(logData)
	return strings.Contains(logText, "[E]") ||
		strings.Contains(logText, "Couldn't mount") ||
		strings.Contains(logText, "Couldn't execve") ||
		strings.Contains(logText, "failed to clone")
}

func firstErrorLine(logData []byte) string {
	for _, line := range strings.Split(string(logData), "\n") {
		if strings.Contains(line, "[E]") || strings.Contains(line, "Couldn't") || strings.Contains(line, "failed") {
			return line
		}
	}
	return "unknown nsjail error"
}

var memoryRegex = regexp.MustCompile(`max_rss:\s*(\d+)\s*kB`)
var signalRegex = regexp.MustCompile(`(?i)(?:killed by|terminated with) signal:?\s*([A-Z0-9]+)`)

func parseMemoryBytes(logData []byte) int64 {
	matches := memoryRegex.FindSubmatch(logData)
	if len(matches) != 2 {
		return 0
	}
	kb, err := strconv.ParseInt(string(matches[1]), 10, 64)
	if err != nil {
		return 0
	}
	return kb * 1024
}

func parseNsjailSignal(logData []byte) string {
	matches := signalRegex.FindSubmatch(logData)
	if len(matches) != 2 {
		return ""
	}
	return strings.ToUpper(string(matches[1]))
}

func nsjailTimedOut(logData []byte) bool {
	return strings.Contains(strings.ToLower(string(logData)), "run time >= time limit")
}

func boundedSandboxLog(value string, maxBytes int) string {
	value = strings.TrimSpace(value)
	if len(value) <= maxBytes {
		return value
	}
	return "..." + value[len(value)-maxBytes:]
}

func classifyMemoryExceeded(result processResult, limits executionLimits, logData []byte) bool {
	limitBytes := int64(limits.MemoryLimitMB) * 1024 * 1024
	if result.memoryBytes >= limitBytes && result.exitCode != 0 {
		return true
	}
	logText := strings.ToLower(string(logData) + "\n" + result.stderr)
	return result.exitCode != 0 && (strings.Contains(logText, "memory limit") || strings.Contains(logText, "cannot allocate memory") || strings.Contains(logText, "outofmemoryerror") || strings.Contains(logText, "memoryerror"))
}

func readOOMKillCount(root string) (int64, bool) {
	data, err := os.ReadFile(filepath.Join(root, "memory.events"))
	if err != nil {
		return 0, false
	}
	var total int64
	found := false
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || (fields[0] != "oom_kill" && fields[0] != "oom_group_kill") {
			continue
		}
		value, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return 0, false
		}
		total += value
		found = true
	}
	return total, found
}

func readMemoryPeak(root string) int64 {
	data, err := os.ReadFile(filepath.Join(root, "memory.peak"))
	if err != nil {
		return 0
	}
	value, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil || value < 0 {
		return 0
	}
	return value
}

func cleanupRunCgroup(root string) {
	// cgroup.kill handles a host-side timeout that kills nsjail before it can
	// remove its NSJAIL.* child. The subsequent removals are best-effort.
	writeExistingFile(filepath.Join(root, "cgroup.kill"), "1")
	entries, _ := os.ReadDir(root)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		child := filepath.Join(root, entry.Name())
		writeExistingFile(filepath.Join(child, "cgroup.kill"), "1")
		_ = os.Remove(child)
	}
	_ = os.Remove(root)
}

func writeExistingFile(path, value string) {
	file, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return
	}
	_, _ = file.WriteString(value)
	_ = file.Close()
}

type executionManifest struct {
	SchemaVersion           string          `json:"schema_version"`
	Operation               string          `json:"operation"`
	ProtocolVersion         string          `json:"protocol_version"`
	Language                string          `json:"language"`
	SourceDigest            string          `json:"source_digest"`
	InputDigests            []string        `json:"input_digests"`
	Limits                  executionLimits `json:"limits"`
	Seed                    int64           `json:"seed"`
	Toolchain               string          `json:"toolchain"`
	SandboxRevision         string          `json:"sandbox_revision"`
	ImageDigest             string          `json:"image_digest"`
	ToolchainManifestDigest string          `json:"toolchain_manifest_digest"`
	SeccompPolicyDigest     string          `json:"seccomp_policy_digest"`
	Profile                 string          `json:"profile,omitempty"`
}

func (e *productionEngine) newAuditMetadata(runID, operation, language, source string, inputs []string, limits executionLimits, seed int64, toolchain string, profiles ...string) (auditMetadata, error) {
	profile := optionalExecutionProfile(profiles)
	if runID == "" {
		return auditMetadata{}, errors.New("run_id is required")
	}
	inputDigests := make([]string, len(inputs))
	for i, input := range inputs {
		inputDigests[i] = sha256String(input)
	}
	manifest := executionManifest{
		SchemaVersion: "sandbox-manifest/v1", Operation: operation, ProtocolVersion: apiVersion,
		Language: language, SourceDigest: sha256String(source), InputDigests: inputDigests,
		Limits: limits, Seed: seed, Toolchain: toolchain, SandboxRevision: e.revision,
		ImageDigest: e.imageDigest, ToolchainManifestDigest: e.toolchainManifestDigest,
		SeccompPolicyDigest: e.seccompPolicyDigest, Profile: profile,
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return auditMetadata{}, err
	}
	return auditMetadata{
		RunID: runID, ManifestDigest: sha256String(string(encoded)),
		Seed: seed, LimitProfile: canonicalLimitProfile(operation, limits, profile), Profile: profile, ImageDigest: e.imageDigest,
		ToolchainManifestDigest: e.toolchainManifestDigest, SeccompPolicyDigest: e.seccompPolicyDigest,
	}, nil
}

func canonicalLimitProfile(operation string, limits executionLimits, profiles ...string) string {
	profile := optionalExecutionProfile(profiles)
	base := fmt.Sprintf("%s-v1:time_ms=%d,memory_mb=%d,stack_mb=%d,output_bytes=%d,pids=%d", operation,
		limits.TimeLimitMS, limits.MemoryLimitMB, limits.MemoryLimitMB, limits.OutputLimitBytes, limits.MaxProcesses)
	if profile == "" {
		return base
	}
	return base + ",profile=" + profile
}

func optionalExecutionProfile(profiles []string) string {
	if len(profiles) == 0 {
		return ""
	}
	return profiles[0]
}

func sha256String(value string) string {
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

func exitSignal(err error) string {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return ""
	}
	status, ok := exitErr.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() {
		return ""
	}
	return status.Signal().String()
}

type outputBudget struct {
	mu        sync.Mutex
	remaining int64
	exceeded  bool
	cancel    context.CancelFunc
}

func newOutputBudget(limit int64, cancel context.CancelFunc) *outputBudget {
	return &outputBudget{remaining: limit, cancel: cancel}
}

func (b *outputBudget) writer(dst *bytes.Buffer) *budgetWriter {
	return &budgetWriter{budget: b, dst: dst}
}

func (b *outputBudget) exceededOutput() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.exceeded
}

type budgetWriter struct {
	budget *outputBudget
	dst    *bytes.Buffer
}

func (w *budgetWriter) Write(p []byte) (int, error) {
	w.budget.mu.Lock()
	defer w.budget.mu.Unlock()
	if w.budget.remaining <= 0 {
		if !w.budget.exceeded {
			w.budget.exceeded = true
			w.budget.cancel()
		}
		return len(p), nil
	}
	allowed := int64(len(p))
	if allowed > w.budget.remaining {
		allowed = w.budget.remaining
	}
	_, _ = w.dst.Write(p[:allowed])
	w.budget.remaining -= allowed
	if allowed < int64(len(p)) {
		w.budget.exceeded = true
		w.budget.cancel()
	}
	return len(p), nil
}
