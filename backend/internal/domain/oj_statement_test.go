package domain

import (
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

func TestOJStatementContractAndSampleRoundTrip(t *testing.T) {
	s := OJStatement{Version: 1, Description: "输入成绩 $score$，输出评语。", Input: "一个整数。", Output: "一行评语。", Constraints: `- $0 \le score \le 100$`, Reason: "统一格式", Samples: []OJSample{{Input: "65\n", Output: "Pass\n", Origin: "original"}, {Input: "```literal\n", Output: "ok", Origin: "original"}}}
	source := "65\nPass\n```literal\nok"
	require.NoError(t, s.Validate(source))
	markdown := s.Markdown()
	require.True(t, strings.HasPrefix(markdown, "#### 题目描述\n"))
	for _, heading := range []string{"#### 输入格式", "#### 输出格式", "#### 输入输出样例 #1", "#### 输入 #1", "#### 输出 #1", "#### 数据规模与约定"} {
		require.Contains(t, markdown, heading)
	}
	samples := ExtractStatementSamples(markdown)
	require.Len(t, samples, 2)
	require.Equal(t, "```literal", samples[1].Input)
	require.Contains(t, markdown, "````\n```literal\n````")
	s.Samples = nil
	require.Error(t, s.Validate(source))
}
func TestOJStatementRejectsOmittedOriginalAndFabricatedConstraintsHeading(t *testing.T) {
	source := "## Sample Input 1\n```\n2\n```\n## Sample Output 1\n```\n4\n```\n## Sample Input 2\n```\n3\n```\n## Sample Output 2\n```\n9\n```"
	s := OJStatement{Version: 1, Description: "Square", Input: "n", Output: "n*n", Constraints: "1<=n<=9", Reason: "format", Samples: []OJSample{{Input: "2", Output: "4", Origin: "original"}}}
	require.ErrorContains(t, s.Validate(source), "omitted")
	s.Samples = append(s.Samples, OJSample{Input: "3", Output: "9", Origin: "original"})
	require.NoError(t, s.Validate(source))
	s.Description = "\\#### 题目描述\nSquare"
	require.ErrorContains(t, s.Validate(source), "headings")
}
func TestOJStatementAllowsEmptyInputAndPreservesWhitespace(t *testing.T) {
	s := OJStatement{Version: 1, Description: "Print", Input: "无输入。", Output: "Print spaces", Constraints: "无额外约束。", Reason: "format", Samples: []OJSample{{Input: "", Output: "  *  ", Origin: "generated"}}}
	require.NoError(t, s.Validate("Print"))
	require.Contains(t, s.Markdown(), "\n  *  \n```")
}

func TestOJStatementRetainsOriginalFormulaImage(t *testing.T) {
	source := "原题公式：![](<https://example.org/formula.png>)"
	value := OJStatement{Version: 1, Description: "求值", Input: "三个整数", Output: "结果", Constraints: "无额外范围", Reason: "整理", Samples: []OJSample{{Input: "1", Output: "1", Origin: "generated"}}}
	require.ErrorContains(t, value.Validate(source), "![](<https://example.org/formula.png>)")
	value.Description += " https://example.org/formula.png"
	if value.Validate(source) == nil {
		t.Fatal("bare link replaced the formula image")
	}
	value.Description += "\n\n![](<https://example.org/formula.png>)"
	if err := value.Validate(source); err != nil {
		t.Fatal(err)
	}
}
