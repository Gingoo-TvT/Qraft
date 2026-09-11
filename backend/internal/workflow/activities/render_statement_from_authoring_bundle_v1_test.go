package activities

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/llm"
	speccontract "github.com/Gingoo-TvT/Qraft/backend/internal/spec"
	"go.temporal.io/sdk/testsuite"
)

type statementDraftCASStore struct {
	bucket  string
	objects map[string][]byte
	puts    []ArtifactRef
	getErr  error
}

func newStatementDraftCASStore() *statementDraftCASStore {
	return &statementDraftCASStore{bucket: "fixture", objects: make(map[string][]byte)}
}

func (s *statementDraftCASStore) Put(_ context.Context, data []byte, contentType string, metadata ArtifactMetadata) (ArtifactRef, error) {
	digest := sha256.Sum256(data)
	digestHex := hex.EncodeToString(digest[:])
	ref := ArtifactRef{
		SchemaVersion:  ArtifactRefSchemaVersion,
		PayloadVersion: metadata.PayloadVersion,
		Bucket:         s.bucket,
		Key:            artifactKey(digestHex),
		SHA256:         digestHex,
		SizeBytes:      int64(len(data)),
		ContentType:    contentType,
		Producer:       metadata.Producer,
		Provider:       metadata.Provider,
		Model:          metadata.Model,
		ModelRevision:  metadata.ModelRevision,
		WorkflowID:     metadata.WorkflowID,
	}
	if err := ref.Validate(s.bucket); err != nil {
		return ArtifactRef{}, err
	}
	s.objects[ref.SHA256] = append([]byte(nil), data...)
	s.puts = append(s.puts, ref)
	return ref, nil
}

func (s *statementDraftCASStore) Get(_ context.Context, ref ArtifactRef) ([]byte, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	if err := ref.Validate(s.bucket); err != nil {
		return nil, err
	}
	data, ok := s.objects[ref.SHA256]
	if !ok {
		return nil, errors.New("fixture CAS object not found")
	}
	if int64(len(data)) != ref.SizeBytes || sha256Hex(data) != ref.SHA256 {
		return nil, errors.New("fixture CAS identity mismatch")
	}
	return append([]byte(nil), data...), nil
}

func (s *statementDraftCASStore) seedAuthoringBundle(t *testing.T, data []byte) ArtifactRef {
	t.Helper()
	digest := sha256.Sum256(data)
	digestHex := hex.EncodeToString(digest[:])
	ref := ArtifactRef{
		SchemaVersion:  ArtifactRefSchemaVersion,
		PayloadVersion: ActivityPayloadVersion,
		Bucket:         s.bucket,
		Key:            artifactKey(digestHex),
		SHA256:         digestHex,
		SizeBytes:      int64(len(data)),
		ContentType:    "application/json",
		Producer:       "GenerateAuthoringPlanActivity",
		Provider:       "algoforge",
		Model:          "not_applicable",
		ModelRevision:  "not_applicable",
		WorkflowID:     "authoring-fixture",
	}
	if err := ref.Validate(s.bucket); err != nil {
		t.Fatalf("seed authoring bundle ref: %v", err)
	}
	s.objects[ref.SHA256] = append([]byte(nil), data...)
	return ref
}

type capturingStatementDraftLLM struct {
	response *llm.Response
	err      error
	calls    int
	request  *llm.Request
}

func (l *capturingStatementDraftLLM) CompleteWithRetry(_ context.Context, request *llm.Request, _ int) (*llm.Response, error) {
	l.calls++
	l.request = request
	return l.response, l.err
}

type statementDraftFixture struct {
	store       *statementDraftCASStore
	bundle      AuthoringPlanBundleV1
	bundleBytes []byte
	input       RenderStatementFromAuthoringBundleInput
}

func newStatementDraftFixture(t *testing.T, samples []string) *statementDraftFixture {
	t.Helper()
	authoringInput := validAuthoringActivityInput()
	authoringInput.Params.TestDataConfig.NumSamples = len(samples)
	modelOutput := validAuthoringModelOutputV1()
	modelOutput.SemanticSpec.SampleInputs = append([]string(nil), samples...)
	modelOutput.SemanticSpec.SchemaVersion = domain.SemanticSpecSchemaV1
	modelOutput.SemanticSpec.BriefSHA256 = authoringInput.CanonicalBriefSHA256
	semanticSHA := speccontract.SemanticSpecSHA256V1(*modelOutput.SemanticSpec)
	modelOutput.AuthoringPlan.SchemaVersion = domain.AuthoringPlanSchemaV1
	modelOutput.AuthoringPlan.BriefSHA256 = authoringInput.CanonicalBriefSHA256
	modelOutput.AuthoringPlan.SemanticSpecSHA256 = semanticSHA
	lint := speccontract.LintV1(speccontract.LintInputV1{
		SemanticSpec:            *modelOutput.SemanticSpec,
		AuthoringPlan:           *modelOutput.AuthoringPlan,
		ExpectedBriefSHA256:     authoringInput.CanonicalBriefSHA256,
		ExpectedDifficulty:      authoringInput.Params.Difficulty,
		RequiredKnowledgePoints: append([]string(nil), authoringInput.Params.Tags...),
	})
	if !lint.Passed {
		t.Fatalf("fixture lint failed: %+v", lint)
	}
	authoringInputSHA, err := canonicalJSONSHA256(authoringInput)
	if err != nil {
		t.Fatal(err)
	}
	bundle := AuthoringPlanBundleV1{
		SchemaVersion:          AuthoringPlanBundleSchemaV1,
		InputSHA256:            authoringInputSHA,
		BriefSHA256:            authoringInput.CanonicalBriefSHA256,
		RequestedTestCaseCount: authoringInput.Params.TestDataConfig.NumTestCases,
		RequestedSampleCount:   authoringInput.Params.TestDataConfig.NumSamples,
		MinTestCaseCount:       domain.MinAdaptiveTestCases,
		MaxTestCaseCount:       domain.MaxAdaptiveTestCases,
		AdaptiveTestCaseCount:  true,
		SourceArtifactSHA256:   strings.Repeat("a", 64),
		SourceRequestSHA256:    strings.Repeat("b", 64),
		DerivationRule:         authoringPlanDerivationRuleV1,
		ModelDecision:          AuthoringPlanDecisionAccepted,
		Decision:               AuthoringPlanDecisionAccepted,
		SemanticSpec:           modelOutput.SemanticSpec,
		AuthoringPlan:          modelOutput.AuthoringPlan,
		LintReport:             &lint,
	}
	encoded, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	store := newStatementDraftCASStore()
	ref := store.seedAuthoringBundle(t, encoded)
	return &statementDraftFixture{
		store:       store,
		bundle:      bundle,
		bundleBytes: append([]byte(nil), encoded...),
		input: RenderStatementFromAuthoringBundleInput{
			PayloadVersion:               RenderStatementFromAuthoringBundlePayloadVersion,
			BundleArtifact:               ref,
			ExpectedBundleSHA256:         ref.SHA256,
			ExpectedAuthoringInputSHA256: authoringInputSHA,
			ExpectedBriefSHA256:          authoringInput.CanonicalBriefSHA256,
			ExpectedSemanticSpecSHA256:   semanticSHA,
			ExpectedDifficulty:           authoringInput.Params.Difficulty,
			RequiredKnowledgePoints:      append([]string(nil), authoringInput.Params.Tags...),
			PresentationLocale:           "en",
		},
	}
}

func (f *statementDraftFixture) replaceBundle(t *testing.T) {
	t.Helper()
	encoded, err := json.Marshal(f.bundle)
	if err != nil {
		t.Fatal(err)
	}
	ref := f.store.seedAuthoringBundle(t, encoded)
	f.bundleBytes = append([]byte(nil), encoded...)
	f.input.BundleArtifact = ref
	f.input.ExpectedBundleSHA256 = ref.SHA256
}

func statementDraftLLMResponse(text string) *llm.Response {
	return &llm.Response{
		Model:         "fixture-narrative-model",
		ModelObserved: true,
		Content:       []llm.ContentBlock{{Type: "text", Text: text}},
	}
}

func executeStatementDraftActivity(
	t *testing.T,
	activities *Activities,
	input RenderStatementFromAuthoringBundleInput,
) (*RenderStatementFromAuthoringBundleResult, error) {
	t.Helper()
	var suite testsuite.WorkflowTestSuite
	environment := suite.NewTestActivityEnvironment()
	environment.RegisterActivity(activities.RenderStatementFromAuthoringBundleActivityV1)
	encoded, err := environment.ExecuteActivity(activities.RenderStatementFromAuthoringBundleActivityV1, input)
	if err != nil {
		return nil, err
	}
	var result RenderStatementFromAuthoringBundleResult
	if err := encoded.Get(&result); err != nil {
		t.Fatalf("decode statement draft result: %v", err)
	}
	return &result, nil
}

func TestRenderStatementFromAuthoringBundleActivityStoresBoundPresentationShell(t *testing.T) {
	fixture := newStatementDraftFixture(t, []string{"PRIVATE_SAMPLE_1\n"})
	runtimeConfig := &domain.LLMRuntimeConfig{Model: " narrative-model "}
	fixture.input.StatementRuntime = runtimeConfig
	runtimeBefore := *runtimeConfig
	provider := &capturingStatementDraftLLM{response: statementDraftLLMResponse(`{"title":"Lantern Gathering","narrative":"Friends meet beneath glowing lanterns."}`)}
	provenance := &captureProvenanceRecorder{}
	activities := &Activities{
		deps: &Dependencies{
			LLM: provider, LLMProvider: "fixture-provider", LLMModel: "fallback-model",
			ProvenanceRecorder: provenance,
		},
		artifacts: fixture.store,
	}

	result, err := executeStatementDraftActivity(t, activities, fixture.input)
	if err != nil {
		t.Fatalf("render statement draft: %v", err)
	}
	if provider.calls != 1 || provider.request == nil || provider.request.Model != "narrative-model" {
		t.Fatalf("statement runtime not applied: calls=%d request=%+v", provider.calls, provider.request)
	}
	if !reflect.DeepEqual(*runtimeConfig, runtimeBefore) {
		t.Fatalf("renderer mutated caller runtime: got=%+v want=%+v", *runtimeConfig, runtimeBefore)
	}
	if result.StatementDraftArtifact == nil || result.StatementDraftSHA256 != result.StatementDraftArtifact.SHA256 || len(result.SourceArtifacts) != 3 {
		t.Fatalf("incomplete statement draft result: %+v", result)
	}
	if result.SourceArtifacts[0].SHA256 != fixture.input.ExpectedBundleSHA256 || result.SourceArtifacts[1].LLMCallReceipt == nil ||
		!reflect.DeepEqual(result.SourceArtifacts[2], result.StatementDraftArtifact) {
		t.Fatalf("statement draft ancestry is incomplete: %+v", result.SourceArtifacts)
	}
	draftBytes, err := fixture.store.Get(context.Background(), *result.StatementDraftArtifact)
	if err != nil {
		t.Fatal(err)
	}
	var draft StatementDraftBundleV1
	if err := json.Unmarshal(draftBytes, &draft); err != nil {
		t.Fatal(err)
	}
	if draft.SchemaVersion != StatementDraftBundleSchemaV1 || draft.DocumentStatus != statementDraftDocumentStatusV1 ||
		draft.RendererInputSHA256 != result.RendererInputSHA256 || draft.FactManifestSHA256 != result.FactManifestSHA256 ||
		draft.MarkdownSHA256 != result.MarkdownSHA256 {
		t.Fatalf("draft bindings are incomplete: %+v", draft)
	}
	if strings.Count(draft.Markdown, StatementSamplesPlaceholder) != 1 ||
		strings.Contains(draft.Markdown, "## Samples") || strings.Contains(draft.Markdown, "## 样例") {
		t.Fatalf("draft sample marker contract violated: %s", draft.Markdown)
	}
	if !strings.Contains(draft.Markdown, fixture.bundle.SemanticSpec.ProblemDefinition) {
		t.Fatal("frozen normative prose was not emitted verbatim")
	}
	if strings.Contains(string(draftBytes), "PRIVATE_SAMPLE_1") || strings.Contains(string(draftBytes), "sample_input_candidates") {
		t.Fatal("untrusted sample candidates leaked into the statement draft")
	}
	if len(provenance.records) != 2 || provenance.records[1].ArtifactType != "qg02c_statement_draft_bundle" {
		t.Fatalf("unexpected statement draft provenance: %+v", provenance.records)
	}
	if !strings.Contains(provider.request.Messages[0].Content, `"presentation_locale":"en"`) ||
		!strings.Contains(provider.request.Messages[0].Content, `"creative_intent":"Keep the statement concise."`) {
		t.Fatalf("compact narrative prompt is incomplete: %s", provider.request.Messages[0].Content)
	}
	for _, forbidden := range []string{"problem_definition", "PRIVATE_SAMPLE_1", "single pass sum", "failure_modes", "target_difficulty"} {
		if strings.Contains(provider.request.Messages[0].Content, forbidden) {
			t.Fatalf("authoring/spec detail %q leaked into narrative prompt: %s", forbidden, provider.request.Messages[0].Content)
		}
	}
}

func TestRenderStatementPromptAndProjectionExcludeZeroOneAndManySampleCandidates(t *testing.T) {
	variants := [][]string{
		nil,
		{"SAMPLE_ONE_UNIQUE\n"},
		{"SAMPLE_MANY_A\n", "SAMPLE_MANY_B\n", "SAMPLE_MANY_C\n"},
	}
	var firstPrompt, firstFactSHA string
	for index, samples := range variants {
		fixture := newStatementDraftFixture(t, samples)
		provider := &capturingStatementDraftLLM{response: statementDraftLLMResponse(`{"title":"Lantern Gathering","narrative":"Friends meet beneath glowing lanterns."}`)}
		activities := &Activities{
			deps: &Dependencies{
				LLM: provider, LLMProvider: "fixture-provider", LLMModel: "fixture-model",
				ProvenanceRecorder: &captureProvenanceRecorder{},
			},
			artifacts: fixture.store,
		}
		result, err := executeStatementDraftActivity(t, activities, fixture.input)
		if err != nil {
			t.Fatalf("variant %d: %v", index, err)
		}
		prompt := provider.request.Messages[0].Content
		for _, sample := range samples {
			if strings.Contains(prompt, strings.TrimSpace(sample)) {
				t.Fatalf("variant %d sample leaked into prompt", index)
			}
		}
		if strings.Contains(prompt, "sample_input_candidates") || strings.Contains(prompt, "problem_definition") {
			t.Fatalf("variant %d prompt contains forbidden fact projection: %s", index, prompt)
		}
		if !bytes.Equal(fixture.store.objects[fixture.input.BundleArtifact.SHA256], fixture.bundleBytes) {
			t.Fatalf("variant %d mutated the immutable authoring bundle", index)
		}
		if index == 0 {
			firstPrompt, firstFactSHA = prompt, result.FactManifestSHA256
		} else if prompt != firstPrompt || result.FactManifestSHA256 != firstFactSHA {
			t.Fatalf("sample candidates influenced prompt/projection for variant %d", index)
		}
	}
}

func TestParseStatementNarrativeResponseV1IsStrict(t *testing.T) {
	valid := `{"title":"Lantern Gathering","narrative":"Friends meet beneath glowing lanterns."}`
	if _, err := parseStatementNarrativeResponseV1(valid); err != nil {
		t.Fatalf("valid response rejected: %v", err)
	}
	cases := map[string]string{
		"unknown key":       `{"title":"Lantern","narrative":"Friends gather.","extra":true}`,
		"duplicate key":     `{"title":"Lantern","title":"Other","narrative":"Friends gather."}`,
		"wrong case":        `{"Title":"Lantern","narrative":"Friends gather."}`,
		"trailing document": valid + `{}`,
		"markdown fence":    "```json\n" + valid + "\n```",
		"missing field":     `{"title":"Lantern"}`,
		"control":           `{"title":"Lantern","narrative":"Friends\u0001gather."}`,
		"markdown":          `{"title":"# Lantern","narrative":"Friends gather."}`,
		"marker":            `{"title":"Lantern","narrative":"` + StatementSamplesPlaceholder + `"}`,
		"invalid utf8":      `{"title":"Lantern","narrative":"` + string([]byte{0xff}) + `"}`,
	}
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := parseStatementNarrativeResponseV1(value); err == nil {
				t.Fatal("malformed response was accepted")
			}
		})
	}
}

func TestStatementNarrativeFactLintRejectsNormativeClaimsAndAllowsArticleA(t *testing.T) {
	fixture := newStatementDraftFixture(t, nil)
	facts := statementFactManifestFromSemanticSpecV1(*fixture.bundle.SemanticSpec)
	if err := lintStatementNarrativeAgainstFactsV1(statementNarrativeModelOutputV1{
		Title: "Contest of Lanterns", Narrative: "A group of friends gathers beneath lanterns.",
	}, facts); err != nil {
		t.Fatalf("common article a or an innocent test substring was rejected: %v", err)
	}
	cases := []statementNarrativeModelOutputV1{
		{Title: "Lantern n", Narrative: "Friends gather."},
		{Title: "Lantern 7", Narrative: "Friends gather."},
		{Title: "Lantern", Narrative: "Friends compute an answer."},
		{Title: "Lantern", Narrative: "A hidden hint awaits."},
		{Title: "灯火", Narrative: "让我逐步分析这个任务。"},
		{Title: "Lantern", Narrative: "Friends compare x=y."},
	}
	for index, output := range cases {
		if err := lintStatementNarrativeAgainstFactsV1(output, facts); err == nil {
			t.Fatalf("normative narrative %d was accepted: %+v", index, output)
		}
	}
}

func TestDecodeCanonicalAuthoringBundleV1RejectsNonCanonicalAndAmbiguousJSON(t *testing.T) {
	fixture := newStatementDraftFixture(t, nil)
	if _, err := decodeCanonicalAuthoringBundleV1(fixture.bundleBytes); err != nil {
		t.Fatalf("canonical bundle rejected: %v", err)
	}
	cases := map[string][]byte{
		"whitespace":  append(append([]byte(nil), fixture.bundleBytes...), '\n'),
		"unknown key": []byte(`{"extra":true,` + string(fixture.bundleBytes[1:])),
		"duplicate":   []byte(`{"schema_version":"other",` + string(fixture.bundleBytes[1:])),
		"wrong case":  []byte(`{"SchemaVersion":"other",` + string(fixture.bundleBytes[1:])),
	}
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeCanonicalAuthoringBundleV1(value); err == nil {
				t.Fatal("ambiguous/non-canonical bundle was accepted")
			}
		})
	}
}

func TestDecodeCanonicalAuthoringBundleV1AcceptsLegacyBundleWithoutRequestedCounts(t *testing.T) {
	fixture := newStatementDraftFixture(t, []string{"3\n1 2 3\n"})
	legacy := fixture.bundle
	legacy.RequestedTestCaseCount = 0
	legacy.RequestedSampleCount = 0
	encoded, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte("requested_test_case_count")) || bytes.Contains(encoded, []byte("requested_sample_count")) {
		t.Fatalf("legacy bundle unexpectedly serialized requested-count fields: %s", encoded)
	}
	decoded, err := decodeCanonicalAuthoringBundleV1(encoded)
	if err != nil {
		t.Fatalf("legacy canonical v1 bundle was rejected: %v", err)
	}
	if decoded.RequestedTestCaseCount != 0 || decoded.RequestedSampleCount != 0 {
		t.Fatalf("legacy requested-count defaults changed: %+v", decoded)
	}
}

func TestRenderStatementFromAuthoringBundleFailsBeforeLLMOnCASHashLintAndRejectedBundle(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, *statementDraftFixture)
	}{
		{
			name: "bundle ref hash mismatch",
			mutate: func(_ *testing.T, fixture *statementDraftFixture) {
				fixture.input.ExpectedBundleSHA256 = strings.Repeat("f", 64)
			},
		},
		{
			name: "CAS read failure",
			mutate: func(_ *testing.T, fixture *statementDraftFixture) {
				fixture.store.getErr = errors.New("injected CAS read failure")
			},
		},
		{
			name: "lint expectation mismatch",
			mutate: func(_ *testing.T, fixture *statementDraftFixture) {
				fixture.input.ExpectedDifficulty += 100
			},
		},
		{
			name: "rejected authoring bundle",
			mutate: func(t *testing.T, fixture *statementDraftFixture) {
				fixture.bundle.ModelDecision = AuthoringPlanDecisionRejected
				fixture.bundle.Decision = AuthoringPlanDecisionRejected
				fixture.bundle.GateReasonCode = AuthoringPlanGateReasonModelRejected
				fixture.bundle.RejectionReason = "frozen concept rejected"
				fixture.bundle.SemanticSpec = nil
				fixture.bundle.AuthoringPlan = nil
				fixture.bundle.LintReport = nil
				fixture.replaceBundle(t)
			},
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newStatementDraftFixture(t, nil)
			testCase.mutate(t, fixture)
			provider := &capturingStatementDraftLLM{response: statementDraftLLMResponse(`{"title":"Lantern Gathering","narrative":"Friends meet beneath glowing lanterns."}`)}
			activities := &Activities{
				deps: &Dependencies{
					LLM: provider, LLMProvider: "fixture-provider", LLMModel: "fixture-model",
					ProvenanceRecorder: &captureProvenanceRecorder{},
				},
				artifacts: fixture.store,
			}
			if _, err := executeStatementDraftActivity(t, activities, fixture.input); err == nil {
				t.Fatal("invalid renderer input/bundle was accepted")
			}
			if provider.calls != 0 {
				t.Fatalf("LLM calls=%d, want 0", provider.calls)
			}
		})
	}
}

func TestRenderStatementFromAuthoringBundleHandlesNilResponseWithoutPanic(t *testing.T) {
	fixture := newStatementDraftFixture(t, nil)
	provider := &capturingStatementDraftLLM{}
	activities := &Activities{
		deps: &Dependencies{
			LLM: provider, LLMProvider: "fixture-provider", LLMModel: "fixture-model",
			ProvenanceRecorder: &captureProvenanceRecorder{},
		},
		artifacts: fixture.store,
	}
	if _, err := executeStatementDraftActivity(t, activities, fixture.input); err == nil {
		t.Fatal("nil model response was accepted")
	}
	if provider.calls != 1 {
		t.Fatalf("LLM calls=%d, want 1", provider.calls)
	}
}
