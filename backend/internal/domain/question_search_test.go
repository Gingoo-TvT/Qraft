package domain

import (
	"strings"
	"testing"
)

func TestQuestionSearchNormalize(t *testing.T) {
	got, err := (QuestionSearchFilter{
		Keyword: "  树 DP  ", Tag: " 100%_tag ", KnowledgePoint: " c_loop ",
	}).Normalize()
	if err != nil {
		t.Fatal(err)
	}
	if got.Keyword != "树 DP" || got.Tag != "100%_tag" || got.KnowledgePoint != "c_loop" || got.Page != 1 || got.Size != 20 {
		t.Fatalf("unexpected normalized filter: %+v", got)
	}
	// Limits count human-readable characters, not UTF-8 bytes.
	if _, err := (QuestionSearchFilter{Keyword: strings.Repeat("树", 200)}).Normalize(); err != nil {
		t.Fatalf("200 Chinese characters: %v", err)
	}
	rating := 1400
	cases := []QuestionSearchFilter{
		{Type: "unknown"}, {QuizDifficulty: "unknown"},
		{MinDifficulty: &rating, QuizDifficulty: QuizDifficultyEasy},
		{MinDifficulty: intPointer(799)}, {MaxDifficulty: intPointer(3501)},
		{MinDifficulty: intPointer(1500), MaxDifficulty: intPointer(1400)},
		{Page: -1}, {Page: 1000001}, {Size: -1}, {Size: 101},
		{Keyword: strings.Repeat("树", 201)}, {Tag: strings.Repeat("x", 201)},
		{KnowledgePoint: strings.Repeat("x", 201)},
	}
	for _, tc := range cases {
		if _, err := tc.Normalize(); err == nil {
			t.Errorf("accepted invalid filter %+v", tc)
		}
	}
}

func intPointer(n int) *int { return &n }
