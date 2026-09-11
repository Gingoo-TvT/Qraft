package activities

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	remotesandbox "github.com/Gingoo-TvT/Qraft/backend/internal/sandbox"
	"github.com/Gingoo-TvT/Qraft/backend/internal/testdatagen"
)

func TestRecipeRetainsCoverageAndRepairsTransportEscapes(t *testing.T) {
	code := "void generate(long long i, long long g, af::Random& rng, std::ostream& out) {\\n out << rng.integer(1, 9) << '\\n';\\n}"
	raw, _ := json.Marshal(map[string]any{
		"generator_recipe": testdatagen.Recipe{Version: testdatagen.Version, Code: code},
		"test_cases":       []map[string]any{{"input": "", "coverage": []string{"small", "random"}}},
	})
	result, err := parseTestDataResponse(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(result.TestCases[0].Coverage, ",") != "random,small_exhaustive" {
		t.Fatalf("coverage lost: %v", result.TestCases[0].Coverage)
	}
	if !strings.Contains(result.GeneratorCode, "{\n out <<") || !strings.Contains(result.GeneratorCode, "'\\n'") {
		t.Fatal("transport repair damaged C++ literals or failed to decode layout")
	}
}

func TestGeneratorInvalidOutputRemainsNonRetryableArtifact(t *testing.T) {
	for _, name := range []string{"budget", "exit", "signal"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("SANDBOX_URL", "http://sandbox:8090")
			fake := &fakeRemoteSandbox{executeFunc: func(_ context.Context, _, _ string, _ []string, _ remotesandbox.RemoteLimits) (*remotesandbox.RemoteExecuteResult, error) {
				item := remotesandbox.RemoteCaseResult{Index: 0, Verdict: remotesandbox.VerdictOK, Stdout: "x"}
				switch name {
				case "budget":
					item.Stdout = "xx"
				case "exit":
					item.ExitCode = 1
				case "signal":
					item.Signal = "SIGTERM"
				}
				return &remotesandbox.RemoteExecuteResult{Compile: *remoteCompileResult("cpp", true, ""), Results: []remotesandbox.RemoteCaseResult{item}}, nil
			}}
			restoreRemoteFactory(t, fake)
			_, err := New(nil).executeGeneratorWithEvidence(context.Background(), "int main(){}", []TestCaseData{{GeneratorOutputLimitBytes: 1}})
			var invalid *invalidGeneratedTestArtifactError
			if !errors.As(err, &invalid) {
				t.Fatalf("expected invalid artifact, got %v", err)
			}
		})
	}
}
