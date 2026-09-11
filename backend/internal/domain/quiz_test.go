package domain

import (
	"reflect"
	"regexp"
	"strings"
	"testing"
)

func TestQuizTypeExcelRoundtrip(t *testing.T) {
	tests := []struct {
		typ    QuizType
		excel  string
		prefix string
	}{
		{QuizTypeProgramming, "编程题", "C"},
		{QuizTypeChoice, "选择题", "X"},
		{QuizTypeFillBlank, "填空题", "T"},
		{QuizTypeJudge, "判断题", "P"},
	}
	for _, tt := range tests {
		if !tt.typ.IsValid() {
			t.Fatalf("%s should be valid", tt.typ)
		}
		got, err := QuizTypeFromExcel(" " + tt.excel + " ")
		if err != nil {
			t.Fatalf("QuizTypeFromExcel(%q): %v", tt.excel, err)
		}
		if got != tt.typ {
			t.Fatalf("QuizTypeFromExcel(%q) = %s, want %s", tt.excel, got, tt.typ)
		}
		if tt.typ.ToExcel() != tt.excel {
			t.Fatalf("%s.ToExcel() = %q, want %q", tt.typ, tt.typ.ToExcel(), tt.excel)
		}
		if tt.typ.CodePrefix() != tt.prefix {
			t.Fatalf("%s.CodePrefix() = %q, want %q", tt.typ, tt.typ.CodePrefix(), tt.prefix)
		}
		fromPrefix, err := QuizTypeFromCodePrefix(strings.ToLower(tt.prefix))
		if err != nil {
			t.Fatalf("QuizTypeFromCodePrefix(%q): %v", tt.prefix, err)
		}
		if fromPrefix != tt.typ {
			t.Fatalf("QuizTypeFromCodePrefix(%q) = %s, want %s", tt.prefix, fromPrefix, tt.typ)
		}
	}
}

func TestQuizDifficultyExcelRoundtrip(t *testing.T) {
	tests := []struct {
		diff  QuizDifficulty
		excel string
		score int
	}{
		{QuizDifficultyEasy, "简单", 0},
		{QuizDifficultyMedium, "中等", 1},
		{QuizDifficultyHard, "困难", 2},
	}
	for _, tt := range tests {
		if !tt.diff.IsValid() {
			t.Fatalf("%s should be valid", tt.diff)
		}
		got, err := QuizDifficultyFromExcel(" " + tt.excel + " ")
		if err != nil {
			t.Fatalf("QuizDifficultyFromExcel(%q): %v", tt.excel, err)
		}
		if got != tt.diff {
			t.Fatalf("QuizDifficultyFromExcel(%q) = %s, want %s", tt.excel, got, tt.diff)
		}
		if tt.diff.ToExcel() != tt.excel {
			t.Fatalf("%s.ToExcel() = %q, want %q", tt.diff, tt.diff.ToExcel(), tt.excel)
		}
		if tt.diff.Score() != tt.score {
			t.Fatalf("%s.Score() = %d, want %d", tt.diff, tt.diff.Score(), tt.score)
		}
	}
}

func TestQuizVisibilityExcelRoundtrip(t *testing.T) {
	tests := []struct {
		vis   QuizVisibility
		excel string
	}{
		{QuizVisibilityPublic, "公开"},
		{QuizVisibilityPrivate, "私有"},
	}
	for _, tt := range tests {
		if !tt.vis.IsValid() {
			t.Fatalf("%s should be valid", tt.vis)
		}
		got, err := QuizVisibilityFromExcel(" " + tt.excel + " ")
		if err != nil {
			t.Fatalf("QuizVisibilityFromExcel(%q): %v", tt.excel, err)
		}
		if got != tt.vis {
			t.Fatalf("QuizVisibilityFromExcel(%q) = %s, want %s", tt.excel, got, tt.vis)
		}
		if tt.vis.ToExcel() != tt.excel {
			t.Fatalf("%s.ToExcel() = %q, want %q", tt.vis, tt.vis.ToExcel(), tt.excel)
		}
	}
}

func TestQuizBreakSplitSemantics(t *testing.T) {
	re := regexp.MustCompile(`(?i)\s*\{break\}\s*`)
	got := splitBreakForDomainTest(re, "A{break}\n B {BREAK} \nC")
	want := []string{"A", "B", "C"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("split = %#v, want %#v", got, want)
	}
}

func splitBreakForDomainTest(re *regexp.Regexp, s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	raw := re.Split(s, -1)
	out := make([]string, 0, len(raw))
	for _, part := range raw {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
