// Package spec implements deterministic validation for the versioned problem
// fact and authoring contracts. It does not infer semantics from Markdown.
package spec

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"regexp"
	"sort"
	"strings"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
)

const (
	LintReportSchemaV1 = "algoforge.spec-lint-report.v1"
	LintRuleVersionV1  = "algoforge.spec-lint.rules.v1"
)

const (
	CodeSpecSchemaUnsupported                = "spec_schema_unsupported"
	CodeSpecBriefHashMismatch                = "spec_brief_hash_mismatch"
	CodeSpecSectionMissing                   = "spec_section_missing"
	CodeSpecInputGrammarInvalid              = "spec_input_grammar_invalid"
	CodeSpecObjectiveInvalid                 = "spec_objective_invalid"
	CodeSpecSymbolInvalid                    = "spec_symbol_invalid"
	CodeSpecSymbolDuplicate                  = "spec_symbol_duplicate"
	CodeSpecSymbolUndefined                  = "spec_symbol_undefined"
	CodeSpecSymbolUnreferenced               = "spec_symbol_unreferenced"
	CodeSpecRangeInvalid                     = "spec_range_invalid"
	CodeSpecConstraintMissing                = "spec_constraint_missing"
	CodeSpecRangeTypeMismatch                = "spec_range_type_mismatch"
	CodeSpecRelationInvalid                  = "spec_relation_invalid"
	CodeSpecTopologyUnsupported              = "spec_topology_unsupported"
	CodeSpecTopologyConflict                 = "spec_topology_conflict"
	CodeSpecBoundaryFactMissing              = "spec_boundary_fact_missing"
	CodeSpecBoundaryFactConflict             = "spec_boundary_fact_conflict"
	CodeSpecNoSolutionSemanticsMissing       = "spec_no_solution_semantics_missing"
	CodeSpecMultipleSolutionSemanticsMissing = "spec_multiple_solution_semantics_missing"
	CodeSpecJudgeContractInvalid             = "spec_judge_contract_invalid"
	CodeAuthoringSchemaUnsupported           = "authoring_schema_unsupported"
	CodeAuthoringBriefHashMismatch           = "authoring_brief_hash_mismatch"
	CodeAuthoringSpecHashMismatch            = "authoring_spec_hash_mismatch"
	CodeAuthoringRequiredFieldMissing        = "authoring_required_field_missing"
	CodeAuthoringDifficultyMismatch          = "authoring_difficulty_mismatch"
	CodeAuthoringKnowledgePointMissing       = "authoring_knowledge_point_missing"
)

const (
	LintSeverityError    = "error"
	LintSeverityAdvisory = "advisory"
)

type LintInputV1 struct {
	SemanticSpec            domain.SemanticSpecV1  `json:"semantic_spec"`
	AuthoringPlan           domain.AuthoringPlanV1 `json:"authoring_plan"`
	ExpectedBriefSHA256     string                 `json:"expected_brief_sha256"`
	ExpectedDifficulty      int                    `json:"expected_difficulty"`
	RequiredKnowledgePoints []string               `json:"required_knowledge_points,omitempty"`
}

type LintIssueV1 struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Path     string `json:"path"`
	Message  string `json:"message"`
}

type LintReportV1 struct {
	SchemaVersion      string        `json:"schema_version"`
	RuleVersion        string        `json:"rule_version"`
	InputSHA256        string        `json:"input_sha256"`
	SemanticSpecSHA256 string        `json:"semantic_spec_sha256"`
	Passed             bool          `json:"passed"`
	Issues             []LintIssueV1 `json:"issues"`
}

type issueCollector struct {
	issues []LintIssueV1
}

func (c *issueCollector) add(code, path, message string) {
	c.issues = append(c.issues, LintIssueV1{Severity: LintSeverityError, Code: code, Path: path, Message: message})
}

func (c *issueCollector) addAdvisory(code, path, message string) {
	c.issues = append(c.issues, LintIssueV1{Severity: LintSeverityAdvisory, Code: code, Path: path, Message: message})
}

var symbolNamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)

// LintV1 returns a stable report for the same structured input. Messages are
// explanatory; code and path are the machine contract.
func LintV1(input LintInputV1) LintReportV1 {
	normalized := input
	requiredSet := make(map[string]struct{}, len(input.RequiredKnowledgePoints))
	for _, value := range input.RequiredKnowledgePoints {
		requiredSet[strings.TrimSpace(value)] = struct{}{}
	}
	normalized.RequiredKnowledgePoints = make([]string, 0, len(requiredSet))
	for value := range requiredSet {
		normalized.RequiredKnowledgePoints = append(normalized.RequiredKnowledgePoints, value)
	}
	sort.Strings(normalized.RequiredKnowledgePoints)

	collector := &issueCollector{}
	specSHA := semanticSpecSHA256(normalized.SemanticSpec)
	lintSemanticSpec(collector, normalized)
	lintAuthoringPlan(collector, normalized, specSHA)

	sort.SliceStable(collector.issues, func(i, j int) bool {
		if collector.issues[i].Path != collector.issues[j].Path {
			return collector.issues[i].Path < collector.issues[j].Path
		}
		if collector.issues[i].Code != collector.issues[j].Code {
			return collector.issues[i].Code < collector.issues[j].Code
		}
		if collector.issues[i].Severity != collector.issues[j].Severity {
			return collector.issues[i].Severity < collector.issues[j].Severity
		}
		return collector.issues[i].Message < collector.issues[j].Message
	})
	if collector.issues == nil {
		collector.issues = []LintIssueV1{}
	}

	passed := true
	for _, issue := range collector.issues {
		if issue.Severity == LintSeverityError {
			passed = false
			break
		}
	}

	return LintReportV1{
		SchemaVersion:      LintReportSchemaV1,
		RuleVersion:        LintRuleVersionV1,
		InputSHA256:        hashJSON(normalized),
		SemanticSpecSHA256: specSHA,
		Passed:             passed,
		Issues:             collector.issues,
	}
}

// SemanticSpecSHA256V1 returns the canonical JSON hash used to bind an
// AuthoringPlanV1 to exactly one SemanticSpecV1.
func SemanticSpecSHA256V1(value domain.SemanticSpecV1) string {
	return semanticSpecSHA256(value)
}

func semanticSpecSHA256(value domain.SemanticSpecV1) string {
	return hashJSON(value)
}

func hashJSON(value interface{}) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(fmt.Sprintf("marshal deterministic spec contract: %v", err))
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func lintSemanticSpec(c *issueCollector, input LintInputV1) {
	value := input.SemanticSpec
	if value.SchemaVersion != domain.SemanticSpecSchemaV1 {
		c.add(CodeSpecSchemaUnsupported, "semantic_spec.schema_version", "unsupported SemanticSpec schema version")
	}
	if !isSHA256(value.BriefSHA256) || value.BriefSHA256 != input.ExpectedBriefSHA256 {
		c.add(CodeSpecBriefHashMismatch, "semantic_spec.brief_sha256", "SemanticSpec is not bound to the expected canonical brief")
	}
	if strings.TrimSpace(value.ProblemDefinition) == "" {
		c.add(CodeSpecSectionMissing, "semantic_spec.problem_definition", "problem definition is required")
	}

	lintSection(c, "input", value.Sections.Input)
	lintSection(c, "output", value.Sections.Output)
	lintSection(c, "constraints", value.Sections.Constraints)

	symbolTypes := make(map[string]string, len(value.Symbols))
	symbolScopes := make(map[string]string, len(value.Symbols))
	boundaryPolicies := make(map[string]string, len(value.Symbols))
	for i, symbol := range value.Symbols {
		path := fmt.Sprintf("semantic_spec.symbols[%d]", i)
		name := strings.TrimSpace(symbol.Name)
		if name != symbol.Name || !symbolNamePattern.MatchString(name) {
			c.add(CodeSpecSymbolInvalid, path+".name", "symbol name must be a stable ASCII identifier")
		}
		if _, exists := symbolTypes[name]; exists && name != "" {
			c.add(CodeSpecSymbolDuplicate, path+".name", "symbol name is duplicated")
		} else if name != "" {
			symbolTypes[name] = symbol.Type
			symbolScopes[name] = symbol.Scope
			boundaryPolicies[name] = symbol.BoundaryPolicy
		}
		if !supportedSymbolType(symbol.Type) {
			c.add(CodeSpecSymbolInvalid, path+".type", "unsupported symbol type")
		}
		if strings.TrimSpace(symbol.Role) == "" {
			c.add(CodeSpecSymbolInvalid, path+".role", "symbol role is required")
		}
		if strings.TrimSpace(symbol.Definition) == "" {
			c.add(CodeSpecSymbolInvalid, path+".definition", "symbol definition is required")
		}
		if !oneOf(symbol.Scope, domain.SemanticSymbolScopeInput, domain.SemanticSymbolScopeOutput, domain.SemanticSymbolScopeDerived) {
			c.add(CodeSpecSymbolInvalid, path+".scope", "unsupported symbol scope")
		}
		if !oneOf(symbol.BoundaryPolicy, domain.SemanticBoundaryPolicyZeroOneRequired, domain.SemanticBoundaryPolicyExplicit, domain.SemanticBoundaryPolicyNotApplicable) {
			c.add(CodeSpecSymbolInvalid, path+".boundary_policy", "unsupported boundary policy")
		}
		if symbol.BoundaryPolicy == domain.SemanticBoundaryPolicyZeroOneRequired && symbol.Type != domain.SemanticSymbolInteger {
			c.add(CodeSpecSymbolInvalid, path+".boundary_policy", "zero_one_required is valid only for integer symbols")
		}
	}
	if len(value.Symbols) == 0 {
		c.add(CodeSpecSectionMissing, "semantic_spec.symbols", "at least one symbol is required")
	}

	lintSectionRefs(c, "input", value.Sections.Input, symbolTypes, symbolScopes)
	lintSectionRefs(c, "output", value.Sections.Output, symbolTypes, symbolScopes)
	lintSectionRefs(c, "constraints", value.Sections.Constraints, symbolTypes, symbolScopes)
	lintObjective(c, value.Objective, symbolTypes, symbolScopes)
	lintSymbolCoverage(c, value, symbolScopes)

	ranges := lintConstraints(c, value.Constraints, symbolTypes)
	lintRequiredConstraints(c, value.Symbols, ranges)
	lintGrammar(c, "input", value.InputGrammar, domain.SemanticSymbolScopeInput, symbolTypes, symbolScopes, ranges)
	lintGrammar(c, "output", value.OutputGrammar, domain.SemanticSymbolScopeOutput, symbolTypes, symbolScopes, ranges)
	lintRelations(c, value.Relations, symbolTypes, ranges)
	lintConstraintSectionCoverage(c, value.Sections.Constraints, value.Constraints, value.Relations)
	lintTopology(c, value.Topology, symbolTypes, symbolScopes, boundaryPolicies, value.Relations)
	lintBoundaries(c, value.Boundaries, symbolTypes, boundaryPolicies, ranges)
	lintAnswerSemantics(c, value.AnswerSemantics, value.Judge)

}

func lintSection(c *issueCollector, name string, section *domain.SemanticSectionV1) {
	path := "semantic_spec.sections." + name
	if section == nil {
		c.add(CodeSpecSectionMissing, path, name+" section is required")
		return
	}
	if strings.TrimSpace(section.Summary) == "" {
		c.add(CodeSpecSectionMissing, path+".summary", name+" section summary is required")
	}
}

func lintSectionRefs(c *issueCollector, name string, section *domain.SemanticSectionV1, symbols, scopes map[string]string) {
	if section == nil {
		return
	}
	for i, ref := range section.SymbolRefs {
		if _, exists := symbols[ref]; !exists {
			c.add(CodeSpecSymbolUndefined, fmt.Sprintf("semantic_spec.sections.%s.symbol_refs[%d]", name, i), "section references an undefined symbol")
		} else if name == "input" && scopes[ref] != domain.SemanticSymbolScopeInput {
			c.add(CodeSpecSymbolInvalid, fmt.Sprintf("semantic_spec.sections.%s.symbol_refs[%d]", name, i), "input section may reference only input-scoped symbols")
		}
	}
}

func lintGrammar(c *issueCollector, name string, grammar domain.SemanticGrammarV1, fieldScope string, symbols, scopes map[string]string, ranges map[string]effectiveRange) {
	base := "semantic_spec." + name + "_grammar"
	if grammar.Profile != domain.SemanticGrammarTokenLinesV1 {
		c.add(CodeSpecInputGrammarInvalid, base+".profile", "unsupported token-line grammar profile")
	}
	if len(grammar.Lines) == 0 {
		c.add(CodeSpecInputGrammarInvalid, base+".lines", name+" grammar requires at least one line")
	}
	lineIDs := make(map[string]struct{}, len(grammar.Lines))
	fieldSymbols := make(map[string]struct{})
	availableInputSymbols := make(map[string]struct{})
	for i, line := range grammar.Lines {
		path := fmt.Sprintf("%s.lines[%d]", base, i)
		if strings.TrimSpace(line.ID) == "" || line.ID != strings.TrimSpace(line.ID) {
			c.add(CodeSpecInputGrammarInvalid, path+".id", "grammar line id is required and must be trimmed")
		} else if _, exists := lineIDs[line.ID]; exists {
			c.add(CodeSpecInputGrammarInvalid, path+".id", "grammar line id is duplicated")
		} else {
			lineIDs[line.ID] = struct{}{}
		}
		lintIntegerExpression(c, path+".repeat", line.Repeat, symbols, ranges, 0)
		lintGrammarExpressionRefs(c, path+".repeat", line.Repeat, scopes, availableInputSymbols, name == "input")
		repeatRange, repeatKnown := expressionInterval(line.Repeat, symbols, ranges, 0)
		if !repeatKnown {
			c.add(CodeSpecInputGrammarInvalid, path+".repeat", "grammar line repeat requires a known bounded interval")
		} else if repeatRange.min.Sign() < 0 {
			c.add(CodeSpecInputGrammarInvalid, path+".repeat", "grammar line repeat count cannot be negative")
		}
		if len(line.Fields) == 0 {
			c.add(CodeSpecInputGrammarInvalid, path+".fields", "grammar line requires at least one field")
		}
		if strings.TrimSpace(line.Meaning) == "" {
			c.add(CodeSpecInputGrammarInvalid, path+".meaning", "grammar line meaning is required")
		}
		requiresUnitRepeat := false
		for j, field := range line.Fields {
			fieldPath := fmt.Sprintf("%s.fields[%d]", path, j)
			symbolType, exists := symbols[field.Symbol]
			if !exists {
				c.add(CodeSpecSymbolUndefined, fieldPath+".symbol", "grammar field references an undefined symbol")
			} else if scopes[field.Symbol] != fieldScope {
				c.add(CodeSpecInputGrammarInvalid, fieldPath+".symbol", "grammar field symbol has the wrong scope")
			}
			if _, duplicate := fieldSymbols[field.Symbol]; duplicate && field.Symbol != "" {
				c.add(CodeSpecInputGrammarInvalid, fieldPath+".symbol", "grammar field symbol is duplicated")
			}
			fieldSymbols[field.Symbol] = struct{}{}
			switch field.Mode {
			case domain.SemanticGrammarFieldScalar:
				requiresUnitRepeat = true
				if field.Count != nil || !oneOf(symbolType, domain.SemanticSymbolInteger, domain.SemanticSymbolString, domain.SemanticSymbolBoolean) {
					c.add(CodeSpecInputGrammarInvalid, fieldPath, "scalar field requires a scalar symbol and no count")
				}
			case domain.SemanticGrammarFieldSequence:
				requiresUnitRepeat = true
				if field.Count == nil || !grammarSequenceType(symbolType) {
					c.add(CodeSpecInputGrammarInvalid, fieldPath, "sequence field requires a sequence-like symbol and count")
				} else {
					lintIntegerExpression(c, fieldPath+".count", *field.Count, symbols, ranges, 0)
					lintGrammarExpressionRefs(c, fieldPath+".count", *field.Count, scopes, availableInputSymbols, name == "input")
					if countRange, known := expressionInterval(*field.Count, symbols, ranges, 0); !known {
						c.add(CodeSpecInputGrammarInvalid, fieldPath+".count", "sequence field count requires a known bounded interval")
					} else {
						if countRange.min.Sign() < 0 {
							c.add(CodeSpecInputGrammarInvalid, fieldPath+".count", "sequence field count cannot be negative")
						}
						lintGrammarExtentRange(c, fieldPath+".count", field.Symbol, symbolType, countRange, ranges)
					}
				}
			case domain.SemanticGrammarFieldElementPerRepeat:
				if field.Count != nil || !grammarSequenceType(symbolType) {
					c.add(CodeSpecInputGrammarInvalid, fieldPath, "element_per_repeat requires a sequence-like symbol and no count")
				} else if repeatKnown {
					lintGrammarExtentRange(c, path+".repeat", field.Symbol, symbolType, repeatRange, ranges)
				}
			default:
				c.add(CodeSpecInputGrammarInvalid, fieldPath+".mode", "unsupported grammar field mode")
			}
			// Fields are read left-to-right. A later field on the same physical
			// line may use an earlier scalar (for example: n a1 ... an), but
			// may not reference itself or a later field.
			if name == "input" && scopes[field.Symbol] == domain.SemanticSymbolScopeInput {
				availableInputSymbols[field.Symbol] = struct{}{}
			}
		}
		if requiresUnitRepeat && !isLiteralIntegerOne(line.Repeat) {
			c.add(CodeSpecInputGrammarInvalid, path+".repeat", "scalar and sequence fields require literal repeat=1; use element_per_repeat for repeated collections")
		}
	}
	for symbol, scope := range scopes {
		if scope == fieldScope {
			if _, exists := fieldSymbols[symbol]; !exists {
				c.add(CodeSpecSymbolUnreferenced, base+".lines", name+" symbol "+symbol+" is absent from the grammar")
			}
		}
	}
}

func isLiteralIntegerOne(expression domain.SemanticExpressionV1) bool {
	one := int64(1)
	return semanticExpressionEqual(expression, domain.SemanticExpressionV1{Kind: domain.SemanticExpressionInteger, Value: &one})
}

func lintGrammarExtentRange(c *issueCollector, path, symbol, symbolType string, extent integerInterval, ranges map[string]effectiveRange) {
	measure := ""
	if lengthConstraintType(symbolType) {
		measure = domain.SemanticBoundaryMeasureLength
	} else if oneOf(symbolType, domain.SemanticSymbolSet, domain.SemanticSymbolMultiset) {
		measure = domain.SemanticBoundaryMeasureCardinality
	}
	if measure == "" {
		return
	}
	declared, known := intervalFromRange(ranges[rangeKey(symbol, measure)])
	if !known {
		return
	}
	if extent.min.Cmp(declared.min) < 0 || extent.max.Cmp(declared.max) > 0 {
		c.add(CodeSpecInputGrammarInvalid, path, "grammar extent can escape the symbol's structured range")
	}
}

func lintGrammarExpressionRefs(c *issueCollector, path string, expression domain.SemanticExpressionV1, scopes map[string]string, available map[string]struct{}, requirePreviousInput bool) {
	for _, ref := range expressionSymbolRefs(expression) {
		if requirePreviousInput {
			if scopes[ref] != domain.SemanticSymbolScopeInput {
				c.add(CodeSpecInputGrammarInvalid, path, "input grammar expressions may reference only input-scoped symbols")
			} else if _, exists := available[ref]; !exists {
				c.add(CodeSpecInputGrammarInvalid, path, "input grammar expression references a symbol before it is read")
			}
		}
	}
}

func expressionSymbolRefs(expression domain.SemanticExpressionV1) []string {
	refs := []string{}
	if expression.Symbol != "" {
		refs = append(refs, expression.Symbol)
	}
	for _, argument := range expression.Args {
		refs = append(refs, expressionSymbolRefs(argument)...)
	}
	return refs
}

func grammarSequenceType(symbolType string) bool {
	return oneOf(symbolType,
		domain.SemanticSymbolIntegerSequence,
		domain.SemanticSymbolStringSequence,
		domain.SemanticSymbolSet,
		domain.SemanticSymbolMultiset,
		domain.SemanticSymbolGraph,
		domain.SemanticSymbolTree,
		domain.SemanticSymbolGrid,
	)
}

func lintObjective(c *issueCollector, objective domain.SemanticObjectiveV1, symbols, scopes map[string]string) {
	base := "semantic_spec.objective"
	if !oneOf(objective.Kind,
		domain.SemanticObjectiveCompute,
		domain.SemanticObjectiveCount,
		domain.SemanticObjectiveDecision,
		domain.SemanticObjectiveMinimize,
		domain.SemanticObjectiveMaximize,
		domain.SemanticObjectiveConstruct,
	) {
		c.add(CodeSpecObjectiveInvalid, base+".kind", "unsupported objective kind")
	}
	if strings.TrimSpace(objective.Summary) == "" {
		c.add(CodeSpecObjectiveInvalid, base+".summary", "objective summary is required")
	}
	if len(objective.SymbolRefs) == 0 {
		c.add(CodeSpecObjectiveInvalid, base+".symbol_refs", "objective must reference at least one symbol")
	}
	for i, ref := range objective.SymbolRefs {
		if _, exists := symbols[ref]; !exists {
			c.add(CodeSpecSymbolUndefined, fmt.Sprintf("%s.symbol_refs[%d]", base, i), "objective references an undefined symbol")
		}
	}
	hasOutput := false
	for _, ref := range objective.SymbolRefs {
		if scopes[ref] == domain.SemanticSymbolScopeOutput {
			hasOutput = true
			break
		}
	}
	if !hasOutput {
		c.add(CodeSpecObjectiveInvalid, base+".symbol_refs", "objective must reference at least one output-scoped symbol")
	}
}

func lintSymbolCoverage(c *issueCollector, value domain.SemanticSpecV1, scopes map[string]string) {
	inputRefs := sectionRefSet(value.Sections.Input)
	outputRefs := sectionRefSet(value.Sections.Output)
	constraintRefs := sectionRefSet(value.Sections.Constraints)
	objectiveRefs := stringSet(value.Objective.SymbolRefs)
	for name, scope := range scopes {
		switch scope {
		case domain.SemanticSymbolScopeInput:
			if _, exists := inputRefs[name]; !exists {
				c.add(CodeSpecSymbolUnreferenced, "semantic_spec.sections.input.symbol_refs", "input symbol "+name+" is not declared by the input section")
			}
		case domain.SemanticSymbolScopeOutput:
			if _, exists := outputRefs[name]; !exists {
				c.add(CodeSpecSymbolUnreferenced, "semantic_spec.sections.output.symbol_refs", "output symbol "+name+" is not declared by the output section")
			}
		case domain.SemanticSymbolScopeDerived:
			_, inInput := inputRefs[name]
			_, inOutput := outputRefs[name]
			_, inConstraints := constraintRefs[name]
			_, inObjective := objectiveRefs[name]
			if !inInput && !inOutput && !inConstraints && !inObjective {
				c.add(CodeSpecSymbolUnreferenced, "semantic_spec.symbols", "derived symbol "+name+" is not used by any structured section")
			}
		}
	}
}

func sectionRefSet(section *domain.SemanticSectionV1) map[string]struct{} {
	if section == nil {
		return map[string]struct{}{}
	}
	return stringSet(section.SymbolRefs)
}

func stringSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

type effectiveRange struct {
	set bool
	min int64
	max int64
}

func lintConstraints(c *issueCollector, constraints []domain.SemanticConstraintV1, symbols map[string]string) map[string]effectiveRange {
	ranges := make(map[string]effectiveRange)
	if len(constraints) == 0 {
		c.add(CodeSpecSectionMissing, "semantic_spec.constraints", "at least one structured constraint is required")
		return ranges
	}
	for i, constraint := range constraints {
		path := fmt.Sprintf("semantic_spec.constraints[%d]", i)
		symbolType, exists := symbols[constraint.Subject]
		if !exists {
			c.add(CodeSpecSymbolUndefined, path+".subject", "constraint subject is undefined")
		}
		if !supportedConstraintKind(constraint.Kind) {
			c.add(CodeSpecRangeTypeMismatch, path+".kind", "unsupported constraint kind")
		} else if exists && !constraintMatchesType(constraint.Kind, symbolType) {
			c.add(CodeSpecRangeTypeMismatch, path+".kind", "constraint kind does not match its subject type")
		}
		if constraint.Min == nil || constraint.Max == nil {
			c.add(CodeSpecRangeInvalid, path, "constraint min and max are both required")
			continue
		}
		if *constraint.Min > *constraint.Max {
			c.add(CodeSpecRangeInvalid, path, "constraint min exceeds max")
			continue
		}
		if oneOf(constraint.Kind, domain.SemanticConstraintLengthRange, domain.SemanticConstraintCardinality) && *constraint.Min < 0 {
			c.add(CodeSpecRangeInvalid, path, "length and cardinality ranges cannot be negative")
			continue
		}
		if strings.TrimSpace(constraint.Meaning) == "" {
			c.add(CodeSpecSectionMissing, path+".meaning", "constraint meaning is required")
		}
		if !exists {
			continue
		}
		key := rangeKey(constraint.Subject, constraintMeasure(constraint.Kind))
		current := ranges[key]
		if !current.set {
			ranges[key] = effectiveRange{set: true, min: *constraint.Min, max: *constraint.Max}
			continue
		}
		if *constraint.Min > current.min {
			current.min = *constraint.Min
		}
		if *constraint.Max < current.max {
			current.max = *constraint.Max
		}
		current.set = true
		ranges[key] = current
		if current.min > current.max {
			c.add(CodeSpecRangeInvalid, path, "constraints for the same subject have an empty intersection")
		}
	}
	return ranges
}

func lintRequiredConstraints(c *issueCollector, symbols []domain.SemanticSymbolV1, ranges map[string]effectiveRange) {
	for i, symbol := range symbols {
		if symbol.Scope != domain.SemanticSymbolScopeInput {
			continue
		}
		for _, measure := range requiredConstraintMeasures(symbol.Type) {
			if valueRange, exists := ranges[rangeKey(symbol.Name, measure)]; !exists || !valueRange.set {
				c.add(CodeSpecConstraintMissing, fmt.Sprintf("semantic_spec.symbols[%d]", i), "input symbol "+symbol.Name+" requires a structured "+measure+" range")
			}
		}
	}
}

func lintConstraintSectionCoverage(c *issueCollector, section *domain.SemanticSectionV1, constraints []domain.SemanticConstraintV1, relations []domain.SemanticRelationV1) {
	if section == nil {
		return
	}
	refs := stringSet(section.SymbolRefs)
	required := make(map[string]struct{})
	for _, constraint := range constraints {
		required[constraint.Subject] = struct{}{}
	}
	for _, relation := range relations {
		for _, ref := range expressionSymbolRefs(relation.Left) {
			required[ref] = struct{}{}
		}
		for _, ref := range expressionSymbolRefs(relation.Right) {
			required[ref] = struct{}{}
		}
	}
	for ref := range required {
		if _, exists := refs[ref]; !exists {
			c.add(CodeSpecSymbolUnreferenced, "semantic_spec.sections.constraints.symbol_refs", "constraint symbol "+ref+" is absent from the constraints section")
		}
	}
}

func lintRelations(c *issueCollector, relations []domain.SemanticRelationV1, symbols map[string]string, ranges map[string]effectiveRange) {
	ids := make(map[string]struct{}, len(relations))
	for i, relation := range relations {
		path := fmt.Sprintf("semantic_spec.relations[%d]", i)
		if strings.TrimSpace(relation.ID) == "" || relation.ID != strings.TrimSpace(relation.ID) {
			c.add(CodeSpecRelationInvalid, path+".id", "relation id is required and must be trimmed")
		} else if _, exists := ids[relation.ID]; exists {
			c.add(CodeSpecRelationInvalid, path+".id", "relation id is duplicated")
		} else {
			ids[relation.ID] = struct{}{}
		}
		if !oneOf(relation.Operator,
			domain.SemanticRelationEqual,
			domain.SemanticRelationLessThan,
			domain.SemanticRelationLessThanOrEqual,
			domain.SemanticRelationGreaterThan,
			domain.SemanticRelationGreaterThanOrEqual,
		) {
			c.add(CodeSpecRelationInvalid, path+".operator", "unsupported relation operator")
		}
		lintIntegerExpression(c, path+".left", relation.Left, symbols, ranges, 0)
		lintIntegerExpression(c, path+".right", relation.Right, symbols, ranges, 0)
		structuralConflict := relationRestrictedAffineConflict(relation.Operator, relation.Left, relation.Right, symbols)
		if structuralConflict {
			c.add(CodeSpecRelationInvalid, path, "relation is impossible by structural constant-offset analysis")
		}
		leftRange, leftKnown := expressionInterval(relation.Left, symbols, ranges, 0)
		rightRange, rightKnown := expressionInterval(relation.Right, symbols, ranges, 0)
		if !structuralConflict && leftKnown && rightKnown && relationRangesConflict(relation.Operator, leftRange, rightRange) {
			c.add(CodeSpecRelationInvalid, path, "relation conflicts with the declared structured ranges")
		}
		if strings.TrimSpace(relation.Meaning) == "" {
			c.add(CodeSpecRelationInvalid, path+".meaning", "relation meaning is required")
		}
	}
}

type integerInterval struct {
	min *big.Int
	max *big.Int
}

type restrictedAffineAtom struct {
	kind   string
	symbol string
}

type restrictedAffineForm struct {
	constant *big.Int
	terms    map[restrictedAffineAtom]*big.Int
}

// normalizeRestrictedAffine collects only exact integer literals, integer
// symbols, length/cardinality leaves, and recursive addition/subtraction. It
// deliberately does not look through multiplication or floor division.
func normalizeRestrictedAffine(expression domain.SemanticExpressionV1, symbols map[string]string, depth int) (restrictedAffineForm, bool) {
	if depth > 16 {
		return restrictedAffineForm{}, false
	}
	constantOnly := func(value *big.Int) restrictedAffineForm {
		return restrictedAffineForm{constant: new(big.Int).Set(value), terms: make(map[restrictedAffineAtom]*big.Int)}
	}
	singleAtom := func(kind, symbol string) restrictedAffineForm {
		return restrictedAffineForm{
			constant: big.NewInt(0),
			terms: map[restrictedAffineAtom]*big.Int{
				{kind: kind, symbol: symbol}: big.NewInt(1),
			},
		}
	}

	switch expression.Kind {
	case domain.SemanticExpressionInteger:
		if expression.Value == nil || expression.Symbol != "" || len(expression.Args) != 0 {
			return restrictedAffineForm{}, false
		}
		return constantOnly(big.NewInt(*expression.Value)), true
	case domain.SemanticExpressionSymbol:
		if expression.Symbol == "" || expression.Value != nil || len(expression.Args) != 0 || symbols[expression.Symbol] != domain.SemanticSymbolInteger {
			return restrictedAffineForm{}, false
		}
		return singleAtom(domain.SemanticExpressionSymbol, expression.Symbol), true
	case domain.SemanticExpressionLength:
		if expression.Symbol == "" || expression.Value != nil || len(expression.Args) != 0 || !lengthConstraintType(symbols[expression.Symbol]) {
			return restrictedAffineForm{}, false
		}
		return singleAtom(domain.SemanticExpressionLength, expression.Symbol), true
	case domain.SemanticExpressionCardinality:
		if expression.Symbol == "" || expression.Value != nil || len(expression.Args) != 0 || !cardinalityConstraintType(symbols[expression.Symbol]) {
			return restrictedAffineForm{}, false
		}
		return singleAtom(domain.SemanticExpressionCardinality, expression.Symbol), true
	case domain.SemanticExpressionAdd, domain.SemanticExpressionSubtract:
		if expression.Symbol != "" || expression.Value != nil || len(expression.Args) != 2 {
			return restrictedAffineForm{}, false
		}
		left, leftKnown := normalizeRestrictedAffine(expression.Args[0], symbols, depth+1)
		right, rightKnown := normalizeRestrictedAffine(expression.Args[1], symbols, depth+1)
		if !leftKnown || !rightKnown {
			return restrictedAffineForm{}, false
		}
		if expression.Kind == domain.SemanticExpressionAdd {
			return combineRestrictedAffine(left, right, 1), true
		}
		return combineRestrictedAffine(left, right, -1), true
	default:
		return restrictedAffineForm{}, false
	}
}

func combineRestrictedAffine(left, right restrictedAffineForm, rightSign int64) restrictedAffineForm {
	result := restrictedAffineForm{
		constant: new(big.Int).Set(left.constant),
		terms:    make(map[restrictedAffineAtom]*big.Int, len(left.terms)+len(right.terms)),
	}
	for atom, coefficient := range left.terms {
		result.terms[atom] = new(big.Int).Set(coefficient)
	}
	sign := big.NewInt(rightSign)
	result.constant.Add(result.constant, new(big.Int).Mul(new(big.Int).Set(right.constant), sign))
	for atom, coefficient := range right.terms {
		delta := new(big.Int).Mul(new(big.Int).Set(coefficient), sign)
		if current, exists := result.terms[atom]; exists {
			current.Add(current, delta)
			if current.Sign() == 0 {
				delete(result.terms, atom)
			}
			continue
		}
		if delta.Sign() != 0 {
			result.terms[atom] = delta
		}
	}
	return result
}

func intervalFromRestrictedAffine(form restrictedAffineForm, ranges map[string]effectiveRange) (integerInterval, bool) {
	minimum := new(big.Int).Set(form.constant)
	maximum := new(big.Int).Set(form.constant)
	atoms := make([]restrictedAffineAtom, 0, len(form.terms))
	for atom := range form.terms {
		atoms = append(atoms, atom)
	}
	sort.Slice(atoms, func(i, j int) bool {
		if atoms[i].kind != atoms[j].kind {
			return atoms[i].kind < atoms[j].kind
		}
		return atoms[i].symbol < atoms[j].symbol
	})
	for _, atom := range atoms {
		measure := ""
		switch atom.kind {
		case domain.SemanticExpressionSymbol:
			measure = domain.SemanticBoundaryMeasureValue
		case domain.SemanticExpressionLength:
			measure = domain.SemanticBoundaryMeasureLength
		case domain.SemanticExpressionCardinality:
			measure = domain.SemanticBoundaryMeasureCardinality
		default:
			return integerInterval{}, false
		}
		atomInterval, known := intervalFromRange(ranges[rangeKey(atom.symbol, measure)])
		if !known {
			return integerInterval{}, false
		}
		coefficient := form.terms[atom]
		first := new(big.Int).Mul(new(big.Int).Set(coefficient), atomInterval.min)
		second := new(big.Int).Mul(new(big.Int).Set(coefficient), atomInterval.max)
		if first.Cmp(second) > 0 {
			first, second = second, first
		}
		minimum.Add(minimum, first)
		maximum.Add(maximum, second)
	}
	return integerInterval{min: minimum, max: maximum}, true
}

func expressionInterval(expression domain.SemanticExpressionV1, symbols map[string]string, ranges map[string]effectiveRange, depth int) (integerInterval, bool) {
	if depth > 16 {
		return integerInterval{}, false
	}
	if expression.Kind == domain.SemanticExpressionSubtract && len(expression.Args) == 2 && semanticExpressionEqual(expression.Args[0], expression.Args[1]) {
		return integerInterval{min: big.NewInt(0), max: big.NewInt(0)}, true
	}
	if affine, known := normalizeRestrictedAffine(expression, symbols, depth); known {
		return intervalFromRestrictedAffine(affine, ranges)
	}
	switch expression.Kind {
	case domain.SemanticExpressionSymbol:
		if symbols[expression.Symbol] != domain.SemanticSymbolInteger {
			return integerInterval{}, false
		}
		return intervalFromRange(ranges[rangeKey(expression.Symbol, domain.SemanticBoundaryMeasureValue)])
	case domain.SemanticExpressionLength:
		if !lengthConstraintType(symbols[expression.Symbol]) {
			return integerInterval{}, false
		}
		return intervalFromRange(ranges[rangeKey(expression.Symbol, domain.SemanticBoundaryMeasureLength)])
	case domain.SemanticExpressionCardinality:
		if !cardinalityConstraintType(symbols[expression.Symbol]) {
			return integerInterval{}, false
		}
		return intervalFromRange(ranges[rangeKey(expression.Symbol, domain.SemanticBoundaryMeasureCardinality)])
	case domain.SemanticExpressionInteger:
		if expression.Value == nil {
			return integerInterval{}, false
		}
		value := big.NewInt(*expression.Value)
		return integerInterval{min: new(big.Int).Set(value), max: new(big.Int).Set(value)}, true
	case domain.SemanticExpressionAdd, domain.SemanticExpressionSubtract, domain.SemanticExpressionMultiply, domain.SemanticExpressionFloorDivide:
		if len(expression.Args) != 2 {
			return integerInterval{}, false
		}
		left, leftKnown := expressionInterval(expression.Args[0], symbols, ranges, depth+1)
		right, rightKnown := expressionInterval(expression.Args[1], symbols, ranges, depth+1)
		if !leftKnown || !rightKnown {
			return integerInterval{}, false
		}
		switch expression.Kind {
		case domain.SemanticExpressionAdd:
			return integerInterval{min: new(big.Int).Add(left.min, right.min), max: new(big.Int).Add(left.max, right.max)}, true
		case domain.SemanticExpressionSubtract:
			return integerInterval{min: new(big.Int).Sub(left.min, right.max), max: new(big.Int).Sub(left.max, right.min)}, true
		case domain.SemanticExpressionMultiply:
			return intervalFromCandidates(
				new(big.Int).Mul(left.min, right.min),
				new(big.Int).Mul(left.min, right.max),
				new(big.Int).Mul(left.max, right.min),
				new(big.Int).Mul(left.max, right.max),
			)
		case domain.SemanticExpressionFloorDivide:
			if right.min.Sign() <= 0 && right.max.Sign() >= 0 {
				return integerInterval{}, false
			}
			return intervalFromCandidates(
				floorDivide(left.min, right.min),
				floorDivide(left.min, right.max),
				floorDivide(left.max, right.min),
				floorDivide(left.max, right.max),
			)
		}
	}
	return integerInterval{}, false
}

func intervalFromRange(value effectiveRange) (integerInterval, bool) {
	if !value.set || value.min > value.max {
		return integerInterval{}, false
	}
	return integerInterval{min: big.NewInt(value.min), max: big.NewInt(value.max)}, true
}

func intervalFromCandidates(values ...*big.Int) (integerInterval, bool) {
	if len(values) == 0 {
		return integerInterval{}, false
	}
	minimum := new(big.Int).Set(values[0])
	maximum := new(big.Int).Set(values[0])
	for _, value := range values[1:] {
		if value.Cmp(minimum) < 0 {
			minimum.Set(value)
		}
		if value.Cmp(maximum) > 0 {
			maximum.Set(value)
		}
	}
	return integerInterval{min: minimum, max: maximum}, true
}

func floorDivide(numerator, denominator *big.Int) *big.Int {
	quotient := new(big.Int)
	remainder := new(big.Int)
	quotient.QuoRem(numerator, denominator, remainder)
	if remainder.Sign() != 0 && numerator.Sign() != denominator.Sign() {
		quotient.Sub(quotient, big.NewInt(1))
	}
	return quotient
}

func relationRangesConflict(operator string, left, right integerInterval) bool {
	switch operator {
	case domain.SemanticRelationEqual:
		return left.max.Cmp(right.min) < 0 || right.max.Cmp(left.min) < 0
	case domain.SemanticRelationLessThan:
		return left.min.Cmp(right.max) >= 0
	case domain.SemanticRelationLessThanOrEqual:
		return left.min.Cmp(right.max) > 0
	case domain.SemanticRelationGreaterThan:
		return left.max.Cmp(right.min) <= 0
	case domain.SemanticRelationGreaterThanOrEqual:
		return left.max.Cmp(right.min) < 0
	default:
		return false
	}
}

// relationRestrictedAffineConflict recognizes an impossible relation only
// when addition/subtraction normalization cancels every typed atom exactly.
func relationRestrictedAffineConflict(operator string, left, right domain.SemanticExpressionV1, symbols map[string]string) bool {
	leftForm, leftKnown := normalizeRestrictedAffine(left, symbols, 0)
	rightForm, rightKnown := normalizeRestrictedAffine(right, symbols, 0)
	if !leftKnown || !rightKnown {
		return false
	}
	difference := combineRestrictedAffine(leftForm, rightForm, -1)
	if len(difference.terms) != 0 {
		return false
	}
	value := difference.constant
	switch operator {
	case domain.SemanticRelationEqual:
		return value.Sign() != 0
	case domain.SemanticRelationLessThan:
		return value.Sign() >= 0
	case domain.SemanticRelationLessThanOrEqual:
		return value.Sign() > 0
	case domain.SemanticRelationGreaterThan:
		return value.Sign() <= 0
	case domain.SemanticRelationGreaterThanOrEqual:
		return value.Sign() < 0
	default:
		return false
	}
}

func lintIntegerExpression(c *issueCollector, path string, expression domain.SemanticExpressionV1, symbols map[string]string, ranges map[string]effectiveRange, depth int) {
	if depth > 16 {
		c.add(CodeSpecRelationInvalid, path, "expression exceeds the maximum nesting depth")
		return
	}
	switch expression.Kind {
	case domain.SemanticExpressionSymbol:
		if expression.Symbol == "" || len(expression.Args) != 0 || expression.Value != nil {
			c.add(CodeSpecRelationInvalid, path, "symbol expression must contain only a symbol")
			return
		}
		symbolType, exists := symbols[expression.Symbol]
		if !exists {
			c.add(CodeSpecSymbolUndefined, path+".symbol", "expression references an undefined symbol")
		} else if symbolType != domain.SemanticSymbolInteger {
			c.add(CodeSpecRelationInvalid, path+".symbol", "arithmetic symbol must be an integer")
		}
	case domain.SemanticExpressionInteger:
		if expression.Value == nil || expression.Symbol != "" || len(expression.Args) != 0 {
			c.add(CodeSpecRelationInvalid, path, "integer expression must contain only a value")
		}
	case domain.SemanticExpressionLength:
		if expression.Symbol == "" || expression.Value != nil || len(expression.Args) != 0 {
			c.add(CodeSpecRelationInvalid, path, "length expression must contain only a symbol")
			return
		}
		symbolType, exists := symbols[expression.Symbol]
		if !exists {
			c.add(CodeSpecSymbolUndefined, path+".symbol", "length references an undefined symbol")
		} else if !lengthConstraintType(symbolType) {
			c.add(CodeSpecRelationInvalid, path+".symbol", "length requires a string, sequence, or grid symbol")
		}
	case domain.SemanticExpressionCardinality:
		if expression.Symbol == "" || expression.Value != nil || len(expression.Args) != 0 {
			c.add(CodeSpecRelationInvalid, path, "cardinality expression must contain only a symbol")
			return
		}
		symbolType, exists := symbols[expression.Symbol]
		if !exists {
			c.add(CodeSpecSymbolUndefined, path+".symbol", "cardinality references an undefined symbol")
		} else if !cardinalityConstraintType(symbolType) {
			c.add(CodeSpecRelationInvalid, path+".symbol", "cardinality requires a collection, graph, or tree symbol")
		}
	case domain.SemanticExpressionAdd, domain.SemanticExpressionSubtract, domain.SemanticExpressionMultiply, domain.SemanticExpressionFloorDivide:
		if expression.Symbol != "" || expression.Value != nil || len(expression.Args) != 2 {
			c.add(CodeSpecRelationInvalid, path, "arithmetic expression requires exactly two arguments")
			return
		}
		for i, argument := range expression.Args {
			lintIntegerExpression(c, fmt.Sprintf("%s.args[%d]", path, i), argument, symbols, ranges, depth+1)
		}
		if expression.Kind == domain.SemanticExpressionFloorDivide && expression.Args[1].Kind == domain.SemanticExpressionInteger && expression.Args[1].Value != nil && *expression.Args[1].Value == 0 {
			c.add(CodeSpecRelationInvalid, path+".args[1]", "floor division by zero is invalid")
		}
		if expression.Kind == domain.SemanticExpressionFloorDivide {
			if denominator, known := expressionInterval(expression.Args[1], symbols, ranges, depth+1); !known {
				c.add(CodeSpecRelationInvalid, path+".args[1]", "floor division denominator requires a known bounded nonzero interval")
			} else if denominator.min.Sign() <= 0 && denominator.max.Sign() >= 0 {
				c.add(CodeSpecRelationInvalid, path+".args[1]", "floor division denominator range includes zero")
			}
		}
	default:
		c.add(CodeSpecRelationInvalid, path+".kind", "unsupported expression kind")
	}
}

func rangeKey(subject, measure string) string {
	return subject + "\x00" + measure
}

func constraintMeasure(kind string) string {
	switch kind {
	case domain.SemanticConstraintIntegerRange:
		return domain.SemanticBoundaryMeasureValue
	case domain.SemanticConstraintLengthRange:
		return domain.SemanticBoundaryMeasureLength
	case domain.SemanticConstraintCardinality:
		return domain.SemanticBoundaryMeasureCardinality
	case domain.SemanticConstraintElementRange:
		return domain.SemanticBoundaryMeasureElementValue
	default:
		return ""
	}
}

func requiredConstraintMeasures(symbolType string) []string {
	switch symbolType {
	case domain.SemanticSymbolInteger:
		return []string{domain.SemanticBoundaryMeasureValue}
	case domain.SemanticSymbolIntegerSequence:
		return []string{domain.SemanticBoundaryMeasureLength, domain.SemanticBoundaryMeasureElementValue}
	case domain.SemanticSymbolString, domain.SemanticSymbolStringSequence, domain.SemanticSymbolGrid:
		return []string{domain.SemanticBoundaryMeasureLength}
	case domain.SemanticSymbolSet, domain.SemanticSymbolMultiset, domain.SemanticSymbolGraph, domain.SemanticSymbolTree:
		return []string{domain.SemanticBoundaryMeasureCardinality}
	default:
		return nil
	}
}

func lengthConstraintType(symbolType string) bool {
	return oneOf(symbolType, domain.SemanticSymbolString, domain.SemanticSymbolIntegerSequence, domain.SemanticSymbolStringSequence, domain.SemanticSymbolGrid)
}

func cardinalityConstraintType(symbolType string) bool {
	return oneOf(symbolType, domain.SemanticSymbolSet, domain.SemanticSymbolMultiset, domain.SemanticSymbolGraph, domain.SemanticSymbolTree)
}

func lintTopology(c *issueCollector, topology domain.SemanticTopologyV1, symbols, scopes, policies map[string]string, relations []domain.SemanticRelationV1) {
	base := "semantic_spec.topology"
	if !oneOf(topology.Kind, domain.SemanticTopologyNone, domain.SemanticTopologyLinear, domain.SemanticTopologyCircular, domain.SemanticTopologyGraph, domain.SemanticTopologyTree, domain.SemanticTopologyGrid) {
		c.add(CodeSpecTopologyUnsupported, base+".kind", "unsupported topology kind")
	}
	if !oneOf(topology.Direction, domain.SemanticTopologyNotApplicable, domain.SemanticTopologyDirected, domain.SemanticTopologyUndirected) {
		c.add(CodeSpecTopologyUnsupported, base+".direction", "unsupported topology direction")
	}
	if !oneOf(topology.CyclePolicy, domain.SemanticTopologyNotApplicable, domain.SemanticTopologyAcyclic, domain.SemanticTopologyCyclic, domain.SemanticTopologyCyclesAllowed) {
		c.add(CodeSpecTopologyUnsupported, base+".cycle_policy", "unsupported topology cycle policy")
	}
	if !oneOf(topology.Connectivity, domain.SemanticTopologyNotApplicable, domain.SemanticTopologyConnected, domain.SemanticTopologyDisconnected) {
		c.add(CodeSpecTopologyUnsupported, base+".connectivity", "unsupported topology connectivity")
	}
	if !oneOf(topology.Dynamics, domain.SemanticTopologyNotApplicable, domain.SemanticTopologyStatic, domain.SemanticTopologyDynamic) {
		c.add(CodeSpecTopologyUnsupported, base+".dynamics", "unsupported topology dynamics")
	}
	if !oneOf(topology.SelfLoops, domain.SemanticTopologyNotApplicable, domain.SemanticTopologyAllowed, domain.SemanticTopologyForbidden) {
		c.add(CodeSpecTopologyUnsupported, base+".self_loops", "unsupported self-loop policy")
	}
	if !oneOf(topology.MultiEdges, domain.SemanticTopologyNotApplicable, domain.SemanticTopologyAllowed, domain.SemanticTopologyForbidden) {
		c.add(CodeSpecTopologyUnsupported, base+".multi_edges", "unsupported multi-edge policy")
	}

	switch topology.Kind {
	case domain.SemanticTopologyNone:
		if topology.Subject != "" || topology.Direction != domain.SemanticTopologyNotApplicable || topology.CyclePolicy != domain.SemanticTopologyNotApplicable || topology.Connectivity != domain.SemanticTopologyNotApplicable || topology.Dynamics != domain.SemanticTopologyNotApplicable || topology.SelfLoops != domain.SemanticTopologyNotApplicable || topology.MultiEdges != domain.SemanticTopologyNotApplicable || topology.VertexCountVariable != "" || topology.EdgeCountVariable != "" {
			c.add(CodeSpecTopologyConflict, base, "topology kind none requires all topology facts to be not_applicable")
		}
	case domain.SemanticTopologyLinear:
		lintTopologySubject(c, base+".subject", topology.Subject, symbols, scopes, domain.SemanticSymbolString, domain.SemanticSymbolIntegerSequence, domain.SemanticSymbolStringSequence)
		if topology.Direction != domain.SemanticTopologyNotApplicable || topology.CyclePolicy != domain.SemanticTopologyAcyclic || topology.Connectivity != domain.SemanticTopologyNotApplicable || !oneOf(topology.Dynamics, domain.SemanticTopologyStatic, domain.SemanticTopologyDynamic) || topology.SelfLoops != domain.SemanticTopologyNotApplicable || topology.MultiEdges != domain.SemanticTopologyNotApplicable || topology.VertexCountVariable != "" || topology.EdgeCountVariable != "" {
			c.add(CodeSpecTopologyConflict, base, "linear topology requires acyclic sequence facts and forbids graph-only facts")
		}
	case domain.SemanticTopologyCircular:
		lintTopologySubject(c, base+".subject", topology.Subject, symbols, scopes, domain.SemanticSymbolString, domain.SemanticSymbolIntegerSequence, domain.SemanticSymbolStringSequence)
		if topology.Direction != domain.SemanticTopologyNotApplicable || topology.CyclePolicy != domain.SemanticTopologyCyclic || topology.Connectivity != domain.SemanticTopologyNotApplicable || !oneOf(topology.Dynamics, domain.SemanticTopologyStatic, domain.SemanticTopologyDynamic) || topology.SelfLoops != domain.SemanticTopologyNotApplicable || topology.MultiEdges != domain.SemanticTopologyNotApplicable || topology.VertexCountVariable != "" || topology.EdgeCountVariable != "" {
			c.add(CodeSpecTopologyConflict, base, "circular topology requires cyclic sequence facts and forbids graph-only facts")
		}
	case domain.SemanticTopologyGraph:
		lintTopologySubject(c, base+".subject", topology.Subject, symbols, scopes, domain.SemanticSymbolGraph)
		if !oneOf(topology.Direction, domain.SemanticTopologyDirected, domain.SemanticTopologyUndirected) || !oneOf(topology.CyclePolicy, domain.SemanticTopologyAcyclic, domain.SemanticTopologyCyclic, domain.SemanticTopologyCyclesAllowed) || !oneOf(topology.Connectivity, domain.SemanticTopologyConnected, domain.SemanticTopologyDisconnected) || !oneOf(topology.Dynamics, domain.SemanticTopologyStatic, domain.SemanticTopologyDynamic) || !oneOf(topology.SelfLoops, domain.SemanticTopologyAllowed, domain.SemanticTopologyForbidden) || !oneOf(topology.MultiEdges, domain.SemanticTopologyAllowed, domain.SemanticTopologyForbidden) {
			c.add(CodeSpecTopologyConflict, base, "graph topology requires explicit direction, cycle, connectivity, dynamics, self-loop and multi-edge facts")
		}
		lintTopologySymbolRef(c, base+".vertex_count_variable", topology.VertexCountVariable, symbols, scopes, policies, true, true)
		lintTopologySymbolRef(c, base+".edge_count_variable", topology.EdgeCountVariable, symbols, scopes, policies, true, true)
		if topology.Subject != "" && topology.VertexCountVariable != "" && !hasCardinalityBinding(relations, topology.Subject, topology.VertexCountVariable) {
			c.add(CodeSpecTopologyConflict, base+".vertex_count_variable", "graph subject cardinality must be bound to its vertex count variable")
		}
	case domain.SemanticTopologyTree:
		lintTopologySubject(c, base+".subject", topology.Subject, symbols, scopes, domain.SemanticSymbolTree)
		if !oneOf(topology.Direction, domain.SemanticTopologyDirected, domain.SemanticTopologyUndirected) || topology.CyclePolicy != domain.SemanticTopologyAcyclic || topology.Connectivity != domain.SemanticTopologyConnected || !oneOf(topology.Dynamics, domain.SemanticTopologyStatic, domain.SemanticTopologyDynamic) || topology.SelfLoops != domain.SemanticTopologyForbidden || topology.MultiEdges != domain.SemanticTopologyForbidden {
			c.add(CodeSpecTopologyConflict, base, "tree topology must be explicit, connected, acyclic, and forbid self-loops and multi-edges")
		}
		lintTopologySymbolRef(c, base+".vertex_count_variable", topology.VertexCountVariable, symbols, scopes, policies, true, true)
		lintTopologySymbolRef(c, base+".edge_count_variable", topology.EdgeCountVariable, symbols, scopes, policies, false, false)
		if topology.Subject != "" && topology.VertexCountVariable != "" && !hasCardinalityBinding(relations, topology.Subject, topology.VertexCountVariable) {
			c.add(CodeSpecTopologyConflict, base+".vertex_count_variable", "tree subject cardinality must be bound to its vertex count variable")
		}
		if topology.EdgeCountVariable != "" && !hasTreeEdgeBinding(relations, topology.VertexCountVariable, topology.EdgeCountVariable) {
			c.add(CodeSpecTopologyConflict, base+".edge_count_variable", "tree edge count must be bound to vertex_count-1")
		}
	case domain.SemanticTopologyGrid:
		lintTopologySubject(c, base+".subject", topology.Subject, symbols, scopes, domain.SemanticSymbolGrid)
		if !oneOf(topology.Dynamics, domain.SemanticTopologyStatic, domain.SemanticTopologyDynamic) || !oneOf(topology.SelfLoops, domain.SemanticTopologyNotApplicable, domain.SemanticTopologyForbidden) || !oneOf(topology.MultiEdges, domain.SemanticTopologyNotApplicable, domain.SemanticTopologyForbidden) || topology.VertexCountVariable != "" || topology.EdgeCountVariable != "" {
			c.add(CodeSpecTopologyConflict, base, "grid topology requires explicit dynamics and forbids graph count variables, self-loops and multi-edges")
		}
	}
}

func hasCardinalityBinding(relations []domain.SemanticRelationV1, subject, countVariable string) bool {
	cardinality := domain.SemanticExpressionV1{Kind: domain.SemanticExpressionCardinality, Symbol: subject}
	count := domain.SemanticExpressionV1{Kind: domain.SemanticExpressionSymbol, Symbol: countVariable}
	for _, relation := range relations {
		if relation.Operator == domain.SemanticRelationEqual && ((semanticExpressionEqual(relation.Left, cardinality) && semanticExpressionEqual(relation.Right, count)) || (semanticExpressionEqual(relation.Right, cardinality) && semanticExpressionEqual(relation.Left, count))) {
			return true
		}
	}
	return false
}

func hasTreeEdgeBinding(relations []domain.SemanticRelationV1, vertexVariable, edgeVariable string) bool {
	one := int64(1)
	vertexMinusOne := domain.SemanticExpressionV1{
		Kind: domain.SemanticExpressionSubtract,
		Args: []domain.SemanticExpressionV1{
			{Kind: domain.SemanticExpressionSymbol, Symbol: vertexVariable},
			{Kind: domain.SemanticExpressionInteger, Value: &one},
		},
	}
	edge := domain.SemanticExpressionV1{Kind: domain.SemanticExpressionSymbol, Symbol: edgeVariable}
	for _, relation := range relations {
		if relation.Operator == domain.SemanticRelationEqual && ((semanticExpressionEqual(relation.Left, vertexMinusOne) && semanticExpressionEqual(relation.Right, edge)) || (semanticExpressionEqual(relation.Right, vertexMinusOne) && semanticExpressionEqual(relation.Left, edge))) {
			return true
		}
	}
	return false
}

// semanticExpressionEqual compares the JSON meaning of expression trees.
// With omitempty, nil and empty Args encode identically and must never produce
// a different lint verdict for the same canonical hash.
func semanticExpressionEqual(left, right domain.SemanticExpressionV1) bool {
	if left.Kind != right.Kind || left.Symbol != right.Symbol || len(left.Args) != len(right.Args) {
		return false
	}
	if (left.Value == nil) != (right.Value == nil) || (left.Value != nil && *left.Value != *right.Value) {
		return false
	}
	for i := range left.Args {
		if !semanticExpressionEqual(left.Args[i], right.Args[i]) {
			return false
		}
	}
	return true
}

func lintTopologySubject(c *issueCollector, path, ref string, symbols, scopes map[string]string, allowedTypes ...string) {
	if strings.TrimSpace(ref) == "" {
		c.add(CodeSpecTopologyConflict, path, "topology subject is required")
		return
	}
	symbolType, exists := symbols[ref]
	if !exists {
		c.add(CodeSpecSymbolUndefined, path, "topology references an undefined subject")
	} else if !oneOf(symbolType, allowedTypes...) {
		c.add(CodeSpecTopologyConflict, path, "topology subject type does not match topology kind")
	} else if scopes[ref] != domain.SemanticSymbolScopeInput {
		c.add(CodeSpecTopologyConflict, path, "topology subject must be input-scoped")
	}
}

func lintTopologySymbolRef(c *issueCollector, path, ref string, symbols, scopes, policies map[string]string, required, requireInput bool) {
	if strings.TrimSpace(ref) == "" {
		if required {
			c.add(CodeSpecSymbolUndefined, path, "topology count variable is required")
		}
		return
	}
	symbolType, exists := symbols[ref]
	if !exists {
		c.add(CodeSpecSymbolUndefined, path, "topology references an undefined count variable")
	} else if symbolType != domain.SemanticSymbolInteger {
		c.add(CodeSpecTopologyConflict, path, "topology count variable must be an integer")
	} else if requireInput && scopes[ref] != domain.SemanticSymbolScopeInput {
		c.add(CodeSpecTopologyConflict, path, "topology count variable must be input-scoped")
	} else if requireInput && policies[ref] != domain.SemanticBoundaryPolicyZeroOneRequired {
		c.add(CodeSpecTopologyConflict, path, "input topology count variable must require explicit zero/one facts")
	}
}

func lintBoundaries(c *issueCollector, boundaries []domain.SemanticBoundaryV1, symbols, policies map[string]string, ranges map[string]effectiveRange) {
	if len(boundaries) < 5 {
		c.add(CodeSpecBoundaryFactMissing, "semantic_spec.boundaries", "at least five high-risk boundary facts are required")
	}
	bySymbolMeasureAndValue := make(map[string]int)
	factsBySymbol := make(map[string]int)
	boundaryIDs := make(map[string]struct{})
	for i, boundary := range boundaries {
		path := fmt.Sprintf("semantic_spec.boundaries[%d]", i)
		if strings.TrimSpace(boundary.ID) == "" || boundary.ID != strings.TrimSpace(boundary.ID) {
			c.add(CodeSpecBoundaryFactMissing, path+".id", "boundary id is required")
		} else if _, exists := boundaryIDs[boundary.ID]; exists {
			c.add(CodeSpecBoundaryFactConflict, path+".id", "boundary id is duplicated")
		} else {
			boundaryIDs[boundary.ID] = struct{}{}
		}
		symbolType, exists := symbols[boundary.Symbol]
		if !exists {
			c.add(CodeSpecSymbolUndefined, path+".symbol", "boundary references an undefined symbol")
		} else if !boundaryMeasureMatchesType(boundary.Measure, symbolType) {
			c.add(CodeSpecBoundaryFactConflict, path+".measure", "boundary measure does not match the symbol type")
		} else if policies[boundary.Symbol] == domain.SemanticBoundaryPolicyNotApplicable {
			c.add(CodeSpecBoundaryFactConflict, path+".symbol", "boundary is forbidden by the symbol boundary policy")
		}
		key := fmt.Sprintf("%s\x00%s\x00%d", boundary.Symbol, boundary.Measure, boundary.Value)
		if previous, exists := bySymbolMeasureAndValue[key]; exists {
			c.add(CodeSpecBoundaryFactConflict, path, fmt.Sprintf("boundary duplicates semantic_spec.boundaries[%d]", previous))
		} else {
			bySymbolMeasureAndValue[key] = i
		}
		factsBySymbol[boundary.Symbol]++
		if strings.TrimSpace(boundary.Meaning) == "" {
			c.add(CodeSpecBoundaryFactMissing, path+".meaning", "boundary meaning is required")
		}
		if valueRange, exists := ranges[rangeKey(boundary.Symbol, boundary.Measure)]; exists && valueRange.set {
			within := boundary.Value >= valueRange.min && boundary.Value <= valueRange.max
			if within != boundary.Allowed {
				c.add(CodeSpecBoundaryFactConflict, path+".allowed", "boundary allowance conflicts with the structured range")
			}
		}
	}

	policySymbols := make([]string, 0, len(policies))
	for symbol := range policies {
		policySymbols = append(policySymbols, symbol)
	}
	sort.Strings(policySymbols)
	for _, symbol := range policySymbols {
		switch policies[symbol] {
		case domain.SemanticBoundaryPolicyZeroOneRequired:
			if valueRange, exists := ranges[rangeKey(symbol, domain.SemanticBoundaryMeasureValue)]; !exists || !valueRange.set {
				c.add(CodeSpecConstraintMissing, "semantic_spec.boundaries", "zero/one boundary symbol "+symbol+" requires an integer range")
			}
			for _, value := range []int64{0, 1} {
				key := fmt.Sprintf("%s\x00%s\x00%d", symbol, domain.SemanticBoundaryMeasureValue, value)
				if _, exists := bySymbolMeasureAndValue[key]; !exists {
					c.add(CodeSpecBoundaryFactMissing, "semantic_spec.boundaries", fmt.Sprintf("symbol %s requires an explicit %d boundary fact", symbol, value))
				}
			}
		case domain.SemanticBoundaryPolicyExplicit:
			if factsBySymbol[symbol] == 0 {
				c.add(CodeSpecBoundaryFactMissing, "semantic_spec.boundaries", "symbol "+symbol+" requires at least one explicit boundary fact")
			}
		}
	}
}

func boundaryMeasureMatchesType(measure, symbolType string) bool {
	switch symbolType {
	case domain.SemanticSymbolInteger, domain.SemanticSymbolBoolean:
		return measure == domain.SemanticBoundaryMeasureValue
	case domain.SemanticSymbolIntegerSequence:
		return oneOf(measure, domain.SemanticBoundaryMeasureLength, domain.SemanticBoundaryMeasureElementValue)
	case domain.SemanticSymbolString, domain.SemanticSymbolStringSequence, domain.SemanticSymbolGrid:
		return measure == domain.SemanticBoundaryMeasureLength
	case domain.SemanticSymbolSet, domain.SemanticSymbolMultiset, domain.SemanticSymbolGraph, domain.SemanticSymbolTree:
		return measure == domain.SemanticBoundaryMeasureCardinality
	default:
		return false
	}
}

func lintAnswerSemantics(c *issueCollector, answer domain.SemanticAnswerSemanticsV1, judge domain.SemanticJudgeV1) {
	base := "semantic_spec.answer_semantics"
	if answer.FloatingPointErrorRule != "" {
		c.add(CodeSpecJudgeContractInvalid, base+".floating_point_error_rule", "floating-point rules are unsupported by SemanticSpec v1")
	}
	switch answer.NoSolutionPolicy {
	case domain.SemanticNoSolutionImpossible, domain.SemanticNoSolutionEmptyOutput:
		if answer.NoSolutionToken != "" {
			c.add(CodeSpecNoSolutionSemanticsMissing, base+".no_solution_token", "no-solution token must be empty for this policy")
		}
	case domain.SemanticNoSolutionToken:
		if strings.TrimSpace(answer.NoSolutionToken) == "" {
			c.add(CodeSpecNoSolutionSemanticsMissing, base+".no_solution_token", "token policy requires an explicit output token")
		}
	default:
		c.add(CodeSpecNoSolutionSemanticsMissing, base+".no_solution_policy", "unsupported or missing no-solution policy")
	}

	switch answer.MultipleSolutionPolicy {
	case domain.SemanticMultipleSolutionUnique:
		if answer.CanonicalizationRule != "" || answer.CustomAcceptanceSemantics != "" {
			c.add(CodeSpecMultipleSolutionSemanticsMissing, base, "unique answers cannot carry canonical or custom acceptance rules")
		}
	case domain.SemanticMultipleSolutionAnyValid:
		if strings.TrimSpace(answer.CustomAcceptanceSemantics) == "" {
			c.add(CodeSpecMultipleSolutionSemanticsMissing, base+".custom_acceptance_semantics", "any-valid policy requires explicit acceptance semantics")
		}
		if answer.CanonicalizationRule != "" {
			c.add(CodeSpecMultipleSolutionSemanticsMissing, base+".canonicalization_rule", "any-valid policy cannot carry a canonicalization rule")
		}
	case domain.SemanticMultipleSolutionCanonical:
		if strings.TrimSpace(answer.CanonicalizationRule) == "" {
			c.add(CodeSpecMultipleSolutionSemanticsMissing, base+".canonicalization_rule", "canonical policy requires a deterministic rule")
		}
		if answer.CustomAcceptanceSemantics != "" {
			c.add(CodeSpecMultipleSolutionSemanticsMissing, base+".custom_acceptance_semantics", "canonical policy cannot carry custom acceptance semantics")
		}
	default:
		c.add(CodeSpecMultipleSolutionSemanticsMissing, base+".multiple_solution_policy", "unsupported or missing multiple-solution policy")
	}

	switch judge.Mode {
	case domain.SemanticJudgeExactNormalized:
		if judge.ComparisonProfile != domain.SemanticComparisonTrimTrailingSpaceLFV1 || judge.CheckerReference != "" || judge.CheckerSHA256 != "" || judge.CheckerRuleVersion != "" {
			c.add(CodeSpecJudgeContractInvalid, "semantic_spec.judge", "exact_normalized requires the frozen comparison profile and no checker fields")
		}
		if answer.MultipleSolutionPolicy == domain.SemanticMultipleSolutionAnyValid {
			c.add(CodeSpecMultipleSolutionSemanticsMissing, base+".multiple_solution_policy", "any-valid answers require a special checker")
		}
	case domain.SemanticJudgeSpecialChecker:
		if judge.ComparisonProfile != "" || judge.CheckerReference != strings.TrimSpace(judge.CheckerReference) || !strings.HasPrefix(judge.CheckerReference, "cas://") || !isSHA256(judge.CheckerSHA256) || judge.CheckerRuleVersion != strings.TrimSpace(judge.CheckerRuleVersion) || judge.CheckerRuleVersion == "" {
			c.add(CodeSpecJudgeContractInvalid, "semantic_spec.judge", "special_checker requires an immutable checker reference, lowercase SHA-256 and rule version, with no exact comparison profile")
		}
	default:
		c.add(CodeSpecJudgeContractInvalid, "semantic_spec.judge.mode", "unsupported judge mode")
	}
}

func lintAuthoringPlan(c *issueCollector, input LintInputV1, specSHA string) {
	value := input.AuthoringPlan
	if value.SchemaVersion != domain.AuthoringPlanSchemaV1 {
		c.add(CodeAuthoringSchemaUnsupported, "authoring_plan.schema_version", "unsupported AuthoringPlan schema version")
	}
	if !isSHA256(value.BriefSHA256) || value.BriefSHA256 != input.ExpectedBriefSHA256 || value.BriefSHA256 != input.SemanticSpec.BriefSHA256 {
		c.add(CodeAuthoringBriefHashMismatch, "authoring_plan.brief_sha256", "AuthoringPlan is not bound to the expected canonical brief")
	}
	if !isSHA256(value.SemanticSpecSHA256) || value.SemanticSpecSHA256 != specSHA {
		c.add(CodeAuthoringSpecHashMismatch, "authoring_plan.semantic_spec_sha256", "AuthoringPlan is not bound to this SemanticSpec")
	}
	if strings.TrimSpace(value.CoreIdea) == "" {
		c.add(CodeAuthoringRequiredFieldMissing, "authoring_plan.core_idea", "core idea is required")
	}
	lintAlgorithmPlan(c, "authoring_plan.intended_solution", value.IntendedSolution)
	lintAlgorithmPlan(c, "authoring_plan.brute_force_baseline", value.BruteForceBaseline)
	lintAlgorithmPlan(c, "authoring_plan.oracle_candidate_strategy", value.OracleCandidateStrategy)
	if value.TargetDifficulty != input.ExpectedDifficulty {
		c.addAdvisory(CodeAuthoringDifficultyMismatch, "authoring_plan.target_difficulty", "target difficulty does not match the generation brief")
	}
	if len(value.TeachingObjectives) == 0 {
		c.add(CodeAuthoringRequiredFieldMissing, "authoring_plan.teaching_objectives", "at least one teaching objective is required")
	}
	for i, objective := range value.TeachingObjectives {
		if strings.TrimSpace(objective) == "" {
			c.add(CodeAuthoringRequiredFieldMissing, fmt.Sprintf("authoring_plan.teaching_objectives[%d]", i), "teaching objective must not be empty")
		}
	}
	if strings.TrimSpace(value.CreativeIntent) == "" {
		c.add(CodeAuthoringRequiredFieldMissing, "authoring_plan.creative_intent", "creative intent is required")
	}

	concepts := make(map[string]struct{}, len(value.ConceptRoles))
	for i, concept := range value.ConceptRoles {
		path := fmt.Sprintf("authoring_plan.concept_roles[%d]", i)
		slug := strings.TrimSpace(concept.Slug)
		if slug == "" || slug != concept.Slug || strings.TrimSpace(concept.Role) == "" || strings.TrimSpace(concept.Necessity) == "" {
			c.add(CodeAuthoringRequiredFieldMissing, path, "concept slug, role and necessity are required")
		}
		if _, exists := concepts[slug]; exists && slug != "" {
			c.add(CodeAuthoringKnowledgePointMissing, path+".slug", "concept role is duplicated")
		}
		concepts[slug] = struct{}{}
	}
	for _, required := range input.RequiredKnowledgePoints {
		if _, exists := concepts[required]; !exists {
			c.add(CodeAuthoringKnowledgePointMissing, "authoring_plan.concept_roles", "required knowledge point "+required+" is missing")
		}
	}

	if len(value.FailureModes) == 0 {
		c.add(CodeAuthoringRequiredFieldMissing, "authoring_plan.failure_modes", "at least one likely failure mode is required")
	}
	failureIDs := make(map[string]struct{}, len(value.FailureModes))
	for i, failure := range value.FailureModes {
		path := fmt.Sprintf("authoring_plan.failure_modes[%d]", i)
		id := strings.TrimSpace(failure.ID)
		if id == "" || id != failure.ID || strings.TrimSpace(failure.Description) == "" || strings.TrimSpace(failure.WitnessIntent) == "" {
			c.add(CodeAuthoringRequiredFieldMissing, path, "failure mode id, description and witness intent are required")
		}
		if _, exists := failureIDs[id]; exists && id != "" {
			c.add(CodeAuthoringRequiredFieldMissing, path+".id", "failure mode id is duplicated")
		}
		failureIDs[id] = struct{}{}
	}

	boundaryIDs := make(map[string]struct{}, len(input.SemanticSpec.Boundaries))
	for _, boundary := range input.SemanticSpec.Boundaries {
		boundaryIDs[boundary.ID] = struct{}{}
	}
	if len(value.TestIntents) == 0 {
		c.add(CodeAuthoringRequiredFieldMissing, "authoring_plan.test_intents", "at least one test intent is required")
	}
	for i, intent := range value.TestIntents {
		path := fmt.Sprintf("authoring_plan.test_intents[%d]", i)
		if strings.TrimSpace(intent.Purpose) == "" || strings.TrimSpace(intent.ConstraintRegion) == "" {
			c.add(CodeAuthoringRequiredFieldMissing, path, "test purpose and constraint region are required")
		}
		for j, ref := range intent.BoundaryRefs {
			if ref != strings.TrimSpace(ref) {
				c.add(CodeAuthoringRequiredFieldMissing, fmt.Sprintf("%s.boundary_refs[%d]", path, j), "boundary reference must be trimmed")
			} else if _, exists := boundaryIDs[ref]; !exists {
				c.add(CodeSpecSymbolUndefined, fmt.Sprintf("%s.boundary_refs[%d]", path, j), "test intent references an undefined boundary")
			}
		}
	}
}

func lintAlgorithmPlan(c *issueCollector, path string, value domain.AuthoringAlgorithmPlanV1) {
	if strings.TrimSpace(value.Summary) == "" || strings.TrimSpace(value.TimeComplexity) == "" || strings.TrimSpace(value.SpaceComplexity) == "" {
		c.add(CodeAuthoringRequiredFieldMissing, path, "algorithm summary, time complexity and space complexity are required")
	}
}

func supportedSymbolType(value string) bool {
	return oneOf(value,
		domain.SemanticSymbolInteger,
		domain.SemanticSymbolString,
		domain.SemanticSymbolBoolean,
		domain.SemanticSymbolIntegerSequence,
		domain.SemanticSymbolStringSequence,
		domain.SemanticSymbolSet,
		domain.SemanticSymbolMultiset,
		domain.SemanticSymbolGraph,
		domain.SemanticSymbolTree,
		domain.SemanticSymbolGrid,
	)
}

func supportedConstraintKind(value string) bool {
	return oneOf(value,
		domain.SemanticConstraintIntegerRange,
		domain.SemanticConstraintLengthRange,
		domain.SemanticConstraintCardinality,
		domain.SemanticConstraintElementRange,
	)
}

func constraintMatchesType(kind, symbolType string) bool {
	switch kind {
	case domain.SemanticConstraintIntegerRange:
		return symbolType == domain.SemanticSymbolInteger
	case domain.SemanticConstraintLengthRange:
		return oneOf(symbolType, domain.SemanticSymbolString, domain.SemanticSymbolIntegerSequence, domain.SemanticSymbolStringSequence, domain.SemanticSymbolGrid)
	case domain.SemanticConstraintCardinality:
		return oneOf(symbolType, domain.SemanticSymbolSet, domain.SemanticSymbolMultiset, domain.SemanticSymbolGraph, domain.SemanticSymbolTree)
	case domain.SemanticConstraintElementRange:
		return symbolType == domain.SemanticSymbolIntegerSequence
	default:
		return false
	}
}

func oneOf(value string, allowed ...string) bool {
	for _, item := range allowed {
		if value == item {
			return true
		}
	}
	return false
}

func isSHA256(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}
