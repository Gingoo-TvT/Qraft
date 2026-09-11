package activities

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	speccontract "github.com/Gingoo-TvT/Qraft/backend/internal/spec"
	"go.temporal.io/sdk/testsuite"
)

type validatedSampleCandidatesFixture struct {
	*statementDraftFixture
	input ValidateAuthoringSampleCandidatesInput
}

func newValidatedSampleCandidatesFixture(
	t *testing.T,
	samples []string,
) *validatedSampleCandidatesFixture {
	t.Helper()
	statementFixture := newStatementDraftFixture(t, samples)
	authoringInput := validAuthoringActivityInput()
	authoringInput.Params.TestDataConfig.NumSamples = len(samples)
	authoringInputSHA, err := canonicalJSONSHA256(authoringInput)
	if err != nil {
		t.Fatal(err)
	}
	statementFixture.bundle.InputSHA256 = authoringInputSHA
	statementFixture.bundle.RequestedTestCaseCount = authoringInput.Params.TestDataConfig.NumTestCases
	statementFixture.bundle.RequestedSampleCount = authoringInput.Params.TestDataConfig.NumSamples
	statementFixture.input.ExpectedAuthoringInputSHA256 = authoringInputSHA
	statementFixture.replaceBundle(t)

	return &validatedSampleCandidatesFixture{
		statementDraftFixture: statementFixture,
		input: ValidateAuthoringSampleCandidatesInput{
			PayloadVersion:               ValidateAuthoringSampleCandidatesPayloadVersion,
			BundleArtifact:               statementFixture.input.BundleArtifact,
			ExpectedBundleSHA256:         statementFixture.input.ExpectedBundleSHA256,
			ExpectedAuthoringInputSHA256: statementFixture.input.ExpectedAuthoringInputSHA256,
			ExpectedBriefSHA256:          statementFixture.input.ExpectedBriefSHA256,
			ExpectedSemanticSpecSHA256:   statementFixture.input.ExpectedSemanticSpecSHA256,
			ExpectedDifficulty:           statementFixture.input.ExpectedDifficulty,
			RequiredKnowledgePoints:      append([]string(nil), statementFixture.input.RequiredKnowledgePoints...),
			ExpectedSampleCount:          len(samples),
		},
	}
}

func executeValidatedSampleCandidatesActivity(
	t *testing.T,
	activities *Activities,
	input ValidateAuthoringSampleCandidatesInput,
) (*ValidateAuthoringSampleCandidatesResult, error) {
	t.Helper()
	var suite testsuite.WorkflowTestSuite
	environment := suite.NewTestActivityEnvironment()
	environment.RegisterActivity(activities.ValidateAuthoringSampleCandidatesActivityV1)
	encoded, err := environment.ExecuteActivity(
		activities.ValidateAuthoringSampleCandidatesActivityV1,
		input,
	)
	if err != nil {
		return nil, err
	}
	var result ValidateAuthoringSampleCandidatesResult
	if err := encoded.Get(&result); err != nil {
		t.Fatalf("decode validated sample-candidate result: %v", err)
	}
	return &result, nil
}

func TestValidateAuthoringSampleCandidatesActivityV1AcceptsZeroOneAndManyAtomically(t *testing.T) {
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
			fixture := newValidatedSampleCandidatesFixture(t, variant.samples)
			inputBefore := fixture.input
			inputBefore.RequiredKnowledgePoints = append([]string(nil), fixture.input.RequiredKnowledgePoints...)
			sourceBytesBefore := append([]byte(nil), fixture.store.objects[fixture.input.BundleArtifact.SHA256]...)
			provenance := &captureProvenanceRecorder{}
			activities := &Activities{
				deps:      &Dependencies{ProvenanceRecorder: provenance},
				artifacts: fixture.store,
			}

			result, err := executeValidatedSampleCandidatesActivity(t, activities, fixture.input)
			if err != nil {
				t.Fatalf("validate sample candidates: %v", err)
			}
			if !reflect.DeepEqual(fixture.input, inputBefore) {
				t.Fatalf("activity mutated caller input: got=%+v want=%+v", fixture.input, inputBefore)
			}
			if !bytes.Equal(fixture.store.objects[fixture.input.BundleArtifact.SHA256], sourceBytesBefore) {
				t.Fatal("activity mutated the immutable accepted authoring bundle")
			}
			if len(fixture.store.puts) != 1 {
				t.Fatalf("CAS puts=%d, want exactly 1 after every candidate passed", len(fixture.store.puts))
			}

			validatorInputSHA, err := canonicalJSONSHA256(fixture.input)
			if err != nil {
				t.Fatal(err)
			}
			inputGrammarSHA := speccontract.InputGrammarSHA256V1(fixture.bundle.SemanticSpec.InputGrammar)
			if result.PayloadVersion != ValidateAuthoringSampleCandidatesPayloadVersion ||
				result.Status != ValidatedSampleCandidatesStatusInputsOnlyV1 ||
				result.ValidatorInputSHA256 != validatorInputSHA ||
				result.AuthoringBundleSHA256 != fixture.input.ExpectedBundleSHA256 ||
				result.AuthoringInputSHA256 != fixture.input.ExpectedAuthoringInputSHA256 ||
				result.BriefSHA256 != fixture.input.ExpectedBriefSHA256 ||
				result.SemanticSpecSHA256 != fixture.input.ExpectedSemanticSpecSHA256 ||
				result.InputGrammarSHA256 != inputGrammarSHA ||
				result.ParserRuleVersion != speccontract.SampleInputParserRuleVersionV1 ||
				result.RequestedTestCaseCount != fixture.bundle.RequestedTestCaseCount ||
				result.SampleCount != len(variant.samples) {
				t.Fatalf("result bindings are incomplete: %+v", result)
			}
			if result.ValidatedBundleArtifact == nil ||
				result.ValidatedBundleSHA256 != result.ValidatedBundleArtifact.SHA256 ||
				len(result.SourceArtifacts) != 2 ||
				!reflect.DeepEqual(*result.SourceArtifacts[0], fixture.input.BundleArtifact) ||
				!reflect.DeepEqual(result.SourceArtifacts[1], result.ValidatedBundleArtifact) {
				t.Fatalf("result CAS ancestry is incomplete: %+v", result)
			}
			if result.ValidatedBundleArtifact.ContentType != "application/json" ||
				result.ValidatedBundleArtifact.Producer != "ValidateAuthoringSampleCandidatesActivityV1" ||
				result.ValidatedBundleArtifact.LLMCallReceipt != nil {
				t.Fatalf("validated bundle artifact identity is invalid: %+v", result.ValidatedBundleArtifact)
			}
			if err := result.ValidatedBundleArtifact.Validate("fixture"); err != nil {
				t.Fatalf("validated bundle artifact ref: %v", err)
			}

			validatedBytes, err := fixture.store.Get(context.Background(), *result.ValidatedBundleArtifact)
			if err != nil {
				t.Fatal(err)
			}
			if len(validatedBytes) > maxValidatedSampleCandidatesBundleBytesV1 ||
				result.ValidatedBundleSHA256 != sha256Hex(validatedBytes) {
				t.Fatalf("stored validated bundle hash/size mismatch: result=%+v bytes=%d", result, len(validatedBytes))
			}
			var validatedBundle ValidatedSampleCandidatesBundleV1
			if err := json.Unmarshal(validatedBytes, &validatedBundle); err != nil {
				t.Fatalf("decode validated sample-candidate bundle: %v", err)
			}
			reencoded, err := json.Marshal(validatedBundle)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(reencoded, validatedBytes) {
				t.Fatal("validated sample-candidate bundle is not canonical JSON")
			}
			if validatedBundle.SchemaVersion != ValidatedSampleCandidatesBundleSchemaV1 ||
				validatedBundle.Status != ValidatedSampleCandidatesStatusInputsOnlyV1 ||
				validatedBundle.ValidatorInputSHA256 != result.ValidatorInputSHA256 ||
				validatedBundle.AuthoringBundleSHA256 != result.AuthoringBundleSHA256 ||
				validatedBundle.AuthoringInputSHA256 != result.AuthoringInputSHA256 ||
				validatedBundle.BriefSHA256 != result.BriefSHA256 ||
				validatedBundle.SemanticSpecSHA256 != result.SemanticSpecSHA256 ||
				validatedBundle.InputGrammarSHA256 != result.InputGrammarSHA256 ||
				validatedBundle.ParserRuleVersion != result.ParserRuleVersion ||
				validatedBundle.DerivationRule != validatedSampleCandidatesDerivationRuleV1 ||
				validatedBundle.RequestedTestCaseCount != fixture.bundle.RequestedTestCaseCount ||
				validatedBundle.RequestedSampleCount != len(variant.samples) ||
				len(validatedBundle.Candidates) != len(variant.samples) {
				t.Fatalf("stored bundle bindings are incomplete: %+v", validatedBundle)
			}
			if len(variant.samples) == 0 && !bytes.Contains(validatedBytes, []byte(`"candidates":[]`)) {
				t.Fatalf("zero-candidate bundle must encode an empty array: %s", validatedBytes)
			}

			for index, rawInput := range variant.samples {
				parsed, err := speccontract.ParseSampleInputV1(*fixture.bundle.SemanticSpec, rawInput)
				if err != nil {
					t.Fatalf("independently parse candidate %d: %v", index, err)
				}
				candidate := validatedBundle.Candidates[index]
				if candidate.SourceIndex != index ||
					candidate.SemanticSpecSHA256 != fixture.input.ExpectedSemanticSpecSHA256 ||
					candidate.RawInputSHA256 != sha256Hex([]byte(rawInput)) ||
					candidate.CanonicalInput != parsed.CanonicalInput ||
					candidate.CanonicalInputSHA256 != sha256Hex([]byte(candidate.CanonicalInput)) ||
					candidate.CanonicalInputSHA256 != parsed.CanonicalSHA256 ||
					candidate.BindingSHA256 != parsed.BindingSHA256 ||
					!reflect.DeepEqual(candidate.Bindings, parsed.Bindings) {
					t.Fatalf("candidate %d bindings are incomplete: got=%+v parsed=%+v", index, candidate, parsed)
				}
				bindingSHA, err := canonicalJSONSHA256(candidate.Bindings)
				if err != nil {
					t.Fatal(err)
				}
				if candidate.BindingSHA256 != bindingSHA {
					t.Fatalf("candidate %d binding hash mismatch", index)
				}
			}

			if len(provenance.records) != 1 ||
				provenance.records[0].ArtifactType != "qg03a_validated_sample_candidates_bundle" ||
				provenance.records[0].SourceType != "derived_validated_sample_candidates" ||
				provenance.records[0].ContentHash != result.ValidatedBundleSHA256 {
				t.Fatalf("validated bundle provenance is incomplete: %+v", provenance.records)
			}
			var provenanceMetadata map[string]interface{}
			if err := json.Unmarshal(provenance.records[0].Metadata, &provenanceMetadata); err != nil {
				t.Fatalf("decode validated bundle provenance: %v", err)
			}
			for key, want := range map[string]string{
				"status":                         ValidatedSampleCandidatesStatusInputsOnlyV1,
				"validator_input_sha256":         result.ValidatorInputSHA256,
				"source_authoring_bundle_sha256": result.AuthoringBundleSHA256,
				"authoring_input_sha256":         result.AuthoringInputSHA256,
				"brief_sha256":                   result.BriefSHA256,
				"semantic_spec_sha256":           result.SemanticSpecSHA256,
				"input_grammar_sha256":           result.InputGrammarSHA256,
				"parser_rule_version":            result.ParserRuleVersion,
				"derivation_rule":                validatedSampleCandidatesDerivationRuleV1,
			} {
				if provenanceMetadata[key] != want {
					t.Fatalf("provenance %s=%v, want %s", key, provenanceMetadata[key], want)
				}
			}
			for key, want := range map[string]int{
				"requested_test_case_count": fixture.bundle.RequestedTestCaseCount,
				"requested_sample_count":    fixture.bundle.RequestedSampleCount,
				"sample_count":              len(variant.samples),
			} {
				if provenanceMetadata[key] != float64(want) {
					t.Fatalf("provenance %s=%v, want %d", key, provenanceMetadata[key], want)
				}
			}

			sourceRefBefore := fixture.input.BundleArtifact
			result.SourceArtifacts[0].WorkflowID = "mutated-result-only"
			if !reflect.DeepEqual(fixture.input.BundleArtifact, sourceRefBefore) {
				t.Fatal("result source artifact aliases caller input")
			}
		})
	}
}

func TestValidateAuthoringSampleCandidatesActivityV1FailsBeforePut(t *testing.T) {
	tests := []struct {
		name    string
		samples []string
		mutate  func(*testing.T, *validatedSampleCandidatesFixture)
		want    string
	}{
		{
			name:    "caller count tamper",
			samples: []string{"1\n5\n"},
			mutate: func(_ *testing.T, fixture *validatedSampleCandidatesFixture) {
				fixture.input.ExpectedSampleCount++
			},
			want: "does not match authoring bundle request",
		},
		{
			name:    "bundle count tamper",
			samples: []string{"1\n5\n"},
			mutate: func(t *testing.T, fixture *validatedSampleCandidatesFixture) {
				fixture.bundle.RequestedSampleCount++
				fixture.replaceBundle(t)
				fixture.input.BundleArtifact = fixture.statementDraftFixture.input.BundleArtifact
				fixture.input.ExpectedBundleSHA256 = fixture.statementDraftFixture.input.ExpectedBundleSHA256
			},
			want: "does not match authoring bundle request",
		},
		{
			name:    "bundle and caller count exceed candidate list",
			samples: []string{"1\n5\n"},
			mutate: func(t *testing.T, fixture *validatedSampleCandidatesFixture) {
				fixture.bundle.RequestedSampleCount++
				fixture.replaceBundle(t)
				fixture.input.BundleArtifact = fixture.statementDraftFixture.input.BundleArtifact
				fixture.input.ExpectedBundleSHA256 = fixture.statementDraftFixture.input.ExpectedBundleSHA256
				fixture.input.ExpectedSampleCount++
			},
			want: "sample-input candidates",
		},
		{
			name:    "invalid bundle total count",
			samples: []string{"1\n5\n"},
			mutate: func(t *testing.T, fixture *validatedSampleCandidatesFixture) {
				fixture.bundle.RequestedTestCaseCount = domain.MaxGeneratedTestCases + 1
				fixture.bundle.MinTestCaseCount = 0
				fixture.bundle.MaxTestCaseCount = 0
				fixture.bundle.AdaptiveTestCaseCount = false
				fixture.replaceBundle(t)
				fixture.input.BundleArtifact = fixture.statementDraftFixture.input.BundleArtifact
				fixture.input.ExpectedBundleSHA256 = fixture.statementDraftFixture.input.ExpectedBundleSHA256
			},
			want: "requested test-case count",
		},
		{
			name:    "CAS read failure",
			samples: []string{"1\n5\n"},
			mutate: func(_ *testing.T, fixture *validatedSampleCandidatesFixture) {
				fixture.store.getErr = errors.New("injected sample-candidate CAS read failure")
			},
			want: "injected sample-candidate CAS read failure",
		},
		{
			name:    "CAS object tamper",
			samples: []string{"1\n5\n"},
			mutate: func(_ *testing.T, fixture *validatedSampleCandidatesFixture) {
				fixture.store.objects[fixture.input.BundleArtifact.SHA256] = []byte(`{}`)
			},
			want: "CAS identity mismatch",
		},
		{
			name:    "stored lint expectation mismatch",
			samples: []string{"1\n5\n"},
			mutate: func(_ *testing.T, fixture *validatedSampleCandidatesFixture) {
				fixture.input.ExpectedDifficulty += 100
			},
			want: "lint receipt does not match",
		},
		{
			name:    "later candidate parser failure is all or nothing",
			samples: []string{"1\n5\n", "2\n7\n"},
			mutate:  func(_ *testing.T, _ *validatedSampleCandidatesFixture) {},
			want:    "parse sample-input candidate 1",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newValidatedSampleCandidatesFixture(t, testCase.samples)
			testCase.mutate(t, fixture)
			inputBefore := fixture.input
			inputBefore.RequiredKnowledgePoints = append([]string(nil), fixture.input.RequiredKnowledgePoints...)
			sourceBytesBefore := append([]byte(nil), fixture.store.objects[fixture.input.BundleArtifact.SHA256]...)
			provenance := &captureProvenanceRecorder{}
			activities := &Activities{
				deps:      &Dependencies{ProvenanceRecorder: provenance},
				artifacts: fixture.store,
			}

			if _, err := executeValidatedSampleCandidatesActivity(t, activities, fixture.input); err == nil {
				t.Fatal("invalid sample-candidate validation input was accepted")
			} else if !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("error %q does not contain %q", err, testCase.want)
			}
			if len(fixture.store.puts) != 0 {
				t.Fatalf("CAS puts=%d, want 0 on pre-store failure", len(fixture.store.puts))
			}
			if len(provenance.records) != 0 {
				t.Fatalf("provenance records=%d, want 0 on pre-store failure", len(provenance.records))
			}
			if !reflect.DeepEqual(fixture.input, inputBefore) {
				t.Fatalf("failed activity mutated caller input: got=%+v want=%+v", fixture.input, inputBefore)
			}
			if !bytes.Equal(fixture.store.objects[fixture.input.BundleArtifact.SHA256], sourceBytesBefore) {
				t.Fatal("failed activity mutated the source CAS object")
			}
		})
	}
}

func TestValidateAuthoringSampleCandidatesActivityV1IsDeterministic(t *testing.T) {
	run := func(t *testing.T) (*ValidateAuthoringSampleCandidatesResult, []byte) {
		t.Helper()
		fixture := newValidatedSampleCandidatesFixture(t, []string{"1\n5\n", "3\n1 2 3\n"})
		activities := &Activities{
			deps:      &Dependencies{ProvenanceRecorder: &captureProvenanceRecorder{}},
			artifacts: fixture.store,
		}
		result, err := executeValidatedSampleCandidatesActivity(t, activities, fixture.input)
		if err != nil {
			t.Fatal(err)
		}
		data, err := fixture.store.Get(context.Background(), *result.ValidatedBundleArtifact)
		if err != nil {
			t.Fatal(err)
		}
		return result, data
	}

	first, firstBytes := run(t)
	second, secondBytes := run(t)
	if first.ValidatorInputSHA256 != second.ValidatorInputSHA256 ||
		first.ValidatedBundleSHA256 != second.ValidatedBundleSHA256 ||
		!bytes.Equal(firstBytes, secondBytes) {
		t.Fatalf("identical inputs produced different identities: first=%+v second=%+v", first, second)
	}
}

func TestValidateParsedSampleInputReceiptV1RejectsHashTampering(t *testing.T) {
	fixture := newValidatedSampleCandidatesFixture(t, []string{"3\n1 2 3\n"})
	parsed, err := speccontract.ParseSampleInputV1(*fixture.bundle.SemanticSpec, fixture.bundle.SemanticSpec.SampleInputs[0])
	if err != nil {
		t.Fatal(err)
	}
	grammarSHA := speccontract.InputGrammarSHA256V1(fixture.bundle.SemanticSpec.InputGrammar)
	if err := validateParsedSampleInputReceiptV1(parsed, grammarSHA); err != nil {
		t.Fatalf("valid parser receipt rejected: %v", err)
	}

	tests := map[string]func(*speccontract.ParsedSampleInputV1){
		"parser rule":    func(value *speccontract.ParsedSampleInputV1) { value.ParserRuleVersion += "-tampered" },
		"grammar hash":   func(value *speccontract.ParsedSampleInputV1) { value.InputGrammarSHA256 = strings.Repeat("f", 64) },
		"canonical hash": func(value *speccontract.ParsedSampleInputV1) { value.CanonicalSHA256 = strings.Repeat("f", 64) },
		"binding hash":   func(value *speccontract.ParsedSampleInputV1) { value.BindingSHA256 = strings.Repeat("f", 64) },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			tampered := parsed
			mutate(&tampered)
			if err := validateParsedSampleInputReceiptV1(tampered, grammarSHA); err == nil {
				t.Fatal("tampered parser receipt was accepted")
			}
		})
	}
}

func TestCloneSampleInputBindingsV1DeepCopiesValues(t *testing.T) {
	integer := int64(7)
	source := []speccontract.SampleInputBindingV1{
		{Symbol: "n", Kind: "integer", Integer: &integer},
		{Symbol: "a", Kind: "integer_sequence", Integers: []int64{1, 2, 3}},
	}
	cloned := cloneSampleInputBindingsV1(source)
	*cloned[0].Integer = 9
	cloned[1].Integers[0] = 8
	if *source[0].Integer != 7 || source[1].Integers[0] != 1 {
		t.Fatalf("binding clone aliases source values: source=%+v clone=%+v", source, cloned)
	}
}
