package qualitygate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	regressionSetSchemaVersionV1 = "algoforge.s3-regression-set.v1"
	qualityReplaySchemaVersionV1 = "algoforge.s3-quality-replay.v1"
)

type regressionSetFixtureV1 struct {
	SchemaVersion             string                    `json:"schema_version"`
	DatasetID                 string                    `json:"dataset_id"`
	DatasetRevision           string                    `json:"dataset_revision"`
	RetiredQG01BaselineSHA256 string                    `json:"retired_qg01_baseline_sha256"`
	Cases                     []regressionCaseFixtureV1 `json:"cases"`
}

type regressionCaseFixtureV1 struct {
	CaseID                  string `json:"case_id"`
	RevisionSeed            string `json:"revision_seed"`
	Scenario                string `json:"scenario"`
	ExpectedDecision        string `json:"expected_decision"`
	ExpectedReviewerInvoked bool   `json:"expected_reviewer_invoked"`
}

type qualityReplayFixtureV1 struct {
	SchemaVersion           string `json:"schema_version"`
	ReplayID                string `json:"replay_id"`
	RevisionSeed            string `json:"revision_seed"`
	Scenario                string `json:"scenario"`
	ExpectedDecision        string `json:"expected_decision"`
	ExpectedReviewerInvoked bool   `json:"expected_reviewer_invoked"`
	Purpose                 string `json:"purpose"`
}

func TestFixedS3RegressionSetHas20FreshDeterministicCases(t *testing.T) {
	var fixture regressionSetFixtureV1
	readStrictFixtureV1(t, "s3_regression_20.v1.json", &fixture)
	if fixture.SchemaVersion != regressionSetSchemaVersionV1 || fixture.DatasetID != "algoforge-s3-fixed-20" || fixture.DatasetRevision != "v1-2026-08-26" {
		t.Fatalf("unexpected regression identity: %+v", fixture)
	}
	if !lowerSHA256PatternV1.MatchString(fixture.RetiredQG01BaselineSHA256) {
		t.Fatalf("retired baseline SHA-256 is invalid: %q", fixture.RetiredQG01BaselineSHA256)
	}
	if len(fixture.Cases) != 20 {
		t.Fatalf("regression case count = %d, want 20", len(fixture.Cases))
	}

	caseIDs := make(map[string]struct{}, len(fixture.Cases))
	inputHashes := make(map[string]struct{}, len(fixture.Cases))
	for index, testCase := range fixture.Cases {
		if !strings.HasPrefix(testCase.CaseID, "s3-new-") || testCase.RevisionSeed == "" {
			t.Fatalf("case %d is not a fresh S3 identity: %+v", index, testCase)
		}
		if testCase.ExpectedDecision != DecisionPass || !testCase.ExpectedReviewerInvoked {
			t.Fatalf("fixed regression case %s must exercise a full nine-gate pass, got decision=%q reviewer=%t", testCase.CaseID, testCase.ExpectedDecision, testCase.ExpectedReviewerInvoked)
		}
		if _, duplicate := caseIDs[testCase.CaseID]; duplicate {
			t.Fatalf("duplicate case_id %q", testCase.CaseID)
		}
		caseIDs[testCase.CaseID] = struct{}{}

		evidence := evidenceForFixtureScenarioV1(t, testCase.CaseID, testCase.RevisionSeed, testCase.Scenario)
		invoke, err := ShouldInvokeReviewerV1(PreReviewEvidenceFromS3V1(evidence))
		if err != nil {
			t.Fatalf("case %s pre-review: %v", testCase.CaseID, err)
		}
		if invoke != testCase.ExpectedReviewerInvoked {
			t.Fatalf("case %s reviewer invoked=%t, want %t", testCase.CaseID, invoke, testCase.ExpectedReviewerInvoked)
		}
		audit, err := RecomputeVerdictV1(evidence)
		if err != nil {
			t.Fatalf("case %s recompute: %v", testCase.CaseID, err)
		}
		if audit.Decision != testCase.ExpectedDecision {
			t.Fatalf("case %s decision=%q, want %q", testCase.CaseID, audit.Decision, testCase.ExpectedDecision)
		}
		if len(audit.GateResults) != len(gateOrderV1) || len(audit.BlockingIssues) != 0 {
			t.Fatalf("case %s did not complete all nine gates cleanly: %+v", testCase.CaseID, audit)
		}
		for gateIndex, gate := range audit.GateResults {
			if gate.Gate != gateOrderV1[gateIndex] || gate.Status != GateStatusPass {
				t.Fatalf("case %s gate %d=%+v, want %s/pass", testCase.CaseID, gateIndex, gate, gateOrderV1[gateIndex])
			}
		}
		if _, duplicate := inputHashes[audit.InputSHA256]; duplicate {
			t.Fatalf("case %s reused a prior canonical input hash", testCase.CaseID)
		}
		inputHashes[audit.InputSHA256] = struct{}{}
		if audit.InputSHA256 == fixture.RetiredQG01BaselineSHA256 {
			t.Fatalf("case %s reused the retired QG-01 baseline identity", testCase.CaseID)
		}
	}
}

func TestS3NegativeGateScenariosRemainSeparateFromFixed20(t *testing.T) {
	scenarios := []string{
		"spec_blocked", "sample_blocked", "oracle_blocked", "sanitizer_blocked",
		"boundary_blocked", "manifest_blocked", "review_low", "review_blocker",
		"dedup_check_failed", "dedup_blocked", "hidden_check_failed", "hidden_exposure",
		"hidden_blocked", "approved_true_low", "deterministic_plus_review", "reviewer_blocker_false",
	}
	for _, scenario := range scenarios {
		t.Run(scenario, func(t *testing.T) {
			evidence := evidenceForFixtureScenarioV1(t, "negative-"+scenario, "negative-"+scenario+"-v1", scenario)
			audit, err := RecomputeVerdictV1(evidence)
			if err != nil {
				t.Fatal(err)
			}
			if audit.Decision != DecisionQuarantine {
				t.Fatalf("negative scenario %s decision=%q, want %q", scenario, audit.Decision, DecisionQuarantine)
			}
		})
	}
}

func TestRequiredS3QualityReplayFixtures(t *testing.T) {
	paths := []string{
		"replay_true_defect_pre_review.v1.json",
		"replay_missing_outputs_false_reject.v1.json",
	}
	seen := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		var fixture qualityReplayFixtureV1
		readStrictFixtureV1(t, path, &fixture)
		if fixture.SchemaVersion != qualityReplaySchemaVersionV1 || strings.TrimSpace(fixture.Purpose) == "" {
			t.Fatalf("replay %s has invalid metadata: %+v", path, fixture)
		}
		if _, duplicate := seen[fixture.ReplayID]; duplicate || fixture.ReplayID == "" || fixture.RevisionSeed == "" {
			t.Fatalf("replay %s has duplicate/incomplete identity: %+v", path, fixture)
		}
		seen[fixture.ReplayID] = struct{}{}

		evidence := evidenceForFixtureScenarioV1(t, fixture.ReplayID, fixture.RevisionSeed, fixture.Scenario)
		invoke, err := ShouldInvokeReviewerV1(PreReviewEvidenceFromS3V1(evidence))
		if err != nil {
			t.Fatalf("replay %s pre-review: %v", fixture.ReplayID, err)
		}
		if invoke != fixture.ExpectedReviewerInvoked {
			t.Fatalf("replay %s reviewer invoked=%t, want %t", fixture.ReplayID, invoke, fixture.ExpectedReviewerInvoked)
		}
		audit, err := RecomputeVerdictV1(evidence)
		if err != nil {
			t.Fatalf("replay %s recompute: %v", fixture.ReplayID, err)
		}
		if audit.Decision != fixture.ExpectedDecision {
			t.Fatalf("replay %s decision=%q, want %q", fixture.ReplayID, audit.Decision, fixture.ExpectedDecision)
		}
		if fixture.Scenario == "approved_false_pass" && (audit.ReviewerDeclaredApproved || !audit.ReviewerAdvisoryApproved) {
			t.Fatalf("false-reject replay did not prove server recomputation: %+v", audit)
		}
	}
	if len(seen) != 2 {
		t.Fatalf("quality replay count = %d, want 2", len(seen))
	}
}

func TestS3EvidenceWireContractCannotCarryHiddenPayloads(t *testing.T) {
	evidence := passingEvidenceV1(t, true)
	encoded, _, err := CanonicalInputV1(evidence)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{`"hidden_seed"`, `"hidden_input"`, `"expected_output"`, `"mutant_source"`} {
		if bytes.Contains(encoded, []byte(forbidden)) {
			t.Fatalf("S3 evidence leaked hidden field %s: %s", forbidden, encoded)
		}
	}
}

func evidenceForFixtureScenarioV1(t *testing.T, subjectID, revisionSeed, scenario string) S3EvidenceV1 {
	t.Helper()
	evidence := passingEvidenceV1(t, true)
	evidence.SubjectID = subjectID
	evidence.SubjectRevision = sha256HexV1([]byte(revisionSeed))
	switch scenario {
	case "pass", "pass_second", "dedup_zero_pass":
	case "spec_blocked":
		evidence.SpecLint = blockedGateV1("spec.undefined_variable")
	case "sample_blocked":
		evidence.SampleOutputBinding = blockedGateV1("sample.output_mismatch")
	case "oracle_blocked":
		evidence.OracleDifferential = blockedGateV1("oracle.counterexample")
	case "sanitizer_blocked":
		evidence.Sanitizer = blockedGateV1("sanitizer.undefined_behavior")
	case "boundary_blocked":
		evidence.BoundaryCoverage = blockedGateV1("boundary.max_missing")
	case "manifest_blocked":
		evidence.TestManifest = blockedGateV1("manifest.incomplete")
	case "review_low", "approved_true_low":
		evidence.Reviewer.Assessment.Dimensions.Correctness.Score = ScoreV1(6)
		bindReviewAssessmentV1(t, &evidence)
	case "review_blocker":
		evidence.Reviewer.Assessment.Blockers = []ReviewBlockerV1{fixtureBlockerV1("review.ambiguity", "cas://statement")}
		bindReviewAssessmentV1(t, &evidence)
	case "dedup_check_failed":
		evidence.Dedup.Status = GateStatusCheckFailed
		evidence.Dedup.NeighborCount = nil
	case "dedup_blocked":
		evidence.Dedup.Status = GateStatusBlocked
		evidence.Dedup.Blockers = []ReviewBlockerV1{fixtureBlockerV1("dedup.duplicate", "cas://statement")}
	case "hidden_check_failed":
		evidence.HiddenRegression.Status = GateStatusCheckFailed
	case "hidden_exposure":
		evidence.HiddenRegression.ExposureDetected = true
	case "hidden_blocked":
		evidence.HiddenRegression.Status = GateStatusBlocked
		evidence.HiddenRegression.Blockers = []ReviewBlockerV1{fixtureBlockerV1("hidden.mutant_survived", "cas://solution")}
	case "approved_false_pass":
		evidence.Reviewer.Assessment.Approved = BoolV1(false)
		bindReviewAssessmentV1(t, &evidence)
	case "deterministic_plus_review":
		evidence.SpecLint = blockedGateV1("spec.undefined_variable")
		evidence.Reviewer.Assessment.Dimensions.Clarity.Score = ScoreV1(6)
		bindReviewAssessmentV1(t, &evidence)
	case "reviewer_blocker_false":
		evidence.Reviewer.Assessment.Approved = BoolV1(false)
		evidence.Reviewer.Assessment.Blockers = []ReviewBlockerV1{fixtureBlockerV1("review.correctness", "cas://solution")}
		bindReviewAssessmentV1(t, &evidence)
	default:
		t.Fatalf("unsupported fixture scenario %q", scenario)
	}
	bindFreshFixtureEvidenceV1(&evidence, revisionSeed)
	return evidence
}

func bindFreshFixtureEvidenceV1(evidence *S3EvidenceV1, revisionSeed string) {
	asset := func(gate string) EvidenceAssetV1 {
		digest := sha256HexV1([]byte(revisionSeed + "/" + gate))
		return EvidenceAssetV1{Ref: "cas://s3-regression/" + digest, SHA256: digest}
	}
	evidence.SpecLint.Evidence = asset(GateSpecLint)
	evidence.SampleOutputBinding.Evidence = asset(GateSampleOutputBinding)
	evidence.OracleDifferential.Evidence = asset(GateOracleDifferential)
	evidence.Sanitizer.Evidence = asset(GateSanitizer)
	evidence.BoundaryCoverage.Evidence = asset(GateBoundaryCoverage)
	evidence.TestManifest.Evidence = asset(GateTestManifest)
	evidence.Reviewer.Evidence = asset(GateReviewerSchemaVerdict)
	evidence.Dedup.Evidence = asset(GateDedup)
	evidence.HiddenRegression.Evidence = asset(GateHiddenRegression)
}

func readStrictFixtureV1(t *testing.T, name string, destination any) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	if err := validateSingleJSONValueNoDuplicateKeysV1(data); err != nil {
		t.Fatalf("fixture %s duplicate/trailing validation: %v", name, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		t.Fatalf("decode fixture %s: %v", name, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		t.Fatalf("fixture %s has trailing data: %v", name, err)
	}
}

func (fixture regressionCaseFixtureV1) String() string {
	return fmt.Sprintf("%s/%s", fixture.CaseID, fixture.Scenario)
}
