package testfixture

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/generationapi"
	qualitygate "github.com/Gingoo-TvT/Qraft/backend/internal/qualitygate/v1"
	"github.com/Gingoo-TvT/Qraft/backend/internal/workflow/activities"
	"github.com/google/uuid"
)

type ObjectReader struct {
	Data map[string][]byte
}

func NewObjectReader() *ObjectReader {
	return &ObjectReader{Data: make(map[string][]byte)}
}

func (reader *ObjectReader) DownloadFile(_ context.Context, objectPath string) ([]byte, error) {
	data, ok := reader.Data[objectPath]
	if !ok {
		return nil, fmt.Errorf("missing fixture object %s", objectPath)
	}
	return append([]byte(nil), data...), nil
}

func (reader *ObjectReader) Merge(other *ObjectReader) {
	if reader == nil || other == nil {
		return
	}
	for key, value := range other.Data {
		reader.Data[key] = append([]byte(nil), value...)
	}
}

type CanonicalS3QualityDraft struct {
	Problem          *domain.Problem
	TestCases        []*domain.TestCase
	Objects          *ObjectReader
	AuditArtifactKey string
	ManifestPath     string
}

// NewCanonicalS3QualityDraft builds the persisted shape emitted by the v6 S3
// draft materializer: positive source limits, zero legacy group/score fields,
// eight same-workflow ArtifactRefs, canonical nine-gate PASS audit, and a
// TestManifest v2 bound at both its CAS key and persisted problem path.
func NewCanonicalS3QualityDraft(problemID uuid.UUID, workflowHex string, title, statement string, tags []string) (*CanonicalS3QualityDraft, error) {
	if problemID == uuid.Nil || len(workflowHex) != 1 || !strings.Contains("0123456789abcdef", workflowHex) {
		return nil, fmt.Errorf("invalid canonical fixture identity")
	}
	workflowID := generationapi.JobIDPrefix + strings.Repeat(workflowHex, 64)
	bucket := "qg15-handler-store-v6-fixture"
	objects := NewObjectReader()
	problem := &domain.Problem{
		ID: problemID, SerialNumber: "AF-" + problemID.String()[:8], Title: title, Statement: statement,
		Tags: append([]string(nil), tags...), TimeLimit: 1000, MemoryLimit: 128,
		Status: domain.ProblemStatusDraft, WorkflowID: &workflowID,
	}

	purposes := []string{
		activities.TestManifestPurposeSample,
		activities.TestManifestPurposeTiny,
		activities.TestManifestPurposeRandom,
		activities.TestManifestPurposeMetamorphic,
		activities.TestManifestPurposeBoundary,
		activities.TestManifestPurposeBoundary,
		activities.TestManifestPurposeBoundary,
		activities.TestManifestPurposeExtreme,
		activities.TestManifestPurposeComplexity,
	}
	regions := []string{"sample", "small", "medium", "medium", "low", "low", "low", "pressure-extreme", "pressure-complexity"}
	boundaryRefs := [][]string{{}, {}, {}, {}, {"n-min"}, {"n-min"}, {"n-max"}, {}, {}}
	testCases := make([]*domain.TestCase, len(purposes))
	manifestCases := make([]activities.TestManifestCaseV2, len(purposes))
	for index := range purposes {
		input := []byte(fmt.Sprintf("%d %d\n", index, index+1))
		output := []byte(fmt.Sprintf("%d\n", index*2+1))
		inputRef := artifactRef(input, bucket, workflowID, fmt.Sprintf("PersistS3InputCase%d", index), "text/plain; charset=utf-8")
		outputRef := artifactRef(output, bucket, workflowID, fmt.Sprintf("PersistS3OutputCase%d", index), "text/plain; charset=utf-8")
		inputPath := fmt.Sprintf("problems/%s/testdata/%d.in", problemID, index)
		outputPath := fmt.Sprintf("problems/%s/testdata/%d.out", problemID, index)
		objects.Data[inputPath] = input
		objects.Data[outputPath] = output
		testCases[index] = &domain.TestCase{
			ID: uuid.NewSHA1(problemID, []byte(fmt.Sprintf("case-%d", index))), ProblemID: problemID,
			TestIndex: index, GroupID: 0, Score: 0, IsSample: index == 0,
			InputPath: inputPath, OutputPath: outputPath,
		}
		seed := int64(2000 + index)
		caseBoundaryRefs := make([]string, len(boundaryRefs[index]))
		copy(caseBoundaryRefs, boundaryRefs[index])
		manifestCases[index] = activities.TestManifestCaseV2{
			TestIndex: index, TestID: fmt.Sprintf("case-%06d", index), Purpose: purposes[index],
			ConstraintRegion: regions[index], BoundaryRefs: caseBoundaryRefs, Seed: &seed,
			InputArtifact: &inputRef, InputSHA256: inputRef.SHA256,
			OutputArtifact: &outputRef, OutputSHA256: outputRef.SHA256,
			KilledWrongIDs: []string{}, KilledWrongIDsRetentionReason: "canonical Store-v6 handler fixture",
		}
	}

	semanticSHA := strings.Repeat("a", 64)
	authoringPlanSHA := strings.Repeat("b", 64)
	refs := map[string]activities.ArtifactRef{
		"authoring": artifactRef([]byte("authoring bundle"), bucket, workflowID, "GenerateAuthoringPlanActivity", "application/json"),
		"main":      artifactRef([]byte("main source"), bucket, workflowID, "GenerateMainSolutionActivityV1", "text/plain; charset=utf-8"),
		"oracle":    artifactRef([]byte("oracle source"), bucket, workflowID, "GenerateOracleCandidateActivityV1", "text/plain; charset=utf-8"),
		"receipt":   artifactRef([]byte("oracle receipt"), bucket, workflowID, activities.VerifiedProgramReceiptProducerV1, "application/json"),
	}
	draftMarkdown := "canonical pre-final statement draft"
	draft := activities.StatementDraftBundleV1{
		SchemaVersion: activities.StatementDraftBundleSchemaV1, AuthoringBundleSHA256: refs["authoring"].SHA256,
		Title: title, Markdown: draftMarkdown, MarkdownSHA256: digest([]byte(draftMarkdown)),
	}
	draftBytes, err := json.Marshal(draft)
	if err != nil {
		return nil, err
	}
	refs["draft"] = artifactRef(draftBytes, bucket, workflowID, "RenderStatementFromAuthoringBundleActivityV1", "application/json")
	final := activities.FinalAuthoringStatementSamplesBundleV1{
		SchemaVersion:         activities.FinalAuthoringStatementSamplesSchemaV1,
		Status:                activities.FinalAuthoringStatementSamplesStatusV1,
		AuthoringBundleSHA256: refs["authoring"].SHA256, StatementDraftSHA256: refs["draft"].SHA256,
		ProgramSHA256: refs["main"].SHA256, IndependentOracleReceiptSHA256: refs["receipt"].SHA256,
		SemanticSpecSHA256: semanticSHA, Samples: []activities.AuthoringStatementSampleRecordV1{},
		Markdown: statement, MarkdownSHA256: digest([]byte(statement)),
	}
	finalBytes, err := json.Marshal(final)
	if err != nil {
		return nil, err
	}
	refs["final"] = artifactRef(finalBytes, bucket, workflowID, "FinalizeAuthoringStatementSamplesActivityV1", "application/json")

	manifest := activities.TestManifestV2{
		SchemaVersion: activities.TestManifestSchemaVersionV2, SemanticSpecSHA256: semanticSHA,
		AuthoringPlanSHA256: authoringPlanSHA, OraclePromotionReceiptSHA256: refs["receipt"].SHA256,
		SanitizerReceiptSHA256: strings.Repeat("d", 64), BoundaryCoverageReceiptSHA256: strings.Repeat("e", 64),
		TestCount: len(manifestCases), Cases: manifestCases,
	}
	manifestBytes, _, err := activities.CanonicalTestManifestV2JSON(manifest)
	if err != nil {
		return nil, fmt.Errorf("canonical TestManifest v2: %w", err)
	}
	manifestRef := artifactRef(manifestBytes, bucket, workflowID, "BuildS3TestManifestActivityV1", "application/json")

	_, ruleSHA, err := qualitygate.CanonicalRuleV1()
	if err != nil {
		return nil, err
	}
	gateNames := []string{
		qualitygate.GateSpecLint, qualitygate.GateSampleOutputBinding, qualitygate.GateOracleDifferential,
		qualitygate.GateSanitizer, qualitygate.GateBoundaryCoverage, qualitygate.GateTestManifest,
		qualitygate.GateReviewerSchemaVerdict, qualitygate.GateDedup, qualitygate.GateHiddenRegression,
	}
	gateResults := make([]qualitygate.GateResultV1, len(gateNames))
	for index, gate := range gateNames {
		gateResults[index] = qualitygate.GateResultV1{Gate: gate, Status: qualitygate.GateStatusPass}
	}
	audit := qualitygate.AuditV1{
		SchemaVersion: qualitygate.AuditSchemaVersionV1, RuleVersion: qualitygate.RuleVersionV1,
		RuleSHA256: ruleSHA, InputSHA256: strings.Repeat("2", 64), SubjectID: workflowID,
		SubjectRevision: refs["final"].SHA256, Decision: qualitygate.DecisionPass,
		DeterministicGatesPassed: true, ReviewerDeclaredApproved: true, ReviewerAdvisoryApproved: true,
		GateResults: gateResults, BlockingIssues: []qualitygate.BlockingIssueV1{},
	}
	auditBytes, _, err := qualitygate.CanonicalAuditV1(audit)
	if err != nil {
		return nil, fmt.Errorf("canonical nine-gate audit: %w", err)
	}
	auditRef := artifactRef(auditBytes, bucket, workflowID, "RecomputeS3VerdictActivityV1", "application/json")
	evidence := activities.S3QualityPassDraftEvidenceV1{
		SchemaVersion: generationapi.S3QualityMaterializationSchemaV1, Decision: qualitygate.DecisionPass,
		SubjectRevision: refs["final"].SHA256, EvidenceLevel: generationapi.EvidenceMinimal,
		AuditArtifact: auditRef, AuthoringBundleArtifact: refs["authoring"], StatementDraftArtifact: refs["draft"],
		FinalStatementArtifact: refs["final"], MainProgramArtifact: refs["main"], OracleProgramArtifact: refs["oracle"],
		OracleReceiptArtifact: refs["receipt"], TestManifestArtifact: manifestRef,
	}
	manifestPath := fmt.Sprintf("problems/%s/test_manifest.v2.json", problemID)
	metadata := map[string]interface{}{
		"activity_payload_version": activities.StoreProblemS3QualityDraftPayloadVersion,
		"test_manifest": map[string]interface{}{
			"schema_version": activities.TestManifestSchemaVersionV2,
			"sha256":         manifestRef.SHA256, "path": manifestPath, "artifact": manifestRef,
		},
		generationapi.S3QualityMaterializationMetadataKey: evidence,
	}
	problem.MetadataJSON, err = json.Marshal(metadata)
	if err != nil {
		return nil, err
	}
	objects.Data[refs["draft"].Key] = draftBytes
	objects.Data[refs["final"].Key] = finalBytes
	objects.Data[auditRef.Key] = auditBytes
	objects.Data[manifestRef.Key] = manifestBytes
	objects.Data[manifestPath] = manifestBytes

	return &CanonicalS3QualityDraft{
		Problem: problem, TestCases: testCases, Objects: objects,
		AuditArtifactKey: auditRef.Key, ManifestPath: manifestPath,
	}, nil
}

func artifactRef(data []byte, bucket, workflowID, producer, contentType string) activities.ArtifactRef {
	sha := digest(data)
	return activities.ArtifactRef{
		SchemaVersion: activities.ArtifactRefSchemaVersion, PayloadVersion: activities.ActivityPayloadVersion,
		Bucket: bucket, Key: fmt.Sprintf("workflow-artifacts/v1/sha256/%s/%s", sha[:2], sha),
		SHA256: sha, SizeBytes: int64(len(data)), ContentType: contentType, Producer: producer,
		Provider: "algoforge-test", Model: "deterministic-fixture", ModelRevision: "v1", WorkflowID: workflowID,
	}
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum[:])
}
