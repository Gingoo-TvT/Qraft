package activities

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/llm"
	"go.temporal.io/sdk/activity"
)

// GenerateEditorialActivity calls the LLM to produce a detailed editorial
// (solution explanation) for the problem. The editorial is written in Markdown
// and covers the problem analysis, solution approach, complexity discussion,
// and annotated code walkthrough.
func (a *Activities) GenerateEditorialActivity(
	ctx context.Context,
	statement StatementResult,
	solution domain.Solution,
	params domain.ProblemGenParams,
) (*EditorialResult, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("generating editorial", "title", statement.Title)

	activity.RecordHeartbeat(ctx, "calling LLM to generate editorial")

	prompt := buildEditorialPrompt(statement, solution, params.Locale)

	req := &llm.Request{
		MaxTokens: 8192,
		System:    editorialSystemPrompt,
		Messages: []llm.Message{
			{
				Role:    "user",
				Content: prompt,
			},
		},
	}
	applyStatementLLMRuntime(req, params)

	stopHB := heartbeatWhile(ctx, "calling LLM to generate editorial", 15*time.Second)
	resp, sourceArtifact, err := a.completeLLMWithProvenance(ctx, "editorial", req, 2)
	stopHB()
	if err != nil {
		return nil, wrapRequiredProviderEffectError("llm call for editorial generation", err)
	}

	activity.RecordHeartbeat(ctx, "parsing editorial response")

	editorial, err := domain.ParseEditorialMarkdownV1(resp.Text())
	if err != nil {
		return nil, fmt.Errorf("parsing editorial response: %w", err)
	}

	if editorial == "" {
		return nil, fmt.Errorf("generated editorial is empty")
	}

	logger.Info("editorial generated", "length", len(editorial))

	return &EditorialResult{
		SourceArtifacts: []*ArtifactRef{sourceArtifact},
		Editorial:       editorial,
	}, nil
}

const editorialSystemPrompt = `You are an expert competitive programming editorial writer. Your task is to write a detailed, pedagogical editorial for a competitive programming problem.

You MUST output valid JSON with the following structure:
{
  "editorial": "Full editorial in Markdown format..."
}

The editorial should include:
1. **Problem Analysis**: Restate the problem in your own words, identify key observations
2. **Approach**: Describe the algorithm step by step
3. **Key Insight**: Highlight the crucial observation or technique that makes the solution work
4. **Complexity Analysis**: Time and space complexity with justification
5. **Code Walkthrough**: Annotated explanation of the solution code
6. **Alternative Approaches**: Briefly mention other valid approaches if applicable
7. **Common Pitfalls**: Mistakes that solvers commonly make

Use LaTeX notation for mathematical expressions (e.g., $O(n \log n)$).
Format the editorial as clean, readable Markdown.`

// buildEditorialPrompt constructs the user prompt for editorial generation.
func buildEditorialPrompt(statement StatementResult, solution domain.Solution, locale string) string {
	var sb strings.Builder

	sb.WriteString("Write a detailed editorial for the following competitive programming problem.\n\n")

	sb.WriteString("## Problem Statement\n")
	sb.WriteString(fmt.Sprintf("**Title:** %s\n", statement.Title))
	sb.WriteString(fmt.Sprintf("**Tags:** %s\n", strings.Join(statement.Tags, ", ")))
	sb.WriteString(fmt.Sprintf("**Difficulty Justification:** %s\n\n", statement.DifficultyJustification))
	sb.WriteString(statement.Statement)
	sb.WriteString("\n\n")

	sb.WriteString("## Model Solution\n")
	sb.WriteString(fmt.Sprintf("**Language:** %s\n", solution.Language))
	sb.WriteString("```" + solution.Language + "\n")
	sb.WriteString(solution.SourceCode)
	sb.WriteString("\n```\n\n")

	sb.WriteString(LocaleInstruction(locale))

	sb.WriteString("Please generate the editorial as a JSON object with an \"editorial\" field containing Markdown.")

	return sb.String()
}

// parseEditorialResponse parses the LLM response into the editorial text.
func parseEditorialResponse(text string) (string, error) {
	return domain.ParseEditorialMarkdownV1(text)
}
