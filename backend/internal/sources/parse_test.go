package sources

import (
	"strings"
	"testing"
)

func TestParseStatementPreservesMathCodeTableAndImageSource(t *testing.T) {
	input := `<html><head><title>A. Fixture - Codeforces</title></head><body><nav>NOT CONTENT</nav>
<div class="problem-statement"><div class="header"><div class="title">A. Fixture</div></div>
<p>Find $$$x+y$$$.</p><script type="math/tex; mode=display">a^2+b^2=c^2</script>
<span class="katex"><span class="katex-mathml"><math><semantics><annotation encoding="application/x-tex">\sum_{i=1}^n i</annotation></semantics></math></span><span class="katex-html">DUPLICATE FORMULA</span></span>
<pre>for (int i=0; i&lt;n; i++) {
  cout &lt;&lt; i;
}</pre>
<table><tr><th>n</th><th>result</th></tr><tr><td>2</td><td>3</td></tr></table>
<img src="/images/fixture.png" alt="figure"><a href="../problem/B">related</a>
</div><section id="editorial">Optional editorial context</section><script>alert('NO')</script></body></html>`
	doc, err := parseDocument("https://codeforces.com/contest/999/problem/A", "https://codeforces.com/contest/999/problem/A", "text/html", []byte(input))
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Items) != 1 {
		t.Fatalf("items=%+v", doc.Items)
	}
	item := doc.Items[0]
	for _, want := range []string{"$x+y$", "$$a^2+b^2=c^2$$", `$\sum_{i=1}^n i$`, "for (int i=0; i<n; i++) {\n  cout << i;\n}", "| n | result |", "https://codeforces.com/images/fixture.png", "https://codeforces.com/contest/999/problem/B"} {
		if !strings.Contains(item.Statement, want) {
			t.Errorf("missing %q in:\n%s", want, item.Statement)
		}
	}
	for _, unwanted := range []string{"NOT CONTENT", "DUPLICATE FORMULA", "alert(", "Optional editorial"} {
		if strings.Contains(item.Statement, unwanted) {
			t.Errorf("unexpected %q in statement", unwanted)
		}
	}
	if !strings.Contains(item.Editorial, "Optional editorial") || len(item.StatementSHA256) != 64 {
		t.Fatalf("item=%+v", item)
	}
}

func TestParseAtCoderUsesOneLanguage(t *testing.T) {
	input := `<title>A - Fixture</title><div id="task-statement"><span class="lang-ja"><p>日本語文</p></span><span class="lang-en"><section><h3>Problem Statement</h3><p>English body \(x &lt; y\).</p></section></span></div>`
	doc, err := parseDocument("https://atcoder.jp/contests/test/tasks/test_a", "https://atcoder.jp/contests/test/tasks/test_a", "text/html", []byte(input))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(doc.Items[0].Statement, "日本語") || !strings.Contains(doc.Items[0].Statement, `\(x < y\)`) {
		t.Fatalf("statement=%s", doc.Items[0].Statement)
	}
}

func TestParseCollectionsUseMembersNotSiteNavigation(t *testing.T) {
	tests := []struct {
		url, html string
		want      int
	}{
		{"https://codeforces.com/contest/999", `<title>Contest</title><a href="/contest/888/problem/Z">unrelated</a><table><tr><td><a href="/contest/999/problem/A">A</a></td><td><a href="/contest/999/problem/A">A full title</a></td><td><a href="/contest/999/problem/B">B title</a></td></tr></table>`, 2},
		{"https://atcoder.jp/contests/abc999/tasks", `<title>Tasks</title><a href="/contests/abc888/tasks/abc888_a">unrelated</a><table><tr><td><a href="/contests/abc999/tasks/abc999_a">A</a></td></tr></table>`, 1},
		{"https://hydro.example/d/demo/contest/123", `<title>Contest</title><nav><a href="/d/demo/p/P999">not a member</a></nav><table class="problem-list"><tr><td><a href="/d/demo/p/P100">Title</a></td></tr></table>`, 1},
	}
	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			doc, err := parseDocument(tt.url, tt.url, "text/html", []byte(tt.html))
			if err != nil {
				t.Fatal(err)
			}
			if doc.Kind != KindCollection || len(doc.Items) != tt.want {
				t.Fatalf("doc=%+v", doc)
			}
			for _, item := range doc.Items {
				if !strings.HasPrefix(item.URL, "https://") || item.Statement != "" || item.StatementSHA256 != "" || item.ID == "" {
					t.Fatalf("member=%+v", item)
				}
				if strings.Contains(item.Title, "unrelated") || strings.Contains(item.Title, "not a member") {
					t.Fatal("navigation became member")
				}
			}
		})
	}
}

func TestGenericArticleIsNotACollectionAndMarkdownResolvesLinks(t *testing.T) {
	doc, err := parseDocument("https://article.example/blog", "https://article.example/blog", "text/html", []byte(`<title>Article</title><main><h1>Idea</h1><p>Use this observation.</p><a href="https://codeforces.com/contest/1/problem/A">one</a><a href="https://codeforces.com/contest/1/problem/B">two</a><img src="javascript:alert(1)"></main>`))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Kind != KindProblem || len(doc.Items) != 1 || len(doc.Warnings) == 0 || strings.Contains(doc.Items[0].Statement, "javascript:") {
		t.Fatalf("doc=%+v", doc)
	}
	markdown := "# Original\n\n![diagram](../img.png)\n\n```\n[x](../literal)\n```"
	doc, err = parseDocument("https://source.example/tasks/a.md", "https://source.example/tasks/a.md", "text/markdown", []byte(markdown))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(doc.Items[0].Statement, "https://source.example/img.png") || !strings.Contains(doc.Items[0].Statement, "[x](../literal)") {
		t.Fatalf("markdown=%s", doc.Items[0].Statement)
	}
}

func TestNoReliableCollectionMembersFailsClearly(t *testing.T) {
	_, err := parseDocument("https://hydro.example/contest/123", "https://hydro.example/contest/123", "text/html", []byte(`<title>Contest</title><nav><a href="/p/P1">site navigation only</a></nav>`))
	assertSourceError(t, err, "SOURCE_NO_MEMBERS")
}

func TestMarkdownCodeIsNotRewrittenAndRaggedTableIsBounded(t *testing.T) {
	text := "# Fixture\n\n`[x](../literal)`\n\n````\n```\n[x](../code)\n````\n\n[x](../target)"
	doc, err := parseDocument("https://source.example/a/b.md", "https://source.example/a/b.md", "text/markdown", []byte(text))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"`[x](../literal)`", "[x](../code)", "https://source.example/target"} {
		if !strings.Contains(doc.Items[0].Statement, want) {
			t.Fatalf("missing %q: %s", want, doc.Items[0].Statement)
		}
	}
	table := "<main><table><tr>" + strings.Repeat("<td>x</td>", 500) + "</tr>" + strings.Repeat("<tr><td>y</td></tr>", 500) + "</table><pre>" + strings.Repeat("`", 2000) + "</pre></main>"
	doc, err = parseDocument("https://source.example/article", "https://source.example/article", "text/html", []byte(table))
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Items[0].Statement) > len(table)*4 {
		t.Fatalf("unbounded table amplification: input=%d output=%d", len(table), len(doc.Items[0].Statement))
	}
}

func TestParseAtCoderPreservesVarFormulaeAndSamples(t *testing.T) {
	input := "<title>A - Synthetic formula</title><div id=\"task-statement\"><span class=\"lang-en\">" +
		"<section><h3>Problem Statement</h3><p>Given <var>H</var> and <var>A</var>, evaluate " +
		"<var>\\left\\lceil \\frac{H}{A} \\right\\rceil</var>.</p></section>" +
		"<section><h3>Constraints</h3><p><var>1 \\leq H &lt; 10^9</var><var>   </var></p></section>" +
		"<h3>Sample Input 1</h3><pre>7 3\n</pre><h3>Sample Output 1</h3><pre>3\n</pre>" +
		"<pre>&lt;var&gt;literal code&lt;/var&gt;</pre></span></div>"
	source := "https://atcoder.jp/contests/synthetic/tasks/synthetic_a"
	doc, err := parseDocument(source, source, "text/html", []byte(input))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Kind != KindProblem || len(doc.Items) != 1 {
		t.Fatalf("kind=%q count=%d", doc.Kind, len(doc.Items))
	}
	body := doc.Items[0].Statement
	for _, want := range []string{
		"$H$", "$A$", "$\\left\\lceil \\frac{H}{A} \\right\\rceil$",
		"$1 \\leq H < 10^9$", "Sample Input 1", "Sample Output 1", "7 3", "<var>literal code</var>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(body, "$$") {
		t.Error("empty var must not create an empty display formula")
	}
}
