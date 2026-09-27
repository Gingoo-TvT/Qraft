package sources

import (
	"encoding/json"
	"strings"
	"testing"
)

func luoguFixture(t *testing.T, pid string) string {
	t.Helper()
	state := map[string]any{"template": "problem.show", "status": 200, "data": map[string]any{"problem": map[string]any{
		"pid": pid, "name": "Synthetic window", "content": map[string]any{
			"name": "Synthetic window", "background": "Background", "description": `Count $a-20<t_p \le a$ and preserve **original** text. ![figure](/image.png)`,
			"formatI": "Read values.", "formatO": "Print count.", "hint": `$1 \le n \le 40$`,
		}, "samples": [][]string{{"1\n3 4\n", "2\n"}, {"```literal\n", "a<b & c"}},
	}}}
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	return `<title>Synthetic</title><script id="lentille-context" type="application/json">` + string(raw) + `</script><main><h1>Broken SEO excerpt</h1>Count $a-20<t_p OMITTED</main>`
}

func TestLuoguUsesCompleteOriginalMarkdownAndSamples(t *testing.T) {
	url := "https://www.luogu.com.cn/problem/P900001"
	doc, err := parseDocument(url, url, "text/html", []byte(luoguFixture(t, "P900001")))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Title != "P900001 Synthetic window" || len(doc.Items) != 1 || len(doc.Warnings) != 0 {
		t.Fatalf("unexpected document: %+v", doc)
	}
	body := doc.Items[0].Statement
	for _, want := range []string{`$a-20<t_p \le a$`, "**original**", "输入样例 1", "1\n3 4\n", "输出样例 2", "a<b & c", "````\n```literal", "https://www.luogu.com.cn/image.png", "说明/提示"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, "Broken SEO") {
		t.Fatal("used corrupted HTML fallback")
	}
}

func TestLuoguRejectsMissingOrMismatchedStructuredContent(t *testing.T) {
	url := "https://www.luogu.com.cn/problem/P900001"
	good := luoguFixture(t, "P900001")
	for _, body := range []string{
		`<title>Task</title><main>Partial $a<t_b</main>`, luoguFixture(t, "P900002"),
		strings.Replace(good, `"samples":`, `"missing_samples":`, 1),
		strings.Replace(good, `"description":`, `"missing_description":`, 1),
		strings.Replace(good, `"status":200`, `"status":403`, 1),
	} {
		_, err := parseDocument(url, url, "text/html", []byte(body))
		assertSourceError(t, err, "SOURCE_INVALID_CONTENT")
	}
	// The adapter is scoped to the actual host and problem route.
	doc, err := parseDocument("https://other.example/article", "https://other.example/article", "text/html", []byte(good))
	if err != nil || len(doc.Warnings) == 0 {
		t.Fatalf("generic article changed: %+v %v", doc, err)
	}
}
