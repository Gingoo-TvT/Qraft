package activities

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/llm"
	remotesandbox "github.com/Gingoo-TvT/Qraft/backend/internal/sandbox"
	"github.com/Gingoo-TvT/Qraft/backend/internal/testdatagen"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
)

func importedRecipeFixture(t *testing.T, marker string) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{"generator_recipe": testdatagen.Recipe{Version: testdatagen.Version, Code: recipeCode + "\n// " + marker}, "test_cases": []map[string]any{{"input": "", "group_id": 1, "is_sample": true}}})
	require.NoError(t, err)
	return string(b)
}

func TestImportedGeneratorExecutionRepairsPreserveCustomCasesAndEvidence(t *testing.T) {
	for _, failure := range []string{"CE", "OLE", "TLE", "empty"} {
		t.Run(failure, func(t *testing.T) {
			t.Setenv("SANDBOX_URL", "http://sandbox:8090")
			client := &importResponseSequence{texts: []string{importedRecipeFixture(t, "broken"), importedRecipeFixture(t, "repaired")}}
			client.observe = func(req *llm.Request) {
				if client.calls == 2 {
					require.Contains(t, req.Messages[len(req.Messages)-1].Content, "generator")
					if failure != "empty" {
						require.Contains(t, req.Messages[len(req.Messages)-1].Content, map[string]string{"CE": "std::string", "OLE": "OLE", "TLE": "TLE"}[failure])
					}
				}
			}
			executions := 0
			fake := &fakeRemoteSandbox{executeFunc: func(_ context.Context, _, code string, inputs []string, limits remotesandbox.RemoteLimits) (*remotesandbox.RemoteExecuteResult, error) {
				executions++
				require.Len(t, inputs, 1)
				require.EqualValues(t, 8<<20, limits.OutputLimitBytes)
				result := &remotesandbox.RemoteExecuteResult{Compile: *remoteCompileResult("cpp", true, ""), Results: []remotesandbox.RemoteCaseResult{{Index: 0, Verdict: remotesandbox.VerdictOK, Stdout: "4\n"}}}
				if strings.Contains(code, "// broken") {
					switch failure {
					case "CE":
						result.Compile = *remoteCompileResult("cpp", false, "error: string does not name a type; use std::string")
					case "empty":
						result.Results[0].Stdout = ""
					default:
						result.Results[0].Verdict = remotesandbox.Verdict(failure)
						result.Results[0].ExitCode = -1
					}
				}
				return result, nil
			}}
			restoreRemoteFactory(t, fake)
			effects := &importEffectFixture{values: map[string]json.RawMessage{}, hashes: map[string]string{}}
			a := &Activities{deps: &Dependencies{LLM: client, LLMProvider: "fixture", LLMModel: "fixture", ProviderEffects: effects, ProviderEffectLease: time.Minute, ProvenanceRecorder: &captureProvenanceRecorder{}}, artifacts: &captureArtifactStore{}}
			var suite testsuite.WorkflowTestSuite
			env := suite.NewTestActivityEnvironment()
			// Run twice with the same activity identity to prove redelivery reuses both
			// durable model responses and revalidates them without duplicate literals.
			fn := func(ctx context.Context) error {
				for run := 0; run < 2; run++ {
					result, err := a.GenerateTestDataActivity(ctx, "Read one number.", domain.TestDataConfig{NumTestCases: 2, NumSamples: 1, CustomCases: []domain.CustomTestCase{{Input: "2\n"}}}, domain.ProblemGenParams{SourceProblem: &domain.SourceProblem{Title: "Synthetic", Statement: "Read one number."}})
					require.NoError(t, err)
					require.Len(t, result.TestCases, 2)
					require.Equal(t, "2\n", result.TestCases[0].Input)
					require.Equal(t, "4\n", result.TestCases[1].Input)
					require.Len(t, result.SourceArtifacts, 2)
					require.Len(t, result.GeneratorBatches, 1)
					require.Equal(t, 0, *result.TestCases[1].GeneratorCaseIndex)
					require.Empty(t, result.GeneratorCode)
					require.NotEmpty(t, result.GeneratorSHA256)
				}
				return nil
			}
			env.RegisterActivity(fn)
			_, err := env.ExecuteActivity(fn)
			require.NoError(t, err)
			require.Equal(t, 2, client.calls)
			require.Equal(t, 4, executions)
			require.Len(t, effects.values, 2)
		})
	}
}

func TestImportedGeneratorRepairExhaustionAndServiceFailures(t *testing.T) {
	for _, infrastructure := range []bool{false, true} {
		t.Run(map[bool]string{true: "service", false: "bad_code"}[infrastructure], func(t *testing.T) {
			t.Setenv("SANDBOX_URL", "http://sandbox:8090")
			payload := importedRecipeFixture(t, "broken")
			model := &importResponseSequence{texts: []string{payload, payload, payload}}
			fake := &fakeRemoteSandbox{executeFunc: func(context.Context, string, string, []string, remotesandbox.RemoteLimits) (*remotesandbox.RemoteExecuteResult, error) {
				if infrastructure {
					return nil, errors.New("sandbox unavailable")
				}
				return &remotesandbox.RemoteExecuteResult{Compile: *remoteCompileResult("cpp", false, "unknown std::type")}, nil
			}}
			restoreRemoteFactory(t, fake)
			a := &Activities{deps: &Dependencies{LLM: model, LLMProvider: "fixture", LLMModel: "fixture"}, artifacts: &captureArtifactStore{}}
			_, _, _, err := a.completeImportedStructured(context.Background(), "testdata", &llm.Request{System: "JSON", MaxTokens: 100}, func(r *llm.Response) error {
				parsed, e := parseTestDataResponse(r.Text())
				if e != nil {
					return e
				}
				return a.prepareImportedTestData(context.Background(), parsed, domain.TestDataConfig{NumTestCases: 1})
			})
			require.Error(t, err)
			if infrastructure {
				require.Equal(t, 1, model.calls)
				require.Contains(t, err.Error(), "sandbox unavailable")
			} else {
				require.Equal(t, 3, model.calls)
				var application *temporal.ApplicationError
				require.True(t, errors.As(err, &application))
				require.True(t, application.NonRetryable())
			}
		})
	}
}

func TestClarificationRepairReceivesExactMathQuote(t *testing.T) {
	source := domain.SourceProblem{ItemID: "synthetic", Title: "Window", Statement: "# Window\n\nCount $n$ values satisfying $x<\n\n## Output\nCount."}
	bad := importAnalysis{Ambiguous: true, Reason: "Truncated inequality", Changes: []domain.ImportClarification{{Original: "Count n values satisfying $x<", Replacement: "Count $n$ values satisfying $x<y$."}}}
	_, err := applyImportClarifications(source, bad)
	require.Error(t, err)
	require.Contains(t, err.Error(), `Count $n$ values satisfying $x<`)
	bad.Changes[0].Original = "Count $n$ values satisfying $x<"
	result, err := applyImportClarifications(source, bad)
	require.NoError(t, err)
	require.Contains(t, result.Statement.Statement, "$x<y$")
	require.Equal(t, source.Statement, result.Evidence.Original.Statement)
	// Diagnostic normalization must never silently approve a wrong quote.
	bad.Changes[0].Original = "Count n values satisfying x<"
	_, err = applyImportClarifications(source, bad)
	require.Error(t, err)
}
