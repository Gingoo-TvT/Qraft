package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
)

func TestProblemSetExportRejectsInvalidModeAndFormatBeforeService(t *testing.T) {
	for _, query := range []string{"mode=unknown", "format=hydro", "mode=publication&format=generic", "mode=testing&format=unknown", "mode=testing", "mode=testing&format=hydro&allow_reuse=invalid"} {
		t.Run(query, func(t *testing.T) {
			e := echo.New()
			req := httptest.NewRequest(http.MethodGet, "/api/v1/problem-sets/id/export.zip?"+query, nil)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			c.SetParamNames("id")
			c.SetParamValues("00000000-0000-0000-0000-000000000001")
			if err := NewProblemSetHandler(nil).HandleExport(c); err != nil {
				t.Fatal(err)
			}
			if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "INVALID_PARAMS") {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestProblemSetTemplateExportRejectsInvalidUploadBeforeService(t *testing.T) {
	for _, input := range []struct{ query, body string }{
		{"mode=publication", `{}`}, {"mode=testing&format=hydro", `{}`},
		{"mode=testing&format=generic", `{"tag_catalog":{"activeTagsTree":[]}}`},
		{"mode=testing&format=generic", `{"extra":"unknown"}`},
		{"mode=testing&format=generic", `{} {}`},
		{"mode=testing&format=generic", `{"tag_catalog":"` + strings.Repeat("x", 1<<20) + `"}`},
	} {
		e := echo.New()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/problem-sets/id/export.zip?"+input.query, strings.NewReader(input.body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.SetParamNames("id")
		c.SetParamValues("00000000-0000-0000-0000-000000000001")
		if err := NewProblemSetHandler(nil).HandleExport(c); err != nil {
			t.Fatal(err)
		}
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status=%d %s", rec.Code, rec.Body.String())
		}
	}
}
