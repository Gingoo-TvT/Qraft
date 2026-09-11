package activities

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/llm"
	remotesandbox "github.com/Gingoo-TvT/Qraft/backend/internal/sandbox"
	"github.com/Gingoo-TvT/Qraft/backend/internal/testdatagen"
	"github.com/rs/zerolog/log"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
)

const testDataMaxTokens = 12000

// Coverage labels are deliberately a small, stable vocabulary.  They make
// the generator's intent auditable without pretending that a label proves the
// semantic properties of the serialized input; deterministic parsing and
// sandbox/oracle gates remain authoritative.
const (
	coverageMinimum       = "minimum"
	coverageMaximum       = "maximum"
	coverageZero          = "zero"
	coverageNegative      = "negative"
	coverageSmall         = "small_exhaustive"
	coverageThreshold     = "threshold"
	coverageAdversarial   = "adversarial"
	coverageRandom        = "random"
	coverageMaximumRandom = "maximum_random"
	coverageMetamorphic   = "metamorphic"
	coverageCustom        = "custom"
)

var coverageAliases = map[string]string{
	"min": coverageMinimum, "minimum_boundary": coverageMinimum, "min_boundary": coverageMinimum,
	"最小": coverageMinimum, "最小值": coverageMinimum, "下界": coverageMinimum,
	"max": coverageMaximum, "maximum_boundary": coverageMaximum, "max_boundary": coverageMaximum,
	"最大": coverageMaximum, "最大值": coverageMaximum, "上界": coverageMaximum,
	"zero_value": coverageZero, "zeros": coverageZero, "零": coverageZero, "零值": coverageZero,
	"negative_value": coverageNegative, "负数": coverageNegative, "负值": coverageNegative,
	"small": coverageSmall, "tiny": coverageSmall, "small_case": coverageSmall,
	"small_exhaustive": coverageSmall, "brute": coverageSmall, "brute_check": coverageSmall,
	"小": coverageSmall, "小规模": coverageSmall, "暴力": coverageSmall, "对拍": coverageSmall,
	"edge": coverageThreshold, "boundary": coverageThreshold, "threshold": coverageThreshold,
	"临界": coverageThreshold, "边界": coverageThreshold, "阈值": coverageThreshold,
	"adversary": coverageAdversarial, "worst_case": coverageAdversarial,
	"对抗": coverageAdversarial, "退化": coverageAdversarial, "极端结构": coverageAdversarial,
	"rand": coverageRandom, "randomized": coverageRandom, "随机": coverageRandom,
	"max_random": coverageMaximumRandom, "max_scale_random": coverageMaximumRandom,
	"maximum_scale_random": coverageMaximumRandom, "maximum_random": coverageMaximumRandom,
	"最大规模随机": coverageMaximumRandom, "极限随机": coverageMaximumRandom,
	"最大规模": coverageMaximum, "极限": coverageMaximum,
	"metamorphic_test": coverageMetamorphic, "变形": coverageMetamorphic,
	"自定义": coverageCustom,
}

var knownCoverageLabels = map[string]struct{}{
	coverageMinimum: {}, coverageMaximum: {}, coverageZero: {}, coverageNegative: {},
	coverageSmall: {}, coverageThreshold: {}, coverageAdversarial: {}, coverageRandom: {},
	coverageMaximumRandom: {}, coverageMetamorphic: {}, coverageCustom: {},
}

func canonicalCoverageTag(raw string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	value = strings.ReplaceAll(value, "-", "_")
	value = strings.ReplaceAll(value, " ", "_")
	if canonical, ok := coverageAliases[value]; ok {
		return canonical
	}
	if _, ok := knownCoverageLabels[value]; ok {
		return value
	}
	return ""
}

func normalizeCoverageTags(tags []string) []string {
	if len(tags) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(tags))
	result := make([]string, 0, len(tags))
	for _, raw := range tags {
		canonical := canonicalCoverageTag(raw)
		if canonical == "" {
			continue
		}
		if _, exists := seen[canonical]; exists {
			continue
		}
		seen[canonical] = struct{}{}
		result = append(result, canonical)
	}
	sort.Strings(result)
	return result
}

func coverageTagsCanonical(tags []string) bool {
	if tags == nil {
		return true
	}
	for i, tag := range tags {
		if canonicalCoverageTag(tag) != tag || (i > 0 && tags[i-1] >= tag) {
			return false
		}
	}
	return true
}

func inferredCoverageTags(description string) []string {
	text := strings.ToLower(strings.TrimSpace(description))
	if text == "" {
		return nil
	}
	var inferred []string
	add := func(tag string) {
		inferred = append(inferred, tag)
	}
	if strings.Contains(text, "最大规模") || strings.Contains(text, "极限规模") || strings.Contains(text, "max-scale") || strings.Contains(text, "maximum-scale") || strings.Contains(text, "max scale") {
		if strings.Contains(text, "随机") || strings.Contains(text, "random") {
			add(coverageMaximumRandom)
		} else {
			add(coverageMaximum)
		}
	}
	if strings.Contains(text, "最小") || strings.Contains(text, "minimum") || strings.Contains(text, "min-bound") {
		add(coverageMinimum)
	}
	if strings.Contains(text, "零") || strings.Contains(text, "zero") {
		add(coverageZero)
	}
	if strings.Contains(text, "负") || strings.Contains(text, "negative") {
		add(coverageNegative)
	}
	if strings.Contains(text, "小规模") || strings.Contains(text, "tiny") || strings.Contains(text, "small") || strings.Contains(text, "暴力") || strings.Contains(text, "对拍") || strings.Contains(text, "brute") {
		add(coverageSmall)
	}
	if strings.Contains(text, "临界") || strings.Contains(text, "边界") || strings.Contains(text, "阈值") || strings.Contains(text, "threshold") || strings.Contains(text, "boundary") {
		add(coverageThreshold)
	}
	if strings.Contains(text, "对抗") || strings.Contains(text, "退化") || strings.Contains(text, "adversarial") || strings.Contains(text, "chain") || strings.Contains(text, "star") {
		add(coverageAdversarial)
	}
	if strings.Contains(text, "随机") || strings.Contains(text, "random") {
		add(coverageRandom)
	}
	if strings.Contains(text, "变形") || strings.Contains(text, "metamorphic") {
		add(coverageMetamorphic)
	}
	return normalizeCoverageTags(inferred)
}

func coverageTagsForCase(explicit []string, description string) []string {
	return normalizeCoverageTags(append(append([]string(nil), explicit...), inferredCoverageTags(description)...))
}

// GenerateTestDataActivity generates test cases for the problem using the LLM.
// It can produce test cases either with a reusable framework recipe or a legacy generator
// program or by directly generating input data. Custom cases from the
// TestDataConfig are included verbatim.
func (a *Activities) GenerateTestDataActivity(
	ctx context.Context,
	statement string,
	config domain.TestDataConfig,
	params domain.ProblemGenParams,
) (*TestDataResult, error) {
	if err := config.NormalizeForGeneration(); err != nil {
		return nil, temporal.NewNonRetryableApplicationError(
			"invalid test-data configuration: "+err.Error(),
			"InvalidParameterError",
			nil,
		)
	}
	logger := activity.GetLogger(ctx)
	logger.Info("generating test data",
		"num_test_cases", config.NumTestCases,
		"min_test_cases", config.MinTestCases,
		"max_test_cases", config.MaxTestCases,
		"adaptive_test_cases", config.IsAdaptive(),
		"num_groups", len(config.Groups),
		"num_custom", len(config.CustomCases),
	)

	activity.RecordHeartbeat(ctx, "calling LLM to generate test data")

	prompt := buildTestDataPromptWithParams(statement, config, params)

	temperature := 0.1
	req := &llm.Request{
		MaxTokens:   testDataMaxTokens,
		System:      testDataSystemPrompt,
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
	applyStatementLLMRuntime(req, params)

	stopHB := heartbeatWhile(ctx, "calling LLM to generate test data", 15*time.Second)
	resp, sourceArtifact, err := a.completeLLMWithProvenance(ctx, "testdata", req, 2)
	stopHB()
	if err != nil {
		return nil, wrapRequiredProviderEffectError("llm call for test data generation", err)
	}

	activity.RecordHeartbeat(ctx, "parsing LLM response for test data")

	responseText := restoreJSONPrefill(resp.Text())
	log.Info().
		Str("stop_reason", resp.StopReason).
		Int("output_tokens", resp.Usage.OutputTokens).
		Int("response_len", len(responseText)).
		Msg("LLM test data response received")

	if resp.StopReason == "max_tokens" {
		log.Error().
			Str("response_preview", truncate(responseText, 2000)).
			Msg("test data response was truncated")
		return nil, temporal.NewNonRetryableApplicationError(
			"test data response was truncated at max_tokens",
			"TruncatedLLMResponse",
			nil,
		)
	}

	result, err := parseTestDataResponse(responseText)
	if err != nil {
		log.Error().
			Str("response_preview", truncate(responseText, 2000)).
			Msg("failed to parse test data response")
		return nil, temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("parsing test data response: %v", err),
			"InvalidGeneratedTestData",
			err,
		)
	}
	result.SourceArtifacts = append(result.SourceArtifacts, sourceArtifact)

	if result.GeneratorCode != "" {
		result.GeneratorSHA256 = sha256Bytes([]byte(result.GeneratorCode))
	}

	// Prepend custom cases before generator execution. Explicit-count callers
	// retain the historical truncation behavior; adaptive callers never lose a
	// requested corner case to an implicit slice operation.
	result.TestCases = mergeCustomCasesForConfig(result.TestCases, config)
	hasEmpty := false
	for _, testCase := range result.TestCases {
		if testCase.Input == "" {
			hasEmpty = true
			break
		}
	}
	if hasEmpty && result.GeneratorCode != "" {
		recordSandboxHeartbeat(ctx, "submitting retained test data generators to remote sandbox")
		execution, err := a.executeGeneratorWithEvidence(ctx, result.GeneratorCode, result.TestCases)
		if err != nil {
			var generatedArtifactErr *invalidGeneratedTestArtifactError
			if errors.As(err, &generatedArtifactErr) {
				return nil, temporal.NewNonRetryableApplicationError(
					fmt.Sprintf("remote generator execution failed closed: %v", err),
					"InvalidGeneratedTestData",
					err,
				)
			}
			return nil, fmt.Errorf("remote generator execution failed closed: %w", err)
		}
		result.TestCases = execution.TestCases
		result.GeneratorBatches = execution.Batches
	}
	if err := validateGeneratedTestInputs(result.TestCases); err != nil {
		return nil, temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("invalid generated test inputs: %v", err),
			"InvalidGeneratedTestData",
			err,
		)
	}
	// Sample cardinality is a server-owned contract. Models often mark an
	// extra edge case as a sample even after being told the requested number;
	// deterministically keep the first requested marks and fill missing marks
	// in order so a harmless metadata slip does not invalidate an otherwise
	// correct suite. An impossible request (more samples than actual cases)
	// remains visible to the workflow gate below.
	normalizeSampleFlags(result.TestCases, config.NumSamples)
	if result.DeclaredTestCaseCount > 0 && result.DeclaredTestCaseCount != len(result.TestCases) {
		return nil, temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("LLM declared %d final test cases but produced %d", result.DeclaredTestCaseCount, len(result.TestCases)),
			"InvalidGeneratedTestData",
			nil,
		)
	}
	if err := config.ValidateGeneratedTestCaseCount(len(result.TestCases)); err != nil {
		return nil, temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("test-data count quality gate failed: %v", err),
			"QualityNotMet",
			err,
		)
	}
	if config.NumSamples > len(result.TestCases) {
		return nil, temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("test-data sample count %d exceeds generated test-case count %d", config.NumSamples, len(result.TestCases)),
			"QualityNotMet",
			nil,
		)
	}
	result.SelectedTestCaseCount = len(result.TestCases)
	if config.IsAdaptive() {
		if err := validateAdaptiveTestDataResult(result.TestCases, config); err != nil {
			return nil, temporal.NewNonRetryableApplicationError(
				fmt.Sprintf("adaptive test-data quality gate failed: %v", err),
				"QualityNotMet",
				err,
			)
		}
	}

	logger.Info("test data generated",
		"total_cases", len(result.TestCases),
		"adaptive_case_count", config.IsAdaptive(),
		"has_generator", result.GeneratorCode != "",
	)

	// Externalize large test inputs to durable content-addressed storage to
	// avoid both Temporal's payload limit and worker-local path coupling.
	if shouldExternalizeTestInputs(result.TestCases) {
		activity.RecordHeartbeat(ctx, "uploading large test inputs to artifact store")
		for i := range result.TestCases {
			if len(result.TestCases[i].Input) == 0 {
				continue
			}
			ref, err := a.putArtifact(ctx, []byte(result.TestCases[i].Input), "text/plain")
			if err != nil {
				return nil, fmt.Errorf("externalizing test input %d: %w", i, err)
			}
			result.TestCases[i].InputArtifact = ref
			result.TestCases[i].Input = ""
		}
		logger.Info("externalized test inputs to artifact store")
	}

	// Clear generator code from the result — it's no longer needed and
	// can be large enough to bloat the Temporal payload.
	result.GeneratorCode = ""
	result.PayloadVersion = ActivityPayloadVersion

	return result, nil
}

const temporalInlineTestDataThreshold = 64 * 1024

func shouldExternalizeTestInputs(testCases []TestCaseData) bool {
	totalBytes := 0
	for _, testCase := range testCases {
		inputBytes := len(testCase.Input)
		if inputBytes > temporalInlineTestDataThreshold {
			return true
		}
		totalBytes += inputBytes
	}
	return totalBytes > temporalInlineTestDataThreshold
}

const testDataSystemPrompt = `You are an expert competitive programming test data generator.

CRITICAL RULES:
1. You MUST generate a reusable C++ generator recipe for producing test inputs.
   Prefer generator_recipe with the framework below; legacy generator_code is accepted.
   Raw inline data is ONLY acceptable for sample cases (is_sample=true) and
   tiny edge-case inputs where the full input fits in a few lines.
2. The server harness (or a legacy generator) reads 3 signed 64-bit integers from standard input:
   test_index group_id seed
   - test_index: 0-based index of this test case
   - group_id: which group this case belongs to
   - seed: random seed for reproducibility
3. It prints the test input to stdout, respecting all constraint bounds for the group.
4. Include deterministic edge cases at specific test indices (e.g. index 0 = min case,
   index 1 = max case, index 2 = chain/star/adversarial structure, rest = random).
5. The user prompt supplies a final test-case count contract. In adaptive mode,
   choose the smallest sufficient count in the inclusive configured range (the
   product default is 10–20) after enumerating distinct corner cases. Do not
   pad with duplicate or near-duplicate data, and include custom cases in the
   final count. In fixed-count mode, obey the requested count.
6. Produce exactly one maximum-scale case when the constraints make it
   meaningful, preferably randomized or pressure-oriented. Do not repeat
   maximum-scale data at later indices: structural diversity is more valuable
   than repeated volume.
7. Apart from that one maximum-scale case, keep each generated input at or below
   1 MiB. Across every case in this request, generated input text must remain
   at or below 32 MiB in total.
8. For every invocation, read exactly one (test_index, group_id, seed) triple and
   print one complete, parseable problem instance. Never return early, print an
   empty file, wait for another line, or loop until EOF.
9. Keep all random loops bounded by the declared constraints. Construct trees,
   graphs, and strings with direct size bounds so generation cannot hang or hit
   the output limit. Do not print debug text to stdout.
10. The generator is compiled as C++20 in the AlgoForge sandbox. Standard C++
   headers and Boost headers (including boost/multiprecision/cpp_int.hpp) are
   available; do not depend on any other third-party library or local file.

Output ONLY valid JSON:
{
  "test_case_count": 12,
  "test_cases": [
    {"input": "actual data for samples", "group_id": 0, "is_sample": true, "description": "...", "coverage": ["small_exhaustive"]},
    {"input": "", "group_id": 0, "is_sample": false, "description": "maximum-scale randomized stress", "coverage": ["maximum_random", "random"]}
  ],
  "generator_recipe": {"version":"algoforge.testdata.v1","code":"void generate(long long i, long long g, af::Random& rng, std::ostream& out) { out << rng.integer(1,100); }"}
}

Output rules:
- Return exactly one compact JSON object and nothing else.
- Do not wrap the JSON in Markdown fences.
- Do not include analysis, self-corrections, or prose outside the JSON object.
- The first output characters after the assistant prefill must be "test_case_count".
- Legacy v1.3.1 payloads may begin with "test_cases"; for those payloads the
  first output characters after the assistant prefill must be "test_cases".
- generator_recipe.code (or legacy generator_code) must be a JSON string, not a Markdown code block.
- Encode JSON string newlines exactly once as \n. After JSON decoding,
  generator_recipe.code or generator_code must contain real line breaks between C++ declarations and
  statements; never emit a literal backslash+n between source lines. Keep
  normal C++ escapes such as "\\n" inside string/character literals intact.
- Keep each description short and specific.
- test_case_count must equal the number of final test_cases after custom cases
  are included; in adaptive mode it must be within the configured range.

For non-sample cases: leave "input" as "" (empty string). The system will compile
and execute the generator to produce the actual test input.

GENERATOR ACCEPTANCE CHECKLIST:
- In fixed-count mode, the number and order of test_cases match the requested
  configuration. In adaptive mode, choose one smallest sufficient total in the
  configured 10–20 range (including custom cases), and make every descriptor map
  to one deterministic test_index branch.
- Every case includes a short description and a coverage array. Use only these
  labels where applicable: minimum, maximum, zero, negative, small_exhaustive,
  threshold, adversarial, random, maximum_random, metamorphic. A
  maximum_random case is mandatory in adaptive mode, as is a genuinely small
  small_exhaustive/brute-check case.
- Every branch emits all required fields, including n/m/t headers when the
  statement declares them, and respects both its group bounds and the global
  byte limits.
- A BruteCheck branch is small enough for the independent oracle; a maximum
  branch is reserved for the main solution and is never sent to the oracle.
- The program compiles with the selected language standard and exits normally
  after one instance. Validate these invariants mentally before returning JSON.` + contestTestDataSkillGuidance + testdatagen.Prompt

// buildTestDataPrompt constructs the user prompt for test data generation.
func buildTestDataPrompt(statement string, config domain.TestDataConfig) string {
	return buildTestDataPromptWithParams(statement, config, domain.ProblemGenParams{})
}

func buildTestDataPromptWithParams(statement string, config domain.TestDataConfig, params domain.ProblemGenParams) string {
	// Prompt construction is also used by legacy/unit-test callers that pass a
	// zero-valued config. Normalize a local copy so the wording always exposes
	// the same bounded adaptive contract as the activity.
	_ = config.NormalizeForGeneration()
	minCases, maxCases, adaptive := config.EffectiveTestCaseRange()
	var sb strings.Builder

	sb.WriteString("Generate test data for the following competitive programming problem.\n\n")
	sb.WriteString("Problem Statement:\n")
	sb.WriteString(statement)
	sb.WriteString("\n\n")
	sb.WriteString(knowledgePointCombinationPrompt(params, knowledgePointPromptTestData))
	appendCrossStageGenerationInstructions(&sb, params, "test-data generator and test manifest")

	sb.WriteString("Requirements:\n")
	if adaptive {
		customCount := len(config.CustomCases)
		sb.WriteString(fmt.Sprintf("- Case-count mode: adaptive; choose the smallest sufficient total in [%d,%d] cases (custom cases included).\n", minCases, maxCases))
		sb.WriteString(fmt.Sprintf("- Generate only the additional cases needed after the %d custom cases are prepended; the combined final count must stay in [%d,%d].\n", customCount, minCases, maxCases))
		sb.WriteString("- Do not pad with duplicate or near-duplicate cases just to reach a round number. Each case must cover a distinct applicable corner, failure mode, or scale region.\n")
		sb.WriteString("- Always include one genuinely small exhaustive/brute-check case and one maximum-scale randomized/pressure case (tag the latter maximum_random). If the maximum bound is tiny, use the largest valid randomized case and explain the limitation.\n")
		sb.WriteString("- Coverage labels are a normalized set chosen from minimum, maximum, zero, negative, small_exhaustive, threshold, adversarial, random, maximum_random, and metamorphic.\n")
	} else {
		sb.WriteString(fmt.Sprintf("- Total test cases needed: %d\n", config.NumTestCases))
		sb.WriteString("- Case-count mode: fixed compatibility mode; return exactly this many cases. Do not invent adaptive-only coverage requirements for a legacy request.\n")
	}
	sb.WriteString(fmt.Sprintf("- Sample test cases: %d\n", config.NumSamples))

	// Describe groups.
	if len(config.Groups) > 0 {
		sb.WriteString("\nTest Groups (subtasks):\n")
		for _, g := range config.Groups {
			if adaptive {
				sb.WriteString(fmt.Sprintf("  Group %d: coverage/scoring hint (model assigns cases), score %d", g.GroupID, g.Score))
			} else {
				sb.WriteString(fmt.Sprintf("  Group %d: %d cases, score %d", g.GroupID, g.NumCases, g.Score))
			}
			if g.BruteCheck {
				sb.WriteString(fmt.Sprintf(" [BRUTE-CHECK: every case must be a genuinely small instance under the problem's semantic constraints; keep the serialized input at or below %d bytes and never put a maximum-scale case in this group]", MaxReferenceDifferentialInputBytes))
			}
			if g.Description != "" {
				sb.WriteString(fmt.Sprintf(" (%s)", g.Description))
			}
			if len(g.Constraints) > 0 {
				constraintParts := make([]string, 0, len(g.Constraints))
				for name, cr := range g.Constraints {
					constraintParts = append(constraintParts, fmt.Sprintf("%s in [%v, %v]", name, cr.Min, cr.Max))
				}
				sb.WriteString(fmt.Sprintf(" [constraints: %s]", strings.Join(constraintParts, ", ")))
			}
			sb.WriteString("\n")
		}
	}

	// Describe boundary configuration.
	sb.WriteString("\nBoundary/Edge Cases:\n")
	if config.BoundaryConfig.IncludeMinCase {
		sb.WriteString("- Include a test case with minimum constraint values\n")
	}
	if config.BoundaryConfig.IncludeMaxCase {
		sb.WriteString("- Include a test case with maximum constraint values\n")
	}
	if config.BoundaryConfig.IncludeZero {
		sb.WriteString("- Include a test case with zero-valued inputs where applicable\n")
	}

	// Note custom cases that will be prepended.
	if len(config.CustomCases) > 0 {
		if adaptive {
			sb.WriteString(fmt.Sprintf("\nNote: %d custom test cases will be prepended separately and count toward the adaptive total. The final suite must contain %d–%d cases.\n", len(config.CustomCases), minCases, maxCases))
		} else {
			sb.WriteString(fmt.Sprintf("\nNote: %d custom test cases will be prepended separately. Generate %d additional cases.\n",
				len(config.CustomCases),
				config.NumTestCases-len(config.CustomCases)))
		}
	}

	sb.WriteString("\nCONSTRAINT CONTEXT (from the problem statement):\n")
	sb.WriteString("The generator MUST produce data up to the maximum constraint values.\n")
	sb.WriteString("For large constraints (n >= 1000), you MUST use the generator_code approach.\n")
	sb.WriteString("Do NOT attempt to output large test data as inline text.\n")
	sb.WriteString("Keep every generated test input at or below 8 MiB.\n")
	sb.WriteString("Framework cases default to 1 MiB each; set output_limit_bytes up to 8388608 only for a larger case. The server batches cases by their declared output budgets.\n")
	sb.WriteString("Keep the combined size of all generated test inputs at or below 32 MiB. " +
		"Use compact whitespace and vary structural adversaries instead of repeating the maximum size in every case.\n")
	sb.WriteString(fmt.Sprintf("For differential validation, only cases in an explicitly BruteCheck group are selected when such groups exist; otherwise only public samples are selected. Every selected case must be genuinely small under the problem's own semantics, not merely short serialized text (for example, do not use a 15-digit upper bound just because it fits in a few bytes). Keep selected inputs at most %d bytes and reserve maximum-scale cases for main-solution-only groups.\n", MaxReferenceDifferentialInputBytes))
	sb.WriteString("Follow the Contest Test Data Skill from the system prompt. Each generated case description must say which boundary, adversarial structure, scale region, or plausible wrong solution it targets, and each case must include normalized coverage labels in the JSON.\n")
	sb.WriteString("Every generator invocation receives exactly one test_index/group_id/seed triple and must emit exactly one complete instance before exiting. Do not wait for EOF, emit an empty output, use unbounded rejection sampling, or print diagnostics to stdout. Make the branch for each configured index explicit and deterministic; if a special construction is impossible under a group's bounds, fall back to a smaller valid construction rather than returning early.\n")
	sb.WriteString("Before returning, cross-check the statement's field order, field count, index base, optional test-count convention, and all numeric bounds against the generator code. The generated input is consumed by both the main solution and (only for selected small cases) the independent brute oracle.\n")

	sb.WriteString("\nPlease generate the test data as a JSON object.")

	return sb.String()
}

// testDataJSON is the expected JSON structure from the LLM for test data.
type testDataJSON struct {
	TestCaseCount int `json:"test_case_count,omitempty"`
	TestCases     []struct {
		Input            string   `json:"input"`
		GroupID          int      `json:"group_id"`
		IsSample         bool     `json:"is_sample"`
		Description      string   `json:"description"`
		Coverage         []string `json:"coverage,omitempty"`
		OutputLimitBytes int64    `json:"output_limit_bytes,omitempty"`
	} `json:"test_cases"`
	GeneratorCode   string              `json:"generator_code"`
	GeneratorRecipe *testdatagen.Recipe `json:"generator_recipe,omitempty"`
}

// parseTestDataResponse parses the LLM response into a TestDataResult.
func parseTestDataResponse(text string) (*TestDataResult, error) {
	text = strings.TrimSpace(text)

	var parsed testDataJSON

	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		jsonContent := extractJSONBlock(text)
		if jsonContent != "" {
			if err2 := json.Unmarshal([]byte(jsonContent), &parsed); err2 == nil {
				goto testdataParsed
			}
		}
		jsonContent = extractOutermostJSON(text)
		if jsonContent != "" {
			if err2 := json.Unmarshal([]byte(jsonContent), &parsed); err2 == nil {
				goto testdataParsed
			}
		}
		return nil, fmt.Errorf("failed to parse test data response as JSON")
	}
testdataParsed:
	if parsed.GeneratorRecipe != nil {
		if strings.TrimSpace(parsed.GeneratorCode) != "" {
			return nil, fmt.Errorf("use generator_recipe or generator_code, not both")
		}
		parsed.GeneratorRecipe.Code = normalizeGeneratedGeneratorCode(parsed.GeneratorRecipe.Code)
		generator, err := testdatagen.Build(*parsed.GeneratorRecipe)
		if err != nil {
			return nil, err
		}
		parsed.GeneratorCode = generator.Source
	} else {
		parsed.GeneratorCode = normalizeGeneratedGeneratorCode(parsed.GeneratorCode)
	}

	result := &TestDataResult{
		TestCases:             make([]TestCaseData, 0, len(parsed.TestCases)),
		DeclaredTestCaseCount: parsed.TestCaseCount,
		GeneratorCode:         parsed.GeneratorCode,
	}

	for testIndex, tc := range parsed.TestCases {
		outputLimit := tc.OutputLimitBytes
		if outputLimit == 0 && parsed.GeneratorRecipe != nil {
			outputLimit = testdatagen.DefaultCaseBytes
		}
		if outputLimit != 0 {
			if _, err := testdatagen.BatchSize(outputLimit); err != nil {
				return nil, err
			}
		}
		origin := TestCaseOriginLLMInline
		var generatorCaseIndex *int
		if tc.Input == "" {
			origin = TestCaseOriginGenerator
			originalIndex := testIndex
			generatorCaseIndex = &originalIndex
		}
		result.TestCases = append(result.TestCases, TestCaseData{
			Input:                     tc.Input,
			GroupID:                   tc.GroupID,
			IsSample:                  tc.IsSample,
			Description:               tc.Description,
			Coverage:                  coverageTagsForCase(tc.Coverage, tc.Description),
			Origin:                    origin,
			GeneratorCaseIndex:        generatorCaseIndex,
			GeneratorOutputLimitBytes: outputLimit,
		})
	}

	return result, nil
}

// convertCustomCases converts domain CustomTestCase entries into TestCaseData.
func convertCustomCases(customs []domain.CustomTestCase) []TestCaseData {
	cases := make([]TestCaseData, 0, len(customs))
	for index, c := range customs {
		description := strings.TrimSpace(c.Description)
		if description == "" {
			description = fmt.Sprintf("caller-supplied custom case %d", index+1)
		}
		cases = append(cases, TestCaseData{
			Input:       c.Input,
			GroupID:     0, // Custom cases typically go in group 0 (ungrouped).
			IsSample:    c.IsSample,
			Description: description,
			Coverage:    coverageTagsForCase([]string{coverageCustom}, description),
			Origin:      TestCaseOriginCustom,
		})
	}
	return cases
}

func mergeCustomCasesAndValidate(generated []TestCaseData, config domain.TestDataConfig) ([]TestCaseData, error) {
	if err := config.NormalizeForGeneration(); err != nil {
		return nil, fmt.Errorf("invalid test-data configuration: %w", err)
	}
	merged := mergeCustomCasesForConfig(generated, config)
	if err := validateGeneratedTestInputs(merged); err != nil {
		return nil, fmt.Errorf("validating merged custom and generated test inputs: %w", err)
	}
	if err := config.ValidateGeneratedTestCaseCount(len(merged)); err != nil {
		return nil, fmt.Errorf("validating test-data count: %w", err)
	}
	if config.IsAdaptive() {
		if err := validateAdaptiveTestDataResult(merged, config); err != nil {
			return nil, fmt.Errorf("validating adaptive test data: %w", err)
		}
	}
	return merged, nil
}

func mergeCustomCases(generated []TestCaseData, config domain.TestDataConfig) []TestCaseData {
	return mergeCustomCasesForConfig(generated, config)
}

func mergeCustomCasesForConfig(generated []TestCaseData, config domain.TestDataConfig) []TestCaseData {
	customCases := convertCustomCases(config.CustomCases)
	merged := append(customCases, generated...)
	// Adaptive mode must preserve the complete model coverage plan so the
	// count gate can reject over-generation; silently truncating would discard
	// a required corner case. Explicit legacy requests retain truncation.
	if config.NumTestCases > 0 && !config.AutoCaseCount && len(merged) > config.NumTestCases {
		merged = merged[:config.NumTestCases]
	}
	return merged
}

func normalizeSampleFlags(cases []TestCaseData, requested int) {
	if requested < 0 {
		return
	}
	remaining := requested
	for index := range cases {
		if cases[index].IsSample && remaining > 0 {
			remaining--
			continue
		}
		cases[index].IsSample = false
	}
	if remaining == 0 {
		return
	}
	for index := range cases {
		if cases[index].IsSample || remaining == 0 {
			continue
		}
		cases[index].IsSample = true
		remaining--
	}
}

func validateAdaptiveTestDataResult(cases []TestCaseData, config domain.TestDataConfig) error {
	minCases, maxCases := config.AdaptiveCaseCountRange()
	if len(cases) < minCases || len(cases) > maxCases {
		return fmt.Errorf("generated %d cases; adaptive range is [%d,%d] (including custom cases)", len(cases), minCases, maxCases)
	}

	hasSmall := false
	hasRandom := false
	hasMaximum := false
	hasMaximumRandom := false
	hasMinimum := false
	hasZero := false
	for index := range cases {
		if strings.TrimSpace(cases[index].Description) == "" {
			return fmt.Errorf("case %d has no coverage description", index+1)
		}
		cases[index].Coverage = coverageTagsForCase(cases[index].Coverage, cases[index].Description)
		for _, tag := range cases[index].Coverage {
			switch tag {
			case coverageSmall:
				hasSmall = true
			case coverageRandom:
				hasRandom = true
			case coverageMaximum:
				hasMaximum = true
			case coverageMaximumRandom:
				hasMaximumRandom = true
				hasRandom = true
			case coverageMinimum:
				hasMinimum = true
			case coverageZero:
				hasZero = true
			}
		}
	}
	if !hasSmall {
		return fmt.Errorf("no small_exhaustive/brute-check coverage case was declared")
	}
	if !hasRandom {
		return fmt.Errorf("no randomized coverage case was declared")
	}
	if !hasMaximumRandom {
		return fmt.Errorf("no maximum_random coverage case was declared")
	}
	if config.BoundaryConfig.IncludeMinCase && !hasMinimum {
		return fmt.Errorf("minimum-boundary coverage was requested but not declared")
	}
	if config.BoundaryConfig.IncludeMaxCase && !hasMaximum && !hasMaximumRandom {
		return fmt.Errorf("maximum-boundary coverage was requested but not declared")
	}
	if config.BoundaryConfig.IncludeZero && !hasZero {
		return fmt.Errorf("zero-value coverage was requested but not declared")
	}
	return nil
}

const (
	generatorTimeLimitMS = 3000
	generatorMemoryMB    = 256
	// Generated problem inputs have a different size profile from judged
	// solution output. The sandbox accepts at most 8 MiB per input and 32 MiB
	// across a judged batch, so enforce those limits at generation time too.
	generatorMaxOutputBytes      int64 = 8 << 20
	generatorMaxTotalOutputBytes       = 32 << 20
	// JSON escaping can expand a control-heavy stdout by six times. A single
	// 8 MiB case leaves headroom below the independent 64 MiB response ceiling.
	generatorMaxBatchCases = 1
)

var startGeneratorHeartbeat = heartbeatWhile

type generatorExecutionResult struct {
	TestCases []TestCaseData
	Batches   []GeneratorBatchAudit
}

type invalidGeneratedTestArtifactError struct {
	message string
}

func (e *invalidGeneratedTestArtifactError) Error() string {
	return e.message
}

func invalidGeneratedTestArtifactErrorf(format string, args ...interface{}) error {
	return &invalidGeneratedTestArtifactError{message: fmt.Sprintf(format, args...)}
}

// executeGenerator sends generated source to the independent sandbox. The
// worker never compiles or executes generator code on its own host.
func (a *Activities) executeGenerator(ctx context.Context, code string, cases []TestCaseData) ([]TestCaseData, error) {
	execution, err := a.executeGeneratorWithEvidence(ctx, code, cases)
	if err != nil {
		return nil, err
	}
	return execution.TestCases, nil
}

func (a *Activities) executeGeneratorWithEvidence(ctx context.Context, code string, cases []TestCaseData) (*generatorExecutionResult, error) {
	caseIndexes := make([]int, 0, len(cases))
	for i := range cases {
		if cases[i].Input != "" {
			continue
		}
		if cases[i].Origin != "" && cases[i].Origin != TestCaseOriginGenerator {
			return nil, invalidGeneratedTestArtifactErrorf("test case %d with origin %q has empty literal input", i+1, cases[i].Origin)
		}
		caseIndexes = append(caseIndexes, i)
	}
	if len(caseIndexes) == 0 {
		return &generatorExecutionResult{TestCases: cases}, nil
	}

	type batchPlan struct {
		start, end  int
		outputLimit int64
	}
	plans := make([]batchPlan, 0, len(caseIndexes))
	maxBatchCases := 0
	for start := 0; start < len(caseIndexes); {
		outputLimit := cases[caseIndexes[start]].GeneratorOutputLimitBytes
		if outputLimit == 0 {
			outputLimit = generatorMaxOutputBytes
		}
		size, err := testdatagen.BatchSize(outputLimit)
		if err != nil {
			return nil, err
		}
		end := min(start+size, len(caseIndexes))
		for j := start + 1; j < end; j++ {
			nextLimit := cases[caseIndexes[j]].GeneratorOutputLimitBytes
			if nextLimit == 0 {
				nextLimit = generatorMaxOutputBytes
			}
			if nextLimit != outputLimit {
				end = j
				break
			}
		}
		plans = append(plans, batchPlan{start, end, outputLimit})
		maxBatchCases = max(maxBatchCases, end-start)
		start = end
	}
	executor, err := a.remoteSandboxExecutor(a.sandboxBatchTimeout(maxBatchCases, generatorTimeLimitMS))
	if err != nil {
		return nil, err
	}
	limits := remotesandbox.NewRemoteLimits(generatorTimeLimitMS, generatorMemoryMB)
	limits.MaxProcesses = 16
	limits.OutputLimitBytes = generatorMaxOutputBytes
	limits.Seed = deterministicGeneratorSeed(code, -1, -1)

	// Write generated inputs into a copy so a later batch failure cannot leak a
	// partially filled result through the caller's backing slice.
	filledCases := append([]TestCaseData(nil), cases...)
	generatorBatches := make([]GeneratorBatchAudit, 0, len(plans))
	batchCount := len(plans)
	for _, plan := range plans {
		batchStart, batchEnd := plan.start, plan.end
		outputLimit := plan.outputLimit
		limits.OutputLimitBytes = outputLimit
		batchCaseIndexes := caseIndexes[batchStart:batchEnd]
		inputs := make([]string, len(batchCaseIndexes))
		for batchResultIndex, caseIndex := range batchCaseIndexes {
			generatorCaseIndex := caseIndex
			if cases[caseIndex].GeneratorCaseIndex != nil {
				generatorCaseIndex = *cases[caseIndex].GeneratorCaseIndex
			}
			generatorBatchIndex := len(generatorBatches)
			seed := deterministicGeneratorSeed(code, generatorCaseIndex, cases[caseIndex].GroupID)
			filledCases[caseIndex].Origin = TestCaseOriginGenerator
			filledCases[caseIndex].GeneratorSeed = seed
			filledCases[caseIndex].GeneratorCaseIndex = &generatorCaseIndex
			filledCases[caseIndex].GeneratorBatchIndex = &generatorBatchIndex
			inputs[batchResultIndex] = fmt.Sprintf("%d %d %d\n", generatorCaseIndex, cases[caseIndex].GroupID, seed)
		}

		batchNumber := len(generatorBatches) + 1
		stopHB := startGeneratorHeartbeat(ctx, fmt.Sprintf("executing generator batch %d/%d in remote sandbox", batchNumber, batchCount), 10*time.Second)
		result, err := executor.Execute(ctx, "cpp", code, inputs, limits)
		stopHB()
		if err != nil {
			return nil, fmt.Errorf("executing generator batch %d/%d in remote sandbox: %w", batchNumber, batchCount, err)
		}
		if result == nil {
			return nil, fmt.Errorf("generator sandbox returned no execution result")
		}
		if !result.Compile.Success {
			return nil, invalidGeneratedTestArtifactErrorf("generator batch %d/%d compilation failed: %s", batchNumber, batchCount, boundedDiagnostic(result.Compile.Stderr))
		}
		if len(result.Results) != len(batchCaseIndexes) {
			return nil, invalidGeneratedTestArtifactErrorf("generator batch %d/%d returned %d results for %d empty cases", batchNumber, batchCount, len(result.Results), len(batchCaseIndexes))
		}

		batchAudit := GeneratorBatchAudit{
			BatchIndex:  batchNumber - 1,
			TestIndexes: append([]int(nil), batchCaseIndexes...),
			Audit:       sandboxAuditMetadataFromRemote(result.Audit),
		}
		batchAudit.GeneratorCaseIndexes = make([]int, 0, len(batchCaseIndexes))
		for batchResultIndex, item := range result.Results {
			caseIndex := batchCaseIndexes[batchResultIndex]
			if item.Index != batchResultIndex {
				return nil, invalidGeneratedTestArtifactErrorf("generator batch %d/%d result %d has unexpected index %d", batchNumber, batchCount, batchResultIndex, item.Index)
			}
			if item.Verdict != remotesandbox.VerdictOK || item.ExitCode != 0 || item.Signal != "" {
				return nil, invalidGeneratedTestArtifactErrorf("generator case %d failed closed with verdict %s (exit=%d signal=%s): %s",
					caseIndex+1, item.Verdict, item.ExitCode, item.Signal, boundedDiagnostic(item.Stderr))
			}
			if item.Stdout == "" {
				return nil, invalidGeneratedTestArtifactErrorf("generator case %d produced empty input", caseIndex+1)
			}
			if int64(len(item.Stdout)) > outputLimit {
				return nil, invalidGeneratedTestArtifactErrorf("generator case %d exceeds output_limit_bytes", caseIndex+1)
			}
			filledCases[caseIndex].Input = item.Stdout
			batchAudit.GeneratorCaseIndexes = append(batchAudit.GeneratorCaseIndexes, *filledCases[caseIndex].GeneratorCaseIndex)
			recordSandboxHeartbeat(ctx, fmt.Sprintf("generated test %d/%d in remote sandbox", batchStart+batchResultIndex+1, len(caseIndexes)))
		}
		generatorBatches = append(generatorBatches, batchAudit)
	}
	return &generatorExecutionResult{TestCases: filledCases, Batches: generatorBatches}, nil
}

func sandboxAuditMetadataFromRemote(audit remotesandbox.RemoteAuditMetadata) SandboxAuditMetadata {
	return SandboxAuditMetadata{
		RunID: audit.RunID, ManifestDigest: audit.ManifestDigest,
		Seed: audit.Seed, LimitProfile: audit.LimitProfile,
		ImageDigest:             audit.ImageDigest,
		ToolchainManifestDigest: audit.ToolchainManifestDigest,
		SeccompPolicyDigest:     audit.SeccompPolicyDigest,
	}
}

func deterministicGeneratorSeed(code string, caseIndex, groupID int) int64 {
	hash := sha256.New()
	_, _ = hash.Write([]byte("algoforge-generator-seed-v1\x00"))
	_, _ = hash.Write([]byte(code))
	var fields [16]byte
	binary.BigEndian.PutUint64(fields[:8], uint64(int64(caseIndex)))
	binary.BigEndian.PutUint64(fields[8:], uint64(int64(groupID)))
	_, _ = hash.Write(fields[:])
	seed := int64(binary.BigEndian.Uint64(hash.Sum(nil)[:8]) & uint64(^uint64(0)>>1))
	if seed == 0 {
		return 1
	}
	return seed
}

func validateGeneratedTestInputs(cases []TestCaseData) error {
	totalBytes := 0
	for i, testCase := range cases {
		if testCase.Input == "" {
			return fmt.Errorf("generated test case %d has empty input without successful generator output", i+1)
		}
		if int64(len(testCase.Input)) > generatorMaxOutputBytes {
			return fmt.Errorf("generated test case %d exceeds the %d-byte sandbox input limit", i+1, generatorMaxOutputBytes)
		}
		totalBytes += len(testCase.Input)
		if totalBytes > generatorMaxTotalOutputBytes {
			return fmt.Errorf("generated test inputs exceed the %d-byte sandbox batch input limit", generatorMaxTotalOutputBytes)
		}
	}
	return nil
}
