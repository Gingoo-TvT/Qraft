package repository

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/google/uuid"
)

func TestProblemEditPatchValidation(t *testing.T) {
	title := "new"
	metadata := json.RawMessage(`[]`)
	tests := []struct {
		name    string
		patch   ProblemEditPatch
		wantErr string
	}{
		{
			name:    "missing problem",
			patch:   ProblemEditPatch{ExpectedUpdatedAt: time.Now(), Actor: "tester", Title: &title},
			wantErr: "problem ID",
		},
		{
			name:    "missing expected updated at",
			patch:   ProblemEditPatch{ProblemID: uuid.New(), Actor: "tester", Title: &title},
			wantErr: "expected_updated_at",
		},
		{
			name:    "missing actor",
			patch:   ProblemEditPatch{ProblemID: uuid.New(), ExpectedUpdatedAt: time.Now(), Title: &title},
			wantErr: "actor",
		},
		{
			name:    "no editable field",
			patch:   ProblemEditPatch{ProblemID: uuid.New(), ExpectedUpdatedAt: time.Now(), Actor: "tester"},
			wantErr: "no mutable fields",
		},
		{
			name: "metadata must be object",
			patch: ProblemEditPatch{
				ProblemID: uuid.New(), ExpectedUpdatedAt: time.Now(), Actor: "tester", MetadataJSON: &metadata,
			},
			wantErr: "JSON object",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateProblemEditPatch(tt.patch)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("validateProblemEditPatch() error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestProblemEditMetadataMarksStale(t *testing.T) {
	metadata, err := problemEditMetadata(json.RawMessage(`{"owner":"codex","publication_gate_status":"published","publication_quarantine_reason":"old decision","last_edit_refresh_validation_sha256":"old"}`), true, time.Date(2026, 8, 17, 1, 2, 3, 0, time.UTC))
	if err != nil {
		t.Fatalf("problemEditMetadata() error = %v", err)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(metadata, &decoded); err != nil {
		t.Fatalf("decode metadata: %v", err)
	}
	for _, key := range []string{"stale", "stale_reason", "stale_at", "stale_source", "edit_requires_republish", "edit_gate_version"} {
		if _, ok := decoded[key]; !ok {
			t.Fatalf("metadata missing %q: %s", key, metadata)
		}
	}
	if decoded["edit_requires_republish"] != true {
		t.Fatalf("edit_requires_republish = %v, want true", decoded["edit_requires_republish"])
	}
	for _, key := range []string{"publication_gate_status", "publication_quarantine_reason", "last_edit_refresh_validation_sha256"} {
		if _, ok := decoded[key]; ok {
			t.Fatalf("stale edit retained obsolete gate field %q: %s", key, metadata)
		}
	}
}

func TestProblemEditDependentsStaleSQLCoversReleaseCriticalRows(t *testing.T) {
	sql := markProblemEditDependentsStaleSQL()
	for _, required := range []string{
		"problem_embeddings pe",
		"embedding_active_pointers active",
		"solutions s",
		"testcases tc",
		"metadata_json",
		"'problem_edit'",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("stale SQL is missing %q:\n%s", required, sql)
		}
	}
}

func TestProblemEditRefreshPreflightBlockingIssues(t *testing.T) {
	preflight := ProblemEditRefreshPreflight{
		CurrentActiveStatementVectorCount: 0,
		SuccessfulMainSolutionCount:       0,
		SuccessfulBruteSolutionCount:      0,
		RunnableTestcaseCount:             0,
	}
	issues := preflight.BlockingIssues()
	for _, want := range []string{
		"current active statement vector count is 0, want 1",
		"successful main solution is required",
		"successful brute solution is required",
		"runnable testcase is required",
	} {
		if !containsString(issues, want) {
			t.Fatalf("blocking issues missing %q: %v", want, issues)
		}
	}

	preflight = ProblemEditRefreshPreflight{
		CurrentActiveStatementVectorCount: 1,
		CurrentActiveStatementVectorStale: 1,
		SuccessfulMainSolutionCount:       1,
		SuccessfulBruteSolutionCount:      1,
		RunnableTestcaseCount:             1,
	}
	issues = preflight.BlockingIssues()
	if !containsString(issues, "current active statement vector is still stale") {
		t.Fatalf("stale vector issue missing: %v", issues)
	}
}

func TestProblemEditRefreshValidationRequiresStableEvidence(t *testing.T) {
	valid := ProblemEditRefreshOptions{
		ProblemID:              uuid.New(),
		OperationKey:           "refresh-key",
		Actor:                  "tester",
		ValidationReportSHA256: strings.Repeat("a", 64),
	}
	if err := validateProblemEditRefreshOptions(valid); err != nil {
		t.Fatalf("valid refresh options rejected: %v", err)
	}

	tests := []struct {
		name    string
		options ProblemEditRefreshOptions
		wantErr string
	}{
		{name: "missing problem", options: ProblemEditRefreshOptions{
			OperationKey: "key", Actor: "tester", ValidationReportSHA256: strings.Repeat("a", 64),
		}, wantErr: "problem ID"},
		{name: "missing operation key", options: ProblemEditRefreshOptions{
			ProblemID: uuid.New(), Actor: "tester", ValidationReportSHA256: strings.Repeat("a", 64),
		}, wantErr: "operation key"},
		{name: "missing actor", options: ProblemEditRefreshOptions{
			ProblemID: uuid.New(), OperationKey: "key", ValidationReportSHA256: strings.Repeat("a", 64),
		}, wantErr: "actor"},
		{name: "invalid validation hash", options: ProblemEditRefreshOptions{
			ProblemID: uuid.New(), OperationKey: "key", Actor: "tester", ValidationReportSHA256: strings.Repeat("A", 64),
		}, wantErr: "validation_report_sha256"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateProblemEditRefreshOptions(tt.options)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("validateProblemEditRefreshOptions() error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestProblemEditRefreshSQLCoversCurrentVectorAndFresheningEvidence(t *testing.T) {
	preflightSQL := problemEditRefreshPreflightSQL()
	for _, required := range []string{
		"embedding_active_pointers active",
		"problem_embeddings pe",
		"embedding_statement_content_hash",
		"metadata_json ->> 'stale'",
		"solutions",
		"testcases",
		"FOR UPDATE",
	} {
		if !strings.Contains(preflightSQL, required) {
			t.Fatalf("refresh preflight SQL is missing %q:\n%s", required, preflightSQL)
		}
	}

	clearSQL := clearProblemEditStaleSQL()
	for _, required := range []string{
		"last_edit_refresh_validation_sha256",
		"last_edit_refresh_actor",
		"last_edit_refresh_at",
		"- 'stale_actor'",
		"embedding_statement_content_hash",
		"jsonb_strip_nulls",
	} {
		if !strings.Contains(clearSQL, required) {
			t.Fatalf("refresh clear SQL is missing %q:\n%s", required, clearSQL)
		}
	}
}

func TestProblemEditableFieldsChangedIgnoresSystemFields(t *testing.T) {
	before := &domain.Problem{
		ID: uuid.New(), Title: "title", Statement: "statement", Level: domain.LevelAlgorithm,
		Difficulty: 1600, Tags: []string{"dp"}, TimeLimit: 1000, MemoryLimit: 256,
		Status: domain.ProblemStatusPublished,
	}
	after := cloneProblemForEdit(before)
	after.Status = domain.ProblemStatusDraft
	if problemEditableFieldsChanged(before, after) {
		t.Fatal("status-only change should not count as user editable change")
	}
	after.Title = "changed"
	if !problemEditableFieldsChanged(before, after) {
		t.Fatal("title change was not detected")
	}
}
