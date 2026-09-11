package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/diversity"
	"github.com/Gingoo-TvT/Qraft/backend/internal/diversityapi"
	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	qualitygate "github.com/Gingoo-TvT/Qraft/backend/internal/qualitygate/v1"
	"github.com/Gingoo-TvT/Qraft/backend/internal/workflow/activities"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
	sdkworkflow "go.temporal.io/sdk/workflow"
)

func TestS5MicroBatchGenerationWorkflowV1HonorsBarriersAndWarnIsAdvisory(t *testing.T) {
	harness := newS5MicroBatchHarnessV1(t, "warn", nil)
	result, err := harness.execute()
	if err != nil {
		t.Fatal(err)
	}
	if harness.orderViolation != "" {
		t.Fatal(harness.orderViolation)
	}
	if harness.creativeCalls != 6 || harness.normalizeCalls != 3 || harness.dedupCalls != 1 || harness.storeCalls != 1 || harness.childCalls != 3 {
		t.Fatalf("calls creative=%d normalize=%d dedup=%d store=%d child=%d", harness.creativeCalls, harness.normalizeCalls, harness.dedupCalls, harness.storeCalls, harness.childCalls)
	}
	if result.PayloadVersion != S5MicroBatchPayloadVersionV1 || result.BatchID != harness.input.BatchID || len(result.Children) != 3 || len(result.DedupObservations) != 12 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if result.DedupObservations[0].Decision != diversity.DedupDecisionWarn {
		t.Fatalf("warn observation was not preserved: %+v", result.DedupObservations[0])
	}
	if len(harness.childInputs) != 3 {
		t.Fatalf("child inputs = %d", len(harness.childInputs))
	}
	for slotIndex, child := range harness.childInputs {
		if child.FrozenConcept == "" || child.FrozenConcept != child.Params.CustomPrompt {
			t.Fatalf("slot %d winner pitch was not frozen into both child fields: %+v", slotIndex, child)
		}
		metadata, ok := child.Params.MetadataExtras["s5_micro_batch_v1"].(map[string]interface{})
		if !ok || metadata["selection_state"] != "reserved" || metadata["parent_batch_id"] != harness.input.BatchID ||
			metadata["reservation_sha256"] != result.ReservationSHA256 || metadata["manifest_sha256"] != result.ManifestSHA256 || metadata["concept_id"] == "" {
			t.Fatalf("slot %d reserved metadata = %#v", slotIndex, child.Params.MetadataExtras)
		}
	}
	queryValue, err := harness.env.QueryWorkflow(S5MicroBatchStateQueryName)
	if err != nil {
		t.Fatal(err)
	}
	var state S5MicroBatchStateV1
	if err := queryValue.Get(&state); err != nil {
		t.Fatal(err)
	}
	if state.Status != domain.WorkflowStatusCompleted || state.Phase != S5MicroBatchPhaseComplete || state.Progress != 100 || len(state.Children) != 3 {
		t.Fatalf("completed state = %+v", state)
	}
}

func TestS5MicroBatchGenerationWorkflowV1StopsUnavailableOrSplicedDedupBeforeReservation(t *testing.T) {
	for _, mode := range []string{"check_failed", "empty_revision", "mixed_revision", "mixed_model", "spliced_hash", "rejected", "warn_below_threshold"} {
		t.Run(mode, func(t *testing.T) {
			harness := newS5MicroBatchHarnessV1(t, mode, nil)
			_, err := harness.execute()
			if err == nil || !strings.Contains(err.Error(), s5MicroBatchContractErrorTypeV1) {
				t.Fatalf("workflow error = %v", err)
			}
			if harness.creativeCalls != 6 || harness.normalizeCalls != 3 || harness.dedupCalls != 1 || harness.storeCalls != 0 || harness.childCalls != 0 {
				t.Fatalf("mode %s calls creative=%d normalize=%d dedup=%d store=%d child=%d", mode, harness.creativeCalls, harness.normalizeCalls, harness.dedupCalls, harness.storeCalls, harness.childCalls)
			}
		})
	}
}

func TestS5MicroBatchGenerationWorkflowV1RequiresReservationCASBeforeChildren(t *testing.T) {
	harness := newS5MicroBatchHarnessV1(t, "pass", errors.New("CAS unavailable"))
	_, err := harness.execute()
	if err == nil || !strings.Contains(err.Error(), "CAS unavailable") {
		t.Fatalf("workflow error = %v", err)
	}
	if harness.storeCalls != 1 || harness.childCalls != 0 {
		t.Fatalf("store calls=%d child calls=%d", harness.storeCalls, harness.childCalls)
	}
}

func TestS5MicroBatchGenerationWorkflowV1DoesNotReplayCreativeActivity(t *testing.T) {
	harness := newS5MicroBatchHarnessV1(t, "pass", nil)
	harness.creativeError = errors.New("creative provider failed after its internal retry budget")
	_, err := harness.execute()
	if err == nil || !strings.Contains(err.Error(), harness.creativeError.Error()) {
		t.Fatalf("workflow error = %v", err)
	}
	if harness.failedCreativeCalls != 1 {
		t.Fatalf("failed logical creative activity calls = %d, want exactly 1", harness.failedCreativeCalls)
	}
	if harness.normalizeCalls != 0 || harness.dedupCalls != 0 || harness.storeCalls != 0 || harness.childCalls != 0 {
		t.Fatalf("creative failure crossed a later barrier: normalize=%d dedup=%d store=%d child=%d", harness.normalizeCalls, harness.dedupCalls, harness.storeCalls, harness.childCalls)
	}
}

func TestS5MicroBatchGenerationWorkflowV1IsDeterministicAcrossTwoRuns(t *testing.T) {
	first := newS5MicroBatchHarnessV1(t, "warn", nil)
	firstResult, err := first.execute()
	if err != nil {
		t.Fatal(err)
	}
	second := newS5MicroBatchHarnessV1(t, "warn", nil)
	secondResult, err := second.execute()
	if err != nil {
		t.Fatal(err)
	}
	firstJSON, _ := json.Marshal(firstResult)
	secondJSON, _ := json.Marshal(secondResult)
	if string(firstJSON) != string(secondJSON) {
		t.Fatalf("results differ\nfirst:  %s\nsecond: %s", firstJSON, secondJSON)
	}
	if string(first.manifestBytes) != string(second.manifestBytes) || !reflect.DeepEqual(first.childInputs, second.childInputs) {
		t.Fatal("manifest or reserved child inputs changed across identical runs")
	}
}

func TestS5MicroBatchGenerationWorkflowV1RejectsNonDerivedChildBeforeEffects(t *testing.T) {
	harness := newS5MicroBatchHarnessV1(t, "pass", nil)
	harness.input.Slots[1].SubjectID = diversityapi.BatchIDPrefix + strings.Repeat("a", 64)
	_, err := harness.execute()
	if err == nil || !strings.Contains(err.Error(), s5MicroBatchContractErrorTypeV1) {
		t.Fatalf("workflow error = %v", err)
	}
	if harness.creativeCalls != 0 || harness.normalizeCalls != 0 || harness.dedupCalls != 0 || harness.storeCalls != 0 || harness.childCalls != 0 {
		t.Fatalf("invalid input produced effects: %+v", harness)
	}
}

type s5MicroBatchHarnessV1 struct {
	t                   *testing.T
	env                 *testsuite.TestWorkflowEnvironment
	input               S5MicroBatchInputV1
	dedupMode           string
	storeError          error
	creativeError       error
	mu                  sync.Mutex
	creativeCalls       int
	failedCreativeCalls int
	normalizeCalls      int
	dedupCalls          int
	storeCalls          int
	childCalls          int
	orderViolation      string
	manifestBytes       []byte
	childInputs         []ProblemGenerationQualityInputV1
}

func newS5MicroBatchHarnessV1(t *testing.T, dedupMode string, storeError error) *s5MicroBatchHarnessV1 {
	t.Helper()
	var suite testsuite.WorkflowTestSuite
	harness := &s5MicroBatchHarnessV1{
		t:           t,
		env:         suite.NewTestWorkflowEnvironment(),
		input:       s5MicroBatchFixtureInputV1(t),
		dedupMode:   dedupMode,
		storeError:  storeError,
		childInputs: make([]ProblemGenerationQualityInputV1, diversityapi.SlotCountV1),
	}
	harness.env.RegisterWorkflow(S5MicroBatchGenerationWorkflowV1)
	harness.registerActivities()
	harness.env.RegisterWorkflowWithOptions(
		func(_ sdkworkflow.Context, input ProblemGenerationQualityInputV1) (*ProblemGenerationQualityResultV1, error) {
			harness.mu.Lock()
			defer harness.mu.Unlock()
			harness.childCalls++
			if harness.storeCalls != 1 && harness.orderViolation == "" {
				harness.orderViolation = "child workflow started before reservation CAS"
			}
			slotIndex := -1
			for index := 0; index < diversityapi.SlotCountV1; index++ {
				expected, _ := diversityapi.ChildJobID(harness.input.BatchID, index)
				if input.SubjectID == expected {
					slotIndex = index
					break
				}
			}
			if slotIndex < 0 {
				return nil, fmt.Errorf("unknown child id %s", input.SubjectID)
			}
			harness.childInputs[slotIndex] = input
			return &ProblemGenerationQualityResultV1{
				PayloadVersion:      ProblemGenerationQualityPayloadVersionV1,
				Decision:            qualitygate.DecisionPass,
				StoredProblemID:     fmt.Sprintf("00000000-0000-0000-0000-%012d", slotIndex+1),
				StoredProblemStatus: domain.ProblemStatusDraft,
			}, nil
		},
		sdkworkflow.RegisterOptions{Name: "ProblemGenerationQualityWorkflowV1"},
	)
	return harness
}

func (h *s5MicroBatchHarnessV1) registerActivities() {
	h.env.RegisterActivityWithOptions(
		func(_ context.Context, input activities.GenerateS5ConceptAttemptInputV1) (*activities.GenerateS5ConceptAttemptResultV1, error) {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.creativeCalls++
			if h.creativeError != nil && input.SlotIndex == 0 && input.AttemptIndex == 0 {
				h.failedCreativeCalls++
				return nil, h.creativeError
			}
			attempt := diversity.ConceptAttemptV1{
				AttemptIndex:     input.AttemptIndex,
				LogicalAttemptID: input.LogicalAttemptID,
				Receipt: diversity.ArtifactRefV1{
					SHA256: strings.Repeat(string(rune('a'+input.SlotIndex*2+input.AttemptIndex)), 64),
					URI:    "cas://sha256/" + strings.Repeat(string(rune('a'+input.SlotIndex*2+input.AttemptIndex)), 64),
				},
				Usage:    diversity.ConceptAttemptUsageV1{ModelCalls: 1, Tokens: 10, WallMilliseconds: 1},
				Concepts: make([]diversity.ConceptCardV1, 2),
			}
			for conceptIndex := range attempt.Concepts {
				conceptID, _ := activities.S5ConceptIDV1(input.BatchID, input.SlotIndex, input.AttemptIndex, conceptIndex)
				attempt.Concepts[conceptIndex] = diversity.ConceptCardV1{
					ConceptIndex:        conceptIndex,
					ConceptID:           conceptID,
					OneParagraphPitch:   fmt.Sprintf("slot %d attempt %d concept %d deterministic pitch", input.SlotIndex, input.AttemptIndex, conceptIndex),
					UnresolvedQuestions: []string{"none"},
				}
			}
			return &activities.GenerateS5ConceptAttemptResultV1{
				PayloadVersion: activities.GenerateS5ConceptAttemptPayloadVersionV1,
				BatchID:        input.BatchID, SlotIndex: input.SlotIndex, SlotID: input.SlotID,
				AttemptIndex: input.AttemptIndex, BriefSHA256: input.BriefSHA256, Attempt: attempt,
			}, nil
		},
		activity.RegisterOptions{Name: "GenerateS5ConceptAttemptActivityV1"},
	)
	h.env.RegisterActivityWithOptions(
		func(_ context.Context, input activities.NormalizeS5ConceptPoolInputV1) (*activities.NormalizeS5ConceptPoolResultV1, error) {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.normalizeCalls++
			if h.creativeCalls != 6 && h.orderViolation == "" {
				h.orderViolation = fmt.Sprintf("normalizer started after only %d creative calls", h.creativeCalls)
			}
			attempts := input.Attempts
			for attemptIndex := range attempts {
				attempts[attemptIndex].Concepts = append([]diversity.ConceptCardV1(nil), attempts[attemptIndex].Concepts...)
				for conceptIndex := range attempts[attemptIndex].Concepts {
					attempts[attemptIndex].Concepts[conceptIndex].Spec = s5ConceptSpecFixtureV1(input.SlotIndex, attemptIndex, conceptIndex)
				}
			}
			draft := activities.S5NormalizedConceptPoolDraftV1{
				SchemaVersion: activities.S5NormalizedConceptPoolDraftSchemaV1,
				BatchID:       input.BatchID, SlotIndex: input.SlotIndex, SlotID: input.SlotID,
				BriefSHA256: input.BriefSHA256, Budget: input.Budget, Attempts: attempts,
				Normalization: diversity.ConceptNormalizationV1{
					NormalizerVersion: activities.S5ConceptNormalizerVersionV1,
					Receipt: diversity.ArtifactRefV1{
						SHA256: strings.Repeat(string(rune('7'+input.SlotIndex)), 64),
						URI:    "cas://sha256/" + strings.Repeat(string(rune('7'+input.SlotIndex)), 64),
					},
					Usage: diversity.ConceptAttemptUsageV1{ModelCalls: 1, Tokens: 10, WallMilliseconds: 1},
				},
			}
			encoded, _ := json.Marshal(draft)
			return &activities.NormalizeS5ConceptPoolResultV1{
				PayloadVersion: activities.NormalizeS5ConceptPoolPayloadVersionV1,
				Draft:          draft,
				DraftSHA256:    diversity.SHA256Hex(encoded),
			}, nil
		},
		activity.RegisterOptions{Name: "NormalizeS5ConceptPoolActivityV1"},
	)
	h.env.RegisterActivityWithOptions(
		func(_ context.Context, input activities.S5DedupObservationBatchInputV1) (*activities.S5DedupObservationBatchResultV1, error) {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.dedupCalls++
			if h.creativeCalls != 6 || h.normalizeCalls != 3 {
				h.orderViolation = fmt.Sprintf("dedup started at creative=%d normalize=%d", h.creativeCalls, h.normalizeCalls)
			}
			return s5DedupFixtureResultV1(input, h.dedupMode), nil
		},
		activity.RegisterOptions{Name: "ObserveS5ConceptDedupBatchV1"},
	)
	h.env.RegisterActivityWithOptions(
		func(_ context.Context, input activities.StoreS5ProvisionalReservationInputV1) (*activities.StoreS5ProvisionalReservationResultV1, error) {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.storeCalls++
			if h.dedupCalls != 1 && h.orderViolation == "" {
				h.orderViolation = "reservation store started before the dedup observation"
			}
			if h.storeError != nil {
				return nil, h.storeError
			}
			h.manifestBytes, _ = json.Marshal(input.Manifest)
			digest := diversity.SHA256Hex(h.manifestBytes)
			return &activities.StoreS5ProvisionalReservationResultV1{
				PayloadVersion: activities.StoreS5ProvisionalReservationPayloadVersionV1,
				ManifestSHA256: digest,
				ManifestArtifact: &activities.ArtifactRef{
					SHA256: digest, Key: "sha256/" + digest, ContentType: "application/json",
				},
			}, nil
		},
		activity.RegisterOptions{Name: "StoreS5ProvisionalReservationActivityV1"},
	)
}

func (h *s5MicroBatchHarnessV1) execute() (*S5MicroBatchResultV1, error) {
	h.env.ExecuteWorkflow(S5MicroBatchGenerationWorkflowV1, h.input)
	if err := h.env.GetWorkflowError(); err != nil {
		return nil, err
	}
	var result S5MicroBatchResultV1
	if err := h.env.GetWorkflowResult(&result); err != nil {
		return nil, err
	}
	return &result, nil
}

func s5MicroBatchFixtureInputV1(t *testing.T) S5MicroBatchInputV1 {
	t.Helper()
	batchID := diversityapi.BatchIDPrefix + strings.Repeat("1", 64)
	slots := make([]ProblemGenerationQualityInputV1, diversityapi.SlotCountV1)
	for slotIndex := range slots {
		jobID, err := diversityapi.ChildJobID(batchID, slotIndex)
		if err != nil {
			t.Fatal(err)
		}
		params := domain.DefaultProblemGenParams()
		params.Tags = []string{"prefix-sum"}
		params.CustomPrompt = fmt.Sprintf("initial slot %d authoring intent", slotIndex)
		params.MetadataExtras = map[string]interface{}{"existing": "kept"}
		slots[slotIndex] = ProblemGenerationQualityInputV1{
			PayloadVersion: ProblemGenerationQualityPayloadVersionV1,
			SubjectID:      jobID,
			Language:       "cpp",
			FrozenConcept:  params.CustomPrompt,
			Params:         params,
		}
	}
	return S5MicroBatchInputV1{
		PayloadVersion: S5MicroBatchPayloadVersionV1,
		BatchID:        batchID,
		Slots:          slots,
		ConceptBudget: diversity.ConceptBudgetV1{
			MaxCreativeAttempts: 2, MaxConcepts: 4, MaxModelCalls: 3,
			MaxNetworkRetries: 0, MaxTokens: 1000, MaxWallMilliseconds: 10000,
		},
		ConceptNetworkRetryBudget: 0,
		DedupTopK:                 8,
		DedupThreshold:            0.9,
		ChildWorkflowTimeout:      2 * time.Hour,
	}
}

func s5ConceptSpecFixtureV1(slotIndex, attemptIndex, conceptIndex int) diversity.ConceptSpecV1 {
	variant := slotIndex*4 + attemptIndex*2 + conceptIndex
	return diversity.ConceptSpecV1{
		SchemaVersion:         diversity.ConceptSpecSchemaV1,
		CanonicalizerVersion:  diversity.ConceptCanonicalizerVersionV1,
		ExtractionConfidence:  0.9,
		QualityTier:           diversity.QualityTierViable,
		ProblemMode:           "offline",
		InputObject:           fmt.Sprintf("sequence-%d", variant),
		Topology:              fmt.Sprintf("topology-%d", slotIndex),
		OperationModel:        "static",
		Objective:             fmt.Sprintf("objective-%d", variant),
		StateDimensions:       []string{fmt.Sprintf("dimension-%d", variant)},
		TransitionOrInvariant: fmt.Sprintf("invariant-%d", variant),
		SolutionOperatorSeq:   []string{"scan", fmt.Sprintf("operator-%d", variant)},
		OutputForm:            "integer",
		ComplexityClass:       "linear",
		ConstraintRegime:      "large",
		WrongSolutionFamilies: []string{"greedy"},
	}
}

func s5DedupFixtureResultV1(input activities.S5DedupObservationBatchInputV1, mode string) *activities.S5DedupObservationBatchResultV1 {
	revision := strings.Repeat("c", 64)
	result := &activities.S5DedupObservationBatchResultV1{
		PayloadVersion: activities.S5DedupObservationBatchPayloadVersionV1,
		CorpusRevision: revision,
		Observations:   make([]diversity.DedupObservationV1, len(input.Concepts)),
	}
	for index, spec := range input.Concepts {
		contentHash, _ := activities.S5ConceptContentHashV1(spec)
		count := 0
		result.Observations[index] = diversity.DedupObservationV1{
			SchemaVersion: diversity.DedupObservationSchemaV1,
			Stage:         diversity.DedupStageConceptSelection, ModelVersion: "model-v1", Kind: diversity.DedupKindStructure,
			ContentHash: contentHash, CorpusRevision: revision, RequestedTopK: input.RequestedTopK,
			Threshold: input.Threshold, Decision: diversity.DedupDecisionPass, NeighborCount: &count,
			ExcludedLineageIDs: append([]string(nil), input.ExcludedLineageIDs...),
		}
	}
	switch mode {
	case "warn":
		count := 1
		result.Observations[0].Decision = diversity.DedupDecisionWarn
		result.Observations[0].NeighborCount = &count
		result.Observations[0].Neighbors = []diversity.DedupNeighborV1{{
			Rank: 1, ProblemID: "00000000-0000-0000-0000-000000000111", ContentHash: strings.Repeat("d", 64),
			CorpusTier: diversity.CorpusTierHistoricalAdvisory, Similarity: 0.95,
		}}
	case "warn_below_threshold":
		count := 1
		result.Observations[0].Decision = diversity.DedupDecisionWarn
		result.Observations[0].NeighborCount = &count
		result.Observations[0].Neighbors = []diversity.DedupNeighborV1{{
			Rank: 1, ProblemID: "00000000-0000-0000-0000-000000000111", ContentHash: strings.Repeat("d", 64),
			CorpusTier: diversity.CorpusTierHistoricalAdvisory, Similarity: 0.5,
		}}
	case "check_failed":
		result.CorpusRevision = ""
		for index := range result.Observations {
			result.Observations[index].Decision = diversity.DedupDecisionCheckFailed
			result.Observations[index].NeighborCount = nil
			result.Observations[index].Reason = "embedding_unavailable"
		}
	case "empty_revision":
		result.CorpusRevision = ""
	case "mixed_revision":
		result.Observations[len(result.Observations)-1].CorpusRevision = strings.Repeat("e", 64)
	case "mixed_model":
		result.Observations[len(result.Observations)-1].ModelVersion = "model-v2"
	case "spliced_hash":
		result.Observations[0].ContentHash = strings.Repeat("f", 64)
	case "rejected":
		result.Observations[0].Decision = diversity.DedupDecisionRejected
	}
	return result
}
