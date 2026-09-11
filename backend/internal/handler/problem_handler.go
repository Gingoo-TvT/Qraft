package handler

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	authmw "github.com/Gingoo-TvT/Qraft/backend/internal/handler/middleware"
	"github.com/Gingoo-TvT/Qraft/backend/internal/llm"
	"github.com/Gingoo-TvT/Qraft/backend/internal/service"
	minioclient "github.com/Gingoo-TvT/Qraft/backend/pkg/minio"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

// ---------------------------------------------------------------------------
// ProblemHandler
// ---------------------------------------------------------------------------

// ProblemHandler exposes HTTP endpoints for problem CRUD, test-case access,
// metadata, editorial, validation, and similarity search.
type ProblemHandler struct {
	problemService      *service.ProblemService
	minioClient         *minioclient.MinIOClient
	runtimeKeys         runtimeKeyStore
	providerConfig      permanentProviderSettings
	modelRoutingEnabled bool
	hydroExport         *service.HydroExportService
	hydroValidate       *service.HydroValidationService
}

// NewProblemHandler creates a new ProblemHandler with the given dependencies.
func NewProblemHandler(ps *service.ProblemService, mc *minioclient.MinIOClient, runtimeStores ...runtimeKeyStore) *ProblemHandler {
	var runtimeKeys runtimeKeyStore
	if len(runtimeStores) > 0 {
		runtimeKeys = runtimeStores[0]
	}
	var hydroExport *service.HydroExportService
	if ps != nil && mc != nil {
		hydroExport = service.NewHydroExportService(ps, mc)
	}
	return &ProblemHandler{
		problemService: ps,
		minioClient:    mc,
		runtimeKeys:    runtimeKeys,
		hydroExport:    hydroExport,
		hydroValidate:  service.NewHydroValidationService(),
	}
}

// SetQG15ExportEnabled keeps legacy Hydro routes live while selecting whether
// their product service uses S3 binding or the pre-QG15 publication gate.
func (h *ProblemHandler) SetQG15ExportEnabled(enabled bool) {
	if h != nil && h.hydroExport != nil {
		h.hydroExport.SetQG15ExportEnabled(enabled)
	}
}

// SetPermanentProviderSettings wires server-side defaults managed through the
// settings page. They are read only when the QG-14 feature flag is enabled;
// request-scoped provider_config values remain available in either mode.
func (h *ProblemHandler) SetPermanentProviderSettings(store permanentProviderSettings, enabled bool) {
	h.providerConfig = store
	h.modelRoutingEnabled = enabled
}

// ---------------------------------------------------------------------------
// HandleGenerate: POST /problems/generate
// ---------------------------------------------------------------------------

// HandleGenerate triggers a problem generation workflow with the given
// parameters. It returns the Temporal workflow ID and run ID for tracking.
func (h *ProblemHandler) HandleGenerate(c echo.Context) error {
	var params domain.ProblemGenParams
	if err := c.Bind(&params); err != nil {
		return badRequest(c, "INVALID_BODY", "failed to parse request body: "+err.Error())
	}
	if err := h.applyPermanentProviderConfig(c.Request().Context(), &params.ProviderConfig); err != nil {
		if isValidationError(err) {
			return badRequest(c, "INVALID_PARAMS", strings.TrimPrefix(err.Error(), "validation: "))
		}
		return internalError(c, "failed to load permanent LLM settings: "+err.Error())
	}
	if err := h.prepareProviderRuntimeConfig(c.Request().Context(), params.ProviderConfig); err != nil {
		return badRequest(c, "INVALID_PARAMS", err.Error())
	}

	run, err := h.problemService.TriggerGeneration(c.Request().Context(), params)
	if err != nil {
		if errors.Is(err, service.ErrNotFound) {
			return notFound(c, err.Error())
		}
		if isValidationError(err) {
			return badRequest(c, "INVALID_PARAMS", strings.TrimPrefix(err.Error(), "validation: "))
		}
		return internalError(c, "failed to trigger generation: "+err.Error())
	}

	return created(c, map[string]string{
		"workflow_id": run.GetID(),
		"run_id":      run.GetRunID(),
	})
}

// ---------------------------------------------------------------------------
// HandleGPLTGenerate: POST /problems/gplt/generate
// ---------------------------------------------------------------------------

// HandleGPLTGenerate triggers a one-click 团体程序设计天梯赛 batch workflow
// that produces 15 problems (8×L1 + 4×L2 + 3×L3). Returns the Temporal
// workflow ID, run ID, and the batch_id embedded into each problem's metadata.
func (h *ProblemHandler) HandleGPLTGenerate(c echo.Context) error {
	var params domain.GPLTBatchParams
	if err := c.Bind(&params); err != nil {
		return badRequest(c, "INVALID_BODY", "failed to parse request body: "+err.Error())
	}
	if err := h.applyPermanentProviderConfig(c.Request().Context(), &params.ProviderConfig); err != nil {
		if isValidationError(err) {
			return badRequest(c, "INVALID_PARAMS", strings.TrimPrefix(err.Error(), "validation: "))
		}
		return internalError(c, "failed to load permanent LLM settings: "+err.Error())
	}
	if err := h.prepareProviderRuntimeConfig(c.Request().Context(), params.ProviderConfig); err != nil {
		return badRequest(c, "INVALID_PARAMS", err.Error())
	}

	// Apply defaults for fields the UI may omit.
	if params.TimeLimit == 0 {
		params.TimeLimit = 1000
	}
	if params.MemoryLimit == 0 {
		params.MemoryLimit = 256
	}
	if len(params.Languages) == 0 {
		params.Languages = []string{"cpp"}
	}
	if params.Locale == "" {
		params.Locale = "zh"
	}
	if params.BatchID == "" {
		params.BatchID = uuid.New().String()
	}

	run, err := h.problemService.TriggerGPLTBatchGeneration(c.Request().Context(), params)
	if err != nil {
		if isValidationError(err) {
			return badRequest(c, "INVALID_PARAMS", strings.TrimPrefix(err.Error(), "validation: "))
		}
		return internalError(c, "failed to trigger GPLT batch generation: "+err.Error())
	}

	return created(c, map[string]string{
		"workflow_id": run.GetID(),
		"run_id":      run.GetRunID(),
		"batch_id":    params.BatchID,
	})
}

// ---------------------------------------------------------------------------
// HandleList: GET /problems
// ---------------------------------------------------------------------------

// HandleList returns a paginated, filtered list of problems. It supports
// query parameters: level, difficulty_min, difficulty_max, tags, status,
// page, size.
func (h *ProblemHandler) HandleList(c echo.Context) error {
	filter := service.ListProblemsFilter{
		Page:     intQueryParam(c, "page", 1),
		PageSize: intQueryParam(c, "size", 20),
	}

	if level := c.QueryParam("level"); level != "" {
		l := domain.ProblemLevel(level)
		filter.Level = &l
	}

	if minDiff := c.QueryParam("difficulty_min"); minDiff != "" {
		if v, err := strconv.Atoi(minDiff); err == nil {
			filter.MinDifficulty = &v
		}
	}

	if maxDiff := c.QueryParam("difficulty_max"); maxDiff != "" {
		if v, err := strconv.Atoi(maxDiff); err == nil {
			filter.MaxDifficulty = &v
		}
	}

	if tags := c.QueryParams()["tags"]; len(tags) > 0 {
		filter.Tags = tags
	}

	if status := c.QueryParam("status"); status != "" {
		filter.Status = &status
	}

	result, err := h.problemService.ListProblems(c.Request().Context(), filter)
	if err != nil {
		return internalError(c, "failed to list problems: "+err.Error())
	}

	return okWithMeta(c, result.Problems, &Meta{
		Total: result.Total,
		Page:  result.Page,
		Size:  result.PageSize,
	})
}

// ---------------------------------------------------------------------------
// HandleGet: GET /problems/:id
// ---------------------------------------------------------------------------

// HandleGet retrieves a single problem by its UUID.
func (h *ProblemHandler) HandleGet(c echo.Context) error {
	id, err := parseUUID(c, "id")
	if err != nil {
		return err
	}

	problem, err := h.problemService.GetProblem(c.Request().Context(), id)
	if err != nil {
		if errors.Is(err, service.ErrNotFound) {
			return notFound(c, "problem not found")
		}
		return internalError(c, "failed to get problem: "+err.Error())
	}

	return ok(c, problem)
}

// ---------------------------------------------------------------------------
// HandleUpdate: PUT /problems/:id
// ---------------------------------------------------------------------------

// HandleUpdate replaces the mutable fields of an existing problem.
func (h *ProblemHandler) HandleUpdate(c echo.Context) error {
	id, err := parseUUID(c, "id")
	if err != nil {
		return err
	}

	input, err := parseProblemEditRequest(c.Request().Body)
	if err != nil {
		return badRequest(c, "INVALID_BODY", "failed to parse request body: "+err.Error())
	}
	if input.Actor == "" {
		input.Actor = editActor(c)
	}

	problem, err := h.problemService.UpdateProblem(c.Request().Context(), id, input)
	if err != nil {
		if errors.Is(err, service.ErrNotFound) {
			return notFound(c, "problem not found")
		}
		if errors.Is(err, service.ErrProtected) {
			return conflict(c, "problem is protected by immutable review evidence")
		}
		if errors.Is(err, service.ErrConflict) {
			return conflict(c, err.Error())
		}
		if isValidationError(err) {
			return badRequest(c, "INVALID_PARAMS", strings.TrimPrefix(err.Error(), "validation: "))
		}
		return internalError(c, "failed to update problem: "+err.Error())
	}

	return ok(c, problem)
}

// HandleCompleteEditRefresh: POST /problems/:id/edit-refresh
//
// Completes the S2.6 edited-problem refresh gate after the caller has produced
// a fresh active statement vector and validation report.
func (h *ProblemHandler) HandleCompleteEditRefresh(c echo.Context) error {
	id, err := parseUUID(c, "id")
	if err != nil {
		return err
	}

	input, err := parseProblemEditRefreshRequest(c.Request().Body)
	if err != nil {
		return badRequest(c, "INVALID_BODY", "failed to parse request body: "+err.Error())
	}
	if input.Actor == "" {
		input.Actor = editActor(c)
	}

	report, err := h.problemService.CompleteProblemEditRefresh(c.Request().Context(), id, input)
	if err != nil {
		if errors.Is(err, service.ErrNotFound) {
			return notFound(c, "problem not found")
		}
		if errors.Is(err, service.ErrConflict) {
			return conflict(c, err.Error())
		}
		if isValidationError(err) {
			return badRequest(c, "INVALID_PARAMS", strings.TrimPrefix(err.Error(), "validation: "))
		}
		return internalError(c, "failed to complete problem edit refresh: "+err.Error())
	}

	return ok(c, report)
}

// HandleApprovePublicRelease: POST /problems/:id/public-release-approval
//
// Records an administrator's explicit release approval and then re-runs the
// normal provenance and quality gate. The approval never bypasses automated
// review quarantine or missing release prerequisites.
func (h *ProblemHandler) HandleApprovePublicRelease(c echo.Context) error {
	id, err := parseUUID(c, "id")
	if err != nil {
		return err
	}

	actor, allowed := publicReleaseApprover(c)
	if !allowed {
		return forbidden(c, "administrator role is required")
	}

	var request struct {
		Approved bool `json:"approved"`
	}
	if err := c.Bind(&request); err != nil {
		return badRequest(c, "INVALID_BODY", "failed to parse request body: "+err.Error())
	}
	if !request.Approved {
		return badRequest(c, "APPROVAL_REQUIRED", "approved must be true")
	}

	report, err := h.problemService.ApprovePublicRelease(
		c.Request().Context(),
		id,
		service.PublicReleaseApprovalInput{Approved: true, Actor: actor},
	)
	if err != nil {
		if errors.Is(err, service.ErrNotFound) {
			return notFound(c, "problem not found")
		}
		if errors.Is(err, service.ErrProtected) {
			return conflict(c, "automated review quarantine cannot be overridden")
		}
		if errors.Is(err, service.ErrConflict) {
			return conflict(c, err.Error())
		}
		if isValidationError(err) {
			return badRequest(c, "INVALID_PARAMS", strings.TrimPrefix(err.Error(), "validation: "))
		}
		return internalError(c, "failed to approve public release: "+err.Error())
	}

	return ok(c, report)
}

// ---------------------------------------------------------------------------
// HandleDelete: DELETE /problems/:id
// ---------------------------------------------------------------------------

// HandleDelete removes a problem and its associated data.
func (h *ProblemHandler) HandleDelete(c echo.Context) error {
	id, err := parseUUID(c, "id")
	if err != nil {
		return err
	}

	if err := h.problemService.DeleteProblem(c.Request().Context(), id); err != nil {
		if errors.Is(err, service.ErrNotFound) {
			return notFound(c, "problem not found")
		}
		if errors.Is(err, service.ErrProtected) {
			return conflict(c, "problem is protected by immutable review evidence")
		}
		return internalError(c, "failed to delete problem: "+err.Error())
	}

	return noContent(c)
}

// ---------------------------------------------------------------------------
// HandleGetTestCases: GET /problems/:id/testcases
// ---------------------------------------------------------------------------

// testCaseResponse is the enriched JSON response for a single test case.
type testCaseResponse struct {
	ID              uuid.UUID `json:"id"`
	ProblemID       uuid.UUID `json:"problem_id"`
	GroupIndex      int       `json:"group_index"`
	CaseIndex       int       `json:"case_index"`
	IsSample        bool      `json:"is_sample"`
	InputPreview    string    `json:"input_preview,omitempty"`
	OutputPreview   string    `json:"output_preview,omitempty"`
	InputSizeBytes  int64     `json:"input_size_bytes"`
	OutputSizeBytes int64     `json:"output_size_bytes"`
	Description     string    `json:"description,omitempty"`
	CreatedAt       string    `json:"created_at"`
}

// HandleGetTestCases returns all test cases for a problem with enriched metadata.
func (h *ProblemHandler) HandleGetTestCases(c echo.Context) error {
	id, err := parseUUID(c, "id")
	if err != nil {
		return err
	}

	testCases, err := h.problemService.GetTestCases(c.Request().Context(), id)
	if err != nil {
		if errors.Is(err, service.ErrNotFound) {
			return notFound(c, "problem not found")
		}
		return internalError(c, "failed to get test cases: "+err.Error())
	}

	ctx := c.Request().Context()
	responses := make([]testCaseResponse, 0, len(testCases))
	for _, tc := range testCases {
		resp := testCaseResponse{
			ID:          tc.ID,
			ProblemID:   tc.ProblemID,
			GroupIndex:  tc.GroupID,
			CaseIndex:   tc.TestIndex,
			IsSample:    tc.IsSample,
			Description: tc.Description,
			CreatedAt:   tc.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		}

		// Get file sizes from MinIO.
		if tc.InputPath != "" {
			if size, err := h.minioClient.StatFile(ctx, tc.InputPath); err == nil {
				resp.InputSizeBytes = size
			}
		}
		if tc.OutputPath != "" {
			if size, err := h.minioClient.StatFile(ctx, tc.OutputPath); err == nil {
				resp.OutputSizeBytes = size
			}
		}

		// For sample cases, include a preview (first 200 bytes).
		if tc.IsSample {
			if data, err := h.minioClient.DownloadFile(ctx, tc.InputPath); err == nil {
				resp.InputPreview = truncateBytes(data, 200)
			}
			if data, err := h.minioClient.DownloadFile(ctx, tc.OutputPath); err == nil {
				resp.OutputPreview = truncateBytes(data, 200)
			}
		}

		responses = append(responses, resp)
	}

	return ok(c, responses)
}

// truncateBytes converts bytes to string, truncating to maxLen with "..." suffix.
func truncateBytes(data []byte, maxLen int) string {
	if len(data) <= maxLen {
		return string(data)
	}
	return string(data[:maxLen]) + "..."
}

// ---------------------------------------------------------------------------
// HandleDownloadAllTestData: GET /problems/:id/testdata.zip
// ---------------------------------------------------------------------------

// HandleDownloadAllTestData packages all test inputs and outputs into a zip.
func (h *ProblemHandler) HandleDownloadAllTestData(c echo.Context) error {
	id, err := parseUUID(c, "id")
	if err != nil {
		return err
	}

	testCases, err := h.problemService.GetTestCases(c.Request().Context(), id)
	if err != nil {
		if errors.Is(err, service.ErrNotFound) {
			return notFound(c, "problem not found")
		}
		return internalError(c, "failed to get test cases: "+err.Error())
	}

	ctx := c.Request().Context()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	for i, tc := range testCases {
		// Input file.
		if tc.InputPath != "" {
			data, err := h.minioClient.DownloadFile(ctx, tc.InputPath)
			if err != nil {
				return internalError(c, fmt.Sprintf("downloading input %d: %v", i, err))
			}
			fw, err := zw.Create(fmt.Sprintf("%03d.in", i+1))
			if err != nil {
				return internalError(c, fmt.Sprintf("creating zip entry: %v", err))
			}
			fw.Write(data)
		}
		// Output file.
		if tc.OutputPath != "" {
			data, err := h.minioClient.DownloadFile(ctx, tc.OutputPath)
			if err != nil {
				return internalError(c, fmt.Sprintf("downloading output %d: %v", i, err))
			}
			fw, err := zw.Create(fmt.Sprintf("%03d.out", i+1))
			if err != nil {
				return internalError(c, fmt.Sprintf("creating zip entry: %v", err))
			}
			fw.Write(data)
		}
	}

	if err := zw.Close(); err != nil {
		return internalError(c, "finalizing zip: "+err.Error())
	}

	c.Response().Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-testdata.zip"`, id.String()[:8]))
	return c.Blob(200, "application/zip", buf.Bytes())
}

// ---------------------------------------------------------------------------
// HandleDownloadHydroPackage: GET /problems/:id/hydro.zip
// ---------------------------------------------------------------------------

// HandleDownloadHydroPackage exports a single problem as a Hydro-compatible
// native ZIP package: problem.yaml, problem_zh.md, testdata/config.yaml,
// testdata files, and an AlgoForge manifest for downstream audit tooling.
func (h *ProblemHandler) HandleDownloadHydroPackage(c echo.Context) error {
	id, err := parseUUID(c, "id")
	if err != nil {
		return err
	}
	if h.hydroExport == nil {
		return internalError(c, "hydro export service is unavailable")
	}

	pkg, err := h.hydroExport.BuildProblemPackage(c.Request().Context(), id)
	if err != nil {
		return handleHydroExportError(c, err)
	}
	c.Response().Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, pkg.FileName))
	return c.Blob(200, "application/zip", pkg.Content)
}

// ---------------------------------------------------------------------------
// HandleDownloadHydroBatch: GET /problems/hydro.zip?ids=:id
// ---------------------------------------------------------------------------

// HandleDownloadHydroBatch exports multiple problems as one Hydro batch ZIP.
// The root contains problem directories directly, never nested ZIP files.
func (h *ProblemHandler) HandleDownloadHydroBatch(c echo.Context) error {
	if h.hydroExport == nil {
		return internalError(c, "hydro export service is unavailable")
	}
	ids, err := parseHydroBatchIDs(c)
	if err != nil {
		return err
	}

	pkg, err := h.hydroExport.BuildBatchPackage(c.Request().Context(), ids)
	if err != nil {
		return handleHydroExportError(c, err)
	}
	c.Response().Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, pkg.FileName))
	return c.Blob(200, "application/zip", pkg.Content)
}

func parseHydroBatchIDs(c echo.Context) ([]uuid.UUID, error) {
	values := c.QueryParams()["ids"]
	if len(values) == 0 {
		values = c.QueryParams()["id"]
	}
	ids := make([]uuid.UUID, 0, len(values))
	seen := make(map[uuid.UUID]bool, len(values))
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			id, err := uuid.Parse(part)
			if err != nil {
				return nil, badRequest(c, "INVALID_PARAMS", "ids must contain UUID values")
			}
			if seen[id] {
				continue
			}
			seen[id] = true
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil, badRequest(c, "INVALID_PARAMS", "ids query parameter is required")
	}
	if len(ids) > 100 {
		return nil, badRequest(c, "INVALID_PARAMS", "hydro batch export supports at most 100 problems")
	}
	return ids, nil
}

func handleHydroExportError(c echo.Context, err error) error {
	if errors.Is(err, service.ErrNotFound) {
		return notFound(c, "problem not found")
	}
	if errors.Is(err, service.ErrConflict) {
		return conflict(c, err.Error())
	}
	if isValidationError(err) {
		return badRequest(c, "INVALID_PARAMS", strings.TrimPrefix(err.Error(), "validation: "))
	}
	return internalError(c, "failed to build hydro package: "+err.Error())
}

// ---------------------------------------------------------------------------
// HandleValidateHydroPackage: POST /problems/hydro/validate
// ---------------------------------------------------------------------------

// HandleValidateHydroPackage validates a Hydro single-problem or batch ZIP
// against AlgoForge's current Hydro phase-1 support without importing it.
func (h *ProblemHandler) HandleValidateHydroPackage(c echo.Context) error {
	fileHeader, err := c.FormFile("file")
	if err != nil {
		return badRequest(c, "MISSING_FILE", "multipart field file is required")
	}
	file, err := fileHeader.Open()
	if err != nil {
		return badRequest(c, "INVALID_FILE", "failed to open uploaded file: "+err.Error())
	}
	defer file.Close()

	if h.hydroValidate == nil {
		h.hydroValidate = service.NewHydroValidationService()
	}
	report, err := h.hydroValidate.ValidatePackage(c.Request().Context(), file)
	if err != nil {
		return badRequest(c, "HYDRO_VALIDATE_FAILED", err.Error())
	}
	return ok(c, report)
}

// ---------------------------------------------------------------------------
// HandleGetTestCaseInput: GET /problems/:id/testcases/:tid/input
// ---------------------------------------------------------------------------

// HandleGetTestCaseInput downloads the input file for a specific test case.
func (h *ProblemHandler) HandleGetTestCaseInput(c echo.Context) error {
	_, err := parseUUID(c, "id")
	if err != nil {
		return err
	}

	tid, err := parseUUID(c, "tid")
	if err != nil {
		return err
	}

	tc, err := h.problemService.GetTestCase(c.Request().Context(), tid)
	if err != nil {
		if errors.Is(err, service.ErrNotFound) {
			return notFound(c, "test case not found")
		}
		return internalError(c, "failed to get test case: "+err.Error())
	}

	data, err := h.minioClient.DownloadFile(c.Request().Context(), tc.InputPath)
	if err != nil {
		return internalError(c, "failed to download test input: "+err.Error())
	}

	return c.Blob(200, "application/octet-stream", data)
}

// ---------------------------------------------------------------------------
// HandleGetTestCaseOutput: GET /problems/:id/testcases/:tid/output
// ---------------------------------------------------------------------------

// HandleGetTestCaseOutput downloads the expected output file for a specific
// test case.
func (h *ProblemHandler) HandleGetTestCaseOutput(c echo.Context) error {
	_, err := parseUUID(c, "id")
	if err != nil {
		return err
	}

	tid, err := parseUUID(c, "tid")
	if err != nil {
		return err
	}

	tc, err := h.problemService.GetTestCase(c.Request().Context(), tid)
	if err != nil {
		if errors.Is(err, service.ErrNotFound) {
			return notFound(c, "test case not found")
		}
		return internalError(c, "failed to get test case: "+err.Error())
	}

	data, err := h.minioClient.DownloadFile(c.Request().Context(), tc.OutputPath)
	if err != nil {
		return internalError(c, "failed to download test output: "+err.Error())
	}

	return c.Blob(200, "application/octet-stream", data)
}

// ---------------------------------------------------------------------------
// HandleGetMetadata: GET /problems/:id/metadata
// ---------------------------------------------------------------------------

// HandleGetMetadata returns the lightweight metadata summary for a problem.
func (h *ProblemHandler) HandleGetMetadata(c echo.Context) error {
	id, err := parseUUID(c, "id")
	if err != nil {
		return err
	}

	meta, err := h.problemService.GetMetadata(c.Request().Context(), id)
	if err != nil {
		if errors.Is(err, service.ErrNotFound) {
			return notFound(c, "problem not found")
		}
		return internalError(c, "failed to get metadata: "+err.Error())
	}

	return ok(c, meta)
}

// ---------------------------------------------------------------------------
// HandleGetEditorial: GET /problems/:id/editorial
// ---------------------------------------------------------------------------

// HandleGetEditorial returns the detailed editorial text for a problem.
func (h *ProblemHandler) HandleGetEditorial(c echo.Context) error {
	id, err := parseUUID(c, "id")
	if err != nil {
		return err
	}

	editorial, err := h.problemService.GetEditorial(c.Request().Context(), id)
	if err != nil {
		if errors.Is(err, service.ErrNotFound) {
			return notFound(c, "problem not found")
		}
		return internalError(c, "failed to get editorial: "+err.Error())
	}

	return ok(c, map[string]string{"editorial": editorial})
}

// ---------------------------------------------------------------------------
// HandleGetSolutions: GET /problems/:id/solutions
// ---------------------------------------------------------------------------

// HandleGetSolutions returns the executable source artefacts linked to a
// problem. The detail page uses the main solution from this endpoint; the
// editorial Markdown remains available through HandleGetEditorial.
func (h *ProblemHandler) HandleGetSolutions(c echo.Context) error {
	id, err := parseUUID(c, "id")
	if err != nil {
		return err
	}

	solutions, err := h.problemService.GetSolutions(c.Request().Context(), id)
	if err != nil {
		if errors.Is(err, service.ErrNotFound) {
			return notFound(c, "problem not found")
		}
		return internalError(c, "failed to get solutions: "+err.Error())
	}

	return ok(c, solutions)
}

// ---------------------------------------------------------------------------
// HandleValidate: POST /problems/:id/validate
// ---------------------------------------------------------------------------

// HandleValidate triggers a validation workflow for an existing problem,
// re-running sandbox execution and output comparison.
func (h *ProblemHandler) HandleValidate(c echo.Context) error {
	id, err := parseUUID(c, "id")
	if err != nil {
		return err
	}

	run, err := h.problemService.ValidateProblem(c.Request().Context(), id)
	if err != nil {
		if errors.Is(err, service.ErrNotFound) {
			return notFound(c, "problem not found")
		}
		return internalError(c, "failed to trigger validation: "+err.Error())
	}

	return ok(c, map[string]string{
		"workflow_id": run.GetID(),
		"run_id":      run.GetRunID(),
	})
}

// ---------------------------------------------------------------------------
// HandleFindSimilar: GET /problems/similar
// ---------------------------------------------------------------------------

// HandleFindSimilar searches for problems similar to the one identified by
// the ?problem_id= query parameter.
func (h *ProblemHandler) HandleFindSimilar(c echo.Context) error {
	problemIDStr := c.QueryParam("problem_id")
	if problemIDStr == "" {
		return badRequest(c, "MISSING_PARAM", "problem_id query parameter is required")
	}

	problemID, err := uuid.Parse(problemIDStr)
	if err != nil {
		return badRequest(c, "INVALID_PARAM", "problem_id must be a valid UUID")
	}

	limit := intQueryParam(c, "limit", 10)

	problems, scores, err := h.problemService.FindSimilarByProblemID(c.Request().Context(), problemID, limit)
	if err != nil {
		if errors.Is(err, service.ErrNotFound) {
			return notFound(c, "problem not found")
		}
		return internalError(c, "failed to find similar problems: "+err.Error())
	}

	type similarResult struct {
		Problem    *domain.Problem `json:"problem"`
		Similarity float64         `json:"similarity"`
	}

	results := make([]similarResult, len(problems))
	for i := range problems {
		results[i] = similarResult{
			Problem:    problems[i],
			Similarity: scores[i],
		}
	}

	return ok(c, results)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// parseUUID extracts and parses a UUID path parameter, returning a badRequest
// error response if the value is not a valid UUID.
func parseUUID(c echo.Context, param string) (uuid.UUID, error) {
	raw := c.Param(param)
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, badRequest(c, "INVALID_PARAM", param+" must be a valid UUID")
	}
	return id, nil
}

// intQueryParam reads an integer query parameter with a default value.
func intQueryParam(c echo.Context, key string, defaultVal int) int {
	raw := c.QueryParam(key)
	if raw == "" {
		return defaultVal
	}
	val, err := strconv.Atoi(raw)
	if err != nil {
		return defaultVal
	}
	return val
}

type problemEditRequest struct {
	ExpectedUpdatedAt *time.Time           `json:"expected_updated_at"`
	Title             *string              `json:"title,omitempty"`
	Statement         *string              `json:"statement,omitempty"`
	Level             *domain.ProblemLevel `json:"level,omitempty"`
	Difficulty        *int                 `json:"difficulty,omitempty"`
	OneLineHint       *string              `json:"one_line_hint,omitempty"`
	DetailedSolution  *string              `json:"detailed_solution,omitempty"`
	Tags              *[]string            `json:"tags,omitempty"`
	TimeLimit         *int                 `json:"time_limit,omitempty"`
	MemoryLimit       *int                 `json:"memory_limit,omitempty"`
	MetadataJSON      *json.RawMessage     `json:"metadata_json,omitempty"`
}

type problemEditRefreshRequest struct {
	OperationKey           string `json:"operation_key,omitempty"`
	ValidationReportSHA256 string `json:"validation_report_sha256"`
	Actor                  string `json:"actor,omitempty"`
}

func parseProblemEditRequest(reader io.Reader) (service.ProblemUpdateInput, error) {
	body, err := io.ReadAll(reader)
	if err != nil {
		return service.ProblemUpdateInput{}, err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return service.ProblemUpdateInput{}, err
	}
	allowed := map[string]bool{
		"expected_updated_at": true,
		"title":               true,
		"statement":           true,
		"level":               true,
		"difficulty":          true,
		"one_line_hint":       true,
		"detailed_solution":   true,
		"tags":                true,
		"time_limit":          true,
		"memory_limit":        true,
		"metadata_json":       true,
	}
	for key := range raw {
		if !allowed[key] {
			return service.ProblemUpdateInput{}, fmt.Errorf("field %q is not editable", key)
		}
	}

	var request problemEditRequest
	if err := json.Unmarshal(body, &request); err != nil {
		return service.ProblemUpdateInput{}, err
	}
	if request.ExpectedUpdatedAt == nil {
		return service.ProblemUpdateInput{}, fmt.Errorf("expected_updated_at is required")
	}
	return service.ProblemUpdateInput{
		ExpectedUpdatedAt: *request.ExpectedUpdatedAt,
		Title:             request.Title,
		Statement:         request.Statement,
		Level:             request.Level,
		Difficulty:        request.Difficulty,
		OneLineHint:       request.OneLineHint,
		DetailedSolution:  request.DetailedSolution,
		Tags:              request.Tags,
		TimeLimit:         request.TimeLimit,
		MemoryLimit:       request.MemoryLimit,
		MetadataJSON:      request.MetadataJSON,
	}, nil
}

func parseProblemEditRefreshRequest(reader io.Reader) (service.ProblemEditRefreshInput, error) {
	body, err := io.ReadAll(reader)
	if err != nil {
		return service.ProblemEditRefreshInput{}, err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return service.ProblemEditRefreshInput{}, err
	}
	allowed := map[string]bool{
		"operation_key":            true,
		"validation_report_sha256": true,
		"actor":                    true,
	}
	for key := range raw {
		if !allowed[key] {
			return service.ProblemEditRefreshInput{}, fmt.Errorf("field %q is not accepted", key)
		}
	}

	var request problemEditRefreshRequest
	if err := json.Unmarshal(body, &request); err != nil {
		return service.ProblemEditRefreshInput{}, err
	}
	if strings.TrimSpace(request.ValidationReportSHA256) == "" {
		return service.ProblemEditRefreshInput{}, fmt.Errorf("validation_report_sha256 is required")
	}
	return service.ProblemEditRefreshInput{
		OperationKey:           request.OperationKey,
		ValidationReportSHA256: request.ValidationReportSHA256,
		Actor:                  request.Actor,
	}, nil
}

func editActor(c echo.Context) string {
	if actor := strings.TrimSpace(c.Request().Header.Get("X-Actor")); actor != "" {
		return actor
	}
	return "api"
}

func publicReleaseApprover(c echo.Context) (string, bool) {
	claims := authmw.GetClaims(c)
	if claims == nil {
		// Authentication middleware only omits claims in explicit local dev mode.
		return "local-admin", true
	}
	if !strings.EqualFold(strings.TrimSpace(claims.Role), "admin") {
		return "", false
	}
	if email := strings.TrimSpace(claims.Email); email != "" {
		return email, true
	}
	if userID := strings.TrimSpace(claims.UserID); userID != "" {
		return userID, true
	}
	return "authenticated-admin", true
}

func isValidationError(err error) bool {
	return err != nil && strings.HasPrefix(err.Error(), "validation: ")
}

type runtimeKeyStore interface {
	Put(ctx context.Context, apiKey string) (string, error)
	PutWithTTL(ctx context.Context, apiKey string, ttl time.Duration) (string, error)
}

type permanentProviderSettings interface {
	EffectiveRuntimeConfig(ctx context.Context, purpose string) (*domain.LLMRuntimeConfig, error)
}

type permanentProviderSavedSettings interface {
	SavedRuntimeConfig(ctx context.Context, purpose string) (*domain.LLMRuntimeConfig, bool, error)
}

func (h *ProblemHandler) applyPermanentProviderConfig(
	ctx context.Context,
	configRef **domain.ProviderRuntimeConfig,
) error {
	if h == nil || h.providerConfig == nil || !h.modelRoutingEnabled {
		return nil
	}
	statement, err := h.providerConfig.EffectiveRuntimeConfig(ctx, "statement")
	if err != nil {
		return fmt.Errorf("statement settings: %w", err)
	}
	verification, verificationSaved, err := h.savedPermanentProviderConfig(ctx, "verification")
	if err != nil {
		return fmt.Errorf("verification settings: %w", err)
	}
	review, reviewSaved, err := h.savedPermanentProviderConfig(ctx, "review")
	if err != nil {
		return fmt.Errorf("review settings: %w", err)
	}
	var statementOverride, verificationOverride, reviewOverride *domain.LLMRuntimeConfig
	if *configRef != nil {
		statementOverride = (*configRef).Statement
		verificationOverride = (*configRef).Verification
		reviewOverride = (*configRef).Review
	}
	if err := validateLLMTransportOverride("provider_config.statement", statement, statementOverride); err != nil {
		return err
	}
	mergedStatement := mergeLLMRuntimeConfig(statement, statementOverride)
	if !verificationSaved {
		verification = mergeLLMRuntimeConfig(mergedStatement, nil)
	}
	if err := validateLLMTransportOverride("provider_config.verification", verification, verificationOverride); err != nil {
		return err
	}
	mergedVerification := mergeLLMRuntimeConfig(verification, verificationOverride)
	if !reviewSaved {
		review = mergeLLMRuntimeConfig(mergedVerification, nil)
	}
	if err := validateLLMTransportOverride("provider_config.review", review, reviewOverride); err != nil {
		return err
	}
	*configRef = &domain.ProviderRuntimeConfig{
		Statement:    mergedStatement,
		Verification: mergedVerification,
		Review:       mergeLLMRuntimeConfig(review, reviewOverride),
	}
	return nil
}

func (h *ProblemHandler) savedPermanentProviderConfig(
	ctx context.Context,
	purpose string,
) (*domain.LLMRuntimeConfig, bool, error) {
	if settings, ok := h.providerConfig.(permanentProviderSavedSettings); ok {
		return settings.SavedRuntimeConfig(ctx, purpose)
	}
	// Compatibility for older injected implementations: their effective value
	// remains an explicit default because they cannot report saved state.
	config, err := h.providerConfig.EffectiveRuntimeConfig(ctx, purpose)
	return config, config != nil, err
}

func mergeLLMRuntimeConfig(
	defaults *domain.LLMRuntimeConfig,
	override *domain.LLMRuntimeConfig,
) *domain.LLMRuntimeConfig {
	if defaults == nil && override == nil {
		return nil
	}
	merged := &domain.LLMRuntimeConfig{}
	if defaults != nil {
		*merged = *defaults
	}
	if override == nil {
		return merged
	}
	if strings.TrimSpace(override.Model) != "" {
		merged.Model = override.Model
	}
	if strings.TrimSpace(override.BaseURL) != "" {
		merged.BaseURL = override.BaseURL
	}
	if strings.TrimSpace(override.Provider) != "" {
		merged.Provider = override.Provider
	}
	if strings.TrimSpace(override.Protocol) != "" {
		merged.Protocol = override.Protocol
	}
	if strings.TrimSpace(override.ReasoningEffort) != "" {
		merged.ReasoningEffort = override.ReasoningEffort
	}
	if strings.TrimSpace(override.APIKeyRef) != "" || strings.TrimSpace(override.APIKey) != "" {
		merged.APIKeyRef = override.APIKeyRef
		merged.APIKey = override.APIKey
	}
	return merged
}

func validateLLMTransportOverride(
	path string,
	defaults *domain.LLMRuntimeConfig,
	override *domain.LLMRuntimeConfig,
) error {
	if override == nil || !llmTransportIdentityChanged(defaults, override) {
		return nil
	}
	if strings.TrimSpace(override.APIKey) != "" || strings.TrimSpace(override.APIKeyRef) != "" {
		return nil
	}
	return fmt.Errorf("validation: %s changes base_url, provider, or protocol and must supply api_key or api_key_ref", path)
}

func llmTransportIdentityChanged(defaults, override *domain.LLMRuntimeConfig) bool {
	if override == nil {
		return false
	}
	defaultBaseURL, defaultProvider, defaultProtocol := "", "", ""
	if defaults != nil {
		defaultBaseURL = canonicalLLMTransportBaseURL(defaults.BaseURL)
		defaultProvider = strings.TrimSpace(defaults.Provider)
		defaultProtocol = canonicalLLMTransportProtocol(defaults.Protocol)
	}
	if strings.TrimSpace(override.BaseURL) != "" && canonicalLLMTransportBaseURL(override.BaseURL) != defaultBaseURL {
		return true
	}
	if strings.TrimSpace(override.Provider) != "" && strings.TrimSpace(override.Provider) != defaultProvider {
		return true
	}
	if strings.TrimSpace(override.Protocol) != "" && canonicalLLMTransportProtocol(override.Protocol) != defaultProtocol {
		return true
	}
	return false
}

func canonicalLLMTransportBaseURL(value string) string {
	if normalized, err := normalizeLLMSettingsBaseURL(value); err == nil {
		return normalized
	}
	return strings.TrimRight(strings.TrimSpace(value), "/")
}

func canonicalLLMTransportProtocol(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" || value == "auto" {
		return value
	}
	protocol, err := llm.ResolveProtocol(&llm.RuntimeConfig{Protocol: value}, "")
	if err != nil {
		return value
	}
	return string(protocol)
}

func (h *ProblemHandler) prepareProviderRuntimeConfig(ctx context.Context, cfg *domain.ProviderRuntimeConfig) error {
	return h.prepareProviderRuntimeConfigWithTTL(ctx, cfg, 0)
}

func (h *ProblemHandler) prepareProviderRuntimeConfigWithTTL(
	ctx context.Context,
	cfg *domain.ProviderRuntimeConfig,
	ttl time.Duration,
) error {
	if cfg == nil {
		return nil
	}
	if err := h.prepareLLMRuntimeConfig(ctx, "provider_config.statement", cfg.Statement, ttl); err != nil {
		return err
	}
	if err := h.prepareLLMRuntimeConfig(ctx, "provider_config.verification", cfg.Verification, ttl); err != nil {
		return err
	}
	if err := h.prepareLLMRuntimeConfig(ctx, "provider_config.review", cfg.Review, ttl); err != nil {
		return err
	}
	return nil
}

func (h *ProblemHandler) prepareLLMRuntimeConfig(ctx context.Context, path string, cfg *domain.LLMRuntimeConfig, ttl time.Duration) error {
	if cfg == nil {
		return nil
	}
	rawKey := strings.TrimSpace(cfg.APIKey)
	if rawKey == "" {
		return nil
	}
	if strings.TrimSpace(cfg.APIKeyRef) != "" {
		return fmt.Errorf("%s.api_key cannot be combined with api_key_ref", path)
	}
	if h.runtimeKeys == nil {
		return fmt.Errorf("%s.api_key requires the API runtime key store to be configured", path)
	}
	var ref string
	var err error
	if ttl > 0 {
		ref, err = h.runtimeKeys.PutWithTTL(ctx, rawKey, ttl)
	} else {
		ref, err = h.runtimeKeys.Put(ctx, rawKey)
	}
	if err != nil {
		return fmt.Errorf("%s.api_key could not be prepared: %w", path, err)
	}
	cfg.APIKey = ""
	cfg.APIKeyRef = ref
	return nil
}
