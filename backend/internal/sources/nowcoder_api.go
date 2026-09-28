package sources

import (
	"encoding/json"
	"fmt"
	"golang.org/x/net/html"
	"net/url"
	"path"
	"strings"
)

// Read only the public statement fields, never answer/submission/user data.
func parseNowcoderAPI(original string, body []byte) (*Document, error) {
	var payload struct {
		Code *int `json:"code"`
		Data struct {
			Question *struct {
				UUID       string          `json:"uuid"`
				Difficulty json.RawMessage `json:"difficulty"`
				Title      string          `json:"title"`
				Content    string          `json:"content"`
				Input      string          `json:"inputDesc"`
				Output     string          `json:"outputDesc"`
				Hint       string          `json:"hint"`
				Paid       bool            `json:"paidQuestion"`
				Hidden     bool            `json:"shield"`
				Deleted    bool            `json:"delete"`
				Samples    []struct {
					Input  string `json:"input"`
					Output string `json:"output"`
					Note   string `json:"note"`
				} `json:"samples"`
			} `json:"question"`
		} `json:"data"`
	}
	invalid := func() (*Document, error) {
		return nil, sourceError("SOURCE_INVALID_CONTENT", "未取得牛客完整题面、公式与样例，不能使用残缺网页摘要")
	}
	if json.Unmarshal(body, &payload) != nil || payload.Code == nil || *payload.Code != 0 || payload.Data.Question == nil {
		return invalid()
	}
	q := payload.Data.Question
	if q.Paid || q.Hidden || q.Deleted {
		return nil, sourceError("SOURCE_ACCESS_DENIED", "题目不是可直接读取的公开题目")
	}
	base, err := url.Parse(original)
	if err != nil {
		return invalid()
	}
	if !strings.EqualFold(q.UUID, path.Base(strings.TrimRight(base.Path, "/"))) || strings.TrimSpace(q.Title) == "" || strings.TrimSpace(q.Content) == "" || strings.TrimSpace(q.Output) == "" || len(q.Samples) == 0 {
		return invalid()
	}
	prose := func(text string) string {
		root, e := html.Parse(strings.NewReader(text))
		if e != nil {
			return ""
		}
		return cleanMarkdown((&markdownRenderer{base: base}).render(root, 0))
	}
	var b strings.Builder
	section := func(label, text string) {
		if s := prose(text); s != "" {
			fmt.Fprintf(&b, "## %s\n\n%s\n\n", label, s)
		}
	}
	section("题目描述", q.Content)
	section("输入格式", q.Input)
	section("输出格式", q.Output)
	for i, sample := range q.Samples {
		if sample.Output == "" {
			return invalid()
		}
		for j, text := range []string{sample.Input, sample.Output} {
			label := "输入样例"
			if j == 1 {
				label = "输出样例"
			}
			fence := codeFence(text, 3)
			fmt.Fprintf(&b, "## %s %d\n\n%s\n%s", label, i+1, fence, text)
			if !strings.HasSuffix(text, "\n") {
				b.WriteByte('\n')
			}
			fmt.Fprintf(&b, "%s\n\n", fence)
		}
		if strings.TrimSpace(sample.Note) != "" {
			fmt.Fprintf(&b, "样例说明：%s\n\n", prose(sample.Note))
		}
	}
	section("说明/提示", q.Hint)
	item := statementItem(original, q.Title, strings.TrimSpace(b.String()), "")
	item.Difficulty = nativeLevel("nowcoder", "nowcoder_level", strings.Trim(string(q.Difficulty), "\""), "", original)
	return &Document{URL: original, FinalURL: original, Title: item.Title, Kind: KindProblem, Items: []Item{item}}, nil
}
