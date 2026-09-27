package activities

import (
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/llm"
	"go.temporal.io/sdk/testsuite"
)

func TestPrepareImportedStatementActivityAcceptsBareJSON(t *testing.T) {
	source := domain.SourceProblem{ItemID: "a", Title: "Synthetic sum", Statement: "Compute $a+b$.\nInput:\n1 2\nOutput:\n3\n"}
	body := `{"ambiguous":false,"reason":"","changes":[],"samples":[{"input":"1 2","output":"3"}],"time_limit_ms":1000,"memory_limit_mb":128}`
	for _, wrapper := range []struct{ name, text string }{
		{"bare", body}, {"fenced", "```json\n" + body + "\n```"}, {"wrapped", "Analysis complete.\n" + body + "\nDone."},
	} {
		t.Run(wrapper.name, func(t *testing.T) {
			model := &capturingAuthoringLLM{response: &llm.Response{Model: "fixture-model", ModelObserved: true, StopReason: "end_turn", Content: []llm.ContentBlock{{Type: "text", Text: wrapper.text}}}}
			artifact := &captureArtifactStore{}
			acts := &Activities{deps: &Dependencies{LLM: model, LLMProvider: "fixture-provider", ProvenanceRecorder: &captureProvenanceRecorder{}}, artifacts: artifact}
			var suite testsuite.WorkflowTestSuite
			env := suite.NewTestActivityEnvironment()
			env.RegisterActivity(acts.PrepareImportedStatementActivity)
			params := domain.ProblemGenParams{ProviderConfig: &domain.ProviderRuntimeConfig{Verification: &domain.LLMRuntimeConfig{Model: "fixture-model", Provider: "fixture-provider", Protocol: "openai-responses", ReasoningEffort: "high"}}}
			encoded, err := env.ExecuteActivity(acts.PrepareImportedStatementActivity, PrepareImportedStatementInput{Source: source, Params: params})
			if err != nil {
				t.Fatal(err)
			}
			var out PreparedImportedStatement
			if err = encoded.Get(&out); err != nil {
				t.Fatal(err)
			}
			if out.Statement.Statement != source.Statement || out.Evidence.FinalSHA256 != source.Hash() || len(out.Samples) != 1 || out.Samples[0].Input != "1 2" || out.Samples[0].Output != "3" || out.TimeLimit != 1000 || out.MemoryLimit != 128 {
				t.Fatalf("original source/sample evidence changed: %+v", out)
			}
			if len(out.Statement.SourceArtifacts) != 1 || out.Statement.SourceArtifacts[0].LLMCallReceipt == nil || len(artifact.data) == 0 {
				t.Fatal("call evidence missing")
			}
			if model.calls != 1 || model.request.Model != "fixture-model" || model.request.MaxTokens != 8192 || model.request.Runtime.ReasoningEffort != "medium" {
				t.Fatal("source planning must use medium effort without changing the model, call count, or output budget")
			}
			if params.ProviderConfig.Verification.ReasoningEffort != "high" {
				t.Fatal("source planning must not mutate the caller's configured reasoning effort")
			}
		})
	}
}

func TestImportedSourceJSONRejectsIncompleteOrUnrelatedObjects(t *testing.T) {
	for _, body := range []string{"", "null", "{}", "[]", `{"ambiguous":false}`, `{"ambiguous":false,"samples":null}`, `{"ambiguous":"false","samples":[]}`, `{"ambiguous":false,"samples":"none"}`, `{"ambiguous":false,"samples":[`, "not JSON"} {
		t.Run(body, func(t *testing.T) {
			var out importAnalysis
			if decodeImportModelJSON(&llm.Response{Content: []llm.ContentBlock{{Type: "text", Text: body}}}, &out, "ambiguous", "samples") == nil {
				t.Fatalf("invalid source analysis accepted: %s", body)
			}
		})
	}
	var out importAnalysis
	if err := decodeImportModelJSON(&llm.Response{StopReason: "max_tokens", Content: []llm.ContentBlock{{Type: "text", Text: `{"ambiguous":false,"samples":[]}`}}}, &out, "ambiguous", "samples"); err == nil || !strings.Contains(err.Error(), "truncated") {
		t.Fatalf("truncated response accepted: %v", err)
	}
	if err := decodeImportModelJSON(&llm.Response{Content: []llm.ContentBlock{{Type: "text", Text: `{"ambiguous":false,"samples":[]}`}}}, &out, "ambiguous", "samples"); err != nil {
		t.Fatalf("explicit no-sample response rejected: %v", err)
	}
}

func TestImportedDuplicateJSONAcceptsBareAndWrappedObjects(t *testing.T) {
	for _, body := range []string{`{"duplicate_of":"","reason":""}`, "```json\n{\"duplicate_of\":\"candidate\",\"reason\":\"same task\"}\n```", `Result: {"duplicate_of":"candidate","reason":"same $\le$ constraint"}`} {
		var out ImportDuplicateResult
		if err := decodeImportModelJSON(&llm.Response{Content: []llm.ContentBlock{{Type: "text", Text: body}}}, &out, "duplicate_of"); err != nil {
			t.Fatalf("duplicate response rejected: %s: %v", body, err)
		}
		if strings.Contains(body, "candidate") && out.DuplicateOf != "candidate" {
			t.Fatal("duplicate id lost")
		}
		if strings.Contains(body, `\le`) && !strings.Contains(out.Reason, `\le`) {
			t.Fatal("mathematical evidence altered")
		}
	}
	for _, body := range []string{"null", "{}", `{"duplicate_of":null}`, `{"duplicate_of":true}`, `{"duplicate_of":"candidate"`, "not JSON"} {
		var out ImportDuplicateResult
		if decodeImportModelJSON(&llm.Response{Content: []llm.ContentBlock{{Type: "text", Text: body}}}, &out, "duplicate_of") == nil {
			t.Fatalf("invalid duplicate response accepted: %s", body)
		}
	}
}
