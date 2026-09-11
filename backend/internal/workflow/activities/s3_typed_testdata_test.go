package activities

import (
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
)

func TestEnrichS3TestDataParamsIncludesTypedContract(t *testing.T) {
	params := domain.ProblemGenParams{CustomPrompt: "operator preference"}
	spec := domain.SemanticSpecV1{SchemaVersion: domain.SemanticSpecSchemaV1, ProblemDefinition: "sum"}
	plan := domain.AuthoringPlanV1{
		TestIntents:  []domain.AuthoringTestIntentV1{{Purpose: "singleton", ConstraintRegion: "tiny"}},
		FailureModes: []domain.AuthoringFailureModeV1{{ID: "overflow", Description: "overflow", WitnessIntent: "max"}},
	}
	got, err := enrichS3TestDataParamsV1(params, spec, plan)
	if err != nil {
		t.Fatalf("enrichS3TestDataParamsV1: %v", err)
	}
	for _, want := range []string{"operator preference", "SERVER-AUTHORED TYPED TEST-DATA CONTRACT", "semantic_spec", "singleton", "overflow"} {
		if !strings.Contains(got.CustomPrompt, want) {
			t.Fatalf("typed test-data context missing %q: %s", want, got.CustomPrompt)
		}
	}
}
