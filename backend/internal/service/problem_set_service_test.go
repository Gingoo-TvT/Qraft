package service

import (
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/google/uuid"
)

func TestRecommendProblemSetItemCountAdaptsToCoverageAxes(t *testing.T) {
	base := &domain.ProblemSet{MinItemCount: 10, MaxItemCount: 20}
	if got := recommendProblemSetItemCount(base); got != 10 {
		t.Fatalf("base recommendation = %d, want 10", got)
	}
	wide := &domain.ProblemSet{
		MinItemCount: 10, MaxItemCount: 20,
		Tags:             []string{"树", "DP", "字符串", "数论", "图"},
		DifficultyPrompt: "覆盖最大规模和边界 corner case",
	}
	got := recommendProblemSetItemCount(wide)
	if got < 10 || got > 20 || got <= 10 {
		t.Fatalf("wide coverage recommendation = %d, want a value in (10,20]", got)
	}
	explicit := &domain.ProblemSet{DesiredItemCount: 17, MinItemCount: 10, MaxItemCount: 20}
	if got := recommendProblemSetItemCount(explicit); got != 17 {
		t.Fatalf("explicit recommendation = %d, want 17", got)
	}
	customRange := &domain.ProblemSet{
		MinItemCount: 2, MaxItemCount: 5,
		Tags: []string{"图论", "DP", "字符串", "数论"},
	}
	if got := recommendProblemSetItemCount(customRange); got < 2 || got > 5 {
		t.Fatalf("custom-range recommendation = %d, want a value in [2,5]", got)
	}
}

func TestCleanPromptRemovesMarkdownFenceAndBoundsLength(t *testing.T) {
	if got := cleanPrompt("```markdown\n  use this brief  \n```"); got != "use this brief" {
		t.Fatalf("cleanPrompt = %q", got)
	}
	long := strings.Repeat("x", problemSetPromptMaxChars+100)
	if got := cleanPrompt(long); len([]rune(got)) != problemSetPromptMaxChars {
		t.Fatalf("cleanPrompt length = %d, want %d", len([]rune(got)), problemSetPromptMaxChars)
	}
}

func TestProblemSetHelpersProduceStableExportMetadata(t *testing.T) {
	id := uuid.MustParse("00000000-0000-0000-0000-000000000123")
	problem := &domain.Problem{SerialNumber: "AF-42", Difficulty: 1500}
	if got := problemSetOJCode(problem, &id); got != "C1042" {
		t.Fatalf("problemSetOJCode = %q, want C1042", got)
	}
	if got := problemDifficultyToExcel(2100); got != "挑战" {
		t.Fatalf("problemDifficultyToExcel = %q, want 挑战", got)
	}
	first := problemSetOJCode(nil, &id)
	second := problemSetOJCode(nil, &id)
	if first == "" || first != second {
		t.Fatalf("fallback OJ code is not stable: %q vs %q", first, second)
	}
}

func TestItemKnowledgeKeysExcludeSourceIdentity(t *testing.T) {
	problemID := uuid.MustParse("00000000-0000-0000-0000-000000000123")
	item := domain.ProblemSetItem{Problem: &domain.Problem{
		ID: problemID, Level: domain.LevelAlgorithm, Tags: []string{"DP"},
	}}
	keys := itemKnowledgeKeys(item)
	for _, key := range keys {
		if strings.HasPrefix(key, "problem:") || strings.HasPrefix(key, "quiz:") {
			t.Fatalf("knowledge key contains source identity: %q", key)
		}
	}
	if len(keys) == 0 {
		t.Fatal("expected level/tag knowledge keys")
	}
	if itemSourceFingerprint(item) == "" {
		t.Fatal("expected a source fingerprint")
	}
}
