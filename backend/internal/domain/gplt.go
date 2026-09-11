package domain

import (
	"fmt"

	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// GPLT (团体程序设计天梯赛 / PAT-GPLT) types
// ---------------------------------------------------------------------------

// GPLTTier identifies the three standard tiers used in PAT 团体程序设计天梯赛.
type GPLTTier string

const (
	GPLTTierL1 GPLTTier = "L1"
	GPLTTierL2 GPLTTier = "L2"
	GPLTTierL3 GPLTTier = "L3"
)

// IsValid reports whether the tier is one of L1/L2/L3.
func (t GPLTTier) IsValid() bool {
	switch t {
	case GPLTTierL1, GPLTTierL2, GPLTTierL3:
		return true
	}
	return false
}

// Level returns the ProblemLevel associated with a tier.
func (t GPLTTier) Level() ProblemLevel {
	switch t {
	case GPLTTierL1:
		return LevelGPLTL1
	case GPLTTierL2:
		return LevelGPLTL2
	case GPLTTierL3:
		return LevelGPLTL3
	}
	return ""
}

// TotalScore returns the total score awarded by a single GPLT problem in the
// given tier.  For L1 this is NOT constant: real 天梯赛 L1 题目分值为
// 5/5/10/10/15/15/20/20（共 100 分）。Use ScoresForTier to get the per-problem
// breakdown.  This method returns a representative value (the max for L1).
func (t GPLTTier) TotalScore() int {
	switch t {
	case GPLTTierL1:
		return 20
	case GPLTTierL2:
		return 25
	case GPLTTierL3:
		return 30
	}
	return 0
}

// ScoresForTier returns the per-problem score list for a tier, in the order
// problems should be emitted. For L1 the canonical PAT-GPLT distribution is
// 5/5/10/10/15/15/20/20 (8 problems, 100 points). L2 uses 4×25 and L3 uses
// 3×30, matching real contests.
func ScoresForTier(tier GPLTTier) []int {
	switch tier {
	case GPLTTierL1:
		return []int{5, 5, 10, 10, 15, 15, 20, 20}
	case GPLTTierL2:
		return []int{25, 25, 25, 25}
	case GPLTTierL3:
		return []int{30, 30, 30}
	}
	return nil
}

// DefaultDifficultyForScore returns a representative difficulty for an L1
// problem of the given score.  Low-score L1 problems are meant to be very
// basic (input/output, single loop), while 20-point L1 problems test more
// thorough simulation or a light algorithmic insight.
func DefaultDifficultyForL1Score(score int) int {
	switch score {
	case 5:
		return 800
	case 10:
		return 1000
	case 15:
		return 1100
	case 20:
		return 1200
	}
	return 1000
}

// DefaultDifficulty returns the representative difficulty used when generating
// a problem of this tier (mid-band of the tier's range).
func (t GPLTTier) DefaultDifficulty() int {
	switch t {
	case GPLTTierL1:
		return 1000
	case GPLTTierL2:
		return 1600
	case GPLTTierL3:
		return 2200
	}
	return 1500
}

// TestDataConfig returns a TestDataConfig preset whose group scores sum to
// totalScore. Groups are coverage/scoring hints; the model chooses the final
// number of distinct cases in the adaptive 10-20 range. Sample cases always
// carry 0 points.
//
// If totalScore <= 0, the tier's representative TotalScore() is used.
func (t GPLTTier) TestDataConfig(totalScore int) TestDataConfig {
	if totalScore <= 0 {
		totalScore = t.TotalScore()
	}
	return buildGPLTTestDataConfig(t, totalScore)
}

// buildGPLTTestDataConfig distributes totalScore across tier-appropriate
// groups. Group counts are hints; case counts are adaptive.
func buildGPLTTestDataConfig(tier GPLTTier, totalScore int) TestDataConfig {
	// Choose group count by score band (mirrors how real 天梯赛 problems
	// layer test data: more points → more subtasks to differentiate solvers).
	var numGroups int
	switch {
	case totalScore <= 5:
		numGroups = 2
	case totalScore <= 10:
		numGroups = 2
	case totalScore <= 15:
		numGroups = 3
	case totalScore <= 20:
		numGroups = 3
	case totalScore <= 25:
		numGroups = 4
	default:
		numGroups = 5
	}

	// Split totalScore as evenly as possible, giving the remainder to the
	// final (hardest) group so it carries the hardest-earned points.
	base := totalScore / numGroups
	remainder := totalScore - base*numGroups
	// Brute-check only the first half of groups (rounded up). Larger groups
	// are typically too big for an O(n^2)/O(n^3) brute to finish within the
	// sandbox activity budget, and cross-validating them against the brute
	// gives no additional correctness signal — the main solution is the one
	// we ship for those cases anyway.
	bruteGroups := (numGroups + 1) / 2
	groups := make([]TestGroup, numGroups)
	for i := 0; i < numGroups; i++ {
		score := base
		if i == numGroups-1 {
			score += remainder
		}
		groups[i] = TestGroup{
			GroupID:     i + 1,
			NumCases:    0,
			Score:       score,
			Description: gpltGroupDescription(i, numGroups),
			BruteCheck:  i < bruteGroups,
		}
	}

	return TestDataConfig{
		NumTestCases:   0,
		MinTestCases:   MinAdaptiveTestCases,
		MaxTestCases:   MaxAdaptiveTestCases,
		NumSamples:     2,
		Groups:         groups,
		BoundaryConfig: BoundaryConfig{IncludeMinCase: true, IncludeMaxCase: true},
	}
}

func gpltGroupDescription(idx, total int) string {
	switch {
	case total <= 2:
		if idx == 0 {
			return "基础样例与常规数据"
		}
		return "最大规模与边界数据"
	case total == 3:
		return []string{"基础样例与小规模", "中等规模常规数据", "最大规模与边界数据"}[idx]
	case total == 4:
		return []string{"小规模基础数据", "中规模常规数据", "大规模与特殊结构", "极限规模与对抗数据"}[idx]
	default:
		if idx == 0 {
			return "基础正确性"
		}
		if idx == total-1 {
			return "极限规模与对抗数据"
		}
		return "中间规模与边界"
	}
}

// ---------------------------------------------------------------------------
// Batch params
// ---------------------------------------------------------------------------

// GPLTStandardDistribution is the canonical 8/4/3 tier split of a real
// 团体程序设计天梯赛 contest.
var GPLTStandardDistribution = map[GPLTTier]int{
	GPLTTierL1: 8,
	GPLTTierL2: 4,
	GPLTTierL3: 3,
}

// GPLTBatchParams bundles the inputs for a one-click GPLT batch generation
// (15 problems, 8×L1 + 4×L2 + 3×L3).
type GPLTBatchParams struct {
	// Optional batch identifier; if empty the workflow generates one.
	BatchID string `json:"batch_id,omitempty"`

	// Default execution limits applied to every generated problem.
	TimeLimit   int `json:"time_limit"`
	MemoryLimit int `json:"memory_limit"`

	// Deprecated: operational break-glass switch for pausing each child
	// workflow. Product callers must leave this false.
	RequireReview bool `json:"require_review"`

	// Whether to generate editorials.
	GenerateEditorial bool `json:"generate_editorial"`

	// Solution languages to produce.
	Languages []string `json:"languages,omitempty"`

	// Locale of the generated content. GPLT is always Chinese; defaults to
	// "zh" when empty.
	Locale string `json:"locale,omitempty"`

	// Similarity-check limit per problem. 0 disables the check.
	SimilarLimit int `json:"similar_limit"`

	// Optional request-scoped LLM runtime settings propagated to every child
	// problem workflow.
	ProviderConfig *ProviderRuntimeConfig `json:"provider_config,omitempty"`

	// Free-form extra instructions appended to every problem's prompt.
	CustomPrompt string `json:"custom_prompt,omitempty"`
}

// Validate checks batch-level parameters for sanity. Per-problem validation is
// handled downstream when ProblemGenParams are constructed.
func (p *GPLTBatchParams) Validate() error {
	if p.TimeLimit <= 0 {
		return fmt.Errorf("time_limit must be positive, got %d", p.TimeLimit)
	}
	if p.MemoryLimit <= 0 {
		return fmt.Errorf("memory_limit must be positive, got %d", p.MemoryLimit)
	}
	if len(p.Languages) == 0 {
		return fmt.Errorf("at least one language must be specified")
	}
	if err := p.ProviderConfig.Validate(); err != nil {
		return err
	}
	return nil
}

// DefaultGPLTBatchParams returns a GPLTBatchParams preset with reasonable
// defaults for a one-click run.
func DefaultGPLTBatchParams() GPLTBatchParams {
	return GPLTBatchParams{
		BatchID:           uuid.New().String(),
		TimeLimit:         1000,
		MemoryLimit:       256,
		RequireReview:     false,
		GenerateEditorial: true,
		Languages:         []string{"cpp"},
		Locale:            "zh",
		SimilarLimit:      0,
	}
}

// ---------------------------------------------------------------------------
// Result types
// ---------------------------------------------------------------------------

// GPLTProblemResult captures the outcome of a single child problem-generation
// workflow within a batch run.
type GPLTProblemResult struct {
	Tier       GPLTTier       `json:"tier"`
	Level      ProblemLevel   `json:"level"`
	Difficulty int            `json:"difficulty"`
	Status     WorkflowStatus `json:"status"`
	WorkflowID string         `json:"workflow_id,omitempty"`
	Error      string         `json:"error,omitempty"`
}

// GPLTBatchState is the final state returned by GPLTBatchGenerationWorkflow.
type GPLTBatchState struct {
	BatchID   string              `json:"batch_id"`
	Total     int                 `json:"total"`
	Succeeded int                 `json:"succeeded"`
	Failed    int                 `json:"failed"`
	Results   []GPLTProblemResult `json:"results"`
}
