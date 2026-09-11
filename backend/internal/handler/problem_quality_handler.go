package handler

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/Gingoo-TvT/Qraft/backend/internal/qualityapi"
	"github.com/Gingoo-TvT/Qraft/backend/internal/service"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

type ProblemQualityReader interface {
	GetProblemQuality(context.Context, uuid.UUID) (*service.ProblemQualityDocumentV1, error)
	GetProblemQualityAudit(context.Context, uuid.UUID) (*service.ProblemQualityAuditDocumentV1, error)
	BuildProblemQualityBatchManifest(context.Context, qualityapi.ProblemQualityBatchRequestV1) (*service.ProblemQualityBatchDocumentV1, error)
}

type ProblemQualityHandler struct {
	quality ProblemQualityReader
}

func NewProblemQualityHandler(quality ProblemQualityReader) *ProblemQualityHandler {
	return &ProblemQualityHandler{quality: quality}
}

// RegisterProblemQualityRoutes is intentionally separate from ProblemHandler
// so the V1 product surface can be wired or rolled back without changing the
// legacy problem CRUD contract. Register the static batch path before generic
// /problems/:id routes.
func RegisterProblemQualityRoutes(group *echo.Group, handler *ProblemQualityHandler) {
	group.POST("/problems/quality/batch-manifest", handler.HandleBuildBatchManifest)
	group.GET("/problems/:id/quality/audit", handler.HandleGetAudit)
	group.GET("/problems/:id/quality", handler.HandleGetQuality)
}

func (h *ProblemQualityHandler) HandleGetQuality(c echo.Context) error {
	id, err := parseUUID(c, "id")
	if err != nil {
		return err
	}
	if h == nil || h.quality == nil {
		return internalError(c, "problem quality service is not configured")
	}
	document, err := h.quality.GetProblemQuality(c.Request().Context(), id)
	if err != nil {
		return mapProblemQualityError(c, err)
	}
	if document == nil || len(document.Bytes) == 0 {
		return internalError(c, "problem quality service returned an empty report")
	}
	c.Response().Header().Set("X-AlgoForge-Quality-Report-SHA256", document.SHA256)
	c.Response().Header().Set("X-AlgoForge-Quality-Audit-SHA256", document.Report.Evidence.Audit.SHA256)
	c.Response().Header().Set("X-AlgoForge-Test-Manifest-SHA256", document.Report.Evidence.TestManifest.SHA256)
	c.Response().Header().Set("X-AlgoForge-External-OJ-Import-Verified", "false")
	return c.Blob(http.StatusOK, echo.MIMEApplicationJSON, document.Bytes)
}

func (h *ProblemQualityHandler) HandleGetAudit(c echo.Context) error {
	id, err := parseUUID(c, "id")
	if err != nil {
		return err
	}
	if h == nil || h.quality == nil {
		return internalError(c, "problem quality service is not configured")
	}
	document, err := h.quality.GetProblemQualityAudit(c.Request().Context(), id)
	if err != nil {
		return mapProblemQualityError(c, err)
	}
	if document == nil || len(document.Bytes) == 0 {
		return internalError(c, "problem quality service returned an empty audit")
	}
	c.Response().Header().Set("X-AlgoForge-Quality-Audit-SHA256", document.SHA256)
	c.Response().Header().Set("X-AlgoForge-External-OJ-Import-Verified", "false")
	return c.Blob(http.StatusOK, echo.MIMEApplicationJSON, document.Bytes)
}

func (h *ProblemQualityHandler) HandleBuildBatchManifest(c echo.Context) error {
	if h == nil || h.quality == nil {
		return internalError(c, "problem quality service is not configured")
	}
	reader := http.MaxBytesReader(c.Response(), c.Request().Body, qualityapi.MaxBatchRequestBytesV1+1)
	body, err := io.ReadAll(reader)
	if err != nil {
		return badRequest(c, "INVALID_BODY", "quality batch request exceeds the allowed size")
	}
	request, _, _, err := qualityapi.DecodeProblemQualityBatchRequestV1(body)
	if err != nil {
		return badRequest(c, "INVALID_BODY", err.Error())
	}
	document, err := h.quality.BuildProblemQualityBatchManifest(c.Request().Context(), request)
	if err != nil {
		return mapProblemQualityError(c, err)
	}
	if document == nil || len(document.Bytes) == 0 {
		return internalError(c, "problem quality service returned an empty batch manifest")
	}
	c.Response().Header().Set("X-AlgoForge-Quality-Batch-Manifest-SHA256", document.SHA256)
	c.Response().Header().Set("X-AlgoForge-External-OJ-Import-Verified", "false")
	return c.Blob(http.StatusOK, echo.MIMEApplicationJSON, document.Bytes)
}

func mapProblemQualityError(c echo.Context, err error) error {
	switch {
	case errors.Is(err, service.ErrNotFound):
		return notFound(c, "problem not found")
	case errors.Is(err, service.ErrConflict):
		return conflict(c, err.Error())
	case strings.HasPrefix(err.Error(), "validation:"):
		return badRequest(c, "INVALID_BODY", strings.TrimSpace(strings.TrimPrefix(err.Error(), "validation:")))
	default:
		return internalError(c, "failed to build problem quality evidence: "+err.Error())
	}
}
