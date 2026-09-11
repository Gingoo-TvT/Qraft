package activities

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	speccontract "github.com/Gingoo-TvT/Qraft/backend/internal/spec"
)

func TestTestManifestV2IntegerCoverageAndCanonicalBytesAreDeterministic(t *testing.T) {
	specValue, plan, manifest, inputs := validIntegerCoverageFixtureV1(t)
	manifest.BoundaryCoverageReceiptSHA256 = ""
	derivedFirst, err := DeriveIntegerBoundaryCasePlanV1(specValue, plan)
	if err != nil {
		t.Fatal(err)
	}
	derivedSecond, err := DeriveIntegerBoundaryCasePlanV1(specValue, plan)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(derivedFirst, derivedSecond) {
		t.Fatal("derived integer boundary plan is nondeterministic")
	}
	firstPlanJSON, firstPlanSHA, err := CanonicalIntegerBoundaryCasePlanV1(*derivedFirst)
	if err != nil {
		t.Fatal(err)
	}
	secondPlanJSON, secondPlanSHA, err := CanonicalIntegerBoundaryCasePlanV1(*derivedSecond)
	if err != nil || !reflect.DeepEqual(firstPlanJSON, secondPlanJSON) || firstPlanSHA != secondPlanSHA {
		t.Fatal("derived integer boundary plan canonical bytes changed for identical input")
	}
	if got, want := len(derivedFirst.Obligations), 16; got != want {
		t.Fatalf("derived obligations=%d want %d: %+v", got, want, derivedFirst.Obligations)
	}
	assertIntegerBoundaryObligationV1(t, derivedFirst.Obligations, "allowed_boundary:n-zero", IntegerBoundaryObligationBoundary, 0)
	assertIntegerBoundaryObligationV1(t, derivedFirst.Obligations, "range_edge:a:element_range:max", IntegerBoundaryObligationRangeMax, 9)
	regionObligations := 0
	for i, obligation := range derivedFirst.Obligations {
		if i > 0 && derivedFirst.Obligations[i-1].ID >= obligation.ID {
			t.Fatal("derived obligations are not sorted and unique")
		}
		if obligation.Kind == IntegerBoundaryObligationRegion {
			regionObligations++
		}
	}
	if regionObligations != len(plan.TestIntents) {
		t.Fatalf("derived region obligations=%d want %d", regionObligations, len(plan.TestIntents))
	}
	first, err := ValidateIntegerBoundaryCoverageV1(specValue, plan, manifest, inputs)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ValidateIntegerBoundaryCoverageV1(specValue, plan, manifest, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("coverage receipt is nondeterministic:\nfirst=%+v\nsecond=%+v", first, second)
	}
	_, receiptSHA, err := CanonicalIntegerBoundaryCoverageReceiptV1(*first)
	if err != nil {
		t.Fatal(err)
	}
	manifest.BoundaryCoverageReceiptSHA256 = receiptSHA
	firstJSON, firstSHA, err := CanonicalTestManifestV2JSON(manifest)
	if err != nil {
		t.Fatal(err)
	}
	secondJSON, secondSHA, err := CanonicalTestManifestV2JSON(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(firstJSON, secondJSON) || firstSHA != secondSHA {
		t.Fatal("TestManifest v2 canonical bytes changed for identical input")
	}
	parsed, err := ParseTestManifestV2JSON(firstJSON)
	if err != nil || !reflect.DeepEqual(*parsed, manifest) {
		t.Fatalf("canonical TestManifest v2 did not round trip: parsed=%+v err=%v", parsed, err)
	}
	for _, required := range []string{`"killed_wrong_ids":[]`, `"constraint_region":`, `"seed":`, `"input_artifact":`, `"output_artifact":`} {
		if !strings.Contains(string(firstJSON), required) {
			t.Fatalf("canonical manifest omitted required field %s: %s", required, firstJSON)
		}
	}
}

func TestTestManifestV2PreManifestBreaksCoverageReceiptCycle(t *testing.T) {
	specValue, plan, manifest, inputs := validIntegerCoverageFixtureV1(t)
	manifest.BoundaryCoverageReceiptSHA256 = ""
	if err := manifest.Validate(); err == nil {
		t.Fatal("final TestManifest v2 accepted a missing boundary receipt")
	}
	receipt, err := ValidateIntegerBoundaryCoverageV1(specValue, plan, manifest, inputs)
	if err != nil {
		t.Fatalf("pre-manifest could not produce boundary receipt: %v", err)
	}
	_, receiptSHA, err := CanonicalIntegerBoundaryCoverageReceiptV1(*receipt)
	if err != nil {
		t.Fatal(err)
	}
	manifest.BoundaryCoverageReceiptSHA256 = receiptSHA
	if err := manifest.Validate(); err != nil {
		t.Fatalf("final manifest rejected its computed boundary receipt: %v", err)
	}
}

func TestDeriveIntegerBoundaryCasePlanFailsClosed(t *testing.T) {
	tests := map[string]func(*domain.SemanticSpecV1, *domain.AuthoringPlanV1){
		"open range": func(specValue *domain.SemanticSpecV1, _ *domain.AuthoringPlanV1) {
			specValue.Constraints[0].Min = nil
		},
		"unknown intent boundary": func(_ *domain.SemanticSpecV1, plan *domain.AuthoringPlanV1) {
			plan.TestIntents[0].BoundaryRefs = append(plan.TestIntents[0].BoundaryRefs, "missing")
		},
		"forbidden intent boundary": func(specValue *domain.SemanticSpecV1, plan *domain.AuthoringPlanV1) {
			specValue.Boundaries[0].Allowed = false
			plan.TestIntents[2].BoundaryRefs = []string{"a-element-max", "a-length-max", "n-max"}
		},
		"unsupported input type": func(specValue *domain.SemanticSpecV1, _ *domain.AuthoringPlanV1) {
			specValue.Symbols[0].Type = domain.SemanticSymbolString
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			specValue, plan, _, _ := validIntegerCoverageFixtureV1(t)
			mutate(&specValue, &plan)
			plan.SemanticSpecSHA256 = speccontract.SemanticSpecSHA256V1(specValue)
			if _, err := DeriveIntegerBoundaryCasePlanV1(specValue, plan); err == nil {
				t.Fatal("invalid deterministic boundary plan inputs were accepted")
			}
		})
	}
}

func TestIntegerBoundaryCoverageRequiresIntentRefsOnOneRegionCase(t *testing.T) {
	specValue, plan, manifest, inputs := validIntegerCoverageFixtureV1(t)
	plan.TestIntents[1].BoundaryRefs = []string{"a-element-zero", "n-max", "n-one"}
	planSHA, err := canonicalJSONSHA256(plan)
	if err != nil {
		t.Fatal(err)
	}
	manifest.AuthoringPlanSHA256 = planSHA
	manifest.BoundaryCoverageReceiptSHA256 = ""
	if _, err := ValidateIntegerBoundaryCoverageV1(specValue, plan, manifest, inputs); err == nil || !strings.Contains(err.Error(), "single matching case") {
		t.Fatalf("region-local intent coverage was not enforced: %v", err)
	}
}

func TestTestManifestV2RejectsPerCaseContractTampering(t *testing.T) {
	_, _, base, _ := validIntegerCoverageFixtureV1(t)
	tests := map[string]func(*TestManifestV2){
		"unknown purpose":          func(m *TestManifestV2) { m.Cases[0].Purpose = "edge-ish" },
		"missing seed":             func(m *TestManifestV2) { m.Cases[0].Seed = nil },
		"nil boundary refs":        func(m *TestManifestV2) { m.Cases[0].BoundaryRefs = nil },
		"nil killed ids":           func(m *TestManifestV2) { m.Cases[0].KilledWrongIDs = nil },
		"unsorted killed ids":      func(m *TestManifestV2) { m.Cases[0].KilledWrongIDs = []string{"wrong-b", "wrong-a"} },
		"missing retention reason": func(m *TestManifestV2) { m.Cases[0].KilledWrongIDsRetentionReason = "" },
		"output ref tamper":        func(m *TestManifestV2) { m.Cases[0].OutputSHA256 = strings.Repeat("f", 64) },
		"receipt tamper":           func(m *TestManifestV2) { m.SanitizerReceiptSHA256 = "not-a-hash" },
		"case reorder":             func(m *TestManifestV2) { m.Cases[0], m.Cases[1] = m.Cases[1], m.Cases[0] },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			manifest := cloneTestManifestV2(t, base)
			mutate(&manifest)
			if err := manifest.Validate(); err == nil {
				t.Fatal("tampered TestManifest v2 was accepted")
			}
		})
	}
}

func TestParseTestManifestV2RejectsUnknownFieldsAndNonCanonicalBytes(t *testing.T) {
	_, _, manifest, _ := validIntegerCoverageFixtureV1(t)
	canonical, _, err := CanonicalTestManifestV2JSON(manifest)
	if err != nil {
		t.Fatal(err)
	}
	unknown := append([]byte(nil), canonical[:len(canonical)-1]...)
	unknown = append(unknown, []byte(`,"unknown":true}`)...)
	if _, err := ParseTestManifestV2JSON(unknown); err == nil {
		t.Fatal("unknown TestManifest v2 field was accepted")
	}
	noncanonical := append([]byte("\n"), canonical...)
	if _, err := ParseTestManifestV2JSON(noncanonical); err == nil {
		t.Fatal("non-canonical TestManifest v2 bytes were accepted")
	}
}

func TestIntegerBoundaryCoverageFailsClosedOnMissingOrFalseCoverage(t *testing.T) {
	tests := map[string]func(*domain.SemanticSpecV1, *domain.AuthoringPlanV1, *TestManifestV2, *[]IntegerBoundaryCoverageCaseInputV1){
		"missing max input": func(_ *domain.SemanticSpecV1, _ *domain.AuthoringPlanV1, m *TestManifestV2, inputs *[]IntegerBoundaryCoverageCaseInputV1) {
			m.Cases = m.Cases[:2]
			m.TestCount = 2
			*inputs = (*inputs)[:2]
		},
		"false boundary label": func(_ *domain.SemanticSpecV1, _ *domain.AuthoringPlanV1, m *TestManifestV2, _ *[]IntegerBoundaryCoverageCaseInputV1) {
			m.Cases[1].BoundaryRefs = []string{"a-element-max"}
		},
		"resolved bytes tamper": func(_ *domain.SemanticSpecV1, _ *domain.AuthoringPlanV1, _ *TestManifestV2, inputs *[]IntegerBoundaryCoverageCaseInputV1) {
			(*inputs)[0].Input = "0\n"
		},
		"unsupported symbol type": func(specValue *domain.SemanticSpecV1, _ *domain.AuthoringPlanV1, _ *TestManifestV2, _ *[]IntegerBoundaryCoverageCaseInputV1) {
			specValue.Symbols[0].Type = domain.SemanticSymbolString
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			specValue, plan, manifest, inputs := validIntegerCoverageFixtureV1(t)
			mutate(&specValue, &plan, &manifest, &inputs)
			if _, err := ValidateIntegerBoundaryCoverageV1(specValue, plan, manifest, inputs); err == nil {
				t.Fatal("invalid integer boundary coverage was accepted")
			}
		})
	}
}

func validIntegerCoverageFixtureV1(t *testing.T) (domain.SemanticSpecV1, domain.AuthoringPlanV1, TestManifestV2, []IntegerBoundaryCoverageCaseInputV1) {
	t.Helper()
	zero, one, two, nine := int64(0), int64(1), int64(2), int64(9)
	literalOne := domain.SemanticExpressionV1{Kind: domain.SemanticExpressionInteger, Value: &one}
	specValue := domain.SemanticSpecV1{
		SchemaVersion: domain.SemanticSpecSchemaV1, BriefSHA256: strings.Repeat("a", 64), ProblemDefinition: "Echo-compatible integer fixture.",
		InputGrammar: domain.SemanticGrammarV1{Profile: domain.SemanticGrammarTokenLinesV1, Lines: []domain.SemanticGrammarLineV1{
			{ID: "n-line", Repeat: literalOne, Fields: []domain.SemanticGrammarFieldV1{{Symbol: "n", Mode: domain.SemanticGrammarFieldScalar}}, Meaning: "length"},
			{ID: "a-line", Repeat: literalOne, Fields: []domain.SemanticGrammarFieldV1{{Symbol: "a", Mode: domain.SemanticGrammarFieldSequence, Count: &domain.SemanticExpressionV1{Kind: domain.SemanticExpressionSymbol, Symbol: "n"}}}, Meaning: "values"},
		}},
		Symbols: []domain.SemanticSymbolV1{
			{Name: "n", Type: domain.SemanticSymbolInteger, Scope: domain.SemanticSymbolScopeInput, Role: "length", Definition: "sequence length", BoundaryPolicy: domain.SemanticBoundaryPolicyZeroOneRequired},
			{Name: "a", Type: domain.SemanticSymbolIntegerSequence, Scope: domain.SemanticSymbolScopeInput, Role: "values", Definition: "sequence values", BoundaryPolicy: domain.SemanticBoundaryPolicyExplicit},
		},
		Constraints: []domain.SemanticConstraintV1{
			{Subject: "n", Kind: domain.SemanticConstraintIntegerRange, Min: &zero, Max: &two, Meaning: "small length"},
			{Subject: "a", Kind: domain.SemanticConstraintLengthRange, Min: &zero, Max: &two, Meaning: "matching length"},
			{Subject: "a", Kind: domain.SemanticConstraintElementRange, Min: &zero, Max: &nine, Meaning: "small values"},
		},
		Relations: []domain.SemanticRelationV1{{ID: "len-eq-n", Left: domain.SemanticExpressionV1{Kind: domain.SemanticExpressionLength, Symbol: "a"}, Operator: domain.SemanticRelationEqual, Right: domain.SemanticExpressionV1{Kind: domain.SemanticExpressionSymbol, Symbol: "n"}, Meaning: "length equals n"}},
		Boundaries: []domain.SemanticBoundaryV1{
			{ID: "a-element-max", Symbol: "a", Measure: domain.SemanticBoundaryMeasureElementValue, Value: 9, Allowed: true, Meaning: "maximum element"},
			{ID: "a-element-zero", Symbol: "a", Measure: domain.SemanticBoundaryMeasureElementValue, Value: 0, Allowed: true, Meaning: "zero element"},
			{ID: "a-length-max", Symbol: "a", Measure: domain.SemanticBoundaryMeasureLength, Value: 2, Allowed: true, Meaning: "maximum length"},
			{ID: "a-length-zero", Symbol: "a", Measure: domain.SemanticBoundaryMeasureLength, Value: 0, Allowed: true, Meaning: "empty sequence"},
			{ID: "n-max", Symbol: "n", Measure: domain.SemanticBoundaryMeasureValue, Value: 2, Allowed: true, Meaning: "maximum n"},
			{ID: "n-one", Symbol: "n", Measure: domain.SemanticBoundaryMeasureValue, Value: 1, Allowed: true, Meaning: "unit n"},
			{ID: "n-zero", Symbol: "n", Measure: domain.SemanticBoundaryMeasureValue, Value: 0, Allowed: true, Meaning: "zero n"},
		},
	}
	specSHA := speccontract.SemanticSpecSHA256V1(specValue)
	plan := domain.AuthoringPlanV1{
		SchemaVersion: domain.AuthoringPlanSchemaV1, BriefSHA256: specValue.BriefSHA256, SemanticSpecSHA256: specSHA,
		TestIntents: []domain.AuthoringTestIntentV1{
			{Purpose: "minimum values", ConstraintRegion: "region-min", BoundaryRefs: []string{"a-length-zero", "n-zero"}},
			{Purpose: "unit values", ConstraintRegion: "region-mid", BoundaryRefs: []string{"a-element-zero", "n-one"}},
			{Purpose: "maximum values", ConstraintRegion: "region-max", BoundaryRefs: []string{"a-element-max", "a-length-max", "n-max"}},
		},
	}
	planSHA, err := canonicalJSONSHA256(plan)
	if err != nil {
		t.Fatal(err)
	}
	rawInputs := []string{"0\n\n", "1\n0\n", "2\n0 9\n"}
	regions := []string{"region-min", "region-mid", "region-max"}
	purposes := []string{TestManifestPurposeBoundary, TestManifestPurposeTiny, TestManifestPurposeExtreme}
	refs := [][]string{{"a-length-zero", "n-zero"}, {"a-element-zero", "n-one"}, {"a-element-max", "a-length-max", "n-max"}}
	cases := make([]TestManifestCaseV2, 0, len(rawInputs))
	inputs := make([]IntegerBoundaryCoverageCaseInputV1, 0, len(rawInputs))
	for i, raw := range rawInputs {
		id := []string{"case-min", "case-mid", "case-max"}[i]
		// Keep manifest case IDs sorted while retaining the semantic fixtures.
		id = []string{"case-a-min", "case-b-mid", "case-c-max"}[i]
		seed := int64(100 + i)
		inputRef := manifestV2FixtureArtifact(raw, "text/plain")
		outputRef := manifestV2FixtureArtifact(strings.TrimSpace(raw)+"\n", "text/plain")
		cases = append(cases, TestManifestCaseV2{
			TestIndex: i, TestID: id, Purpose: purposes[i], ConstraintRegion: regions[i], BoundaryRefs: refs[i], Seed: &seed,
			InputArtifact: &inputRef, InputSHA256: inputRef.SHA256, OutputArtifact: &outputRef, OutputSHA256: outputRef.SHA256,
			KilledWrongIDs: []string{}, KilledWrongIDsRetentionReason: "no eligible wrong programs supplied to this fixture",
		})
		inputs = append(inputs, IntegerBoundaryCoverageCaseInputV1{TestID: id, Input: raw})
	}
	manifest := TestManifestV2{
		SchemaVersion: TestManifestSchemaVersionV2, SemanticSpecSHA256: specSHA, AuthoringPlanSHA256: planSHA,
		OraclePromotionReceiptSHA256: strings.Repeat("b", 64), SanitizerReceiptSHA256: strings.Repeat("c", 64),
		BoundaryCoverageReceiptSHA256: strings.Repeat("d", 64), TestCount: len(cases), Cases: cases,
	}
	return specValue, plan, manifest, inputs
}

func manifestV2FixtureArtifact(value, contentType string) ArtifactRef {
	digest := sha256Hex([]byte(value))
	return ArtifactRef{
		SchemaVersion: ArtifactRefSchemaVersion, PayloadVersion: ActivityPayloadVersion, Bucket: "fixture", Key: artifactKey(digest),
		SHA256: digest, SizeBytes: int64(len(value)), ContentType: contentType, Producer: "fixture", Provider: "fixture-provider",
		Model: "fixture-model", ModelRevision: "fixture-revision", WorkflowID: "fixture-workflow",
	}
}

func assertIntegerBoundaryObligationV1(t *testing.T, obligations []IntegerBoundaryCaseObligationV1, id, kind string, value int64) {
	t.Helper()
	for _, obligation := range obligations {
		if obligation.ID == id {
			if obligation.Kind != kind || obligation.Value == nil || *obligation.Value != value {
				t.Fatalf("obligation %q=%+v want kind=%q value=%d", id, obligation, kind, value)
			}
			return
		}
	}
	t.Fatalf("derived obligation %q is missing", id)
}

func cloneTestManifestV2(t *testing.T, value TestManifestV2) TestManifestV2 {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var result TestManifestV2
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	return result
}
