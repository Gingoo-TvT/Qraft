package activities

import (
	"context"
	"fmt"
	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	remotesandbox "github.com/Gingoo-TvT/Qraft/backend/internal/sandbox"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

func TestImportedLargeOutputUsesBoundedBatchesAndExactReceipts(t *testing.T) {
	t.Setenv("SANDBOX_URL", "http://sandbox:8090")
	calls := 0
	fake := &fakeRemoteSandbox{executeFunc: func(_ context.Context, _, _ string, inputs []string, limits remotesandbox.RemoteLimits) (*remotesandbox.RemoteExecuteResult, error) {
		calls++
		require.LessOrEqual(t, int64(len(inputs))*limits.OutputLimitBytes, int64(64<<20))
		require.EqualValues(t, map[int]int{1: 2 << 20, 2: 8 << 20, 3: 2 << 20}[calls], limits.OutputLimitBytes)
		if calls < 3 {
			require.Len(t, inputs, 8)
			require.Equal(t, "0", inputs[0])
		} else {
			require.Equal(t, []string{"8"}, inputs)
		}
		r := &remotesandbox.RemoteExecuteResult{Compile: *remoteCompileResult("cpp", true, ""), Audit: remotesandbox.RemoteAuditMetadata{RunID: fmt.Sprint(calls)}}
		for i, v := range inputs {
			r.Results = append(r.Results, remotesandbox.RemoteCaseResult{Index: i, Verdict: remotesandbox.VerdictOK, Stdout: v})
		}
		if calls == 1 {
			r.Results[0].Verdict = remotesandbox.Verdict("OLE")
		} else if calls == 2 {
			r.Results[0].Stdout = strings.Repeat("x", 7<<20)
		}
		return r, nil
	}}
	restoreRemoteFactory(t, fake)
	a := &Activities{deps: &Dependencies{}, artifacts: &captureArtifactStore{}}
	cases := make([]TestCaseData, 9)
	for i := range cases {
		cases[i].Input = fmt.Sprint(i)
	}
	result, err := a.RunSandboxActivity(context.Background(), domain.Solution{Language: "cpp"}, cases, ExecutionLimits{TimeLimitMs: 1000, MemoryLimitMB: 256, UseDeploymentLimits: true})
	require.NoError(t, err)
	require.Equal(t, 3, calls)
	require.Equal(t, SandboxAuditMetadata{}, result.Audit)
	require.Len(t, result.Batches, 2)
	require.Equal(t, 0, result.Batches[0].FirstCase)
	require.Equal(t, 8, result.Batches[0].CaseCount)
	require.Equal(t, "2", result.Batches[0].Audit.RunID)
	require.Equal(t, 8, result.Batches[1].FirstCase)
	require.Equal(t, 1, result.Batches[1].CaseCount)
	require.Equal(t, "3", result.Batches[1].Audit.RunID)
	require.Len(t, result.OutputArtifacts, 9)
	require.Nil(t, result.OutputArtifacts[8])
	require.Equal(t, "8", result.Outputs[8])
	require.EqualValues(t, 7<<20, result.OutputArtifacts[0].SizeBytes)
	require.Equal(t, manifestSHA256([]byte(strings.Repeat("x", 7<<20))), result.OutputArtifacts[0].SHA256)
}

func TestBatchedExecutionManifestRejectsIncompleteEvidence(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	audit := SandboxAuditMetadata{RunID: "one", ManifestDigest: digest, ImageDigest: digest, ToolchainManifestDigest: digest, SeccompPolicyDigest: digest, LimitProfile: "test"}
	batches := []SandboxExecutionBatch{{FirstCase: 0, CaseCount: 8, Audit: audit}, {FirstCase: 8, CaseCount: 2, Audit: audit}}
	require.NoError(t, validateTestManifestExecution("main", TestManifestSandboxIdentity{}, batches, 10))
	require.Error(t, validateTestManifestExecution("main", testManifestSandboxIdentity(audit), batches, 10))
	require.Error(t, validateTestManifestExecution("main", TestManifestSandboxIdentity{}, batches[:1], 10))
	batches[1].FirstCase = 7
	require.Error(t, validateTestManifestExecution("main", TestManifestSandboxIdentity{}, batches, 10))
	batches[1].FirstCase = 8
	batches[1].Audit.RunID = ""
	require.Error(t, validateTestManifestExecution("main", TestManifestSandboxIdentity{}, batches, 10))
}

func TestBuildManifestRoundTripsImportedBatchReceipts(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	audit := SandboxAuditMetadata{RunID: "first", ManifestDigest: digest, ImageDigest: digest, ToolchainManifestDigest: digest, SeccompPolicyDigest: digest, LimitProfile: "test"}
	second := audit
	second.RunID = "second"
	second.ManifestDigest = "sha256:" + strings.Repeat("b", 64)
	input := BuildTestManifestInput{
		PayloadVersion: ActivityPayloadVersion,
		MainOutput:     SandboxResult{PayloadVersion: ActivityPayloadVersion, Batches: []SandboxExecutionBatch{{FirstCase: 0, CaseCount: 8, Audit: audit}, {FirstCase: 8, CaseCount: 2, Audit: second}}},
		BruteOutput:    SandboxResult{PayloadVersion: ActivityPayloadVersion, Outputs: []string{"x"}, Audit: audit},
		BruteIndices:   []int{0}, MainSolution: domain.Solution{Language: "cpp", SourceCode: "synthetic-main"}, BruteSolution: domain.Solution{Language: "cpp", SourceCode: "synthetic-brute"},
	}
	for i := 0; i < 10; i++ {
		input.TestCases = append(input.TestCases, TestCaseData{Input: "1", Origin: TestCaseOriginCustom, Description: "synthetic case"})
		input.MainOutput.Outputs = append(input.MainOutput.Outputs, "x")
	}
	manifest, err := New(nil).BuildTestManifestActivity(context.Background(), input)
	require.NoError(t, err)
	encoded, _, err := CanonicalTestManifestJSON(*manifest)
	require.NoError(t, err)
	restored, err := ParseTestManifestJSON(encoded)
	require.NoError(t, err)
	canonical, _, err := CanonicalTestManifestJSON(*restored)
	require.NoError(t, err)
	require.Equal(t, encoded, canonical)
	require.Equal(t, manifest.MainBatches, restored.MainBatches)
	require.Equal(t, "second", restored.MainBatches[1].Audit.RunID)
}
