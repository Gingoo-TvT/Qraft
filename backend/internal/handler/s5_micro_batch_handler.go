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

	"github.com/Gingoo-TvT/Qraft/backend/internal/diversity"
	"github.com/Gingoo-TvT/Qraft/backend/internal/diversityapi"
	"github.com/Gingoo-TvT/Qraft/backend/internal/diversitymode"
	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/generationapi"
	"github.com/Gingoo-TvT/Qraft/backend/internal/qualitymode"
	algoworkflow "github.com/Gingoo-TvT/Qraft/backend/internal/workflow"
	"github.com/Gingoo-TvT/Qraft/backend/internal/workflow/activities"
	"github.com/labstack/echo/v4"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	workflowpb "go.temporal.io/api/workflow/v1"
	"go.temporal.io/sdk/client"
)

// The parent reserves up to 30 minutes for concept selection before starting
// the longest child workflow. Keep runtime keys alive for that overhead and
// the same five-minute completion grace used by the stable Job API.
const s5MicroBatchRuntimeKeyTTLGraceV1 = 35 * time.Minute

var (
	errS5MicroBatchNotFound            = errors.New("S5 micro-batch not found")
	errS5MicroBatchNotOwned            = errors.New("S5 micro-batch does not belong to principal")
	errS5MicroBatchIdempotencyConflict = errors.New("idempotency key is bound to a different S5 micro-batch request")
)

type s5MicroBatchService interface {
	TriggerS5MicroBatch(
		context.Context,
		string,
		string,
		string,
		time.Duration,
		[]domain.ProblemGenParams,
	) (client.WorkflowRun, error)
	GetProblemByWorkflowID(context.Context, string) (*domain.Problem, error)
}

// S5MicroBatchHandler exposes the independently reversible S5 parent
// resource. It never treats child workflows as independently owned product
// jobs and therefore never relies on child Temporal visibility metadata.
type S5MicroBatchHandler struct {
	service        s5MicroBatchService
	temporal       generationJobTemporalClient
	providerHelper *ProblemHandler
	qualityMode    qualitymode.Audit
}

func NewS5MicroBatchHandler(
	service s5MicroBatchService,
	temporalClient generationJobTemporalClient,
	providerHelper *ProblemHandler,
	qualityMode qualitymode.Audit,
) (*S5MicroBatchHandler, error) {
	if err := qualitymode.ValidateAudit(qualityMode); err != nil {
		return nil, err
	}
	return &S5MicroBatchHandler{
		service: service, temporal: temporalClient, providerHelper: providerHelper, qualityMode: qualityMode,
	}, nil
}

// RegisterS5MicroBatchRoutes validates the startup audit and registers no
// route at all in legacy-only mode. The stable generation/jobs routes are not
// inspected or modified here.
func RegisterS5MicroBatchRoutes(
	group *echo.Group,
	handler *S5MicroBatchHandler,
	mode diversitymode.Audit,
) error {
	if err := diversitymode.ValidateAudit(mode); err != nil {
		return err
	}
	if !mode.MicroBatchRouteEnabled {
		return nil
	}
	if group == nil || handler == nil {
		return fmt.Errorf("S5 micro-batch route dependencies are required")
	}
	group.POST("/generation/micro-batches", handler.HandleCreate)
	group.GET("/generation/micro-batches/:id", handler.HandleStatus)
	group.GET("/generation/micro-batches/:id/result", handler.HandleResult)
	group.DELETE("/generation/micro-batches/:id", handler.HandleCancel)
	return nil
}

func (h *S5MicroBatchHandler) HandleCreate(c echo.Context) error {
	request, err := decodeS5MicroBatchRequest(c)
	if err != nil {
		return generationJobHTTPError(c, http.StatusBadRequest, generationapi.ErrorUnsatisfiableSpec, "request body does not match the S5 micro-batch v1 contract")
	}
	_, payloadSHA256, err := request.CanonicalPayload()
	if err != nil {
		return generationJobContractError(c, err)
	}
	for _, slot := range request.Canonical().Slots {
		if slot.Output.EvidenceLevel != generationapi.EvidenceMinimal && !h.qualityMode.ExtendedEvidenceLevels {
			return generationJobHTTPError(
				c,
				http.StatusUnprocessableEntity,
				generationapi.ErrorUnsupportedConstraint,
				"non-minimal output.evidence_level is unavailable while quality mode is legacy-only",
			)
		}
	}

	principalScope, err := generationJobPrincipalScope(c)
	if err != nil {
		return generationJobHTTPError(c, http.StatusUnauthorized, generationapi.ErrorUnauthorized, errGenerationJobPrincipalRequired.Error())
	}
	batchID, principalSHA256, err := diversityapi.BatchIDForIdempotencyKey(
		principalScope,
		c.Request().Header.Get("Idempotency-Key"),
	)
	if err != nil {
		return generationJobHTTPError(c, http.StatusBadRequest, generationapi.ErrorUnsatisfiableSpec, err.Error())
	}

	ctx := c.Request().Context()
	_, err = h.describeS5MicroBatch(ctx, batchID, principalSHA256, payloadSHA256)
	switch {
	case err == nil:
		return c.JSON(http.StatusOK, APIResponse{Success: true, Data: s5MicroBatchAccepted(batchID, true)})
	case errors.Is(err, errS5MicroBatchIdempotencyConflict):
		return s5MicroBatchHTTPError(c, http.StatusConflict, generationapi.ErrorIdempotencyConflict, "idempotency key is already bound to a different request")
	case errors.Is(err, errS5MicroBatchNotFound):
		// A new parent may be started below.
	case err != nil:
		return s5MicroBatchLookupError(c, err)
	}

	if h.providerHelper == nil {
		return s5MicroBatchHTTPError(c, http.StatusServiceUnavailable, generationapi.ErrorInternal, "generation provider configuration is unavailable")
	}
	canonical := request.Canonical()
	params := make([]domain.ProblemGenParams, len(canonical.Slots))
	runtimeKeyTTL := request.WorkflowTimeout() + s5MicroBatchRuntimeKeyTTLGraceV1
	for index, slot := range canonical.Slots {
		params[index] = slot.ToProblemGenParams()
		if err := h.providerHelper.applyPermanentProviderConfig(ctx, &params[index].ProviderConfig); err != nil {
			return s5MicroBatchHTTPError(c, http.StatusServiceUnavailable, generationapi.ErrorInternal, "generation provider configuration is unavailable")
		}
		if err := h.providerHelper.prepareProviderRuntimeConfigWithTTL(ctx, params[index].ProviderConfig, runtimeKeyTTL); err != nil {
			return s5MicroBatchHTTPError(c, http.StatusServiceUnavailable, generationapi.ErrorInternal, "generation provider configuration could not be prepared")
		}
	}

	_, err = h.service.TriggerS5MicroBatch(
		ctx,
		batchID,
		payloadSHA256,
		principalSHA256,
		request.WorkflowTimeout(),
		params,
	)
	if err != nil {
		var alreadyStarted *serviceerror.WorkflowExecutionAlreadyStarted
		if errors.As(err, &alreadyStarted) {
			_, describeErr := h.describeS5MicroBatch(ctx, batchID, principalSHA256, payloadSHA256)
			switch {
			case describeErr == nil:
				return c.JSON(http.StatusOK, APIResponse{Success: true, Data: s5MicroBatchAccepted(batchID, true)})
			case errors.Is(describeErr, errS5MicroBatchIdempotencyConflict):
				return s5MicroBatchHTTPError(c, http.StatusConflict, generationapi.ErrorIdempotencyConflict, "idempotency key is already bound to a different request")
			default:
				return s5MicroBatchLookupError(c, describeErr)
			}
		}
		if strings.HasPrefix(err.Error(), "validation: ") || strings.Contains(err.Error(), ": validation: ") {
			return s5MicroBatchHTTPError(c, http.StatusUnprocessableEntity, generationapi.ErrorUnsatisfiableSpec, boundedGenerationJobText(err.Error()))
		}
		return s5MicroBatchHTTPError(c, http.StatusServiceUnavailable, generationapi.ErrorInternal, "S5 micro-batch could not be started")
	}

	return c.JSON(http.StatusCreated, APIResponse{Success: true, Data: s5MicroBatchAccepted(batchID, false)})
}

func (h *S5MicroBatchHandler) HandleStatus(c echo.Context) error {
	snapshot, err := h.loadS5MicroBatchSnapshot(c, c.Param("id"))
	if err != nil {
		return s5MicroBatchLookupError(c, err)
	}
	return ok(c, snapshot.status)
}

func (h *S5MicroBatchHandler) HandleResult(c echo.Context) error {
	snapshot, err := h.loadS5MicroBatchSnapshot(c, c.Param("id"))
	if err != nil {
		return s5MicroBatchLookupError(c, err)
	}
	if !generationJobTerminal(snapshot.status.Status) {
		return s5MicroBatchHTTPError(c, http.StatusConflict, generationapi.ErrorJobNotReady, "S5 micro-batch result is not ready")
	}
	result := diversityapi.BatchResultV1{
		ContractVersion:   diversityapi.ContractVersion,
		BatchID:           snapshot.status.BatchID,
		Status:            snapshot.status.Status,
		CorpusRevision:    snapshot.status.CorpusRevision,
		ReservationSHA256: snapshot.status.ReservationSHA256,
		ManifestSHA256:    snapshot.status.ManifestSHA256,
		Slots:             []diversityapi.SlotResultV1{},
		Error:             snapshot.status.Error,
	}
	if snapshot.parentResult != nil {
		result.DedupObservations = append([]diversity.DedupObservationV1(nil), snapshot.parentResult.DedupObservations...)
	}
	for index, problem := range snapshot.problems {
		childID, childErr := diversityapi.ChildJobID(snapshot.status.BatchID, index)
		if childErr != nil || problem == nil {
			return s5MicroBatchHTTPError(c, http.StatusServiceUnavailable, generationapi.ErrorInternal, "stored S5 result is inconsistent")
		}
		problemID := problem.ID.String()
		slot := diversityapi.SlotResultV1{
			SlotIndex:  index,
			ChildID:    childID,
			Status:     generationapi.JobStatusSucceeded,
			ProblemID:  problemID,
			ProblemURL: "/api/v1/problems/" + url.PathEscape(problemID),
		}
		if snapshot.parentResult != nil {
			reserved := snapshot.parentResult.Reservation.Slots[index]
			slot.ConceptID = reserved.Winner.ConceptID
			slot.PoolSHA256 = reserved.PoolSHA256
			slot.StructuralSignatureSHA256 = reserved.Winner.StructuralSignatureSHA
		}
		result.Slots = append(result.Slots, slot)
	}
	return ok(c, result)
}

func (h *S5MicroBatchHandler) HandleCancel(c echo.Context) error {
	batchID := c.Param("id")
	principalSHA256, err := s5MicroBatchPrincipalSHA256(c)
	if err != nil {
		if errors.Is(err, errGenerationJobPrincipalRequired) {
			return s5MicroBatchHTTPError(c, http.StatusUnauthorized, generationapi.ErrorUnauthorized, errGenerationJobPrincipalRequired.Error())
		}
		return s5MicroBatchHTTPError(c, http.StatusBadRequest, generationapi.ErrorUnsatisfiableSpec, "invalid S5 micro-batch id")
	}
	info, err := h.describeS5MicroBatch(c.Request().Context(), batchID, principalSHA256, "")
	if err != nil {
		return s5MicroBatchLookupError(c, err)
	}
	if info.GetStatus() != enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING {
		snapshot, projectErr := h.projectS5MicroBatchSnapshot(c.Request().Context(), batchID, info)
		if projectErr != nil {
			return s5MicroBatchLookupError(c, projectErr)
		}
		if generationJobTerminal(snapshot.status.Status) {
			return ok(c, diversityapi.BatchCancellationV1{
				ContractVersion: diversityapi.ContractVersion,
				BatchID:         batchID,
				Status:          snapshot.status.Status,
				Links:           s5MicroBatchLinks(batchID),
			})
		}
	}
	if err := h.temporal.CancelWorkflow(c.Request().Context(), batchID, info.GetExecution().GetRunId()); err != nil {
		return s5MicroBatchHTTPError(c, http.StatusServiceUnavailable, generationapi.ErrorInternal, "S5 micro-batch cancellation could not be requested")
	}
	return c.JSON(http.StatusAccepted, APIResponse{Success: true, Data: diversityapi.BatchCancellationV1{
		ContractVersion: diversityapi.ContractVersion,
		BatchID:         batchID,
		Status:          generationapi.JobStatusCancellationRequested,
		Links:           s5MicroBatchLinks(batchID),
	}})
}

type s5MicroBatchSnapshot struct {
	status       diversityapi.BatchStatusV1
	problems     []*domain.Problem
	parentResult *algoworkflow.S5MicroBatchResultV1
}

func (h *S5MicroBatchHandler) loadS5MicroBatchSnapshot(c echo.Context, batchID string) (*s5MicroBatchSnapshot, error) {
	principalSHA256, err := s5MicroBatchPrincipalSHA256(c)
	if err != nil {
		if errors.Is(err, errGenerationJobPrincipalRequired) {
			return nil, err
		}
		return nil, errS5MicroBatchNotFound
	}
	info, err := h.describeS5MicroBatch(c.Request().Context(), batchID, principalSHA256, "")
	if err != nil {
		return nil, err
	}
	return h.projectS5MicroBatchSnapshot(c.Request().Context(), batchID, info)
}

func (h *S5MicroBatchHandler) projectS5MicroBatchSnapshot(
	ctx context.Context,
	batchID string,
	info *workflowpb.WorkflowExecutionInfo,
) (*s5MicroBatchSnapshot, error) {
	status := diversityapi.BatchStatusV1{
		ContractVersion: diversityapi.ContractVersion,
		BatchID:         batchID,
		Status:          generationapi.JobStatusQueued,
		Phase:           generationapi.JobPhaseQueued,
		Progress:        0,
		Slots:           s5MicroBatchSlotStatuses(batchID, generationapi.JobStatusQueued),
		Links:           s5MicroBatchLinks(batchID),
	}
	snapshot := &s5MicroBatchSnapshot{status: status}
	switch info.GetStatus() {
	case enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING:
		value, err := h.temporal.QueryWorkflow(
			ctx,
			batchID,
			info.GetExecution().GetRunId(),
			algoworkflow.S5MicroBatchStateQueryName,
		)
		if err != nil {
			return nil, fmt.Errorf("querying S5 micro-batch state: %w", err)
		}
		var state algoworkflow.S5MicroBatchStateV1
		if err := value.Get(&state); err != nil {
			return nil, fmt.Errorf("decoding S5 micro-batch state: %w", err)
		}
		slots, err := projectS5MicroBatchSlotState(batchID, state)
		if err != nil {
			return nil, err
		}
		snapshot.status.Status = generationapi.JobStatusRunning
		snapshot.status.Phase = projectS5MicroBatchPhase(state.Phase)
		snapshot.status.Progress = clampGenerationJobProgress(state.Progress, false)
		snapshot.status.Slots = slots
		if err := bindS5StatusHashesV1(&snapshot.status, state.CorpusRevision, state.ReservationSHA256, state.ManifestSHA256); err != nil {
			return nil, err
		}
		return snapshot, nil
	case enumspb.WORKFLOW_EXECUTION_STATUS_COMPLETED:
		run := h.temporal.GetWorkflow(ctx, batchID, info.GetExecution().GetRunId())
		var parentResult algoworkflow.S5MicroBatchResultV1
		if err := run.Get(ctx, &parentResult); err != nil {
			return nil, fmt.Errorf("loading S5 parent result: %w", err)
		}
		if parentResult.PayloadVersion != algoworkflow.S5MicroBatchPayloadVersionV1 ||
			parentResult.BatchID != batchID || len(parentResult.ChildJobIDs) != diversityapi.SlotCountV1 ||
			len(parentResult.Children) != diversityapi.SlotCountV1 {
			return nil, fmt.Errorf("S5 parent result has an invalid shape")
		}
		if err := validateS5ParentResultProjectionV1(parentResult); err != nil {
			return nil, err
		}
		for index := 0; index < diversityapi.SlotCountV1; index++ {
			childID, err := diversityapi.ChildJobID(batchID, index)
			if err != nil {
				return nil, err
			}
			child := parentResult.Children[index]
			if parentResult.ChildJobIDs[index] != childID || child.SlotIndex != index || child.JobID != childID ||
				child.Decision != "pass" || strings.TrimSpace(child.StoredProblemID) == "" ||
				child.StoredProblemStatus != domain.ProblemStatusDraft {
				return nil, fmt.Errorf("S5 parent result has an invalid child binding")
			}
			problem, err := h.service.GetProblemByWorkflowID(ctx, childID)
			if err != nil {
				return nil, fmt.Errorf("loading S5 slot %d product result: %w", index, err)
			}
			if problem == nil || problem.ID.String() != child.StoredProblemID {
				return nil, fmt.Errorf("S5 slot %d parent result does not match durable product data", index)
			}
			snapshot.problems = append(snapshot.problems, problem)
		}
		snapshot.status.Status = generationapi.JobStatusSucceeded
		snapshot.status.Phase = generationapi.JobPhaseCompleted
		snapshot.status.Progress = 100
		snapshot.status.ResultAvailable = true
		snapshot.status.Slots = s5MicroBatchSlotStatuses(batchID, generationapi.JobStatusSucceeded)
		snapshot.status.CorpusRevision = parentResult.CorpusRevision
		snapshot.status.ReservationSHA256 = parentResult.ReservationSHA256
		snapshot.status.ManifestSHA256 = parentResult.ManifestSHA256
		snapshot.parentResult = &parentResult
		return snapshot, nil
	case enumspb.WORKFLOW_EXECUTION_STATUS_FAILED:
		run := h.temporal.GetWorkflow(ctx, batchID, info.GetExecution().GetRunId())
		var ignored interface{}
		runErr := run.Get(ctx, &ignored)
		snapshot.status.Status = generationapi.JobStatusFailed
		snapshot.status.Phase = generationapi.JobPhaseCompleted
		snapshot.status.Error = classifyGenerationJobFailure(runErr)
		snapshot.status.Slots = s5MicroBatchSlotStatuses(batchID, generationapi.JobStatusFailed)
		return snapshot, nil
	case enumspb.WORKFLOW_EXECUTION_STATUS_TIMED_OUT:
		snapshot.status.Status = generationapi.JobStatusFailed
		snapshot.status.Phase = generationapi.JobPhaseCompleted
		snapshot.status.Error = &generationapi.JobError{
			Code: generationapi.ErrorBudgetExhausted, Message: "S5 micro-batch exhausted its wall-time budget",
			Retryable: false, OutcomeCategory: generationapi.OutcomeCategoryTechnical,
		}
		snapshot.status.Slots = s5MicroBatchSlotStatuses(batchID, generationapi.JobStatusFailed)
		return snapshot, nil
	case enumspb.WORKFLOW_EXECUTION_STATUS_CANCELED:
		snapshot.status.Status = generationapi.JobStatusCancelled
		snapshot.status.Phase = generationapi.JobPhaseCompleted
		snapshot.status.Slots = s5MicroBatchSlotStatuses(batchID, generationapi.JobStatusCancelled)
		return snapshot, nil
	default:
		return nil, fmt.Errorf("S5 micro-batch ended in an unsupported Temporal state")
	}
}

func (h *S5MicroBatchHandler) describeS5MicroBatch(
	ctx context.Context,
	batchID string,
	principalSHA256 string,
	payloadSHA256 string,
) (*workflowpb.WorkflowExecutionInfo, error) {
	if !diversityapi.IsBatchID(batchID) {
		return nil, errS5MicroBatchNotFound
	}
	description, err := h.temporal.DescribeWorkflowExecution(ctx, batchID, "")
	if err != nil {
		var notFound *serviceerror.NotFound
		if errors.As(err, &notFound) {
			return nil, errS5MicroBatchNotFound
		}
		return nil, err
	}
	info := description.GetWorkflowExecutionInfo()
	if info == nil || info.GetType().GetName() != diversityapi.WorkflowTypeV1 {
		return nil, errS5MicroBatchNotFound
	}
	contractVersion, err := generationJobMemoString(info, diversityapi.MemoContractVersionKey)
	if err != nil || contractVersion != diversityapi.ContractVersion {
		return nil, errS5MicroBatchNotFound
	}
	storedPrincipal, err := generationJobMemoString(info, diversityapi.MemoPrincipalScopeKey)
	if err != nil || storedPrincipal != principalSHA256 {
		return nil, errS5MicroBatchNotOwned
	}
	if payloadSHA256 != "" {
		storedPayload, err := generationJobMemoString(info, diversityapi.MemoPayloadSHA256Key)
		if err != nil {
			return nil, errS5MicroBatchNotFound
		}
		if storedPayload != payloadSHA256 {
			return nil, errS5MicroBatchIdempotencyConflict
		}
	}
	return info, nil
}

func decodeS5MicroBatchRequest(c echo.Context) (diversityapi.RequestV1, error) {
	reader := http.MaxBytesReader(c.Response(), c.Request().Body, diversityapi.MaxRequestBytes)
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	var request diversityapi.RequestV1
	if err := decoder.Decode(&request); err != nil {
		return diversityapi.RequestV1{}, err
	}
	var trailing interface{}
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return diversityapi.RequestV1{}, fmt.Errorf("request contains a trailing JSON document")
		}
		return diversityapi.RequestV1{}, err
	}
	return request, nil
}

func s5MicroBatchPrincipalSHA256(c echo.Context) (string, error) {
	principalScope, err := generationJobPrincipalScope(c)
	if err != nil {
		return "", err
	}
	_, principalSHA256, err := diversityapi.BatchIDForIdempotencyKey(principalScope, "principal-scope-probe")
	return principalSHA256, err
}

func s5MicroBatchAccepted(batchID string, replay bool) diversityapi.BatchAcceptedV1 {
	return diversityapi.BatchAcceptedV1{
		ContractVersion: diversityapi.ContractVersion,
		BatchID:         batchID, Status: generationapi.JobStatusAccepted,
		IdempotentReplay: replay, Links: s5MicroBatchLinks(batchID),
	}
}

func s5MicroBatchLinks(batchID string) diversityapi.BatchLinksV1 {
	base := "/api/v1/generation/micro-batches/" + url.PathEscape(batchID)
	return diversityapi.BatchLinksV1{Status: base, Result: base + "/result", Cancel: base}
}

func s5MicroBatchSlotStatuses(batchID string, status string) []diversityapi.SlotStatusV1 {
	slots := make([]diversityapi.SlotStatusV1, 0, diversityapi.SlotCountV1)
	for index := 0; index < diversityapi.SlotCountV1; index++ {
		childID, err := diversityapi.ChildJobID(batchID, index)
		if err != nil {
			return []diversityapi.SlotStatusV1{}
		}
		slots = append(slots, diversityapi.SlotStatusV1{SlotIndex: index, ChildID: childID, Status: status})
	}
	return slots
}

func projectS5MicroBatchSlotState(
	batchID string,
	state algoworkflow.S5MicroBatchStateV1,
) ([]diversityapi.SlotStatusV1, error) {
	if state.PayloadVersion != algoworkflow.S5MicroBatchPayloadVersionV1 || state.BatchID != batchID ||
		len(state.Children) != diversityapi.SlotCountV1 || len(state.ChildJobIDs) != diversityapi.SlotCountV1 {
		return nil, fmt.Errorf("S5 micro-batch query state has an invalid parent shape")
	}
	slots := make([]diversityapi.SlotStatusV1, diversityapi.SlotCountV1)
	for index, child := range state.Children {
		expectedID, err := diversityapi.ChildJobID(batchID, index)
		if err != nil || child.SlotIndex != index || child.JobID != expectedID || state.ChildJobIDs[index] != expectedID {
			return nil, fmt.Errorf("S5 micro-batch query state has an invalid child binding")
		}
		status := generationapi.JobStatusQueued
		switch child.Status {
		case domain.WorkflowStatusPending:
		case domain.WorkflowStatusRunning, domain.WorkflowStatusWaitingReview:
			status = generationapi.JobStatusRunning
		case domain.WorkflowStatusCompleted:
			status = generationapi.JobStatusSucceeded
		case domain.WorkflowStatusFailed:
			status = generationapi.JobStatusFailed
		default:
			return nil, fmt.Errorf("S5 micro-batch query state has an unsupported child status")
		}
		slots[index] = diversityapi.SlotStatusV1{SlotIndex: index, ChildID: expectedID, Status: status}
	}
	return slots, nil
}

func bindS5StatusHashesV1(
	status *diversityapi.BatchStatusV1,
	corpusRevision string,
	reservationSHA256 string,
	manifestSHA256 string,
) error {
	if status == nil {
		return fmt.Errorf("S5 status projection is unavailable")
	}
	for name, value := range map[string]string{
		"corpus_revision": corpusRevision, "reservation_sha256": reservationSHA256, "manifest_sha256": manifestSHA256,
	} {
		if value != "" && !isCanonicalSHA256(value) {
			return fmt.Errorf("S5 query state has an invalid %s", name)
		}
	}
	status.CorpusRevision = corpusRevision
	status.ReservationSHA256 = reservationSHA256
	status.ManifestSHA256 = manifestSHA256
	return nil
}

// validateS5ParentResultProjectionV1 independently recomputes the frozen
// reservation and binds every ordered observation to the exact ConceptSpec
// that produced its structure embedding. The API never trusts a merely
// shape-correct workflow result.
func validateS5ParentResultProjectionV1(result algoworkflow.S5MicroBatchResultV1) error {
	if result.PayloadVersion != algoworkflow.S5MicroBatchPayloadVersionV1 || !diversityapi.IsBatchID(result.BatchID) ||
		!isCanonicalSHA256(result.CorpusRevision) || !isCanonicalSHA256(result.ReservationSHA256) ||
		!isCanonicalSHA256(result.ManifestSHA256) || len(result.Pools) != diversityapi.SlotCountV1 ||
		len(result.ChildJobIDs) != diversityapi.SlotCountV1 || len(result.Children) != diversityapi.SlotCountV1 ||
		len(result.DedupObservations) != 12 {
		return fmt.Errorf("S5 parent result has an invalid projection shape")
	}
	if result.ManifestArtifact == nil || result.ManifestArtifact.SHA256 != result.ManifestSHA256 ||
		result.ManifestArtifact.WorkflowID != result.BatchID ||
		result.ManifestArtifact.Producer != "StoreS5ProvisionalReservationActivityV1" ||
		result.ManifestArtifact.ContentType != "application/json" ||
		result.ManifestArtifact.Validate(result.ManifestArtifact.Bucket) != nil {
		return fmt.Errorf("S5 parent result has an invalid manifest artifact binding")
	}

	_, canonicalReservationSHA, err := diversity.CanonicalProvisionalReservationV1(result.Reservation)
	if err != nil || canonicalReservationSHA != result.ReservationSHA256 ||
		result.Reservation.BatchID != result.BatchID || result.Reservation.CorpusRevision != result.CorpusRevision {
		return fmt.Errorf("S5 parent result has an invalid reservation binding")
	}
	_, _, recomputedReservationSHA, err := diversity.SelectMicroBatchV1(result.Pools)
	if err != nil || recomputedReservationSHA != result.ReservationSHA256 {
		return fmt.Errorf("S5 parent result reservation does not match its concept pools")
	}

	observationIndex := 0
	dedupBindings := make([]activities.S5ConceptDedupBindingV1, 0, len(result.DedupObservations))
	for slotIndex := 0; slotIndex < diversityapi.SlotCountV1; slotIndex++ {
		pool := result.Pools[slotIndex]
		_, poolSHA, err := diversity.CanonicalConceptPoolV1(pool)
		if err != nil || pool.SlotIndex != slotIndex || pool.BatchID != result.BatchID ||
			pool.CorpusRevision != result.CorpusRevision || result.Reservation.Slots[slotIndex].PoolSHA256 != poolSHA {
			return fmt.Errorf("S5 parent result pool %d is not bound by its reservation", slotIndex)
		}
		expectedChildID, err := diversityapi.ChildJobID(result.BatchID, slotIndex)
		child := result.Children[slotIndex]
		reserved := result.Reservation.Slots[slotIndex]
		if err != nil || result.ChildJobIDs[slotIndex] != expectedChildID || child.SlotIndex != slotIndex ||
			child.JobID != expectedChildID || child.ConceptID != reserved.Winner.ConceptID ||
			child.SlotID != reserved.SlotID ||
			child.Decision != "pass" || strings.TrimSpace(child.StoredProblemID) == "" ||
			child.StoredProblemStatus != domain.ProblemStatusDraft {
			return fmt.Errorf("S5 parent result child %d is not bound by its reservation", slotIndex)
		}
		for attemptIndex, attempt := range pool.Attempts {
			for conceptIndex, card := range attempt.Concepts {
				observation := result.DedupObservations[observationIndex]
				if err := observation.Validate(); err != nil || observation.Stage != diversity.DedupStageConceptSelection ||
					observation.Kind != diversity.DedupKindStructure || observation.CorpusRevision != result.CorpusRevision ||
					(observation.Decision != diversity.DedupDecisionPass && observation.Decision != diversity.DedupDecisionWarn) {
					return fmt.Errorf("S5 parent result observation %d is invalid", observationIndex)
				}
				expectedHash, err := activities.S5ConceptContentHashV1(card.Spec)
				if err != nil || observation.ContentHash != expectedHash {
					return fmt.Errorf("S5 parent result observation %d does not bind its ConceptSpec", observationIndex)
				}
				dedupBindings = append(dedupBindings, activities.S5ConceptDedupBindingV1{
					SlotIndex: slotIndex, AttemptIndex: attemptIndex, ConceptIndex: conceptIndex,
					ConceptID: card.ConceptID, Observation: observation,
				})
				observationIndex++
			}
		}
	}
	if observationIndex != len(result.DedupObservations) {
		return fmt.Errorf("S5 parent result observation order is incomplete")
	}
	manifest := activities.S5ProvisionalReservationManifestV1{
		SchemaVersion: activities.S5ProvisionalReservationManifestSchemaV1,
		BatchID:       result.BatchID, CorpusRevision: result.CorpusRevision,
		ReservationSHA256: result.ReservationSHA256, Reservation: result.Reservation,
		Pools:         append([]diversity.ConceptPoolV1(nil), result.Pools...),
		DedupBindings: dedupBindings, ChildJobIDs: append([]string(nil), result.ChildJobIDs...),
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil || diversity.SHA256Hex(manifestBytes) != result.ManifestSHA256 {
		return fmt.Errorf("S5 parent result does not reproduce its reservation manifest")
	}
	return nil
}

func projectS5MicroBatchPhase(phase string) string {
	switch phase {
	case algoworkflow.S5MicroBatchPhaseCreative, algoworkflow.S5MicroBatchPhaseNormalize:
		return generationapi.JobPhaseGenerating
	case algoworkflow.S5MicroBatchPhaseDedup:
		return generationapi.JobPhaseValidating
	case algoworkflow.S5MicroBatchPhaseReserve:
		return generationapi.JobPhaseStoring
	case algoworkflow.S5MicroBatchPhaseChildren:
		return generationapi.JobPhaseGenerating
	case algoworkflow.S5MicroBatchPhaseComplete, algoworkflow.S5MicroBatchPhaseFailed:
		return generationapi.JobPhaseCompleted
	default:
		return generationapi.JobPhaseQueued
	}
}

func s5MicroBatchLookupError(c echo.Context, err error) error {
	switch {
	case errors.Is(err, errGenerationJobPrincipalRequired):
		return s5MicroBatchHTTPError(c, http.StatusUnauthorized, generationapi.ErrorUnauthorized, errGenerationJobPrincipalRequired.Error())
	case errors.Is(err, errS5MicroBatchNotFound), errors.Is(err, errS5MicroBatchNotOwned):
		return s5MicroBatchHTTPError(c, http.StatusNotFound, generationapi.ErrorNotFound, "S5 micro-batch not found")
	case errors.Is(err, errS5MicroBatchIdempotencyConflict):
		return s5MicroBatchHTTPError(c, http.StatusConflict, generationapi.ErrorIdempotencyConflict, "idempotency key is already bound to a different request")
	default:
		return s5MicroBatchHTTPError(c, http.StatusServiceUnavailable, generationapi.ErrorInternal, "S5 micro-batch state is temporarily unavailable")
	}
}

func s5MicroBatchHTTPError(c echo.Context, status int, code generationapi.ErrorCode, message string) error {
	return generationJobHTTPError(c, status, code, message)
}
