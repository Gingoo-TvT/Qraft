package prompts

import (
	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/llm"
)

// TemplateName constants for statement generation.
const (
	TemplateNameStatement = "generate_statement"
)

// statementSystemPrompt is the system prompt for problem statement generation.
const statementSystemPrompt = `你是一位算法竞赛资深出题人，拥有多年 ACM/ICPC、IOI、Codeforces 出题经验。你的任务是根据给定的参数生成一道高质量的算法竞赛题目。

核心原则：
1. 题目必须原创，不得与已知经典题目雷同。
2. 题面描述必须严谨、无歧义，所有变量和约束必须明确定义。
3. 题目难度必须与指定的 difficulty rating 精确匹配。
4. 题目必须有优雅的核心算法思路，不能是纯暴力或纯模拟。
5. 样例必须具有代表性，帮助选手理解题意。


生成前请静默完成质量预检（不要把预检过程输出到 JSON）：
- 固定一个明确目标，形式化所有输入、输出、变量、边界和并列规则。
- 同时推导预期算法与自然暴力基线，确认数据范围确实需要核心观察。
- 用最小、最大、重复、相等、溢出和退化情形攻击题面与样例。
- 故事只作为形式化任务的薄包装，不能把必要语义藏在叙事中。
- 最后核对样例、约束、标签和难度说明的一致性，只输出最终 JSON。

输出格式要求：
- 题面使用 Markdown 格式，数学公式使用 LaTeX（用 $...$ 行内，$$...$$ 行间）。
- 严格按照指定的 JSON schema 输出。
- 不要输出任何 JSON 之外的内容。`

// statementUserTemplate is the Go template for the user message.
const statementUserTemplate = `请根据以下参数生成一道算法竞赛题目。

## 题目级别
- 级别：{{.LevelContext.Level}}
- 描述：{{.LevelContext.Description}}
- 风格指导：{{.LevelContext.StyleGuidance}}

## 难度参数
- 目标 difficulty rating：{{.Difficulty}}
- 有效范围：[{{.LevelContext.DifficultyMin}}, {{.LevelContext.DifficultyMax}}]
{{- if eq (printf "%s" .LevelContext.Level) "syntax"}}
- 难度校准：800=最基础的输入输出和简单运算，900=简单循环和条件判断，1000=嵌套循环和字符串基本操作，1100=简单模拟和数组操作，1200=需要一定思维的基础编程题
{{- else if eq (printf "%s" .LevelContext.Level) "gplt_l1"}}
- 难度校准（天梯赛 L1，本题分值见下方 gplt_score 字段）：800=5 分基础 I/O 题，1000=10 分简单循环条件，1100=15 分多步骤模拟，1200-1300=20 分稍带思维的综合模拟
{{- else if eq (printf "%s" .LevelContext.Level) "gplt_l2"}}
- 难度校准（天梯赛 L2，总分 25）：L2 的"难度"不等同于"代码量大"。代码量可能比 20 分 L1 还少，关键是题目要能指向某个具体的数据结构或算法（栈/队列/链表/树/并查集/基础图论/基础 DP/二分/贪心）。1000-1300=基础数据结构直接应用，1400-1600=常见算法套路题，1700-1800=综合数据结构/算法应用题
{{- else if eq (printf "%s" .LevelContext.Level) "gplt_l3"}}
- 难度校准（天梯赛 L3，总分 30）：1700-1900=中等难度 DP/图论，2000-2300=需要关键观察的综合题，2400-2600=高阶数据结构/复杂建模/数学+算法结合
{{- else}}
- 难度校准：800-1000=简单贪心/模拟，1100-1400=基础DP/图论/二分，1500-1800=中等难度组合技巧，1900-2200=较难的算法题需要深入思考，2300-2600=高难度题需要巧妙观察，2700-3000=极难题通常涉及高级数据结构或数论，3100-3500=最高难度题需要极强的数学和算法功底
{{- end}}

## 标签要求
- 可用标签：[{{join .LevelContext.AvailableTags ", "}}]
- 指定标签：[{{join .Tags ", "}}]
- 题目的核心算法/知识点必须覆盖指定标签。

## 执行限制
- 时间限制：{{.TimeLimit}} ms
- 内存限制：{{.MemoryLimit}} MB

{{- if .ContestStyle}}

## 比赛风格
- 风格：{{.ContestStyle}}
{{- end}}

{{- if .CustomPrompt}}

## 附加要求
{{.CustomPrompt}}
{{- end}}

## 输出 JSON Schema

请严格按照以下 JSON 格式输出，不要添加任何额外文字：

` + "```json" + `
{
  "title": "题目标题（中文，简洁有力）",
  "statement": "完整题面（Markdown 格式，包含：\n## 题目描述\n...\n## 输入格式\n...\n## 输出格式\n...\n## 样例\n### 样例输入 1\n...\n### 样例输出 1\n...\n### 样例说明 1\n...\n## 数据范围与约定\n...）",
  "tags": ["tag1", "tag2"],
  "one_line_hint": "一句话提示（不超过50字）",
  "difficulty_justification": "解释为什么这道题的难度评级是准确的（包含预期解法的时间复杂度分析）"
}
` + "```"

// StatementInput holds the data needed to render the statement generation
// prompt template.
type StatementInput struct {
	// LevelContext provides level-specific calibration data.
	LevelContext LevelContext

	// Difficulty is the target difficulty rating.
	Difficulty int

	// Tags to target.
	Tags []string

	// TimeLimit in milliseconds.
	TimeLimit int

	// MemoryLimit in megabytes.
	MemoryLimit int

	// ContestStyle (e.g. "codeforces", "icpc").
	ContestStyle string

	// CustomPrompt is optional additional instructions.
	CustomPrompt string
}

// NewStatementInput creates a StatementInput from ProblemGenParams and
// available tags.
func NewStatementInput(params domain.ProblemGenParams, tags []domain.TagCategory) StatementInput {
	return StatementInput{
		LevelContext: NewLevelContext(params.Level, tags, params.Difficulty),
		Difficulty:   params.Difficulty,
		Tags:         params.Tags,
		TimeLimit:    params.TimeLimit,
		MemoryLimit:  params.MemoryLimit,
		ContestStyle: params.ContestStyle,
		CustomPrompt: params.CustomPrompt,
	}
}

// StatementTemplate returns the prompt template for problem statement
// generation.
func StatementTemplate() *PromptTemplate {
	return &PromptTemplate{
		Name:               TemplateNameStatement,
		Version:            PromptVersionV1,
		SystemPrompt:       statementSystemPrompt,
		UserPromptTemplate: statementUserTemplate,
	}
}

// StatementFewShotMessages returns few-shot example messages that can be
// prepended to the statement generation prompt to improve output quality.
// The examples differ based on the problem level.
func StatementFewShotMessages(level domain.ProblemLevel) []llm.Message {
	switch level {
	case domain.LevelSyntax:
		return syntaxFewShotMessages()
	case domain.LevelAlgorithm:
		return algorithmFewShotMessages()
	case domain.LevelGPLTL1, domain.LevelGPLTL2, domain.LevelGPLTL3:
		return gpltFewShotMessages(level)
	default:
		return nil
	}
}

// syntaxFewShotMessages returns example messages for syntax-level problems.
func syntaxFewShotMessages() []llm.Message {
	return []llm.Message{
		{
			Role: "user",
			Content: `请根据以下参数生成一道算法竞赛题目。

## 题目级别
- 级别：syntax
- 目标 difficulty rating：900
- 指定标签：[loops, conditionals]

## 执行限制
- 时间限制：1000 ms
- 内存限制：256 MB

请严格按照 JSON 格式输出。`,
		},
		{
			Role: "assistant",
			Content: `{
  "title": "数字分类统计",
  "statement": "## 题目描述\n\n给定 $n$ 个整数 $a_1, a_2, \\ldots, a_n$，请统计其中正数、零和负数各有多少个。\n\n## 输入格式\n\n第一行包含一个整数 $n$（$1 \\le n \\le 1000$）。\n\n第二行包含 $n$ 个整数 $a_1, a_2, \\ldots, a_n$（$-10^6 \\le a_i \\le 10^6$）。\n\n## 输出格式\n\n输出一行，包含三个整数，分别表示正数、零和负数的个数，用空格分隔。\n\n## 样例\n\n### 样例输入 1\n` + "```" + `\n5\n3 -1 0 2 -5\n` + "```" + `\n\n### 样例输出 1\n` + "```" + `\n2 1 2\n` + "```" + `\n\n### 样例说明 1\n\n正数有 $3, 2$ 共 2 个；零有 1 个；负数有 $-1, -5$ 共 2 个。\n\n## 数据范围与约定\n\n- $1 \\le n \\le 1000$\n- $-10^6 \\le a_i \\le 10^6$",
  "tags": ["loops", "conditionals"],
  "one_line_hint": "遍历数组，根据正负零分别计数",
  "difficulty_justification": "本题只需一次遍历加条件判断，时间复杂度 O(n)，属于最基本的循环+条件语句练习，适合 900 rating。"
}`,
		},
	}
}

// algorithmFewShotMessages returns example messages for algorithm-level problems.
func algorithmFewShotMessages() []llm.Message {
	return []llm.Message{
		{
			Role: "user",
			Content: `请根据以下参数生成一道算法竞赛题目。

## 题目级别
- 级别：algorithm
- 目标 difficulty rating：1500
- 指定标签：[dp, greedy]

## 执行限制
- 时间限制：2000 ms
- 内存限制：256 MB

请严格按照 JSON 格式输出。`,
		},
		{
			Role: "assistant",
			Content: `{
  "title": "最优任务调度",
  "statement": "## 题目描述\n\n有 $n$ 个任务需要完成，第 $i$ 个任务需要 $t_i$ 单位时间，且有截止时间 $d_i$。如果在截止时间之前完成任务 $i$，可以获得 $p_i$ 的奖励。\n\n你一次只能执行一个任务，且每个任务一旦开始就必须连续完成。请求出能获得的最大总奖励。\n\n## 输入格式\n\n第一行包含一个整数 $n$（$1 \\le n \\le 2000$）。\n\n接下来 $n$ 行，每行三个整数 $t_i, d_i, p_i$（$1 \\le t_i \\le 10^4$，$1 \\le d_i \\le 10^5$，$1 \\le p_i \\le 10^6$）。\n\n## 输出格式\n\n输出一个整数，表示能获得的最大总奖励。\n\n## 样例\n\n### 样例输入 1\n` + "```" + `\n3\n2 4 10\n1 2 5\n3 6 8\n` + "```" + `\n\n### 样例输出 1\n` + "```" + `\n23\n` + "```" + `\n\n### 样例说明 1\n\n最优策略：先完成任务 2（时间 0-1，截止 2，获得 5），再完成任务 1（时间 1-3，截止 4，获得 10），最后完成任务 3（时间 3-6，截止 6，获得 8）。总奖励 $5+10+8=23$。\n\n## 数据范围与约定\n\n- $1 \\le n \\le 2000$\n- $1 \\le t_i \\le 10^4$\n- $1 \\le d_i \\le 10^5$\n- $1 \\le p_i \\le 10^6$",
  "tags": ["dp", "greedy"],
  "one_line_hint": "按截止时间排序后，用背包DP求解",
  "difficulty_justification": "本题需要观察到按截止时间排序的贪心性质，然后转化为类似0-1背包的DP问题。时间复杂度 O(n * max_d)，需要一定的建模能力，适合 1500 rating。"
}`,
		},
	}
}
