package activities

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	remotesandbox "github.com/Gingoo-TvT/Qraft/backend/internal/sandbox"
)

func TestExecuteGeneratorUsesRemoteSandboxWithDeterministicSeeds(t *testing.T) {
	t.Setenv("SANDBOX_URL", "http://sandbox:8090")
	const code = "#include <iostream>\nint main(){long long i,g,s;std::cin>>i>>g>>s;std::cout<<i<<' '<<g<<' '<<s<<'\\n';}"
	baseCases := []TestCaseData{
		{Input: "sample\n", GroupID: 0, IsSample: true},
		{GroupID: 2},
		{GroupID: 7},
	}

	var capturedInputs [][]string
	var capturedSeeds []int64
	fake := &fakeRemoteSandbox{executeFunc: func(_ context.Context, language, source string, inputs []string, limits remotesandbox.RemoteLimits) (*remotesandbox.RemoteExecuteResult, error) {
		if language != "cpp" || source != code {
			t.Fatalf("unexpected generator request language=%q source=%q", language, source)
		}
		if limits.TimeLimitMS != generatorTimeLimitMS || limits.MemoryLimitMB != generatorMemoryMB || limits.OutputLimitBytes != generatorMaxOutputBytes || limits.MaxProcesses != 16 {
			t.Fatalf("unexpected generator limits: %+v", limits)
		}
		capturedInputs = append(capturedInputs, append([]string(nil), inputs...))
		capturedSeeds = append(capturedSeeds, limits.Seed)
		results := make([]remotesandbox.RemoteCaseResult, len(inputs))
		for i, input := range inputs {
			results[i] = remotesandbox.RemoteCaseResult{Index: i, Verdict: remotesandbox.VerdictOK, Stdout: input}
		}
		return &remotesandbox.RemoteExecuteResult{
			Version: remotesandbox.ProtocolVersion,
			Compile: *remoteCompileResult("cpp", true, ""),
			Results: results,
		}, nil
	}}
	restoreRemoteFactory(t, fake)

	for run := 0; run < 2; run++ {
		cases := append([]TestCaseData(nil), baseCases...)
		result, err := New(nil).executeGenerator(context.Background(), code, cases)
		if err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
		if result[0].Input != "sample\n" || result[1].Input == "" || result[2].Input == "" {
			t.Fatalf("run %d returned incomplete cases: %+v", run, result)
		}
	}

	if len(capturedInputs) != 4 ||
		!reflect.DeepEqual(capturedInputs[0], capturedInputs[2]) ||
		!reflect.DeepEqual(capturedInputs[1], capturedInputs[3]) {
		t.Fatalf("generator stdin drifted across retries: %#v", capturedInputs)
	}
	if len(capturedSeeds) != 4 || capturedSeeds[0] <= 0 || capturedSeeds[1] <= 0 ||
		capturedSeeds[0] != capturedSeeds[2] || capturedSeeds[1] != capturedSeeds[3] {
		t.Fatalf("generator batch seed drifted across retries: %#v", capturedSeeds)
	}
	for batchIndex, caseIndex := range []int{1, 2} {
		if len(capturedInputs[batchIndex]) != 1 {
			t.Fatalf("batch %d contains %d inputs, want 1", batchIndex, len(capturedInputs[batchIndex]))
		}
		var gotIndex, gotGroup int
		var gotSeed int64
		if _, err := fmt.Sscan(capturedInputs[batchIndex][0], &gotIndex, &gotGroup, &gotSeed); err != nil {
			t.Fatalf("parse generator stdin %q: %v", capturedInputs[batchIndex][0], err)
		}
		wantSeed := deterministicGeneratorSeed(code, caseIndex, baseCases[caseIndex].GroupID)
		if gotIndex != caseIndex || gotGroup != baseCases[caseIndex].GroupID || gotSeed != wantSeed {
			t.Fatalf("stdin[%d]=(%d,%d,%d), want=(%d,%d,%d)", batchIndex, gotIndex, gotGroup, gotSeed, caseIndex, baseCases[caseIndex].GroupID, wantSeed)
		}
	}
}

func TestGeneratorEvidencePreservesOriginalIndexesAfterCustomPrepend(t *testing.T) {
	t.Setenv("SANDBOX_URL", "http://sandbox:8090")
	const code = "int main(){}"
	parsed, err := parseTestDataResponse(`{"test_cases":[{"input":"","group_id":2,"description":"retained generated case"},{"input":"","group_id":3,"description":"discarded generated case"}],"generator_code":"int main(){}"}`)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.GeneratorCode != code {
		t.Fatalf("generator code = %q", parsed.GeneratorCode)
	}
	retained := mergeCustomCases(parsed.TestCases, domain.TestDataConfig{
		NumTestCases: 2,
		CustomCases: []domain.CustomTestCase{{
			Input: "custom\n", Description: "required custom case",
		}},
	})
	if len(retained) != 2 || retained[0].Origin != TestCaseOriginCustom {
		t.Fatalf("retained cases = %+v", retained)
	}

	wantAudit := SandboxAuditMetadata{
		RunID:                   "generator-run-0",
		ManifestDigest:          "sha256:generator-request-0",
		Seed:                    91,
		LimitProfile:            "generator-limits",
		ImageDigest:             "sha256:generator-image",
		ToolchainManifestDigest: "sha256:generator-toolchain",
		SeccompPolicyDigest:     "sha256:generator-seccomp",
	}
	calls := 0
	fake := &fakeRemoteSandbox{executeFunc: func(_ context.Context, _, _ string, inputs []string, _ remotesandbox.RemoteLimits) (*remotesandbox.RemoteExecuteResult, error) {
		calls++
		if len(inputs) != 1 {
			t.Fatalf("generator inputs = %q, want one retained case", inputs)
		}
		var gotIndex, gotGroup int
		var gotSeed int64
		if _, err := fmt.Sscan(inputs[0], &gotIndex, &gotGroup, &gotSeed); err != nil {
			t.Fatalf("parse generator stdin %q: %v", inputs[0], err)
		}
		wantSeed := deterministicGeneratorSeed(code, 0, 2)
		if gotIndex != 0 || gotGroup != 2 || gotSeed != wantSeed {
			t.Fatalf("generator stdin=(%d,%d,%d), want=(0,2,%d)", gotIndex, gotGroup, gotSeed, wantSeed)
		}
		return &remotesandbox.RemoteExecuteResult{
			Version: remotesandbox.ProtocolVersion,
			Compile: *remoteCompileResult("cpp", true, ""),
			Results: []remotesandbox.RemoteCaseResult{{Index: 0, Verdict: remotesandbox.VerdictOK, Stdout: "generated\n"}},
			Audit: remotesandbox.RemoteAuditMetadata{
				RunID: wantAudit.RunID, ManifestDigest: wantAudit.ManifestDigest,
				Seed: wantAudit.Seed, LimitProfile: wantAudit.LimitProfile,
				ImageDigest:             wantAudit.ImageDigest,
				ToolchainManifestDigest: wantAudit.ToolchainManifestDigest,
				SeccompPolicyDigest:     wantAudit.SeccompPolicyDigest,
			},
		}, nil
	}}
	restoreRemoteFactory(t, fake)

	execution, err := New(nil).executeGeneratorWithEvidence(context.Background(), code, retained)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || execution.TestCases[1].Input != "generated\n" {
		t.Fatalf("calls=%d execution=%+v", calls, execution)
	}
	// Durable replay identities remain zero-based; only user-facing diagnostics are one-based.
	generatedCase := execution.TestCases[1]
	if generatedCase.GeneratorCaseIndex == nil || *generatedCase.GeneratorCaseIndex != 0 || generatedCase.GeneratorBatchIndex == nil || *generatedCase.GeneratorBatchIndex != 0 {
		t.Fatalf("generator replay indexes = %+v", generatedCase)
	}
	if len(execution.Batches) != 1 || !reflect.DeepEqual(execution.Batches[0].TestIndexes, []int{1}) || !reflect.DeepEqual(execution.Batches[0].GeneratorCaseIndexes, []int{0}) || execution.Batches[0].Audit != wantAudit {
		t.Fatalf("generator batch evidence = %+v", execution.Batches)
	}
}

func TestExecuteGeneratorRejectsEmptyLiteralCaseBeforeSandbox(t *testing.T) {
	t.Setenv("SANDBOX_URL", "http://sandbox:8090")
	remoteFactoryCalls := 0
	original := newRemoteSandboxExecutor
	newRemoteSandboxExecutor = func(string, time.Duration) (remotesandbox.RemoteExecutor, error) {
		remoteFactoryCalls++
		return nil, errors.New("sandbox factory must not be called")
	}
	t.Cleanup(func() { newRemoteSandboxExecutor = original })

	for _, origin := range []TestCaseOrigin{TestCaseOriginCustom, TestCaseOriginLLMInline} {
		t.Run(string(origin), func(t *testing.T) {
			result, err := New(nil).executeGeneratorWithEvidence(context.Background(), "int main(){}", []TestCaseData{
				{Input: "literal\n"},
				{Origin: origin},
			})
			if err == nil || result != nil {
				t.Fatalf("empty %s literal was accepted: result=%+v err=%v", origin, result, err)
			}
			want := fmt.Sprintf("test case 2 with origin %q has empty literal input", origin)
			if err.Error() != want || strings.Contains(err.Error(), "test case 1") {
				t.Fatalf("diagnostic = %q, want %q", err, want)
			}
		})
	}
	if remoteFactoryCalls != 0 {
		t.Fatalf("sandbox factory calls = %d, want 0", remoteFactoryCalls)
	}
}

func TestExecuteGeneratorBatchesEmptyCasesAndMapsBatchIndexes(t *testing.T) {
	t.Setenv("SANDBOX_URL", "http://sandbox:8090")
	const code = "int main(){}"
	cases := make([]TestCaseData, 37)
	for i := range cases {
		cases[i].GroupID = i%5 + 1
	}
	cases[4].Input = "inline-four\n"
	cases[35].Input = "inline-thirty-five\n"

	var capturedInputs [][]string
	fake := &fakeRemoteSandbox{executeFunc: func(_ context.Context, _, _ string, inputs []string, _ remotesandbox.RemoteLimits) (*remotesandbox.RemoteExecuteResult, error) {
		batchIndex := len(capturedInputs)
		capturedInputs = append(capturedInputs, append([]string(nil), inputs...))
		if len(inputs) > generatorMaxBatchCases {
			t.Fatalf("batch %d contains %d inputs, max %d", batchIndex, len(inputs), generatorMaxBatchCases)
		}
		results := make([]remotesandbox.RemoteCaseResult, len(inputs))
		for resultIndex, input := range inputs {
			results[resultIndex] = remotesandbox.RemoteCaseResult{
				Index:   resultIndex,
				Verdict: remotesandbox.VerdictOK,
				Stdout:  fmt.Sprintf("batch-%d-local-%d|%s", batchIndex, resultIndex, input),
			}
		}
		return &remotesandbox.RemoteExecuteResult{
			Version: remotesandbox.ProtocolVersion,
			Compile: *remoteCompileResult("cpp", true, ""),
			Results: results,
		}, nil
	}}
	restoreRemoteFactory(t, fake)

	result, err := New(nil).executeGenerator(context.Background(), code, cases)
	if err != nil {
		t.Fatal(err)
	}
	gotBatchSizes := make([]int, len(capturedInputs))
	for i := range capturedInputs {
		gotBatchSizes[i] = len(capturedInputs[i])
	}
	remaining := len(cases) - 2
	wantBatchSizes := make([]int, 0, (remaining+generatorMaxBatchCases-1)/generatorMaxBatchCases)
	for remaining > 0 {
		size := generatorMaxBatchCases
		if remaining < size {
			size = remaining
		}
		wantBatchSizes = append(wantBatchSizes, size)
		remaining -= size
	}
	if !reflect.DeepEqual(gotBatchSizes, wantBatchSizes) {
		t.Fatalf("batch sizes = %v, want %v", gotBatchSizes, wantBatchSizes)
	}

	emptyIndex := 0
	for caseIndex, original := range cases {
		if original.Input != "" {
			if result[caseIndex].Input != original.Input {
				t.Fatalf("inline case %d changed from %q to %q", caseIndex, original.Input, result[caseIndex].Input)
			}
			continue
		}
		batchIndex := emptyIndex / generatorMaxBatchCases
		localIndex := emptyIndex % generatorMaxBatchCases
		seed := deterministicGeneratorSeed(code, caseIndex, original.GroupID)
		wantInput := fmt.Sprintf("%d %d %d\n", caseIndex, original.GroupID, seed)
		if capturedInputs[batchIndex][localIndex] != wantInput {
			t.Fatalf("batch %d input %d = %q, want %q", batchIndex, localIndex, capturedInputs[batchIndex][localIndex], wantInput)
		}
		wantOutput := fmt.Sprintf("batch-%d-local-%d|%s", batchIndex, localIndex, wantInput)
		if result[caseIndex].Input != wantOutput {
			t.Fatalf("case %d input = %q, want %q", caseIndex, result[caseIndex].Input, wantOutput)
		}
		emptyIndex++
	}
}

func TestValidateGeneratedTestInputsEnforcesSandboxInputBudgets(t *testing.T) {
	tests := []struct {
		name      string
		cases     []TestCaseData
		wantError string
	}{
		{
			name:      "empty second case",
			cases:     []TestCaseData{{Input: "first\n"}, {}},
			wantError: "generated test case 2 has empty input without successful generator output",
		},
		{
			name:      "single oversized case",
			cases:     []TestCaseData{{Input: string(make([]byte, generatorMaxOutputBytes+1))}},
			wantError: fmt.Sprintf("generated test case 1 exceeds the %d-byte sandbox input limit", generatorMaxOutputBytes),
		},
		{
			name: "combined cases",
			cases: []TestCaseData{
				{Input: string(make([]byte, generatorMaxOutputBytes))},
				{Input: string(make([]byte, generatorMaxOutputBytes))},
				{Input: string(make([]byte, generatorMaxOutputBytes))},
				{Input: string(make([]byte, generatorMaxOutputBytes))},
				{Input: "x"},
			},
			wantError: fmt.Sprintf("generated test inputs exceed the %d-byte sandbox batch input limit", generatorMaxTotalOutputBytes),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := validateGeneratedTestInputs(tt.cases); err == nil || err.Error() != tt.wantError {
				t.Fatalf("diagnostic = %v, want %q", err, tt.wantError)
			}
		})
	}
}

func TestExecuteGeneratorFailsClosedOnLaterBatch(t *testing.T) {
	t.Setenv("SANDBOX_URL", "http://sandbox:8090")
	tests := []struct {
		name              string
		result            *remotesandbox.RemoteExecuteResult
		err               error
		wantDiagnostic    string
		wantArtifactError bool
	}{
		{name: "transport", err: errors.New("connection refused")},
		{name: "compile", result: &remotesandbox.RemoteExecuteResult{
			Compile: *remoteCompileResult("cpp", false, "syntax error"),
		}, wantArtifactError: true},
		{name: "runtime verdict", result: &remotesandbox.RemoteExecuteResult{
			Compile: *remoteCompileResult("cpp", true, ""),
			Results: []remotesandbox.RemoteCaseResult{{Index: 0, Verdict: remotesandbox.VerdictTLE}},
		}, wantDiagnostic: "generator case 2 failed closed with verdict TLE", wantArtifactError: true},
		{name: "empty output", result: &remotesandbox.RemoteExecuteResult{
			Compile: *remoteCompileResult("cpp", true, ""),
			Results: []remotesandbox.RemoteCaseResult{{Index: 0, Verdict: remotesandbox.VerdictOK}},
		}, wantDiagnostic: "generator case 2 produced empty input", wantArtifactError: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			heartbeatsStarted := 0
			heartbeatsStopped := 0
			originalStartHeartbeat := startGeneratorHeartbeat
			startGeneratorHeartbeat = func(_ context.Context, msg string, interval time.Duration) context.CancelFunc {
				heartbeatsStarted++
				if interval != 10*time.Second {
					t.Fatalf("heartbeat interval = %s, want 10s", interval)
				}
				wantMessage := fmt.Sprintf("executing generator batch %d/2 in remote sandbox", heartbeatsStarted)
				if msg != wantMessage {
					t.Fatalf("heartbeat message = %q, want %q", msg, wantMessage)
				}
				stopped := false
				return func() {
					if stopped {
						t.Fatal("generator heartbeat stopped more than once")
					}
					stopped = true
					heartbeatsStopped++
				}
			}
			t.Cleanup(func() { startGeneratorHeartbeat = originalStartHeartbeat })

			fake := &fakeRemoteSandbox{executeFunc: func(_ context.Context, _, _ string, inputs []string, _ remotesandbox.RemoteLimits) (*remotesandbox.RemoteExecuteResult, error) {
				calls++
				if heartbeatsStarted != calls || heartbeatsStopped != calls-1 {
					t.Fatalf("remote call %d started with heartbeat state started=%d stopped=%d", calls, heartbeatsStarted, heartbeatsStopped)
				}
				if calls == 2 {
					return tt.result, tt.err
				}
				results := make([]remotesandbox.RemoteCaseResult, len(inputs))
				for i := range inputs {
					results[i] = remotesandbox.RemoteCaseResult{Index: i, Verdict: remotesandbox.VerdictOK, Stdout: "generated\n"}
				}
				return &remotesandbox.RemoteExecuteResult{
					Compile: *remoteCompileResult("cpp", true, ""),
					Results: results,
				}, nil
			}}
			restoreRemoteFactory(t, fake)

			cases := make([]TestCaseData, generatorMaxBatchCases+1)
			result, err := New(nil).executeGenerator(context.Background(), "int main(){}", cases)
			if err == nil || result != nil {
				t.Fatalf("expected later batch to fail closed, result=%+v err=%v", result, err)
			}
			var artifactErr *invalidGeneratedTestArtifactError
			if got := errors.As(err, &artifactErr); got != tt.wantArtifactError {
				t.Fatalf("invalid generated artifact classification = %v, want %v (err=%v)", got, tt.wantArtifactError, err)
			}
			if tt.wantDiagnostic != "" && (!strings.Contains(err.Error(), tt.wantDiagnostic) || strings.Contains(err.Error(), "generator case 1")) {
				t.Fatalf("diagnostic = %q, want to contain %q", err, tt.wantDiagnostic)
			}
			if calls != 2 {
				t.Fatalf("remote calls = %d, want 2", calls)
			}
			if heartbeatsStarted != 2 || heartbeatsStopped != 2 {
				t.Fatalf("heartbeat state after failure: started=%d stopped=%d, want 2/2", heartbeatsStarted, heartbeatsStopped)
			}
			for i := range cases {
				if cases[i].Input != "" {
					t.Fatalf("caller case %d was partially filled with %q", i, cases[i].Input)
				}
			}
		})
	}
}

func TestGeneratorRemoteSandboxContract(t *testing.T) {
	baseURL := os.Getenv("SANDBOX_CONTRACT_URL")
	if baseURL == "" {
		t.Skip("set SANDBOX_CONTRACT_URL to run the remote generator contract")
	}
	t.Setenv("SANDBOX_URL", baseURL)
	const code = "#include <iostream>\nint main(){long long i,g,s;if(!(std::cin>>i>>g>>s))return 1;std::cout<<i<<' '<<g<<' '<<s<<'\\n';}"
	cases := []TestCaseData{{GroupID: 9}}
	result, err := New(nil).executeGenerator(context.Background(), code, cases)
	if err != nil {
		t.Fatalf("remote generator: %v", err)
	}
	var gotIndex, gotGroup int
	var gotSeed int64
	if _, err := fmt.Sscan(result[0].Input, &gotIndex, &gotGroup, &gotSeed); err != nil {
		t.Fatalf("parse remote generator output %q: %v", result[0].Input, err)
	}
	wantSeed := deterministicGeneratorSeed(code, 0, 9)
	if gotIndex != 0 || gotGroup != 9 || gotSeed != wantSeed {
		t.Fatalf("remote generator output=(%d,%d,%d), want=(0,9,%d)", gotIndex, gotGroup, gotSeed, wantSeed)
	}
}

func TestExecuteGeneratorFailsClosedOnRemoteFailures(t *testing.T) {
	t.Setenv("SANDBOX_URL", "http://sandbox:8090")
	tests := []struct {
		name   string
		result *remotesandbox.RemoteExecuteResult
		err    error
	}{
		{name: "transport", err: errors.New("connection refused")},
		{name: "compile", result: &remotesandbox.RemoteExecuteResult{
			Compile: *remoteCompileResult("cpp", false, "syntax error"),
		}},
		{name: "runtime verdict", result: &remotesandbox.RemoteExecuteResult{
			Compile: *remoteCompileResult("cpp", true, ""),
			Results: []remotesandbox.RemoteCaseResult{{Index: 0, Verdict: remotesandbox.VerdictTLE}},
		}},
		{name: "empty output", result: &remotesandbox.RemoteExecuteResult{
			Compile: *remoteCompileResult("cpp", true, ""),
			Results: []remotesandbox.RemoteCaseResult{{Index: 0, Verdict: remotesandbox.VerdictOK}},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeRemoteSandbox{executeFunc: func(context.Context, string, string, []string, remotesandbox.RemoteLimits) (*remotesandbox.RemoteExecuteResult, error) {
				return tt.result, tt.err
			}}
			restoreRemoteFactory(t, fake)
			result, err := New(nil).executeGenerator(context.Background(), "int main(){}", []TestCaseData{{GroupID: 1}})
			if err == nil || result != nil {
				t.Fatalf("expected fail-closed error, result=%+v err=%v", result, err)
			}
		})
	}
}

func TestGeneratorSandboxUnavailableNeverInvokesHostCompiler(t *testing.T) {
	t.Setenv("SANDBOX_URL", "")
	tempDir := t.TempDir()
	sentinel := filepath.Join(tempDir, "host-compiler-called")
	fakeCompiler := filepath.Join(tempDir, "g++")
	if err := os.WriteFile(fakeCompiler, []byte("#!/bin/sh\n: > \""+sentinel+"\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tempDir)

	original := newRemoteSandboxExecutor
	newRemoteSandboxExecutor = func(string, time.Duration) (remotesandbox.RemoteExecutor, error) {
		t.Fatal("remote client factory must not run without SANDBOX_URL")
		return nil, nil
	}
	t.Cleanup(func() { newRemoteSandboxExecutor = original })

	result, err := New(nil).executeGenerator(context.Background(), "int main(){}", []TestCaseData{{GroupID: 1}})
	if err == nil || result != nil {
		t.Fatalf("expected missing sandbox failure, result=%+v err=%v", result, err)
	}
	if _, statErr := os.Stat(sentinel); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("host compiler sentinel exists or cannot be checked: %v", statErr)
	}
}
