package activities

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	speccontract "github.com/Gingoo-TvT/Qraft/backend/internal/spec"
	"go.temporal.io/sdk/testsuite"
)

type oracleMemoryArtifactStore struct {
	objects map[string][]byte
}

func newOracleMemoryArtifactStore() *oracleMemoryArtifactStore {
	return &oracleMemoryArtifactStore{objects: make(map[string][]byte)}
}

func (s *oracleMemoryArtifactStore) Put(_ context.Context, data []byte, contentType string, metadata ArtifactMetadata) (ArtifactRef, error) {
	digest := sha256.Sum256(data)
	digestHex := hex.EncodeToString(digest[:])
	s.objects[digestHex] = append([]byte(nil), data...)
	return ArtifactRef{
		SchemaVersion: ArtifactRefSchemaVersion, PayloadVersion: ActivityPayloadVersion,
		Bucket: "fixture", Key: artifactKey(digestHex), SHA256: digestHex, SizeBytes: int64(len(data)), ContentType: contentType,
		Producer: nonemptyOracleFixture(metadata.Producer, "fixture"), Provider: nonemptyOracleFixture(metadata.Provider, "fixture-provider"),
		Model: nonemptyOracleFixture(metadata.Model, "fixture-model"), ModelRevision: nonemptyOracleFixture(metadata.ModelRevision, "fixture-revision"),
		WorkflowID: nonemptyOracleFixture(metadata.WorkflowID, "fixture-workflow"),
	}, nil
}

func (s *oracleMemoryArtifactStore) Get(_ context.Context, ref ArtifactRef) ([]byte, error) {
	return append([]byte(nil), s.objects[ref.SHA256]...), nil
}

func nonemptyOracleFixture(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func TestOracleCandidateInputAndPromptExposeOnlySemanticSpec(t *testing.T) {
	typeOfInput := reflect.TypeOf(GenerateOracleCandidateInputV1{})
	got := make([]string, 0, typeOfInput.NumField())
	for i := 0; i < typeOfInput.NumField(); i++ {
		got = append(got, strings.Split(typeOfInput.Field(i).Tag.Get("json"), ",")[0])
	}
	want := []string{"payload_version", "semantic_spec_artifact", "expected_semantic_spec_sha256", "language", "verification_runtime"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("oracle activity input surface changed: got=%v want=%v", got, want)
	}
	specValue := validOracleSemanticSpecV1(t)
	prompt := buildOracleCandidatePromptV1(specValue, "cpp")
	for _, forbidden := range []string{"authoring_plan", "intended_solution", "core_idea", "failure_modes", "one_line_hint", "main_solution", "hidden_seed", "mutant"} {
		if strings.Contains(strings.ToLower(prompt), forbidden) {
			t.Fatalf("V prompt leaked forbidden authoring field %q: %s", forbidden, prompt)
		}
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal([]byte(prompt), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope) != 2 || envelope["language"] == nil || envelope["semantic_spec"] == nil {
		t.Fatalf("unexpected V prompt envelope: %v", envelope)
	}
}

func TestParseOracleCandidateResponseV1RejectsUnknownAndLanguageFallback(t *testing.T) {
	if _, err := parseOracleCandidateResponseV1(`{"source_code":"int main(){}","language":"cpp","hint":"x"}`); err == nil {
		t.Fatal("unknown oracle response field was accepted")
	}
	if _, err := parseOracleCandidateResponseV1(`{"source_code":"int main(){}","language":""}`); err == nil {
		t.Fatal("empty oracle language fallback was accepted")
	}
}

func TestGenerateOracleCandidateActivityV1BindsExactSpecPromptAndCAS(t *testing.T) {
	store := newOracleMemoryArtifactStore()
	specValue := validOracleSemanticSpecV1(t)
	specBytes, err := json.Marshal(specValue)
	if err != nil {
		t.Fatal(err)
	}
	specRef, err := store.Put(context.Background(), specBytes, "application/json", ArtifactMetadata{})
	if err != nil {
		t.Fatal(err)
	}
	provider := &capturingAuthoringLLM{response: authoringLLMResponse(`{"source_code":"#include <iostream>\nint main(){return 0;}","language":"cpp"}`)}
	acts := &Activities{deps: &Dependencies{
		LLM: provider, LLMProvider: "fixture-provider", LLMModel: "fallback-model",
		LLMBaseURL: "https://fallback.invalid/v1", ProvenanceRecorder: &captureProvenanceRecorder{},
	}, artifacts: store}
	in := GenerateOracleCandidateInputV1{
		PayloadVersion: OracleCandidatePayloadVersionV1, SemanticSpecArtifact: specRef,
		ExpectedSemanticSpecSHA256: specRef.SHA256, Language: "cpp",
		VerificationRuntime: &domain.LLMRuntimeConfig{Model: "v-requested", Provider: "openai-compatible", BaseURL: "https://v.invalid/v1", Protocol: "openai-chat", APIKeyRef: "env:V_TEST_KEY"},
	}
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(acts.GenerateOracleCandidateActivityV1)
	encoded, err := env.ExecuteActivity(acts.GenerateOracleCandidateActivityV1, in)
	if err != nil {
		t.Fatal(err)
	}
	var result GenerateOracleCandidateResultV1
	if err := encoded.Get(&result); err != nil {
		t.Fatal(err)
	}
	if provider.request == nil || provider.request.Model != "v-requested" || provider.request.Runtime == nil || provider.request.Runtime.BaseURL != "https://v.invalid/v1" {
		t.Fatalf("V route was not applied: %+v", provider.request)
	}
	if result.SourceBinding.SemanticSpecSHA256 != specRef.SHA256 || result.CandidateSourceArtifact == nil || result.ProviderResponseArtifact == nil || result.ProviderResponseArtifact.LLMCallReceipt == nil {
		t.Fatalf("candidate bindings are incomplete: %+v", result)
	}
	if result.SourceBinding.ProviderRequestSHA256 != result.ProviderResponseArtifact.LLMCallReceipt.RequestSHA256 || result.SourceBinding.PromptSHA256 != result.ProviderResponseArtifact.LLMCallReceipt.PromptHash {
		t.Fatalf("provider request binding mismatch: %+v", result.SourceBinding)
	}
}

func TestValidateAndPromoteOracleCandidateV1FailsClosedAndSortsCases(t *testing.T) {
	base := validOraclePromotionInputV1(t)
	receipt, err := ValidateAndPromoteOracleCandidateV1(base)
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Promoted || !reflect.DeepEqual(receipt.DifferentialCaseIDs, []string{"case-a", "case-b"}) {
		t.Fatalf("unexpected promotion receipt: %+v", receipt)
	}

	tests := map[string]func(*ValidateOraclePromotionInputV1){
		"single mismatch": func(in *ValidateOraclePromotionInputV1) {
			in.Mismatches = []OracleDifferentialMismatchV1{{CaseID: "case-a", MainOutputSHA256: strings.Repeat("a", 64), OracleOutputSHA256: strings.Repeat("b", 64)}}
		},
		"correlated": func(in *ValidateOraclePromotionInputV1) {
			in.Candidate.ProviderResponseArtifact.LLMCallReceipt.ReturnedModel = in.MainProviderArtifact.LLMCallReceipt.ReturnedModel
		},
		"missing identity": func(in *ValidateOraclePromotionInputV1) {
			in.Candidate.ProviderResponseArtifact.LLMCallReceipt.EndpointID = ""
		},
		"not compiled": func(in *ValidateOraclePromotionInputV1) { in.CandidateCompiled = false },
		"tampered source binding": func(in *ValidateOraclePromotionInputV1) {
			in.Candidate.SourceBinding.CandidateSourceSHA256 = strings.Repeat("f", 64)
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			input := validOraclePromotionInputV1(t)
			mutate(&input)
			if got, err := ValidateAndPromoteOracleCandidateV1(input); err == nil || got != nil || !strings.Contains(err.Error(), "QualityNotMet") {
				t.Fatalf("fail-closed promotion returned got=%+v err=%v", got, err)
			}
		})
	}
}

func validOracleSemanticSpecV1(t *testing.T) domain.SemanticSpecV1 {
	t.Helper()
	model := validAuthoringModelOutputV1()
	specValue := *model.SemanticSpec
	specValue.SchemaVersion = domain.SemanticSpecSchemaV1
	specValue.BriefSHA256 = strings.Repeat("a", 64)
	return specValue
}

func validOraclePromotionInputV1(t *testing.T) ValidateOraclePromotionInputV1 {
	t.Helper()
	specSHA := strings.Repeat("a", 64)
	sourceSHA := strings.Repeat("b", 64)
	responseSHA := strings.Repeat("c", 64)
	specArtifactSHA := strings.Repeat("d", 64)
	requestSHA := strings.Repeat("e", 64)
	promptSHA := strings.Repeat("f", 64)
	candidateSource := oracleFixtureArtifact(sourceSHA, nil)
	providerArtifact := oracleFixtureArtifact(responseSHA, &LLMCallReceipt{
		SchemaVersion: 1, RequestedModel: "v-requested", ReturnedModel: "v-returned", Provider: "v-provider",
		EndpointID: "v-endpoint", PromptHash: promptSHA, RequestSHA256: requestSHA,
	})
	specArtifact := oracleFixtureArtifact(specArtifactSHA, nil)
	mainArtifact := oracleFixtureArtifact(strings.Repeat("1", 64), &LLMCallReceipt{
		SchemaVersion: 1, RequestedModel: "g-requested", ReturnedModel: "g-returned", Provider: "g-provider",
		EndpointID: "g-endpoint", PromptHash: strings.Repeat("2", 64), RequestSHA256: strings.Repeat("3", 64),
	})
	return ValidateOraclePromotionInputV1{
		PayloadVersion: OracleCandidatePayloadVersionV1, ExpectedSemanticSpecSHA256: specSHA,
		MainProviderArtifact: mainArtifact, CandidateCompiled: true, DifferentialCaseIDs: []string{"case-b", "case-a"},
		Candidate: GenerateOracleCandidateResultV1{
			PayloadVersion: OracleCandidatePayloadVersionV1, SchemaVersion: OracleCandidateSchemaV1,
			InputSHA256: strings.Repeat("4", 64), Language: "cpp", CandidateSourceSHA256: sourceSHA,
			CandidateSourceArtifact: &candidateSource, ProviderResponseArtifact: &providerArtifact,
			SourceBinding: OracleCandidateSourceBindingV1{
				SchemaVersion: OracleCandidateSchemaV1, SemanticSpecSHA256: specSHA, SemanticSpecArtifactSHA256: specArtifactSHA,
				ProviderResponseSHA256: responseSHA, ProviderRequestSHA256: requestSHA, PromptSHA256: promptSHA, CandidateSourceSHA256: sourceSHA,
			},
			SourceArtifacts: []*ArtifactRef{&specArtifact, &providerArtifact, &candidateSource},
		},
	}
}

func oracleFixtureArtifact(digest string, receipt *LLMCallReceipt) ArtifactRef {
	return ArtifactRef{
		SchemaVersion: ArtifactRefSchemaVersion, PayloadVersion: ActivityPayloadVersion, Bucket: "fixture", Key: artifactKey(digest),
		SHA256: digest, SizeBytes: 1, ContentType: "application/json", Producer: "fixture", Provider: "fixture-provider",
		Model: "fixture-model", ModelRevision: "fixture-revision", WorkflowID: "fixture-workflow", LLMCallReceipt: receipt,
	}
}

func TestOracleSemanticSpecFixtureIsCanonical(t *testing.T) {
	specValue := validOracleSemanticSpecV1(t)
	encoded, err := json.Marshal(specValue)
	if err != nil {
		t.Fatal(err)
	}
	if speccontract.SemanticSpecSHA256V1(specValue) != sha256Hex(encoded) {
		t.Fatal("fixture SemanticSpec hash is not canonical")
	}
}
