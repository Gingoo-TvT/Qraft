package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
)

func TestAnnotationPathsBypassJWTForTokenAuthentication(t *testing.T) {
	tests := []struct {
		method string
		path   string
	}{
		{method: http.MethodGet, path: "/api/v1/annotate/annotator-secret/next"},
		{method: http.MethodPost, path: "/api/v1/annotate/annotator-secret/submit"},
	}
	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			response := invokeAnnotationAuth(t, tt.method, tt.path)
			if response.Code != http.StatusNoContent {
				t.Fatalf("annotation status = %d, want %d", response.Code, http.StatusNoContent)
			}
		})
	}
}

func TestAnnotationJWTBypassRejectsOtherMethodsAndPaths(t *testing.T) {
	tests := []struct {
		method string
		path   string
	}{
		{method: http.MethodPost, path: "/api/v1/annotate/annotator-secret/next"},
		{method: http.MethodGet, path: "/api/v1/annotate/annotator-secret/submit"},
		{method: http.MethodGet, path: "/api/v1/annotate/annotator-secret/next/extra"},
		{method: http.MethodGet, path: "/api/v1/annotate//next"},
		{method: http.MethodGet, path: "/api/v1/annotate/annotator-secret"},
	}
	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			response := invokeAnnotationAuth(t, tt.method, tt.path)
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("annotation status = %d, want %d", response.Code, http.StatusUnauthorized)
			}
		})
	}
}

func TestRequestLogPathRedactsAnnotationToken(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{path: "/api/v1/annotate/annotator-secret/next", want: "/api/v1/annotate/[redacted]/next"},
		{path: "/api/v1/annotate/annotator-secret", want: "/api/v1/annotate/[redacted]"},
		{path: "/api/v1/annotate/", want: "/api/v1/annotate/"},
		{path: "/api/v1/quizzes", want: "/api/v1/quizzes"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := requestLogPath(tt.path); got != tt.want {
				t.Fatalf("requestLogPath(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func invokeAnnotationAuth(t *testing.T, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	e := echo.New()
	handler := Auth("jwt-secret", false)(func(c echo.Context) error {
		return c.NoContent(http.StatusNoContent)
	})
	request := httptest.NewRequest(method, path, nil)
	response := httptest.NewRecorder()
	if err := handler(e.NewContext(request, response)); err != nil {
		t.Fatalf("annotation request returned error: %v", err)
	}
	return response
}
