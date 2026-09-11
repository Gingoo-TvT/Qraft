package spec

import (
	"encoding/json"
	"math/big"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
)

func TestLintV1ValidAndDeterministic(t *testing.T) {
	input := validLintInput(t)
	first := LintV1(input)
	second := LintV1(input)
	if !first.Passed || len(first.Issues) != 0 {
		t.Fatalf("valid contract rejected: %+v", first)
	}
	if first.SchemaVersion != LintReportSchemaV1 || first.RuleVersion != LintRuleVersionV1 {
		t.Fatalf("unexpected lint identity: %+v", first)
	}
	if first.SemanticSpecSHA256 != input.AuthoringPlan.SemanticSpecSHA256 {
		t.Fatalf("semantic hash=%q want=%q", first.SemanticSpecSHA256, input.AuthoringPlan.SemanticSpecSHA256)
	}
	firstJSON, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	secondJSON, err := json.Marshal(second)
	if err != nil {
		t.Fatal(err)
	}
	if string(firstJSON) != string(secondJSON) {
		t.Fatalf("same input produced different reports:\n%s\n%s", firstJSON, secondJSON)
	}
}

func TestLintV1KnownDefects(t *testing.T) {
	tests := []struct {
		name string
		edit func(*LintInputV1)
		code string
		path string
	}{
		{
			name: "missing output section",
			edit: func(in *LintInputV1) { in.SemanticSpec.Sections.Output = nil },
			code: CodeSpecSectionMissing,
			path: "semantic_spec.sections.output",
		},
		{
			name: "undefined input symbol",
			edit: func(in *LintInputV1) {
				in.SemanticSpec.Sections.Input.SymbolRefs = append(in.SemanticSpec.Sections.Input.SymbolRefs, "m")
			},
			code: CodeSpecSymbolUndefined,
			path: "semantic_spec.sections.input.symbol_refs[2]",
		},
		{
			name: "invalid range",
			edit: func(in *LintInputV1) {
				*in.SemanticSpec.Constraints[0].Min = 5
				*in.SemanticSpec.Constraints[0].Max = 1
			},
			code: CodeSpecRangeInvalid,
			path: "semantic_spec.constraints[0]",
		},
		{
			name: "range type mismatch",
			edit: func(in *LintInputV1) { in.SemanticSpec.Constraints[0].Kind = domain.SemanticConstraintLengthRange },
			code: CodeSpecRangeTypeMismatch,
			path: "semantic_spec.constraints[0].kind",
		},
		{
			name: "unsupported topology",
			edit: func(in *LintInputV1) { in.SemanticSpec.Topology.Kind = "linear+circular" },
			code: CodeSpecTopologyUnsupported,
			path: "semantic_spec.topology.kind",
		},
		{
			name: "zero fact conflicts with range",
			edit: func(in *LintInputV1) { in.SemanticSpec.Boundaries[0].Allowed = true },
			code: CodeSpecBoundaryFactConflict,
			path: "semantic_spec.boundaries[0].allowed",
		},
		{
			name: "one fact missing",
			edit: func(in *LintInputV1) {
				in.SemanticSpec.Boundaries = append([]domain.SemanticBoundaryV1(nil), in.SemanticSpec.Boundaries[:1]...)
				in.SemanticSpec.Boundaries = append(in.SemanticSpec.Boundaries, validLintInput(t).SemanticSpec.Boundaries[2:]...)
				in.SemanticSpec.Boundaries = append(in.SemanticSpec.Boundaries, domain.SemanticBoundaryV1{ID: "a-max", Symbol: "a", Measure: domain.SemanticBoundaryMeasureLength, Value: 200000, Allowed: true, Meaning: "maximum sequence"})
			},
			code: CodeSpecBoundaryFactMissing,
			path: "semantic_spec.boundaries",
		},
		{
			name: "no solution token missing",
			edit: func(in *LintInputV1) {
				in.SemanticSpec.AnswerSemantics.NoSolutionPolicy = domain.SemanticNoSolutionToken
				in.SemanticSpec.AnswerSemantics.NoSolutionToken = ""
			},
			code: CodeSpecNoSolutionSemanticsMissing,
			path: "semantic_spec.answer_semantics.no_solution_token",
		},
		{
			name: "multiple solution semantics missing",
			edit: func(in *LintInputV1) {
				in.SemanticSpec.AnswerSemantics.MultipleSolutionPolicy = domain.SemanticMultipleSolutionAnyValid
				in.SemanticSpec.AnswerSemantics.CustomAcceptanceSemantics = ""
				in.SemanticSpec.Judge = domain.SemanticJudgeV1{Mode: domain.SemanticJudgeSpecialChecker, CheckerReference: "checker.cpp", CheckerRuleVersion: "v1"}
			},
			code: CodeSpecMultipleSolutionSemanticsMissing,
			path: "semantic_spec.answer_semantics.custom_acceptance_semantics",
		},
		{
			name: "authoring spec hash mismatch",
			edit: func(in *LintInputV1) { in.AuthoringPlan.SemanticSpecSHA256 = strings.Repeat("b", 64) },
			code: CodeAuthoringSpecHashMismatch,
			path: "authoring_plan.semantic_spec_sha256",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			input := validLintInput(t)
			tc.edit(&input)
			report := LintV1(input)
			if report.Passed {
				t.Fatalf("known defect passed: %+v", report)
			}
			if !hasIssue(report, tc.code, tc.path) {
				t.Fatalf("missing issue %s at %s: %+v", tc.code, tc.path, report.Issues)
			}
		})
	}
}

func TestLintV1DifficultyMismatchIsAdvisory(t *testing.T) {
	input := validLintInput(t)
	input.AuthoringPlan.TargetDifficulty++
	report := LintV1(input)
	if !report.Passed {
		t.Fatalf("difficulty advisory blocked the contract: %+v", report)
	}
	if !hasIssueWithSeverity(report, CodeAuthoringDifficultyMismatch, "authoring_plan.target_difficulty", LintSeverityAdvisory) {
		t.Fatalf("missing difficulty advisory: %+v", report.Issues)
	}
}

func TestLintV1StructuralClosure(t *testing.T) {
	tests := []struct {
		name string
		edit func(*LintInputV1)
		code string
		path string
	}{
		{
			name: "input integer requires range",
			edit: func(in *LintInputV1) { in.SemanticSpec.Constraints = in.SemanticSpec.Constraints[1:] },
			code: CodeSpecConstraintMissing,
			path: "semantic_spec.symbols[0]",
		},
		{
			name: "relation rejects noninteger arithmetic symbol",
			edit: func(in *LintInputV1) { in.SemanticSpec.Relations[0].Right.Symbol = "a" },
			code: CodeSpecRelationInvalid,
			path: "semantic_spec.relations[0].right.symbol",
		},
		{
			name: "relation range contradiction",
			edit: func(in *LintInputV1) {
				*in.SemanticSpec.Constraints[0].Min = 10
				*in.SemanticSpec.Constraints[1].Max = 5
				in.SemanticSpec.Boundaries[1].Allowed = false
			},
			code: CodeSpecRelationInvalid,
			path: "semantic_spec.relations[0]",
		},
		{
			name: "constant relation contradiction",
			edit: func(in *LintInputV1) {
				one, two := int64(1), int64(2)
				in.SemanticSpec.Relations[0].Left = domain.SemanticExpressionV1{Kind: domain.SemanticExpressionInteger, Value: &one}
				in.SemanticSpec.Relations[0].Right = domain.SemanticExpressionV1{Kind: domain.SemanticExpressionInteger, Value: &two}
			},
			code: CodeSpecRelationInvalid,
			path: "semantic_spec.relations[0]",
		},
		{
			name: "same expression strict relation contradiction",
			edit: func(in *LintInputV1) {
				n := domain.SemanticExpressionV1{Kind: domain.SemanticExpressionSymbol, Symbol: "n"}
				in.SemanticSpec.Relations[0].Left = n
				in.SemanticSpec.Relations[0].Operator = domain.SemanticRelationLessThan
				in.SemanticSpec.Relations[0].Right = n
			},
			code: CodeSpecRelationInvalid,
			path: "semantic_spec.relations[0]",
		},
		{
			name: "constant offset equality contradiction",
			edit: func(in *LintInputV1) {
				one := int64(1)
				n := domain.SemanticExpressionV1{Kind: domain.SemanticExpressionSymbol, Symbol: "n"}
				in.SemanticSpec.Relations[0].Left = n
				in.SemanticSpec.Relations[0].Operator = domain.SemanticRelationEqual
				in.SemanticSpec.Relations[0].Right = domain.SemanticExpressionV1{
					Kind: domain.SemanticExpressionAdd,
					Args: []domain.SemanticExpressionV1{n, {Kind: domain.SemanticExpressionInteger, Value: &one}},
				}
			},
			code: CodeSpecRelationInvalid,
			path: "semantic_spec.relations[0]",
		},
		{
			name: "division denominator range includes zero",
			edit: func(in *LintInputV1) {
				zero := int64(0)
				n := domain.SemanticExpressionV1{Kind: domain.SemanticExpressionSymbol, Symbol: "n"}
				in.SemanticSpec.Relations[0].Left = domain.SemanticExpressionV1{
					Kind: domain.SemanticExpressionFloorDivide,
					Args: []domain.SemanticExpressionV1{n, {Kind: domain.SemanticExpressionSubtract, Args: []domain.SemanticExpressionV1{n, n}}},
				}
				in.SemanticSpec.Relations[0].Right = domain.SemanticExpressionV1{Kind: domain.SemanticExpressionInteger, Value: &zero}
			},
			code: CodeSpecRelationInvalid,
			path: "semantic_spec.relations[0].left.args[1]",
		},
		{
			name: "negative division result cannot drive a repeat",
			edit: func(in *LintInputV1) {
				minusOne := int64(-1)
				in.SemanticSpec.InputGrammar.Lines[1].Repeat = domain.SemanticExpressionV1{
					Kind: domain.SemanticExpressionFloorDivide,
					Args: []domain.SemanticExpressionV1{
						{Kind: domain.SemanticExpressionSymbol, Symbol: "n"},
						{Kind: domain.SemanticExpressionInteger, Value: &minusOne},
					},
				}
			},
			code: CodeSpecInputGrammarInvalid,
			path: "semantic_spec.input_grammar.lines[1].repeat",
		},
		{
			name: "grammar repeat requires a known interval",
			edit: func(in *LintInputV1) {
				in.SemanticSpec.OutputGrammar.Lines[0].Repeat = domain.SemanticExpressionV1{Kind: domain.SemanticExpressionSymbol, Symbol: "answer"}
			},
			code: CodeSpecInputGrammarInvalid,
			path: "semantic_spec.output_grammar.lines[0].repeat",
		},
		{
			name: "floor division denominator requires a known interval",
			edit: func(in *LintInputV1) {
				one := int64(1)
				in.SemanticSpec.OutputGrammar.Lines[0].Repeat = domain.SemanticExpressionV1{
					Kind: domain.SemanticExpressionFloorDivide,
					Args: []domain.SemanticExpressionV1{
						{Kind: domain.SemanticExpressionInteger, Value: &one},
						{Kind: domain.SemanticExpressionSymbol, Symbol: "answer"},
					},
				}
			},
			code: CodeSpecRelationInvalid,
			path: "semantic_spec.output_grammar.lines[0].repeat.args[1]",
		},
		{
			name: "sequence field forbids repeated physical line",
			edit: func(in *LintInputV1) {
				in.SemanticSpec.InputGrammar.Lines[1].Repeat = domain.SemanticExpressionV1{Kind: domain.SemanticExpressionSymbol, Symbol: "n"}
			},
			code: CodeSpecInputGrammarInvalid,
			path: "semantic_spec.input_grammar.lines[1].repeat",
		},
		{
			name: "length range cannot be negative",
			edit: func(in *LintInputV1) { *in.SemanticSpec.Constraints[1].Min = -1 },
			code: CodeSpecRangeInvalid,
			path: "semantic_spec.constraints[1]",
		},
		{
			name: "cardinality range cannot be negative",
			edit: func(in *LintInputV1) {
				in.SemanticSpec.Symbols[1].Type = domain.SemanticSymbolSet
				in.SemanticSpec.Constraints[1].Kind = domain.SemanticConstraintCardinality
				*in.SemanticSpec.Constraints[1].Min = -1
			},
			code: CodeSpecRangeInvalid,
			path: "semantic_spec.constraints[1]",
		},
		{
			name: "linear rejects graph-only direction",
			edit: func(in *LintInputV1) { in.SemanticSpec.Topology.Direction = domain.SemanticTopologyDirected },
			code: CodeSpecTopologyConflict,
			path: "semantic_spec.topology",
		},
		{
			name: "graph count must be integer",
			edit: func(in *LintInputV1) {
				in.SemanticSpec.Topology = domain.SemanticTopologyV1{
					Kind: domain.SemanticTopologyGraph, Direction: domain.SemanticTopologyUndirected,
					CyclePolicy: domain.SemanticTopologyCyclesAllowed, Connectivity: domain.SemanticTopologyDisconnected,
					Dynamics: domain.SemanticTopologyStatic, SelfLoops: domain.SemanticTopologyForbidden,
					MultiEdges: domain.SemanticTopologyForbidden, VertexCountVariable: "n", EdgeCountVariable: "a",
				}
			},
			code: CodeSpecTopologyConflict,
			path: "semantic_spec.topology.edge_count_variable",
		},
		{
			name: "input grammar must cover input symbols",
			edit: func(in *LintInputV1) { in.SemanticSpec.InputGrammar.Lines = in.SemanticSpec.InputGrammar.Lines[:1] },
			code: CodeSpecSymbolUnreferenced,
			path: "semantic_spec.input_grammar.lines",
		},
		{
			name: "output grammar must cover output symbols",
			edit: func(in *LintInputV1) { in.SemanticSpec.OutputGrammar.Lines = nil },
			code: CodeSpecSymbolUnreferenced,
			path: "semantic_spec.output_grammar.lines",
		},
		{
			name: "input integer sequence requires element range",
			edit: func(in *LintInputV1) { in.SemanticSpec.Constraints = in.SemanticSpec.Constraints[:2] },
			code: CodeSpecConstraintMissing,
			path: "semantic_spec.symbols[1]",
		},
		{
			name: "sequence count must stay inside its length range",
			edit: func(in *LintInputV1) { *in.SemanticSpec.Constraints[1].Max = 10 },
			code: CodeSpecInputGrammarInvalid,
			path: "semantic_spec.input_grammar.lines[1].fields[0].count",
		},
		{
			name: "element per repeat count must stay inside its length range",
			edit: func(in *LintInputV1) {
				*in.SemanticSpec.Constraints[1].Max = 10
				in.SemanticSpec.InputGrammar.Lines[1].Repeat = domain.SemanticExpressionV1{Kind: domain.SemanticExpressionSymbol, Symbol: "n"}
				in.SemanticSpec.InputGrammar.Lines[1].Fields[0].Mode = domain.SemanticGrammarFieldElementPerRepeat
				in.SemanticSpec.InputGrammar.Lines[1].Fields[0].Count = nil
			},
			code: CodeSpecInputGrammarInvalid,
			path: "semantic_spec.input_grammar.lines[1].repeat",
		},
		{
			name: "input section cannot self-report derived scope",
			edit: func(in *LintInputV1) { in.SemanticSpec.Symbols[0].Scope = domain.SemanticSymbolScopeDerived },
			code: CodeSpecSymbolInvalid,
			path: "semantic_spec.sections.input.symbol_refs[0]",
		},
		{
			name: "topology subject is required",
			edit: func(in *LintInputV1) { in.SemanticSpec.Topology.Subject = "" },
			code: CodeSpecTopologyConflict,
			path: "semantic_spec.topology.subject",
		},
		{
			name: "objective must bind output",
			edit: func(in *LintInputV1) { in.SemanticSpec.Objective.SymbolRefs = []string{"a"} },
			code: CodeSpecObjectiveInvalid,
			path: "semantic_spec.objective.symbol_refs",
		},
		{
			name: "constraints section must bind structured facts",
			edit: func(in *LintInputV1) { in.SemanticSpec.Sections.Constraints.SymbolRefs = []string{"n"} },
			code: CodeSpecSymbolUnreferenced,
			path: "semantic_spec.sections.constraints.symbol_refs",
		},
		{
			name: "exact judge rejects checker residue",
			edit: func(in *LintInputV1) { in.SemanticSpec.Judge.CheckerRuleVersion = "v1" },
			code: CodeSpecJudgeContractInvalid,
			path: "semantic_spec.judge",
		},
		{
			name: "v1 rejects floating rule residue",
			edit: func(in *LintInputV1) { in.SemanticSpec.AnswerSemantics.FloatingPointErrorRule = "1e-9" },
			code: CodeSpecJudgeContractInvalid,
			path: "semantic_spec.answer_semantics.floating_point_error_rule",
		},
		{
			name: "digest spelling must be canonical lowercase",
			edit: func(in *LintInputV1) {
				upper := strings.Repeat("A", 64)
				in.ExpectedBriefSHA256 = upper
				in.SemanticSpec.BriefSHA256 = upper
				in.AuthoringPlan.BriefSHA256 = upper
				in.AuthoringPlan.SemanticSpecSHA256 = SemanticSpecSHA256V1(in.SemanticSpec)
			},
			code: CodeSpecBriefHashMismatch,
			path: "semantic_spec.brief_sha256",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			input := validLintInput(t)
			tc.edit(&input)
			report := LintV1(input)
			if report.Passed || !hasIssue(report, tc.code, tc.path) {
				t.Fatalf("missing structural issue %s at %s: %+v", tc.code, tc.path, report)
			}
		})
	}
}

func TestLintV1AllowsEarlierFieldReferenceOnSameInputLine(t *testing.T) {
	input := validLintInput(t)
	one := int64(1)
	n := domain.SemanticExpressionV1{Kind: domain.SemanticExpressionSymbol, Symbol: "n"}
	input.SemanticSpec.InputGrammar.Lines = []domain.SemanticGrammarLineV1{{
		ID:     "values",
		Repeat: domain.SemanticExpressionV1{Kind: domain.SemanticExpressionInteger, Value: &one},
		Fields: []domain.SemanticGrammarFieldV1{
			{Symbol: "n", Mode: domain.SemanticGrammarFieldScalar},
			{Symbol: "a", Mode: domain.SemanticGrammarFieldSequence, Count: &n},
		},
		Meaning: "read n followed by exactly n values on one line",
	}}
	input.AuthoringPlan.SemanticSpecSHA256 = SemanticSpecSHA256V1(input.SemanticSpec)

	report := LintV1(input)
	if !report.Passed {
		t.Fatalf("valid same-line earlier-field reference rejected: %+v", report.Issues)
	}
}

func TestGrammarExtentDoesNotTreatGraphEdgeCountAsVertexCardinality(t *testing.T) {
	extent := integerInterval{min: big.NewInt(20), max: big.NewInt(20)}
	ranges := map[string]effectiveRange{
		rangeKey("g", domain.SemanticBoundaryMeasureCardinality): {set: true, min: 1, max: 10},
	}
	collector := &issueCollector{}
	lintGrammarExtentRange(collector, "semantic_spec.input_grammar.lines[0].fields[0].count", "g", domain.SemanticSymbolGraph, extent, ranges)
	if len(collector.issues) != 0 {
		t.Fatalf("graph edge-list extent was conflated with vertex cardinality: %+v", collector.issues)
	}
}

func TestExpressionIntervalPreservesIdenticalSubtractionCorrelation(t *testing.T) {
	one := int64(1)
	n := domain.SemanticExpressionV1{Kind: domain.SemanticExpressionSymbol, Symbol: "n"}
	expression := domain.SemanticExpressionV1{
		Kind: domain.SemanticExpressionAdd,
		Args: []domain.SemanticExpressionV1{
			{Kind: domain.SemanticExpressionSubtract, Args: []domain.SemanticExpressionV1{n, n}},
			{Kind: domain.SemanticExpressionInteger, Value: &one},
		},
	}
	interval, known := expressionInterval(expression,
		map[string]string{"n": domain.SemanticSymbolInteger},
		map[string]effectiveRange{rangeKey("n", domain.SemanticBoundaryMeasureValue): {set: true, min: 1, max: 200000}},
		0,
	)
	if !known || interval.min.Cmp(big.NewInt(1)) != 0 || interval.max.Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("(n-n)+1 interval=%+v known=%v, want [1,1]", interval, known)
	}
}

func TestRestrictedAffineNormalizesAddSubtract(t *testing.T) {
	integer := func(value int64) domain.SemanticExpressionV1 {
		return domain.SemanticExpressionV1{Kind: domain.SemanticExpressionInteger, Value: &value}
	}
	add := func(left, right domain.SemanticExpressionV1) domain.SemanticExpressionV1 {
		return domain.SemanticExpressionV1{Kind: domain.SemanticExpressionAdd, Args: []domain.SemanticExpressionV1{left, right}}
	}
	subtract := func(left, right domain.SemanticExpressionV1) domain.SemanticExpressionV1 {
		return domain.SemanticExpressionV1{Kind: domain.SemanticExpressionSubtract, Args: []domain.SemanticExpressionV1{left, right}}
	}
	n := domain.SemanticExpressionV1{Kind: domain.SemanticExpressionSymbol, Symbol: "n"}
	symbols := map[string]string{"n": domain.SemanticSymbolInteger}
	nAtom := restrictedAffineAtom{kind: domain.SemanticExpressionSymbol, symbol: "n"}

	tests := []struct {
		name        string
		expression  domain.SemanticExpressionV1
		wantConst   string
		wantNCoeff  string
		wantTermLen int
	}{
		{name: "left cancellation", expression: subtract(add(n, integer(1)), n), wantConst: "1", wantTermLen: 0},
		{name: "nested cancellation", expression: add(n, subtract(integer(1), n)), wantConst: "1", wantTermLen: 0},
		{name: "coefficient collection", expression: subtract(add(n, n), n), wantConst: "0", wantNCoeff: "1", wantTermLen: 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			form, known := normalizeRestrictedAffine(tc.expression, symbols, 0)
			if !known || form.constant.String() != tc.wantConst || len(form.terms) != tc.wantTermLen {
				t.Fatalf("form=%+v known=%v, want constant=%s terms=%d", form, known, tc.wantConst, tc.wantTermLen)
			}
			if tc.wantNCoeff != "" {
				coefficient, exists := form.terms[nAtom]
				if !exists || coefficient.String() != tc.wantNCoeff {
					t.Fatalf("n coefficient=%v exists=%v, want %s", coefficient, exists, tc.wantNCoeff)
				}
			}
		})
	}
}

func TestRestrictedAffineKeepsAtomsDistinctAndRejectsNonAffine(t *testing.T) {
	integer := func(value int64) domain.SemanticExpressionV1 {
		return domain.SemanticExpressionV1{Kind: domain.SemanticExpressionInteger, Value: &value}
	}
	add := func(left, right domain.SemanticExpressionV1) domain.SemanticExpressionV1 {
		return domain.SemanticExpressionV1{Kind: domain.SemanticExpressionAdd, Args: []domain.SemanticExpressionV1{left, right}}
	}
	subtract := func(left, right domain.SemanticExpressionV1) domain.SemanticExpressionV1 {
		return domain.SemanticExpressionV1{Kind: domain.SemanticExpressionSubtract, Args: []domain.SemanticExpressionV1{left, right}}
	}
	n := domain.SemanticExpressionV1{Kind: domain.SemanticExpressionSymbol, Symbol: "n"}
	m := domain.SemanticExpressionV1{Kind: domain.SemanticExpressionSymbol, Symbol: "m"}
	lengthA := domain.SemanticExpressionV1{Kind: domain.SemanticExpressionLength, Symbol: "a"}
	symbols := map[string]string{
		"n": domain.SemanticSymbolInteger,
		"m": domain.SemanticSymbolInteger,
		"a": domain.SemanticSymbolIntegerSequence,
	}

	form, known := normalizeRestrictedAffine(subtract(subtract(n, m), lengthA), symbols, 0)
	if !known || len(form.terms) != 3 {
		t.Fatalf("distinct atoms collapsed: form=%+v known=%v", form, known)
	}
	if relationRestrictedAffineConflict(domain.SemanticRelationEqual, add(n, integer(1)), add(m, integer(1)), symbols) {
		t.Fatal("different variables were treated as a common affine base")
	}

	nonAffine := []domain.SemanticExpressionV1{
		{Kind: domain.SemanticExpressionMultiply, Args: []domain.SemanticExpressionV1{n, integer(2)}},
		{Kind: domain.SemanticExpressionFloorDivide, Args: []domain.SemanticExpressionV1{n, integer(2)}},
		{Kind: domain.SemanticExpressionSymbol, Symbol: "unknown"},
		{Kind: domain.SemanticExpressionSymbol, Symbol: "a"},
		{Kind: domain.SemanticExpressionAdd, Args: []domain.SemanticExpressionV1{n}},
	}
	deep := n
	for i := 0; i < 17; i++ {
		deep = add(deep, integer(0))
	}
	nonAffine = append(nonAffine, deep)
	for i, expression := range nonAffine {
		if _, accepted := normalizeRestrictedAffine(expression, symbols, 0); accepted {
			t.Fatalf("non-affine expression %d was accepted: %+v", i, expression)
		}
	}
}

func TestRestrictedAffineIntervalUsesCoefficientSigns(t *testing.T) {
	integer := func(value int64) domain.SemanticExpressionV1 {
		return domain.SemanticExpressionV1{Kind: domain.SemanticExpressionInteger, Value: &value}
	}
	n := domain.SemanticExpressionV1{Kind: domain.SemanticExpressionSymbol, Symbol: "n"}
	// 5-(n+n), with n in [-2,3], has the exact interval [-1,9].
	expression := domain.SemanticExpressionV1{
		Kind: domain.SemanticExpressionSubtract,
		Args: []domain.SemanticExpressionV1{
			integer(5),
			{Kind: domain.SemanticExpressionAdd, Args: []domain.SemanticExpressionV1{n, n}},
		},
	}
	interval, known := expressionInterval(expression,
		map[string]string{"n": domain.SemanticSymbolInteger},
		map[string]effectiveRange{rangeKey("n", domain.SemanticBoundaryMeasureValue): {set: true, min: -2, max: 3}},
		0,
	)
	if !known || interval.min.String() != "-1" || interval.max.String() != "9" {
		t.Fatalf("interval=%+v known=%v, want [-1,9]", interval, known)
	}
}

func TestRelationRestrictedAffineConflictTruthTable(t *testing.T) {
	integer := func(value int64) domain.SemanticExpressionV1 {
		return domain.SemanticExpressionV1{Kind: domain.SemanticExpressionInteger, Value: &value}
	}
	add := func(left, right domain.SemanticExpressionV1) domain.SemanticExpressionV1 {
		return domain.SemanticExpressionV1{Kind: domain.SemanticExpressionAdd, Args: []domain.SemanticExpressionV1{left, right}}
	}
	n := domain.SemanticExpressionV1{Kind: domain.SemanticExpressionSymbol, Symbol: "n"}
	symbols := map[string]string{"n": domain.SemanticSymbolInteger}

	tests := []struct {
		name     string
		operator string
		left     domain.SemanticExpressionV1
		right    domain.SemanticExpressionV1
		conflict bool
	}{
		{name: "equal different offsets", operator: domain.SemanticRelationEqual, left: add(n, integer(1)), right: add(n, integer(2)), conflict: true},
		{name: "less true", operator: domain.SemanticRelationLessThan, left: add(n, integer(1)), right: add(n, integer(2)), conflict: false},
		{name: "less false", operator: domain.SemanticRelationLessThan, left: add(n, integer(2)), right: add(n, integer(1)), conflict: true},
		{name: "less equal same", operator: domain.SemanticRelationLessThanOrEqual, left: n, right: n, conflict: false},
		{name: "greater false", operator: domain.SemanticRelationGreaterThan, left: n, right: n, conflict: true},
		{name: "greater true", operator: domain.SemanticRelationGreaterThan, left: add(n, integer(2)), right: add(n, integer(1)), conflict: false},
		{name: "greater equal false", operator: domain.SemanticRelationGreaterThanOrEqual, left: add(n, integer(1)), right: add(n, integer(2)), conflict: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := relationRestrictedAffineConflict(tc.operator, tc.left, tc.right, symbols); got != tc.conflict {
				t.Fatalf("conflict=%v, want %v", got, tc.conflict)
			}
		})
	}
}

func TestLintV1AllowsCorrelatedKnownNonzeroDenominator(t *testing.T) {
	input := validLintInput(t)
	one := int64(1)
	n := domain.SemanticExpressionV1{Kind: domain.SemanticExpressionSymbol, Symbol: "n"}
	denominator := domain.SemanticExpressionV1{
		Kind: domain.SemanticExpressionSubtract,
		Args: []domain.SemanticExpressionV1{
			{Kind: domain.SemanticExpressionAdd, Args: []domain.SemanticExpressionV1{n, {Kind: domain.SemanticExpressionInteger, Value: &one}}},
			n,
		},
	}
	input.SemanticSpec.Relations[0].Right = domain.SemanticExpressionV1{
		Kind: domain.SemanticExpressionFloorDivide,
		Args: []domain.SemanticExpressionV1{n, denominator},
	}
	input.AuthoringPlan.SemanticSpecSHA256 = SemanticSpecSHA256V1(input.SemanticSpec)

	report := LintV1(input)
	if !report.Passed {
		t.Fatalf("known denominator (n+1)-n was rejected: %+v", report.Issues)
	}
}

func TestLintV1RejectsCommonBaseDifferentOffsetsWithoutRejectingTrueOrder(t *testing.T) {
	integer := func(value int64) domain.SemanticExpressionV1 {
		return domain.SemanticExpressionV1{Kind: domain.SemanticExpressionInteger, Value: &value}
	}
	add := func(left, right domain.SemanticExpressionV1) domain.SemanticExpressionV1 {
		return domain.SemanticExpressionV1{Kind: domain.SemanticExpressionAdd, Args: []domain.SemanticExpressionV1{left, right}}
	}
	n := domain.SemanticExpressionV1{Kind: domain.SemanticExpressionSymbol, Symbol: "n"}

	input := validLintInput(t)
	input.SemanticSpec.Relations[0].Left = add(n, integer(1))
	input.SemanticSpec.Relations[0].Right = add(n, integer(2))
	input.AuthoringPlan.SemanticSpecSHA256 = SemanticSpecSHA256V1(input.SemanticSpec)
	report := LintV1(input)
	if report.Passed || !hasIssue(report, CodeSpecRelationInvalid, "semantic_spec.relations[0]") {
		t.Fatalf("impossible common-base equality passed: %+v", report)
	}

	input.SemanticSpec.Relations[0].Operator = domain.SemanticRelationLessThan
	input.AuthoringPlan.SemanticSpecSHA256 = SemanticSpecSHA256V1(input.SemanticSpec)
	report = LintV1(input)
	if !report.Passed {
		t.Fatalf("true common-base ordering was rejected: %+v", report.Issues)
	}
}

func TestRestrictedAffineNilEmptyArgsAndMinInt64(t *testing.T) {
	symbols := map[string]string{"n": domain.SemanticSymbolInteger}
	nilArgs, nilKnown := normalizeRestrictedAffine(domain.SemanticExpressionV1{Kind: domain.SemanticExpressionSymbol, Symbol: "n"}, symbols, 0)
	emptyArgs, emptyKnown := normalizeRestrictedAffine(domain.SemanticExpressionV1{Kind: domain.SemanticExpressionSymbol, Symbol: "n", Args: []domain.SemanticExpressionV1{}}, symbols, 0)
	if !nilKnown || !emptyKnown || !reflect.DeepEqual(nilArgs, emptyArgs) {
		t.Fatalf("nil/empty args changed affine meaning: nil=%+v empty=%+v", nilArgs, emptyArgs)
	}

	minimum := int64(-1 << 63)
	zero := int64(0)
	expression := domain.SemanticExpressionV1{
		Kind: domain.SemanticExpressionSubtract,
		Args: []domain.SemanticExpressionV1{
			{Kind: domain.SemanticExpressionInteger, Value: &zero},
			{Kind: domain.SemanticExpressionInteger, Value: &minimum},
		},
	}
	form, known := normalizeRestrictedAffine(expression, nil, 0)
	if !known || form.constant.String() != "9223372036854775808" || len(form.terms) != 0 {
		t.Fatalf("MinInt64 negation overflowed: form=%+v known=%v", form, known)
	}
}

func TestTopologyBindingsIgnoreNilVersusEmptyExpressionArgs(t *testing.T) {
	empty := []domain.SemanticExpressionV1{}
	cardinalityBinding := domain.SemanticRelationV1{
		Operator: domain.SemanticRelationEqual,
		Left: domain.SemanticExpressionV1{
			Kind: domain.SemanticExpressionCardinality, Symbol: "g", Args: empty,
		},
		Right: domain.SemanticExpressionV1{
			Kind: domain.SemanticExpressionSymbol, Symbol: "n", Args: empty,
		},
	}
	if !hasCardinalityBinding([]domain.SemanticRelationV1{cardinalityBinding}, "g", "n") {
		t.Fatal("cardinality binding changed meaning for omitted versus empty args")
	}

	one := int64(1)
	treeEdgeBinding := domain.SemanticRelationV1{
		Operator: domain.SemanticRelationEqual,
		Left: domain.SemanticExpressionV1{
			Kind: domain.SemanticExpressionSubtract,
			Args: []domain.SemanticExpressionV1{
				{Kind: domain.SemanticExpressionSymbol, Symbol: "n", Args: empty},
				{Kind: domain.SemanticExpressionInteger, Value: &one, Args: empty},
			},
		},
		Right: domain.SemanticExpressionV1{
			Kind: domain.SemanticExpressionSymbol, Symbol: "m", Args: empty,
		},
	}
	if !hasTreeEdgeBinding([]domain.SemanticRelationV1{treeEdgeBinding}, "n", "m") {
		t.Fatal("tree edge binding changed meaning for omitted versus empty args")
	}
}

func TestLintV1EmptyRangeIntersectionFailsWithoutPanic(t *testing.T) {
	input := validLintInput(t)
	zero := int64(0)
	one := int64(1)
	input.SemanticSpec.Constraints = append(input.SemanticSpec.Constraints, domain.SemanticConstraintV1{
		Subject: "n", Kind: domain.SemanticConstraintIntegerRange,
		Min: &zero, Max: &zero, Meaning: "contradicts the original n range",
	})
	input.SemanticSpec.InputGrammar.Lines[1].Repeat = domain.SemanticExpressionV1{
		Kind: domain.SemanticExpressionFloorDivide,
		Args: []domain.SemanticExpressionV1{
			{Kind: domain.SemanticExpressionInteger, Value: &one},
			{Kind: domain.SemanticExpressionSymbol, Symbol: "n"},
		},
	}
	input.AuthoringPlan.SemanticSpecSHA256 = SemanticSpecSHA256V1(input.SemanticSpec)

	report := LintV1(input)
	if report.Passed || !hasIssue(report, CodeSpecRangeInvalid, "semantic_spec.constraints[3]") {
		t.Fatalf("empty range intersection did not fail closed: %+v", report)
	}
}

func TestLintV1MultiIssueReportIsByteStable(t *testing.T) {
	input := validLintInput(t)
	input.SemanticSpec.SchemaVersion = "unknown"
	input.SemanticSpec.Sections.Input.SymbolRefs = append(input.SemanticSpec.Sections.Input.SymbolRefs, "missing")
	input.SemanticSpec.OutputGrammar.Lines = nil
	input.SemanticSpec.Topology.Subject = ""
	input.AuthoringPlan.TargetDifficulty++

	want, err := json.Marshal(LintV1(input))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		got, err := json.Marshal(LintV1(input))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want) {
			t.Fatalf("iteration %d produced a different report:\n%s\n%s", i, want, got)
		}
	}
}

func TestLintV1SpecialCheckerContract(t *testing.T) {
	input := validLintInput(t)
	input.SemanticSpec.AnswerSemantics.MultipleSolutionPolicy = domain.SemanticMultipleSolutionAnyValid
	input.SemanticSpec.AnswerSemantics.CustomAcceptanceSemantics = "accept any output satisfying the frozen checker predicate"
	input.SemanticSpec.Judge = domain.SemanticJudgeV1{
		Mode:               domain.SemanticJudgeSpecialChecker,
		CheckerReference:   "cas://checker/example",
		CheckerSHA256:      strings.Repeat("b", 64),
		CheckerRuleVersion: "algoforge.special-checker.v1",
	}
	input.AuthoringPlan.SemanticSpecSHA256 = SemanticSpecSHA256V1(input.SemanticSpec)
	report := LintV1(input)
	if !report.Passed {
		t.Fatalf("valid special-checker contract rejected: %+v", report)
	}
}

func TestLintV1DoesNotMutateInput(t *testing.T) {
	input := validLintInput(t)
	before, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	_ = LintV1(input)
	after, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("lint mutated caller input:\n%s\n%s", before, after)
	}
}

func TestLintV1CanonicalizesKnowledgePointOrderAndSortsIssues(t *testing.T) {
	firstInput := validLintInput(t)
	firstInput.RequiredKnowledgePoints = []string{"prefix-sum", "array"}
	firstInput.AuthoringPlan.ConceptRoles = append(firstInput.AuthoringPlan.ConceptRoles,
		domain.AuthoringConceptRoleV1{Slug: "array", Role: "input model", Necessity: "defines the aggregate"},
	)
	secondInput := firstInput
	secondInput.RequiredKnowledgePoints = []string{"array", "prefix-sum"}

	first := LintV1(firstInput)
	second := LintV1(secondInput)
	if first.InputSHA256 != second.InputSHA256 || !reflect.DeepEqual(first, second) {
		t.Fatalf("set order changed deterministic report:\n%+v\n%+v", first, second)
	}

	broken := validLintInput(t)
	broken.SemanticSpec.Sections.Input.SymbolRefs = append(broken.SemanticSpec.Sections.Input.SymbolRefs, "z")
	broken.SemanticSpec.SchemaVersion = "unknown"
	report := LintV1(broken)
	if sort.SliceIsSorted(report.Issues, func(i, j int) bool {
		if report.Issues[i].Path != report.Issues[j].Path {
			return report.Issues[i].Path < report.Issues[j].Path
		}
		if report.Issues[i].Code != report.Issues[j].Code {
			return report.Issues[i].Code < report.Issues[j].Code
		}
		if report.Issues[i].Severity != report.Issues[j].Severity {
			return report.Issues[i].Severity < report.Issues[j].Severity
		}
		return report.Issues[i].Message < report.Issues[j].Message
	}) == false {
		t.Fatalf("issues are not stably sorted: %+v", report.Issues)
	}
}

func validLintInput(t *testing.T) LintInputV1 {
	t.Helper()
	briefSHA := strings.Repeat("a", 64)
	nMin, nMax := int64(1), int64(200000)
	aMin, aMax := int64(1), int64(200000)
	elementMin, elementMax := int64(-1000000000), int64(1000000000)
	one := int64(1)
	aCount := domain.SemanticExpressionV1{Kind: domain.SemanticExpressionSymbol, Symbol: "n"}
	semantic := domain.SemanticSpecV1{
		SchemaVersion:     domain.SemanticSpecSchemaV1,
		BriefSHA256:       briefSHA,
		ProblemDefinition: "Given an integer sequence, output the sum of all elements.",
		Sections: domain.SemanticSpecSectionsV1{
			Input:       &domain.SemanticSectionV1{Summary: "Read n and then n integers.", SymbolRefs: []string{"n", "a"}},
			Output:      &domain.SemanticSectionV1{Summary: "Print the unique integer sum.", SymbolRefs: []string{"answer"}},
			Constraints: &domain.SemanticSectionV1{Summary: "n and the sequence length are bounded.", SymbolRefs: []string{"n", "a"}},
		},
		InputGrammar: domain.SemanticGrammarV1{
			Profile: domain.SemanticGrammarTokenLinesV1,
			Lines: []domain.SemanticGrammarLineV1{
				{ID: "size", Repeat: domain.SemanticExpressionV1{Kind: domain.SemanticExpressionInteger, Value: &one}, Fields: []domain.SemanticGrammarFieldV1{{Symbol: "n", Mode: domain.SemanticGrammarFieldScalar}}, Meaning: "read the sequence length"},
				{ID: "values", Repeat: domain.SemanticExpressionV1{Kind: domain.SemanticExpressionInteger, Value: &one}, Fields: []domain.SemanticGrammarFieldV1{{Symbol: "a", Mode: domain.SemanticGrammarFieldSequence, Count: &aCount}}, Meaning: "read exactly n values"},
			},
		},
		OutputGrammar: domain.SemanticGrammarV1{
			Profile: domain.SemanticGrammarTokenLinesV1,
			Lines: []domain.SemanticGrammarLineV1{
				{ID: "answer", Repeat: domain.SemanticExpressionV1{Kind: domain.SemanticExpressionInteger, Value: &one}, Fields: []domain.SemanticGrammarFieldV1{{Symbol: "answer", Mode: domain.SemanticGrammarFieldScalar}}, Meaning: "print the computed sum"},
			},
		},
		Objective: domain.SemanticObjectiveV1{
			Kind:       domain.SemanticObjectiveCompute,
			Summary:    "Compute the sum of every element in a.",
			SymbolRefs: []string{"a", "answer"},
		},
		Symbols: []domain.SemanticSymbolV1{
			{Name: "n", Type: domain.SemanticSymbolInteger, Scope: domain.SemanticSymbolScopeInput, Role: "sequence length", Definition: "number of input elements", BoundaryPolicy: domain.SemanticBoundaryPolicyZeroOneRequired},
			{Name: "a", Type: domain.SemanticSymbolIntegerSequence, Scope: domain.SemanticSymbolScopeInput, Role: "input values", Definition: "sequence of n integers", BoundaryPolicy: domain.SemanticBoundaryPolicyExplicit},
			{Name: "answer", Type: domain.SemanticSymbolInteger, Scope: domain.SemanticSymbolScopeOutput, Role: "result", Definition: "sum of all input values", BoundaryPolicy: domain.SemanticBoundaryPolicyNotApplicable},
		},
		Constraints: []domain.SemanticConstraintV1{
			{Subject: "n", Kind: domain.SemanticConstraintIntegerRange, Min: &nMin, Max: &nMax, Meaning: "valid sequence length"},
			{Subject: "a", Kind: domain.SemanticConstraintLengthRange, Min: &aMin, Max: &aMax, Meaning: "valid sequence length"},
			{Subject: "a", Kind: domain.SemanticConstraintElementRange, Min: &elementMin, Max: &elementMax, Meaning: "valid value range for every sequence element"},
		},
		Relations: []domain.SemanticRelationV1{
			{
				ID:       "a-length-equals-n",
				Left:     domain.SemanticExpressionV1{Kind: domain.SemanticExpressionLength, Symbol: "a"},
				Operator: domain.SemanticRelationEqual,
				Right:    domain.SemanticExpressionV1{Kind: domain.SemanticExpressionSymbol, Symbol: "n"},
				Meaning:  "the sequence contains exactly n values",
			},
		},
		Topology: domain.SemanticTopologyV1{
			Kind:         domain.SemanticTopologyLinear,
			Subject:      "a",
			Direction:    domain.SemanticTopologyNotApplicable,
			CyclePolicy:  domain.SemanticTopologyAcyclic,
			Connectivity: domain.SemanticTopologyNotApplicable,
			Dynamics:     domain.SemanticTopologyStatic,
			SelfLoops:    domain.SemanticTopologyNotApplicable,
			MultiEdges:   domain.SemanticTopologyNotApplicable,
		},
		Boundaries: []domain.SemanticBoundaryV1{
			{ID: "n-zero", Symbol: "n", Measure: domain.SemanticBoundaryMeasureValue, Value: 0, Allowed: false, Meaning: "empty input is forbidden"},
			{ID: "n-one", Symbol: "n", Measure: domain.SemanticBoundaryMeasureValue, Value: 1, Allowed: true, Meaning: "single element is valid"},
			{ID: "n-max", Symbol: "n", Measure: domain.SemanticBoundaryMeasureValue, Value: 200000, Allowed: true, Meaning: "maximum length"},
			{ID: "a-zero", Symbol: "a", Measure: domain.SemanticBoundaryMeasureLength, Value: 0, Allowed: false, Meaning: "empty sequence is forbidden"},
			{ID: "a-one", Symbol: "a", Measure: domain.SemanticBoundaryMeasureLength, Value: 1, Allowed: true, Meaning: "single-element sequence"},
		},
		AnswerSemantics: domain.SemanticAnswerSemanticsV1{
			NoSolutionPolicy:       domain.SemanticNoSolutionImpossible,
			MultipleSolutionPolicy: domain.SemanticMultipleSolutionUnique,
		},
		Judge: domain.SemanticJudgeV1{
			Mode:              domain.SemanticJudgeExactNormalized,
			ComparisonProfile: domain.SemanticComparisonTrimTrailingSpaceLFV1,
		},
		SampleInputs: []string{"3\n1 2 3\n"},
	}
	authoring := domain.AuthoringPlanV1{
		SchemaVersion:      domain.AuthoringPlanSchemaV1,
		BriefSHA256:        briefSHA,
		SemanticSpecSHA256: SemanticSpecSHA256V1(semantic),
		CoreIdea:           "Accumulate every value exactly once.",
		ConceptRoles: []domain.AuthoringConceptRoleV1{
			{Slug: "prefix-sum", Role: "main algorithm", Necessity: "linear accumulation is required"},
		},
		IntendedSolution:        domain.AuthoringAlgorithmPlanV1{Summary: "single pass sum", TimeComplexity: "O(n)", SpaceComplexity: "O(1)"},
		BruteForceBaseline:      domain.AuthoringAlgorithmPlanV1{Summary: "same direct sum on tiny inputs", TimeComplexity: "O(n)", SpaceComplexity: "O(1)"},
		OracleCandidateStrategy: domain.AuthoringAlgorithmPlanV1{Summary: "checked integer accumulation", TimeComplexity: "O(n)", SpaceComplexity: "O(1)"},
		FailureModes: []domain.AuthoringFailureModeV1{
			{ID: "overflow", Description: "uses a narrow accumulator", WitnessIntent: "large values near the bound"},
		},
		TestIntents: []domain.AuthoringTestIntentV1{
			{Purpose: "minimum", ConstraintRegion: "n=1", BoundaryRefs: []string{"n-one"}},
			{Purpose: "maximum", ConstraintRegion: "n=max", BoundaryRefs: []string{"n-max"}},
		},
		TargetDifficulty:   1200,
		TeachingObjectives: []string{"linear aggregation"},
		CreativeIntent:     "Keep the statement minimal and unambiguous.",
	}
	return LintInputV1{
		SemanticSpec:            semantic,
		AuthoringPlan:           authoring,
		ExpectedBriefSHA256:     briefSHA,
		ExpectedDifficulty:      1200,
		RequiredKnowledgePoints: []string{"prefix-sum"},
	}
}

func hasIssue(report LintReportV1, code, path string) bool {
	for _, issue := range report.Issues {
		if issue.Code == code && issue.Path == path {
			return true
		}
	}
	return false
}

func hasIssueWithSeverity(report LintReportV1, code, path, severity string) bool {
	for _, issue := range report.Issues {
		if issue.Code == code && issue.Path == path && issue.Severity == severity {
			return true
		}
	}
	return false
}
