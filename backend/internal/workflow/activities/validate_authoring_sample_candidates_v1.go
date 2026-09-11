package activities

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	speccontract "github.com/Gingoo-TvT/Qraft/backend/internal/spec"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
)

const (
	ValidateAuthoringSampleCandidatesPayloadVersion = 1
	ValidatedSampleCandidatesBundleSchemaV1         = "algoforge.validated-sample-candidates-bundle.v1"
	ValidatedSampleCandidatesStatusInputsOnlyV1     = "grammar_validated_inputs_only_no_outputs"
	validatedSampleCandidatesDerivationRuleV1       = "algoforge.sample-candidates.bind-and-parse.v1"
	validatedSampleCandidatesContractErrorTypeV1    = "ValidatedSampleCandidatesContractError"
	maxValidatedSampleCandidatesBundleBytesV1       = 1 << 20
)

// ValidateAuthoringSampleCandidatesInput binds the untrusted sample-input
// suggestions in one accepted QG-02A bundle to the original formalizer
// expectations. It intentionally carries no program, output, statement, LLM,
// sandbox, or finalizer input.
type ValidateAuthoringSampleCandidatesInput struct {
	PayloadVersion               int         `json:"payload_version"`
	BundleArtifact               ArtifactRef `json:"bundle_artifact"`
	ExpectedBundleSHA256         string      `json:"expected_bundle_sha256"`
	ExpectedAuthoringInputSHA256 string      `json:"expected_authoring_input_sha256"`
	ExpectedBriefSHA256          string      `json:"expected_brief_sha256"`
	ExpectedSemanticSpecSHA256   string      `json:"expected_semantic_spec_sha256"`
	ExpectedDifficulty           int         `json:"expected_difficulty"`
	RequiredKnowledgePoints      []string    `json:"required_knowledge_points,omitempty"`
	ExpectedSampleCount          int         `json:"expected_sample_count"`
}

// ValidatedSampleCandidateV1 records only a grammar-validated input. It makes
// no claim about a correct program or expected output.
type ValidatedSampleCandidateV1 struct {
	SourceIndex          int                                 `json:"source_index"`
	SemanticSpecSHA256   string                              `json:"semantic_spec_sha256"`
	RawInputSHA256       string                              `json:"raw_input_sha256"`
	CanonicalInput       string                              `json:"canonical_input"`
	CanonicalInputSHA256 string                              `json:"canonical_input_sha256"`
	BindingSHA256        string                              `json:"binding_sha256"`
	Bindings             []speccontract.SampleInputBindingV1 `json:"bindings"`
}

// ValidatedSampleCandidatesBundleV1 is the immutable CAS output of QG-03A.
// Status is intentionally explicit: downstream code must not interpret this
// object as containing program-produced or otherwise validated outputs.
type ValidatedSampleCandidatesBundleV1 struct {
	SchemaVersion          string                       `json:"schema_version"`
	Status                 string                       `json:"status"`
	ValidatorInputSHA256   string                       `json:"validator_input_sha256"`
	AuthoringBundleSHA256  string                       `json:"authoring_bundle_sha256"`
	AuthoringInputSHA256   string                       `json:"authoring_input_sha256"`
	BriefSHA256            string                       `json:"brief_sha256"`
	SemanticSpecSHA256     string                       `json:"semantic_spec_sha256"`
	InputGrammarSHA256     string                       `json:"input_grammar_sha256"`
	ParserRuleVersion      string                       `json:"parser_rule_version"`
	DerivationRule         string                       `json:"derivation_rule"`
	RequestedTestCaseCount int                          `json:"requested_test_case_count"`
	RequestedSampleCount   int                          `json:"requested_sample_count"`
	MinTestCaseCount       int                          `json:"min_test_case_count,omitempty"`
	MaxTestCaseCount       int                          `json:"max_test_case_count,omitempty"`
	AdaptiveTestCaseCount  bool                         `json:"adaptive_test_case_count,omitempty"`
	Candidates             []ValidatedSampleCandidateV1 `json:"candidates"`
}

// ValidateAuthoringSampleCandidatesResult keeps candidate contents and
// bindings out of Temporal history. SourceArtifacts is exactly
// [accepted-authoring-bundle, validated-candidates-bundle].
type ValidateAuthoringSampleCandidatesResult struct {
	PayloadVersion          int            `json:"payload_version"`
	Status                  string         `json:"status"`
	ValidatorInputSHA256    string         `json:"validator_input_sha256"`
	AuthoringBundleSHA256   string         `json:"authoring_bundle_sha256"`
	AuthoringInputSHA256    string         `json:"authoring_input_sha256"`
	BriefSHA256             string         `json:"brief_sha256"`
	SemanticSpecSHA256      string         `json:"semantic_spec_sha256"`
	InputGrammarSHA256      string         `json:"input_grammar_sha256"`
	ParserRuleVersion       string         `json:"parser_rule_version"`
	RequestedTestCaseCount  int            `json:"requested_test_case_count"`
	MinTestCaseCount        int            `json:"min_test_case_count,omitempty"`
	MaxTestCaseCount        int            `json:"max_test_case_count,omitempty"`
	AdaptiveTestCaseCount   bool           `json:"adaptive_test_case_count,omitempty"`
	SampleCount             int            `json:"sample_count"`
	ValidatedBundleSHA256   string         `json:"validated_bundle_sha256"`
	ValidatedBundleArtifact *ArtifactRef   `json:"validated_bundle_artifact"`
	SourceArtifacts         []*ArtifactRef `json:"source_artifacts"`
}

// ValidateAuthoringSampleCandidatesActivityV1 reads one canonical accepted
// authoring bundle, replays the original lint expectations, and parses every
// sample-input candidate with the deterministic SemanticGrammar parser. A
// single parse failure prevents the only CAS write.
func (a *Activities) ValidateAuthoringSampleCandidatesActivityV1(
	ctx context.Context,
	in ValidateAuthoringSampleCandidatesInput,
) (*ValidateAuthoringSampleCandidatesResult, error) {
	if err := validateAuthoringSampleCandidatesInputV1(in); err != nil {
		return nil, nonRetryableValidatedSampleCandidatesErrorV1(err)
	}
	validatorInputSHA, err := canonicalJSONSHA256(in)
	if err != nil {
		return nil, fmt.Errorf("hash sample-candidate validator input: %w", err)
	}
	if a == nil || a.artifacts == nil {
		return nil, fmt.Errorf("artifact store is not configured")
	}

	activity.RecordHeartbeat(ctx, "reading accepted authoring bundle for sample validation")
	bundleBytes, err := a.artifacts.Get(ctx, in.BundleArtifact)
	if err != nil {
		return nil, fmt.Errorf("read authoring bundle for sample validation: %w", err)
	}
	if len(bundleBytes) > maxAuthoringPlanBundleBytes || int64(len(bundleBytes)) != in.BundleArtifact.SizeBytes {
		return nil, nonRetryableValidatedSampleCandidatesErrorV1(
			fmt.Errorf("authoring bundle size is outside the declared QG-02A contract"),
		)
	}
	if sha256Hex(bundleBytes) != in.ExpectedBundleSHA256 {
		return nil, nonRetryableValidatedSampleCandidatesErrorV1(fmt.Errorf("authoring bundle CAS hash mismatch"))
	}
	bundle, err := decodeCanonicalAuthoringBundleV1(bundleBytes)
	if err != nil {
		return nil, nonRetryableValidatedSampleCandidatesErrorV1(
			fmt.Errorf("decode canonical authoring bundle: %w", err),
		)
	}
	expectations := authoringSampleCandidatesRenderExpectationsV1(in)
	if err := validateAcceptedAuthoringBundleForStatementV1(bundle, expectations); err != nil {
		return nil, nonRetryableValidatedSampleCandidatesErrorV1(err)
	}
	minTestCases, maxTestCases, adaptiveTestCases, err := authoringTestCaseBoundsV1(*bundle)
	if err != nil {
		return nil, nonRetryableValidatedSampleCandidatesErrorV1(err)
	}
	if bundle.RequestedSampleCount < 0 || bundle.RequestedSampleCount > maxTestCases {
		return nil, nonRetryableValidatedSampleCandidatesErrorV1(fmt.Errorf(
			"authoring bundle requested sample count must be in [0,%d]",
			maxTestCases,
		))
	}
	if bundle.RequestedSampleCount != in.ExpectedSampleCount {
		return nil, nonRetryableValidatedSampleCandidatesErrorV1(fmt.Errorf(
			"caller expected sample count %d does not match authoring bundle request %d",
			in.ExpectedSampleCount, bundle.RequestedSampleCount,
		))
	}
	if len(bundle.SemanticSpec.SampleInputs) != bundle.RequestedSampleCount {
		return nil, nonRetryableValidatedSampleCandidatesErrorV1(fmt.Errorf(
			"authoring bundle has %d sample-input candidates, want exactly %d",
			len(bundle.SemanticSpec.SampleInputs), bundle.RequestedSampleCount,
		))
	}

	inputGrammarSHA := speccontract.InputGrammarSHA256V1(bundle.SemanticSpec.InputGrammar)
	if !statementDraftIsSHA256V1(inputGrammarSHA) {
		return nil, nonRetryableValidatedSampleCandidatesErrorV1(fmt.Errorf("input grammar hash is invalid"))
	}
	candidates := make([]ValidatedSampleCandidateV1, len(bundle.SemanticSpec.SampleInputs))
	for index, rawInput := range bundle.SemanticSpec.SampleInputs {
		activity.RecordHeartbeat(ctx, fmt.Sprintf("parsing sample-input candidate %d/%d", index+1, len(candidates)))
		parsed, err := speccontract.ParseSampleInputV1(*bundle.SemanticSpec, rawInput)
		if err != nil {
			return nil, nonRetryableValidatedSampleCandidatesErrorV1(
				fmt.Errorf("parse sample-input candidate %d: %w", index, err),
			)
		}
		if err := validateParsedSampleInputReceiptV1(parsed, inputGrammarSHA); err != nil {
			return nil, nonRetryableValidatedSampleCandidatesErrorV1(
				fmt.Errorf("validate sample-input candidate %d receipt: %w", index, err),
			)
		}
		candidates[index] = ValidatedSampleCandidateV1{
			SourceIndex:          index,
			SemanticSpecSHA256:   in.ExpectedSemanticSpecSHA256,
			RawInputSHA256:       sha256Hex([]byte(rawInput)),
			CanonicalInput:       parsed.CanonicalInput,
			CanonicalInputSHA256: parsed.CanonicalSHA256,
			BindingSHA256:        parsed.BindingSHA256,
			Bindings:             cloneSampleInputBindingsV1(parsed.Bindings),
		}
	}

	validatedMinTestCases, validatedMaxTestCases := 0, 0
	if adaptiveTestCases {
		validatedMinTestCases, validatedMaxTestCases = minTestCases, maxTestCases
	}
	validatedBundle := ValidatedSampleCandidatesBundleV1{
		SchemaVersion:          ValidatedSampleCandidatesBundleSchemaV1,
		Status:                 ValidatedSampleCandidatesStatusInputsOnlyV1,
		ValidatorInputSHA256:   validatorInputSHA,
		AuthoringBundleSHA256:  in.ExpectedBundleSHA256,
		AuthoringInputSHA256:   in.ExpectedAuthoringInputSHA256,
		BriefSHA256:            in.ExpectedBriefSHA256,
		SemanticSpecSHA256:     in.ExpectedSemanticSpecSHA256,
		InputGrammarSHA256:     inputGrammarSHA,
		ParserRuleVersion:      speccontract.SampleInputParserRuleVersionV1,
		DerivationRule:         validatedSampleCandidatesDerivationRuleV1,
		RequestedTestCaseCount: bundle.RequestedTestCaseCount,
		RequestedSampleCount:   bundle.RequestedSampleCount,
		MinTestCaseCount:       validatedMinTestCases,
		MaxTestCaseCount:       validatedMaxTestCases,
		AdaptiveTestCaseCount:  adaptiveTestCases,
		Candidates:             candidates,
	}
	validatedBytes, err := json.Marshal(validatedBundle)
	if err != nil {
		return nil, fmt.Errorf("marshal canonical validated sample candidates: %w", err)
	}
	if len(validatedBytes) > maxValidatedSampleCandidatesBundleBytesV1 {
		return nil, nonRetryableValidatedSampleCandidatesErrorV1(fmt.Errorf(
			"validated sample-candidate bundle exceeds %d bytes", maxValidatedSampleCandidatesBundleBytesV1,
		))
	}
	validatedSHA := sha256Hex(validatedBytes)
	metadata := artifactMetadataFromActivity(ctx)
	metadata.ArtifactType = "qg03a_validated_sample_candidates_bundle"
	metadata.SourceType = "derived_validated_sample_candidates"
	metadata.RetentionClass = "workflow_cas_unreviewed"
	provenance := map[string]interface{}{
		"status":                         ValidatedSampleCandidatesStatusInputsOnlyV1,
		"validator_input_sha256":         validatorInputSHA,
		"source_authoring_bundle_sha256": in.ExpectedBundleSHA256,
		"authoring_input_sha256":         in.ExpectedAuthoringInputSHA256,
		"brief_sha256":                   in.ExpectedBriefSHA256,
		"semantic_spec_sha256":           in.ExpectedSemanticSpecSHA256,
		"input_grammar_sha256":           inputGrammarSHA,
		"parser_rule_version":            speccontract.SampleInputParserRuleVersionV1,
		"derivation_rule":                validatedSampleCandidatesDerivationRuleV1,
		"requested_test_case_count":      bundle.RequestedTestCaseCount,
		"requested_sample_count":         bundle.RequestedSampleCount,
		"sample_count":                   len(candidates),
	}
	if adaptiveTestCases {
		provenance["min_test_case_count"] = minTestCases
		provenance["max_test_case_count"] = maxTestCases
		provenance["adaptive_test_case_count"] = true
	}
	metadata.ProvenanceMetadata, err = json.Marshal(provenance)
	if err != nil {
		return nil, fmt.Errorf("encode validated sample-candidate provenance: %w", err)
	}
	validatedArtifact, err := a.putArtifactWithMetadata(ctx, validatedBytes, "application/json", metadata)
	if err != nil {
		return nil, fmt.Errorf("store canonical validated sample candidates: %w", err)
	}
	if validatedArtifact == nil {
		return nil, fmt.Errorf("validated sample-candidate CAS returned a nil artifact ref")
	}
	if err := validatedArtifact.Validate(in.BundleArtifact.Bucket); err != nil {
		return nil, fmt.Errorf("validated sample-candidate CAS returned an invalid artifact ref: %w", err)
	}
	if validatedArtifact.SHA256 != validatedSHA ||
		validatedArtifact.SizeBytes != int64(len(validatedBytes)) ||
		validatedArtifact.ContentType != "application/json" ||
		validatedArtifact.Producer != "ValidateAuthoringSampleCandidatesActivityV1" ||
		validatedArtifact.LLMCallReceipt != nil {
		return nil, fmt.Errorf("validated sample-candidate CAS identity mismatch")
	}

	sourceBundleRef := in.BundleArtifact
	resultMinTestCases, resultMaxTestCases := 0, 0
	if adaptiveTestCases {
		resultMinTestCases, resultMaxTestCases = minTestCases, maxTestCases
	}
	return &ValidateAuthoringSampleCandidatesResult{
		PayloadVersion:          ValidateAuthoringSampleCandidatesPayloadVersion,
		Status:                  ValidatedSampleCandidatesStatusInputsOnlyV1,
		ValidatorInputSHA256:    validatorInputSHA,
		AuthoringBundleSHA256:   in.ExpectedBundleSHA256,
		AuthoringInputSHA256:    in.ExpectedAuthoringInputSHA256,
		BriefSHA256:             in.ExpectedBriefSHA256,
		SemanticSpecSHA256:      in.ExpectedSemanticSpecSHA256,
		InputGrammarSHA256:      inputGrammarSHA,
		ParserRuleVersion:       speccontract.SampleInputParserRuleVersionV1,
		RequestedTestCaseCount:  bundle.RequestedTestCaseCount,
		MinTestCaseCount:        resultMinTestCases,
		MaxTestCaseCount:        resultMaxTestCases,
		AdaptiveTestCaseCount:   adaptiveTestCases,
		SampleCount:             len(candidates),
		ValidatedBundleSHA256:   validatedSHA,
		ValidatedBundleArtifact: validatedArtifact,
		SourceArtifacts:         []*ArtifactRef{&sourceBundleRef, validatedArtifact},
	}, nil
}

// authoringTestCaseBoundsV1 keeps QG-03A independent from the later data
// generator while carrying the same adaptive contract through CAS. A zero
// requested count is valid only when the producer explicitly marks adaptive
// bounds; a bare zero remains invalid for old or tampered bundles.
func authoringTestCaseBoundsV1(bundle AuthoringPlanBundleV1) (min, max int, adaptive bool, err error) {
	if bundle.RequestedTestCaseCount > 0 {
		if bundle.RequestedTestCaseCount > domain.MaxGeneratedTestCases {
			return 0, 0, false, fmt.Errorf(
				"authoring bundle requested test-case count must be in [1,%d]",
				domain.MaxGeneratedTestCases,
			)
		}
		// A v1.3.1 auto-count request carried its upper capacity in
		// RequestedTestCaseCount. v1.3.2 may add the explicit bounds while
		// retaining that field for ancestry, so accept the combined form when
		// the upper bound still agrees with the legacy capacity.
		if bundle.AdaptiveTestCaseCount {
			min, max = bundle.MinTestCaseCount, bundle.MaxTestCaseCount
			if min < domain.MinAdaptiveTestCases || max > domain.MaxAdaptiveTestCases ||
				min > max || max != bundle.RequestedTestCaseCount {
				return 0, 0, false, fmt.Errorf(
					"adaptive authoring test-case range must be within [%d,%d] and match requested capacity %d",
					domain.MinAdaptiveTestCases, domain.MaxAdaptiveTestCases, bundle.RequestedTestCaseCount,
				)
			}
			return min, max, true, nil
		}
		if bundle.MinTestCaseCount != 0 || bundle.MaxTestCaseCount != 0 {
			return 0, 0, false, fmt.Errorf("explicit authoring test-case count must not carry adaptive bounds")
		}
		return bundle.RequestedTestCaseCount, bundle.RequestedTestCaseCount, false, nil
	}
	min, max = bundle.MinTestCaseCount, bundle.MaxTestCaseCount
	if !bundle.AdaptiveTestCaseCount {
		return 0, 0, false, fmt.Errorf("authoring bundle requested test-case count must be explicit or adaptive")
	}
	if min < domain.MinAdaptiveTestCases || max > domain.MaxAdaptiveTestCases || min > max {
		return 0, 0, false, fmt.Errorf(
			"adaptive authoring test-case range must be within [%d,%d]",
			domain.MinAdaptiveTestCases, domain.MaxAdaptiveTestCases,
		)
	}
	return min, max, true, nil
}

func validateAuthoringSampleCandidatesInputV1(in ValidateAuthoringSampleCandidatesInput) error {
	if in.PayloadVersion != ValidateAuthoringSampleCandidatesPayloadVersion {
		return fmt.Errorf("unsupported sample-candidate validator payload version %d", in.PayloadVersion)
	}
	if in.ExpectedSampleCount < 0 || in.ExpectedSampleCount > domain.MaxGeneratedTestCases {
		return fmt.Errorf("expected sample count must be in [0,%d]", domain.MaxGeneratedTestCases)
	}
	_, err := validateStatementRendererInputV1(authoringSampleCandidatesRenderExpectationsV1(in))
	return err
}

func authoringSampleCandidatesRenderExpectationsV1(
	in ValidateAuthoringSampleCandidatesInput,
) RenderStatementFromAuthoringBundleInput {
	return RenderStatementFromAuthoringBundleInput{
		PayloadVersion:               RenderStatementFromAuthoringBundlePayloadVersion,
		BundleArtifact:               in.BundleArtifact,
		ExpectedBundleSHA256:         in.ExpectedBundleSHA256,
		ExpectedAuthoringInputSHA256: in.ExpectedAuthoringInputSHA256,
		ExpectedBriefSHA256:          in.ExpectedBriefSHA256,
		ExpectedSemanticSpecSHA256:   in.ExpectedSemanticSpecSHA256,
		ExpectedDifficulty:           in.ExpectedDifficulty,
		RequiredKnowledgePoints:      append([]string(nil), in.RequiredKnowledgePoints...),
		PresentationLocale:           "en",
	}
}

func validateParsedSampleInputReceiptV1(
	parsed speccontract.ParsedSampleInputV1,
	expectedGrammarSHA string,
) error {
	if parsed.ParserRuleVersion != speccontract.SampleInputParserRuleVersionV1 {
		return fmt.Errorf("parser rule version mismatch")
	}
	if parsed.InputGrammarSHA256 != expectedGrammarSHA {
		return fmt.Errorf("input grammar hash mismatch")
	}
	if !statementDraftIsSHA256V1(parsed.CanonicalSHA256) ||
		parsed.CanonicalSHA256 != sha256Hex([]byte(parsed.CanonicalInput)) {
		return fmt.Errorf("canonical input hash mismatch")
	}
	bindingSHA, err := canonicalJSONSHA256(parsed.Bindings)
	if err != nil {
		return fmt.Errorf("hash parsed bindings: %w", err)
	}
	if !statementDraftIsSHA256V1(parsed.BindingSHA256) || parsed.BindingSHA256 != bindingSHA {
		return fmt.Errorf("binding hash mismatch")
	}
	return nil
}

func cloneSampleInputBindingsV1(bindings []speccontract.SampleInputBindingV1) []speccontract.SampleInputBindingV1 {
	cloned := make([]speccontract.SampleInputBindingV1, len(bindings))
	for index, binding := range bindings {
		cloned[index] = binding
		if binding.Integer != nil {
			value := *binding.Integer
			cloned[index].Integer = &value
		}
		if binding.Integers != nil {
			cloned[index].Integers = append([]int64(nil), binding.Integers...)
		}
	}
	return cloned
}

func nonRetryableValidatedSampleCandidatesErrorV1(err error) error {
	return temporal.NewNonRetryableApplicationError(
		err.Error(), validatedSampleCandidatesContractErrorTypeV1, err,
	)
}
