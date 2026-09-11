package repository

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestActivePointerSwitchValidation(t *testing.T) {
	target := uuid.New()
	tests := []struct {
		name    string
		options ActivePointerSwitchOptions
		wantErr string
	}{
		{
			name: "missing target",
			options: ActivePointerSwitchOptions{
				Kind:                EmbeddingKindStatement,
				Operation:           EmbeddingSwitchOperationCutover,
				Actor:               "tester",
				Reason:              "fixture",
				DatasetReportSHA256: strings.Repeat("a", 64),
			},
			wantErr: "to_model_version_id is required",
		},
		{
			name: "bad operation",
			options: ActivePointerSwitchOptions{
				Kind:                EmbeddingKindStatement,
				Operation:           "promote",
				ToModelVersionID:    target,
				Actor:               "tester",
				Reason:              "fixture",
				DatasetReportSHA256: strings.Repeat("a", 64),
			},
			wantErr: "operation",
		},
		{
			name: "missing report hash",
			options: ActivePointerSwitchOptions{
				Kind:             EmbeddingKindStatement,
				Operation:        EmbeddingSwitchOperationCutover,
				ToModelVersionID: target,
				Actor:            "tester",
				Reason:           "fixture",
			},
			wantErr: "dataset_report_sha256",
		},
		{
			name: "shadow errors exceed reads",
			options: ActivePointerSwitchOptions{
				Kind:                EmbeddingKindStatement,
				Operation:           EmbeddingSwitchOperationCutover,
				ToModelVersionID:    target,
				Actor:               "tester",
				Reason:              "fixture",
				DatasetReportSHA256: strings.Repeat("a", 64),
				ShadowReadCount:     10,
				ShadowErrorCount:    11,
			},
			wantErr: "shadow_error_count",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateActivePointerSwitchOptions(tt.options)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("validateActivePointerSwitchOptions() error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestActivePointerSwitchPreflightBlockingIssues(t *testing.T) {
	preflight := ActivePointerSwitchPreflight{
		EligibleProblemCount:              100,
		TargetMissingCurrentVectorCount:   1,
		TargetDuplicateCurrentVectorCount: 1,
		ShadowReadCount:                   10000,
		ShadowErrorCount:                  10,
		ShadowErrorRate:                   0.001,
	}
	issues := preflight.BlockingIssues()
	for _, want := range []string{"coverage", "duplicate", "shadow error rate"} {
		found := false
		for _, issue := range issues {
			if strings.Contains(issue, want) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("blocking issues missing %q: %v", want, issues)
		}
	}
	if got := preflight.Coverage(); got >= 1 {
		t.Fatalf("Coverage() = %f, want below 1", got)
	}
}

func TestActivePointerSwitchPreflightSQLUsesCurrentPublishedContent(t *testing.T) {
	sql := activePointerSwitchPreflightSQL()
	for _, required := range []string{
		"p.status = 'published'",
		"problem_quarantine_records",
		"problem_embeddings pe",
		"pe.model_version_id = $1",
		"pe.embedding_kind = $2",
		"embedding_statement_content_hash",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("active pointer switch preflight SQL is missing %q:\n%s", required, sql)
		}
	}
}
