package domain

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

type ProblemSetKind string

const (
	ProblemSetKindContest    ProblemSetKind = "contest"
	ProblemSetKindHomework   ProblemSetKind = "homework"
	ProblemSetKindCurriculum ProblemSetKind = "curriculum"
	ProblemSetKindMockExam   ProblemSetKind = "mock_exam"
)

func (k ProblemSetKind) IsValid() bool {
	switch k {
	case ProblemSetKindContest, ProblemSetKindHomework, ProblemSetKindCurriculum, ProblemSetKindMockExam:
		return true
	default:
		return false
	}
}

type ProblemSetVisibility string

const (
	ProblemSetVisibilityPublic  ProblemSetVisibility = "public"
	ProblemSetVisibilityPrivate ProblemSetVisibility = "private"
)

func (v ProblemSetVisibility) IsValid() bool {
	return v == ProblemSetVisibilityPublic || v == ProblemSetVisibilityPrivate
}

type ProblemSetStatus string

const (
	ProblemSetStatusDraft    ProblemSetStatus = "draft"
	ProblemSetStatusReady    ProblemSetStatus = "ready"
	ProblemSetStatusExported ProblemSetStatus = "exported"
)

func (s ProblemSetStatus) IsValid() bool {
	return s == ProblemSetStatusDraft || s == ProblemSetStatusReady || s == ProblemSetStatusExported
}

// ProblemSet is a reusable mixed contest/homework collection. New records use
// DesiredItemCount as one exact, user-entered item count. MinItemCount and
// MaxItemCount remain only as a compatibility representation for records
// created by the old 10-20 range UI.
type ProblemSet struct {
	ID                   uuid.UUID                   `json:"id"`
	Code                 string                      `json:"code"`
	Title                string                      `json:"title"`
	Description          string                      `json:"description"`
	Kind                 ProblemSetKind              `json:"kind"`
	Visibility           ProblemSetVisibility        `json:"visibility"`
	Subject              string                      `json:"subject"`
	Tags                 []string                    `json:"tags"`
	StylePrompt          string                      `json:"style_prompt"`
	DifficultyPrompt     string                      `json:"difficulty_prompt"`
	GeneratedPrompt      string                      `json:"generated_prompt,omitempty"`
	GeneratedPromptModel string                      `json:"generated_prompt_model,omitempty"`
	GeneratedPromptAt    *time.Time                  `json:"generated_prompt_at,omitempty"`
	DesiredItemCount     int                         `json:"desired_item_count"`
	MinItemCount         int                         `json:"min_item_count"`
	MaxItemCount         int                         `json:"max_item_count"`
	CooldownSets         int                         `json:"cooldown_sets"`
	TotalScore           int                         `json:"total_score"`
	Status               ProblemSetStatus            `json:"status"`
	CreatedBy            string                      `json:"created_by"`
	CreatedAt            time.Time                   `json:"created_at"`
	UpdatedAt            time.Time                   `json:"updated_at"`
	GenerationError      string                      `json:"generation_error,omitempty"`
	GenerationConfig     *ProblemSetGenerationConfig `json:"generation_config,omitempty"`
	Generation           *ProblemSetGenerationState  `json:"generation,omitempty"`
	Items                []ProblemSetItem            `json:"items,omitempty"`
	Quality              *ProblemSetQuality          `json:"quality,omitempty"`
}

type ProblemSetItem struct {
	ID                 uuid.UUID    `json:"id"`
	SetID              uuid.UUID    `json:"set_id"`
	ProblemID          *uuid.UUID   `json:"problem_id,omitempty"`
	QuizID             *uuid.UUID   `json:"quiz_id,omitempty"`
	Position           int          `json:"position"`
	Score              int          `json:"score"`
	Section            string       `json:"section"`
	Notes              string       `json:"notes"`
	CreatedAt          time.Time    `json:"created_at"`
	UpdatedAt          time.Time    `json:"updated_at"`
	Problem            *Problem     `json:"problem,omitempty"`
	Quiz               *QuizProblem `json:"quiz,omitempty"`
	KnowledgePointKeys []string     `json:"knowledge_point_keys,omitempty"`
	Fingerprint        string       `json:"fingerprint,omitempty"`
}

type ProblemSetLedgerEntry struct {
	ID                 uuid.UUID              `json:"id"`
	SetID              uuid.UUID              `json:"set_id"`
	Revision           int                    `json:"revision"`
	EventType          string                 `json:"event_type"`
	SnapshotSHA256     string                 `json:"snapshot_sha256"`
	KnowledgePointKeys []string               `json:"knowledge_point_keys"`
	ItemFingerprints   []string               `json:"item_fingerprints"`
	OverlapReport      map[string]interface{} `json:"overlap_report"`
	CreatedAt          time.Time              `json:"created_at"`
}

type ProblemSetQuality struct {
	Valid                 bool      `json:"valid"`
	ReadyForExport        bool      `json:"ready_for_export"`
	RecommendedItemCount  int       `json:"recommended_item_count"`
	ItemCount             int       `json:"item_count"`
	KnowledgePointCount   int       `json:"knowledge_point_count"`
	OverlapSetIDs         []string  `json:"overlap_set_ids,omitempty"`
	ReusedKnowledgePoints []string  `json:"reused_knowledge_points,omitempty"`
	ReusedItems           []string  `json:"reused_items,omitempty"`
	BlockingIssues        []string  `json:"blocking_issues,omitempty"`
	Warnings              []string  `json:"warnings,omitempty"`
	GeneratedAt           time.Time `json:"generated_at"`
}

// NormalizeProblemSet applies defaults and validates user-editable fields.
func (s *ProblemSet) NormalizeProblemSet() error {
	if s == nil {
		return fmt.Errorf("problem set is required")
	}
	s.Code = strings.TrimSpace(s.Code)
	s.Title = strings.TrimSpace(s.Title)
	s.Description = strings.TrimSpace(s.Description)
	s.Kind = ProblemSetKind(strings.TrimSpace(string(s.Kind)))
	s.Visibility = ProblemSetVisibility(strings.TrimSpace(string(s.Visibility)))
	s.Subject = strings.TrimSpace(s.Subject)
	s.Tags = compactUniqueStrings(s.Tags)
	s.StylePrompt = strings.TrimSpace(s.StylePrompt)
	s.DifficultyPrompt = strings.TrimSpace(s.DifficultyPrompt)
	s.GeneratedPrompt = strings.TrimSpace(s.GeneratedPrompt)
	s.GeneratedPromptModel = strings.TrimSpace(s.GeneratedPromptModel)
	if s.Code == "" {
		return fmt.Errorf("code is required")
	}
	if len(s.Code) > 64 {
		return fmt.Errorf("code must be at most 64 characters")
	}
	for _, r := range s.Code {
		if !(r == '-' || r == '_' || r >= '0' && r <= '9' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z') {
			return fmt.Errorf("code contains unsupported characters")
		}
	}
	if s.Title == "" {
		return fmt.Errorf("title is required")
	}
	if len([]rune(s.Title)) > 255 {
		return fmt.Errorf("title must be at most 255 characters")
	}
	if !s.Kind.IsValid() {
		return fmt.Errorf("invalid kind %q", s.Kind)
	}
	if !s.Visibility.IsValid() {
		return fmt.Errorf("invalid visibility %q", s.Visibility)
	}
	if s.DesiredItemCount < 0 || s.DesiredItemCount > MaxProblemSetItemCount {
		return fmt.Errorf("desired_item_count must be between 0 and %d", MaxProblemSetItemCount)
	}
	// A positive desired count without legacy bounds is the new exact-count
	// form. Collapse the compatibility columns so quality/export checks use
	// one unambiguous target.
	if s.DesiredItemCount > 0 && s.MinItemCount == 0 && s.MaxItemCount == 0 {
		s.MinItemCount = s.DesiredItemCount
		s.MaxItemCount = s.DesiredItemCount
	}
	// Zero desired plus omitted bounds is retained for old API clients and
	// existing rows that used the historical automatic 10-20 range.
	if s.MinItemCount == 0 {
		s.MinItemCount = 10
	}
	if s.MaxItemCount == 0 {
		s.MaxItemCount = 20
	}
	if s.MinItemCount < 1 || s.MinItemCount > MaxProblemSetItemCount ||
		s.MaxItemCount < 1 || s.MaxItemCount > MaxProblemSetItemCount ||
		s.MinItemCount > s.MaxItemCount {
		return fmt.Errorf("item count range must be within 1..%d", MaxProblemSetItemCount)
	}
	if s.DesiredItemCount != 0 && (s.DesiredItemCount < s.MinItemCount || s.DesiredItemCount > s.MaxItemCount) {
		return fmt.Errorf("desired_item_count must be inside item count range")
	}
	if s.CooldownSets < 0 || s.CooldownSets > 50 {
		return fmt.Errorf("cooldown_sets must be between 0 and 50")
	}
	if s.TotalScore < 0 {
		return fmt.Errorf("total_score must not be negative")
	}
	if s.Status == "" {
		s.Status = ProblemSetStatusDraft
	}
	if !s.Status.IsValid() {
		return fmt.Errorf("invalid status %q", s.Status)
	}
	return nil
}

// MaxProblemSetItemCount bounds a directly entered contest size while leaving
// enough room for larger custom training sets. It is deliberately independent
// from the 10-20 per-problem test-point range.
const MaxProblemSetItemCount = 1000

func compactUniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
