package activities

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/llm/prompts"
	remotesandbox "github.com/Gingoo-TvT/Qraft/backend/internal/sandbox"
	"github.com/Gingoo-TvT/Qraft/backend/internal/testdatagen"
)

const recipeCode = "void generate(long long i, long long g, af::Random& rng, std::ostream& out) { out << i << ' ' << g << ' ' << rng.integer(1,100) << '\\n'; }"

func TestReusableGeneratorRecipeAndPromptProtocol(t *testing.T) {
	input := map[string]any{"generator_recipe": testdatagen.Recipe{Version: testdatagen.Version, Code: recipeCode}, "test_cases": []map[string]any{{"input": "", "group_id": 3}}}
	bytes, _ := json.Marshal(input)
	parsed, err := parseTestDataResponse(string(bytes))
	if err != nil {
		t.Fatal(err)
	}
	built, _ := testdatagen.Build(testdatagen.Recipe{Version: testdatagen.Version, Code: recipeCode})
	if parsed.GeneratorCode != built.Source || parsed.TestCases[0].GeneratorOutputLimitBytes != testdatagen.DefaultCaseBytes {
		t.Fatal("recipe not materialized")
	}
	if deterministicGeneratorSeed(built.Source, 0, 3) != testdatagen.Seed(built.Source, 0, 3) {
		t.Fatal("tool and workflow seeds differ")
	}
	input["generator_code"] = "int main(){}"
	bytes, _ = json.Marshal(input)
	if _, err := parseTestDataResponse(string(bytes)); err == nil {
		t.Fatal("conflicting code and recipe accepted")
	}
	delete(input, "generator_code")
	input["generator_recipe"] = testdatagen.Recipe{Version: "future", Code: recipeCode}
	bytes, _ = json.Marshal(input)
	if _, err := parseTestDataResponse(string(bytes)); err == nil {
		t.Fatal("unsupported version accepted")
	}
	input["generator_recipe"] = testdatagen.Recipe{Version: testdatagen.Version, Code: recipeCode}
	input["test_cases"] = []map[string]any{{"output_limit_bytes": -1}}
	bytes, _ = json.Marshal(input)
	if _, err := parseTestDataResponse(string(bytes)); err == nil {
		t.Fatal("invalid case limit accepted")
	}

	req, err := prompts.TestDataTemplate().Render(prompts.TestDataInput{Config: domain.DefaultTestDataConfig(), PreferGenerator: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, prompt := range []string{req.System, testDataSystemPrompt} {
		if !strings.Contains(prompt, testdatagen.Prompt) {
			t.Fatal("framework API not shared")
		}
	}
	if strings.Contains(req.Messages[0].Content, "C++17") || strings.Contains(req.Messages[0].Content, "testlib 兼容") {
		t.Fatal("old generator contract remains")
	}
}

func TestRecipeBatchingPreservesCustomAndGeneratorIndexes(t *testing.T) {
	t.Setenv("SANDBOX_URL", "http://sandbox:8090")
	entries := []map[string]any{{"input": "sample\n", "group_id": 0, "is_sample": true}}
	for i := 1; i < 18; i++ {
		limit := testdatagen.DefaultCaseBytes
		if i == 1 {
			limit = testdatagen.MaxCaseBytes
		}
		entries = append(entries, map[string]any{"input": "", "group_id": 2, "output_limit_bytes": limit})
	}
	raw, _ := json.Marshal(map[string]any{"generator_recipe": testdatagen.Recipe{Version: testdatagen.Version, Code: recipeCode}, "test_cases": entries})
	parsed, err := parseTestDataResponse(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	cases := mergeCustomCases(parsed.TestCases, domain.TestDataConfig{NumTestCases: 20, CustomCases: []domain.CustomTestCase{{Input: "custom1\n"}, {Input: "custom2\n"}}})
	sizes := []int{}
	fake := &fakeRemoteSandbox{executeFunc: func(_ context.Context, _, source string, inputs []string, limits remotesandbox.RemoteLimits) (*remotesandbox.RemoteExecuteResult, error) {
		sizes = append(sizes, len(inputs))
		if int64(len(inputs))*limits.OutputLimitBytes > testdatagen.MaxCaseBytes {
			t.Fatal("unsafe JSON response budget")
		}
		result := &remotesandbox.RemoteExecuteResult{Compile: *remoteCompileResult("cpp", true, "")}
		for j, input := range inputs {
			var index, group int
			var seed int64
			if _, err := fmt.Sscan(input, &index, &group, &seed); err != nil {
				t.Fatal(err)
			}
			if seed != testdatagen.Seed(source, index, group) {
				t.Fatal("seed identity drift")
			}
			result.Results = append(result.Results, remotesandbox.RemoteCaseResult{Index: j, Verdict: remotesandbox.VerdictOK, Stdout: fmt.Sprintf("%d\n", index)})
		}
		return result, nil
	}}
	restoreRemoteFactory(t, fake)
	execution, err := New(nil).executeGeneratorWithEvidence(context.Background(), parsed.GeneratorCode, cases)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sizes, []int{1, 8, 8}) {
		t.Fatalf("sizes=%v", sizes)
	}
	if execution.TestCases[0].Input != "custom1\n" || execution.TestCases[2].Input != "sample\n" {
		t.Fatal("literal cases changed")
	}
	for i := 3; i < len(cases); i++ {
		tc := execution.TestCases[i]
		if *tc.GeneratorCaseIndex != i-2 || tc.Input != fmt.Sprintf("%d\n", i-2) {
			t.Fatalf("case %d lost original index", i)
		}
	}
	if cases[3].Input != "" {
		t.Fatal("caller cases mutated")
	}
}
