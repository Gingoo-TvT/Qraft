package replay_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	algoworkflow "github.com/Gingoo-TvT/Qraft/backend/internal/workflow"
	"go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/worker"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

const legacyFailOpenHistorySHA256 = "ee13870e9adf867c931a45f2da70e301a311fdf3d8a9a66d1f15c8c3406db6ff"
const reviewGateV1HistorySHA256 = "a92dc50acba185def43fd054a66e6de2540017d5b61183b97d925142022da1ee"

type replayManifest struct {
	SchemaVersion         int    `json:"schema_version"`
	WorkflowType          string `json:"workflow_type"`
	WorkflowID            string `json:"workflow_id"`
	RunID                 string `json:"run_id"`
	SourceRevision        string `json:"source_revision"`
	TemporalServerVersion string `json:"temporal_server_version"`
	TemporalGoSDKVersion  string `json:"temporal_go_sdk_version"`
	Scenario              string `json:"scenario"`
	EventCount            int    `json:"event_count"`
	HistoryFile           string `json:"history_file"`
	HistorySHA256         string `json:"history_sha256"`
	Sanitization          string `json:"sanitization"`
}

type discardLogger struct{}

func (discardLogger) Debug(string, ...interface{}) {}
func (discardLogger) Info(string, ...interface{})  {}
func (discardLogger) Warn(string, ...interface{})  {}
func (discardLogger) Error(string, ...interface{}) {}

func TestProblemGenerationLegacyFailOpenHistoryReplay(t *testing.T) {
	manifestPath := filepath.Join("testdata", "problem_generation_legacy_fail_open.manifest.json")
	manifestData, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read replay manifest: %v", err)
	}
	var manifest replayManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		t.Fatalf("decode replay manifest: %v", err)
	}
	if manifest.SchemaVersion != 1 || manifest.WorkflowType != "ProblemGenerationWorkflow" {
		t.Fatalf("unexpected replay manifest identity: %+v", manifest)
	}
	if manifest.WorkflowID == "" || manifest.RunID == "" || manifest.Sanitization == "" {
		t.Fatalf("incomplete replay manifest provenance: %+v", manifest)
	}
	if manifest.SourceRevision != "0f25c01b9a211b128e5ef1afaa5244b540c5f28d" ||
		manifest.TemporalServerVersion != "1.25.2" || manifest.TemporalGoSDKVersion != "1.30.1" ||
		manifest.Scenario == "" || manifest.EventCount != 75 {
		t.Fatalf("unexpected replay capture metadata: %+v", manifest)
	}
	if manifest.HistorySHA256 != legacyFailOpenHistorySHA256 {
		t.Fatalf("manifest history SHA256 = %s, want %s", manifest.HistorySHA256, legacyFailOpenHistorySHA256)
	}

	historyPath := filepath.Join("testdata", manifest.HistoryFile)
	historyData, err := os.ReadFile(historyPath)
	if err != nil {
		t.Fatalf("read replay history: %v", err)
	}
	digest := sha256.Sum256(historyData)
	if got := hex.EncodeToString(digest[:]); got != legacyFailOpenHistorySHA256 {
		t.Fatalf("history SHA256 = %s, want %s", got, legacyFailOpenHistorySHA256)
	}
	assertHistoryGenerationEvidenceNil(t, historyData)

	replayer := worker.NewWorkflowReplayer()
	replayer.RegisterWorkflow(algoworkflow.ProblemGenerationWorkflow)
	if err := replayer.ReplayWorkflowHistoryFromJSONFile(discardLogger{}, historyPath); err != nil {
		t.Fatalf("replay legacy fail-open history: %v", err)
	}
}

func TestProblemGenerationReviewGateV1HistoryReplay(t *testing.T) {
	manifestPath := filepath.Join("testdata", "problem_generation_review_gate_v1.manifest.json")
	manifestData, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read v1 replay manifest: %v", err)
	}
	var manifest replayManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		t.Fatalf("decode v1 replay manifest: %v", err)
	}
	if manifest.SchemaVersion != 1 || manifest.WorkflowType != "ProblemGenerationWorkflow" ||
		manifest.SourceRevision != "9ead77799a8c1ed4a2dfe9a1b743c362f55eb132" ||
		manifest.TemporalServerVersion != "1.25.2" || manifest.TemporalGoSDKVersion != "1.30.1" ||
		manifest.EventCount != 84 || manifest.HistorySHA256 != reviewGateV1HistorySHA256 {
		t.Fatalf("unexpected v1 replay manifest: %+v", manifest)
	}

	historyPath := filepath.Join("testdata", manifest.HistoryFile)
	historyData, err := os.ReadFile(historyPath)
	if err != nil {
		t.Fatalf("read v1 replay history: %v", err)
	}
	digest := sha256.Sum256(historyData)
	if got := hex.EncodeToString(digest[:]); got != reviewGateV1HistorySHA256 {
		t.Fatalf("v1 history SHA256 = %s, want %s", got, reviewGateV1HistorySHA256)
	}
	assertHistoryGenerationEvidenceNil(t, historyData)

	var history historypb.History
	if err := protojson.Unmarshal(historyData, &history); err != nil {
		t.Fatalf("decode v1 replay history: %v", err)
	}
	var v1Markers, v2Markers, differentialMarkers, finalSimilarityMarkers, reviewTimers, storeActivities, completions int
	for _, event := range history.Events {
		switch event.GetEventType() {
		case enums.EVENT_TYPE_MARKER_RECORDED:
			attrs := event.GetMarkerRecordedEventAttributes()
			if attrs == nil || attrs.GetMarkerName() != "Version" {
				continue
			}
			var changeID string
			if payloads := attrs.GetDetails()["change-id"]; payloads != nil {
				if err := converter.GetDefaultDataConverter().FromPayloads(payloads, &changeID); err != nil {
					t.Fatalf("decode version marker change ID: %v", err)
				}
			}
			switch changeID {
			case "problem-generation-review-gate-v1":
				v1Markers++
			case "problem-generation-review-gate-v2":
				v2Markers++
			case "problem-generation-differential-validation-v1":
				differentialMarkers++
			case "problem-generation-final-statement-similarity-v1":
				finalSimilarityMarkers++
			}
		case enums.EVENT_TYPE_TIMER_STARTED:
			if attrs := event.GetTimerStartedEventAttributes(); attrs != nil && attrs.GetStartToFireTimeout().AsDuration() == 72*time.Hour {
				reviewTimers++
			}
		case enums.EVENT_TYPE_ACTIVITY_TASK_SCHEDULED:
			if attrs := event.GetActivityTaskScheduledEventAttributes(); attrs != nil && attrs.GetActivityType().GetName() == "StoreProblemActivity" {
				storeActivities++
			}
		case enums.EVENT_TYPE_WORKFLOW_EXECUTION_COMPLETED:
			completions++
		}
	}
	if v1Markers != 1 || v2Markers != 0 || differentialMarkers != 0 || finalSimilarityMarkers != 0 ||
		reviewTimers != 1 || storeActivities != 1 || completions != 1 {
		t.Fatalf("v1 history contract markers=%d v2_markers=%d differential_markers=%d final_similarity_markers=%d timers=%d stores=%d completions=%d",
			v1Markers, v2Markers, differentialMarkers, finalSimilarityMarkers, reviewTimers, storeActivities, completions)
	}

	replayer := worker.NewWorkflowReplayer()
	replayer.RegisterWorkflow(algoworkflow.ProblemGenerationWorkflow)
	if err := replayer.ReplayWorkflowHistoryFromJSONFile(discardLogger{}, historyPath); err != nil {
		t.Fatalf("replay v1 review-gate history: %v", err)
	}
}

func TestProblemGenerationOptionalLiveHistoryReplay(t *testing.T) {
	historyPath := strings.TrimSpace(os.Getenv("ALGOFORGE_LIVE_PROBLEM_HISTORY"))
	if historyPath == "" {
		t.Skip("ALGOFORGE_LIVE_PROBLEM_HISTORY is not set")
	}
	if _, err := os.Stat(historyPath); err != nil {
		t.Fatalf("stat live replay history: %v", err)
	}

	replayer := worker.NewWorkflowReplayer()
	replayer.RegisterWorkflow(algoworkflow.ProblemGenerationWorkflow)
	if err := replayer.ReplayWorkflowHistoryFromJSONFile(discardLogger{}, historyPath); err != nil {
		t.Fatalf("replay live problem-generation history: %v", err)
	}
}

func TestProblemGenerationStructuredSamplesV1ZeroSampleDerivedHistoryReplay(t *testing.T) {
	// This is deliberately a derived SDK replay fixture, not a captured live
	// sample_count=0 run. It starts from the checked-in complete Temporal server
	// history whose bytes are pinned above, changes the synthetic workflow input
	// to zero samples, and adds the v1 Version marker that such a history records.
	historyPath := filepath.Join("testdata", "problem_generation_legacy_fail_open.json")
	historyData, err := os.ReadFile(historyPath)
	if err != nil {
		t.Fatalf("read complete base replay history: %v", err)
	}
	digest := sha256.Sum256(historyData)
	if got := hex.EncodeToString(digest[:]); got != legacyFailOpenHistorySHA256 {
		t.Fatalf("base history SHA256 = %s, want %s", got, legacyFailOpenHistorySHA256)
	}

	var history historypb.History
	if err := protojson.Unmarshal(historyData, &history); err != nil {
		t.Fatalf("decode complete base replay history: %v", err)
	}
	if err := deriveStructuredSamplesV1ZeroSampleHistory(&history); err != nil {
		t.Fatalf("derive structured-samples v1 zero-sample history: %v", err)
	}
	for index, event := range history.Events {
		if expected := int64(index + 1); event.GetEventId() != expected {
			t.Fatalf("derived history event index %d has event_id=%d, want %d", index, event.GetEventId(), expected)
		}
	}

	var v1Markers, v2Markers, finalizeActivities int
	for _, event := range history.Events {
		switch event.GetEventType() {
		case enums.EVENT_TYPE_MARKER_RECORDED:
			attrs := event.GetMarkerRecordedEventAttributes()
			if attrs == nil || attrs.GetMarkerName() != "Version" {
				continue
			}
			var changeID string
			if payloads := attrs.GetDetails()["change-id"]; payloads != nil {
				if err := converter.GetDefaultDataConverter().FromPayloads(payloads, &changeID); err != nil {
					t.Fatalf("decode structured-samples marker change ID: %v", err)
				}
			}
			switch changeID {
			case "problem-generation-structured-samples-v1":
				v1Markers++
			case "problem-generation-structured-samples-v2":
				v2Markers++
			}
		case enums.EVENT_TYPE_ACTIVITY_TASK_SCHEDULED:
			attrs := event.GetActivityTaskScheduledEventAttributes()
			if attrs != nil && attrs.GetActivityType().GetName() == "FinalizeStatementSamplesActivity" {
				finalizeActivities++
			}
		}
	}
	if v1Markers != 1 || v2Markers != 0 || finalizeActivities != 0 {
		t.Fatalf("derived v1 zero-sample history v1_markers=%d v2_markers=%d finalize_activities=%d",
			v1Markers, v2Markers, finalizeActivities)
	}

	replayer := worker.NewWorkflowReplayer()
	replayer.RegisterWorkflow(algoworkflow.ProblemGenerationWorkflow)
	if err := replayer.ReplayWorkflowHistory(discardLogger{}, &history); err != nil {
		t.Fatalf("replay derived structured-samples v1 zero-sample history: %v", err)
	}
}

func deriveStructuredSamplesV1ZeroSampleHistory(history *historypb.History) error {
	if history == nil {
		return fmt.Errorf("complete base history is nil")
	}
	if len(history.Events) < 9 {
		return fmt.Errorf("complete base history has %d events, want at least 9", len(history.Events))
	}
	started := history.Events[0].GetWorkflowExecutionStartedEventAttributes()
	if started == nil {
		return fmt.Errorf("base history does not start with WorkflowExecutionStarted")
	}

	dataConverter := converter.GetDefaultDataConverter()
	var params domain.ProblemGenParams
	if err := dataConverter.FromPayloads(started.GetInput(), &params); err != nil {
		return fmt.Errorf("decode base workflow input: %w", err)
	}
	if params.TestDataConfig.NumSamples != 1 {
		return fmt.Errorf("base workflow input num_samples=%d, want pinned value 1", params.TestDataConfig.NumSamples)
	}
	if params.GenerationEvidence != nil {
		return fmt.Errorf("base workflow input unexpectedly contains generation evidence contract")
	}
	params.TestDataConfig.NumSamples = 0
	input, err := dataConverter.ToPayloads(params)
	if err != nil {
		return fmt.Errorf("encode zero-sample workflow input: %w", err)
	}
	started.Input = input

	const (
		firstShiftedEventID = int64(9)
		insertedEventCount  = int64(2)
		changeID            = "problem-generation-structured-samples-v1"
	)
	for _, event := range history.Events {
		shiftEventIDFields(event.ProtoReflect(), firstShiftedEventID, insertedEventCount)
	}

	marker := proto.Clone(history.Events[6]).(*historypb.HistoryEvent)
	marker.EventId = firstShiftedEventID
	markerAttrs := marker.GetMarkerRecordedEventAttributes()
	if markerAttrs == nil || markerAttrs.GetMarkerName() != "Version" {
		return fmt.Errorf("base event 7 is not a Version marker")
	}
	changeIDPayloads, err := dataConverter.ToPayloads(changeID)
	if err != nil {
		return fmt.Errorf("encode structured-samples change ID: %w", err)
	}
	markerAttrs.Details["change-id"] = changeIDPayloads

	upsert := proto.Clone(history.Events[7]).(*historypb.HistoryEvent)
	upsert.EventId = firstShiftedEventID + 1
	upsertAttrs := upsert.GetUpsertWorkflowSearchAttributesEventAttributes()
	if upsertAttrs == nil || upsertAttrs.GetSearchAttributes() == nil {
		return fmt.Errorf("base event 8 is not a version search-attribute upsert")
	}
	versionPayload := upsertAttrs.GetSearchAttributes().GetIndexedFields()["TemporalChangeVersion"]
	if versionPayload == nil {
		return fmt.Errorf("base event 8 has no TemporalChangeVersion payload")
	}
	var versions []string
	if err := dataConverter.FromPayload(versionPayload, &versions); err != nil {
		return fmt.Errorf("decode TemporalChangeVersion payload: %w", err)
	}
	versions = append([]string{changeID + "-1"}, versions...)
	updatedVersionPayload, err := dataConverter.ToPayload(versions)
	if err != nil {
		return fmt.Errorf("encode TemporalChangeVersion payload: %w", err)
	}
	if typeMetadata := versionPayload.GetMetadata()["type"]; typeMetadata != nil {
		updatedVersionPayload.Metadata["type"] = append([]byte(nil), typeMetadata...)
	}
	upsertAttrs.SearchAttributes.IndexedFields["TemporalChangeVersion"] = updatedVersionPayload

	events := make([]*historypb.HistoryEvent, 0, len(history.Events)+int(insertedEventCount))
	events = append(events, history.Events[:8]...)
	events = append(events, marker, upsert)
	events = append(events, history.Events[8:]...)
	history.Events = events
	return nil
}

func assertHistoryGenerationEvidenceNil(t *testing.T, historyData []byte) {
	t.Helper()
	var history historypb.History
	if err := protojson.Unmarshal(historyData, &history); err != nil {
		t.Fatalf("decode replay history for generation evidence assertion: %v", err)
	}
	if len(history.Events) == 0 {
		t.Fatal("replay history has no events")
	}
	started := history.Events[0].GetWorkflowExecutionStartedEventAttributes()
	if started == nil {
		t.Fatal("replay history does not start with WorkflowExecutionStarted")
	}
	var params domain.ProblemGenParams
	if err := converter.GetDefaultDataConverter().FromPayloads(started.GetInput(), &params); err != nil {
		t.Fatalf("decode replay workflow input: %v", err)
	}
	if params.GenerationEvidence != nil {
		t.Fatalf("legacy replay input unexpectedly contains generation evidence contract: %+v", params.GenerationEvidence)
	}
}

func shiftEventIDFields(message protoreflect.Message, threshold, delta int64) {
	message.Range(func(field protoreflect.FieldDescriptor, value protoreflect.Value) bool {
		switch {
		case field.IsList():
			if field.Kind() == protoreflect.MessageKind {
				list := value.List()
				for i := 0; i < list.Len(); i++ {
					shiftEventIDFields(list.Get(i).Message(), threshold, delta)
				}
			}
		case field.IsMap():
			if field.MapValue().Kind() == protoreflect.MessageKind {
				value.Map().Range(func(_ protoreflect.MapKey, item protoreflect.Value) bool {
					shiftEventIDFields(item.Message(), threshold, delta)
					return true
				})
			}
		case field.Kind() == protoreflect.MessageKind:
			shiftEventIDFields(value.Message(), threshold, delta)
		case field.Kind() == protoreflect.Int64Kind &&
			(field.Name() == "event_id" || strings.HasSuffix(string(field.Name()), "_event_id")):
			if eventID := value.Int(); eventID >= threshold {
				message.Set(field, protoreflect.ValueOfInt64(eventID+delta))
			}
		case field.Kind() == protoreflect.StringKind &&
			(field.Name() == "activity_id" || field.Name() == "timer_id"):
			commandID, err := strconv.ParseInt(value.String(), 10, 64)
			if err == nil && commandID >= threshold {
				message.Set(field, protoreflect.ValueOfString(strconv.FormatInt(commandID+delta, 10)))
			}
		}
		return true
	})
}
