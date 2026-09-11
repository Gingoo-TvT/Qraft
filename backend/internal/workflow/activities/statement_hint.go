package activities

import (
	"regexp"
	"strings"
)

var statementHintWhitespacePatternV1 = regexp.MustCompile(`\s+`)

// These markers are intentionally broad and used only to detect an
// implementation recipe, not to judge the problem's algorithm. A public
// one-line hint may name one useful idea, but a chain of several concrete
// data structures/steps gives away the whole solution and is especially
// confusing when the UI displays the field without Markdown rendering.
var statementHintImplementationMarkerGroupsV1 = [][]string{
	{"时间线段树", "线段树", "区间分解", "segment tree"},
	{"可回滚", "回滚", "rollback"},
	{"异或基", "线性基", "basis"},
	{"秩", "rank"},
	{"自由变量", "free variable"},
	{"扫描线", "scanline"},
	{"并查集", "union-find"},
	{"单调栈"},
	{"树状数组", "fenwick"},
	{"倍增", "binary lifting"},
}

// NormalizeOneLineHintV1 prepares the internal authoring clue for the current
// UI, which renders it as plain text rather than Markdown. Models sometimes
// copy TeX or Markdown delimiters from the statement; keeping those bytes
// would make the authoring metadata hard to read and could leak it if a caller
// accidentally rendered the field as contestant content.
func NormalizeOneLineHintV1(hint string) string {
	hint = strings.TrimSpace(hint)
	if hint == "" {
		return ""
	}
	replacements := []struct {
		from string
		to   string
	}{
		{`\mathbb{F}_2`, "F₂"},
		{`\mathbb F_2`, "F₂"},
		{`\mathbb{F}_2`, "F₂"},
		{`\oplus`, "异或"},
		{`\times`, "×"},
		{`\cdot`, "·"},
		{`\leq`, "≤"},
		{`\le`, "≤"},
		{`\geq`, "≥"},
		{`\ge`, "≥"},
		{`\ldots`, "…"},
		{`\dots`, "…"},
	}
	for _, replacement := range replacements {
		hint = strings.ReplaceAll(hint, replacement.from, replacement.to)
	}
	// Remove remaining TeX command markers/braces conservatively. A hint is
	// not an equation, so retaining the command name is less useful than plain
	// readable text; ordinary Unicode/ASCII content is preserved.
	var builder strings.Builder
	for index := 0; index < len(hint); index++ {
		if hint[index] == '\\' {
			// A transport-leaked "\\n" is a layout break, not part of the
			// visible hint. Preserve a word boundary when removing it.
			if index+1 < len(hint) && hint[index+1] == 'n' {
				builder.WriteByte(' ')
				index++
				continue
			}
			if index+1 < len(hint) && hint[index+1] == '_' {
				builder.WriteByte('_')
				index++
				continue
			}
			builder.WriteByte(' ')
			index++
			for index < len(hint) && ((hint[index] >= 'a' && hint[index] <= 'z') || (hint[index] >= 'A' && hint[index] <= 'Z')) {
				index++
			}
			index--
			continue
		}
		if hint[index] == '$' || hint[index] == '`' || hint[index] == '{' || hint[index] == '}' {
			continue
		}
		builder.WriteByte(hint[index])
	}
	hint = statementHintWhitespacePatternV1.ReplaceAllString(builder.String(), " ")
	hint = strings.TrimSpace(hint)
	if runes := []rune(hint); len(runes) > 160 {
		hint = string(runes[:160]) + "…"
	}
	if statementHintIsImplementationRecipeV1(hint) {
		if containsCJKV1(hint) {
			return "先刻画当前状态的关键关系，再处理每次查询。"
		}
		return "Identify the key invariant of the current state before answering queries."
	}
	return hint
}

func statementHintIsImplementationRecipeV1(hint string) bool {
	lower := strings.ToLower(hint)
	markerCount := 0
	for _, group := range statementHintImplementationMarkerGroupsV1 {
		for _, marker := range group {
			if strings.Contains(lower, strings.ToLower(marker)) {
				markerCount++
				break
			}
		}
	}
	if markerCount >= 3 {
		return true
	}
	if markerCount >= 2 && (strings.ContainsAny(hint, ";；→➜") || strings.Contains(hint, "->")) {
		return true
	}
	return false
}

func containsCJKV1(value string) bool {
	for _, r := range value {
		if (r >= '\u4e00' && r <= '\u9fff') || (r >= '\u3400' && r <= '\u4dbf') {
			return true
		}
	}
	return false
}
