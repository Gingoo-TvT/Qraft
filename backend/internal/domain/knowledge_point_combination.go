package domain

import (
	"fmt"
	"sort"
	"strings"
)

const (
	KnowledgePointCombinationSchemaV1 = "algoforge.knowledge-point-combination.v1"
	KnowledgePointConformanceSchemaV1 = "algoforge.knowledge-point-conformance.v1"

	KnowledgePointCombinationSingle   = "single"
	KnowledgePointCombinationSet      = "set"
	KnowledgePointCombinationSequence = "sequence"
	KnowledgePointCombinationMixed    = "mixed"
)

// KnowledgePointCombinationContract gives jobs v1 knowledge-point requests
// stable semantics after they enter the Temporal workflow. Legacy workflows
// omit this pointer and retain their historical tag behavior.
type KnowledgePointCombinationContract struct {
	SchemaVersion string `json:"schema_version"`
	Mode          string `json:"mode"`
	MaxConcepts   int    `json:"max_concepts"`
}

// CanonicalKnowledgePointSlugs normalizes stable tag_name slugs while
// preserving the ordering semantics of sequence and mixed combinations.
func CanonicalKnowledgePointSlugs(mode string, values []string) ([]string, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if !isKnowledgePointCombinationMode(mode) {
		return nil, fmt.Errorf("unsupported knowledge-point combination mode %q", mode)
	}

	result := make([]string, len(values))
	seen := make(map[string]struct{}, len(values))
	for i, raw := range values {
		value := strings.ToLower(strings.TrimSpace(raw))
		if value == "" {
			return nil, fmt.Errorf("knowledge-point slugs contain an empty value")
		}
		if _, ok := seen[value]; ok {
			return nil, fmt.Errorf("knowledge-point slugs contain duplicate value %q", value)
		}
		seen[value] = struct{}{}
		result[i] = value
	}

	switch mode {
	case KnowledgePointCombinationSet:
		sort.Strings(result)
	case KnowledgePointCombinationMixed:
		if len(result) > 1 {
			sort.Strings(result[1:])
		}
	}
	return result, nil
}

func (contract *KnowledgePointCombinationContract) Validate(required []string) error {
	if contract == nil {
		return nil
	}
	if contract.SchemaVersion != KnowledgePointCombinationSchemaV1 {
		return fmt.Errorf("unsupported knowledge-point combination schema %q", contract.SchemaVersion)
	}
	canonical, err := CanonicalKnowledgePointSlugs(contract.Mode, required)
	if err != nil {
		return err
	}
	if len(canonical) == 0 {
		return fmt.Errorf("knowledge-point combination requires at least one slug")
	}
	for i := range canonical {
		if canonical[i] != required[i] {
			return fmt.Errorf("knowledge-point slugs are not canonical for %s mode", contract.Mode)
		}
	}
	if contract.Mode == KnowledgePointCombinationSingle && len(required) != 1 {
		return fmt.Errorf("single knowledge-point combination requires exactly one slug")
	}
	if contract.Mode == KnowledgePointCombinationMixed && len(required) < 2 {
		return fmt.Errorf("mixed knowledge-point combination requires a primary slug and at least one auxiliary slug")
	}
	if contract.MaxConcepts < len(required) {
		return fmt.Errorf("knowledge-point max_concepts %d is below required slug count %d", contract.MaxConcepts, len(required))
	}
	return nil
}

// Conform validates the model-reported tags against the exact requested
// combination and returns their canonical representation for persistence.
func (contract *KnowledgePointCombinationContract) Conform(required, observed []string) ([]string, error) {
	if contract == nil {
		return append([]string(nil), observed...), nil
	}
	if err := contract.Validate(required); err != nil {
		return nil, err
	}
	canonical, err := CanonicalKnowledgePointSlugs(contract.Mode, observed)
	if err != nil {
		return nil, err
	}
	if len(canonical) != len(required) {
		return nil, fmt.Errorf("model reported %d knowledge-point slugs, want %d", len(canonical), len(required))
	}
	for i := range required {
		if canonical[i] != required[i] {
			return nil, fmt.Errorf("model knowledge-point slugs do not match the requested %s combination", contract.Mode)
		}
	}
	return canonical, nil
}

func isKnowledgePointCombinationMode(mode string) bool {
	switch mode {
	case KnowledgePointCombinationSingle,
		KnowledgePointCombinationSet,
		KnowledgePointCombinationSequence,
		KnowledgePointCombinationMixed:
		return true
	default:
		return false
	}
}
