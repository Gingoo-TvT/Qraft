package handler

import (
	"context"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
	"github.com/labstack/echo/v4"
)

const reviewSettingsScope = "future_eligible_problems"

type reviewSettingsStore interface {
	GetReviewSettings(ctx context.Context) (repository.ReviewSettingsRecord, error)
	UpdateReviewSettings(
		ctx context.Context,
		autoApprovePublicRelease bool,
		actor string,
	) (repository.ReviewSettingsRecord, error)
}

type ReviewSettingsHandler struct {
	store reviewSettingsStore
}

func NewReviewSettingsHandler(store reviewSettingsStore) *ReviewSettingsHandler {
	return &ReviewSettingsHandler{store: store}
}

type reviewSettingsView struct {
	AutoApprovePublicRelease bool      `json:"auto_approve_public_release"`
	Scope                    string    `json:"scope"`
	UpdatedBy                string    `json:"updated_by"`
	UpdatedAt                time.Time `json:"updated_at"`
}

type reviewSettingsUpdate struct {
	AutoApprovePublicRelease *bool `json:"auto_approve_public_release"`
}

func (h *ReviewSettingsHandler) HandleGet(c echo.Context) error {
	if h == nil || h.store == nil {
		return serviceUnavailable(c, "review settings store is unavailable")
	}
	record, err := h.store.GetReviewSettings(c.Request().Context())
	if err != nil {
		return internalError(c, "failed to read review settings: "+err.Error())
	}
	return ok(c, reviewSettingsRecordView(record))
}

func (h *ReviewSettingsHandler) HandleUpdate(c echo.Context) error {
	if h == nil || h.store == nil {
		return serviceUnavailable(c, "review settings store is unavailable")
	}
	actor, allowed := publicReleaseApprover(c)
	if !allowed {
		return forbidden(c, "administrator role is required")
	}

	var request reviewSettingsUpdate
	if err := c.Bind(&request); err != nil {
		return badRequest(c, "INVALID_BODY", "failed to parse request body: "+err.Error())
	}
	if request.AutoApprovePublicRelease == nil {
		return badRequest(
			c,
			"AUTO_APPROVE_REQUIRED",
			"auto_approve_public_release must be true or false",
		)
	}

	record, err := h.store.UpdateReviewSettings(
		c.Request().Context(),
		*request.AutoApprovePublicRelease,
		actor,
	)
	if err != nil {
		return internalError(c, "failed to save review settings: "+err.Error())
	}
	return ok(c, reviewSettingsRecordView(record))
}

func reviewSettingsRecordView(record repository.ReviewSettingsRecord) reviewSettingsView {
	return reviewSettingsView{
		AutoApprovePublicRelease: record.AutoApprovePublicRelease,
		Scope:                    reviewSettingsScope,
		UpdatedBy:                record.UpdatedBy,
		UpdatedAt:                record.UpdatedAt,
	}
}
