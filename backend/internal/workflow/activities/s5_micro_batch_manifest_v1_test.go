package activities

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/diversity"
	"github.com/Gingoo-TvT/Qraft/backend/internal/diversityapi"
)

func TestS5ProvisionalReservationManifestIsCanonicalAndStoredBeforeChildren(t *testing.T) {
	manifest := validS5ProvisionalManifestV1(t)
	_, first, firstSHA, err := canonicalS5ProvisionalReservationManifestV1(manifest)
	if err != nil {
		t.Fatal(err)
	}
	reordered := manifest
	reordered.Pools = []diversity.ConceptPoolV1{manifest.Pools[2], manifest.Pools[0], manifest.Pools[1]}
	reordered.DedupBindings = append([]S5ConceptDedupBindingV1(nil), manifest.DedupBindings...)
	for left, right := 0, len(reordered.DedupBindings)-1; left < right; left, right = left+1, right-1 {
		reordered.DedupBindings[left], reordered.DedupBindings[right] = reordered.DedupBindings[right], reordered.DedupBindings[left]
	}
	_, second, secondSHA, err := canonicalS5ProvisionalReservationManifestV1(reordered)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) || firstSHA != secondSHA {
		t.Fatalf("canonical manifest drifted: %s / %s", firstSHA, secondSHA)
	}

	store := &captureArtifactStore{}
	activities := New(&Dependencies{ArtifactStore: store})
	result, err := activities.StoreS5ProvisionalReservationActivityV1(context.Background(), StoreS5ProvisionalReservationInputV1{
		PayloadVersion: StoreS5ProvisionalReservationPayloadVersionV1,
		Manifest:       manifest,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ManifestArtifact == nil || result.ManifestSHA256 != firstSHA || !bytes.Equal(store.data, first) {
		t.Fatalf("stored manifest binding = %+v", result)
	}
}

func TestS5ProvisionalReservationManifestRejectsUnavailableDedupAndForgedChild(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*S5ProvisionalReservationManifestV1)
	}{
		{name: "check failed", mutate: func(manifest *S5ProvisionalReservationManifestV1) {
			observation := &manifest.DedupBindings[0].Observation
			observation.Decision = diversity.DedupDecisionCheckFailed
			observation.ModelVersion = ""
			observation.NeighborCount = nil
			observation.Reason = "embedding provider unavailable"
		}},
		{name: "mixed corpus", mutate: func(manifest *S5ProvisionalReservationManifestV1) {
			manifest.DedupBindings[0].Observation.CorpusRevision = strings.Repeat("c", 64)
		}},
		{name: "content hash does not bind concept spec", mutate: func(manifest *S5ProvisionalReservationManifestV1) {
			manifest.DedupBindings[0].Observation.ContentHash = strings.Repeat("d", 64)
		}},
		{name: "wrong observation stage", mutate: func(manifest *S5ProvisionalReservationManifestV1) {
			manifest.DedupBindings[0].Observation.Stage = diversity.DedupStagePostStatement
		}},
		{name: "wrong observation kind", mutate: func(manifest *S5ProvisionalReservationManifestV1) {
			manifest.DedupBindings[0].Observation.Kind = diversity.DedupKindStatement
		}},
		{name: "forged child", mutate: func(manifest *S5ProvisionalReservationManifestV1) {
			manifest.ChildJobIDs[1] = manifest.ChildJobIDs[0]
		}},
		{name: "missing concept observation", mutate: func(manifest *S5ProvisionalReservationManifestV1) {
			manifest.DedupBindings = manifest.DedupBindings[:11]
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifest := validS5ProvisionalManifestV1(t)
			test.mutate(&manifest)
			if _, _, _, err := canonicalS5ProvisionalReservationManifestV1(manifest); err == nil {
				t.Fatal("invalid manifest was accepted")
			}
		})
	}
}

func validS5ProvisionalManifestV1(t *testing.T) S5ProvisionalReservationManifestV1 {
	t.Helper()
	batchID, _, err := diversityapi.BatchIDForIdempotencyKey("local-dev", "manifest-fixture")
	if err != nil {
		t.Fatal(err)
	}
	corpusRevision := strings.Repeat("b", 64)
	pools := []diversity.ConceptPoolV1{
		validS5ConceptPoolV1(batchID, corpusRevision, 0),
		validS5ConceptPoolV1(batchID, corpusRevision, 1),
		validS5ConceptPoolV1(batchID, corpusRevision, 2),
	}
	reservation, _, reservationSHA, err := diversity.SelectMicroBatchV1(pools)
	if err != nil {
		t.Fatal(err)
	}
	zero := 0
	bindings := make([]S5ConceptDedupBindingV1, 0, 12)
	for slotIndex, pool := range pools {
		for attemptIndex, attempt := range pool.Attempts {
			for conceptIndex, card := range attempt.Concepts {
				contentHash, err := S5ConceptContentHashV1(card.Spec)
				if err != nil {
					t.Fatal(err)
				}
				bindings = append(bindings, S5ConceptDedupBindingV1{
					SlotIndex: slotIndex, AttemptIndex: attemptIndex, ConceptIndex: conceptIndex, ConceptID: card.ConceptID,
					Observation: diversity.DedupObservationV1{
						SchemaVersion:  diversity.DedupObservationSchemaV1,
						Stage:          diversity.DedupStageConceptSelection,
						ModelVersion:   "fixture-embedding-v1",
						Kind:           diversity.DedupKindStructure,
						ContentHash:    contentHash,
						CorpusRevision: corpusRevision,
						RequestedTopK:  5,
						Threshold:      0.92,
						Decision:       diversity.DedupDecisionPass,
						NeighborCount:  &zero,
					},
				})
			}
		}
	}
	children := make([]string, diversityapi.SlotCountV1)
	for index := range children {
		children[index], err = diversityapi.ChildJobID(batchID, index)
		if err != nil {
			t.Fatal(err)
		}
	}
	return S5ProvisionalReservationManifestV1{
		SchemaVersion:     S5ProvisionalReservationManifestSchemaV1,
		BatchID:           batchID,
		CorpusRevision:    corpusRevision,
		ReservationSHA256: reservationSHA,
		Reservation:       reservation,
		Pools:             pools,
		DedupBindings:     bindings,
		ChildJobIDs:       children,
	}
}

func validS5ConceptPoolV1(batchID, corpusRevision string, slotIndex int) diversity.ConceptPoolV1 {
	slotID := batchID + "-slot-" + string(rune('0'+slotIndex))
	pool := diversity.ConceptPoolV1{
		SchemaVersion:  diversity.ConceptPoolSchemaV1,
		BatchID:        batchID,
		SlotIndex:      slotIndex,
		SlotID:         slotID,
		BriefSHA256:    diversity.SHA256Hex([]byte(slotID + "-brief")),
		CorpusRevision: corpusRevision,
		Budget:         diversity.ConceptBudgetV1{MaxCreativeAttempts: 2, MaxConcepts: 4, MaxModelCalls: 3, MaxNetworkRetries: 2, MaxTokens: 10000, MaxWallMilliseconds: 10000},
		Attempts:       make([]diversity.ConceptAttemptV1, 2),
		Normalization: diversity.ConceptNormalizationV1{
			NormalizerVersion: "algoforge.s5-concept-normalizer.v1",
			Receipt:           diversity.ArtifactRefV1{SHA256: strings.Repeat("f", 64), URI: "cas://fixture/normalization/" + slotID},
			Usage:             diversity.ConceptAttemptUsageV1{ModelCalls: 1, Tokens: 100, WallMilliseconds: 50},
		},
	}
	for attemptIndex := 0; attemptIndex < 2; attemptIndex++ {
		attempt := diversity.ConceptAttemptV1{
			AttemptIndex:     attemptIndex,
			LogicalAttemptID: slotID + "-attempt-" + string(rune('0'+attemptIndex)),
			Receipt:          diversity.ArtifactRefV1{SHA256: strings.Repeat(string(rune('c'+slotIndex+attemptIndex)), 64), URI: "cas://fixture/attempt/" + slotID + "/" + string(rune('0'+attemptIndex))},
			Usage:            diversity.ConceptAttemptUsageV1{ModelCalls: 1, Tokens: 100, WallMilliseconds: 50},
			Concepts:         make([]diversity.ConceptCardV1, 2),
		}
		for conceptIndex := 0; conceptIndex < 2; conceptIndex++ {
			ordinal := attemptIndex*2 + conceptIndex
			conceptID := slotID + "-concept-" + string(rune('0'+ordinal))
			attempt.Concepts[conceptIndex] = diversity.ConceptCardV1{
				ConceptIndex:        conceptIndex,
				ConceptID:           conceptID,
				OneParagraphPitch:   "Distinct competitive programming pitch for " + conceptID,
				UnresolvedQuestions: []string{"boundary semantics", "tie handling"},
				Spec: diversity.ConceptSpecV1{
					SchemaVersion:         diversity.ConceptSpecSchemaV1,
					CanonicalizerVersion:  diversity.ConceptCanonicalizerVersionV1,
					ExtractionConfidence:  0.8,
					QualityTier:           diversity.QualityTierViable,
					ProblemMode:           "static",
					InputObject:           "family-" + string(rune('a'+slotIndex*4+ordinal)),
					Topology:              "line",
					OperationModel:        "offline",
					Objective:             "count",
					StateDimensions:       []string{"position", "prefix"},
					TransitionOrInvariant: "prefix invariant " + conceptID,
					SolutionOperatorSeq:   []string{"scan", "aggregate"},
					OutputForm:            "integer",
					ComplexityClass:       "linear",
					ConstraintRegime:      "n up to 200000",
					WrongSolutionFamilies: []string{"off by one", "overflow"},
				},
			}
		}
		pool.Attempts[attemptIndex] = attempt
	}
	return pool
}
