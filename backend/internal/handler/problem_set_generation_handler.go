package handler

import (
	"context"
	"net/http"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

type problemSetGenerationController interface {
	Start(context.Context, uuid.UUID) (*domain.ProblemSetGenerationState, error)
	Status(context.Context, uuid.UUID) (*domain.ProblemSetGenerationState, error)
	Cancel(context.Context, uuid.UUID) error
}

func (h *ProblemSetHandler) SetGenerationService(g problemSetGenerationController) { h.generation = g }
func (h *ProblemSetHandler) HandleStartGeneration(c echo.Context) error {
	id, err := parseUUID(c, "id")
	if err != nil {
		return err
	}
	state, err := h.generation.Start(c.Request().Context(), id)
	if err != nil {
		return handleProblemSetError(c, err, "failed to start set generation")
	}
	return c.JSON(http.StatusAccepted, APIResponse{Success: true, Data: state})
}
func (h *ProblemSetHandler) HandleGenerationStatus(c echo.Context) error {
	id, err := parseUUID(c, "id")
	if err != nil {
		return err
	}
	state, err := h.generation.Status(c.Request().Context(), id)
	if err != nil {
		return handleProblemSetError(c, err, "failed to read set generation")
	}
	return ok(c, state)
}
func (h *ProblemSetHandler) HandleCancelGeneration(c echo.Context) error {
	id, err := parseUUID(c, "id")
	if err != nil {
		return err
	}
	if err = h.generation.Cancel(c.Request().Context(), id); err != nil {
		return handleProblemSetError(c, err, "failed to stop set generation")
	}
	return c.JSON(http.StatusAccepted, APIResponse{Success: true, Data: map[string]bool{"stop_requested": true}})
}
