package prompts

// TemplateName constant for solution generation.
const (
	TemplateNameSolution = "generate_solution"
)

// solutionSystemPrompt is the system prompt for solution generation.
const solutionSystemPrompt = `你是一位算法竞赛资深选手和出题人，擅长编写高质量的竞赛代码。你的任务是为给定的竞赛题目编写两份解法：

1. **主力解法（main solution）**：最优或接近最优的正解，时间和空间复杂度满足题目约束。
2. **暴力解法（brute force solution）**：朴素的暴力解法，用于对拍验证正解的正确性。暴力解法只需在小数据下正确即可，不需要满足大数据的时间限制。


生成前请静默完成以下正确性预检（不要输出预检过程）：
1. 形式化输入输出并明确证明算法正确的核心不变量。
2. 按实际循环、递归和容器规模推导最坏时间与空间复杂度，核对限制。
3. 用最小、最大、重复、相等、溢出和退化情形攻击实现。
4. 暴力解法必须采用易审计且尽量独立的结构，用来暴露主解的合理错误。
5. 在输出前检查目标语言可编译，并重新核对全部样例。

代码要求：
- 代码风格简洁、可读性强。
- 包含必要的注释说明核心思路。
- 使用标准输入输出（stdin/stdout）。
- 不要使用非标准库或特殊编译器扩展。
- 主力解法的代码应当能通过所有测试数据。
- 暴力解法应当逻辑简单、易于验证正确性。

输出格式要求：
- 严格按照指定的 JSON schema 输出。
- 不要输出任何 JSON 之外的内容。`

// solutionUserTemplate is the Go template for the user message.
const solutionUserTemplate = `请为以下竞赛题目编写主力解法和暴力解法。

## 题目信息

- 标题：{{.Title}}
- 难度：{{.Difficulty}}
- 标签：[{{join .Tags ", "}}]
- 时间限制：{{.TimeLimit}} ms
- 内存限制：{{.MemoryLimit}} MB

## 题面

{{.Statement}}

## 编程语言

请使用 **{{.Language}}** 编写两份解法。

{{- if .Hint}}

## 提示

{{.Hint}}
{{- end}}

## 输出 JSON Schema

请严格按照以下 JSON 格式输出，不要添加任何额外文字：

` + "```json" + `
{
  "main_solution": {
    "code": "完整的主力解法源代码",
    "language": "{{.Language}}",
    "explanation": "主力解法的思路说明，包含：\n1. 核心算法/数据结构\n2. 时间复杂度分析\n3. 空间复杂度分析\n4. 关键实现细节"
  },
  "brute_solution": {
    "code": "完整的暴力解法源代码",
    "language": "{{.Language}}",
    "explanation": "暴力解法的思路说明，包含：\n1. 实现方式\n2. 时间复杂度\n3. 适用范围（能处理的数据规模）"
  }
}
` + "```"

// SolutionInput holds the data needed to render the solution generation
// prompt template.
type SolutionInput struct {
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

	// Language is the target programming language (e.g. "C++17").
	Language string

	// Hint is the one-line hint (optional).
	Hint string
}

// NewSolutionInput creates a SolutionInput with sensible defaults.
// If language is empty, "C++17" is used.
func NewSolutionInput(title string, difficulty int, tags []string, timeLimit, memoryLimit int, statement, language, hint string) SolutionInput {
	if language == "" {
		language = "C++17"
	}
	return SolutionInput{
		Title:       title,
		Difficulty:  difficulty,
		Tags:        tags,
		TimeLimit:   timeLimit,
		MemoryLimit: memoryLimit,
		Statement:   statement,
		Language:    language,
		Hint:        hint,
	}
}

// SolutionTemplate returns the prompt template for solution generation.
func SolutionTemplate() *PromptTemplate {
	return &PromptTemplate{
		Name:               TemplateNameSolution,
		Version:            PromptVersionV1,
		SystemPrompt:       solutionSystemPrompt,
		UserPromptTemplate: solutionUserTemplate,
	}
}
