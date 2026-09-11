package diversity

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
)

const (
	SelectorVersionV1      = "algoforge.s5-deterministic-max-min-selector.v1"
	ReservationProvisional = "provisional"
	microBatchSlotsV1      = 3
)

type ProvisionalReservationV1 struct {
	SchemaVersion          string           `json:"schema_version"`
	Status                 string           `json:"status"`
	BatchID                string           `json:"batch_id"`
	CorpusRevision         string           `json:"corpus_revision"`
	SelectorVersion        string           `json:"selector_version"`
	SignatureSchemaVersion string           `json:"signature_schema_version"`
	MinimumPairDistance    int              `json:"minimum_pair_distance"`
	TotalPairDistance      int              `json:"total_pair_distance"`
	Slots                  []ReservedSlotV1 `json:"slots"`
}

type ReservedSlotV1 struct {
	SlotIndex  int               `json:"slot_index"`
	SlotID     string            `json:"slot_id"`
	PoolSHA256 string            `json:"pool_sha256"`
	Winner     ReservedConceptV1 `json:"winner"`
	RunnerUp   ReservedConceptV1 `json:"runner_up"`
}

type ReservedConceptV1 struct {
	ConceptID              string `json:"concept_id"`
	QualityTier            string `json:"quality_tier"`
	StructuralSignatureSHA string `json:"structural_signature_sha256"`
}

type selectorCandidateV1 struct {
	slotIndex int
	slotID    string
	poolSHA   string
	card      ConceptCardV1
	signature StructuralSignatureV1
}

type selectorScoreV1 struct {
	minimum int
	total   int
	ids     string
}

// SelectMicroBatchV1 performs an exhaustive deterministic max-min selection.
// With the fixed 2x2 pool and three slots the search space is at most 4^3.
func SelectMicroBatchV1(pools []ConceptPoolV1) (ProvisionalReservationV1, []byte, string, error) {
	if len(pools) != microBatchSlotsV1 {
		return ProvisionalReservationV1{}, nil, "", fmt.Errorf("micro-batch requires exactly %d concept pools", microBatchSlotsV1)
	}
	canonicalPools := make([]ConceptPoolV1, len(pools))
	poolSHAs := make([]string, len(pools))
	for index, pool := range pools {
		canonical, err := canonicalizeConceptPoolV1(pool)
		if err != nil {
			return ProvisionalReservationV1{}, nil, "", fmt.Errorf("pool %d: %w", index, err)
		}
		encoded, err := json.Marshal(canonical)
		if err != nil {
			return ProvisionalReservationV1{}, nil, "", err
		}
		canonicalPools[index] = canonical
		poolSHAs[index] = SHA256Hex(encoded)
	}
	sort.SliceStable(canonicalPools, func(i, j int) bool {
		return canonicalPools[i].SlotIndex < canonicalPools[j].SlotIndex
	})
	// Recompute hashes after ordering because the hash belongs to each pool,
	// not its original input position.
	for index := range canonicalPools {
		encoded, err := json.Marshal(canonicalPools[index])
		if err != nil {
			return ProvisionalReservationV1{}, nil, "", err
		}
		poolSHAs[index] = SHA256Hex(encoded)
	}
	batchID := canonicalPools[0].BatchID
	corpusRevision := canonicalPools[0].CorpusRevision
	seenSlots := make(map[string]struct{}, microBatchSlotsV1)
	seenConcepts := make(map[string]struct{}, microBatchSlotsV1*conceptsPerPool)
	allCandidates := make([][]selectorCandidateV1, microBatchSlotsV1)
	viableCandidates := make([][]selectorCandidateV1, microBatchSlotsV1)
	for index, pool := range canonicalPools {
		if pool.SlotIndex != index {
			return ProvisionalReservationV1{}, nil, "", fmt.Errorf("slot indices must be exactly 0..%d", microBatchSlotsV1-1)
		}
		if pool.BatchID != batchID || pool.CorpusRevision != corpusRevision {
			return ProvisionalReservationV1{}, nil, "", errors.New("all concept pools must bind the same batch_id and corpus_revision")
		}
		if _, duplicate := seenSlots[pool.SlotID]; duplicate {
			return ProvisionalReservationV1{}, nil, "", fmt.Errorf("slot_id %q is duplicated", pool.SlotID)
		}
		seenSlots[pool.SlotID] = struct{}{}
		for _, attempt := range pool.Attempts {
			for _, card := range attempt.Concepts {
				if _, duplicate := seenConcepts[card.ConceptID]; duplicate {
					return ProvisionalReservationV1{}, nil, "", fmt.Errorf("concept_id %q is duplicated across slots", card.ConceptID)
				}
				seenConcepts[card.ConceptID] = struct{}{}
				signature, err := NewStructuralSignatureV1(card.Spec)
				if err != nil {
					return ProvisionalReservationV1{}, nil, "", fmt.Errorf("concept %q signature: %w", card.ConceptID, err)
				}
				candidate := selectorCandidateV1{
					slotIndex: pool.SlotIndex, slotID: pool.SlotID, poolSHA: poolSHAs[index],
					card: card, signature: signature,
				}
				if card.Spec.QualityTier != QualityTierInvalid {
					allCandidates[index] = append(allCandidates[index], candidate)
				}
				if card.Spec.QualityTier == QualityTierViable {
					viableCandidates[index] = append(viableCandidates[index], candidate)
				}
			}
		}
		if len(viableCandidates[index]) == 0 {
			return ProvisionalReservationV1{}, nil, "", fmt.Errorf("slot %d has no viable winner candidate", index)
		}
	}

	best, bestScore, err := bestViableCombinationV1(viableCandidates)
	if err != nil {
		return ProvisionalReservationV1{}, nil, "", err
	}
	reservation := ProvisionalReservationV1{
		SchemaVersion:          ProvisionalReservationSchemaV1,
		Status:                 ReservationProvisional,
		BatchID:                batchID,
		CorpusRevision:         corpusRevision,
		SelectorVersion:        SelectorVersionV1,
		SignatureSchemaVersion: StructuralSignatureSchemaV1,
		MinimumPairDistance:    bestScore.minimum,
		TotalPairDistance:      bestScore.total,
		Slots:                  make([]ReservedSlotV1, microBatchSlotsV1),
	}
	for slotIndex := 0; slotIndex < microBatchSlotsV1; slotIndex++ {
		runnerUp, err := bestRunnerUpV1(slotIndex, best, allCandidates[slotIndex])
		if err != nil {
			return ProvisionalReservationV1{}, nil, "", err
		}
		winner := best[slotIndex]
		reservation.Slots[slotIndex] = ReservedSlotV1{
			SlotIndex: slotIndex, SlotID: winner.slotID, PoolSHA256: winner.poolSHA,
			Winner: reservedConceptV1(winner), RunnerUp: reservedConceptV1(runnerUp),
		}
	}
	encoded, digest, err := CanonicalProvisionalReservationV1(reservation)
	if err != nil {
		return ProvisionalReservationV1{}, nil, "", err
	}
	return reservation, encoded, digest, nil
}

func bestViableCombinationV1(candidates [][]selectorCandidateV1) ([microBatchSlotsV1]selectorCandidateV1, selectorScoreV1, error) {
	var best [microBatchSlotsV1]selectorCandidateV1
	bestScore := selectorScoreV1{minimum: math.MinInt, total: math.MinInt}
	found := false
	for _, first := range candidates[0] {
		for _, second := range candidates[1] {
			for _, third := range candidates[2] {
				combination := [microBatchSlotsV1]selectorCandidateV1{first, second, third}
				score, err := scoreCombinationV1(combination)
				if err != nil {
					return best, selectorScoreV1{}, err
				}
				if !found || betterSelectorScoreV1(score, bestScore) {
					best = combination
					bestScore = score
					found = true
				}
			}
		}
	}
	if !found {
		return best, selectorScoreV1{}, errors.New("no viable micro-batch combination")
	}
	return best, bestScore, nil
}

func bestRunnerUpV1(slotIndex int, winners [microBatchSlotsV1]selectorCandidateV1, candidates []selectorCandidateV1) (selectorCandidateV1, error) {
	preferredTier := QualityTierViable
	hasViable := false
	for _, candidate := range candidates {
		if candidate.card.ConceptID != winners[slotIndex].card.ConceptID && candidate.card.Spec.QualityTier == QualityTierViable {
			hasViable = true
			break
		}
	}
	if !hasViable {
		preferredTier = QualityTierUncertain
	}
	var best selectorCandidateV1
	bestScore := selectorScoreV1{minimum: math.MinInt, total: math.MinInt}
	found := false
	for _, candidate := range candidates {
		if candidate.card.ConceptID == winners[slotIndex].card.ConceptID || candidate.card.Spec.QualityTier != preferredTier {
			continue
		}
		combination := winners
		combination[slotIndex] = candidate
		score, err := scoreCombinationV1(combination)
		if err != nil {
			return selectorCandidateV1{}, err
		}
		if !found || betterSelectorScoreV1(score, bestScore) {
			best = candidate
			bestScore = score
			found = true
		}
	}
	if !found {
		return selectorCandidateV1{}, fmt.Errorf("slot %d has no non-invalid runner-up", slotIndex)
	}
	return best, nil
}

func scoreCombinationV1(combination [microBatchSlotsV1]selectorCandidateV1) (selectorScoreV1, error) {
	distances := make([]int, 0, 3)
	for left := 0; left < microBatchSlotsV1; left++ {
		for right := left + 1; right < microBatchSlotsV1; right++ {
			distance, err := StructuralDistanceV1(combination[left].signature, combination[right].signature)
			if err != nil {
				return selectorScoreV1{}, err
			}
			distances = append(distances, distance)
		}
	}
	minimum := distances[0]
	total := 0
	ids := make([]string, microBatchSlotsV1)
	for _, distance := range distances {
		if distance < minimum {
			minimum = distance
		}
		total += distance
	}
	for index := range combination {
		ids[index] = combination[index].card.ConceptID
	}
	return selectorScoreV1{minimum: minimum, total: total, ids: strings.Join(ids, "\x00")}, nil
}

func betterSelectorScoreV1(candidate, incumbent selectorScoreV1) bool {
	if candidate.minimum != incumbent.minimum {
		return candidate.minimum > incumbent.minimum
	}
	if candidate.total != incumbent.total {
		return candidate.total > incumbent.total
	}
	return candidate.ids < incumbent.ids
}

func reservedConceptV1(candidate selectorCandidateV1) ReservedConceptV1 {
	return ReservedConceptV1{
		ConceptID:              candidate.card.ConceptID,
		QualityTier:            candidate.card.Spec.QualityTier,
		StructuralSignatureSHA: candidate.signature.SHA256,
	}
}

func CanonicalProvisionalReservationV1(reservation ProvisionalReservationV1) ([]byte, string, error) {
	canonical := reservation
	canonical.Status = canonicalLowerText(reservation.Status)
	canonical.BatchID = canonicalText(reservation.BatchID)
	canonical.CorpusRevision = strings.ToLower(strings.TrimSpace(reservation.CorpusRevision))
	canonical.Slots = append([]ReservedSlotV1(nil), reservation.Slots...)
	for index := range canonical.Slots {
		slot := &canonical.Slots[index]
		slot.SlotID = canonicalText(slot.SlotID)
		slot.PoolSHA256 = strings.ToLower(strings.TrimSpace(slot.PoolSHA256))
		slot.Winner.ConceptID = canonicalText(slot.Winner.ConceptID)
		slot.Winner.QualityTier = canonicalLowerText(slot.Winner.QualityTier)
		slot.Winner.StructuralSignatureSHA = strings.ToLower(strings.TrimSpace(slot.Winner.StructuralSignatureSHA))
		slot.RunnerUp.ConceptID = canonicalText(slot.RunnerUp.ConceptID)
		slot.RunnerUp.QualityTier = canonicalLowerText(slot.RunnerUp.QualityTier)
		slot.RunnerUp.StructuralSignatureSHA = strings.ToLower(strings.TrimSpace(slot.RunnerUp.StructuralSignatureSHA))
	}
	sort.Slice(canonical.Slots, func(i, j int) bool { return canonical.Slots[i].SlotIndex < canonical.Slots[j].SlotIndex })
	if err := canonical.Validate(); err != nil {
		return nil, "", err
	}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return nil, "", err
	}
	return encoded, SHA256Hex(encoded), nil
}

func (reservation ProvisionalReservationV1) Validate() error {
	if reservation.SchemaVersion != ProvisionalReservationSchemaV1 || reservation.Status != ReservationProvisional ||
		reservation.SelectorVersion != SelectorVersionV1 || reservation.SignatureSchemaVersion != StructuralSignatureSchemaV1 {
		return errors.New("provisional reservation schema, status, selector, or signature version is invalid")
	}
	if err := validateStableID("batch_id", reservation.BatchID); err != nil {
		return err
	}
	if !sha256Pattern.MatchString(reservation.CorpusRevision) {
		return errors.New("reservation corpus_revision must be canonical SHA-256")
	}
	if reservation.MinimumPairDistance < 0 || reservation.TotalPairDistance < reservation.MinimumPairDistance {
		return errors.New("reservation pair-distance audit is invalid")
	}
	if len(reservation.Slots) != microBatchSlotsV1 {
		return fmt.Errorf("reservation requires exactly %d slots", microBatchSlotsV1)
	}
	seenSlots := make(map[string]struct{}, microBatchSlotsV1)
	seenConcepts := make(map[string]struct{}, microBatchSlotsV1*2)
	for index, slot := range reservation.Slots {
		if slot.SlotIndex != index {
			return fmt.Errorf("reservation slot %d has non-canonical slot_index %d", index, slot.SlotIndex)
		}
		if err := validateStableID("slot_id", slot.SlotID); err != nil {
			return err
		}
		if _, duplicate := seenSlots[slot.SlotID]; duplicate {
			return fmt.Errorf("reservation slot_id %q is duplicated", slot.SlotID)
		}
		seenSlots[slot.SlotID] = struct{}{}
		if !sha256Pattern.MatchString(slot.PoolSHA256) {
			return fmt.Errorf("reservation slot %d pool SHA-256 is invalid", index)
		}
		if slot.Winner.QualityTier != QualityTierViable {
			return fmt.Errorf("reservation slot %d winner must be viable", index)
		}
		if slot.RunnerUp.QualityTier != QualityTierViable && slot.RunnerUp.QualityTier != QualityTierUncertain {
			return fmt.Errorf("reservation slot %d runner-up must be viable or uncertain", index)
		}
		for role, concept := range map[string]ReservedConceptV1{"winner": slot.Winner, "runner_up": slot.RunnerUp} {
			if err := validateStableID(role+".concept_id", concept.ConceptID); err != nil {
				return err
			}
			if !sha256Pattern.MatchString(concept.StructuralSignatureSHA) {
				return fmt.Errorf("reservation slot %d %s signature SHA-256 is invalid", index, role)
			}
			if _, duplicate := seenConcepts[concept.ConceptID]; duplicate {
				return fmt.Errorf("reservation concept_id %q is duplicated", concept.ConceptID)
			}
			seenConcepts[concept.ConceptID] = struct{}{}
		}
	}
	return nil
}
