package generationapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
)

type generationStandardEvidenceOutcomeV1 struct {
	JobStatus       string          `json:"job_status"`
	ProblemStatus   string          `json:"problem_status"`
	OutcomeCategory OutcomeCategory `json:"outcome_category"`
}

type generationStandardEvidenceReceiptV1 struct {
	SchemaVersion string                              `json:"schema_version"`
	Contract      domain.GenerationEvidenceContract   `json:"contract"`
	FinalOutcome  generationStandardEvidenceOutcomeV1 `json:"final_outcome"`
	Evidence      []canonicalEvidenceItemV0           `json:"evidence"`
}

// CanonicalGenerationStandardEvidenceV1 returns the exact receipt bytes and
// their content digest. Locators, model bodies, workflow identities, and raw
// review text are deliberately outside this product evidence surface.
func CanonicalGenerationStandardEvidenceV1(
	contract *domain.GenerationEvidenceContract,
	problemStatus domain.ProblemStatus,
	outcomeCategory OutcomeCategory,
	refs []EvidenceRef,
) ([]byte, string, error) {
	if err := validateCurrentGenerationEvidenceContract(contract); err != nil {
		return nil, "", err
	}

	jobStatus := ""
	switch problemStatus {
	case domain.ProblemStatusPublished:
		jobStatus = JobStatusSucceeded
		if outcomeCategory != OutcomeCategoryPublicationEligibility {
			return nil, "", fmt.Errorf("published receipt requires publication_eligibility outcome")
		}
	case domain.ProblemStatusQuarantined:
		jobStatus = JobStatusQuarantined
		if outcomeCategory != OutcomeCategoryReview && outcomeCategory != OutcomeCategoryPublicationEligibility {
			return nil, "", fmt.Errorf("quarantined receipt has unsupported outcome category %q", outcomeCategory)
		}
	default:
		return nil, "", fmt.Errorf("unsupported final problem status %q", problemStatus)
	}

	items := make([]canonicalEvidenceItemV0, 0, len(refs))
	seen := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		item := canonicalEvidenceItemV0{
			Kind:   strings.TrimSpace(ref.Kind),
			SHA256: strings.ToLower(strings.TrimSpace(ref.SHA256)),
		}
		if item.Kind == "" || !isCanonicalSHA256(item.SHA256) {
			return nil, "", fmt.Errorf("invalid standard evidence identity")
		}
		if _, exists := seen[item.Kind]; exists {
			return nil, "", fmt.Errorf("duplicate standard evidence kind %q", item.Kind)
		}
		seen[item.Kind] = struct{}{}
		items = append(items, item)
	}
	expectedOutcomeKind := "publication_decision"
	if outcomeCategory == OutcomeCategoryReview {
		expectedOutcomeKind = "review_result"
	}
	if len(items) != 2 {
		return nil, "", fmt.Errorf("standard evidence requires test_manifest and one outcome identity")
	}
	if _, ok := seen["test_manifest"]; !ok {
		return nil, "", fmt.Errorf("standard evidence is missing test_manifest")
	}
	if _, ok := seen[expectedOutcomeKind]; !ok {
		return nil, "", fmt.Errorf("standard evidence is missing %s", expectedOutcomeKind)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Kind == items[j].Kind {
			return items[i].SHA256 < items[j].SHA256
		}
		return items[i].Kind < items[j].Kind
	})

	encoded, err := json.Marshal(generationStandardEvidenceReceiptV1{
		SchemaVersion: domain.GenerationStandardEvidenceSchemaV1,
		Contract:      *contract,
		FinalOutcome: generationStandardEvidenceOutcomeV1{
			JobStatus:       jobStatus,
			ProblemStatus:   string(problemStatus),
			OutcomeCategory: outcomeCategory,
		},
		Evidence: items,
	})
	if err != nil {
		return nil, "", fmt.Errorf("encode generation standard evidence: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return encoded, hex.EncodeToString(digest[:]), nil
}

func validateCurrentGenerationEvidenceContract(contract *domain.GenerationEvidenceContract) error {
	if contract == nil {
		return fmt.Errorf("generation evidence contract is required")
	}
	if err := contract.Validate(); err != nil {
		return fmt.Errorf("invalid generation evidence contract: %w", err)
	}
	if contract.JobContractVersion != JobContractVersion ||
		contract.GenerationArm != GenerationArmBaseline ||
		contract.GenerationArmMode != GenerationArmModeBaseline ||
		contract.GenerationBehavior != "unchanged" ||
		contract.ReviewerProfile != ReviewerProfileV1 ||
		!isAcceptedReviewerProfileV1Descriptor(contract.ReviewerProfileDescriptorSHA256) ||
		contract.EvidenceProfile != EvidenceProfileV0 ||
		contract.EvidenceProfileDescriptorSHA256 != EvidenceProfileDescriptorSHA256 ||
		contract.OutcomeTaxonomy != OutcomeTaxonomyVersion {
		return fmt.Errorf("generation evidence contract does not match the current jobs v1 identities")
	}
	return nil
}

// isAcceptedReviewerProfileV1Descriptor keeps verification of historical
// standard receipts compatible with the pre-visibility-boundary prompt while
// making the rotated digest the identity emitted for all new jobs.
func isAcceptedReviewerProfileV1Descriptor(value string) bool {
	return value == ReviewerProfileV1DescriptorSHA256 || value == ReviewerProfileV1LegacyDescriptorSHA256
}

// ValidateGenerationEvidenceContractV1 verifies the complete server-authored
// identity without requiring receipt outcome data.
func ValidateGenerationEvidenceContractV1(contract *domain.GenerationEvidenceContract) error {
	return validateCurrentGenerationEvidenceContract(contract)
}
