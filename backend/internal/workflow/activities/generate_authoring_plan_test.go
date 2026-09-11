package activities

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/llm"
	"go.temporal.io/sdk/testsuite"
)

type capturingAuthoringLLM struct {
	response *llm.Response
	calls    int
	request  *llm.Request
}

func (l *capturingAuthoringLLM) CompleteWithRetry(_ context.Context, request *llm.Request, _ int) (*llm.Response, error) {
	l.calls++
	l.request = request
	return l.response, nil
}

type failOnPutArtifactStore struct {
	inner  captureArtifactStore
	puts   int
	failOn int
}

func TestGenerateAuthoringPlanActivityRejectsUnboundSampleCountsBeforeProviderEffect(t *testing.T) {
	for _, sampleCount := range []int{-1, domain.MaxAdaptiveTestCases + 1} {
		t.Run(fmt.Sprintf("samples_%d", sampleCount), func(t *testing.T) {
			provider := &capturingAuthoringLLM{}
			store := &captureArtifactStore{}
			provenance := &captureProvenanceRecorder{}
			activities := &Activities{
				deps: &Dependencies{
					LLM: provider, LLMProvider: "fixture-provider", LLMModel: "fixture-model",
					ProvenanceRecorder: provenance,
				},
				artifacts: store,
			}
			input := validAuthoringActivityInput()
			input.Params.TestDataConfig.NumSamples = sampleCount
			if _, err := executeAuthoringActivityRaw(activities, input); err == nil {
				t.Fatal("invalid sample count was accepted")
			}
			if provider.calls != 0 || store.data != nil || len(provenance.records) != 0 {
				t.Fatalf("invalid sample count caused side effects: calls=%d data=%d provenance=%d", provider.calls, len(store.data), len(provenance.records))
			}
		})
	}
}

func TestBuildAuthoringPlanPromptCarriesExactRequestedSampleCount(t *testing.T) {
	for _, sampleCount := range []int{0, 1, 3} {
		params := domain.DefaultProblemGenParams()
		params.TestDataConfig.NumTestCases = 20
		params.TestDataConfig.NumSamples = sampleCount
		prompt := buildAuthoringPlanPromptV1("Frozen brief.", params, nil)
		expected := fmt.Sprintf(`"requested_test_case_count":20,"requested_sample_count":%d`, sampleCount)
		if !strings.Contains(prompt, expected) {
			t.Fatalf("prompt lost requested counts for samples=%d: %s", sampleCount, prompt)
		}
	}
	if !strings.Contains(authoringPlanSystemPromptV1, "sample_input_candidates must contain exactly requested_sample_count") ||
		!strings.Contains(authoringPlanSystemPromptV1, "when requested_sample_count is 0, return an empty array") {
		t.Fatal("system prompt does not define exact zero/one/many sample-candidate cardinality")
	}
}

func TestBuildAuthoringPlanPromptCarriesAdaptiveCasePolicy(t *testing.T) {
	params := domain.DefaultProblemGenParams()
	params.TestDataConfig.AutoCaseCount = true
	params.TestDataConfig.NumTestCases = 20
	params.TestDataConfig.NumSamples = 3
	prompt := buildAuthoringPlanPromptV1("Frozen brief.", params, nil)
	for _, want := range []string{
		"Test-data count policy: adaptive count in [10,20]",
		"smallest sufficient suite",
		"custom cases included",
		"maximum-scale randomized/pressure intent",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("adaptive authoring prompt omitted %q: %s", want, prompt)
		}
	}
}

func (s *failOnPutArtifactStore) Put(ctx context.Context, data []byte, contentType string, metadata ArtifactMetadata) (ArtifactRef, error) {
	s.puts++
	if s.puts == s.failOn {
		return ArtifactRef{}, errors.New("injected authoring bundle CAS failure")
	}
	return s.inner.Put(ctx, data, contentType, metadata)
}

func (s *failOnPutArtifactStore) Get(ctx context.Context, ref ArtifactRef) ([]byte, error) {
	return s.inner.Get(ctx, ref)
}

func TestGenerateAuthoringPlanActivityStoresBoundLintedBundle(t *testing.T) {
	modelOutput := validAuthoringModelOutputV1()
	responseJSON, err := marshalAuthoringModelOutputV1(modelOutput)
	if err != nil {
		t.Fatal(err)
	}
	provider := &capturingAuthoringLLM{response: authoringLLMResponse(string(responseJSON[1:]))}
	store := &captureArtifactStore{}
	provenance := &captureProvenanceRecorder{}
	activities := &Activities{
		deps: &Dependencies{
			LLM: provider, LLMProvider: "fixture-provider", LLMModel: "fallback-model",
			ProvenanceRecorder: provenance,
		},
		artifacts: store,
	}
	input := validAuthoringActivityInput()
	input.Params.KnowledgePointCombination = &domain.KnowledgePointCombinationContract{
		SchemaVersion: domain.KnowledgePointCombinationSchemaV1,
		Mode:          domain.KnowledgePointCombinationSingle,
		MaxConcepts:   1,
	}
	input.Params.ProviderConfig = &domain.ProviderRuntimeConfig{Statement: &domain.LLMRuntimeConfig{
		Model: "g-formalizer", APIKeyRef: "env:ALGOFORGE_TEST_G_KEY",
		BaseURL: "https://example.invalid/v1", Provider: "openai-compatible", Protocol: "openai-chat",
	}}

	result := executeAuthoringActivity(t, activities, input)
	if result.ModelDecision != AuthoringPlanDecisionAccepted || result.Decision != AuthoringPlanDecisionAccepted || result.GateReasonCode != "" || !result.LintPassed || result.LintErrorCount != 0 {
		t.Fatalf("unexpected authoring result: %+v", result)
	}
	if provider.calls != 1 || provider.request == nil || provider.request.Model != "g-formalizer" || provider.request.Runtime == nil || provider.request.Runtime.APIKeyRef != "env:ALGOFORGE_TEST_G_KEY" {
		t.Fatalf("G route was not applied: calls=%d request=%+v", provider.calls, provider.request)
	}
	if !strings.Contains(provider.request.Messages[0].Content, `"knowledge_point_combination":{"schema_version":"algoforge.knowledge-point-combination.v1","mode":"single","max_concepts":1}`) {
		t.Fatalf("knowledge-point combination semantics were omitted from prompt: %s", provider.request.Messages[0].Content)
	}
	if result.InputSHA256 == "" || result.BundleArtifact == nil || result.BundleSHA256 != result.BundleArtifact.SHA256 || len(result.SourceArtifacts) != 2 || result.SourceArtifacts[0] == nil || result.SourceArtifacts[1] == nil || result.SourceArtifacts[1].SHA256 != result.BundleArtifact.SHA256 {
		t.Fatalf("missing source/bundle CAS bindings: %+v", result)
	}
	if len(provenance.records) != 2 || provenance.records[1].ArtifactType != "qg02a_authoring_bundle" {
		t.Fatalf("unexpected provenance records: %+v", provenance.records)
	}
	var bundle AuthoringPlanBundleV1
	if err := json.Unmarshal(store.data, &bundle); err != nil {
		t.Fatalf("decode stored bundle: %v", err)
	}
	if bundle.ModelDecision != AuthoringPlanDecisionAccepted || bundle.Decision != AuthoringPlanDecisionAccepted || bundle.SemanticSpec == nil || bundle.AuthoringPlan == nil || bundle.LintReport == nil || !bundle.LintReport.Passed {
		t.Fatalf("stored bundle is incomplete: %+v", bundle)
	}
	if bundle.RequestedTestCaseCount != input.Params.TestDataConfig.NumTestCases ||
		bundle.RequestedSampleCount != input.Params.TestDataConfig.NumSamples {
		t.Fatalf("stored bundle lost requested test/sample counts: bundle=%+v params=%+v", bundle, input.Params.TestDataConfig)
	}
	var bundleProvenance map[string]interface{}
	if err := json.Unmarshal(provenance.records[1].Metadata, &bundleProvenance); err != nil {
		t.Fatalf("decode authoring bundle provenance: %v", err)
	}
	if bundleProvenance["requested_test_case_count"] != float64(input.Params.TestDataConfig.NumTestCases) ||
		bundleProvenance["requested_sample_count"] != float64(input.Params.TestDataConfig.NumSamples) {
		t.Fatalf("authoring bundle provenance lost requested counts: %+v", bundleProvenance)
	}
	if bundle.InputSHA256 != result.InputSHA256 || bundle.BriefSHA256 != input.CanonicalBriefSHA256 || bundle.SourceArtifactSHA256 != result.SourceArtifacts[0].SHA256 || bundle.SourceRequestSHA256 != result.SourceArtifacts[0].LLMCallReceipt.RequestSHA256 || bundle.DerivationRule != authoringPlanDerivationRuleV1 {
		t.Fatalf("bundle ancestry is wrong: %+v", bundle)
	}
	if bundle.SemanticSpec.SchemaVersion != domain.SemanticSpecSchemaV1 || bundle.SemanticSpec.BriefSHA256 != input.CanonicalBriefSHA256 || bundle.AuthoringPlan.SemanticSpecSHA256 != result.SemanticSpecSHA256 {
		t.Fatalf("server-owned bindings are wrong: %+v", bundle)
	}
	var provenanceMetadata map[string]interface{}
	if err := json.Unmarshal(provenance.records[1].Metadata, &provenanceMetadata); err != nil {
		t.Fatalf("decode bundle provenance metadata: %v", err)
	}
	if provenanceMetadata["source_artifact_sha256"] != result.SourceArtifacts[0].SHA256 || provenanceMetadata["source_request_sha256"] != result.SourceArtifacts[0].LLMCallReceipt.RequestSHA256 || provenanceMetadata["input_sha256"] != result.InputSHA256 {
		t.Fatalf("bundle provenance ancestry is incomplete: %+v", provenanceMetadata)
	}
}

func TestGenerateAuthoringPlanActivityReturnsLintFailureWithoutFallback(t *testing.T) {
	modelOutput := validAuthoringModelOutputV1()
	modelOutput.SemanticSpec.Sections.Output = nil
	responseJSON, err := marshalAuthoringModelOutputV1(modelOutput)
	if err != nil {
		t.Fatal(err)
	}
	provider := &capturingAuthoringLLM{response: authoringLLMResponse(string(responseJSON))}
	store := &captureArtifactStore{}
	provenance := &captureProvenanceRecorder{}
	activities := &Activities{
		deps:      &Dependencies{LLM: provider, LLMProvider: "fixture-provider", LLMModel: "fixture-model", ProvenanceRecorder: provenance},
		artifacts: store,
	}

	result := executeAuthoringActivity(t, activities, validAuthoringActivityInput())
	if result.ModelDecision != AuthoringPlanDecisionAccepted || result.Decision != AuthoringPlanDecisionRejected || result.GateReasonCode != AuthoringPlanGateReasonSpecLintFailed || result.LintPassed || result.LintErrorCount == 0 || provider.calls != 1 {
		t.Fatalf("lint failure fell through or retried another generation path: result=%+v calls=%d", result, provider.calls)
	}
	if result.BundleArtifact == nil || len(result.SourceArtifacts) != 2 || len(provenance.records) != 2 {
		t.Fatalf("lint failure lost its immutable artifacts: result=%+v provenance=%+v", result, provenance.records)
	}
	var bundle AuthoringPlanBundleV1
	if err := json.Unmarshal(store.data, &bundle); err != nil {
		t.Fatalf("decode lint-rejected bundle: %v", err)
	}
	if bundle.ModelDecision != AuthoringPlanDecisionAccepted || bundle.Decision != AuthoringPlanDecisionRejected || bundle.GateReasonCode != AuthoringPlanGateReasonSpecLintFailed || bundle.LintReport == nil || bundle.LintReport.Passed {
		t.Fatalf("lint-rejected bundle is not fail closed: %+v", bundle)
	}
}

func TestGenerateAuthoringPlanActivityPersistsExplicitRejection(t *testing.T) {
	responseJSON := `{"decision":"rejected","rejection_reason":"the frozen objective contradicts its output rule"}`
	provider := &capturingAuthoringLLM{response: authoringLLMResponse(responseJSON)}
	store := &captureArtifactStore{}
	provenance := &captureProvenanceRecorder{}
	activities := &Activities{
		deps:      &Dependencies{LLM: provider, LLMProvider: "fixture-provider", LLMModel: "fixture-model", ProvenanceRecorder: provenance},
		artifacts: store,
	}

	result := executeAuthoringActivity(t, activities, validAuthoringActivityInput())
	if result.ModelDecision != AuthoringPlanDecisionRejected || result.Decision != AuthoringPlanDecisionRejected || result.GateReasonCode != AuthoringPlanGateReasonModelRejected || result.RejectionReason == "" || result.LintPassed || result.BundleArtifact == nil || len(result.SourceArtifacts) != 2 {
		t.Fatalf("rejection was not preserved: %+v", result)
	}
}

func TestGenerateAuthoringPlanActivityBindsRejectedBundleToInputAndSource(t *testing.T) {
	responseJSON := `{"decision":"rejected","rejection_reason":"the frozen objective contradicts its output rule"}`
	execute := func(t *testing.T, brief string) (*GenerateAuthoringPlanResult, AuthoringPlanBundleV1) {
		t.Helper()
		store := &captureArtifactStore{}
		provenance := &captureProvenanceRecorder{}
		activities := &Activities{
			deps:      &Dependencies{LLM: staticLLM{response: authoringLLMResponse(responseJSON)}, LLMProvider: "fixture-provider", LLMModel: "fixture-model", ProvenanceRecorder: provenance},
			artifacts: store,
		}
		input := validAuthoringActivityInput()
		input.CanonicalBrief = brief
		input.CanonicalBriefSHA256 = sha256Hex([]byte(brief))
		result := executeAuthoringActivity(t, activities, input)
		var bundle AuthoringPlanBundleV1
		if err := json.Unmarshal(store.data, &bundle); err != nil {
			t.Fatalf("decode rejected bundle: %v", err)
		}
		if bundle.InputSHA256 != result.InputSHA256 || bundle.BriefSHA256 != input.CanonicalBriefSHA256 ||
			bundle.RequestedTestCaseCount != input.Params.TestDataConfig.NumTestCases ||
			bundle.RequestedSampleCount != input.Params.TestDataConfig.NumSamples ||
			bundle.SourceArtifactSHA256 != result.SourceArtifacts[0].SHA256 || bundle.SourceRequestSHA256 == "" {
			t.Fatalf("rejected bundle lacks input/source binding: bundle=%+v result=%+v", bundle, result)
		}
		return result, bundle
	}

	first, _ := execute(t, "Frozen brief A.")
	second, _ := execute(t, "Frozen brief B.")
	if first.BundleSHA256 == second.BundleSHA256 || first.InputSHA256 == second.InputSHA256 {
		t.Fatalf("different rejected inputs collapsed to one identity: first=%+v second=%+v", first, second)
	}
}

func TestGenerateAuthoringPlanActivityFailsClosedOnProviderAndBundleCAS(t *testing.T) {
	t.Run("provider", func(t *testing.T) {
		store := &captureArtifactStore{}
		provenance := &captureProvenanceRecorder{}
		activities := &Activities{
			deps:      &Dependencies{LLM: failingLLM{err: errors.New("injected provider failure")}, LLMProvider: "fixture-provider", LLMModel: "fixture-model", ProvenanceRecorder: provenance},
			artifacts: store,
		}
		_, err := executeAuthoringActivityRaw(activities, validAuthoringActivityInput())
		if err == nil || len(provenance.records) != 1 {
			t.Fatalf("provider failure err=%v provenance=%+v", err, provenance.records)
		}
	})

	t.Run("bundle CAS", func(t *testing.T) {
		modelOutput := validAuthoringModelOutputV1()
		responseJSON, err := marshalAuthoringModelOutputV1(modelOutput)
		if err != nil {
			t.Fatal(err)
		}
		store := &failOnPutArtifactStore{failOn: 2}
		provenance := &captureProvenanceRecorder{}
		activities := &Activities{
			deps:      &Dependencies{LLM: staticLLM{response: authoringLLMResponse(string(responseJSON))}, LLMProvider: "fixture-provider", LLMModel: "fixture-model", ProvenanceRecorder: provenance},
			artifacts: store,
		}
		_, err = executeAuthoringActivityRaw(activities, validAuthoringActivityInput())
		if err == nil || !strings.Contains(err.Error(), "injected authoring bundle CAS failure") || store.puts != 2 || len(provenance.records) != 1 {
			t.Fatalf("CAS failure err=%v puts=%d provenance=%+v", err, store.puts, provenance.records)
		}
	})
}

func TestGenerateAuthoringPlanActivityRejectsOversizeCanonicalBundle(t *testing.T) {
	modelOutput := validAuthoringModelOutputV1()
	modelOutput.AuthoringPlan.CreativeIntent = strings.Repeat("x", maxAuthoringPlanBundleBytes)
	responseJSON, err := marshalAuthoringModelOutputV1(modelOutput)
	if err != nil {
		t.Fatal(err)
	}
	store := &captureArtifactStore{}
	provenance := &captureProvenanceRecorder{}
	activities := &Activities{
		deps:      &Dependencies{LLM: staticLLM{response: authoringLLMResponse(string(responseJSON))}, LLMProvider: "fixture-provider", LLMModel: "fixture-model", ProvenanceRecorder: provenance},
		artifacts: store,
	}
	_, err = executeAuthoringActivityRaw(activities, validAuthoringActivityInput())
	if err == nil || !strings.Contains(err.Error(), "canonical authoring bundle exceeds") || len(provenance.records) != 1 {
		t.Fatalf("oversize bundle err=%v provenance=%+v", err, provenance.records)
	}
}

func TestParseAuthoringPlanResponseV1IsStrict(t *testing.T) {
	for name, value := range map[string]string{
		"unknown field":      `{"decision":"rejected","rejection_reason":"x","extra":true}`,
		"trailing JSON":      `{"decision":"rejected","rejection_reason":"x"}{}`,
		"mixed reject":       `{"decision":"rejected","rejection_reason":"x","semantic_spec":{}}`,
		"duplicate top key":  `{"decision":"rejected","decision":"accepted","rejection_reason":"x"}`,
		"duplicate nested":   `{"decision":"accepted","semantic_spec":{"problem_definition":"x","problem_definition":"y"},"authoring_plan":{}}`,
		"case variant":       `{"Decision":"rejected","rejection_reason":"x"}`,
		"server owned empty": `{"decision":"accepted","semantic_spec":{"schema_version":""},"authoring_plan":{}}`,
		"server owned null":  `{"decision":"accepted","semantic_spec":{"schema_version":null},"authoring_plan":{}}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseAuthoringPlanResponseV1(value); err == nil {
				t.Fatalf("strict parser accepted %s", value)
			}
		})
	}
}

func executeAuthoringActivity(t *testing.T, activities *Activities, input GenerateAuthoringPlanInput) *GenerateAuthoringPlanResult {
	t.Helper()
	encoded, err := executeAuthoringActivityRaw(activities, input)
	if err != nil {
		t.Fatalf("execute authoring activity: %v", err)
	}
	var result GenerateAuthoringPlanResult
	if err := encoded.Get(&result); err != nil {
		t.Fatalf("decode authoring result: %v", err)
	}
	return &result
}

func executeAuthoringActivityRaw(activities *Activities, input GenerateAuthoringPlanInput) (converterEncodedValue, error) {
	var suite testsuite.WorkflowTestSuite
	environment := suite.NewTestActivityEnvironment()
	environment.RegisterActivity(activities.GenerateAuthoringPlanActivity)
	return environment.ExecuteActivity(activities.GenerateAuthoringPlanActivity, input)
}

// converterEncodedValue is the narrow result surface used by test helpers.
type converterEncodedValue interface {
	Get(interface{}) error
}

func validAuthoringActivityInput() GenerateAuthoringPlanInput {
	params := domain.DefaultProblemGenParams()
	params.Tags = []string{"prefix-sum"}
	brief := "Given a sequence, compute its aggregate exactly once."
	return GenerateAuthoringPlanInput{
		PayloadVersion:       GenerateAuthoringPlanPayloadVersion,
		Params:               params,
		CanonicalBrief:       brief,
		CanonicalBriefSHA256: sha256Hex([]byte(brief)),
	}
}

func authoringLLMResponse(text string) *llm.Response {
	return &llm.Response{
		Model: "fixture-returned-model", ModelObserved: true,
		Content: []llm.ContentBlock{{Type: "text", Text: text}},
	}
}

func marshalAuthoringModelOutputV1(output authoringPlanModelOutputV1) ([]byte, error) {
	encoded, err := json.Marshal(output)
	if err != nil {
		return nil, err
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &top); err != nil {
		return nil, err
	}
	for _, objectName := range []string{"semantic_spec", "authoring_plan"} {
		raw, exists := top[objectName]
		if !exists {
			continue
		}
		var object map[string]json.RawMessage
		if err := json.Unmarshal(raw, &object); err != nil {
			return nil, err
		}
		delete(object, "schema_version")
		delete(object, "brief_sha256")
		delete(object, "semantic_spec_sha256")
		top[objectName], err = json.Marshal(object)
		if err != nil {
			return nil, err
		}
	}
	return json.Marshal(top)
}

func validAuthoringModelOutputV1() authoringPlanModelOutputV1 {
	nMin, nMax := int64(1), int64(200000)
	aMin, aMax := int64(1), int64(200000)
	elementMin, elementMax := int64(-1000000000), int64(1000000000)
	one := int64(1)
	aCount := domain.SemanticExpressionV1{Kind: domain.SemanticExpressionSymbol, Symbol: "n"}
	semantic := &domain.SemanticSpecV1{
		ProblemDefinition: "Given an integer sequence, output the sum of all elements.",
		Sections: domain.SemanticSpecSectionsV1{
			Input:       &domain.SemanticSectionV1{Summary: "Read n and then n integers.", SymbolRefs: []string{"n", "a"}},
			Output:      &domain.SemanticSectionV1{Summary: "Print the unique integer sum.", SymbolRefs: []string{"answer"}},
			Constraints: &domain.SemanticSectionV1{Summary: "n, sequence length, and element values are bounded.", SymbolRefs: []string{"n", "a"}},
		},
		InputGrammar: domain.SemanticGrammarV1{
			Profile: domain.SemanticGrammarTokenLinesV1,
			Lines: []domain.SemanticGrammarLineV1{
				{ID: "size", Repeat: domain.SemanticExpressionV1{Kind: domain.SemanticExpressionInteger, Value: &one}, Fields: []domain.SemanticGrammarFieldV1{{Symbol: "n", Mode: domain.SemanticGrammarFieldScalar}}, Meaning: "read the sequence length"},
				{ID: "values", Repeat: domain.SemanticExpressionV1{Kind: domain.SemanticExpressionInteger, Value: &one}, Fields: []domain.SemanticGrammarFieldV1{{Symbol: "a", Mode: domain.SemanticGrammarFieldSequence, Count: &aCount}}, Meaning: "read exactly n values"},
			},
		},
		OutputGrammar: domain.SemanticGrammarV1{
			Profile: domain.SemanticGrammarTokenLinesV1,
			Lines: []domain.SemanticGrammarLineV1{
				{ID: "answer", Repeat: domain.SemanticExpressionV1{Kind: domain.SemanticExpressionInteger, Value: &one}, Fields: []domain.SemanticGrammarFieldV1{{Symbol: "answer", Mode: domain.SemanticGrammarFieldScalar}}, Meaning: "print the sum"},
			},
		},
		Objective: domain.SemanticObjectiveV1{Kind: domain.SemanticObjectiveCompute, Summary: "Compute the sequence sum.", SymbolRefs: []string{"a", "answer"}},
		Symbols: []domain.SemanticSymbolV1{
			{Name: "n", Type: domain.SemanticSymbolInteger, Scope: domain.SemanticSymbolScopeInput, Role: "sequence length", Definition: "number of values", BoundaryPolicy: domain.SemanticBoundaryPolicyZeroOneRequired},
			{Name: "a", Type: domain.SemanticSymbolIntegerSequence, Scope: domain.SemanticSymbolScopeInput, Role: "input values", Definition: "sequence of n integers", BoundaryPolicy: domain.SemanticBoundaryPolicyExplicit},
			{Name: "answer", Type: domain.SemanticSymbolInteger, Scope: domain.SemanticSymbolScopeOutput, Role: "result", Definition: "sum of all values", BoundaryPolicy: domain.SemanticBoundaryPolicyNotApplicable},
		},
		Constraints: []domain.SemanticConstraintV1{
			{Subject: "n", Kind: domain.SemanticConstraintIntegerRange, Min: &nMin, Max: &nMax, Meaning: "valid sequence length"},
			{Subject: "a", Kind: domain.SemanticConstraintLengthRange, Min: &aMin, Max: &aMax, Meaning: "valid sequence length"},
			{Subject: "a", Kind: domain.SemanticConstraintElementRange, Min: &elementMin, Max: &elementMax, Meaning: "valid element values"},
		},
		Relations: []domain.SemanticRelationV1{{
			ID: "a-length-equals-n", Left: domain.SemanticExpressionV1{Kind: domain.SemanticExpressionLength, Symbol: "a"},
			Operator: domain.SemanticRelationEqual, Right: domain.SemanticExpressionV1{Kind: domain.SemanticExpressionSymbol, Symbol: "n"},
			Meaning: "the sequence contains exactly n values",
		}},
		Topology: domain.SemanticTopologyV1{
			Kind: domain.SemanticTopologyLinear, Subject: "a", Direction: domain.SemanticTopologyNotApplicable,
			CyclePolicy: domain.SemanticTopologyAcyclic, Connectivity: domain.SemanticTopologyNotApplicable,
			Dynamics: domain.SemanticTopologyStatic, SelfLoops: domain.SemanticTopologyNotApplicable, MultiEdges: domain.SemanticTopologyNotApplicable,
		},
		Boundaries: []domain.SemanticBoundaryV1{
			{ID: "n-zero", Symbol: "n", Measure: domain.SemanticBoundaryMeasureValue, Value: 0, Allowed: false, Meaning: "empty input forbidden"},
			{ID: "n-one", Symbol: "n", Measure: domain.SemanticBoundaryMeasureValue, Value: 1, Allowed: true, Meaning: "single element allowed"},
			{ID: "n-max", Symbol: "n", Measure: domain.SemanticBoundaryMeasureValue, Value: 200000, Allowed: true, Meaning: "maximum length"},
			{ID: "a-zero", Symbol: "a", Measure: domain.SemanticBoundaryMeasureLength, Value: 0, Allowed: false, Meaning: "empty sequence forbidden"},
			{ID: "a-one", Symbol: "a", Measure: domain.SemanticBoundaryMeasureLength, Value: 1, Allowed: true, Meaning: "single value sequence"},
		},
		AnswerSemantics: domain.SemanticAnswerSemanticsV1{NoSolutionPolicy: domain.SemanticNoSolutionImpossible, MultipleSolutionPolicy: domain.SemanticMultipleSolutionUnique},
		Judge:           domain.SemanticJudgeV1{Mode: domain.SemanticJudgeExactNormalized, ComparisonProfile: domain.SemanticComparisonTrimTrailingSpaceLFV1},
		SampleInputs:    []string{"3\n1 2 3\n"},
	}
	authoring := &domain.AuthoringPlanV1{
		CoreIdea:                "Accumulate every value exactly once.",
		ConceptRoles:            []domain.AuthoringConceptRoleV1{{Slug: "prefix-sum", Role: "main algorithm", Necessity: "linear aggregation"}},
		IntendedSolution:        domain.AuthoringAlgorithmPlanV1{Summary: "single pass sum", TimeComplexity: "O(n)", SpaceComplexity: "O(1)"},
		BruteForceBaseline:      domain.AuthoringAlgorithmPlanV1{Summary: "direct sum on tiny inputs", TimeComplexity: "O(n)", SpaceComplexity: "O(1)"},
		OracleCandidateStrategy: domain.AuthoringAlgorithmPlanV1{Summary: "checked integer accumulation", TimeComplexity: "O(n)", SpaceComplexity: "O(1)"},
		FailureModes:            []domain.AuthoringFailureModeV1{{ID: "overflow", Description: "narrow accumulator", WitnessIntent: "large values"}},
		TestIntents:             []domain.AuthoringTestIntentV1{{Purpose: "minimum", ConstraintRegion: "n=1", BoundaryRefs: []string{"n-one"}}},
		TargetDifficulty:        1500, TeachingObjectives: []string{"linear aggregation"}, CreativeIntent: "Keep the statement concise.",
	}
	return authoringPlanModelOutputV1{Decision: AuthoringPlanDecisionAccepted, SemanticSpec: semantic, AuthoringPlan: authoring}
}

func TestAuthoringFixtureDigestHelper(t *testing.T) {
	digest := sha256.Sum256([]byte("fixture"))
	if sha256Hex([]byte("fixture")) != hex.EncodeToString(digest[:]) {
		t.Fatal("unexpected SHA-256 helper result")
	}
}
