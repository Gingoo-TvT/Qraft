package sources

import (
	"fmt"
	"golang.org/x/net/html"
	"net/url"
	"regexp"
	"strings"
)

var nowcoderTask = regexp.MustCompile(`^/practice/[a-fA-F0-9]+/?$`)

func isNowcoderTask(u *url.URL) bool {
	return (u.Hostname() == "www.nowcoder.com" || u.Hostname() == "nowcoder.com") && nowcoderTask.MatchString(u.Path)
}

// The public practice page includes an accessible statement and sample DOM
// before its application shell. Read that region, never the entire loading UI.
func parseNowcoderProblem(root *html.Node, base *url.URL, title string) (Item, error) {
	heading := findNode(root, func(n *html.Node) bool { return element(n, "h5") && strings.Contains(rawText(n, 0), "输入描述") })
	if heading == nil || heading.Parent == nil {
		return Item{}, sourceError("SOURCE_EMPTY", "牛客页面未提供完整公开题面，请粘贴题面")
	}
	content := heading.Parent
	if element(content, "body") || element(content, "html") {
		return Item{}, sourceError("SOURCE_EMPTY", "无法确定牛客题面区域，请粘贴题面")
	}
	// Description <pre> elements contain prose, while question-oi samples are
	// actual code blocks. Convert only the former before generic rendering.
	walk(content, 0, func(n *html.Node) {
		if !element(n, "pre") {
			return
		}
		sample := false
		for p := n.Parent; p != nil && p != content; p = p.Parent {
			if hasClass(p, "question-oi") {
				sample = true
				break
			}
		}
		if !sample {
			n.Data = "div"
		}
	})
	body := cleanMarkdown((&markdownRenderer{base: base}).render(content, 0))
	if strings.TrimSpace(body) == "" {
		return Item{}, sourceError("SOURCE_EMPTY", "牛客题面为空")
	}
	// Samples can also be supplied as public textarea fields on some templates.
	if findNode(content, func(n *html.Node) bool { return hasClass(n, "question-oi") }) == nil {
		i := 0
		walk(root, 0, func(n *html.Node) {
			if !hasClass(n, "js-sample-io") {
				return
			}
			input := findNode(n, func(c *html.Node) bool { return element(c, "textarea") && attr(c, "data-type") == "input" })
			output := findNode(n, func(c *html.Node) bool { return element(c, "textarea") && attr(c, "data-type") == "output" })
			if input == nil || output == nil {
				return
			}
			i++
			for j, node := range []*html.Node{input, output} {
				label := "输入"
				if j == 1 {
					label = "输出"
				}
				text := rawText(node, 0)
				fence := codeFence(text, 3)
				body += fmt.Sprintf("\n\n#### %s #%d\n\n%s\n%s\n%s", label, i, fence, text, fence)
			}
		})
	}
	title = strings.TrimSuffix(strings.TrimSuffix(title, "_牛客网"), "_牛客题霸")
	return statementItem(base.String(), title, normalizeMarkdownReferences(body, base), ""), nil
}
