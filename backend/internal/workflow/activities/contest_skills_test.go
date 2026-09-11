package activities

import (
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
)

func TestContestSkillGuidanceInjectedIntoPrompts(t *testing.T) {
	params := domain.ProblemGenParams{
		Level:       domain.LevelAlgorithm,
		Difficulty:  1800,
		TimeLimit:   2000,
		MemoryLimit: 256,
		Tags:        []string{"dp", "greedy"},
		Languages:   []string{"cpp"},
		Locale:      "zh",
	}

	statementPrompt := buildStatementPrompt(params, nil)
	assertContains(t, statementSystemPrompt, "Algorithm Contest Problemsetter Skill")
	assertContains(t, statementSystemPrompt, "at most 8 MiB")
	assertContains(t, statementSystemPrompt, "QUALITY PREFLIGHT")
	assertContains(t, statementPrompt, "likely wrong solutions and the hidden-test attack points")
	assertContains(t, statementPrompt, "provisional authoring inputs")
	assertContains(t, statementPrompt, "single concise conceptual clue")

	testDataPrompt := buildTestDataPrompt("Given n numbers, compute the answer.", domain.TestDataConfig{
		NumTestCases: 4,
		NumSamples:   1,
		Groups: []domain.TestGroup{{
			GroupID:     1,
			NumCases:    3,
			Score:       100,
			Description: "max constraints",
		}},
		BoundaryConfig: domain.BoundaryConfig{IncludeMinCase: true, IncludeMaxCase: true},
	})
	assertContains(t, testDataSystemPrompt, "Contest Test Data Skill")
	assertContains(t, testDataSystemPrompt, "exactly one maximum-scale case")
	assertContains(t, testDataSystemPrompt, "1 MiB")
	assertContains(t, testDataPrompt, "plausible wrong solution it targets")

	mainSolutionPrompt := buildMainSolutionPrompt("Given n numbers, compute the answer.", "cpp", params)
	bruteSolutionPrompt := buildBruteSolutionPrompt("Given n numbers, compute the answer.", "cpp", params)
	assertContains(t, solutionSystemPrompt, "Contest Solution Skill")
	assertContains(t, solutionSystemPrompt, "SILENT CORRECTNESS PREFLIGHT")
	assertContains(t, mainSolutionPrompt, "overflow limits")
	assertContains(t, bruteSolutionPrompt, "independent from the intended solution")

	reviewPrompt := buildReviewPrompt(
		StatementResult{
			Title:                   "Sample",
			Statement:               "Given n numbers, compute the answer.",
			Tags:                    []string{"dp", "greedy"},
			OneLineHint:             "Use the key invariant.",
			DifficultyJustification: "The intended solution is O(n log n).",
		},
		SolutionResult{
			MainSolution:  domain.Solution{Language: "cpp", SourceCode: "int main(){return 0;}"},
			BruteSolution: domain.Solution{Language: "cpp", SourceCode: "int main(){return 0;}"},
		},
		[]TestCaseData{
			{Input: "1\n", GroupID: 1, IsSample: true, Description: "sample"},
			{InputArtifact: &ArtifactRef{SizeBytes: 65536, SHA256: "abc123"}, GroupID: 1, Description: "large adversarial case"},
		},
		params,
		nil,
	)
	assertContains(t, reviewSystemPrompt, "Algorithm Contest Tester Skill")
	assertContains(t, reviewPrompt, "attacker-style ambiguity checks")
	assertContains(t, reviewPrompt, "Do not treat this case as empty or missing")
	assertContains(t, reviewPrompt, "Do not treat the absence of displayed expected-output blocks as missing test data")
}

func TestReviewRepairInstructionsReachEveryGeneratedArtifact(t *testing.T) {
	params := domain.DefaultProblemGenParams()
	params.Difficulty = 2000
	params.CustomPrompt = `[AlgoForge bounded review repair 1/3]
- issue_1: "recursive DFS can overflow at n=200000"
- suggested_repair_1: "use iterative parent order and add a 200000-node chain"
- suggested_repair_2: "recalibrate the problem to 1800"`

	prompts := map[string]string{
		"statement": buildStatementPrompt(params, nil),
		"testdata":  buildTestDataPromptWithParams("Solve the tree problem.", domain.DefaultTestDataConfig(), params),
		"main":      buildMainSolutionPrompt("Solve the tree problem.", "cpp", params),
		"brute":     buildBruteSolutionPrompt("Solve the tree problem.", "cpp", params),
	}
	for name, prompt := range prompts {
		assertContains(t, prompt, "recursive DFS can overflow at n=200000")
		assertContains(t, prompt, "use iterative parent order and add a 200000-node chain")
		assertContains(t, prompt, "Authoritative repair constraint: the requested target difficulty remains 2000")
		if strings.LastIndex(prompt, "Authoritative repair constraint") < strings.LastIndex(prompt, "recalibrate the problem to 1800") {
			t.Fatalf("%s prompt lets quoted difficulty feedback override the authoritative target", name)
		}
	}
}

func assertContains(t *testing.T, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Fatalf("expected prompt to contain %q", want)
	}
}
