package sources

import (
	"strings"
	"testing"
)

func TestSourceDifficultyKeepsNativeScalesAndIgnoresContestPoints(t *testing.T) {
	cf := "https://codeforces.com/problemset/problem/900001/A"
	body := `<div class="problem-statement"><div class="title">Synthetic sum</div>Add two values. <span class="tag-box" title="Difficulty">*3500</span></div><div id="sidebar"><span class="tag-box" title="Difficulty">*800</span></div>`
	doc, err := parseDocument(cf, cf, "text/html", []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if d := doc.Items[0].Difficulty; d == nil || d.Scale != "codeforces_rating" || d.Value != "800" {
		t.Fatalf("incorrect source difficulty: %+v", d)
	}
	at := "https://atcoder.jp/contests/synthetic/tasks/synthetic_a"
	doc, err = parseDocument(at, at, "text/html", []byte(`<div id="task-statement">Score: 500 points. Add values.</div>`))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Items[0].Difficulty != nil {
		t.Fatal("contest points became a rating")
	}
	luogu := "https://www.luogu.com.cn/problem/P900001"
	body = strings.Replace(luoguFixture(t, "P900001"), `"pid":`, `"difficulty":3,"pid":`, 1)
	doc, err = parseDocument(luogu, luogu, "text/html", []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if d := doc.Items[0].Difficulty; d == nil || d.Scale != "luogu_level" || d.Value != "3" {
		t.Fatalf("native level lost: %+v", d)
	}
	doc, err = parseDocument(luogu, luogu, "text/html", []byte(luoguFixture(t, "P900001")))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Items[0].Difficulty != nil {
		t.Fatal("missing difficulty was fabricated")
	}
}
