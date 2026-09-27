package activities

import (
	"context"
	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	remotesandbox "github.com/Gingoo-TvT/Qraft/backend/internal/sandbox"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestExpandedSandboxOutputFitsTenCaseBatch(t *testing.T) {
	t.Setenv("SANDBOX_URL", "http://sandbox:8090")
	calls := 0
	fake := &fakeRemoteSandbox{executeFunc: func(_ context.Context, _, _ string, inputs []string, limits remotesandbox.RemoteLimits) (*remotesandbox.RemoteExecuteResult, error) {
		calls++
		require.LessOrEqual(t, limits.OutputLimitBytes*int64(len(inputs)), int64(64<<20))
		result := &remotesandbox.RemoteExecuteResult{Compile: *remoteCompileResult("cpp", true, ""), Results: make([]remotesandbox.RemoteCaseResult, len(inputs)), Audit: remotesandbox.RemoteAuditMetadata{RunID: "complete-final-run"}}
		for i := range result.Results {
			result.Results[i] = remotesandbox.RemoteCaseResult{Index: i, Verdict: remotesandbox.VerdictOK, Stdout: "x"}
		}
		if calls == 1 {
			result.Results[0].Verdict = remotesandbox.Verdict("OLE")
		} else {
			require.EqualValues(t, (64<<20)/10, limits.OutputLimitBytes)
		}
		return result, nil
	}}
	restoreRemoteFactory(t, fake)
	got, err := New(nil).RunSandboxActivity(context.Background(), domain.Solution{Language: "cpp"}, make([]TestCaseData, 10), ExecutionLimits{TimeLimitMs: 1000, MemoryLimitMB: 256})
	require.NoError(t, err)
	require.Equal(t, 2, calls)
	require.Len(t, got.Outputs, 10)
	require.Equal(t, "complete-final-run", got.Audit.RunID)
}
func TestImportedSandboxOptInUsesStricterMemoryCeiling(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "strict_contract", true: "import_opt_in"}[enabled], func(t *testing.T) {
			t.Setenv("SANDBOX_URL", "http://sandbox:8090")
			calls := 0
			fake := &fakeRemoteSandbox{executeFunc: func(_ context.Context, _, _ string, _ []string, limits remotesandbox.RemoteLimits) (*remotesandbox.RemoteExecuteResult, error) {
				calls++
				if calls == 1 {
					require.Equal(t, 1024, limits.MemoryLimitMB)
					return nil, &remotesandbox.RemoteError{StatusCode: 422, Code: "deployment_limit_exceeded", Message: "memory_limit_mb 1024 exceeds deployed execute maximum 512"}
				}
				require.Equal(t, 512, limits.MemoryLimitMB)
				return &remotesandbox.RemoteExecuteResult{Compile: *remoteCompileResult("cpp", true, ""), Results: []remotesandbox.RemoteCaseResult{{Index: 0, Verdict: remotesandbox.VerdictOK, Stdout: "ok"}}}, nil
			}}
			restoreRemoteFactory(t, fake)
			_, err := New(nil).RunSandboxActivity(context.Background(), domain.Solution{Language: "cpp"}, []TestCaseData{{Input: "1"}}, ExecutionLimits{TimeLimitMs: 1000, MemoryLimitMB: 1024, UseDeploymentLimits: enabled})
			if enabled {
				require.NoError(t, err)
				require.Equal(t, 2, calls)
			} else {
				require.Error(t, err)
				require.Equal(t, 1, calls)
			}
		})
	}
}
func TestImportedMemoryCeilingDoesNotChangeExplicitProfiles(t *testing.T) {
	limits := remotesandbox.NewRemoteLimits(1000, 1024)
	limits.Profile = remotesandbox.SanitizerCAndCPPProfileV1
	err := &remotesandbox.RemoteError{StatusCode: 422, Code: "deployment_limit_exceeded", Message: "memory_limit_mb 1024 exceeds deployed execute maximum 512"}
	require.False(t, lowerImportedMemoryCeiling(err, &limits))
	require.Equal(t, 1024, limits.MemoryLimitMB)
}
