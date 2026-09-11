package generationapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const (
	EvidenceBundleSchemaV0      = "algoforge.generation-evidence-bundle.v0"
	PublicationDecisionSchemaV0 = "algoforge.publication-decision.v0"
)

type EvidenceBundleIdentity struct {
	SchemaVersion string `json:"schema_version"`
	Profile       string `json:"profile"`
	SHA256        string `json:"sha256"`
}

type canonicalEvidenceItemV0 struct {
	Kind   string `json:"kind"`
	SHA256 string `json:"sha256"`
}

type canonicalEvidenceBundleV0 struct {
	SchemaVersion string                    `json:"schema_version"`
	Profile       string                    `json:"profile"`
	Items         []canonicalEvidenceItemV0 `json:"items"`
}

// BuildEvidenceBundleIdentityV0 hashes content identities only. A URI is a
// mutable locator and therefore cannot change the evidence bundle identity.
func BuildEvidenceBundleIdentityV0(refs []EvidenceRef) (EvidenceBundleIdentity, error) {
	items := make([]canonicalEvidenceItemV0, 0, len(refs))
	seen := make(map[canonicalEvidenceItemV0]struct{}, len(refs))
	for _, ref := range refs {
		item := canonicalEvidenceItemV0{
			Kind:   strings.TrimSpace(ref.Kind),
			SHA256: strings.ToLower(strings.TrimSpace(ref.SHA256)),
		}
		if item.Kind == "" || !isCanonicalSHA256(item.SHA256) {
			return EvidenceBundleIdentity{}, fmt.Errorf("invalid evidence identity")
		}
		if _, ok := seen[item]; ok {
			return EvidenceBundleIdentity{}, fmt.Errorf("duplicate evidence identity")
		}
		seen[item] = struct{}{}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Kind == items[j].Kind {
			return items[i].SHA256 < items[j].SHA256
		}
		return items[i].Kind < items[j].Kind
	})
	encoded, err := json.Marshal(canonicalEvidenceBundleV0{
		SchemaVersion: EvidenceBundleSchemaV0,
		Profile:       EvidenceProfileV0,
		Items:         items,
	})
	if err != nil {
		return EvidenceBundleIdentity{}, fmt.Errorf("encode evidence bundle identity: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return EvidenceBundleIdentity{
		SchemaVersion: EvidenceBundleSchemaV0,
		Profile:       EvidenceProfileV0,
		SHA256:        hex.EncodeToString(digest[:]),
	}, nil
}

type publicationDecisionV0 struct {
	SchemaVersion string `json:"schema_version"`
	GateVersion   string `json:"gate_version"`
	PolicyVersion string `json:"policy_version"`
	Status        string `json:"status"`
	Reason        string `json:"reason"`
}

func PublicationDecisionEvidenceV0(
	gateVersion string,
	policyVersion string,
	status string,
	reason string,
) (EvidenceRef, error) {
	decision := publicationDecisionV0{
		SchemaVersion: PublicationDecisionSchemaV0,
		GateVersion:   strings.TrimSpace(gateVersion),
		PolicyVersion: strings.TrimSpace(policyVersion),
		Status:        strings.TrimSpace(status),
		Reason:        strings.TrimSpace(reason),
	}
	if decision.GateVersion == "" || decision.Status == "" {
		return EvidenceRef{}, fmt.Errorf("publication decision evidence is incomplete")
	}
	switch decision.Status {
	case "published":
		// A successful publication decision has no quarantine reason.
	case "quarantined":
		if decision.Reason == "" {
			return EvidenceRef{}, fmt.Errorf("publication decision evidence is incomplete")
		}
	default:
		return EvidenceRef{}, fmt.Errorf("publication decision evidence has unsupported status %q", decision.Status)
	}
	encoded, err := json.Marshal(decision)
	if err != nil {
		return EvidenceRef{}, fmt.Errorf("encode publication decision evidence: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return EvidenceRef{
		Kind:   "publication_decision",
		SHA256: hex.EncodeToString(digest[:]),
	}, nil
}

func isCanonicalSHA256(value string) bool {
	if len(value) != sha256.Size*2 || value != strings.ToLower(value) {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}
