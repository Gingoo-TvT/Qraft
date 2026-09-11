package repository

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/google/uuid"
)

type valueScanner struct {
	values []interface{}
}

func (s valueScanner) Scan(dest ...interface{}) error {
	for i := range dest {
		reflect.ValueOf(dest[i]).Elem().Set(reflect.ValueOf(s.values[i]))
	}
	return nil
}

func TestPublicationGateDecisionFailsClosed(t *testing.T) {
	tests := []struct {
		name         string
		bindingFound bool
		allowed      bool
		reasons      []string
		wantStatus   domain.ProblemStatus
		wantReason   bool
	}{
		{name: "allowed", bindingFound: true, allowed: true, wantStatus: domain.ProblemStatusPublished},
		{name: "policy denied", bindingFound: true, allowed: false, wantStatus: domain.ProblemStatusQuarantined, wantReason: true},
		{name: "binding missing", bindingFound: false, allowed: true, wantStatus: domain.ProblemStatusQuarantined, wantReason: true},
		{name: "publication prerequisite missing", bindingFound: true, allowed: true, reasons: []string{"missing current active statement vector"}, wantStatus: domain.ProblemStatusQuarantined, wantReason: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status, reason := publicationGateDecision(test.bindingFound, test.allowed, test.reasons...)
			if status != test.wantStatus {
				t.Fatalf("status = %q, want %q", status, test.wantStatus)
			}
			if (reason != "") != test.wantReason {
				t.Fatalf("reason = %q, wantReason=%v", reason, test.wantReason)
			}
		})
	}
}

func TestGenerationStandardEvidenceAttachIsNarrowAndIdempotent(t *testing.T) {
	query := generationStandardEvidenceAttachSQL()
	for _, required := range []string{
		"INSERT INTO problem_generation_standard_evidence",
		"outcome_category",
		"outcome_sha256",
		"SELECT $1, $2, $3, $4, $5, $6, $7, $8, $9",
		"FROM problems",
		"status = $5::text",
		"ON CONFLICT (problem_id) DO NOTHING",
	} {
		if !strings.Contains(query, required) {
			t.Fatalf("standard evidence attach SQL is missing %q:\n%s", required, query)
		}
	}
	for _, forbidden := range []string{"UPDATE problems", "metadata_json", "title =", "statement =", "DELETE"} {
		if strings.Contains(query, forbidden) {
			t.Fatalf("standard evidence attach SQL mutates unrelated field %q:\n%s", forbidden, query)
		}
	}
}

func TestPublicReleaseApprovalReportCarriesAuditIdentity(t *testing.T) {
	now := time.Now().UTC()
	report := PublicReleaseApprovalReport{
		ProblemID:     uuid.New(),
		ArtifactID:    uuid.New(),
		ApprovalID:    uuid.New(),
		ApprovedBy:    "admin@example.test",
		ApprovedAt:    now,
		ReleaseStatus: domain.ProblemStatusPublished,
	}
	if report.ProblemID == uuid.Nil || report.ArtifactID == uuid.Nil || report.ApprovalID == uuid.Nil {
		t.Fatal("approval report is missing immutable identifiers")
	}
	if report.ApprovedBy == "" || report.ApprovedAt.IsZero() {
		t.Fatal("approval report is missing actor or timestamp")
	}
	if report.ReleaseStatus != domain.ProblemStatusPublished {
		t.Fatalf("release status = %q", report.ReleaseStatus)
	}
}

func TestOperatorTermsSnapshotUsesAuditedStateTransition(t *testing.T) {
	insertSQL := insertOperatorTermsSnapshotSQL()
	if !strings.Contains(insertSQL, "'unreviewed'") || strings.Contains(insertSQL, "'approved'") {
		t.Fatalf("terms snapshot insert must start unreviewed:\n%s", insertSQL)
	}
	approveSQL := approveOperatorTermsSnapshotSQL()
	for _, required := range []string{"SET review_status='approved'", "review_status='unreviewed'"} {
		if !strings.Contains(approveSQL, required) {
			t.Fatalf("terms snapshot approval SQL is missing %q:\n%s", required, approveSQL)
		}
	}
}

func TestOperatorApprovalCoversStrictTransitiveLineage(t *testing.T) {
	sql := applyOperatorApprovalToLineageSQL()
	for _, required := range []string{
		"WITH RECURSIVE lineage",
		"provenance_artifact_ancestry",
		"artifact.takedown_status='quarantined_unknown'",
		"artifact.license_basis='unknown'",
		"operator_public_release_approved",
		"FROM updated",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("lineage approval SQL is missing %q:\n%s", required, sql)
		}
	}
}

func TestMergeReviewQuarantineReasonPreservesMetadata(t *testing.T) {
	got, err := mergeReviewQuarantineReason(
		json.RawMessage(`{"publication_quarantine_reason":"policy denied"}`),
		"automated review rejected candidate",
	)
	if err != nil {
		t.Fatalf("merge quarantine reason: %v", err)
	}
	var values map[string]interface{}
	if err := json.Unmarshal(got, &values); err != nil {
		t.Fatalf("decode merged metadata: %v", err)
	}
	if values["publication_quarantine_reason"] != "policy denied" {
		t.Fatalf("existing metadata was not preserved: %s", got)
	}
	if values["review_quarantine_reason"] != "automated review rejected candidate" {
		t.Fatalf("review reason missing: %s", got)
	}

	unchanged, err := mergeReviewQuarantineReason(got, "  ")
	if err != nil || string(unchanged) != string(got) {
		t.Fatalf("blank reason changed metadata: got=%s err=%v", unchanged, err)
	}
}

func TestPublicationGatePrerequisitesIncludeMandatoryArtifactsAndActiveVector(t *testing.T) {
	sql := publicationGatePrerequisitesSQL()
	for _, required := range []string{
		"solutions s",
		"s.solution_type = 'main'",
		"s.solution_type = 'brute'",
		"testcases tc",
		"embedding_active_pointers active",
		"problem_embeddings pe",
		"embedding_statement_content_hash",
		"metadata_json ->> 'stale'",
		"COALESCE(p.metadata_json ->> 'stale', 'false') <> 'true'",
		"FOR SHARE OF p",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("publication prerequisite SQL is missing %q:\n%s", required, sql)
		}
	}
}

func TestPublicationGateUpdatePersistsPolicyVersionEvidence(t *testing.T) {
	sql := updateProblemPublicationGateSQL()
	for _, required := range []string{
		"SET status = $2::text",
		"metadata_json",
		"publication_policy_version",
		"publication_gate_version",
		"publication_gate_status",
		"publication_quarantine_reason",
		"jsonb_strip_nulls",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("publication gate update SQL is missing %q:\n%s", required, sql)
		}
	}
}

func TestPublicationGatePrerequisiteReasons(t *testing.T) {
	reasons := publicationGatePrerequisites{}.blockingReasons()
	for _, want := range []string{
		"missing detailed solution",
		"missing successful main solution",
		"missing successful brute solution",
		"missing runnable test artifacts",
		"missing current active statement vector",
		"problem edit refresh is stale",
		"current active statement vector is stale",
		"solution artifacts are stale",
		"test artifacts are stale",
	} {
		if !containsString(reasons, want) {
			t.Fatalf("blocking reasons missing %q: %v", want, reasons)
		}
	}
}

func TestProblemCanonicalFieldsAreWrittenAndScanned(t *testing.T) {
	for _, field := range []string{"time_limit", "memory_limit", "source", "workflow_id"} {
		if !strings.Contains(createProblemQuery, field) {
			t.Fatalf("create query is missing %s", field)
		}
		if !strings.Contains(updateProblemQuery, field) {
			t.Fatalf("update query is missing %s", field)
		}
		if !strings.Contains(problemSelectColumns, field) {
			t.Fatalf("select columns are missing %s", field)
		}
	}

	id := uuid.New()
	workflowID := "problem-gen-fixture"
	created := time.Date(2026, 7, 13, 1, 2, 3, 0, time.UTC)
	updated := created.Add(time.Minute)
	metadata := json.RawMessage(`{"fixture":true}`)
	want := &domain.Problem{
		ID:               id,
		SerialNumber:     "42",
		Title:            "title",
		Statement:        "statement",
		Level:            domain.LevelAlgorithm,
		Difficulty:       1800,
		OneLineHint:      "hint",
		DetailedSolution: "solution",
		Tags:             []string{"graphs"},
		TimeLimit:        3210,
		MemoryLimit:      768,
		Source:           "algoforge_workflow",
		Status:           domain.ProblemStatusQuarantined,
		WorkflowID:       &workflowID,
		MetadataJSON:     metadata,
		CreatedAt:        created,
		UpdatedAt:        updated,
	}
	values := []interface{}{
		want.ID, want.SerialNumber, want.Title, want.Statement, want.Level, want.Difficulty,
		want.OneLineHint, want.DetailedSolution, want.Tags, want.TimeLimit, want.MemoryLimit,
		want.Source, want.Status, sql.NullString{String: workflowID, Valid: true},
		want.MetadataJSON, want.CreatedAt, want.UpdatedAt,
	}
	got := &domain.Problem{}
	if err := scanProblemRow(valueScanner{values: values}, got); err != nil {
		t.Fatalf("scan problem: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("scanned problem mismatch:\n got: %+v\nwant: %+v", got, want)
	}

	values[13] = sql.NullString{}
	if err := scanProblemRow(valueScanner{values: values}, got); err != nil {
		t.Fatalf("scan problem with null workflow ID: %v", err)
	}
	if got.WorkflowID != nil {
		t.Fatalf("workflow ID = %q, want nil", *got.WorkflowID)
	}
}

func TestProblemPublishedSurfaceQueriesExcludeQuarantineAndRejected(t *testing.T) {
	conditions, _, _ := buildProblemListWhere(ProblemFilter{ExcludeQuarantined: true, ExcludeRejected: true})
	if !containsString(conditions, "status <> 'quarantined'") {
		t.Fatalf("default list conditions do not exclude quarantine: %v", conditions)
	}
	if !containsString(conditions, "status <> 'rejected'") {
		t.Fatalf("default list conditions do not exclude rejected candidates: %v", conditions)
	}
	if !containsSubstring(conditions, "problem_quarantine_records") {
		t.Fatalf("default list conditions do not exclude immutable quarantine ledgers: %v", conditions)
	}

	status := "quarantined"
	conditions, _, _ = buildProblemListWhere(ProblemFilter{Status: &status})
	if containsString(conditions, "status <> 'quarantined'") || !containsString(conditions, "status = $1") {
		t.Fatalf("explicit quarantine lookup conditions = %v", conditions)
	}
	status = "rejected"
	conditions, _, _ = buildProblemListWhere(ProblemFilter{Status: &status})
	if containsString(conditions, "status <> 'rejected'") || !containsString(conditions, "status = $1") {
		t.Fatalf("explicit rejected lookup conditions = %v", conditions)
	}

	if !strings.Contains(findByTitleQuery(), "status <> 'quarantined'") ||
		!strings.Contains(findByTitleQuery(), "status <> 'rejected'") ||
		!strings.Contains(findByTitleQuery(), "problem_quarantine_records") ||
		!strings.Contains(findByTitleQuery(), "metadata_json ->> 'stale'") {
		t.Fatal("default exact-title retrieval does not exclude quarantined/rejected candidates")
	}
	for name, query := range map[string]string{
		"vector similarity":        findSimilarQuery(),
		"vector similarity except": findSimilarExceptQuery(),
	} {
		if !strings.Contains(query, "p.status <> 'quarantined'") ||
			!strings.Contains(query, "p.status <> 'rejected'") ||
			!strings.Contains(query, "problem_quarantine_records") {
			t.Fatalf("%s retrieval does not exclude quarantined/rejected candidates", name)
		}
	}
}

func TestValidateReviewQuarantineRecordRequiresCompleteHashedEvidence(t *testing.T) {
	reviewJSON := json.RawMessage(`{"approved":false,"issues":["ambiguous"],"suggestions":["clarify"],"confidence":0.8,"estimated_difficulty":1500,"is_duplicate":false,"duplicate_of":"","duplicate_reason":"","source_artifacts":[{"model_revision":"fixture-r1"}],"full_text":"complete review"}`)
	digest := sha256.Sum256(reviewJSON)
	record := ReviewQuarantineRecord{
		ProblemID:          uuid.New(),
		OperationKey:       "workflow/store-problem/v1",
		Reason:             "review rejected",
		WorkflowID:         "workflow",
		WorkflowRunID:      "run",
		ReviewGateChangeID: "problem-generation-review-gate-v2",
		ReviewGateVersion:  2,
		ReviewResultSHA256: hex.EncodeToString(digest[:]),
		ReviewResultJSON:   reviewJSON,
		ReviewText:         "complete review",
		SourceAncestryJSON: json.RawMessage(`[{"model_revision":"fixture-r1"}]`),
	}
	if err := validateReviewQuarantineRecord(record); err != nil {
		t.Fatalf("valid quarantine record rejected: %v", err)
	}

	record.ReviewText = "truncated"
	if err := validateReviewQuarantineRecord(record); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("mismatched full review text error = %v", err)
	}
	record.ReviewText = "complete review"
	record.SourceAncestryJSON = json.RawMessage(`[{"model_revision":"different-r1"}]`)
	if err := validateReviewQuarantineRecord(record); err == nil || !strings.Contains(err.Error(), "source ancestry does not match") {
		t.Fatalf("mismatched source ancestry error = %v", err)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func containsSubstring(values []string, want string) bool {
	for _, value := range values {
		if strings.Contains(value, want) {
			return true
		}
	}
	return false
}
