package service

import (
	"archive/zip"
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/google/uuid"
)

func TestHydroValidationAcceptsExportedPhaseOnePackage(t *testing.T) {
	problemID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	pkg, err := BuildHydroProblemPackage(context.Background(), HydroPackageRequest{
		Problem: &domain.Problem{
			ID:           problemID,
			SerialNumber: "AF-0007",
			Title:        "A+B",
			Statement:    "Read two integers and print their sum.",
			Tags:         []string{"math"},
			TimeLimit:    1000,
			MemoryLimit:  256,
		},
		TestCases: []*domain.TestCase{
			{
				ID:         uuid.MustParse("22222222-2222-2222-2222-222222222222"),
				ProblemID:  problemID,
				TestIndex:  0,
				InputPath:  "in/1",
				OutputPath: "out/1",
				Score:      100,
			},
		},
		ObjectReader: fakeHydroReader{
			"in/1":  []byte("1 2\n"),
			"out/1": []byte("3\n"),
		},
		GeneratedAt: time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("BuildHydroProblemPackage returned error: %v", err)
	}

	report, err := NewHydroValidationService().ValidatePackage(context.Background(), bytes.NewReader(pkg.Content))
	if err != nil {
		t.Fatalf("ValidatePackage returned error: %v", err)
	}
	if !report.Valid {
		t.Fatalf("report should be valid: %+v", report)
	}
	if report.Mode != "single" || report.ProblemCount != 1 || report.SuccessCount != 1 {
		t.Fatalf("report summary = %+v", report)
	}
	got := report.Problems[0]
	if got.PID != "AF0007" || got.Title != "A+B" || got.ConfigMode != "subtasks" || got.CaseCount != 1 {
		t.Fatalf("validated problem = %+v", got)
	}
}

func TestHydroValidationRejectsUnsupportedPhaseTwoFields(t *testing.T) {
	data := buildHydroZip(t, map[string]string{
		"problem.yaml": `pid: C1000
title: Interactive
`,
		"problem_zh.md": "# Interactive\n",
		"testdata/config.yaml": `type: interactive
checker_type: testlib
interactor: interactor.cc
subtasks:
  - score: 100
    type: max
    if: [1]
    cases:
      - input: 1.in
        output: 1.out
`,
		"testdata/1.in":  "1\n",
		"testdata/1.out": "1\n",
	})

	report, err := NewHydroValidationService().ValidatePackage(context.Background(), bytes.NewReader(data))
	if err != nil {
		t.Fatalf("ValidatePackage returned error: %v", err)
	}
	if report.Valid || report.FailedCount != 1 {
		t.Fatalf("report should be invalid: %+v", report)
	}
	unsupported := report.Problems[0].Unsupported
	if len(unsupported) < 4 {
		t.Fatalf("unsupported issues = %+v", unsupported)
	}
	wantFragments := []string{"type", "checker_type", "interactor", "subtasks[0].type", "subtasks[0].if"}
	for _, fragment := range wantFragments {
		if !hydroIssuesContainField(unsupported, fragment) {
			t.Fatalf("unsupported issues missing %q: %+v", fragment, unsupported)
		}
	}
}

func TestHydroValidationDetectsBatchDuplicatePID(t *testing.T) {
	data := buildHydroZip(t, map[string]string{
		"A/problem.yaml":         "pid: C1000\ntitle: First\n",
		"A/problem_zh.md":        "# First\n",
		"A/testdata/1.in":        "1\n",
		"A/testdata/1.out":       "1\n",
		"B/problem.yaml":         "pid: C1000\ntitle: Second\n",
		"B/problem_zh.md":        "# Second\n",
		"B/testdata/config.yaml": "cases:\n  - input: 1.in\n    output: 1.out\n",
		"B/testdata/1.in":        "2\n",
		"B/testdata/1.out":       "2\n",
	})

	report, err := NewHydroValidationService().ValidatePackage(context.Background(), bytes.NewReader(data))
	if err != nil {
		t.Fatalf("ValidatePackage returned error: %v", err)
	}
	if report.Mode != "batch" || report.ProblemCount != 2 {
		t.Fatalf("batch summary = %+v", report)
	}
	if report.Valid || report.FailedCount != 1 {
		t.Fatalf("duplicate pid should invalidate one problem: %+v", report)
	}
	if !strings.Contains(report.Errors[0].Message, "duplicate pid") {
		t.Fatalf("top errors = %+v", report.Errors)
	}
}

func TestHydroValidationRejectsUnsafeZipPath(t *testing.T) {
	data := buildHydroZip(t, map[string]string{
		"../problem.yaml": "pid: C1000\ntitle: Unsafe\n",
	})
	report, err := NewHydroValidationService().ValidatePackage(context.Background(), bytes.NewReader(data))
	if err != nil {
		t.Fatalf("ValidatePackage returned error: %v", err)
	}
	if report.Valid || len(report.Errors) == 0 {
		t.Fatalf("unsafe path should be reported: %+v", report)
	}
}

func buildHydroZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		fw, err := zw.Create(name)
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		if _, err := fw.Write([]byte(content)); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

func hydroIssuesContainField(issues []HydroValidationIssue, field string) bool {
	for _, issue := range issues {
		if issue.Field == field {
			return true
		}
	}
	return false
}
