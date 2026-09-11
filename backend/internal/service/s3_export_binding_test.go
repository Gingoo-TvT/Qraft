package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/generationapi"
	qualitygate "github.com/Gingoo-TvT/Qraft/backend/internal/qualitygate/v1"
	speccontract "github.com/Gingoo-TvT/Qraft/backend/internal/spec"
	"github.com/Gingoo-TvT/Qraft/backend/internal/workflow/activities"
)

type qg15AS3ExportFixtureV1 struct {
	request          HydroPackageRequest
	manifest         *activities.TestManifestV2
	reader           *qg15AHydroCountingReader
	evidence         activities.S3QualityPassDraftEvidenceV1
	manifestMetadata s3ExportManifestMetadataV1
}

type qg15AS3SecondReadTamperReaderV1 struct {
	base       HydroObjectReader
	targetPath string
	reads      int
}

func (reader *qg15AS3SecondReadTamperReaderV1) DownloadFile(ctx context.Context, objectPath string) ([]byte, error) {
	data, err := reader.base.DownloadFile(ctx, objectPath)
	if err != nil {
		return nil, err
	}
	if objectPath == reader.targetPath {
		reader.reads++
		if reader.reads == 2 {
			return []byte("tampered only after loader verification\n"), nil
		}
	}
	return data, nil
}

func TestS3ExportBindingLoaderAcceptsCanonicalV6(t *testing.T) {
	fixture := qg15AS3ExportFixture(t)
	binding, err := loadS3ExportBindingV1(context.Background(), fixture.request.Problem, fixture.request.TestCases, fixture.reader)
	if err != nil {
		t.Fatalf("load canonical S3 export binding: %v", err)
	}
	if binding == nil || binding.TestManifestV2 == nil || binding.TestManifestV2.TestCount != len(fixture.request.TestCases) ||
		binding.StatementDraftV1 == nil || binding.QualityAuditV1.Decision != qualitygate.DecisionPass ||
		!bytes.Equal(binding.QualityAuditBytes, fixture.reader.data[fixture.evidence.AuditArtifact.Key]) ||
		binding.QualityAuditRef != fixture.evidence.AuditArtifact || binding.TestManifestRef != fixture.evidence.TestManifestArtifact ||
		binding.AuthoringBundleRef != fixture.evidence.AuthoringBundleArtifact ||
		binding.EvidenceLevel != fixture.evidence.EvidenceLevel || binding.SubjectRevision != fixture.evidence.SubjectRevision {
		t.Fatalf("binding = %+v", binding)
	}
	fixture.request.Problem.Status = domain.ProblemStatusPublished
	if _, err := loadS3ExportBindingV1(context.Background(), fixture.request.Problem, fixture.request.TestCases, fixture.reader); err != nil {
		t.Fatalf("same S3 evidence on published record: %v", err)
	}
}

func TestS3ExportBindingLoaderAcceptsAuditProfileAndRejectsPersistedLevelDrift(t *testing.T) {
	fixture := qg15AS3ExportFixture(t)
	fixture.evidence.EvidenceLevel = generationapi.EvidenceAudit
	qg15ARefreshS3ExportMetadata(t, fixture, activities.StoreProblemS3QualityDraftPayloadVersion)
	if _, err := loadS3ExportBindingV1(context.Background(), fixture.request.Problem, fixture.request.TestCases, fixture.reader); err != nil {
		t.Fatalf("load audit S3 export binding: %v", err)
	}
	qg15ASetRootMetadataField(t, fixture.request.Problem, generationapi.QualityEvidenceLevelMetadataKey, generationapi.EvidenceStandard)
	if _, err := loadS3ExportBindingV1(context.Background(), fixture.request.Problem, fixture.request.TestCases, fixture.reader); err == nil || !strings.Contains(err.Error(), "evidence level") {
		t.Fatalf("persisted evidence-level drift was accepted: %v", err)
	}
	var metadata map[string]interface{}
	if err := json.Unmarshal(fixture.request.Problem.MetadataJSON, &metadata); err != nil {
		t.Fatal(err)
	}
	delete(metadata, generationapi.QualityEvidenceLevelMetadataKey)
	missingLevel, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	fixture.request.Problem.MetadataJSON = missingLevel
	if _, err := loadS3ExportBindingV1(context.Background(), fixture.request.Problem, fixture.request.TestCases, fixture.reader); err == nil || !strings.Contains(err.Error(), "evidence level is missing") {
		t.Fatalf("missing audit evidence-level binding was accepted: %v", err)
	}
}

func TestS3ExportBindingLoaderRejectsMetadataAuditManifestAndAssetTampering(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, *qg15AS3ExportFixtureV1)
		want   string
	}{
		{
			name: "legacy published metadata bypass",
			mutate: func(_ *testing.T, fixture *qg15AS3ExportFixtureV1) {
				fixture.request.Problem.MetadataJSON = json.RawMessage(`{"publication_gate_status":"published"}`)
			},
			want: "activity_payload_version",
		},
		{
			name: "wrong v6 payload version",
			mutate: func(t *testing.T, fixture *qg15AS3ExportFixtureV1) {
				qg15ARefreshS3ExportMetadata(t, fixture, 5)
			},
			want: "activity_payload_version",
		},
		{
			name: "unknown test manifest metadata field",
			mutate: func(t *testing.T, fixture *qg15AS3ExportFixtureV1) {
				var metadata map[string]interface{}
				if err := json.Unmarshal(fixture.request.Problem.MetadataJSON, &metadata); err != nil {
					t.Fatal(err)
				}
				manifestMetadata := metadata["test_manifest"].(map[string]interface{})
				manifestMetadata["unknown"] = true
				encoded, err := json.Marshal(metadata)
				if err != nil {
					t.Fatal(err)
				}
				fixture.request.Problem.MetadataJSON = encoded
			},
			want: "unknown field",
		},
		{
			name: "S3 decision is not pass",
			mutate: func(t *testing.T, fixture *qg15AS3ExportFixtureV1) {
				fixture.evidence.Decision = qualitygate.DecisionQuarantine
				qg15ARefreshS3ExportMetadata(t, fixture, activities.StoreProblemS3QualityDraftPayloadVersion)
			},
			want: "canonical pass decision",
		},
		{
			name: "stale trusted draft",
			mutate: func(t *testing.T, fixture *qg15AS3ExportFixtureV1) {
				qg15ASetRootMetadataField(t, fixture.request.Problem, "stale", true)
			},
			want: "problem is stale",
		},
		{
			name: "edited statement drifts from final artifact",
			mutate: func(_ *testing.T, fixture *qg15AS3ExportFixtureV1) {
				fixture.request.Problem.Statement += "\npost-gate edit"
			},
			want: "persisted problem statement or final-statement ancestry drifted",
		},
		{
			name: "review status is not exportable",
			mutate: func(_ *testing.T, fixture *qg15AS3ExportFixtureV1) {
				fixture.request.Problem.Status = domain.ProblemStatusReview
			},
			want: "not a fresh trusted draft or published",
		},
		{
			name: "explicit publication denial",
			mutate: func(t *testing.T, fixture *qg15AS3ExportFixtureV1) {
				qg15ASetRootMetadataField(t, fixture.request.Problem, "publication_gate_status", string(domain.ProblemStatusQuarantined))
			},
			want: "explicitly denied",
		},
		{
			name: "artifact producer tamper",
			mutate: func(t *testing.T, fixture *qg15AS3ExportFixtureV1) {
				fixture.evidence.MainProgramArtifact.Producer = "GenerateSolution"
				qg15ARefreshS3ExportMetadata(t, fixture, activities.StoreProblemS3QualityDraftPayloadVersion)
			},
			want: "producer/workflow",
		},
		{
			name: "artifact workflow tamper",
			mutate: func(t *testing.T, fixture *qg15AS3ExportFixtureV1) {
				fixture.evidence.OracleProgramArtifact.WorkflowID += "-other"
				qg15ARefreshS3ExportMetadata(t, fixture, activities.StoreProblemS3QualityDraftPayloadVersion)
			},
			want: "producer/workflow",
		},
		{
			name: "quality audit bytes tamper",
			mutate: func(_ *testing.T, fixture *qg15AS3ExportFixtureV1) {
				fixture.reader.data[fixture.evidence.AuditArtifact.Key] = []byte("{}")
			},
			want: "quality audit",
		},
		{
			name: "canonical audit is not pass",
			mutate: func(t *testing.T, fixture *qg15AS3ExportFixtureV1) {
				oldRef := fixture.evidence.AuditArtifact
				var audit qualitygate.AuditV1
				if err := json.Unmarshal(fixture.reader.data[oldRef.Key], &audit); err != nil {
					t.Fatal(err)
				}
				audit.Decision = qualitygate.DecisionQuarantine
				encoded, _, err := qualitygate.CanonicalAuditV1(audit)
				if err != nil {
					t.Fatal(err)
				}
				newRef := qg15AS3ArtifactRef(encoded, oldRef.Bucket, oldRef.WorkflowID, oldRef.Producer)
				fixture.evidence.AuditArtifact = newRef
				fixture.reader.data[newRef.Key] = encoded
				qg15ARefreshS3ExportMetadata(t, fixture, activities.StoreProblemS3QualityDraftPayloadVersion)
			},
			want: "nine-gate PASS",
		},
		{
			name: "manifest path tamper",
			mutate: func(t *testing.T, fixture *qg15AS3ExportFixtureV1) {
				fixture.manifestMetadata.Path = "problems/other/test_manifest.v2.json"
				qg15ARefreshS3ExportMetadata(t, fixture, activities.StoreProblemS3QualityDraftPayloadVersion)
			},
			want: "test_manifest path",
		},
		{
			name: "manifest CAS size tamper",
			mutate: func(t *testing.T, fixture *qg15AS3ExportFixtureV1) {
				fixture.evidence.TestManifestArtifact.SizeBytes++
				fixture.manifestMetadata.Artifact = fixture.evidence.TestManifestArtifact
				qg15ARefreshS3ExportMetadata(t, fixture, activities.StoreProblemS3QualityDraftPayloadVersion)
			},
			want: "path size or SHA-256",
		},
		{
			name: "manifest path bytes tamper",
			mutate: func(_ *testing.T, fixture *qg15AS3ExportFixtureV1) {
				fixture.reader.data[fixture.manifestMetadata.Path] = []byte("{}")
			},
			want: "path size or SHA-256",
		},
		{
			name: "persisted input bytes tamper",
			mutate: func(_ *testing.T, fixture *qg15AS3ExportFixtureV1) {
				fixture.reader.data[fixture.request.TestCases[1].InputPath] = []byte("asset splice\n")
			},
			want: "SHA-256 or size mismatch",
		},
		{
			name: "persisted index tamper",
			mutate: func(_ *testing.T, fixture *qg15AS3ExportFixtureV1) {
				fixture.request.TestCases[2].TestIndex = 99
			},
			want: "index/problem identity mismatch",
		},
		{
			name: "persisted sample tamper",
			mutate: func(_ *testing.T, fixture *qg15AS3ExportFixtureV1) {
				fixture.request.TestCases[0].IsSample = false
			},
			want: "sample identity mismatch",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := qg15AS3ExportFixture(t)
			test.mutate(t, fixture)
			_, err := loadS3ExportBindingV1(context.Background(), fixture.request.Problem, fixture.request.TestCases, fixture.reader)
			if err == nil || !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v, want conflict containing %q", err, test.want)
			}
		})
	}
}

func TestHydroExportServiceRejectsLegacyPublishedBypass(t *testing.T) {
	request, _, reader := qg15AHydroManifestFixture(t)
	workflowID := generationapi.JobIDPrefix + strings.Repeat("a", 64)
	request.Problem.WorkflowID = &workflowID
	request.Problem.Status = domain.ProblemStatusDraft
	request.Problem.MetadataJSON = json.RawMessage(`{"publication_gate_status":"published"}`)
	service := &HydroExportService{
		problems: &qg15AHydroProblemSource{problem: request.Problem, testCases: request.TestCases},
		objects:  reader,
	}
	_, err := service.BuildProblemPackage(context.Background(), request.Problem.ID)
	if err == nil || !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "activity_payload_version") {
		t.Fatalf("legacy Hydro product export error=%v", err)
	}
}

func qg15AS3ExportFixture(t *testing.T) *qg15AS3ExportFixtureV1 {
	t.Helper()
	request, manifest, reader := qg15AHydroManifestFixture(t)
	workflowID := generationapi.JobIDPrefix + strings.Repeat("1", 64)
	bucket := "qg15a-s3-export-fixture"
	request.Problem.WorkflowID = &workflowID
	request.Problem.Status = domain.ProblemStatusDraft

	for index := range manifest.Cases {
		manifest.Cases[index].InputArtifact.WorkflowID = workflowID
		manifest.Cases[index].InputArtifact.Bucket = bucket
		manifest.Cases[index].OutputArtifact.WorkflowID = workflowID
		manifest.Cases[index].OutputArtifact.Bucket = bucket
	}
	briefSHA := strings.Repeat("9", 64)
	semantic := domain.SemanticSpecV1{SchemaVersion: domain.SemanticSpecSchemaV1, BriefSHA256: briefSHA}
	semanticSHA := speccontract.SemanticSpecSHA256V1(semantic)
	plan := domain.AuthoringPlanV1{
		SchemaVersion: domain.AuthoringPlanSchemaV1, BriefSHA256: briefSHA, SemanticSpecSHA256: semanticSHA,
		ConceptRoles:     []domain.AuthoringConceptRoleV1{{Slug: "dp", Role: "primary", Necessity: "required"}, {Slug: "graphs", Role: "auxiliary", Necessity: "required"}},
		TargetDifficulty: 1500,
	}
	planBytes, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("encode fixture authoring plan: %v", err)
	}
	manifest.SemanticSpecSHA256 = semanticSHA
	manifest.AuthoringPlanSHA256 = sha256Hex(planBytes)
	authoring := activities.AuthoringPlanBundleV1{
		SchemaVersion: activities.AuthoringPlanBundleSchemaV1,
		ModelDecision: activities.AuthoringPlanDecisionAccepted,
		Decision:      activities.AuthoringPlanDecisionAccepted,
		SemanticSpec:  &semantic,
		AuthoringPlan: &plan,
	}
	authoringBytes, err := json.Marshal(authoring)
	if err != nil {
		t.Fatalf("encode fixture authoring bundle: %v", err)
	}
	request.Problem.Difficulty = plan.TargetDifficulty
	request.Problem.Tags = []string{"dp", "graphs"}
	refs := map[string]activities.ArtifactRef{
		"authoring": qg15AS3ArtifactRef(authoringBytes, bucket, workflowID, "GenerateAuthoringPlanActivity"),
		"main":      qg15AS3ArtifactRef([]byte("main source"), bucket, workflowID, "GenerateMainSolutionActivityV1"),
		"oracle":    qg15AS3ArtifactRef([]byte("oracle source"), bucket, workflowID, "GenerateOracleCandidateActivityV1"),
		"receipt":   qg15AS3ArtifactRef([]byte("oracle receipt"), bucket, workflowID, activities.VerifiedProgramReceiptProducerV1),
	}
	draftMarkdown := "draft statement without finalized samples"
	factManifest := activities.StatementFactManifestV1{
		SchemaVersion:     activities.StatementFactManifestSchemaV1,
		ProblemDefinition: "Return the required deterministic value.",
		InputGrammar: domain.SemanticGrammarV1{
			Profile: domain.SemanticGrammarTokenLinesV1,
			Lines:   []domain.SemanticGrammarLineV1{},
		},
		OutputGrammar: domain.SemanticGrammarV1{
			Profile: domain.SemanticGrammarTokenLinesV1,
			Lines:   []domain.SemanticGrammarLineV1{},
		},
		Symbols:     []domain.SemanticSymbolV1{},
		Constraints: []domain.SemanticConstraintV1{},
		Relations:   []domain.SemanticRelationV1{},
		Boundaries:  []domain.SemanticBoundaryV1{},
	}
	factManifestBytes, err := json.Marshal(factManifest)
	if err != nil {
		t.Fatalf("encode fixture fact manifest: %v", err)
	}
	draft := activities.StatementDraftBundleV1{
		SchemaVersion:         activities.StatementDraftBundleSchemaV1,
		AuthoringBundleSHA256: refs["authoring"].SHA256,
		SemanticSpecSHA256:    manifest.SemanticSpecSHA256,
		FactManifestSHA256:    sha256Hex(factManifestBytes),
		FactManifest:          factManifest,
		Title:                 request.Problem.Title, Markdown: draftMarkdown, MarkdownSHA256: sha256Hex([]byte(draftMarkdown)),
	}
	draftBytes, err := json.Marshal(draft)
	if err != nil {
		t.Fatalf("encode fixture statement draft: %v", err)
	}
	refs["draft"] = qg15AS3ArtifactRef(draftBytes, bucket, workflowID, "RenderStatementFromAuthoringBundleActivityV1")
	final := activities.FinalAuthoringStatementSamplesBundleV1{
		SchemaVersion:                  activities.FinalAuthoringStatementSamplesSchemaV1,
		Status:                         activities.FinalAuthoringStatementSamplesStatusV1,
		AuthoringBundleSHA256:          refs["authoring"].SHA256,
		StatementDraftSHA256:           refs["draft"].SHA256,
		ProgramSHA256:                  refs["main"].SHA256,
		IndependentOracleReceiptSHA256: refs["receipt"].SHA256,
		SemanticSpecSHA256:             manifest.SemanticSpecSHA256,
		Samples:                        []activities.AuthoringStatementSampleRecordV1{},
		Markdown:                       request.Problem.Statement, MarkdownSHA256: sha256Hex([]byte(request.Problem.Statement)),
	}
	finalBytes, err := json.Marshal(final)
	if err != nil {
		t.Fatalf("encode fixture final statement: %v", err)
	}
	refs["final"] = qg15AS3ArtifactRef(finalBytes, bucket, workflowID, "FinalizeAuthoringStatementSamplesActivityV1")
	manifest.OraclePromotionReceiptSHA256 = refs["receipt"].SHA256
	manifestBytes, _, err := activities.CanonicalTestManifestV2JSON(*manifest)
	if err != nil {
		t.Fatalf("canonical fixture TestManifest v2: %v", err)
	}
	manifestRef := qg15AS3ArtifactRef(manifestBytes, bucket, workflowID, "BuildS3TestManifestActivityV1")

	_, ruleSHA, err := qualitygate.CanonicalRuleV1()
	if err != nil {
		t.Fatalf("canonical quality rule: %v", err)
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
		t.Fatalf("canonical quality audit: %v", err)
	}
	auditRef := qg15AS3ArtifactRef(auditBytes, bucket, workflowID, "RecomputeS3VerdictActivityV1")
	evidence := activities.S3QualityPassDraftEvidenceV1{
		SchemaVersion: generationapi.S3QualityMaterializationSchemaV1, Decision: qualitygate.DecisionPass,
		SubjectRevision: refs["final"].SHA256, EvidenceLevel: generationapi.EvidenceMinimal,
		AuditArtifact: auditRef, AuthoringBundleArtifact: refs["authoring"], StatementDraftArtifact: refs["draft"],
		FinalStatementArtifact: refs["final"], MainProgramArtifact: refs["main"], OracleProgramArtifact: refs["oracle"],
		OracleReceiptArtifact: refs["receipt"], TestManifestArtifact: manifestRef,
	}
	manifestPath := fmt.Sprintf("problems/%s/test_manifest.v2.json", request.Problem.ID.String())
	fixture := &qg15AS3ExportFixtureV1{
		request: request, manifest: manifest, reader: reader, evidence: evidence,
		manifestMetadata: s3ExportManifestMetadataV1{
			SchemaVersion: activities.TestManifestSchemaVersionV2,
			SHA256:        manifestRef.SHA256, Path: manifestPath, Artifact: manifestRef,
		},
	}
	reader.data[auditRef.Key] = auditBytes
	reader.data[refs["authoring"].Key] = authoringBytes
	reader.data[refs["draft"].Key] = draftBytes
	reader.data[refs["final"].Key] = finalBytes
	reader.data[manifestRef.Key] = manifestBytes
	reader.data[manifestPath] = manifestBytes
	qg15ARefreshS3ExportMetadata(t, fixture, activities.StoreProblemS3QualityDraftPayloadVersion)
	return fixture
}

func qg15ARefreshS3ExportMetadata(t *testing.T, fixture *qg15AS3ExportFixtureV1, payloadVersion int) {
	t.Helper()
	metadata := map[string]interface{}{
		"activity_payload_version":                        payloadVersion,
		"test_manifest":                                   fixture.manifestMetadata,
		generationapi.QualityEvidenceLevelMetadataKey:     fixture.evidence.EvidenceLevel,
		generationapi.S3QualityMaterializationMetadataKey: fixture.evidence,
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		t.Fatalf("encode S3 export fixture metadata: %v", err)
	}
	fixture.request.Problem.MetadataJSON = encoded
}

func qg15AS3ArtifactRef(data []byte, bucket, workflowID, producer string) activities.ArtifactRef {
	digest := sha256Hex(data)
	return activities.ArtifactRef{
		SchemaVersion: activities.ArtifactRefSchemaVersion, PayloadVersion: activities.ActivityPayloadVersion,
		Bucket: bucket, Key: fmt.Sprintf("workflow-artifacts/v1/sha256/%s/%s", digest[:2], digest),
		SHA256: digest, SizeBytes: int64(len(data)), ContentType: "application/json",
		Producer: producer, Provider: "algoforge-test", Model: "deterministic-fixture",
		ModelRevision: "v1", WorkflowID: workflowID,
	}
}

func qg15AAddHydroMetadata(t *testing.T, problem *domain.Problem, hydro map[string]interface{}) {
	t.Helper()
	var metadata map[string]interface{}
	if err := json.Unmarshal(problem.MetadataJSON, &metadata); err != nil {
		t.Fatalf("decode fixture metadata: %v", err)
	}
	metadata["hydro"] = hydro
	encoded, err := json.Marshal(metadata)
	if err != nil {
		t.Fatalf("encode fixture Hydro metadata: %v", err)
	}
	problem.MetadataJSON = encoded
}

func qg15ASetRootMetadataField(t *testing.T, problem *domain.Problem, name string, value interface{}) {
	t.Helper()
	var metadata map[string]interface{}
	if err := json.Unmarshal(problem.MetadataJSON, &metadata); err != nil {
		t.Fatalf("decode fixture metadata: %v", err)
	}
	metadata[name] = value
	encoded, err := json.Marshal(metadata)
	if err != nil {
		t.Fatalf("encode fixture metadata: %v", err)
	}
	problem.MetadataJSON = encoded
}

var _ hydroProblemSource = (*qg15AHydroProblemSource)(nil)
var _ HydroObjectReader = (*qg15AS3SecondReadTamperReaderV1)(nil)
