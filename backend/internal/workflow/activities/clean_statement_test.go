package activities

import "testing"

func TestCoTPatternDetectsChineseReasoningLeak(t *testing.T) {
	statement := "样例解释\n等等，题目要求最少消耗燃料。让我重新审视题目。"
	if !cotPatterns.MatchString(statement) {
		t.Fatalf("expected Chinese reasoning trace to be detected")
	}
}

func TestCoTPatternAllowsCleanSampleExplanation(t *testing.T) {
	statement := "样例解释\n前缀和始终不小于目标前缀和，最后一次需要向右搬运发生在第 2 条边，因此答案为 2。"
	if cotPatterns.MatchString(statement) {
		t.Fatalf("did not expect clean sample explanation to be flagged")
	}
}
