package service

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/google/uuid"
	"github.com/xuri/excelize/v2"
)

const syntheticOJCatalog = `{"activeTagsTree":[{"id":"9007199254740993123","name":"算法","status":1,"delFlag":"0","children":[{"id":"9007199254740993124","name":"字符串","status":1,"delFlag":"0","parentId":"9007199254740993123"},{"id":"9007199254740993125","name":"失效标签","status":1,"delFlag":"1","parentId":"9007199254740993123"}]},{"id":"9007199254740993126","name":"语言","status":1,"delFlag":"0","children":[{"id":"9007199254740993127","name":"字符串","status":1,"delFlag":"0","parentId":"9007199254740993126"}]}]}`

func TestOJTagCatalogPreservesIDsAndRefusesAmbiguousNames(t *testing.T) {
	catalog, err := ParseOJTagCatalog([]byte(syntheticOJCatalog))
	if err != nil {
		t.Fatal(err)
	}
	ids, missing := catalog.resolve([]string{"字符串", "算法 / 字符串", "9007199254740993124", "失效标签", "不存在"})
	if !reflect.DeepEqual(ids, []string{"9007199254740993124"}) || !reflect.DeepEqual(missing, []string{"字符串", "失效标签", "不存在"}) {
		t.Fatalf("ids=%v unresolved=%v", ids, missing)
	}
	// An unqualified root name must not beat a same-name descendant.
	rootCollision := strings.Replace(syntheticOJCatalog, `"name":"算法"`, `"name":"字符串"`, 1)
	collisionCatalog, e := ParseOJTagCatalog([]byte(rootCollision))
	if e != nil {
		t.Fatal(e)
	}
	if matches, _ := collisionCatalog.resolve([]string{"字符串"}); len(matches) != 0 {
		t.Fatal("ambiguous root name was selected")
	}
	// storedLevel/path are untrusted hints. Reconstruct paths from the tree.
	catalog, err = ParseOJTagCatalog([]byte(strings.Replace(syntheticOJCatalog, `"name":"算法"`, `"name":"算法","path":"假的路径","level":99`, 1)))
	if err != nil {
		t.Fatal(err)
	}
	ids, _ = catalog.resolve([]string{"算法 / 字符串"})
	if len(ids) != 1 {
		t.Fatal("stored level overrode tree")
	}
	for _, bad := range []string{`{}`, `{"activeTagsTree":[]}`, strings.Replace(syntheticOJCatalog, `"9007199254740993123"`, `9007199254740993123`, 1), strings.Replace(syntheticOJCatalog, `"parentId":"9007199254740993123"`, `"parentId":"2"`, 1), strings.Replace(syntheticOJCatalog, `"id":"9007199254740993126"`, `"id":"9007199254740993123"`, 1)} {
		if _, err := ParseOJTagCatalog([]byte(bad)); err == nil {
			t.Fatalf("accepted bad catalog %.80s", bad)
		}
	}
}

func TestOJSixDifficultyBands(t *testing.T) {
	values := map[int]string{0: "", 799: "", 800: "入门", 999: "入门", 1000: "简单", 1399: "简单", 1400: "进阶", 1799: "进阶", 1800: "困难", 2199: "困难", 2200: "挑战", 2599: "挑战", 2600: "地狱", 3500: "地狱", 3501: ""}
	for rating, want := range values {
		if got := ojProgrammingDifficulty(rating); got != want {
			t.Errorf("%d: %s != %s", rating, got, want)
		}
	}
}

func TestOJTemplateMixedZIPContract(t *testing.T) {
	svc, source, _ := testingExportFixture(t)
	source.problem.Difficulty = 1650
	source.problem.Title = "=SUM(1,2)"
	source.problem.Statement = "# 原题\n\n$1 \\le n$\n\n```cpp\ncout << n;\n```\n"
	// Keep fixture provenance bound to the changed synthetic statement.
	var metadata map[string]interface{}
	json.Unmarshal(source.problem.MetadataJSON, &metadata)
	metadata["import_source"] = map[string]string{"final_sha256": sha256Hex([]byte(source.problem.Statement))}
	source.problem.MetadataJSON, _ = json.Marshal(metadata)
	source.problem.Tags = []string{"算法 / 字符串", "字符串"}
	choice := publicQuizFixture(t)
	choice.Difficulty = domain.QuizDifficultyMedium
	choice.Options = []domain.QuizOption{{Label: "A", Content: "$x<2$"}, {Label: "B", Content: "代码 `x++`"}}
	choice.Answers = []string{"A", "B"}
	choice.Tags = []string{"9007199254740993124"}
	judge := choice
	judge.Type = domain.QuizTypeJudge
	judge.Options = nil
	judge.Answers = []string{"对"}
	fill := choice
	fill.Type = domain.QuizTypeFillBlank
	fill.Options = nil
	fill.Answers = []string{"6{or}六", "60"}
	set := &domain.ProblemSet{ID: uuid.MustParse("00000000-0000-0000-0000-000000000099"), Code: "synthetic", Title: "混合题集测试", Items: []domain.ProblemSetItem{{Problem: source.problem, Score: 60}, {Quiz: &choice, Score: 10}, {Quiz: &judge, Score: 10}, {Quiz: &fill, Score: 20}, {Problem: source.problem, Score: 100}}}
	catalog, err := ParseOJTagCatalog([]byte(syntheticOJCatalog))
	if err != nil {
		t.Fatal(err)
	}
	// Optional external input/output is local QA only; never a repository fixture.
	if path := os.Getenv("QRAFT_OJ_CATALOG_QA"); path != "" {
		data, e := os.ReadFile(path)
		if e != nil {
			t.Fatal(e)
		}
		catalog, e = ParseOJTagCatalog(data)
		if e != nil {
			t.Fatal(e)
		}
		t.Logf("external active catalog: %d", len(catalog.byID))
		source.problem.Tags = []string{"输入输出", "字符串"}
		if matches, _ := catalog.resolve([]string{"字符串"}); len(matches) != 0 {
			t.Fatal("real catalog ambiguity was ignored")
		}
		choice.Tags = []string{"输入输出"}
	}
	pkg, err := buildProblemSetTestingPackage(context.Background(), set, "generic", svc, TestingExportOptions{TagCatalog: catalog})
	if err != nil {
		t.Fatal(err)
	}
	files := readPublicPackage(t, pkg.Content)
	reader, err := zip.NewReader(bytes.NewReader(pkg.Content), int64(len(pkg.Content)))
	if err != nil {
		t.Fatal(err)
	}
	roots := 0
	names := map[string]bool{}
	for _, f := range reader.File {
		if names[f.Name] {
			t.Fatal("duplicate ZIP entry")
		}
		names[f.Name] = true
		if strings.HasSuffix(f.Name, ".xlsx") && !strings.Contains(f.Name, "/") {
			roots++
		}
		if strings.Contains(f.Name, "..") || strings.HasPrefix(f.Name, "/") {
			t.Fatal("unsafe path")
		}
	}
	if roots != 1 {
		t.Fatalf("%d root workbooks", roots)
	}
	book, err := excelize.OpenReader(bytes.NewReader(files["problems.xlsx"]))
	if err != nil {
		t.Fatal(err)
	}
	defer book.Close()
	rows, err := book.GetRows("题目列表")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 6 || !reflect.DeepEqual(rows[0], ojProblemHeaders) {
		t.Fatalf("invalid rows/headers: %v", rows)
	}
	get := func(cell string) string {
		v, e := book.GetCellValue("题目列表", cell)
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	if get("B2") != "=SUM(1,2)" || get("C2") != source.problem.Statement || get("I2") != "进阶" || get("I3") != "进阶" || get("O2") != "2" || get("P2") != "1s" || get("Q2") != "128m" || get("S2") != "full" {
		t.Fatal("programming cells changed")
	}
	if f, _ := book.GetCellFormula("题目列表", "B2"); f != "" {
		t.Fatal("formula injection")
	}
	if os.Getenv("QRAFT_OJ_CATALOG_QA") == "" && get("L2") != "9007199254740993124" {
		t.Fatalf("long tag ID rounded: %s", get("L2"))
	}
	if get("H3") != "A{break}\nB" || get("H4") != "对" || get("H5") != "6{or}六{break}\n60" {
		t.Fatal("objective answer encoding changed")
	}
	seen := map[string]bool{}
	for i, row := range rows[1:] {
		if seen[row[0]] {
			t.Fatal("duplicate problem code")
		}
		seen[row[0]] = true
		if !strings.HasPrefix(row[0], []string{"C", "X", "P", "T", "C"}[i]) {
			t.Fatal("wrong code prefix")
		}
	}
	tests, err := book.GetRows("测试点配置")
	if err != nil {
		t.Fatal(err)
	}
	if len(tests) != 5 || !reflect.DeepEqual(tests[0], ojTestHeaders) {
		t.Fatalf("bad tests: %v", tests)
	}
	for _, r := range tests[1:] {
		for _, col := range []int{5, 6} {
			if files["datas/"+r[0]+"/"+r[col]] == nil {
				t.Fatalf("missing paired data %v", r)
			}
		}
	}
	var manifest testingSetManifest
	json.Unmarshal(files["problem-set.json"], &manifest)
	if manifest.Format != "qraft.problem-set.testing.generic.v2" || manifest.Items[0].Rating != 1650 || manifest.Items[0].ImportCode != get("A2") {
		t.Fatalf("bad manifest %+v", manifest)
	}
	for _, item := range manifest.Items {
		if item.Type == domain.QuizTypeProgramming {
			var j struct {
				TestCases []struct {
					InputFile  string `json:"input_file"`
					OutputFile string `json:"output_file"`
				} `json:"test_cases"`
			}
			if e := json.Unmarshal(files[item.Directory+"/judge.json"], &j); e != nil {
				t.Fatal(e)
			}
			for _, c := range j.TestCases {
				if files[c.InputFile] == nil || files[c.OutputFile] == nil {
					t.Fatal("manifest points to missing data")
				}
			}
		}
	}
	if bytes.Contains(files["export-notes.json"], []byte("失效标签")) {
		t.Fatal("unused catalog leaked")
	}
	if dir := os.Getenv("QRAFT_EXPORT_QA_DIR"); dir != "" {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "synthetic-template.zip"), pkg.Content, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "problems.xlsx"), files["problems.xlsx"], 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestOJTemplateSubtasksAndLongCellGuard(t *testing.T) {
	p := &domain.Problem{Title: "Test", Statement: "Preserve", Difficulty: 2000, TimeLimit: 1500, MemoryLimit: 256}
	rows := []ojTestingRow{{Code: "C1000", Problem: p, Subtasks: []hydroSubtask{{ID: 1, Type: "sum", Score: 40, Cases: []hydroCase{{Input: "1.in", Output: "1.out", Score: 15}, {Input: "2.in", Output: "2.out", Score: 25}}}, {ID: 2, Type: "min", Score: 60, Time: "2s", Cases: []hydroCase{{Input: "3.in", Output: "3.out", Time: "3s"}}}}}}
	data, _, err := buildOJTestingWorkbook(rows, TestingExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if s, _ := b.GetCellValue("测试点配置", "H4"); s != "" {
		t.Fatal("min points must be blank")
	}
	if s, _ := b.GetCellValue("测试点配置", "D4"); s != "60" {
		t.Fatal("min score lost")
	}
	if s, _ := b.GetCellValue("测试点配置", "I4"); s != "2s" {
		t.Fatal("subtask limits lost")
	}
	if s, _ := b.GetCellValue("测试点配置", "K4"); s != "3s" {
		t.Fatal("case limits lost")
	}
	p.Statement = strings.Repeat("文", 32768)
	if _, _, err := buildOJTestingWorkbook(rows, TestingExportOptions{}); err == nil {
		t.Fatal("long statement silently truncated")
	}
	p.Statement = "ok"
	rows[0].Subtasks[0].Score = 39
	if _, _, err := buildOJTestingWorkbook(rows, TestingExportOptions{}); err == nil {
		t.Fatal("sum score mismatch accepted")
	}
}
