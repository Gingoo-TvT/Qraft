package middleware

import (
	"github.com/labstack/echo/v4"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRatingInvitationHeaderIsAllowedWithoutAuthorization(t *testing.T) {
	e := echo.New()
	e.Use(CORS(DefaultCORSConfig()))
	e.GET("/api/v1/public/rating/review", func(c echo.Context) error { return c.NoContent(200) })
	req := httptest.NewRequest(http.MethodOptions, "/api/v1/public/rating/review", nil)
	req.Header.Set("Origin", "https://review.qraft.invalid")
	req.Header.Set("Access-Control-Request-Method", "GET")
	req.Header.Set("Access-Control-Request-Headers", "X-Qraft-Review-Token")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent || !strings.Contains(strings.ToLower(rec.Header().Get("Access-Control-Allow-Headers")), "x-qraft-review-token") {
		t.Fatalf("invited review preflight failed: %d %v", rec.Code, rec.Header())
	}
}
