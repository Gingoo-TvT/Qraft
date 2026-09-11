package activities

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/generationapi"
	qualitygate "github.com/Gingoo-TvT/Qraft/backend/internal/qualitygate/v1"
)

func TestS3QualityDraftTestCasesLeaveLegacyGroupAndScoreZero(t *testing.T) {
	inputOne := ArtifactRef{SHA256: "input-one"}
	outputOne := ArtifactRef{SHA256: "output-one"}
	inputTwo := ArtifactRef{SHA256: "input-two"}
	outputTwo := ArtifactRef{SHA256: "output-two"}
	manifest := &TestManifestV2{Cases: []TestManifestCaseV2{
		{Purpose: TestManifestPurposeSample, ConstraintRegion: "sample", InputArtifact: &inputOne, OutputArtifact: &outputOne},
		{Purpose: TestManifestPurposeBoundary, ConstraintRegion: "boundary", InputArtifact: &inputTwo, OutputArtifact: &outputTwo},
	}}

	cases, outputs := s3QualityDraftTestCasesV1(manifest)
	if len(cases) != 2 || len(outputs) != 2 {
		t.Fatalf("materialized cases=%d outputs=%d, want 2/2", len(cases), len(outputs))
	}
	for index, testCase := range cases {
		if testCase.GroupID != 0 {
			t.Fatalf("case %d legacy GroupID=%d, want 0", index, testCase.GroupID)
		}
	}
	if !cases[0].IsSample || cases[1].IsSample {
		t.Fatalf("sample flags = [%v,%v], want [true,false]", cases[0].IsSample, cases[1].IsSample)
	}
	if !reflect.DeepEqual(outputs, []*ArtifactRef{&outputOne, &outputTwo}) {
		t.Fatalf("output artifact projection differs: %#v", outputs)
	}

	legacyConfig := domain.TestDataConfig{Groups: []domain.TestGroup{{GroupID: 1, NumCases: 2, Score: 100}}}
	if scores := computeCaseScores(legacyConfig, cases); !reflect.DeepEqual(scores, []int{0, 0}) {
		t.Fatalf("persisted legacy scores = %#v, want [0 0]", scores)
	}
}

func TestBuildS3QualityDraftStoreInputV1MaterializesCanonicalCASWithoutLegacyScoring(t *testing.T) {
	ctx := context.Background()
	store := newOracleMemoryArtifactStore()
	activities := &Activities{artifacts: store}
	workflowID := generationapi.JobIDPrefix + strings.Repeat("6", 64)
	put := func(data []byte, contentType, producer string) ArtifactRef {
		t.Helper()
		ref, err := store.Put(ctx, data, contentType, ArtifactMetadata{
			Producer: producer, Provider: "fixture-provider", Model: "fixture-model",
			ModelRevision: "fixture-revision", WorkflowID: workflowID,
		})
		if err != nil {
			t.Fatalf("put %s fixture: %v", producer, err)
		}
		return ref
	}
	putJSON := func(value interface{}, producer string) ArtifactRef {
		t.Helper()
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("marshal %s fixture: %v", producer, err)
		}
		return put(data, "application/json", producer)
	}
	receipt := func(label string) *LLMCallReceipt {
		return &LLMCallReceipt{
			SchemaVersion:  1,
			RequestedModel: "fixture-model",
			ReturnedModel:  "fixture-model-" + label,
			Provider:       "fixture-provider",
			EndpointID:     strings.Repeat("a", 64),
			PromptHash:     sha256Hex([]byte("prompt:" + label)),
			RequestSHA256:  sha256Hex([]byte("request:" + label)),
		}
	}

	authoringRef := put([]byte(`{"fixture":"authoring"}`), "application/json", "GenerateAuthoringPlanActivity")
	mainRef := put([]byte("int main(){return 0;}\n"), "text/plain; charset=utf-8", "GenerateMainSolutionActivityV1")
	oracleRef := put([]byte("int main(){return 0;} // oracle\n"), "text/plain; charset=utf-8", "GenerateOracleCandidateActivityV1")
	semanticSpec := validOracleSemanticSpecV1(t)
	semanticSHA, err := canonicalJSONSHA256(semanticSpec)
	if err != nil {
		t.Fatal(err)
	}
	facts := statementFactManifestFromSemanticSpecV1(semanticSpec)
	factSHA, err := canonicalJSONSHA256(facts)
	if err != nil {
		t.Fatal(err)
	}
	model := statementNarrativeModelOutputV1{Title: "Canonical materializer fixture", Narrative: "A deterministic seam fixture."}
	modelSHA, err := canonicalJSONSHA256(model)
	if err != nil {
		t.Fatal(err)
	}
	draftMarkdown, err := renderStatementDraftMarkdownV1("en", model, facts)
	if err != nil {
		t.Fatal(err)
	}
	draftRef := putJSON(StatementDraftBundleV1{
		SchemaVersion: StatementDraftBundleSchemaV1, DocumentStatus: statementDraftDocumentStatusV1,
		RendererInputSHA256: strings.Repeat("1", 64), AuthoringBundleSHA256: authoringRef.SHA256,
		AuthoringInputSHA256: strings.Repeat("2", 64), BriefSHA256: semanticSpec.BriefSHA256,
		SemanticSpecSHA256: semanticSHA, FactManifestSHA256: factSHA, ModelOutputSHA256: modelSHA,
		RendererSourceArtifactSHA256: strings.Repeat("3", 64), RendererSourceRequestSHA256: strings.Repeat("4", 64),
		DerivationRule: statementDraftDerivationRuleV1, PresentationLocale: "en",
		Title: model.Title, Narrative: model.Narrative, FactManifest: facts,
		Markdown: draftMarkdown, MarkdownSHA256: sha256Hex([]byte(draftMarkdown)),
	}, "RenderStatementFromAuthoringBundleActivityV1")
	oracleReceiptRef := putJSON(S3OracleGateReceiptV1{
		SchemaVersion: S3OracleGateReceiptSchemaV1, Status: qualitygate.GateStatusPass,
		SemanticSpecSHA256: semanticSHA, CandidateSHA256: mainRef.SHA256,
		DifferentialCaseIDs: []string{"case-000000", "case-000001"}, OrderedInputSHA256: []string{},
		Promotion: &OraclePromotionReceiptV1{
			SchemaVersion: OraclePromotionSchemaV1, SemanticSpecSHA256: semanticSHA,
			CandidateSourceSHA256: oracleRef.SHA256, DifferentialCaseIDs: []string{"case-000000", "case-000001"}, Promoted: true,
		},
	}, VerifiedProgramReceiptProducerV1)
	statement := "# Canonical problem\n\nRead two integers."
	finalRef := putJSON(FinalAuthoringStatementSamplesBundleV1{
		SchemaVersion: FinalAuthoringStatementSamplesSchemaV1, Status: FinalAuthoringStatementSamplesStatusV1,
		AuthoringBundleSHA256: authoringRef.SHA256, StatementDraftSHA256: draftRef.SHA256,
		ProgramSHA256: mainRef.SHA256, IndependentOracleReceiptSHA256: oracleReceiptRef.SHA256,
		SemanticSpecSHA256: semanticSHA, Samples: []AuthoringStatementSampleRecordV1{},
		Markdown: statement, MarkdownSHA256: sha256Hex([]byte(statement)),
	}, "FinalizeAuthoringStatementSamplesActivityV1")

	inputRefs := make([]ArtifactRef, 2)
	outputRefs := make([]ArtifactRef, 2)
	manifestCases := make([]TestManifestCaseV2, 2)
	for index := range manifestCases {
		inputRefs[index] = put([]byte{byte('1' + index), '\n'}, "text/plain; charset=utf-8", "MaterializerFixtureInput")
		outputRefs[index] = put([]byte{byte('2' + index), '\n'}, "text/plain; charset=utf-8", "MaterializerFixtureOutput")
		if index == 0 {
			inputRefs[index].LLMCallReceipt = receipt("input")
			outputRefs[index].LLMCallReceipt = receipt("output")
		}
		seed := int64(9000 + index)
		purpose := TestManifestPurposeBoundary
		region := "boundary"
		boundaryRefs := []string{"n-max"}
		if index == 0 {
			purpose = TestManifestPurposeSample
			region = "sample"
			boundaryRefs = []string{}
		}
		manifestCases[index] = TestManifestCaseV2{
			TestIndex: index, TestID: "case-00000" + string(rune('0'+index)), Purpose: purpose,
			ConstraintRegion: region, BoundaryRefs: boundaryRefs, Seed: &seed,
			InputArtifact: &inputRefs[index], InputSHA256: inputRefs[index].SHA256,
			OutputArtifact: &outputRefs[index], OutputSHA256: outputRefs[index].SHA256,
			KilledWrongIDs: []string{}, KilledWrongIDsRetentionReason: "materializer seam fixture",
		}
	}
	manifestBytes, _, err := CanonicalTestManifestV2JSON(TestManifestV2{
		SchemaVersion: TestManifestSchemaVersionV2, SemanticSpecSHA256: semanticSHA,
		AuthoringPlanSHA256: strings.Repeat("b", 64), OraclePromotionReceiptSHA256: oracleReceiptRef.SHA256,
		SanitizerReceiptSHA256: strings.Repeat("d", 64), BoundaryCoverageReceiptSHA256: strings.Repeat("e", 64),
		TestCount: len(manifestCases), Cases: manifestCases,
	})
	if err != nil {
		t.Fatalf("canonical TestManifest v2: %v", err)
	}
	manifestRef := put(manifestBytes, "application/json", "BuildS3TestManifestActivityV1")
	manifestRef.LLMCallReceipt = receipt("manifest")

	_, ruleSHA, err := qualitygate.CanonicalRuleV1()
	if err != nil {
		t.Fatal(err)
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
	auditBytes, _, err := qualitygate.CanonicalAuditV1(qualitygate.AuditV1{
		SchemaVersion: qualitygate.AuditSchemaVersionV1, RuleVersion: qualitygate.RuleVersionV1,
		RuleSHA256: ruleSHA, InputSHA256: strings.Repeat("9", 64), SubjectID: workflowID,
		SubjectRevision: finalRef.SHA256, Decision: qualitygate.DecisionPass,
		DeterministicGatesPassed: true, ReviewerDeclaredApproved: true, ReviewerAdvisoryApproved: true,
		GateResults: gateResults, BlockingIssues: []qualitygate.BlockingIssueV1{},
	})
	if err != nil {
		t.Fatalf("canonical quality audit: %v", err)
	}
	auditRef := put(auditBytes, "application/json", "RecomputeS3VerdictActivityV1")

	params := domain.DefaultProblemGenParams()
	params.TimeLimit = 1500
	params.MemoryLimit = 192
	params.TestDataConfig = domain.TestDataConfig{
		NumTestCases: 2, NumSamples: 1,
		Groups: []domain.TestGroup{{GroupID: 1, NumCases: 2, Score: 100}},
	}
	materializerInput := StoreS3QualityDraftInputV1{
		PayloadVersion: StoreS3QualityDraftPayloadVersionV1, WorkflowID: workflowID,
		SubjectRevision: finalRef.SHA256, FrozenConcept: "canonical seam", Language: "cpp",
		EvidenceLevel: generationapi.EvidenceMinimal, Params: params,
		AuthoringBundleArtifact: authoringRef, StatementDraftArtifact: draftRef, FinalStatementArtifact: finalRef,
		MainProgramArtifact: mainRef, OracleProgramArtifact: oracleRef, OracleReceiptArtifact: oracleReceiptRef,
		TestManifestArtifact: manifestRef, AuditArtifact: auditRef,
	}
	storeInput, err := activities.buildS3QualityDraftStoreInputV1(ctx, materializerInput)
	if err != nil {
		t.Fatalf("buildS3QualityDraftStoreInputV1: %v", err)
	}
	if storeInput.PayloadVersion != StoreProblemS3QualityDraftPayloadVersion || storeInput.Params.TimeLimit != 1500 || storeInput.Params.MemoryLimit != 192 {
		t.Fatalf("materialized Store input identity/limits = version %d time %d memory %d", storeInput.PayloadVersion, storeInput.Params.TimeLimit, storeInput.Params.MemoryLimit)
	}
	if len(storeInput.TestCases) != 2 || !storeInput.TestCases[0].IsSample || storeInput.TestCases[1].IsSample {
		t.Fatalf("materialized sample projection = %#v", storeInput.TestCases)
	}
	for index, testCase := range storeInput.TestCases {
		if testCase.GroupID != 0 || testCase.InputArtifact == nil || testCase.InputArtifact.SHA256 != inputRefs[index].SHA256 {
			t.Fatalf("materialized case %d = %#v", index, testCase)
		}
	}
	if scores := computeCaseScores(storeInput.Params.TestDataConfig, storeInput.TestCases); !reflect.DeepEqual(scores, []int{0, 0}) {
		t.Fatalf("materialized legacy scores = %#v, want [0 0]", scores)
	}
	if storeInput.QualityPassDraft == nil || storeInput.TestManifestV2 == nil || len(storeInput.SourceArtifacts) != 8 {
		t.Fatalf("materialized S3 evidence is incomplete: %+v", storeInput)
	}
	encodedStoreInput, err := json.Marshal(storeInput)
	if err != nil {
		t.Fatal(err)
	}
	var roundTrippedStoreInput StoreInput
	if err := json.Unmarshal(encodedStoreInput, &roundTrippedStoreInput); err != nil {
		t.Fatal(err)
	}
	if roundTrippedStoreInput.SourceArtifacts[6].LLMCallReceipt == roundTrippedStoreInput.QualityPassDraft.TestManifestArtifact.LLMCallReceipt ||
		roundTrippedStoreInput.TestCases[0].InputArtifact.LLMCallReceipt == roundTrippedStoreInput.TestManifestV2.Cases[0].InputArtifact.LLMCallReceipt {
		t.Fatal("JSON round trip unexpectedly preserved S3 ArtifactRef receipt pointer aliases")
	}
	if err := activities.validateS3QualityPassDraftStoreInputV1(ctx, roundTrippedStoreInput); err != nil {
		t.Fatalf("validate JSON-round-tripped S3 Store evidence: %v", err)
	}
	if err := activities.validateTestManifestStoreInput(ctx, roundTrippedStoreInput); err != nil {
		t.Fatalf("validate JSON-round-tripped S3 TestManifest bindings: %v", err)
	}

	roundTrippedStoreInput.SourceArtifacts[6].LLMCallReceipt.ReturnedModel = "tampered-manifest-model"
	if err := activities.validateS3QualityPassDraftStoreInputV1(ctx, roundTrippedStoreInput); err == nil {
		t.Fatal("S3 source closure accepted receipt drift")
	}
	roundTrippedStoreInput.SourceArtifacts[6].LLMCallReceipt.ReturnedModel = "fixture-model-manifest"
	roundTrippedStoreInput.TestCases[0].InputArtifact.LLMCallReceipt.ReturnedModel = "tampered-input-model"
	if err := activities.validateTestManifestStoreInput(ctx, roundTrippedStoreInput); err == nil {
		t.Fatal("S3 TestManifest binding accepted receipt drift")
	}
	for _, level := range []string{generationapi.EvidenceStandard, generationapi.EvidenceAudit} {
		extended := materializerInput
		extended.EvidenceLevel = level
		extended.Params.MetadataExtras = map[string]interface{}{generationapi.QualityEvidenceLevelMetadataKey: level}
		extendedStore, extendedErr := activities.buildS3QualityDraftStoreInputV1(ctx, extended)
		if extendedErr != nil {
			t.Fatalf("build %s S3 quality draft: %v", level, extendedErr)
		}
		if extendedStore.QualityPassDraft == nil || extendedStore.QualityPassDraft.EvidenceLevel != level ||
			extendedStore.Params.MetadataExtras[generationapi.QualityEvidenceLevelMetadataKey] != level {
			t.Fatalf("materialized %s evidence binding = %+v", level, extendedStore)
		}
	}
	mismatched := materializerInput
	mismatched.EvidenceLevel = generationapi.EvidenceAudit
	if _, err := activities.buildS3QualityDraftStoreInputV1(ctx, mismatched); err == nil || !strings.Contains(err.Error(), "evidence level") {
		t.Fatalf("mismatched materializer profile was accepted: %v", err)
	}

	historicalNullAudit := bytes.Replace(auditBytes, []byte(`"blocking_issues":[]`), []byte(`"blocking_issues":null`), 1)
	if bytes.Equal(historicalNullAudit, auditBytes) {
		t.Fatal("canonical PASS audit did not expose an empty blocking_issues array")
	}
	materializerInput.AuditArtifact = put(historicalNullAudit, "application/json", "RecomputeS3VerdictActivityV1")
	if _, err := activities.buildS3QualityDraftStoreInputV1(ctx, materializerInput); err == nil || !strings.Contains(err.Error(), "null is forbidden") {
		t.Fatalf("historical null audit was not rejected: %v", err)
	}
}
