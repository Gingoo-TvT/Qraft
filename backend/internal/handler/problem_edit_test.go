package handler

import (
	"strings"
	"testing"
	"time"
)

func TestParseProblemEditRequestRejectsSystemFields(t *testing.T) {
	body := `{"expected_updated_at":"2026-08-17T10:00:00Z","title":"new","status":"published"}`
	_, err := parseProblemEditRequest(strings.NewReader(body))
	if err == nil || !strings.Contains(err.Error(), `"status"`) {
		t.Fatalf("parseProblemEditRequest() error = %v, want status rejection", err)
	}
}

func TestParseProblemEditRequestRequiresExpectedUpdatedAt(t *testing.T) {
	_, err := parseProblemEditRequest(strings.NewReader(`{"title":"new"}`))
	if err == nil || !strings.Contains(err.Error(), "expected_updated_at") {
		t.Fatalf("parseProblemEditRequest() error = %v, want expected_updated_at requirement", err)
	}
}

func TestParseProblemEditRequestAcceptsEditableFields(t *testing.T) {
	body := `{
		"expected_updated_at":"2026-08-17T10:00:00Z",
		"title":"new",
		"statement":"statement",
		"level":"algorithm",
		"difficulty":1600,
		"one_line_hint":"hint",
		"detailed_solution":"solution",
		"tags":["dp"],
		"time_limit":1000,
		"memory_limit":256,
		"metadata_json":{"owner":"codex"}
	}`
	input, err := parseProblemEditRequest(strings.NewReader(body))
	if err != nil {
		t.Fatalf("parseProblemEditRequest() error = %v", err)
	}
	if input.Title == nil || *input.Title != "new" {
		t.Fatalf("Title = %v, want new", input.Title)
	}
	want := time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC)
	if !input.ExpectedUpdatedAt.Equal(want) {
		t.Fatalf("ExpectedUpdatedAt = %s, want %s", input.ExpectedUpdatedAt, want)
	}
}

func TestParseProblemEditRefreshRequestRequiresValidationHash(t *testing.T) {
	_, err := parseProblemEditRefreshRequest(strings.NewReader(`{"operation_key":"refresh-1"}`))
	if err == nil || !strings.Contains(err.Error(), "validation_report_sha256") {
		t.Fatalf("parseProblemEditRefreshRequest() error = %v, want validation hash requirement", err)
	}
}

func TestParseProblemEditRefreshRequestRejectsUnknownFields(t *testing.T) {
	_, err := parseProblemEditRefreshRequest(strings.NewReader(`{
		"validation_report_sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"status":"published"
	}`))
	if err == nil || !strings.Contains(err.Error(), `"status"`) {
		t.Fatalf("parseProblemEditRefreshRequest() error = %v, want unknown field rejection", err)
	}
}

func TestParseProblemEditRefreshRequestAcceptsOperationKeyAndActor(t *testing.T) {
	input, err := parseProblemEditRefreshRequest(strings.NewReader(`{
		"operation_key":"refresh-1",
		"validation_report_sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"actor":"tester"
	}`))
	if err != nil {
		t.Fatalf("parseProblemEditRefreshRequest() error = %v", err)
	}
	if input.OperationKey != "refresh-1" || input.Actor != "tester" ||
		input.ValidationReportSHA256 != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Fatalf("unexpected refresh input: %+v", input)
	}
}
