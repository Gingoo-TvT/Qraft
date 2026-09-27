package domain

import (
	"testing"

	"github.com/google/uuid"
)

func TestStoredProblemRatingIndependentOfGenerationLevel(t *testing.T) {
	for _, level := range []ProblemLevel{LevelAlgorithm, LevelSyntax, LevelGPLTL1, LevelGPLTL2, LevelGPLTL3} {
		for _, rating := range []int{800, 1000, 1300, 3500} {
			p := Problem{ID: uuid.New(), Title: "Edited synthetic problem", Statement: "Compute a value.", Level: level, Difficulty: rating, Status: ProblemStatusDraft, TimeLimit: 1000, MemoryLimit: 128}
			if err := p.Validate(); err != nil {
				t.Errorf("level=%s rating=%d: %v", level, rating, err)
			}
		}
	}
	for _, rating := range []int{0, 700, 1050, 3600} {
		p := Problem{ID: uuid.New(), Title: "Synthetic", Statement: "Compute.", Level: LevelAlgorithm, Difficulty: rating, Status: ProblemStatusDraft, TimeLimit: 1000, MemoryLimit: 128}
		if p.Validate() == nil {
			t.Errorf("invalid rating %d accepted", rating)
		}
	}
	// Authoring still follows the configured level's target range.
	params := DefaultProblemGenParams()
	params.Level, params.Difficulty = LevelAlgorithm, 1000
	if params.Validate() == nil {
		t.Fatal("generation target validation was relaxed")
	}
}
