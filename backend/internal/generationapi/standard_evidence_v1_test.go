package generationapi

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
)

func TestGenerationStandardEvidenceV1IsCanonicalAndSafe(t *testing.T) {
	request := loadFixtureRequest(t)
	request.Output.EvidenceLevel = EvidenceStandard
	contract := request.ToProblemGenParams().GenerationEvidence
	manifest := EvidenceRef{Kind: "test_manifest", SHA256: strings.Repeat("a", 64), URI: "/mutable/manifest"}
	decision := EvidenceRef{Kind: "publication_decision", SHA256: strings.Repeat("b", 64), URI: "/mutable/decision"}

	first, firstSHA, err := CanonicalGenerationStandardEvidenceV1(
		contract,
		domain.ProblemStatusPublished,
		OutcomeCategoryPublicationEligibility,
		[]EvidenceRef{manifest, decision},
	)
	if err != nil {
		t.Fatal(err)
	}
	second, secondSHA, err := CanonicalGenerationStandardEvidenceV1(
		contract,
		domain.ProblemStatusPublished,
		OutcomeCategoryPublicationEligibility,
		[]EvidenceRef{{Kind: decision.Kind, SHA256: decision.SHA256}, {Kind: manifest.Kind, SHA256: manifest.SHA256}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) || firstSHA != secondSHA || !isCanonicalSHA256(firstSHA) {
		t.Fatalf("standard receipt is not canonical: %s/%s", firstSHA, secondSHA)
	}
	for _, forbidden := range []string{"mutable/", "source_artifacts", "workflow_id", "run_id", "activity_id", "full_text", "bucket", "object_key"} {
		if bytes.Contains(first, []byte(forbidden)) {
			t.Fatalf("standard receipt exposed %q: %s", forbidden, first)
		}
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(first, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["schema_version"] != domain.GenerationStandardEvidenceSchemaV1 {
		t.Fatalf("receipt schema = %v", decoded["schema_version"])
	}
}

func TestGenerationStandardEvidenceV1RejectsOutcomeDrift(t *testing.T) {
	request := loadFixtureRequest(t)
	request.Output.EvidenceLevel = EvidenceStandard
	contract := request.ToProblemGenParams().GenerationEvidence
	manifest := EvidenceRef{Kind: "test_manifest", SHA256: strings.Repeat("a", 64)}
	review := EvidenceRef{Kind: "review_result", SHA256: strings.Repeat("b", 64)}

	if _, _, err := CanonicalGenerationStandardEvidenceV1(contract, domain.ProblemStatusPublished, OutcomeCategoryReview, []EvidenceRef{manifest, review}); err == nil {
		t.Fatal("published review outcome was accepted")
	}
	if _, _, err := CanonicalGenerationStandardEvidenceV1(contract, domain.ProblemStatusQuarantined, OutcomeCategoryReview, []EvidenceRef{manifest}); err == nil {
		t.Fatal("receipt without outcome identity was accepted")
	}
	bad := *contract
	bad.ReviewerProfile = ReviewerProfileV0
	if _, _, err := CanonicalGenerationStandardEvidenceV1(&bad, domain.ProblemStatusQuarantined, OutcomeCategoryReview, []EvidenceRef{manifest, review}); err == nil {
		t.Fatal("drifted standard evidence contract was accepted")
	}
}
