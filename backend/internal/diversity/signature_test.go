package diversity

import (
	"reflect"
	"testing"
)

func TestStructuralSignatureCanonicalAndSelectionOnlyDistance(t *testing.T) {
	firstSpec := testConceptSpecV1(QualityTierViable, "graph")
	secondSpec := firstSpec
	secondSpec.StateDimensions = []string{"position", "prefix"}
	secondSpec.WrongSolutionFamilies = []string{"overflow", "off by one"}

	first, err := NewStructuralSignatureV1(firstSpec)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewStructuralSignatureV1(secondSpec)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("canonical signatures differ:\n%+v\n%+v", first, second)
	}

	weakOnly := first
	weakOnly.WeakSignals.ComplexityClass = "quadratic"
	digest, err := structuralSignatureDigestV1(weakOnly)
	if err != nil {
		t.Fatal(err)
	}
	weakOnly.SHA256 = digest
	distance, err := StructuralDistanceV1(first, weakOnly)
	if err != nil {
		t.Fatal(err)
	}
	if distance != 0 {
		t.Fatalf("weak signals changed selector distance: %d", distance)
	}

	fineDifferentSpec := firstSpec
	fineDifferentSpec.TransitionOrInvariant = "maintain a disjoint-set component invariant"
	fineDifferent, err := NewStructuralSignatureV1(fineDifferentSpec)
	if err != nil {
		t.Fatal(err)
	}
	distance, err = StructuralDistanceV1(first, fineDifferent)
	if err != nil || distance == 0 {
		t.Fatalf("fine mechanic distance=%d err=%v", distance, err)
	}

	operatorOrderSpec := firstSpec
	operatorOrderSpec.SolutionOperatorSeq = []string{"aggregate", "scan"}
	operatorOrder, err := NewStructuralSignatureV1(operatorOrderSpec)
	if err != nil {
		t.Fatal(err)
	}
	distance, err = StructuralDistanceV1(first, operatorOrder)
	if err != nil || distance == 0 {
		t.Fatalf("operator order was not preserved: distance=%d err=%v", distance, err)
	}
}

func TestStructuralDistanceDoesNotRewardUnknown(t *testing.T) {
	knownSpec := testConceptSpecV1(QualityTierViable, "graph")
	unknownSpec := knownSpec
	unknownSpec.Topology = UnknownValue
	unknownSpec.TransitionOrInvariant = UnknownValue
	known, err := NewStructuralSignatureV1(knownSpec)
	if err != nil {
		t.Fatal(err)
	}
	unknown, err := NewStructuralSignatureV1(unknownSpec)
	if err != nil {
		t.Fatal(err)
	}
	distance, err := StructuralDistanceV1(known, unknown)
	if err != nil {
		t.Fatal(err)
	}
	if distance != 0 {
		t.Fatalf("unknown extraction was rewarded with distance %d", distance)
	}

	tampered := known
	tampered.SHA256 = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	if err := tampered.Validate(); err == nil {
		t.Fatal("tampered structural signature was accepted")
	}
}
