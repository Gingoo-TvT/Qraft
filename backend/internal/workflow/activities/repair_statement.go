package activities

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/llm"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
)

const statementRepairMaxCandidateRunesV1 = 50000

// RepairStatementInputV1 binds a targeted repair to the exact statement and
// the deterministic diagnostics produced from it. The activity may edit only
// the Markdown body; title, tags, hint, difficulty justification, and source
// lineage are carried forward by code rather than trusted to the model.
type RepairStatementInputV1 struct {
	PayloadVersion        int                             `json:"payload_version"`
	Candidate             StatementResult                 `json:"candidate"`
	Params                domain.ProblemGenParams         `json:"params"`
	UseStructuredSamples  bool                            `json:"use_structured_samples,omitempty"`
	StrictMarkdownQuality bool                            `json:"strict_markdown_quality,omitempty"`
	RepairAttempt         int                             `json:"repair_attempt"`
	Diagnostics           []StatementMarkdownDiagnosticV1 `json:"diagnostics"`
}

func (in RepairStatementInputV1) Validate() error {
	if in.PayloadVersion != ActivityPayloadVersion {
		return fmt.Errorf("unsupported statement repair payload version %d", in.PayloadVersion)
	}
	if in.RepairAttempt < 1 || in.RepairAttempt > 2 {
		return fmt.Errorf("statement repair attempt must be between 1 and 2")
	}
	if strings.TrimSpace(in.Candidate.Title) == "" || strings.TrimSpace(in.Candidate.Statement) == "" {
		return fmt.Errorf("statement repair requires a non-empty title and candidate")
	}
	if len([]rune(in.Candidate.Statement)) > statementRepairMaxCandidateRunesV1 {
		return fmt.Errorf("statement repair candidate exceeds %d runes", statementRepairMaxCandidateRunesV1)
	}
	if len(in.Diagnostics) == 0 || len(in.Diagnostics) > maxStatementMarkdownDiagnosticsV1 {
		return fmt.Errorf("statement repair requires between 1 and %d diagnostics", maxStatementMarkdownDiagnosticsV1)
	}
	for index, diagnostic := range in.Diagnostics {
		if strings.TrimSpace(diagnostic.Code) == "" || strings.TrimSpace(diagnostic.Message) == "" {
			return fmt.Errorf("statement repair diagnostic %d is incomplete", index)
		}
		if len([]rune(diagnostic.Code)) > 100 || len([]rune(diagnostic.Message)) > 2000 {
			return fmt.Errorf("statement repair diagnostic %d exceeds bounded text limits", index)
		}
	}
	return nil
}

const repairStatementSystemPromptV1 = `You are a competitive programming statement repair editor.

You receive one existing problem statement and deterministic quality diagnostics. Repair only the listed defects. This is not a request to invent or redesign a different problem.

Hard preservation rules:
- Preserve the title, story, mathematical objective, input/output semantics, constraints, limits, variable names, and intended algorithm.
- Do not add a new input field or silently change a bound merely to make prose look consistent.
- When a declared field count disagrees with an explicit field list, reconcile it using the complete input contract; never invent a phantom field.
- Keep every mathematical expression inside $...$ or $$...$$ and produce real Markdown line breaks.
- Write numeric bounds and inequalities as complete LaTeX expressions with scientific notation for large powers of ten; for example, use $1 \\le q \\le 2 \\times 10^5$, $1 \\le x,c \\le 10^9$, and $1 \\le k \\le 10^{14}$ instead of fragmented Unicode inequalities or long decimal strings.
- Use exactly one standalone heading for each canonical section: Input Format, Output Format, and Constraints (or the corresponding Chinese labels). Never duplicate a section at another heading level.
- Never put mathematical TeX commands (for example \ldots, \cdot, \le, or \sum) inside inline code spans. Use plain text in code spans, or put the complete expression inside $...$.
- Preserve the single-source sample contract: keep exactly one structured sample marker when requested, and never invent sample values.
- Return a polished statement with standalone input, output, and constraints headings, with every symbol and field count defined exactly once.
- Do not include analysis, repair notes, apologies, or chain-of-thought in the statement.

Return exactly one compact JSON object and nothing else:
{"statement":"the repaired full Markdown statement"}

The first output characters after the assistant prefill must be "statement".`

// RepairStatementActivityV1 performs one bounded, contextual statement repair.
// Remaining defects are returned as diagnostics rather than hidden behind an
// activity error so the workflow can stop on repeated feedback or after two
// repair rounds.
func (a *Activities) RepairStatementActivityV1(
	ctx context.Context,
	in RepairStatementInputV1,
) (*StatementResult, error) {
	if err := in.Validate(); err != nil {
		return nil, temporal.NewNonRetryableApplicationError(
			"invalid statement repair input: "+err.Error(),
			"InvalidParameterError",
			err,
		)
	}

	logger := activity.GetLogger(ctx)
	activity.RecordHeartbeat(ctx, fmt.Sprintf("repairing statement attempt %d", in.RepairAttempt))

	userPrompt := buildStatementRepairPromptV1(in)
	temperature := 0.0
	req := &llm.Request{
		MaxTokens:   12000,
		System:      repairStatementSystemPromptV1,
		Temperature: &temperature,
		Messages: []llm.Message{
			{Role: "user", Content: userPrompt},
			{Role: "assistant", Content: "{"},
		},
	}
	applyStatementLLMRuntime(req, in.Params)

	stopHB := heartbeatWhile(ctx, "calling LLM for targeted statement repair", 15*time.Second)
	resp, sourceArtifact, err := a.completeLLMWithProvenance(ctx, "repair_statement", req, 2)
	stopHB()
	if err != nil {
		return nil, wrapRequiredProviderEffectError("llm call for targeted statement repair", err)
	}

	repairedStatement, err := parseStatementRepairResponseV1(restoreJSONPrefill(resp.Text()))
	if err != nil {
		logger.Warn(
			"targeted statement repair returned invalid JSON",
			"repair_attempt", in.RepairAttempt,
			"error", err,
		)
		return statementRepairModelOutputFailureV1(in, sourceArtifact, err), nil
	}
	repairedStatement = normalizeGeneratedStatementMarkdown(repairedStatement)
	repairedStatement = SynchronizeStatementResourceLimitsV1(
		repairedStatement,
		in.Params.TimeLimit,
		in.Params.MemoryLimit,
	)
	if strings.TrimSpace(repairedStatement) == "" {
		err := fmt.Errorf("targeted statement repair returned an empty statement")
		return statementRepairModelOutputFailureV1(in, sourceArtifact, err), nil
	}

	result := in.Candidate
	result.Statement = repairedStatement
	if in.StrictMarkdownQuality {
		result.MarkdownDiagnostics = DiagnoseGeneratedStatementMarkdownStrictV1(
			repairedStatement,
			in.UseStructuredSamples,
		)
	} else {
		result.MarkdownDiagnostics = diagnoseGeneratedStatementMarkdownV1(
			repairedStatement,
			in.UseStructuredSamples,
			true,
		)
	}
	result.SourceArtifacts = append(result.SourceArtifacts, sourceArtifact)

	logger.Info(
		"targeted statement repair completed",
		"repair_attempt", in.RepairAttempt,
		"remaining_diagnostics", len(result.MarkdownDiagnostics),
	)
	return &result, nil
}

func statementRepairModelOutputFailureV1(
	in RepairStatementInputV1,
	sourceArtifact *ArtifactRef,
	cause error,
) *StatementResult {
	result := in.Candidate
	result.SourceArtifacts = append(result.SourceArtifacts, sourceArtifact)
	diagnostics := make([]StatementMarkdownDiagnosticV1, 0, len(in.Diagnostics)+1)
	for _, diagnostic := range in.Diagnostics {
		if diagnostic.Code == "repair_response_invalid" {
			continue
		}
		diagnostics = append(diagnostics, diagnostic)
		if len(diagnostics) >= maxStatementMarkdownDiagnosticsV1-1 {
			break
		}
	}
	diagnostics = append(diagnostics, StatementMarkdownDiagnosticV1{
		Code:    "repair_response_invalid",
		Message: "targeted repair response could not be parsed as a non-empty statement JSON object: " + boundedRetryPromptText(cause.Error(), 500),
	})
	result.MarkdownDiagnostics = diagnostics
	return &result
}

func buildStatementRepairPromptV1(in RepairStatementInputV1) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "Repair attempt: %d/2\n", in.RepairAttempt)
	fmt.Fprintf(&builder, "Frozen title: %s\n", boundedRetryPromptText(in.Candidate.Title, 200))
	fmt.Fprintf(&builder, "Requested difficulty: %d; time limit: %d ms; memory limit: %d MB\n",
		in.Params.Difficulty,
		in.Params.TimeLimit,
		in.Params.MemoryLimit,
	)
	if len(in.Params.Tags) > 0 {
		fmt.Fprintf(&builder, "Frozen requested tags: %s\n", strings.Join(in.Params.Tags, ", "))
	}
	builder.WriteString("\nThe following deterministic diagnostics must all be resolved:\n")
	for index, diagnostic := range in.Diagnostics {
		if diagnostic.Line > 0 {
			fmt.Fprintf(&builder, "%d. [%s] line %d: %s\n", index+1, diagnostic.Code, diagnostic.Line, diagnostic.Message)
		} else {
			fmt.Fprintf(&builder, "%d. [%s] %s\n", index+1, diagnostic.Code, diagnostic.Message)
		}
	}
	if in.UseStructuredSamples {
		builder.WriteString("\nStructured samples are authoritative. Keep exactly one literal " + StatementSamplesPlaceholder + " marker, do not write sample values, and do not add a Samples heading around it.\n")
	} else {
		builder.WriteString("\nDo not leave any " + StatementSamplesPlaceholder + " marker in the final statement.\n")
	}
	if in.StrictMarkdownQuality {
		builder.WriteString("Strict markdown gate is active: before returning, check unique canonical headings, no TeX commands in inline code, real newlines (not literal \\n), balanced math delimiters, no placeholders, and no undefined symbols.\n")
	}
	builder.WriteString("\nMake the smallest coherent edit that resolves every diagnostic. Return the complete repaired statement, not a patch.\n")
	builder.WriteString("\n<statement>\n")
	builder.WriteString(in.Candidate.Statement)
	builder.WriteString("\n</statement>\n")
	return builder.String()
}

func parseStatementRepairResponseV1(responseText string) (string, error) {
	var parsed struct {
		Statement string `json:"statement"`
	}
	tryParse := func(candidate string) bool {
		if candidate == "" {
			return false
		}
		if err := json.Unmarshal([]byte(candidate), &parsed); err == nil {
			return true
		}
		repaired := repairInvalidJSONStringEscapes(candidate)
		return repaired != candidate && json.Unmarshal([]byte(repaired), &parsed) == nil
	}

	if !tryParse(strings.TrimSpace(responseText)) {
		candidate := extractJSONBlock(responseText)
		if candidate == "" {
			candidate = extractOutermostJSON(responseText)
		}
		if !tryParse(candidate) {
			return "", fmt.Errorf("response is not valid statement JSON")
		}
	}
	if strings.TrimSpace(parsed.Statement) == "" {
		return "", fmt.Errorf("response contains an empty statement field")
	}
	return parsed.Statement, nil
}
