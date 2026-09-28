package sources

import (
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

var luoguTask = regexp.MustCompile(`^/problem/[A-Za-z0-9_]+/?$`)

func isLuoguTask(u *url.URL) bool {
	host := strings.ToLower(u.Hostname())
	return (host == "www.luogu.com.cn" || host == "luogu.com.cn") && luoguTask.MatchString(u.Path)
}

// Luogu's SEO HTML embeds unescaped Markdown. Parsing inequalities such as
// <t can silently swallow the rest of a paragraph, and samples live only in
// page state. Read the same public page's original Markdown without running JS.
func parseLuoguProblem(root *html.Node, base *url.URL) (Item, error) {
	node := findNode(root, func(n *html.Node) bool {
		return element(n, "script") && attr(n, "id") == "lentille-context" && attr(n, "type") == "application/json"
	})
	invalid := func() (Item, error) {
		return Item{}, sourceError("SOURCE_INVALID_CONTENT", "未取得完整的洛谷原始题面和样例，不能使用可能截断公式的网页摘要")
	}
	if node == nil {
		return invalid()
	}
	var state struct {
		Template string `json:"template"`
		Status   int    `json:"status"`
		Data     struct {
			Problem struct {
				PID        string          `json:"pid"`
				Difficulty json.RawMessage `json:"difficulty"`
				Name       string          `json:"name"`
				Content    struct {
					Name        string `json:"name"`
					Background  string `json:"background"`
					Description string `json:"description"`
					Input       string `json:"formatI"`
					Output      string `json:"formatO"`
					Hint        string `json:"hint"`
				} `json:"content"`
				Samples [][]string `json:"samples"`
			} `json:"problem"`
		} `json:"data"`
	}
	if json.Unmarshal([]byte(rawText(node, 0)), &state) != nil || state.Status != 200 || state.Template != "problem.show" {
		return invalid()
	}
	problem := state.Data.Problem
	if problem.PID != path.Base(strings.TrimRight(base.Path, "/")) || strings.TrimSpace(problem.Content.Description) == "" || problem.Samples == nil {
		return invalid()
	}
	name := problem.Content.Name
	if strings.TrimSpace(name) == "" {
		name = problem.Name
	}
	if strings.TrimSpace(name) == "" {
		return invalid()
	}
	title := problem.PID + " " + name
	var body strings.Builder
	fmt.Fprintf(&body, "# %s\n\n", title)
	section := func(label, text string) {
		if strings.TrimSpace(text) != "" {
			fmt.Fprintf(&body, "## %s\n\n%s\n\n", label, text)
		}
	}
	section("题目背景", problem.Content.Background)
	section("题目描述", problem.Content.Description)
	section("输入格式", problem.Content.Input)
	section("输出格式", problem.Content.Output)
	for i, sample := range problem.Samples {
		if len(sample) != 2 {
			return invalid()
		}
		for j, label := range []string{"输入样例", "输出样例"} {
			fence := codeFence(sample[j], 3)
			fmt.Fprintf(&body, "## %s %d\n\n%s\n%s", label, i+1, fence, sample[j])
			if !strings.HasSuffix(sample[j], "\n") {
				body.WriteByte('\n')
			}
			fmt.Fprintf(&body, "%s\n\n", fence)
		}
	}
	section("说明/提示", problem.Content.Hint)
	statement := normalizeMarkdownReferences(strings.TrimSpace(body.String()), base)
	item := statementItem(base.String(), title, statement, "")
	// Preserve the native ordinal without inventing a cross-platform mapping.
	value := strings.Trim(string(problem.Difficulty), "\"")
	item.Difficulty = nativeLevel("luogu", "luogu_level", value, "", base.String())
	return item, nil
}
