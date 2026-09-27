package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/xuri/excelize/v2"
	"gopkg.in/yaml.v3"
)

func TestTestingNumberingRejectsUnsafeAndOverflowRanges(t *testing.T) {
	for _, prefix := range []string{"../outside", "a/b", "=formula", "中文", strings.Repeat("a", 25)} {
		if _, err := ParseTestingExportNumbering(prefix, "1"); err == nil {
			t.Errorf("accepted %q", prefix)
		}
	}
	for _, start := range []string{"0", "-1", "1.5", "NaN", "1000000", "99999999999999999999999"} {
		if _, err := ParseTestingExportNumbering("Demo", start); err == nil {
			t.Errorf("accepted %q", start)
		}
	}
	n, err := ParseTestingExportNumbering("Demo", "999999")
	if err != nil {
		t.Fatal(err)
	}
	if n.validate(2) == nil {
		t.Fatal("accepted overflowing range")
	}
}

func TestTestingNumberingConsistentAcrossZIPFormats(t *testing.T) {
	for _, format := range []string{"generic", "hydro"} {
		t.Run(format, func(t *testing.T) {
			svc, source, _ := testingExportFixture(t)
			originalCode := source.problem.SerialNumber
			originalMetadata := string(source.problem.MetadataJSON)
			set := &domain.ProblemSet{Code: "synthetic-numbering", Items: []domain.ProblemSetItem{{Problem: source.problem}, {Problem: source.problem}}}
			pkg, err := buildProblemSetTestingPackage(context.Background(), set, format, svc, TestingExportOptions{Numbering: &TestingExportNumbering{Prefix: "Demo", Start: 50}})
			if err != nil {
				t.Fatal(err)
			}
			files := unzipEntries(t, pkg.Content)
			var manifest testingSetManifest
			if err = json.Unmarshal(files["problem-set.json"], &manifest); err != nil {
				t.Fatal(err)
			}
			for i, item := range manifest.Items {
				expected := fmt.Sprintf("DemoP%03d", 50+i)
				if format == "hydro" {
					var meta hydroProblemYAML
					if err = yaml.Unmarshal(files[fmt.Sprintf("%03d/problem.yaml", i+1)], &meta); err != nil {
						t.Fatal(err)
					}
					if item.HydroPID != expected || meta.PID != expected {
						t.Fatalf("wrong Hydro number: %+v %+v", item, meta)
					}
				} else {
					if item.ImportCode != expected || item.DataDirectory != "datas/"+expected {
						t.Fatalf("wrong generic number: %+v", item)
					}
					found := false
					for name := range files {
						if strings.HasPrefix(name, "datas/"+expected+"/") {
							found = true
						}
					}
					if !found {
						t.Fatal("numbered data directory missing")
					}
				}
			}
			if format == "generic" {
				book, err := excelize.OpenReader(bytes.NewReader(files["problems.xlsx"]))
				if err != nil {
					t.Fatal(err)
				}
				defer book.Close()
				rows, err := book.GetRows("题目列表")
				if err != nil {
					t.Fatal(err)
				}
				if rows[1][0] != "DemoP050" || rows[2][0] != "DemoP051" {
					t.Fatalf("workbook numbering: %v", rows)
				}
				cases, err := book.GetRows("测试点配置")
				if err != nil {
					t.Fatal(err)
				}
				for _, row := range cases[1:] {
					if row[0] != "DemoP050" && row[0] != "DemoP051" {
						t.Fatalf("stale test case identifier %q", row[0])
					}
				}
			}
			if source.problem.SerialNumber != originalCode || string(source.problem.MetadataJSON) != originalMetadata {
				t.Fatal("download mutated source problem")
			}
		})
	}
}
