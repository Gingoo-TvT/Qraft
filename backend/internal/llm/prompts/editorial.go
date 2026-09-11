package prompts

// TemplateName constant for editorial generation.
const (
	TemplateNameEditorial = "generate_editorial"
)

// editorialSystemPrompt is the system prompt for editorial generation.
const editorialSystemPrompt = `你是一位算法竞赛教育专家，擅长编写详细、清晰的题解（editorial）。你的题解应当帮助选手理解解题思路并从中学习。

题解写作原则：
1. 从直觉和观察入手，引导读者逐步发现关键性质。
2. 避免直接给出结论——要解释"为什么"而不仅仅是"怎么做"。
3. 复杂度分析必须严谨，包含证明或论证。
4. 指出常见的错误和陷阱，帮助选手避免踩坑。
5. 如果有多种解法，简要提及替代方案。
6. 代码实现注释要与文字解释对应。

格式要求：
- 使用 Markdown 格式。
- 数学公式使用 LaTeX（$...$  行内，$$...$$ 行间）。
- 代码块标明语言。
- 使用清晰的标题层级。`

// editorialUserTemplate is the Go template for the user message.
const editorialUserTemplate = `请为以下竞赛题目编写一篇详细的题解（editorial）。

## 题目信息

- 标题：{{.Title}}
- 难度：{{.Difficulty}}
- 标签：[{{join .Tags ", "}}]
- 时间限制：{{.TimeLimit}} ms
- 内存限制：{{.MemoryLimit}} MB

## 题面

{{.Statement}}

## 正解代码（{{.SolutionLanguage}}）

` + "```{{.SolutionLanguage}}" + `
{{.SolutionCode}}
` + "```" + `

## 正解思路概要

{{.SolutionExplanation}}

{{- if .BruteCode}}

## 暴力解法代码（{{.BruteLanguage}}）

` + "```{{.BruteLanguage}}" + `
{{.BruteCode}}
` + "```" + `

## 暴力解法思路

{{.BruteExplanation}}
{{- end}}

{{- if .OneLineHint}}

## 一句话提示

{{.OneLineHint}}
{{- end}}

## 输出要求

请直接输出 Markdown 格式的题解，包含以下章节（按顺序）：

### 必须包含的章节

1. **## 题意简述** — 用 2-3 句话概括题意。

2. **## 解题思路**
   - 从观察和直觉入手。
   - 逐步推导核心性质或定理。
   - 解释为什么这个方法是正确的。
   - 如果涉及经典算法/数据结构，简要介绍其原理。

3. **## 复杂度分析**
   - 时间复杂度：包含推导过程。
   - 空间复杂度：包含推导过程。
   - 解释为什么满足题目限制。

4. **## 实现要点**
   - 关键的实现细节和注意事项。
   - 数据类型选择（是否需要 long long 等）。
   - 初始化、边界处理等容易出错的地方。

5. **## 常见错误与陷阱**
   - 列举选手可能犯的典型错误。
   - 对每个错误解释为什么是错的。

6. **## 参考代码**
   - 带有详细注释的代码。

{{- if .IncludeAlternative}}

7. **## 其他解法**（如果存在多种思路）
   - 简要介绍替代方案。
   - 与主力解法的复杂度对比。
{{- end}}

请直接输出 Markdown 内容，不要包裹在 JSON 或代码块中。`

// EditorialInput holds the data needed to render the editorial generation
// prompt template.
type EditorialInput struct {
	// Title of the problem.
	Title string

	// Difficulty rating.
	Difficulty int

	// Tags for the problem.
	Tags []string

	// TimeLimit in milliseconds.
	TimeLimit int

	// MemoryLimit in megabytes.
	MemoryLimit int

	// Statement is the full problem statement in Markdown.
	Statement string

	// Main solution fields.
	SolutionCode        string
	SolutionLanguage    string
	SolutionExplanation string

	// Brute force solution fields (optional).
	BruteCode        string
	BruteLanguage    string
	BruteExplanation string

	// OneLineHint from the statement generation step.
	OneLineHint string

	// IncludeAlternative indicates whether to request alternative solution
	// approaches in the editorial.
	IncludeAlternative bool
}

// NewEditorialInput creates an EditorialInput with language defaulting to
// "C++17" if empty.
func NewEditorialInput(
	title string,
	difficulty int,
	tags []string,
	timeLimit, memoryLimit int,
	statement string,
	solutionCode, solutionLanguage, solutionExplanation string,
	bruteCode, bruteLanguage, bruteExplanation string,
	hint string,
	includeAlternative bool,
) EditorialInput {
	if solutionLanguage == "" {
		solutionLanguage = "C++17"
	}
	if bruteLanguage == "" && bruteCode != "" {
		bruteLanguage = "C++17"
	}
	return EditorialInput{
		Title:               title,
		Difficulty:          difficulty,
		Tags:                tags,
		TimeLimit:           timeLimit,
		MemoryLimit:         memoryLimit,
		Statement:           statement,
		SolutionCode:        solutionCode,
		SolutionLanguage:    solutionLanguage,
		SolutionExplanation: solutionExplanation,
		BruteCode:           bruteCode,
		BruteLanguage:       bruteLanguage,
		BruteExplanation:    bruteExplanation,
		OneLineHint:         hint,
		IncludeAlternative:  includeAlternative,
	}
}

// EditorialTemplate returns the prompt template for editorial generation.
func EditorialTemplate() *PromptTemplate {
	return &PromptTemplate{
		Name:               TemplateNameEditorial,
		Version:            PromptVersionV1,
		SystemPrompt:       editorialSystemPrompt,
		UserPromptTemplate: editorialUserTemplate,
	}
}
