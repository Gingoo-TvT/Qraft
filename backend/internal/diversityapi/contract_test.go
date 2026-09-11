package diversityapi

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/generationapi"
)

func validSlotV1(requirement string) generationapi.Request {
	return generationapi.Request{
		SchemaVersion: generationapi.RequestSchemaVersion,
		Domain: generationapi.DomainTarget{
			Name: "competitive_programming", Level: "algorithm",
			KnowledgePoints: []string{"prefix-sum"},
			Combination:     generationapi.KnowledgePointCombination{Mode: "single", MaxConcepts: 1},
		},
		Difficulty:  generationapi.DifficultyTarget{Rating: 1300, CalibrationProfile: "default"},
		ProblemType: generationapi.ProblemTypeStandard,
		Constraints: generationapi.ConstraintTarget{TimeLimitMS: 1000, MemoryLimitMB: 256, TestCaseCount: 8, SampleCount: 2},
		Quality: generationapi.QualityTarget{
			Strategy: "baseline", DedupMode: "reject", SimilarLimit: 5,
			Budget: generationapi.QualityBudget{MaxWallTimeSeconds: 3600},
		},
		CandidateCount: 1,
		Output: generationapi.OutputTarget{
			EvidenceLevel: generationapi.EvidenceMinimal, Formats: []string{"algoforge"},
			IncludeEditorial: true, IncludeSolutions: true, IncludeTestData: true,
		},
		Locale: "zh", Languages: []string{"cpp"}, ContestStyle: "icpc",
		CustomRequirements: requirement,
		Runtime:            generationapi.RuntimeSelection{StatementProfile: "default", VerificationProfile: "default"},
	}
}

func TestProductResponseDTOsDoNotExposeTemporalInternals(t *testing.T) {
	batchID, _, err := BatchIDForIdempotencyKey("local-dev", "response-fixture")
	if err != nil {
		t.Fatal(err)
	}
	childID, err := ChildJobID(batchID, 0)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(BatchStatusV1{
		ContractVersion: ContractVersion,
		BatchID:         batchID,
		Status:          generationapi.JobStatusRunning,
		Phase:           generationapi.JobPhaseGenerating,
		Progress:        25,
		Slots:           []SlotStatusV1{{SlotIndex: 0, ChildID: childID, Status: generationapi.JobStatusQueued}},
		Links:           BatchLinksV1{Status: "/status", Result: "/result", Cancel: "/cancel"},
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded := string(payload)
	for _, forbidden := range []string{"workflow_id", "run_id", "task_queue", "temporal"} {
		if strings.Contains(encoded, forbidden) {
			t.Fatalf("response leaked %q: %s", forbidden, encoded)
		}
	}
	if !strings.Contains(encoded, `"batch_id":"`+batchID+`"`) || !strings.Contains(encoded, `"child_id":"`+childID+`"`) {
		t.Fatalf("stable identities missing: %s", encoded)
	}
}

func validRequestV1() RequestV1 {
	return RequestV1{SchemaVersion: ContractVersion, Slots: []generationapi.Request{
		validSlotV1("slot zero"), validSlotV1("slot one"), validSlotV1("slot two"),
	}}
}

func TestCanonicalPayloadIsStableAndSlotOrderIsBinding(t *testing.T) {
	left := validRequestV1()
	right := validRequestV1()
	right.Slots[0].Languages = []string{" CPP "}
	right.Slots[0].Output.Formats = []string{" ALGOFORGE "}
	leftBytes, leftSHA, err := left.CanonicalPayload()
	if err != nil {
		t.Fatal(err)
	}
	rightBytes, rightSHA, err := right.CanonicalPayload()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(leftBytes, rightBytes) || leftSHA != rightSHA {
		t.Fatalf("canonical equivalents differ: %s / %s", leftSHA, rightSHA)
	}

	right = validRequestV1()
	right.Slots[0], right.Slots[1] = right.Slots[1], right.Slots[0]
	_, swappedSHA, err := right.CanonicalPayload()
	if err != nil {
		t.Fatal(err)
	}
	if swappedSHA == leftSHA {
		t.Fatal("slot reordering did not change the frozen micro-batch identity")
	}
}

func TestRequestRequiresExactlyThreeStableSingleCandidateSlots(t *testing.T) {
	request := validRequestV1()
	request.Slots = request.Slots[:2]
	if err := request.Validate(); err == nil {
		t.Fatal("two-slot request was accepted")
	}
	request = validRequestV1()
	request.Slots[1].CandidateCount = 3
	if err := request.Validate(); err == nil {
		t.Fatal("multi-candidate inner request widened generation-jobs v1")
	}
	request = validRequestV1()
	request.SchemaVersion = "future"
	if err := request.Validate(); err == nil {
		t.Fatal("unknown schema was accepted")
	}
}

func TestBatchAndChildIdentitiesAreOpaqueAndDeterministic(t *testing.T) {
	batch, scope, err := BatchIDForIdempotencyKey("user:one", "private retry key")
	if err != nil {
		t.Fatal(err)
	}
	replay, replayScope, err := BatchIDForIdempotencyKey("user:one", "private retry key")
	if err != nil {
		t.Fatal(err)
	}
	if batch != replay || scope != replayScope || !IsBatchID(batch) {
		t.Fatalf("unstable batch identity: %q / %q", batch, replay)
	}
	first, err := ChildJobID(batch, 0)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ChildJobID(batch, 1)
	if err != nil {
		t.Fatal(err)
	}
	if first == second || !generationapi.IsJobID(first) || !generationapi.IsJobID(second) {
		t.Fatalf("invalid child identities: %q / %q", first, second)
	}
	if _, err := ChildJobID(batch, 3); err == nil {
		t.Fatal("out-of-range slot received a child identity")
	}
}
