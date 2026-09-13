package domain

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// QuestionSearchItem keeps each source's identity and difficulty scale. Source,
// not Type, determines whether the detail belongs to /problems or /quizzes.
type QuestionSearchItem struct {
	ID              uuid.UUID      `json:"id"`
	Source          string         `json:"source"`
	Type            QuizType       `json:"type"`
	Code            string         `json:"code"`
	Title           string         `json:"title"`
	Tags            []string       `json:"tags"`
	KnowledgePoints []string       `json:"knowledge_points"`
	Difficulty      *int           `json:"difficulty,omitempty"`
	QuizDifficulty  QuizDifficulty `json:"quiz_difficulty,omitempty"`
	Level           string         `json:"level,omitempty"`
	Status          string         `json:"status,omitempty"`
	UpdatedAt       time.Time      `json:"updated_at"`
}

type QuestionSearchFilter struct {
	Keyword        string
	Type           QuizType
	Tag            string
	KnowledgePoint string
	MinDifficulty  *int
	MaxDifficulty  *int
	QuizDifficulty QuizDifficulty
	Page           int
	Size           int
}

type QuestionSearchResult struct {
	Items []QuestionSearchItem
	Total int
	Page  int
	Size  int
}

// Normalize applies bounded pagination and rejects mixed difficulty scales.
// Text filters are literal substrings: SQL wildcard syntax has no special role.
func (f QuestionSearchFilter) Normalize() (QuestionSearchFilter, error) {
	f.Keyword = strings.TrimSpace(f.Keyword)
	f.Tag = strings.TrimSpace(f.Tag)
	f.KnowledgePoint = strings.TrimSpace(f.KnowledgePoint)
	for _, text := range []string{f.Keyword, f.Tag, f.KnowledgePoint} {
		if !utf8.ValidString(text) || strings.ContainsRune(text, 0) {
			return f, fmt.Errorf("搜索条件包含无效字符")
		}
	}
	if utf8.RuneCountInString(f.Keyword) > 200 || utf8.RuneCountInString(f.Tag) > 200 || utf8.RuneCountInString(f.KnowledgePoint) > 200 {
		return f, fmt.Errorf("搜索词、标签和知识点不能超过 200 个字符")
	}
	if f.Type != "" && !f.Type.IsValid() {
		return f, fmt.Errorf("题型无效")
	}
	if f.QuizDifficulty != "" && !f.QuizDifficulty.IsValid() {
		return f, fmt.Errorf("客观题难度无效")
	}
	numeric := f.MinDifficulty != nil || f.MaxDifficulty != nil
	if numeric && f.QuizDifficulty != "" {
		return f, fmt.Errorf("编程题评分与客观题难度不能同时筛选")
	}
	for _, value := range []*int{f.MinDifficulty, f.MaxDifficulty} {
		if value != nil && (*value < 800 || *value > 3500) {
			return f, fmt.Errorf("编程题难度须在 800 至 3500 之间")
		}
	}
	if f.MinDifficulty != nil && f.MaxDifficulty != nil && *f.MinDifficulty > *f.MaxDifficulty {
		return f, fmt.Errorf("最低难度不能高于最高难度")
	}
	if f.Page == 0 {
		f.Page = 1
	}
	if f.Size == 0 {
		f.Size = 20
	}
	if f.Page < 1 || f.Page > 1000000 || f.Size < 1 || f.Size > 100 {
		return f, fmt.Errorf("页码须在 1 至 1000000 之间，每页条数须在 1 至 100 之间")
	}
	return f, nil
}
