package prompts

import (
	"bytes"
	"fmt"
	"text/template"
)

const QuizSystemPrompt = `你是一位资深的计算机基础课程命题教师，擅长 C 语言程序设计与数据结构/算法课程。你的任务是根据指定的【知识点 + 题型 + 难度】生成高质量客观题。

通用要求：
1. 题目原创，不抄袭已有题库。
2. 题面表述严谨，所有变量/数据范围明确。
3. 题目难度与"简单/中等/困难"标签匹配。
4. 选择题选项数量为 3-5 个；判断题答案只能是"对"或"错"；填空题答案数量等于题面中下划线 ___ 的数量。
5. 答案与题面必须严格对应；如有代码片段，必须能在标准 C/C++ 环境下编译运行并产生题中所述行为。
6. 输出 JSON 数组，严格按下方 schema，不要输出任何额外文字。`

const QuizUserTemplate = `请为以下要求生成 {{.Count}} 道{{.TypeChinese}}（学科：{{.SubjectChinese}}，难度：{{.DifficultyChinese}}）。

知识点：
{{- range $i, $kp := .KnowledgePoints}}
- {{$kp}}
{{- end}}

{{- if .Tags}}
额外标签：{{join .Tags "、"}}
{{- end}}

{{- if .CustomPrompt}}
附加要求：{{.CustomPrompt}}
{{- end}}

输出 JSON 数组，每个元素必须包含：

{{ if eq .Type "choice" }}
{
  "title": "...",
  "statement": "...(可含代码片段，markdown)",
  "options": [{"label":"A","content":"..."},{"label":"B","content":"..."}],
  "answers": ["A"],
  "explanation": "..."
}
{{ else if eq .Type "judge" }}
{
  "title": "...",
  "statement": "...",
  "answers": ["对"],
  "explanation": "..."
}
{{ else if eq .Type "fill_blank" }}
{
  "title": "...",
  "statement": "题面中用三个连续下划线 ___ 标记空位",
  "answers": ["第一空答案", "第二空答案"],
  "explanation": "..."
}
{{ end }}
`

type QuizPromptInput struct {
	Type              string
	TypeChinese       string
	Subject           string
	SubjectChinese    string
	Difficulty        string
	DifficultyChinese string
	Count             int
	KnowledgePoints   []string
	Tags              []string
	CustomPrompt      string
}

func RenderQuizPrompt(in QuizPromptInput) (system, user string, err error) {
	tmpl, err := template.New("quiz_user").Funcs(templateFuncs).Parse(QuizUserTemplate)
	if err != nil {
		return "", "", fmt.Errorf("compiling quiz prompt: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, in); err != nil {
		return "", "", fmt.Errorf("executing quiz prompt: %w", err)
	}
	return QuizSystemPrompt, buf.String(), nil
}
