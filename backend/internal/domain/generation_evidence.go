package domain

import (
	"encoding/hex"
	"fmt"
	"strings"
)

const (
	GenerationEvidenceContractSchemaV1       = "algoforge.generation-standard-evidence-request.v1"
	GenerationStandardEvidenceSchemaV1       = "algoforge.generation-standard-evidence.v1"
	GenerationStandardEvidenceLevel          = "standard"
	GenerationStandardEvidenceRequestMetaKey = "generation_standard_evidence_request"
)

// GenerationEvidenceContract is the server-authored, replay-stable request
// for a downloadable standard evidence receipt. Nil preserves legacy and
// minimal generation behavior.
type GenerationEvidenceContract struct {
	SchemaVersion                   string `json:"schema_version"`
	EvidenceLevel                   string `json:"evidence_level"`
	JobContractVersion              string `json:"job_contract_version"`
	GenerationArm                   string `json:"generation_arm"`
	GenerationArmMode               string `json:"generation_arm_mode"`
	GenerationBehavior              string `json:"generation_behavior"`
	ReviewerProfile                 string `json:"reviewer_profile"`
	ReviewerProfileDescriptorSHA256 string `json:"reviewer_profile_descriptor_sha256"`
	EvidenceProfile                 string `json:"evidence_profile"`
	EvidenceProfileDescriptorSHA256 string `json:"evidence_profile_descriptor_sha256"`
	OutcomeTaxonomy                 string `json:"outcome_taxonomy"`
	IncludeEditorial                bool   `json:"include_editorial"`
	IncludeSolutions                bool   `json:"include_solutions"`
	IncludeTestData                 bool   `json:"include_test_data"`
}

// Validate rejects partial contracts before a workflow starts. Product-level
// exact identities are checked by the generation API and receipt builder.
func (contract *GenerationEvidenceContract) Validate() error {
	if contract == nil {
		return nil
	}
	if contract.SchemaVersion != GenerationEvidenceContractSchemaV1 {
		return fmt.Errorf("unsupported schema_version %q", contract.SchemaVersion)
	}
	if contract.EvidenceLevel != GenerationStandardEvidenceLevel {
		return fmt.Errorf("unsupported evidence_level %q", contract.EvidenceLevel)
	}
	for name, value := range map[string]string{
		"job_contract_version": contract.JobContractVersion,
		"generation_arm":       contract.GenerationArm,
		"generation_arm_mode":  contract.GenerationArmMode,
		"generation_behavior":  contract.GenerationBehavior,
		"reviewer_profile":     contract.ReviewerProfile,
		"evidence_profile":     contract.EvidenceProfile,
		"outcome_taxonomy":     contract.OutcomeTaxonomy,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required", name)
		}
	}
	for name, value := range map[string]string{
		"reviewer_profile_descriptor_sha256": contract.ReviewerProfileDescriptorSHA256,
		"evidence_profile_descriptor_sha256": contract.EvidenceProfileDescriptorSHA256,
	} {
		if !isGenerationEvidenceSHA256(value) {
			return fmt.Errorf("%s must be a canonical sha256", name)
		}
	}
	if !contract.IncludeEditorial || !contract.IncludeSolutions || !contract.IncludeTestData {
		return fmt.Errorf("standard evidence requires editorial, solutions, and test data")
	}
	return nil
}

// GenerationStandardEvidenceReference is the immutable database binding for
// the receipt body stored at a stable object path.
type GenerationStandardEvidenceReference struct {
	SchemaVersion string `json:"schema_version"`
	SHA256        string `json:"sha256"`
	Path          string `json:"path"`
}

// GenerationStandardEvidenceBinding keeps the immutable receipt reference
// outside Problem.MetadataJSON. Metadata participates in the governed problem
// content identity, so a post-gate receipt must not be attached there.
type GenerationStandardEvidenceBinding struct {
	Reference        GenerationStandardEvidenceReference `json:"reference"`
	FinalStatus      ProblemStatus                       `json:"final_status"`
	QuarantineReason string                              `json:"quarantine_reason,omitempty"`
	OutcomeCategory  string                              `json:"outcome_category"`
	OutcomeKind      string                              `json:"outcome_kind"`
	OutcomeSHA256    string                              `json:"outcome_sha256"`
}

func (binding GenerationStandardEvidenceBinding) Validate(problemID string) error {
	expectedPath := "problems/" + problemID + "/generation_standard_evidence.v1.json"
	if err := binding.Reference.Validate(expectedPath); err != nil {
		return fmt.Errorf("reference: %w", err)
	}
	switch binding.FinalStatus {
	case ProblemStatusPublished:
		if strings.TrimSpace(binding.QuarantineReason) != "" {
			return fmt.Errorf("published evidence must not have a quarantine reason")
		}
	case ProblemStatusQuarantined:
		if strings.TrimSpace(binding.QuarantineReason) == "" {
			return fmt.Errorf("quarantined evidence requires a quarantine reason")
		}
	default:
		return fmt.Errorf("unsupported final_status %q", binding.FinalStatus)
	}
	if !isGenerationEvidenceSHA256(binding.OutcomeSHA256) {
		return fmt.Errorf("outcome_sha256 must be canonical")
	}
	switch binding.OutcomeCategory {
	case "review":
		if binding.FinalStatus != ProblemStatusQuarantined || binding.OutcomeKind != "review_result" {
			return fmt.Errorf("review outcome requires quarantined status and review_result evidence")
		}
	case "publication_eligibility":
		if binding.OutcomeKind != "publication_decision" {
			return fmt.Errorf("publication outcome requires publication_decision evidence")
		}
	default:
		return fmt.Errorf("unsupported outcome_category %q", binding.OutcomeCategory)
	}
	return nil
}

func (ref GenerationStandardEvidenceReference) Validate(expectedPath string) error {
	if ref.SchemaVersion != GenerationStandardEvidenceSchemaV1 {
		return fmt.Errorf("unsupported schema_version %q", ref.SchemaVersion)
	}
	if !isGenerationEvidenceSHA256(ref.SHA256) {
		return fmt.Errorf("sha256 must be canonical")
	}
	if ref.Path != expectedPath {
		return fmt.Errorf("path %q does not match expected path", ref.Path)
	}
	return nil
}

func isGenerationEvidenceSHA256(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32
}
