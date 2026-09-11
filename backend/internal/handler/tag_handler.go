package handler

import (
	"strconv"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
	"github.com/labstack/echo/v4"
)

// ---------------------------------------------------------------------------
// TagHandler
// ---------------------------------------------------------------------------

// TagHandler exposes HTTP endpoints for the tag taxonomy. Tags are organized
// by level (syntax or algorithm) and are used for problem classification.
type TagHandler struct {
	tagRepo *repository.TagRepository
}

// NewTagHandler creates a new TagHandler with the given tag repository.
func NewTagHandler(tagRepo *repository.TagRepository) *TagHandler {
	return &TagHandler{tagRepo: tagRepo}
}

// ---------------------------------------------------------------------------
// HandleList: GET /tags
// ---------------------------------------------------------------------------

// HandleList returns all tags, optionally filtered by the ?level= and
// ?difficulty= query parameters. Valid level values are "syntax" and
// "algorithm". When difficulty is specified along with level, only tags whose
// difficulty range includes the target rating are returned.
func (h *TagHandler) HandleList(c echo.Context) error {
	ctx := c.Request().Context()

	level := c.QueryParam("level")
	difficultyStr := c.QueryParam("difficulty")

	if level != "" {
		// Validate the level parameter.
		pl := domain.ProblemLevel(level)
		if !pl.IsValid() {
			return badRequest(c, "INVALID_PARAM", "level must be 'syntax' or 'algorithm'")
		}

		// If difficulty is specified, filter tags by difficulty range.
		if difficultyStr != "" {
			difficulty, err := strconv.Atoi(difficultyStr)
			if err != nil {
				return badRequest(c, "INVALID_PARAM", "difficulty must be a valid integer")
			}

			tags, err := h.tagRepo.GetByLevelAndDifficulty(ctx, pl, difficulty)
			if err != nil {
				return internalError(c, "failed to list tags: "+err.Error())
			}
			return ok(c, tags)
		}

		tags, err := h.tagRepo.GetByLevel(ctx, pl)
		if err != nil {
			return internalError(c, "failed to list tags: "+err.Error())
		}

		return ok(c, tags)
	}

	tags, err := h.tagRepo.GetAll(ctx)
	if err != nil {
		return internalError(c, "failed to list tags: "+err.Error())
	}

	return ok(c, tags)
}
