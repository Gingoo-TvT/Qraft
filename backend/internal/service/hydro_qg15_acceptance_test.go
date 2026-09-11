package service

import (
	"archive/zip"
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/google/uuid"
)

func TestQG15AHydroSixRequirementCases(t *testing.T) {
	base := map[string]string{
		"problem.yaml":  "pid: QG15A\ntitle: QG15A fixture\n",
		"problem_zh.md": "# QG15A fixture\n",
	}
	tests := []struct {
		name            string
		files           map[string]string
		wantValid       bool
		wantMode        string
		wantCases       int
		wantUnsupported []string
	}{
		{
			name: "auto detect in out ans and txt",
			files: map[string]string{
				"testdata/1.in":        "1\n",
				"testdata/1.out":       "1\n",
				"testdata/2.in":        "2\n",
				"testdata/2.ans":       "2\n",
				"testdata/input3.txt":  "3\n",
				"testdata/output3.txt": "3\n",
			},
			wantValid: true,
			wantMode:  "auto_detect",
			wantCases: 3,
		},
		{
			name: "explicit cases with top and case limits",
			files: map[string]string{
				"testdata/config.yaml": "type: default\nchecker_type: default\ntime: 2s\nmemory: 256m\ncases:\n  - input: 1.in\n    output: 1.out\n    score: 100\n    time: 1s\n    memory: 64m\n",
				"testdata/1.in":        "1\n",
				"testdata/1.out":       "1\n",
			},
			wantValid: true,
			wantMode:  "cases",
			wantCases: 1,
		},
		{
			name: "sum subtask with three level limits",
			files: map[string]string{
				"testdata/config.yaml": "time: 3s\nmemory: 512m\nsubtasks:\n  - id: 1\n    score: 100\n    type: sum\n    time: 2s\n    memory: 256m\n    cases:\n      - input: 1.in\n        output: 1.out\n        score: 100\n        time: 1s\n        memory: 64m\n",
				"testdata/1.in":        "1\n",
				"testdata/1.out":       "1\n",
			},
			wantValid: true,
			wantMode:  "subtasks",
			wantCases: 1,
		},
		{
			name: "min subtask with omitted default type and checker",
			files: map[string]string{
				"testdata/config.yaml": "subtasks:\n  - id: 1\n    score: 100\n    type: min\n    cases:\n      - input: 1.in\n        output: 1.out\n",
				"testdata/1.in":        "1\n",
				"testdata/1.out":       "1\n",
			},
			wantValid: true,
			wantMode:  "subtasks",
			wantCases: 1,
		},
		{
			name: "file io filename",
			files: map[string]string{
				"testdata/config.yaml": "filename: answer\ncases:\n  - input: 1.in\n    output: 1.out\n",
				"testdata/1.in":        "1\n",
				"testdata/1.out":       "1\n",
			},
			wantValid: true,
			wantMode:  "cases",
			wantCases: 1,
		},
		{
			name: "unsupported phase two fields fail closed",
			files: map[string]string{
				"testdata/config.yaml": "type: interactive\nchecker_type: testlib\ninteractor: interactor.cc\ncases:\n  - input: 1.in\n    output: 1.out\n",
				"testdata/1.in":        "1\n",
				"testdata/1.out":       "1\n",
			},
			wantValid:       false,
			wantMode:        "cases",
			wantCases:       1,
			wantUnsupported: []string{"type", "checker_type", "interactor"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files := make(map[string]string, len(base)+len(tt.files))
			for name, content := range base {
				files[name] = content
			}
			for name, content := range tt.files {
				files[name] = content
			}
			data := buildHydroZip(t, files)
			report, err := NewHydroValidationService().ValidatePackage(context.Background(), bytes.NewReader(data))
			if err != nil {
				t.Fatalf("ValidatePackage returned error: %v", err)
			}
			if report.Valid != tt.wantValid || report.ProblemCount != 1 {
				t.Fatalf("report = %+v, want valid=%v and one problem", report, tt.wantValid)
			}
			problem := report.Problems[0]
			if problem.ConfigMode != tt.wantMode || problem.CaseCount != tt.wantCases {
				t.Fatalf("problem = %+v, want mode=%q cases=%d", problem, tt.wantMode, tt.wantCases)
			}
			for _, field := range tt.wantUnsupported {
				if !hydroIssuesContainField(problem.Unsupported, field) {
					t.Fatalf("unsupported fields = %+v, want %q", problem.Unsupported, field)
				}
			}
		})
	}
}

func TestQG15AHydroProductionBuilderBatchZipRoundTrip(t *testing.T) {
	first := buildQG15AHydroBatch(t)
	second := buildQG15AHydroBatch(t)
	if !bytes.Equal(first, second) {
		t.Fatal("fixed Hydro batch fixture must be byte deterministic")
	}

	report, err := NewHydroValidationService().ValidatePackage(context.Background(), bytes.NewReader(first))
	if err != nil {
		t.Fatalf("ValidatePackage returned error: %v", err)
	}
	if !report.Valid || report.Mode != "batch" || report.ProblemCount != 2 || report.SuccessCount != 2 {
		t.Fatalf("batch round trip report = %+v", report)
	}
}

func buildQG15AHydroBatch(t *testing.T) []byte {
	t.Helper()
	generatedAt := time.Date(2026, 8, 26, 0, 0, 0, 0, time.UTC)
	requests := []HydroPackageRequest{
		{
			Problem: &domain.Problem{
				ID:           uuid.MustParse("15150000-0000-0000-0000-000000000001"),
				SerialNumber: "QG15A-1",
				Title:        "Batch one",
				Statement:    "Echo one.",
				TimeLimit:    1000,
				MemoryLimit:  128,
			},
			TestCases: []*domain.TestCase{{
				ID:         uuid.MustParse("15150000-0000-0000-0000-000000000011"),
				TestIndex:  0,
				InputPath:  "first.in",
				OutputPath: "first.out",
				Score:      100,
			}},
			ObjectReader: fakeHydroReader{"first.in": []byte("1\n"), "first.out": []byte("1\n")},
			GeneratedAt:  generatedAt,
			Prefix:       "QG15A1/",
		},
		{
			Problem: &domain.Problem{
				ID:           uuid.MustParse("15150000-0000-0000-0000-000000000002"),
				SerialNumber: "QG15A-2",
				Title:        "Batch two",
				Statement:    "Echo two.",
				TimeLimit:    2000,
				MemoryLimit:  256,
			},
			TestCases: []*domain.TestCase{{
				ID:         uuid.MustParse("15150000-0000-0000-0000-000000000022"),
				TestIndex:  0,
				InputPath:  "second.in",
				OutputPath: "second.out",
				Score:      100,
			}},
			ObjectReader: fakeHydroReader{"second.in": []byte("2\n"), "second.out": []byte("2\n")},
			GeneratedAt:  generatedAt,
			Prefix:       "QG15A2/",
		},
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, request := range requests {
		if err := writeHydroProblemEntries(context.Background(), zw, request); err != nil {
			t.Fatalf("writeHydroProblemEntries: %v", err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close Hydro batch zip: %v", err)
	}
	return buf.Bytes()
}
