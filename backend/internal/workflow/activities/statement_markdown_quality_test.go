package activities

import (
	"strings"
	"testing"
)

func TestNormalizeGeneratedStatementMarkdownRepairsDoubleEscapedLayout(t *testing.T) {
	raw := `叙述中的 $\nabla f$ 保持为数学命令。\n\n## 输入格式\n第一行包含两个整数 n,k。\n随后保证 u\ne v。\n\n## 输出格式\n输出 c\cdot x。\n\n## 约束\n- 1\le n\le 10\n\n<!-- ALGOFORGE_SAMPLES -->`

	got := normalizeGeneratedStatementMarkdown(raw)
	for _, want := range []string{
		"\n\n## 输入格式\n",
		"u≠ v",
		"c· x",
		"1≤ n≤ 10",
		`$\nabla f$`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("normalized statement is missing %q:\n%s", want, got)
		}
	}
	if firstLiteralStatementNewline(got) >= 0 {
		t.Fatalf("normalized statement still contains a literal layout escape: %q", got)
	}
}

func TestValidateGeneratedStatementMarkdownRejectsFieldCountMismatch(t *testing.T) {
	statement := normalizeGeneratedStatementMarkdown(`题目。\n\n## 输入格式\n每行包含四个整数 u,v,c，表示一条边。\n\n## 输出格式\n输出答案。\n\n## 约束\n- n 不超过 10。\n\n<!-- ALGOFORGE_SAMPLES -->`)
	err := validateGeneratedStatementMarkdown(statement, true)
	if err == nil || !strings.Contains(err.Error(), "claims 4 integers but lists 3 fields") {
		t.Fatalf("field-count mismatch error = %v", err)
	}
}

func TestNormalizeGeneratedStatementMarkdownPromotesUnambiguousSectionLabels(t *testing.T) {
	raw := "题目描述。\n\n输入格式：第一行包含一个整数 n。\n\n输出：输出答案。\n\n数据范围：1 <= n <= 10。"
	got := normalizeGeneratedStatementMarkdown(raw)
	for _, want := range []string{
		"## 输入格式\n\n第一行包含一个整数 n。",
		"## 输出格式\n\n输出答案。",
		"## 约束\n\n1 <= n <= 10。",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("normalized statement is missing %q:\n%s", want, got)
		}
	}
	if err := validateGeneratedStatementMarkdown(got, false); err != nil {
		t.Fatalf("promoted section labels did not pass validation: %v", err)
	}
}

func TestDiagnoseGeneratedStatementMarkdownCollectsBoundedRepairContext(t *testing.T) {
	statement := "题目。actually, let me reconsider.\n\n## 输入格式\n每行包含四个整数 u,v,c。\n\n## 输出格式\n输出 \\frac{n}{2}。"
	diagnostics := diagnoseGeneratedStatementMarkdownV1(statement, true, true)
	codes := make(map[string]bool, len(diagnostics))
	for _, diagnostic := range diagnostics {
		codes[diagnostic.Code] = true
	}
	for _, want := range []string{
		"bare_tex_command",
		"integer_field_count_mismatch",
		"missing_constraints_heading",
		"structured_sample_placeholder_count",
		"reasoning_trace",
	} {
		if !codes[want] {
			t.Fatalf("diagnostics omitted %q: %+v", want, diagnostics)
		}
	}
	if len(diagnostics) > maxStatementMarkdownDiagnosticsV1 {
		t.Fatalf("diagnostics count = %d, want <= %d", len(diagnostics), maxStatementMarkdownDiagnosticsV1)
	}
}

func TestValidateGeneratedStatementMarkdownRequiresMathDelimitersForComplexTeX(t *testing.T) {
	bad := "题目。\n\n## 输入格式\n输入 n。\n\n## 输出格式\n输出 \\frac{n}{2}。\n\n## 约束\n- n 不超过 10。"
	if err := validateGeneratedStatementMarkdown(bad, false); err == nil || !strings.Contains(err.Error(), `bare TeX command \frac`) {
		t.Fatalf("bare TeX error = %v", err)
	}

	good := "题目。\n\n## 输入格式\n每行包含三个整数 $u,v,c$。\n\n## 输出格式\n输出 $\\frac{n}{2}$。\n\n## 约束\n- $1 \\le n \\le 10$。"
	if err := validateGeneratedStatementMarkdown(good, false); err != nil {
		t.Fatalf("valid Markdown/KaTeX statement was rejected: %v", err)
	}
}

func TestStrictStatementMarkdownQualityRejectsDuplicateCanonicalHeadings(t *testing.T) {
	statement := "题目。\n\n### 输入格式\n输入 n。\n\n### 输出格式\n输出答案。\n\n## 输出格式\n输出答案。\n\n## 约束\n- 1 <= n <= 10。"
	diagnostics := DiagnoseGeneratedStatementMarkdownStrictV1(statement, false)
	if len(diagnostics) == 0 || diagnostics[0].Code != "duplicate_section_heading" {
		t.Fatalf("strict diagnostics = %+v, want duplicate_section_heading first", diagnostics)
	}
	if err := ValidateGeneratedStatementMarkdownStrictV1(statement, false); err == nil || !strings.Contains(err.Error(), "repeats") {
		t.Fatalf("strict validator error = %v, want duplicate heading", err)
	}
	// The legacy validator remains replay-compatible and does not gain this
	// newly introduced defect code implicitly.
	legacy := DiagnoseGeneratedStatementMarkdownV1(statement, false)
	for _, diagnostic := range legacy {
		if diagnostic.Code == "duplicate_section_heading" {
			t.Fatalf("legacy diagnostics unexpectedly changed: %+v", legacy)
		}
	}
}

func TestStrictStatementMarkdownQualityRejectsTeXInsideInlineCode(t *testing.T) {
	statement := "题目。\n\n## 输入格式\n输入 `w_1,w_2,\\ldots,w_n`。\n\n## 输出格式\n输出答案。\n\n## 约束\n- 1 <= n <= 10。"
	diagnostics := DiagnoseGeneratedStatementMarkdownStrictV1(statement, false)
	found := false
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == "bare_tex_in_code_span" {
			found = true
			if !strings.Contains(diagnostic.Message, "\\ldots") || diagnostic.Line != 4 {
				t.Fatalf("unexpected code-span TeX diagnostic: %+v", diagnostic)
			}
		}
	}
	if !found {
		t.Fatalf("strict diagnostics omitted bare_tex_in_code_span: %+v", diagnostics)
	}
	if err := ValidateGeneratedStatementMarkdownStrictV1(statement, false); err == nil || !strings.Contains(err.Error(), "inline code span") {
		t.Fatalf("strict validator error = %v, want inline-code TeX defect", err)
	}
}

func TestStatementPromptPinsDecodedMarkdownAndFieldArity(t *testing.T) {
	for _, want := range []string{
		"real line breaks",
		"$...$ (inline) or $$...$$ (block)",
		"u,v,c are three integers, never four",
		"exactly one standalone heading",
		"TeX commands inside inline code spans",
		"executable input/output contract",
	} {
		if !strings.Contains(statementSystemPrompt, want) {
			t.Fatalf("statement prompt is missing %q", want)
		}
	}
}
