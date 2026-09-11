package activities

import (
	"strings"
	"testing"
)

func TestStatementMathNotationStandaloneScientificBounds(t *testing.T) {
	input := "## 约束\n- $1≤q≤200000$；\n- $1≤x≤1000000000$，$1≤c≤1000000000$；\n- $1≤k≤100000000000000$。"
	got := NormalizeStatementMathNotationV1(input)
	for _, want := range []string{
		"$1\\le q\\le 2 \\times 10^5$",
		"$1\\le x\\le 10^9$",
		"$1\\le c\\le 10^9$",
		"$1\\le k\\le 10^{14}$",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("normalized statement missing %q: %s", want, got)
		}
	}
}

func TestStatementMathNotationStandaloneSkipsCode(t *testing.T) {
	fence := strings.Repeat(string(rune(96)), 3)
	inline := string(rune(96)) + "1000000000" + string(rune(96))
	input := strings.Join([]string{fence + "text", "1000000000", fence, "", inline}, "\n")
	got := NormalizeStatementMathNotationV1(input)
	if got != input {
		t.Fatalf("code content changed: got %q want %q", got, input)
	}
}
