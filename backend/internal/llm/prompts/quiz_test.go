package prompts

import (
	"strings"
	"testing"
)

func TestQuizPromptRenderChoice(t *testing.T) {
	_, user, err := RenderQuizPrompt(QuizPromptInput{
		Type:              "choice",
		TypeChinese:       "选择题",
		SubjectChinese:    "C语言程序设计",
		DifficultyChinese: "中等",
		Count:             2,
		KnowledgePoints:   []string{"指针基础"},
		Tags:              []string{"指针", "C语言"},
		CustomPrompt:      "包含代码片段",
	})
	if err != nil {
		t.Fatalf("RenderQuizPrompt returned error: %v", err)
	}
	for _, want := range []string{`"options"`, `额外标签：指针、C语言`, `附加要求：包含代码片段`} {
		if !strings.Contains(user, want) {
			t.Fatalf("user prompt missing %q:\n%s", want, user)
		}
	}
}

func TestQuizPromptRenderJudgeWithoutOptionalSections(t *testing.T) {
	_, user, err := RenderQuizPrompt(QuizPromptInput{
		Type:              "judge",
		TypeChinese:       "判断题",
		SubjectChinese:    "数据结构与算法",
		DifficultyChinese: "简单",
		Count:             1,
		KnowledgePoints:   []string{"二叉树遍历"},
	})
	if err != nil {
		t.Fatalf("RenderQuizPrompt returned error: %v", err)
	}
	if !strings.Contains(user, `"answers": ["对"]`) {
		t.Fatalf("judge schema missing:\n%s", user)
	}
	if strings.Contains(user, "额外标签：") || strings.Contains(user, "附加要求：") {
		t.Fatalf("optional sections should be omitted:\n%s", user)
	}
}

func TestQuizPromptRenderFillBlank(t *testing.T) {
	_, user, err := RenderQuizPrompt(QuizPromptInput{
		Type:              "fill_blank",
		TypeChinese:       "填空题",
		SubjectChinese:    "C语言程序设计",
		DifficultyChinese: "困难",
		Count:             3,
		KnowledgePoints:   []string{"数组基础"},
	})
	if err != nil {
		t.Fatalf("RenderQuizPrompt returned error: %v", err)
	}
	if !strings.Contains(user, "___") || !strings.Contains(user, `"第一空答案"`) {
		t.Fatalf("fill blank schema missing:\n%s", user)
	}
}
