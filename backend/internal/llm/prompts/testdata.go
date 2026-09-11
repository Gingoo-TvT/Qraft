package prompts

import (
	"encoding/json"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/testdatagen"
)

// TemplateName constant for test data generation.
const (
	TemplateNameTestData = "generate_testdata"
)

// testdataSystemPrompt is the system prompt for test data generation.
const testdataSystemPrompt = `你是一位算法竞赛资深出题人。根据题面约束、测试意图和可能的错误解法设计数据。
优先输出框架生成方案 generator_recipe，通用构造调用现成库，特殊约束用少量专用 C++ 实现。
只有样例和少量微型边界允许直接写原始输入。输出仅包含一个有效 JSON 对象。
未指定固定数量时，选择 10-20 个最少且充分的不同测试点；自定义用例计入总数，禁止随机数据凑数。
数据描述说明测试目的，不能把描述或模型判断视为实际验证结果。
` + testdatagen.Prompt

// testdataUserTemplate is the Go template for the user message.
const testdataUserTemplate = `请为以下竞赛题目生成测试数据。

## 题目信息

- 标题：{{.Title}}
- 难度：{{.Difficulty}}
- 标签：[{{join .Tags ", "}}]
- 时间限制：{{.TimeLimit}} ms
- 内存限制：{{.MemoryLimit}} MB

## 题面

{{.Statement}}

## 数据生成配置

{{- if or (eq .Config.NumTestCases 0) .Config.AutoCaseCount}}
- 最终测试点数量：由模型根据 corner case 自行选择，必须在 10-20 个（含）之间；自定义用例也计入总数。
{{- else}}
- 总测试用例数：{{.Config.NumTestCases}}（固定）
{{- end}}
- 样例数量：{{.Config.NumSamples}}
{{- if .Config.Groups}}

### 测试分组
{{- range $i, $g := .Config.Groups}}
- 组 {{$g.GroupID}}：{{$g.NumCases}} 个用例，分值 {{$g.Score}}{{if $g.Description}}（{{$g.Description}}）{{end}}{{if $g.BruteCheck}} [将与暴力对拍，规模请保证暴力 O(n²)/O(n³) 能在 3 秒内跑完]{{else}} [仅标程验证，可使用较大规模]{{end}}
{{- if $g.Constraints}}
  约束：
  {{- range $k, $v := $g.Constraints}}
    - {{$k}}: [{{$v.Min}}, {{$v.Max}}]
  {{- end}}
{{- end}}
{{- end}}
{{- end}}

### 边界配置
- 包含最小值用例：{{.Config.BoundaryConfig.IncludeMinCase}}
- 包含最大值用例：{{.Config.BoundaryConfig.IncludeMaxCase}}
- 包含零值用例：{{.Config.BoundaryConfig.IncludeZero}}

{{- if .Config.CustomCases}}

### 自定义用例
{{- range $i, $c := .Config.CustomCases}}
- 用例 {{$i}}: {{if $c.IsSample}}(样例) {{end}}{{$c.Description}}
{{- end}}
{{- end}}

## 生成方式

{{- if .PreferGenerator}}
请输出 generator_recipe 和 test_cases。每个非内联测试点的 input 留空，服务端编译执行生成方案。
{{- else}}
请输出样例及微型边界输入；需要大规模或随机数据时仍使用 generator_recipe。
{{- end}}

## 输出 JSON Schema

{
  "test_case_count": 12,
  "test_cases": [
    {"input": "", "group_id": 1, "is_sample": false, "description": "说明要检测的错误或边界", "coverage": ["threshold", "adversarial"], "output_limit_bytes": 1048576}
  ],
  "generator_recipe": {
    "version": "algoforge.testdata.v1",
    "code": "void generate(long long i, long long g, af::Random& rng, std::ostream& out) { out << rng.integer(1, 100); }"
  }
}
上面仅展示条目结构。实际必须列出全部测试点，test_case_count 为包含自定义输入后的最终总数。
生成函数使用原始 test_cases 的零基下标，内联样例也占下标。生成方案和旧 generator_code 只能选择一种。
`

// TestDataInput holds the data needed to render the test data generation
// prompt template.
type TestDataInput struct {
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

	// Config is the test data generation configuration.
	Config domain.TestDataConfig

	// PreferGenerator controls whether to generate a testlib generator
	// (true) or raw test cases (false).
	PreferGenerator bool
}

// ConfigJSON returns the TestDataConfig serialised as indented JSON for
// debugging or logging purposes.
func (tdi *TestDataInput) ConfigJSON() string {
	b, err := json.MarshalIndent(tdi.Config, "", "  ")
	if err != nil {
		return "{}"
	}
	return string(b)
}

// TestDataTemplate returns the prompt template for test data generation.
func TestDataTemplate() *PromptTemplate {
	return &PromptTemplate{
		Name:               TemplateNameTestData,
		Version:            PromptVersionV1,
		SystemPrompt:       testdataSystemPrompt,
		UserPromptTemplate: testdataUserTemplate,
	}
}
