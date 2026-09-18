package rating

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestBlindInputExcludesAuthorHints(t *testing.T) {
	s := Subject{Title: "synthetic title", Statement: "Compute f(n)", TimeLimit: 1000, MemoryLimit: 256, TargetDifficulty: 3400, ExpectedTags: []string{"secret_expected_tag"}, OfficialSolution: "secret_official", Metadata: json.RawMessage(`{"private":"secret_metadata"}`), Tests: []TestArtifact{{IsSample: true, Input: "sample", Output: "sample answer"}, {Input: "hidden_test", Output: "hidden_answer"}}}
	b, _ := json.Marshal(BlindInput(s))
	text := string(b)
	for _, secret := range []string{"3400", "secret_expected_tag", "secret_official", "secret_metadata", "hidden_test", "hidden_answer", "target_difficulty", "official_solution"} {
		if strings.Contains(text, secret) {
			t.Fatalf("blind input leaked %s: %s", secret, text)
		}
	}
	if !strings.Contains(text, "sample answer") {
		t.Fatal("public sample missing")
	}
}
func anchorsForTest() []Anchor {
	out := []Anchor{}
	for i, r := range []int{1200, 1600, 2000} {
		out = append(out, Anchor{ID: uuid.New(), Rating: r, SourceURL: "https://example.invalid/" + string(rune('a'+i)), Family: string(rune('a' + i)), SourceConfirmed: true, ReviewedBy: "synthetic-reviewer", ReviewedAt: time.Unix(1, 0)})
	}
	return out
}
func TestReferenceRequiresReviewedBidirectionalDistinctAnchors(t *testing.T) {
	a := anchorsForTest()
	c := []AnchorComparison{{AnchorID: a[0].ID, Relation: "harder"}, {AnchorID: a[1].ID, Relation: "similar"}, {AnchorID: a[2].ID, Relation: "easier"}}
	got := EstimateReference(a, c)
	if got.Status != "provisional" || got.Lower == nil || *got.Lower != 1500 || *got.Upper != 1700 {
		t.Fatalf("%+v", got)
	}
	for _, tc := range []struct {
		name string
		a    []Anchor
		c    []AnchorComparison
	}{
		{"none", nil, c}, {"too_few", a[:2], c}, {"one_sided", a, []AnchorComparison{{AnchorID: a[0].ID, Relation: "harder"}, {AnchorID: a[1].ID, Relation: "harder"}, {AnchorID: a[2].ID, Relation: "harder"}}},
		{"invented", a, []AnchorComparison{{AnchorID: uuid.New(), Relation: "similar"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := EstimateReference(tc.a, tc.c)
			if r.Lower != nil || r.Upper != nil || r.Representative != nil {
				t.Fatalf("invented numeric estimate %+v", r)
			}
		})
	}
	a[1].ReviewedAt = time.Time{}
	if EstimateReference(a, c).Lower != nil {
		t.Fatal("unreviewed anchor counted")
	}
	a = anchorsForTest()
	for i := range a {
		a[i].Family = "same-variant-family"
		c[i].AnchorID = a[i].ID
	}
	if EstimateReference(a, c).Lower != nil {
		t.Fatal("variants counted as independent anchors")
	}
}
func TestConflictingAnchorsNoNumber(t *testing.T) {
	a := anchorsForTest()
	c := []AnchorComparison{{AnchorID: a[0].ID, Relation: "easier"}, {AnchorID: a[1].ID, Relation: "similar"}, {AnchorID: a[2].ID, Relation: "harder"}}
	got := EstimateReference(a, c)
	if got.Status != "conflicting_anchors" || got.Lower != nil {
		t.Fatalf("%+v", got)
	}
}
func TestModelOutputCannotClaimExecutionOrHumanEvidence(t *testing.T) {
	a := Analysis{Paths: []Path{{ID: "shortcut", Name: "shortcut", Kind: "alternative", Code: "int main(){}", ConstraintScope: "full", SemanticReview: "equivalent_dependency", Validation: "tested", HumanObservations: 99, Evidence: []Evidence{{Status: "passed"}}}}}
	if err := NormalizeAnalysis(&a); err != nil {
		t.Fatal(err)
	}
	p := a.Paths[0]
	if p.Validation != "candidate" || len(p.Evidence) != 0 || p.HumanObservations != 0 || p.SemanticReview != "equivalent_dependency" {
		t.Fatalf("%+v", p)
	}
}
func TestDisagreementWithholdsReferenceAndCapsAdditionalRound(t *testing.T) {
	a := anchorsForTest()
	r := Report{Anchors: a, Comparisons: []AnchorComparison{{AnchorID: a[0].ID, Relation: "harder"}, {AnchorID: a[1].ID, Relation: "similar"}, {AnchorID: a[2].ID, Relation: "easier"}}, Paths: []Path{{Validation: "tested", ConstraintScope: "full"}}, Disagreements: []string{"proof incomplete"}}
	NormalizeReport(&r)
	if r.Estimate.Lower != nil || !NeedsAdditionalRound(r) {
		t.Fatalf("%+v", r)
	}
	r.Models = []ModelRun{{Role: "blind_a", Provider: "p", Model: "same"}, {Role: "blind_b", Provider: "p", Model: "same"}}
	NormalizeReport(&r)
	if r.ModelDiversity != "same_configured_model" {
		t.Fatal(r.ModelDiversity)
	}
}

func TestModelAliasDoesNotFabricateIndependentModels(t *testing.T) {
	models := []ModelRun{{Role: "blind_a", Model: "alias-a", Provider: "p", ReturnedModel: "underlying-one"}, {Role: "blind_b", Model: "alias-b", Provider: "q", ReturnedModel: "underlying-one"}}
	if got := ModelDiversity(models); got != "same_returned_model" {
		t.Fatal(got)
	}
	models[1].ReturnedModel = ""
	if got := ModelDiversity(models); got != "unverified_model_diversity" {
		t.Fatal(got)
	}
}

func TestComparisonRequiresReasonAndKnownKCReferences(t *testing.T) {
	for _, a := range []Analysis{
		{Comparisons: []AnchorComparison{{Relation: "similar"}}},
		{Comparisons: []AnchorComparison{{Relation: "arbitrary", Reason: "x"}}},
		{Paths: []Path{{ID: "a", Name: "a", Kind: "alternative", ConstraintScope: "full", KCIDs: []string{"missing"}}}},
	} {
		if err := NormalizeAnalysis(&a); err == nil {
			t.Fatal("unexplained or broken analysis accepted")
		}
	}
}
