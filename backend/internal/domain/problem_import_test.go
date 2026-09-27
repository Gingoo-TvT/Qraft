package domain

import (
	"strings"
	"testing"
)

func TestProblemImportRequestContract(t *testing.T) {
	valid := func() ProblemImportRequest {
		return ProblemImportRequest{Mode: "preserve_statement", Items: []SourceProblem{{ItemID: "one", Title: "Sample", Statement: "A **B**\n```cpp\na < b;\n```"}}}
	}
	req := valid()
	if e := req.Normalize(); e != nil {
		t.Fatal(e)
	}
	if req.Difficulty != 1500 || req.Language != "cpp" || req.Locale != "zh" {
		t.Fatal("defaults missing")
	}
	cases := []func(*ProblemImportRequest){func(r *ProblemImportRequest) { r.Mode = "rewrite" }, func(r *ProblemImportRequest) { r.Items = append(r.Items, r.Items[0]) }, func(r *ProblemImportRequest) { r.Items[0].Statement = strings.Repeat("x", 128*1024+1) }, func(r *ProblemImportRequest) { r.Items[0].SourceURL = "file:///secret" }, func(r *ProblemImportRequest) { r.Items[0].SourceURL = "https://user:password@example.test/p" }, func(r *ProblemImportRequest) { r.Language = "shell" }, func(r *ProblemImportRequest) { r.Difficulty = 1550 }}
	for i, change := range cases {
		r := valid()
		change(&r)
		if r.Normalize() == nil {
			t.Fatalf("case %d accepted", i)
		}
	}
	if (SourceProblem{Statement: "x\n"}).Hash() == (SourceProblem{Statement: "x"}).Hash() {
		t.Fatal("statement bytes normalized")
	}
}

func TestProblemImportTitleRespectsDatabaseUnicodeLimit(t *testing.T) {
	source := SourceProblem{ItemID: "one", Title: strings.Repeat("题", 200), Statement: "source"}
	if e := source.Validate(); e != nil {
		t.Fatal(e)
	}
	source.Title += "题"
	if source.Validate() == nil {
		t.Fatal("overlong Unicode title accepted")
	}
}

func TestProblemImportCollectionTitleUnicodeLimit(t *testing.T) {
	req := ProblemImportRequest{Mode: "preserve_statement", Title: strings.Repeat("集", 255), Items: []SourceProblem{{ItemID: "one", Title: "T", Statement: "source"}}}
	if e := req.Normalize(); e != nil {
		t.Fatal(e)
	}
	req.Title += "集"
	if req.Normalize() == nil {
		t.Fatal("overlong collection title accepted")
	}
}
