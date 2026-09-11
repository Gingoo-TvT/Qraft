package activities

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/generationapi"
)

func TestStoreInputHashExcludesIdempotencyKey(t *testing.T) {
	first := StoreInput{
		PayloadVersion: ActivityPayloadVersion,
		IdempotencyKey: "workflow-a/store-problem/v1",
		Statement:      StatementResult{Title: "Stable", Statement: "Statement"},
	}
	second := first
	second.IdempotencyKey = "workflow-b/store-problem/v1"

	firstHash, err := storeInputHash(first)
	if err != nil {
		t.Fatalf("hash first input: %v", err)
	}
	secondHash, err := storeInputHash(second)
	if err != nil {
		t.Fatalf("hash second input: %v", err)
	}
	if firstHash != secondHash {
		t.Fatalf("idempotency key changed payload hash: %s != %s", firstHash, secondHash)
	}

	second.Statement.Title = "Changed"
	changedHash, err := storeInputHash(second)
	if err != nil {
		t.Fatalf("hash changed input: %v", err)
	}
	if firstHash == changedHash {
		t.Fatal("payload change did not change hash")
	}
}

func TestVerifyStoredOperation(t *testing.T) {
	metadata, err := json.Marshal(map[string]interface{}{
		"store_idempotency_key": "workflow/store-problem/v1",
		"store_input_sha256":    "abc123",
	})
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}

	if err := verifyStoredOperation(metadata, "workflow/store-problem/v1", "abc123"); err != nil {
		t.Fatalf("matching operation rejected: %v", err)
	}
	if err := verifyStoredOperation(metadata, "workflow/store-problem/v1", "different"); err == nil {
		t.Fatal("expected different payload hash to be rejected")
	}
	if err := verifyStoredOperation(metadata, "different-key", "abc123"); err == nil {
		t.Fatal("expected different operation key to be rejected")
	}
}

func TestValidateReviewQuarantineEvidencePreservesFullReviewAndAncestry(t *testing.T) {
	ref := &ArtifactRef{
		ModelRevision: "review-model-r1",
		LLMCallReceipt: &LLMCallReceipt{
			SchemaVersion:  1,
			RequestedModel: "review-model",
			ReturnedModel:  "review-model-r1",
			Provider:       "fixture-provider",
			EndpointID:     strings.Repeat("a", 64),
			PromptHash:     strings.Repeat("b", 64),
			RequestSHA256:  strings.Repeat("c", 64),
		},
	}
	review := ReviewResult{
		SourceArtifacts:     []*ArtifactRef{ref},
		Approved:            false,
		Issues:              []string{"ambiguous invariant"},
		Suggestions:         []string{"state the invariant"},
		Confidence:          0.88,
		EstimatedDifficulty: 1700,
		FullText:            `{"approved":false,"review_details":{"correctness":{"score":4,"notes":"invariant missing"}}}`,
	}
	_, digest, err := CanonicalReviewResultJSON(review)
	if err != nil {
		t.Fatal(err)
	}
	input := StoreInput{
		PayloadVersion:  StoreProblemPayloadVersion,
		WorkflowID:      "problem-generation-test",
		SourceArtifacts: []*ArtifactRef{ref},
		ReviewQuarantine: &ReviewQuarantineEvidence{
			Reason:             "automated review rejected candidate",
			WorkflowRunID:      "run-test",
			ReviewGateChangeID: "problem-generation-review-gate-v2",
			ReviewGateVersion:  2,
			ReviewResultSHA256: digest,
			ReviewResult:       review,
			SourceAncestry:     []*ArtifactRef{ref},
		},
	}
	encodedInput, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	var roundTrippedInput StoreInput
	if err := json.Unmarshal(encodedInput, &roundTrippedInput); err != nil {
		t.Fatal(err)
	}
	input = roundTrippedInput
	if input.SourceArtifacts[0].LLMCallReceipt == input.ReviewQuarantine.SourceAncestry[0].LLMCallReceipt ||
		input.SourceArtifacts[0].LLMCallReceipt == input.ReviewQuarantine.ReviewResult.SourceArtifacts[0].LLMCallReceipt {
		t.Fatal("JSON round trip unexpectedly preserved receipt pointer aliases")
	}
	reviewJSON, ancestryJSON, err := validateReviewQuarantineEvidence(input)
	if err != nil {
		t.Fatalf("valid review quarantine evidence rejected: %v", err)
	}
	for _, field := range [][]byte{[]byte(`"approved"`), []byte(`"issues"`), []byte(`"suggestions"`), []byte(`"confidence"`), []byte(`"estimated_difficulty"`), []byte(`"is_duplicate"`), []byte(`"duplicate_of"`), []byte(`"duplicate_reason"`), []byte(`"full_text"`)} {
		if !bytes.Contains(reviewJSON, field) {
			t.Fatalf("canonical review JSON omitted %s: %s", field, reviewJSON)
		}
	}
	var ancestry []ArtifactRef
	if err := json.Unmarshal(ancestryJSON, &ancestry); err != nil {
		t.Fatal(err)
	}
	if len(ancestry) != 1 || ancestry[0].ModelRevision != "review-model-r1" {
		t.Fatalf("source ancestry = %+v", ancestry)
	}
	input.PayloadVersion = StoreProblemTestManifestPayloadVersion
	if _, _, err := validateReviewQuarantineEvidence(input); err != nil {
		t.Fatalf("v3 Store payload rejected valid review quarantine evidence: %v", err)
	}
	input.PayloadVersion = StoreProblemKnowledgePointCombinationPayloadVersion
	if _, _, err := validateReviewQuarantineEvidence(input); err != nil {
		t.Fatalf("v4 Store payload rejected valid review quarantine evidence: %v", err)
	}

	input.SourceArtifacts[0].LLMCallReceipt.ReturnedModel = "tampered-model"
	if _, _, err := validateReviewQuarantineEvidence(input); err == nil {
		t.Fatal("review quarantine accepted receipt drift in Store source artifacts")
	}
	input.SourceArtifacts[0].LLMCallReceipt.ReturnedModel = "review-model-r1"

	input.ReviewQuarantine.ReviewResult.FullText = ""
	if _, _, err := validateReviewQuarantineEvidence(input); err == nil {
		t.Fatal("missing full review text was accepted")
	}
}

func TestReviewQuarantineUsesFailClosedStorePayloadVersion(t *testing.T) {
	if StoreProblemPayloadVersion <= ActivityPayloadVersion {
		t.Fatalf("Store payload version %d must exceed legacy version %d", StoreProblemPayloadVersion, ActivityPayloadVersion)
	}
	if err := validateActivityPayloadVersion(StoreProblemPayloadVersion); err == nil {
		t.Fatal("legacy activity validator accepted the v2 Store payload")
	}
	input := StoreInput{
		PayloadVersion: ActivityPayloadVersion,
		ReviewQuarantine: &ReviewQuarantineEvidence{
			Reason: "automated review denied candidate",
		},
	}
	if _, _, err := validateReviewQuarantineEvidence(input); err == nil {
		t.Fatal("legacy Store payload accepted review quarantine evidence")
	}
}

func TestStoreTestManifestRequiresV3OrV4AndHashesExactJSON(t *testing.T) {
	manifest := &TestManifestV1{
		SchemaVersion:            TestManifestSchemaVersion,
		ComparisonMode:           TestManifestComparisonMode,
		TestCount:                1,
		DifferentialCheckedCount: 1,
		MainSolutionSHA256:       manifestSHA256([]byte("main-solution")),
		BruteSolutionSHA256:      manifestSHA256([]byte("brute-solution")),
		MainSandbox: TestManifestSandboxIdentity{
			ManifestDigest:          "sha256:" + manifestSHA256([]byte("main-request")),
			ImageDigest:             "sha256:" + manifestSHA256([]byte("main-image")),
			ToolchainManifestDigest: "sha256:" + manifestSHA256([]byte("main-toolchain")),
			SeccompPolicyDigest:     "sha256:" + manifestSHA256([]byte("main-seccomp")),
			LimitProfile:            "main-limits",
		},
		Cases: []TestManifestCase{{
			TestIndex:            0,
			Purpose:              "identity case",
			Origin:               TestCaseOriginLLMInline,
			InputSHA256:          manifestSHA256([]byte("1\n")),
			ExpectedOutputSHA256: manifestSHA256([]byte("1")),
			DifferentialChecked:  true,
			DifferentialMatch:    true,
			BruteOutputSHA256:    manifestSHA256([]byte("1")),
		}},
	}
	manifest.BruteSandbox = manifest.MainSandbox
	input := StoreInput{
		PayloadVersion: StoreProblemTestManifestPayloadVersion,
		TestCases:      []TestCaseData{{Input: "1\n"}},
		TestManifest:   manifest,
	}
	encoded, digest, err := prepareTestManifestForStore(input)
	if err != nil {
		t.Fatalf("prepare v3 test manifest: %v", err)
	}
	wantJSON, wantDigest, err := CanonicalTestManifestJSON(*manifest)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != string(wantJSON) || digest != wantDigest || digest != manifestSHA256(encoded) {
		t.Fatalf("prepared manifest mismatch: digest=%s want=%s json=%s", digest, wantDigest, encoded)
	}
	if err := validateStoreProblemPayloadVersion(StoreProblemTestManifestPayloadVersion); err != nil || !isVersionedStorePayload(StoreProblemTestManifestPayloadVersion) {
		t.Fatalf("v3 Store payload was not accepted as versioned: %v", err)
	}
	input.PayloadVersion = StoreProblemKnowledgePointCombinationPayloadVersion
	input.Params = versionedKnowledgePointParams(domain.KnowledgePointCombinationSingle, []string{"dp"})
	encodedV4, digestV4, err := prepareTestManifestForStore(input)
	if err != nil || string(encodedV4) != string(wantJSON) || digestV4 != wantDigest {
		t.Fatalf("v4 Store manifest = digest %s err %v json %s", digestV4, err, encodedV4)
	}
	input.PayloadVersion = StoreProblemStandardEvidencePayloadVersion
	input.Params.GenerationEvidence = standardEvidenceStoreContract()
	encodedV5, digestV5, err := prepareTestManifestForStore(input)
	if err != nil || string(encodedV5) != string(wantJSON) || digestV5 != wantDigest {
		t.Fatalf("v5 Store manifest = digest %s err %v json %s", digestV5, err, encodedV5)
	}

	input.PayloadVersion = StoreProblemPayloadVersion
	if _, _, err := prepareTestManifestForStore(input); err == nil {
		t.Fatal("v2 Store payload silently accepted a test manifest")
	}
	input.PayloadVersion = StoreProblemTestManifestPayloadVersion
	input.TestManifest = nil
	if _, _, err := prepareTestManifestForStore(input); err == nil {
		t.Fatal("v3 Store payload accepted a missing test manifest")
	}
}

func TestStoreKnowledgePointCombinationRequiresV4AndBindsMetadata(t *testing.T) {
	params := versionedKnowledgePointParams(
		domain.KnowledgePointCombinationSet,
		[]string{"dp", "prefix-sum"},
	)
	input := StoreInput{
		PayloadVersion: StoreProblemKnowledgePointCombinationPayloadVersion,
		Params:         params,
		Statement:      StatementResult{Tags: []string{"PREFIX-SUM", "dp"}},
	}
	if err := validateKnowledgePointStorePayload(input); err != nil {
		t.Fatalf("v4 combination payload rejected: %v", err)
	}
	canonical, err := conformStatementKnowledgePoints(input.Params, input.Statement.Tags)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(canonical, ",") != "dp,prefix-sum" {
		t.Fatalf("canonical persisted tags = %v", canonical)
	}
	metadata := knowledgePointConformanceMetadata(input.Params, canonical)
	if metadata["schema_version"] != domain.KnowledgePointConformanceSchemaV1 ||
		metadata["combination_schema"] != domain.KnowledgePointCombinationSchemaV1 ||
		metadata["mode"] != domain.KnowledgePointCombinationSet ||
		metadata["decision"] != "pass" ||
		strings.Join(metadata["persisted_statement_tags"].([]string), ",") != "dp,prefix-sum" {
		t.Fatalf("knowledge-point conformance metadata = %#v", metadata)
	}

	legacyVersion := input
	legacyVersion.PayloadVersion = StoreProblemTestManifestPayloadVersion
	if err := validateKnowledgePointStorePayload(legacyVersion); err == nil {
		t.Fatal("v3 Store payload silently accepted a knowledge-point contract")
	}
	missingContract := input
	missingContract.Params.KnowledgePointCombination = nil
	if err := validateKnowledgePointStorePayload(missingContract); err == nil {
		t.Fatal("v4 Store payload accepted a missing knowledge-point contract")
	}
}

func TestStoreStandardEvidenceRequiresV5AndAllPriorContracts(t *testing.T) {
	params := versionedKnowledgePointParams(domain.KnowledgePointCombinationSingle, []string{"dp"})
	params.GenerationEvidence = standardEvidenceStoreContract()
	input := StoreInput{
		PayloadVersion: StoreProblemStandardEvidencePayloadVersion,
		Params:         params,
	}
	if err := validateStoreProblemPayloadVersion(input.PayloadVersion); err != nil || !isVersionedStorePayload(input.PayloadVersion) {
		t.Fatalf("v5 Store payload was not accepted as versioned: %v", err)
	}
	if err := validateKnowledgePointStorePayload(input); err != nil {
		t.Fatalf("v5 knowledge-point contract rejected: %v", err)
	}
	if err := validateGenerationEvidenceStorePayload(input); err != nil {
		t.Fatalf("v5 standard evidence contract rejected: %v", err)
	}

	legacyVersion := input
	legacyVersion.PayloadVersion = StoreProblemKnowledgePointCombinationPayloadVersion
	if err := validateGenerationEvidenceStorePayload(legacyVersion); err == nil {
		t.Fatal("v4 Store payload silently accepted standard evidence")
	}
	missingEvidence := input
	missingEvidence.Params.GenerationEvidence = nil
	if err := validateGenerationEvidenceStorePayload(missingEvidence); err == nil {
		t.Fatal("v5 Store payload accepted missing standard evidence contract")
	}
	missingKnowledge := input
	missingKnowledge.Params.KnowledgePointCombination = nil
	if err := validateKnowledgePointStorePayload(missingKnowledge); err == nil {
		t.Fatal("v5 Store payload accepted missing knowledge-point contract")
	}
	drifted := input
	drifted.Params.GenerationEvidence = standardEvidenceStoreContract()
	drifted.Params.GenerationEvidence.ReviewerProfile = generationapi.ReviewerProfileV0
	if err := validateGenerationEvidenceStorePayload(drifted); err == nil {
		t.Fatal("v5 Store payload accepted drifted standard evidence identities")
	}
}

func standardEvidenceStoreContract() *domain.GenerationEvidenceContract {
	return &domain.GenerationEvidenceContract{
		SchemaVersion:                   domain.GenerationEvidenceContractSchemaV1,
		EvidenceLevel:                   domain.GenerationStandardEvidenceLevel,
		JobContractVersion:              generationapi.JobContractVersion,
		GenerationArm:                   generationapi.GenerationArmBaseline,
		GenerationArmMode:               generationapi.GenerationArmModeBaseline,
		GenerationBehavior:              "unchanged",
		ReviewerProfile:                 generationapi.ReviewerProfileV1,
		ReviewerProfileDescriptorSHA256: generationapi.ReviewerProfileV1DescriptorSHA256,
		EvidenceProfile:                 generationapi.EvidenceProfileV0,
		EvidenceProfileDescriptorSHA256: generationapi.EvidenceProfileDescriptorSHA256,
		OutcomeTaxonomy:                 generationapi.OutcomeTaxonomyVersion,
		IncludeEditorial:                true,
		IncludeSolutions:                true,
		IncludeTestData:                 true,
	}
}

func TestGenerationStandardEvidenceRecoveryRebuildsBoundReceipt(t *testing.T) {
	contract := standardEvidenceStoreContract()
	testManifestSHA := strings.Repeat("a", 64)
	outcomeSHA := strings.Repeat("b", 64)
	body, digest, err := generationapi.CanonicalGenerationStandardEvidenceV1(
		contract,
		domain.ProblemStatusQuarantined,
		generationapi.OutcomeCategoryReview,
		[]generationapi.EvidenceRef{
			{Kind: "test_manifest", SHA256: testManifestSHA},
			{Kind: "review_result", SHA256: outcomeSHA},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	binding := domain.GenerationStandardEvidenceBinding{
		Reference: domain.GenerationStandardEvidenceReference{
			SchemaVersion: domain.GenerationStandardEvidenceSchemaV1,
			SHA256:        digest,
			Path:          "problems/00000000-0000-0000-0000-000000000001/generation_standard_evidence.v1.json",
		},
		FinalStatus:      domain.ProblemStatusQuarantined,
		QuarantineReason: "quality review denied",
		OutcomeCategory:  string(generationapi.OutcomeCategoryReview),
		OutcomeKind:      "review_result",
		OutcomeSHA256:    outcomeSHA,
	}
	rebuilt, rebuiltSHA, err := generationStandardEvidenceBodyFromBinding(contract, binding, testManifestSHA)
	if err != nil || rebuiltSHA != digest || string(rebuilt) != string(body) {
		t.Fatalf("recovered receipt = sha %s err %v body %s", rebuiltSHA, err, rebuilt)
	}
	drifted := binding
	drifted.OutcomeSHA256 = strings.Repeat("c", 64)
	if _, _, err := generationStandardEvidenceBodyFromBinding(contract, drifted, testManifestSHA); err == nil {
		t.Fatal("recovery accepted outcome evidence drift")
	}
}

func TestValidateTestManifestStoreInputBindsPersistedBytesAndMetadata(t *testing.T) {
	audit := SandboxAuditMetadata{
		RunID:                   "store-generator-run",
		ManifestDigest:          "sha256:" + manifestSHA256([]byte("request")),
		ImageDigest:             "sha256:" + manifestSHA256([]byte("image")),
		ToolchainManifestDigest: "sha256:" + manifestSHA256([]byte("toolchain")),
		SeccompPolicyDigest:     "sha256:" + manifestSHA256([]byte("seccomp")),
		LimitProfile:            "store-binding-limits",
		Seed:                    71,
	}
	generatorCaseIndex := 0
	generatorBatchIndex := 0
	testCases := []TestCaseData{{
		Input:               "1 2\n",
		GroupID:             1,
		IsSample:            true,
		Description:         "addition sample",
		Origin:              TestCaseOriginGenerator,
		GeneratorSeed:       42,
		GeneratorCaseIndex:  &generatorCaseIndex,
		GeneratorBatchIndex: &generatorBatchIndex,
	}}
	mainSolution := domain.Solution{Language: "cpp", SourceCode: "main"}
	bruteSolution := domain.Solution{Language: "cpp", SourceCode: "brute"}
	mainOutput := SandboxResult{PayloadVersion: ActivityPayloadVersion, Outputs: []string{"3"}, Audit: audit}
	manifest, err := New(&Dependencies{}).BuildTestManifestActivity(context.Background(), BuildTestManifestInput{
		PayloadVersion:  ActivityPayloadVersion,
		TestCases:       testCases,
		MainOutput:      mainOutput,
		BruteOutput:     SandboxResult{PayloadVersion: ActivityPayloadVersion, Outputs: []string{"3"}, Audit: audit},
		BruteIndices:    []int{0},
		MainSolution:    mainSolution,
		BruteSolution:   bruteSolution,
		GeneratorSHA256: manifestSHA256([]byte("store-generator")),
		GeneratorBatches: []GeneratorBatchAudit{{
			BatchIndex:           0,
			TestIndexes:          []int{0},
			GeneratorCaseIndexes: []int{0},
			Audit:                audit,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	input := StoreInput{
		PayloadVersion: StoreProblemTestManifestPayloadVersion,
		Solutions:      SolutionResult{MainSolution: mainSolution, BruteSolution: bruteSolution},
		TestCases:      testCases,
		SandboxOutput:  mainOutput,
		TestManifest:   manifest,
	}
	activities := New(&Dependencies{})
	if err := activities.validateTestManifestStoreInput(context.Background(), input); err != nil {
		t.Fatalf("validate matching Store input: %v", err)
	}

	changedMainSolution := input
	changedMainSolution.Solutions.MainSolution.SourceCode = "different main"
	if err := activities.validateTestManifestStoreInput(context.Background(), changedMainSolution); err == nil {
		t.Fatal("manifest accepted a different Store main solution")
	}

	changedBruteSolution := input
	changedBruteSolution.Solutions.BruteSolution.SourceCode = "different brute"
	if err := activities.validateTestManifestStoreInput(context.Background(), changedBruteSolution); err == nil {
		t.Fatal("manifest accepted a different Store brute solution")
	}

	changedMainSandbox := input
	changedMainSandbox.SandboxOutput.Audit.ImageDigest = "sha256:" + manifestSHA256([]byte("different image"))
	if err := activities.validateTestManifestStoreInput(context.Background(), changedMainSandbox); err == nil {
		t.Fatal("manifest accepted a different Store main sandbox identity")
	}

	changedInput := input
	changedInput.TestCases = append([]TestCaseData(nil), input.TestCases...)
	changedInput.TestCases[0].Input = "2 2\n"
	if err := activities.validateTestManifestStoreInput(context.Background(), changedInput); err == nil {
		t.Fatal("manifest accepted different Store input bytes")
	}

	changedOutput := input
	changedOutput.SandboxOutput.Outputs = []string{"4"}
	if err := activities.validateTestManifestStoreInput(context.Background(), changedOutput); err == nil {
		t.Fatal("manifest accepted different Store output bytes")
	}

	changedMetadata := input
	changedMetadata.TestCases = append([]TestCaseData(nil), input.TestCases...)
	changedMetadata.TestCases[0].IsSample = false
	if err := activities.validateTestManifestStoreInput(context.Background(), changedMetadata); err == nil {
		t.Fatal("manifest accepted different Store test metadata")
	}

	changedGeneratorCaseIndex := input
	changedGeneratorCaseIndex.TestCases = append([]TestCaseData(nil), input.TestCases...)
	differentCaseIndex := 1
	changedGeneratorCaseIndex.TestCases[0].GeneratorCaseIndex = &differentCaseIndex
	if err := activities.validateTestManifestStoreInput(context.Background(), changedGeneratorCaseIndex); err == nil {
		t.Fatal("manifest accepted a different generator original case index")
	}

	changedGeneratorBatchIndex := input
	changedGeneratorBatchIndex.TestCases = append([]TestCaseData(nil), input.TestCases...)
	differentBatchIndex := 1
	changedGeneratorBatchIndex.TestCases[0].GeneratorBatchIndex = &differentBatchIndex
	if err := activities.validateTestManifestStoreInput(context.Background(), changedGeneratorBatchIndex); err == nil {
		t.Fatal("manifest accepted a different generator batch index")
	}
}

func TestReviewDeniedCandidateStartsQuarantined(t *testing.T) {
	if got := initialStoreProblemStatus(StoreInput{ReviewQuarantine: &ReviewQuarantineEvidence{}}); got != domain.ProblemStatusQuarantined {
		t.Fatalf("review-denied initial status = %q", got)
	}
	if got := initialStoreProblemStatus(StoreInput{}); got != domain.ProblemStatusGenerating {
		t.Fatalf("normal initial status = %q", got)
	}
}

func TestAutoApprovalCandidateOnlyAcceptsProvenanceOnlyQuarantine(t *testing.T) {
	if !autoApprovalCandidate(
		StoreInput{}, domain.ProblemStatusQuarantined, autoApprovalQuarantineReason,
	) {
		t.Fatal("eligible provenance-only quarantine was not selected")
	}
	if autoApprovalCandidate(
		StoreInput{ReviewQuarantine: &ReviewQuarantineEvidence{}},
		domain.ProblemStatusQuarantined,
		autoApprovalQuarantineReason,
	) {
		t.Fatal("automated-review quarantine was selected for auto approval")
	}
	if autoApprovalCandidate(
		StoreInput{},
		domain.ProblemStatusQuarantined,
		autoApprovalQuarantineReason+"; missing runnable test artifacts",
	) {
		t.Fatal("quarantine with another blocking reason was selected")
	}
	if autoApprovalCandidate(
		StoreInput{}, domain.ProblemStatusPublished, autoApprovalQuarantineReason,
	) {
		t.Fatal("published problem was selected for auto approval")
	}
}
