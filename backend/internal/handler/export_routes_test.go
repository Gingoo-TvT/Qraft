package handler

import (
	"github.com/labstack/echo/v4"
	"testing"
)

func TestPublicExportRoutesKeepHydroWithoutPrivateAdapters(t *testing.T) {
	for _, mode := range []bool{true, false} {
		e := echo.New()
		RegisterExportRoutes(e.Group("/api/v1"), &ProblemHandler{}, &QuizHandler{}, mode)
		routes := map[string]bool{}
		for _, route := range e.Routes() {
			routes[route.Method+" "+route.Path] = true
		}
		for _, path := range []string{"GET /api/v1/problems/hydro.zip", "POST /api/v1/problems/hydro/validate", "GET /api/v1/problems/:id/hydro.zip"} {
			if !routes[path] {
				t.Fatalf("missing %s", path)
			}
		}
		if len(routes) != 3 {
			t.Fatalf("unexpected private route: %v", routes)
		}
	}
}
