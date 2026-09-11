package domain

const SemanticSpecSchemaV1 = "algoforge.semantic-spec.v1"

// SemanticSpecV1 contains only problem facts. It deliberately excludes the
// intended solution, target difficulty, oracle plan, and likely wrong answers
// so an independent verifier can receive this object without authoring hints.
type SemanticSpecV1 struct {
	SchemaVersion     string                    `json:"schema_version"`
	BriefSHA256       string                    `json:"brief_sha256"`
	ProblemDefinition string                    `json:"problem_definition"`
	Sections          SemanticSpecSectionsV1    `json:"sections"`
	InputGrammar      SemanticGrammarV1         `json:"input_grammar"`
	OutputGrammar     SemanticGrammarV1         `json:"output_grammar"`
	Objective         SemanticObjectiveV1       `json:"objective"`
	Symbols           []SemanticSymbolV1        `json:"symbols"`
	Constraints       []SemanticConstraintV1    `json:"constraints"`
	Relations         []SemanticRelationV1      `json:"relations,omitempty"`
	Topology          SemanticTopologyV1        `json:"topology"`
	Boundaries        []SemanticBoundaryV1      `json:"boundaries"`
	AnswerSemantics   SemanticAnswerSemanticsV1 `json:"answer_semantics"`
	Judge             SemanticJudgeV1           `json:"judge"`
	SampleInputs      []string                  `json:"sample_input_candidates,omitempty"`
}

type SemanticSpecSectionsV1 struct {
	Input       *SemanticSectionV1 `json:"input"`
	Output      *SemanticSectionV1 `json:"output"`
	Constraints *SemanticSectionV1 `json:"constraints"`
}

type SemanticSectionV1 struct {
	Summary    string   `json:"summary"`
	SymbolRefs []string `json:"symbol_refs,omitempty"`
}

const SemanticGrammarTokenLinesV1 = "algoforge.io-grammar.token-lines.v1"

const (
	SemanticGrammarFieldScalar           = "scalar"
	SemanticGrammarFieldSequence         = "sequence"
	SemanticGrammarFieldElementPerRepeat = "element_per_repeat"
)

// SemanticGrammarV1 is the authoritative line/token source used by later
// renderers and sample parsers. Prose summaries are not parsed for variables.
type SemanticGrammarV1 struct {
	Profile string                  `json:"profile"`
	Lines   []SemanticGrammarLineV1 `json:"lines"`
}

type SemanticGrammarLineV1 struct {
	ID      string                   `json:"id"`
	Repeat  SemanticExpressionV1     `json:"repeat"`
	Fields  []SemanticGrammarFieldV1 `json:"fields"`
	Meaning string                   `json:"meaning"`
}

type SemanticGrammarFieldV1 struct {
	Symbol string                `json:"symbol"`
	Mode   string                `json:"mode"`
	Count  *SemanticExpressionV1 `json:"count,omitempty"`
}

const (
	SemanticObjectiveCompute   = "compute"
	SemanticObjectiveCount     = "count"
	SemanticObjectiveDecision  = "decision"
	SemanticObjectiveMinimize  = "minimize"
	SemanticObjectiveMaximize  = "maximize"
	SemanticObjectiveConstruct = "construct"
)

type SemanticObjectiveV1 struct {
	Kind       string   `json:"kind"`
	Summary    string   `json:"summary"`
	SymbolRefs []string `json:"symbol_refs,omitempty"`
}

const (
	SemanticSymbolInteger         = "integer"
	SemanticSymbolString          = "string"
	SemanticSymbolBoolean         = "boolean"
	SemanticSymbolIntegerSequence = "integer_sequence"
	SemanticSymbolStringSequence  = "string_sequence"
	SemanticSymbolSet             = "set"
	SemanticSymbolMultiset        = "multiset"
	SemanticSymbolGraph           = "graph"
	SemanticSymbolTree            = "tree"
	SemanticSymbolGrid            = "grid"
)

type SemanticSymbolV1 struct {
	Name           string `json:"name"`
	Type           string `json:"type"`
	Scope          string `json:"scope"`
	Role           string `json:"role"`
	Definition     string `json:"definition"`
	BoundaryPolicy string `json:"boundary_policy"`
}

const (
	SemanticSymbolScopeInput   = "input"
	SemanticSymbolScopeOutput  = "output"
	SemanticSymbolScopeDerived = "derived"

	SemanticBoundaryPolicyZeroOneRequired = "zero_one_required"
	SemanticBoundaryPolicyExplicit        = "explicit"
	SemanticBoundaryPolicyNotApplicable   = "not_applicable"
)

const (
	SemanticConstraintIntegerRange = "integer_range"
	SemanticConstraintLengthRange  = "length_range"
	// SemanticConstraintCardinality is set/member count; for graph/tree
	// symbols it is the vertex count and must bind to topology.vertex_count.
	SemanticConstraintCardinality  = "cardinality"
	SemanticConstraintElementRange = "element_range"
)

type SemanticConstraintV1 struct {
	Subject string `json:"subject"`
	Kind    string `json:"kind"`
	Min     *int64 `json:"min"`
	Max     *int64 `json:"max"`
	Meaning string `json:"meaning"`
}

const (
	SemanticRelationEqual              = "equal"
	SemanticRelationLessThan           = "less_than"
	SemanticRelationLessThanOrEqual    = "less_than_or_equal"
	SemanticRelationGreaterThan        = "greater_than"
	SemanticRelationGreaterThanOrEqual = "greater_than_or_equal"

	SemanticExpressionSymbol      = "symbol"
	SemanticExpressionInteger     = "integer"
	SemanticExpressionLength      = "length"
	SemanticExpressionCardinality = "cardinality"
	SemanticExpressionAdd         = "add"
	SemanticExpressionSubtract    = "subtract"
	SemanticExpressionMultiply    = "multiply"
	SemanticExpressionFloorDivide = "floor_divide"
)

// SemanticRelationV1 makes cross-variable facts such as len(a)=n or
// m<=n*(n-1)/2 machine-readable without parsing prose.
type SemanticRelationV1 struct {
	ID       string               `json:"id"`
	Left     SemanticExpressionV1 `json:"left"`
	Operator string               `json:"operator"`
	Right    SemanticExpressionV1 `json:"right"`
	Meaning  string               `json:"meaning"`
}

type SemanticExpressionV1 struct {
	Kind   string                 `json:"kind"`
	Symbol string                 `json:"symbol,omitempty"`
	Value  *int64                 `json:"value,omitempty"`
	Args   []SemanticExpressionV1 `json:"args,omitempty"`
}

const (
	SemanticTopologyNone     = "none"
	SemanticTopologyLinear   = "linear"
	SemanticTopologyCircular = "circular"
	SemanticTopologyGraph    = "graph"
	SemanticTopologyTree     = "tree"
	SemanticTopologyGrid     = "grid"

	SemanticTopologyNotApplicable = "not_applicable"
	SemanticTopologyDirected      = "directed"
	SemanticTopologyUndirected    = "undirected"
	SemanticTopologyAcyclic       = "acyclic"
	SemanticTopologyCyclic        = "cyclic"
	SemanticTopologyCyclesAllowed = "cycles_allowed"
	SemanticTopologyConnected     = "connected"
	SemanticTopologyDisconnected  = "may_be_disconnected"
	SemanticTopologyStatic        = "static"
	SemanticTopologyDynamic       = "dynamic"
	SemanticTopologyAllowed       = "allowed"
	SemanticTopologyForbidden     = "forbidden"
)

// SemanticTopologyV1 uses one enum per fact so mutually exclusive statements
// cannot be represented by two independent booleans.
type SemanticTopologyV1 struct {
	Kind                string `json:"kind"`
	Subject             string `json:"subject,omitempty"`
	Direction           string `json:"direction"`
	CyclePolicy         string `json:"cycle_policy"`
	Connectivity        string `json:"connectivity"`
	Dynamics            string `json:"dynamics"`
	SelfLoops           string `json:"self_loops"`
	MultiEdges          string `json:"multi_edges"`
	VertexCountVariable string `json:"vertex_count_variable,omitempty"`
	EdgeCountVariable   string `json:"edge_count_variable,omitempty"`
}

type SemanticBoundaryV1 struct {
	ID      string `json:"id"`
	Symbol  string `json:"symbol"`
	Measure string `json:"measure"`
	Value   int64  `json:"value"`
	Allowed bool   `json:"allowed"`
	Meaning string `json:"meaning"`
}

const (
	SemanticBoundaryMeasureValue        = "value"
	SemanticBoundaryMeasureLength       = "length"
	SemanticBoundaryMeasureCardinality  = "cardinality"
	SemanticBoundaryMeasureElementValue = "element_value"
)

const (
	SemanticNoSolutionImpossible  = "impossible"
	SemanticNoSolutionToken       = "token"
	SemanticNoSolutionEmptyOutput = "empty_output"

	SemanticMultipleSolutionUnique    = "unique"
	SemanticMultipleSolutionAnyValid  = "any_valid"
	SemanticMultipleSolutionCanonical = "canonical"
)

type SemanticAnswerSemanticsV1 struct {
	NoSolutionPolicy          string `json:"no_solution_policy"`
	NoSolutionToken           string `json:"no_solution_token,omitempty"`
	MultipleSolutionPolicy    string `json:"multiple_solution_policy"`
	CanonicalizationRule      string `json:"canonicalization_rule,omitempty"`
	FloatingPointErrorRule    string `json:"floating_point_error_rule,omitempty"`
	CustomAcceptanceSemantics string `json:"custom_acceptance_semantics,omitempty"`
}

const (
	SemanticJudgeExactNormalized = "exact_normalized"
	SemanticJudgeSpecialChecker  = "special_checker"

	SemanticComparisonTrimTrailingSpaceLFV1 = "trim-trailing-space-lf-v1"
)

type SemanticJudgeV1 struct {
	Mode               string `json:"mode"`
	ComparisonProfile  string `json:"comparison_profile,omitempty"`
	CheckerReference   string `json:"checker_reference,omitempty"`
	CheckerSHA256      string `json:"checker_sha256,omitempty"`
	CheckerRuleVersion string `json:"checker_rule_version,omitempty"`
}
