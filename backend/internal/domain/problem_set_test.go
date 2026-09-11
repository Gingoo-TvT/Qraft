package domain

import "testing"

func TestProblemSetNormalizeDefaultsAndCompactsTags(t *testing.T) {
	set := &ProblemSet{
		Code:       "  weekly-01 ",
		Title:      "  树与动态规划  ",
		Kind:       ProblemSetKindContest,
		Visibility: ProblemSetVisibilityPrivate,
		Tags:       []string{" tree ", "DP", "dp", ""},
	}
	if err := set.NormalizeProblemSet(); err != nil {
		t.Fatalf("NormalizeProblemSet returned error: %v", err)
	}
	if set.Code != "weekly-01" || set.Title != "树与动态规划" {
		t.Fatalf("trimmed fields = code %q title %q", set.Code, set.Title)
	}
	if set.MinItemCount != 10 || set.MaxItemCount != 20 || set.Status != ProblemSetStatusDraft {
		t.Fatalf("defaults = min %d max %d status %q", set.MinItemCount, set.MaxItemCount, set.Status)
	}
	if len(set.Tags) != 2 || set.Tags[0] != "DP" || set.Tags[1] != "tree" {
		t.Fatalf("compacted tags = %#v", set.Tags)
	}
}

func TestProblemSetNormalizeRejectsInvalidDesiredRange(t *testing.T) {
	set := &ProblemSet{
		Code: "set-1", Title: "set", Kind: ProblemSetKindContest,
		Visibility: ProblemSetVisibilityPrivate, DesiredItemCount: 9,
		MinItemCount: 10, MaxItemCount: 20,
	}
	if err := set.NormalizeProblemSet(); err == nil {
		t.Fatal("desired count outside configured range was accepted")
	}
}

func TestProblemSetNormalizeCollapsesNewExactCount(t *testing.T) {
	set := &ProblemSet{
		Code: "set-exact", Title: "exact", Kind: ProblemSetKindContest,
		Visibility: ProblemSetVisibilityPrivate, DesiredItemCount: 1000,
	}
	if err := set.NormalizeProblemSet(); err != nil {
		t.Fatalf("exact count was rejected: %v", err)
	}
	if set.MinItemCount != 1000 || set.MaxItemCount != 1000 {
		t.Fatalf("exact count bounds = [%d,%d], want [1000,1000]", set.MinItemCount, set.MaxItemCount)
	}
}

func TestProblemSetNormalizeRejectsCountAboveNewLimit(t *testing.T) {
	set := &ProblemSet{
		Code: "set-too-large", Title: "too large", Kind: ProblemSetKindContest,
		Visibility: ProblemSetVisibilityPrivate, DesiredItemCount: MaxProblemSetItemCount + 1,
	}
	if err := set.NormalizeProblemSet(); err == nil {
		t.Fatal("desired count above the direct-count limit was accepted")
	}
}
