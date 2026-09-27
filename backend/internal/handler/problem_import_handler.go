package handler

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/service"
	"github.com/labstack/echo/v4"
	"io"
	"net/http"
	"strings"
)

type problemImportController interface {
	Start(context.Context, domain.ProblemImportRequest) (string, error)
	Status(context.Context, string) (*domain.ProblemImportState, error)
}
type ProblemImportHandler struct{ service problemImportController }

func NewProblemImportHandler(s problemImportController) *ProblemImportHandler {
	return &ProblemImportHandler{s}
}
func (h *ProblemImportHandler) HandleStart(c echo.Context) error {
	var req domain.ProblemImportRequest
	dec := json.NewDecoder(io.LimitReader(c.Request().Body, 2*1024*1024+1))
	dec.DisallowUnknownFields()
	if e := dec.Decode(&req); e != nil {
		return badRequest(c, "INVALID_IMPORT", "无法读取导入内容")
	}
	if e := dec.Decode(&struct{}{}); e != io.EOF {
		return badRequest(c, "INVALID_IMPORT", "只能提交一个JSON对象")
	}
	if e := req.Normalize(); e != nil {
		return badRequest(c, "INVALID_IMPORT", e.Error())
	}
	id, e := h.service.Start(c.Request().Context(), req)
	if e != nil {
		if errors.Is(e, service.ErrWorkflowAccessDenied) {
			return notFound(c, "import not found")
		}
		if strings.HasPrefix(e.Error(), "validation:") {
			return badRequest(c, "INVALID_IMPORT", e.Error())
		}
		return serviceUnavailable(c, "无法启动导入，请检查模型和工作流配置")
	}
	return c.JSON(http.StatusAccepted, APIResponse{Success: true, Data: map[string]string{"workflow_id": id}})
}
func (h *ProblemImportHandler) HandleGet(c echo.Context) error {
	state, e := h.service.Status(c.Request().Context(), c.Param("id"))
	if e != nil {
		if errors.Is(e, service.ErrNotFound) || errors.Is(e, service.ErrWorkflowAccessDenied) {
			return notFound(c, "import not found")
		}
		return serviceUnavailable(c, "暂时无法获取导入进度")
	}
	return ok(c, state)
}

func (h *ProblemImportHandler) HandleResume(c echo.Context) error {
	controller, ok := h.service.(interface {
		Resume(context.Context, string) (string, error)
	})
	if !ok {
		return serviceUnavailable(c, "暂时无法恢复导入")
	}
	id, err := controller.Resume(c.Request().Context(), c.Param("id"))
	if errors.Is(err, service.ErrNotFound) || errors.Is(err, service.ErrWorkflowAccessDenied) {
		return notFound(c, "import not found")
	}
	if errors.Is(err, service.ErrImportStillRunning) {
		return c.JSON(http.StatusConflict, map[string]any{"success": false, "error": map[string]string{"code": "IMPORT_STILL_RUNNING", "message": "本批仍在处理，请等待完成或停止后再继续未完成项目"}})
	}
	if err != nil {
		return serviceUnavailable(c, "暂时无法恢复，请检查模型配置并稍后重试")
	}
	return c.JSON(http.StatusAccepted, APIResponse{Success: true, Data: map[string]string{"workflow_id": id}})
}
