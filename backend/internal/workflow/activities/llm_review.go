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
)

// LLMReviewActivity sends the generated problem, solutions, and test cases
// to the LLM for a quality review. The LLM evaluates the problem across
// multiple dimensions: clarity, correctness, test coverage, difficulty
// calibration, and tag accuracy. It returns a verdict with confidence score,
// a list of issues found, an estimated difficulty rating, and optional
// suggestions for improvement.
func (a *Activities) LLMReviewActivity(
	ctx context.Context,
	in LLMReviewInput,
) (*ReviewResult, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("starting LLM review",
		"title", in.Statement.Title,
		"test_count", len(in.TestCases),
		"target_difficulty", in.Params.Difficulty,
	)

	activity.RecordHeartbeat(ctx, "calling LLM for problem review")

	prompt := buildReviewPromptWithResourceCalibration(
		in.Statement,
		in.Solutions,
		in.TestCases,
		in.Params,
		in.Neighbors,
		in.ResourceCalibration,
	)

	req := &llm.Request{
		MaxTokens: 4096,
		System:    reviewSystemPrompt,
		Messages: []llm.Message{
			{
				Role:    "user",
				Content: prompt,
			},
		},
	}
	applyReviewLLMRuntime(req, in.Params)

	stopHB := heartbeatWhile(ctx, "calling LLM for problem review", 15*time.Second)
	resp, sourceArtifact, err := a.completeLLMWithProvenance(ctx, "review", req, 2)
	stopHB()
	if err != nil {
		return nil, wrapRequiredProviderEffectError("llm call for review", err)
	}

	activity.RecordHeartbeat(ctx, "parsing review response")

	result, err := parseReviewResponse(resp.Text())
	if err != nil {
		return nil, fmt.Errorf("parsing review response: %w", err)
	}
	result.SourceArtifacts = append(result.SourceArtifacts, sourceArtifact)

	logger.Info("LLM review completed",
		"approved", result.Approved,
		"confidence", result.Confidence,
		"issues", len(result.Issues),
		"estimated_difficulty", result.EstimatedDifficulty,
	)

	return result, nil
}

const reviewSystemPrompt = `You are an expert competitive programming problem reviewer. Your task is to evaluate a generated problem for quality, correctness, and suitability for a programming contest.

Review-scope boundary:
- The contestant-visible surface is only the supplied problem title, statement, samples, and the published resource-limit lines.
- Auxiliary authoring notes (including a one-line clue and an author's difficulty rationale) are deliberately not supplied to this reviewer and are not part of the contestant contract.
- Judge statement clarity and contestant-facing difficulty from that visible surface alone. Do not require, reward, or penalize an omitted auxiliary clue or author note.
- Model/brute programs, test-case descriptors, tags, and target difficulty are internal evidence. Use them for correctness, coverage, duplicate, and calibration checks as instructed, but never treat information present only in those artifacts as a statement defect.

You MUST output valid JSON with the following structure:
{
  "approved": true,
  "issues": ["issue1", "issue2"],
  "suggestions": ["suggestion1", "suggestion2"],
  "confidence": 0.95,
  "estimated_difficulty": 1800,
  "is_duplicate": false,
  "duplicate_of": "",
  "duplicate_reason": "",
  "review_details": {
    "clarity": {"score": 9, "notes": "..."},
    "correctness": {"score": 10, "notes": "..."},
    "test_coverage": {"score": 8, "notes": "..."},
    "difficulty_calibration": {
      "score": 9,
      "notes": "...",
      "estimated_difficulty": 1800,
      "key_observations": ["observation1", "observation2"],
      "requires_modeling": true
    },
    "tag_accuracy": {"score": 10, "notes": "..."}
  }
}

Evaluation Criteria:
1. Clarity: Is the problem statement unambiguous? Are input/output formats clear?
2. Correctness: Is the problem well-defined? Does the solution match the statement?
3. Test Coverage: Do the test cases cover edge cases, boundary conditions, and adversarial inputs?
4. Difficulty Calibration (CRITICAL): Evaluate carefully using these questions:
   a. How many key observations are needed to solve this problem?
   b. How non-obvious are these observations? Would they require contest experience?
   c. Does it require combining multiple algorithmic techniques?
   d. How long would a contestant at this rating level take to solve it?
   e. What is your estimated actual difficulty rating? (must be a multiple of 100)
5. Tag Accuracy: Do the assigned tags accurately reflect the required techniques?

Set "approved" to true only if all criteria score 7 or above (out of 10).
Set "confidence" to a value between 0.0 and 1.0 representing your confidence in the review.
Set "estimated_difficulty" to your honest assessment of the actual difficulty (multiple of 100).
Set "is_duplicate" to true only when the current problem is substantially isomorphic to a provided near-neighbor.
Set "duplicate_of" to the matching problem title when is_duplicate=true, otherwise an empty string.
Set "duplicate_reason" to the concrete isomorphism reason when is_duplicate=true, otherwise an empty string.

` + DifficultyRubric + contestTesterSkillGuidance

// buildReviewPrompt constructs the user prompt for the LLM review activity.
// It separates the contestant-visible statement from internal calibration and
// validation evidence. Authoring-only notes are intentionally not serialized
// into this prompt.
func buildReviewPrompt(statement StatementResult, solutions SolutionResult, testCases []TestCaseData, params domain.ProblemGenParams, neighbors []NeighborInfo) string {
	var sb strings.Builder

	sb.WriteString("Please review the following competitive programming problem:\n\n")

	sb.WriteString("## Contestant-visible Problem Statement\n")
	sb.WriteString(fmt.Sprintf("**Title:** %s\n", statement.Title))
	sb.WriteString(statement.Statement)
	sb.WriteString("\n\n")

	sb.WriteString("## Internal Calibration Metadata (not contestant-visible)\n")
	sb.WriteString(fmt.Sprintf("**Tags:** %s\n", strings.Join(statement.Tags, ", ")))
	sb.WriteString(fmt.Sprintf("**Target Difficulty:** %d\n", params.Difficulty))
	sb.WriteString("\n\n")

	// Difficulty calibration context.
	bandDesc := DifficultyBandDescription(params.Difficulty)
	if bandDesc != "" {
		sb.WriteString("### Expected Difficulty Band (internal)\n")
		sb.WriteString(bandDesc)
		sb.WriteString("\n\n")
	}

	if len(params.Tags) > 0 {
		sb.WriteString(BuildTagDifficultySection(params.Tags, params.TagRanges, params.Difficulty))
		sb.WriteString("\n")
	}
	sb.WriteString(knowledgePointCombinationPrompt(params, knowledgePointPromptReview))

	sb.WriteString("## Internal Validation Evidence (not contestant-visible)\n")
	sb.WriteString("### Model Solution\n")
	sb.WriteString(fmt.Sprintf("**Language:** %s\n", solutions.MainSolution.Language))
	sb.WriteString("```\n")
	sb.WriteString(solutions.MainSolution.SourceCode)
	sb.WriteString("\n```\n\n")

	sb.WriteString("### Brute-Force Solution\n")
	sb.WriteString(fmt.Sprintf("**Language:** %s\n", solutions.BruteSolution.Language))
	sb.WriteString("```\n")
	sb.WriteString(solutions.BruteSolution.SourceCode)
	sb.WriteString("\n```\n\n")

	sb.WriteString("Expected outputs are intentionally omitted from this compact review prompt. Before this review, the sandbox executed the generated solutions and the workflow cross-validated their outputs. Do not treat the absence of displayed expected-output blocks as missing test data.\n\n")

	sb.WriteString(fmt.Sprintf("### Test Cases (%d total, internal evidence)\n", len(testCases)))
	// Show up to 5 representative test cases to keep the prompt manageable.
	shown := 0
	for i, tc := range testCases {
		if shown >= 5 {
			sb.WriteString(fmt.Sprintf("\n... and %d more test cases\n", len(testCases)-shown))
			break
		}
		sb.WriteString(fmt.Sprintf("\n### Test %d (Group %d, Sample: %v)\n", i+1, tc.GroupID, tc.IsSample))
		if tc.Description != "" {
			sb.WriteString(fmt.Sprintf("Description: %s\n", tc.Description))
		}
		if len(tc.Coverage) > 0 {
			sb.WriteString(fmt.Sprintf("Coverage labels (internal evidence): %s\n", strings.Join(normalizeCoverageTags(tc.Coverage), ", ")))
		}
		switch {
		case tc.Input != "":
			sb.WriteString("Input preview:\n```\n")
			sb.WriteString(truncate(tc.Input, 1000))
			sb.WriteString("\n```\n")
		case tc.InputArtifact != nil:
			sb.WriteString("Input: validated immutable artifact; full payload omitted from this review prompt.\n")
			sb.WriteString(fmt.Sprintf("Artifact size: %d bytes; SHA-256: %s.\n", tc.InputArtifact.SizeBytes, tc.InputArtifact.SHA256))
			sb.WriteString("Do not treat this case as empty or missing; assess its stated attack purpose and the completed sandbox validation evidence.\n")
		default:
			sb.WriteString("Input: missing (this is a blocking test-data defect).\n")
		}
		shown++
	}

	sb.WriteString("\n\nIMPORTANT: In your review, carefully assess the actual difficulty of this problem. ")
	sb.WriteString(fmt.Sprintf("The target difficulty is %d. ", params.Difficulty))
	sb.WriteString("Provide your honest estimated_difficulty (as a multiple of 100) in the JSON output. ")
	sb.WriteString("If the problem is significantly easier or harder than the target, flag it as an issue.")
	sb.WriteString(" For clarity and difficulty, rely on the contestant-visible statement and its verified samples/limits only. Internal programs, test descriptors, tags, and target metadata are evidence for their designated checks and must not be treated as text that contestants were expected to see.")
	sb.WriteString(" Apply the Algorithm Contest Tester Skill from the system prompt: include attacker-style ambiguity checks, proof/complexity attacks, and whether hidden tests catch plausible wrong solutions.")

	if len(neighbors) > 0 {
		sb.WriteString("\n\n## Internal Duplicate Check (not contestant-visible)\n")
		sb.WriteString("以下是题库中与当前题目题面最接近的若干题目，请判断是否构成重复出题：\n")
		for i, n := range neighbors {
			sb.WriteString(fmt.Sprintf("%d. 《%s》（相似度 %.2f）\n", i+1, n.Title, n.Similarity))
		}
		sb.WriteString("\n判定标准：当且仅当题目背景、核心算法、输入输出三者中至少两项与某道近邻题目实质同构时，标记 is_duplicate=true。\n")
	}

	sb.WriteString("\n\nPlease provide your review as a JSON object.")

	return sb.String()
}

func buildReviewPromptWithResourceCalibration(
	statement StatementResult,
	solutions SolutionResult,
	testCases []TestCaseData,
	params domain.ProblemGenParams,
	neighbors []NeighborInfo,
	calibration *ResourceCalibrationV1,
) string {
	prompt := buildReviewPrompt(statement, solutions, testCases, params, neighbors)
	if calibration == nil {
		return prompt
	}

	var sb strings.Builder
	sb.WriteString(prompt)
	sb.WriteString("\n\n## Internal Target Sandbox Resource Evidence (authoritative)\n")
	sb.WriteString(fmt.Sprintf("Calibration policy: `%s`\n", calibration.Policy))
	sb.WriteString(fmt.Sprintf(
		"The exact model solution was benchmarked on all %d generated cases with %d ms, %d MB memory, and %d MB stack. ",
		calibration.ObservedCaseCount,
		calibration.BenchmarkLimits.TimeLimitMs,
		calibration.BenchmarkLimits.MemoryLimitMB,
		calibration.BenchmarkLimits.MemoryLimitMB,
	))
	sb.WriteString(fmt.Sprintf(
		"Its measured peaks were %d ms and %d bytes.\n",
		calibration.ObservedMaxTimeMS,
		calibration.ObservedMaxMemoryBytes,
	))
	sb.WriteString(fmt.Sprintf(
		"The workflow derived final limits of %d ms and %d MB; this target sandbox also set the process stack limit to %d MB.\n",
		calibration.FinalLimits.TimeLimitMs,
		calibration.FinalLimits.MemoryLimitMB,
		calibration.StackLimitMB,
	))
	sb.WriteString(fmt.Sprintf(
		"Benchmark receipt: run_id=%s, manifest=%s, limit_profile=%s.\n",
		calibration.BenchmarkAudit.RunID,
		calibration.BenchmarkAudit.ManifestDigest,
		calibration.BenchmarkAudit.LimitProfile,
	))
	sb.WriteString(fmt.Sprintf(
		"Final-limit receipt: run_id=%s, manifest=%s, limit_profile=%s. The exact model solution passed the entire generated suite again under these final limits.\n",
		calibration.FinalAudit.RunID,
		calibration.FinalAudit.ManifestDigest,
		calibration.FinalAudit.LimitProfile,
	))
	sb.WriteString("Resource-safety conclusions MUST be evidence-bound to these target sandbox receipts. Do not reject the solution merely from a hypothetical platform default (for example, assuming an 8 MB stack) when the recorded target stack and exact execution contradict it. ")
	sb.WriteString("If a required maximum-scale case is absent, report that concrete test-coverage gap; do not transform the absence into an unsupported claim that the exact binary already failed. A runtime or stack defect is blocking only when reproduced by the target sandbox evidence or demonstrated on a concrete missing in-constraint case.\n")
	sb.WriteString("Please incorporate this authoritative evidence into the JSON review.")
	return sb.String()
}

// reviewJSON is the expected JSON structure from the LLM for the review.
type reviewDetailsJSON struct {
	Clarity struct {
		Score int    `json:"score"`
		Notes string `json:"notes"`
	} `json:"clarity"`
	Correctness struct {
		Score int    `json:"score"`
		Notes string `json:"notes"`
	} `json:"correctness"`
	TestCoverage struct {
		Score int    `json:"score"`
		Notes string `json:"notes"`
	} `json:"test_coverage"`
	DifficultyCal struct {
		Score               int      `json:"score"`
		Notes               string   `json:"notes"`
		EstimatedDifficulty int      `json:"estimated_difficulty,omitempty"`
		KeyObservations     []string `json:"key_observations,omitempty"`
		RequiresModeling    bool     `json:"requires_modeling,omitempty"`
	} `json:"difficulty_calibration"`
	TagAccuracy struct {
		Score int    `json:"score"`
		Notes string `json:"notes"`
	} `json:"tag_accuracy"`
}

type reviewJSON struct {
	Approved            bool               `json:"approved"`
	Issues              []string           `json:"issues"`
	Suggestions         []string           `json:"suggestions"`
	Confidence          float64            `json:"confidence"`
	EstimatedDifficulty int                `json:"estimated_difficulty,omitempty"`
	IsDuplicate         bool               `json:"is_duplicate"`
	DuplicateOf         string             `json:"duplicate_of,omitempty"`
	DuplicateReason     string             `json:"duplicate_reason,omitempty"`
	ReviewDetails       *reviewDetailsJSON `json:"review_details,omitempty"`
}

// parseReviewResponse parses the LLM response text into a ReviewResult,
// including the estimated_difficulty field.
func parseReviewResponse(text string) (*ReviewResult, error) {
	text = strings.TrimSpace(text)

	var parsed reviewJSON

	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		jsonContent := extractJSONBlock(text)
		if jsonContent != "" {
			if err2 := json.Unmarshal([]byte(jsonContent), &parsed); err2 == nil {
				goto reviewParsed
			}
		}
		jsonContent = extractOutermostJSON(text)
		if jsonContent != "" {
			if err2 := json.Unmarshal([]byte(jsonContent), &parsed); err2 == nil {
				goto reviewParsed
			}
		}
		return nil, fmt.Errorf("failed to parse review response as JSON")
	}
reviewParsed:

	// Clamp confidence to [0, 1].
	confidence := parsed.Confidence
	if confidence < 0 {
		confidence = 0
	}
	if confidence > 1 {
		confidence = 1
	}

	// Resolve estimated difficulty: prefer top-level field, fall back to
	// review_details.difficulty_calibration.estimated_difficulty.
	estimatedDifficulty := parsed.EstimatedDifficulty
	if estimatedDifficulty == 0 && parsed.ReviewDetails != nil {
		estimatedDifficulty = parsed.ReviewDetails.DifficultyCal.EstimatedDifficulty
	}
	var reviewDetails json.RawMessage
	if parsed.ReviewDetails != nil {
		reviewDetails, _ = json.Marshal(parsed.ReviewDetails)
	}

	return &ReviewResult{
		Approved:            parsed.Approved,
		Issues:              parsed.Issues,
		Suggestions:         parsed.Suggestions,
		Confidence:          confidence,
		EstimatedDifficulty: estimatedDifficulty,
		IsDuplicate:         parsed.IsDuplicate,
		DuplicateOf:         parsed.DuplicateOf,
		DuplicateReason:     parsed.DuplicateReason,
		ReviewDetails:       reviewDetails,
		FullText:            text,
	}, nil
}
