package diversity

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
)

const StructuralSignatureSchemaV1 = "algoforge.s5-structural-signature.v1"

// StructuralSignatureV1 is a selection-only projection. It is not a quality
// gate and does not replace the V1 CAS identity signature.
type StructuralSignatureV1 struct {
	SchemaVersion        string                   `json:"schema_version"`
	CanonicalizerVersion string                   `json:"canonicalizer_version"`
	ExtractionConfidence float64                  `json:"extraction_confidence"`
	CoarseFamily         StructuralCoarseFamilyV1 `json:"coarse_family"`
	FineMechanic         StructuralFineMechanicV1 `json:"fine_mechanic"`
	WeakSignals          StructuralWeakSignalsV1  `json:"weak_signals"`
	SHA256               string                   `json:"sha256"`
}

type StructuralCoarseFamilyV1 struct {
	ProblemMode    string `json:"problem_mode"`
	InputObject    string `json:"input_object"`
	Topology       string `json:"topology"`
	OperationModel string `json:"operation_model"`
	Objective      string `json:"objective"`
}

type StructuralFineMechanicV1 struct {
	StateDimensions       []string `json:"state_dimensions"`
	TransitionOrInvariant string   `json:"transition_or_invariant"`
	SolutionOperatorSeq   []string `json:"solution_operator_sequence"`
	OutputForm            string   `json:"output_form"`
}

type StructuralWeakSignalsV1 struct {
	ComplexityClass       string   `json:"complexity_class"`
	ConstraintRegime      string   `json:"constraint_regime"`
	WrongSolutionFamilies []string `json:"wrong_solution_families"`
}

func NewStructuralSignatureV1(spec ConceptSpecV1) (StructuralSignatureV1, error) {
	canonical := canonicalConceptSpecV1(spec)
	if err := canonical.Validate(); err != nil {
		return StructuralSignatureV1{}, err
	}
	signature := StructuralSignatureV1{
		SchemaVersion:        StructuralSignatureSchemaV1,
		CanonicalizerVersion: ConceptCanonicalizerVersionV1,
		ExtractionConfidence: canonical.ExtractionConfidence,
		CoarseFamily: StructuralCoarseFamilyV1{
			ProblemMode: canonical.ProblemMode, InputObject: canonical.InputObject,
			Topology: canonical.Topology, OperationModel: canonical.OperationModel, Objective: canonical.Objective,
		},
		FineMechanic: StructuralFineMechanicV1{
			StateDimensions:       append([]string(nil), canonical.StateDimensions...),
			TransitionOrInvariant: canonical.TransitionOrInvariant,
			SolutionOperatorSeq:   append([]string(nil), canonical.SolutionOperatorSeq...),
			OutputForm:            canonical.OutputForm,
		},
		WeakSignals: StructuralWeakSignalsV1{
			ComplexityClass:       canonical.ComplexityClass,
			ConstraintRegime:      canonical.ConstraintRegime,
			WrongSolutionFamilies: append([]string(nil), canonical.WrongSolutionFamilies...),
		},
	}
	digest, err := structuralSignatureDigestV1(signature)
	if err != nil {
		return StructuralSignatureV1{}, err
	}
	signature.SHA256 = digest
	return signature, nil
}

func (signature StructuralSignatureV1) Validate() error {
	if signature.SchemaVersion != StructuralSignatureSchemaV1 ||
		signature.CanonicalizerVersion != ConceptCanonicalizerVersionV1 {
		return errors.New("structural signature schema or canonicalizer version is invalid")
	}
	if math.IsNaN(signature.ExtractionConfidence) || math.IsInf(signature.ExtractionConfidence, 0) ||
		signature.ExtractionConfidence < 0 || signature.ExtractionConfidence > 1 {
		return errors.New("structural signature extraction_confidence is invalid")
	}
	for name, value := range map[string]string{
		"problem_mode":            signature.CoarseFamily.ProblemMode,
		"input_object":            signature.CoarseFamily.InputObject,
		"topology":                signature.CoarseFamily.Topology,
		"operation_model":         signature.CoarseFamily.OperationModel,
		"objective":               signature.CoarseFamily.Objective,
		"transition_or_invariant": signature.FineMechanic.TransitionOrInvariant,
		"output_form":             signature.FineMechanic.OutputForm,
		"complexity_class":        signature.WeakSignals.ComplexityClass,
		"constraint_regime":       signature.WeakSignals.ConstraintRegime,
	} {
		if value == "" {
			return fmt.Errorf("structural signature %s is empty", name)
		}
	}
	for name, values := range map[string][]string{
		"state_dimensions":        signature.FineMechanic.StateDimensions,
		"wrong_solution_families": signature.WeakSignals.WrongSolutionFamilies,
	} {
		if len(values) == 0 {
			return fmt.Errorf("structural signature %s is empty", name)
		}
		if err := validateCanonicalStringSet(name, values); err != nil {
			return err
		}
	}
	if len(signature.FineMechanic.SolutionOperatorSeq) == 0 {
		return errors.New("structural signature solution_operator_sequence is empty")
	}
	if err := validateCanonicalStringList("solution_operator_sequence", signature.FineMechanic.SolutionOperatorSeq); err != nil {
		return err
	}
	if !sha256Pattern.MatchString(signature.SHA256) {
		return errors.New("structural signature SHA-256 is invalid")
	}
	want, err := structuralSignatureDigestV1(signature)
	if err != nil {
		return err
	}
	if signature.SHA256 != want {
		return errors.New("structural signature SHA-256 does not match its canonical descriptor")
	}
	return nil
}

func structuralSignatureDigestV1(signature StructuralSignatureV1) (string, error) {
	descriptor := struct {
		SchemaVersion        string                   `json:"schema_version"`
		CanonicalizerVersion string                   `json:"canonicalizer_version"`
		ExtractionConfidence float64                  `json:"extraction_confidence"`
		CoarseFamily         StructuralCoarseFamilyV1 `json:"coarse_family"`
		FineMechanic         StructuralFineMechanicV1 `json:"fine_mechanic"`
		WeakSignals          StructuralWeakSignalsV1  `json:"weak_signals"`
	}{
		SchemaVersion: signature.SchemaVersion, CanonicalizerVersion: signature.CanonicalizerVersion,
		ExtractionConfidence: signature.ExtractionConfidence, CoarseFamily: signature.CoarseFamily,
		FineMechanic: signature.FineMechanic, WeakSignals: signature.WeakSignals,
	}
	encoded, err := json.Marshal(descriptor)
	if err != nil {
		return "", err
	}
	return SHA256Hex(encoded), nil
}

// StructuralDistanceV1 is an integer-only max-min selector distance. Unknown
// fields and weak signals contribute no distance, preventing missing facts or
// story/style changes from being rewarded as diversity.
func StructuralDistanceV1(left, right StructuralSignatureV1) (int, error) {
	if err := left.Validate(); err != nil {
		return 0, fmt.Errorf("left signature: %w", err)
	}
	if err := right.Validate(); err != nil {
		return 0, fmt.Errorf("right signature: %w", err)
	}
	distance := 0
	for _, pair := range [][2]string{
		{left.CoarseFamily.ProblemMode, right.CoarseFamily.ProblemMode},
		{left.CoarseFamily.InputObject, right.CoarseFamily.InputObject},
		{left.CoarseFamily.Topology, right.CoarseFamily.Topology},
		{left.CoarseFamily.OperationModel, right.CoarseFamily.OperationModel},
		{left.CoarseFamily.Objective, right.CoarseFamily.Objective},
	} {
		if differentKnownV1(pair[0], pair[1]) {
			distance += 2
		}
	}
	if differentKnownSliceV1(left.FineMechanic.StateDimensions, right.FineMechanic.StateDimensions) {
		distance += 4
	}
	if differentKnownV1(left.FineMechanic.TransitionOrInvariant, right.FineMechanic.TransitionOrInvariant) {
		distance += 4
	}
	if differentKnownSliceV1(left.FineMechanic.SolutionOperatorSeq, right.FineMechanic.SolutionOperatorSeq) {
		distance += 4
	}
	if differentKnownV1(left.FineMechanic.OutputForm, right.FineMechanic.OutputForm) {
		distance += 4
	}
	return distance, nil
}

func differentKnownV1(left, right string) bool {
	return left != UnknownValue && right != UnknownValue && left != right
}

func differentKnownSliceV1(left, right []string) bool {
	if slices.Contains(left, UnknownValue) || slices.Contains(right, UnknownValue) {
		return false
	}
	return !slices.Equal(left, right)
}
