package sources

import (
	"fmt"
	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"golang.org/x/net/html"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

func stampDifficulties(doc *Document) {
	for i := range doc.Items {
		if d := doc.Items[i].Difficulty; d != nil {
			d.FetchedAt = doc.FetchedAt
		}
	}
}

var cfDifficulty = regexp.MustCompile(`^\*([0-9]{3,4})$`)

func pageDifficulty(root *html.Node, base *url.URL) *domain.SourceDifficulty {
	if !isCodeforces(base.Hostname()) || !cfTask.MatchString(base.Path) {
		return nil
	}
	sidebar := findNode(root, func(n *html.Node) bool { return attr(n, "id") == "sidebar" })
	var result *domain.SourceDifficulty
	walk(sidebar, 0, func(n *html.Node) {
		if !hasClass(n, "tag-box") || !strings.EqualFold(strings.TrimSpace(attr(n, "title")), "Difficulty") {
			return
		}
		m := cfDifficulty.FindStringSubmatch(compactText(rawText(n, 0)))
		if m == nil {
			return
		}
		value, _ := strconv.Atoi(m[1])
		if value < 800 || value > 4000 || value%100 != 0 {
			return
		}
		result = &domain.SourceDifficulty{Platform: "codeforces", Scale: "codeforces_rating", Value: m[1], Label: "Codeforces " + m[1], SourceURL: base.String()}
	})
	return result
}

func nativeLevel(platform, scale, value, label, sourceURL string) *domain.SourceDifficulty {
	value = strings.TrimSpace(value)
	if value == "" || value == "0" || value == "null" || len(value) > 40 {
		return nil
	}
	if label == "" {
		label = fmt.Sprintf("原站等级 %s", value)
	}
	return &domain.SourceDifficulty{Platform: platform, Scale: scale, Value: value, Label: label, SourceURL: sourceURL}
}
