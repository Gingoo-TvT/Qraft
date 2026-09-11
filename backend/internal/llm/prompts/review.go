package prompts

// TemplateName constant for LLM review.
const (
	TemplateNameReview = "llm_review"
)

// reviewSystemPrompt is the system prompt for the LLM review step.
const reviewSystemPrompt = `你是一位算法竞赛题目质量审查专家，负责对自动生成的竞赛题目进行全面的质量审查。

审查范围边界：
- 选手实际可见的材料只有题目标题、题面、样例以及发布时的资源限制。
- 生成阶段的辅助提示和作者难度说明属于内部作者元数据，不是选手题目的一部分；它们不会提供给审查模型，也不得被当作题面缺陷来评价。
- 题面清晰度和选手感知的难度只能依据选手可见材料判断。主力/暴力代码、测试概况、标签和目标难度仅用于各自的内部校验。

你需要从以下维度进行严格审查：

1. **题面清晰度（Statement Clarity）**
   - 题目描述是否无歧义？
   - 输入输出格式是否明确？
   - 约束条件是否完整且无矛盾？
   - 样例是否正确且有说明？
   - LaTeX 公式是否正确？

2. **解法正确性（Solution Correctness）**
   - 主力解法的算法是否正确？
   - 时间复杂度是否满足约束？
   - 空间复杂度是否在内存限制内？
   - 是否处理了所有边界情况？
   - 暴力解法是否逻辑正确（即使效率低）？

3. **测试数据覆盖（Test Data Coverage）**
   - 是否覆盖了边界情况？
   - 是否有对抗性数据？
   - 数据规模是否分层合理？
   - 样例数据是否有代表性？

4. **难度准确性（Difficulty Accuracy）**
   - 标定的 difficulty rating 是否准确？
   - 预期解法的复杂度与难度是否匹配？
   - 对目标群体是否适当？

5. **标签正确性（Tag Correctness）**
   - 标签是否准确反映了核心算法/知识点？
   - 是否遗漏了重要标签？
   - 是否有不相关的标签？

审查标准：
- 任何可能导致选手误解的描述都应标记为 issue。
- 任何可能影响评测正确性的问题都是 critical issue。
- confidence 分数反映你对整体质量的信心（0.0-1.0）。
- 即使总体合格，也应提供改进建议。

输出格式要求：
- 严格按照指定的 JSON schema 输出。
- 不要输出任何 JSON 之外的内容。`

// reviewUserTemplate is the Go template for the user message.
const reviewUserTemplate = `请对以下自动生成的竞赛题目进行全面质量审查。

## 题目元信息

- 标题：{{.Title}}
- 级别：{{.Level}}
- 难度：{{.Difficulty}}
- 标签：[{{join .Tags ", "}}]
- 时间限制：{{.TimeLimit}} ms
- 内存限制：{{.MemoryLimit}} MB

## 题面

{{.Statement}}

以上题面是唯一的选手可见内容。不要要求题面额外包含内部作者提示或难度说明。

## 主力解法

### 思路说明
{{.MainSolutionExplanation}}

### 源代码（{{.MainSolutionLanguage}}）
` + "```{{.MainSolutionLanguage}}" + `
{{.MainSolutionCode}}
` + "```" + `

## 暴力解法

### 思路说明
{{.BruteSolutionExplanation}}

### 源代码（{{.BruteSolutionLanguage}}）
` + "```{{.BruteSolutionLanguage}}" + `
{{.BruteSolutionCode}}
` + "```" + `

{{- if .TestDataSummary}}

## 测试数据概况

{{.TestDataSummary}}
{{- end}}

## 输出 JSON Schema

请严格按照以下 JSON 格式输出，不要添加任何额外文字：

` + "```json" + `
{
  "approved": true或false,
  "issues": [
    {
      "category": "类别（statement_clarity/solution_correctness/test_coverage/difficulty_accuracy/tag_correctness）",
      "severity": "严重程度（critical/major/minor）",
      "description": "问题的详细描述",
      "suggestion": "修改建议"
    }
  ],
  "suggestions": [
    "改进建议1（非必须修改，但会提升质量的建议）",
    "改进建议2"
  ],
  "confidence": 0.85
}
` + "```" + `

注意：
- 如果存在任何 critical 级别的 issue，approved 必须为 false。
- 如果存在多个 major 级别的 issue，approved 应为 false。
- confidence 反映你对审查结论的信心，不是对题目质量的评分。`

// ReviewInput holds the data needed to render the LLM review prompt template.
type ReviewInput struct {
	// Title of the problem.
	Title string

	// Level is the problem level.
	Level string

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
	MainSolutionCode        string
	MainSolutionLanguage    string
	MainSolutionExplanation string

	// Brute force solution fields.
	BruteSolutionCode        string
	BruteSolutionLanguage    string
	BruteSolutionExplanation string

	// TestDataSummary is a human-readable summary of the test data coverage
	// (optional).
	TestDataSummary string

	// OneLineHint is retained for source compatibility with older callers, but
	// is intentionally ignored by the review template because it is internal
	// authoring metadata rather than contestant-visible content.
	OneLineHint string

	// DifficultyJustification is retained for source compatibility with older
	// callers, but is intentionally ignored by the review template.
	DifficultyJustification string
}

// ReviewTemplate returns the prompt template for the LLM review step.
func ReviewTemplate() *PromptTemplate {
	return &PromptTemplate{
		Name:               TemplateNameReview,
		Version:            PromptVersionV1,
		SystemPrompt:       reviewSystemPrompt,
		UserPromptTemplate: reviewUserTemplate,
	}
}
