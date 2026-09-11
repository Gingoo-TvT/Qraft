package service

import (
	"strings"
	"testing"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/diversityapi"
	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/generationapi"
)

func TestS5MicroBatchInputKeepsThreeSingleCandidateQualityChildren(t *testing.T) {
	batchID, _, err := diversityapi.BatchIDForIdempotencyKey("local-dev", "service-fixture")
	if err != nil {
		t.Fatal(err)
	}
	params := []domain.ProblemGenParams{
		validS5ServiceParamsV1("slot zero"),
		validS5ServiceParamsV1("slot one"),
		validS5ServiceParamsV1("slot two"),
	}
	input, err := buildS5MicroBatchWorkflowInputV1(batchID, time.Hour, params)
	if err != nil {
		t.Fatal(err)
	}
	if len(input.Slots) != diversityapi.SlotCountV1 || input.ConceptBudget.MaxCreativeAttempts != 2 ||
		input.ConceptBudget.MaxConcepts != 4 || input.DedupTopK <= 0 {
		t.Fatalf("S5 input = %+v", input)
	}
	for slotIndex, slot := range input.Slots {
		expected, err := diversityapi.ChildJobID(batchID, slotIndex)
		if err != nil {
			t.Fatal(err)
		}
		if slot.SubjectID != expected || !generationapi.IsJobID(slot.SubjectID) || slot.FrozenConcept != params[slotIndex].CustomPrompt {
			t.Fatalf("slot %d = %+v", slotIndex, slot)
		}
	}
}

func TestS5MicroBatchStartOptionsExposeOnlyStableHashes(t *testing.T) {
	batchID, _, err := diversityapi.BatchIDForIdempotencyKey("user:one", "private retry key")
	if err != nil {
		t.Fatal(err)
	}
	payloadSHA := strings.Repeat("a", 64)
	principalSHA := strings.Repeat("b", 64)
	opts := s5MicroBatchStartOptionsV1(batchID, "queue", payloadSHA, principalSHA, time.Hour)
	if opts.ID != batchID || opts.WorkflowExecutionTimeout != time.Hour+s5ConceptOverheadV1 {
		t.Fatalf("start options = %+v", opts)
	}
	if opts.Memo[diversityapi.MemoPayloadSHA256Key] != payloadSHA ||
		opts.Memo[diversityapi.MemoPrincipalScopeKey] != principalSHA {
		t.Fatalf("memo = %+v", opts.Memo)
	}
	encoded := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(batchID, diversityapi.BatchIDPrefix)))
	if strings.Contains(encoded, "private") || strings.Contains(encoded, "user") {
		t.Fatalf("opaque batch id leaked caller material: %q", batchID)
	}
}

func TestS5MicroBatchInputRejectsWrongShapeAndTimeout(t *testing.T) {
	batchID, _, err := diversityapi.BatchIDForIdempotencyKey("local-dev", "negative")
	if err != nil {
		t.Fatal(err)
	}
	params := []domain.ProblemGenParams{validS5ServiceParamsV1("one"), validS5ServiceParamsV1("two")}
	if _, err := buildS5MicroBatchWorkflowInputV1(batchID, time.Hour, params); err == nil {
		t.Fatal("two-slot input was accepted")
	}
	params = append(params, validS5ServiceParamsV1("three"))
	if _, err := buildS5MicroBatchWorkflowInputV1(batchID, 0, params); err == nil {
		t.Fatal("zero child timeout was accepted")
	}
}

func validS5ServiceParamsV1(prompt string) domain.ProblemGenParams {
	config := domain.DefaultTestDataConfig()
	config.NumTestCases = 8
	config.NumSamples = 2
	return domain.ProblemGenParams{
		Level:      domain.LevelAlgorithm,
		Difficulty: 1300,
		Tags:       []string{"prefix-sum"},
		KnowledgePointCombination: &domain.KnowledgePointCombinationContract{
			SchemaVersion: domain.KnowledgePointCombinationSchemaV1,
			Mode:          domain.KnowledgePointCombinationSingle,
			MaxConcepts:   1,
		},
		ContestStyle:      "icpc",
		TimeLimit:         1000,
		MemoryLimit:       256,
		TestDataConfig:    config,
		GenerateEditorial: true,
		CustomPrompt:      prompt,
		Languages:         []string{"cpp"},
		Locale:            "zh",
		SimilarLimit:      5,
		MetadataExtras:    map[string]interface{}{generationapi.QualityEvidenceLevelMetadataKey: generationapi.EvidenceMinimal},
	}
}
