package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	authmw "github.com/Gingoo-TvT/Qraft/backend/internal/handler/middleware"
	"github.com/Gingoo-TvT/Qraft/backend/internal/rating"
	"github.com/Gingoo-TvT/Qraft/backend/internal/service"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

// DevMode is an explicit deployment choice, never inferred from absent claims.
// Public review routes never invoke the administrator fallback.
type RatingHandlerOptions struct {
	ReconcileAssessment func(context.Context, uuid.UUID) error
	DevMode             bool
	StartAssessment     func(context.Context, uuid.UUID) error
	CancelAssessment    func(context.Context, uuid.UUID) error
}

const ratingStartCleanupTimeout = 5 * time.Second

type RatingHandler struct {
	store rating.Store
	opts  RatingHandlerOptions
}

func NewRatingHandler(store rating.Store, opts RatingHandlerOptions) *RatingHandler {
	return &RatingHandler{store: store, opts: opts}
}
func (h *RatingHandler) Register(g *echo.Group) {
	g.GET("/rating/problems/:id", h.HandleWorkspace)
	g.POST("/rating/problems/:id/assessments", h.HandleCreateAssessment)
	g.GET("/rating/assessments/:id", h.HandleGetAssessment)
	g.DELETE("/rating/assessments/:id", h.HandleCancelAssessment)
	g.GET("/rating/anchors", h.HandleListAnchors)
	g.POST("/rating/anchors", h.HandleCreateAnchor)
	g.GET("/rating/problems/:id/invitations", h.HandleListInvitations)
	g.POST("/rating/problems/:id/invitations", h.HandleIssueInvitation)
	g.DELETE("/rating/problems/:id/invitations/:invitation_id", h.HandleRevokeInvitation)
	g.GET("/rating/problems/:id/feedback", h.HandleListFeedback)
	g.POST("/rating/problems/:id/calibration", h.HandleCalibrate)
	g.GET("/rating/problems/:id/decisions", h.HandleListDecisions)
	g.POST("/rating/problems/:id/decisions", h.HandleDecide)
	g.GET("/public/rating/review", h.HandleReviewTask)
	g.POST("/public/rating/review", h.HandleSubmitFeedback)
}
func (h *RatingHandler) admin(c echo.Context) (string, error) {
	actor, allowed := ratingAdminActor(c, h.opts.DevMode)
	if !allowed {
		return "", forbidden(c, "administrator with a stable identity is required")
	}
	if h.store == nil {
		return "", serviceUnavailable(c, "rating service is unavailable")
	}
	c.Response().Header().Set("Cache-Control", "no-store")
	return actor, nil
}
func ratingAdminActor(c echo.Context, devMode bool) (string, bool) {
	claims := authmw.GetClaims(c)
	if claims == nil {
		if devMode {
			return "local-admin", true
		}
		return "", false
	}
	if !strings.EqualFold(strings.TrimSpace(claims.Role), "admin") {
		return "", false
	}
	if id := strings.TrimSpace(claims.UserID); id != "" {
		return "user:" + id, true
	}
	if sub := strings.TrimSpace(claims.Subject); sub != "" {
		return "subject:" + sub, true
	}
	return "", false
}
func ratingResponseError(c echo.Context, err error) error {
	switch {
	case errors.Is(err, rating.ErrNotFound):
		return notFound(c, "rating record not found")
	case errors.Is(err, rating.ErrUnauthorized):
		return unauthorized(c, "review invitation is invalid, expired, or revoked")
	case errors.Is(err, rating.ErrConflict):
		return conflict(c, "evidence or task state changed; refresh before continuing")
	case errors.Is(err, rating.ErrInvalid):
		return badRequest(c, "INVALID_RATING_INPUT", err.Error())
	default:
		return internalError(c, "rating operation failed")
	}
}
func ratingBody(c echo.Context, v any) error {
	reader := io.LimitReader(c.Request().Body, (256<<10)+1)
	raw, err := io.ReadAll(reader)
	if err != nil {
		return err
	}
	if len(raw) > 256<<10 {
		return rating.ErrInvalid
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(v); err != nil {
		return err
	}
	if err = decoder.Decode(new(any)); err != io.EOF {
		return rating.ErrInvalid
	}
	return nil
}
func (h *RatingHandler) HandleWorkspace(c echo.Context) error {
	if actor, err := h.admin(c); actor == "" {
		return err
	}
	id, err := parseUUID(c, "id")
	if err != nil {
		return err
	}
	w, err := h.store.Workspace(c.Request().Context(), id)
	if err != nil {
		return ratingResponseError(c, err)
	}
	if h.opts.ReconcileAssessment != nil {
		for i, assessment := range w.Assessments {
			if assessment.Status != "pending" && assessment.Status != "running" {
				continue
			}
			if err := h.opts.ReconcileAssessment(c.Request().Context(), assessment.ID); err != nil {
				return serviceUnavailable(c, "暂时无法同步评估任务状态，请重试")
			}
			latest, err := h.store.GetAssessment(c.Request().Context(), assessment.ID)
			if err != nil {
				return ratingResponseError(c, err)
			}
			latest.Stale = latest.Subject.Hash != w.Subject.Hash
			w.Assessments[i] = latest
		}
	}
	return ok(c, w)
}
func (h *RatingHandler) HandleCreateAssessment(c echo.Context) error {
	actor, err := h.admin(c)
	if actor == "" {
		return err
	}
	id, err := parseUUID(c, "id")
	if err != nil {
		return err
	}
	if h.opts.StartAssessment == nil {
		return serviceUnavailable(c, "rating workflow is unavailable")
	}
	if h.opts.ReconcileAssessment != nil {
		current, err := h.store.Workspace(c.Request().Context(), id)
		if err != nil {
			return ratingResponseError(c, err)
		}
		for _, item := range current.Assessments {
			if item.Status == "pending" || item.Status == "running" {
				if err := h.opts.ReconcileAssessment(c.Request().Context(), item.ID); err != nil {
					return serviceUnavailable(c, "暂时无法同步旧评估任务状态，请重试")
				}
			}
		}
	}
	a, err := h.store.CreateAssessment(c.Request().Context(), id, actor)
	if err != nil {
		return ratingResponseError(c, err)
	}
	if err = h.opts.StartAssessment(c.Request().Context(), a.ID); err != nil {
		if errors.Is(err, service.ErrRatingStartUncertain) {
			// The execution service may have accepted the request. Preserve its
			// reservation; polling or explicit cancel will reconcile it safely.
			return created(c, a)
		}
		// Do not persist raw provider errors: they may contain credentials.
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(c.Request().Context()), ratingStartCleanupTimeout)
		defer cancel()
		_ = h.store.UpdateAssessment(cleanupCtx, a.ID, "failed", "configuration", nil, "Assessment could not start; check model and workflow configuration.")
		return serviceUnavailable(c, "assessment could not start; check model and workflow configuration")
	}
	return created(c, a)
}
func (h *RatingHandler) HandleGetAssessment(c echo.Context) error {
	if actor, err := h.admin(c); actor == "" {
		return err
	}
	id, err := parseUUID(c, "id")
	if err != nil {
		return err
	}
	if h.opts.ReconcileAssessment != nil {
		if err := h.opts.ReconcileAssessment(c.Request().Context(), id); err != nil {
			return serviceUnavailable(c, "暂时无法同步评估任务状态，请重试")
		}
	}
	a, err := h.store.GetAssessment(c.Request().Context(), id)
	if err != nil {
		return ratingResponseError(c, err)
	}
	s, err := h.store.CaptureSubject(c.Request().Context(), a.ProblemID)
	if err != nil {
		return ratingResponseError(c, err)
	}
	a.Stale = s.Hash != a.Subject.Hash
	return ok(c, a)
}
func (h *RatingHandler) HandleCancelAssessment(c echo.Context) error {
	if actor, err := h.admin(c); actor == "" {
		return err
	}
	id, err := parseUUID(c, "id")
	if err != nil {
		return err
	}
	a, err := h.store.GetAssessment(c.Request().Context(), id)
	if err != nil {
		return ratingResponseError(c, err)
	}
	if a.Status == "completed" || a.Status == "failed" {
		return conflict(c, "assessment has already finished")
	}
	if a.Status == "cancelled" {
		return noContent(c)
	}
	if h.opts.CancelAssessment == nil {
		return serviceUnavailable(c, "workflow cancellation is unavailable")
	}
	if err = h.opts.CancelAssessment(c.Request().Context(), id); err != nil {
		return serviceUnavailable(c, "could not request workflow cancellation")
	}
	latest, err := h.store.GetAssessment(c.Request().Context(), id)
	if err != nil {
		return ratingResponseError(c, err)
	}
	if latest.Status == "completed" || latest.Status == "cancelled" || latest.Status == "failed" {
		return noContent(c)
	}
	if err = h.store.UpdateAssessment(c.Request().Context(), id, "cancelled", "cancelled", nil, ""); err != nil {
		return ratingResponseError(c, err)
	}
	return noContent(c)
}
func (h *RatingHandler) HandleListAnchors(c echo.Context) error {
	if actor, err := h.admin(c); actor == "" {
		return err
	}
	a, err := h.store.ListAnchors(c.Request().Context())
	if err != nil {
		return ratingResponseError(c, err)
	}
	return ok(c, a)
}
func (h *RatingHandler) HandleCreateAnchor(c echo.Context) error {
	actor, err := h.admin(c)
	if actor == "" {
		return err
	}
	var a rating.Anchor
	if err = ratingBody(c, &a); err != nil {
		return badRequest(c, "INVALID_BODY", "invalid anchor payload")
	}
	a, err = h.store.CreateAnchor(c.Request().Context(), a, actor)
	if err != nil {
		return ratingResponseError(c, err)
	}
	return created(c, a)
}
func (h *RatingHandler) HandleIssueInvitation(c echo.Context) error {
	actor, err := h.admin(c)
	if actor == "" {
		return err
	}
	id, err := parseUUID(c, "id")
	if err != nil {
		return err
	}
	var q rating.InvitationRequest
	if err = ratingBody(c, &q); err != nil {
		return badRequest(c, "INVALID_BODY", "invalid invitation payload")
	}
	i, err := h.store.IssueInvitation(c.Request().Context(), id, q, actor)
	if err != nil {
		return ratingResponseError(c, err)
	}
	return created(c, i)
}
func (h *RatingHandler) HandleListInvitations(c echo.Context) error {
	if actor, err := h.admin(c); actor == "" {
		return err
	}
	id, err := parseUUID(c, "id")
	if err != nil {
		return err
	}
	result, err := h.store.ListInvitations(c.Request().Context(), id)
	if err != nil {
		return ratingResponseError(c, err)
	}
	return ok(c, result)
}
func (h *RatingHandler) HandleRevokeInvitation(c echo.Context) error {
	if actor, err := h.admin(c); actor == "" {
		return err
	}
	id, err := parseUUID(c, "id")
	if err != nil {
		return err
	}
	inv, err := parseUUID(c, "invitation_id")
	if err != nil {
		return err
	}
	if err = h.store.RevokeInvitation(c.Request().Context(), id, inv); err != nil {
		return ratingResponseError(c, err)
	}
	return noContent(c)
}
func (h *RatingHandler) HandleListFeedback(c echo.Context) error {
	if actor, err := h.admin(c); actor == "" {
		return err
	}
	id, err := parseUUID(c, "id")
	if err != nil {
		return err
	}
	s, err := h.store.CaptureSubject(c.Request().Context(), id)
	if err != nil {
		return ratingResponseError(c, err)
	}
	f, err := h.store.ListFeedback(c.Request().Context(), id, s.Hash)
	if err != nil {
		return ratingResponseError(c, err)
	}
	return ok(c, map[string]any{"feedback": f, "feedback_hash": rating.FeedbackSnapshotHash(f), "summary": rating.SummarizeFeedback(f), "subject_hash": s.Hash})
}
func (h *RatingHandler) HandleCalibrate(c echo.Context) error {
	if actor, err := h.admin(c); actor == "" {
		return err
	}
	id, err := parseUUID(c, "id")
	if err != nil {
		return err
	}
	s, err := h.store.CaptureSubject(c.Request().Context(), id)
	if err != nil {
		return ratingResponseError(c, err)
	}
	fs, err := h.store.ListFeedback(c.Request().Context(), id, s.Hash)
	if err != nil {
		return ratingResponseError(c, err)
	}
	cal, err := h.store.SaveCalibration(c.Request().Context(), rating.BuildCalibration(id, s.Hash, fs))
	if err != nil {
		return ratingResponseError(c, err)
	}
	return created(c, cal)
}
func (h *RatingHandler) HandleListDecisions(c echo.Context) error {
	if actor, err := h.admin(c); actor == "" {
		return err
	}
	id, err := parseUUID(c, "id")
	if err != nil {
		return err
	}
	w, err := h.store.Workspace(c.Request().Context(), id)
	if err != nil {
		return ratingResponseError(c, err)
	}
	return ok(c, w.Decisions)
}
func (h *RatingHandler) HandleDecide(c echo.Context) error {
	actor, err := h.admin(c)
	if actor == "" {
		return err
	}
	id, err := parseUUID(c, "id")
	if err != nil {
		return err
	}
	var q rating.DecisionInput
	if err = ratingBody(c, &q); err != nil {
		return badRequest(c, "INVALID_BODY", "invalid decision payload")
	}
	d, err := h.store.Decide(c.Request().Context(), id, q, actor)
	if err != nil {
		return ratingResponseError(c, err)
	}
	return created(c, d)
}
func reviewToken(c echo.Context) string {
	c.Response().Header().Set("Cache-Control", "no-store")
	c.Response().Header().Set("Referrer-Policy", "no-referrer")
	return strings.TrimSpace(c.Request().Header.Get("X-Qraft-Review-Token"))
}
func (h *RatingHandler) HandleReviewTask(c echo.Context) error {
	token := reviewToken(c)
	if token == "" {
		return unauthorized(c, "review invitation token is required")
	}
	if h.store == nil {
		return serviceUnavailable(c, "rating service is unavailable")
	}
	task, err := h.store.ReviewTask(c.Request().Context(), token)
	if err != nil {
		return ratingResponseError(c, err)
	}
	return ok(c, task)
}
func (h *RatingHandler) HandleSubmitFeedback(c echo.Context) error {
	token := reviewToken(c)
	if token == "" {
		return unauthorized(c, "review invitation token is required")
	}
	if h.store == nil {
		return serviceUnavailable(c, "rating service is unavailable")
	}
	var f rating.FeedbackInput
	if err := ratingBody(c, &f); err != nil {
		return badRequest(c, "INVALID_BODY", "invalid feedback payload")
	}
	saved, err := h.store.SubmitFeedback(c.Request().Context(), token, f)
	if err != nil {
		return ratingResponseError(c, err)
	}
	return ok(c, map[string]any{"revision": saved.Revision, "updated_at": saved.UpdatedAt})
}
