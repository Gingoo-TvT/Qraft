package activities

import (
	"errors"
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"go.temporal.io/sdk/temporal"
)

func versionedKnowledgePointParams(mode string, tags []string) domain.ProblemGenParams {
	params := domain.DefaultProblemGenParams()
	params.Tags = append([]string(nil), tags...)
	params.TagRanges = make(map[string][2]int, len(tags))
	for _, tag := range tags {
		params.TagRanges[tag] = [2]int{800, 3500}
	}
	params.KnowledgePointCombination = &domain.KnowledgePointCombinationContract{
		SchemaVersion: domain.KnowledgePointCombinationSchemaV1,
		Mode:          mode,
		MaxConcepts:   len(tags),
	}
	return params
}

func TestKnowledgePointCombinationPromptsReachGenerationAndReview(t *testing.T) {
	params := versionedKnowledgePointParams(
		domain.KnowledgePointCombinationSequence,
		[]string{"sorting", "two-pointers"},
	)
	config := domain.DefaultTestDataConfig()
	statement := StatementResult{Title: "KC", Statement: "Solve it.", Tags: params.Tags}
	solutions := SolutionResult{
		MainSolution:  domain.Solution{Language: "cpp", SourceCode: "int main(){}"},
		BruteSolution: domain.Solution{Language: "cpp", SourceCode: "int main(){}"},
	}

	prompts := map[string]string{
		"similarity": buildSimilarityQuery(params),
		"statement":  buildStatementPrompt(params, nil),
		"solution":   buildMainSolutionPrompt("Solve it.", "cpp", params),
		"testdata":   buildTestDataPromptWithParams("Solve it.", config, params),
		"review":     buildReviewPrompt(statement, solutions, nil, params, nil),
	}
	for name, prompt := range prompts {
		if !strings.Contains(prompt, domain.KnowledgePointCombinationSchemaV1) ||
			!strings.Contains(prompt, "sequence") ||
			!strings.Contains(prompt, "sorting, two-pointers") {
			t.Fatalf("%s prompt omitted combination contract:\n%s", name, prompt)
		}
	}
	if !strings.Contains(prompts["statement"], "exactly these slugs") {
		t.Fatalf("statement prompt omitted exact tag binding:\n%s", prompts["statement"])
	}
	if !strings.Contains(prompts["testdata"], "omit any requested slug") {
		t.Fatalf("testdata prompt omitted per-concept attacks:\n%s", prompts["testdata"])
	}
	if !strings.Contains(prompts["review"], "Reject the candidate") {
		t.Fatalf("review prompt omitted combination rejection rule:\n%s", prompts["review"])
	}
}

func TestLegacyPromptsDoNotGainKnowledgePointContractText(t *testing.T) {
	params := domain.DefaultProblemGenParams()
	params.Tags = []string{"dp"}
	for name, prompt := range map[string]string{
		"similarity": buildSimilarityQuery(params),
		"statement":  buildStatementPrompt(params, nil),
		"solution":   buildMainSolutionPrompt("Solve it.", "cpp", params),
		"testdata":   buildTestDataPrompt("Solve it.", domain.DefaultTestDataConfig()),
	} {
		if strings.Contains(prompt, "Knowledge-point combination contract") || strings.Contains(prompt, domain.KnowledgePointCombinationSchemaV1) {
			t.Fatalf("legacy %s prompt changed:\n%s", name, prompt)
		}
	}
}

func TestKnowledgePointCombinationMismatchIsQualityNotMet(t *testing.T) {
	params := versionedKnowledgePointParams(
		domain.KnowledgePointCombinationMixed,
		[]string{"graph", "dfs", "greedy"},
	)
	canonical, err := conformStatementKnowledgePoints(params, []string{"graph", "greedy", "DFS"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(canonical, ",") != "graph,dfs,greedy" {
		t.Fatalf("canonical tags = %v", canonical)
	}

	_, err = conformStatementKnowledgePoints(params, []string{"dfs", "graph", "greedy"})
	var applicationErr *temporal.ApplicationError
	if !errors.As(err, &applicationErr) || applicationErr.Type() != "QualityNotMet" || !applicationErr.NonRetryable() {
		t.Fatalf("mismatch error = %T %v, want non-retryable QualityNotMet", err, err)
	}
}

func TestLegacyKnowledgePointConformancePreservesModelTags(t *testing.T) {
	params := domain.DefaultProblemGenParams()
	observed := []string{"Model Tag", "Another"}
	got, err := conformStatementKnowledgePoints(params, observed)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, "|") != strings.Join(observed, "|") {
		t.Fatalf("legacy tags = %v, want %v", got, observed)
	}
}
