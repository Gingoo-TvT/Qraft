package activities

import (
	"context"
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	remotesandbox "github.com/Gingoo-TvT/Qraft/backend/internal/sandbox"
	"github.com/stretchr/testify/require"
)

func TestDefaultSandboxOutputBudgetExpandsOnceWithFinalAudit(t *testing.T) {
	for _, eventualSuccess := range []bool{true, false} {
		t.Run(map[bool]string{true: "large_correct_output", false: "unbounded_output"}[eventualSuccess], func(t *testing.T) {
			t.Setenv("SANDBOX_URL", "http://sandbox:8090")
			calls := 0
			fake := &fakeRemoteSandbox{executeFunc: func(_ context.Context, _, _ string, inputs []string, limits remotesandbox.RemoteLimits) (*remotesandbox.RemoteExecuteResult, error) {
				calls++
				require.Equal(t, []string{"input\n"}, inputs)
				require.EqualValues(t, map[int]int{1: 2 << 20, 2: 8 << 20}[calls], limits.OutputLimitBytes)
				r := &remotesandbox.RemoteExecuteResult{Compile: *remoteCompileResult("cpp", true, ""), Results: []remotesandbox.RemoteCaseResult{{Index: 0, Verdict: remotesandbox.Verdict("OLE"), ExitCode: -1}}, Audit: remotesandbox.RemoteAuditMetadata{RunID: "original"}}
				if calls == 2 {
					r.Audit.RunID = "expanded"
					if eventualSuccess {
						r.Results[0] = remotesandbox.RemoteCaseResult{Index: 0, Verdict: remotesandbox.VerdictOK, Stdout: strings.Repeat("x", 3<<20)}
					}
				}
				return r, nil
			}}
			restoreRemoteFactory(t, fake)
			a := &Activities{deps: &Dependencies{}, artifacts: &captureArtifactStore{}}
			result, err := a.RunSandboxActivity(context.Background(), domain.Solution{Language: "cpp"}, []TestCaseData{{Input: "input\n"}}, ExecutionLimits{TimeLimitMs: 1000, MemoryLimitMB: 256})
			require.Equal(t, 2, calls)
			if eventualSuccess {
				require.NoError(t, err)
				require.Equal(t, "expanded", result.Audit.RunID)
				require.Len(t, result.OutputArtifacts, 1)
				require.Empty(t, result.Outputs[0])
			} else {
				require.Error(t, err)
				require.Nil(t, result)
				require.Contains(t, err.Error(), "8388608 bytes")
			}
		})
	}
}

func TestExplicitSandboxOutputLimitNeverExpands(t *testing.T) {
	t.Setenv("SANDBOX_URL", "http://sandbox:8090")
	calls := 0
	fake := &fakeRemoteSandbox{executeFunc: func(_ context.Context, _, _ string, _ []string, limits remotesandbox.RemoteLimits) (*remotesandbox.RemoteExecuteResult, error) {
		calls++
		require.EqualValues(t, 12345, limits.OutputLimitBytes)
		return &remotesandbox.RemoteExecuteResult{Compile: *remoteCompileResult("cpp", true, ""), Results: []remotesandbox.RemoteCaseResult{{Index: 0, Verdict: remotesandbox.Verdict("OLE"), ExitCode: -1}}}, nil
	}}
	restoreRemoteFactory(t, fake)
	_, err := New(nil).RunSandboxActivity(context.Background(), domain.Solution{Language: "cpp"}, []TestCaseData{{Input: "1"}}, ExecutionLimits{TimeLimitMs: 1000, MemoryLimitMB: 256, OutputLimitBytes: 12345})
	require.Error(t, err)
	require.Equal(t, 1, calls)
	require.Contains(t, err.Error(), "12345 bytes")
}
