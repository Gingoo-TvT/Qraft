package activities

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/llm"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
)

const solutionMaxTokens = 12000

// GenerateSolutionActivity calls the LLM to generate both a main (model)
// solution and a brute-force solution for the given problem statement. The
// main solution is optimized for the target time/memory constraints, while
// the brute-force solution prioritises correctness over efficiency.
func (a *Activities) GenerateSolutionActivity(
	ctx context.Context,
	statement string,
	params domain.ProblemGenParams,
) (*SolutionResult, error) {
	return a.generateSolutionPair(ctx, statement, params, nil)
}

// RepairSolutionActivity regenerates both implementations with the exact
// bounded diagnostics from the preceding failed differential attempt.
func (a *Activities) RepairSolutionActivity(
	ctx context.Context,
	in GenerateSolutionRepairInput,
) (*SolutionResult, error) {
	if err := in.Validate(); err != nil {
		return nil, temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("invalid solution repair input: %v", err),
			"InvalidParameterError",
			err,
		)
	}
	return a.generateSolutionPair(ctx, in.Statement, in.Params, &in.Feedback)
}

func (a *Activities) generateSolutionPair(
	ctx context.Context,
	statement string,
	params domain.ProblemGenParams,
	retryFeedback *SolutionRetryFeedbackV1,
) (*SolutionResult, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("generating solutions",
		"difficulty", params.Difficulty,
		"languages", params.Languages,
		"repair", retryFeedback != nil,
	)

	language := "cpp"
	if len(params.Languages) > 0 {
		language = params.Languages[0]
	}

	mainRepairSection, bruteRepairSection, err := a.buildSolutionRetryPromptSections(ctx, retryFeedback)
	if err != nil {
		return nil, temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("building solution repair context: %v", err),
			"InvalidParameterError",
			err,
		)
	}

	// Build both requests before starting either provider call.  The two
	// roles are independent: the brute oracle does not consume the main
	// response, and each has its own provider-effect key.  Keeping the calls in
	// one activity preserves the existing durable result contract while letting
	// the provider overlap the two long-running requests.
	mainPrompt := buildMainSolutionPromptWithRepair(statement, language, params, mainRepairSection)
	temperature := 0.1
	mainReq := &llm.Request{
		MaxTokens:   solutionMaxTokens,
		System:      solutionSystemPrompt,
		Temperature: &temperature,
		Messages: []llm.Message{
			{
				Role:    "user",
				Content: mainPrompt,
			},
			{
				Role:    "assistant",
				Content: "{",
			},
		},
	}
	applyStatementLLMRuntime(mainReq, params)
	brutePrompt := buildBruteSolutionPromptWithRepair(statement, language, params, bruteRepairSection)
	bruteReq := &llm.Request{
		MaxTokens:   solutionMaxTokens,
		System:      solutionSystemPrompt,
		Temperature: &temperature,
		Messages: []llm.Message{
			{
				Role:    "user",
				Content: brutePrompt,
			},
			{
				Role:    "assistant",
				Content: "{",
			},
		},
	}
	applyBruteSolutionLLMRuntime(bruteReq, params)

	type solutionCallResult struct {
		response *llm.Response
		artifact *ArtifactRef
		err      error
		role     string
		duration time.Duration
	}
	callResults := make([]solutionCallResult, 2)
	var calls sync.WaitGroup
	calls.Add(2)
	stopHB := heartbeatWhile(ctx, "generating main and brute-force solutions", 15*time.Second)
	defer stopHB()
	go func() {
		defer calls.Done()
		startedAt := time.Now()
		response, artifact, callErr := a.completeLLMWithProvenance(ctx, "solution_main", mainReq, 2)
		callResults[0] = solutionCallResult{response: response, artifact: artifact, err: callErr, role: "main", duration: time.Since(startedAt)}
	}()
	go func() {
		defer calls.Done()
		startedAt := time.Now()
		response, artifact, callErr := a.completeLLMWithProvenance(ctx, "solution_brute", bruteReq, 2)
		callResults[1] = solutionCallResult{response: response, artifact: artifact, err: callErr, role: "brute", duration: time.Since(startedAt)}
	}()
	calls.Wait()
	for _, call := range callResults {
		logger.Info("solution provider call completed",
			"role", call.role,
			"duration_ms", call.duration.Milliseconds(),
			"parallel", true,
			"error", call.err,
		)
	}

	if callResults[0].err != nil {
		return nil, wrapRequiredProviderEffectError("llm call for main solution", callResults[0].err)
	}
	if callResults[1].err != nil {
		return nil, wrapRequiredProviderEffectError("llm call for brute solution", callResults[1].err)
	}
	mainResp, mainSourceArtifact := callResults[0].response, callResults[0].artifact
	bruteResp, bruteSourceArtifact := callResults[1].response, callResults[1].artifact
	if mainResp == nil || bruteResp == nil {
		return nil, fmt.Errorf("solution provider returned an empty response")
	}

	mainResponseText := restoreJSONPrefill(mainResp.Text())
	log.Info().
		Str("stop_reason", mainResp.StopReason).
		Int("output_tokens", mainResp.Usage.OutputTokens).
		Int("response_len", len(mainResponseText)).
		Msg("LLM main solution response received")
	if mainResp.StopReason == "max_tokens" {
		log.Error().
			Str("response_preview", truncate(mainResponseText, 2000)).
			Msg("main solution response was truncated")
		return nil, temporal.NewNonRetryableApplicationError(
			"main solution response was truncated at max_tokens",
			"TruncatedLLMResponse",
			nil,
		)
	}
	mainSolution, err := parseSolutionResponse(mainResponseText, domain.SolutionTypeMain, language)
	if err != nil {
		log.Error().
			Str("response_preview", truncate(mainResponseText, 2000)).
			Msg("failed to parse main solution response")
		return nil, fmt.Errorf("parsing main solution: %w", err)
	}

	bruteResponseText := restoreJSONPrefill(bruteResp.Text())
	log.Info().
		Str("stop_reason", bruteResp.StopReason).
		Int("output_tokens", bruteResp.Usage.OutputTokens).
		Int("response_len", len(bruteResponseText)).
		Msg("LLM brute solution response received")
	if bruteResp.StopReason == "max_tokens" {
		log.Error().
			Str("response_preview", truncate(bruteResponseText, 2000)).
			Msg("brute solution response was truncated")
		return nil, temporal.NewNonRetryableApplicationError(
			"brute solution response was truncated at max_tokens",
			"TruncatedLLMResponse",
			nil,
		)
	}
	bruteSolution, err := parseSolutionResponse(bruteResponseText, domain.SolutionTypeBrute, language)
	if err != nil {
		log.Error().
			Str("response_preview", truncate(bruteResponseText, 2000)).
			Msg("failed to parse brute solution response")
		return nil, fmt.Errorf("parsing brute solution: %w", err)
	}

	logger.Info("solutions generated successfully",
		"main_lines", strings.Count(mainSolution.SourceCode, "\n"),
		"brute_lines", strings.Count(bruteSolution.SourceCode, "\n"),
	)

	return &SolutionResult{
		SourceArtifacts:    []*ArtifactRef{mainSourceArtifact, bruteSourceArtifact},
		MainSolution:       *mainSolution,
		BruteSolution:      *bruteSolution,
		OracleIndependence: assessOracleIndependence(mainSourceArtifact, bruteSourceArtifact),
	}, nil
}

func applyBruteSolutionLLMRuntime(request *llm.Request, params domain.ProblemGenParams) {
	applyVerificationLLMRuntime(request, params)
}

const solutionSystemPrompt = `You are an expert competitive programmer. Your task is to write correct, efficient solutions for competitive programming problems.

You MUST output valid JSON with the following structure:
{
  "source_code": "// Full source code here...",
  "language": "cpp",
  "complexity_time": "O(n log n)",
  "complexity_space": "O(n)",
  "explanation": "Brief explanation of the approach"
}

Output rules:
- Return exactly one compact JSON object and nothing else.
- Do not wrap the JSON in Markdown fences.
- Do not include analysis, self-corrections, or prose outside the JSON object.
- The first output characters after the assistant prefill must be "source_code".
- source_code must be a JSON string, not a Markdown code block.
- Escape all newlines inside JSON strings as \n. Do not put raw line breaks inside string values.
- Keep explanation under 300 characters and keep code comments minimal.

SILENT CORRECTNESS PREFLIGHT (do this internally; never emit the checklist):
1. Formalize the input/output contract and state the invariant that proves the algorithm.
2. Derive worst-case time and memory from actual loops, recursion, and container sizes; compare them with the limits.
3. Attack with minimum, maximum, duplicate, tie, disconnected, overflow, and degenerate cases as applicable.
4. For the brute role, choose an independently structured baseline that is easy to audit and can expose plausible main-solution mistakes.
5. Mentally compile the requested language and re-evaluate every sample before emitting the single JSON object.

Guidelines:
- The code must compile and run correctly
- Include all necessary headers/imports
- Use standard input/output (stdin/stdout)
- Follow competitive programming conventions (fast I/O, etc.)
- The code should handle all edge cases mentioned in the problem constraints
- Treat the statement as the sole input/output contract: read exactly the
  declared fields for one instance and print exactly the declared answer. Do
  not assume an unstated test-count prefix or read repeated instances until
  EOF. Preserve valid negative answers; never clamp an optimum to zero.
- Check integer width, recursion depth, and peak memory against the maximum
  constraints. If a chain can reach the stated limit, use an iterative
  traversal or an explicitly sized safe stack instead of unbounded recursion.

IMPORTANT — Simplicity principle:
- Before coding, identify the KEY INSIGHT or mathematical property that simplifies
  the problem. A shorter, cleaner solution based on a deep observation is always
  preferred over a longer solution that uses heavier machinery.
- If you can prove a structural property (e.g. "the optimal always uses edge X",
  "greedy ordering by Y is sufficient"), state it and build the solution on it.
- Do NOT reach for complex data structures (LCA, segment tree, etc.) when a simpler
  invariant makes them unnecessary.
- The explanation field MUST state the key insight first, then the algorithm.` + contestSolutionSkillGuidance

// buildMainSolutionPrompt creates the prompt for generating the optimal solution.
func buildMainSolutionPrompt(statement, language string, params domain.ProblemGenParams) string {
	return buildMainSolutionPromptWithRepair(statement, language, params, "")
}

func buildMainSolutionPromptWithRepair(statement, language string, params domain.ProblemGenParams, repairSection string) string {
	var sb strings.Builder
	sb.WriteString("Write an optimal (model) solution for the following competitive programming problem.\n\n")
	sb.WriteString(fmt.Sprintf("Language: %s\n", language))
	sb.WriteString(fmt.Sprintf("Time limit: %d ms\n", params.TimeLimit))
	sb.WriteString(fmt.Sprintf("Memory limit: %d MB\n", params.MemoryLimit))
	sb.WriteString(fmt.Sprintf("Target difficulty: %d\n", params.Difficulty))

	if len(params.Tags) > 0 {
		sb.WriteString(fmt.Sprintf("Tags: %s\n", strings.Join(params.Tags, ", ")))
	}
	sb.WriteString(knowledgePointCombinationPrompt(params, knowledgePointPromptSolution))

	bandDesc := DifficultyBandDescription(params.Difficulty)
	if bandDesc != "" {
		sb.WriteString(fmt.Sprintf("\nDifficulty band: %s\n", bandDesc))
		sb.WriteString("The solution approach complexity should match this difficulty level. ")
		sb.WriteString("A higher difficulty means the key algorithmic insight should be more sophisticated, ")
		sb.WriteString("not just longer code.\n")
	}
	appendCrossStageGenerationInstructions(&sb, params, "optimal solution and its implementation")

	sb.WriteString("\nIMPORTANT: Start by identifying the simplest correct algorithm. ")
	sb.WriteString("If a structural property allows a simpler approach than the general technique, ")
	sb.WriteString("prefer that. State the key insight explicitly in the explanation.\n")
	sb.WriteString("\nUse the Contest Solution Skill guidance from the system prompt. Explicitly cover overflow limits, recursion depth, and the edge cases most likely to break near-correct solutions. Before returning, cross-check every symbol, field count, bound, and output convention against the statement and the test-data plan.\n")
	sb.WriteString("\nSANDBOX INVOCATION CONTRACT: Each generated test case is executed in a separate process and contains exactly the input grammar declared by the statement. If the statement does not declare a test-count field, process exactly one instance and print exactly one answer. Never read repeated instances until EOF unless the input format explicitly requires that behavior.\n")
	sb.WriteString(repairSection)

	sb.WriteString("\nProblem Statement:\n")
	sb.WriteString(statement)
	sb.WriteString("\n\nThe solution must run within the given time and memory limits. Use the most efficient algorithm possible.")
	return sb.String()
}

// buildBruteSolutionPrompt creates the prompt for generating the brute-force solution.
func buildBruteSolutionPrompt(statement, language string, params domain.ProblemGenParams) string {
	return buildBruteSolutionPromptWithRepair(statement, language, params, "")
}

func buildBruteSolutionPromptWithRepair(statement, language string, params domain.ProblemGenParams, repairSection string) string {
	var sb strings.Builder
	sb.WriteString("Write a brute-force (naive) solution for the following competitive programming problem.\n\n")
	sb.WriteString(fmt.Sprintf("Language: %s\n", language))
	sb.WriteString(fmt.Sprintf("Time limit for every selected differential case: %d ms (the problem's actual limit)\n", params.TimeLimit))
	sb.WriteString(fmt.Sprintf("Reference memory limit: %d MB (fixed oracle budget; independent of the problem memory limit)\n", ReferenceSolutionMemoryLimitMB))
	sb.WriteString(fmt.Sprintf("Target difficulty: %d\n\n", params.Difficulty))
	appendCrossStageGenerationInstructions(&sb, params, "independent brute-force oracle")
	sb.WriteString("SANDBOX INVOCATION CONTRACT: Each generated test case is executed in a separate process and contains exactly the input grammar declared by the statement. If the statement does not declare a test-count field, process exactly one instance and print exactly one answer. Never read repeated instances until EOF unless the input format explicitly requires that behavior.\n")
	sb.WriteString("ORACLE CONTRACT: This program is a correctness oracle only. It is run on a bounded set of small, explicitly selected differential cases, never on the full maximum-scale suite. It must finish each selected case within the actual problem time limit and must return exactly one correct answer. Do not use this oracle to calibrate the main solution's time or memory. If the oracle itself gets TLE, MLE, RE, or CE, the oracle is defective and must be regenerated; that is not a main-solution verdict.\n")
	sb.WriteString("The oracle must still implement the complete declared input grammar for every small valid case, including negative values and lower/upper boundary cases when allowed. Keep loops and recursion bounded and account for recursion depth; a crash, hang, output overflow, or missing answer is an oracle defect and must be fixed before comparison.\n")
	sb.WriteString("Before returning, cross-check every symbol, field count, bound, and output convention against the statement and the test-data plan.\n")
	sb.WriteString(repairSection)
	sb.WriteString("Problem Statement:\n")
	sb.WriteString(statement)
	sb.WriteString("\n\nThis solution is used for stress testing against the optimal solution. ")
	sb.WriteString("Prioritise simplicity and correctness over efficiency. ")
	sb.WriteString("Use the most straightforward algorithm possible, even if it is O(n^2) or worse. ")
	sb.WriteString("Make it independent from the intended solution so it can expose bugs during stress testing.")
	return sb.String()
}

// solutionJSON is the expected JSON structure from the LLM for a solution.
type solutionJSON struct {
	SourceCode      string `json:"source_code"`
	Language        string `json:"language"`
	ComplexityTime  string `json:"complexity_time"`
	ComplexitySpace string `json:"complexity_space"`
	Explanation     string `json:"explanation"`
}

// parseSolutionResponse parses the LLM response text into a domain.Solution.
func parseSolutionResponse(text string, solType domain.SolutionType, defaultLang string) (*domain.Solution, error) {
	text = strings.TrimSpace(text)

	var parsed solutionJSON

	// Try direct JSON parse.
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		// Try extracting from a fenced code block.
		jsonContent := extractJSONBlock(text)
		if jsonContent != "" {
			if err2 := json.Unmarshal([]byte(jsonContent), &parsed); err2 == nil {
				goto parsed
			}
		}
		// Fallback to outermost brace matching.
		jsonContent = extractOutermostJSON(text)
		if jsonContent != "" {
			if err2 := json.Unmarshal([]byte(jsonContent), &parsed); err2 == nil {
				goto parsed
			}
		}
		return nil, fmt.Errorf("failed to parse solution response as JSON")
	}
parsed:

	if parsed.SourceCode == "" {
		return nil, temporal.NewNonRetryableApplicationError(
			"generated solution has empty source code",
			"InvalidParameterError",
			nil,
		)
	}

	language := parsed.Language
	if language == "" {
		language = defaultLang
	}

	return &domain.Solution{
		ID:           uuid.New(),
		SolutionType: solType,
		Language:     language,
		SourceCode:   parsed.SourceCode,
	}, nil
}
