// Package qualityapi defines the stable, deterministic product projection for
// problem-quality evidence. It deliberately contains no repository, object
// store, workflow, or HTTP dependencies.
package qualityapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"sort"
	"strings"
)

const (
	ProblemQualityReportSchemaV1        = "algoforge.problem-quality-report.v1"
	ConceptSignatureSchemaV1            = "algoforge.problem-concept-signature.v1"
	StructuralSignatureSchemaV1         = "algoforge.problem-structural-signature.v1"
	ProblemQualityBatchRequestSchemaV1  = "algoforge.problem-quality-batch-request.v1"
	ProblemQualityBatchManifestSchemaV1 = "algoforge.problem-quality-batch-manifest.v1"
	MaxBatchProblemIDsV1                = 500
	MaxBatchRequestBytesV1              = 64 << 10

	LayerStandard         = "standard"
	LayerVerified         = "verified"
	LayerPublicationReady = "publication_ready"

	LayerStatusPass                    = "pass"
	LayerStatusReady                   = "ready"
	LayerStatusPendingExplicitApproval = "pending_explicit_approval"

	CostStatusUnavailable = "unavailable"
)

var (
	canonicalSHA256V1 = regexp.MustCompile(`^[a-f0-9]{64}$`)
	canonicalUUIDV1   = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)
)

var gateOrderV1 = []string{
	"spec_lint",
	"sample_output_binding",
	"oracle_differential",
	"sanitizer",
	"boundary_coverage",
	"test_manifest",
	"reviewer_schema_verdict",
	"dedup",
	"hidden_regression",
}

type ProblemQualityReportV1 struct {
	SchemaVersion             string                `json:"schema_version"`
	ProblemID                 string                `json:"problem_id"`
	WorkflowID                string                `json:"workflow_id"`
	SubjectRevision           string                `json:"subject_revision"`
	GenerationEvidenceProfile string                `json:"generation_evidence_profile"`
	Decision                  string                `json:"decision"`
	Evidence                  QualityEvidenceV1     `json:"evidence"`
	Gates                     []QualityGateV1       `json:"gates"`
	Layers                    []QualityLayerV1      `json:"layers"`
	ConceptSignature          ConceptSignatureV1    `json:"concept_signature"`
	StructuralSignature       StructuralSignatureV1 `json:"structural_signature"`
	Difficulty                DifficultyAdvisoryV1  `json:"difficulty"`
	Cost                      CostStatusV1          `json:"cost"`
	ExternalOJ                ExternalOJBoundaryV1  `json:"external_oj"`
}

type QualityEvidenceV1 struct {
	Audit        EvidenceBindingV1 `json:"audit"`
	TestManifest EvidenceBindingV1 `json:"test_manifest"`
}

type EvidenceBindingV1 struct {
	Kind          string `json:"kind"`
	SchemaVersion string `json:"schema_version"`
	SHA256        string `json:"sha256"`
	SizeBytes     int64  `json:"size_bytes"`
	Producer      string `json:"producer"`
	URI           string `json:"uri"`
}

type QualityGateV1 struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

type QualityLayerV1 struct {
	Name           string `json:"name"`
	Status         string `json:"status"`
	Basis          string `json:"basis"`
	EvidenceSHA256 string `json:"evidence_sha256"`
}

type ConceptSignatureV1 struct {
	SchemaVersion string   `json:"schema_version"`
	Source        string   `json:"source"`
	Values        []string `json:"values"`
	SHA256        string   `json:"sha256"`
}

type StructuralSignatureV1 struct {
	SchemaVersion       string `json:"schema_version"`
	Source              string `json:"source"`
	SemanticSpecSHA256  string `json:"semantic_spec_sha256"`
	AuthoringPlanSHA256 string `json:"authoring_plan_sha256"`
	FactManifestSHA256  string `json:"fact_manifest_sha256"`
	SHA256              string `json:"sha256"`
}

type DifficultyAdvisoryV1 struct {
	GenerationTarget int    `json:"generation_target"`
	Calibrated       bool   `json:"calibrated"`
	Source           string `json:"source"`
}

type CostStatusV1 struct {
	Status string `json:"status"`
	Source string `json:"source"`
}

type ExternalOJBoundaryV1 struct {
	ImportVerified bool   `json:"import_verified"`
	Source         string `json:"source"`
}

type ProblemQualityBatchRequestV1 struct {
	SchemaVersion string   `json:"schema_version"`
	ProblemIDs    []string `json:"problem_ids"`
}

type ProblemQualityBatchManifestV1 struct {
	SchemaVersion            string                      `json:"schema_version"`
	RequestSchemaVersion     string                      `json:"request_schema_version"`
	RequestSHA256            string                      `json:"request_sha256"`
	Items                    []ProblemQualityBatchItemV1 `json:"items"`
	ExternalOJImportVerified bool                        `json:"external_oj_import_verified"`
}

type ProblemQualityBatchItemV1 struct {
	ProblemID           string `json:"problem_id"`
	QualityReportSHA256 string `json:"quality_report_sha256"`
	AuditSHA256         string `json:"audit_sha256"`
	TestManifestSHA256  string `json:"test_manifest_sha256"`
	QualityURI          string `json:"quality_uri"`
	AuditURI            string `json:"audit_uri"`
	TestManifestURI     string `json:"test_manifest_uri"`
}

func NewConceptSignatureV1(tags []string) (ConceptSignatureV1, error) {
	values := make([]string, len(tags))
	seen := make(map[string]struct{}, len(tags))
	for index, raw := range tags {
		value := strings.ToLower(strings.TrimSpace(raw))
		if value == "" {
			return ConceptSignatureV1{}, fmt.Errorf("concept tag %d is empty", index)
		}
		if _, duplicate := seen[value]; duplicate {
			return ConceptSignatureV1{}, fmt.Errorf("concept tag %q is duplicated", value)
		}
		seen[value] = struct{}{}
		values[index] = value
	}
	if len(values) == 0 {
		return ConceptSignatureV1{}, errors.New("at least one concept tag is required")
	}
	sort.Strings(values)
	descriptor := struct {
		SchemaVersion string   `json:"schema_version"`
		Source        string   `json:"source"`
		Values        []string `json:"values"`
	}{ConceptSignatureSchemaV1, "cas_bound_authoring_plan_concept_roles", values}
	encoded, err := json.Marshal(descriptor)
	if err != nil {
		return ConceptSignatureV1{}, err
	}
	return ConceptSignatureV1{
		SchemaVersion: descriptor.SchemaVersion,
		Source:        descriptor.Source,
		Values:        append([]string{}, values...),
		SHA256:        SHA256Hex(encoded),
	}, nil
}

func NewStructuralSignatureV1(semanticSpecSHA, authoringPlanSHA, factManifestSHA string) (StructuralSignatureV1, error) {
	for name, value := range map[string]string{
		"semantic_spec_sha256":  semanticSpecSHA,
		"authoring_plan_sha256": authoringPlanSHA,
		"fact_manifest_sha256":  factManifestSHA,
	} {
		if !canonicalSHA256V1.MatchString(value) {
			return StructuralSignatureV1{}, fmt.Errorf("%s is not canonical SHA-256", name)
		}
	}
	descriptor := struct {
		SchemaVersion       string `json:"schema_version"`
		Source              string `json:"source"`
		SemanticSpecSHA256  string `json:"semantic_spec_sha256"`
		AuthoringPlanSHA256 string `json:"authoring_plan_sha256"`
		FactManifestSHA256  string `json:"fact_manifest_sha256"`
	}{
		StructuralSignatureSchemaV1,
		"cas_bound_statement_fact_manifest_and_test_manifest_v2",
		semanticSpecSHA,
		authoringPlanSHA,
		factManifestSHA,
	}
	encoded, err := json.Marshal(descriptor)
	if err != nil {
		return StructuralSignatureV1{}, err
	}
	return StructuralSignatureV1{
		SchemaVersion:       descriptor.SchemaVersion,
		Source:              descriptor.Source,
		SemanticSpecSHA256:  descriptor.SemanticSpecSHA256,
		AuthoringPlanSHA256: descriptor.AuthoringPlanSHA256,
		FactManifestSHA256:  descriptor.FactManifestSHA256,
		SHA256:              SHA256Hex(encoded),
	}, nil
}

func CanonicalProblemQualityReportV1(report ProblemQualityReportV1) ([]byte, string, error) {
	if err := report.Validate(); err != nil {
		return nil, "", err
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		return nil, "", err
	}
	return encoded, SHA256Hex(encoded), nil
}

func (report ProblemQualityReportV1) Validate() error {
	if report.SchemaVersion != ProblemQualityReportSchemaV1 || !canonicalUUIDV1.MatchString(report.ProblemID) {
		return errors.New("quality report schema_version or problem_id is invalid")
	}
	if strings.TrimSpace(report.WorkflowID) == "" || len(report.WorkflowID) > 256 || !canonicalSHA256V1.MatchString(report.SubjectRevision) {
		return errors.New("quality report workflow or subject identity is invalid")
	}
	if report.GenerationEvidenceProfile != "minimal" && report.GenerationEvidenceProfile != "standard" && report.GenerationEvidenceProfile != "audit" {
		return fmt.Errorf("unsupported generation evidence profile %q", report.GenerationEvidenceProfile)
	}
	if report.Decision != "pass" {
		return errors.New("quality report requires a pass decision")
	}
	if err := validateEvidenceV1(report); err != nil {
		return err
	}
	if len(report.Gates) != len(gateOrderV1) {
		return fmt.Errorf("quality report has %d gates, want %d", len(report.Gates), len(gateOrderV1))
	}
	for index, gate := range report.Gates {
		if gate.Name != gateOrderV1[index] || gate.Status != "pass" {
			return fmt.Errorf("quality gate %d is %q/%q", index, gate.Name, gate.Status)
		}
	}
	if err := validateLayersV1(report); err != nil {
		return err
	}
	wantConcept, err := NewConceptSignatureV1(report.ConceptSignature.Values)
	if err != nil || wantConcept.SchemaVersion != report.ConceptSignature.SchemaVersion ||
		wantConcept.Source != report.ConceptSignature.Source || wantConcept.SHA256 != report.ConceptSignature.SHA256 ||
		!slices.Equal(wantConcept.Values, report.ConceptSignature.Values) {
		return errors.New("concept signature is not canonical")
	}
	wantStructural, err := NewStructuralSignatureV1(
		report.StructuralSignature.SemanticSpecSHA256,
		report.StructuralSignature.AuthoringPlanSHA256,
		report.StructuralSignature.FactManifestSHA256,
	)
	if err != nil || wantStructural != report.StructuralSignature {
		return errors.New("structural signature is not canonical")
	}
	if report.Difficulty.GenerationTarget <= 0 || report.Difficulty.GenerationTarget%100 != 0 || report.Difficulty.Calibrated || report.Difficulty.Source != "cas_bound_authoring_plan_generation_target_advisory" {
		return errors.New("difficulty must remain an uncalibrated CAS-bound generation-target advisory")
	}
	if report.Cost.Status != CostStatusUnavailable || report.Cost.Source != "not_recomputable_from_persisted_s3_evidence" {
		return errors.New("cost must be explicitly unavailable when it cannot be recomputed")
	}
	if report.ExternalOJ.ImportVerified || report.ExternalOJ.Source != "outside_algoforge_quality_evidence" {
		return errors.New("external OJ verification boundary is invalid")
	}
	return nil
}

func validateEvidenceV1(report ProblemQualityReportV1) error {
	want := []struct {
		value    EvidenceBindingV1
		kind     string
		schema   string
		producer string
		uri      string
	}{
		{report.Evidence.Audit, "quality_audit", "algoforge.s3-quality-audit.v1", "RecomputeS3VerdictActivityV1", "/api/v1/problems/" + report.ProblemID + "/quality/audit"},
		{report.Evidence.TestManifest, "test_manifest", "algoforge.test-manifest.v2", "BuildS3TestManifestActivityV1", "/api/v1/problems/" + report.ProblemID + "/test-manifest"},
	}
	for _, item := range want {
		if item.value.Kind != item.kind || item.value.SchemaVersion != item.schema || item.value.Producer != item.producer || item.value.URI != item.uri ||
			!canonicalSHA256V1.MatchString(item.value.SHA256) || item.value.SizeBytes <= 0 {
			return fmt.Errorf("%s evidence binding is invalid", item.kind)
		}
	}
	return nil
}

func validateLayersV1(report ProblemQualityReportV1) error {
	if len(report.Layers) != 3 {
		return errors.New("quality report requires exactly three ordered layers")
	}
	want := []struct {
		name   string
		status map[string]bool
		sha    string
	}{
		{LayerStandard, map[string]bool{LayerStatusPass: true}, report.Evidence.Audit.SHA256},
		{LayerVerified, map[string]bool{LayerStatusPass: true}, report.Evidence.TestManifest.SHA256},
		{LayerPublicationReady, map[string]bool{LayerStatusReady: true, LayerStatusPendingExplicitApproval: true}, report.SubjectRevision},
	}
	for index, expected := range want {
		layer := report.Layers[index]
		if layer.Name != expected.name || !expected.status[layer.Status] || strings.TrimSpace(layer.Basis) == "" || layer.EvidenceSHA256 != expected.sha {
			return fmt.Errorf("quality layer %d is invalid", index)
		}
	}
	return nil
}

func DecodeProblemQualityBatchRequestV1(data []byte) (ProblemQualityBatchRequestV1, []byte, string, error) {
	if len(data) == 0 {
		return ProblemQualityBatchRequestV1{}, nil, "", errors.New("batch request JSON is empty")
	}
	if len(data) > MaxBatchRequestBytesV1 {
		return ProblemQualityBatchRequestV1{}, nil, "", fmt.Errorf("batch request JSON exceeds %d bytes", MaxBatchRequestBytesV1)
	}
	if err := validateSingleJSONValueNoDuplicateKeysV1(data); err != nil {
		return ProblemQualityBatchRequestV1{}, nil, "", err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var request ProblemQualityBatchRequestV1
	if err := decoder.Decode(&request); err != nil {
		return ProblemQualityBatchRequestV1{}, nil, "", fmt.Errorf("decode batch request: %w", err)
	}
	if err := requireJSONEOFV1(decoder); err != nil {
		return ProblemQualityBatchRequestV1{}, nil, "", err
	}
	canonical, digest, err := CanonicalProblemQualityBatchRequestV1(request)
	if err != nil {
		return ProblemQualityBatchRequestV1{}, nil, "", err
	}
	return request, canonical, digest, nil
}

func CanonicalProblemQualityBatchRequestV1(request ProblemQualityBatchRequestV1) ([]byte, string, error) {
	if request.SchemaVersion != ProblemQualityBatchRequestSchemaV1 {
		return nil, "", fmt.Errorf("schema_version must be %q", ProblemQualityBatchRequestSchemaV1)
	}
	if request.ProblemIDs == nil || len(request.ProblemIDs) == 0 || len(request.ProblemIDs) > MaxBatchProblemIDsV1 {
		return nil, "", fmt.Errorf("problem_ids count must be within [1,%d]", MaxBatchProblemIDsV1)
	}
	previous := ""
	for index, id := range request.ProblemIDs {
		if !canonicalUUIDV1.MatchString(id) {
			return nil, "", fmt.Errorf("problem_ids[%d] is not a canonical UUID", index)
		}
		if index > 0 && id <= previous {
			if id == previous {
				return nil, "", fmt.Errorf("problem_ids[%d] duplicates %q", index, id)
			}
			return nil, "", fmt.Errorf("problem_ids must be strictly ascending")
		}
		previous = id
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		return nil, "", err
	}
	return encoded, SHA256Hex(encoded), nil
}

func CanonicalProblemQualityBatchManifestV1(manifest ProblemQualityBatchManifestV1) ([]byte, string, error) {
	if manifest.SchemaVersion != ProblemQualityBatchManifestSchemaV1 || manifest.RequestSchemaVersion != ProblemQualityBatchRequestSchemaV1 ||
		!canonicalSHA256V1.MatchString(manifest.RequestSHA256) || manifest.ExternalOJImportVerified ||
		manifest.Items == nil || len(manifest.Items) == 0 || len(manifest.Items) > MaxBatchProblemIDsV1 {
		return nil, "", errors.New("quality batch manifest identity is invalid")
	}
	previous := ""
	for index, item := range manifest.Items {
		if !canonicalUUIDV1.MatchString(item.ProblemID) || (index > 0 && item.ProblemID <= previous) {
			return nil, "", fmt.Errorf("quality batch item %d identity is invalid or unsorted", index)
		}
		for name, value := range map[string]string{
			"quality_report_sha256": item.QualityReportSHA256,
			"audit_sha256":          item.AuditSHA256,
			"test_manifest_sha256":  item.TestManifestSHA256,
		} {
			if !canonicalSHA256V1.MatchString(value) {
				return nil, "", fmt.Errorf("quality batch item %d %s is invalid", index, name)
			}
		}
		prefix := "/api/v1/problems/" + item.ProblemID
		if item.QualityURI != prefix+"/quality" || item.AuditURI != prefix+"/quality/audit" || item.TestManifestURI != prefix+"/test-manifest" {
			return nil, "", fmt.Errorf("quality batch item %d URI binding is invalid", index)
		}
		previous = item.ProblemID
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return nil, "", err
	}
	return encoded, SHA256Hex(encoded), nil
}

func SHA256Hex(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func requireJSONEOFV1(decoder *json.Decoder) error {
	var trailing interface{}
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return errors.New("trailing JSON value")
		}
		return fmt.Errorf("decode trailing JSON data: %w", err)
	}
	return nil
}

func validateSingleJSONValueNoDuplicateKeysV1(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := scanJSONValueV1(decoder, "$"); err != nil {
		return err
	}
	if token, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return fmt.Errorf("trailing JSON token %v", token)
		}
		return fmt.Errorf("decode trailing JSON data: %w", err)
	}
	return nil
}

func scanJSONValueV1(decoder *json.Decoder, path string) error {
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("decode JSON at %s: %w", path, err)
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return fmt.Errorf("decode object key at %s: %w", path, err)
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("non-string object key at %s", path)
			}
			if _, duplicate := seen[key]; duplicate {
				return fmt.Errorf("duplicate JSON field %q at %s", key, path)
			}
			seen[key] = struct{}{}
			if err := scanJSONValueV1(decoder, path+"."+key); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return fmt.Errorf("unterminated JSON object at %s", path)
		}
	case '[':
		for index := 0; decoder.More(); index++ {
			if err := scanJSONValueV1(decoder, fmt.Sprintf("%s[%d]", path, index)); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return fmt.Errorf("unterminated JSON array at %s", path)
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter %q at %s", delimiter, path)
	}
	return nil
}
