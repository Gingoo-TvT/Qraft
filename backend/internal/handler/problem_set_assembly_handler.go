package handler

import (
	"github.com/Gingoo-TvT/Qraft/backend/internal/service"
	"github.com/labstack/echo/v4"
)

func (h *ProblemSetHandler) HandleAssemblyPreview(c echo.Context) error {
	var req service.ProblemSetAssemblyRequest
	if err := c.Bind(&req); err != nil {
		return badRequest(c, "INVALID_BODY", "无法解析组卷条件")
	}
	preview, err := h.svc.PreviewAssembly(c.Request().Context(), req)
	if err != nil {
		return handleProblemSetError(c, err, "无法预览组卷")
	}
	return ok(c, preview)
}

func (h *ProblemSetHandler) HandleAssemble(c echo.Context) error {
	var req service.ProblemSetAssembleRequest
	if err := c.Bind(&req); err != nil {
		return badRequest(c, "INVALID_BODY", "无法解析组卷请求")
	}
	req.CreatedBy = editActor(c)
	set, err := h.svc.Assemble(c.Request().Context(), req)
	if err != nil {
		return handleProblemSetError(c, err, "组卷保存失败")
	}
	return created(c, set)
}
