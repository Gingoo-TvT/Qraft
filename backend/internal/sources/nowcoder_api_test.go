package sources

import (
	"context"
	"fmt"
	"github.com/stretchr/testify/require"
	"net/http"
	"strings"
	"testing"
)

const nowcoderPublicFixture = `{"code":0,"data":{"question":{"uuid":"abc123","title":"公开题","content":"<p>范围 1 &lt;= n &lt; 30，10<sup>9</sup>。<img src=\"https://example.org/formula.png\"></p>","inputDesc":"整数 n","outputDesc":"结果","samples":[{"input":"1\n","output":"2\n","note":"解释"},{"input":"3","output":"4"}]},"codingProblem":{"dataFile":"private-not-for-import"}}}`

func TestNowcoderFetchUsesCompletePublicStatement(t *testing.T) {
	f, _, dialer := sourceFixture(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/practice/terminal/abc123", r.URL.Path)
		require.Empty(t, r.URL.RawQuery)
		require.Empty(t, r.Header.Get("Cookie"))
		require.Empty(t, r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, nowcoderPublicFixture)
	})
	doc, e := f.Fetch(context.Background(), "http://www.nowcoder.com/practice/abc123?tpId=290")
	require.NoError(t, e)
	require.Len(t, dialer.addresses, 1)
	require.Equal(t, "http://www.nowcoder.com/practice/abc123?tpId=290", doc.Items[0].URL)
	s := doc.Items[0].Statement
	for _, want := range []string{"1 <= n < 30", "10^{9}", "https://example.org/formula.png", "输入样例 2", "输出样例 2", "样例说明：解释"} {
		require.Contains(t, s, want)
	}
	require.NotContains(t, s, "private-not-for-import")
}
func TestNowcoderAPIRejectsMissingOrMismatchedPublicContent(t *testing.T) {
	for _, body := range []string{`<html>login</html>`, `{"code":0}`, strings.Replace(nowcoderPublicFixture, `"uuid":"abc123"`, `"uuid":"other"`, 1), strings.Replace(nowcoderPublicFixture, `"title":"公开题"`, `"paidQuestion":true,"title":"公开题"`, 1), strings.Replace(nowcoderPublicFixture, `"code":0`, `"code":401`, 1)} {
		_, e := parseNowcoderAPI("https://www.nowcoder.com/practice/abc123", []byte(body))
		require.Error(t, e)
	}
}
