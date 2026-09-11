package spec

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
)

func TestParseSampleInputV1CanonicalReceiptAndDeterminism(t *testing.T) {
	spec := validLintInput(t).SemanticSpec
	raw := "  +03  \r\n +01   002 -0 \r\n"
	parsed, err := ParseSampleInputV1(spec, raw)
	if err != nil {
		t.Fatalf("parse valid candidate: %v", err)
	}
	if parsed.ParserRuleVersion != SampleInputParserRuleVersionV1 {
		t.Fatalf("parser rule=%q", parsed.ParserRuleVersion)
	}
	if parsed.InputGrammarSHA256 != InputGrammarSHA256V1(spec.InputGrammar) {
		t.Fatalf("grammar hash=%q", parsed.InputGrammarSHA256)
	}
	if parsed.CanonicalInput != "3\n1 2 0\n" {
		t.Fatalf("canonical=%q", parsed.CanonicalInput)
	}
	if parsed.CanonicalSHA256 != sampleInputSHA256V1([]byte(parsed.CanonicalInput)) {
		t.Fatalf("canonical hash=%q", parsed.CanonicalSHA256)
	}
	if len(parsed.Bindings) != 2 || parsed.Bindings[0].Symbol != "n" || parsed.Bindings[1].Symbol != "a" {
		t.Fatalf("bindings=%+v", parsed.Bindings)
	}
	if parsed.Bindings[0].Kind != SampleInputBindingIntegerV1 || parsed.Bindings[0].Integer == nil || *parsed.Bindings[0].Integer != 3 || parsed.Bindings[0].Integers != nil {
		t.Fatalf("integer binding=%+v", parsed.Bindings[0])
	}
	if parsed.Bindings[1].Kind != SampleInputBindingIntegerSequenceV1 || parsed.Bindings[1].Integer != nil || len(parsed.Bindings[1].Integers) != 3 {
		t.Fatalf("sequence binding=%+v", parsed.Bindings[1])
	}
	bindingJSON, err := json.Marshal(parsed.Bindings)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.BindingSHA256 != sampleInputSHA256V1(bindingJSON) {
		t.Fatalf("binding hash=%q", parsed.BindingSHA256)
	}

	wantJSON, err := json.Marshal(parsed)
	if err != nil {
		t.Fatal(err)
	}
	for iteration := 0; iteration < 50; iteration++ {
		again, err := ParseSampleInputV1(spec, raw)
		if err != nil {
			t.Fatal(err)
		}
		gotJSON, err := json.Marshal(again)
		if err != nil {
			t.Fatal(err)
		}
		if string(gotJSON) != string(wantJSON) {
			t.Fatalf("iteration %d receipt changed:\n%s\n%s", iteration, wantJSON, gotJSON)
		}
	}
}

func TestParseSampleInputV1SameLineEarlierFieldReference(t *testing.T) {
	spec := validLintInput(t).SemanticSpec
	one := int64(1)
	n := sampleInputSymbolExpr("n")
	spec.InputGrammar.Lines = []domain.SemanticGrammarLineV1{{
		ID: "all", Repeat: sampleInputIntegerExpr(one), Meaning: "n then values",
		Fields: []domain.SemanticGrammarFieldV1{
			{Symbol: "n", Mode: domain.SemanticGrammarFieldScalar},
			{Symbol: "a", Mode: domain.SemanticGrammarFieldSequence, Count: &n},
		},
	}}
	parsed, err := ParseSampleInputV1(spec, "3 5 6 7")
	if err != nil {
		t.Fatalf("same-line parse failed: %v", err)
	}
	if parsed.CanonicalInput != "3 5 6 7\n" {
		t.Fatalf("canonical=%q", parsed.CanonicalInput)
	}
}

func TestParseSampleInputV1ElementPerRepeat(t *testing.T) {
	spec := validLintInput(t).SemanticSpec
	spec.InputGrammar.Lines[1].Repeat = sampleInputSymbolExpr("n")
	spec.InputGrammar.Lines[1].Fields[0].Mode = domain.SemanticGrammarFieldElementPerRepeat
	spec.InputGrammar.Lines[1].Fields[0].Count = nil
	parsed, err := ParseSampleInputV1(spec, "3\n10\n20\n30\n")
	if err != nil {
		t.Fatalf("repeated parse failed: %v", err)
	}
	if parsed.CanonicalInput != "3\n10\n20\n30\n" || len(parsed.Bindings[1].Integers) != 3 || parsed.Bindings[1].Integers[2] != 30 {
		t.Fatalf("parsed=%+v", parsed)
	}
}

func TestParseSampleInputV1ZeroLengthSequenceUsesOneEmptyPhysicalLine(t *testing.T) {
	spec := validLintInput(t).SemanticSpec
	zero := int64(0)
	*spec.Constraints[0].Min = zero
	*spec.Constraints[1].Min = zero
	for index := range spec.Boundaries {
		if spec.Boundaries[index].Value == 0 {
			spec.Boundaries[index].Allowed = true
		}
	}
	parsed, err := ParseSampleInputV1(spec, "0\n\n")
	if err != nil {
		t.Fatalf("zero-length sequence failed: %v", err)
	}
	if parsed.CanonicalInput != "0\n\n" || parsed.Bindings[1].Integers == nil || len(parsed.Bindings[1].Integers) != 0 {
		t.Fatalf("zero receipt=%+v", parsed)
	}
	if _, err := ParseSampleInputV1(spec, "0\n"); err == nil {
		t.Fatal("missing zero-token physical line passed")
	}
}

func TestParseSampleInputV1ExactPhysicalLinesAndTokens(t *testing.T) {
	spec := validLintInput(t).SemanticSpec
	invalid := []string{
		"3\n1 2\n",
		"3\n1 2 3 4\n",
		"3\n1 2 3\nextra\n",
		"3 1\n1 2 3\n",
		"3\n1 2 3\n\n",
	}
	for _, raw := range invalid {
		if _, err := ParseSampleInputV1(spec, raw); err == nil {
			t.Fatalf("invalid physical/token shape passed: %q", raw)
		}
	}
}

func TestParseSampleInputV1ConstraintsRelationsAndBoundaries(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		edit func(*domain.SemanticSpecV1)
	}{
		{name: "integer range", raw: "0\n\n"},
		{name: "element range", raw: "1\n1000000001\n"},
		{
			name: "false input relation", raw: "3\n1 2 3\n",
			edit: func(spec *domain.SemanticSpecV1) {
				four := int64(4)
				spec.Relations[0].Right = sampleInputIntegerExpr(four)
			},
		},
		{
			name: "forbidden element boundary", raw: "3\n1 2 3\n",
			edit: func(spec *domain.SemanticSpecV1) {
				spec.Boundaries = append(spec.Boundaries, domain.SemanticBoundaryV1{
					ID: "a-two-forbidden", Symbol: "a", Measure: domain.SemanticBoundaryMeasureElementValue,
					Value: 2, Allowed: false, Meaning: "two is forbidden for this contract",
				})
			},
		},
		{
			name: "missing sequence constraint", raw: "3\n1 2 3\n",
			edit: func(spec *domain.SemanticSpecV1) { spec.Constraints = spec.Constraints[:2] },
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			spec := validLintInput(t).SemanticSpec
			if tc.edit != nil {
				tc.edit(&spec)
			}
			if _, err := ParseSampleInputV1(spec, tc.raw); err == nil {
				t.Fatal("invalid candidate passed")
			}
		})
	}
}

func TestParseSampleInputV1FloorDivideUsesMathematicalFloor(t *testing.T) {
	spec := validLintInput(t).SemanticSpec
	zero, two, minusTwo := int64(0), int64(2), int64(-2)
	negativeN := domain.SemanticExpressionV1{
		Kind: domain.SemanticExpressionSubtract,
		Args: []domain.SemanticExpressionV1{sampleInputIntegerExpr(zero), sampleInputSymbolExpr("n")},
	}
	spec.Relations[0] = domain.SemanticRelationV1{
		ID: "negative-floor",
		Left: domain.SemanticExpressionV1{
			Kind: domain.SemanticExpressionFloorDivide,
			Args: []domain.SemanticExpressionV1{negativeN, sampleInputIntegerExpr(two)},
		},
		Operator: domain.SemanticRelationEqual,
		Right:    sampleInputIntegerExpr(minusTwo),
		Meaning:  "floor(-3/2) is -2",
	}
	if _, err := ParseSampleInputV1(spec, "3\n1 2 3\n"); err != nil {
		t.Fatalf("mathematical floor relation failed: %v", err)
	}
}

func TestParseSampleInputV1SkipsRelationsThatNeedNonInputSymbols(t *testing.T) {
	spec := validLintInput(t).SemanticSpec
	spec.Relations = append(spec.Relations, domain.SemanticRelationV1{
		ID:       "output-not-yet-bound",
		Left:     sampleInputSymbolExpr("n"),
		Operator: domain.SemanticRelationEqual,
		Right:    sampleInputSymbolExpr("answer"),
		Meaning:  "not an input-only relation",
	})
	if _, err := ParseSampleInputV1(spec, "3\n1 2 3\n"); err != nil {
		t.Fatalf("non-input relation should be deferred: %v", err)
	}
}

func TestParseSampleInputV1FailsClosedOnUnsupportedOrMalformedContracts(t *testing.T) {
	tests := []struct {
		name string
		edit func(*domain.SemanticSpecV1)
	}{
		{
			name: "unsupported input symbol",
			edit: func(spec *domain.SemanticSpecV1) { spec.Symbols[0].Type = domain.SemanticSymbolString },
		},
		{
			name: "unsupported cardinality expression",
			edit: func(spec *domain.SemanticSpecV1) {
				cardinality := domain.SemanticExpressionV1{Kind: domain.SemanticExpressionCardinality, Symbol: "a"}
				spec.Relations[0].Left = cardinality
			},
		},
		{
			name: "division by zero",
			edit: func(spec *domain.SemanticSpecV1) {
				zero := int64(0)
				spec.Relations[0].Left = domain.SemanticExpressionV1{
					Kind: domain.SemanticExpressionFloorDivide,
					Args: []domain.SemanticExpressionV1{sampleInputSymbolExpr("n"), sampleInputIntegerExpr(zero)},
				}
			},
		},
		{
			name: "sequence field repeated",
			edit: func(spec *domain.SemanticSpecV1) { spec.InputGrammar.Lines[1].Repeat = sampleInputSymbolExpr("n") },
		},
		{
			name: "duplicate field symbol",
			edit: func(spec *domain.SemanticSpecV1) {
				spec.InputGrammar.Lines[1].Fields[0].Symbol = "n"
				spec.InputGrammar.Lines[1].Fields[0].Mode = domain.SemanticGrammarFieldScalar
				spec.InputGrammar.Lines[1].Fields[0].Count = nil
			},
		},
		{
			name: "allowed boundary with unsupported measure",
			edit: func(spec *domain.SemanticSpecV1) {
				spec.Boundaries = append(spec.Boundaries, domain.SemanticBoundaryV1{
					ID: "n-bad-measure", Symbol: "n", Measure: domain.SemanticBoundaryMeasureLength,
					Value: 3, Allowed: true, Meaning: "invalid even though allowed",
				})
			},
		},
		{
			name: "negative length constraint",
			edit: func(spec *domain.SemanticSpecV1) {
				minusOne := int64(-1)
				spec.Constraints[1].Min = &minusOne
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			spec := validLintInput(t).SemanticSpec
			tc.edit(&spec)
			if _, err := ParseSampleInputV1(spec, "3\n1 2 3\n"); err == nil {
				t.Fatal("unsupported or malformed contract passed")
			}
		})
	}
}

func TestParseSampleInputV1RejectsUnsafeTextAndResourceOverflow(t *testing.T) {
	spec := validLintInput(t).SemanticSpec
	invalid := []string{
		"3\n1\t2 3\n",
		"3\r1 2 3\n",
		"\ufeff3\n1 2 3\n",
		"3\n1\u00a02 3\n",
		"3\n1 2 \u0663\n",
		"9223372036854775808\n",
		strings.Repeat("0", maxSampleInputBytesV1+1),
	}
	for _, raw := range invalid {
		if _, err := ParseSampleInputV1(spec, raw); err == nil {
			t.Fatalf("unsafe candidate passed (len=%d)", len(raw))
		}
	}
}

func TestInputGrammarSHA256V1BindsExactGrammar(t *testing.T) {
	spec := validLintInput(t).SemanticSpec
	first := InputGrammarSHA256V1(spec.InputGrammar)
	spec.InputGrammar.Lines[0].Meaning += " changed"
	second := InputGrammarSHA256V1(spec.InputGrammar)
	if first == second || len(first) != 64 || len(second) != 64 {
		t.Fatalf("grammar hashes first=%q second=%q", first, second)
	}
}

func sampleInputIntegerExpr(value int64) domain.SemanticExpressionV1 {
	return domain.SemanticExpressionV1{Kind: domain.SemanticExpressionInteger, Value: &value}
}

func sampleInputSymbolExpr(symbol string) domain.SemanticExpressionV1 {
	return domain.SemanticExpressionV1{Kind: domain.SemanticExpressionSymbol, Symbol: symbol}
}
