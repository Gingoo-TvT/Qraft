package service

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/google/uuid"
	"github.com/xuri/excelize/v2"
)

func publicQuizFixture(t *testing.T) domain.QuizProblem {
	t.Helper()
	q, err := ParseQuizRow(2, validChoiceImportRow())
	if err != nil {
		t.Fatal(err)
	}
	q.ID = uuid.New()
	q.Explanation = "The sum of 1 and 1 is 2."
	return q
}

func TestPublicQuizTemplateIsEmptyAndRoundTripsWorkspaceRows(t *testing.T) {
	empty, err := BuildQuizWorkbook(nil)
	if err != nil {
		t.Fatal(err)
	}
	workbook, err := excelize.OpenReader(bytes.NewReader(empty))
	if err != nil {
		t.Fatal(err)
	}
	rows, err := workbook.GetRows(QuizExcelSheetName)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || len(workbook.GetSheetList()) != 1 || len(rows[0]) != 14 {
		t.Fatalf("template contains unexpected data: %v", rows)
	}
	workbook.Close()
	original := publicQuizFixture(t)
	original.Statement = "=1+1 is shown as text, never executed."
	original.Difficulty = domain.QuizDifficultyMedium
	data, err := BuildQuizWorkbook([]domain.QuizProblem{original})
	if err != nil {
		t.Fatal(err)
	}
	repo := &fakeQuizBulkRepo{}
	report, err := newQuizImportServiceWithRepo(repo).Import(context.Background(), ImportRequest{File: bytes.NewReader(data), Subject: domain.QuizSubjectCLanguage})
	if err != nil {
		t.Fatal(err)
	}
	if report.SuccessCount != 1 || report.FailedCount != 0 || len(repo.quizzes) != 1 {
		t.Fatalf("roundtrip failed: %+v", report)
	}
	got := repo.quizzes[0]
	if got.Statement != original.Statement || got.Explanation != original.Explanation || got.Difficulty != original.Difficulty {
		t.Fatalf("content drifted: %+v", got)
	}
	q := original
	q.Type = domain.QuizTypeProgramming
	if _, err := BuildQuizWorkbook([]domain.QuizProblem{q}); err == nil {
		t.Fatal("programming row exported without its verified assets")
	}
}

func readPublicPackage(t *testing.T, data []byte) map[string][]byte {
	t.Helper()
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	entries := map[string][]byte{}
	for _, f := range z.File {
		if strings.Contains(f.Name, "..") || strings.HasPrefix(f.Name, "/") {
			t.Fatalf("unsafe path: %s", f.Name)
		}
		reader, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		content, err := io.ReadAll(reader)
		reader.Close()
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := entries[f.Name]; ok {
			t.Fatalf("duplicate path %s", f.Name)
		}
		entries[f.Name] = content
	}
	return entries
}

func TestPublicProblemSetPackagePreservesMixedContentAndScores(t *testing.T) {
	fixture := qg15AS3ExportFixture(t)
	hydro := &HydroExportService{problems: &qg15AHydroProblemSource{problem: fixture.request.Problem, testCases: fixture.request.TestCases}, objects: fixture.reader}
	q := publicQuizFixture(t)
	set := &domain.ProblemSet{ID: uuid.New(), Code: "practice", Title: "Mixed practice", Kind: domain.ProblemSetKind("homework"), CreatedBy: "private-operator-marker", GeneratedPrompt: "private-model-prompt-marker", Items: []domain.ProblemSetItem{
		{ID: uuid.New(), Quiz: &q, Position: 1, Score: 20, Section: "Concepts"},
		{ID: uuid.New(), Problem: fixture.request.Problem, Position: 2, Score: 80, Section: "Programming"},
	}}
	pkg, err := buildProblemSetPackage(context.Background(), set, hydro)
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Profile != ProblemSetPackageFormat || pkg.FileName != "practice-qraft.zip" {
		t.Fatalf("package identity: %+v", pkg)
	}
	entries := readPublicPackage(t, pkg.Content)
	if len(entries) != 4 || entries["quizzes.xlsx"] == nil || entries["programming/002-hydro.zip"] == nil || entries["README.txt"] == nil {
		t.Fatalf("unexpected entries: %v", entries)
	}
	var manifest problemSetExportManifest
	if err := json.Unmarshal(entries["problem-set.json"], &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.TotalScore != 100 || len(manifest.Items) != 2 || manifest.Items[0].Score != 20 || manifest.Items[1].Score != 80 || manifest.Items[0].Quiz.Explanation != q.Explanation || manifest.Items[1].HydroPackage != "programming/002-hydro.zip" {
		t.Fatalf("manifest content: %+v", manifest)
	}
	for _, private := range []string{"private-operator-marker", "private-model-prompt-marker", set.ID.String(), "metadata_json", "generation_config"} {
		if bytes.Contains(entries["problem-set.json"], []byte(private)) {
			t.Fatalf("internal state leaked: %s", private)
		}
	}
	// The nested programming archive is built through the real S3/Hydro service,
	// so artifact edits after authoring still block the entire collection export.
	fixture.reader.data[fixture.request.TestCases[0].InputPath] = []byte("changed")
	if _, err := buildProblemSetPackage(context.Background(), set, hydro); err == nil {
		t.Fatal("tampered programming asset was accepted")
	}
}

func TestPublicProblemSetSupportsPureObjectiveAndRejectsMissingSource(t *testing.T) {
	q := publicQuizFixture(t)
	set := &domain.ProblemSet{Code: "quiz", Items: []domain.ProblemSetItem{{Quiz: &q, Score: 10}}}
	if _, err := buildProblemSetPackage(context.Background(), set, nil); err != nil {
		t.Fatal(err)
	}
	set.Items[0].Problem = &domain.Problem{ID: uuid.New()}
	if _, err := buildProblemSetPackage(context.Background(), set, nil); err == nil {
		t.Fatal("ambiguous source accepted")
	}
	set.Items[0].Quiz = nil
	if _, err := buildProblemSetPackage(context.Background(), set, nil); err == nil {
		t.Fatal("missing asset verifier accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := buildProblemSetPackage(ctx, set, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel ignored: %v", err)
	}
}
