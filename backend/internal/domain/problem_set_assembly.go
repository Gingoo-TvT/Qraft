package domain

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Assembly filters share the existing set configuration; selecting existing
// questions needs no additional workflow or persistence lifecycle.
type ProblemSetAssemblyFilter struct {
	Tags              []string       `json:"tags"`
	Keyword           string         `json:"keyword"`
	MinDifficulty     int            `json:"min_difficulty"`
	MaxDifficulty     int            `json:"max_difficulty"`
	QuizDifficulty    QuizDifficulty `json:"quiz_difficulty"`
	ExcludeRecentSets int            `json:"exclude_recent_sets"`
	Seed              string         `json:"seed"`
}

func (f *ProblemSetAssemblyFilter) Validate() error {
	if f == nil {
		return nil
	}
	if f.MinDifficulty == 0 {
		f.MinDifficulty = 800
	}
	if f.MaxDifficulty == 0 {
		f.MaxDifficulty = 3500
	}
	if f.MinDifficulty < 800 || f.MaxDifficulty > 3500 || f.MinDifficulty > f.MaxDifficulty ||
		f.MinDifficulty%100 != 0 || f.MaxDifficulty%100 != 0 {
		return fmt.Errorf("编程题难度须在 800-3500 内，按 100 递增，且下限不能大于上限")
	}
	if f.QuizDifficulty != "" && !f.QuizDifficulty.IsValid() {
		return fmt.Errorf("客观题难度无效")
	}
	if f.ExcludeRecentSets < 0 || f.ExcludeRecentSets > 50 {
		return fmt.Errorf("回看题集数须为 0-50")
	}
	f.Keyword = strings.TrimSpace(f.Keyword)
	if len([]rune(f.Keyword)) > 200 || len(f.Seed) > 128 || len(f.Tags) > 50 {
		return fmt.Errorf("组卷筛选条件过长")
	}
	tags := make([]string, 0, len(f.Tags))
	seen := map[string]bool{}
	for _, tag := range f.Tags {
		tag = strings.ToLower(strings.TrimSpace(tag))
		if len([]rune(tag)) > 100 {
			return fmt.Errorf("标签长度不能超过 100 字")
		}
		if tag != "" && !seen[tag] {
			tags = append(tags, tag)
			seen[tag] = true
		}
	}
	f.Tags = tags
	return nil
}

type ProblemSetAssemblyRef struct {
	ID        uuid.UUID `json:"id"`
	Type      QuizType  `json:"type"`
	UpdatedAt time.Time `json:"updated_at"`
}

// A preview transfers metadata, never full solutions or test data.
type ProblemSetAssemblyCandidate struct {
	ProblemSetAssemblyRef
	Code           string         `json:"code"`
	Title          string         `json:"title"`
	Difficulty     int            `json:"difficulty,omitempty"`
	QuizDifficulty QuizDifficulty `json:"quiz_difficulty,omitempty"`
	Tags           []string       `json:"tags"`
	Fingerprint    string         `json:"-"`
	Score          int            `json:"score"`
}
type ProblemSetAssemblyQuotaResult struct {
	Type      QuizType `json:"type"`
	Requested int      `json:"requested"`
	Available int      `json:"available"`
	Selected  int      `json:"selected"`
	Missing   int      `json:"missing"`
}
type ProblemSetAssemblyPreview struct {
	Filter       ProblemSetAssemblyFilter        `json:"filter"`
	Items        []ProblemSetAssemblyCandidate   `json:"items"`
	Distribution []ProblemSetAssemblyQuotaResult `json:"distribution"`
	TotalScore   int                             `json:"total_score"`
	MissingCount int                             `json:"missing_count"`
}
