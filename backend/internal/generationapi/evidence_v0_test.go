package generationapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

func TestEvidenceV0DescriptorIsFrozen(t *testing.T) {
	descriptor, err := json.Marshal(struct {
		SchemaVersion string   `json:"schema_version"`
		BundleSchema  string   `json:"bundle_schema"`
		Fields        []string `json:"canonical_fields"`
		Sort          []string `json:"sort"`
		URIInIdentity bool     `json:"uri_in_identity"`
	}{
		SchemaVersion: EvidenceProfileV0,
		BundleSchema:  EvidenceBundleSchemaV0,
		Fields:        []string{"kind", "sha256"},
		Sort:          []string{"kind", "sha256"},
		URIInIdentity: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(descriptor)
	got := hex.EncodeToString(digest[:])
	if got != EvidenceProfileDescriptorSHA256 {
		t.Fatalf("evidence_v0 descriptor changed: got=%s want=%s", got, EvidenceProfileDescriptorSHA256)
	}
}

func TestEvidenceBundleIdentityV0IsOrderStableAndIgnoresURI(t *testing.T) {
	a := strings.Repeat("a", 64)
	b := strings.Repeat("b", 64)
	first, err := BuildEvidenceBundleIdentityV0([]EvidenceRef{
		{Kind: "test_manifest", SHA256: a, URI: "/first"},
		{Kind: "review_result", SHA256: b, URI: "/second"},
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildEvidenceBundleIdentityV0([]EvidenceRef{
		{Kind: "review_result", SHA256: b, URI: "/changed"},
		{Kind: "test_manifest", SHA256: a},
	})
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("evidence identity changed with order/URI: %+v != %+v", first, second)
	}
	const wantSHA256 = "c5e1a4fd6bddade762099a9a683bd32f17ac096e29d5e053a9c5d44f155287dc"
	if first.SHA256 != wantSHA256 {
		t.Fatalf("evidence bundle golden SHA changed: got=%s want=%s", first.SHA256, wantSHA256)
	}
	changed, err := BuildEvidenceBundleIdentityV0([]EvidenceRef{
		{Kind: "test_manifest", SHA256: b},
		{Kind: "review_result", SHA256: a},
	})
	if err != nil {
		t.Fatal(err)
	}
	if changed.SHA256 == first.SHA256 {
		t.Fatal("evidence identity did not bind kind/hash pairs")
	}
}

func TestEvidenceBundleIdentityV0RejectsInvalidOrDuplicateRefs(t *testing.T) {
	valid := EvidenceRef{Kind: "test_manifest", SHA256: strings.Repeat("a", 64)}
	for _, refs := range [][]EvidenceRef{
		{{Kind: "", SHA256: valid.SHA256}},
		{{Kind: valid.Kind, SHA256: "not-a-sha"}},
		{valid, valid},
	} {
		if _, err := BuildEvidenceBundleIdentityV0(refs); err == nil {
			t.Fatalf("invalid refs accepted: %+v", refs)
		}
	}
}

func TestPublicationDecisionEvidenceV0IsContentSensitive(t *testing.T) {
	first, err := PublicationDecisionEvidenceV0(
		"2026-08-16.t10", "policy-v1", "quarantined", "rights unknown",
	)
	if err != nil {
		t.Fatal(err)
	}
	second, err := PublicationDecisionEvidenceV0(
		"2026-08-16.t10", "policy-v1", "quarantined", "different reason",
	)
	if err != nil {
		t.Fatal(err)
	}
	if first.Kind != "publication_decision" || first.SHA256 == second.SHA256 {
		t.Fatalf("publication evidence = %+v / %+v", first, second)
	}
	const wantSHA256 = "30312a3698e52eb66c3ed8f8f0346a750ac0cedb162ee04284489f0b5707fb94"
	if first.SHA256 != wantSHA256 {
		t.Fatalf("publication decision golden SHA changed: got=%s want=%s", first.SHA256, wantSHA256)
	}
}

func TestPublicationDecisionEvidenceV0BindsSuccessfulEmptyReason(t *testing.T) {
	ref, err := PublicationDecisionEvidenceV0(
		"2026-08-16.t10", "policy-v1", "published", "",
	)
	if err != nil {
		t.Fatalf("published decision rejected: %v", err)
	}
	if ref.Kind != "publication_decision" || !isCanonicalSHA256(ref.SHA256) {
		t.Fatalf("published decision evidence = %+v", ref)
	}
	if _, err := PublicationDecisionEvidenceV0("2026-08-16.t10", "policy-v1", "quarantined", ""); err == nil {
		t.Fatal("quarantined decision without reason was accepted")
	}
}
