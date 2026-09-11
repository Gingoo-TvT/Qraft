package activities

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/generationapi"
)

func TestReviewerV0DescriptorBindsPromptContract(t *testing.T) {
	statement := StatementResult{
		Title:                   "Frozen reviewer fixture",
		Statement:               "Given n integers, output their sum.",
		Tags:                    []string{"prefix-sum"},
		OneLineHint:             "Accumulate once.",
		DifficultyJustification: "One direct observation.",
	}
	solutions := SolutionResult{
		MainSolution:  domain.Solution{Language: "cpp", SourceCode: "int main() { return 0; }"},
		BruteSolution: domain.Solution{Language: "cpp", SourceCode: "int main() { return 0; }"},
	}
	testCases := make([]TestCaseData, 6)
	for index := range testCases {
		testCases[index] = TestCaseData{
			Input:       "1\n",
			GroupID:     1,
			IsSample:    index == 0,
			Description: "frozen case",
		}
	}
	params := domain.ProblemGenParams{
		Difficulty: 1600,
		Tags:       []string{"prefix-sum"},
		TagRanges:  map[string][2]int{"prefix-sum": {800, 2400}},
	}
	userPrompt := buildReviewPrompt(statement, solutions, testCases, params, []NeighborInfo{{
		Title: "Neighbor", Similarity: 0.75, OneLineHint: "Compare prefixes.",
	}})
	systemDigest := sha256.Sum256([]byte(reviewSystemPrompt))
	userDigest := sha256.Sum256([]byte(userPrompt))
	descriptor, err := json.Marshal(struct {
		SchemaVersion       string `json:"schema_version"`
		PromptBuilder       string `json:"prompt_builder"`
		SystemPromptSHA256  string `json:"system_prompt_sha256"`
		FixturePromptSHA256 string `json:"fixture_prompt_sha256"`
		VerdictPolicy       string `json:"verdict_policy"`
	}{
		SchemaVersion:       generationapi.ReviewerProfileV0,
		PromptBuilder:       "algoforge.review-prompt-builder.v0",
		SystemPromptSHA256:  hex.EncodeToString(systemDigest[:]),
		FixturePromptSHA256: hex.EncodeToString(userDigest[:]),
		VerdictPolicy:       "model-self-report-threshold-7-v0",
	})
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(descriptor)
	got := hex.EncodeToString(digest[:])
	if got != generationapi.ReviewerProfileDescriptorSHA256 {
		t.Fatalf("reviewer_v0 descriptor changed: got=%s want=%s", got, generationapi.ReviewerProfileDescriptorSHA256)
	}
}

func TestReviewerV1DescriptorBindsKnowledgePointCombinationPrompt(t *testing.T) {
	statement := StatementResult{
		Title:                   "Frozen combination reviewer fixture",
		Statement:               "Given an array, answer staged prefix queries.",
		Tags:                    []string{"prefix-sum", "two-pointers"},
		OneLineHint:             "Build prefixes, then scan queries.",
		DifficultyJustification: "Both stages are necessary.",
	}
	solutions := SolutionResult{
		MainSolution:  domain.Solution{Language: "cpp", SourceCode: "int main() { return 0; }"},
		BruteSolution: domain.Solution{Language: "cpp", SourceCode: "int main() { return 0; }"},
	}
	params := versionedKnowledgePointParams(
		domain.KnowledgePointCombinationSequence,
		[]string{"prefix-sum", "two-pointers"},
	)
	params.Difficulty = 1600
	testCases := []TestCaseData{{
		Input: "1\n", GroupID: 1, IsSample: true, Description: "frozen case",
	}}
	userPrompt := buildReviewPrompt(statement, solutions, testCases, params, nil)
	systemDigest := sha256.Sum256([]byte(reviewSystemPrompt))
	userDigest := sha256.Sum256([]byte(userPrompt))
	descriptor, err := json.Marshal(struct {
		SchemaVersion       string `json:"schema_version"`
		PromptBuilder       string `json:"prompt_builder"`
		SystemPromptSHA256  string `json:"system_prompt_sha256"`
		FixturePromptSHA256 string `json:"fixture_prompt_sha256"`
		VerdictPolicy       string `json:"verdict_policy"`
		TagConformance      string `json:"tag_conformance"`
	}{
		SchemaVersion:       generationapi.ReviewerProfileV1,
		PromptBuilder:       "algoforge.review-prompt-builder.v1",
		SystemPromptSHA256:  hex.EncodeToString(systemDigest[:]),
		FixturePromptSHA256: hex.EncodeToString(userDigest[:]),
		VerdictPolicy:       "model-self-report-threshold-7-plus-kc-contract-v1",
		TagConformance:      "deterministic-exact-before-review-v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(descriptor)
	got := hex.EncodeToString(digest[:])
	if got != generationapi.ReviewerProfileV1DescriptorSHA256 {
		t.Fatalf("reviewer_v1 descriptor changed: got=%s want=%s", got, generationapi.ReviewerProfileV1DescriptorSHA256)
	}
}

func TestBuildReviewPromptUsesContestantVisibleScope(t *testing.T) {
	const (
		internalHint      = "INTERNAL_HINT_SENTINEL_7f2a"
		internalRationale = "INTERNAL_RATIONALE_SENTINEL_91c4"
		neighborHint      = "INTERNAL_NEIGHBOR_HINT_SENTINEL_3b8d"
	)
	statement := StatementResult{
		Title:                   "Visible title",
		Statement:               "Visible statement with verified samples.",
		Tags:                    []string{"dp"},
		OneLineHint:             internalHint,
		DifficultyJustification: internalRationale,
	}
	params := domain.DefaultProblemGenParams()
	params.Difficulty = 1600
	prompt := buildReviewPrompt(
		statement,
		SolutionResult{
			MainSolution:  domain.Solution{Language: "cpp", SourceCode: "int main(){}"},
			BruteSolution: domain.Solution{Language: "cpp", SourceCode: "int main(){}"},
		},
		[]TestCaseData{{Input: "1\n", IsSample: true}},
		params,
		[]NeighborInfo{{Title: "Neighbor", Similarity: 0.8, OneLineHint: neighborHint}},
	)
	for _, forbidden := range []string{internalHint, internalRationale, neighborHint, "**Hint:**", "## Difficulty Justification", "   一句话："} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("review prompt leaked non-contestant metadata %q:\n%s", forbidden, prompt)
		}
	}
	for _, required := range []string{
		"## Contestant-visible Problem Statement",
		"Visible statement with verified samples.",
		"## Internal Calibration Metadata (not contestant-visible)",
		"## Internal Validation Evidence (not contestant-visible)",
		"For clarity and difficulty, rely on the contestant-visible statement",
	} {
		if !strings.Contains(prompt, required) {
			t.Fatalf("review prompt omitted scope boundary %q:\n%s", required, prompt)
		}
	}
}

func TestParseReviewResponsePreservesFullConclusion(t *testing.T) {
	raw := `{"approved":false,"issues":["ambiguous"],"suggestions":["clarify"],"confidence":0.8,"estimated_difficulty":1600,"is_duplicate":false,"duplicate_of":"","duplicate_reason":"","review_details":{"clarity":{"score":4,"notes":"unclear boundary"}}}`
	result, err := parseReviewResponse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if result.FullText != raw {
		t.Fatalf("full review text changed:\n got: %s\nwant: %s", result.FullText, raw)
	}
	if len(result.ReviewDetails) == 0 || !json.Valid(result.ReviewDetails) {
		t.Fatalf("review details were not preserved: %s", result.ReviewDetails)
	}
	var details map[string]interface{}
	if err := json.Unmarshal(result.ReviewDetails, &details); err != nil {
		t.Fatal(err)
	}
	if _, ok := details["clarity"]; !ok {
		t.Fatalf("review details omitted clarity: %v", details)
	}
}

func TestBuildReviewPromptWithResourceCalibrationBindsTargetSandboxEvidence(t *testing.T) {
	statement := StatementResult{Title: "Stack fixture", Statement: "Process a tree."}
	solutions := SolutionResult{
		MainSolution:  domain.Solution{Language: "cpp", SourceCode: "void dfs(int u){}"},
		BruteSolution: domain.Solution{Language: "cpp", SourceCode: "int main(){}"},
	}
	params := domain.DefaultProblemGenParams()
	calibration := &ResourceCalibrationV1{
		SchemaVersion:          1,
		Policy:                 "fixture-policy",
		ObservedCaseCount:      20,
		BenchmarkLimits:        ExecutionLimits{TimeLimitMs: 10000, MemoryLimitMB: 512},
		ObservedMaxTimeMS:      127,
		ObservedMaxMemoryBytes: 24 << 20,
		FinalLimits:            ExecutionLimits{TimeLimitMs: 1000, MemoryLimitMB: 80},
		StackLimitMB:           80,
		BenchmarkAudit: SandboxAuditMetadata{
			RunID: "benchmark-run", ManifestDigest: "benchmark-manifest", LimitProfile: "time_ms=10000,memory_mb=512,stack_mb=512",
		},
		FinalAudit: SandboxAuditMetadata{
			RunID: "final-run", ManifestDigest: "final-manifest", LimitProfile: "time_ms=1000,memory_mb=80,stack_mb=80",
		},
	}

	legacy := buildReviewPrompt(statement, solutions, nil, params, nil)
	if got := buildReviewPromptWithResourceCalibration(statement, solutions, nil, params, nil, nil); got != legacy {
		t.Fatal("nil calibration changed the frozen legacy review prompt")
	}
	prompt := buildReviewPromptWithResourceCalibration(statement, solutions, nil, params, nil, calibration)
	for _, required := range []string{
		"Target Sandbox Resource Evidence (authoritative)",
		"process stack limit to 80 MB",
		"benchmark-run",
		"final-run",
		"Do not reject the solution merely from a hypothetical platform default",
		"required maximum-scale case is absent",
	} {
		if !strings.Contains(prompt, required) {
			t.Fatalf("resource evidence prompt omitted %q:\n%s", required, prompt)
		}
	}
}
