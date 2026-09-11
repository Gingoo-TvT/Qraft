package repository

import (
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
)

func TestSameGeneratedQuizTreatsNilAndEmptySlicesAsEqual(t *testing.T) {
	base := &domain.QuizProblem{
		Title:      "title",
		Statement:  "statement",
		Type:       domain.QuizTypeChoice,
		Difficulty: domain.QuizDifficultyEasy,
		Visibility: domain.QuizVisibilityPublic,
		Subject:    "go",
	}
	persisted := *base
	persisted.Options = []domain.QuizOption{}
	persisted.Answers = []string{}
	persisted.Tags = []string{}

	if !sameGeneratedQuiz(&persisted, base) {
		t.Fatal("nil and empty slices should be equivalent after database round trip")
	}
	persisted.Statement = "changed"
	if sameGeneratedQuiz(&persisted, base) {
		t.Fatal("payload change was not detected")
	}
}
