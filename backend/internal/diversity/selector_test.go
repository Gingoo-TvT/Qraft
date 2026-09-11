package diversity

import (
	"bytes"
	"testing"
)

func TestSelectMicroBatchDeterministicMaxMinAndUncertainRunnerUp(t *testing.T) {
	pools := []ConceptPoolV1{testConceptPoolV1(0), testConceptPoolV1(1), testConceptPoolV1(2)}
	setPoolTiersV1(&pools[0], []string{QualityTierViable, QualityTierUncertain, QualityTierInvalid, QualityTierInvalid})

	reservation, first, firstSHA, err := SelectMicroBatchV1(pools)
	if err != nil {
		t.Fatalf("SelectMicroBatchV1: %v", err)
	}
	if len(reservation.Slots) != 3 || reservation.Slots[0].RunnerUp.QualityTier != QualityTierUncertain {
		t.Fatalf("reservation=%+v", reservation)
	}
	seenSignatures := map[string]struct{}{}
	for _, slot := range reservation.Slots {
		if slot.Winner.QualityTier != QualityTierViable {
			t.Fatalf("non-viable winner: %+v", slot.Winner)
		}
		seenSignatures[slot.Winner.StructuralSignatureSHA] = struct{}{}
	}
	if len(seenSignatures) < 2 {
		t.Fatalf("selector chose an avoidably homogeneous batch: %+v", reservation.Slots)
	}

	reordered := []ConceptPoolV1{clonePoolV1(t, pools[2]), clonePoolV1(t, pools[0]), clonePoolV1(t, pools[1])}
	for index := range reordered {
		reordered[index].Attempts[0], reordered[index].Attempts[1] = reordered[index].Attempts[1], reordered[index].Attempts[0]
	}
	_, second, secondSHA, err := SelectMicroBatchV1(reordered)
	if err != nil {
		t.Fatalf("SelectMicroBatchV1 reordered: %v", err)
	}
	if !bytes.Equal(first, second) || firstSHA != secondSHA {
		t.Fatalf("selection is not deterministic:\n%s\n%s", first, second)
	}
}

func TestSelectMicroBatchRejectsIncompleteOrInconsistentBarrier(t *testing.T) {
	tests := []struct {
		name   string
		mutate func([]ConceptPoolV1) []ConceptPoolV1
	}{
		{name: "missing slot", mutate: func(pools []ConceptPoolV1) []ConceptPoolV1 { return pools[:2] }},
		{name: "mixed batch", mutate: func(pools []ConceptPoolV1) []ConceptPoolV1 { pools[2].BatchID = "other"; return pools }},
		{name: "mixed corpus", mutate: func(pools []ConceptPoolV1) []ConceptPoolV1 {
			pools[2].CorpusRevision = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
			return pools
		}},
		{name: "duplicate slot index", mutate: func(pools []ConceptPoolV1) []ConceptPoolV1 { pools[2].SlotIndex = 1; return pools }},
		{name: "duplicate concept across slots", mutate: func(pools []ConceptPoolV1) []ConceptPoolV1 {
			pools[2].Attempts[0].Concepts[0].ConceptID = pools[0].Attempts[0].Concepts[0].ConceptID
			return pools
		}},
		{name: "no viable winner", mutate: func(pools []ConceptPoolV1) []ConceptPoolV1 {
			setPoolTiersV1(&pools[1], []string{QualityTierUncertain, QualityTierInvalid, QualityTierInvalid, QualityTierInvalid})
			return pools
		}},
		{name: "no runner up", mutate: func(pools []ConceptPoolV1) []ConceptPoolV1 {
			setPoolTiersV1(&pools[1], []string{QualityTierViable, QualityTierInvalid, QualityTierInvalid, QualityTierInvalid})
			return pools
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pools := []ConceptPoolV1{testConceptPoolV1(0), testConceptPoolV1(1), testConceptPoolV1(2)}
			pools = test.mutate(pools)
			if _, _, _, err := SelectMicroBatchV1(pools); err == nil {
				t.Fatal("invalid micro-batch was accepted")
			}
		})
	}
}

func TestProvisionalReservationRejectsUncertainWinner(t *testing.T) {
	pools := []ConceptPoolV1{testConceptPoolV1(0), testConceptPoolV1(1), testConceptPoolV1(2)}
	reservation, _, _, err := SelectMicroBatchV1(pools)
	if err != nil {
		t.Fatal(err)
	}
	reservation.Slots[0].Winner.QualityTier = QualityTierUncertain
	if _, _, err := CanonicalProvisionalReservationV1(reservation); err == nil {
		t.Fatal("uncertain winner was accepted")
	}
}

func setPoolTiersV1(pool *ConceptPoolV1, tiers []string) {
	index := 0
	for attemptIndex := range pool.Attempts {
		for conceptIndex := range pool.Attempts[attemptIndex].Concepts {
			pool.Attempts[attemptIndex].Concepts[conceptIndex].Spec.QualityTier = tiers[index]
			index++
		}
	}
}
