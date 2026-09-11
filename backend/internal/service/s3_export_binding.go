package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/generationapi"
	qualitygate "github.com/Gingoo-TvT/Qraft/backend/internal/qualitygate/v1"
	"github.com/Gingoo-TvT/Qraft/backend/internal/workflow/activities"
	"github.com/google/uuid"
)

type s3ExportBindingV1 struct {
	TestManifestV2     *activities.TestManifestV2
	TestManifestRef    activities.ArtifactRef
	AuthoringBundleRef activities.ArtifactRef
	AuthoringBundleV1  *activities.AuthoringPlanBundleV1
	QualityAuditV1     qualitygate.AuditV1
	QualityAuditBytes  []byte
	QualityAuditRef    activities.ArtifactRef
	StatementDraftV1   *activities.StatementDraftBundleV1
	EvidenceLevel      string
	SubjectRevision    string
}

type s3ExportManifestMetadataV1 struct {
	SchemaVersion string                 `json:"schema_version"`
	SHA256        string                 `json:"sha256"`
	Path          string                 `json:"path"`
	Artifact      activities.ArtifactRef `json:"artifact"`
}

// loadS3ExportBindingV1 is the single product-export trust boundary shared by
// Hydro and portable problem-set packages. Low-level builders remain usable for
// old offline tooling, but product services must pass through this loader.
func loadS3ExportBindingV1(
	ctx context.Context,
	problem *domain.Problem,
	testCases []*domain.TestCase,
	objects HydroObjectReader,
) (*s3ExportBindingV1, error) {
	fail := func(format string, args ...interface{}) (*s3ExportBindingV1, error) {
		return nil, fmt.Errorf("%w: S3 export binding: %s", ErrConflict, fmt.Sprintf(format, args...))
	}
	if problem == nil || problem.ID == uuid.Nil {
		return fail("persisted problem identity is missing")
	}
	if objects == nil {
		return fail("object reader is missing")
	}
	if problem.WorkflowID == nil || !generationapi.IsJobID(strings.TrimSpace(*problem.WorkflowID)) {
		return fail("canonical v6 workflow identity is missing")
	}
	workflowID := strings.TrimSpace(*problem.WorkflowID)

	metadata, err := decodeS3ExportRootMetadataV1(problem.MetadataJSON)
	if err != nil {
		return fail("metadata is invalid: %v", err)
	}
	if err := validateS3ExportProblemStateV1(problem, metadata); err != nil {
		return fail("problem state is not exportable: %v", err)
	}
	var payloadVersion int
	if err := decodeS3ExportStrictJSONV1(metadata["activity_payload_version"], &payloadVersion); err != nil || payloadVersion != activities.StoreProblemS3QualityDraftPayloadVersion {
		return fail("activity_payload_version must be %d", activities.StoreProblemS3QualityDraftPayloadVersion)
	}
	var manifestMetadata s3ExportManifestMetadataV1
	if err := decodeS3ExportStrictJSONV1(metadata["test_manifest"], &manifestMetadata); err != nil {
		return fail("test_manifest metadata is invalid: %v", err)
	}
	var evidence activities.S3QualityPassDraftEvidenceV1
	if err := decodeS3ExportStrictJSONV1(metadata[generationapi.S3QualityMaterializationMetadataKey], &evidence); err != nil {
		return fail("%s metadata is invalid: %v", generationapi.S3QualityMaterializationMetadataKey, err)
	}
	if evidence.SchemaVersion != generationapi.S3QualityMaterializationSchemaV1 || evidence.Decision != qualitygate.DecisionPass {
		return fail("%s must record the canonical pass decision", generationapi.S3QualityMaterializationMetadataKey)
	}
	if evidence.EvidenceLevel != generationapi.EvidenceMinimal && evidence.EvidenceLevel != generationapi.EvidenceStandard && evidence.EvidenceLevel != generationapi.EvidenceAudit {
		return fail("S3 evidence level is unsupported")
	}
	if raw, ok := metadata[generationapi.QualityEvidenceLevelMetadataKey]; ok {
		var persistedLevel string
		if err := decodeS3ExportStrictJSONV1(raw, &persistedLevel); err != nil || persistedLevel != evidence.EvidenceLevel {
			return fail("persisted quality evidence level does not match S3 evidence")
		}
	} else if evidence.EvidenceLevel != generationapi.EvidenceMinimal {
		return fail("persisted quality evidence level is missing")
	}
	if evidence.SubjectRevision != evidence.FinalStatementArtifact.SHA256 {
		return fail("S3 subject revision does not bind the final statement")
	}

	refs := []struct {
		name     string
		producer string
		ref      activities.ArtifactRef
	}{
		{"authoring bundle", "GenerateAuthoringPlanActivity", evidence.AuthoringBundleArtifact},
		{"statement draft", "RenderStatementFromAuthoringBundleActivityV1", evidence.StatementDraftArtifact},
		{"final statement", "FinalizeAuthoringStatementSamplesActivityV1", evidence.FinalStatementArtifact},
		{"main program", "GenerateMainSolutionActivityV1", evidence.MainProgramArtifact},
		{"oracle program", "GenerateOracleCandidateActivityV1", evidence.OracleProgramArtifact},
		{"oracle receipt", activities.VerifiedProgramReceiptProducerV1, evidence.OracleReceiptArtifact},
		{"test manifest", "BuildS3TestManifestActivityV1", evidence.TestManifestArtifact},
		{"quality audit", "RecomputeS3VerdictActivityV1", evidence.AuditArtifact},
	}
	artifactBucket := ""
	for _, item := range refs {
		if err := item.ref.Validate(item.ref.Bucket); err != nil {
			return fail("%s ArtifactRef is invalid: %v", item.name, err)
		}
		if item.ref.Producer != item.producer || item.ref.WorkflowID != workflowID {
			return fail("%s ArtifactRef producer/workflow binding is invalid", item.name)
		}
		if artifactBucket == "" {
			artifactBucket = item.ref.Bucket
		} else if item.ref.Bucket != artifactBucket {
			return fail("the eight S3 ArtifactRefs do not share one bucket")
		}
	}
	statementDraftBytes, err := readS3ExportArtifactV1(ctx, objects, evidence.StatementDraftArtifact)
	if err != nil {
		return fail("statement draft: %v", err)
	}
	var statementDraft activities.StatementDraftBundleV1
	if err := decodeCanonicalS3ExportArtifactJSONV1(statementDraftBytes, &statementDraft); err != nil {
		return fail("statement draft is not canonical: %v", err)
	}
	if statementDraft.SchemaVersion != activities.StatementDraftBundleSchemaV1 || statementDraft.Title != problem.Title ||
		statementDraft.MarkdownSHA256 != sha256Hex([]byte(statementDraft.Markdown)) {
		return fail("persisted problem title or statement-draft identity drifted")
	}
	finalStatementBytes, err := readS3ExportArtifactV1(ctx, objects, evidence.FinalStatementArtifact)
	if err != nil {
		return fail("final statement: %v", err)
	}
	var finalStatement activities.FinalAuthoringStatementSamplesBundleV1
	if err := decodeCanonicalS3ExportArtifactJSONV1(finalStatementBytes, &finalStatement); err != nil {
		return fail("final statement is not canonical: %v", err)
	}
	if finalStatement.SchemaVersion != activities.FinalAuthoringStatementSamplesSchemaV1 ||
		finalStatement.Status != activities.FinalAuthoringStatementSamplesStatusV1 ||
		finalStatement.Markdown != problem.Statement || finalStatement.MarkdownSHA256 != sha256Hex([]byte(finalStatement.Markdown)) ||
		finalStatement.AuthoringBundleSHA256 != evidence.AuthoringBundleArtifact.SHA256 ||
		finalStatement.StatementDraftSHA256 != evidence.StatementDraftArtifact.SHA256 ||
		finalStatement.ProgramSHA256 != evidence.MainProgramArtifact.SHA256 ||
		finalStatement.IndependentOracleReceiptSHA256 != evidence.OracleReceiptArtifact.SHA256 {
		return fail("persisted problem statement or final-statement ancestry drifted")
	}
	if !reflect.DeepEqual(manifestMetadata.Artifact, evidence.TestManifestArtifact) {
		return fail("test_manifest artifact does not match %s", generationapi.S3QualityMaterializationMetadataKey)
	}
	if manifestMetadata.SchemaVersion != activities.TestManifestSchemaVersionV2 || !isS3ExportSHA256V1(manifestMetadata.SHA256) {
		return fail("test_manifest schema or digest is not canonical v2")
	}
	wantManifestPath := fmt.Sprintf("problems/%s/test_manifest.v2.json", problem.ID.String())
	if manifestMetadata.Path != wantManifestPath {
		return fail("test_manifest path %q does not match %q", manifestMetadata.Path, wantManifestPath)
	}
	if manifestMetadata.SHA256 != evidence.TestManifestArtifact.SHA256 {
		return fail("test_manifest metadata and CAS digest disagree")
	}

	auditBytes, err := readS3ExportArtifactV1(ctx, objects, evidence.AuditArtifact)
	if err != nil {
		return fail("quality audit: %v", err)
	}
	var audit qualitygate.AuditV1
	if err := decodeS3ExportStrictJSONV1(auditBytes, &audit); err != nil {
		return fail("quality audit JSON is invalid: %v", err)
	}
	canonicalAudit, auditSHA, err := qualitygate.CanonicalAuditV1(audit)
	if err != nil || !bytes.Equal(canonicalAudit, auditBytes) || auditSHA != evidence.AuditArtifact.SHA256 {
		return fail("quality audit is not canonical or CAS-bound")
	}
	if err := validateS3ExportAuditPassV1(audit, workflowID, evidence.SubjectRevision); err != nil {
		return fail("quality audit is not a complete nine-gate PASS: %v", err)
	}

	manifestPathBytes, err := objects.DownloadFile(ctx, manifestMetadata.Path)
	if err != nil {
		return fail("read persisted TestManifest v2 path: %v", err)
	}
	if int64(len(manifestPathBytes)) != evidence.TestManifestArtifact.SizeBytes || sha256Hex(manifestPathBytes) != manifestMetadata.SHA256 {
		return fail("TestManifest v2 path size or SHA-256 does not match metadata/CAS")
	}
	manifestCASBytes, err := readS3ExportArtifactV1(ctx, objects, evidence.TestManifestArtifact)
	if err != nil {
		return fail("read TestManifest v2 CAS: %v", err)
	}
	if !bytes.Equal(manifestPathBytes, manifestCASBytes) {
		return fail("TestManifest v2 path and CAS bytes are not identical and digest-bound")
	}
	manifest, err := activities.ParseTestManifestV2JSON(manifestCASBytes)
	if err != nil {
		return fail("TestManifest v2 is invalid or non-canonical: %v", err)
	}
	if manifest.OraclePromotionReceiptSHA256 != evidence.OracleReceiptArtifact.SHA256 {
		return fail("TestManifest v2 oracle receipt ancestry is inconsistent")
	}
	if manifest.SemanticSpecSHA256 != finalStatement.SemanticSpecSHA256 {
		return fail("TestManifest v2 semantic subject does not match the final statement")
	}
	if err := validateS3ExportPersistedCasesV1(ctx, problem, testCases, objects, workflowID, artifactBucket, manifest); err != nil {
		return fail("persisted testcase binding is invalid: %v", err)
	}
	return &s3ExportBindingV1{
		TestManifestV2:     manifest,
		TestManifestRef:    evidence.TestManifestArtifact,
		AuthoringBundleRef: evidence.AuthoringBundleArtifact,
		QualityAuditV1:     audit,
		QualityAuditBytes:  append([]byte(nil), auditBytes...),
		QualityAuditRef:    evidence.AuditArtifact,
		StatementDraftV1:   &statementDraft,
		EvidenceLevel:      evidence.EvidenceLevel,
		SubjectRevision:    evidence.SubjectRevision,
	}, nil
}

func validateS3ExportProblemStateV1(problem *domain.Problem, metadata map[string]json.RawMessage) error {
	if problem.Status != domain.ProblemStatusDraft && problem.Status != domain.ProblemStatusPublished {
		return fmt.Errorf("status %q is not a fresh trusted draft or published record", problem.Status)
	}
	if raw, ok := metadata["stale"]; ok {
		var stale bool
		if err := decodeS3ExportStrictJSONV1(raw, &stale); err != nil {
			return fmt.Errorf("stale flag is invalid: %w", err)
		}
		if stale {
			return fmt.Errorf("problem is stale")
		}
	}
	if raw, ok := metadata["publication_quarantine_reason"]; ok {
		var reason string
		if err := decodeS3ExportStrictJSONV1(raw, &reason); err != nil {
			return fmt.Errorf("publication quarantine reason is invalid: %w", err)
		}
		if strings.TrimSpace(reason) != "" {
			return fmt.Errorf("publication is explicitly denied: %s", strings.TrimSpace(reason))
		}
	}
	if raw, ok := metadata["publication_gate_status"]; ok {
		var status string
		if err := decodeS3ExportStrictJSONV1(raw, &status); err != nil {
			return fmt.Errorf("publication gate status is invalid: %w", err)
		}
		switch strings.TrimSpace(status) {
		case "", string(domain.ProblemStatusDraft), string(domain.ProblemStatusPublished):
		case string(domain.ProblemStatusQuarantined), string(domain.ProblemStatusRejected), "blocked", "denied", qualitygate.GateStatusCheckFailed:
			return fmt.Errorf("publication gate is explicitly denied with status %q", status)
		default:
			return fmt.Errorf("publication gate status %q is unknown", status)
		}
	}
	return nil
}

func decodeS3ExportRootMetadataV1(data []byte) (map[string]json.RawMessage, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, fmt.Errorf("metadata is empty")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	var metadata map[string]json.RawMessage
	if err := decoder.Decode(&metadata); err != nil {
		return nil, err
	}
	if err := requireS3ExportJSONEOFV1(decoder); err != nil {
		return nil, err
	}
	if metadata == nil {
		return nil, fmt.Errorf("metadata object is missing")
	}
	return metadata, nil
}

func decodeS3ExportStrictJSONV1(data []byte, target interface{}) error {
	if len(bytes.TrimSpace(data)) == 0 {
		return fmt.Errorf("required JSON value is missing")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return requireS3ExportJSONEOFV1(decoder)
}

func decodeCanonicalS3ExportArtifactJSONV1(data []byte, target interface{}) error {
	if err := decodeS3ExportStrictJSONV1(data, target); err != nil {
		return err
	}
	canonical, err := json.Marshal(target)
	if err != nil {
		return err
	}
	if !bytes.Equal(canonical, data) {
		return fmt.Errorf("artifact JSON bytes are not canonical")
	}
	return nil
}

func requireS3ExportJSONEOFV1(decoder *json.Decoder) error {
	var trailing interface{}
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("trailing JSON value")
		}
		return err
	}
	return nil
}

func readS3ExportArtifactV1(ctx context.Context, objects HydroObjectReader, ref activities.ArtifactRef) ([]byte, error) {
	data, err := objects.DownloadFile(ctx, ref.Key)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) != ref.SizeBytes {
		return nil, fmt.Errorf("artifact %q size=%d, want %d", ref.Key, len(data), ref.SizeBytes)
	}
	if digest := sha256Hex(data); digest != ref.SHA256 {
		return nil, fmt.Errorf("artifact %q SHA-256=%s, want %s", ref.Key, digest, ref.SHA256)
	}
	return data, nil
}

func validateS3ExportAuditPassV1(audit qualitygate.AuditV1, workflowID, revision string) error {
	_, canonicalRuleSHA, err := qualitygate.CanonicalRuleV1()
	if err != nil {
		return err
	}
	want := []string{
		qualitygate.GateSpecLint,
		qualitygate.GateSampleOutputBinding,
		qualitygate.GateOracleDifferential,
		qualitygate.GateSanitizer,
		qualitygate.GateBoundaryCoverage,
		qualitygate.GateTestManifest,
		qualitygate.GateReviewerSchemaVerdict,
		qualitygate.GateDedup,
		qualitygate.GateHiddenRegression,
	}
	if audit.RuleSHA256 != canonicalRuleSHA || audit.SubjectID != workflowID || audit.SubjectRevision != revision ||
		audit.Decision != qualitygate.DecisionPass || !audit.DeterministicGatesPassed ||
		len(audit.BlockingIssues) != 0 || len(audit.GateResults) != len(want) {
		return fmt.Errorf("audit identity, decision, or gate count is invalid")
	}
	for index, gate := range audit.GateResults {
		if gate.Gate != want[index] || gate.Status != qualitygate.GateStatusPass {
			return fmt.Errorf("gate %d is %q/%q", index, gate.Gate, gate.Status)
		}
	}
	return nil
}

func validateS3ExportPersistedCasesV1(
	ctx context.Context,
	problem *domain.Problem,
	testCases []*domain.TestCase,
	objects HydroObjectReader,
	workflowID string,
	artifactBucket string,
	manifest *activities.TestManifestV2,
) error {
	if manifest == nil || len(testCases) != len(manifest.Cases) {
		return fmt.Errorf("persisted and manifest testcase counts differ")
	}
	ordered := append([]*domain.TestCase(nil), testCases...)
	for index, testCase := range ordered {
		if testCase == nil {
			return fmt.Errorf("testcase %d is nil", index)
		}
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].TestIndex == ordered[j].TestIndex {
			return ordered[i].ID.String() < ordered[j].ID.String()
		}
		return ordered[i].TestIndex < ordered[j].TestIndex
	})
	for index, testCase := range ordered {
		manifestCase := manifest.Cases[index]
		if testCase.TestIndex != index || manifestCase.TestIndex != index || testCase.ProblemID != problem.ID {
			return fmt.Errorf("case %d index/problem identity mismatch", index)
		}
		if testCase.IsSample != (manifestCase.Purpose == activities.TestManifestPurposeSample) {
			return fmt.Errorf("case %d sample identity mismatch", index)
		}
		for label, ref := range map[string]*activities.ArtifactRef{"input": manifestCase.InputArtifact, "output": manifestCase.OutputArtifact} {
			if ref == nil || ref.WorkflowID != workflowID || ref.Bucket != artifactBucket {
				return fmt.Errorf("case %d %s ArtifactRef workflow/bucket mismatch", index, label)
			}
		}
		inputBytes, err := objects.DownloadFile(ctx, testCase.InputPath)
		if err != nil {
			return fmt.Errorf("read case %d input: %w", index, err)
		}
		outputBytes, err := objects.DownloadFile(ctx, testCase.OutputPath)
		if err != nil {
			return fmt.Errorf("read case %d output: %w", index, err)
		}
		if sha256Hex(inputBytes) != manifestCase.InputSHA256 || int64(len(inputBytes)) != manifestCase.InputArtifact.SizeBytes ||
			sha256Hex(outputBytes) != manifestCase.OutputSHA256 || int64(len(outputBytes)) != manifestCase.OutputArtifact.SizeBytes {
			return fmt.Errorf("case %d input/output SHA-256 or size mismatch", index)
		}
	}
	return nil
}

func isS3ExportSHA256V1(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}
