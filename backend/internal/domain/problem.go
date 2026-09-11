package domain

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// ProblemStatus enum
// ---------------------------------------------------------------------------

// ProblemStatus tracks the lifecycle of a problem from draft to published.
type ProblemStatus string

const (
	// ProblemStatusDraft is the initial state when a problem is first created.
	ProblemStatusDraft ProblemStatus = "draft"
	// ProblemStatusGenerating means the generation workflow is running.
	ProblemStatusGenerating ProblemStatus = "generating"
	// ProblemStatusReview means the problem is awaiting human review.
	ProblemStatusReview ProblemStatus = "review"
	// ProblemStatusPublished means the problem has been approved and published.
	ProblemStatusPublished ProblemStatus = "published"
	// ProblemStatusRejected means the problem was rejected during review.
	ProblemStatusRejected ProblemStatus = "rejected"
	// ProblemStatusQuarantined means all artifacts may be present but policy or
	// integrity gates do not permit publication.
	ProblemStatusQuarantined ProblemStatus = "quarantined"
)

// IsValid reports whether the status is one of the known values.
func (s ProblemStatus) IsValid() bool {
	switch s {
	case ProblemStatusDraft, ProblemStatusGenerating, ProblemStatusReview,
		ProblemStatusPublished, ProblemStatusRejected, ProblemStatusQuarantined:
		return true
	default:
		return false
	}
}

// ---------------------------------------------------------------------------
// Problem entity
// ---------------------------------------------------------------------------

// Problem is the central domain entity representing a competitive-programming
// problem managed by AlgoForge.
type Problem struct {
	// Primary key.
	ID uuid.UUID `json:"id" db:"id"`

	// Human-readable serial number (e.g. "AF-0042").
	SerialNumber string `json:"serial_number" db:"serial_number"`

	// Title of the problem.
	Title string `json:"title" db:"title"`

	// Full problem statement (Markdown/HTML).
	Statement string `json:"statement" db:"statement"`

	// Two-tier classification: syntax or algorithm.
	Level ProblemLevel `json:"level" db:"level"`

	// Difficulty rating in the range [800, 3500], step 100.
	Difficulty int `json:"difficulty" db:"difficulty"`

	// Classification tags (e.g. ["dp", "greedy"]).
	Tags []string `json:"tags" db:"tags"`

	// Internal authoring clue. It is deliberately omitted from contestant
	// exports and from the reviewer-visible statement surface.
	OneLineHint string `json:"one_line_hint,omitempty" db:"one_line_hint"`

	// Full editorial / detailed solution text.
	DetailedSolution string `json:"detailed_solution,omitempty" db:"detailed_solution"`

	// Execution time limit in milliseconds.
	TimeLimit int `json:"time_limit" db:"time_limit"`

	// Memory limit in megabytes.
	MemoryLimit int `json:"memory_limit" db:"memory_limit"`

	// Origin or inspiration source for the problem.
	Source string `json:"source,omitempty" db:"source"`

	// Current lifecycle status.
	Status ProblemStatus `json:"status" db:"status"`

	// Foreign key to the workflow that created/manages this problem.
	WorkflowID *string `json:"workflow_id,omitempty" db:"workflow_id"`

	// Arbitrary metadata stored as raw JSON (for extensibility).
	MetadataJSON json.RawMessage `json:"metadata_json,omitempty" db:"metadata_json"`

	// Vector embedding used for similarity search.
	Embedding []float32 `json:"embedding,omitempty" db:"embedding"`

	// Timestamps.
	CreatedAt time.Time `json:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
}

// ---------------------------------------------------------------------------
// Validation
// ---------------------------------------------------------------------------

// Validate performs domain-level validation on the Problem.  It checks that
// required fields are populated and that values fall within acceptable ranges.
func (p *Problem) Validate() error {
	if p.ID == uuid.Nil {
		return fmt.Errorf("problem ID must not be nil")
	}
	if p.Title == "" {
		return fmt.Errorf("problem title must not be empty")
	}
	if p.Statement == "" {
		return fmt.Errorf("problem statement must not be empty")
	}
	if !p.Level.IsValid() {
		return fmt.Errorf("invalid problem level: %q", p.Level)
	}
	if !p.Status.IsValid() {
		return fmt.Errorf("invalid problem status: %q", p.Status)
	}

	// Difficulty range check.
	minD, maxD := DifficultyRange(p.Level)
	if p.Difficulty < minD || p.Difficulty > maxD {
		return fmt.Errorf("difficulty %d out of range [%d, %d] for level %q",
			p.Difficulty, minD, maxD, p.Level)
	}
	if p.Difficulty%100 != 0 {
		return fmt.Errorf("difficulty %d must be a multiple of 100", p.Difficulty)
	}

	// Limits sanity check.
	if p.TimeLimit <= 0 {
		return fmt.Errorf("time_limit must be positive, got %d", p.TimeLimit)
	}
	if p.MemoryLimit <= 0 {
		return fmt.Errorf("memory_limit must be positive, got %d", p.MemoryLimit)
	}

	return nil
}

// ---------------------------------------------------------------------------
// ProblemMetadata -- lightweight JSON-friendly summary
// ---------------------------------------------------------------------------

// ProblemMetadata is a lightweight struct suitable for JSON output that
// captures the most important identifying attributes of a Problem.
type ProblemMetadata struct {
	SerialNumber string       `json:"serial_number"`
	Title        string       `json:"title"`
	Level        ProblemLevel `json:"level"`
	Tags         []string     `json:"tags"`
	Difficulty   int          `json:"difficulty"`
	TimeLimit    int          `json:"time_limit"`
	MemoryLimit  int          `json:"memory_limit"`
	OneLineHint  string       `json:"one_line_hint,omitempty"`
}

// Metadata returns a ProblemMetadata snapshot of the current problem state.
func (p *Problem) Metadata() ProblemMetadata {
	return ProblemMetadata{
		SerialNumber: p.SerialNumber,
		Title:        p.Title,
		Level:        p.Level,
		Tags:         p.Tags,
		Difficulty:   p.Difficulty,
		TimeLimit:    p.TimeLimit,
		MemoryLimit:  p.MemoryLimit,
		OneLineHint:  p.OneLineHint,
	}
}

// MetadataJSONBytes returns the ProblemMetadata serialised as a JSON byte
// slice.  This is useful for storing a compact summary alongside the full
// problem record.
func (p *Problem) MetadataJSONBytes() ([]byte, error) {
	return json.Marshal(p.Metadata())
}
