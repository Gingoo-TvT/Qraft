package activities

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/Gingoo-TvT/Qraft/backend/internal/diversity"
	"github.com/Gingoo-TvT/Qraft/backend/internal/diversityapi"
	"github.com/Gingoo-TvT/Qraft/backend/internal/generationapi"
)

const (
	StoreS5ProvisionalReservationPayloadVersionV1 = 1
	S5ProvisionalReservationManifestSchemaV1      = "algoforge.s5-provisional-reservation-manifest.v1"
)

// S5ConceptDedupBindingV1 keeps the ordered batch-observation result bound to
// the exact concept that was embedded. The observation itself intentionally
// remains advisory; check_failed is rejected before a reservation is stored.
type S5ConceptDedupBindingV1 struct {
	SlotIndex    int                          `json:"slot_index"`
	AttemptIndex int                          `json:"attempt_index"`
	ConceptIndex int                          `json:"concept_index"`
	ConceptID    string                       `json:"concept_id"`
	Observation  diversity.DedupObservationV1 `json:"observation"`
}

// S5ProvisionalReservationManifestV1 is one immutable manifest for one
// three-slot batch. It provides intra-batch binding, not a global cross-batch
// lease, and is written before any child problem-generation workflow can start.
type S5ProvisionalReservationManifestV1 struct {
	SchemaVersion     string                             `json:"schema_version"`
	BatchID           string                             `json:"batch_id"`
	CorpusRevision    string                             `json:"corpus_revision"`
	ReservationSHA256 string                             `json:"reservation_sha256"`
	Reservation       diversity.ProvisionalReservationV1 `json:"reservation"`
	Pools             []diversity.ConceptPoolV1          `json:"pools"`
	DedupBindings     []S5ConceptDedupBindingV1          `json:"dedup_bindings"`
	ChildJobIDs       []string                           `json:"child_job_ids"`
}

type StoreS5ProvisionalReservationInputV1 struct {
	PayloadVersion int                                `json:"payload_version"`
	Manifest       S5ProvisionalReservationManifestV1 `json:"manifest"`
}

type StoreS5ProvisionalReservationResultV1 struct {
	PayloadVersion   int          `json:"payload_version"`
	ManifestSHA256   string       `json:"manifest_sha256"`
	ManifestArtifact *ArtifactRef `json:"manifest_artifact"`
}

func (a *Activities) StoreS5ProvisionalReservationActivityV1(
	ctx context.Context,
	in StoreS5ProvisionalReservationInputV1,
) (*StoreS5ProvisionalReservationResultV1, error) {
	if in.PayloadVersion != StoreS5ProvisionalReservationPayloadVersionV1 {
		return nil, fmt.Errorf("unsupported S5 reservation payload version %d", in.PayloadVersion)
	}
	manifest, encoded, digest, err := canonicalS5ProvisionalReservationManifestV1(in.Manifest)
	if err != nil {
		return nil, fmt.Errorf("invalid S5 provisional reservation manifest: %w", err)
	}
	metadata := artifactMetadataFromActivity(ctx)
	metadata.PayloadVersion = StoreS5ProvisionalReservationPayloadVersionV1
	metadata.ArtifactType = "s5_provisional_reservation_manifest"
	metadata.SourceType = "deterministic_batch_selection"
	metadata.RetentionClass = "workflow_cas_unreviewed"
	metadata.ProvenanceMetadata, err = json.Marshal(map[string]interface{}{
		"batch_id":           manifest.BatchID,
		"corpus_revision":    manifest.CorpusRevision,
		"reservation_sha256": manifest.ReservationSHA256,
		"selector_version":   manifest.Reservation.SelectorVersion,
	})
	if err != nil {
		return nil, fmt.Errorf("encode S5 reservation provenance: %w", err)
	}
	ref, err := a.putArtifactWithMetadata(ctx, encoded, "application/json", metadata)
	if err != nil {
		return nil, fmt.Errorf("store S5 provisional reservation manifest: %w", err)
	}
	if ref.SHA256 != digest || ref.SizeBytes != int64(len(encoded)) {
		return nil, fmt.Errorf("S5 provisional reservation CAS identity mismatch")
	}
	return &StoreS5ProvisionalReservationResultV1{
		PayloadVersion:   StoreS5ProvisionalReservationPayloadVersionV1,
		ManifestSHA256:   digest,
		ManifestArtifact: ref,
	}, nil
}

func canonicalS5ProvisionalReservationManifestV1(
	manifest S5ProvisionalReservationManifestV1,
) (S5ProvisionalReservationManifestV1, []byte, string, error) {
	canonical := manifest
	if canonical.SchemaVersion != S5ProvisionalReservationManifestSchemaV1 {
		return canonical, nil, "", fmt.Errorf("schema_version must be %q", S5ProvisionalReservationManifestSchemaV1)
	}
	if !diversityapi.IsBatchID(canonical.BatchID) {
		return canonical, nil, "", fmt.Errorf("batch_id is invalid")
	}
	reservationBytes, reservationSHA, err := diversity.CanonicalProvisionalReservationV1(canonical.Reservation)
	if err != nil {
		return canonical, nil, "", fmt.Errorf("reservation: %w", err)
	}
	if canonical.ReservationSHA256 != reservationSHA {
		return canonical, nil, "", fmt.Errorf("reservation_sha256 mismatch")
	}
	if err := json.Unmarshal(reservationBytes, &canonical.Reservation); err != nil {
		return canonical, nil, "", fmt.Errorf("decode canonical reservation: %w", err)
	}
	if canonical.Reservation.BatchID != canonical.BatchID ||
		canonical.Reservation.CorpusRevision != canonical.CorpusRevision {
		return canonical, nil, "", fmt.Errorf("reservation batch or corpus binding mismatch")
	}

	if len(canonical.Pools) != diversityapi.SlotCountV1 {
		return canonical, nil, "", fmt.Errorf("manifest requires exactly %d pools", diversityapi.SlotCountV1)
	}
	canonical.Pools = append([]diversity.ConceptPoolV1(nil), canonical.Pools...)
	sort.Slice(canonical.Pools, func(i, j int) bool { return canonical.Pools[i].SlotIndex < canonical.Pools[j].SlotIndex })
	concepts := make(map[string][3]int, 12)
	conceptSpecs := make(map[string]diversity.ConceptSpecV1, 12)
	for slotIndex := range canonical.Pools {
		poolBytes, poolSHA, err := diversity.CanonicalConceptPoolV1(canonical.Pools[slotIndex])
		if err != nil {
			return canonical, nil, "", fmt.Errorf("pool %d: %w", slotIndex, err)
		}
		if err := json.Unmarshal(poolBytes, &canonical.Pools[slotIndex]); err != nil {
			return canonical, nil, "", fmt.Errorf("decode canonical pool %d: %w", slotIndex, err)
		}
		pool := canonical.Pools[slotIndex]
		if pool.SlotIndex != slotIndex || pool.BatchID != canonical.BatchID || pool.CorpusRevision != canonical.CorpusRevision {
			return canonical, nil, "", fmt.Errorf("pool %d batch, slot, or corpus binding mismatch", slotIndex)
		}
		if canonical.Reservation.Slots[slotIndex].PoolSHA256 != poolSHA {
			return canonical, nil, "", fmt.Errorf("pool %d SHA is not bound by reservation", slotIndex)
		}
		for attemptIndex, attempt := range pool.Attempts {
			for conceptIndex, card := range attempt.Concepts {
				if _, duplicate := concepts[card.ConceptID]; duplicate {
					return canonical, nil, "", fmt.Errorf("concept_id %q is duplicated in manifest", card.ConceptID)
				}
				concepts[card.ConceptID] = [3]int{slotIndex, attemptIndex, conceptIndex}
				conceptSpecs[card.ConceptID] = card.Spec
			}
		}
	}

	if len(canonical.DedupBindings) != len(concepts) {
		return canonical, nil, "", fmt.Errorf("dedup bindings must cover all %d concepts", len(concepts))
	}
	canonical.DedupBindings = append([]S5ConceptDedupBindingV1(nil), canonical.DedupBindings...)
	sort.Slice(canonical.DedupBindings, func(i, j int) bool {
		left, right := canonical.DedupBindings[i], canonical.DedupBindings[j]
		if left.SlotIndex != right.SlotIndex {
			return left.SlotIndex < right.SlotIndex
		}
		if left.AttemptIndex != right.AttemptIndex {
			return left.AttemptIndex < right.AttemptIndex
		}
		return left.ConceptIndex < right.ConceptIndex
	})
	seenBindings := make(map[string]struct{}, len(canonical.DedupBindings))
	for index := range canonical.DedupBindings {
		binding := &canonical.DedupBindings[index]
		coordinates, ok := concepts[binding.ConceptID]
		if !ok || coordinates != [3]int{binding.SlotIndex, binding.AttemptIndex, binding.ConceptIndex} {
			return canonical, nil, "", fmt.Errorf("dedup binding %d does not identify its concept", index)
		}
		if _, duplicate := seenBindings[binding.ConceptID]; duplicate {
			return canonical, nil, "", fmt.Errorf("concept %q has multiple dedup bindings", binding.ConceptID)
		}
		seenBindings[binding.ConceptID] = struct{}{}
		observationBytes, _, err := diversity.CanonicalDedupObservationV1(binding.Observation)
		if err != nil {
			return canonical, nil, "", fmt.Errorf("dedup binding %d: %w", index, err)
		}
		if err := json.Unmarshal(observationBytes, &binding.Observation); err != nil {
			return canonical, nil, "", fmt.Errorf("decode canonical observation %d: %w", index, err)
		}
		if binding.Observation.Decision == diversity.DedupDecisionCheckFailed ||
			binding.Observation.CorpusRevision != canonical.CorpusRevision {
			return canonical, nil, "", fmt.Errorf("dedup binding %d is unavailable or uses another corpus", index)
		}
		if binding.Observation.Stage != diversity.DedupStageConceptSelection ||
			binding.Observation.Kind != diversity.DedupKindStructure {
			return canonical, nil, "", fmt.Errorf("dedup binding %d is not a concept-selection structure observation", index)
		}
		expectedContentHash, err := S5ConceptContentHashV1(conceptSpecs[binding.ConceptID])
		if err != nil || binding.Observation.ContentHash != expectedContentHash {
			return canonical, nil, "", fmt.Errorf("dedup binding %d content hash does not bind its ConceptSpec", index)
		}
	}

	if len(canonical.ChildJobIDs) != diversityapi.SlotCountV1 {
		return canonical, nil, "", fmt.Errorf("manifest requires exactly %d child job ids", diversityapi.SlotCountV1)
	}
	canonical.ChildJobIDs = append([]string(nil), canonical.ChildJobIDs...)
	for slotIndex, childID := range canonical.ChildJobIDs {
		expected, err := diversityapi.ChildJobID(canonical.BatchID, slotIndex)
		if err != nil || childID != expected || !generationapi.IsJobID(childID) {
			return canonical, nil, "", fmt.Errorf("child job id %d is not server-derived", slotIndex)
		}
	}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return canonical, nil, "", fmt.Errorf("encode canonical S5 reservation manifest: %w", err)
	}
	return canonical, encoded, diversity.SHA256Hex(encoded), nil
}
