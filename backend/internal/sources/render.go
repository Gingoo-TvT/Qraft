package sources

import (
	"net/url"
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

type markdownRenderer struct {
	base    *url.URL
	exclude *html.Node
}

func (r *markdownRenderer) render(n *html.Node, depth int) string {
	if n == nil || n == r.exclude || depth > 128 {
		return ""
	}
	if n.Type == html.TextNode {
		// Codeforces uses $$$ delimiters; normalize only prose, not code blocks.
		return strings.ReplaceAll(n.Data, "$$$", "$")
	}
	if n.Type != html.ElementNode && n.Type != html.DocumentNode {
		return ""
	}
	children := func() string {
		var out strings.Builder
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			out.WriteString(r.render(child, depth+1))
		}
		return out.String()
	}
	if hasClass(n, "katex") || hasClass(n, "katex-display") || element(n, "math") {
		if annotation := findNode(n, func(v *html.Node) bool {
			return element(v, "annotation") && strings.Contains(strings.ToLower(attr(v, "encoding")), "tex")
		}); annotation != nil {
			delimiter := "$"
			if attr(n, "display") == "block" || hasClass(n, "katex-display") {
				delimiter = "$$"
			}
			return delimiter + strings.TrimSpace(rawText(annotation, 0)) + delimiter
		}
	}
	if element(n, "script") && strings.HasPrefix(strings.ToLower(attr(n, "type")), "math/tex") {
		delimiter := "$"
		if strings.Contains(attr(n, "type"), "mode=display") {
			delimiter = "$$"
		}
		return delimiter + strings.TrimSpace(rawText(n, 0)) + delimiter
	}
	switch n.Data {
	case "script", "style", "nav", "header", "footer", "form", "button", "input", "select", "textarea", "iframe", "noscript", "svg", "head":
		return ""
	case "pre":
		code := strings.Trim(rawText(n, 0), "\n")
		fence := codeFence(code, 3)
		return "\n\n" + fence + "\n" + code + "\n" + fence + "\n\n"
	case "var":
		// AtCoder stores inline LaTeX in <var> rather than math/tex scripts.
		// Keep the formula source intact; prose rendering can alter its TeX.
		formula := strings.TrimSpace(rawText(n, 0))
		if formula == "" {
			return ""
		}
		return "$" + formula + "$"
	case "code":
		code := rawText(n, 0)
		fence := codeFence(code, 1)
		return fence + " " + code + " " + fence
	case "br":
		return "\n"
	case "hr":
		return "\n\n---\n\n"
	case "h1", "h2", "h3", "h4", "h5", "h6":
		return "\n\n" + strings.Repeat("#", int(n.Data[1]-'0')) + " " + strings.TrimSpace(children()) + "\n\n"
	case "p", "section", "article", "main", "div":
		if hasClass(n, "section-title") {
			return "\n\n## " + strings.TrimSpace(children()) + "\n\n"
		}
		return "\n\n" + children() + "\n\n"
	case "li":
		return "\n- " + strings.TrimSpace(children()) + "\n"
	case "ul", "ol":
		return "\n" + children() + "\n"
	case "strong", "b":
		return "**" + children() + "**"
	case "em", "i":
		return "*" + children() + "*"
	case "sup":
		return "^{" + children() + "}"
	case "sub":
		return "_{" + children() + "}"
	case "a":
		text := strings.TrimSpace(children())
		href := referenceURL(r.base, attr(n, "href"))
		if href == "" {
			return text
		}
		if text == "" {
			text = href
		}
		return "[" + strings.ReplaceAll(text, "]", "\\]") + "](<" + href + ">)"
	case "img":
		src := referenceURL(r.base, attr(n, "src"))
		if src == "" {
			src = referenceURL(r.base, attr(n, "data-src"))
		}
		if src == "" {
			return ""
		}
		alt := strings.ReplaceAll(attr(n, "alt"), "]", "\\]")
		return "![" + alt + "](<" + src + ">)"
	case "table":
		return r.table(n, depth+1)
	case "blockquote":
		return "\n\n> " + strings.ReplaceAll(strings.TrimSpace(children()), "\n", "\n> ") + "\n\n"
	}
	return children()
}

func (r *markdownRenderer) table(n *html.Node, depth int) string {
	rows := [][]string{}
	var visit func(*html.Node, int)
	visit = func(node *html.Node, level int) {
		if node == nil || level > 128 {
			return
		}
		if element(node, "table") && node != n {
			return
		}
		if element(node, "tr") {
			cells := []string{}
			for cell := node.FirstChild; cell != nil; cell = cell.NextSibling {
				if element(cell, "td") || element(cell, "th") {
					text := strings.TrimSpace(r.render(cell, depth+1))
					text = strings.ReplaceAll(strings.ReplaceAll(text, "|", "\\|"), "\n", "<br>")
					cells = append(cells, text)
				}
			}
			if len(cells) > 0 {
				rows = append(rows, cells)
			}
			return
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child, level+1)
		}
	}
	visit(n, depth)
	if len(rows) == 0 {
		return ""
	}
	width := 0
	for _, row := range rows {
		if len(row) > width {
			width = len(row)
		}
	}
	var out strings.Builder
	out.WriteString("\n\n")
	for i, row := range rows {
		// Only the header needs padding: Markdown treats absent data cells as
		// empty. Padding every row could amplify a hostile ragged table.
		if i == 0 {
			for len(row) < width {
				row = append(row, "")
			}
		}
		out.WriteString("| " + strings.Join(row, " | ") + " |\n")
		if i == 0 {
			out.WriteString("|" + strings.Repeat(" --- |", width) + "\n")
		}
	}
	out.WriteString("\n")
	return out.String()
}

func referenceURL(base *url.URL, raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.HasPrefix(raw, "#") {
		return ""
	}
	reference, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	resolved := base.ResolveReference(reference)
	if _, err := validateURL(resolved.String()); err != nil {
		return ""
	}
	// Angle-delimited Markdown URLs must not break out of their destination.
	return strings.NewReplacer("<", "%3C", ">", "%3E", "\n", "%0A", "\r", "%0D").Replace(resolved.String())
}

func cleanMarkdown(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	lines := strings.Split(value, "\n")
	out := make([]string, 0, len(lines))
	blank := false
	inCode := false
	fence := ""
	for _, line := range lines {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "```") {
			if !inCode {
				inCode = true
				fence = trim
			} else if trim == fence {
				inCode = false
			}
		}
		if inCode {
			out = append(out, line)
			blank = false
			continue
		}
		if trim == "" {
			if !blank {
				out = append(out, "")
			}
			blank = true
			continue
		}
		out = append(out, strings.TrimRight(line, " \t"))
		blank = false
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

var markdownLink = regexp.MustCompile(`(!?\[[^\]\n]*\])\((<?[^)\s]+>?)([^)]*)\)`)

// Resolve ordinary inline Markdown links while leaving fenced code unchanged.
func normalizeMarkdownReferences(text string, base *url.URL) string {
	lines := strings.Split(text, "\n")
	fence := ""
	rewrite := func(text string) string {
		return markdownLink.ReplaceAllStringFunc(text, func(match string) string {
			parts := markdownLink.FindStringSubmatch(match)
			target := referenceURL(base, strings.Trim(parts[2], "<>"))
			if target == "" {
				return strings.TrimPrefix(parts[1], "!")
			}
			return parts[1] + "(<" + target + ">" + parts[3] + ")"
		})
	}
	for i, line := range lines {
		trim := strings.TrimSpace(line)
		run := ""
		if strings.HasPrefix(trim, "```") || strings.HasPrefix(trim, "~~~") {
			j := 0
			for j < len(trim) && trim[j] == trim[0] {
				j++
			}
			run = trim[:j]
		}
		if fence != "" {
			if len(run) >= len(fence) && run[0] == fence[0] && strings.TrimSpace(trim[len(run):]) == "" {
				fence = ""
			}
			continue
		}
		if run != "" {
			fence = run
			continue
		}
		var out strings.Builder
		for rest := line; rest != ""; {
			start := strings.IndexByte(rest, '`')
			if start < 0 {
				out.WriteString(rewrite(rest))
				break
			}
			out.WriteString(rewrite(rest[:start]))
			end := start
			for end < len(rest) && rest[end] == '`' {
				end++
			}
			delimiter := rest[start:end]
			closeAt := strings.Index(rest[end:], delimiter)
			if closeAt < 0 {
				out.WriteString(rest[start:])
				break
			}
			closeAt += end + len(delimiter)
			out.WriteString(rest[start:closeAt])
			rest = rest[closeAt:]
		}
		lines[i] = out.String()
	}
	return strings.Join(lines, "\n")
}

// Fence construction must be linear even for untrusted code containing a long
// run of backticks.
func codeFence(code string, minimum int) string {
	longest, current := 0, 0
	for _, ch := range code {
		if ch == '`' {
			current++
			if current > longest {
				longest = current
			}
		} else {
			current = 0
		}
	}
	if longest >= minimum {
		minimum = longest + 1
	}
	return strings.Repeat("`", minimum)
}
