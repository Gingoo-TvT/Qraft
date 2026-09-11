package prompts

import (
	"strings"
	"testing"
)

func TestReviewTemplateOmitsAuthoringOnlyNotes(t *testing.T) {
	const (
		hint      = "INTERNAL_TEMPLATE_HINT_SENTINEL_54aa"
		rationale = "INTERNAL_TEMPLATE_RATIONALE_SENTINEL_8c21"
	)
	template := ReviewTemplate()
	req, err := template.Render(ReviewInput{
		Title:                   "Visible title",
		Statement:               "Visible statement",
		OneLineHint:             hint,
		DifficultyJustification: rationale,
		MainSolutionCode:        "int main(){}",
		MainSolutionLanguage:    "cpp",
	})
	if err != nil {
		t.Fatalf("ReviewTemplate.Render returned error: %v", err)
	}
	if len(req.Messages) != 1 {
		t.Fatalf("review request messages=%d, want 1", len(req.Messages))
	}
	content := req.Messages[0].Content
	for _, forbidden := range []string{hint, rationale, "## 一句话提示", "## 难度理由"} {
		if strings.Contains(content, forbidden) {
			t.Fatalf("legacy review template leaked %q:\n%s", forbidden, content)
		}
	}
	for _, required := range []string{
		"以上题面是唯一的选手可见内容",
		"Visible statement",
		"输出 JSON Schema",
	} {
		if !strings.Contains(content, required) {
			t.Fatalf("legacy review template omitted %q:\n%s", required, content)
		}
	}
}
