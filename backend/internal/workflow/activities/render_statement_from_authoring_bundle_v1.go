package activities

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/llm"
	speccontract "github.com/Gingoo-TvT/Qraft/backend/internal/spec"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
)

const (
	RenderStatementFromAuthoringBundlePayloadVersion = 1
	StatementFactManifestSchemaV1                    = "algoforge.statement-fact-manifest.v1"
	StatementDraftBundleSchemaV1                     = "algoforge.statement-draft-bundle.v1"
	statementDraftDerivationRuleV1                   = "algoforge.statement-draft.typed-render.v1"
	statementDraftContractErrorTypeV1                = "StatementDraftContractError"
	statementDraftDocumentStatusV1                   = "presentation_shell_not_final_statement"
	maxStatementDraftTitleBytesV1                    = 240
	maxStatementDraftNarrativeBytesV1                = 2048
	maxStatementNarrativeResponseBytesV1             = 8 << 10
	maxStatementDraftBundleBytesV1                   = 256 << 10
)

// RenderStatementFromAuthoringBundleInput is deliberately compact. It does
// not repeat the frozen brief, generation parameters, neighbors, or custom
// prompt in Temporal history. The accepted CAS bundle and the hashes already
// validated by the authoring workflow are the only authoring inputs.
type RenderStatementFromAuthoringBundleInput struct {
	PayloadVersion               int                      `json:"payload_version"`
	BundleArtifact               ArtifactRef              `json:"bundle_artifact"`
	ExpectedBundleSHA256         string                   `json:"expected_bundle_sha256"`
	ExpectedAuthoringInputSHA256 string                   `json:"expected_authoring_input_sha256"`
	ExpectedBriefSHA256          string                   `json:"expected_brief_sha256"`
	ExpectedSemanticSpecSHA256   string                   `json:"expected_semantic_spec_sha256"`
	ExpectedDifficulty           int                      `json:"expected_difficulty"`
	RequiredKnowledgePoints      []string                 `json:"required_knowledge_points,omitempty"`
	PresentationLocale           string                   `json:"presentation_locale"`
	StatementRuntime             *domain.LLMRuntimeConfig `json:"statement_runtime,omitempty"`
}

// StatementFactManifestV1 is the exact normative projection used by the
// deterministic Markdown renderer. It is deliberately not sent to the
// narrative model. SemanticSpec.SampleInputs is intentionally absent: sample
// candidates are neither trusted nor disclosed and belong to a later stage.
type StatementFactManifestV1 struct {
	SchemaVersion     string                           `json:"schema_version"`
	ProblemDefinition string                           `json:"problem_definition"`
	Sections          domain.SemanticSpecSectionsV1    `json:"sections"`
	InputGrammar      domain.SemanticGrammarV1         `json:"input_grammar"`
	OutputGrammar     domain.SemanticGrammarV1         `json:"output_grammar"`
	Objective         domain.SemanticObjectiveV1       `json:"objective"`
	Symbols           []domain.SemanticSymbolV1        `json:"symbols"`
	Constraints       []domain.SemanticConstraintV1    `json:"constraints"`
	Relations         []domain.SemanticRelationV1      `json:"relations,omitempty"`
	Topology          domain.SemanticTopologyV1        `json:"topology"`
	Boundaries        []domain.SemanticBoundaryV1      `json:"boundaries"`
	AnswerSemantics   domain.SemanticAnswerSemanticsV1 `json:"answer_semantics"`
	Judge             domain.SemanticJudgeV1           `json:"judge"`
}

// StatementDraftBundleV1 is the internal, immutable CAS output of QG-02C.
// The model contributes only Title and Narrative. FactManifest and Markdown
// are server-derived from the typed SemanticSpec projection.
type StatementDraftBundleV1 struct {
	SchemaVersion                string                  `json:"schema_version"`
	DocumentStatus               string                  `json:"document_status"`
	RendererInputSHA256          string                  `json:"renderer_input_sha256"`
	AuthoringBundleSHA256        string                  `json:"authoring_bundle_sha256"`
	AuthoringInputSHA256         string                  `json:"authoring_input_sha256"`
	BriefSHA256                  string                  `json:"brief_sha256"`
	SemanticSpecSHA256           string                  `json:"semantic_spec_sha256"`
	FactManifestSHA256           string                  `json:"fact_manifest_sha256"`
	ModelOutputSHA256            string                  `json:"model_output_sha256"`
	RendererSourceArtifactSHA256 string                  `json:"renderer_source_artifact_sha256"`
	RendererSourceRequestSHA256  string                  `json:"renderer_source_request_sha256"`
	DerivationRule               string                  `json:"derivation_rule"`
	PresentationLocale           string                  `json:"presentation_locale"`
	Title                        string                  `json:"title"`
	Narrative                    string                  `json:"narrative"`
	FactManifest                 StatementFactManifestV1 `json:"fact_manifest"`
	Markdown                     string                  `json:"markdown"`
	MarkdownSHA256               string                  `json:"markdown_sha256"`
}

// RenderStatementFromAuthoringBundleResult keeps the rendered Markdown out of
// workflow history. The full draft and fact manifest are available through the
// content-addressed StatementDraftArtifact.
type RenderStatementFromAuthoringBundleResult struct {
	PayloadVersion         int            `json:"payload_version"`
	RendererInputSHA256    string         `json:"renderer_input_sha256"`
	AuthoringBundleSHA256  string         `json:"authoring_bundle_sha256"`
	AuthoringInputSHA256   string         `json:"authoring_input_sha256"`
	BriefSHA256            string         `json:"brief_sha256"`
	SemanticSpecSHA256     string         `json:"semantic_spec_sha256"`
	FactManifestSHA256     string         `json:"fact_manifest_sha256"`
	MarkdownSHA256         string         `json:"markdown_sha256"`
	StatementDraftSHA256   string         `json:"statement_draft_sha256"`
	StatementDraftArtifact *ArtifactRef   `json:"statement_draft_artifact"`
	SourceArtifacts        []*ArtifactRef `json:"source_artifacts"`
}

type statementNarrativeModelOutputV1 struct {
	Title     string `json:"title"`
	Narrative string `json:"narrative"`
}

// RenderStatementFromAuthoringBundleActivityV1 reads one canonical accepted
// authoring bundle, asks G only for non-normative presentation text, and
// deterministically renders every normative fact into an internal draft.
func (a *Activities) RenderStatementFromAuthoringBundleActivityV1(
	ctx context.Context,
	in RenderStatementFromAuthoringBundleInput,
) (*RenderStatementFromAuthoringBundleResult, error) {
	normalizedRuntime, err := validateStatementRendererInputV1(in)
	if err != nil {
		return nil, nonRetryableStatementDraftContractErrorV1(err)
	}
	normalizedInput := in
	normalizedInput.StatementRuntime = normalizedRuntime
	rendererInputSHA, err := canonicalJSONSHA256(normalizedInput)
	if err != nil {
		return nil, fmt.Errorf("hash statement renderer input: %w", err)
	}
	if a == nil || a.artifacts == nil {
		return nil, fmt.Errorf("artifact store is not configured")
	}

	activity.RecordHeartbeat(ctx, "reading accepted authoring bundle")
	bundleBytes, err := a.artifacts.Get(ctx, in.BundleArtifact)
	if err != nil {
		return nil, fmt.Errorf("read authoring bundle: %w", err)
	}
	if len(bundleBytes) > maxAuthoringPlanBundleBytes || int64(len(bundleBytes)) != in.BundleArtifact.SizeBytes {
		return nil, nonRetryableStatementDraftContractErrorV1(fmt.Errorf("authoring bundle size is outside the declared QG-02A contract"))
	}
	if sha256Hex(bundleBytes) != in.ExpectedBundleSHA256 {
		return nil, nonRetryableStatementDraftContractErrorV1(fmt.Errorf("authoring bundle CAS hash mismatch"))
	}

	bundle, err := decodeCanonicalAuthoringBundleV1(bundleBytes)
	if err != nil {
		return nil, nonRetryableStatementDraftContractErrorV1(fmt.Errorf("decode canonical authoring bundle: %w", err))
	}
	if err := validateAcceptedAuthoringBundleForStatementV1(bundle, in); err != nil {
		return nil, nonRetryableStatementDraftContractErrorV1(err)
	}

	factManifest := statementFactManifestFromSemanticSpecV1(*bundle.SemanticSpec)
	factManifestSHA, err := canonicalJSONSHA256(factManifest)
	if err != nil {
		return nil, fmt.Errorf("hash statement fact manifest: %w", err)
	}
	prompt, err := buildStatementNarrativePromptV1(in.PresentationLocale, bundle.AuthoringPlan.CreativeIntent)
	if err != nil {
		return nil, fmt.Errorf("build statement narrative prompt: %w", err)
	}

	temperature := 0.2
	req := &llm.Request{
		MaxTokens:   2048,
		System:      statementNarrativeSystemPromptV1,
		Temperature: &temperature,
		Messages:    []llm.Message{{Role: "user", Content: prompt}},
	}
	applyLLMRuntime(req, normalizedRuntime)

	activity.RecordHeartbeat(ctx, "calling LLM for title and narrative")
	stopHB := heartbeatWhile(ctx, "calling LLM for title and narrative", 15*time.Second)
	response, sourceArtifact, err := a.completeLLMWithProvenance(ctx, "authoring_statement_v1", req, 2)
	stopHB()
	if err != nil {
		return nil, wrapRequiredProviderEffectError("llm call for authoring statement narrative", err)
	}
	if response == nil {
		return nil, nonRetryableStatementDraftContractErrorV1(fmt.Errorf("renderer returned a nil model response"))
	}
	if sourceArtifact == nil || sourceArtifact.LLMCallReceipt == nil ||
		!statementDraftIsSHA256V1(sourceArtifact.SHA256) ||
		!statementDraftIsSHA256V1(sourceArtifact.LLMCallReceipt.RequestSHA256) {
		return nil, nonRetryableStatementDraftContractErrorV1(fmt.Errorf("renderer source artifact lacks immutable request identity"))
	}

	responseText := response.Text()
	if len(responseText) > maxStatementNarrativeResponseBytesV1 {
		return nil, nonRetryableStatementDraftContractErrorV1(fmt.Errorf("statement narrative response exceeds %d bytes", maxStatementNarrativeResponseBytesV1))
	}
	modelOutput, err := parseStatementNarrativeResponseV1(responseText)
	if err != nil {
		return nil, nonRetryableStatementDraftContractErrorV1(fmt.Errorf("parse statement narrative response: %w", err))
	}
	if err := lintStatementNarrativeAgainstFactsV1(*modelOutput, factManifest); err != nil {
		return nil, nonRetryableStatementDraftContractErrorV1(err)
	}
	modelOutputSHA, err := canonicalJSONSHA256(modelOutput)
	if err != nil {
		return nil, fmt.Errorf("hash statement narrative output: %w", err)
	}
	markdown, err := renderStatementDraftMarkdownV1(in.PresentationLocale, *modelOutput, factManifest)
	if err != nil {
		return nil, nonRetryableStatementDraftContractErrorV1(err)
	}
	markdownSHA := sha256Hex([]byte(markdown))

	draft := StatementDraftBundleV1{
		SchemaVersion:                StatementDraftBundleSchemaV1,
		DocumentStatus:               statementDraftDocumentStatusV1,
		RendererInputSHA256:          rendererInputSHA,
		AuthoringBundleSHA256:        in.ExpectedBundleSHA256,
		AuthoringInputSHA256:         in.ExpectedAuthoringInputSHA256,
		BriefSHA256:                  in.ExpectedBriefSHA256,
		SemanticSpecSHA256:           in.ExpectedSemanticSpecSHA256,
		FactManifestSHA256:           factManifestSHA,
		ModelOutputSHA256:            modelOutputSHA,
		RendererSourceArtifactSHA256: sourceArtifact.SHA256,
		RendererSourceRequestSHA256:  sourceArtifact.LLMCallReceipt.RequestSHA256,
		DerivationRule:               statementDraftDerivationRuleV1,
		PresentationLocale:           in.PresentationLocale,
		Title:                        modelOutput.Title,
		Narrative:                    modelOutput.Narrative,
		FactManifest:                 factManifest,
		Markdown:                     markdown,
		MarkdownSHA256:               markdownSHA,
	}
	draftBytes, err := json.Marshal(draft)
	if err != nil {
		return nil, fmt.Errorf("marshal canonical statement draft bundle: %w", err)
	}
	if len(draftBytes) > maxStatementDraftBundleBytesV1 {
		return nil, nonRetryableStatementDraftContractErrorV1(fmt.Errorf("canonical statement draft bundle exceeds %d bytes", maxStatementDraftBundleBytesV1))
	}

	metadata := artifactMetadataFromActivity(ctx)
	metadata.ArtifactType = "qg02c_statement_draft_bundle"
	metadata.SourceType = "generated_statement_draft"
	metadata.RetentionClass = "workflow_cas_unreviewed"
	metadata.ProvenanceMetadata, err = json.Marshal(map[string]string{
		"renderer_input_sha256":           rendererInputSHA,
		"authoring_bundle_sha256":         in.ExpectedBundleSHA256,
		"authoring_input_sha256":          in.ExpectedAuthoringInputSHA256,
		"brief_sha256":                    in.ExpectedBriefSHA256,
		"semantic_spec_sha256":            in.ExpectedSemanticSpecSHA256,
		"fact_manifest_sha256":            factManifestSHA,
		"markdown_sha256":                 markdownSHA,
		"renderer_source_artifact_sha256": sourceArtifact.SHA256,
		"renderer_source_request_sha256":  sourceArtifact.LLMCallReceipt.RequestSHA256,
		"derivation_rule":                 statementDraftDerivationRuleV1,
	})
	if err != nil {
		return nil, fmt.Errorf("encode statement draft provenance: %w", err)
	}
	draftArtifact, err := a.putArtifactWithMetadata(ctx, draftBytes, "application/json", metadata)
	if err != nil {
		return nil, fmt.Errorf("store canonical statement draft bundle: %w", err)
	}
	draftSHA := sha256Hex(draftBytes)
	if draftArtifact.SHA256 != draftSHA || draftArtifact.SizeBytes != int64(len(draftBytes)) {
		return nil, fmt.Errorf("canonical statement draft CAS identity mismatch")
	}

	bundleRef := in.BundleArtifact
	return &RenderStatementFromAuthoringBundleResult{
		PayloadVersion:         RenderStatementFromAuthoringBundlePayloadVersion,
		RendererInputSHA256:    rendererInputSHA,
		AuthoringBundleSHA256:  in.ExpectedBundleSHA256,
		AuthoringInputSHA256:   in.ExpectedAuthoringInputSHA256,
		BriefSHA256:            in.ExpectedBriefSHA256,
		SemanticSpecSHA256:     in.ExpectedSemanticSpecSHA256,
		FactManifestSHA256:     factManifestSHA,
		MarkdownSHA256:         markdownSHA,
		StatementDraftSHA256:   draftSHA,
		StatementDraftArtifact: draftArtifact,
		SourceArtifacts:        []*ArtifactRef{&bundleRef, sourceArtifact, draftArtifact},
	}, nil
}

func validateStatementRendererInputV1(in RenderStatementFromAuthoringBundleInput) (*domain.LLMRuntimeConfig, error) {
	if in.PayloadVersion != RenderStatementFromAuthoringBundlePayloadVersion {
		return nil, fmt.Errorf("unsupported statement renderer payload version %d", in.PayloadVersion)
	}
	if in.PresentationLocale != "en" && in.PresentationLocale != "zh" {
		return nil, fmt.Errorf("statement renderer locale must be en or zh")
	}
	if in.ExpectedDifficulty <= 0 || in.ExpectedDifficulty%100 != 0 {
		return nil, fmt.Errorf("expected difficulty must be a positive multiple of 100")
	}
	if len(in.RequiredKnowledgePoints) > 64 {
		return nil, fmt.Errorf("required knowledge-point list is too large")
	}
	seenKnowledgePoints := make(map[string]struct{}, len(in.RequiredKnowledgePoints))
	for _, value := range in.RequiredKnowledgePoints {
		if value == "" || value != strings.TrimSpace(value) {
			return nil, fmt.Errorf("required knowledge points must be non-empty and trimmed")
		}
		if _, duplicate := seenKnowledgePoints[value]; duplicate {
			return nil, fmt.Errorf("required knowledge point %q is duplicated", value)
		}
		seenKnowledgePoints[value] = struct{}{}
	}
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "expected_bundle_sha256", value: in.ExpectedBundleSHA256},
		{name: "expected_authoring_input_sha256", value: in.ExpectedAuthoringInputSHA256},
		{name: "expected_brief_sha256", value: in.ExpectedBriefSHA256},
		{name: "expected_semantic_spec_sha256", value: in.ExpectedSemanticSpecSHA256},
	} {
		if !statementDraftIsSHA256V1(field.value) {
			return nil, fmt.Errorf("%s is not a canonical SHA-256", field.name)
		}
	}
	if err := in.BundleArtifact.Validate(in.BundleArtifact.Bucket); err != nil {
		return nil, fmt.Errorf("invalid authoring bundle artifact ref: %w", err)
	}
	if in.BundleArtifact.SHA256 != in.ExpectedBundleSHA256 {
		return nil, fmt.Errorf("authoring bundle ref does not match expected bundle SHA-256")
	}
	if in.BundleArtifact.SizeBytes <= 0 || in.BundleArtifact.SizeBytes > maxAuthoringPlanBundleBytes {
		return nil, fmt.Errorf("authoring bundle artifact exceeds the QG-02A size contract")
	}
	if in.BundleArtifact.ContentType != "application/json" || in.BundleArtifact.Producer != "GenerateAuthoringPlanActivity" {
		return nil, fmt.Errorf("authoring bundle artifact type or producer is invalid")
	}
	if in.BundleArtifact.LLMCallReceipt != nil {
		return nil, fmt.Errorf("derived authoring bundle artifact must not carry a provider call receipt")
	}
	if in.StatementRuntime == nil {
		return nil, nil
	}
	normalizedRuntime := *in.StatementRuntime
	if err := normalizedRuntime.Validate("statement_runtime"); err != nil {
		return nil, err
	}
	return &normalizedRuntime, nil
}

func decodeCanonicalAuthoringBundleV1(data []byte) (*AuthoringPlanBundleV1, error) {
	if err := validateExactJSONKeysV1(string(data), reflectTypeAuthoringPlanBundleV1); err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var bundle AuthoringPlanBundleV1
	if err := decoder.Decode(&bundle); err != nil {
		return nil, err
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return nil, err
	}
	canonical, err := json.Marshal(bundle)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(canonical, data) {
		return nil, fmt.Errorf("authoring bundle bytes are not canonical JSON")
	}
	return &bundle, nil
}

func validateAcceptedAuthoringBundleForStatementV1(
	bundle *AuthoringPlanBundleV1,
	in RenderStatementFromAuthoringBundleInput,
) error {
	if bundle == nil {
		return fmt.Errorf("authoring bundle is required")
	}
	if bundle.SchemaVersion != AuthoringPlanBundleSchemaV1 || bundle.DerivationRule != authoringPlanDerivationRuleV1 {
		return fmt.Errorf("unsupported authoring bundle schema or derivation rule")
	}
	if bundle.InputSHA256 != in.ExpectedAuthoringInputSHA256 ||
		bundle.BriefSHA256 != in.ExpectedBriefSHA256 {
		return fmt.Errorf("authoring bundle input or brief binding mismatch")
	}
	if !statementDraftIsSHA256V1(bundle.SourceArtifactSHA256) || !statementDraftIsSHA256V1(bundle.SourceRequestSHA256) {
		return fmt.Errorf("authoring bundle source ancestry is invalid")
	}
	if bundle.ModelDecision != AuthoringPlanDecisionAccepted || bundle.Decision != AuthoringPlanDecisionAccepted ||
		bundle.GateReasonCode != "" || bundle.RejectionReason != "" {
		return fmt.Errorf("statement renderer accepts only an accepted authoring bundle")
	}
	if bundle.SemanticSpec == nil || bundle.AuthoringPlan == nil || bundle.LintReport == nil || !bundle.LintReport.Passed {
		return fmt.Errorf("accepted authoring bundle lacks spec, plan, or passing lint report")
	}
	semanticSHA := speccontract.SemanticSpecSHA256V1(*bundle.SemanticSpec)
	if semanticSHA != in.ExpectedSemanticSpecSHA256 ||
		bundle.SemanticSpec.SchemaVersion != domain.SemanticSpecSchemaV1 ||
		bundle.SemanticSpec.BriefSHA256 != in.ExpectedBriefSHA256 {
		return fmt.Errorf("authoring bundle SemanticSpec binding mismatch")
	}
	if bundle.AuthoringPlan.SchemaVersion != domain.AuthoringPlanSchemaV1 ||
		bundle.AuthoringPlan.BriefSHA256 != in.ExpectedBriefSHA256 ||
		bundle.AuthoringPlan.SemanticSpecSHA256 != semanticSHA {
		return fmt.Errorf("authoring bundle AuthoringPlan binding mismatch")
	}
	if bundle.LintReport.SchemaVersion != speccontract.LintReportSchemaV1 ||
		bundle.LintReport.RuleVersion != speccontract.LintRuleVersionV1 ||
		bundle.LintReport.SemanticSpecSHA256 != semanticSHA ||
		!statementDraftIsSHA256V1(bundle.LintReport.InputSHA256) {
		return fmt.Errorf("authoring bundle lint receipt binding mismatch")
	}
	for _, issue := range bundle.LintReport.Issues {
		if issue.Severity != speccontract.LintSeverityAdvisory {
			return fmt.Errorf("accepted authoring bundle contains a non-advisory lint issue")
		}
	}
	recomputedLint := speccontract.LintV1(speccontract.LintInputV1{
		SemanticSpec:            *bundle.SemanticSpec,
		AuthoringPlan:           *bundle.AuthoringPlan,
		ExpectedBriefSHA256:     in.ExpectedBriefSHA256,
		ExpectedDifficulty:      in.ExpectedDifficulty,
		RequiredKnowledgePoints: append([]string(nil), in.RequiredKnowledgePoints...),
	})
	storedLintJSON, storedErr := json.Marshal(bundle.LintReport)
	recomputedLintJSON, recomputedErr := json.Marshal(recomputedLint)
	if storedErr != nil || recomputedErr != nil ||
		!reflect.DeepEqual(recomputedLint, *bundle.LintReport) ||
		!bytes.Equal(storedLintJSON, recomputedLintJSON) {
		return fmt.Errorf("authoring bundle lint receipt does not match the original formalizer expectations")
	}
	return nil
}

func statementFactManifestFromSemanticSpecV1(spec domain.SemanticSpecV1) StatementFactManifestV1 {
	return StatementFactManifestV1{
		SchemaVersion:     StatementFactManifestSchemaV1,
		ProblemDefinition: spec.ProblemDefinition,
		Sections:          spec.Sections,
		InputGrammar:      spec.InputGrammar,
		OutputGrammar:     spec.OutputGrammar,
		Objective:         spec.Objective,
		Symbols:           append([]domain.SemanticSymbolV1(nil), spec.Symbols...),
		Constraints:       append([]domain.SemanticConstraintV1(nil), spec.Constraints...),
		Relations:         append([]domain.SemanticRelationV1(nil), spec.Relations...),
		Topology:          spec.Topology,
		Boundaries:        append([]domain.SemanticBoundaryV1(nil), spec.Boundaries...),
		AnswerSemantics:   spec.AnswerSemantics,
		Judge:             spec.Judge,
	}
}

func buildStatementNarrativePromptV1(presentationLocale string, creativeIntent string) (string, error) {
	payload := struct {
		PresentationLocale string `json:"presentation_locale"`
		CreativeIntent     string `json:"creative_intent"`
	}{
		PresentationLocale: presentationLocale,
		CreativeIntent:     creativeIntent,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return "Create only a short title and one-sentence non-normative presentation narrative. " +
		"No problem facts are supplied to you. Do not invent or imply any normative fact.\n" + string(encoded), nil
}

func parseStatementNarrativeResponseV1(text string) (*statementNarrativeModelOutputV1, error) {
	if !utf8.ValidString(text) {
		return nil, fmt.Errorf("statement narrative response must be valid UTF-8")
	}
	trimmed := strings.TrimSpace(text)
	if err := validateExactJSONKeysV1(trimmed, reflectTypeStatementNarrativeModelOutputV1); err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(strings.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	var output statementNarrativeModelOutputV1
	if err := decoder.Decode(&output); err != nil {
		return nil, err
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return nil, err
	}
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "title", value: output.Title},
		{name: "narrative", value: output.Narrative},
	} {
		if field.value == "" || field.value != strings.TrimSpace(field.value) || !utf8.ValidString(field.value) {
			return nil, fmt.Errorf("%s must be non-empty, trimmed valid UTF-8", field.name)
		}
		if strings.ContainsAny(field.value, "\r\n\t`#*_[]<>|") || strings.Contains(field.value, "<!--") || strings.Contains(field.value, "-->") {
			return nil, fmt.Errorf("%s must be one plain-text line", field.name)
		}
		for _, character := range field.value {
			if unicode.IsControl(character) {
				return nil, fmt.Errorf("%s must not contain control characters", field.name)
			}
		}
		if strings.Contains(field.value, StatementSamplesPlaceholder) {
			return nil, fmt.Errorf("%s must not contain the sample placeholder", field.name)
		}
	}
	if len(output.Title) > maxStatementDraftTitleBytesV1 {
		return nil, fmt.Errorf("title exceeds %d bytes", maxStatementDraftTitleBytesV1)
	}
	if len(output.Narrative) > maxStatementDraftNarrativeBytesV1 {
		return nil, fmt.Errorf("narrative exceeds %d bytes", maxStatementDraftNarrativeBytesV1)
	}
	return &output, nil
}

func lintStatementNarrativeAgainstFactsV1(
	output statementNarrativeModelOutputV1,
	facts StatementFactManifestV1,
) error {
	symbols := make(map[string]struct{}, len(facts.Symbols))
	for _, symbol := range facts.Symbols {
		if normalized := strings.ToLower(strings.TrimSpace(symbol.Name)); normalized != "" {
			if normalized == "a" || normalized == "i" {
				continue
			}
			symbols[normalized] = struct{}{}
		}
	}
	for _, outputField := range []struct {
		name  string
		value string
	}{
		{name: "title", value: output.Title},
		{name: "narrative", value: output.Narrative},
	} {
		for _, character := range outputField.value {
			if unicode.IsDigit(character) {
				return fmt.Errorf("%s must not introduce numeric facts", outputField.name)
			}
			if strings.ContainsRune("=<>≤≥+-*/%^", character) {
				return fmt.Errorf("%s must not introduce normative operators", outputField.name)
			}
		}
		lower := strings.ToLower(outputField.value)
		for _, token := range statementNarrativeASCIITokenPatternV1.FindAllString(lower, -1) {
			if _, exists := symbols[token]; exists {
				return fmt.Errorf("%s must not mention SemanticSpec symbol %q", outputField.name, token)
			}
			for _, forbidden := range statementNarrativeNormativeWordTermsV1 {
				if token == forbidden {
					return fmt.Errorf("%s contains normative or meta term %q", outputField.name, forbidden)
				}
			}
		}
		for _, forbidden := range statementNarrativeNormativePhraseTermsV1 {
			if containsStatementNarrativeASCIIWordSequenceV1(lower, forbidden) {
				return fmt.Errorf("%s contains normative or meta term %q", outputField.name, forbidden)
			}
		}
		for _, forbidden := range statementNarrativeNormativeCJKTermsV1 {
			if strings.Contains(outputField.value, forbidden) {
				return fmt.Errorf("%s contains normative or meta term %q", outputField.name, forbidden)
			}
		}
	}
	return nil
}

func containsStatementNarrativeASCIIWordSequenceV1(value string, sequence string) bool {
	for offset := 0; offset <= len(value)-len(sequence); {
		index := strings.Index(value[offset:], sequence)
		if index < 0 {
			return false
		}
		start := offset + index
		end := start + len(sequence)
		leftBounded := start == 0 || !isStatementNarrativeASCIIWordByteV1(value[start-1])
		rightBounded := end == len(value) || !isStatementNarrativeASCIIWordByteV1(value[end])
		if leftBounded && rightBounded {
			return true
		}
		offset = start + 1
	}
	return false
}

func isStatementNarrativeASCIIWordByteV1(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= '0' && value <= '9' || value == '_'
}

func renderStatementDraftMarkdownV1(
	locale string,
	model statementNarrativeModelOutputV1,
	facts StatementFactManifestV1,
) (string, error) {
	labels := statementDraftLabelsV1{
		NarrativeNotice: "Presentation shell only, not a final problem statement. Narrative is non-normative; typed facts below remain in their original wording.",
		Problem:         "Normative Problem Definition",
		Input:           "Normative Input",
		Output:          "Normative Output",
		Objective:       "Normative Objective",
		Constraints:     "Normative Constraints and Relations",
		Answer:          "Normative Answer and Judge Semantics",
	}
	if locale == "zh" {
		labels = statementDraftLabelsV1{
			NarrativeNotice: "仅为展示壳，不是最终题面。叙事不具规范性；下方类型化事实保持原文，不声明语言一致性。",
			Problem:         "规范题意",
			Input:           "规范输入",
			Output:          "规范输出",
			Objective:       "规范目标",
			Constraints:     "规范约束与关系",
			Answer:          "规范答案与判题语义",
		}
	}

	var builder strings.Builder
	builder.WriteString("# ")
	builder.WriteString(escapeStatementDraftMarkdownInlineV1(model.Title))
	builder.WriteString("\n\n> ")
	builder.WriteString(labels.NarrativeNotice)
	builder.WriteString("\n\n")
	builder.WriteString(escapeStatementDraftMarkdownInlineV1(model.Narrative))
	builder.WriteString("\n\n")
	if err := appendStatementDraftJSONSectionV1(&builder, labels.Problem, struct {
		ProblemDefinition string                        `json:"problem_definition"`
		Sections          domain.SemanticSpecSectionsV1 `json:"sections"`
	}{facts.ProblemDefinition, facts.Sections}); err != nil {
		return "", err
	}
	if err := appendStatementDraftJSONSectionV1(&builder, labels.Input, facts.InputGrammar); err != nil {
		return "", err
	}
	if err := appendStatementDraftJSONSectionV1(&builder, labels.Output, facts.OutputGrammar); err != nil {
		return "", err
	}
	if err := appendStatementDraftJSONSectionV1(&builder, labels.Objective, facts.Objective); err != nil {
		return "", err
	}
	if err := appendStatementDraftJSONSectionV1(&builder, labels.Constraints, struct {
		Symbols     []domain.SemanticSymbolV1     `json:"symbols"`
		Constraints []domain.SemanticConstraintV1 `json:"constraints"`
		Relations   []domain.SemanticRelationV1   `json:"relations,omitempty"`
		Topology    domain.SemanticTopologyV1     `json:"topology"`
		Boundaries  []domain.SemanticBoundaryV1   `json:"boundaries"`
	}{facts.Symbols, facts.Constraints, facts.Relations, facts.Topology, facts.Boundaries}); err != nil {
		return "", err
	}
	if err := appendStatementDraftJSONSectionV1(&builder, labels.Answer, struct {
		AnswerSemantics domain.SemanticAnswerSemanticsV1 `json:"answer_semantics"`
		Judge           domain.SemanticJudgeV1           `json:"judge"`
	}{facts.AnswerSemantics, facts.Judge}); err != nil {
		return "", err
	}
	builder.WriteString(StatementSamplesPlaceholder)
	builder.WriteString("\n")
	markdown := builder.String()
	if strings.Count(markdown, StatementSamplesPlaceholder) != 1 {
		return "", fmt.Errorf("statement draft must contain exactly one sample placeholder")
	}
	return markdown, nil
}

type statementDraftLabelsV1 struct {
	NarrativeNotice string
	Problem         string
	Input           string
	Output          string
	Objective       string
	Constraints     string
	Answer          string
}

func appendStatementDraftJSONSectionV1(builder *strings.Builder, heading string, value interface{}) error {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	builder.WriteString("## ")
	builder.WriteString(heading)
	builder.WriteString("\n\n")
	for _, line := range strings.Split(string(encoded), "\n") {
		builder.WriteString("    ")
		builder.WriteString(line)
		builder.WriteByte('\n')
	}
	builder.WriteByte('\n')
	return nil
}

func escapeStatementDraftMarkdownInlineV1(value string) string {
	replacer := strings.NewReplacer(
		"\\", "\\\\", "`", "\\`", "*", "\\*", "_", "\\_", "{", "\\{", "}", "\\}",
		"[", "\\[", "]", "\\]", "<", "\\<", ">", "\\>", "#", "\\#", "|", "\\|",
	)
	return replacer.Replace(value)
}

func statementDraftIsSHA256V1(value string) bool {
	if len(value) != sha256.Size*2 || value != strings.ToLower(value) {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}

func nonRetryableStatementDraftContractErrorV1(err error) error {
	return temporal.NewNonRetryableApplicationError(err.Error(), statementDraftContractErrorTypeV1, err)
}

const statementNarrativeSystemPromptV1 = `You render presentation text for a typed competitive-programming specification.

Return exactly one JSON object with exactly these keys:
{"title":"one plain-text line","narrative":"one plain-text sentence"}

The title and narrative are presentation only. Do not state or paraphrase input, output, constraints, objective, judge rules, algorithms, samples, numeric bounds, complexity, solution hints, oracle plans, failure modes, test intents, or difficulty. Do not use Markdown, code fences, headings, or commentary. Only the title, narrative, and server-owned headings follow presentation_locale. Frozen normative prose is emitted verbatim by the server; no language-equivalence claim is made.`

var (
	reflectTypeAuthoringPlanBundleV1           = reflect.TypeOf(AuthoringPlanBundleV1{})
	reflectTypeStatementNarrativeModelOutputV1 = reflect.TypeOf(statementNarrativeModelOutputV1{})
	statementNarrativeASCIITokenPatternV1      = regexp.MustCompile(`[a-zA-Z_][a-zA-Z0-9_]*`)
	statementNarrativeNormativeWordTermsV1     = []string{
		"input", "output", "constraint", "objective", "judge", "algorithm", "solution", "complexity",
		"sample", "integer", "array", "graph", "compute", "print", "return", "answer", "must", "shall",
		"exactly", "problem", "task", "specification", "proof", "hint", "oracle", "test", "failure",
		"difficulty", "reasoning", "analysis",
	}
	statementNarrativeNormativePhraseTermsV1 = []string{
		"at most", "at least", "time limit", "memory limit", "step by step",
	}
	statementNarrativeNormativeCJKTermsV1 = []string{
		"输入", "输出", "约束", "目标", "判题", "算法", "解法", "复杂度", "样例", "整数", "数组", "图",
		"计算", "求出", "打印", "返回", "答案", "必须", "至多", "至少", "恰好", "时间限制", "内存限制",
		"题目", "任务", "规范", "语义", "字段", "变量", "符号", "证明", "提示", "预言机", "测试",
		"失败", "难度", "推理", "分析", "逐步", "让我", "等等", "重新检查", "修正",
	}
)
