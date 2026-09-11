package activities

import (
	"fmt"
	"strings"
)

// DifficultyRubric is a shared reference describing what each Codeforces
// difficulty band looks like in practice. It is appended to LLM prompts so
// the model has concrete criteria instead of a bare numeric target.
const DifficultyRubric = `## Codeforces Difficulty Rubric

Difficulty reflects the "thinking difficulty" — how hard the key observation is to find — NOT code length or implementation effort. Each +200 rating means the key insight should be noticeably less obvious.

- **800–1000 (Beginner):** Pure syntax/simulation, direct translation of the problem statement, 1–2 step reasoning. No algorithmic knowledge needed.
- **1100–1300 (Easy):** One basic algorithmic observation (sorting, prefix sum, simple greedy). Straightforward once the observation is made.
- **1400–1600 (Medium):** One non-trivial observation or trick, possibly combining two simple algorithms. Careful edge-case handling may be needed.
- **1700–1900 (Medium-Hard):** Clever modeling or reduction to a known algorithm; combine 2–3 algorithmic techniques; mathematical reasoning with some depth. The approach is not immediately obvious.
- **2000–2200 (Hard):** Deep mathematical/combinatorial insight, or mapping to a non-obvious data structure/algorithm. Multiple key observations required; the approach is far from obvious.
- **2300–2500 (Very Hard):** Complex multi-step derivation, may involve advanced DS-optimized DP or non-standard algorithm combinations. Requires strong mathematical maturity.
- **2600–2800 (Expert):** Original thinking required, may use advanced mathematical theorems or extremely complex algorithm combinations. Only top competitors solve these.
- **2900–3500 (Legendary):** Competition-level ultra-hard, requires deep mathematical foundation combined with exquisite algorithm design. Extremely rare to solve in contest.

### Critical principle
If a tag's algorithm is basic (e.g. binary-search typical range 800–1400), a high-rated problem using that tag MUST combine it with additional non-trivial techniques. A 1800-rated binary-search problem is NOT "sort and binary search" — it requires clever problem modeling, binary search on answer with a non-trivial check function, or combination with other algorithmic paradigms.`

// DifficultyBandDescription returns a concise description for the difficulty
// band that contains the given rating. Returns an empty string for out-of-range
// values.
func DifficultyBandDescription(difficulty int) string {
	switch {
	case difficulty >= 800 && difficulty <= 1000:
		return "800–1000 (Beginner): Pure syntax/simulation, direct translation, 1–2 step reasoning."
	case difficulty >= 1100 && difficulty <= 1300:
		return "1100–1300 (Easy): One basic algorithmic observation. Straightforward after the observation."
	case difficulty >= 1400 && difficulty <= 1600:
		return "1400–1600 (Medium): One non-trivial observation or trick, possibly combining two simple algorithms."
	case difficulty >= 1700 && difficulty <= 1900:
		return "1700–1900 (Medium-Hard): Clever modeling/reduction to known algorithm; combine 2–3 techniques; mathematical reasoning with depth."
	case difficulty >= 2000 && difficulty <= 2200:
		return "2000–2200 (Hard): Deep mathematical/combinatorial insight or mapping to non-obvious algorithm. Multiple key observations needed."
	case difficulty >= 2300 && difficulty <= 2500:
		return "2300–2500 (Very Hard): Complex multi-step derivation, advanced DS-optimized DP or non-standard algorithm combinations."
	case difficulty >= 2600 && difficulty <= 2800:
		return "2600–2800 (Expert): Original thinking, advanced mathematical theorems or extremely complex algorithm combinations."
	case difficulty >= 2900 && difficulty <= 3500:
		return "2900–3500 (Legendary): Ultra-hard, deep mathematical foundation + exquisite algorithm design."
	default:
		return ""
	}
}

// TagDifficultyContext produces guidance text explaining the relationship
// between a tag's typical difficulty range and the target difficulty. When the
// target exceeds the tag's max difficulty, the text explicitly instructs the
// LLM to combine additional techniques.
func TagDifficultyContext(tagName string, tagMinDifficulty, tagMaxDifficulty, targetDifficulty int) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("- Tag \"%s\" has typical difficulty range %d–%d.\n",
		tagName, tagMinDifficulty, tagMaxDifficulty))

	if targetDifficulty > tagMaxDifficulty {
		sb.WriteString(fmt.Sprintf(
			"  ⚠ Target difficulty %d EXCEEDS the typical range for %s (max %d). "+
				"The problem MUST go beyond basic %s — it should require clever problem modeling, "+
				"combining %s with other non-trivial algorithmic paradigms, or using %s in a sophisticated way "+
				"(e.g. binary search on answer with complex check function, segment tree with lazy propagation, etc.).\n",
			targetDifficulty, tagName, tagMaxDifficulty,
			tagName, tagName, tagName))
	} else if targetDifficulty < tagMinDifficulty {
		sb.WriteString(fmt.Sprintf(
			"  Note: Target difficulty %d is below the typical minimum for %s (%d). "+
				"The usage of %s should be simplified or the problem should focus on easier aspects.\n",
			targetDifficulty, tagName, tagMinDifficulty, tagName))
	}

	return sb.String()
}

// LocaleInstruction returns a prompt fragment that instructs the LLM to write
// all content in the specified locale. Currently supports "zh" (Chinese);
// any other value (including empty) returns an empty string (English default).
func LocaleInstruction(locale string) string {
	if locale == "zh" {
		return "\n\n**IMPORTANT: You MUST write ALL problem content in Chinese (中文). " +
			"This includes the title, problem statement, narrative, input/output format descriptions, " +
			"constraints, sample explanations, hints, and editorial. " +
			"Only keep mathematical notation, variable names, and code in English/ASCII.**\n"
	}
	return ""
}

// BuildTagDifficultySection produces a combined section for all tags. When tag
// min/max info is not available (zeros), only the tag name is mentioned without
// range details.
func BuildTagDifficultySection(tags []string, tagRanges map[string][2]int, targetDifficulty int) string {
	if len(tags) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("\n### Tag Difficulty Context\n")

	for _, tag := range tags {
		if r, ok := tagRanges[tag]; ok && (r[0] > 0 || r[1] > 0) {
			sb.WriteString(TagDifficultyContext(tag, r[0], r[1], targetDifficulty))
		} else {
			sb.WriteString(fmt.Sprintf("- Tag \"%s\" (difficulty range not available)\n", tag))
		}
	}

	return sb.String()
}
