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
)

const assessFeasibilitySystemPrompt = `You are an expert competitive programming problem quality assessor.

You will be given a problem statement and a list of test cases where two independently generated solutions (a "main" solution and a "brute-force" solution) produced different outputs.

Your task: determine whether the discrepancy is most likely caused by:
  (A) A FLAWED PROBLEM — the problem is ambiguous, contradictory, ill-defined, or genuinely unsolvable as stated. In this case feasible=false.
  (B) BUGGY SOLUTION CODE — the problem is well-formed, but one or both generated solutions contain implementation bugs. In this case feasible=true.

Rules:
- If the problem statement is clear and the expected answer can be unambiguously determined for each mismatched test case, return feasible=true.
- If the problem has contradictory constraints, undefined edge cases, or the two outputs could both be valid interpretations, return feasible=false.
- Be decisive. Pick the most likely cause.

Output ONLY valid JSON:
{"feasible": true, "reason": "concise one-sentence explanation"}
or
{"feasible": false, "reason": "concise one-sentence explanation"}`

// AssessProblemFeasibilityActivity evaluates whether a validation mismatch
// is caused by a flawed problem statement or by buggy solution code.
// Returns FeasibilityResult{Feasible: true} when the problem is well-formed
// (solutions should be regenerated), or {Feasible: false} when the problem
// itself needs to be replaced.
func (a *Activities) AssessProblemFeasibilityActivity(
	ctx context.Context,
	statement StatementResult,
	mismatches []Mismatch,
	params domain.ProblemGenParams,
) (*FeasibilityResult, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("assessing problem feasibility", "mismatches", len(mismatches))

	userPrompt := buildFeasibilityPrompt(statement, mismatches)

	req := &llm.Request{
		MaxTokens: 1024,
		System:    assessFeasibilitySystemPrompt,
		Messages: []llm.Message{
			{Role: "user", Content: userPrompt},
		},
	}
	applyVerificationLLMRuntime(req, params)

	activity.RecordHeartbeat(ctx, "calling LLM to assess problem feasibility")

	stopHB := heartbeatWhile(ctx, "assessing problem feasibility", 15*time.Second)
	resp, sourceArtifact, err := a.completeLLMWithProvenance(ctx, "feasibility", req, 2)
	stopHB()
	if err != nil {
		return nil, wrapRequiredProviderEffectError("llm call for feasibility assessment", err)
	}

	responseText := resp.Text()
	log.Info().
		Str("stop_reason", resp.StopReason).
		Int("output_tokens", resp.Usage.OutputTokens).
		Msg("LLM feasibility assessment received")

	result, err := parseFeasibilityResponse(responseText)
	if err != nil {
		return nil, fmt.Errorf("parsing feasibility response: %w", err)
	}
	result.SourceArtifacts = append(result.SourceArtifacts, sourceArtifact)

	logger.Info("feasibility assessment complete", "feasible", result.Feasible, "reason", result.Reason)
	return result, nil
}

// buildFeasibilityPrompt constructs the user message for the feasibility LLM call.
func buildFeasibilityPrompt(statement StatementResult, mismatches []Mismatch) string {
	var sb strings.Builder

	sb.WriteString("## Problem Statement\n\n")
	sb.WriteString(statement.Statement)
	sb.WriteString("\n\n## Validation Mismatches\n\n")
	sb.WriteString(fmt.Sprintf("The two solutions disagreed on %d test case(s):\n\n", len(mismatches)))

	for i, m := range mismatches {
		if i >= 5 {
			sb.WriteString(fmt.Sprintf("... and %d more mismatches.\n", len(mismatches)-5))
			break
		}
		sb.WriteString(fmt.Sprintf("**Test case %d**\n", m.TestIndex+1))
		sb.WriteString(fmt.Sprintf("- Main solution output:        %q\n", truncate(m.MainOutput, 200)))
		sb.WriteString(fmt.Sprintf("- Brute-force solution output: %q\n\n", truncate(m.BruteOutput, 200)))
	}

	sb.WriteString("Is this mismatch due to a flawed problem, or due to buggy solution code?")
	return sb.String()
}

// parseFeasibilityResponse parses the LLM JSON response for feasibility assessment.
func parseFeasibilityResponse(text string) (*FeasibilityResult, error) {
	text = strings.TrimSpace(text)

	var result FeasibilityResult

	if err := json.Unmarshal([]byte(text), &result); err == nil {
		return &result, nil
	}

	jsonContent := extractJSONBlock(text)
	if jsonContent == "" {
		jsonContent = extractOutermostJSON(text)
	}
	if jsonContent != "" {
		if err := json.Unmarshal([]byte(jsonContent), &result); err == nil {
			return &result, nil
		}
	}

	return nil, fmt.Errorf("failed to parse feasibility response as JSON")
}
