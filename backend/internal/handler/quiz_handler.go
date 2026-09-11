package handler

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
	"github.com/Gingoo-TvT/Qraft/backend/internal/service"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

type QuizHandler struct {
	svc       *service.QuizService
	importSvc *service.QuizImportService
	exportSvc *service.QuizExportService
	kpRepo    *repository.KnowledgePointRepository
}

func NewQuizHandler(
	svc *service.QuizService,
	importSvc *service.QuizImportService,
	exportSvc *service.QuizExportService,
	kpRepo *repository.KnowledgePointRepository,
) *QuizHandler {
	handler := &QuizHandler{svc: svc, importSvc: importSvc, exportSvc: exportSvc, kpRepo: kpRepo}
	return handler
}

func (h *QuizHandler) HandleCreate(c echo.Context) error {
	var q domain.QuizProblem
	if err := c.Bind(&q); err != nil {
		return badRequest(c, "INVALID_BODY", "failed to parse request body: "+err.Error())
	}
	createdQuiz, err := h.svc.Create(c.Request().Context(), &q)
	if err != nil {
		return badRequest(c, "INVALID_QUIZ", err.Error())
	}
	return created(c, createdQuiz)
}

func (h *QuizHandler) HandleList(c echo.Context) error {
	filter, err := quizListFilterFromQuery(c)
	if err != nil {
		return err
	}
	result, err := h.svc.List(c.Request().Context(), service.QuizListRequest{
		Filter:   filter,
		Page:     intQueryParam(c, "page", 1),
		PageSize: intQueryParam(c, "size", 20),
	})
	if err != nil {
		return internalError(c, "failed to list quizzes: "+err.Error())
	}
	return okWithMeta(c, result.Quizzes, &Meta{
		Total: result.Total,
		Page:  result.Page,
		Size:  result.PageSize,
	})
}

func (h *QuizHandler) HandleGet(c echo.Context) error {
	id, err := parseUUID(c, "id")
	if err != nil {
		return err
	}
	q, err := h.svc.Get(c.Request().Context(), id)
	if err != nil {
		if errors.Is(err, service.ErrNotFound) {
			return notFound(c, "quiz not found")
		}
		return internalError(c, "failed to get quiz: "+err.Error())
	}
	return ok(c, q)
}

func (h *QuizHandler) HandleUpdate(c echo.Context) error {
	id, err := parseUUID(c, "id")
	if err != nil {
		return err
	}
	var q domain.QuizProblem
	if err := c.Bind(&q); err != nil {
		return badRequest(c, "INVALID_BODY", "failed to parse request body: "+err.Error())
	}
	q.ID = id
	if err := h.svc.Update(c.Request().Context(), &q); err != nil {
		if errors.Is(err, service.ErrNotFound) {
			return notFound(c, "quiz not found")
		}
		return badRequest(c, "INVALID_QUIZ", err.Error())
	}
	return ok(c, q)
}

func (h *QuizHandler) HandleDelete(c echo.Context) error {
	id, err := parseUUID(c, "id")
	if err != nil {
		return err
	}
	if err := h.svc.Delete(c.Request().Context(), id); err != nil {
		if errors.Is(err, service.ErrNotFound) {
			return notFound(c, "quiz not found")
		}
		return internalError(c, "failed to delete quiz: "+err.Error())
	}
	return noContent(c)
}

func (h *QuizHandler) HandleGenerate(c echo.Context) error {
	var req service.QuizGenerateRequest
	if err := c.Bind(&req); err != nil {
		return badRequest(c, "INVALID_BODY", "failed to parse request body: "+err.Error())
	}
	workflowID, err := h.svc.Generate(c.Request().Context(), req)
	if err != nil {
		if errors.Is(err, service.ErrNotFound) {
			return notFound(c, err.Error())
		}
		return badRequest(c, "INVALID_GENERATE_REQUEST", err.Error())
	}
	return c.JSON(http.StatusAccepted, APIResponse{
		Success: true,
		Data: map[string]interface{}{
			"workflow_id": workflowID,
			"count":       req.Count,
		},
	})
}

func (h *QuizHandler) HandleImport(c echo.Context) error {
	fileHeader, err := c.FormFile("file")
	if err != nil {
		return badRequest(c, "MISSING_FILE", "multipart field file is required")
	}
	file, err := fileHeader.Open()
	if err != nil {
		return badRequest(c, "INVALID_FILE", "failed to open uploaded file: "+err.Error())
	}
	defer file.Close()

	subject := c.FormValue("subject")
	if subject == "" {
		subject = c.QueryParam("subject")
	}
	report, err := h.importSvc.Import(c.Request().Context(), service.ImportRequest{
		File:       file,
		OnConflict: c.QueryParam("on_conflict"),
		Subject:    subject,
	})
	if err != nil {
		return badRequest(c, "IMPORT_FAILED", err.Error())
	}
	return ok(c, report)
}

// HandleTemplate returns a clean workbook generated from the public schema.
func (h *QuizHandler) HandleTemplate(c echo.Context) error {
	workbook, err := service.BuildQuizWorkbook(nil)
	if err != nil {
		return internalError(c, "failed to build quiz template")
	}
	c.Response().Header().Set(echo.HeaderContentDisposition, `attachment; filename="qraft-quiz-template.xlsx"`)
	return c.Blob(http.StatusOK, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", workbook)
}

func (h *QuizHandler) HandleExport(c echo.Context) error {
	filter, err := quizListFilterFromQuery(c)
	if err != nil {
		return err
	}
	filename := fmt.Sprintf("quizzes_export_%s.xlsx", time.Now().Format("20060102_150405"))
	c.Response().Header().Set(echo.HeaderContentDisposition, fmt.Sprintf(`attachment; filename="%s"`, filename))
	c.Response().Header().Set(echo.HeaderContentType, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	return h.exportSvc.Export(c.Request().Context(), service.ExportRequest{
		Filter: filter,
		Writer: c.Response(),
	})
}

func (h *QuizHandler) HandleListKnowledgePoints(c echo.Context) error {
	points, err := h.kpRepo.ListBySubject(c.Request().Context(), strings.TrimSpace(c.QueryParam("subject")))
	if err != nil {
		return internalError(c, "failed to list knowledge points: "+err.Error())
	}
	return ok(c, points)
}

func quizListFilterFromQuery(c echo.Context) (repository.QuizListFilter, error) {
	var filter repository.QuizListFilter
	if raw := strings.TrimSpace(c.QueryParam("type")); raw != "" {
		typ := domain.QuizType(raw)
		if !typ.IsValid() {
			return filter, badRequest(c, "INVALID_PARAM", "type is invalid")
		}
		filter.Type = &typ
	}
	if raw := strings.TrimSpace(c.QueryParam("subject")); raw != "" {
		filter.Subject = &raw
	}
	if raw := strings.TrimSpace(c.QueryParam("difficulty")); raw != "" {
		diff := domain.QuizDifficulty(raw)
		if !diff.IsValid() {
			return filter, badRequest(c, "INVALID_PARAM", "difficulty is invalid")
		}
		filter.Difficulty = &diff
	}
	if raw := strings.TrimSpace(c.QueryParam("tag")); raw != "" {
		filter.Tag = &raw
	}
	if raw := strings.TrimSpace(c.QueryParam("knowledge_point_id")); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			return filter, badRequest(c, "INVALID_PARAM", "knowledge_point_id must be a valid UUID")
		}
		filter.KnowledgePointID = &id
	}
	return filter, nil
}
