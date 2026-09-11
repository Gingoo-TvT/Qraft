package activities

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	speccontract "github.com/Gingoo-TvT/Qraft/backend/internal/spec"
	"go.temporal.io/sdk/testsuite"
)

type sampleClosureFixture struct {
	validatedFixture *validatedSampleCandidatesFixture
	activities       *Activities
	store            *statementDraftCASStore
	receipt          VerifiedProgramReceiptV1
	validated        ValidatedSampleCandidatesBundleV1
	stageInput       StageAuthoringStatementSamplesInput
	outputs          []string
}

func newSampleClosureFixture(t *testing.T, samples []string) *sampleClosureFixture {
	t.Helper()
	validatedFixture := newValidatedSampleCandidatesFixture(t, samples)
	store := validatedFixture.store
	activities := &Activities{deps: &Dependencies{ProvenanceRecorder: &captureProvenanceRecorder{}}, artifacts: store}
	validatedResult, err := executeValidatedSampleCandidatesActivity(t, activities, validatedFixture.input)
	if err != nil {
		t.Fatalf("validate sample candidates fixture: %v", err)
	}
	validatedBytes, err := store.Get(context.Background(), *validatedResult.ValidatedBundleArtifact)
	if err != nil {
		t.Fatal(err)
	}
	var validated ValidatedSampleCandidatesBundleV1
	if err := decodeCanonicalSampleClosureJSONV1(validatedBytes, &validated); err != nil {
		t.Fatal(err)
	}

	draftRef := seedSampleClosureStatementDraft(t, validatedFixture)
	receipt := VerifiedProgramReceiptV1{
		SchemaVersion:                  VerifiedProgramReceiptSchemaV1,
		Status:                         VerifiedProgramReceiptStatusV1,
		ProgramSHA256:                  strings.Repeat("c", 64),
		IndependentOracleReceiptSHA256: strings.Repeat("d", 64),
		SandboxImageDigest:             "sha256:" + strings.Repeat("1", 64),
		ToolchainManifestDigest:        "sha256:" + strings.Repeat("2", 64),
		SeccompPolicyDigest:            "sha256:" + strings.Repeat("3", 64),
		LimitProfile:                   "sample-closure-v1",
	}
	receiptBytes, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	receiptRef := seedSampleClosureJSONArtifact(t, store, receiptBytes, VerifiedProgramReceiptProducerV1)
	inputSHA := make([]string, len(validated.Candidates))
	outputs := make([]string, len(validated.Candidates))
	for index := range validated.Candidates {
		inputSHA[index] = validated.Candidates[index].CanonicalInputSHA256
		outputs[index] = fmt.Sprintf("verified-output-%d", index+1)
	}
	firstRun := sampleClosureSandboxRun(AuthoringSampleSandboxRunPhaseFirst, receiptRef.SHA256, receipt, inputSHA, outputs)
	return &sampleClosureFixture{
		validatedFixture: validatedFixture, activities: activities, store: store,
		receipt: receipt, validated: validated, outputs: outputs,
		stageInput: StageAuthoringStatementSamplesInput{
			PayloadVersion:                StageAuthoringStatementSamplesPayloadVersion,
			AuthoringBundleArtifact:       validatedFixture.input.BundleArtifact,
			ExpectedAuthoringBundleSHA256: validatedFixture.input.ExpectedBundleSHA256,
			StatementDraftArtifact:        draftRef, ExpectedStatementDraftSHA256: draftRef.SHA256,
			ValidatedSamplesArtifact:             *validatedResult.ValidatedBundleArtifact,
			ExpectedValidatedSamplesSHA256:       validatedResult.ValidatedBundleSHA256,
			VerifiedProgramReceiptArtifact:       receiptRef,
			ExpectedVerifiedProgramReceiptSHA256: receiptRef.SHA256,
			FirstRun:                             firstRun,
		},
	}
}

func seedSampleClosureStatementDraft(t *testing.T, fixture *validatedSampleCandidatesFixture) ArtifactRef {
	t.Helper()
	facts := statementFactManifestFromSemanticSpecV1(*fixture.bundle.SemanticSpec)
	factSHA, err := canonicalJSONSHA256(facts)
	if err != nil {
		t.Fatal(err)
	}
	model := statementNarrativeModelOutputV1{Title: "Fixture", Narrative: "Friends gather beneath lanterns."}
	markdown, err := renderStatementDraftMarkdownV1("en", model, facts)
	if err != nil {
		t.Fatal(err)
	}
	modelSHA, _ := canonicalJSONSHA256(model)
	draft := StatementDraftBundleV1{
		SchemaVersion: StatementDraftBundleSchemaV1, DocumentStatus: statementDraftDocumentStatusV1,
		RendererInputSHA256:   strings.Repeat("4", 64),
		AuthoringBundleSHA256: fixture.input.ExpectedBundleSHA256,
		AuthoringInputSHA256:  fixture.bundle.InputSHA256, BriefSHA256: fixture.bundle.BriefSHA256,
		SemanticSpecSHA256: fixture.input.ExpectedSemanticSpecSHA256,
		FactManifestSHA256: factSHA, ModelOutputSHA256: modelSHA,
		RendererSourceArtifactSHA256: strings.Repeat("5", 64),
		RendererSourceRequestSHA256:  strings.Repeat("6", 64),
		DerivationRule:               statementDraftDerivationRuleV1, PresentationLocale: "en",
		Title: model.Title, Narrative: model.Narrative, FactManifest: facts,
		Markdown: markdown, MarkdownSHA256: sha256Hex([]byte(markdown)),
	}
	encoded, err := json.Marshal(draft)
	if err != nil {
		t.Fatal(err)
	}
	return seedSampleClosureJSONArtifact(t, fixture.store, encoded, "RenderStatementFromAuthoringBundleActivityV1")
}

func seedSampleClosureJSONArtifact(t *testing.T, store *statementDraftCASStore, data []byte, producer string) ArtifactRef {
	t.Helper()
	ref, err := store.Put(context.Background(), data, "application/json", ArtifactMetadata{
		Producer: producer, Provider: "algoforge", Model: "not_applicable", ModelRevision: "not_applicable",
		WorkflowID: "sample-closure-fixture", PayloadVersion: ActivityPayloadVersion,
	})
	if err != nil {
		t.Fatalf("seed %s: %v", producer, err)
	}
	return ref
}

func sampleClosureSandboxRun(phase, receiptSHA string, receipt VerifiedProgramReceiptV1, inputSHA, outputs []string) AuthoringSampleSandboxRunV1 {
	return AuthoringSampleSandboxRunV1{
		SchemaVersion: AuthoringSampleSandboxRunSchemaV1, Phase: phase,
		VerifiedProgramReceiptSHA256: receiptSHA, ProgramSHA256: receipt.ProgramSHA256,
		IndependentOracleReceiptSHA256: receipt.IndependentOracleReceiptSHA256,
		InputSHA256:                    append([]string(nil), inputSHA...),
		SandboxOutput: SandboxResult{
			PayloadVersion: ActivityPayloadVersion, Outputs: append([]string(nil), outputs...),
			TimeTaken: make([]time.Duration, len(outputs)), MemoryUsed: make([]int64, len(outputs)),
			Audit: SandboxAuditMetadata{
				RunID: phase + "-fixture-run", ManifestDigest: "sha256:" + strings.Repeat("7", 64), Seed: 7,
				LimitProfile: receipt.LimitProfile, ImageDigest: receipt.SandboxImageDigest,
				ToolchainManifestDigest: receipt.ToolchainManifestDigest, SeccompPolicyDigest: receipt.SeccompPolicyDigest,
			},
		},
	}
}

func executeStageAuthoringSamples(t *testing.T, activities *Activities, input StageAuthoringStatementSamplesInput) (*StageAuthoringStatementSamplesResult, error) {
	t.Helper()
	var suite testsuite.WorkflowTestSuite
	environment := suite.NewTestActivityEnvironment()
	environment.RegisterActivity(activities.StageAuthoringStatementSamplesActivityV1)
	encoded, err := environment.ExecuteActivity(activities.StageAuthoringStatementSamplesActivityV1, input)
	if err != nil {
		return nil, err
	}
	var result StageAuthoringStatementSamplesResult
	if err := encoded.Get(&result); err != nil {
		t.Fatal(err)
	}
	return &result, nil
}

func executeFinalizeAuthoringSamples(t *testing.T, activities *Activities, input FinalizeAuthoringStatementSamplesInput) (*FinalizeAuthoringStatementSamplesResult, error) {
	t.Helper()
	var suite testsuite.WorkflowTestSuite
	environment := suite.NewTestActivityEnvironment()
	environment.RegisterActivity(activities.FinalizeAuthoringStatementSamplesActivityV1)
	encoded, err := environment.ExecuteActivity(activities.FinalizeAuthoringStatementSamplesActivityV1, input)
	if err != nil {
		return nil, err
	}
	var result FinalizeAuthoringStatementSamplesResult
	if err := encoded.Get(&result); err != nil {
		t.Fatal(err)
	}
	return &result, nil
}

func (fixture *sampleClosureFixture) finalizeInput(t *testing.T, staged *StageAuthoringStatementSamplesResult, outputs []string) FinalizeAuthoringStatementSamplesInput {
	t.Helper()
	stagedBytes, err := fixture.store.Get(context.Background(), *staged.StagedBundleArtifact)
	if err != nil {
		t.Fatal(err)
	}
	var stagedBundle StagedAuthoringStatementSamplesBundleV1
	if err := decodeCanonicalSampleClosureJSONV1(stagedBytes, &stagedBundle); err != nil {
		t.Fatal(err)
	}
	parsed, err := speccontract.ParseStatementSamplesV1(stagedBundle.Markdown)
	if err != nil {
		t.Fatal(err)
	}
	inputSHA := make([]string, len(parsed))
	for index := range parsed {
		inputSHA[index] = sha256Hex([]byte(parsed[index].Input))
	}
	return FinalizeAuthoringStatementSamplesInput{
		PayloadVersion:       FinalizeAuthoringStatementSamplesPayloadVersion,
		StagedBundleArtifact: *staged.StagedBundleArtifact, ExpectedStagedBundleSHA256: staged.StagedBundleSHA256,
		SecondRun: sampleClosureSandboxRun(AuthoringSampleSandboxRunPhaseReparse, stagedBundle.VerifiedProgramReceiptSHA256, fixture.receipt, inputSHA, outputs),
	}
}

func TestAuthoringStatementSamplesV1ClosesZeroOneAndManyDeterministically(t *testing.T) {
	variants := []struct {
		name    string
		samples []string
	}{
		{name: "zero"},
		{name: "one", samples: []string{"1\n5\n"}},
		{name: "many", samples: []string{"1\n-7\n", "3\n1 2 3\n", "4\n4 3 2 1\n"}},
	}
	for _, variant := range variants {
		t.Run(variant.name, func(t *testing.T) {
			fixture := newSampleClosureFixture(t, variant.samples)
			staged, err := executeStageAuthoringSamples(t, fixture.activities, fixture.stageInput)
			if err != nil {
				t.Fatalf("stage: %v", err)
			}
			finalInput := fixture.finalizeInput(t, staged, fixture.outputs)
			finalResult, err := executeFinalizeAuthoringSamples(t, fixture.activities, finalInput)
			if err != nil {
				t.Fatalf("finalize: %v", err)
			}
			if finalResult.Status != FinalAuthoringStatementSamplesStatusV1 || finalResult.SampleCount != len(variant.samples) {
				t.Fatalf("unexpected final result: %+v", finalResult)
			}
			finalBytes, err := fixture.store.Get(context.Background(), *finalResult.FinalBundleArtifact)
			if err != nil {
				t.Fatal(err)
			}
			var finalBundle FinalAuthoringStatementSamplesBundleV1
			if err := decodeCanonicalSampleClosureJSONV1(finalBytes, &finalBundle); err != nil {
				t.Fatal(err)
			}
			parsed, err := speccontract.ParseStatementSamplesV1(finalBundle.Markdown)
			if err != nil || len(parsed) != len(variant.samples) {
				t.Fatalf("final parse-back failed: count=%d err=%v", len(parsed), err)
			}
			stagedAgain, err := executeStageAuthoringSamples(t, fixture.activities, fixture.stageInput)
			if err != nil || stagedAgain.StagedBundleSHA256 != staged.StagedBundleSHA256 {
				t.Fatalf("stage not deterministic: got=%v err=%v", stagedAgain, err)
			}
		})
	}
}

func TestStageAuthoringStatementSamplesV1RejectsReceiptInputOrderAndCrossBundleSplice(t *testing.T) {
	baseSamples := []string{"1\n-7\n", "3\n1 2 3\n"}
	t.Run("receipt tamper", func(t *testing.T) {
		fixture := newSampleClosureFixture(t, baseSamples)
		fixture.stageInput.FirstRun.ProgramSHA256 = strings.Repeat("e", 64)
		putsBefore := len(fixture.store.puts)
		if _, err := executeStageAuthoringSamples(t, fixture.activities, fixture.stageInput); err == nil {
			t.Fatal("receipt tamper was accepted")
		}
		if len(fixture.store.puts) != putsBefore {
			t.Fatal("stage wrote CAS after receipt tamper")
		}
	})
	t.Run("input order", func(t *testing.T) {
		fixture := newSampleClosureFixture(t, baseSamples)
		fixture.stageInput.FirstRun.InputSHA256[0], fixture.stageInput.FirstRun.InputSHA256[1] = fixture.stageInput.FirstRun.InputSHA256[1], fixture.stageInput.FirstRun.InputSHA256[0]
		if _, err := executeStageAuthoringSamples(t, fixture.activities, fixture.stageInput); err == nil {
			t.Fatal("input order splice was accepted")
		}
	})
	t.Run("cross authoring bundle", func(t *testing.T) {
		fixture := newSampleClosureFixture(t, baseSamples)
		spliced := fixture.validated
		spliced.AuthoringBundleSHA256 = strings.Repeat("f", 64)
		encoded, _ := json.Marshal(spliced)
		ref := seedSampleClosureJSONArtifact(t, fixture.store, encoded, "ValidateAuthoringSampleCandidatesActivityV1")
		fixture.stageInput.ValidatedSamplesArtifact = ref
		fixture.stageInput.ExpectedValidatedSamplesSHA256 = ref.SHA256
		if _, err := executeStageAuthoringSamples(t, fixture.activities, fixture.stageInput); err == nil {
			t.Fatal("cross-bundle validated samples were accepted")
		}
	})
}

func TestFinalizeAuthoringStatementSamplesV1RejectsManualEditReorderAndSecondRunMismatch(t *testing.T) {
	fixture := newSampleClosureFixture(t, []string{"1\n5\n", "1\n7\n"})
	staged, err := executeStageAuthoringSamples(t, fixture.activities, fixture.stageInput)
	if err != nil {
		t.Fatal(err)
	}
	t.Run("second run mismatch", func(t *testing.T) {
		outputs := append([]string(nil), fixture.outputs...)
		outputs[1] += "-different"
		input := fixture.finalizeInput(t, staged, outputs)
		putsBefore := len(fixture.store.puts)
		if _, err := executeFinalizeAuthoringSamples(t, fixture.activities, input); err == nil {
			t.Fatal("different second-run output was accepted")
		}
		if len(fixture.store.puts) != putsBefore {
			t.Fatal("final CAS was written after second-run mismatch")
		}
	})
	for _, variant := range []struct {
		name   string
		mutate func(string) string
	}{
		{name: "manual embedded output edit", mutate: func(value string) string {
			return strings.Replace(value, fixture.outputs[0], fixture.outputs[0]+"x", 1)
		}},
		{name: "reordered label", mutate: func(value string) string { return strings.Replace(value, "#### input1", "#### input2", 1) }},
	} {
		t.Run(variant.name, func(t *testing.T) {
			stagedBytes, _ := fixture.store.Get(context.Background(), *staged.StagedBundleArtifact)
			var bundle StagedAuthoringStatementSamplesBundleV1
			if err := decodeCanonicalSampleClosureJSONV1(stagedBytes, &bundle); err != nil {
				t.Fatal(err)
			}
			bundle.Markdown = variant.mutate(bundle.Markdown)
			bundle.MarkdownSHA256 = sha256Hex([]byte(bundle.Markdown))
			encoded, _ := json.Marshal(bundle)
			ref := seedSampleClosureJSONArtifact(t, fixture.store, encoded, "StageAuthoringStatementSamplesActivityV1")
			input := fixture.finalizeInput(t, staged, fixture.outputs)
			input.StagedBundleArtifact = ref
			input.ExpectedStagedBundleSHA256 = ref.SHA256
			putsBefore := len(fixture.store.puts)
			if _, err := executeFinalizeAuthoringSamples(t, fixture.activities, input); err == nil {
				t.Fatal("tampered staged Markdown was accepted")
			}
			if len(fixture.store.puts) != putsBefore {
				t.Fatal("final CAS was written after staged tamper")
			}
		})
	}
}

func TestStageAuthoringStatementSamplesV1RejectsUnsupportedGrammarWithZeroSamples(t *testing.T) {
	fixture := newSampleClosureFixture(t, nil)
	mutated := fixture.validatedFixture.bundle
	mutated.SemanticSpec = cloneSemanticSpecForSampleClosureTest(t, *mutated.SemanticSpec)
	for index := range mutated.SemanticSpec.Symbols {
		if mutated.SemanticSpec.Symbols[index].Scope == domain.SemanticSymbolScopeInput {
			mutated.SemanticSpec.Symbols[index].Type = domain.SemanticSymbolString
			break
		}
	}
	semanticSHA := speccontract.SemanticSpecSHA256V1(*mutated.SemanticSpec)
	mutated.AuthoringPlan.SemanticSpecSHA256 = semanticSHA
	mutated.LintReport.SemanticSpecSHA256 = semanticSHA
	encoded, _ := json.Marshal(mutated)
	ref := seedSampleClosureJSONArtifact(t, fixture.store, encoded, "GenerateAuthoringPlanActivity")
	fixture.stageInput.AuthoringBundleArtifact = ref
	fixture.stageInput.ExpectedAuthoringBundleSHA256 = ref.SHA256
	if _, err := executeStageAuthoringSamples(t, fixture.activities, fixture.stageInput); err == nil || !strings.Contains(err.Error(), "unsupported QG-03 input profile") {
		t.Fatalf("unsupported zero-sample grammar did not fail closed: %v", err)
	}
}

func cloneSemanticSpecForSampleClosureTest(t *testing.T, value domain.SemanticSpecV1) *domain.SemanticSpecV1 {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var cloned domain.SemanticSpecV1
	if err := json.Unmarshal(encoded, &cloned); err != nil {
		t.Fatal(err)
	}
	return &cloned
}
