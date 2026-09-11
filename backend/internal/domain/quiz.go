package domain

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

type QuizType string

const (
	QuizTypeProgramming QuizType = "programming"
	QuizTypeChoice      QuizType = "choice"
	QuizTypeFillBlank   QuizType = "fill_blank"
	QuizTypeJudge       QuizType = "judge"
)

func (t QuizType) IsValid() bool {
	switch t {
	case QuizTypeProgramming, QuizTypeChoice, QuizTypeFillBlank, QuizTypeJudge:
		return true
	default:
		return false
	}
}

func QuizTypeFromExcel(s string) (QuizType, error) {
	switch strings.TrimSpace(s) {
	case "编程题":
		return QuizTypeProgramming, nil
	case "选择题":
		return QuizTypeChoice, nil
	case "填空题":
		return QuizTypeFillBlank, nil
	case "判断题":
		return QuizTypeJudge, nil
	default:
		return "", fmt.Errorf("题目类型未识别: %q", s)
	}
}

func QuizTypeFromCodePrefix(s string) (QuizType, error) {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "C":
		return QuizTypeProgramming, nil
	case "X":
		return QuizTypeChoice, nil
	case "T":
		return QuizTypeFillBlank, nil
	case "P":
		return QuizTypeJudge, nil
	default:
		return "", fmt.Errorf("题目编号前缀未识别: %q", s)
	}
}

func (t QuizType) ToExcel() string {
	switch t {
	case QuizTypeProgramming:
		return "编程题"
	case QuizTypeChoice:
		return "选择题"
	case QuizTypeFillBlank:
		return "填空题"
	case QuizTypeJudge:
		return "判断题"
	default:
		return ""
	}
}

func (t QuizType) CodePrefix() string {
	switch t {
	case QuizTypeProgramming:
		return "C"
	case QuizTypeChoice:
		return "X"
	case QuizTypeFillBlank:
		return "T"
	case QuizTypeJudge:
		return "P"
	default:
		return ""
	}
}

type QuizDifficulty string

const (
	QuizDifficultyEasy   QuizDifficulty = "easy"
	QuizDifficultyMedium QuizDifficulty = "medium"
	QuizDifficultyHard   QuizDifficulty = "hard"
)

func (d QuizDifficulty) IsValid() bool {
	switch d {
	case QuizDifficultyEasy, QuizDifficultyMedium, QuizDifficultyHard:
		return true
	default:
		return false
	}
}

func QuizDifficultyFromExcel(s string) (QuizDifficulty, error) {
	switch strings.TrimSpace(s) {
	case "简单":
		return QuizDifficultyEasy, nil
	case "中等":
		return QuizDifficultyMedium, nil
	case "困难":
		return QuizDifficultyHard, nil
	default:
		return "", fmt.Errorf("难度未识别: %q", s)
	}
}

func (d QuizDifficulty) ToExcel() string {
	switch d {
	case QuizDifficultyEasy:
		return "简单"
	case QuizDifficultyMedium:
		return "中等"
	case QuizDifficultyHard:
		return "困难"
	default:
		return ""
	}
}

func (d QuizDifficulty) Score() int {
	switch d {
	case QuizDifficultyEasy:
		return 0
	case QuizDifficultyMedium:
		return 1
	case QuizDifficultyHard:
		return 2
	default:
		return -1
	}
}

type QuizVisibility string

const (
	QuizVisibilityPublic  QuizVisibility = "public"
	QuizVisibilityPrivate QuizVisibility = "private"
)

func (v QuizVisibility) IsValid() bool {
	switch v {
	case QuizVisibilityPublic, QuizVisibilityPrivate:
		return true
	default:
		return false
	}
}

func QuizVisibilityFromExcel(s string) (QuizVisibility, error) {
	switch strings.TrimSpace(s) {
	case "公开":
		return QuizVisibilityPublic, nil
	case "私有":
		return QuizVisibilityPrivate, nil
	default:
		return "", fmt.Errorf("访问类型未识别: %q", s)
	}
}

func (v QuizVisibility) ToExcel() string {
	switch v {
	case QuizVisibilityPublic:
		return "公开"
	case QuizVisibilityPrivate:
		return "私有"
	default:
		return ""
	}
}

type QuizOption struct {
	Label   string `json:"label"`
	Content string `json:"content"`
}

type QuizProblem struct {
	ID                uuid.UUID      `json:"id" db:"id"`
	Code              string         `json:"code" db:"code"`
	Title             string         `json:"title" db:"title"`
	Statement         string         `json:"statement" db:"statement"`
	Type              QuizType       `json:"type" db:"type"`
	CodeID            *int           `json:"code_id,omitempty" db:"code_id"`
	CodeHint          string         `json:"code_hint,omitempty" db:"code_hint"`
	Options           []QuizOption   `json:"options,omitempty" db:"options"`
	Answers           []string       `json:"answers" db:"answers"`
	Difficulty        QuizDifficulty `json:"difficulty" db:"difficulty"`
	Visibility        QuizVisibility `json:"visibility" db:"visibility"`
	IsVIP             bool           `json:"is_vip" db:"is_vip"`
	Tags              []string       `json:"tags" db:"tags"`
	Langs             []int          `json:"langs,omitempty" db:"langs"`
	Explanation       string         `json:"explanation,omitempty" db:"explanation"`
	KnowledgePointIDs []uuid.UUID    `json:"knowledge_point_ids,omitempty" db:"-"`
	Subject           string         `json:"subject" db:"subject"`
	CreatedAt         time.Time      `json:"created_at" db:"created_at"`
	UpdatedAt         time.Time      `json:"updated_at" db:"updated_at"`
}
