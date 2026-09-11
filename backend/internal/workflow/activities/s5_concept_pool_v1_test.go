package activities

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/diversity"
	"github.com/Gingoo-TvT/Qraft/backend/internal/diversityapi"
	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/llm"
)

type s5CapturingLLMV1 struct {
	responses    []*llm.Response
	requests     []*llm.Request
	retryBudgets []int
}

func (client *s5CapturingLLMV1) CompleteWithRetry(_ context.Context, request *llm.Request, retries int) (*llm.Response, error) {
	copyRequest := *request
	copyRequest.Messages = append([]llm.Message(nil), request.Messages...)
	if request.Runtime != nil {
		copyRuntime := *request.Runtime
		copyRequest.Runtime = &copyRuntime
	}
	client.requests = append(client.requests, &copyRequest)
	client.retryBudgets = append(client.retryBudgets, retries)
	if len(client.responses) == 0 {
		return nil, fmt.Errorf("unexpected S5 LLM call")
	}
	response := client.responses[0]
	client.responses = client.responses[1:]
	return response, nil
}

func TestGenerateS5ConceptAttemptActivityV1TwoByTwoAndSeparatedIDs(t *testing.T) {
	client := &s5CapturingLLMV1{responses: []*llm.Response{
		s5TestLLMResponseV1(`{"cards":[{"one_paragraph_pitch":"  Prefix extrema with offline events  ","unresolved_questions":["Tie handling","Boundary semantics"]},{"one_paragraph_pitch":"Parity paths under interval toggles","unresolved_questions":[]}]}`, 11, 7, 1),
		s5TestLLMResponseV1(`{"cards":[{"one_paragraph_pitch":"Compressed states on a rooted forest","unresolved_questions":["Root convention"]},{"one_paragraph_pitch":"Meet-in-the-middle transition counting","unresolved_questions":["Overflow"]}]}`, 13, 9, 0),
	}}
	activities, _ := s5TestActivitiesV1(client)
	firstInput := s5TestGenerateInputV1(t, 0)
	secondInput := s5TestGenerateInputV1(t, 1)
	first, err := activities.GenerateS5ConceptAttemptActivityV1(context.Background(), firstInput)
	if err != nil {
		t.Fatalf("first attempt: %v", err)
	}
	second, err := activities.GenerateS5ConceptAttemptActivityV1(context.Background(), secondInput)
	if err != nil {
		t.Fatalf("second attempt: %v", err)
	}

	if len(first.Attempt.Concepts) != 2 || len(second.Attempt.Concepts) != 2 {
		t.Fatalf("attempts are not 2x2: first=%d second=%d", len(first.Attempt.Concepts), len(second.Attempt.Concepts))
	}
	if first.Attempt.LogicalAttemptID == second.Attempt.LogicalAttemptID ||
		first.Attempt.Concepts[0].ConceptID == second.Attempt.Concepts[0].ConceptID {
		t.Fatalf("server identities are not separated: first=%+v second=%+v", first.Attempt, second.Attempt)
	}
	for _, result := range []*GenerateS5ConceptAttemptResultV1{first, second} {
		for _, card := range result.Attempt.Concepts {
			if card.Spec.SchemaVersion != "" {
				t.Fatalf("creative model prefilled a spec: %+v", card.Spec)
			}
		}
		if result.Attempt.Receipt.SHA256 == "" || !strings.HasPrefix(result.Attempt.Receipt.URI, "cas://sha256/") {
			t.Fatalf("missing compact CAS receipt: %+v", result.Attempt.Receipt)
		}
	}
	if first.Attempt.Usage.ModelCalls != 2 || first.Attempt.Usage.NetworkRetries != 1 || first.Attempt.Usage.Tokens != 18 {
		t.Fatalf("observed creative usage = %+v", first.Attempt.Usage)
	}
	if first.Attempt.Concepts[0].OneParagraphPitch != "Prefix extrema with offline events" ||
		!strings.EqualFold(first.Attempt.Concepts[0].UnresolvedQuestions[0], "Boundary semantics") {
		t.Fatalf("creative card was not canonicalized: %+v", first.Attempt.Concepts[0])
	}
	if len(client.requests) != 2 || client.retryBudgets[0] != firstInput.NetworkRetryBudget ||
		client.retryBudgets[1] != secondInput.NetworkRetryBudget {
		t.Fatalf("provider calls or retry allowances = requests:%d retries:%v", len(client.requests), client.retryBudgets)
	}
	if *client.requests[0].Temperature != s5CreativeTemperatureV1 ||
		client.requests[0].Runtime == nil || client.requests[0].Runtime.APIKeyRef != "env:ALGOFORGE_S5_TEST_KEY" {
		t.Fatalf("statement runtime/high temperature not applied: %+v", client.requests[0])
	}
	if first.Attempt.Receipt.SHA256 == second.Attempt.Receipt.SHA256 {
		t.Fatal("independent attempts shared a provider receipt")
	}
	encoded, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte("ALGOFORGE_S5_TEST_KEY")) {
		t.Fatalf("result leaked a credential reference: %s", encoded)
	}
}

func TestCreativeS5ResponseStrictShapeAndDuplicateRejection(t *testing.T) {
	in := s5TestGenerateInputV1(t, 0)
	tests := []struct {
		name string
		raw  string
	}{
		{name: "unknown root field", raw: `{"cards":[],"extra":true}`},
		{name: "wrong card count", raw: `{"cards":[{"one_paragraph_pitch":"one","unresolved_questions":[]}]}`},
		{name: "model supplied id", raw: `{"cards":[{"concept_id":"forged","one_paragraph_pitch":"one","unresolved_questions":[]},{"one_paragraph_pitch":"two","unresolved_questions":[]}]}`},
		{name: "model supplied spec", raw: `{"cards":[{"one_paragraph_pitch":"one","unresolved_questions":[],"spec":{}},{"one_paragraph_pitch":"two","unresolved_questions":[]}]}`},
		{name: "trailing JSON", raw: `{"cards":[{"one_paragraph_pitch":"one","unresolved_questions":[]},{"one_paragraph_pitch":"two","unresolved_questions":[]}]} {}`},
		{name: "duplicate pitch", raw: `{"cards":[{"one_paragraph_pitch":"same","unresolved_questions":[]},{"one_paragraph_pitch":" SAME ","unresolved_questions":[]}]}`},
		{name: "duplicate question", raw: `{"cards":[{"one_paragraph_pitch":"one","unresolved_questions":["x","x"]},{"one_paragraph_pitch":"two","unresolved_questions":[]}]}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := parseS5CreativeCardsV1(test.raw, in); err == nil {
				t.Fatalf("invalid creative response accepted: %s", test.raw)
			}
		})
	}
}

func TestNormalizeS5ConceptPoolActivityV1UnknownDraftAndDeterministicFinalize(t *testing.T) {
	in := s5TestNormalizeInputV1(t)
	originalAttempts, err := json.Marshal(in.Attempts)
	if err != nil {
		t.Fatal(err)
	}
	rawSpecs := s5TestRawSpecsV1(in.Attempts)
	rawSpecs[0] = s5TestUnknownRawSpecV1(rawSpecs[0].ConceptID)
	for left, right := 0, len(rawSpecs)-1; left < right; left, right = left+1, right-1 {
		rawSpecs[left], rawSpecs[right] = rawSpecs[right], rawSpecs[left]
	}
	responseBytes, err := json.Marshal(s5NormalizeModelOutputV1{Specs: rawSpecs})
	if err != nil {
		t.Fatal(err)
	}
	client := &s5CapturingLLMV1{responses: []*llm.Response{s5TestLLMResponseV1(string(responseBytes), 29, 31, 1)}}
	activities, _ := s5TestActivitiesV1(client)
	result, err := activities.NormalizeS5ConceptPoolActivityV1(context.Background(), in)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if result.Draft.SchemaVersion != S5NormalizedConceptPoolDraftSchemaV1 || result.DraftSHA256 == "" {
		t.Fatalf("draft identity = %+v", result)
	}
	draftBytes, err := json.Marshal(result.Draft)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(draftBytes, []byte("corpus_revision")) {
		t.Fatalf("normalizer forged a corpus revision: %s", draftBytes)
	}
	foundUnknown := false
	for _, attempt := range result.Draft.Attempts {
		for _, card := range attempt.Concepts {
			if card.Spec.Topology == diversity.UnknownValue {
				foundUnknown = card.Spec.SchemaVersion == diversity.ConceptSpecSchemaV1 &&
					card.Spec.CanonicalizerVersion == diversity.ConceptCanonicalizerVersionV1
			}
		}
	}
	if !foundUnknown {
		t.Fatalf("unknown or server-owned spec fields were lost: %+v", result.Draft.Attempts)
	}
	if result.Draft.Normalization.Usage.NetworkRetries != 1 || result.Draft.Normalization.Usage.ModelCalls != 2 ||
		result.Draft.Normalization.Usage.Tokens != 60 || result.Draft.Normalization.Receipt.SHA256 == "" {
		t.Fatalf("normalization usage/receipt = %+v", result.Draft.Normalization)
	}
	afterAttempts, err := json.Marshal(in.Attempts)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(originalAttempts, afterAttempts) {
		t.Fatal("normalization mutated caller-owned free cards")
	}

	corpusRevision := strings.Repeat("c", 64)
	firstPool, firstBytes, firstSHA, err := FinalizeS5ConceptPoolV1(result.Draft, corpusRevision)
	if err != nil {
		t.Fatalf("finalize: %v", err)
	}
	secondPool, secondBytes, secondSHA, err := FinalizeS5ConceptPoolV1(result.Draft, corpusRevision)
	if err != nil {
		t.Fatalf("second finalize: %v", err)
	}
	if firstPool.CorpusRevision != corpusRevision || secondPool.CorpusRevision != corpusRevision ||
		!bytes.Equal(firstBytes, secondBytes) || firstSHA != secondSHA {
		t.Fatalf("finalization is not deterministic: first=%s second=%s", firstSHA, secondSHA)
	}
	if _, _, _, err := FinalizeS5ConceptPoolV1(result.Draft, "placeholder"); err == nil {
		t.Fatal("placeholder corpus revision was accepted")
	}
}

func TestNormalizeS5ResponseStrictIdentityUnknownFieldAndDuplicateRejection(t *testing.T) {
	attempts := s5TestFreeAttemptsV1(t)
	valid := s5TestRawSpecsV1(attempts)
	encode := func(t *testing.T, specs []s5NormalizedSpecModelV1) string {
		t.Helper()
		value, err := json.Marshal(s5NormalizeModelOutputV1{Specs: specs})
		if err != nil {
			t.Fatal(err)
		}
		return string(value)
	}

	duplicateID := append([]s5NormalizedSpecModelV1(nil), valid...)
	duplicateID[1].ConceptID = duplicateID[0].ConceptID
	unknownID := append([]s5NormalizedSpecModelV1(nil), valid...)
	unknownID[0].ConceptID = "s5-concept-v1-" + strings.Repeat("f", 64)
	duplicateSpec := append([]s5NormalizedSpecModelV1(nil), valid...)
	duplicateSpec[1] = duplicateSpec[0]
	duplicateSpec[1].ConceptID = valid[1].ConceptID

	tests := []struct {
		name string
		raw  string
	}{
		{name: "wrong count", raw: encode(t, valid[:3])},
		{name: "duplicate concept id", raw: encode(t, duplicateID)},
		{name: "unknown concept id", raw: encode(t, unknownID)},
		{name: "duplicate spec", raw: encode(t, duplicateSpec)},
		{name: "unknown root field", raw: `{"specs":[],"extra":true}`},
		{name: "pitch mutation field", raw: strings.Replace(encode(t, valid), `"extraction_confidence"`, `"one_paragraph_pitch":"forged","extraction_confidence"`, 1)},
		{name: "server schema field", raw: strings.Replace(encode(t, valid), `"extraction_confidence"`, `"schema_version":"forged","extraction_confidence"`, 1)},
		{name: "trailing JSON", raw: encode(t, valid) + `{}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := parseS5NormalizedSpecsV1(test.raw, attempts); err == nil {
				t.Fatalf("invalid normalizer response accepted: %s", test.raw)
			}
		})
	}
}

func TestS5ConceptActivitiesFailClosedOnReceiptBudgetRetryAndRawSecret(t *testing.T) {
	t.Run("missing creative receipt", func(t *testing.T) {
		in := s5TestNormalizeInputV1(t)
		in.Attempts[0].Receipt.SHA256 = ""
		client := &s5CapturingLLMV1{}
		activities, _ := s5TestActivitiesV1(client)
		if _, err := activities.NormalizeS5ConceptPoolActivityV1(context.Background(), in); err == nil {
			t.Fatal("missing receipt was accepted")
		}
		if len(client.requests) != 0 {
			t.Fatal("invalid input reached the provider")
		}
	})

	t.Run("aggregate token budget", func(t *testing.T) {
		in := s5TestNormalizeInputV1(t)
		in.Budget.MaxTokens = 50
		raw, err := json.Marshal(s5NormalizeModelOutputV1{Specs: s5TestRawSpecsV1(in.Attempts)})
		if err != nil {
			t.Fatal(err)
		}
		client := &s5CapturingLLMV1{responses: []*llm.Response{s5TestLLMResponseV1(string(raw), 20, 20, 0)}}
		activities, _ := s5TestActivitiesV1(client)
		if _, err := activities.NormalizeS5ConceptPoolActivityV1(context.Background(), in); err == nil {
			t.Fatal("over-budget normalization was accepted")
		}
	})

	t.Run("observed retries exceed allowance", func(t *testing.T) {
		in := s5TestGenerateInputV1(t, 0)
		in.NetworkRetryBudget = 0
		client := &s5CapturingLLMV1{responses: []*llm.Response{
			s5TestLLMResponseV1(`{"cards":[{"one_paragraph_pitch":"one","unresolved_questions":[]},{"one_paragraph_pitch":"two","unresolved_questions":[]}]}`, 1, 1, 1),
		}}
		activities, _ := s5TestActivitiesV1(client)
		if _, err := activities.GenerateS5ConceptAttemptActivityV1(context.Background(), in); err == nil {
			t.Fatal("impossible retry observation was accepted")
		}
	})

	t.Run("missing token usage", func(t *testing.T) {
		in := s5TestGenerateInputV1(t, 0)
		client := &s5CapturingLLMV1{responses: []*llm.Response{
			s5TestLLMResponseV1(`{"cards":[{"one_paragraph_pitch":"one","unresolved_questions":[]},{"one_paragraph_pitch":"two","unresolved_questions":[]}]}`, 0, 0, 0),
		}}
		activities, _ := s5TestActivitiesV1(client)
		if _, err := activities.GenerateS5ConceptAttemptActivityV1(context.Background(), in); err == nil {
			t.Fatal("missing token usage was accepted as zero cost")
		}
	})

	t.Run("raw secret", func(t *testing.T) {
		in := s5TestGenerateInputV1(t, 0)
		in.Params.ProviderConfig.Statement.APIKey = "raw-secret"
		client := &s5CapturingLLMV1{}
		activities, _ := s5TestActivitiesV1(client)
		if _, err := activities.GenerateS5ConceptAttemptActivityV1(context.Background(), in); err == nil {
			t.Fatal("raw provider secret was accepted")
		}
		if len(client.requests) != 0 {
			t.Fatal("raw secret reached the provider")
		}
	})
}

func TestS5ServerIDHelpersAreStableAndSeparated(t *testing.T) {
	batchID := s5TestBatchIDV1(t)
	seen := make(map[string]struct{}, 12)
	for slotIndex := 0; slotIndex < diversityapi.SlotCountV1; slotIndex++ {
		for attemptIndex := 0; attemptIndex < s5AttemptsPerPoolV1; attemptIndex++ {
			for conceptIndex := 0; conceptIndex < s5ConceptsPerAttemptV1; conceptIndex++ {
				first, err := S5ConceptIDV1(batchID, slotIndex, attemptIndex, conceptIndex)
				if err != nil {
					t.Fatal(err)
				}
				second, err := S5ConceptIDV1(batchID, slotIndex, attemptIndex, conceptIndex)
				if err != nil || first != second {
					t.Fatalf("unstable concept identity: first=%q second=%q err=%v", first, second, err)
				}
				if _, duplicate := seen[first]; duplicate {
					t.Fatalf("duplicate server identity %q", first)
				}
				seen[first] = struct{}{}
			}
		}
	}
}

func TestCanonicalS5BriefV1NormalizesWindowsLinesAndRejectsControls(t *testing.T) {
	windows, windowsSHA, err := CanonicalS5BriefV1("  first\r\nsecond\rthird  \r\n")
	if err != nil {
		t.Fatal(err)
	}
	unix, unixSHA, err := CanonicalS5BriefV1("first\nsecond\nthird")
	if err != nil {
		t.Fatal(err)
	}
	if windows != unix || windowsSHA != unixSHA || windows != "first\nsecond\nthird" {
		t.Fatalf("line-ending canonicalization drifted: windows=%q/%s unix=%q/%s", windows, windowsSHA, unix, unixSHA)
	}
	if _, _, err := CanonicalS5BriefV1("valid\x00invalid"); err == nil {
		t.Fatal("forbidden control character was accepted")
	}
}

func s5TestActivitiesV1(client *s5CapturingLLMV1) (*Activities, *captureArtifactStore) {
	store := &captureArtifactStore{}
	return New(&Dependencies{
		LLM: client, LLMProvider: "fixture-provider", LLMModel: "fixture-model",
		LLMBaseURL: "https://fixture.invalid/v1", ArtifactStore: store,
	}), store
}

func s5TestLLMResponseV1(text string, inputTokens, outputTokens, retries int) *llm.Response {
	return &llm.Response{
		Content: []llm.ContentBlock{{Type: "text", Text: text}},
		Model:   "fixture-model-r1", ModelObserved: true,
		Usage:          llm.Usage{InputTokens: inputTokens, OutputTokens: outputTokens},
		NetworkRetries: retries,
	}
}

func s5TestBatchIDV1(t *testing.T) string {
	t.Helper()
	batchID, _, err := diversityapi.BatchIDForIdempotencyKey("s5-test-scope", "s5-test-key")
	if err != nil {
		t.Fatal(err)
	}
	return batchID
}

func s5TestBudgetV1() diversity.ConceptBudgetV1 {
	return diversity.ConceptBudgetV1{
		MaxCreativeAttempts: 2, MaxConcepts: 4, MaxModelCalls: 5,
		MaxNetworkRetries: 2, MaxTokens: 1000, MaxWallMilliseconds: 60000,
	}
}

func s5TestParamsV1() domain.ProblemGenParams {
	params := domain.DefaultProblemGenParams()
	params.Tags = []string{"graphs", "data-structures"}
	params.ContestStyle = "icpc"
	params.CustomPrompt = "Prefer a non-standard invariant."
	params.Locale = "en"
	params.ProviderConfig = &domain.ProviderRuntimeConfig{Statement: &domain.LLMRuntimeConfig{
		Model: "fixture-model", APIKeyRef: "env:ALGOFORGE_S5_TEST_KEY",
		BaseURL: "https://fixture.invalid/v1", Provider: "fixture-provider", Protocol: "openai-chat",
	}}
	return params
}

func s5TestGenerateInputV1(t *testing.T, attemptIndex int) GenerateS5ConceptAttemptInputV1 {
	t.Helper()
	batchID := s5TestBatchIDV1(t)
	slotID, err := S5SlotIDV1(batchID, 0)
	if err != nil {
		t.Fatal(err)
	}
	attemptID, err := S5LogicalAttemptIDV1(batchID, 0, attemptIndex)
	if err != nil {
		t.Fatal(err)
	}
	brief := `{"schema_version":"fixture","goal":"distinct concepts"}`
	return GenerateS5ConceptAttemptInputV1{
		PayloadVersion: GenerateS5ConceptAttemptPayloadVersionV1,
		BatchID:        batchID, SlotIndex: 0, SlotID: slotID,
		AttemptIndex: attemptIndex, LogicalAttemptID: attemptID,
		CanonicalBrief: brief, BriefSHA256: diversity.SHA256Hex([]byte(brief)),
		Params: s5TestParamsV1(), Budget: s5TestBudgetV1(), NetworkRetryBudget: 1,
	}
}

func s5TestNormalizeInputV1(t *testing.T) NormalizeS5ConceptPoolInputV1 {
	t.Helper()
	generate := s5TestGenerateInputV1(t, 0)
	attempts := s5TestFreeAttemptsV1(t)
	attempts[0], attempts[1] = attempts[1], attempts[0]
	return NormalizeS5ConceptPoolInputV1{
		PayloadVersion: NormalizeS5ConceptPoolPayloadVersionV1,
		BatchID:        generate.BatchID, SlotIndex: generate.SlotIndex, SlotID: generate.SlotID,
		BriefSHA256: generate.BriefSHA256, Params: generate.Params, Budget: generate.Budget,
		Attempts: attempts, NormalizerVersion: S5ConceptNormalizerVersionV1, NetworkRetryBudget: 1,
	}
}

func s5TestFreeAttemptsV1(t *testing.T) []diversity.ConceptAttemptV1 {
	t.Helper()
	batchID := s5TestBatchIDV1(t)
	attempts := make([]diversity.ConceptAttemptV1, s5AttemptsPerPoolV1)
	for attemptIndex := range attempts {
		attemptID, err := S5LogicalAttemptIDV1(batchID, 0, attemptIndex)
		if err != nil {
			t.Fatal(err)
		}
		attempts[attemptIndex] = diversity.ConceptAttemptV1{
			AttemptIndex: attemptIndex, LogicalAttemptID: attemptID,
			Receipt: diversity.ArtifactRefV1{
				SHA256: strings.Repeat(string(rune('a'+attemptIndex)), 64),
				URI:    "cas://sha256/" + strings.Repeat(string(rune('a'+attemptIndex)), 64),
			},
			Usage:    diversity.ConceptAttemptUsageV1{ModelCalls: 1, Tokens: 10, WallMilliseconds: 1},
			Concepts: make([]diversity.ConceptCardV1, s5ConceptsPerAttemptV1),
		}
		for conceptIndex := 0; conceptIndex < s5ConceptsPerAttemptV1; conceptIndex++ {
			conceptID, err := S5ConceptIDV1(batchID, 0, attemptIndex, conceptIndex)
			if err != nil {
				t.Fatal(err)
			}
			ordinal := attemptIndex*s5ConceptsPerAttemptV1 + conceptIndex
			attempts[attemptIndex].Concepts[conceptIndex] = diversity.ConceptCardV1{
				ConceptIndex: conceptIndex, ConceptID: conceptID,
				OneParagraphPitch:   fmt.Sprintf("Distinct free concept pitch %d", ordinal),
				UnresolvedQuestions: []string{"boundary", fmt.Sprintf("tie-%d", ordinal)},
			}
			sortStringsV1(attempts[attemptIndex].Concepts[conceptIndex].UnresolvedQuestions)
		}
	}
	return attempts
}

func s5TestRawSpecsV1(attempts []diversity.ConceptAttemptV1) []s5NormalizedSpecModelV1 {
	result := make([]s5NormalizedSpecModelV1, 0, s5ConceptsPerPoolV1)
	ordinal := 0
	for _, attempt := range attempts {
		for _, card := range attempt.Concepts {
			family := fmt.Sprintf("family-%d", ordinal)
			result = append(result, s5NormalizedSpecModelV1{
				ConceptID: card.ConceptID, ExtractionConfidence: 0.8,
				QualityTier: diversity.QualityTierViable,
				ProblemMode: "static", InputObject: family, Topology: "line",
				OperationModel: "offline", Objective: "count",
				StateDimensions:       []string{"position", family},
				TransitionOrInvariant: family + " invariant",
				SolutionOperatorSeq:   []string{"scan", "aggregate-" + family},
				OutputForm:            "integer", ComplexityClass: "linear",
				ConstraintRegime:      "n up to 200000",
				WrongSolutionFamilies: []string{"overflow", "off-by-one-" + family},
			})
			ordinal++
		}
	}
	return result
}

func s5TestUnknownRawSpecV1(conceptID string) s5NormalizedSpecModelV1 {
	return s5NormalizedSpecModelV1{
		ConceptID: conceptID, ExtractionConfidence: 0.2,
		QualityTier: diversity.QualityTierUncertain,
		ProblemMode: diversity.UnknownValue, InputObject: diversity.UnknownValue,
		Topology: diversity.UnknownValue, OperationModel: diversity.UnknownValue,
		Objective: diversity.UnknownValue, StateDimensions: []string{diversity.UnknownValue},
		TransitionOrInvariant: diversity.UnknownValue,
		SolutionOperatorSeq:   []string{diversity.UnknownValue},
		OutputForm:            diversity.UnknownValue, ComplexityClass: diversity.UnknownValue,
		ConstraintRegime:      diversity.UnknownValue,
		WrongSolutionFamilies: []string{diversity.UnknownValue},
	}
}

func sortStringsV1(values []string) {
	for left := 0; left < len(values); left++ {
		for right := left + 1; right < len(values); right++ {
			if values[right] < values[left] {
				values[left], values[right] = values[right], values[left]
			}
		}
	}
}
