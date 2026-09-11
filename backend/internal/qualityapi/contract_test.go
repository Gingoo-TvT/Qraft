package qualityapi

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestProblemQualityReportCanonicalBytesAndBoundaries(t *testing.T) {
	report := canonicalReportFixture(t)
	first, firstSHA, err := CanonicalProblemQualityReportV1(report)
	if err != nil {
		t.Fatalf("canonical report: %v", err)
	}
	second, secondSHA, err := CanonicalProblemQualityReportV1(report)
	if err != nil {
		t.Fatalf("second canonical report: %v", err)
	}
	if !bytes.Equal(first, second) || firstSHA != secondSHA || firstSHA != SHA256Hex(first) {
		t.Fatal("quality report bytes/hash are not deterministic")
	}
	if bytes.Contains(first, []byte(`"calibrated":true`)) || !bytes.Contains(first, []byte(`"status":"unavailable"`)) ||
		!bytes.Contains(first, []byte(`"import_verified":false`)) {
		t.Fatalf("quality report loses required evidence boundaries: %s", first)
	}
	auditProfile := report
	auditProfile.GenerationEvidenceProfile = "audit"
	if _, _, err := CanonicalProblemQualityReportV1(auditProfile); err != nil {
		t.Fatalf("audit generation evidence profile rejected: %v", err)
	}
	unknownProfile := report
	unknownProfile.GenerationEvidenceProfile = "future"
	if _, _, err := CanonicalProblemQualityReportV1(unknownProfile); err == nil {
		t.Fatal("unknown generation evidence profile was accepted")
	}

	mutations := []struct {
		name   string
		mutate func(*ProblemQualityReportV1)
	}{
		{"gate removed", func(value *ProblemQualityReportV1) { value.Gates = value.Gates[:8] }},
		{"audit drift", func(value *ProblemQualityReportV1) { value.Evidence.Audit.SHA256 = strings.Repeat("f", 64) }},
		{"concept drift", func(value *ProblemQualityReportV1) { value.ConceptSignature.SHA256 = strings.Repeat("f", 64) }},
		{"structural drift", func(value *ProblemQualityReportV1) { value.StructuralSignature.SHA256 = strings.Repeat("f", 64) }},
		{"difficulty claimed calibrated", func(value *ProblemQualityReportV1) { value.Difficulty.Calibrated = true }},
		{"cost claimed available", func(value *ProblemQualityReportV1) { value.Cost.Status = "available" }},
		{"external import claimed", func(value *ProblemQualityReportV1) { value.ExternalOJ.ImportVerified = true }},
	}
	for _, test := range mutations {
		t.Run(test.name, func(t *testing.T) {
			value := canonicalReportFixture(t)
			test.mutate(&value)
			if _, _, err := CanonicalProblemQualityReportV1(value); err == nil {
				t.Fatal("invalid report was accepted")
			}
		})
	}
}

func TestConceptAndStructuralSignaturesAreCanonical(t *testing.T) {
	first, err := NewConceptSignatureV1([]string{" Graphs ", "DP"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewConceptSignatureV1([]string{"dp", "graphs"})
	if err != nil {
		t.Fatal(err)
	}
	if first.SHA256 != second.SHA256 || strings.Join(first.Values, ",") != "dp,graphs" {
		t.Fatalf("concept signatures differ: %+v %+v", first, second)
	}
	if _, err := NewConceptSignatureV1([]string{"dp", "DP"}); err == nil {
		t.Fatal("duplicate normalized concept was accepted")
	}

	structural, err := NewStructuralSignatureV1(strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 64))
	if err != nil || structural.SHA256 == "" {
		t.Fatalf("structural signature = %+v err=%v", structural, err)
	}
}

func TestDecodeProblemQualityBatchRequestStrictOrderedUniqueAndBounded(t *testing.T) {
	firstID := "11111111-1111-1111-1111-111111111111"
	secondID := "22222222-2222-2222-2222-222222222222"
	input := []byte(`{"schema_version":"` + ProblemQualityBatchRequestSchemaV1 + `","problem_ids":["` + firstID + `","` + secondID + `"]}`)
	request, canonical, digest, err := DecodeProblemQualityBatchRequestV1(input)
	if err != nil {
		t.Fatalf("decode canonical request: %v", err)
	}
	if len(request.ProblemIDs) != 2 || digest != SHA256Hex(canonical) {
		t.Fatalf("decoded request = %+v sha=%s", request, digest)
	}
	_, canonicalAgain, digestAgain, err := DecodeProblemQualityBatchRequestV1(append([]byte(" \n"), input...))
	if err != nil || !bytes.Equal(canonical, canonicalAgain) || digest != digestAgain {
		t.Fatal("equivalent strict JSON did not canonicalize deterministically")
	}

	tooMany := make([]string, MaxBatchProblemIDsV1+1)
	for index := range tooMany {
		tooMany[index] = strings.Repeat("0", 4) + strings.Repeat("0", 4) + "-0000-0000-0000-" + strings.Repeat("0", 8) + strings.Repeat("0", 3) + string(rune('0'+index%10))
	}
	tooManyJSON, err := json.Marshal(ProblemQualityBatchRequestV1{SchemaVersion: ProblemQualityBatchRequestSchemaV1, ProblemIDs: tooMany})
	if err != nil {
		t.Fatal(err)
	}

	invalid := map[string][]byte{
		"missing":         []byte(`{}`),
		"wrong schema":    []byte(`{"schema_version":"wrong","problem_ids":["` + firstID + `"]}`),
		"unknown":         []byte(`{"schema_version":"` + ProblemQualityBatchRequestSchemaV1 + `","problem_ids":["` + firstID + `"],"extra":true}`),
		"duplicate field": []byte(`{"schema_version":"` + ProblemQualityBatchRequestSchemaV1 + `","problem_ids":["` + firstID + `"],"problem_ids":["` + secondID + `"]}`),
		"trailing":        append(append([]byte{}, input...), []byte(` {}`)...),
		"duplicate id":    []byte(`{"schema_version":"` + ProblemQualityBatchRequestSchemaV1 + `","problem_ids":["` + firstID + `","` + firstID + `"]}`),
		"unsorted":        []byte(`{"schema_version":"` + ProblemQualityBatchRequestSchemaV1 + `","problem_ids":["` + secondID + `","` + firstID + `"]}`),
		"bad uuid":        []byte(`{"schema_version":"` + ProblemQualityBatchRequestSchemaV1 + `","problem_ids":["NOT-A-UUID"]}`),
		"too many":        tooManyJSON,
		"oversize":        []byte(`{"schema_version":"` + ProblemQualityBatchRequestSchemaV1 + `","problem_ids":["` + firstID + `"],"` + strings.Repeat("x", MaxBatchRequestBytesV1) + `":0}`),
	}
	for name, body := range invalid {
		t.Run(name, func(t *testing.T) {
			if _, _, _, err := DecodeProblemQualityBatchRequestV1(body); err == nil {
				t.Fatal("invalid batch request was accepted")
			}
		})
	}
}

func TestProblemQualityBatchManifestCanonical(t *testing.T) {
	request := ProblemQualityBatchRequestV1{SchemaVersion: ProblemQualityBatchRequestSchemaV1, ProblemIDs: []string{"11111111-1111-1111-1111-111111111111"}}
	_, requestSHA, err := CanonicalProblemQualityBatchRequestV1(request)
	if err != nil {
		t.Fatal(err)
	}
	prefix := "/api/v1/problems/" + request.ProblemIDs[0]
	manifest := ProblemQualityBatchManifestV1{
		SchemaVersion: ProblemQualityBatchManifestSchemaV1, RequestSchemaVersion: ProblemQualityBatchRequestSchemaV1,
		RequestSHA256: requestSHA, ExternalOJImportVerified: false,
		Items: []ProblemQualityBatchItemV1{{
			ProblemID: request.ProblemIDs[0], QualityReportSHA256: strings.Repeat("a", 64),
			AuditSHA256: strings.Repeat("b", 64), TestManifestSHA256: strings.Repeat("c", 64),
			QualityURI: prefix + "/quality", AuditURI: prefix + "/quality/audit", TestManifestURI: prefix + "/test-manifest",
		}},
	}
	first, firstSHA, err := CanonicalProblemQualityBatchManifestV1(manifest)
	if err != nil {
		t.Fatal(err)
	}
	second, secondSHA, err := CanonicalProblemQualityBatchManifestV1(manifest)
	if err != nil || !bytes.Equal(first, second) || firstSHA != secondSHA {
		t.Fatal("batch manifest is not deterministic")
	}
	manifest.ExternalOJImportVerified = true
	if _, _, err := CanonicalProblemQualityBatchManifestV1(manifest); err == nil {
		t.Fatal("external OJ verification claim was accepted")
	}
}

func canonicalReportFixture(t *testing.T) ProblemQualityReportV1 {
	t.Helper()
	problemID := "11111111-1111-1111-1111-111111111111"
	auditSHA := strings.Repeat("a", 64)
	manifestSHA := strings.Repeat("b", 64)
	concept, err := NewConceptSignatureV1([]string{"dp", "graphs"})
	if err != nil {
		t.Fatal(err)
	}
	structural, err := NewStructuralSignatureV1(strings.Repeat("c", 64), strings.Repeat("d", 64), strings.Repeat("e", 64))
	if err != nil {
		t.Fatal(err)
	}
	gates := make([]QualityGateV1, len(gateOrderV1))
	for index, name := range gateOrderV1 {
		gates[index] = QualityGateV1{Name: name, Status: "pass"}
	}
	return ProblemQualityReportV1{
		SchemaVersion: ProblemQualityReportSchemaV1, ProblemID: problemID,
		WorkflowID: "algoforge-generation-v1-" + strings.Repeat("1", 64), SubjectRevision: strings.Repeat("f", 64),
		GenerationEvidenceProfile: "minimal", Decision: "pass",
		Evidence: QualityEvidenceV1{
			Audit:        EvidenceBindingV1{Kind: "quality_audit", SchemaVersion: "algoforge.s3-quality-audit.v1", SHA256: auditSHA, SizeBytes: 123, Producer: "RecomputeS3VerdictActivityV1", URI: "/api/v1/problems/" + problemID + "/quality/audit"},
			TestManifest: EvidenceBindingV1{Kind: "test_manifest", SchemaVersion: "algoforge.test-manifest.v2", SHA256: manifestSHA, SizeBytes: 456, Producer: "BuildS3TestManifestActivityV1", URI: "/api/v1/problems/" + problemID + "/test-manifest"},
		},
		Gates: gates,
		Layers: []QualityLayerV1{
			{Name: LayerStandard, Status: LayerStatusPass, Basis: "nine gates", EvidenceSHA256: auditSHA},
			{Name: LayerVerified, Status: LayerStatusPass, Basis: "CAS", EvidenceSHA256: manifestSHA},
			{Name: LayerPublicationReady, Status: LayerStatusPendingExplicitApproval, Basis: "approval required", EvidenceSHA256: strings.Repeat("f", 64)},
		},
		ConceptSignature: concept, StructuralSignature: structural,
		Difficulty: DifficultyAdvisoryV1{GenerationTarget: 1500, Calibrated: false, Source: "cas_bound_authoring_plan_generation_target_advisory"},
		Cost:       CostStatusV1{Status: CostStatusUnavailable, Source: "not_recomputable_from_persisted_s3_evidence"},
		ExternalOJ: ExternalOJBoundaryV1{ImportVerified: false, Source: "outside_algoforge_quality_evidence"},
	}
}
