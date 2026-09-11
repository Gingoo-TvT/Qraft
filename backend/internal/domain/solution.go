package domain

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// SolutionType enum
// ---------------------------------------------------------------------------

// SolutionType distinguishes between the different kinds of solution source
// files that can be associated with a problem.
type SolutionType string

const (
	// SolutionTypeMain is the intended (model) solution.
	SolutionTypeMain SolutionType = "main"
	// SolutionTypeBrute is a brute-force reference solution used for stress
	// testing.
	SolutionTypeBrute SolutionType = "brute"
	// SolutionTypeGenerator is the test-data generator program.
	SolutionTypeGenerator SolutionType = "generator"
	// SolutionTypeChecker is a custom checker/interactor program.
	SolutionTypeChecker SolutionType = "checker"
)

// IsValid reports whether the solution type is one of the known values.
func (st SolutionType) IsValid() bool {
	switch st {
	case SolutionTypeMain, SolutionTypeBrute, SolutionTypeGenerator, SolutionTypeChecker:
		return true
	default:
		return false
	}
}

// ---------------------------------------------------------------------------
// Solution entity
// ---------------------------------------------------------------------------

// Solution represents a source-code artefact linked to a Problem.  Every
// problem may have multiple solutions of different types (model solution,
// brute-force, generator, checker).
type Solution struct {
	// Primary key.
	ID uuid.UUID `json:"id" db:"id"`

	// Foreign key referencing the parent Problem.
	ProblemID uuid.UUID `json:"problem_id" db:"problem_id"`

	// The role this solution plays (main, brute, generator, checker).
	SolutionType SolutionType `json:"solution_type" db:"solution_type"`

	// Programming language identifier (e.g. "cpp", "python3", "java").
	Language string `json:"language" db:"language"`

	// The full source code of the solution.
	SourceCode string `json:"source_code" db:"source_code"`

	// Compile status reported by the sandbox (e.g. "success", "error").
	CompileStatus string `json:"compile_status,omitempty" db:"compile_status"`

	// Timestamp of creation.
	CreatedAt time.Time `json:"created_at" db:"created_at"`
}

// ---------------------------------------------------------------------------
// Validation
// ---------------------------------------------------------------------------

// Validate performs domain-level validation on the Solution.
func (s *Solution) Validate() error {
	if s.ID == uuid.Nil {
		return fmt.Errorf("solution ID must not be nil")
	}
	if s.ProblemID == uuid.Nil {
		return fmt.Errorf("solution problem_id must not be nil")
	}
	if !s.SolutionType.IsValid() {
		return fmt.Errorf("invalid solution type: %q", s.SolutionType)
	}
	if s.Language == "" {
		return fmt.Errorf("solution language must not be empty")
	}
	if s.SourceCode == "" {
		return fmt.Errorf("solution source_code must not be empty")
	}
	return nil
}
