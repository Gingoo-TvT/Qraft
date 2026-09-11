package handler

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
	"github.com/Gingoo-TvT/Qraft/backend/internal/service"
	"github.com/labstack/echo/v4"
)

// ProblemSetHandler exposes contest-set authoring, diversity quality reports,
// LLM brief generation, and the portable JSON/Hydro ZIP download.
type ProblemSetHandler struct {
	svc        *service.ProblemSetService
	generation problemSetGenerationController
}

func NewProblemSetHandler(svc *service.ProblemSetService) *ProblemSetHandler {
	return &ProblemSetHandler{svc: svc}
}

func (h *ProblemSetHandler) HandleCreate(c echo.Context) error {
	var req service.ProblemSetCreateRequest
	if err := c.Bind(&req); err != nil {
		return badRequest(c, "INVALID_BODY", "failed to parse request body: "+err.Error())
	}
	if req.StartGeneration {
		if h.generation == nil {
			return serviceUnavailable(c, "题集自动生成未启用")
		}
		req.GeneratePrompt = false
	}
	if req.CreatedBy == "" {
		req.CreatedBy = editActor(c)
	}
	set, err := h.svc.Create(c.Request().Context(), req)
	if err != nil {
		return handleProblemSetError(c, err, "failed to create problem set")
	}
	if req.StartGeneration {
		state, startErr := h.generation.Start(c.Request().Context(), set.ID)
		if startErr != nil {
			set.GenerationError = startErr.Error()
		} else {
			set.Generation = state
		}
	}
	return created(c, set)
}

func (h *ProblemSetHandler) HandleList(c echo.Context) error {
	filter := repository.ProblemSetListFilter{Page: intQueryParam(c, "page", 1), Size: intQueryParam(c, "size", 20), Search: c.QueryParam("search")}
	if raw := strings.TrimSpace(c.QueryParam("kind")); raw != "" {
		kind := domain.ProblemSetKind(raw)
		if !kind.IsValid() {
			return badRequest(c, "INVALID_PARAMS", "kind is invalid")
		}
		filter.Kind = &kind
	}
	if raw := strings.TrimSpace(c.QueryParam("status")); raw != "" {
		status := domain.ProblemSetStatus(raw)
		if !status.IsValid() {
			return badRequest(c, "INVALID_PARAMS", "status is invalid")
		}
		filter.Status = &status
	}
	sets, total, err := h.svc.List(c.Request().Context(), filter)
	if err != nil {
		return internalError(c, "failed to list problem sets: "+err.Error())
	}
	return okWithMeta(c, sets, &Meta{Total: total, Page: filter.Page, Size: filter.Size})
}

func (h *ProblemSetHandler) HandleGet(c echo.Context) error {
	id, err := parseUUID(c, "id")
	if err != nil {
		return err
	}
	set, err := h.svc.Get(c.Request().Context(), id)
	if err != nil {
		return handleProblemSetError(c, err, "failed to get problem set")
	}
	return ok(c, set)
}

func (h *ProblemSetHandler) HandleUpdate(c echo.Context) error {
	id, err := parseUUID(c, "id")
	if err != nil {
		return err
	}
	var req service.ProblemSetUpdateRequest
	if err := c.Bind(&req); err != nil {
		return badRequest(c, "INVALID_BODY", "failed to parse request body: "+err.Error())
	}
	set, err := h.svc.Update(c.Request().Context(), id, req)
	if err != nil {
		return handleProblemSetError(c, err, "failed to update problem set")
	}
	return ok(c, set)
}

func (h *ProblemSetHandler) HandleDelete(c echo.Context) error {
	id, err := parseUUID(c, "id")
	if err != nil {
		return err
	}
	if err := h.svc.Delete(c.Request().Context(), id); err != nil {
		return handleProblemSetError(c, err, "failed to delete problem set")
	}
	return noContent(c)
}

func (h *ProblemSetHandler) HandleAddItem(c echo.Context) error {
	setID, err := parseUUID(c, "id")
	if err != nil {
		return err
	}
	var req service.ProblemSetAddItemRequest
	if err := c.Bind(&req); err != nil {
		return badRequest(c, "INVALID_BODY", "failed to parse request body: "+err.Error())
	}
	set, err := h.svc.AddItem(c.Request().Context(), setID, req)
	if err != nil {
		return handleProblemSetError(c, err, "failed to add problem-set item")
	}
	return created(c, set)
}

func (h *ProblemSetHandler) HandleRemoveItem(c echo.Context) error {
	setID, err := parseUUID(c, "id")
	if err != nil {
		return err
	}
	itemID, err := parseUUID(c, "item_id")
	if err != nil {
		return err
	}
	set, err := h.svc.RemoveItem(c.Request().Context(), setID, itemID)
	if err != nil {
		return handleProblemSetError(c, err, "failed to remove problem-set item")
	}
	return ok(c, set)
}

func (h *ProblemSetHandler) HandleReorder(c echo.Context) error {
	setID, err := parseUUID(c, "id")
	if err != nil {
		return err
	}
	var req service.ProblemSetReorderRequest
	if err := c.Bind(&req); err != nil {
		return badRequest(c, "INVALID_BODY", "failed to parse request body: "+err.Error())
	}
	set, err := h.svc.Reorder(c.Request().Context(), setID, req.ItemIDs)
	if err != nil {
		return handleProblemSetError(c, err, "failed to reorder problem-set items")
	}
	return ok(c, set)
}

func (h *ProblemSetHandler) HandleQuality(c echo.Context) error {
	id, err := parseUUID(c, "id")
	if err != nil {
		return err
	}
	quality, err := h.svc.Quality(c.Request().Context(), id)
	if err != nil {
		return handleProblemSetError(c, err, "failed to assess problem-set quality")
	}
	return ok(c, quality)
}

func (h *ProblemSetHandler) HandleGeneratePrompt(c echo.Context) error {
	id, err := parseUUID(c, "id")
	if err != nil {
		return err
	}
	set, err := h.svc.GeneratePrompt(c.Request().Context(), id)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "not configured") || strings.Contains(strings.ToLower(err.Error()), "LLM") {
			return serviceUnavailable(c, err.Error())
		}
		return handleProblemSetError(c, err, "failed to generate problem-set prompt")
	}
	return ok(c, set)
}

func (h *ProblemSetHandler) HandleExport(c echo.Context) error {
	id, err := parseUUID(c, "id")
	if err != nil {
		return err
	}
	allowReuse, err := parseOptionalBool(c.QueryParam("allow_reuse"))
	if err != nil {
		return badRequest(c, "INVALID_PARAMS", "allow_reuse must be true or false")
	}
	result, err := h.svc.Export(c.Request().Context(), id, allowReuse)
	if err != nil {
		return handleProblemSetError(c, err, "failed to build problem-set package")
	}
	if result == nil || result.Package == nil {
		return internalError(c, "problem-set export returned no package")
	}
	c.Response().Header().Set(echo.HeaderContentDisposition, fmt.Sprintf(`attachment; filename="%s"`, result.Package.FileName))
	c.Response().Header().Set("X-AlgoForge-Export-Profile", result.Package.Profile)
	c.Response().Header().Set("X-AlgoForge-Problem-Set-Quality", qualityHeader(result.Quality))
	return c.Blob(http.StatusOK, "application/zip", result.Package.Content)
}

// RegisterProblemSetRoutes keeps CRUD available in all deployment modes. The
// export handler fails closed with a clear service-unavailable response when
// a required programming asset is unavailable or fails verification.
func RegisterProblemSetRoutes(group *echo.Group, handler *ProblemSetHandler) {
	if handler.generation != nil {
		group.POST("/problem-sets/:id/generation", handler.HandleStartGeneration)
		group.GET("/problem-sets/:id/generation", handler.HandleGenerationStatus)
		group.POST("/problem-sets/:id/generation/cancel", handler.HandleCancelGeneration)
	}
	group.POST("/problem-sets/assembly-preview", handler.HandleAssemblyPreview)
	group.POST("/problem-sets/assemble", handler.HandleAssemble)
	group.POST("/problem-sets", handler.HandleCreate)
	group.GET("/problem-sets", handler.HandleList)
	group.POST("/problem-sets/:id/items", handler.HandleAddItem)
	group.PUT("/problem-sets/:id/items/reorder", handler.HandleReorder)
	group.DELETE("/problem-sets/:id/items/:item_id", handler.HandleRemoveItem)
	group.GET("/problem-sets/:id/quality", handler.HandleQuality)
	group.POST("/problem-sets/:id/generate-prompt", handler.HandleGeneratePrompt)
	group.GET("/problem-sets/:id/export.zip", handler.HandleExport)
	group.GET("/problem-sets/:id", handler.HandleGet)
	group.PUT("/problem-sets/:id", handler.HandleUpdate)
	group.DELETE("/problem-sets/:id", handler.HandleDelete)
}

func parseOptionalBool(raw string) (bool, error) {
	if strings.TrimSpace(raw) == "" {
		return false, nil
	}
	return strconv.ParseBool(raw)
}

func qualityHeader(quality *domain.ProblemSetQuality) string {
	if quality == nil {
		return "unknown"
	}
	if quality.ReadyForExport {
		return "ready"
	}
	return "review"
}

func handleProblemSetError(c echo.Context, err error, prefix string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, service.ErrNotFound) || errors.Is(err, sql.ErrNoRows) {
		return notFound(c, "problem set not found")
	}
	if errors.Is(err, service.ErrConflict) || errors.Is(err, repository.ErrProblemSetAssemblyChanged) || errors.Is(err, repository.ErrProblemSetGenerationActive) || errors.Is(err, repository.ErrProblemSetGenerationChanged) {
		return conflict(c, err.Error())
	}
	if isValidationError(err) || strings.HasPrefix(err.Error(), "validation:") {
		return badRequest(c, "INVALID_PARAMS", strings.TrimPrefix(err.Error(), "validation: "))
	}
	if strings.Contains(strings.ToLower(err.Error()), "unavailable") || strings.Contains(strings.ToLower(err.Error()), "not configured") {
		return serviceUnavailable(c, err.Error())
	}
	return internalError(c, prefix+": "+err.Error())
}
