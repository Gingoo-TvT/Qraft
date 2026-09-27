package sources

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"path"
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

var (
	atCoderContestRoot = regexp.MustCompile(`^/contests/[A-Za-z0-9_-]+/?$`)
	atCoderTask        = regexp.MustCompile(`^/contests/[A-Za-z0-9_-]+/tasks/[A-Za-z0-9_-]+/?$`)
	cfTask             = regexp.MustCompile(`^/(?:contest|gym)/[0-9]+/problem/[A-Za-z0-9]+/?$|^/problemset/problem/[0-9]+/[A-Za-z0-9]+/?$`)
	cfCollection       = regexp.MustCompile(`^/(?:contest|gym)/[0-9]+/?$|^/problemset/?$`)
	hydroTask          = regexp.MustCompile(`^/(?:d/[^/]+/)?p/[^/]+/?$`)
	hydroCollection    = regexp.MustCompile(`^/(?:d/[^/]+/)?(?:p/?|contest/[^/]+(?:/problems)?/?)$`)
)

func isAtCoder(host string) bool {
	return strings.EqualFold(host, "atcoder.jp") || strings.EqualFold(host, "www.atcoder.jp")
}
func isCodeforces(host string) bool {
	host = strings.ToLower(host)
	return host == "codeforces.com" || strings.HasSuffix(host, ".codeforces.com")
}

func parseDocument(original, finalURL, mediaType string, body []byte) (*Document, error) {
	base, err := validateURL(finalURL)
	if err != nil {
		return nil, err
	}
	doc := &Document{URL: original, FinalURL: finalURL, Kind: KindProblem, Items: []Item{}}
	if mediaType == "text/plain" || mediaType == "text/markdown" || mediaType == "text/x-markdown" {
		statement := normalizeMarkdownReferences(strings.TrimSpace(string(body)), base)
		if statement == "" {
			return nil, sourceError("SOURCE_EMPTY", "来源页面没有正文")
		}
		title := strings.TrimSpace(strings.TrimLeft(strings.SplitN(statement, "\n", 2)[0], "# "))
		if len([]rune(title)) > 150 {
			title = path.Base(base.Path)
		}
		if title == "" || title == "." || title == "/" {
			title = base.Hostname()
		}
		doc.Title = title
		doc.Items = append(doc.Items, statementItem(finalURL, title, statement, ""))
		return doc, nil
	}
	root, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return nil, sourceError("SOURCE_INVALID_CONTENT", "无法解析来源网页")
	}
	pageTitle := compactText(rawText(findNode(root, func(n *html.Node) bool { return element(n, "title") }), 0))
	if blockedPage(root, pageTitle) {
		return nil, sourceError("SOURCE_ACCESS_DENIED", "来源页面是登录或反爬验证页")
	}
	if isLuoguTask(base) {
		item, err := parseLuoguProblem(root, base)
		if err != nil {
			return nil, err
		}
		doc.Title, doc.Items = item.Title, []Item{item}
		return doc, nil
	}
	if isNowcoderTask(base) {
		item, err := parseNowcoderProblem(root, base, pageTitle)
		if err != nil {
			return nil, err
		}
		doc.Title, doc.Items = item.Title, []Item{item}
		return doc, nil
	}
	specific := findNode(root, func(n *html.Node) bool {
		return hasClass(n, "problem-statement") || attr(n, "id") == "task-statement" ||
			attr(n, "id") == "problem-statement" || hasClass(n, "problem__content") ||
			hasClass(n, "problem-content") || hasClass(n, "problem__statement")
	})
	knownSingle := (isCodeforces(base.Hostname()) && cfTask.MatchString(base.Path)) ||
		(isAtCoder(base.Hostname()) && atCoderTask.MatchString(base.Path)) || hydroTask.MatchString(base.Path)
	if !knownSingle && specific == nil && collectionURL(base) {
		items := collectMembers(root, base)
		if len(items) == 0 {
			return nil, sourceError("SOURCE_NO_MEMBERS", "未识别出公开题集成员，页面可能需要登录或动态加载")
		}
		doc.Kind = KindCollection
		doc.Title = pageTitle
		doc.Items = items
		if len(items) == maxMembers {
			doc.Warnings = append(doc.Warnings, "目录最多预览 100 题；单次导入请最多选择 50 题。")
		}
		return doc, nil
	}
	content := specific
	if isAtCoder(base.Hostname()) && specific != nil {
		if english := findNode(specific, func(n *html.Node) bool { return hasClass(n, "lang-en") }); english != nil {
			content = english
		} else if japanese := findNode(specific, func(n *html.Node) bool { return hasClass(n, "lang-ja") }); japanese != nil {
			content = japanese
		}
	}
	if content == nil {
		// Known OJ problem routes must contain their expected statement region;
		// a login shell or JS-only page is not a usable problem statement.
		if knownSingle && (isCodeforces(base.Hostname()) || isAtCoder(base.Hostname())) {
			return nil, sourceError("SOURCE_EMPTY", "未找到题面区域，来源页面可能限制访问")
		}
		content = findNode(root, func(n *html.Node) bool { return element(n, "article") })
		if content == nil {
			content = findNode(root, func(n *html.Node) bool { return element(n, "main") || attr(n, "role") == "main" })
		}
		if content == nil {
			content = findNode(root, func(n *html.Node) bool { return element(n, "body") })
		}
		doc.Warnings = append(doc.Warnings, "这是通用网页正文提取，请确认题目范围、公式和样例后使用；普通文章适合作为创意来源。")
	}
	editorialNode := findNode(root, func(n *html.Node) bool {
		return attr(n, "id") == "editorial" || attr(n, "id") == "solution" || hasClass(n, "problem__solution") || hasClass(n, "editorial")
	})
	renderer := markdownRenderer{base: base, exclude: editorialNode}
	statement := cleanMarkdown(renderer.render(content, 0))
	if statement == "" {
		return nil, sourceError("SOURCE_EMPTY", "来源页面没有可提取的正文")
	}
	titleNode := findNode(content, func(n *html.Node) bool { return element(n, "h1") || hasClass(n, "title") })
	title := compactText(rawText(titleNode, 0))
	if title == "" {
		title = pageTitle
	}
	if title == "" {
		title = base.Hostname()
	}
	editorial := ""
	if editorialNode != nil {
		editorial = cleanMarkdown((&markdownRenderer{base: base}).render(editorialNode, 0))
	}
	doc.Title = title
	doc.Items = append(doc.Items, statementItem(finalURL, title, statement, editorial))
	return doc, nil
}

func statementItem(sourceURL, title, statement, editorial string) Item {
	digest := sha256.Sum256([]byte(statement))
	id := sha256.Sum256([]byte(sourceURL))
	return Item{ID: hex.EncodeToString(id[:8]), URL: sourceURL, Title: title, Statement: statement, Editorial: editorial, StatementSHA256: hex.EncodeToString(digest[:])}
}

func collectionURL(u *url.URL) bool {
	switch {
	case isCodeforces(u.Hostname()):
		return cfCollection.MatchString(u.Path)
	case isAtCoder(u.Hostname()):
		return atCoderContestRoot.MatchString(u.Path) || (strings.HasPrefix(u.Path, "/contests/") && strings.HasSuffix(strings.TrimRight(u.Path, "/"), "/tasks"))
	default:
		return hydroCollection.MatchString(u.Path)
	}
}

func collectMembers(root *html.Node, base *url.URL) []Item {
	result := []Item{}
	seen := map[string]int{}
	if !isCodeforces(base.Hostname()) && !isAtCoder(base.Hostname()) {
		root = findNode(root, func(n *html.Node) bool {
			return hasClass(n, "problem-list") || hasClass(n, "contest-problems") ||
				attr(n, "id") == "contest-problems" || element(n, "table")
		})
		if root == nil {
			return result
		}
	}
	walk(root, 0, func(n *html.Node) {
		if !element(n, "a") {
			return
		}
		absolute := referenceURL(base, attr(n, "href"))
		if absolute == "" {
			return
		}
		u, err := url.Parse(absolute)
		if err != nil || !strings.EqualFold(u.Hostname(), base.Hostname()) {
			return
		}
		match := false
		switch {
		case isCodeforces(base.Hostname()):
			match = cfTask.MatchString(u.Path)
		case isAtCoder(base.Hostname()):
			match = atCoderTask.MatchString(u.Path)
		default:
			match = hydroTask.MatchString(u.Path)
		}
		if !match {
			return
		}
		// Never classify navigation to unrelated contests as collection members.
		if isAtCoder(base.Hostname()) {
			prefix := strings.Split(strings.Trim(base.Path, "/"), "/")
			if len(prefix) >= 2 && !strings.HasPrefix(u.Path, "/contests/"+prefix[1]+"/tasks/") {
				return
			}
		}
		if isCodeforces(base.Hostname()) && !strings.HasPrefix(base.Path, "/problemset") {
			if !strings.HasPrefix(u.Path, strings.TrimRight(base.Path, "/")+"/problem/") {
				return
			}
		}
		u.RawQuery = ""
		u.Fragment = ""
		absolute = u.String()
		title := compactText(rawText(n, 0))
		if title == "" {
			title = path.Base(u.Path)
		}
		if index, exists := seen[absolute]; exists {
			if len(title) > len(result[index].Title) {
				result[index].Title = title
			}
			return
		}
		if len(result) >= maxMembers {
			return
		}
		seen[absolute] = len(result)
		item := statementItem(absolute, title, "", "")
		item.StatementSHA256 = ""
		result = append(result, item)
	})
	return result
}

func blockedPage(root *html.Node, title string) bool {
	title = strings.ToLower(title)
	for _, hint := range []string{"just a moment", "checking your browser", "access denied", "attention required", "验证码", "安全验证"} {
		if strings.Contains(title, hint) {
			return true
		}
	}
	password := findNode(root, func(n *html.Node) bool { return element(n, "input") && strings.EqualFold(attr(n, "type"), "password") })
	if password != nil {
		for _, hint := range []string{"login", "log in", "sign in", "登录"} {
			if strings.Contains(title, hint) {
				return true
			}
		}
	}
	return findNode(root, func(n *html.Node) bool {
		return attr(n, "id") == "challenge-form" || attr(n, "id") == "cf-challenge-running"
	}) != nil
}

func element(n *html.Node, name string) bool {
	return n != nil && n.Type == html.ElementNode && n.Data == name
}
func attr(n *html.Node, key string) string {
	if n == nil {
		return ""
	}
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}
func hasClass(n *html.Node, name string) bool {
	for _, c := range strings.Fields(attr(n, "class")) {
		if c == name {
			return true
		}
	}
	return false
}
func findNode(root *html.Node, predicate func(*html.Node) bool) *html.Node {
	var found *html.Node
	walk(root, 0, func(n *html.Node) {
		if found == nil && predicate(n) {
			found = n
		}
	})
	return found
}
func walk(n *html.Node, depth int, visit func(*html.Node)) {
	if n == nil || depth > 128 {
		return
	}
	visit(n)
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		walk(child, depth+1, visit)
	}
}
func compactText(text string) string { return strings.Join(strings.Fields(text), " ") }
func rawText(n *html.Node, depth int) string {
	if n == nil || depth > 128 {
		return ""
	}
	if n.Type == html.TextNode {
		return n.Data
	}
	if element(n, "br") {
		return "\n"
	}
	var out strings.Builder
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		out.WriteString(rawText(child, depth+1))
		if element(child, "div") || element(child, "p") {
			out.WriteByte('\n')
		}
	}
	return out.String()
}
