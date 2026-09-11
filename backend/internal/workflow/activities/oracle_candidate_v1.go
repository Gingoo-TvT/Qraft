package activities

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/llm"
	speccontract "github.com/Gingoo-TvT/Qraft/backend/internal/spec"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
)

const (
	OracleCandidatePayloadVersionV1 = 1
	OracleCandidateSchemaV1         = "algoforge.oracle-candidate.v1"
	OraclePromotionSchemaV1         = "algoforge.oracle-promotion.v1"
	maxOracleCandidateSourceBytesV1 = 2 << 20
)

// GenerateOracleCandidateInputV1 is deliberately narrow. In particular it has
// no AuthoringPlan, rendered statement, main solution, test data, or hidden
// regression fields. V can therefore receive only the accepted SemanticSpec.
type GenerateOracleCandidateInputV1 struct {
	PayloadVersion             int                      `json:"payload_version"`
	SemanticSpecArtifact       ArtifactRef              `json:"semantic_spec_artifact"`
	ExpectedSemanticSpecSHA256 string                   `json:"expected_semantic_spec_sha256"`
	Language                   string                   `json:"language"`
	VerificationRuntime        *domain.LLMRuntimeConfig `json:"verification_runtime,omitempty"`
}

type OracleCandidateSourceBindingV1 struct {
	SchemaVersion              string `json:"schema_version"`
	SemanticSpecSHA256         string `json:"semantic_spec_sha256"`
	SemanticSpecArtifactSHA256 string `json:"semantic_spec_artifact_sha256"`
	ProviderResponseSHA256     string `json:"provider_response_sha256"`
	ProviderRequestSHA256      string `json:"provider_request_sha256"`
	PromptSHA256               string `json:"prompt_sha256"`
	CandidateSourceSHA256      string `json:"candidate_source_sha256"`
}

type GenerateOracleCandidateResultV1 struct {
	PayloadVersion           int                            `json:"payload_version"`
	SchemaVersion            string                         `json:"schema_version"`
	InputSHA256              string                         `json:"input_sha256"`
	Language                 string                         `json:"language"`
	CandidateSourceSHA256    string                         `json:"candidate_source_sha256"`
	CandidateSourceArtifact  *ArtifactRef                   `json:"candidate_source_artifact"`
	ProviderResponseArtifact *ArtifactRef                   `json:"provider_response_artifact"`
	SourceBinding            OracleCandidateSourceBindingV1 `json:"source_binding"`
	SourceArtifacts          []*ArtifactRef                 `json:"source_artifacts"`
}

type oracleCandidateModelOutputV1 struct {
	SourceCode string `json:"source_code"`
	Language   string `json:"language"`
}

// GenerateOracleCandidateActivityV1 stores a candidate, but never promotes it.
func (a *Activities) GenerateOracleCandidateActivityV1(ctx context.Context, in GenerateOracleCandidateInputV1) (*GenerateOracleCandidateResultV1, error) {
	canonicalInput, specValue, err := validateOracleCandidateInputV1(ctx, a, in)
	if err != nil {
		return nil, temporal.NewNonRetryableApplicationError(err.Error(), "InvalidParameterError", err)
	}
	temperature := 0.0
	request := &llm.Request{
		MaxTokens:   16000,
		System:      oracleCandidateSystemPromptV1,
		Temperature: &temperature,
		Messages:    []llm.Message{{Role: "user", Content: buildOracleCandidatePromptV1(specValue, in.Language)}},
	}
	applyLLMRuntime(request, in.VerificationRuntime)
	activity.RecordHeartbeat(ctx, "generating independent oracle candidate from SemanticSpec")
	stopHB := heartbeatWhile(ctx, "generating independent oracle candidate", 15*time.Second)
	response, providerArtifact, err := a.completeLLMWithProvenance(ctx, "oracle_candidate_v1", request, 2)
	stopHB()
	if err != nil {
		return nil, wrapRequiredProviderEffectError("llm call for oracle candidate", err)
	}
	if response == nil || providerArtifact == nil || providerArtifact.LLMCallReceipt == nil {
		return nil, fmt.Errorf("oracle candidate provider response lacks immutable call identity")
	}
	if response.StopReason == "max_tokens" {
		return nil, temporal.NewNonRetryableApplicationError("oracle candidate response was truncated", "TruncatedLLMResponse", nil)
	}
	parsed, err := parseOracleCandidateResponseV1(response.Text())
	if err != nil {
		return nil, temporal.NewNonRetryableApplicationError("parsing oracle candidate: "+err.Error(), "InvalidParameterError", err)
	}
	if parsed.Language != in.Language {
		return nil, temporal.NewNonRetryableApplicationError(fmt.Sprintf("oracle language %q does not match requested language %q", parsed.Language, in.Language), "InvalidParameterError", nil)
	}

	sourceBytes := []byte(parsed.SourceCode)
	metadata := artifactMetadataFromActivity(ctx)
	metadata.ArtifactType = "qg04_oracle_candidate_source"
	metadata.SourceType = "verification_model_candidate"
	metadata.RetentionClass = "workflow_cas_unreviewed"
	metadata.ProvenanceMetadata, err = json.Marshal(map[string]string{
		"semantic_spec_sha256":          in.ExpectedSemanticSpecSHA256,
		"semantic_spec_artifact_sha256": in.SemanticSpecArtifact.SHA256,
		"provider_response_sha256":      providerArtifact.SHA256,
		"provider_request_sha256":       providerArtifact.LLMCallReceipt.RequestSHA256,
		"prompt_sha256":                 providerArtifact.LLMCallReceipt.PromptHash,
		"candidate_source_sha256":       sha256Hex(sourceBytes),
	})
	if err != nil {
		return nil, fmt.Errorf("encode oracle candidate provenance: %w", err)
	}
	sourceArtifact, err := a.putArtifactWithMetadata(ctx, sourceBytes, oracleSourceContentTypeV1(in.Language), metadata)
	if err != nil {
		return nil, fmt.Errorf("store oracle candidate source: %w", err)
	}
	if sourceArtifact.SHA256 != sha256Hex(sourceBytes) {
		return nil, fmt.Errorf("oracle candidate source CAS identity mismatch")
	}
	binding := OracleCandidateSourceBindingV1{
		SchemaVersion:              OracleCandidateSchemaV1,
		SemanticSpecSHA256:         in.ExpectedSemanticSpecSHA256,
		SemanticSpecArtifactSHA256: in.SemanticSpecArtifact.SHA256,
		ProviderResponseSHA256:     providerArtifact.SHA256,
		ProviderRequestSHA256:      providerArtifact.LLMCallReceipt.RequestSHA256,
		PromptSHA256:               providerArtifact.LLMCallReceipt.PromptHash,
		CandidateSourceSHA256:      sourceArtifact.SHA256,
	}
	return &GenerateOracleCandidateResultV1{
		PayloadVersion:           OracleCandidatePayloadVersionV1,
		SchemaVersion:            OracleCandidateSchemaV1,
		InputSHA256:              sha256Hex(canonicalInput),
		Language:                 in.Language,
		CandidateSourceSHA256:    sourceArtifact.SHA256,
		CandidateSourceArtifact:  sourceArtifact,
		ProviderResponseArtifact: providerArtifact,
		SourceBinding:            binding,
		SourceArtifacts:          []*ArtifactRef{&in.SemanticSpecArtifact, providerArtifact, sourceArtifact},
	}, nil
}

func validateOracleCandidateInputV1(ctx context.Context, a *Activities, in GenerateOracleCandidateInputV1) ([]byte, domain.SemanticSpecV1, error) {
	if in.PayloadVersion != OracleCandidatePayloadVersionV1 {
		return nil, domain.SemanticSpecV1{}, fmt.Errorf("unsupported oracle-candidate payload version %d", in.PayloadVersion)
	}
	if !isManifestSHA256(in.ExpectedSemanticSpecSHA256) {
		return nil, domain.SemanticSpecV1{}, fmt.Errorf("expected SemanticSpec SHA-256 is invalid")
	}
	if err := in.SemanticSpecArtifact.Validate(in.SemanticSpecArtifact.Bucket); err != nil {
		return nil, domain.SemanticSpecV1{}, fmt.Errorf("invalid SemanticSpec artifact: %w", err)
	}
	if in.SemanticSpecArtifact.ContentType != "application/json" || in.SemanticSpecArtifact.SHA256 != in.ExpectedSemanticSpecSHA256 {
		return nil, domain.SemanticSpecV1{}, fmt.Errorf("SemanticSpec artifact identity or content type mismatch")
	}
	language := strings.TrimSpace(in.Language)
	if language == "" || language != in.Language || len(language) > 32 || !utf8.ValidString(language) {
		return nil, domain.SemanticSpecV1{}, fmt.Errorf("oracle language is empty or non-canonical")
	}
	if in.VerificationRuntime != nil {
		copyRuntime := *in.VerificationRuntime
		if err := copyRuntime.Validate("verification_runtime"); err != nil {
			return nil, domain.SemanticSpecV1{}, err
		}
	}
	specBytes, err := a.getArtifact(ctx, &in.SemanticSpecArtifact)
	if err != nil {
		return nil, domain.SemanticSpecV1{}, fmt.Errorf("read SemanticSpec artifact: %w", err)
	}
	if sha256Hex(specBytes) != in.ExpectedSemanticSpecSHA256 {
		return nil, domain.SemanticSpecV1{}, fmt.Errorf("SemanticSpec artifact bytes do not match expected SHA-256")
	}
	if err := validateExactJSONKeysV1(string(specBytes), reflect.TypeOf(domain.SemanticSpecV1{})); err != nil {
		return nil, domain.SemanticSpecV1{}, fmt.Errorf("strict SemanticSpec decode: %w", err)
	}
	decoder := json.NewDecoder(strings.NewReader(string(specBytes)))
	decoder.DisallowUnknownFields()
	var specValue domain.SemanticSpecV1
	if err := decoder.Decode(&specValue); err != nil {
		return nil, domain.SemanticSpecV1{}, fmt.Errorf("decode SemanticSpec: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return nil, domain.SemanticSpecV1{}, err
	}
	canonicalSpec, err := json.Marshal(specValue)
	if err != nil {
		return nil, domain.SemanticSpecV1{}, err
	}
	if string(canonicalSpec) != string(specBytes) || speccontract.SemanticSpecSHA256V1(specValue) != in.ExpectedSemanticSpecSHA256 {
		return nil, domain.SemanticSpecV1{}, fmt.Errorf("SemanticSpec artifact is not the canonical accepted object")
	}
	canonicalInput, err := json.Marshal(in)
	if err != nil {
		return nil, domain.SemanticSpecV1{}, err
	}
	return canonicalInput, specValue, nil
}

func parseOracleCandidateResponseV1(text string) (*oracleCandidateModelOutputV1, error) {
	trimmed := strings.TrimSpace(text)
	if err := validateExactJSONKeysV1(trimmed, reflect.TypeOf(oracleCandidateModelOutputV1{})); err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(strings.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	var output oracleCandidateModelOutputV1
	if err := decoder.Decode(&output); err != nil {
		return nil, err
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return nil, err
	}
	if output.SourceCode == "" || len(output.SourceCode) > maxOracleCandidateSourceBytesV1 || !utf8.ValidString(output.SourceCode) || strings.IndexByte(output.SourceCode, 0) >= 0 {
		return nil, fmt.Errorf("oracle source is empty, oversized, or invalid UTF-8")
	}
	if output.Language == "" || output.Language != strings.TrimSpace(output.Language) {
		return nil, fmt.Errorf("oracle language is empty or non-canonical")
	}
	return &output, nil
}

func buildOracleCandidatePromptV1(specValue domain.SemanticSpecV1, language string) string {
	encoded, err := json.Marshal(struct {
		Language     string                `json:"language"`
		SemanticSpec domain.SemanticSpecV1 `json:"semantic_spec"`
	}{Language: language, SemanticSpec: specValue})
	if err != nil {
		panic(fmt.Sprintf("marshal oracle candidate prompt: %v", err))
	}
	return string(encoded)
}

const oracleCandidateSystemPromptV1 = `You are the independent V oracle stage for a competitive-programming system.
You receive only a fact-only SemanticSpec and a target language. Implement the simplest obviously-correct executable oracle for bounded differential testing.
Return exactly one compact JSON object with exactly these fields and no Markdown or commentary:
{"source_code":"full source","language":"requested language"}
Do not infer or request an intended solution, authoring plan, rendered statement, model hint, hidden seed, mutant, or main-solution source.`

func oracleSourceContentTypeV1(language string) string {
	switch language {
	case "c":
		return "text/x-csrc"
	case "cpp", "c++", "cc":
		return "text/x-c++src"
	case "python", "python3", "py":
		return "text/x-python"
	case "java":
		return "text/x-java-source"
	case "go", "golang":
		return "text/x-go"
	default:
		return "text/plain"
	}
}

type OracleDifferentialMismatchV1 struct {
	CaseID             string `json:"case_id"`
	MainOutputSHA256   string `json:"main_output_sha256"`
	OracleOutputSHA256 string `json:"oracle_output_sha256"`
}

type ValidateOraclePromotionInputV1 struct {
	PayloadVersion             int                             `json:"payload_version"`
	ExpectedSemanticSpecSHA256 string                          `json:"expected_semantic_spec_sha256"`
	MainProviderArtifact       ArtifactRef                     `json:"main_provider_artifact"`
	Candidate                  GenerateOracleCandidateResultV1 `json:"candidate"`
	CandidateCompiled          bool                            `json:"candidate_compiled"`
	DifferentialCaseIDs        []string                        `json:"differential_case_ids"`
	Mismatches                 []OracleDifferentialMismatchV1  `json:"mismatches"`
}

type OraclePromotionReceiptV1 struct {
	SchemaVersion         string                    `json:"schema_version"`
	SemanticSpecSHA256    string                    `json:"semantic_spec_sha256"`
	CandidateSourceSHA256 string                    `json:"candidate_source_sha256"`
	ProviderRequestSHA256 string                    `json:"provider_request_sha256"`
	DifferentialCaseIDs   []string                  `json:"differential_case_ids"`
	OracleIndependence    OracleIndependenceReceipt `json:"oracle_independence"`
	Promoted              bool                      `json:"promoted"`
}

// ValidateAndPromoteOracleCandidateV1 is the D-side promotion gate.
func ValidateAndPromoteOracleCandidateV1(in ValidateOraclePromotionInputV1) (*OraclePromotionReceiptV1, error) {
	fail := func(format string, args ...interface{}) (*OraclePromotionReceiptV1, error) {
		return nil, temporal.NewNonRetryableApplicationError(fmt.Sprintf(format, args...), "QualityNotMet", nil)
	}
	if in.PayloadVersion != OracleCandidatePayloadVersionV1 || !isManifestSHA256(in.ExpectedSemanticSpecSHA256) {
		return fail("oracle promotion input contract is invalid")
	}
	if err := validateOracleCandidateResultV1(in.Candidate, in.ExpectedSemanticSpecSHA256); err != nil {
		return fail("oracle candidate binding is invalid: %v", err)
	}
	if !in.CandidateCompiled {
		return fail("oracle candidate did not compile")
	}
	if len(in.DifferentialCaseIDs) == 0 {
		return fail("oracle promotion compared no test cases")
	}
	caseIDs := append([]string(nil), in.DifferentialCaseIDs...)
	sort.Strings(caseIDs)
	for i, caseID := range caseIDs {
		if caseID == "" || caseID != strings.TrimSpace(caseID) || (i > 0 && caseIDs[i-1] == caseID) {
			return fail("oracle differential case ids are empty, non-canonical, or duplicated")
		}
	}
	if len(in.Mismatches) != 0 {
		return fail("oracle differential mismatch on case %q", in.Mismatches[0].CaseID)
	}
	independence := assessOracleIndependence(&in.MainProviderArtifact, in.Candidate.ProviderResponseArtifact)
	if independence == nil || independence.Status != OracleIdentityCrossModelIndependent {
		reason := "missing_independence_receipt"
		if independence != nil {
			reason = independence.Reason
		}
		return fail("oracle candidate is correlated: %s", reason)
	}
	return &OraclePromotionReceiptV1{
		SchemaVersion: OraclePromotionSchemaV1, SemanticSpecSHA256: in.ExpectedSemanticSpecSHA256,
		CandidateSourceSHA256: in.Candidate.CandidateSourceSHA256,
		ProviderRequestSHA256: in.Candidate.SourceBinding.ProviderRequestSHA256,
		DifferentialCaseIDs:   caseIDs, OracleIndependence: *independence, Promoted: true,
	}, nil
}

func validateOracleCandidateResultV1(result GenerateOracleCandidateResultV1, expectedSpecSHA string) error {
	if result.PayloadVersion != OracleCandidatePayloadVersionV1 || result.SchemaVersion != OracleCandidateSchemaV1 {
		return fmt.Errorf("unsupported candidate result version")
	}
	if !isManifestSHA256(result.InputSHA256) || !isManifestSHA256(result.CandidateSourceSHA256) {
		return fmt.Errorf("candidate hashes are invalid")
	}
	if result.CandidateSourceArtifact == nil || result.ProviderResponseArtifact == nil || result.ProviderResponseArtifact.LLMCallReceipt == nil {
		return fmt.Errorf("candidate artifacts or provider identity are missing")
	}
	if result.CandidateSourceArtifact.SHA256 != result.CandidateSourceSHA256 || result.SourceBinding.SchemaVersion != OracleCandidateSchemaV1 ||
		result.SourceBinding.SemanticSpecSHA256 != expectedSpecSHA || result.SourceBinding.CandidateSourceSHA256 != result.CandidateSourceSHA256 ||
		result.SourceBinding.ProviderResponseSHA256 != result.ProviderResponseArtifact.SHA256 ||
		result.SourceBinding.ProviderRequestSHA256 != result.ProviderResponseArtifact.LLMCallReceipt.RequestSHA256 ||
		result.SourceBinding.PromptSHA256 != result.ProviderResponseArtifact.LLMCallReceipt.PromptHash {
		return fmt.Errorf("candidate source binding mismatch")
	}
	if len(result.SourceArtifacts) != 3 || result.SourceArtifacts[0] == nil || result.SourceArtifacts[1] == nil || result.SourceArtifacts[2] == nil ||
		result.SourceArtifacts[0].SHA256 != result.SourceBinding.SemanticSpecArtifactSHA256 ||
		result.SourceArtifacts[1].SHA256 != result.ProviderResponseArtifact.SHA256 ||
		result.SourceArtifacts[2].SHA256 != result.CandidateSourceArtifact.SHA256 {
		return fmt.Errorf("candidate ancestry is incomplete")
	}
	return nil
}
