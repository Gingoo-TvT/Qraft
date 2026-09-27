package handler

import (
	"context"
	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/labstack/echo/v4"
	"net/http/httptest"
	"strings"
	"testing"
)

type importFakeController struct{ calls int }

func (s *importFakeController) Start(_ context.Context, r domain.ProblemImportRequest) (string, error) {
	s.calls++
	return "problem-import-fixture", nil
}
func (s *importFakeController) Status(_ context.Context, id string) (*domain.ProblemImportState, error) {
	return &domain.ProblemImportState{WorkflowID: id}, nil
}
func TestProblemImportHandlerRejectsUntrustedWorkflowFields(t *testing.T) {
	valid := `{"mode":"preserve_statement","items":[{"item_id":"a","title":"T","statement":"original"}],"create_set":true}`
	for _, body := range []string{valid, strings.TrimSuffix(valid, "}") + `,"owner_user_id":"admin"}`, strings.TrimSuffix(valid, "}") + `,"provider_config":{"statement":{"api_key":"secret"}}}`, valid + `{}`} {
		e := echo.New()
		rec := httptest.NewRecorder()
		c := e.NewContext(httptest.NewRequest("POST", "/api/v1/problem-imports", strings.NewReader(body)), rec)
		fake := &importFakeController{}
		if err := NewProblemImportHandler(fake).HandleStart(c); err != nil {
			t.Fatal(err)
		}
		if body == valid {
			if rec.Code != 202 || fake.calls != 1 {
				t.Fatalf("valid request rejected: %s", rec.Body)
			}
		} else if rec.Code != 400 || fake.calls != 0 {
			t.Fatalf("untrusted payload accepted: %s", body)
		}
	}
}
