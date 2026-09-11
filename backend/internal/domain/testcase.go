package domain

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// TestCase entity
// ---------------------------------------------------------------------------

// TestCase represents a single input/output test for a Problem.  Test cases
// are organised into groups and may be flagged as sample cases that are shown
// to contestants.
type TestCase struct {
	// Primary key.
	ID uuid.UUID `json:"id" db:"id"`

	// Foreign key referencing the parent Problem.
	ProblemID uuid.UUID `json:"problem_id" db:"problem_id"`

	// Zero-based index of this test case within the problem.
	TestIndex int `json:"test_index" db:"test_index"`

	// Logical group this test case belongs to (for subtask scoring).
	GroupID int `json:"group_id" db:"group_id"`

	// Whether this test case is displayed to contestants as a sample.
	IsSample bool `json:"is_sample" db:"is_sample"`

	// Path to the input file on the storage backend.
	InputPath string `json:"input_path" db:"input_path"`

	// Path to the expected-output file on the storage backend.
	OutputPath string `json:"output_path" db:"output_path"`

	// Score awarded for passing this test case (relevant in subtask scoring).
	Score int `json:"score" db:"score"`

	// Optional human-readable description of what this test case covers.
	Description string `json:"description,omitempty" db:"description"`

	// Timestamp of creation.
	CreatedAt time.Time `json:"created_at" db:"created_at"`
}

// Validate performs domain-level validation on the TestCase.
func (tc *TestCase) Validate() error {
	if tc.ID == uuid.Nil {
		return fmt.Errorf("testcase ID must not be nil")
	}
	if tc.ProblemID == uuid.Nil {
		return fmt.Errorf("testcase problem_id must not be nil")
	}
	if tc.TestIndex < 0 {
		return fmt.Errorf("testcase test_index must be non-negative, got %d", tc.TestIndex)
	}
	if tc.InputPath == "" {
		return fmt.Errorf("testcase input_path must not be empty")
	}
	if tc.OutputPath == "" {
		return fmt.Errorf("testcase output_path must not be empty")
	}
	return nil
}

// ---------------------------------------------------------------------------
// Test data configuration types
// ---------------------------------------------------------------------------

// ConstraintRange describes the lower and upper bounds of a single constraint
// parameter.  Min and Max are interface{} to support both integer and
// floating-point bounds.
type ConstraintRange struct {
	Min interface{} `json:"min"`
	Max interface{} `json:"max"`
}

// TestGroup defines a group of test cases that share the same constraint
// profile and scoring weight.  This maps naturally to the "subtask" concept
// used by many competitive-programming judges.
type TestGroup struct {
	// Unique identifier for the group within a TestDataConfig.
	GroupID int `json:"group_id"`

	// Number of test cases to generate for this group.
	NumCases int `json:"num_cases"`

	// Score awarded to a contestant who passes all cases in the group.
	Score int `json:"score"`

	// Constraint overrides keyed by parameter name (e.g. "n", "m").
	Constraints map[string]ConstraintRange `json:"constraints,omitempty"`

	// Human-readable description of the group's purpose.
	Description string `json:"description,omitempty"`

	// BruteCheck reports whether this group's test cases should be
	// cross-validated against the brute-force reference solution. Typically
	// only small/medium groups are brute-checked: running an O(n^3) brute on
	// extreme-scale data wastes wall-clock time and gives no additional
	// signal (the main solution is what we ship for those cases anyway).
	//
	// A config with no group marked BruteCheck uses a conservative compatibility
	// fallback (samples and tiny inline cases).  It must not promote a missing
	// group annotation into a full-suite brute run, because the reference
	// implementation is intentionally allowed to be much slower than the main
	// solution.
	BruteCheck bool `json:"brute_check,omitempty"`
}

// BoundaryConfig controls whether the generator should include special
// boundary / edge-case tests.
type BoundaryConfig struct {
	// Generate a test case using minimum constraint values.
	IncludeMinCase bool `json:"include_min_case"`

	// Generate a test case using maximum constraint values.
	IncludeMaxCase bool `json:"include_max_case"`

	// Generate a test case that includes zero-valued inputs where applicable.
	IncludeZero bool `json:"include_zero"`
}

// CustomTestCase is a hand-crafted test case supplied directly in the
// configuration rather than being generated.
type CustomTestCase struct {
	// Literal input data.
	Input string `json:"input"`

	// Human-readable note explaining why this case is included.
	Description string `json:"description,omitempty"`

	// Whether this custom case should be presented as a sample to contestants.
	IsSample bool `json:"is_sample"`
}

// TestDataConfig is the top-level configuration that drives test-data
// generation for a problem. A zero NumTestCases selects adaptive mode: the
// model enumerates the applicable corner cases and chooses the smallest
// sufficient final suite in MinTestCases..MaxTestCases. Positive counts remain
// supported for replay and older callers.
type TestDataConfig struct {
	// Total number of test cases to generate (across all groups). Zero means
	// that the model chooses the final count from the adaptive bounds below.
	NumTestCases int `json:"num_test_cases"`

	// Inclusive bounds for adaptive selection. They default to 10 and 20.
	MinTestCases int `json:"min_test_cases,omitempty"`
	MaxTestCases int `json:"max_test_cases,omitempty"`

	// AutoCaseCount is a deprecated compatibility alias used by v1.3.1
	// callers. New requests must use NumTestCases == 0 and the explicit bounds.
	AutoCaseCount bool `json:"auto_case_count,omitempty"`

	// How many of NumTestCases should be flagged as samples.
	NumSamples int `json:"num_samples"`

	// Grouped test-case definitions (for subtask-style scoring).
	Groups []TestGroup `json:"groups,omitempty"`

	// Boundary / edge-case generation settings.
	BoundaryConfig BoundaryConfig `json:"boundary_config"`

	// Hand-crafted test cases to include verbatim.
	CustomCases []CustomTestCase `json:"custom_cases,omitempty"`
}

// MaxGeneratedTestCases matches the worker's current single-batch solution
// execution contract: 2 MiB per case within the sandbox's 64 MiB raw output
// budget. Generator execution itself is chunked, but judged solution results
// intentionally retain one audit identity and therefore remain one batch.
const MaxGeneratedTestCases = 32

// MinAdaptiveTestCases and MaxAdaptiveTestCases bound the product's self-sized
// test suite. The wider MaxGeneratedTestCases limit remains the
// compatibility/sandbox ceiling for explicit counts.
const (
	MinAdaptiveTestCases = 10
	MaxAdaptiveTestCases = 20
	// Deprecated aliases retained for integrations compiled against v1.3.1.
	AdaptiveMinTestCases = MinAdaptiveTestCases
	AdaptiveMaxTestCases = MaxAdaptiveTestCases
)

// IsAdaptive reports whether the model is expected to choose the final count.
// AutoCaseCount is honored only as a backwards-compatible alias.
func (c TestDataConfig) IsAdaptive() bool {
	return c.NumTestCases == 0 || c.AutoCaseCount
}

// NormalizeForGeneration fills adaptive defaults and validates the count
// contract. It intentionally mutates the receiver so callers can pass the
// normalized bounds to downstream activities and provenance.
func (c *TestDataConfig) NormalizeForGeneration() error {
	if c == nil {
		return fmt.Errorf("test data config is required")
	}
	if c.NumTestCases < 0 {
		return fmt.Errorf("num_test_cases must not be negative, got %d", c.NumTestCases)
	}
	if c.NumSamples < 0 {
		return fmt.Errorf("num_samples must not be negative, got %d", c.NumSamples)
	}
	if c.AutoCaseCount && c.NumTestCases > 0 {
		// v1.3.1 used NumTestCases as an adaptive capacity. Preserve that
		// interpretation for old payloads while all new payloads use zero.
		// Do not silently clamp an out-of-contract capacity: callers must see a
		// fail-closed validation error instead of an unexpected smaller suite.
		if c.NumTestCases < MinAdaptiveTestCases || c.NumTestCases > MaxAdaptiveTestCases {
			return fmt.Errorf("adaptive test data count capacity must be in [%d,%d], got %d", MinAdaptiveTestCases, MaxAdaptiveTestCases, c.NumTestCases)
		}
		max := c.NumTestCases
		c.NumTestCases = 0
		c.MinTestCases = MinAdaptiveTestCases
		c.MaxTestCases = max
	}
	if c.NumTestCases == 0 {
		if c.MinTestCases == 0 {
			c.MinTestCases = MinAdaptiveTestCases
		}
		if c.MaxTestCases == 0 {
			c.MaxTestCases = MaxAdaptiveTestCases
		}
		if c.MinTestCases < MinAdaptiveTestCases ||
			c.MaxTestCases > MaxAdaptiveTestCases ||
			c.MinTestCases > c.MaxTestCases {
			return fmt.Errorf("adaptive test-case range must be within [%d,%d]", MinAdaptiveTestCases, MaxAdaptiveTestCases)
		}
		if c.NumSamples > c.MaxTestCases {
			return fmt.Errorf("num_samples must not exceed adaptive max test cases %d, got %d", c.MaxTestCases, c.NumSamples)
		}
		if len(c.CustomCases) > c.MaxTestCases {
			return fmt.Errorf("custom case count %d exceeds adaptive max test cases %d", len(c.CustomCases), c.MaxTestCases)
		}
		return nil
	}
	if c.NumTestCases > MaxGeneratedTestCases {
		return fmt.Errorf("num_test_cases must not exceed %d, got %d", MaxGeneratedTestCases, c.NumTestCases)
	}
	// A positive explicit count is authoritative. Bounds copied from the
	// adaptive UI are stale and must not alter replay semantics.
	c.MinTestCases = 0
	c.MaxTestCases = 0
	if c.NumSamples > c.NumTestCases {
		return fmt.Errorf("num_samples must not exceed num_test_cases %d, got %d", c.NumTestCases, c.NumSamples)
	}
	return nil
}

// AdaptiveCaseCountRange is retained for v1.3.1 callers. New code should use
// EffectiveTestCaseRange after NormalizeForGeneration.
func (c TestDataConfig) AdaptiveCaseCountRange() (int, int) {
	if c.NumTestCases == 0 {
		min, max, _ := c.EffectiveTestCaseRange()
		return min, max
	}
	min := MinAdaptiveTestCases
	// Samples and caller-supplied cases are hard members of the final suite;
	// an adaptive run must therefore leave room for both before the model picks
	// its corner-case set.
	if c.NumSamples > min {
		min = c.NumSamples
	}
	if len(c.CustomCases) > min {
		min = len(c.CustomCases)
	}
	max := c.NumTestCases
	if max > MaxAdaptiveTestCases {
		max = MaxAdaptiveTestCases
	}
	return min, max
}

// EffectiveTestCaseRange returns the final count range and whether it is
// adaptive. It accepts both the new zero-count form and the old alias.
func (c TestDataConfig) EffectiveTestCaseRange() (min, max int, adaptive bool) {
	if c.NumTestCases > 0 && !c.AutoCaseCount {
		return c.NumTestCases, c.NumTestCases, false
	}
	if c.NumTestCases > 0 && c.AutoCaseCount {
		min, max = c.AdaptiveCaseCountRange()
		return min, max, true
	}
	min, max = c.MinTestCases, c.MaxTestCases
	if min == 0 {
		min = MinAdaptiveTestCases
	}
	if max == 0 {
		max = MaxAdaptiveTestCases
	}
	// Samples and caller-supplied cases are members of the final suite, so an
	// adaptive run cannot select fewer total cases than either requirement.
	if c.NumSamples > min {
		min = c.NumSamples
	}
	if len(c.CustomCases) > min {
		min = len(c.CustomCases)
	}
	return min, max, true
}

// ValidateGeneratedTestCaseCount checks the final count after custom cases
// and generator execution. Adaptive requests fail closed outside their range;
// explicit legacy requests retain their historical upper-bound behavior.
func (c TestDataConfig) ValidateGeneratedTestCaseCount(actual int) error {
	min, max, adaptive := c.EffectiveTestCaseRange()
	if actual <= 0 {
		return fmt.Errorf("generated test-case count must be positive")
	}
	if adaptive {
		if actual < min || actual > max {
			return fmt.Errorf("generated test-case count %d is outside adaptive range [%d,%d]", actual, min, max)
		}
		return nil
	}
	if actual > max {
		return fmt.Errorf("generated test-case count %d exceeds requested count %d", actual, max)
	}
	return nil
}

// Validate checks the count invariants without changing the caller's value.
// Explicit-count configurations retain their v1.3.1 behavior.
func (c TestDataConfig) Validate() error {
	if c.AutoCaseCount && c.NumTestCases > 0 {
		min, max := c.AdaptiveCaseCountRange()
		if c.NumTestCases < MinAdaptiveTestCases || c.NumTestCases > MaxAdaptiveTestCases {
			return fmt.Errorf("adaptive test data count capacity must be in [%d,%d], got %d", MinAdaptiveTestCases, MaxAdaptiveTestCases, c.NumTestCases)
		}
		if len(c.CustomCases) > max {
			return fmt.Errorf("adaptive custom case count %d exceeds capacity %d", len(c.CustomCases), max)
		}
		if max < min {
			return fmt.Errorf("adaptive test data count range is empty: [%d,%d]", min, max)
		}
		if c.NumSamples < 0 || c.NumSamples > max {
			return fmt.Errorf("adaptive sample count must be in [0,%d], got %d", max, c.NumSamples)
		}
		return nil
	}
	copy := c
	return copy.NormalizeForGeneration()
}

// ---------------------------------------------------------------------------
// Factory / default helpers
// ---------------------------------------------------------------------------

// BruteCheckGroupIDs returns the set of group IDs whose test cases should be
// cross-validated against the brute-force solution. If no group is marked,
// returns nil (callers should use a conservative samples/tiny-input fallback).
func (c TestDataConfig) BruteCheckGroupIDs() map[int]bool {
	var anyMarked bool
	for _, g := range c.Groups {
		if g.BruteCheck {
			anyMarked = true
			break
		}
	}
	if !anyMarked {
		return nil
	}
	set := make(map[int]bool, len(c.Groups))
	for _, g := range c.Groups {
		if g.BruteCheck {
			set[g.GroupID] = true
		}
	}
	return set
}

// DefaultTestDataConfig returns a sensible default TestDataConfig suitable
// for a standard competitive-programming problem with a single group and
// typical boundary settings.
func DefaultTestDataConfig() TestDataConfig {
	return TestDataConfig{
		NumTestCases: 0,
		MinTestCases: 10,
		MaxTestCases: 20,
		NumSamples:   2,
		Groups: []TestGroup{
			{
				GroupID:     1,
				NumCases:    0,
				Score:       100,
				Description: "由模型按不同 corner case 分配，不是固定数量",
			},
		},
		BoundaryConfig: BoundaryConfig{
			IncludeMinCase: true,
			IncludeMaxCase: true,
			IncludeZero:    false,
		},
	}
}

// SubtaskTestDataConfig returns a TestDataConfig pre-configured for a subtask
// scoring model with the given number of groups.  Each group receives an equal
// share of the total score and an equal share of the total test cases.
func SubtaskTestDataConfig(numGroups, totalCases, totalScore int) TestDataConfig {
	if numGroups <= 0 {
		numGroups = 1
	}
	casesPerGroup := totalCases / numGroups
	scorePerGroup := totalScore / numGroups

	groups := make([]TestGroup, numGroups)
	for i := 0; i < numGroups; i++ {
		groups[i] = TestGroup{
			GroupID:  i + 1,
			NumCases: casesPerGroup,
			Score:    scorePerGroup,
		}
	}

	// Distribute any remainder to the last group.
	if rem := totalCases % numGroups; rem > 0 {
		groups[numGroups-1].NumCases += rem
	}
	if rem := totalScore % numGroups; rem > 0 {
		groups[numGroups-1].Score += rem
	}

	return TestDataConfig{
		NumTestCases: totalCases,
		NumSamples:   2,
		Groups:       groups,
		BoundaryConfig: BoundaryConfig{
			IncludeMinCase: true,
			IncludeMaxCase: true,
			IncludeZero:    false,
		},
	}
}
