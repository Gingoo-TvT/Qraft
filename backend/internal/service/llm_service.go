package service

import (
	"context"
	"fmt"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/llm"
	"github.com/Gingoo-TvT/Qraft/backend/internal/llm/prompts"
	"github.com/rs/zerolog/log"
)

// ---------------------------------------------------------------------------
// LLMService
// ---------------------------------------------------------------------------

// LLMService wraps the Anthropic LLM client and prompt template registry to
// provide high-level problem-generation operations. Each method renders the
// appropriate prompt template, sends it to Claude, and returns the parsed
// result.
type LLMService struct {
	client   *llm.Client
	registry *prompts.Registry
}

// NewLLMService creates a new LLMService with the given LLM client and prompt
// template registry.
func NewLLMService(client *llm.Client, registry *prompts.Registry) *LLMService {
	return &LLMService{
		client:   client,
		registry: registry,
	}
}

// ---------------------------------------------------------------------------
// GenerateStatement
// ---------------------------------------------------------------------------

// StatementParams contains the inputs required to generate a problem statement.
type StatementParams struct {
	Level        domain.ProblemLevel `json:"level"`
	Difficulty   int                 `json:"difficulty"`
	Tags         []string            `json:"tags"`
	ContestStyle string              `json:"contest_style,omitempty"`
	CustomPrompt string              `json:"custom_prompt,omitempty"`
}

// StatementOutput contains the generated problem statement and metadata.
type StatementOutput struct {
	Title       string `json:"title"`
	Statement   string `json:"statement"`
	OneLineHint string `json:"one_line_hint"`
}

// GenerateStatement generates a problem statement using the statement prompt
// template. It renders the template with the given parameters, sends it to
// Claude, and returns the raw response text.
func (s *LLMService) GenerateStatement(ctx context.Context, params StatementParams) (string, error) {
	tmpl, err := s.registry.Get(prompts.TemplateNameStatement)
	if err != nil {
		return "", fmt.Errorf("getting statement template: %w", err)
	}

	input := prompts.NewStatementInput(domain.ProblemGenParams{
		Level:          params.Level,
		Difficulty:     params.Difficulty,
		Tags:           append([]string(nil), params.Tags...),
		ContestStyle:   params.ContestStyle,
		CustomPrompt:   params.CustomPrompt,
		TimeLimit:      2000,
		MemoryLimit:    256,
		TestDataConfig: domain.DefaultTestDataConfig(),
		Languages:      []string{"cpp"},
	}, nil)
	req, err := tmpl.Render(input)
	if err != nil {
		return "", fmt.Errorf("rendering statement template: %w", err)
	}

	log.Debug().
		Str("level", string(params.Level)).
		Int("difficulty", params.Difficulty).
		Msg("generating problem statement via LLM")

	resp, err := s.client.CompleteWithRetry(ctx, req, 3)
	if err != nil {
		return "", fmt.Errorf("calling LLM for statement: %w", err)
	}

	text := resp.Text()

	log.Info().
		Int("input_tokens", resp.Usage.InputTokens).
		Int("output_tokens", resp.Usage.OutputTokens).
		Msg("statement generated")

	return text, nil
}

// ---------------------------------------------------------------------------
// GenerateSolution
// ---------------------------------------------------------------------------

// SolutionParams contains the inputs required to generate solutions.
type SolutionParams struct {
	Level      domain.ProblemLevel `json:"level"`
	Difficulty int                 `json:"difficulty"`
	Languages  []string            `json:"languages"`
	Tags       []string            `json:"tags,omitempty"`
}

// GenerateSolution generates main and brute-force solutions for a problem.
// It sends the statement along with solution parameters to Claude and returns
// the raw response text containing both solutions.
func (s *LLMService) GenerateSolution(ctx context.Context, statement string, params SolutionParams) (string, error) {
	tmpl, err := s.registry.Get(prompts.TemplateNameSolution)
	if err != nil {
		return "", fmt.Errorf("getting solution template: %w", err)
	}

	language := "C++17"
	if len(params.Languages) > 0 && params.Languages[0] != "" {
		language = params.Languages[0]
	}
	req, err := tmpl.Render(prompts.NewSolutionInput(
		"",
		params.Difficulty,
		params.Tags,
		2000,
		256,
		statement,
		language,
		"",
	))
	if err != nil {
		return "", fmt.Errorf("rendering solution template: %w", err)
	}

	log.Debug().
		Strs("languages", params.Languages).
		Msg("generating solutions via LLM")

	resp, err := s.client.CompleteWithRetry(ctx, req, 3)
	if err != nil {
		return "", fmt.Errorf("calling LLM for solutions: %w", err)
	}

	text := resp.Text()

	log.Info().
		Int("input_tokens", resp.Usage.InputTokens).
		Int("output_tokens", resp.Usage.OutputTokens).
		Msg("solutions generated")

	return text, nil
}

// ---------------------------------------------------------------------------
// GenerateTestData
// ---------------------------------------------------------------------------

// TestDataParams contains the inputs required to generate test data.
type TestDataParams struct {
	Config domain.TestDataConfig `json:"config"`
}

// GenerateTestData generates test data for a problem. It sends the statement,
// solution code, and test configuration to Claude and returns the raw response
// text containing the generated test data.
func (s *LLMService) GenerateTestData(ctx context.Context, statement string, solution string, config domain.TestDataConfig) (string, error) {
	tmpl, err := s.registry.Get(prompts.TemplateNameTestData)
	if err != nil {
		return "", fmt.Errorf("getting testdata template: %w", err)
	}

	req, err := tmpl.Render(prompts.TestDataInput{
		Statement:       statement,
		TimeLimit:       2000,
		MemoryLimit:     256,
		Config:          config,
		PreferGenerator: true,
	})
	if err != nil {
		return "", fmt.Errorf("rendering testdata template: %w", err)
	}

	log.Debug().
		Int("num_test_cases", config.NumTestCases).
		Int("min_test_cases", config.MinTestCases).
		Int("max_test_cases", config.MaxTestCases).
		Bool("adaptive_test_cases", config.IsAdaptive()).
		Msg("generating test data via LLM")

	resp, err := s.client.CompleteWithRetry(ctx, req, 3)
	if err != nil {
		return "", fmt.Errorf("calling LLM for test data: %w", err)
	}

	text := resp.Text()

	log.Info().
		Int("input_tokens", resp.Usage.InputTokens).
		Int("output_tokens", resp.Usage.OutputTokens).
		Msg("test data generated")

	return text, nil
}

// ---------------------------------------------------------------------------
// ReviewProblem
// ---------------------------------------------------------------------------

// ReviewProblem performs an LLM-based quality review of a generated problem.
// It sends the statement, solution, and test data to the configured provider
// and returns the raw review response text. The legacy service path uses the
// same visibility-safe review template as the workflow activity.
func (s *LLMService) ReviewProblem(ctx context.Context, statement string, solution string, testdata string) (string, error) {
	tmpl, err := s.registry.Get(prompts.TemplateNameReview)
	if err != nil {
		return "", fmt.Errorf("getting review template: %w", err)
	}

	req, err := tmpl.Render(prompts.ReviewInput{
		Level:                "algorithm",
		TimeLimit:            2000,
		MemoryLimit:          256,
		Statement:            statement,
		MainSolutionCode:     solution,
		MainSolutionLanguage: "C++17",
		TestDataSummary:      testdata,
	})
	if err != nil {
		return "", fmt.Errorf("rendering review template: %w", err)
	}

	log.Debug().Msg("reviewing problem via LLM")

	resp, err := s.client.CompleteWithRetry(ctx, req, 3)
	if err != nil {
		return "", fmt.Errorf("calling LLM for review: %w", err)
	}

	text := resp.Text()

	log.Info().
		Int("input_tokens", resp.Usage.InputTokens).
		Int("output_tokens", resp.Usage.OutputTokens).
		Msg("problem reviewed")

	return text, nil
}

// ---------------------------------------------------------------------------
// GenerateEditorial
// ---------------------------------------------------------------------------

// GenerateEditorial generates a detailed editorial write-up for a problem.
// It sends the statement and model solution to Claude and returns the raw
// editorial text.
func (s *LLMService) GenerateEditorial(ctx context.Context, statement string, solution string) (string, error) {
	tmpl, err := s.registry.Get(prompts.TemplateNameEditorial)
	if err != nil {
		return "", fmt.Errorf("getting editorial template: %w", err)
	}

	req, err := tmpl.Render(prompts.EditorialInput{
		TimeLimit:        2000,
		MemoryLimit:      256,
		Statement:        statement,
		SolutionCode:     solution,
		SolutionLanguage: "C++17",
	})
	if err != nil {
		return "", fmt.Errorf("rendering editorial template: %w", err)
	}

	log.Debug().Msg("generating editorial via LLM")

	resp, err := s.client.CompleteWithRetry(ctx, req, 3)
	if err != nil {
		return "", fmt.Errorf("calling LLM for editorial: %w", err)
	}

	text := resp.Text()

	log.Info().
		Int("input_tokens", resp.Usage.InputTokens).
		Int("output_tokens", resp.Usage.OutputTokens).
		Msg("editorial generated")

	return text, nil
}
