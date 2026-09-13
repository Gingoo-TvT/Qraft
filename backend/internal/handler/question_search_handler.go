package handler

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/labstack/echo/v4"
)

type questionSearcher interface {
	Search(context.Context, domain.QuestionSearchFilter) (domain.QuestionSearchResult, error)
}

type QuestionSearchHandler struct{ repo questionSearcher }

func NewQuestionSearchHandler(repo questionSearcher) *QuestionSearchHandler {
	return &QuestionSearchHandler{repo: repo}
}

func (h *QuestionSearchHandler) HandleSearch(c echo.Context) error {
	filter, err := questionSearchFilterFromQuery(c)
	if err != nil {
		return badRequest(c, "INVALID_PARAM", err.Error())
	}
	result, err := h.repo.Search(c.Request().Context(), filter)
	if err != nil {
		c.Logger().Errorf("question search: %v", err)
		return internalError(c, "题目搜索失败，请稍后重试")
	}
	if result.Items == nil {
		result.Items = []domain.QuestionSearchItem{}
	}
	// Total is explicit even at zero, unlike the optional generic Meta fields.
	return c.JSON(http.StatusOK, map[string]interface{}{
		"success": true, "data": result.Items,
		"meta": map[string]int{"total": result.Total, "page": result.Page, "size": result.Size},
	})
}

func questionSearchFilterFromQuery(c echo.Context) (domain.QuestionSearchFilter, error) {
	filter := domain.QuestionSearchFilter{
		Keyword: c.QueryParam("q"),
		Type:    domain.QuizType(strings.TrimSpace(c.QueryParam("type"))),
		Tag:     c.QueryParam("tag"), KnowledgePoint: c.QueryParam("knowledge_point"),
		QuizDifficulty: domain.QuizDifficulty(strings.TrimSpace(c.QueryParam("quiz_difficulty"))),
		Page:           1, Size: 20,
	}
	for _, param := range []struct {
		name   string
		target **int
	}{
		{"min_difficulty", &filter.MinDifficulty}, {"max_difficulty", &filter.MaxDifficulty},
	} {
		if raw, exists := c.QueryParams()[param.name]; exists {
			if len(raw) != 1 {
				return filter, fmt.Errorf("%s 只能指定一次", param.name)
			}
			value, err := strconv.Atoi(strings.TrimSpace(raw[0]))
			if err != nil {
				return filter, fmt.Errorf("%s 须为整数", param.name)
			}
			*param.target = &value
		}
	}
	for _, param := range []struct {
		name   string
		target *int
	}{
		{"page", &filter.Page}, {"size", &filter.Size},
	} {
		if raw, exists := c.QueryParams()[param.name]; exists {
			if len(raw) != 1 {
				return filter, fmt.Errorf("%s 只能指定一次", param.name)
			}
			value, err := strconv.Atoi(strings.TrimSpace(raw[0]))
			if err != nil || value < 1 {
				return filter, fmt.Errorf("%s 须为正整数", param.name)
			}
			*param.target = value
		}
	}
	return filter.Normalize()
}
