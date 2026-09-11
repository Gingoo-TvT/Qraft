package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/generationapi"
	authmw "github.com/Gingoo-TvT/Qraft/backend/internal/handler/middleware"
	"github.com/Gingoo-TvT/Qraft/backend/internal/qualitymode"
	"github.com/Gingoo-TvT/Qraft/backend/internal/workflow/activities"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	workflowpb "go.temporal.io/api/workflow/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/temporal"
)

const generationJobWorkflowType = "ProblemGenerationWorkflow"
const generationJobRuntimeKeyTTLGrace = 5 * time.Minute

var (
	errGenerationJobNotFound            = errors.New("generation job not found")
	errGenerationJobNotOwned            = errors.New("generation job does not belong to principal")
	errGenerationJobIdempotencyConflict = errors.New("idempotency key is bound to a different request")
	errGenerationJobPrincipalRequired   = errors.New("authenticated principal requires a stable subject identifier")
)

type generationJobService interface {
	TriggerGenerationJob(
		context.Context,
		string,
		string,
		string,
		time.Duration,
		domain.ProblemGenParams,
	) (client.WorkflowRun, error)
	GetProblemByWorkflowID(context.Context, string) (*domain.Problem, error)
	GetGenerationStandardEvidence(context.Context, uuid.UUID) (domain.GenerationStandardEvidenceBinding, bool, error)
}

type generationJobTemporalClient interface {
	DescribeWorkflowExecution(context.Context, string, string) (*workflowservice.DescribeWorkflowExecutionResponse, error)
	QueryWorkflow(context.Context, string, string, string, ...interface{}) (converter.EncodedValue, error)
	GetWorkflow(context.Context, string, string) client.WorkflowRun
	CancelWorkflow(context.Context, string, string) error
}

// GenerationJobHandler exposes the additive stable generation-jobs resource.
// Temporal execution details remain an internal implementation concern.
type GenerationJobHandler struct {
	service        generationJobService
	temporal       generationJobTemporalClient
	providerHelper *ProblemHandler
	now            func() time.Time
	pollInterval   time.Duration
	qualityMode    qualitymode.Audit
}

func NewGenerationJobHandler(
	service generationJobService,
	temporalClient generationJobTemporalClient,
	providerHelper *ProblemHandler,
) *GenerationJobHandler {
	mode, _ := qualitymode.ResolveMode("", false, nil)
	handler, _ := NewGenerationJobHandlerWithQualityMode(service, temporalClient, providerHelper, mode)
	return handler
}

// NewGenerationJobHandlerWithQualityMode applies a startup-resolved profile.
// Invalid manually assembled audits are rejected instead of silently enabling
// a partial standard/audit surface.
func NewGenerationJobHandlerWithQualityMode(
	service generationJobService,
	temporalClient generationJobTemporalClient,
	providerHelper *ProblemHandler,
	mode qualitymode.Audit,
) (*GenerationJobHandler, error) {
	if err := qualitymode.ValidateAudit(mode); err != nil {
		return nil, err
	}
	return &GenerationJobHandler{
		service:        service,
		temporal:       temporalClient,
		providerHelper: providerHelper,
		now:            time.Now,
		pollInterval:   2 * time.Second,
		qualityMode:    mode,
	}, nil
}

func RegisterGenerationJobRoutes(group *echo.Group, handler *GenerationJobHandler) {
	group.POST("/generation/jobs", handler.HandleCreate)
	group.GET("/generation/jobs/:id", handler.HandleStatus)
	group.GET("/generation/jobs/:id/result", handler.HandleResult)
	group.GET("/generation/jobs/:id/events", handler.HandleEvents)
	group.DELETE("/generation/jobs/:id", handler.HandleCancel)
}

func (h *GenerationJobHandler) HandleCreate(c echo.Context) error {
	request, err := decodeGenerationJobRequest(c)
	if err != nil {
		return generationJobHTTPError(c, http.StatusBadRequest, generationapi.ErrorUnsatisfiableSpec, "request body does not match the generation jobs v1 contract")
	}
	_, payloadSHA256, err := request.CanonicalPayload()
	if err != nil {
		return generationJobContractError(c, err)
	}
	evidenceLevel := request.Canonical().Output.EvidenceLevel
	if evidenceLevel != generationapi.EvidenceMinimal && !h.qualityMode.ExtendedEvidenceLevels {
		return generationJobHTTPError(
			c,
			http.StatusUnprocessableEntity,
			generationapi.ErrorUnsupportedConstraint,
			"output.evidence_level="+evidenceLevel+" is unavailable while quality mode is legacy-only",
		)
	}

	principalScope, err := generationJobPrincipalScope(c)
	if err != nil {
		return generationJobHTTPError(c, http.StatusUnauthorized, generationapi.ErrorUnauthorized, errGenerationJobPrincipalRequired.Error())
	}
	jobID, principalSHA256, err := generationapi.JobIDForIdempotencyKey(
		principalScope,
		c.Request().Header.Get("Idempotency-Key"),
	)
	if err != nil {
		return generationJobHTTPError(c, http.StatusBadRequest, generationapi.ErrorUnsatisfiableSpec, err.Error())
	}

	ctx := c.Request().Context()
	_, err = h.describeGenerationJob(ctx, jobID, principalSHA256, payloadSHA256)
	switch {
	case err == nil:
		return c.JSON(http.StatusOK, APIResponse{Success: true, Data: generationJobAccepted(jobID, true)})
	case errors.Is(err, errGenerationJobIdempotencyConflict):
		return generationJobHTTPError(c, http.StatusConflict, generationapi.ErrorIdempotencyConflict, "idempotency key is already bound to a different request")
	case errors.Is(err, errGenerationJobNotFound):
		// A new job may be started below.
	case err != nil:
		return generationJobLookupError(c, err)
	}

	params := request.ToProblemGenParams()
	if h.providerHelper == nil {
		return generationJobHTTPError(c, http.StatusServiceUnavailable, generationapi.ErrorInternal, "generation provider configuration is unavailable")
	}
	if err := h.providerHelper.applyPermanentProviderConfig(ctx, &params.ProviderConfig); err != nil {
		return generationJobHTTPError(c, http.StatusServiceUnavailable, generationapi.ErrorInternal, "generation provider configuration is unavailable")
	}
	runtimeKeyTTL := request.WorkflowTimeout() + generationJobRuntimeKeyTTLGrace
	if err := h.providerHelper.prepareProviderRuntimeConfigWithTTL(
		ctx,
		params.ProviderConfig,
		runtimeKeyTTL,
	); err != nil {
		return generationJobHTTPError(c, http.StatusServiceUnavailable, generationapi.ErrorInternal, "generation provider configuration could not be prepared")
	}

	_, err = h.service.TriggerGenerationJob(
		ctx,
		jobID,
		payloadSHA256,
		principalSHA256,
		request.WorkflowTimeout(),
		params,
	)
	if err != nil {
		var alreadyStarted *serviceerror.WorkflowExecutionAlreadyStarted
		if errors.As(err, &alreadyStarted) {
			_, describeErr := h.describeGenerationJob(ctx, jobID, principalSHA256, payloadSHA256)
			switch {
			case describeErr == nil:
				return c.JSON(http.StatusOK, APIResponse{Success: true, Data: generationJobAccepted(jobID, true)})
			case errors.Is(describeErr, errGenerationJobIdempotencyConflict):
				return generationJobHTTPError(c, http.StatusConflict, generationapi.ErrorIdempotencyConflict, "idempotency key is already bound to a different request")
			default:
				return generationJobLookupError(c, describeErr)
			}
		}
		if strings.HasPrefix(err.Error(), "validation: ") {
			return generationJobHTTPError(c, http.StatusUnprocessableEntity, generationapi.ErrorUnsatisfiableSpec, strings.TrimPrefix(err.Error(), "validation: "))
		}
		return generationJobHTTPError(c, http.StatusServiceUnavailable, generationapi.ErrorInternal, "generation job could not be started")
	}

	return c.JSON(http.StatusCreated, APIResponse{Success: true, Data: generationJobAccepted(jobID, false)})
}

func (h *GenerationJobHandler) HandleStatus(c echo.Context) error {
	snapshot, err := h.loadGenerationJobSnapshot(c, c.Param("id"))
	if err != nil {
		return generationJobLookupError(c, err)
	}
	return ok(c, snapshot.Status)
}

func (h *GenerationJobHandler) HandleResult(c echo.Context) error {
	snapshot, err := h.loadGenerationJobSnapshot(c, c.Param("id"))
	if err != nil {
		return generationJobLookupError(c, err)
	}
	status := snapshot.Status
	if !generationJobTerminal(status.Status) {
		return generationJobHTTPError(c, http.StatusConflict, generationapi.ErrorJobNotReady, "generation job result is not ready")
	}

	result := generationapi.JobResult{
		ContractVersion: generationapi.JobContractVersion,
		JobID:           status.JobID,
		Status:          status.Status,
		EvidenceLevel:   snapshot.EvidenceLevel,
		Candidates:      []generationapi.CandidateResult{},
		Error:           status.Error,
	}
	if snapshot.Problem != nil {
		problemID := snapshot.Problem.ID.String()
		candidate := generationapi.CandidateResult{
			CandidateID: status.JobID + "-c1",
			ProblemID:   problemID,
			Status:      status.Status,
			ProblemURL:  "/api/v1/problems/" + url.PathEscape(problemID),
		}
		if snapshot.WorkflowType == generationapi.QualityWorkflowTypeV1 && h.qualityMode.ExtendedEvidenceLevels {
			candidate.QualityReportURI = "/api/v1/problems/" + url.PathEscape(problemID) + "/quality"
		}
		if status.Status == generationapi.JobStatusQuarantined {
			if snapshot.StandardEvidence != nil {
				candidate.QuarantineReason = snapshot.StandardEvidence.QuarantineReason
				candidate.OutcomeCategory = generationapi.OutcomeCategory(snapshot.StandardEvidence.OutcomeCategory)
				result.Evidence = append(result.Evidence, generationapi.EvidenceRef{
					Kind:   snapshot.StandardEvidence.OutcomeKind,
					SHA256: snapshot.StandardEvidence.OutcomeSHA256,
				})
			} else {
				candidate.QuarantineReason = generationJobQuarantineReason(snapshot.Problem)
				candidate.OutcomeCategory, err = generationJobQuarantineOutcomeCategory(snapshot.Problem)
				if err != nil {
					return generationJobHTTPError(c, http.StatusInternalServerError, generationapi.ErrorInternal, "stored quarantine outcome is inconsistent")
				}
				outcomeEvidence, evidenceErr := generationJobOutcomeEvidenceRef(snapshot.Problem, candidate.OutcomeCategory)
				if evidenceErr != nil {
					return generationJobHTTPError(c, http.StatusInternalServerError, generationapi.ErrorInternal, "stored quarantine evidence is inconsistent")
				}
				result.Evidence = append(result.Evidence, outcomeEvidence)
			}
		}
		result.Candidates = append(result.Candidates, candidate)
		if snapshot.QualityEvidence != nil {
			qualityEvidence := *snapshot.QualityEvidence
			if !h.qualityMode.ExtendedEvidenceLevels {
				qualityEvidence.URI = ""
			}
			result.Evidence = append(result.Evidence, qualityEvidence)
		}
		manifest, ok, manifestErr := problemTestManifestReference(snapshot.Problem)
		if manifestErr != nil {
			return generationJobHTTPError(c, http.StatusInternalServerError, generationapi.ErrorInternal, "stored test manifest reference is inconsistent")
		}
		if ok {
			result.Evidence = append(result.Evidence, generationapi.EvidenceRef{
				Kind:   "test_manifest",
				SHA256: manifest.SHA256,
				URI:    "/api/v1/problems/" + url.PathEscape(problemID) + "/test-manifest",
			})
		}
		standardEvidence, ok, standardEvidenceErr := problemStandardEvidenceReference(snapshot.Problem, snapshot.StandardEvidence)
		if standardEvidenceErr != nil {
			return generationJobHTTPError(c, http.StatusInternalServerError, generationapi.ErrorInternal, "stored standard evidence reference is inconsistent")
		}
		if ok {
			result.Evidence = append(result.Evidence, generationapi.EvidenceRef{
				Kind:   "standard_evidence",
				SHA256: standardEvidence.SHA256,
				URI:    "/api/v1/problems/" + url.PathEscape(problemID) + "/standard-evidence",
			})
		}
	}
	if len(result.Evidence) > 0 {
		identity, identityErr := generationapi.BuildEvidenceBundleIdentityV0(result.Evidence)
		if identityErr != nil {
			return generationJobHTTPError(c, http.StatusInternalServerError, generationapi.ErrorInternal, "stored evidence identity is inconsistent")
		}
		result.EvidenceIdentity = &identity
	}
	return ok(c, result)
}

func (h *GenerationJobHandler) HandleEvents(c echo.Context) error {
	snapshot, err := h.loadGenerationJobSnapshot(c, c.Param("id"))
	if err != nil {
		return generationJobLookupError(c, err)
	}

	response := c.Response()
	response.Header().Set(echo.HeaderContentType, "text/event-stream")
	response.Header().Set("Cache-Control", "no-cache")
	response.Header().Set("Connection", "keep-alive")
	response.Header().Set("X-Accel-Buffering", "no")
	response.WriteHeader(http.StatusOK)
	flusher, ok := response.Writer.(http.Flusher)
	if !ok {
		return nil
	}

	sequence := 1
	if err := h.writeGenerationJobEvent(response, flusher, sequence, snapshot.Status); err != nil {
		return nil
	}
	if generationJobTerminal(snapshot.Status.Status) {
		return nil
	}

	ticker := time.NewTicker(h.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-c.Request().Context().Done():
			return nil
		case <-ticker.C:
			sequence++
			next, loadErr := h.loadGenerationJobSnapshot(c, c.Param("id"))
			if loadErr != nil {
				return nil
			}
			if err := h.writeGenerationJobEvent(response, flusher, sequence, next.Status); err != nil {
				return nil
			}
			if generationJobTerminal(next.Status.Status) {
				return nil
			}
		}
	}
}

func (h *GenerationJobHandler) HandleCancel(c echo.Context) error {
	jobID := c.Param("id")
	principalSHA256, err := generationJobPrincipalSHA256(c)
	if err != nil {
		if errors.Is(err, errGenerationJobPrincipalRequired) {
			return generationJobHTTPError(c, http.StatusUnauthorized, generationapi.ErrorUnauthorized, errGenerationJobPrincipalRequired.Error())
		}
		return generationJobHTTPError(c, http.StatusBadRequest, generationapi.ErrorUnsatisfiableSpec, "invalid generation job id")
	}
	info, err := h.describeGenerationJob(c.Request().Context(), jobID, principalSHA256, "")
	if err != nil {
		return generationJobLookupError(c, err)
	}
	if info.GetStatus() != enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING {
		snapshot, err := h.projectGenerationJobSnapshot(c.Request().Context(), jobID, info)
		if err != nil {
			return generationJobLookupError(c, err)
		}
		if generationJobTerminal(snapshot.Status.Status) {
			return ok(c, generationapi.JobCancellation{
				ContractVersion: generationapi.JobContractVersion,
				JobID:           jobID,
				Status:          snapshot.Status.Status,
				Links:           generationJobLinks(jobID),
			})
		}
	}

	runID := info.GetExecution().GetRunId()
	if err := h.temporal.CancelWorkflow(c.Request().Context(), jobID, runID); err != nil {
		var notFound *serviceerror.NotFound
		if errors.As(err, &notFound) {
			latest, loadErr := h.loadGenerationJobSnapshot(c, jobID)
			if loadErr == nil && generationJobTerminal(latest.Status.Status) {
				return ok(c, generationapi.JobCancellation{
					ContractVersion: generationapi.JobContractVersion,
					JobID:           jobID,
					Status:          latest.Status.Status,
					Links:           generationJobLinks(jobID),
				})
			}
		}
		return generationJobHTTPError(c, http.StatusServiceUnavailable, generationapi.ErrorInternal, "generation job cancellation could not be requested")
	}
	return c.JSON(http.StatusAccepted, APIResponse{Success: true, Data: generationapi.JobCancellation{
		ContractVersion: generationapi.JobContractVersion,
		JobID:           jobID,
		Status:          generationapi.JobStatusCancellationRequested,
		Links:           generationJobLinks(jobID),
	}})
}

type generationJobSnapshot struct {
	Status           generationapi.JobStatus
	Problem          *domain.Problem
	WorkflowType     string
	EvidenceLevel    string
	StandardEvidence *domain.GenerationStandardEvidenceBinding
	QualityEvidence  *generationapi.EvidenceRef
}

func (h *GenerationJobHandler) loadGenerationJobSnapshot(
	c echo.Context,
	jobID string,
) (*generationJobSnapshot, error) {
	principalSHA256, err := generationJobPrincipalSHA256(c)
	if err != nil {
		return nil, errGenerationJobNotFound
	}
	info, err := h.describeGenerationJob(c.Request().Context(), jobID, principalSHA256, "")
	if err != nil {
		return nil, err
	}
	return h.projectGenerationJobSnapshot(c.Request().Context(), jobID, info)
}

func (h *GenerationJobHandler) projectGenerationJobSnapshot(
	ctx context.Context,
	jobID string,
	info *workflowpb.WorkflowExecutionInfo,
) (*generationJobSnapshot, error) {
	status := generationapi.JobStatus{
		ContractVersion: generationapi.JobContractVersion,
		JobID:           jobID,
		Status:          generationapi.JobStatusQueued,
		Phase:           generationapi.JobPhaseQueued,
		Progress:        0,
		Links:           generationJobLinks(jobID),
	}
	workflowType := info.GetType().GetName()
	snapshot := &generationJobSnapshot{Status: status, WorkflowType: workflowType}
	evidenceLevel, err := generationJobEvidenceLevelFromInfo(info)
	if err != nil {
		return nil, fmt.Errorf("generation job evidence routing is inconsistent: %w", err)
	}
	snapshot.EvidenceLevel = evidenceLevel

	switch info.GetStatus() {
	case enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING:
		value, err := h.temporal.QueryWorkflow(
			ctx,
			jobID,
			info.GetExecution().GetRunId(),
			domain.WorkflowStateQueryName,
		)
		if err != nil {
			return nil, fmt.Errorf("querying generation job state: %w", err)
		}
		var query domain.WorkflowStateQuery
		if err := value.Get(&query); err != nil {
			return nil, fmt.Errorf("decoding generation job state: %w", err)
		}
		snapshot.Status.Status = generationapi.JobStatusRunning
		snapshot.Status.Phase = generationJobPhase(query.State.CurrentStep, query.State.Status)
		snapshot.Status.Progress = clampGenerationJobProgress(query.State.Progress, false)
		if query.State.Status == domain.WorkflowStatusPending {
			snapshot.Status.Status = generationapi.JobStatusQueued
		}
		return snapshot, nil

	case enumspb.WORKFLOW_EXECUTION_STATUS_COMPLETED:
		problem, err := h.service.GetProblemByWorkflowID(ctx, jobID)
		if err != nil {
			return nil, err
		}
		snapshot.Problem = problem
		if workflowType == generationapi.QualityWorkflowTypeV1 {
			qualityEvidence, evidenceErr := generationJobQualityEvidenceReference(problem, evidenceLevel)
			if evidenceErr != nil {
				return nil, evidenceErr
			}
			snapshot.QualityEvidence = &qualityEvidence
		} else {
			binding, hasBinding, bindingErr := h.service.GetGenerationStandardEvidence(ctx, problem.ID)
			if bindingErr != nil {
				return nil, bindingErr
			}
			if hasBinding {
				snapshot.StandardEvidence = &binding
			}
			_, hasStandardEvidence, evidenceErr := problemStandardEvidenceReference(problem, snapshot.StandardEvidence)
			if evidenceErr != nil {
				return nil, evidenceErr
			}
			if (evidenceLevel == generationapi.EvidenceStandard) != hasStandardEvidence {
				return nil, fmt.Errorf("generation job evidence level does not match stored receipt binding")
			}
		}
		snapshot.Status.Phase = generationapi.JobPhaseCompleted
		snapshot.Status.Progress = 100
		snapshot.Status.ResultAvailable = true
		finalStatus := problem.Status
		if workflowType == generationapi.QualityWorkflowTypeV1 {
			snapshot.Status.Status = generationapi.JobStatusSucceeded
			return snapshot, nil
		}
		if snapshot.StandardEvidence != nil {
			finalStatus = snapshot.StandardEvidence.FinalStatus
		}
		if finalStatus == domain.ProblemStatusPublished {
			snapshot.Status.Status = generationapi.JobStatusSucceeded
		} else {
			snapshot.Status.Status = generationapi.JobStatusQuarantined
		}
		return snapshot, nil

	case enumspb.WORKFLOW_EXECUTION_STATUS_FAILED:
		run := h.temporal.GetWorkflow(ctx, jobID, info.GetExecution().GetRunId())
		var state domain.WorkflowState
		runErr := run.Get(ctx, &state)
		snapshot.Status.Status = generationapi.JobStatusFailed
		snapshot.Status.Phase = generationapi.JobPhaseCompleted
		snapshot.Status.Error = classifyGenerationJobFailure(runErr)
		return snapshot, nil

	case enumspb.WORKFLOW_EXECUTION_STATUS_TIMED_OUT:
		snapshot.Status.Status = generationapi.JobStatusFailed
		snapshot.Status.Phase = generationapi.JobPhaseCompleted
		snapshot.Status.Error = &generationapi.JobError{
			Code:            generationapi.ErrorBudgetExhausted,
			Message:         "generation job exhausted its wall-time budget",
			Retryable:       false,
			OutcomeCategory: generationapi.OutcomeCategoryTechnical,
		}
		return snapshot, nil

	case enumspb.WORKFLOW_EXECUTION_STATUS_CANCELED:
		snapshot.Status.Status = generationapi.JobStatusCancelled
		snapshot.Status.Phase = generationapi.JobPhaseCompleted
		return snapshot, nil

	default:
		snapshot.Status.Status = generationapi.JobStatusFailed
		snapshot.Status.Phase = generationapi.JobPhaseCompleted
		snapshot.Status.Error = &generationapi.JobError{
			Code:            generationapi.ErrorInternal,
			Message:         "generation job ended without a supported terminal result",
			Retryable:       true,
			OutcomeCategory: generationapi.OutcomeCategoryTechnical,
		}
		return snapshot, nil
	}
}

func (h *GenerationJobHandler) describeGenerationJob(
	ctx context.Context,
	jobID string,
	principalSHA256 string,
	payloadSHA256 string,
) (*workflowpb.WorkflowExecutionInfo, error) {
	if !generationapi.IsJobID(jobID) {
		return nil, errGenerationJobNotFound
	}
	description, err := h.temporal.DescribeWorkflowExecution(ctx, jobID, "")
	if err != nil {
		var notFound *serviceerror.NotFound
		if errors.As(err, &notFound) {
			return nil, errGenerationJobNotFound
		}
		return nil, err
	}
	info := description.GetWorkflowExecutionInfo()
	if info == nil || !generationJobSupportedWorkflowType(info.GetType().GetName()) {
		return nil, errGenerationJobNotFound
	}
	contractVersion, err := generationJobMemoString(info, generationapi.MemoContractVersionKey)
	if err != nil || contractVersion != generationapi.JobContractVersion {
		return nil, errGenerationJobNotFound
	}
	storedPrincipal, err := generationJobMemoString(info, generationapi.MemoPrincipalScopeKey)
	if err != nil || storedPrincipal != principalSHA256 {
		return nil, errGenerationJobNotOwned
	}
	if payloadSHA256 != "" {
		storedPayload, err := generationJobMemoString(info, generationapi.MemoPayloadSHA256Key)
		if err != nil {
			return nil, errGenerationJobNotFound
		}
		if storedPayload != payloadSHA256 {
			return nil, errGenerationJobIdempotencyConflict
		}
	}
	return info, nil
}

func generationJobSupportedWorkflowType(workflowType string) bool {
	return workflowType == generationJobWorkflowType ||
		workflowType == generationapi.StandardEvidenceWorkflowTypeV1 ||
		workflowType == generationapi.QualityWorkflowTypeV1
}

func generationJobEvidenceLevelFromInfo(info *workflowpb.WorkflowExecutionInfo) (string, error) {
	if info == nil || info.GetType() == nil {
		return "", fmt.Errorf("workflow type is missing")
	}
	workflowType := info.GetType().GetName()
	level, err := generationJobMemoString(info, generationapi.MemoEvidenceLevelKey)
	if err != nil {
		// Jobs created before the additive standard contract had no level memo
		// and always used the legacy minimal workflow type.
		if workflowType == generationJobWorkflowType {
			return generationapi.EvidenceMinimal, nil
		}
		return "", err
	}
	switch workflowType {
	case generationJobWorkflowType:
		if level != generationapi.EvidenceMinimal {
			return "", fmt.Errorf("legacy workflow type requires minimal evidence")
		}
	case generationapi.StandardEvidenceWorkflowTypeV1:
		if level != generationapi.EvidenceStandard {
			return "", fmt.Errorf("standard workflow type requires standard evidence")
		}
	case generationapi.QualityWorkflowTypeV1:
		if level != generationapi.EvidenceMinimal && level != generationapi.EvidenceStandard && level != generationapi.EvidenceAudit {
			return "", fmt.Errorf("quality workflow type has unsupported evidence level")
		}
	default:
		return "", fmt.Errorf("unsupported workflow type %q", workflowType)
	}
	return level, nil
}

func generationJobQualityEvidenceReference(problem *domain.Problem, expectedEvidenceLevel string) (generationapi.EvidenceRef, error) {
	if problem == nil || problem.WorkflowID == nil || !generationapi.IsJobID(*problem.WorkflowID) {
		return generationapi.EvidenceRef{}, fmt.Errorf("quality materialization has no generation job binding")
	}
	var metadata map[string]json.RawMessage
	if err := json.Unmarshal(problem.MetadataJSON, &metadata); err != nil {
		return generationapi.EvidenceRef{}, fmt.Errorf("decode quality materialization metadata: %w", err)
	}
	raw, ok := metadata[generationapi.S3QualityMaterializationMetadataKey]
	if !ok {
		return generationapi.EvidenceRef{}, fmt.Errorf("quality materialization metadata is missing")
	}
	var evidence activities.S3QualityPassDraftEvidenceV1
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&evidence); err != nil {
		return generationapi.EvidenceRef{}, fmt.Errorf("decode quality materialization evidence: %w", err)
	}
	if evidence.SchemaVersion != generationapi.S3QualityMaterializationSchemaV1 || evidence.Decision != "pass" ||
		evidence.EvidenceLevel != expectedEvidenceLevel || !isCanonicalSHA256(evidence.SubjectRevision) ||
		evidence.SubjectRevision != evidence.FinalStatementArtifact.SHA256 {
		return generationapi.EvidenceRef{}, fmt.Errorf("quality materialization evidence identity is inconsistent")
	}
	if rawLevel, ok := metadata[generationapi.QualityEvidenceLevelMetadataKey]; ok {
		var persistedLevel string
		if err := json.Unmarshal(rawLevel, &persistedLevel); err != nil || persistedLevel != expectedEvidenceLevel {
			return generationapi.EvidenceRef{}, fmt.Errorf("quality materialization evidence level is inconsistent")
		}
	} else if expectedEvidenceLevel != generationapi.EvidenceMinimal {
		// Older minimal quality drafts predate the additive root field. No
		// standard/audit quality job was product-creatable before this field.
		return generationapi.EvidenceRef{}, fmt.Errorf("quality materialization evidence level is missing")
	}
	refs := []struct {
		producer string
		ref      activities.ArtifactRef
	}{
		{"GenerateAuthoringPlanActivity", evidence.AuthoringBundleArtifact},
		{"RenderStatementFromAuthoringBundleActivityV1", evidence.StatementDraftArtifact},
		{"FinalizeAuthoringStatementSamplesActivityV1", evidence.FinalStatementArtifact},
		{"GenerateMainSolutionActivityV1", evidence.MainProgramArtifact},
		{"GenerateOracleCandidateActivityV1", evidence.OracleProgramArtifact},
		{activities.VerifiedProgramReceiptProducerV1, evidence.OracleReceiptArtifact},
		{"BuildS3TestManifestActivityV1", evidence.TestManifestArtifact},
		{"RecomputeS3VerdictActivityV1", evidence.AuditArtifact},
	}
	for _, item := range refs {
		if err := item.ref.Validate(item.ref.Bucket); err != nil || item.ref.Producer != item.producer || item.ref.WorkflowID != *problem.WorkflowID {
			return generationapi.EvidenceRef{}, fmt.Errorf("quality materialization artifact binding is inconsistent")
		}
	}
	manifest, hasManifest, err := problemTestManifestReference(problem)
	if err != nil || !hasManifest || manifest.SchemaVersion != activities.TestManifestSchemaVersionV2 ||
		manifest.SHA256 != evidence.TestManifestArtifact.SHA256 || manifest.Artifact == nil || !manifest.Artifact.Equal(evidence.TestManifestArtifact) {
		return generationapi.EvidenceRef{}, fmt.Errorf("quality materialization TestManifest v2 binding is inconsistent")
	}
	return generationapi.EvidenceRef{
		Kind: "quality_audit", SHA256: evidence.AuditArtifact.SHA256,
		URI: "/api/v1/problems/" + url.PathEscape(problem.ID.String()) + "/quality/audit",
	}, nil
}

func generationJobMemoString(info *workflowpb.WorkflowExecutionInfo, key string) (string, error) {
	if info == nil || info.GetMemo() == nil {
		return "", fmt.Errorf("generation job memo is missing")
	}
	payload, ok := info.GetMemo().GetFields()[key]
	if !ok || payload == nil {
		return "", fmt.Errorf("generation job memo field is missing")
	}
	var value string
	if err := converter.GetDefaultDataConverter().FromPayload(payload, &value); err != nil {
		return "", fmt.Errorf("decode generation job memo: %w", err)
	}
	return value, nil
}

func decodeGenerationJobRequest(c echo.Context) (generationapi.Request, error) {
	reader := http.MaxBytesReader(c.Response(), c.Request().Body, generationapi.MaxRequestBytes)
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	var request generationapi.Request
	if err := decoder.Decode(&request); err != nil {
		return generationapi.Request{}, err
	}
	var trailing interface{}
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return generationapi.Request{}, fmt.Errorf("request contains a trailing JSON document")
		}
		return generationapi.Request{}, err
	}
	return request, nil
}

func generationJobPrincipalScope(c echo.Context) (string, error) {
	claims := authmw.GetClaims(c)
	if claims == nil {
		return "local-dev", nil
	}
	if userID := strings.TrimSpace(claims.UserID); userID != "" {
		return "user:" + userID, nil
	}
	if subject := strings.TrimSpace(claims.Subject); subject != "" {
		return "subject:" + subject, nil
	}
	if email := strings.ToLower(strings.TrimSpace(claims.Email)); email != "" {
		return "email:" + email, nil
	}
	return "", errGenerationJobPrincipalRequired
}

func generationJobPrincipalSHA256(c echo.Context) (string, error) {
	principalScope, err := generationJobPrincipalScope(c)
	if err != nil {
		return "", err
	}
	_, principalSHA256, err := generationapi.JobIDForIdempotencyKey(
		principalScope,
		"principal-scope-probe",
	)
	return principalSHA256, err
}

func generationJobAccepted(jobID string, replay bool) generationapi.JobAccepted {
	return generationapi.JobAccepted{
		ContractVersion:  generationapi.JobContractVersion,
		JobID:            jobID,
		Status:           generationapi.JobStatusAccepted,
		IdempotentReplay: replay,
		Links:            generationJobLinks(jobID),
	}
}

func generationJobLinks(jobID string) generationapi.JobLinks {
	base := "/api/v1/generation/jobs/" + url.PathEscape(jobID)
	return generationapi.JobLinks{
		Status: base,
		Result: base + "/result",
		Cancel: base,
		Events: base + "/events",
	}
}

func generationJobPhase(step domain.WorkflowStep, status domain.WorkflowStatus) string {
	if status == domain.WorkflowStatusWaitingReview {
		return generationapi.JobPhaseReviewing
	}
	switch step {
	case domain.StepSimilarityCheck,
		domain.StepGenerateStatement,
		domain.StepPostStatementSimilarity,
		domain.StepGenerateSolution,
		domain.StepGenerateTestdata:
		return generationapi.JobPhaseGenerating
	case domain.StepCompileCheck,
		domain.StepRunSandbox,
		domain.StepValidate,
		domain.StepAssessFeasibility:
		return generationapi.JobPhaseValidating
	case domain.StepLLMReview, domain.StepHumanReview:
		return generationapi.JobPhaseReviewing
	case domain.StepStore:
		return generationapi.JobPhaseStoring
	default:
		return generationapi.JobPhaseQueued
	}
}

func clampGenerationJobProgress(progress int, terminal bool) int {
	if progress < 0 {
		return 0
	}
	if terminal {
		if progress > 100 {
			return 100
		}
		return progress
	}
	if progress > 99 {
		return 99
	}
	return progress
}

func classifyGenerationJobFailure(err error) *generationapi.JobError {
	var timeoutErr *temporal.TimeoutError
	if errors.As(err, &timeoutErr) {
		return &generationapi.JobError{
			Code:            generationapi.ErrorBudgetExhausted,
			Message:         "generation job exhausted its execution budget",
			Retryable:       false,
			OutcomeCategory: generationapi.OutcomeCategoryTechnical,
		}
	}
	var applicationErr *temporal.ApplicationError
	if errors.As(err, &applicationErr) {
		switch applicationErr.Type() {
		case "TooSimilarError", "StatementTooSimilar", "MaxRetriesExceeded",
			"DuplicateProblem", "QualityNotMet", "CompilationError", "ValidationFailed":
			return &generationapi.JobError{
				Code:            generationapi.ErrorQualityNotMet,
				Message:         "candidate did not satisfy the quality gate",
				Retryable:       false,
				OutcomeCategory: generationapi.OutcomeCategoryContent,
			}
		case "ReferenceSolutionFailure":
			return &generationapi.JobError{
				Code:            generationapi.ErrorQualityNotMet,
				Message:         "reference/brute solution failed; regenerate the oracle (main solution was not rejected)",
				Retryable:       false,
				OutcomeCategory: generationapi.OutcomeCategoryTechnical,
			}
		case "HumanReviewRejection":
			return &generationapi.JobError{
				Code:            generationapi.ErrorQualityNotMet,
				Message:         "candidate did not satisfy the review gate",
				Retryable:       false,
				OutcomeCategory: generationapi.OutcomeCategoryReview,
			}
		case "UnsupportedConstraint":
			return &generationapi.JobError{
				Code:            generationapi.ErrorUnsupportedConstraint,
				Message:         "generation constraint is not supported",
				Retryable:       false,
				OutcomeCategory: generationapi.OutcomeCategoryTechnical,
			}
		case "UnsatisfiableSpec":
			return &generationapi.JobError{
				Code:            generationapi.ErrorUnsatisfiableSpec,
				Message:         "generation specification is unsatisfiable",
				Retryable:       false,
				OutcomeCategory: generationapi.OutcomeCategoryTechnical,
			}
		case "InvalidParameterError":
			return &generationapi.JobError{
				Code:            generationapi.ErrorQualityNotMet,
				Message:         "generation job failed inside the quality pipeline",
				Retryable:       false,
				OutcomeCategory: generationapi.OutcomeCategoryContent,
			}
		case "TruncatedLLMResponse":
			return &generationapi.JobError{
				Code:            generationapi.ErrorQualityNotMet,
				Message:         "generation job failed inside the quality pipeline",
				Retryable:       false,
				OutcomeCategory: generationapi.OutcomeCategoryTechnical,
			}
		}
	}
	return &generationapi.JobError{
		Code:            generationapi.ErrorInternal,
		Message:         "generation job failed inside the quality pipeline",
		Retryable:       true,
		OutcomeCategory: generationapi.OutcomeCategoryTechnical,
	}
}

func generationJobQuarantineOutcomeCategory(problem *domain.Problem) (generationapi.OutcomeCategory, error) {
	if problem == nil || len(problem.MetadataJSON) == 0 {
		return "", fmt.Errorf("quarantined problem metadata is missing")
	}
	var metadata map[string]interface{}
	if err := json.Unmarshal(problem.MetadataJSON, &metadata); err != nil {
		return "", fmt.Errorf("decode quarantined problem metadata: %w", err)
	}
	reviewRaw, reviewPresent := metadata["review_quarantine"]
	publicationPresent := false
	for _, key := range []string{
		"publication_gate_status",
		"publication_gate_version",
		"publication_policy_version",
		"publication_quarantine_reason",
	} {
		if _, ok := metadata[key]; ok {
			publicationPresent = true
			break
		}
	}
	if reviewPresent {
		if publicationPresent {
			return "", fmt.Errorf("review and publication quarantine metadata conflict")
		}
		nested, ok := reviewRaw.(map[string]interface{})
		if !ok || nested == nil {
			return "", fmt.Errorf("review quarantine metadata is incomplete")
		}
		changeID, _ := nested["review_gate_change_id"].(string)
		version, _ := nested["review_gate_version"].(float64)
		reason, _ := nested["reason"].(string)
		workflowRunID, _ := nested["workflow_run_id"].(string)
		reviewSHA256, _ := nested["review_result_sha256"].(string)
		if changeID != "problem-generation-review-gate-v2" || version != 2 ||
			strings.TrimSpace(reason) == "" || strings.TrimSpace(workflowRunID) == "" ||
			!isCanonicalSHA256(reviewSHA256) {
			return "", fmt.Errorf("review quarantine metadata is incomplete")
		}
		return generationapi.OutcomeCategoryReview, nil
	}
	publicationStatus, statusOK := metadata["publication_gate_status"].(string)
	publicationVersion, versionOK := metadata["publication_gate_version"].(string)
	publicationReason, reasonOK := metadata["publication_quarantine_reason"].(string)
	if policy, present := metadata["publication_policy_version"]; present {
		if _, ok := policy.(string); !ok {
			return "", fmt.Errorf("publication quarantine metadata is incomplete")
		}
	}
	if !publicationPresent || !statusOK || !versionOK || !reasonOK ||
		publicationStatus != string(domain.ProblemStatusQuarantined) ||
		strings.TrimSpace(publicationVersion) == "" || strings.TrimSpace(publicationReason) == "" {
		return "", fmt.Errorf("publication quarantine metadata is incomplete")
	}
	return generationapi.OutcomeCategoryPublicationEligibility, nil
}

func generationJobOutcomeEvidenceRef(
	problem *domain.Problem,
	category generationapi.OutcomeCategory,
) (generationapi.EvidenceRef, error) {
	if problem == nil || len(problem.MetadataJSON) == 0 {
		return generationapi.EvidenceRef{}, fmt.Errorf("quarantined problem metadata is missing")
	}
	var metadata map[string]interface{}
	if err := json.Unmarshal(problem.MetadataJSON, &metadata); err != nil {
		return generationapi.EvidenceRef{}, fmt.Errorf("decode quarantined problem metadata: %w", err)
	}
	switch category {
	case generationapi.OutcomeCategoryReview:
		nested, ok := metadata["review_quarantine"].(map[string]interface{})
		if !ok || nested == nil {
			return generationapi.EvidenceRef{}, fmt.Errorf("review quarantine evidence is missing")
		}
		reviewSHA256, _ := nested["review_result_sha256"].(string)
		if !isCanonicalSHA256(reviewSHA256) {
			return generationapi.EvidenceRef{}, fmt.Errorf("review result identity is invalid")
		}
		return generationapi.EvidenceRef{Kind: "review_result", SHA256: reviewSHA256}, nil
	case generationapi.OutcomeCategoryPublicationEligibility:
		gateVersion, _ := metadata["publication_gate_version"].(string)
		policyVersion, _ := metadata["publication_policy_version"].(string)
		status, _ := metadata["publication_gate_status"].(string)
		reason, _ := metadata["publication_quarantine_reason"].(string)
		return generationapi.PublicationDecisionEvidenceV0(
			gateVersion,
			policyVersion,
			status,
			reason,
		)
	default:
		return generationapi.EvidenceRef{}, fmt.Errorf("unsupported quarantine outcome category %q", category)
	}
}

func generationJobQuarantineReason(problem *domain.Problem) string {
	if problem == nil || len(problem.MetadataJSON) == 0 {
		return "candidate is not eligible for public release"
	}
	var metadata map[string]interface{}
	if err := json.Unmarshal(problem.MetadataJSON, &metadata); err != nil {
		return "candidate is not eligible for public release"
	}
	for _, key := range []string{"review_quarantine_reason", "publication_quarantine_reason"} {
		if value, ok := metadata[key].(string); ok {
			if reason := boundedGenerationJobText(value); reason != "" {
				return reason
			}
		}
	}
	if nested, ok := metadata["review_quarantine"].(map[string]interface{}); ok {
		if value, ok := nested["reason"].(string); ok {
			if reason := boundedGenerationJobText(value); reason != "" {
				return reason
			}
		}
	}
	return "candidate is not eligible for public release"
}

func boundedGenerationJobText(value string) string {
	value = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, strings.TrimSpace(value))
	runes := []rune(value)
	if len(runes) > 500 {
		runes = runes[:500]
	}
	return strings.TrimSpace(string(runes))
}

func generationJobTerminal(status string) bool {
	switch status {
	case generationapi.JobStatusSucceeded,
		generationapi.JobStatusQuarantined,
		generationapi.JobStatusFailed,
		generationapi.JobStatusCancelled:
		return true
	default:
		return false
	}
}

func (h *GenerationJobHandler) writeGenerationJobEvent(
	response *echo.Response,
	flusher http.Flusher,
	sequence int,
	status generationapi.JobStatus,
) error {
	eventType := "job_updated"
	if generationJobTerminal(status.Status) {
		eventType = "job_terminal"
	}
	event := generationapi.JobEvent{
		ContractVersion: generationapi.JobContractVersion,
		EventID:         fmt.Sprintf("%d", sequence),
		Type:            eventType,
		JobID:           status.JobID,
		Status:          status.Status,
		Phase:           status.Phase,
		Progress:        status.Progress,
		Error:           status.Error,
		OccurredAt:      h.now().UTC().Format(time.RFC3339Nano),
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(
		response,
		"id: %s\nevent: %s\ndata: %s\n\n",
		event.EventID,
		event.Type,
		payload,
	); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

func generationJobContractError(c echo.Context, err error) error {
	code := generationapi.ErrorCodeOf(err)
	status := http.StatusUnprocessableEntity
	if code == generationapi.ErrorUnsupportedConstraint {
		status = http.StatusUnprocessableEntity
	}
	return generationJobHTTPError(c, status, code, err.Error())
}

func generationJobLookupError(c echo.Context, err error) error {
	switch {
	case errors.Is(err, errGenerationJobPrincipalRequired):
		return generationJobHTTPError(c, http.StatusUnauthorized, generationapi.ErrorUnauthorized, errGenerationJobPrincipalRequired.Error())
	case errors.Is(err, errGenerationJobNotFound), errors.Is(err, errGenerationJobNotOwned):
		return generationJobHTTPError(c, http.StatusNotFound, generationapi.ErrorNotFound, "generation job not found")
	case errors.Is(err, errGenerationJobIdempotencyConflict):
		return generationJobHTTPError(c, http.StatusConflict, generationapi.ErrorIdempotencyConflict, "idempotency key is already bound to a different request")
	default:
		return generationJobHTTPError(c, http.StatusServiceUnavailable, generationapi.ErrorInternal, "generation job state is temporarily unavailable")
	}
}

func generationJobHTTPError(c echo.Context, status int, code generationapi.ErrorCode, message string) error {
	return c.JSON(status, APIResponse{
		Success: false,
		Error: &APIError{
			Code:    string(code),
			Message: message,
		},
	})
}
