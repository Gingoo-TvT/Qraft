package activities

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/llm"
	"github.com/rs/zerolog/log"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
)

// GenerateStatementActivity calls the LLM to generate a problem statement
// based on the provided generation parameters. It parses the structured
// output into a StatementResult containing the title, statement body, tags,
// hint, and difficulty justification.
func (a *Activities) GenerateStatementActivity(ctx context.Context, in GenerateStatementInput) (*StatementResult, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("generating problem statement",
		"level", in.Params.Level,
		"difficulty", in.Params.Difficulty,
		"tags", in.Params.Tags,
	)

	activity.RecordHeartbeat(ctx, "calling LLM to generate statement")

	prompt := buildStatementPromptWithOptions(in.Params, in.Neighbors, in.UseStructuredSamples)
	if in.RetryFeedback != nil {
		var repairedPrompt strings.Builder
		repairedPrompt.WriteString(prompt)
		appendStatementRetryFeedback(&repairedPrompt, in.RetryFeedback)
		prompt = repairedPrompt.String()
	}

	temperature := 0.2
	req := &llm.Request{
		MaxTokens:   12000,
		System:      statementSystemPromptForInput(in.UseStructuredSamples),
		Temperature: &temperature,
		Messages: []llm.Message{
			{
				Role:    "user",
				Content: prompt,
			},
			{
				Role:    "assistant",
				Content: "{",
			},
		},
	}
	applyStatementLLMRuntime(req, in.Params)

	stopHB := heartbeatWhile(ctx, "calling LLM to generate statement", 15*time.Second)
	resp, sourceArtifact, err := a.completeLLMWithProvenance(ctx, "statement", req, 2)
	stopHB()
	if err != nil {
		return nil, wrapRequiredProviderEffectError("llm call for statement generation", err)
	}

	activity.RecordHeartbeat(ctx, "parsing LLM response")

	responseText := restoreJSONPrefill(resp.Text())
	log.Info().
		Str("stop_reason", resp.StopReason).
		Int("output_tokens", resp.Usage.OutputTokens).
		Int("response_len", len(responseText)).
		Msg("LLM statement response received")

	result, err := parseStatementResponse(responseText)
	if err != nil {
		return nil, fmt.Errorf("parsing statement response: %w", err)
	}

	// Validate the result.
	if result.Title == "" {
		return nil, temporal.NewNonRetryableApplicationError(
			"generated statement has empty title",
			"InvalidParameterError",
			nil,
		)
	}
	if result.Statement == "" {
		return nil, temporal.NewNonRetryableApplicationError(
			"generated statement has empty body",
			"InvalidParameterError",
			nil,
		)
	}
	// Normalize the public math surface before any downstream activity sees the
	// statement.  This turns exact powers such as 1000000000 into 10^9 and
	// canonicalizes \\(...\\) / \\[...\\] delimiters, while leaving code samples
	// untouched.
	result.Statement = NormalizeStatementMathNotationV1(result.Statement)
	result.Statement = normalizeGeneratedStatementMarkdown(result.Statement)
	// Limits supplied to the model are the current public contract until the
	// standard-solution calibration step. Normalize them here so every
	// downstream generator sees one canonical declaration instead of a stale
	// prose value or a duplicate line.
	result.Statement = SynchronizeStatementResourceLimitsV1(
		result.Statement,
		in.Params.TimeLimit,
		in.Params.MemoryLimit,
	)
	if in.EnableStatementRepair {
		if in.StrictMarkdownQuality {
			result.MarkdownDiagnostics = DiagnoseGeneratedStatementMarkdownStrictV1(
				result.Statement,
				in.UseStructuredSamples,
			)
		} else {
			result.MarkdownDiagnostics = diagnoseGeneratedStatementMarkdownV1(
				result.Statement,
				in.UseStructuredSamples,
				true,
			)
		}
	} else {
		// Preserve the legacy fail-fast activity behavior for workflow histories
		// that were started before bounded statement repair was introduced.
		if in.UseStructuredSamples && strings.Count(result.Statement, StatementSamplesPlaceholder) != 1 {
			return nil, temporal.NewNonRetryableApplicationError(
				"generated statement must contain exactly one structured sample placeholder",
				"InvalidParameterError",
				nil,
			)
		}
		var qualityErr error
		if in.StrictMarkdownQuality {
			qualityErr = ValidateGeneratedStatementMarkdownStrictV1(result.Statement, in.UseStructuredSamples)
		} else {
			qualityErr = validateGeneratedStatementMarkdown(result.Statement, in.UseStructuredSamples)
		}
		if qualityErr != nil {
			return nil, temporal.NewNonRetryableApplicationError(
				"generated statement markdown quality: "+qualityErr.Error(),
				"InvalidParameterError",
				qualityErr,
			)
		}
	}
	canonicalTags, err := conformStatementKnowledgePoints(in.Params, result.Tags)
	if err != nil {
		return nil, err
	}
	result.Tags = canonicalTags
	result.SourceArtifacts = append(result.SourceArtifacts, sourceArtifact)

	logger.Info("statement generated successfully", "title", result.Title)
	return result, nil
}

const statementSystemPrompt = `You are an expert competitive programming problem setter. Your task is to create original, high-quality problem statements for competitive programming contests.

You MUST output valid JSON with the following structure:
{
  "title": "Problem Title",
  "statement": "Full problem statement in Markdown...",
  "tags": ["tag1", "tag2"],
  "one_line_hint": "An internal authoring clue (not included in the contestant export)",
  "difficulty_justification": "Internal explanation of why this problem matches the target difficulty"
}

Output rules:
- Return exactly one compact JSON object and nothing else.
- Do not wrap the JSON in Markdown fences.
- Do not include analysis, proof, solution explanation, or hidden reasoning in the output.
- The first output characters after the assistant prefill must be "title".
- Encode each Markdown newline exactly once as the JSON escape \n. Never encode a newline as \\n; after JSON decoding the statement must contain real line breaks, not the two visible characters backslash+n.
- Put every mathematical expression inside $...$ (inline) or $$...$$ (block). In the JSON source, encode one TeX backslash as \\; after JSON decoding, Markdown math must contain a single backslash.
- Use LaTeX for every numeric bound and inequality. Write powers of ten in scientific notation, for example $1 \le q \le 2 \times 10^5$, $1 \le x \le 10^9$, and $1 \le k \le 10^{14}$; never spell these powers as long decimal strings such as 1000000000 or 100000000000000.
- Keep each inequality as one complete math expression on one logical line. Never put one number, operator, or variable per separate line, and never repeat the same bound in both fragmented and complete forms.
- Never leave TeX commands such as \\le, \\cdot, or \\sum outside math delimiters.
- Keep the statement concise but complete: a focused narrative followed by complete input, output, constraints, and sample sections. The sample count follows the active sample contract below.

The problem statement must include:
- A clear narrative or scenario
- Input format specification
- Output format specification
- Constraints section
- A sample section with verified input/output and explanations, unless the structured-sample contract is active (then use its single marker)
- Exactly one standalone time-limit line and one standalone memory-limit line. Use the
  requested values only in those lines; never repeat a different limit in the story,
  constraints, or sample explanation. The workflow may calibrate these values after
  the standard solution runs, so do not describe them as algorithmic facts elsewhere.

Guidelines:
- The problem must be original and not a copy of any well-known problem
- The statement should be unambiguous and precisely defined
- Constraints must be consistent with the target difficulty and time/memory limits
- Every declared field count must equal the fields that follow it. For example, u,v,c are three integers, never four.
- Tags should accurately reflect the algorithms/techniques needed to solve the problem
- one_line_hint is internal authoring metadata, not part of the contestant-visible
  statement or exported contest package. Keep it to one conceptual clue (at most
  about 100 characters) for downstream solution/editorial generation, and do not
  enumerate the full implementation pipeline, state formula, or every data
  structure needed by the solution.
- The one_line_hint is rendered only in the authoring UI: use plain Unicode/ASCII only.
  Do not use Markdown, dollar signs, backslashes, TeX commands, braces, code fences,
  or line breaks in this field.
- A good hint names one observation (for example, "寻找维护不变量") rather than
  listing several named data structures or sequential implementation steps.

PUBLIC STATEMENT CONTRACT (run this checklist silently before returning):
- Use exactly one standalone heading for each canonical section, in this order: Input Format, Output Format, Constraints (or the corresponding Chinese labels). Never repeat a section at another heading level, and do not turn a prose label such as "输出格式：" into a second heading.
- Never put TeX commands inside inline code spans. Code spans must use plain ASCII/Unicode such as w_1, w_2, ..., w_n; if an expression needs \\ldots or \\cdot, put the complete expression inside $...$.
- Treat the statement as the executable input/output contract shared by the main solution, brute oracle, generator, and tests. Every symbol, field count, index range, bound, edge case, complexity claim, and sample must agree across those artifacts.
- Do not emit placeholders, TODOs, escaped-newline leakage, undefined variables, contradictory limits, or a sample that has not been verified against the stated semantics. If a sample cannot be verified, omit it.
- State the observation point for every operation (including whether updates take
  effect before the next query), the endpoint convention for any interval, and the
  exact modulus/overflow convention. These must be unambiguous to an independent
  generator, brute oracle, and solution writer.
- Before returning, check that math delimiters are balanced and that the statement contains no analysis, self-correction, or hidden reasoning.

QUALITY PREFLIGHT (perform silently before emitting JSON):
- Fix one precise objective and formalize every input, output, variable, and tie rule.
- Derive the intended algorithm and a natural brute-force baseline; ensure the limits make the intended insight necessary.
- Attack the design with minimum, maximum, duplicate, tie, disconnected, overflow, and degenerate cases as applicable.
- Keep the story as a thin wrapper around the formal task; do not hide required semantics in narrative prose.
- Recheck every sample, constraint, tag, and difficulty claim for consistency. Emit only the final statement JSON.

SANDBOX INPUT CONTRACT:
- Every complete input file allowed by the constraints must serialize to at most 8 MiB, including multi-test input. Choose n, m, t, string lengths, and aggregate constraints accordingly.
- Do not declare a maximum case that inherently needs more than 8 MiB of text. For graph problems, account for every printed endpoint and weight; for arrays and strings, account for separators and line breaks.
CRITICAL OUTPUT QUALITY RULES:
- Your output must be FINAL and POLISHED. Do NOT include any thinking process, self-corrections, or reasoning traces in the statement or examples. Forbidden phrases include: "actually", "let me reconsider", "wait", "hmm", "let me recheck", "no that's wrong", "let me fix", "upon reflection", etc.
- VERIFY every sample input/output is correct BEFORE writing it. Do all verification internally, then output only the clean, final result.
- Each sample explanation must be concise and authoritative: state the approach, show the computation, give the answer. No trial-and-error narratives.
- If you cannot verify an example is correct, omit it rather than include an uncertain or self-correcting explanation.

多概念题专项要求（当指定标签 ≥ 2 时）：
合法的多概念组合范式必须属于以下之一，请在 difficulty_justification 中明确说明属于哪一类：
A. 主算法 + 辅助数据结构（如：二分答案 + 并查集判可行）
B. 预处理 + 在线查询（如：倍增 LCA + 树上差分）
C. 建模转化（如：贪心问题转最小割）
D. 分阶段求解（如：离线扫描线后再回答查询）

判定铁律：移除任意一个指定标签后，题目应"无法成立"或"严重退化"。
若做不到，说明组合是拼贴而非有机融合，需重新设计题面。

` + DifficultyRubric + contestProblemsetterSkillGuidance

const structuredSamplesStatementInstruction = `

STRUCTURED SAMPLE SINGLE-SOURCE CONTRACT (overrides the sample-writing rules above):
- Do not invent or write any sample input, sample output, or sample explanation.
- Put exactly this literal marker where the complete sample section belongs: <!-- ALGOFORGE_SAMPLES -->
- The marker must appear exactly once in the statement and nowhere else.
- Do not add a Samples heading around the marker. The verified sample section will replace the marker later.
- Keep the input format, output format, and constraints complete enough for a separate generator to create the samples.
- The outer workflow will insert the configured number of verified samples after differential validation; do not duplicate or describe that section yourself.
`

func statementSystemPromptForInput(useStructuredSamples bool) string {
	if !useStructuredSamples {
		return statementSystemPrompt
	}
	return statementSystemPrompt + structuredSamplesStatementInstruction
}

// buildStatementPrompt constructs the user prompt for statement generation
// from the provided parameters, including difficulty calibration context.
func buildStatementPrompt(params domain.ProblemGenParams, neighbors []NeighborInfo) string {
	return buildStatementPromptWithOptions(params, neighbors, false)
}

func buildStatementPromptWithOptions(params domain.ProblemGenParams, neighbors []NeighborInfo, useStructuredSamples bool) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("Generate a competitive programming problem with the following specifications:\n\n"))
	sb.WriteString(fmt.Sprintf("- Level: %s\n", params.Level))
	sb.WriteString(fmt.Sprintf("- Difficulty Rating: %d\n", params.Difficulty))
	sb.WriteString(fmt.Sprintf("- Time Limit: %d ms\n", params.TimeLimit))
	sb.WriteString(fmt.Sprintf("- Memory Limit: %d MB\n", params.MemoryLimit))
	sb.WriteString("These are provisional authoring inputs. Put the limits only in the two standalone resource lines of the statement; the workflow may replace them after benchmarking the standard solution. Do not repeat a different value in narrative prose.\n")

	if len(params.Tags) > 0 {
		sb.WriteString(fmt.Sprintf("- Required Tags/Topics: %s\n", strings.Join(params.Tags, ", ")))
	}
	sb.WriteString(knowledgePointCombinationPrompt(params, knowledgePointPromptStatement))

	if params.ContestStyle != "" {
		sb.WriteString(fmt.Sprintf("- Contest Style: %s\n", params.ContestStyle))
	}

	// Difficulty calibration context.
	bandDesc := DifficultyBandDescription(params.Difficulty)
	if bandDesc != "" {
		sb.WriteString(fmt.Sprintf("\n### Difficulty Calibration\nThis is a %d-rated problem. Expected difficulty band: %s\n", params.Difficulty, bandDesc))
	}

	// Per-tag difficulty context.
	if len(params.Tags) > 0 {
		sb.WriteString(BuildTagDifficultySection(params.Tags, params.TagRanges, params.Difficulty))
	}

	appendCrossStageGenerationInstructions(&sb, params, "problem statement")

	if len(neighbors) > 0 {
		sb.WriteString("\n### 库中已有的近似题目（必须避免雷同）\n")
		sb.WriteString("生成的题目标题不得与以下任一题目相同或近似；不得与以下任一题目在【题目背景/故事场景】【核心算法的状态定义或转移方程】【输入数据形态与约束规模】上同构。\n\n")
		sb.WriteString("近似题目清单：\n")
		for i, n := range neighbors {
			sb.WriteString(fmt.Sprintf("%d. 《%s》\n", i+1, n.Title))
			if n.OneLineHint != "" {
				sb.WriteString(fmt.Sprintf("   一句话：%s\n", n.OneLineHint))
			}
			if len(n.Tags) > 0 {
				sb.WriteString(fmt.Sprintf("   标签：[%s]\n", strings.Join(n.Tags, ", ")))
			}
		}
		sb.WriteString("\n要求：必须换用不同标题和不同故事背景，并且至少在【建模角度 / 主数据结构 / 输入维度】中的一项上与上述题目有显著区分。\n")
	}

	sb.WriteString(LocaleInstruction(params.Locale))

	if useStructuredSamples {
		sb.WriteString("\nThe statement MUST use the structured sample single-source contract from the system prompt. Include exactly one literal " +
			StatementSamplesPlaceholder + " marker and no invented sample values.\n")
	}

	sb.WriteString("\nThe one_line_hint field MUST be a single concise conceptual clue (at most about 100 characters) rendered as plain text. Name one observation only; do not reveal the complete algorithm chain, exact answer formula, or a list of implementation data structures. Prefer a clue about the key invariant and never enumerate sequential implementation steps. Do not use Markdown, dollar signs, backslashes, TeX commands, braces, code fences, or line breaks in the hint. The difficulty_justification field MUST contain seven concise points: (1) intended key observation; (2) expected solution time complexity; (3) natural brute-force baseline; (4) role of each specified tag (main / auxiliary / modeling); (5) why this combination fits the target difficulty; (6) why removing any single tag would make the problem trivially solvable or unsolvable; (7) likely wrong solutions and the hidden-test attack points needed to catch them. Do not use this field to introduce facts that are absent from the statement.\n")
	sb.WriteString("\nFinal authoring check: compare every number and symbol in the narrative, input/output sections, constraints, samples, and difficulty justification; resolve any mismatch before returning the JSON.\n")

	sb.WriteString("\nReturn only the JSON object. Keep it concise enough to finish without truncation.")

	return sb.String()
}

// statementJSON is the expected structure of the LLM's JSON response for
// statement generation.
type statementJSON struct {
	Title                   string   `json:"title"`
	Statement               string   `json:"statement"`
	Tags                    []string `json:"tags"`
	OneLineHint             string   `json:"one_line_hint"`
	DifficultyJustification string   `json:"difficulty_justification"`
}

// parseStatementResponse parses the LLM response text into a StatementResult.
// It attempts JSON parsing first, then falls back to extracting content from
// a fenced JSON block.
func parseStatementResponse(text string) (*StatementResult, error) {
	text = strings.TrimSpace(text)

	// Try direct JSON parse.
	var parsed statementJSON
	if err := json.Unmarshal([]byte(text), &parsed); err == nil {
		return statementJSONToResult(parsed), nil
	}
	if repaired := repairInvalidJSONStringEscapes(text); repaired != text {
		if err := json.Unmarshal([]byte(repaired), &parsed); err == nil {
			return statementJSONToResult(parsed), nil
		}
	}

	// Try extracting JSON from a fenced code block.
	jsonContent := extractJSONBlock(text)
	if jsonContent != "" {
		if err := json.Unmarshal([]byte(jsonContent), &parsed); err == nil {
			return statementJSONToResult(parsed), nil
		} else if repaired := repairInvalidJSONStringEscapes(jsonContent); repaired != jsonContent {
			err2 := json.Unmarshal([]byte(repaired), &parsed)
			if err2 == nil {
				return statementJSONToResult(parsed), nil
			}
			log.Warn().
				Err(err2).
				Msg("repaired fenced JSON block still failed to parse, trying brace matching")
		} else {
			log.Warn().
				Err(err).
				Msg("extracted JSON block via fence but failed to parse, trying brace matching")
		}
	}

	// Fallback: find the outermost JSON object by matching braces.
	jsonContent = extractOutermostJSON(text)
	if jsonContent != "" {
		if err := json.Unmarshal([]byte(jsonContent), &parsed); err == nil {
			return statementJSONToResult(parsed), nil
		} else if repaired := repairInvalidJSONStringEscapes(jsonContent); repaired != jsonContent {
			err2 := json.Unmarshal([]byte(repaired), &parsed)
			if err2 == nil {
				return statementJSONToResult(parsed), nil
			}
			log.Warn().
				Err(err2).
				Str("json_preview", truncate(repaired, 300)).
				Msg("repaired brace-matched JSON still failed to parse")
		} else {
			log.Warn().
				Err(err).
				Str("json_preview", truncate(jsonContent, 300)).
				Msg("brace-matched JSON also failed to parse")
		}
	}

	return nil, fmt.Errorf("failed to parse LLM response as valid statement JSON")
}

func statementJSONToResult(parsed statementJSON) *StatementResult {
	return &StatementResult{
		Title:                   parsed.Title,
		Statement:               normalizeGeneratedStatementMarkdown(parsed.Statement),
		Tags:                    parsed.Tags,
		OneLineHint:             NormalizeOneLineHintV1(parsed.OneLineHint),
		DifficultyJustification: parsed.DifficultyJustification,
	}
}

func repairInvalidJSONStringEscapes(text string) string {
	var b strings.Builder
	b.Grow(len(text))

	inString := false
	changed := false
	for i := 0; i < len(text); i++ {
		c := text[i]
		if !inString {
			b.WriteByte(c)
			if c == '"' {
				inString = true
			}
			continue
		}

		if c == '\\' {
			if i+1 >= len(text) {
				b.WriteString(`\\`)
				changed = true
				continue
			}
			next := text[i+1]
			if isValidJSONEscapeStart(next) {
				b.WriteByte(c)
				b.WriteByte(next)
				i++
			} else {
				b.WriteString(`\\`)
				changed = true
			}
			continue
		}

		b.WriteByte(c)
		if c == '"' {
			inString = false
		}
	}

	if !changed {
		return text
	}
	return b.String()
}

func isValidJSONEscapeStart(c byte) bool {
	switch c {
	case '"', '\\', '/', 'b', 'f', 'n', 'r', 't', 'u':
		return true
	default:
		return false
	}
}

func restoreJSONPrefill(text string) string {
	text = strings.TrimSpace(text)
	if text == "" || strings.HasPrefix(text, "{") || strings.HasPrefix(text, "[") {
		return text
	}
	if strings.HasPrefix(text, `"`) {
		return "{" + text
	}
	return text
}

// extractJSONBlock extracts JSON content from a markdown fenced code block.
// It first tries all ```json blocks, then falls back to trying all ``` blocks.
// For each candidate, it verifies the content starts with '{' to avoid
// extracting non-JSON code blocks (e.g. ```cpp blocks).
func extractJSONBlock(text string) string {
	// Priority 1: try all ```json blocks (most specific).
	if result := extractFencedBlocks(text, "```json"); result != "" {
		return result
	}
	// Priority 2: try all ``` blocks, but only those that look like JSON.
	if result := extractFencedBlocks(text, "```"); result != "" {
		return result
	}
	return ""
}

// extractFencedBlocks finds all fenced code blocks starting with the given
// marker and returns the content of the first one that looks like JSON
// (starts with '{' or '['). This handles cases where the LLM outputs
// multiple code blocks (e.g. an explanation block before the JSON block).
func extractFencedBlocks(text, marker string) string {
	remaining := text
	for {
		// Find the next occurrence of the marker followed by a newline.
		idx := strings.Index(remaining, marker)
		if idx == -1 {
			break
		}

		// Skip past the marker and any language annotation on the same line.
		afterMarker := remaining[idx+len(marker):]
		nlIdx := strings.IndexByte(afterMarker, '\n')
		if nlIdx == -1 {
			break
		}
		content := afterMarker[nlIdx+1:]

		// Find the closing fence on its own line.
		endIdx := strings.Index(content, "\n```")
		var block string
		if endIdx != -1 {
			block = strings.TrimSpace(content[:endIdx])
		} else if strings.HasSuffix(strings.TrimSpace(content), "```") {
			trimmed := strings.TrimSpace(content)
			block = strings.TrimSpace(trimmed[:len(trimmed)-3])
		}

		// Check if this block looks like JSON.
		if block != "" && len(block) > 0 && (block[0] == '{' || block[0] == '[') {
			return block
		}

		// Move past this marker to find the next one.
		remaining = remaining[idx+len(marker):]
	}
	return ""
}

// extractOutermostJSON attempts to find a valid JSON object in the text by
// trying progressively deeper '{' positions paired with the last '}'.
// This handles cases where the LLM wraps the JSON with extra text that may
// contain stray braces (e.g. "{medium-hard}").
func extractOutermostJSON(text string) string {
	last := strings.LastIndex(text, "}")
	if last == -1 {
		return ""
	}

	// Try each '{' position from left to right. The first one that produces
	// valid JSON is returned. This gracefully handles stray braces in
	// explanatory text before the actual JSON.
	searchFrom := 0
	for {
		first := strings.Index(text[searchFrom:], "{")
		if first == -1 || searchFrom+first >= last {
			break
		}
		candidate := strings.TrimSpace(text[searchFrom+first : last+1])
		if json.Valid([]byte(candidate)) {
			return candidate
		}
		searchFrom += first + 1
	}

	// Fallback: return the first-to-last brace range even if not valid JSON,
	// so callers can attempt their own parsing.
	first := strings.Index(text, "{")
	if first != -1 && first < last {
		return strings.TrimSpace(text[first : last+1])
	}
	return ""
}
