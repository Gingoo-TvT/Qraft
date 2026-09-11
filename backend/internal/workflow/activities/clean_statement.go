package activities

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/llm"
	"github.com/rs/zerolog/log"
	"go.temporal.io/sdk/activity"
)

// cotPatterns detects chain-of-thought reasoning traces that may leak into
// generated problem statements. Case-insensitive.
var cotPatterns = regexp.MustCompile(
	`(?i)(\bactually\b.*\b(let me|reconsider|recheck)|` +
		`\blet me re(check|consider|calculate)|` +
		`\bwait[,\s]|hmm[,\s.]|` +
		`\bno that'?s (wrong|not)|` +
		`\bI (made a mistake|was wrong)|` +
		`\bon second thought|upon reflection|` +
		`\boops|scratch that|` +
		`\blet me (fix|correct) (the|this)|` +
		`等等[，,。]?|让我(重新|再|先)?(审视|检查|计算|确认)|` +
		`我需要(重新|再)?(修正|检查|确认)|` +
		`我(自己)?编的|我的理解有误|` +
		`不[，,。]?这不(对|符合)|` +
		`实际上.*(我需要|让我|等等)|` +
		`为了让题目更有趣|保持简单|增加一点难度)`)

const cleanStatementSystemPrompt = `You are a competitive programming problem editor. Your sole task is to clean a problem statement by removing any chain-of-thought reasoning, self-corrections, or thinking traces that were accidentally left in.

You MUST:
- Remove all reasoning traces, self-corrections, and thinking-out-loud passages
- Fix any examples whose explanations contain trial-and-error narratives; rewrite them as concise, authoritative explanations
- If an example's input/output appears incorrect due to a self-correction gone wrong, fix it or remove it
- Preserve the problem's semantic contract exactly: do not change the objective, fields, bounds, limits, or intended answer.
- Keep exactly one standalone Input Format, Output Format, and Constraints heading (or the corresponding Chinese labels); remove accidental duplicate headings.
- Never put TeX commands such as \\ldots or \\cdot inside inline code spans. Use plain text there, or delimit the complete expression with $...$.
- Keep real Markdown line breaks, remove literal \\n escapes, and leave no placeholders or undefined symbols.
- Preserve all other content exactly as-is unless a listed quality defect requires a minimal coherent edit.

You MUST output valid JSON with the following structure:
{"statement": "the cleaned statement text..."}`

// CleanStatementActivity scans the generated statement for chain-of-thought
// leakage patterns. If contamination is detected, it calls the LLM to produce
// a cleaned version. If the statement is already clean, it passes through
// unchanged with zero LLM cost.
func (a *Activities) CleanStatementActivity(
	ctx context.Context,
	statement StatementResult,
	params domain.ProblemGenParams,
) (*StatementResult, error) {
	logger := activity.GetLogger(ctx)
	statement.OneLineHint = NormalizeOneLineHintV1(statement.OneLineHint)
	statement.Statement = NormalizeStatementMathNotationV1(statement.Statement)
	statement.Statement = normalizeGeneratedStatementMarkdown(statement.Statement)
	statement.Statement = SynchronizeStatementResourceLimitsV1(
		statement.Statement,
		params.TimeLimit,
		params.MemoryLimit,
	)
	expectStructuredSamples := strings.Contains(statement.Statement, StatementSamplesPlaceholder)
	if err := validateGeneratedStatementMarkdown(statement.Statement, expectStructuredSamples); err != nil {
		return nil, fmt.Errorf("statement markdown quality: %w", err)
	}

	if !cotPatterns.MatchString(statement.Statement) {
		logger.Info("statement is clean, no CoT leakage detected")
		return &statement, nil
	}

	logger.Warn("CoT leakage detected in statement, invoking LLM cleanup")

	userPrompt := fmt.Sprintf(
		"The following competitive programming problem statement contains chain-of-thought reasoning traces that must be removed. "+
			"Clean it up, preserve its exact input/output contract, and also repair any duplicate canonical heading or TeX-in-inline-code defect you notice. Return ONLY the cleaned statement as JSON {\"statement\": \"...\"}.\n\n"+
			"--- BEGIN CONTAMINATED STATEMENT ---\n%s\n--- END CONTAMINATED STATEMENT ---",
		statement.Statement,
	)

	req := &llm.Request{
		MaxTokens: 8192,
		System:    cleanStatementSystemPrompt,
		Messages: []llm.Message{
			{
				Role:    "user",
				Content: userPrompt,
			},
		},
	}
	applyVerificationLLMRuntime(req, params)

	activity.RecordHeartbeat(ctx, "calling LLM to clean statement")

	stopHB := heartbeatWhile(ctx, "cleaning statement via LLM", 15*time.Second)
	resp, sourceArtifact, err := a.completeLLMWithProvenance(ctx, "clean_statement", req, 2)
	stopHB()
	if err != nil {
		return nil, wrapRequiredProviderEffectError("llm call for statement cleaning", err)
	}

	responseText := resp.Text()
	log.Info().
		Str("stop_reason", resp.StopReason).
		Int("output_tokens", resp.Usage.OutputTokens).
		Msg("LLM clean-statement response received")

	var parsed struct {
		Statement string `json:"statement"`
	}

	if err := json.Unmarshal([]byte(responseText), &parsed); err != nil {
		// Try extracting from a fenced code block.
		jsonContent := extractJSONBlock(responseText)
		if jsonContent == "" {
			jsonContent = extractOutermostJSON(responseText)
		}
		if jsonContent != "" {
			err = json.Unmarshal([]byte(jsonContent), &parsed)
		}
		if err != nil {
			return nil, fmt.Errorf("parsing clean-statement response: %w", err)
		}
	}

	if parsed.Statement == "" {
		return nil, fmt.Errorf("cleaned statement is empty")
	}
	parsed.Statement = NormalizeStatementMathNotationV1(parsed.Statement)
	parsed.Statement = normalizeGeneratedStatementMarkdown(parsed.Statement)
	parsed.Statement = SynchronizeStatementResourceLimitsV1(
		parsed.Statement,
		params.TimeLimit,
		params.MemoryLimit,
	)
	if cotPatterns.MatchString(parsed.Statement) {
		return nil, fmt.Errorf("cleaned statement still contains reasoning traces")
	}
	if err := validateGeneratedStatementMarkdown(parsed.Statement, expectStructuredSamples); err != nil {
		return nil, fmt.Errorf("cleaned statement markdown quality: %w", err)
	}

	result := statement
	result.Statement = parsed.Statement
	result.OneLineHint = NormalizeOneLineHintV1(result.OneLineHint)
	result.SourceArtifacts = append(result.SourceArtifacts, sourceArtifact)

	logger.Info("statement cleaned successfully")
	return &result, nil
}
