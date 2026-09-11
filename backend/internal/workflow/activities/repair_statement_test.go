package activities

import (
	"errors"
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
)

func TestRepairStatementInputV1AndPromptCarryExactBoundedContext(t *testing.T) {
	in := RepairStatementInputV1{
		PayloadVersion: ActivityPayloadVersion,
		Candidate: StatementResult{
			Title:     "树题",
			Statement: "题目。\n\n## 输入格式\n每行包含四个整数 u,v,c。",
			Tags:      []string{"dp-tree"},
		},
		Params: domain.ProblemGenParams{
			Difficulty:  2000,
			TimeLimit:   1000,
			MemoryLimit: 64,
			Tags:        []string{"dp-tree"},
		},
		UseStructuredSamples: true,
		RepairAttempt:        1,
		Diagnostics: []StatementMarkdownDiagnosticV1{{
			Code:    "integer_field_count_mismatch",
			Message: "line 4 claims 4 integers but lists 3 fields: u,v,c",
			Line:    4,
		}},
	}
	if err := in.Validate(); err != nil {
		t.Fatalf("valid repair input rejected: %v", err)
	}

	prompt := buildStatementRepairPromptV1(in)
	for _, want := range []string{
		"Frozen title: 树题",
		"[integer_field_count_mismatch] line 4",
		"每行包含四个整数 u,v,c",
		StatementSamplesPlaceholder,
		"Frozen requested tags: dp-tree",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("repair prompt omitted %q:\n%s", want, prompt)
		}
	}
	strictPrompt := buildStatementRepairPromptV1(RepairStatementInputV1{
		Candidate:             in.Candidate,
		Params:                in.Params,
		UseStructuredSamples:  true,
		StrictMarkdownQuality: true,
		RepairAttempt:         1,
		Diagnostics:           in.Diagnostics,
	})
	for _, want := range []string{"Strict markdown gate is active", "unique canonical headings", "no TeX commands in inline code"} {
		if !strings.Contains(strictPrompt, want) {
			t.Fatalf("strict repair prompt omitted %q:\n%s", want, strictPrompt)
		}
	}
}

func TestRepairStatementInputV1RejectsUnboundedOrContextFreeInput(t *testing.T) {
	base := RepairStatementInputV1{
		PayloadVersion: ActivityPayloadVersion,
		Candidate:      StatementResult{Title: "T", Statement: "S"},
		RepairAttempt:  1,
		Diagnostics: []StatementMarkdownDiagnosticV1{{
			Code:    "missing_input_heading",
			Message: "missing input heading",
		}},
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("base repair input rejected: %v", err)
	}

	withoutDiagnostics := base
	withoutDiagnostics.Diagnostics = nil
	if err := withoutDiagnostics.Validate(); err == nil {
		t.Fatal("repair input without deterministic diagnostics was accepted")
	}

	outOfRange := base
	outOfRange.RepairAttempt = 3
	if err := outOfRange.Validate(); err == nil {
		t.Fatal("third repair attempt was accepted")
	}
}

func TestParseStatementRepairResponseV1RepairsDoubleEscapedMarkdownNewlines(t *testing.T) {
	parsed, err := parseStatementRepairResponseV1(`{"statement":"题目。\\n\\n## 输入格式\\n输入 n。"}`)
	if err != nil {
		t.Fatalf("parse repair response: %v", err)
	}
	parsed = normalizeGeneratedStatementMarkdown(parsed)
	if strings.Contains(parsed, `\n`) || !strings.Contains(parsed, "\n\n## 输入格式\n") {
		t.Fatalf("repair response newline normalization failed: %q", parsed)
	}
}

func TestStatementRepairModelOutputFailureV1RetainsOriginalDiagnosticsWithoutDuplication(t *testing.T) {
	in := RepairStatementInputV1{
		Candidate: StatementResult{Title: "T", Statement: "S"},
		Diagnostics: []StatementMarkdownDiagnosticV1{
			{Code: "missing_input_heading", Message: "missing input heading"},
			{Code: "repair_response_invalid", Message: "old parse error"},
		},
	}
	result := statementRepairModelOutputFailureV1(in, nil, errors.New("bad JSON"))
	if len(result.MarkdownDiagnostics) != 2 {
		t.Fatalf("diagnostics = %+v, want original defect plus one parse defect", result.MarkdownDiagnostics)
	}
	if result.MarkdownDiagnostics[0].Code != "missing_input_heading" || result.MarkdownDiagnostics[1].Code != "repair_response_invalid" {
		t.Fatalf("unexpected diagnostic order/content: %+v", result.MarkdownDiagnostics)
	}
}
