package activities

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/rating"
)

func TestRatingAnalysisSummaryRetainsLabelsWithStableBoundedFormatting(t *testing.T) {
	const want = "conclusion: retain uncertainty\nproblem: synthetic sum\nsemantic_review: candidate only"
	for _, raw := range []string{
		`{"problem":"synthetic sum","conclusion":"retain uncertainty","semantic_review":"candidate only"}`,
		`{"semantic_review":"candidate only","conclusion":"retain uncertainty","problem":"synthetic sum"}`,
	} {
		var got rating.Analysis
		if err := ratingDecode(`{"summary":`+raw+`}`, &got); err != nil {
			t.Fatal(err)
		}
		if got.Summary != want {
			t.Fatalf("labels/value order changed: %q", got.Summary)
		}
	}
	text := `  original text\nnot rewritten  `
	encoded, _ := json.Marshal(text)
	var got rating.Analysis
	if err := ratingDecode(`{"summary":`+string(encoded)+`}`, &got); err != nil || got.Summary != text {
		t.Fatalf("original text changed: %q %v", got.Summary, err)
	}
	allowed, _ := json.Marshal(map[string]string{"x": strings.Repeat("a", (16<<10)-3)})
	if _, err := ratingAnalysisSummaryText(allowed); err != nil {
		t.Fatal(err)
	}
	tooBig, _ := json.Marshal(map[string]string{"x": strings.Repeat("a", (16<<10)-2)})
	if _, err := ratingAnalysisSummaryText(tooBig); err == nil {
		t.Fatal("oversized rendered summary accepted")
	}
}

func TestRatingAnalysisSummaryRejectsStructuralAndSemanticBypasses(t *testing.T) {
	for _, raw := range []string{
		`{}`, `null`, `[]`, `1`, `false`,
		`{"x":null}`, `{"x":[]}`, `{"x":{}}`, `{"x":1}`, `{"x":false}`,
		`{"a":"","b":"","c":"","d":"","e":"","f":"","g":"","h":"","i":""}`,
	} {
		var got rating.Analysis
		if ratingDecode(`{"summary":`+raw+`}`, &got) == nil {
			t.Fatalf("invalid summary accepted: %s", raw)
		}
	}
	for _, text := range []string{
		`{"summary":{"x":"text"},"unknown":"no"}`,
		`{"summary":{"x":"text"},"kcs":"not an array"}`,
		`{"summary":{"x":"text"}} {"summary":"trailing"}`,
	} {
		var got rating.Analysis
		if ratingDecode(text, &got) == nil {
			t.Fatalf("strict schema changed: %s", text)
		}
	}
	var blind rating.BlindSolution
	if ratingDecode(`{"summary":{"x":"text"}}`, &blind) == nil {
		t.Fatal("analysis summary coercion leaked into blind contract")
	}
}

func TestRatingAnalyzeActivityNormalizesSummaryButKeepsKCGates(t *testing.T) {
	for _, tc := range []struct {
		name, reference string
		wantError       bool
	}{
		{"valid_reference", "kc1", false}, {"unknown_kc", "invented", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newOracleMemoryArtifactStore()
			response := map[string]any{
				"summary":     map[string]string{"problem": "synthetic sum", "conclusion": "candidate comparison", "semantic_review": "needs review"},
				"kcs":         []rating.KC{{ID: "kc1", Name: "addition", Definition: "compute the sum", Conditions: "integer input", RelatedTags: []string{}, Status: "candidate"}},
				"paths":       []rating.Path{{ID: "blind_a", Name: "sum", Kind: "alternative", Summary: "add", Proof: "addition", Complexity: "O(1)", Language: "cpp", Code: "int main(){}", KCIDs: []string{tc.reference}, Bypasses: []string{}, ConstraintScope: "full", SemanticReview: "candidate", Counterexamples: []rating.Counterexample{}}},
				"comparisons": []rating.AnchorComparison{}, "disagreements": []string{}, "limitations": []string{},
			}
			body := string(rating.StableJSON(response))
			model := &capturingAuthoringLLM{response: authoringLLMResponse(body)}
			acts := &Activities{deps: &Dependencies{LLM: model, LLMProvider: "fixture-provider", LLMModel: "fixture-model", LLMBaseURL: "https://fixture.invalid/v1", ProvenanceRecorder: &captureProvenanceRecorder{}}, artifacts: store}
			snapshot := ratingStoreFixture(t, store, RatingSnapshot{Subject: rating.Subject{Title: "Synthetic sum", Statement: "Add two integers.", Hash: "frozen"}})
			blinds := []ArtifactRef{}
			for _, role := range []string{"blind_a", "blind_b"} {
				blinds = append(blinds, ratingStoreFixture(t, store, RatingBlindResult{Solution: rating.BlindSolution{Name: "sum", Summary: "add", Proof: "arithmetic", Complexity: "O(1)", ConstraintScope: "full", Language: "cpp", Code: "int main(){}"}, Model: rating.ModelRun{Role: role}}))
			}
			result, err := acts.RatingAnalyzeActivity(context.Background(), RatingAnalyzeInput{Snapshot: snapshot, Blinds: blinds, Round: 0})
			if tc.wantError {
				if err == nil || !strings.Contains(err.Error(), "unknown KC") {
					t.Fatalf("unknown KC semantic gate bypassed: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var report rating.Report
			if err = acts.ratingRead(context.Background(), *result, &report); err != nil {
				t.Fatal(err)
			}
			if report.Summary != "conclusion: candidate comparison\nproblem: synthetic sum\nsemantic_review: needs review" {
				t.Fatalf("summary changed: %q", report.Summary)
			}
			if len(report.Paths) != 2 || report.Paths[0].Validation != "candidate" || report.Paths[0].HumanObservations != 0 || report.Estimate.Representative != nil {
				t.Fatal("display normalization promoted unverified rating evidence")
			}
			if model.calls != 1 || model.request.MaxTokens != 18000 || model.request.System != rating.AnalysisSystemPrompt || model.request.PromptVersion != rating.ModelPromptVersion || report.RuleVersion != rating.LegacyRuleVersion {
				t.Fatal("prompt identity/scoring-rule boundary changed")
			}
			var modelRef ArtifactRef
			last := report.Evidence[len(report.Evidence)-1]
			if err = json.Unmarshal(last.Details, &modelRef); err != nil {
				t.Fatal(err)
			}
			var envelope recordedLLMResponse
			if err = acts.ratingRead(context.Background(), modelRef, &envelope); err != nil {
				t.Fatal(err)
			}
			if envelope.Response == nil || envelope.Response.Text() != body || envelope.CallReceipt == nil {
				t.Fatal("original structured summary evidence lost")
			}
		})
	}
}

func TestRatingPromptsPinOutputTypesWithoutChangingScoringRule(t *testing.T) {
	if !strings.Contains(rating.AnalysisSystemPrompt, "summary 必须是单个字符串") || !strings.Contains(rating.BlindSystemPrompt, "uncertainties 必须是字符串数组") {
		t.Fatal("model output field types remain ambiguous")
	}
	if rating.ModelPromptVersion == rating.RuleVersion || rating.LegacyRuleVersion != "kc-rating-pilot-v1" || rating.RuleVersion == rating.LegacyRuleVersion {
		t.Fatal("prompt revision must not invalidate stored scoring rules")
	}
}
