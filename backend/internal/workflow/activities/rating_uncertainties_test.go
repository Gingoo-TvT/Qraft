package activities

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/rating"
)

func TestRatingBlindUncertaintiesNormalizesOnlyStringRepresentation(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		want      []string
	}{
		{"single", `" boundary case unproven "`, []string{" boundary case unproven "}},
		{"empty", `""`, []string{}},
		{"whitespace", `" \n\t"`, []string{}},
		{"array", `["first","second"]`, []string{"first", "second"}},
		{"empty_array", `[]`, []string{}},
		// The preexisting []string decoder accepts null as an absent list.
		{"existing_null_contract", `null`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got rating.BlindSolution
			if err := ratingDecode(`{"uncertainties":`+tc.raw+`}`, &got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.Uncertainties, tc.want) {
				t.Fatalf("got=%#v want=%#v", got.Uncertainties, tc.want)
			}
		})
	}
}

func TestRatingBlindUncertaintiesKeepsStrictSchemaAndValidation(t *testing.T) {
	for _, text := range []string{
		`{"uncertainties":123}`, `{"uncertainties":false}`, `{"uncertainties":{}}`, `{"uncertainties":["a",5]}`,
		`{"uncertainties":"one","unknown_field":"x"}`, `{"uncertainties":"one","proof":[]}`,
		`{"uncertainties":"one"} {"uncertainties":[]}`,
	} {
		var got rating.BlindSolution
		if ratingDecode(text, &got) == nil {
			t.Fatalf("invalid schema accepted: %s", text)
		}
	}
	var other rating.Analysis
	if ratingDecode(`{"uncertainties":"one"}`, &other) == nil {
		t.Fatal("blind-only normalization leaked into analysis schema")
	}
	var empty rating.BlindSolution
	if err := ratingDecode(`{"uncertainties":""}`, &empty); err != nil {
		t.Fatal(err)
	}
	if rating.ValidateBlind(empty) == nil {
		t.Fatal("representation normalization bypassed blind-solution gate")
	}
}

func TestRatingBlindActivityRetainsScalarUncertaintyAndOriginalReceipt(t *testing.T) {
	const body = `{"name":"synthetic sum","summary":"add the values","proof":"addition computes the required sum","complexity":"O(n)","constraint_scope":"full","language":"cpp","code":"int main(){return 0;}","uncertainties":"Synthetic remaining boundary check."}`
	store := newOracleMemoryArtifactStore()
	provider := &capturingAuthoringLLM{response: authoringLLMResponse(body)}
	acts := &Activities{deps: &Dependencies{LLM: provider, LLMProvider: "fixture-provider", LLMModel: "fixture-model", LLMBaseURL: "https://fixture.invalid/v1", ProvenanceRecorder: &captureProvenanceRecorder{}}, artifacts: store}
	snapshot := ratingStoreFixture(t, store, RatingSnapshot{Subject: rating.Subject{Title: "Synthetic sum", Statement: "Add the input integers."}})
	result, err := acts.RatingBlindSolveActivity(context.Background(), RatingBlindInput{Snapshot: snapshot, Role: "blind_b"})
	if err != nil {
		t.Fatal(err)
	}
	var got RatingBlindResult
	if err = acts.ratingRead(context.Background(), *result, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Solution.Uncertainties, []string{"Synthetic remaining boundary check."}) {
		t.Fatalf("uncertainty was changed: %+v", got.Solution)
	}
	if provider.calls != 1 || provider.request.MaxTokens != 18000 || provider.request.System != rating.BlindSystemPrompt {
		t.Fatal("normalization changed model calls, budget, or prompt")
	}
	var receiptRef ArtifactRef
	if err = json.Unmarshal(got.Evidence.Details, &receiptRef); err != nil {
		t.Fatal(err)
	}
	var envelope recordedLLMResponse
	if err = acts.ratingRead(context.Background(), receiptRef, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Response == nil || envelope.Response.Text() != body || envelope.CallReceipt == nil {
		t.Fatal("original provider response/receipt was not preserved")
	}
}
