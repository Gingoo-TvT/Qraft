package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/generationapi"
	"github.com/Gingoo-TvT/Qraft/backend/internal/qualitymode"
	"github.com/Gingoo-TvT/Qraft/backend/internal/workflow/activities"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	workflowpb "go.temporal.io/api/workflow/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	temporalmocks "go.temporal.io/sdk/mocks"
	"go.temporal.io/sdk/temporal"
)

const generationJobTestRunID = "generation-job-run"

type fakeGenerationJobService struct {
	triggerCalls       int
	trigger            func(context.Context, string, string, string, time.Duration, domain.ProblemGenParams) error
	problem            *domain.Problem
	problemErr         error
	standardBinding    domain.GenerationStandardEvidenceBinding
	hasStandardBinding bool
	standardBindingErr error
}

func (fake *fakeGenerationJobService) TriggerGenerationJob(
	ctx context.Context,
	jobID string,
	payloadSHA256 string,
	principalSHA256 string,
	timeout time.Duration,
	params domain.ProblemGenParams,
) (client.WorkflowRun, error) {
	fake.triggerCalls++
	if fake.trigger != nil {
		return nil, fake.trigger(ctx, jobID, payloadSHA256, principalSHA256, timeout, params)
	}
	return nil, nil
}

func (fake *fakeGenerationJobService) GetProblemByWorkflowID(context.Context, string) (*domain.Problem, error) {
	return fake.problem, fake.problemErr
}

func (fake *fakeGenerationJobService) GetGenerationStandardEvidence(
	context.Context,
	uuid.UUID,
) (domain.GenerationStandardEvidenceBinding, bool, error) {
	return fake.standardBinding, fake.hasStandardBinding, fake.standardBindingErr
}

func TestGenerationJobCreateStartsStableSingleCandidateWorkflow(t *testing.T) {
	body := generationJobTestRequestBody(t)
	jobID, payloadSHA256, principalSHA256 := generationJobTestIdentity(t, body, "create-key")
	temporalClient := temporalmocks.NewClient(t)
	temporalClient.On("DescribeWorkflowExecution", mock.Anything, jobID, "").
		Return(nil, serviceerror.NewNotFound("absent")).Once()

	fake := &fakeGenerationJobService{}
	fake.trigger = func(
		_ context.Context,
		gotJobID string,
		gotPayload string,
		gotPrincipal string,
		gotTimeout time.Duration,
		params domain.ProblemGenParams,
	) error {
		require.Equal(t, jobID, gotJobID)
		require.Equal(t, payloadSHA256, gotPayload)
		require.Equal(t, principalSHA256, gotPrincipal)
		require.Equal(t, time.Hour, gotTimeout)
		require.Equal(t, domain.LevelAlgorithm, params.Level)
		require.Equal(t, []string{"cpp"}, params.Languages)
		return nil
	}
	handler := NewGenerationJobHandler(fake, temporalClient, &ProblemHandler{})
	recorder := invokeGenerationJobHandler(t, http.MethodPost, "/api/v1/generation/jobs", "", body, "create-key", handler.HandleCreate)
	require.Equal(t, http.StatusCreated, recorder.Code)
	require.Equal(t, 1, fake.triggerCalls)
	require.Contains(t, recorder.Body.String(), `"job_id":"`+jobID+`"`)
	require.Contains(t, recorder.Body.String(), `"idempotent_replay":false`)
	requireGenerationJobResponseDoesNotLeakTemporal(t, recorder.Body.String())
}

func TestGenerationJobCreateIsIdempotentAndRejectsPayloadCollision(t *testing.T) {
	body := generationJobTestRequestBody(t)
	jobID, payloadSHA256, principalSHA256 := generationJobTestIdentity(t, body, "replay-key")

	t.Run("same payload", func(t *testing.T) {
		temporalClient := temporalmocks.NewClient(t)
		temporalClient.On("DescribeWorkflowExecution", mock.Anything, jobID, "").
			Return(generationJobTestDescription(t, jobID, enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING, payloadSHA256, principalSHA256), nil).Once()
		fake := &fakeGenerationJobService{}
		handler := NewGenerationJobHandler(fake, temporalClient, nil)
		recorder := invokeGenerationJobHandler(t, http.MethodPost, "/api/v1/generation/jobs", "", body, "replay-key", handler.HandleCreate)
		require.Equal(t, http.StatusOK, recorder.Code)
		require.Zero(t, fake.triggerCalls)
		require.Contains(t, recorder.Body.String(), `"idempotent_replay":true`)
	})

	t.Run("different payload", func(t *testing.T) {
		temporalClient := temporalmocks.NewClient(t)
		temporalClient.On("DescribeWorkflowExecution", mock.Anything, jobID, "").
			Return(generationJobTestDescription(t, jobID, enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING, strings.Repeat("0", 64), principalSHA256), nil).Once()
		fake := &fakeGenerationJobService{}
		handler := NewGenerationJobHandler(fake, temporalClient, nil)
		recorder := invokeGenerationJobHandler(t, http.MethodPost, "/api/v1/generation/jobs", "", body, "replay-key", handler.HandleCreate)
		require.Equal(t, http.StatusConflict, recorder.Code)
		require.Zero(t, fake.triggerCalls)
		require.Contains(t, recorder.Body.String(), `"code":"idempotency_conflict"`)
	})
}

func TestGenerationJobCreateStrictJSONFailsBeforeTemporal(t *testing.T) {
	body := strings.TrimSpace(generationJobTestRequestBody(t))
	unknown := strings.TrimSuffix(body, "}") + `,"unknown":true}`
	for _, test := range []struct {
		name string
		body string
	}{
		{name: "unknown field", body: unknown},
		{name: "trailing document", body: body + `{}`},
		{name: "malformed", body: `{`},
	} {
		t.Run(test.name, func(t *testing.T) {
			temporalClient := temporalmocks.NewClient(t)
			handler := NewGenerationJobHandler(&fakeGenerationJobService{}, temporalClient, &ProblemHandler{})
			recorder := invokeGenerationJobHandler(t, http.MethodPost, "/api/v1/generation/jobs", "", test.body, "strict-key", handler.HandleCreate)
			require.Equal(t, http.StatusBadRequest, recorder.Code)
			require.Contains(t, recorder.Body.String(), `"code":"unsatisfiable_spec"`)
		})
	}
}

func TestGenerationJobCreateRoutesAllEvidenceLevelsThroughQualityMode(t *testing.T) {
	for _, level := range []string{generationapi.EvidenceMinimal, generationapi.EvidenceStandard, generationapi.EvidenceAudit} {
		t.Run(level, func(t *testing.T) {
			var request generationapi.Request
			require.NoError(t, json.Unmarshal([]byte(generationJobTestRequestBody(t)), &request))
			request.Output.EvidenceLevel = level
			bodyBytes, err := json.Marshal(request)
			require.NoError(t, err)
			body := string(bodyBytes)
			jobID, _, _ := generationJobTestIdentity(t, body, "quality-mode-"+level)
			temporalClient := temporalmocks.NewClient(t)
			temporalClient.On("DescribeWorkflowExecution", mock.Anything, jobID, "").
				Return(nil, serviceerror.NewNotFound("absent")).Once()
			fake := &fakeGenerationJobService{}
			fake.trigger = func(_ context.Context, _ string, _ string, _ string, _ time.Duration, params domain.ProblemGenParams) error {
				got, evidenceErr := generationapi.QualityEvidenceLevelFromParams(params)
				require.NoError(t, evidenceErr)
				require.Equal(t, level, got)
				return nil
			}
			handler := NewGenerationJobHandler(fake, temporalClient, &ProblemHandler{})
			recorder := invokeGenerationJobHandler(t, http.MethodPost, "/api/v1/generation/jobs", "", body, "quality-mode-"+level, handler.HandleCreate)
			require.Equal(t, http.StatusCreated, recorder.Code)
			require.Equal(t, 1, fake.triggerCalls)
			requireGenerationJobResponseDoesNotLeakTemporal(t, recorder.Body.String())
		})
	}
}

func TestGenerationJobLegacyQualityModeRejectsExtendedEvidenceBeforeTemporal(t *testing.T) {
	mode, err := qualitymode.ResolveMode(qualitymode.ModeLegacyOnly, true, nil)
	require.NoError(t, err)
	t.Run("minimal remains available", func(t *testing.T) {
		body := generationJobTestRequestBody(t)
		jobID, _, _ := generationJobTestIdentity(t, body, "legacy-minimal")
		temporalClient := temporalmocks.NewClient(t)
		temporalClient.On("DescribeWorkflowExecution", mock.Anything, jobID, "").
			Return(nil, serviceerror.NewNotFound("absent")).Once()
		fake := &fakeGenerationJobService{}
		handler, handlerErr := NewGenerationJobHandlerWithQualityMode(fake, temporalClient, &ProblemHandler{}, mode)
		require.NoError(t, handlerErr)
		recorder := invokeGenerationJobHandler(t, http.MethodPost, "/api/v1/generation/jobs", "", body, "legacy-minimal", handler.HandleCreate)
		require.Equal(t, http.StatusCreated, recorder.Code)
		require.Equal(t, 1, fake.triggerCalls)
	})
	for _, level := range []string{generationapi.EvidenceStandard, generationapi.EvidenceAudit} {
		t.Run(level, func(t *testing.T) {
			var request generationapi.Request
			require.NoError(t, json.Unmarshal([]byte(generationJobTestRequestBody(t)), &request))
			request.Output.EvidenceLevel = level
			body, marshalErr := json.Marshal(request)
			require.NoError(t, marshalErr)
			temporalClient := temporalmocks.NewClient(t)
			fake := &fakeGenerationJobService{}
			handler, handlerErr := NewGenerationJobHandlerWithQualityMode(fake, temporalClient, nil, mode)
			require.NoError(t, handlerErr)
			recorder := invokeGenerationJobHandler(t, http.MethodPost, "/api/v1/generation/jobs", "", string(body), "legacy-"+level, handler.HandleCreate)
			require.Equal(t, http.StatusUnprocessableEntity, recorder.Code)
			require.Zero(t, fake.triggerCalls)
			require.Contains(t, recorder.Body.String(), `"code":"unsupported_constraint"`)
			require.Contains(t, recorder.Body.String(), "output.evidence_level="+level)
			requireGenerationJobResponseDoesNotLeakTemporal(t, recorder.Body.String())
		})
	}
}

func TestGenerationJobHandlerRejectsForgedQualityModeAudit(t *testing.T) {
	if _, err := NewGenerationJobHandlerWithQualityMode(nil, nil, nil, qualitymode.Audit{}); err == nil {
		t.Fatal("zero-value quality mode audit was accepted")
	}
}

func TestGenerationJobStatusMapsInternalStepToStablePhase(t *testing.T) {
	body := generationJobTestRequestBody(t)
	jobID, payloadSHA256, principalSHA256 := generationJobTestIdentity(t, body, "status-key")
	temporalClient := temporalmocks.NewClient(t)
	temporalClient.On("DescribeWorkflowExecution", mock.Anything, jobID, "").
		Return(generationJobTestDescription(t, jobID, enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING, payloadSHA256, principalSHA256), nil).Once()
	query := domain.WorkflowStateQuery{State: domain.WorkflowState{
		Status:      domain.WorkflowStatusRunning,
		CurrentStep: domain.StepRunSandbox,
		Progress:    140,
	}}
	temporalClient.On("QueryWorkflow", mock.Anything, jobID, generationJobTestRunID, domain.WorkflowStateQueryName).
		Return(generationJobTestEncodedQuery(t, query), nil).Once()

	handler := NewGenerationJobHandler(&fakeGenerationJobService{}, temporalClient, nil)
	recorder := invokeGenerationJobHandler(t, http.MethodGet, "/api/v1/generation/jobs/"+jobID, jobID, "", "", handler.HandleStatus)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"status":"running"`)
	require.Contains(t, recorder.Body.String(), `"phase":"validating"`)
	require.Contains(t, recorder.Body.String(), `"progress":99`)
	requireGenerationJobResponseDoesNotLeakTemporal(t, recorder.Body.String())
}

func TestGenerationJobResultAndEventsProjectQuarantinedProblem(t *testing.T) {
	body := generationJobTestRequestBody(t)
	jobID, payloadSHA256, principalSHA256 := generationJobTestIdentity(t, body, "result-key")
	problemID := uuid.New()
	manifestSHA256 := strings.Repeat("a", 64)
	metadata, err := json.Marshal(map[string]interface{}{
		"activity_payload_version": storedTestManifestPayloadVersion,
		"review_quarantine": map[string]interface{}{
			"reason":                "automated review denied publication",
			"workflow_run_id":       generationJobTestRunID,
			"review_gate_change_id": "problem-generation-review-gate-v2",
			"review_gate_version":   2,
			"review_result_sha256":  strings.Repeat("b", 64),
		},
		"test_manifest": map[string]interface{}{
			"schema_version": storedTestManifestSchemaVersion,
			"sha256":         manifestSHA256,
			"path":           "problems/" + problemID.String() + "/test_manifest.v1.json",
		},
	})
	require.NoError(t, err)
	problem := &domain.Problem{
		ID:           problemID,
		Status:       domain.ProblemStatusQuarantined,
		MetadataJSON: metadata,
	}

	t.Run("result", func(t *testing.T) {
		temporalClient := temporalmocks.NewClient(t)
		temporalClient.On("DescribeWorkflowExecution", mock.Anything, jobID, "").
			Return(generationJobTestDescription(t, jobID, enumspb.WORKFLOW_EXECUTION_STATUS_COMPLETED, payloadSHA256, principalSHA256), nil).Once()
		handler := NewGenerationJobHandler(&fakeGenerationJobService{problem: problem}, temporalClient, nil)
		recorder := invokeGenerationJobHandler(t, http.MethodGet, "/api/v1/generation/jobs/"+jobID+"/result", jobID, "", "", handler.HandleResult)
		require.Equal(t, http.StatusOK, recorder.Code)
		require.Contains(t, recorder.Body.String(), `"status":"quarantined"`)
		require.Contains(t, recorder.Body.String(), `"outcome_category":"review"`)
		require.Contains(t, recorder.Body.String(), problemID.String())
		require.Contains(t, recorder.Body.String(), "automated review denied publication")
		require.Contains(t, recorder.Body.String(), `"kind":"test_manifest"`)
		require.Contains(t, recorder.Body.String(), `"kind":"review_result"`)
		require.Contains(t, recorder.Body.String(), `"sha256":"`+manifestSHA256+`"`)
		require.Contains(t, recorder.Body.String(), `"uri":"/api/v1/problems/`+problemID.String()+`/test-manifest"`)
		require.Contains(t, recorder.Body.String(), `"profile":"`+generationapi.EvidenceProfileV0+`"`)
		require.NotContains(t, recorder.Body.String(), `"hydro_url"`)
		requireGenerationJobResponseDoesNotLeakTemporal(t, recorder.Body.String())
	})

	t.Run("terminal event", func(t *testing.T) {
		temporalClient := temporalmocks.NewClient(t)
		temporalClient.On("DescribeWorkflowExecution", mock.Anything, jobID, "").
			Return(generationJobTestDescription(t, jobID, enumspb.WORKFLOW_EXECUTION_STATUS_COMPLETED, payloadSHA256, principalSHA256), nil).Once()
		handler := NewGenerationJobHandler(&fakeGenerationJobService{problem: problem}, temporalClient, nil)
		handler.now = func() time.Time { return time.Date(2026, 8, 21, 1, 2, 3, 0, time.UTC) }
		recorder := invokeGenerationJobHandler(t, http.MethodGet, "/api/v1/generation/jobs/"+jobID+"/events", jobID, "", "", handler.HandleEvents)
		require.Equal(t, http.StatusOK, recorder.Code)
		require.Contains(t, recorder.Body.String(), "event: job_terminal")
		require.Contains(t, recorder.Body.String(), `"status":"quarantined"`)
		requireGenerationJobResponseDoesNotLeakTemporal(t, recorder.Body.String())
	})
}

func TestGenerationJobSucceededResultOmitsQuarantineFields(t *testing.T) {
	body := generationJobTestRequestBody(t)
	jobID, payloadSHA256, principalSHA256 := generationJobTestIdentity(t, body, "succeeded-result-key")
	problem := &domain.Problem{
		ID:           uuid.New(),
		Status:       domain.ProblemStatusPublished,
		MetadataJSON: json.RawMessage(`{}`),
	}
	temporalClient := temporalmocks.NewClient(t)
	temporalClient.On("DescribeWorkflowExecution", mock.Anything, jobID, "").
		Return(generationJobTestDescription(t, jobID, enumspb.WORKFLOW_EXECUTION_STATUS_COMPLETED, payloadSHA256, principalSHA256), nil).Once()
	handler := NewGenerationJobHandler(&fakeGenerationJobService{problem: problem}, temporalClient, nil)
	recorder := invokeGenerationJobHandler(t, http.MethodGet, "/api/v1/generation/jobs/"+jobID+"/result", jobID, "", "", handler.HandleResult)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"status":"succeeded"`)
	require.NotContains(t, recorder.Body.String(), `"quarantine_reason"`)
	require.NotContains(t, recorder.Body.String(), `"outcome_category"`)
	require.NotContains(t, recorder.Body.String(), `"kind":"standard_evidence"`)
}

func TestGenerationJobQualityDraftProjectsSucceededWithV2Evidence(t *testing.T) {
	body := generationJobTestRequestBody(t)
	jobID, payloadSHA256, principalSHA256 := generationJobTestIdentity(t, body, "quality-draft-result-key")
	problemID := uuid.New()
	refs := []activities.ArtifactRef{
		generationJobQualityArtifactRef(t, jobID, strings.Repeat("a", 64), "GenerateAuthoringPlanActivity", "application/json"),
		generationJobQualityArtifactRef(t, jobID, strings.Repeat("b", 64), "RenderStatementFromAuthoringBundleActivityV1", "application/json"),
		generationJobQualityArtifactRef(t, jobID, strings.Repeat("c", 64), "FinalizeAuthoringStatementSamplesActivityV1", "application/json"),
		generationJobQualityArtifactRef(t, jobID, strings.Repeat("d", 64), "GenerateMainSolutionActivityV1", "text/x-c++src"),
		generationJobQualityArtifactRef(t, jobID, strings.Repeat("e", 64), "GenerateOracleCandidateActivityV1", "text/x-c++src"),
		generationJobQualityArtifactRef(t, jobID, strings.Repeat("f", 64), activities.VerifiedProgramReceiptProducerV1, "application/json"),
		generationJobQualityArtifactRef(t, jobID, strings.Repeat("1", 64), "BuildS3TestManifestActivityV1", "application/json"),
		generationJobQualityArtifactRef(t, jobID, strings.Repeat("2", 64), "RecomputeS3VerdictActivityV1", "application/json"),
	}
	refs[6].LLMCallReceipt = &activities.LLMCallReceipt{
		SchemaVersion:  1,
		RequestedModel: "fixture-model",
		ReturnedModel:  "fixture-model-r1",
		Provider:       "fixture-provider",
		EndpointID:     strings.Repeat("3", 64),
		PromptHash:     strings.Repeat("4", 64),
		RequestSHA256:  strings.Repeat("5", 64),
	}
	evidence := activities.S3QualityPassDraftEvidenceV1{
		SchemaVersion: generationapi.S3QualityMaterializationSchemaV1,
		Decision:      "pass", SubjectRevision: refs[2].SHA256, EvidenceLevel: generationapi.EvidenceMinimal,
		AuthoringBundleArtifact: refs[0], StatementDraftArtifact: refs[1], FinalStatementArtifact: refs[2],
		MainProgramArtifact: refs[3], OracleProgramArtifact: refs[4], OracleReceiptArtifact: refs[5],
		TestManifestArtifact: refs[6], AuditArtifact: refs[7],
	}
	metadata, err := json.Marshal(map[string]interface{}{
		"activity_payload_version": activities.StoreProblemS3QualityDraftPayloadVersion,
		"test_manifest": map[string]interface{}{
			"schema_version": activities.TestManifestSchemaVersionV2,
			"sha256":         refs[6].SHA256,
			"path":           "problems/" + problemID.String() + "/test_manifest.v2.json",
			"artifact":       refs[6],
		},
		generationapi.S3QualityMaterializationMetadataKey: evidence,
	})
	require.NoError(t, err)
	problem := &domain.Problem{ID: problemID, WorkflowID: &jobID, Status: domain.ProblemStatusDraft, MetadataJSON: metadata}
	temporalClient := temporalmocks.NewClient(t)
	temporalClient.On("DescribeWorkflowExecution", mock.Anything, jobID, "").
		Return(generationJobQualityTestDescription(t, jobID, enumspb.WORKFLOW_EXECUTION_STATUS_COMPLETED, payloadSHA256, principalSHA256, generationapi.EvidenceMinimal), nil).Once()
	handler := NewGenerationJobHandler(&fakeGenerationJobService{problem: problem}, temporalClient, nil)
	recorder := invokeGenerationJobHandler(t, http.MethodGet, "/api/v1/generation/jobs/"+jobID+"/result", jobID, "", "", handler.HandleResult)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"status":"succeeded"`)
	require.Contains(t, recorder.Body.String(), `"kind":"quality_audit"`)
	require.Contains(t, recorder.Body.String(), `"sha256":"`+refs[7].SHA256+`"`)
	require.Contains(t, recorder.Body.String(), `"uri":"/api/v1/problems/`+problemID.String()+`/quality/audit"`)
	require.Contains(t, recorder.Body.String(), `"evidence_level":"minimal"`)
	require.Contains(t, recorder.Body.String(), `"quality_report_uri":"/api/v1/problems/`+problemID.String()+`/quality"`)
	require.Contains(t, recorder.Body.String(), `"kind":"test_manifest"`)
	require.NotContains(t, recorder.Body.String(), `"kind":"standard_evidence"`)
	require.NotContains(t, recorder.Body.String(), `"quarantine_reason"`)

	tamperedEvidence := evidence
	tamperedReceipt := *evidence.TestManifestArtifact.LLMCallReceipt
	tamperedReceipt.ReturnedModel = "tampered-model"
	tamperedEvidence.TestManifestArtifact.LLMCallReceipt = &tamperedReceipt
	tamperedMetadata, err := json.Marshal(map[string]interface{}{
		"activity_payload_version": activities.StoreProblemS3QualityDraftPayloadVersion,
		"test_manifest": map[string]interface{}{
			"schema_version": activities.TestManifestSchemaVersionV2,
			"sha256":         refs[6].SHA256,
			"path":           "problems/" + problemID.String() + "/test_manifest.v2.json",
			"artifact":       refs[6],
		},
		generationapi.S3QualityMaterializationMetadataKey: tamperedEvidence,
	})
	require.NoError(t, err)
	tamperedProblem := *problem
	tamperedProblem.MetadataJSON = tamperedMetadata
	_, err = generationJobQualityEvidenceReference(&tamperedProblem, generationapi.EvidenceMinimal)
	require.ErrorContains(t, err, "TestManifest v2 binding is inconsistent")

	legacyMode, modeErr := qualitymode.ResolveMode(qualitymode.ModeLegacyOnly, true, nil)
	require.NoError(t, modeErr)
	legacyTemporal := temporalmocks.NewClient(t)
	legacyTemporal.On("DescribeWorkflowExecution", mock.Anything, jobID, "").
		Return(generationJobQualityTestDescription(t, jobID, enumspb.WORKFLOW_EXECUTION_STATUS_COMPLETED, payloadSHA256, principalSHA256, generationapi.EvidenceMinimal), nil).Once()
	legacyHandler, handlerErr := NewGenerationJobHandlerWithQualityMode(&fakeGenerationJobService{problem: problem}, legacyTemporal, nil, legacyMode)
	require.NoError(t, handlerErr)
	legacyRecorder := invokeGenerationJobHandler(t, http.MethodGet, "/api/v1/generation/jobs/"+jobID+"/result", jobID, "", "", legacyHandler.HandleResult)
	require.Equal(t, http.StatusOK, legacyRecorder.Code)
	require.Contains(t, legacyRecorder.Body.String(), `"kind":"quality_audit","sha256":"`+refs[7].SHA256+`"}`)
	require.NotContains(t, legacyRecorder.Body.String(), `"quality_report_uri"`)
	require.NotContains(t, legacyRecorder.Body.String(), "/quality/audit")

	for _, level := range []string{generationapi.EvidenceStandard, generationapi.EvidenceAudit} {
		t.Run(level+" quality report", func(t *testing.T) {
			extendedEvidence := evidence
			extendedEvidence.EvidenceLevel = level
			extendedMetadata, marshalErr := json.Marshal(map[string]interface{}{
				"activity_payload_version":                    activities.StoreProblemS3QualityDraftPayloadVersion,
				generationapi.QualityEvidenceLevelMetadataKey: level,
				"test_manifest": map[string]interface{}{
					"schema_version": activities.TestManifestSchemaVersionV2,
					"sha256":         refs[6].SHA256,
					"path":           "problems/" + problemID.String() + "/test_manifest.v2.json",
					"artifact":       refs[6],
				},
				generationapi.S3QualityMaterializationMetadataKey: extendedEvidence,
			})
			require.NoError(t, marshalErr)
			extendedProblem := &domain.Problem{
				ID: problemID, WorkflowID: &jobID, Status: domain.ProblemStatusDraft, MetadataJSON: extendedMetadata,
			}
			extendedTemporal := temporalmocks.NewClient(t)
			extendedTemporal.On("DescribeWorkflowExecution", mock.Anything, jobID, "").
				Return(generationJobQualityTestDescription(t, jobID, enumspb.WORKFLOW_EXECUTION_STATUS_COMPLETED, payloadSHA256, principalSHA256, level), nil).Once()
			extendedHandler := NewGenerationJobHandler(&fakeGenerationJobService{problem: extendedProblem}, extendedTemporal, nil)
			extendedRecorder := invokeGenerationJobHandler(t, http.MethodGet, "/api/v1/generation/jobs/"+jobID+"/result", jobID, "", "", extendedHandler.HandleResult)
			require.Equal(t, http.StatusOK, extendedRecorder.Code)
			require.Contains(t, extendedRecorder.Body.String(), `"evidence_level":"`+level+`"`)
			require.Contains(t, extendedRecorder.Body.String(), `"quality_report_uri":"/api/v1/problems/`+problemID.String()+`/quality"`)
			require.Contains(t, extendedRecorder.Body.String(), `"uri":"/api/v1/problems/`+problemID.String()+`/quality/audit"`)
			require.NotContains(t, extendedRecorder.Body.String(), "workflow-artifacts/")
			require.NotContains(t, extendedRecorder.Body.String(), "cas://")

			var driftedMetadata map[string]interface{}
			require.NoError(t, json.Unmarshal(extendedMetadata, &driftedMetadata))
			driftedMetadata[generationapi.QualityEvidenceLevelMetadataKey] = generationapi.EvidenceMinimal
			driftedBytes, driftErr := json.Marshal(driftedMetadata)
			require.NoError(t, driftErr)
			driftedProblem := *extendedProblem
			driftedProblem.MetadataJSON = driftedBytes
			driftedTemporal := temporalmocks.NewClient(t)
			driftedTemporal.On("DescribeWorkflowExecution", mock.Anything, jobID, "").
				Return(generationJobQualityTestDescription(t, jobID, enumspb.WORKFLOW_EXECUTION_STATUS_COMPLETED, payloadSHA256, principalSHA256, level), nil).Once()
			driftedHandler := NewGenerationJobHandler(&fakeGenerationJobService{problem: &driftedProblem}, driftedTemporal, nil)
			driftedRecorder := invokeGenerationJobHandler(t, http.MethodGet, "/api/v1/generation/jobs/"+jobID+"/result", jobID, "", "", driftedHandler.HandleResult)
			require.Equal(t, http.StatusServiceUnavailable, driftedRecorder.Code)
		})
	}
}

func TestGenerationJobStandardEvidenceResultAddsDownloadableReceipt(t *testing.T) {
	var request generationapi.Request
	require.NoError(t, json.Unmarshal([]byte(generationJobTestRequestBody(t)), &request))
	request.Output.EvidenceLevel = generationapi.EvidenceStandard
	encodedRequest, err := json.Marshal(request)
	require.NoError(t, err)
	body := string(encodedRequest)
	jobID, payloadSHA256, principalSHA256 := generationJobTestIdentity(t, body, "standard-result-key")
	problemID := uuid.New()
	manifestSHA := strings.Repeat("a", 64)
	receiptSHA := strings.Repeat("c", 64)
	metadata, err := json.Marshal(map[string]interface{}{
		"activity_payload_version": activities.StoreProblemStandardEvidencePayloadVersion,
		"test_manifest": map[string]interface{}{
			"schema_version": storedTestManifestSchemaVersion,
			"sha256":         manifestSHA,
			"path":           "problems/" + problemID.String() + "/test_manifest.v1.json",
		},
		domain.GenerationStandardEvidenceRequestMetaKey: request.ToProblemGenParams().GenerationEvidence,
	})
	receiptBinding := domain.GenerationStandardEvidenceBinding{
		Reference: domain.GenerationStandardEvidenceReference{
			SchemaVersion: domain.GenerationStandardEvidenceSchemaV1,
			SHA256:        receiptSHA,
			Path:          "problems/" + problemID.String() + "/generation_standard_evidence.v1.json",
		},
		FinalStatus:     domain.ProblemStatusPublished,
		OutcomeCategory: string(generationapi.OutcomeCategoryPublicationEligibility),
		OutcomeKind:     "publication_decision",
		OutcomeSHA256:   strings.Repeat("d", 64),
	}
	require.NoError(t, err)
	problem := &domain.Problem{ID: problemID, Status: domain.ProblemStatusPublished, MetadataJSON: metadata}
	temporalClient := temporalmocks.NewClient(t)
	temporalClient.On("DescribeWorkflowExecution", mock.Anything, jobID, "").
		Return(generationJobStandardTestDescription(t, jobID, enumspb.WORKFLOW_EXECUTION_STATUS_COMPLETED, payloadSHA256, principalSHA256), nil).Once()
	handler := NewGenerationJobHandler(&fakeGenerationJobService{
		problem: problem, standardBinding: receiptBinding, hasStandardBinding: true,
	}, temporalClient, nil)
	recorder := invokeGenerationJobHandler(t, http.MethodGet, "/api/v1/generation/jobs/"+jobID+"/result", jobID, "", "", handler.HandleResult)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"kind":"standard_evidence"`)
	require.Contains(t, recorder.Body.String(), `"sha256":"`+receiptSHA+`"`)
	require.Contains(t, recorder.Body.String(), `"uri":"/api/v1/problems/`+problemID.String()+`/standard-evidence"`)
	require.Contains(t, recorder.Body.String(), `"kind":"test_manifest"`)
	require.Contains(t, recorder.Body.String(), `"evidence_level":"standard"`)
	require.NotContains(t, recorder.Body.String(), `"quality_report_uri"`)
}

func TestGenerationJobStandardEvidenceFailsClosedWithoutBinding(t *testing.T) {
	var request generationapi.Request
	require.NoError(t, json.Unmarshal([]byte(generationJobTestRequestBody(t)), &request))
	request.Output.EvidenceLevel = generationapi.EvidenceStandard
	bodyBytes, err := json.Marshal(request)
	require.NoError(t, err)
	jobID, payloadSHA256, principalSHA256 := generationJobTestIdentity(t, string(bodyBytes), "standard-missing-binding-key")
	metadata, err := json.Marshal(map[string]interface{}{
		domain.GenerationStandardEvidenceRequestMetaKey: request.ToProblemGenParams().GenerationEvidence,
	})
	require.NoError(t, err)
	problem := &domain.Problem{ID: uuid.New(), Status: domain.ProblemStatusPublished, MetadataJSON: metadata}
	temporalClient := temporalmocks.NewClient(t)
	temporalClient.On("DescribeWorkflowExecution", mock.Anything, jobID, "").
		Return(generationJobStandardTestDescription(t, jobID, enumspb.WORKFLOW_EXECUTION_STATUS_COMPLETED, payloadSHA256, principalSHA256), nil).Once()
	handler := NewGenerationJobHandler(&fakeGenerationJobService{problem: problem}, temporalClient, nil)
	recorder := invokeGenerationJobHandler(t, http.MethodGet, "/api/v1/generation/jobs/"+jobID+"/result", jobID, "", "", handler.HandleResult)
	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
}

func TestGenerationJobStandardEvidenceKeepsImmutableOutcomeAfterProblemApproval(t *testing.T) {
	var request generationapi.Request
	require.NoError(t, json.Unmarshal([]byte(generationJobTestRequestBody(t)), &request))
	request.Output.EvidenceLevel = generationapi.EvidenceStandard
	bodyBytes, err := json.Marshal(request)
	require.NoError(t, err)
	jobID, payloadSHA256, principalSHA256 := generationJobTestIdentity(t, string(bodyBytes), "standard-approved-later-key")
	problemID := uuid.New()
	manifestSHA := strings.Repeat("a", 64)
	metadata, err := json.Marshal(map[string]interface{}{
		"activity_payload_version": activities.StoreProblemStandardEvidencePayloadVersion,
		"test_manifest": map[string]interface{}{
			"schema_version": storedTestManifestSchemaVersion,
			"sha256":         manifestSHA,
			"path":           "problems/" + problemID.String() + "/test_manifest.v1.json",
		},
		domain.GenerationStandardEvidenceRequestMetaKey: request.ToProblemGenParams().GenerationEvidence,
	})
	require.NoError(t, err)
	// The problem was approved after generation, but the completed job receipt
	// remains an immutable record of its original quarantined outcome.
	problem := &domain.Problem{ID: problemID, Status: domain.ProblemStatusPublished, MetadataJSON: metadata}
	binding := domain.GenerationStandardEvidenceBinding{
		Reference: domain.GenerationStandardEvidenceReference{
			SchemaVersion: domain.GenerationStandardEvidenceSchemaV1,
			SHA256:        strings.Repeat("c", 64),
			Path:          "problems/" + problemID.String() + "/generation_standard_evidence.v1.json",
		},
		FinalStatus:      domain.ProblemStatusQuarantined,
		QuarantineReason: "public release denied at generation completion",
		OutcomeCategory:  string(generationapi.OutcomeCategoryPublicationEligibility),
		OutcomeKind:      "publication_decision",
		OutcomeSHA256:    strings.Repeat("d", 64),
	}
	temporalClient := temporalmocks.NewClient(t)
	temporalClient.On("DescribeWorkflowExecution", mock.Anything, jobID, "").
		Return(generationJobStandardTestDescription(t, jobID, enumspb.WORKFLOW_EXECUTION_STATUS_COMPLETED, payloadSHA256, principalSHA256), nil).Once()
	handler := NewGenerationJobHandler(&fakeGenerationJobService{
		problem: problem, standardBinding: binding, hasStandardBinding: true,
	}, temporalClient, nil)
	recorder := invokeGenerationJobHandler(t, http.MethodGet, "/api/v1/generation/jobs/"+jobID+"/result", jobID, "", "", handler.HandleResult)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"status":"quarantined"`)
	require.Contains(t, recorder.Body.String(), `"outcome_category":"publication_eligibility"`)
	require.Contains(t, recorder.Body.String(), `"sha256":"`+binding.OutcomeSHA256+`"`)
	require.Contains(t, recorder.Body.String(), binding.QuarantineReason)
}

func TestGenerationJobCancelUsesExactRunAndReturnsRequestedState(t *testing.T) {
	body := generationJobTestRequestBody(t)
	jobID, payloadSHA256, principalSHA256 := generationJobTestIdentity(t, body, "cancel-key")
	temporalClient := temporalmocks.NewClient(t)
	temporalClient.On("DescribeWorkflowExecution", mock.Anything, jobID, "").
		Return(generationJobTestDescription(t, jobID, enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING, payloadSHA256, principalSHA256), nil).Once()
	temporalClient.On("CancelWorkflow", mock.Anything, jobID, generationJobTestRunID).Return(nil).Once()

	handler := NewGenerationJobHandler(&fakeGenerationJobService{}, temporalClient, nil)
	recorder := invokeGenerationJobHandler(t, http.MethodDelete, "/api/v1/generation/jobs/"+jobID, jobID, "", "", handler.HandleCancel)
	require.Equal(t, http.StatusAccepted, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"status":"cancellation_requested"`)
	requireGenerationJobResponseDoesNotLeakTemporal(t, recorder.Body.String())
}

func TestGenerationJobRoutesAreAdditiveAndStable(t *testing.T) {
	e := echo.New()
	v1 := e.Group("/api/v1")
	v1.POST("/problems/generate", func(c echo.Context) error { return c.NoContent(http.StatusNoContent) })
	RegisterGenerationJobRoutes(v1, &GenerationJobHandler{})
	want := map[string]bool{
		http.MethodPost + " /api/v1/problems/generate":         false,
		http.MethodPost + " /api/v1/generation/jobs":           false,
		http.MethodGet + " /api/v1/generation/jobs/:id":        false,
		http.MethodGet + " /api/v1/generation/jobs/:id/result": false,
		http.MethodGet + " /api/v1/generation/jobs/:id/events": false,
		http.MethodDelete + " /api/v1/generation/jobs/:id":     false,
	}
	for _, route := range e.Routes() {
		key := route.Method + " " + route.Path
		if _, ok := want[key]; ok {
			want[key] = true
		}
	}
	for route, found := range want {
		if !found {
			t.Fatalf("route missing: %s", route)
		}
	}
}

func TestGenerationJobFailureClassifierHidesProviderErrorText(t *testing.T) {
	err := temporal.NewNonRetryableApplicationError("private provider failure text", "DuplicateProblem", nil)
	classified := classifyGenerationJobFailure(err)
	require.Equal(t, generationapi.ErrorQualityNotMet, classified.Code)
	require.Equal(t, generationapi.OutcomeCategoryContent, classified.OutcomeCategory)
	require.NotContains(t, classified.Message, "private provider")
}

func TestGenerationJobFailureClassifierSeparatesStageZeroOutcomeDenominators(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want generationapi.OutcomeCategory
	}{
		{
			name: "content gate",
			err:  temporal.NewNonRetryableApplicationError("fixture", "ValidationFailed", nil),
			want: generationapi.OutcomeCategoryContent,
		},
		{
			name: "invalid generated content",
			err:  temporal.NewNonRetryableApplicationError("fixture", "InvalidParameterError", nil),
			want: generationapi.OutcomeCategoryContent,
		},
		{
			name: "review gate",
			err:  temporal.NewNonRetryableApplicationError("fixture", "HumanReviewRejection", nil),
			want: generationapi.OutcomeCategoryReview,
		},
		{
			name: "technical pipeline",
			err:  errors.New("fixture provider unavailable"),
			want: generationapi.OutcomeCategoryTechnical,
		},
		{
			name: "truncated provider response",
			err:  temporal.NewNonRetryableApplicationError("fixture", "TruncatedLLMResponse", nil),
			want: generationapi.OutcomeCategoryTechnical,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			classified := classifyGenerationJobFailure(test.err)
			require.Equal(t, test.want, classified.OutcomeCategory)
		})
	}
}

func TestGenerationJobQuarantineOutcomeSeparatesReviewAndPublicationEligibility(t *testing.T) {
	for _, test := range []struct {
		name     string
		metadata string
		want     generationapi.OutcomeCategory
	}{
		{
			name:     "review",
			metadata: `{"review_quarantine":{"reason":"review denied","workflow_run_id":"run-v0","review_gate_change_id":"problem-generation-review-gate-v2","review_gate_version":2,"review_result_sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}`,
			want:     generationapi.OutcomeCategoryReview,
		},
		{
			name:     "publication eligibility",
			metadata: `{"publication_gate_status":"quarantined","publication_gate_version":"2026-08-16.t10","publication_quarantine_reason":"rights unknown"}`,
			want:     generationapi.OutcomeCategoryPublicationEligibility,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			problem := &domain.Problem{MetadataJSON: json.RawMessage(test.metadata)}
			got, err := generationJobQuarantineOutcomeCategory(problem)
			require.NoError(t, err)
			require.Equal(t, test.want, got)
			evidence, err := generationJobOutcomeEvidenceRef(problem, got)
			require.NoError(t, err)
			require.NotEmpty(t, evidence.Kind)
			require.True(t, isCanonicalSHA256(evidence.SHA256))
		})
	}
}

func TestGenerationJobQuarantineOutcomeRejectsAmbiguousMetadata(t *testing.T) {
	for _, metadata := range []string{
		`{}`,
		`{"review_quarantine":{"reason":"review denied"}}`,
		`{"publication_quarantine_reason":"rights unknown"}`,
		`{"review_quarantine":{"reason":"review denied","workflow_run_id":"run-v0","review_gate_change_id":"problem-generation-review-gate-v2","review_gate_version":2,"review_result_sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},"publication_gate_status":"quarantined"}`,
		`{"review_quarantine":"corrupt","publication_gate_status":"quarantined","publication_gate_version":"v","publication_quarantine_reason":"r"}`,
		`{"review_quarantine":{"reason":"review denied","workflow_run_id":"run-v0","review_gate_change_id":"problem-generation-review-gate-v2","review_gate_version":2,"review_result_sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},"publication_gate_version":"v"}`,
		`{"publication_gate_status":"quarantined","publication_gate_version":"v","publication_policy_version":7,"publication_quarantine_reason":"r"}`,
	} {
		problem := &domain.Problem{MetadataJSON: json.RawMessage(metadata)}
		_, err := generationJobQuarantineOutcomeCategory(problem)
		require.Error(t, err)
	}
}

func generationJobTestRequestBody(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "generationapi", "testdata", "product-request-v0.json"))
	require.NoError(t, err)
	return string(data)
}

func generationJobTestIdentity(t *testing.T, body string, key string) (string, string, string) {
	t.Helper()
	var request generationapi.Request
	require.NoError(t, json.Unmarshal([]byte(body), &request))
	_, payloadSHA256, err := request.CanonicalPayload()
	require.NoError(t, err)
	jobID, principalSHA256, err := generationapi.JobIDForIdempotencyKey("local-dev", key)
	require.NoError(t, err)
	return jobID, payloadSHA256, principalSHA256
}

func generationJobTestDescription(
	t *testing.T,
	jobID string,
	status enumspb.WorkflowExecutionStatus,
	payloadSHA256 string,
	principalSHA256 string,
) *workflowservice.DescribeWorkflowExecutionResponse {
	t.Helper()
	fields := make(map[string]*commonpb.Payload)
	for key, value := range map[string]string{
		generationapi.MemoContractVersionKey: generationapi.JobContractVersion,
		generationapi.MemoPayloadSHA256Key:   payloadSHA256,
		generationapi.MemoPrincipalScopeKey:  principalSHA256,
	} {
		payload, err := converter.GetDefaultDataConverter().ToPayload(value)
		require.NoError(t, err)
		fields[key] = payload
	}
	return &workflowservice.DescribeWorkflowExecutionResponse{
		WorkflowExecutionInfo: &workflowpb.WorkflowExecutionInfo{
			Execution: &commonpb.WorkflowExecution{WorkflowId: jobID, RunId: generationJobTestRunID},
			Type:      &commonpb.WorkflowType{Name: generationJobWorkflowType},
			Status:    status,
			Memo:      &commonpb.Memo{Fields: fields},
		},
	}
}

func generationJobStandardTestDescription(
	t *testing.T,
	jobID string,
	status enumspb.WorkflowExecutionStatus,
	payloadSHA256 string,
	principalSHA256 string,
) *workflowservice.DescribeWorkflowExecutionResponse {
	description := generationJobTestDescription(t, jobID, status, payloadSHA256, principalSHA256)
	info := description.GetWorkflowExecutionInfo()
	info.Type = &commonpb.WorkflowType{Name: generationapi.StandardEvidenceWorkflowTypeV1}
	payload, err := converter.GetDefaultDataConverter().ToPayload(generationapi.EvidenceStandard)
	require.NoError(t, err)
	info.Memo.Fields[generationapi.MemoEvidenceLevelKey] = payload
	return description
}

func generationJobQualityTestDescription(
	t *testing.T,
	jobID string,
	status enumspb.WorkflowExecutionStatus,
	payloadSHA256 string,
	principalSHA256 string,
	evidenceLevel string,
) *workflowservice.DescribeWorkflowExecutionResponse {
	description := generationJobTestDescription(t, jobID, status, payloadSHA256, principalSHA256)
	info := description.GetWorkflowExecutionInfo()
	info.Type = &commonpb.WorkflowType{Name: generationapi.QualityWorkflowTypeV1}
	payload, err := converter.GetDefaultDataConverter().ToPayload(evidenceLevel)
	require.NoError(t, err)
	info.Memo.Fields[generationapi.MemoEvidenceLevelKey] = payload
	return description
}

func generationJobQualityArtifactRef(t *testing.T, workflowID, digest, producer, contentType string) activities.ArtifactRef {
	t.Helper()
	ref := activities.ArtifactRef{
		SchemaVersion:  activities.ArtifactRefSchemaVersion,
		PayloadVersion: activities.ActivityPayloadVersion,
		Bucket:         "quality-test", Key: "workflow-artifacts/v1/sha256/" + digest[:2] + "/" + digest,
		SHA256: digest, SizeBytes: 1, ContentType: contentType,
		Producer: producer, Provider: "algoforge", Model: "not_applicable",
		ModelRevision: "not_applicable", WorkflowID: workflowID,
	}
	require.NoError(t, ref.Validate(ref.Bucket))
	return ref
}

func TestGenerationJobEvidenceRoutingPairsWorkflowTypeAndMemo(t *testing.T) {
	jobID := generationapi.JobIDPrefix + strings.Repeat("a", 64)
	minimal := generationJobTestDescription(t, jobID, enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING, strings.Repeat("b", 64), strings.Repeat("c", 64))
	level, err := generationJobEvidenceLevelFromInfo(minimal.GetWorkflowExecutionInfo())
	require.NoError(t, err)
	require.Equal(t, generationapi.EvidenceMinimal, level)

	standard := generationJobStandardTestDescription(t, jobID, enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING, strings.Repeat("b", 64), strings.Repeat("c", 64))
	level, err = generationJobEvidenceLevelFromInfo(standard.GetWorkflowExecutionInfo())
	require.NoError(t, err)
	require.Equal(t, generationapi.EvidenceStandard, level)

	standard.GetWorkflowExecutionInfo().Type = &commonpb.WorkflowType{Name: generationJobWorkflowType}
	if _, err := generationJobEvidenceLevelFromInfo(standard.GetWorkflowExecutionInfo()); err == nil {
		t.Fatal("minimal workflow type accepted standard evidence memo")
	}

	quality := generationJobQualityTestDescription(t, jobID, enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING, strings.Repeat("b", 64), strings.Repeat("c", 64), generationapi.EvidenceStandard)
	level, err = generationJobEvidenceLevelFromInfo(quality.GetWorkflowExecutionInfo())
	require.NoError(t, err)
	require.Equal(t, generationapi.EvidenceStandard, level)
}

func generationJobTestEncodedQuery(t *testing.T, query domain.WorkflowStateQuery) converter.EncodedValue {
	t.Helper()
	payloads, err := converter.GetDefaultDataConverter().ToPayloads(query)
	require.NoError(t, err)
	return client.NewValue(payloads)
}

func invokeGenerationJobHandler(
	t *testing.T,
	method string,
	path string,
	jobID string,
	body string,
	idempotencyKey string,
	handler echo.HandlerFunc,
) *httptest.ResponseRecorder {
	t.Helper()
	e := echo.New()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	if idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}
	recorder := httptest.NewRecorder()
	ctx := e.NewContext(request, recorder)
	if jobID != "" {
		ctx.SetParamNames("id")
		ctx.SetParamValues(jobID)
	}
	require.NoError(t, handler(ctx))
	return recorder
}

func requireGenerationJobResponseDoesNotLeakTemporal(t *testing.T, body string) {
	t.Helper()
	for _, forbidden := range []string{"run_id", "workflow_id", "current_step", "steps", "activity_type", "failure_reason", "private provider failure text"} {
		require.NotContains(t, body, forbidden)
	}
}
