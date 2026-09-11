package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestSanitizerProfileCompileCommandsKeepLegacyDefaults(t *testing.T) {
	tests := []struct {
		language      string
		compiler      string
		source        string
		wantDefault   []string
		wantSanitizer []string
	}{
		{
			language: "c", compiler: "/usr/bin/gcc", source: "/tmp/work/main.c",
			wantDefault:   []string{"/usr/bin/gcc", "-std=c17", "-O2", "-pipe", "-Wall", "-Wextra", "-DONLINE_JUDGE", "-o", "/workspace/program", "/workspace/main.c"},
			wantSanitizer: []string{"/usr/bin/gcc", "-std=c17", "-O1", "-g", "-fno-omit-frame-pointer", "-fsanitize=address,undefined", "-fno-sanitize-recover=all", "-pipe", "-Wall", "-Wextra", "-DONLINE_JUDGE", "-o", "/workspace/program", "/workspace/main.c"},
		},
		{
			language: "cpp", compiler: "/usr/bin/g++", source: "/tmp/work/main.cpp",
			wantDefault:   []string{"/usr/bin/g++", "-std=c++20", "-O2", "-pipe", "-Wall", "-Wextra", "-DONLINE_JUDGE", "-o", "/workspace/program", "/workspace/main.cpp"},
			wantSanitizer: []string{"/usr/bin/g++", "-std=c++20", "-O1", "-g", "-fno-omit-frame-pointer", "-fsanitize=address,undefined", "-fno-sanitize-recover=all", "-D_GLIBCXX_ASSERTIONS", "-pipe", "-Wall", "-Wextra", "-DONLINE_JUDGE", "-o", "/workspace/program", "/workspace/main.cpp"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.language, func(t *testing.T) {
			tc := resolvedToolchain{spec: languageSpecs[tt.language], compilerPath: tt.compiler}
			defaultCommand, _ := buildCompileCommand(tc, tt.source, "/tmp/work")
			if !reflect.DeepEqual(defaultCommand, tt.wantDefault) {
				t.Fatalf("legacy compile command changed:\ngot  %#v\nwant %#v", defaultCommand, tt.wantDefault)
			}
			sanitizerCommand, _ := buildCompileCommand(tc, tt.source, "/tmp/work", sanitizerCAndCPPProfileV1)
			if !reflect.DeepEqual(sanitizerCommand, tt.wantSanitizer) {
				t.Fatalf("sanitizer compile command differs:\ngot  %#v\nwant %#v", sanitizerCommand, tt.wantSanitizer)
			}
		})
	}
}

func TestSanitizerProfileValidationIsCAndCPPOnly(t *testing.T) {
	for _, language := range []string{"c", "cpp"} {
		if err := validateExecutionProfile(sanitizerCAndCPPProfileV1, language); err != nil {
			t.Fatalf("profile rejected %s: %v", language, err)
		}
	}
	for _, test := range []struct {
		profile  string
		language string
	}{
		{sanitizerCAndCPPProfileV1, "python3"},
		{" sanitizer-c-cpp-v1", "cpp"},
		{"sanitizer-v2", "cpp"},
	} {
		if err := validateExecutionProfile(test.profile, test.language); err == nil {
			t.Fatalf("profile=%q language=%q was accepted", test.profile, test.language)
		}
	}
	if err := validateExecutionProfile("", "python3"); err != nil {
		t.Fatalf("legacy empty profile was rejected: %v", err)
	}
}

func TestSanitizerProfileKeepsCgroupButRelaxesVirtualAddressLimit(t *testing.T) {
	limits := executionLimits{TimeLimitMS: 1000, MemoryLimitMB: 64, OutputLimitBytes: 1024, MaxProcesses: 1}
	base := compiledArtifact{workspace: "/tmp/work", root: "/tmp/root", language: "cpp", seed: 7}
	defaultArgs := buildNsjailArgs(&base, false, limits, "/tmp/log", "/sys/fs/cgroup/algoforge-runs/default")
	if got := nsjailFlagValueV1(defaultArgs, "--rlimit_as"); got != "64" {
		t.Fatalf("legacy rlimit_as=%q want 64", got)
	}
	if got := nsjailFlagValueV1(defaultArgs, "--rlimit_stack"); got != "64" {
		t.Fatalf("legacy rlimit_stack=%q want 64", got)
	}
	if strings.Contains(strings.Join(defaultArgs, " "), "ASAN_OPTIONS=") {
		t.Fatal("legacy execution unexpectedly received sanitizer environment")
	}

	sanitized := base
	sanitized.profile = sanitizerCAndCPPProfileV1
	args := buildNsjailArgs(&sanitized, false, limits, "/tmp/log", "/sys/fs/cgroup/algoforge-runs/sanitized")
	joined := strings.Join(args, " ")
	if got := nsjailFlagValueV1(args, "--rlimit_as"); got != "inf" {
		t.Fatalf("sanitizer rlimit_as=%q want inf", got)
	}
	if got := nsjailFlagValueV1(args, "--rlimit_stack"); got != "64" {
		t.Fatalf("sanitizer rlimit_stack=%q want 64", got)
	}
	for _, required := range []string{
		"--cgroup_mem_max 67108864",
		"--cgroup_mem_swap_max 0",
		"ASAN_OPTIONS=abort_on_error=1:detect_leaks=0:symbolize=0",
		"UBSAN_OPTIONS=halt_on_error=1:print_stacktrace=0",
	} {
		if !strings.Contains(joined, required) {
			t.Fatalf("sanitizer jail args omit %q: %s", required, joined)
		}
	}
}

func TestSanitizerProfileBindsManifestAndLimitReceipt(t *testing.T) {
	engine := newProductionEngine("nsjail", "revision")
	engine.imageDigest = "sha256:" + strings.Repeat("a", 64)
	engine.toolchainManifestDigest = "sha256:" + strings.Repeat("b", 64)
	limits := executionLimits{TimeLimitMS: 1000, MemoryLimitMB: 64, OutputLimitBytes: 1024, MaxProcesses: 1}
	runID := "run_0123456789abcdef0123456789abcdef"
	legacy, err := engine.newAuditMetadata(runID, "execute", "cpp", "source", []string{"input"}, limits, 9, "g++")
	if err != nil {
		t.Fatal(err)
	}
	sanitized, err := engine.newAuditMetadata(runID, "execute", "cpp", "source", []string{"input"}, limits, 9, "g++", sanitizerCAndCPPProfileV1)
	if err != nil {
		t.Fatal(err)
	}
	if legacy.Profile != "" || legacy.LimitProfile != "execute-v1:time_ms=1000,memory_mb=64,stack_mb=64,output_bytes=1024,pids=1" {
		t.Fatalf("legacy audit changed: %+v", legacy)
	}
	if sanitized.Profile != sanitizerCAndCPPProfileV1 || !strings.HasSuffix(sanitized.LimitProfile, ",profile="+sanitizerCAndCPPProfileV1) {
		t.Fatalf("sanitizer profile is not bound to audit metadata: %+v", sanitized)
	}
	if sanitized.ManifestDigest == legacy.ManifestDigest {
		t.Fatal("sanitizer profile did not change the execution manifest digest")
	}
}

func nsjailFlagValueV1(args []string, flag string) string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag {
			return args[i+1]
		}
	}
	return ""
}
