package activities

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/llm"
	speccontract "github.com/Gingoo-TvT/Qraft/backend/internal/spec"
	"go.temporal.io/sdk/activity"
)

const (
	GenerateAuthoringPlanPayloadVersion   = 1
	AuthoringPlanBundleSchemaV1           = "algoforge.authoring-bundle.v1"
	AuthoringPlanDecisionAccepted         = "accepted"
	AuthoringPlanDecisionRejected         = "rejected"
	AuthoringPlanGateReasonModelRejected  = "model_rejected"
	AuthoringPlanGateReasonSpecLintFailed = "spec_lint_failed"
	authoringPlanDerivationRuleV1         = "algoforge.authoring-bundle.bind-and-lint.v1"
	maxCanonicalBriefBytes                = 64 << 10
	maxAuthoringPlanBundleBytes           = 64 << 10
)

// GenerateAuthoringPlanInput remains isolated from the legacy generation
// pipeline. Only the additive, dormant ProblemGenerationAuthoringWorkflowV1
// consumes it; production starters do not route to that workflow yet.
type GenerateAuthoringPlanInput struct {
	PayloadVersion       int                     `json:"payload_version"`
	Params               domain.ProblemGenParams `json:"params"`
	Neighbors            []NeighborInfo          `json:"neighbors,omitempty"`
	CanonicalBrief       string                  `json:"canonical_brief"`
	CanonicalBriefSHA256 string                  `json:"canonical_brief_sha256"`
}

// GenerateAuthoringPlanResult keeps Temporal history small. The full typed
// contract and lint report are stored in BundleArtifact.
type GenerateAuthoringPlanResult struct {
	PayloadVersion     int            `json:"payload_version"`
	ModelDecision      string         `json:"model_decision"`
	Decision           string         `json:"decision"`
	GateReasonCode     string         `json:"gate_reason_code,omitempty"`
	RejectionReason    string         `json:"rejection_reason,omitempty"`
	InputSHA256        string         `json:"input_sha256"`
	BriefSHA256        string         `json:"brief_sha256"`
	SemanticSpecSHA256 string         `json:"semantic_spec_sha256,omitempty"`
	BundleSHA256       string         `json:"bundle_sha256"`
	BundleArtifact     *ArtifactRef   `json:"bundle_artifact"`
	SourceArtifacts    []*ArtifactRef `json:"source_artifacts"`
	LintPassed         bool           `json:"lint_passed"`
	LintErrorCount     int            `json:"lint_error_count"`
	LintAdvisoryCount  int            `json:"lint_advisory_count"`
}

// AuthoringPlanBundleV1 is the immutable CAS payload consumed by later QG-02
// workflow versions. A model rejection has no spec or plan; a lint rejection
// retains both invalid candidates together with the deterministic report.
type AuthoringPlanBundleV1 struct {
	SchemaVersion          string                     `json:"schema_version"`
	InputSHA256            string                     `json:"input_sha256"`
	BriefSHA256            string                     `json:"brief_sha256"`
	RequestedTestCaseCount int                        `json:"requested_test_case_count,omitempty"`
	RequestedSampleCount   int                        `json:"requested_sample_count,omitempty"`
	MinTestCaseCount       int                        `json:"min_test_case_count,omitempty"`
	MaxTestCaseCount       int                        `json:"max_test_case_count,omitempty"`
	AdaptiveTestCaseCount  bool                       `json:"adaptive_test_case_count,omitempty"`
	SourceArtifactSHA256   string                     `json:"source_artifact_sha256"`
	SourceRequestSHA256    string                     `json:"source_request_sha256"`
	DerivationRule         string                     `json:"derivation_rule"`
	ModelDecision          string                     `json:"model_decision"`
	Decision               string                     `json:"decision"`
	GateReasonCode         string                     `json:"gate_reason_code,omitempty"`
	RejectionReason        string                     `json:"rejection_reason,omitempty"`
	SemanticSpec           *domain.SemanticSpecV1     `json:"semantic_spec,omitempty"`
	AuthoringPlan          *domain.AuthoringPlanV1    `json:"authoring_plan,omitempty"`
	LintReport             *speccontract.LintReportV1 `json:"lint_report,omitempty"`
}

type authoringPlanModelOutputV1 struct {
	Decision        string                  `json:"decision"`
	RejectionReason string                  `json:"rejection_reason,omitempty"`
	SemanticSpec    *domain.SemanticSpecV1  `json:"semantic_spec,omitempty"`
	AuthoringPlan   *domain.AuthoringPlanV1 `json:"authoring_plan,omitempty"`
}

// GenerateAuthoringPlanActivity asks the G route for two isolated objects,
// applies server-owned identity bindings, and runs deterministic lint. It
// never falls back to legacy statement generation.
func (a *Activities) GenerateAuthoringPlanActivity(ctx context.Context, in GenerateAuthoringPlanInput) (*GenerateAuthoringPlanResult, error) {
	if in.PayloadVersion != GenerateAuthoringPlanPayloadVersion {
		return nil, fmt.Errorf("unsupported authoring-plan activity payload version %d", in.PayloadVersion)
	}
	params := in.Params
	if err := params.Validate(); err != nil {
		return nil, fmt.Errorf("invalid generation parameters: %w", err)
	}
	minTestCases, maxTestCases, adaptiveTestCases := params.TestDataConfig.EffectiveTestCaseRange()
	if params.TestDataConfig.NumSamples > maxTestCases {
		return nil, fmt.Errorf(
			"invalid authoring sample count %d for test-case range [%d,%d]",
			params.TestDataConfig.NumSamples,
			minTestCases,
			maxTestCases,
		)
	}
	brief := in.CanonicalBrief
	if brief == "" || brief != strings.TrimSpace(brief) {
		return nil, fmt.Errorf("canonical brief must be non-empty and trimmed")
	}
	if len(brief) > maxCanonicalBriefBytes {
		return nil, fmt.Errorf("canonical brief exceeds %d bytes", maxCanonicalBriefBytes)
	}
	briefSHA := sha256Hex([]byte(brief))
	if in.CanonicalBriefSHA256 != briefSHA {
		return nil, fmt.Errorf("canonical brief SHA-256 mismatch")
	}
	inputSHA, err := canonicalJSONSHA256(in)
	if err != nil {
		return nil, fmt.Errorf("hash authoring-plan input: %w", err)
	}

	activity.RecordHeartbeat(ctx, "calling LLM to generate SemanticSpec and AuthoringPlan")
	temperature := 0.1
	req := &llm.Request{
		MaxTokens:   16000,
		System:      authoringPlanSystemPromptV1,
		Temperature: &temperature,
		Messages: []llm.Message{
			{Role: "user", Content: buildAuthoringPlanPromptV1(brief, params, in.Neighbors)},
			{Role: "assistant", Content: "{"},
		},
	}
	applyStatementLLMRuntime(req, params)

	stopHB := heartbeatWhile(ctx, "calling LLM to generate SemanticSpec and AuthoringPlan", 15*time.Second)
	response, sourceArtifact, err := a.completeLLMWithProvenance(ctx, "authoring_plan", req, 2)
	stopHB()
	if err != nil {
		return nil, wrapRequiredProviderEffectError("llm call for authoring-plan generation", err)
	}
	if sourceArtifact == nil {
		return nil, fmt.Errorf("authoring-plan source artifact is required")
	}
	if sourceArtifact.SHA256 == "" || sourceArtifact.LLMCallReceipt == nil || sourceArtifact.LLMCallReceipt.RequestSHA256 == "" {
		return nil, fmt.Errorf("authoring-plan source artifact lacks immutable request identity")
	}

	parsed, err := parseAuthoringPlanResponseV1(restoreJSONPrefill(response.Text()))
	if err != nil {
		return nil, fmt.Errorf("parsing authoring-plan response: %w", err)
	}

	bundle := AuthoringPlanBundleV1{
		SchemaVersion:          AuthoringPlanBundleSchemaV1,
		InputSHA256:            inputSHA,
		BriefSHA256:            briefSHA,
		RequestedTestCaseCount: params.TestDataConfig.NumTestCases,
		RequestedSampleCount:   params.TestDataConfig.NumSamples,
		SourceArtifactSHA256:   sourceArtifact.SHA256,
		SourceRequestSHA256:    sourceArtifact.LLMCallReceipt.RequestSHA256,
		DerivationRule:         authoringPlanDerivationRuleV1,
		ModelDecision:          parsed.Decision,
		Decision:               parsed.Decision,
		RejectionReason:        parsed.RejectionReason,
	}
	if adaptiveTestCases {
		bundle.MinTestCaseCount = minTestCases
		bundle.MaxTestCaseCount = maxTestCases
		bundle.AdaptiveTestCaseCount = true
	}
	result := &GenerateAuthoringPlanResult{
		PayloadVersion:  GenerateAuthoringPlanPayloadVersion,
		ModelDecision:   parsed.Decision,
		Decision:        parsed.Decision,
		RejectionReason: parsed.RejectionReason,
		InputSHA256:     inputSHA,
		BriefSHA256:     briefSHA,
		SourceArtifacts: []*ArtifactRef{sourceArtifact},
	}
	if parsed.Decision == AuthoringPlanDecisionRejected {
		bundle.GateReasonCode = AuthoringPlanGateReasonModelRejected
		result.GateReasonCode = AuthoringPlanGateReasonModelRejected
	}

	if parsed.Decision == AuthoringPlanDecisionAccepted {
		if parsed.SemanticSpec.SchemaVersion != "" || parsed.SemanticSpec.BriefSHA256 != "" || parsed.AuthoringPlan.SchemaVersion != "" || parsed.AuthoringPlan.BriefSHA256 != "" || parsed.AuthoringPlan.SemanticSpecSHA256 != "" {
			return nil, fmt.Errorf("model response contains server-owned schema or hash fields")
		}
		parsed.SemanticSpec.SchemaVersion = domain.SemanticSpecSchemaV1
		parsed.SemanticSpec.BriefSHA256 = briefSHA
		parsed.AuthoringPlan.SchemaVersion = domain.AuthoringPlanSchemaV1
		parsed.AuthoringPlan.BriefSHA256 = briefSHA
		parsed.AuthoringPlan.SemanticSpecSHA256 = speccontract.SemanticSpecSHA256V1(*parsed.SemanticSpec)

		lintReport := speccontract.LintV1(speccontract.LintInputV1{
			SemanticSpec:            *parsed.SemanticSpec,
			AuthoringPlan:           *parsed.AuthoringPlan,
			ExpectedBriefSHA256:     briefSHA,
			ExpectedDifficulty:      params.Difficulty,
			RequiredKnowledgePoints: append([]string(nil), params.Tags...),
		})
		bundle.SemanticSpec = parsed.SemanticSpec
		bundle.AuthoringPlan = parsed.AuthoringPlan
		bundle.LintReport = &lintReport
		result.SemanticSpecSHA256 = lintReport.SemanticSpecSHA256
		result.LintPassed = lintReport.Passed
		for _, issue := range lintReport.Issues {
			switch issue.Severity {
			case speccontract.LintSeverityAdvisory:
				result.LintAdvisoryCount++
			default:
				result.LintErrorCount++
			}
		}
		if !lintReport.Passed {
			bundle.Decision = AuthoringPlanDecisionRejected
			bundle.GateReasonCode = AuthoringPlanGateReasonSpecLintFailed
			bundle.RejectionReason = "deterministic spec lint failed"
			result.Decision = AuthoringPlanDecisionRejected
			result.GateReasonCode = AuthoringPlanGateReasonSpecLintFailed
			result.RejectionReason = "deterministic spec lint failed"
		}
	}

	encoded, err := json.Marshal(bundle)
	if err != nil {
		return nil, fmt.Errorf("marshal canonical authoring bundle: %w", err)
	}
	if len(encoded) > maxAuthoringPlanBundleBytes {
		return nil, fmt.Errorf("canonical authoring bundle exceeds %d bytes", maxAuthoringPlanBundleBytes)
	}
	metadata := artifactMetadataFromActivity(ctx)
	metadata.ArtifactType = "qg02a_authoring_bundle"
	metadata.SourceType = "generated_authoring_contract"
	metadata.RetentionClass = "workflow_cas_unreviewed"
	provenance := map[string]interface{}{
		"brief_sha256":              briefSHA,
		"input_sha256":              inputSHA,
		"requested_test_case_count": params.TestDataConfig.NumTestCases,
		"requested_sample_count":    params.TestDataConfig.NumSamples,
		"source_artifact_sha256":    sourceArtifact.SHA256,
		"source_request_sha256":     sourceArtifact.LLMCallReceipt.RequestSHA256,
		"derivation_rule":           authoringPlanDerivationRuleV1,
		"model_decision":            result.ModelDecision,
		"gate_decision":             result.Decision,
	}
	if adaptiveTestCases {
		provenance["min_test_case_count"] = minTestCases
		provenance["max_test_case_count"] = maxTestCases
		provenance["adaptive_test_case_count"] = true
	}
	metadata.ProvenanceMetadata, err = json.Marshal(provenance)
	if err != nil {
		return nil, fmt.Errorf("encode authoring bundle provenance: %w", err)
	}
	bundleArtifact, err := a.putArtifactWithMetadata(ctx, encoded, "application/json", metadata)
	if err != nil {
		return nil, fmt.Errorf("store canonical authoring bundle: %w", err)
	}
	bundleSHA := sha256Hex(encoded)
	if bundleArtifact.SHA256 != bundleSHA || bundleArtifact.SizeBytes != int64(len(encoded)) {
		return nil, fmt.Errorf("canonical authoring bundle CAS identity mismatch")
	}
	result.BundleSHA256 = bundleSHA
	result.BundleArtifact = bundleArtifact
	result.SourceArtifacts = append(result.SourceArtifacts, bundleArtifact)
	return result, nil
}

func parseAuthoringPlanResponseV1(text string) (*authoringPlanModelOutputV1, error) {
	trimmed := strings.TrimSpace(text)
	if err := validateExactJSONKeysV1(trimmed, reflect.TypeOf(authoringPlanModelOutputV1{})); err != nil {
		return nil, err
	}
	if err := rejectServerOwnedAuthoringFieldsV1(trimmed); err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(strings.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	var output authoringPlanModelOutputV1
	if err := decoder.Decode(&output); err != nil {
		return nil, err
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return nil, err
	}
	switch output.Decision {
	case AuthoringPlanDecisionAccepted:
		if output.RejectionReason != "" || output.SemanticSpec == nil || output.AuthoringPlan == nil {
			return nil, fmt.Errorf("accepted output requires spec and plan and forbids rejection_reason")
		}
	case AuthoringPlanDecisionRejected:
		if output.RejectionReason == "" || output.RejectionReason != strings.TrimSpace(output.RejectionReason) || output.SemanticSpec != nil || output.AuthoringPlan != nil {
			return nil, fmt.Errorf("rejected output requires a trimmed reason and forbids spec and plan")
		}
	default:
		return nil, fmt.Errorf("unsupported authoring decision %q", output.Decision)
	}
	return &output, nil
}

func validateExactJSONKeysV1(text string, target reflect.Type) error {
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.UseNumber()
	if err := validateExactJSONValueV1(decoder, target, "$"); err != nil {
		return err
	}
	return ensureJSONEOF(decoder)
}

func validateExactJSONValueV1(decoder *json.Decoder, target reflect.Type, path string) error {
	allowNull := false
	for target.Kind() == reflect.Pointer {
		allowNull = true
		target = target.Elem()
	}
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if token == nil {
		if allowNull {
			return nil
		}
		return fmt.Errorf("%s: null is forbidden", path)
	}

	switch target.Kind() {
	case reflect.Struct:
		if token != json.Delim('{') {
			return fmt.Errorf("%s: expected JSON object", path)
		}
		fields := exactJSONFieldTypesV1(target)
		seen := make(map[string]struct{}, len(fields))
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return fmt.Errorf("%s: read object key: %w", path, err)
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("%s: object key is not a string", path)
			}
			if _, duplicate := seen[key]; duplicate {
				return fmt.Errorf("%s.%s: duplicate JSON key", path, key)
			}
			seen[key] = struct{}{}
			fieldType, exists := fields[key]
			if !exists {
				return fmt.Errorf("%s.%s: unknown or non-canonical JSON key", path, key)
			}
			if err := validateExactJSONValueV1(decoder, fieldType, path+"."+key); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return fmt.Errorf("%s: invalid JSON object terminator", path)
		}
	case reflect.Slice, reflect.Array:
		if token != json.Delim('[') {
			return fmt.Errorf("%s: expected JSON array", path)
		}
		index := 0
		for decoder.More() {
			if err := validateExactJSONValueV1(decoder, target.Elem(), fmt.Sprintf("%s[%d]", path, index)); err != nil {
				return err
			}
			index++
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return fmt.Errorf("%s: invalid JSON array terminator", path)
		}
	case reflect.Map:
		if target.Key().Kind() != reflect.String || token != json.Delim('{') {
			return fmt.Errorf("%s: expected string-keyed JSON object", path)
		}
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return fmt.Errorf("%s: read map key: %w", path, err)
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("%s: map key is not a string", path)
			}
			if _, duplicate := seen[key]; duplicate {
				return fmt.Errorf("%s.%s: duplicate JSON key", path, key)
			}
			seen[key] = struct{}{}
			if err := validateExactJSONValueV1(decoder, target.Elem(), path+"."+key); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return fmt.Errorf("%s: invalid JSON map terminator", path)
		}
	default:
		if _, isDelimiter := token.(json.Delim); isDelimiter {
			return fmt.Errorf("%s: expected scalar JSON value", path)
		}
	}
	return nil
}

func exactJSONFieldTypesV1(target reflect.Type) map[string]reflect.Type {
	fields := make(map[string]reflect.Type, target.NumField())
	for i := 0; i < target.NumField(); i++ {
		field := target.Field(i)
		if field.PkgPath != "" {
			continue
		}
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name == "-" {
			continue
		}
		if name == "" {
			name = field.Name
		}
		fields[name] = field.Type
	}
	return fields
}

func rejectServerOwnedAuthoringFieldsV1(text string) error {
	var top map[string]json.RawMessage
	if err := json.Unmarshal([]byte(text), &top); err != nil {
		return err
	}
	objects := []struct {
		name      string
		forbidden []string
	}{
		{name: "semantic_spec", forbidden: []string{"schema_version", "brief_sha256"}},
		{name: "authoring_plan", forbidden: []string{"schema_version", "brief_sha256", "semantic_spec_sha256"}},
	}
	for _, candidate := range objects {
		raw, exists := top[candidate.name]
		if !exists || string(raw) == "null" {
			continue
		}
		var object map[string]json.RawMessage
		if err := json.Unmarshal(raw, &object); err != nil {
			return fmt.Errorf("%s: %w", candidate.name, err)
		}
		for _, key := range candidate.forbidden {
			if _, exists := object[key]; exists {
				return fmt.Errorf("%s.%s is server-owned and must be omitted", candidate.name, key)
			}
		}
	}
	return nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var trailing interface{}
	err := decoder.Decode(&trailing)
	if err == io.EOF {
		return nil
	}
	if err == nil {
		return fmt.Errorf("trailing JSON document is forbidden")
	}
	return fmt.Errorf("invalid trailing content: %w", err)
}

func buildAuthoringPlanPromptV1(brief string, params domain.ProblemGenParams, neighbors []NeighborInfo) string {
	_ = params.TestDataConfig.NormalizeForGeneration()
	minTestCases, maxTestCases, adaptiveTestCases := params.TestDataConfig.EffectiveTestCaseRange()
	type promptParams struct {
		Level                     domain.ProblemLevel                       `json:"level"`
		Difficulty                int                                       `json:"difficulty"`
		RequestedTestCaseCount    int                                       `json:"requested_test_case_count"`
		RequestedSampleCount      int                                       `json:"requested_sample_count"`
		MinTestCaseCount          int                                       `json:"min_test_case_count,omitempty"`
		MaxTestCaseCount          int                                       `json:"max_test_case_count,omitempty"`
		AdaptiveTestCaseCount     bool                                      `json:"adaptive_test_case_count,omitempty"`
		Tags                      []string                                  `json:"tags"`
		KnowledgePointCombination *domain.KnowledgePointCombinationContract `json:"knowledge_point_combination,omitempty"`
		ContestStyle              string                                    `json:"contest_style,omitempty"`
		TimeLimit                 int                                       `json:"time_limit_ms"`
		MemoryLimit               int                                       `json:"memory_limit_mb"`
		Locale                    string                                    `json:"locale,omitempty"`
		CustomPrompt              string                                    `json:"custom_prompt,omitempty"`
	}
	type promptNeighbor struct {
		Title       string   `json:"title"`
		OneLineHint string   `json:"one_line_hint,omitempty"`
		Tags        []string `json:"tags,omitempty"`
	}
	neighborFacts := make([]promptNeighbor, 0, len(neighbors))
	for _, neighbor := range neighbors {
		neighborFacts = append(neighborFacts, promptNeighbor{Title: neighbor.Title, OneLineHint: neighbor.OneLineHint, Tags: append([]string(nil), neighbor.Tags...)})
	}
	encodedParams, _ := json.Marshal(promptParams{
		Level: params.Level, Difficulty: params.Difficulty,
		RequestedTestCaseCount:    params.TestDataConfig.NumTestCases,
		RequestedSampleCount:      params.TestDataConfig.NumSamples,
		MinTestCaseCount:          minTestCases,
		MaxTestCaseCount:          maxTestCases,
		AdaptiveTestCaseCount:     adaptiveTestCases,
		Tags:                      append([]string(nil), params.Tags...),
		KnowledgePointCombination: params.KnowledgePointCombination,
		ContestStyle:              params.ContestStyle, TimeLimit: params.TimeLimit, MemoryLimit: params.MemoryLimit,
		Locale: params.Locale, CustomPrompt: params.CustomPrompt,
	})
	encodedNeighbors, _ := json.Marshal(neighborFacts)
	countPolicy := "fixed exact count"
	countInstructions := "The later test-data stage must produce exactly the requested count."
	if adaptiveTestCases {
		countPolicy = fmt.Sprintf("adaptive count in [%d,%d]", minTestCases, maxTestCases)
		countInstructions = fmt.Sprintf("The later test-data stage must choose the smallest sufficient suite in [%d,%d] (custom cases included), without padding duplicates. Derive test intents from distinct applicable boundary, adversarial, and scale regions; include a genuinely small exhaustive/brute-check intent and a maximum-scale randomized/pressure intent when the constraints make them meaningful.", minTestCases, maxTestCases)
	}
	return "Canonical brief (do not change its core mechanism):\n" + brief +
		"\n\nGeneration parameters:\n" + string(encodedParams) +
		"\n\nTest-data count policy: " + countPolicy + ". " + countInstructions +
		"\n\nNearby problems to avoid imitating:\n" + string(encodedNeighbors) +
		"\n\nReturn exactly the JSON object required by the system instruction."
}

func sha256Hex(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func canonicalJSONSHA256(value interface{}) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return sha256Hex(encoded), nil
}

const authoringPlanSystemPromptV1 = `You are the G formalization stage for a competitive-programming authoring system.

Return exactly one compact JSON object, with no Markdown fences or commentary. Top-level shape:
{"decision":"accepted","semantic_spec":{...},"authoring_plan":{...}}
or, only when the frozen concept is contradictory or cannot be specified reliably:
{"decision":"rejected","rejection_reason":"specific reason"}

For accepted output, omit these server-owned fields entirely: schema_version, brief_sha256, semantic_spec_sha256. The server binds them after strict decoding.

SemanticSpec contains problem facts only. It must include:
- problem_definition and nonempty input/output/constraints sections with authoritative symbol_refs;
- executable token-line input_grammar and output_grammar using lines, repeat expressions, fields, modes and counts;
- symbols with explicit input/output/derived scope and boundary policy;
- typed constant ranges, including element_range for every integer sequence;
- relation ASTs for facts such as length(a)=n; do not hide relations in prose;
- an objective that references an output symbol;
- topology kind plus its subject and every applicable direction/cycle/connectivity/dynamics/self-loop/multi-edge/count fact;
- at least five meaningful high-risk boundary facts; size/count variables such as n, m and k use zero_one_required;
- explicit no-solution and multiple-solution semantics;
- exact_normalized with comparison_profile trim-trailing-space-lf-v1, or special_checker with immutable cas:// reference, SHA-256 and rule version;
- sample_input_candidates must contain exactly requested_sample_count raw input strings and never final outputs; when requested_sample_count is 0, return an empty array.

Expression kinds: symbol, integer, length, cardinality, add, subtract, multiply, floor_divide. Relation operators: equal, less_than, less_than_or_equal, greater_than, greater_than_or_equal.
Grammar field modes: scalar, sequence, element_per_repeat. Every repeat/count is an expression object.

AuthoringPlan contains generation guidance only: core idea, role/necessity of every requested concept, intended solution, brute-force baseline, oracle_candidate strategy, likely failure modes, test intents, target difficulty, teaching objectives and creative intent. Do not put these hints in SemanticSpec.

Do not invent a different problem when the frozen concept fails; return rejected instead.`
