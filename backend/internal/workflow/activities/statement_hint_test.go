package activities

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNormalizeOneLineHintV1RemovesTeXAndMarkdown(t *testing.T) {
	got := NormalizeOneLineHintV1("把记录拆成区间，用 $\\mathbb{F}_2$ \u5f02\u6216\u57fa维护\\n查询时判断 \\oplus")
	if strings.ContainsAny(got, "$\\`{}\n\r") {
		t.Fatalf("normalized hint still contains markup or a line break: %q", got)
	}
	if !strings.Contains(got, "F₂") || !strings.Contains(got, "异或") {
		t.Fatalf("expected readable replacements, got %q", got)
	}
	if !strings.Contains(got, "维护 查询") {
		t.Fatalf("literal newline escape should preserve a word boundary, got %q", got)
	}
}

func TestNormalizeOneLineHintV1KeepsPlainHintAndBoundsLength(t *testing.T) {
	plain := "先观察不变量，再按时间顺序处理查询。"
	if got := NormalizeOneLineHintV1(plain); got != plain {
		t.Fatalf("plain hint changed: got %q want %q", got, plain)
	}
	long := strings.Repeat("x", 200)
	got := NormalizeOneLineHintV1(long)
	if len([]rune(got)) != 161 || !strings.HasSuffix(got, "…") {
		t.Fatalf("hint was not bounded to 160 runes plus ellipsis: len=%d value=%q", len([]rune(got)), got)
	}
}

func TestNormalizeOneLineHintV1CollapsesImplementationRecipe(t *testing.T) {
	raw := "把每条记录的活跃时间拆成时间线段树上的区间，并用可回滚的 $\\mathbb{F}_2$ 异或基维护每个时间点的秩；查询时同时判断目标是否可表示以及自由变量数量。"
	got := NormalizeOneLineHintV1(raw)
	want := "先刻画当前状态的关键关系，再处理每次查询。"
	if got != want {
		t.Fatalf("implementation recipe was not reduced: got %q want %q", got, want)
	}
}

func TestParseStatementResponseNormalizesPublicHint(t *testing.T) {
	raw, err := json.Marshal(statementJSON{
		Title:       "T",
		Statement:   "## Input Format\n\n## Output Format\n\n## Constraints",
		OneLineHint: "用 $\\mathbb{F}_2$ 异或基判断。",
	})
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	result, err := parseStatementResponse(string(raw))
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	if result.OneLineHint != "用 F₂ 异或基判断。" {
		t.Fatalf("parser did not normalize hint: %q", result.OneLineHint)
	}
}
