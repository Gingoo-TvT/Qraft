package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/labstack/echo/v4"
)

type questionSearchFake struct {
	calls  int
	filter domain.QuestionSearchFilter
	result domain.QuestionSearchResult
	err    error
}

func (f *questionSearchFake) Search(_ context.Context, filter domain.QuestionSearchFilter) (domain.QuestionSearchResult, error) {
	f.calls++
	f.filter = filter
	return f.result, f.err
}

func searchResponse(t *testing.T, fake *questionSearchFake, query string) *httptest.ResponseRecorder {
	t.Helper()
	e := echo.New()
	e.GET("/api/v1/questions/search", NewQuestionSearchHandler(fake).HandleSearch)
	recorder := httptest.NewRecorder()
	e.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/questions/search"+query, nil))
	return recorder
}

func TestQuestionSearchHandlerPreservesLiteralFiltersAndPagination(t *testing.T) {
	fake := &questionSearchFake{result: domain.QuestionSearchResult{Page: 3, Size: 10}}
	query := url.Values{
		"q": {"  数组 100%_'  "}, "type": {"choice"}, "tag": {" Tag "},
		"knowledge_point": {" CODE_loop "}, "quiz_difficulty": {"medium"},
		"page": {"3"}, "size": {"10"},
	}
	got := searchResponse(t, fake, "?"+query.Encode())
	if got.Code != http.StatusOK {
		t.Fatalf("status %d: %s", got.Code, got.Body.String())
	}
	f := fake.filter
	if fake.calls != 1 || f.Keyword != "数组 100%_'" || f.Type != domain.QuizTypeChoice || f.Tag != "Tag" || f.KnowledgePoint != "CODE_loop" || f.QuizDifficulty != domain.QuizDifficultyMedium || f.Page != 3 || f.Size != 10 {
		t.Fatalf("lost search criteria: %+v", f)
	}
	var body struct {
		Success bool                        `json:"success"`
		Data    []domain.QuestionSearchItem `json:"data"`
		Meta    map[string]int              `json:"meta"`
	}
	if err := json.Unmarshal(got.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.Success || body.Data == nil || len(body.Data) != 0 || len(body.Meta) != 3 || body.Meta["total"] != 0 || body.Meta["page"] != 3 || body.Meta["size"] != 10 {
		t.Fatalf("empty result envelope: %s", got.Body.String())
	}
}

func TestQuestionSearchHandlerRejectsInvalidFiltersBeforeDatabase(t *testing.T) {
	cases := []string{
		"?type=invalid", "?quiz_difficulty=invalid", "?min_difficulty=oops",
		"?min_difficulty=799", "?max_difficulty=3501",
		"?min_difficulty=1500&max_difficulty=1400",
		"?min_difficulty=800&quiz_difficulty=easy",
		"?min_difficulty=800&min_difficulty=900",
		"?page=0", "?page=-1", "?page=1000001", "?page=1.5",
		"?page=99999999999999999999999", "?page=1&page=2", "?page=",
		"?size=101", "?size=bad", "?q=%00", "?tag=%00", "?q=%FF", "?q=" + strings.Repeat("x", 201),
	}
	for _, query := range cases {
		t.Run(query, func(t *testing.T) {
			fake := &questionSearchFake{}
			got := searchResponse(t, fake, query)
			if got.Code != http.StatusBadRequest || fake.calls != 0 || !strings.Contains(got.Body.String(), "INVALID_PARAM") {
				t.Fatalf("status %d calls %d: %s", got.Code, fake.calls, got.Body.String())
			}
		})
	}
}

func TestQuestionSearchHandlerDefaultsAndDatabaseFailure(t *testing.T) {
	fake := &questionSearchFake{err: errors.New("postgres://private-host/internal-table")}
	got := searchResponse(t, fake, "")
	if fake.calls != 1 || fake.filter.Page != 1 || fake.filter.Size != 20 {
		t.Fatalf("default filter: %+v", fake.filter)
	}
	if got.Code != http.StatusInternalServerError || !strings.Contains(got.Body.String(), "INTERNAL_ERROR") || strings.Contains(got.Body.String(), "private-host") {
		t.Fatalf("database failure response: %d %s", got.Code, got.Body.String())
	}
}

func TestQuestionSearchHandlerNumericDifficulty(t *testing.T) {
	fake := &questionSearchFake{}
	got := searchResponse(t, fake, "?min_difficulty=1300&max_difficulty=1800")
	if got.Code != http.StatusOK || fake.filter.MinDifficulty == nil || *fake.filter.MinDifficulty != 1300 || fake.filter.MaxDifficulty == nil || *fake.filter.MaxDifficulty != 1800 || fake.filter.QuizDifficulty != "" {
		t.Fatalf("numeric filter: %+v, status %d", fake.filter, got.Code)
	}
}
