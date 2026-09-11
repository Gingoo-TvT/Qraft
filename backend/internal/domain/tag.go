// Package domain defines the core entities and value objects for the AlgoForge
// problem generation system. These types are independent of any infrastructure
// or delivery mechanism.
package domain

import "fmt"

// ---------------------------------------------------------------------------
// ProblemLevel -- two-tier classification
// ---------------------------------------------------------------------------

// ProblemLevel represents the two-tier classification used to distinguish
// between syntax-oriented and algorithm-oriented problems.
type ProblemLevel string

const (
	// LevelSyntax indicates a syntax-level problem (difficulty 800-1200).
	LevelSyntax ProblemLevel = "syntax"
	// LevelAlgorithm indicates an algorithm-level problem (difficulty 800-3500).
	LevelAlgorithm ProblemLevel = "algorithm"
	// LevelGPLTL1 — 团体程序设计天梯赛 L1 (基础编程题，总分 20).
	LevelGPLTL1 ProblemLevel = "gplt_l1"
	// LevelGPLTL2 — 团体程序设计天梯赛 L2 (进阶算法题，总分 25).
	LevelGPLTL2 ProblemLevel = "gplt_l2"
	// LevelGPLTL3 — 团体程序设计天梯赛 L3 (综合难题，总分 30).
	LevelGPLTL3 ProblemLevel = "gplt_l3"
)

// IsValid reports whether the level is one of the known values.
func (l ProblemLevel) IsValid() bool {
	switch l {
	case LevelSyntax, LevelAlgorithm, LevelGPLTL1, LevelGPLTL2, LevelGPLTL3:
		return true
	default:
		return false
	}
}

// IsGPLT reports whether the level belongs to the GPLT (团体程序设计天梯赛)
// family.
func (l ProblemLevel) IsGPLT() bool {
	switch l {
	case LevelGPLTL1, LevelGPLTL2, LevelGPLTL3:
		return true
	default:
		return false
	}
}

// ---------------------------------------------------------------------------
// TagCategory
// ---------------------------------------------------------------------------

// TagCategory represents a tag in the classification system.  Tags belong to a
// specific level (syntax or algorithm) and carry a human-readable display name
// together with an optional description.
type TagCategory struct {
	ID            int          `json:"id" db:"id"`
	Level         ProblemLevel `json:"level" db:"level"`
	TagName       string       `json:"tag_name" db:"tag_name"`
	DisplayName   string       `json:"display_name" db:"display_name"`
	Description   string       `json:"description,omitempty" db:"description"`
	SortOrder     int          `json:"sort_order" db:"sort_order"`
	MinDifficulty int          `json:"min_difficulty" db:"min_difficulty"`
	MaxDifficulty int          `json:"max_difficulty" db:"max_difficulty"`
}

// ---------------------------------------------------------------------------
// Tag helpers
// ---------------------------------------------------------------------------

// ValidateTags checks that every tag in tags belongs to the specified level
// according to validTags.  It returns an error listing any invalid tags.
func ValidateTags(level ProblemLevel, tags []string, validTags []TagCategory) error {
	// Build a set of valid tag names for the requested level.
	allowed := make(map[string]struct{}, len(validTags))
	for _, t := range validTags {
		if t.Level == level {
			allowed[t.TagName] = struct{}{}
		}
	}

	var invalid []string
	for _, tag := range tags {
		if _, ok := allowed[tag]; !ok {
			invalid = append(invalid, tag)
		}
	}

	if len(invalid) > 0 {
		return fmt.Errorf("tags %v are not valid for level %q", invalid, level)
	}
	return nil
}

// DifficultyRange returns the inclusive [min, max] difficulty range that is
// valid for the given problem level.  Unknown levels fall back to the widest
// range.
func DifficultyRange(level ProblemLevel) (min, max int) {
	switch level {
	case LevelSyntax:
		return 800, 1200
	case LevelAlgorithm:
		return 1300, 3500
	case LevelGPLTL1:
		return 800, 1300
	case LevelGPLTL2:
		// L2 overlaps with L1 by design — real 天梯赛 L2 problems vary widely
		// in implementation difficulty; some are conceptually harder (data
		// structures / algorithms) but no harder to code than an L1 simulation.
		return 1000, 1800
	case LevelGPLTL3:
		return 1700, 2600
	default:
		return 800, 3500
	}
}

// IsTagValidForDifficulty checks whether a tag is appropriate for the given
// difficulty rating based on its min/max difficulty range.
func IsTagValidForDifficulty(tag TagCategory, difficulty int) bool {
	return difficulty >= tag.MinDifficulty && difficulty <= tag.MaxDifficulty
}

// FilterTagsByDifficulty returns only those tags whose difficulty range
// includes the specified difficulty rating.
func FilterTagsByDifficulty(tags []TagCategory, difficulty int) []TagCategory {
	filtered := make([]TagCategory, 0, len(tags))
	for _, t := range tags {
		if IsTagValidForDifficulty(t, difficulty) {
			filtered = append(filtered, t)
		}
	}
	return filtered
}
