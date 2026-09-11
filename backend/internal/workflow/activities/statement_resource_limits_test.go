package activities

import (
	"strings"
	"testing"
)

func TestSynchronizeStatementResourceLimitsV1ReplacesChineseStandaloneLimits(t *testing.T) {
	statement := "题目说明\n\n时间限制：2000 ms\n内存限制：128 MB\n时间限制：3 秒"
	got := SynchronizeStatementResourceLimitsV1(statement, 4600, 256)
	if strings.Count(got, "时间限制：") != 1 || strings.Count(got, "内存限制：") != 1 {
		t.Fatalf("resource declarations were not deduplicated: %q", got)
	}
	if !strings.Contains(got, "时间限制：4600 ms") || !strings.Contains(got, "内存限制：256 MB") {
		t.Fatalf("resource declarations were not synchronized: %q", got)
	}
}

func TestSynchronizeStatementResourceLimitsV1ReplacesInlineSeconds(t *testing.T) {
	statement := "本题时间限制为 2 秒，内存限制为 64 MB。\n\n## Input Format\n..."
	got := SynchronizeStatementResourceLimitsV1(statement, 1000, 512)
	if !strings.Contains(got, "时间限制为 1000 ms") || !strings.Contains(got, "内存限制为 512 MB") {
		t.Fatalf("inline resource declarations were not synchronized: %q", got)
	}
	if strings.Contains(got, "2 秒") || strings.Contains(got, "64 MB") {
		t.Fatalf("stale inline resource values remain: %q", got)
	}
}

func TestSynchronizeStatementResourceLimitsV1AddsMissingEnglishDeclarations(t *testing.T) {
	statement := "This is an English problem statement.\n\n## Input Format\nRead n."
	got := SynchronizeStatementResourceLimitsV1(statement, 1200, 256)
	if !strings.Contains(got, "Time limit: 1200 ms") || !strings.Contains(got, "Memory limit: 256 MB") {
		t.Fatalf("missing declarations were not added: %q", got)
	}
}

func TestSynchronizeStatementResourceLimitsV1PlacesMissingDeclarationsBeforeSampleMarker(t *testing.T) {
	statement := "题面\n\n" + StatementSamplesPlaceholder
	got := SynchronizeStatementResourceLimitsV1(statement, 1000, 256)
	wantOrder := "时间限制：1000 ms\n内存限制：256 MB\n\n" + StatementSamplesPlaceholder
	if !strings.Contains(got, wantOrder) {
		t.Fatalf("resource declarations were not placed before structured samples: %q", got)
	}
}

func TestSynchronizeStatementResourceLimitsV1LeavesFencedCodeUntouched(t *testing.T) {
	statement := "说明\n\n```text\n时间限制：2000 ms\n内存限制：64 MB\n```\n\n时间限制：2000 ms"
	got := SynchronizeStatementResourceLimitsV1(statement, 1000, 256)
	if !strings.Contains(got, "```text\n时间限制：2000 ms\n内存限制：64 MB\n```") {
		t.Fatalf("fenced code was modified: %q", got)
	}
	if !strings.Contains(got, "时间限制：1000 ms") {
		t.Fatalf("visible resource declaration was not synchronized: %q", got)
	}
}
