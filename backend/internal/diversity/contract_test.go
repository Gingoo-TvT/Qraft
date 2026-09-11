package diversity

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestConceptPoolCanonicalTwoByTwoAllowsUnknown(t *testing.T) {
	pool := testConceptPoolV1(0)
	pool.Attempts[0].Concepts[0].Spec.Topology = " UNKNOWN "
	first, firstSHA, err := CanonicalConceptPoolV1(pool)
	if err != nil {
		t.Fatalf("CanonicalConceptPoolV1: %v", err)
	}

	reordered := clonePoolV1(t, pool)
	reordered.Attempts[0], reordered.Attempts[1] = reordered.Attempts[1], reordered.Attempts[0]
	for index := range reordered.Attempts {
		reordered.Attempts[index].Concepts[0], reordered.Attempts[index].Concepts[1] =
			reordered.Attempts[index].Concepts[1], reordered.Attempts[index].Concepts[0]
	}
	second, secondSHA, err := CanonicalConceptPoolV1(reordered)
	if err != nil {
		t.Fatalf("CanonicalConceptPoolV1 reordered: %v", err)
	}
	if !bytes.Equal(first, second) || firstSHA != secondSHA {
		t.Fatalf("canonical pool drifted:\n%s\n%s", first, second)
	}
	var canonical ConceptPoolV1
	if err := json.Unmarshal(first, &canonical); err != nil {
		t.Fatal(err)
	}
	if len(canonical.Attempts) != 2 || len(canonical.Attempts[0].Concepts) != 2 ||
		canonical.Attempts[0].Concepts[0].Spec.Topology != UnknownValue {
		t.Fatalf("canonical pool=%+v", canonical)
	}
}

func TestConceptPoolRejectsIdentityShapeReceiptAndBudgetDrift(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ConceptPoolV1)
	}{
		{name: "one attempt", mutate: func(pool *ConceptPoolV1) { pool.Attempts = pool.Attempts[:1] }},
		{name: "one concept", mutate: func(pool *ConceptPoolV1) { pool.Attempts[0].Concepts = pool.Attempts[0].Concepts[:1] }},
		{name: "duplicate logical attempt", mutate: func(pool *ConceptPoolV1) { pool.Attempts[1].LogicalAttemptID = pool.Attempts[0].LogicalAttemptID }},
		{name: "duplicate concept id", mutate: func(pool *ConceptPoolV1) {
			pool.Attempts[1].Concepts[1].ConceptID = pool.Attempts[0].Concepts[0].ConceptID
		}},
		{name: "duplicate pitch", mutate: func(pool *ConceptPoolV1) {
			pool.Attempts[1].Concepts[1].OneParagraphPitch = pool.Attempts[0].Concepts[0].OneParagraphPitch
		}},
		{name: "missing receipt", mutate: func(pool *ConceptPoolV1) { pool.Attempts[0].Receipt.SHA256 = "" }},
		{name: "missing normalization receipt", mutate: func(pool *ConceptPoolV1) { pool.Normalization.Receipt.SHA256 = "" }},
		{name: "retry counted as creative call", mutate: func(pool *ConceptPoolV1) { pool.Attempts[0].Usage.ModelCalls = 2 }},
		{name: "normalization call budget exceeded", mutate: func(pool *ConceptPoolV1) {
			pool.Budget.MaxModelCalls = 3
			pool.Normalization.Usage.NetworkRetries = 1
			pool.Normalization.Usage.ModelCalls = 2
		}},
		{name: "normalization retry budget exceeded", mutate: func(pool *ConceptPoolV1) {
			pool.Budget.MaxModelCalls = 10
			pool.Normalization.Usage.NetworkRetries = 3
			pool.Normalization.Usage.ModelCalls = 4
		}},
		{name: "normalization token budget exceeded", mutate: func(pool *ConceptPoolV1) {
			pool.Normalization.Usage.Tokens = 9801
		}},
		{name: "normalization wall budget exceeded", mutate: func(pool *ConceptPoolV1) {
			pool.Normalization.Usage.WallMilliseconds = 9901
		}},
		{name: "budget exceeded", mutate: func(pool *ConceptPoolV1) {
			pool.Attempts[0].Usage.NetworkRetries = 2
			pool.Attempts[0].Usage.ModelCalls = 3
			pool.Attempts[1].Usage.NetworkRetries = 2
			pool.Attempts[1].Usage.ModelCalls = 3
		}},
		{name: "invalid tier", mutate: func(pool *ConceptPoolV1) { pool.Attempts[0].Concepts[0].Spec.QualityTier = "score-0.9" }},
		{name: "unknown schema", mutate: func(pool *ConceptPoolV1) { pool.SchemaVersion = "v2" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pool := testConceptPoolV1(0)
			test.mutate(&pool)
			if _, _, err := CanonicalConceptPoolV1(pool); err == nil {
				t.Fatalf("invalid pool was accepted: %+v", pool)
			}
		})
	}
}

func TestDedupObservationDistinguishesSuccessfulZeroAndCheckFailed(t *testing.T) {
	zero := 0
	success := testDedupObservationV1()
	success.NeighborCount = &zero
	first, firstSHA, err := CanonicalDedupObservationV1(success)
	if err != nil {
		t.Fatalf("successful zero: %v", err)
	}
	if !strings.Contains(string(first), `"neighbor_count":0`) {
		t.Fatalf("successful zero did not preserve an explicit count: %s", first)
	}
	second, secondSHA, err := CanonicalDedupObservationV1(success)
	if err != nil || !bytes.Equal(first, second) || firstSHA != secondSHA {
		t.Fatalf("successful zero is not deterministic: err=%v", err)
	}

	failed := testDedupObservationV1()
	failed.Decision = DedupDecisionCheckFailed
	failed.ModelVersion = ""
	failed.NeighborCount = nil
	failed.Reason = "embedding provider unavailable"
	encoded, _, err := CanonicalDedupObservationV1(failed)
	if err != nil {
		t.Fatalf("check_failed: %v", err)
	}
	if strings.Contains(string(encoded), "neighbor_count") || strings.Contains(string(encoded), "neighbors") {
		t.Fatalf("check_failed forged neighbor evidence: %s", encoded)
	}
}

func TestDedupObservationRejectsFalseZeroOrderAndLineageLeak(t *testing.T) {
	zero := 0
	one := 1
	neighborID := "11111111-1111-4111-8111-111111111111"
	otherID := "22222222-2222-4222-8222-222222222222"
	neighbor := DedupNeighborV1{
		Rank: 1, ProblemID: neighborID, ContentHash: strings.Repeat("c", 64),
		CorpusTier: CorpusTierSelectionCandidate, Similarity: 0.8,
	}
	tests := []struct {
		name   string
		mutate func(*DedupObservationV1)
	}{
		{name: "successful missing count", mutate: func(value *DedupObservationV1) { value.NeighborCount = nil }},
		{name: "failed explicit zero", mutate: func(value *DedupObservationV1) {
			value.Decision = DedupDecisionCheckFailed
			value.NeighborCount = &zero
			value.Reason = "provider unavailable"
		}},
		{name: "warn zero", mutate: func(value *DedupObservationV1) { value.Decision = DedupDecisionWarn; value.NeighborCount = &zero }},
		{name: "count mismatch", mutate: func(value *DedupObservationV1) { value.NeighborCount = &one }},
		{name: "lineage leak", mutate: func(value *DedupObservationV1) {
			value.NeighborCount = &one
			value.Neighbors = []DedupNeighborV1{neighbor}
			value.ExcludedLineageIDs = []string{neighborID}
		}},
		{name: "ascending similarity", mutate: func(value *DedupObservationV1) {
			two := 2
			value.NeighborCount = &two
			value.Neighbors = []DedupNeighborV1{neighbor, {Rank: 2, ProblemID: otherID, ContentHash: strings.Repeat("d", 64), CorpusTier: CorpusTierHistoricalAdvisory, Similarity: 0.9}}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := testDedupObservationV1()
			value.NeighborCount = &zero
			test.mutate(&value)
			if _, _, err := CanonicalDedupObservationV1(value); err == nil {
				t.Fatalf("invalid observation was accepted: %+v", value)
			}
		})
	}
}

func testConceptPoolV1(slotIndex int) ConceptPoolV1 {
	pool := ConceptPoolV1{
		SchemaVersion:  ConceptPoolSchemaV1,
		BatchID:        "batch-s5-v1",
		SlotIndex:      slotIndex,
		SlotID:         "slot-" + string(rune('a'+slotIndex)),
		BriefSHA256:    strings.Repeat("a", 64),
		CorpusRevision: strings.Repeat("b", 64),
		Budget:         ConceptBudgetV1{MaxCreativeAttempts: 2, MaxConcepts: 4, MaxModelCalls: 4, MaxNetworkRetries: 2, MaxTokens: 10000, MaxWallMilliseconds: 10000},
		Attempts:       make([]ConceptAttemptV1, 2),
		Normalization: ConceptNormalizationV1{
			NormalizerVersion: "algoforge.s5-concept-normalizer.v1",
			Receipt:           ArtifactRefV1{SHA256: strings.Repeat("f", 64), URI: "cas://receipt/" + "slot-" + string(rune('a'+slotIndex)) + "/normalization"},
			Usage:             ConceptAttemptUsageV1{ModelCalls: 1, Tokens: 100, WallMilliseconds: 50},
		},
	}
	for attemptIndex := 0; attemptIndex < 2; attemptIndex++ {
		attempt := ConceptAttemptV1{
			AttemptIndex:     attemptIndex,
			LogicalAttemptID: pool.SlotID + "-attempt-" + string(rune('0'+attemptIndex)),
			Receipt:          ArtifactRefV1{SHA256: strings.Repeat(string(rune('c'+slotIndex+attemptIndex)), 64), URI: "cas://receipt/" + pool.SlotID + "/" + string(rune('0'+attemptIndex))},
			Usage:            ConceptAttemptUsageV1{ModelCalls: 1, Tokens: 100, WallMilliseconds: 50},
			Concepts:         make([]ConceptCardV1, 2),
		}
		for conceptIndex := 0; conceptIndex < 2; conceptIndex++ {
			ordinal := attemptIndex*2 + conceptIndex
			id := pool.SlotID + "-concept-" + string(rune('0'+ordinal))
			attempt.Concepts[conceptIndex] = ConceptCardV1{
				ConceptIndex:        conceptIndex,
				ConceptID:           id,
				OneParagraphPitch:   "Distinct pitch for " + id,
				UnresolvedQuestions: []string{"Boundary semantics", "Tie handling"},
				Spec:                testConceptSpecV1(QualityTierViable, "family-"+string(rune('a'+ordinal))),
			}
		}
		pool.Attempts[attemptIndex] = attempt
	}
	return pool
}

func testConceptSpecV1(tier, family string) ConceptSpecV1 {
	return ConceptSpecV1{
		SchemaVersion:         ConceptSpecSchemaV1,
		CanonicalizerVersion:  ConceptCanonicalizerVersionV1,
		ExtractionConfidence:  0.8,
		QualityTier:           tier,
		ProblemMode:           "static",
		InputObject:           family,
		Topology:              "line",
		OperationModel:        "offline",
		Objective:             "count",
		StateDimensions:       []string{"prefix", "position"},
		TransitionOrInvariant: family + " invariant",
		SolutionOperatorSeq:   []string{"scan", "aggregate"},
		OutputForm:            "integer",
		ComplexityClass:       "linear",
		ConstraintRegime:      "n up to 200000",
		WrongSolutionFamilies: []string{"off by one", "overflow"},
	}
}

func testDedupObservationV1() DedupObservationV1 {
	return DedupObservationV1{
		SchemaVersion:  DedupObservationSchemaV1,
		Stage:          DedupStageConceptSelection,
		ModelVersion:   "statement-model-v1",
		Kind:           DedupKindStatement,
		ContentHash:    strings.Repeat("a", 64),
		CorpusRevision: strings.Repeat("b", 64),
		RequestedTopK:  8,
		Threshold:      0.76,
		Decision:       DedupDecisionPass,
	}
}

func clonePoolV1(t *testing.T, pool ConceptPoolV1) ConceptPoolV1 {
	t.Helper()
	encoded, err := json.Marshal(pool)
	if err != nil {
		t.Fatal(err)
	}
	var clone ConceptPoolV1
	if err := json.Unmarshal(encoded, &clone); err != nil {
		t.Fatal(err)
	}
	return clone
}
