package activities

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/Gingoo-TvT/Qraft/backend/internal/diversity"
	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
	"github.com/google/uuid"
)

const (
	S5DedupObservationBatchPayloadVersionV1 = 1
	MaxS5ConceptsPerDedupBatchV1            = 12

	s5ConceptOriginalityInputSchemaV1 = "algoforge.s5-concept-originality-input.v1"
	s5UnavailableCorpusMarkerV1       = "algoforge.s5-originality-corpus.unavailable.v1"

	s5DedupFailureActiveModelUnavailable = "active_model_unavailable"
	s5DedupFailureEmbeddingUnavailable   = "embedding_unavailable"
	s5DedupFailureEmbeddingInvalid       = "embedding_response_invalid"
	s5DedupFailureVectorQueryUnavailable = "vector_query_unavailable"
	s5DedupFailureCorpusRevisionInvalid  = "corpus_revision_unavailable"
)

type S5DedupObservationBatchInputV1 struct {
	PayloadVersion     int                       `json:"payload_version"`
	Concepts           []diversity.ConceptSpecV1 `json:"concepts"`
	RequestedTopK      int                       `json:"requested_top_k"`
	Threshold          float64                   `json:"threshold"`
	ExcludedLineageIDs []string                  `json:"excluded_lineage_ids,omitempty"`
}

// S5DedupObservationBatchResultV1 exposes CorpusRevision only when every
// concept was successfully checked against the same revision. A caller may
// then freeze that value into each ConceptPoolV1 before selection.
type S5DedupObservationBatchResultV1 struct {
	PayloadVersion int                            `json:"payload_version"`
	CorpusRevision string                         `json:"corpus_revision,omitempty"`
	Observations   []diversity.DedupObservationV1 `json:"observations"`
}

type s5OriginalityQueryReaderV1 interface {
	QueryS5OriginalityV1(context.Context, repository.S5OriginalityQueryV1) (repository.S5OriginalityResultV1, error)
}

// ObserveS5ConceptDedupBatchV1 performs exactly one batch embedding call and
// returns one observation per input concept in input order. Dependency
// failures are data, not activity errors: they become secret-free
// check_failed observations with an absent neighbor_count.
func (a *Activities) ObserveS5ConceptDedupBatchV1(
	ctx context.Context,
	input S5DedupObservationBatchInputV1,
) (*S5DedupObservationBatchResultV1, error) {
	prepared, err := prepareS5DedupObservationBatchV1(input)
	if err != nil {
		return nil, err
	}
	result := &S5DedupObservationBatchResultV1{
		PayloadVersion: S5DedupObservationBatchPayloadVersionV1,
		Observations:   make([]diversity.DedupObservationV1, len(prepared.contentHashes)),
	}

	if a == nil || a.deps == nil {
		return failS5DedupBatchV1(result, prepared, "", s5DedupFailureActiveModelUnavailable), nil
	}
	modelVersionID, err := a.deps.configuredStatementModelVersion()
	if err != nil {
		return failS5DedupBatchV1(result, prepared, "", s5DedupFailureActiveModelUnavailable), nil
	}
	modelVersion := modelVersionID.String()
	if !a.deps.EmbeddingEnabled || a.deps.Embedding == nil {
		return failS5DedupBatchV1(result, prepared, modelVersion, s5DedupFailureEmbeddingUnavailable), nil
	}
	reader, ok := a.deps.VectorRepo.(s5OriginalityQueryReaderV1)
	if !ok || reader == nil {
		return failS5DedupBatchV1(result, prepared, modelVersion, s5DedupFailureVectorQueryUnavailable), nil
	}

	vectors, err := a.deps.Embedding.EmbedBatch(ctx, prepared.embeddingTexts)
	if err != nil {
		return failS5DedupBatchV1(result, prepared, modelVersion, s5DedupFailureEmbeddingUnavailable), nil
	}
	if !validS5EmbeddingBatchV1(vectors, len(prepared.embeddingTexts)) {
		return failS5DedupBatchV1(result, prepared, modelVersion, s5DedupFailureEmbeddingInvalid), nil
	}

	var corpusRevision string
	for index, vector := range vectors {
		queryResult, queryErr := reader.QueryS5OriginalityV1(ctx, repository.S5OriginalityQueryV1{
			ModelVersionID:     modelVersionID,
			Kind:               repository.EmbeddingKindStatement,
			Embedding:          vector,
			TopK:               prepared.requestedTopK,
			ExcludedProblemIDs: prepared.excludedProblemIDs,
		})
		if queryErr != nil {
			reason := s5DedupFailureVectorQueryUnavailable
			if errors.Is(queryErr, repository.ErrS5CorpusRevisionUnavailable) {
				reason = s5DedupFailureCorpusRevisionInvalid
			}
			return failS5DedupBatchV1(result, prepared, modelVersion, reason), nil
		}
		if !canonicalSHA256V1(queryResult.CorpusRevision) {
			return failS5DedupBatchV1(result, prepared, modelVersion, s5DedupFailureCorpusRevisionInvalid), nil
		}
		if corpusRevision == "" {
			corpusRevision = queryResult.CorpusRevision
		} else if corpusRevision != queryResult.CorpusRevision {
			return failS5DedupBatchV1(result, prepared, modelVersion, s5DedupFailureCorpusRevisionInvalid), nil
		}
		if queryResult.NeighborCount != len(queryResult.Neighbors) ||
			queryResult.NeighborCount < 0 || queryResult.NeighborCount > prepared.requestedTopK {
			return failS5DedupBatchV1(result, prepared, modelVersion, s5DedupFailureVectorQueryUnavailable), nil
		}

		neighbors := make([]diversity.DedupNeighborV1, len(queryResult.Neighbors))
		for neighborIndex, neighbor := range queryResult.Neighbors {
			neighbors[neighborIndex] = diversity.DedupNeighborV1{
				Rank:        neighborIndex + 1,
				ProblemID:   neighbor.ProblemID.String(),
				ContentHash: neighbor.ContentHash,
				CorpusTier:  neighbor.CorpusTier,
				Similarity:  neighbor.Similarity,
			}
		}
		decision := diversity.DedupDecisionPass
		if len(neighbors) > 0 && neighbors[0].Similarity >= prepared.threshold {
			decision = diversity.DedupDecisionWarn
		}
		neighborCount := queryResult.NeighborCount
		observation := diversity.DedupObservationV1{
			SchemaVersion:      diversity.DedupObservationSchemaV1,
			Stage:              diversity.DedupStageConceptSelection,
			ModelVersion:       modelVersion,
			Kind:               diversity.DedupKindStructure,
			ContentHash:        prepared.contentHashes[index],
			CorpusRevision:     corpusRevision,
			RequestedTopK:      prepared.requestedTopK,
			Threshold:          prepared.threshold,
			Decision:           decision,
			NeighborCount:      &neighborCount,
			Neighbors:          neighbors,
			ExcludedLineageIDs: append([]string(nil), prepared.excludedLineageIDs...),
		}
		if err := observation.Validate(); err != nil {
			return failS5DedupBatchV1(result, prepared, modelVersion, s5DedupFailureVectorQueryUnavailable), nil
		}
		result.Observations[index] = observation
	}

	result.CorpusRevision = corpusRevision
	return result, nil
}

type preparedS5DedupBatchV1 struct {
	embeddingTexts     []string
	contentHashes      []string
	excludedLineageIDs []string
	excludedProblemIDs []uuid.UUID
	requestedTopK      int
	threshold          float64
}

func prepareS5DedupObservationBatchV1(input S5DedupObservationBatchInputV1) (preparedS5DedupBatchV1, error) {
	if input.PayloadVersion != S5DedupObservationBatchPayloadVersionV1 {
		return preparedS5DedupBatchV1{}, fmt.Errorf("unsupported S5 dedup observation payload_version %d", input.PayloadVersion)
	}
	if len(input.Concepts) == 0 || len(input.Concepts) > MaxS5ConceptsPerDedupBatchV1 {
		return preparedS5DedupBatchV1{}, fmt.Errorf("S5 dedup observation batch requires 1..%d concepts", MaxS5ConceptsPerDedupBatchV1)
	}
	if input.RequestedTopK <= 0 || input.RequestedTopK > repository.MaxS5OriginalityTopKV1 {
		return preparedS5DedupBatchV1{}, fmt.Errorf("requested_top_k must be within [1,%d]", repository.MaxS5OriginalityTopKV1)
	}
	if math.IsNaN(input.Threshold) || math.IsInf(input.Threshold, 0) || input.Threshold < 0 || input.Threshold > 1 {
		return preparedS5DedupBatchV1{}, fmt.Errorf("threshold must be finite and within [0,1]")
	}

	prepared := preparedS5DedupBatchV1{
		embeddingTexts: make([]string, len(input.Concepts)),
		contentHashes:  make([]string, len(input.Concepts)),
		requestedTopK:  input.RequestedTopK,
		threshold:      input.Threshold,
	}
	for index, spec := range input.Concepts {
		text, contentHash, err := canonicalS5ConceptEmbeddingV1(spec)
		if err != nil {
			return preparedS5DedupBatchV1{}, fmt.Errorf("concept %d: %w", index, err)
		}
		prepared.embeddingTexts[index] = text
		prepared.contentHashes[index] = contentHash
	}

	seenLineage := make(map[string]struct{}, len(input.ExcludedLineageIDs))
	for index, rawID := range input.ExcludedLineageIDs {
		parsed, err := uuid.Parse(strings.TrimSpace(rawID))
		if err != nil || parsed == uuid.Nil {
			return preparedS5DedupBatchV1{}, fmt.Errorf("excluded_lineage_ids[%d] must be a UUID", index)
		}
		canonical := parsed.String()
		if _, duplicate := seenLineage[canonical]; duplicate {
			return preparedS5DedupBatchV1{}, fmt.Errorf("excluded lineage ID %s is duplicated", canonical)
		}
		seenLineage[canonical] = struct{}{}
		prepared.excludedLineageIDs = append(prepared.excludedLineageIDs, canonical)
	}
	sort.Strings(prepared.excludedLineageIDs)
	prepared.excludedProblemIDs = make([]uuid.UUID, len(prepared.excludedLineageIDs))
	for index, id := range prepared.excludedLineageIDs {
		prepared.excludedProblemIDs[index] = uuid.MustParse(id)
	}
	return prepared, nil
}

// S5ConceptContentHashV1 returns the exact canonical structural-content hash
// used by ObserveS5ConceptDedupBatchV1. Callers that bind an observation back
// to a ConceptSpec must use this helper instead of reproducing the projection.
func S5ConceptContentHashV1(spec diversity.ConceptSpecV1) (string, error) {
	_, contentHash, err := canonicalS5ConceptEmbeddingV1(spec)
	if err != nil {
		return "", err
	}
	return contentHash, nil
}

func canonicalS5ConceptEmbeddingV1(spec diversity.ConceptSpecV1) (string, string, error) {
	signature, err := diversity.NewStructuralSignatureV1(spec)
	if err != nil {
		return "", "", err
	}
	descriptor := struct {
		SchemaVersion string                          `json:"schema_version"`
		Signature     diversity.StructuralSignatureV1 `json:"structural_signature"`
	}{
		SchemaVersion: s5ConceptOriginalityInputSchemaV1,
		Signature:     signature,
	}
	encoded, err := json.Marshal(descriptor)
	if err != nil {
		return "", "", err
	}
	return string(encoded), diversity.SHA256Hex(encoded), nil
}

func validS5EmbeddingBatchV1(vectors [][]float32, expected int) bool {
	if len(vectors) != expected || expected == 0 {
		return false
	}
	dimensions := 0
	for _, vector := range vectors {
		if len(vector) == 0 || (dimensions != 0 && len(vector) != dimensions) {
			return false
		}
		dimensions = len(vector)
		hasMagnitude := false
		for _, value := range vector {
			if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
				return false
			}
			if value != 0 {
				hasMagnitude = true
			}
		}
		if !hasMagnitude {
			return false
		}
	}
	return true
}

func failS5DedupBatchV1(
	result *S5DedupObservationBatchResultV1,
	prepared preparedS5DedupBatchV1,
	modelVersion string,
	reason string,
) *S5DedupObservationBatchResultV1 {
	result.CorpusRevision = ""
	failureRevision := diversity.SHA256Hex([]byte(s5UnavailableCorpusMarkerV1))
	for index, contentHash := range prepared.contentHashes {
		result.Observations[index] = diversity.DedupObservationV1{
			SchemaVersion:      diversity.DedupObservationSchemaV1,
			Stage:              diversity.DedupStageConceptSelection,
			ModelVersion:       modelVersion,
			Kind:               diversity.DedupKindStructure,
			ContentHash:        contentHash,
			CorpusRevision:     failureRevision,
			RequestedTopK:      prepared.requestedTopK,
			Threshold:          prepared.threshold,
			Decision:           diversity.DedupDecisionCheckFailed,
			ExcludedLineageIDs: append([]string(nil), prepared.excludedLineageIDs...),
			Reason:             reason,
		}
	}
	return result
}

func canonicalSHA256V1(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}
