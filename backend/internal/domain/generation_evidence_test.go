package domain

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestGenerationStandardEvidenceBindingValidation(t *testing.T) {
	problemID := uuid.New().String()
	validRef := GenerationStandardEvidenceReference{
		SchemaVersion: GenerationStandardEvidenceSchemaV1,
		SHA256:        strings.Repeat("a", 64),
		Path:          "problems/" + problemID + "/generation_standard_evidence.v1.json",
	}
	for _, binding := range []GenerationStandardEvidenceBinding{
		{Reference: validRef, FinalStatus: ProblemStatusPublished, OutcomeCategory: "publication_eligibility", OutcomeKind: "publication_decision", OutcomeSHA256: strings.Repeat("b", 64)},
		{Reference: validRef, FinalStatus: ProblemStatusQuarantined, QuarantineReason: "quality review denied", OutcomeCategory: "review", OutcomeKind: "review_result", OutcomeSHA256: strings.Repeat("b", 64)},
	} {
		if err := binding.Validate(problemID); err != nil {
			t.Fatalf("valid binding rejected: %v", err)
		}
	}
	invalid := []GenerationStandardEvidenceBinding{
		{Reference: validRef, FinalStatus: ProblemStatusPublished, QuarantineReason: "stale reason", OutcomeCategory: "publication_eligibility", OutcomeKind: "publication_decision", OutcomeSHA256: strings.Repeat("b", 64)},
		{Reference: validRef, FinalStatus: ProblemStatusQuarantined, OutcomeCategory: "review", OutcomeKind: "review_result", OutcomeSHA256: strings.Repeat("b", 64)},
		{Reference: validRef, FinalStatus: ProblemStatusDraft, OutcomeCategory: "publication_eligibility", OutcomeKind: "publication_decision", OutcomeSHA256: strings.Repeat("b", 64)},
		{Reference: validRef, FinalStatus: ProblemStatusQuarantined, QuarantineReason: "denied", OutcomeCategory: "review", OutcomeKind: "publication_decision", OutcomeSHA256: strings.Repeat("b", 64)},
	}
	for _, binding := range invalid {
		if err := binding.Validate(problemID); err == nil {
			t.Fatalf("invalid binding accepted: %+v", binding)
		}
	}
}
