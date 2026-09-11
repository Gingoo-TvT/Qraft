package service

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
)

type fakeHydroReader map[string][]byte

func (f fakeHydroReader) DownloadFile(ctx context.Context, objectPath string) ([]byte, error) {
	data, ok := f[objectPath]
	if !ok {
		return nil, io.ErrUnexpectedEOF
	}
	return data, nil
}

func TestBuildHydroProblemPackagePhaseOneFormat(t *testing.T) {
	workflowID := "workflow-123"
	problemID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	problem := &domain.Problem{
		ID:           problemID,
		SerialNumber: "AF-0042",
		Title:        "两数之和",
		Statement:    "读取两个整数并输出它们的和。",
		Tags:         []string{"入门", "模拟", "入门"},
		TimeLimit:    1500,
		MemoryLimit:  128,
		WorkflowID:   &workflowID,
		MetadataJSON: json.RawMessage(`{"hydro":{"pid":"C1000","filename":"sum","detail":"full"}}`),
	}
	testCases := []*domain.TestCase{
		{
			ID:          uuid.MustParse("22222222-2222-2222-2222-222222222222"),
			ProblemID:   problemID,
			TestIndex:   0,
			GroupID:     1,
			IsSample:    true,
			InputPath:   "input/0",
			OutputPath:  "output/0",
			Score:       0,
			Description: "sample",
		},
		{
			ID:         uuid.MustParse("33333333-3333-3333-3333-333333333333"),
			ProblemID:  problemID,
			TestIndex:  1,
			GroupID:    1,
			InputPath:  "input/1",
			OutputPath: "output/1",
			Score:      100,
		},
	}

	pkg, err := BuildHydroProblemPackage(context.Background(), HydroPackageRequest{
		Problem:   problem,
		TestCases: testCases,
		ObjectReader: fakeHydroReader{
			"input/0":  []byte("1 2\n"),
			"output/0": []byte("3\n"),
			"input/1":  []byte("100 200\n"),
			"output/1": []byte("300\n"),
		},
		GeneratedAt: time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("BuildHydroProblemPackage returned error: %v", err)
	}
	if pkg.FileName != "C1000-hydro.zip" {
		t.Fatalf("filename = %q, want C1000-hydro.zip", pkg.FileName)
	}

	entries := unzipEntries(t, pkg.Content)
	for _, name := range []string{
		"problem.yaml",
		"problem_zh.md",
		"testdata/config.yaml",
		"testdata/1.in",
		"testdata/1.out",
		"testdata/2.in",
		"testdata/2.out",
		"additional_file/algoforge_manifest.json",
	} {
		if _, ok := entries[name]; !ok {
			t.Fatalf("zip entry %q missing; entries=%v", name, sortedKeys(entries))
		}
	}

	var problemYAML hydroProblemYAML
	if err := yaml.Unmarshal(entries["problem.yaml"], &problemYAML); err != nil {
		t.Fatalf("problem.yaml invalid: %v", err)
	}
	if problemYAML.PID != "C1000" || problemYAML.Title != "两数之和" {
		t.Fatalf("problem.yaml = %+v", problemYAML)
	}
	if len(problemYAML.Tag) != 2 || problemYAML.Tag[0] != "入门" || problemYAML.Tag[1] != "模拟" {
		t.Fatalf("problem tags = %#v", problemYAML.Tag)
	}

	var config hydroConfigYAML
	if err := yaml.Unmarshal(entries["testdata/config.yaml"], &config); err != nil {
		t.Fatalf("config.yaml invalid: %v", err)
	}
	if config.Type != "default" || config.CheckerType != "default" {
		t.Fatalf("config type/checker = %q/%q", config.Type, config.CheckerType)
	}
	if config.Time != "1500ms" || config.Memory != "128m" || config.Filename != "sum" || config.Detail != "full" {
		t.Fatalf("config limits/io/detail = %+v", config)
	}
	if len(config.Subtasks) != 1 {
		t.Fatalf("subtask count = %d", len(config.Subtasks))
	}
	if got := config.Subtasks[0].Score; got != 100 {
		t.Fatalf("subtask score = %d, want 100", got)
	}
	if len(config.Subtasks[0].Cases) != 2 || config.Subtasks[0].Cases[0].Score != 0 || config.Subtasks[0].Cases[1].Score != 100 {
		t.Fatalf("subtask cases = %+v", config.Subtasks[0].Cases)
	}

	statement := string(entries["problem_zh.md"])
	if !strings.HasPrefix(statement, "# 两数之和") || !strings.Contains(statement, "读取两个整数") {
		t.Fatalf("statement content = %q", statement)
	}

	var manifest hydroManifest
	if err := json.Unmarshal(entries["additional_file/algoforge_manifest.json"], &manifest); err != nil {
		t.Fatalf("manifest invalid: %v", err)
	}
	if manifest.Format != hydroExportFormat || manifest.HydroPhase != hydroPhaseOne {
		t.Fatalf("manifest identity = %+v", manifest)
	}
	if manifest.ProblemID != problemID.String() || manifest.WorkflowID != workflowID {
		t.Fatalf("manifest problem/workflow = %+v", manifest)
	}
	if len(manifest.TestCases) != 2 || manifest.TestCases[1].InputFile != "testdata/2.in" {
		t.Fatalf("manifest test cases = %+v", manifest.TestCases)
	}
	if len(manifest.Compatibility.ReservedUnsupported) == 0 {
		t.Fatalf("manifest should document reserved unsupported Hydro features")
	}
}

func TestBuildHydroProblemPackageRejectsUnsupportedChecker(t *testing.T) {
	problem := &domain.Problem{
		ID:           uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		SerialNumber: "AF-0042",
		Title:        "SPJ",
		Statement:    "statement",
		TimeLimit:    1000,
		MemoryLimit:  256,
		MetadataJSON: json.RawMessage(`{"hydro":{"checker_type":"testlib"}}`),
	}
	_, err := BuildHydroProblemPackage(context.Background(), HydroPackageRequest{
		Problem: problem,
		TestCases: []*domain.TestCase{{
			ID:         uuid.MustParse("22222222-2222-2222-2222-222222222222"),
			ProblemID:  problem.ID,
			InputPath:  "in",
			OutputPath: "out",
		}},
		ObjectReader: fakeHydroReader{"in": []byte(""), "out": []byte("")},
	})
	if err == nil || !strings.Contains(err.Error(), "checker_type=default") {
		t.Fatalf("err = %v, want unsupported checker validation error", err)
	}
}

func TestEnsureHydroExportAllowedRequiresPublishedFreshGate(t *testing.T) {
	tests := []struct {
		name    string
		problem *domain.Problem
		wantErr string
	}{
		{
			name: "published legacy metadata is allowed",
			problem: &domain.Problem{
				ID:     uuid.New(),
				Status: domain.ProblemStatusPublished,
			},
		},
		{
			name: "draft is blocked",
			problem: &domain.Problem{
				ID:     uuid.New(),
				Status: domain.ProblemStatusDraft,
			},
			wantErr: "requires published status",
		},
		{
			name: "published stale is blocked",
			problem: &domain.Problem{
				ID:           uuid.New(),
				Status:       domain.ProblemStatusPublished,
				MetadataJSON: json.RawMessage(`{"stale":true,"stale_reason":"problem edited"}`),
			},
			wantErr: "problem edit refresh is stale",
		},
		{
			name: "published denied gate is blocked",
			problem: &domain.Problem{
				ID:           uuid.New(),
				Status:       domain.ProblemStatusPublished,
				MetadataJSON: json.RawMessage(`{"publication_gate_status":"quarantined","publication_quarantine_reason":"missing test artifacts"}`),
			},
			wantErr: "publication_gate_status=published",
		},
		{
			name: "quarantined with reason is blocked",
			problem: &domain.Problem{
				ID:           uuid.New(),
				Status:       domain.ProblemStatusQuarantined,
				MetadataJSON: json.RawMessage(`{"publication_quarantine_reason":"policy denied"}`),
			},
			wantErr: "policy denied",
		},
		{
			name: "malformed metadata fails closed",
			problem: &domain.Problem{
				ID:           uuid.New(),
				Status:       domain.ProblemStatusPublished,
				MetadataJSON: json.RawMessage(`{"stale":`),
			},
			wantErr: "invalid publication metadata",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ensureHydroExportAllowed(tt.problem)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("ensureHydroExportAllowed() error = %v, want nil", err)
				}
				return
			}
			if err == nil || !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("ensureHydroExportAllowed() error = %v, want conflict containing %q", err, tt.wantErr)
			}
		})
	}
}

func unzipEntries(t *testing.T, data []byte) map[string][]byte {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	entries := make(map[string][]byte, len(reader.File))
	for _, file := range reader.File {
		rc, err := file.Open()
		if err != nil {
			t.Fatalf("open %s: %v", file.Name, err)
		}
		content, err := io.ReadAll(rc)
		if closeErr := rc.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
		if err != nil {
			t.Fatalf("read %s: %v", file.Name, err)
		}
		entries[file.Name] = content
	}
	return entries
}

func sortedKeys(values map[string][]byte) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
