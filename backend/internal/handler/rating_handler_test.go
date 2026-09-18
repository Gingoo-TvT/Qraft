package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	authmw "github.com/Gingoo-TvT/Qraft/backend/internal/handler/middleware"
	"github.com/Gingoo-TvT/Qraft/backend/internal/rating"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/require"
)

type ratingHTTPStore struct {
	rating.Store
	calls  int
	failed bool
	token  string
}

func (s *ratingHTTPStore) Workspace(context.Context, uuid.UUID) (rating.Workspace, error) {
	s.calls++
	return rating.Workspace{}, nil
}
func (s *ratingHTTPStore) ReviewTask(_ context.Context, token string) (rating.ReviewTask, error) {
	s.calls++
	s.token = token
	return rating.ReviewTask{Title: "Blind fixture", Statement: "Compute a sum.", SubjectHash: strings.Repeat("a", 64), Samples: []rating.ReviewSample{{Input: "1 2", Output: "3"}}, WindowMinutes: 60, ExpiresAt: time.Now().Add(time.Hour)}, nil
}
func (s *ratingHTTPStore) SubmitFeedback(_ context.Context, token string, f rating.FeedbackInput) (rating.Feedback, error) {
	s.calls++
	s.token = token
	return rating.Feedback{Revision: 2, ReviewerID: uuid.New(), FeedbackInput: f}, nil
}
func (s *ratingHTTPStore) CreateAssessment(context.Context, uuid.UUID, string) (rating.Assessment, error) {
	s.calls++
	return rating.Assessment{ID: uuid.New()}, nil
}
func (s *ratingHTTPStore) UpdateAssessment(_ context.Context, _ uuid.UUID, status, _ string, _ *rating.Report, message string) error {
	s.failed = status == "failed" && !strings.Contains(message, "secret")
	return nil
}
func ratingTestContext(method, path, body string) (echo.Context, *httptest.ResponseRecorder) {
	e := echo.New()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	c := e.NewContext(req, rec)
	c.SetParamNames("id")
	c.SetParamValues(uuid.NewString())
	return c, rec
}
func TestRatingAdminNeverInfersAnonymousOrHeaderIdentity(t *testing.T) {
	for _, tc := range []struct {
		name   string
		claims *authmw.JWTClaims
		dev    bool
		status int
	}{
		{"anonymous", nil, false, 403}, {"forged admin role", &authmw.JWTClaims{Role: "admin"}, false, 403}, {"member", &authmw.JWTClaims{Role: "user", UserID: "u"}, true, 403}, {"admin", &authmw.JWTClaims{Role: "admin", UserID: "a"}, false, 200}, {"explicit local deployment", nil, true, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &ratingHTTPStore{}
			h := NewRatingHandler(s, RatingHandlerOptions{DevMode: tc.dev})
			c, rec := ratingTestContext(http.MethodGet, "/api/v1/rating/problems/p", "")
			c.Request().Header.Set("X-Actor", "admin")
			if tc.claims != nil {
				c.Set("user", tc.claims)
			}
			require.NoError(t, h.HandleWorkspace(c))
			require.Equal(t, tc.status, rec.Code)
			require.Equal(t, tc.status == 200, s.calls > 0)
		})
	}
}
func TestRatingPublicRequiresInvitationEvenInDevMode(t *testing.T) {
	s := &ratingHTTPStore{}
	h := NewRatingHandler(s, RatingHandlerOptions{DevMode: true})
	c, rec := ratingTestContext(http.MethodGet, "/api/v1/public/rating/review?token=secret", "")
	c.Set("user", &authmw.JWTClaims{Role: "admin", UserID: "admin"})
	require.NoError(t, h.HandleReviewTask(c))
	require.Equal(t, 401, rec.Code)
	require.Zero(t, s.calls)
	c, rec = ratingTestContext(http.MethodGet, "/api/v1/public/rating/review", "")
	c.Request().Header.Set("X-Qraft-Review-Token", "scoped-secret")
	require.NoError(t, h.HandleReviewTask(c))
	require.Equal(t, 200, rec.Code)
	require.Equal(t, "scoped-secret", s.token)
	require.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	for _, hidden := range []string{"target_difficulty", "official_solution", "expected_tags", "reviewer_id", "other_feedback", "scoped-secret"} {
		require.NotContains(t, rec.Body.String(), hidden)
	}
	require.Contains(t, rec.Body.String(), "Compute a sum.")
}
func TestRatingPublicCannotForgeReviewerOrDecision(t *testing.T) {
	s := &ratingHTTPStore{}
	h := NewRatingHandler(s, RatingHandlerOptions{DevMode: true})
	for _, body := range []string{`{"outcome":"solved","reviewer_id":"fake"}`, `{"outcome":"solved","rating":3500}`} {
		c, rec := ratingTestContext(http.MethodPost, "/api/v1/public/rating/review", body)
		c.Request().Header.Set("X-Qraft-Review-Token", "scoped-secret")
		require.NoError(t, h.HandleSubmitFeedback(c))
		require.Equal(t, 400, rec.Code)
	}
	require.Zero(t, s.calls)
	c, rec := ratingTestContext(http.MethodPost, "/api/v1/public/rating/review", `{"outcome":"solved","result_source":"self_report"}`)
	c.Request().Header.Set("X-Qraft-Review-Token", "scoped-secret")
	require.NoError(t, h.HandleSubmitFeedback(c))
	require.Equal(t, 200, rec.Code)
	require.NotContains(t, rec.Body.String(), "reviewer_id")
	require.Contains(t, rec.Body.String(), `"revision":2`)
}
func TestRatingStartFailureDoesNotLeavePendingOrExposeProviderError(t *testing.T) {
	s := &ratingHTTPStore{}
	h := NewRatingHandler(s, RatingHandlerOptions{DevMode: true, StartAssessment: func(context.Context, uuid.UUID) error { return errors.New("provider secret") }})
	c, rec := ratingTestContext(http.MethodPost, "/api/v1/rating/problems/p/assessments", "")
	require.NoError(t, h.HandleCreateAssessment(c))
	require.Equal(t, 503, rec.Code)
	require.True(t, s.failed)
	require.NotContains(t, rec.Body.String(), "secret")
}
