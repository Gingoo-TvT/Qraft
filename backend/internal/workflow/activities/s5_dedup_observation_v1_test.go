package activities

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/diversity"
	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
	"github.com/google/uuid"
)

func TestObserveS5ConceptDedupBatchV1UsesOneEmbeddingBatchAndReturnsStableRevision(t *testing.T) {
	modelVersionID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	revision := strings.Repeat("a", 64)
	lineageFirst := uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaa1")
	lineageSecond := uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaa2")
	neighborFirst := uuid.MustParse("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbb1")
	neighborSecond := uuid.MustParse("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbb2")
	embedder := &s5ObservationEmbedder{vectors: [][]float32{{1, 0}, {0, 1}}}
	store := &s5ObservationVectorStore{results: []repository.S5OriginalityResultV1{
		{CorpusRevision: revision, CorpusSize: 0, NeighborCount: 0},
		{
			CorpusRevision: revision,
			CorpusSize:     3,
			NeighborCount:  2,
			Neighbors: []repository.S5OriginalityNeighborV1{
				{ProblemID: neighborFirst, ContentHash: strings.Repeat("b", 64), CorpusTier: diversity.CorpusTierHistoricalAdvisory, Similarity: 0.93},
				{ProblemID: neighborSecond, ContentHash: strings.Repeat("c", 64), CorpusTier: diversity.CorpusTierQuarantineAdvisory, Similarity: 0.70},
			},
		},
	}}
	activitySet := New(&Dependencies{
		Embedding:               embedder,
		EmbeddingEnabled:        true,
		EmbeddingModelVersionID: modelVersionID,
		VectorRepo:              store,
	})

	result, err := activitySet.ObserveS5ConceptDedupBatchV1(context.Background(), S5DedupObservationBatchInputV1{
		PayloadVersion: S5DedupObservationBatchPayloadVersionV1,
		Concepts: []diversity.ConceptSpecV1{
			s5ObservationTestConcept("shortest path"),
			s5ObservationTestConcept("maximum matching"),
		},
		RequestedTopK: 2,
		Threshold:     0.80,
		ExcludedLineageIDs: []string{
			strings.ToUpper(lineageSecond.String()),
			lineageFirst.String(),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if embedder.batchCalls != 1 || embedder.singleCalls != 0 || len(embedder.texts) != 2 {
		t.Fatalf("embedding calls: batch=%d single=%d texts=%d", embedder.batchCalls, embedder.singleCalls, len(embedder.texts))
	}
	if len(store.calls) != 2 {
		t.Fatalf("originality query calls = %d, want 2", len(store.calls))
	}
	for _, call := range store.calls {
		if call.ModelVersionID != modelVersionID || call.Kind != repository.EmbeddingKindStatement || call.TopK != 2 {
			t.Fatalf("query call = %+v", call)
		}
		if len(call.ExcludedProblemIDs) != 2 || call.ExcludedProblemIDs[0] != lineageFirst || call.ExcludedProblemIDs[1] != lineageSecond {
			t.Fatalf("server lineage exclusion = %v", call.ExcludedProblemIDs)
		}
	}
	if result.CorpusRevision != revision || len(result.Observations) != 2 {
		t.Fatalf("batch result = %+v", result)
	}
	first := result.Observations[0]
	if first.NeighborCount == nil || *first.NeighborCount != 0 || first.Decision != diversity.DedupDecisionPass || len(first.Neighbors) != 0 {
		t.Fatalf("explicit empty observation = %+v", first)
	}
	second := result.Observations[1]
	if second.NeighborCount == nil || *second.NeighborCount != 2 || second.Decision != diversity.DedupDecisionWarn ||
		len(second.Neighbors) != 2 || second.Neighbors[0].ProblemID != neighborFirst.String() {
		t.Fatalf("neighbor observation = %+v", second)
	}
	for index, observation := range result.Observations {
		if observation.Kind != diversity.DedupKindStructure {
			t.Fatalf("observation %d kind = %q, want structure", index, observation.Kind)
		}
		if err := observation.Validate(); err != nil {
			t.Fatalf("observation %d invalid: %v", index, err)
		}
	}
}

func TestObserveS5ConceptDedupBatchV1DependencyFailuresAreSecretFree(t *testing.T) {
	modelVersionID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	baseInput := S5DedupObservationBatchInputV1{
		PayloadVersion: S5DedupObservationBatchPayloadVersionV1,
		Concepts: []diversity.ConceptSpecV1{
			s5ObservationTestConcept("shortest path"),
			s5ObservationTestConcept("maximum matching"),
		},
		RequestedTopK: 4,
		Threshold:     0.76,
	}
	secret := "sk-live-do-not-leak"
	tests := []struct {
		name       string
		deps       *Dependencies
		wantReason string
	}{
		{
			name: "model unavailable",
			deps: &Dependencies{
				Embedding:        &s5ObservationEmbedder{vectors: [][]float32{{1}, {1}}},
				EmbeddingEnabled: true,
				VectorRepo:       &s5ObservationVectorStore{},
			},
			wantReason: s5DedupFailureActiveModelUnavailable,
		},
		{
			name: "embedding unavailable",
			deps: &Dependencies{
				Embedding:               &s5ObservationEmbedder{err: fmt.Errorf("provider rejected token %s", secret)},
				EmbeddingEnabled:        true,
				EmbeddingModelVersionID: modelVersionID,
				VectorRepo:              &s5ObservationVectorStore{},
			},
			wantReason: s5DedupFailureEmbeddingUnavailable,
		},
		{
			name: "vector unavailable",
			deps: &Dependencies{
				Embedding:               &s5ObservationEmbedder{vectors: [][]float32{{1}, {1}}},
				EmbeddingEnabled:        true,
				EmbeddingModelVersionID: modelVersionID,
				VectorRepo:              &s5ObservationVectorStore{err: fmt.Errorf("database DSN password=%s", secret)},
			},
			wantReason: s5DedupFailureVectorQueryUnavailable,
		},
		{
			name: "revision unavailable",
			deps: &Dependencies{
				Embedding:               &s5ObservationEmbedder{vectors: [][]float32{{1}, {1}}},
				EmbeddingEnabled:        true,
				EmbeddingModelVersionID: modelVersionID,
				VectorRepo: &s5ObservationVectorStore{err: fmt.Errorf(
					"database DSN password=%s: %w", secret, repository.ErrS5CorpusRevisionUnavailable,
				)},
			},
			wantReason: s5DedupFailureCorpusRevisionInvalid,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := New(test.deps).ObserveS5ConceptDedupBatchV1(context.Background(), baseInput)
			if err != nil {
				t.Fatal(err)
			}
			if result.CorpusRevision != "" || len(result.Observations) != len(baseInput.Concepts) {
				t.Fatalf("failed batch result = %+v", result)
			}
			for index, observation := range result.Observations {
				if observation.Decision != diversity.DedupDecisionCheckFailed || observation.Reason != test.wantReason ||
					observation.Kind != diversity.DedupKindStructure || observation.NeighborCount != nil || len(observation.Neighbors) != 0 {
					t.Fatalf("observation %d = %+v", index, observation)
				}
				if err := observation.Validate(); err != nil {
					t.Fatalf("observation %d invalid: %v", index, err)
				}
			}
			encoded, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(encoded), secret) {
				t.Fatalf("secret leaked in result: %s", encoded)
			}
		})
	}
}

func TestObserveS5ConceptDedupBatchV1RejectsInvalidBatchBeforeEffects(t *testing.T) {
	concepts := make([]diversity.ConceptSpecV1, MaxS5ConceptsPerDedupBatchV1+1)
	for index := range concepts {
		concepts[index] = s5ObservationTestConcept(fmt.Sprintf("objective %d", index))
	}
	embedder := &s5ObservationEmbedder{}
	store := &s5ObservationVectorStore{}
	_, err := New(&Dependencies{
		Embedding:               embedder,
		EmbeddingEnabled:        true,
		EmbeddingModelVersionID: uuid.New(),
		VectorRepo:              store,
	}).ObserveS5ConceptDedupBatchV1(context.Background(), S5DedupObservationBatchInputV1{
		PayloadVersion: S5DedupObservationBatchPayloadVersionV1,
		Concepts:       concepts,
		RequestedTopK:  8,
		Threshold:      0.76,
	})
	if err == nil || !strings.Contains(err.Error(), "1..12") {
		t.Fatalf("oversized batch error = %v", err)
	}
	if embedder.batchCalls != 0 || len(store.calls) != 0 {
		t.Fatal("invalid batch performed an external effect")
	}
}

func TestObserveS5ConceptDedupBatchV1FailsClosedOnInvalidEmbeddingOrRevisionDrift(t *testing.T) {
	modelVersionID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	input := S5DedupObservationBatchInputV1{
		PayloadVersion: S5DedupObservationBatchPayloadVersionV1,
		Concepts: []diversity.ConceptSpecV1{
			s5ObservationTestConcept("shortest path"),
			s5ObservationTestConcept("maximum matching"),
		},
		RequestedTopK: 2,
		Threshold:     0.8,
	}
	t.Run("invalid embedding response", func(t *testing.T) {
		result, err := New(&Dependencies{
			Embedding:               &s5ObservationEmbedder{vectors: [][]float32{{1}}},
			EmbeddingEnabled:        true,
			EmbeddingModelVersionID: modelVersionID,
			VectorRepo:              &s5ObservationVectorStore{},
		}).ObserveS5ConceptDedupBatchV1(context.Background(), input)
		if err != nil {
			t.Fatal(err)
		}
		if result.Observations[0].Reason != s5DedupFailureEmbeddingInvalid || result.Observations[0].NeighborCount != nil {
			t.Fatalf("invalid embedding result = %+v", result)
		}
	})

	t.Run("revision drift", func(t *testing.T) {
		store := &s5ObservationVectorStore{results: []repository.S5OriginalityResultV1{
			{CorpusRevision: strings.Repeat("a", 64), CorpusSize: 0, NeighborCount: 0},
			{CorpusRevision: strings.Repeat("b", 64), CorpusSize: 0, NeighborCount: 0},
		}}
		result, err := New(&Dependencies{
			Embedding:               &s5ObservationEmbedder{vectors: [][]float32{{1}, {1}}},
			EmbeddingEnabled:        true,
			EmbeddingModelVersionID: modelVersionID,
			VectorRepo:              store,
		}).ObserveS5ConceptDedupBatchV1(context.Background(), input)
		if err != nil {
			t.Fatal(err)
		}
		if result.CorpusRevision != "" || result.Observations[0].Reason != s5DedupFailureCorpusRevisionInvalid ||
			result.Observations[1].Reason != s5DedupFailureCorpusRevisionInvalid {
			t.Fatalf("revision drift result = %+v", result)
		}
	})
}

func TestCanonicalS5ConceptEmbeddingV1UsesCanonicalStructuralSpec(t *testing.T) {
	first := s5ObservationTestConcept("Shortest   Path")
	first.StateDimensions = []string{"vertex", "distance"}
	second := first
	second.Objective = " shortest path "
	second.StateDimensions = []string{"distance", "vertex"}
	firstText, firstHash, err := canonicalS5ConceptEmbeddingV1(first)
	if err != nil {
		t.Fatal(err)
	}
	secondText, secondHash, err := canonicalS5ConceptEmbeddingV1(second)
	if err != nil {
		t.Fatal(err)
	}
	if firstText != secondText || firstHash != secondHash {
		t.Fatalf("canonical embeddings differ:\n%s\n%s\n%s\n%s", firstText, secondText, firstHash, secondHash)
	}
	exportedFirst, err := S5ConceptContentHashV1(first)
	if err != nil {
		t.Fatal(err)
	}
	exportedSecond, err := S5ConceptContentHashV1(second)
	if err != nil {
		t.Fatal(err)
	}
	if exportedFirst != firstHash || exportedSecond != secondHash {
		t.Fatalf("exported content hash drifted from the embedding path: %s/%s want %s/%s", exportedFirst, exportedSecond, firstHash, secondHash)
	}
}

func TestS5ConceptContentHashV1DetectsStructuralTampering(t *testing.T) {
	original := s5ObservationTestConcept("shortest path")
	originalHash, err := S5ConceptContentHashV1(original)
	if err != nil {
		t.Fatal(err)
	}

	tampered := original
	tampered.Topology = "rooted tree"
	tamperedHash, err := S5ConceptContentHashV1(tampered)
	if err != nil {
		t.Fatal(err)
	}
	if tamperedHash == originalHash {
		t.Fatal("structural field tampering did not change the canonical content hash")
	}

	invalid := original
	invalid.CanonicalizerVersion = "forged"
	if _, err := S5ConceptContentHashV1(invalid); err == nil {
		t.Fatal("invalid ConceptSpec canonicalizer version received a content hash")
	}
}

func s5ObservationTestConcept(objective string) diversity.ConceptSpecV1 {
	return diversity.ConceptSpecV1{
		SchemaVersion:         diversity.ConceptSpecSchemaV1,
		CanonicalizerVersion:  diversity.ConceptCanonicalizerVersionV1,
		ExtractionConfidence:  0.9,
		QualityTier:           diversity.QualityTierViable,
		ProblemMode:           "offline",
		InputObject:           "graph",
		Topology:              "general graph",
		OperationModel:        "static queries",
		Objective:             objective,
		StateDimensions:       []string{"distance"},
		TransitionOrInvariant: "relax edges",
		SolutionOperatorSeq:   []string{"scan", "aggregate"},
		OutputForm:            "integer",
		ComplexityClass:       "n log n",
		ConstraintRegime:      "large",
		WrongSolutionFamilies: []string{"off by one"},
	}
}

type s5ObservationEmbedder struct {
	vectors     [][]float32
	err         error
	batchCalls  int
	singleCalls int
	texts       []string
}

func (embedder *s5ObservationEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	embedder.singleCalls++
	return nil, errors.New("single embedding call is not supported by this test double")
}

func (embedder *s5ObservationEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	embedder.batchCalls++
	embedder.texts = append([]string(nil), texts...)
	if embedder.err != nil {
		return nil, embedder.err
	}
	result := make([][]float32, len(embedder.vectors))
	for index, vector := range embedder.vectors {
		result[index] = append([]float32(nil), vector...)
	}
	return result, nil
}

type s5ObservationVectorStore struct {
	results []repository.S5OriginalityResultV1
	err     error
	calls   []repository.S5OriginalityQueryV1
}

func (store *s5ObservationVectorStore) QueryS5OriginalityV1(
	_ context.Context,
	query repository.S5OriginalityQueryV1,
) (repository.S5OriginalityResultV1, error) {
	query.Embedding = append([]float32(nil), query.Embedding...)
	query.ExcludedProblemIDs = append([]uuid.UUID(nil), query.ExcludedProblemIDs...)
	store.calls = append(store.calls, query)
	if store.err != nil {
		return repository.S5OriginalityResultV1{}, store.err
	}
	index := len(store.calls) - 1
	if index >= len(store.results) {
		return repository.S5OriginalityResultV1{}, fmt.Errorf("missing test result %d", index)
	}
	return store.results[index], nil
}

func (store *s5ObservationVectorStore) FindSimilarForVersion(
	context.Context,
	[]float32,
	uuid.UUID,
	string,
	int,
	float64,
) ([]*domain.Problem, []float64, error) {
	return nil, nil, nil
}

func (store *s5ObservationVectorStore) UpdateEmbeddingForVersion(context.Context, repository.EmbeddingWrite) error {
	return nil
}
