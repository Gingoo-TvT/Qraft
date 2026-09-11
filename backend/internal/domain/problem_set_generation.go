package domain

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ProblemSetGenerationConfig describes the requested output, not workflow internals.
type ProblemSetGenerationConfig struct {
	Mode         string                    `json:"mode"`
	Requirements string                    `json:"requirements"`
	Distribution []ProblemSetTypeQuota     `json:"distribution"`
	Assembly     *ProblemSetAssemblyFilter `json:"assembly,omitempty"`
}
type ProblemSetTypeQuota struct {
	Type  QuizType `json:"type"`
	Count int      `json:"count"`
	Score int      `json:"score"`
}

func (c *ProblemSetGenerationConfig) Validate(total int) error {
	if c == nil {
		return nil
	}
	if c.Mode != "programming" && c.Mode != "mixed" {
		return fmt.Errorf("请选择纯编程比赛或混合题型")
	}
	if len([]rune(c.Requirements)) > 12000 {
		return fmt.Errorf("整套需求不能超过 12000 字")
	}
	if total < 1 || total > 1000 {
		return fmt.Errorf("题数必须为 1-1000")
	}
	seen := map[QuizType]bool{}
	sum := 0
	for _, q := range c.Distribution {
		if !q.Type.IsValid() || seen[q.Type] {
			return fmt.Errorf("题型无效或重复: %s", q.Type)
		}
		seen[q.Type] = true
		if q.Count < 0 || q.Count > 1000 || q.Score < 0 || q.Score > 10000 {
			return fmt.Errorf("题型数量或分值超出范围")
		}
		if c.Mode == "programming" && q.Type != QuizTypeProgramming && q.Count != 0 {
			return fmt.Errorf("纯编程比赛不能包含客观题")
		}
		sum += q.Count
	}
	if sum != total {
		return fmt.Errorf("各题型数量之和 %d 必须等于题集题数 %d", sum, total)
	}
	if err := c.Assembly.Validate(); err != nil {
		return err
	}
	c.Requirements = strings.TrimSpace(c.Requirements)
	return nil
}

type ProblemSetGenerationRef struct {
	SetID uuid.UUID `json:"set_id"`
	RunID string    `json:"run_id"`
}
type ProblemSetGenerationState struct {
	ID                string                     `json:"id"`
	Status            string                     `json:"status"`
	ConfigFingerprint string                     `json:"config_fingerprint"`
	Brief             string                     `json:"brief,omitempty"`
	Slots             []ProblemSetGenerationSlot `json:"slots"`
	Error             string                     `json:"error,omitempty"`
	StartedAt         time.Time                  `json:"started_at"`
	UpdatedAt         time.Time                  `json:"updated_at"`
}

func (s *ProblemSetGenerationState) Active() bool {
	return s != nil && (s.Status == "queued" || s.Status == "planning" || s.Status == "generating")
}
func (s *ProblemSetGenerationState) FindSlot(position int) *ProblemSetGenerationSlot {
	for i := range s.Slots {
		if s.Slots[i].Position == position {
			return &s.Slots[i]
		}
	}
	return nil
}

type ProblemSetGenerationSlot struct {
	Position       int            `json:"position"`
	Type           QuizType       `json:"type"`
	Score          int            `json:"score"`
	Title          string         `json:"title,omitempty"`
	Brief          string         `json:"brief,omitempty"`
	Tags           []string       `json:"tags,omitempty"`
	Level          ProblemLevel   `json:"level,omitempty"`
	Difficulty     int            `json:"difficulty,omitempty"`
	QuizDifficulty QuizDifficulty `json:"quiz_difficulty,omitempty"`
	Status         string         `json:"status"`
	Regenerate     bool           `json:"regenerate,omitempty"`
	Attempt        int            `json:"attempt"`
	Fingerprint    string         `json:"fingerprint,omitempty"`
	ChildID        string         `json:"child_id,omitempty"`
	ProblemID      *uuid.UUID     `json:"problem_id,omitempty"`
	QuizID         *uuid.UUID     `json:"quiz_id,omitempty"`
	Error          string         `json:"error,omitempty"`
}
