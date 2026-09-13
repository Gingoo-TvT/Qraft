//go:build integration

package repository_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/handler"
	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v4"
)

// This test uses synthetic fixtures in an explicitly disposable migrated DB.
// Both repositories are exercised through one SQL-backed HTTP search endpoint.
func TestQuestionSearchIntegration(t *testing.T) {
	if os.Getenv("ALGOFORGE_INTEGRATION_DISPOSABLE_DB") != "true" {
		t.Skip("set ALGOFORGE_INTEGRATION_DISPOSABLE_DB=true with a disposable migrated DATABASE_URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	marker := "qsearch" + strings.ReplaceAll(uuid.NewString(), "-", "")
	tag := marker + "-graph"
	kpCode := marker + "-concept"
	tagName := "检索图论" + marker
	kpName := "检索概念" + marker
	kpID, kpSecond := uuid.New(), uuid.New()
	problemID, hardID, choiceID, fillID, judgeID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	// A legacy quiz programming question may have the same UUID as a problem;
	// source and ID together identify its own detail route.
	legacyID := problemID
	literalID, decoyID := uuid.New(), uuid.New()
	quarantinedID, rejectedID := uuid.New(), uuid.New()
	problemIDs := []uuid.UUID{problemID, hardID, quarantinedID, rejectedID}
	quizIDs := []uuid.UUID{choiceID, fillID, judgeID, legacyID, literalID, decoyID}
	defer func() {
		clean, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		for _, query := range []struct {
			sql string
			arg interface{}
		}{
			{"DELETE FROM quiz_problems WHERE id = ANY($1)", quizIDs},
			{"DELETE FROM problems WHERE id = ANY($1)", problemIDs},
			{"DELETE FROM knowledge_points WHERE id = ANY($1)", []uuid.UUID{kpID, kpSecond}},
			{"DELETE FROM tag_categories WHERE tag_name = $1", tag},
		} {
			if _, err := pool.Exec(clean, query.sql, query.arg); err != nil {
				t.Errorf("cleanup synthetic fixture: %v", err)
			}
		}
	}()
	exec := func(sql string, args ...interface{}) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec("INSERT INTO tag_categories(level, tag_name, display_name) VALUES ('algorithm',$1,$2)", tag, tagName)
	exec("INSERT INTO knowledge_points(id, subject, code, name) VALUES ($1,'data_structure_algorithm',$2,$3),($4,'data_structure_algorithm',$5,$6)",
		kpID, kpCode, kpName, kpSecond, kpCode+"-second", kpName+"第二")
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	var serial string
	err = pool.QueryRow(ctx, "INSERT INTO problems(id,title,statement,level,difficulty,tags,status,created_at,updated_at) VALUES ($1,$2,'Synthetic graph task','algorithm',1200,$3,'published',$4,$4) RETURNING serial_number::text",
		problemID, marker+" 混合编程题", []string{tag}, created).Scan(&serial)
	if err != nil {
		t.Fatal(err)
	}
	exec("INSERT INTO problems(id,title,statement,level,difficulty,tags,status,created_at,updated_at) VALUES ($1,$2,'Synthetic hard task','algorithm',2200,'{}','review',$3,$3)",
		hardID, marker+" 进阶编程题", created)
	for _, hidden := range []struct {
		id     uuid.UUID
		status string
	}{{quarantinedID, "quarantined"}, {rejectedID, "rejected"}} {
		exec("INSERT INTO problems(id,title,statement,level,difficulty,tags,status,created_at,updated_at) VALUES ($1,$2,'Synthetic hidden task','algorithm',1200,$3,$4,$5,$5)",
			hidden.id, marker+" 隔离拒绝题", []string{tag}, hidden.status, created)
	}
	for _, q := range []struct {
		id                                       uuid.UUID
		code, title, typ, difficulty, visibility string
	}{
		{choiceID, "X" + marker, marker + " 选择题", "choice", "easy", "public"},
		{fillID, "T" + marker, marker + " 填空题", "fill_blank", "medium", "public"},
		{judgeID, "P" + marker, marker + " 判断题", "judge", "hard", "private"},
		{legacyID, "C" + marker, marker + " 导入编程题", "programming", "medium", "public"},
		{literalID, "L" + marker, marker + " 字面%_匹配", "choice", "easy", "public"},
		{decoyID, "D" + marker, marker + " 字面XX匹配", "choice", "easy", "public"},
	} {
		exec("INSERT INTO quiz_problems(id,code,title,statement,type,code_hint,explanation,langs,difficulty,visibility,tags,subject,created_at,updated_at) VALUES ($1,$2,$3,'Synthetic quiz statement',$4,'','','{}',$5,$6,$7,'data_structure_algorithm',$8,$8)",
			q.id, q.code, q.title, q.typ, q.difficulty, q.visibility, []string{tag}, created)
	}
	exec("INSERT INTO quiz_knowledge_points(quiz_id,knowledge_point_id) VALUES ($1,$2),($1,$3),($4,$2)",
		choiceID, kpID, kpSecond, fillID)
	repo := repository.NewQuestionSearchRepository(pool)
	key := func(source string, id uuid.UUID) string { return source + "/" + id.String() }
	keys := func(items []domain.QuestionSearchItem) []string {
		result := make([]string, 0, len(items))
		for _, item := range items {
			result = append(result, key(item.Source, item.ID))
		}
		sort.Strings(result)
		return result
	}
	wantAll := []string{key("problem", problemID), key("problem", hardID), key("quiz", choiceID),
		key("quiz", fillID), key("quiz", judgeID), key("quiz", legacyID), key("quiz", literalID), key("quiz", decoyID)}
	sort.Strings(wantAll)
	check := func(t *testing.T, filter domain.QuestionSearchFilter, want []string) domain.QuestionSearchResult {
		t.Helper()
		if filter.Page == 0 {
			filter.Page = 1
		}
		if filter.Size == 0 {
			filter.Size = 100
		}
		result, err := repo.Search(ctx, filter)
		if err != nil {
			t.Fatal(err)
		}
		sortedWant := append([]string{}, want...)
		sort.Strings(sortedWant)
		if result.Total != len(want) || !reflect.DeepEqual(keys(result.Items), sortedWant) {
			t.Fatalf("search result total=%d keys=%v, want %v", result.Total, keys(result.Items), sortedWant)
		}
		return result
	}
	min, max := 1200, 1200
	for _, tc := range []struct {
		name   string
		filter domain.QuestionSearchFilter
		want   []string
	}{
		{"all kinds and source collisions", domain.QuestionSearchFilter{Keyword: marker}, wantAll},
		{"Chinese tag display name", domain.QuestionSearchFilter{Keyword: tagName}, []string{key("problem", problemID)}},
		{"tag slug across libraries", domain.QuestionSearchFilter{Keyword: marker, Tag: tag}, []string{key("problem", problemID), key("quiz", choiceID), key("quiz", fillID), key("quiz", judgeID), key("quiz", legacyID), key("quiz", literalID), key("quiz", decoyID)}},
		{"Chinese knowledge point", domain.QuestionSearchFilter{Keyword: kpName}, []string{key("quiz", choiceID), key("quiz", fillID)}},
		{"knowledge point code", domain.QuestionSearchFilter{Keyword: kpCode}, []string{key("quiz", choiceID), key("quiz", fillID)}},
		{"combined type and knowledge point", domain.QuestionSearchFilter{Keyword: marker, Type: domain.QuizTypeChoice, KnowledgePoint: kpCode}, []string{key("quiz", choiceID)}},
		{"programming includes legacy quizzes", domain.QuestionSearchFilter{Keyword: marker, Type: domain.QuizTypeProgramming}, []string{key("problem", problemID), key("problem", hardID), key("quiz", legacyID)}},
		{"numeric difficulty is inclusive and does not convert quiz difficulty", domain.QuestionSearchFilter{Keyword: marker, MinDifficulty: &min, MaxDifficulty: &max}, []string{key("problem", problemID)}},
		{"quiz difficulty does not convert numeric ratings", domain.QuestionSearchFilter{Keyword: marker, QuizDifficulty: domain.QuizDifficultyMedium}, []string{key("quiz", fillID), key("quiz", legacyID)}},
		{"mixed case quiz code", domain.QuestionSearchFilter{Keyword: strings.ToLower("X" + marker)}, []string{key("quiz", choiceID)}},
		{"UUID keeps both sources", domain.QuestionSearchFilter{Keyword: problemID.String()}, []string{key("problem", problemID), key("quiz", legacyID)}},
		{"literal SQL wildcard characters", domain.QuestionSearchFilter{Keyword: marker + " 字面%_匹配"}, []string{key("quiz", literalID)}},
		{"SQL syntax stays data", domain.QuestionSearchFilter{Keyword: "' OR true -- " + marker}, []string{}},
		{"empty result", domain.QuestionSearchFilter{Keyword: marker + "-missing"}, []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) { check(t, tc.filter, tc.want) })
	}
	t.Run("serial number search", func(t *testing.T) {
		result, err := repo.Search(ctx, domain.QuestionSearchFilter{Keyword: serial, Type: domain.QuizTypeProgramming, KnowledgePoint: tag, Page: 1, Size: 100})
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, item := range result.Items {
			if item.Source == "problem" && item.ID == problemID {
				found = true
			}
		}
		if !found {
			t.Fatalf("serial number %q did not locate the programming problem", serial)
		}
	})
	t.Run("stable pagination has one global count", func(t *testing.T) {
		var seen []string
		for page := 1; page <= 4; page++ {
			filter := domain.QuestionSearchFilter{Keyword: marker, Page: page, Size: 2}
			a, err := repo.Search(ctx, filter)
			if err != nil {
				t.Fatal(err)
			}
			b, err := repo.Search(ctx, filter)
			if err != nil {
				t.Fatal(err)
			}
			if a.Total != len(wantAll) || a.Page != page || a.Size != 2 || !reflect.DeepEqual(a, b) {
				t.Fatalf("unstable pagination: %#v / %#v", a, b)
			}
			seen = append(seen, keys(a.Items)...)
		}
		sort.Strings(seen)
		if !reflect.DeepEqual(seen, wantAll) {
			t.Fatalf("pagination duplicates or loses records: %v", seen)
		}
		beyond, err := repo.Search(ctx, domain.QuestionSearchFilter{Keyword: marker, Page: 99, Size: 2})
		if err != nil {
			t.Fatal(err)
		}
		if beyond.Total != len(wantAll) || beyond.Items == nil || len(beyond.Items) != 0 {
			t.Fatalf("empty page lost total or uses null items: %#v", beyond)
		}
	})
	t.Run("result is a summary without answer content", func(t *testing.T) {
		result := check(t, domain.QuestionSearchFilter{Keyword: marker, Type: domain.QuizTypeChoice, KnowledgePoint: kpCode}, []string{key("quiz", choiceID)})
		item := result.Items[0]
		if item.Difficulty != nil || item.QuizDifficulty != domain.QuizDifficultyEasy || len(item.KnowledgePoints) != 2 {
			t.Fatalf("wrong summary: %#v", item)
		}
		raw, err := json.Marshal(item)
		if err != nil {
			t.Fatal(err)
		}
		for _, field := range []string{"statement", "answers", "explanation", "detailed_solution", "embedding"} {
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(raw, &fields); err != nil {
				t.Fatal(err)
			}
			if _, exists := fields[field]; exists {
				t.Fatalf("search summary exposes %s", field)
			}
		}
	})
	t.Run("HTTP uses the SQL search result", func(t *testing.T) {
		e := echo.New()
		search := handler.NewQuestionSearchHandler(repo)
		e.GET("/api/v1/questions/search", search.HandleSearch)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/questions/search?q="+url.QueryEscape(marker)+"&type=fill_blank&quiz_difficulty=medium&page=1&size=2", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("HTTP status=%d body=%s", rec.Code, rec.Body.String())
		}
		var body struct {
			Success bool
			Data    []domain.QuestionSearchItem
			Meta    struct{ Total, Page, Size int }
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if !body.Success || body.Meta.Total != 1 || body.Meta.Page != 1 || body.Meta.Size != 2 || !reflect.DeepEqual(keys(body.Data), []string{key("quiz", fillID)}) {
			t.Fatalf("unexpected HTTP response %s", rec.Body.String())
		}
		empty := httptest.NewRecorder()
		e.ServeHTTP(empty, httptest.NewRequest(http.MethodGet, "/api/v1/questions/search?q="+url.QueryEscape(marker+"-missing"), nil))
		var emptyBody struct {
			Success bool
			Data    []domain.QuestionSearchItem
			Meta    map[string]int
		}
		if err := json.Unmarshal(empty.Body.Bytes(), &emptyBody); err != nil {
			t.Fatal(err)
		}
		total, totalPresent := emptyBody.Meta["total"]
		if empty.Code != http.StatusOK || !emptyBody.Success || emptyBody.Data == nil || len(emptyBody.Data) != 0 || !totalPresent || total != 0 {
			t.Fatalf("empty HTTP result must include data [] and total 0: %s", empty.Body.String())
		}
		for _, query := range []string{"type=unknown", "min_difficulty=1200&quiz_difficulty=easy", "page=-1", "size=101", "min_difficulty=2000&max_difficulty=1000"} {
			bad := httptest.NewRecorder()
			e.ServeHTTP(bad, httptest.NewRequest(http.MethodGet, "/api/v1/questions/search?"+query, nil))
			if bad.Code != http.StatusBadRequest {
				t.Errorf("query %q: status=%d body=%s", query, bad.Code, bad.Body.String())
			}
		}
	})
	t.Run("deletion is immediately reflected", func(t *testing.T) {
		exec("DELETE FROM quiz_problems WHERE id=$1", choiceID)
		check(t, domain.QuestionSearchFilter{Keyword: "X" + marker}, []string{})
		exec("DELETE FROM problems WHERE id=$1", hardID)
		check(t, domain.QuestionSearchFilter{Keyword: hardID.String()}, []string{})
	})
}
