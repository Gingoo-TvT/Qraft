package generationapi

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func loadFixtureRequest(t *testing.T) Request {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "product-request-v0.json"))
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var request Request
	if err := decoder.Decode(&request); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	return request
}

func TestProductRequestFixtureValidatesForJobsV1WithoutPaperAudit(t *testing.T) {
	request := loadFixtureRequest(t)
	if len(request.Quality.AuditProfiles) != 0 {
		t.Fatalf("fixture unexpectedly requires audit profiles: %v", request.Quality.AuditProfiles)
	}
	if err := request.ValidateV1(); err != nil {
		t.Fatalf("ValidateV1() error = %v", err)
	}
}

func TestPaperAValidatorIsOptionalAuditProfile(t *testing.T) {
	request := loadFixtureRequest(t)
	if err := request.ValidatePreview(); err != nil {
		t.Fatal(err)
	}
	request.Quality.AuditProfiles = []string{"paper-a-validator-v1"}
	if err := request.ValidatePreview(); err != nil {
		t.Fatalf("optional Paper A profile rejected: %v", err)
	}
}

func TestProductRequestRejectsInvalidCombinationAndCandidateCount(t *testing.T) {
	request := loadFixtureRequest(t)
	request.Domain.Combination.Mode = "single"
	if err := request.ValidatePreview(); err == nil {
		t.Fatal("single mode accepted multiple knowledge points")
	}
	request = loadFixtureRequest(t)
	request.CandidateCount = MaxCandidateCount + 1
	if err := request.ValidatePreview(); err == nil {
		t.Fatal("candidate_count above the contract cap was accepted")
	}
}

func TestResolveModeDefaultsToAdditiveJobsV1(t *testing.T) {
	audit, err := ResolveMode("", false, nil)
	if err != nil {
		t.Fatalf("ResolveMode() error = %v", err)
	}
	if audit.EffectiveMode != ModeJobsV1 || !audit.ProductRouteEnabled ||
		audit.LegacyBehavior != "unchanged" {
		t.Fatalf("audit = %+v", audit)
	}
	legacy, err := ResolveMode(ModeLegacyOnly, true, nil)
	if err != nil || legacy.ProductRouteEnabled || legacy.LegacyBehavior != "unchanged" {
		t.Fatalf("legacy audit = %+v, err = %v", legacy, err)
	}
	preview, err := ResolveMode(ModeContractPreview, true, nil)
	if err != nil {
		t.Fatalf("ResolveMode(contract-preview) error = %v", err)
	}
	if !preview.ContractPreview || preview.ProductRouteEnabled {
		t.Fatalf("preview audit = %+v", preview)
	}
}

func TestResolveModeRejectsUnknownValue(t *testing.T) {
	if _, err := ResolveMode("enabled", true, nil); err == nil {
		t.Fatal("ResolveMode() accepted an unknown product API mode")
	}
}

func TestResolveModeExplicitBlankFailsClosedToLegacyOnly(t *testing.T) {
	audit, err := ResolveMode("", true, nil)
	if err != nil {
		t.Fatalf("ResolveMode(explicit blank) error = %v", err)
	}
	if audit.EffectiveMode != ModeLegacyOnly || audit.ProductRouteEnabled || audit.Source != "flag" {
		t.Fatalf("explicit blank did not fail closed: %+v", audit)
	}
}
