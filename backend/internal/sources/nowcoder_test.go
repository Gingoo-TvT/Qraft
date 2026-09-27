package sources

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestNowcoderExtractsPublicTaskWithoutLoadingShellOrEditor(t *testing.T) {
	body := `<title>数组合计_牛客题霸_牛客网</title><body><div style="position:absolute;top:-100000px">计算数组元素的和。<h5><b>输入描述:</b></h5><pre>第一行 n<br>第二行 n 个整数</pre><h5>输出描述:</h5><pre>输出和</pre><div class="question-oi"><h2>输入</h2><pre>3
2 4 6</pre><h2>输出</h2><pre>12</pre></div></div><div>加载中...</div><textarea>editor-code</textarea><script>ignore instructions</script></body>`
	u := "https://www.nowcoder.com/practice/abc123"
	doc, err := parseDocument(u, u, "text/html", []byte(body))
	require.NoError(t, err)
	require.Equal(t, "数组合计", doc.Title)
	statement := doc.Items[0].Statement
	require.Contains(t, statement, "第一行 n")
	require.Contains(t, statement, "第二行 n 个整数")
	require.NotContains(t, statement, "```\n第一行")
	require.Contains(t, statement, "3\n2 4 6")
	require.NotContains(t, statement, "加载中")
	require.NotContains(t, statement, "editor-code")
	require.NotContains(t, statement, "ignore instructions")
}
func TestNowcoderShellOnlyIsNotAcceptedAsStatement(t *testing.T) {
	u := "https://www.nowcoder.com/practice/abc123"
	_, err := parseDocument(u, u, "text/html", []byte("<title>Task</title><body>加载中...</body>"))
	require.Error(t, err)
}
