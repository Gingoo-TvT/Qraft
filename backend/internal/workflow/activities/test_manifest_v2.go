package activities

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	speccontract "github.com/Gingoo-TvT/Qraft/backend/internal/spec"
)

const (
	TestManifestSchemaVersionV2        = "algoforge.test-manifest.v2"
	IntegerBoundaryCoverageSchemaV1    = "algoforge.integer-boundary-coverage.v1"
	IntegerBoundaryCoverageRuleVersion = "algoforge.integer-boundary-coverage.rules.v1"
	IntegerBoundaryCasePlanSchemaV1    = "algoforge.integer-boundary-case-plan.v1"
	IntegerBoundaryObligationBoundary  = "allowed_boundary"
	IntegerBoundaryObligationRangeMin  = "range_minimum"
	IntegerBoundaryObligationRangeMax  = "range_maximum"
	IntegerBoundaryObligationRegion    = "constraint_region"
	TestManifestPurposeSample          = "sample"
	TestManifestPurposeTiny            = "tiny"
	TestManifestPurposeRandom          = "random"
	TestManifestPurposeBoundary        = "boundary"
	TestManifestPurposeExtreme         = "extreme"
	TestManifestPurposeComplexity      = "complexity"
	TestManifestPurposeMetamorphic     = "metamorphic"
)

// TestManifestV2 is additive. V1 remains unchanged for old workflow histories.
// Receipt fields bind the manifest to the independent-oracle, sanitizer, and
// deterministic boundary checks without embedding their potentially large data.
type TestManifestV2 struct {
	SchemaVersion                 string               `json:"schema_version"`
	SemanticSpecSHA256            string               `json:"semantic_spec_sha256"`
	AuthoringPlanSHA256           string               `json:"authoring_plan_sha256"`
	OraclePromotionReceiptSHA256  string               `json:"oracle_promotion_receipt_sha256"`
	SanitizerReceiptSHA256        string               `json:"sanitizer_receipt_sha256"`
	BoundaryCoverageReceiptSHA256 string               `json:"boundary_coverage_receipt_sha256"`
	TestCount                     int                  `json:"test_count"`
	Cases                         []TestManifestCaseV2 `json:"cases"`
}

type TestManifestCaseV2 struct {
	TestIndex                     int          `json:"test_index"`
	TestID                        string       `json:"test_id"`
	Purpose                       string       `json:"purpose"`
	ConstraintRegion              string       `json:"constraint_region"`
	BoundaryRefs                  []string     `json:"boundary_refs"`
	Seed                          *int64       `json:"seed"`
	InputArtifact                 *ArtifactRef `json:"input_artifact"`
	InputSHA256                   string       `json:"input_sha256"`
	OutputArtifact                *ArtifactRef `json:"output_artifact"`
	OutputSHA256                  string       `json:"output_sha256"`
	KilledWrongIDs                []string     `json:"killed_wrong_ids"`
	KilledWrongIDsRetentionReason string       `json:"killed_wrong_ids_retention_reason"`
}

func (manifest TestManifestV2) Validate() error {
	return validateTestManifestV2Core(manifest, true)
}

// validateTestManifestV2Core allows D to validate a pre-manifest before the
// boundary receipt exists. Every other binding remains mandatory. The final
// manifest must use Validate, which requires the computed receipt SHA.
func validateTestManifestV2Core(manifest TestManifestV2, requireBoundaryReceipt bool) error {
	if manifest.SchemaVersion != TestManifestSchemaVersionV2 {
		return fmt.Errorf("unsupported TestManifest schema %q", manifest.SchemaVersion)
	}
	for name, value := range map[string]string{
		"semantic_spec_sha256":            manifest.SemanticSpecSHA256,
		"authoring_plan_sha256":           manifest.AuthoringPlanSHA256,
		"oracle_promotion_receipt_sha256": manifest.OraclePromotionReceiptSHA256,
		"sanitizer_receipt_sha256":        manifest.SanitizerReceiptSHA256,
	} {
		if !isManifestSHA256(value) {
			return fmt.Errorf("%s is not a canonical SHA-256 digest", name)
		}
	}
	if requireBoundaryReceipt {
		if !isManifestSHA256(manifest.BoundaryCoverageReceiptSHA256) {
			return fmt.Errorf("boundary_coverage_receipt_sha256 is not a canonical SHA-256 digest")
		}
	} else if manifest.BoundaryCoverageReceiptSHA256 != "" && !isManifestSHA256(manifest.BoundaryCoverageReceiptSHA256) {
		return fmt.Errorf("boundary_coverage_receipt_sha256 is not empty or a canonical SHA-256 digest")
	}
	if manifest.TestCount <= 0 || manifest.TestCount != len(manifest.Cases) {
		return fmt.Errorf("test_count=%d does not match %d cases", manifest.TestCount, len(manifest.Cases))
	}
	previousID := ""
	for i, item := range manifest.Cases {
		if item.TestIndex != i {
			return fmt.Errorf("case %d records test_index=%d", i, item.TestIndex)
		}
		if item.TestID == "" || item.TestID != strings.TrimSpace(item.TestID) || (i > 0 && item.TestID <= previousID) {
			return fmt.Errorf("case %d test_id is empty, non-canonical, duplicated, or unsorted", i)
		}
		previousID = item.TestID
		if !validTestManifestPurposeV2(item.Purpose) {
			return fmt.Errorf("case %q has unsupported purpose %q", item.TestID, item.Purpose)
		}
		if item.ConstraintRegion == "" || item.ConstraintRegion != strings.TrimSpace(item.ConstraintRegion) {
			return fmt.Errorf("case %q has empty or non-canonical constraint_region", item.TestID)
		}
		if item.Seed == nil {
			return fmt.Errorf("case %q omits seed", item.TestID)
		}
		if item.BoundaryRefs == nil || !sortedUniqueCanonicalStringsV2(item.BoundaryRefs) {
			return fmt.Errorf("case %q boundary_refs must be a present sorted unique array", item.TestID)
		}
		if item.KilledWrongIDs == nil || !sortedUniqueCanonicalStringsV2(item.KilledWrongIDs) {
			return fmt.Errorf("case %q killed_wrong_ids must be a present sorted unique array", item.TestID)
		}
		if item.KilledWrongIDsRetentionReason == "" || item.KilledWrongIDsRetentionReason != strings.TrimSpace(item.KilledWrongIDsRetentionReason) {
			return fmt.Errorf("case %q killed_wrong_ids retention reason is required", item.TestID)
		}
		if err := validateManifestArtifactV2(item.TestID+" input", item.InputArtifact, item.InputSHA256); err != nil {
			return err
		}
		if err := validateManifestArtifactV2(item.TestID+" output", item.OutputArtifact, item.OutputSHA256); err != nil {
			return err
		}
	}
	return nil
}

func validTestManifestPurposeV2(value string) bool {
	switch value {
	case TestManifestPurposeSample, TestManifestPurposeTiny, TestManifestPurposeRandom,
		TestManifestPurposeBoundary, TestManifestPurposeExtreme, TestManifestPurposeComplexity,
		TestManifestPurposeMetamorphic:
		return true
	default:
		return false
	}
}

func sortedUniqueCanonicalStringsV2(values []string) bool {
	for i, value := range values {
		if value == "" || value != strings.TrimSpace(value) || (i > 0 && values[i-1] >= value) {
			return false
		}
	}
	return true
}

func validateManifestArtifactV2(label string, ref *ArtifactRef, digest string) error {
	if ref == nil || !isManifestSHA256(digest) {
		return fmt.Errorf("%s artifact or digest is missing", label)
	}
	if err := ref.Validate(ref.Bucket); err != nil {
		return fmt.Errorf("%s artifact is invalid: %w", label, err)
	}
	if ref.SHA256 != digest {
		return fmt.Errorf("%s artifact digest mismatch", label)
	}
	return nil
}

func CanonicalTestManifestV2JSON(manifest TestManifestV2) (json.RawMessage, string, error) {
	if err := manifest.Validate(); err != nil {
		return nil, "", err
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return nil, "", err
	}
	return encoded, sha256Hex(encoded), nil
}

func ParseTestManifestV2JSON(data []byte) (*TestManifestV2, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var manifest TestManifestV2
	if err := decoder.Decode(&manifest); err != nil {
		return nil, err
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return nil, err
	}
	if err := manifest.Validate(); err != nil {
		return nil, err
	}
	canonical, _, err := CanonicalTestManifestV2JSON(manifest)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(data, canonical) {
		return nil, fmt.Errorf("TestManifest v2 bytes are not canonical")
	}
	return &manifest, nil
}

// IntegerBoundaryCasePlanV1 is derived only from the accepted structured
// contracts. It is the deterministic list of obligations that D must satisfy;
// test metadata cannot silently redefine the required boundary surface.
type IntegerBoundaryCasePlanV1 struct {
	SchemaVersion       string                            `json:"schema_version"`
	RuleVersion         string                            `json:"rule_version"`
	SemanticSpecSHA256  string                            `json:"semantic_spec_sha256"`
	AuthoringPlanSHA256 string                            `json:"authoring_plan_sha256"`
	Obligations         []IntegerBoundaryCaseObligationV1 `json:"obligations"`
}

type IntegerBoundaryCaseObligationV1 struct {
	ID               string   `json:"id"`
	Kind             string   `json:"kind"`
	Subject          string   `json:"subject"`
	Dimension        string   `json:"dimension"`
	ConstraintRegion string   `json:"constraint_region"`
	BoundaryRefs     []string `json:"boundary_refs"`
	Value            *int64   `json:"value"`
}

// DeriveIntegerBoundaryCasePlanV1 derives allowed explicit boundaries
// (including every allowed zero), every closed range minimum/maximum, and one
// obligation for every AuthoringPlan TestIntent region. The output is sorted
// and byte-stable for identical accepted contracts.
func DeriveIntegerBoundaryCasePlanV1(specValue domain.SemanticSpecV1, plan domain.AuthoringPlanV1) (*IntegerBoundaryCasePlanV1, error) {
	if specValue.SchemaVersion != domain.SemanticSpecSchemaV1 || plan.SchemaVersion != domain.AuthoringPlanSchemaV1 {
		return nil, fmt.Errorf("unsupported SemanticSpec or AuthoringPlan schema")
	}
	if err := validateIntegerCoverageProfileV1(specValue); err != nil {
		return nil, err
	}
	specSHA := speccontract.SemanticSpecSHA256V1(specValue)
	planSHA, err := canonicalJSONSHA256(plan)
	if err != nil {
		return nil, err
	}
	if plan.SemanticSpecSHA256 != specSHA {
		return nil, fmt.Errorf("AuthoringPlan is not bound to the supplied SemanticSpec")
	}

	boundaryByID := make(map[string]domain.SemanticBoundaryV1, len(specValue.Boundaries))
	obligations := make([]IntegerBoundaryCaseObligationV1, 0, len(specValue.Boundaries)+len(specValue.Constraints)*2+len(plan.TestIntents))
	for _, boundary := range specValue.Boundaries {
		if boundary.ID == "" || boundary.ID != strings.TrimSpace(boundary.ID) {
			return nil, fmt.Errorf("boundary has an empty or non-canonical id")
		}
		if _, exists := boundaryByID[boundary.ID]; exists {
			return nil, fmt.Errorf("boundary id %q is duplicated", boundary.ID)
		}
		boundaryByID[boundary.ID] = boundary
		if !boundary.Allowed {
			continue
		}
		value := boundary.Value
		obligations = append(obligations, IntegerBoundaryCaseObligationV1{
			ID: "allowed_boundary:" + boundary.ID, Kind: IntegerBoundaryObligationBoundary,
			Subject: boundary.Symbol, Dimension: boundary.Measure, BoundaryRefs: []string{boundary.ID}, Value: &value,
		})
	}
	for _, constraint := range specValue.Constraints {
		if constraint.Subject == "" || constraint.Subject != strings.TrimSpace(constraint.Subject) || constraint.Min == nil || constraint.Max == nil {
			return nil, fmt.Errorf("constraint %s/%s has no canonical subject or closed range", constraint.Subject, constraint.Kind)
		}
		for _, edge := range []struct {
			kind  string
			label string
			value int64
		}{{IntegerBoundaryObligationRangeMin, "min", *constraint.Min}, {IntegerBoundaryObligationRangeMax, "max", *constraint.Max}} {
			value := edge.value
			obligations = append(obligations, IntegerBoundaryCaseObligationV1{
				ID: integerRangeObligationIDV1(constraint, edge.label), Kind: edge.kind,
				Subject: constraint.Subject, Dimension: constraint.Kind, BoundaryRefs: []string{}, Value: &value,
			})
		}
	}
	if len(plan.TestIntents) == 0 {
		return nil, fmt.Errorf("AuthoringPlan has no test intents")
	}
	for index, intent := range plan.TestIntents {
		if intent.ConstraintRegion == "" || intent.ConstraintRegion != strings.TrimSpace(intent.ConstraintRegion) {
			return nil, fmt.Errorf("test intent %d has an empty or non-canonical constraint region", index)
		}
		refs := append([]string{}, intent.BoundaryRefs...)
		sort.Strings(refs)
		if !sortedUniqueCanonicalStringsV2(refs) {
			return nil, fmt.Errorf("test intent %d boundary refs are empty, duplicated, or non-canonical", index)
		}
		for _, ref := range refs {
			boundary, exists := boundaryByID[ref]
			if !exists || !boundary.Allowed {
				return nil, fmt.Errorf("test intent %d references unknown or forbidden boundary %q", index, ref)
			}
		}
		identity, err := canonicalJSONSHA256(struct {
			Index            int      `json:"index"`
			ConstraintRegion string   `json:"constraint_region"`
			BoundaryRefs     []string `json:"boundary_refs"`
		}{index, intent.ConstraintRegion, refs})
		if err != nil {
			return nil, err
		}
		obligations = append(obligations, IntegerBoundaryCaseObligationV1{
			ID: "constraint_region:" + identity, Kind: IntegerBoundaryObligationRegion,
			ConstraintRegion: intent.ConstraintRegion, BoundaryRefs: refs,
		})
	}
	sort.Slice(obligations, func(i, j int) bool { return obligations[i].ID < obligations[j].ID })
	for i := 1; i < len(obligations); i++ {
		if obligations[i-1].ID == obligations[i].ID {
			return nil, fmt.Errorf("derived boundary obligation %q is duplicated", obligations[i].ID)
		}
	}
	return &IntegerBoundaryCasePlanV1{
		SchemaVersion: IntegerBoundaryCasePlanSchemaV1, RuleVersion: IntegerBoundaryCoverageRuleVersion,
		SemanticSpecSHA256: specSHA, AuthoringPlanSHA256: planSHA, Obligations: obligations,
	}, nil
}

func integerRangeObligationIDV1(constraint domain.SemanticConstraintV1, edge string) string {
	return fmt.Sprintf("range_edge:%s:%s:%s", constraint.Subject, constraint.Kind, edge)
}

func CanonicalIntegerBoundaryCasePlanV1(plan IntegerBoundaryCasePlanV1) (json.RawMessage, string, error) {
	if plan.SchemaVersion != IntegerBoundaryCasePlanSchemaV1 || plan.RuleVersion != IntegerBoundaryCoverageRuleVersion ||
		!isManifestSHA256(plan.SemanticSpecSHA256) || !isManifestSHA256(plan.AuthoringPlanSHA256) || len(plan.Obligations) == 0 {
		return nil, "", fmt.Errorf("invalid integer boundary case plan")
	}
	previousID := ""
	for _, obligation := range plan.Obligations {
		if obligation.ID == "" || obligation.ID != strings.TrimSpace(obligation.ID) || obligation.ID <= previousID ||
			obligation.BoundaryRefs == nil || !sortedUniqueCanonicalStringsV2(obligation.BoundaryRefs) {
			return nil, "", fmt.Errorf("invalid or unsorted integer boundary obligation")
		}
		previousID = obligation.ID
		switch obligation.Kind {
		case IntegerBoundaryObligationBoundary:
			if obligation.Subject == "" || obligation.Dimension == "" || obligation.ConstraintRegion != "" || len(obligation.BoundaryRefs) != 1 || obligation.Value == nil {
				return nil, "", fmt.Errorf("invalid allowed-boundary obligation %q", obligation.ID)
			}
		case IntegerBoundaryObligationRangeMin, IntegerBoundaryObligationRangeMax:
			if obligation.Subject == "" || obligation.Dimension == "" || obligation.ConstraintRegion != "" || len(obligation.BoundaryRefs) != 0 || obligation.Value == nil {
				return nil, "", fmt.Errorf("invalid range-edge obligation %q", obligation.ID)
			}
		case IntegerBoundaryObligationRegion:
			if obligation.Subject != "" || obligation.Dimension != "" || obligation.ConstraintRegion == "" || obligation.Value != nil {
				return nil, "", fmt.Errorf("invalid constraint-region obligation %q", obligation.ID)
			}
		default:
			return nil, "", fmt.Errorf("unknown integer boundary obligation kind %q", obligation.Kind)
		}
	}
	encoded, err := json.Marshal(plan)
	if err != nil {
		return nil, "", err
	}
	return encoded, sha256Hex(encoded), nil
}

// IntegerBoundaryCoverageCaseInputV1 supplies the exact resolved CAS bytes to D.
// It is not persisted in Temporal; the receipt binds their hashes instead.
type IntegerBoundaryCoverageCaseInputV1 struct {
	TestID string `json:"test_id"`
	Input  string `json:"input"`
}

type IntegerBoundaryCoverageReceiptV1 struct {
	SchemaVersion       string   `json:"schema_version"`
	RuleVersion         string   `json:"rule_version"`
	SemanticSpecSHA256  string   `json:"semantic_spec_sha256"`
	AuthoringPlanSHA256 string   `json:"authoring_plan_sha256"`
	DerivedPlanSHA256   string   `json:"derived_plan_sha256"`
	CasePlanSHA256      string   `json:"case_plan_sha256"`
	CoveredRegions      []string `json:"covered_regions"`
	CoveredBoundaryRefs []string `json:"covered_boundary_refs"`
	CoveredRangeEdges   []string `json:"covered_range_edges"`
}

// ValidateIntegerBoundaryCoverageV1 reparses exact input bytes and proves that
// metadata labels correspond to actual integer/integer-sequence values.
func ValidateIntegerBoundaryCoverageV1(
	specValue domain.SemanticSpecV1,
	plan domain.AuthoringPlanV1,
	manifest TestManifestV2,
	inputs []IntegerBoundaryCoverageCaseInputV1,
) (*IntegerBoundaryCoverageReceiptV1, error) {
	if err := validateTestManifestV2Core(manifest, false); err != nil {
		return nil, fmt.Errorf("invalid TestManifest v2: %w", err)
	}
	derivedPlan, err := DeriveIntegerBoundaryCasePlanV1(specValue, plan)
	if err != nil {
		return nil, fmt.Errorf("derive integer boundary plan: %w", err)
	}
	_, derivedPlanSHA, err := CanonicalIntegerBoundaryCasePlanV1(*derivedPlan)
	if err != nil {
		return nil, err
	}
	specSHA := derivedPlan.SemanticSpecSHA256
	planSHA := derivedPlan.AuthoringPlanSHA256
	if manifest.SemanticSpecSHA256 != specSHA || manifest.AuthoringPlanSHA256 != planSHA {
		return nil, fmt.Errorf("coverage inputs are not bound to the same SemanticSpec and AuthoringPlan")
	}
	if len(inputs) != len(manifest.Cases) {
		return nil, fmt.Errorf("resolved input count %d does not match %d cases", len(inputs), len(manifest.Cases))
	}
	inputByID := make(map[string]string, len(inputs))
	for _, input := range inputs {
		if input.TestID == "" || input.TestID != strings.TrimSpace(input.TestID) {
			return nil, fmt.Errorf("resolved input has an invalid test id")
		}
		if _, exists := inputByID[input.TestID]; exists {
			return nil, fmt.Errorf("resolved input %q is duplicated", input.TestID)
		}
		inputByID[input.TestID] = input.Input
	}

	boundaryByID := make(map[string]domain.SemanticBoundaryV1, len(specValue.Boundaries))
	boundaryCovered := make(map[string]bool, len(specValue.Boundaries))
	for _, boundary := range specValue.Boundaries {
		boundaryByID[boundary.ID] = boundary
	}
	regionCovered := make(map[string]bool)
	edgeCovered := make(map[string]bool)
	type regionCaseV1 struct {
		region string
		refs   map[string]bool
	}
	regionCases := make([]regionCaseV1, 0, len(manifest.Cases))
	casePlan := make([]interface{}, 0, len(manifest.Cases))
	for _, item := range manifest.Cases {
		raw, exists := inputByID[item.TestID]
		if !exists || sha256Hex([]byte(raw)) != item.InputSHA256 {
			return nil, fmt.Errorf("case %q resolved bytes do not match input artifact", item.TestID)
		}
		parsed, err := speccontract.ParseSampleInputV1(specValue, raw)
		if err != nil {
			return nil, fmt.Errorf("case %q does not satisfy the integer input contract: %w", item.TestID, err)
		}
		if parsed.CanonicalInput != raw {
			return nil, fmt.Errorf("case %q input is not canonical", item.TestID)
		}
		bindings := integerCoverageBindingMapV1(parsed.Bindings)
		regionCovered[item.ConstraintRegion] = true
		caseRefs := make(map[string]bool, len(item.BoundaryRefs))
		for _, ref := range item.BoundaryRefs {
			boundary, exists := boundaryByID[ref]
			if !exists || !boundary.Allowed {
				return nil, fmt.Errorf("case %q references unknown or forbidden boundary %q", item.TestID, ref)
			}
			if !integerBoundaryHitV1(boundary, bindings) {
				return nil, fmt.Errorf("case %q labels boundary %q but its input does not hit it", item.TestID, ref)
			}
			boundaryCovered[ref] = true
			caseRefs[ref] = true
		}
		regionCases = append(regionCases, regionCaseV1{region: item.ConstraintRegion, refs: caseRefs})
		for _, constraint := range specValue.Constraints {
			if constraint.Min == nil || constraint.Max == nil {
				return nil, fmt.Errorf("constraint %s/%s has no closed range", constraint.Subject, constraint.Kind)
			}
			for _, edge := range []struct {
				name  string
				value int64
			}{{"min", *constraint.Min}, {"max", *constraint.Max}} {
				key := integerRangeObligationIDV1(constraint, edge.name)
				if integerConstraintEdgeHitV1(constraint, edge.value, bindings) {
					edgeCovered[key] = true
				}
			}
		}
		casePlan = append(casePlan, struct {
			TestID           string   `json:"test_id"`
			Purpose          string   `json:"purpose"`
			ConstraintRegion string   `json:"constraint_region"`
			BoundaryRefs     []string `json:"boundary_refs"`
			Seed             int64    `json:"seed"`
			InputSHA256      string   `json:"input_sha256"`
		}{item.TestID, item.Purpose, item.ConstraintRegion, item.BoundaryRefs, *item.Seed, item.InputSHA256})
	}

	wantEdges := make([]string, 0, len(specValue.Constraints)*2)
	for _, obligation := range derivedPlan.Obligations {
		switch obligation.Kind {
		case IntegerBoundaryObligationBoundary:
			ref := obligation.BoundaryRefs[0]
			if !boundaryCovered[ref] {
				return nil, fmt.Errorf("derived allowed boundary obligation %q is not covered", obligation.ID)
			}
		case IntegerBoundaryObligationRangeMin, IntegerBoundaryObligationRangeMax:
			if !edgeCovered[obligation.ID] {
				return nil, fmt.Errorf("derived range-edge obligation %q is not covered", obligation.ID)
			}
			wantEdges = append(wantEdges, obligation.ID)
		case IntegerBoundaryObligationRegion:
			covered := false
			for _, candidate := range regionCases {
				if candidate.region != obligation.ConstraintRegion {
					continue
				}
				covered = true
				for _, ref := range obligation.BoundaryRefs {
					if !candidate.refs[ref] {
						covered = false
						break
					}
				}
				if covered {
					break
				}
			}
			if !covered {
				return nil, fmt.Errorf("derived constraint-region obligation %q has no single matching case", obligation.ID)
			}
		default:
			return nil, fmt.Errorf("derived plan contains unsupported obligation %q", obligation.Kind)
		}
	}
	regions := sortedTrueKeysV1(regionCovered)
	boundaries := sortedTrueKeysV1(boundaryCovered)
	sort.Strings(wantEdges)
	casePlanSHA, err := canonicalJSONSHA256(casePlan)
	if err != nil {
		return nil, err
	}
	return &IntegerBoundaryCoverageReceiptV1{
		SchemaVersion: IntegerBoundaryCoverageSchemaV1, RuleVersion: IntegerBoundaryCoverageRuleVersion,
		SemanticSpecSHA256: specSHA, AuthoringPlanSHA256: planSHA, DerivedPlanSHA256: derivedPlanSHA, CasePlanSHA256: casePlanSHA,
		CoveredRegions: regions, CoveredBoundaryRefs: boundaries, CoveredRangeEdges: wantEdges,
	}, nil
}

func validateIntegerCoverageProfileV1(specValue domain.SemanticSpecV1) error {
	for _, symbol := range specValue.Symbols {
		if symbol.Scope != domain.SemanticSymbolScopeInput {
			continue
		}
		if symbol.Type != domain.SemanticSymbolInteger && symbol.Type != domain.SemanticSymbolIntegerSequence {
			return fmt.Errorf("integer coverage profile does not support input symbol %q type %q", symbol.Name, symbol.Type)
		}
	}
	for _, constraint := range specValue.Constraints {
		switch constraint.Kind {
		case domain.SemanticConstraintIntegerRange, domain.SemanticConstraintLengthRange, domain.SemanticConstraintElementRange:
		default:
			return fmt.Errorf("integer coverage profile does not support constraint kind %q", constraint.Kind)
		}
	}
	return nil
}

func integerCoverageBindingMapV1(bindings []speccontract.SampleInputBindingV1) map[string]speccontract.SampleInputBindingV1 {
	result := make(map[string]speccontract.SampleInputBindingV1, len(bindings))
	for _, binding := range bindings {
		result[binding.Symbol] = binding
	}
	return result
}

func integerBoundaryHitV1(boundary domain.SemanticBoundaryV1, bindings map[string]speccontract.SampleInputBindingV1) bool {
	binding, exists := bindings[boundary.Symbol]
	if !exists {
		return false
	}
	switch boundary.Measure {
	case domain.SemanticBoundaryMeasureValue:
		return binding.Integer != nil && *binding.Integer == boundary.Value
	case domain.SemanticBoundaryMeasureLength:
		return int64(len(binding.Integers)) == boundary.Value
	case domain.SemanticBoundaryMeasureElementValue:
		for _, value := range binding.Integers {
			if value == boundary.Value {
				return true
			}
		}
	}
	return false
}

func integerConstraintEdgeHitV1(constraint domain.SemanticConstraintV1, value int64, bindings map[string]speccontract.SampleInputBindingV1) bool {
	binding, exists := bindings[constraint.Subject]
	if !exists {
		return false
	}
	switch constraint.Kind {
	case domain.SemanticConstraintIntegerRange:
		return binding.Integer != nil && *binding.Integer == value
	case domain.SemanticConstraintLengthRange:
		return int64(len(binding.Integers)) == value
	case domain.SemanticConstraintElementRange:
		for _, item := range binding.Integers {
			if item == value {
				return true
			}
		}
	}
	return false
}

func sortedTrueKeysV1(values map[string]bool) []string {
	result := make([]string, 0, len(values))
	for key, covered := range values {
		if covered {
			result = append(result, key)
		}
	}
	sort.Strings(result)
	return result
}

func CanonicalIntegerBoundaryCoverageReceiptV1(receipt IntegerBoundaryCoverageReceiptV1) (json.RawMessage, string, error) {
	if receipt.SchemaVersion != IntegerBoundaryCoverageSchemaV1 || receipt.RuleVersion != IntegerBoundaryCoverageRuleVersion ||
		!isManifestSHA256(receipt.SemanticSpecSHA256) || !isManifestSHA256(receipt.AuthoringPlanSHA256) ||
		!isManifestSHA256(receipt.DerivedPlanSHA256) || !isManifestSHA256(receipt.CasePlanSHA256) ||
		!sortedUniqueCanonicalStringsV2(receipt.CoveredRegions) || !sortedUniqueCanonicalStringsV2(receipt.CoveredBoundaryRefs) || !sortedUniqueCanonicalStringsV2(receipt.CoveredRangeEdges) {
		return nil, "", fmt.Errorf("invalid integer boundary coverage receipt")
	}
	encoded, err := json.Marshal(receipt)
	if err != nil {
		return nil, "", err
	}
	return encoded, sha256Hex(encoded), nil
}
