package qualitygate

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func TestRecomputeVerdictV1ReviewerApprovedCannotOverrideDeterministicBlocker(t *testing.T) {
	evidence := passingEvidenceV1(t, true)
	evidence.SpecLint = blockedGateV1("spec.undefined_variable")

	audit, err := RecomputeVerdictV1(evidence)
	if err != nil {
		t.Fatal(err)
	}
	if audit.Decision != DecisionQuarantine || audit.DeterministicGatesPassed {
		t.Fatalf("audit = %+v, want deterministic quarantine", audit)
	}
	if len(audit.BlockingIssues) == 0 || audit.BlockingIssues[0].Source != IssueSourceDeterministic || audit.BlockingIssues[0].Code != "spec.undefined_variable" {
		t.Fatalf("deterministic blocker did not take priority: %+v", audit.BlockingIssues)
	}
}

func TestCanonicalAuditV1KeepsPassingBlockingIssuesAsEmptyArray(t *testing.T) {
	audit, err := RecomputeVerdictV1(passingEvidenceV1(t, true))
	if err != nil {
		t.Fatal(err)
	}
	encoded, digest, err := CanonicalAuditV1(audit)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(encoded, []byte(`"blocking_issues":[]`)) || bytes.Contains(encoded, []byte(`"blocking_issues":null`)) {
		t.Fatalf("passing audit blocking_issues is not a canonical empty array: %s", encoded)
	}
	encodedAgain, digestAgain, err := CanonicalAuditV1(audit)
	if err != nil || !bytes.Equal(encodedAgain, encoded) || digestAgain != digest {
		t.Fatalf("passing audit canonical bytes/hash changed: digest=%q again=%q err=%v", digest, digestAgain, err)
	}
}

func TestRecomputeVerdictV1ReviewerApprovedIsAdvisoryInBothDirections(t *testing.T) {
	t.Run("declared false does not reject passing evidence", func(t *testing.T) {
		evidence := passingEvidenceV1(t, false)
		audit, err := RecomputeVerdictV1(evidence)
		if err != nil {
			t.Fatal(err)
		}
		if audit.Decision != DecisionPass || audit.ReviewerDeclaredApproved || !audit.ReviewerAdvisoryApproved {
			t.Fatalf("audit = %+v, want server-recomputed pass", audit)
		}
	})

	t.Run("declared true cannot hide low score", func(t *testing.T) {
		evidence := passingEvidenceV1(t, true)
		evidence.Reviewer.Assessment.Dimensions.Correctness.Score = ScoreV1(6)
		bindReviewAssessmentV1(t, &evidence)
		audit, err := RecomputeVerdictV1(evidence)
		if err != nil {
			t.Fatal(err)
		}
		if audit.Decision != DecisionQuarantine || audit.ReviewerAdvisoryApproved {
			t.Fatalf("audit = %+v, want reviewer quarantine", audit)
		}
		if !hasIssueCodeV1(audit, "review.score_below_threshold.correctness") {
			t.Fatalf("audit omitted server score blocker: %+v", audit.BlockingIssues)
		}
	})
}

func TestRecomputeVerdictV1DedupCheckFailedAndHiddenExposureFailClosed(t *testing.T) {
	t.Run("dedup check_failed is not zero neighbors", func(t *testing.T) {
		evidence := passingEvidenceV1(t, true)
		evidence.Dedup.Status = GateStatusCheckFailed
		evidence.Dedup.NeighborCount = nil
		audit, err := RecomputeVerdictV1(evidence)
		if err != nil {
			t.Fatal(err)
		}
		if audit.Decision != DecisionQuarantine || !hasIssueCodeV1(audit, "dedup.check_failed") {
			t.Fatalf("audit = %+v, want dedup check_failed quarantine", audit)
		}
		zero := 0
		evidence.Dedup.NeighborCount = &zero
		if _, err := RecomputeVerdictV1(evidence); err == nil || !strings.Contains(err.Error(), "must not expose") {
			t.Fatalf("check_failed with zero neighbors error = %v", err)
		}
	})

	t.Run("hidden exposure changes gate result to blocked", func(t *testing.T) {
		evidence := passingEvidenceV1(t, true)
		evidence.HiddenRegression.ExposureDetected = true
		audit, err := RecomputeVerdictV1(evidence)
		if err != nil {
			t.Fatal(err)
		}
		if audit.Decision != DecisionQuarantine || !hasIssueCodeV1(audit, "hidden.exposure_detected") {
			t.Fatalf("audit = %+v, want hidden exposure quarantine", audit)
		}
		for _, result := range audit.GateResults {
			if result.Gate == GateHiddenRegression && result.Status != GateStatusBlocked {
				t.Fatalf("hidden gate result = %+v, want blocked", result)
			}
		}
	})
}

func TestShouldInvokeReviewerV1UsesOnlyPreReviewEvidence(t *testing.T) {
	full := passingEvidenceV1(t, true)
	pre := PreReviewEvidenceFromS3V1(full)
	pre.SpecLint = blockedGateV1("spec.undefined_variable")
	invoke, err := ShouldInvokeReviewerV1(pre)
	if err != nil {
		t.Fatal(err)
	}
	if invoke {
		t.Fatal("known deterministic defect invoked reviewer")
	}

	pre.SpecLint = passingGateV1("cas://spec")
	invoke, err = ShouldInvokeReviewerV1(pre)
	if err != nil || !invoke {
		t.Fatalf("passing pre-review evidence invoke=%t err=%v", invoke, err)
	}
}

func TestRecomputeVerdictV1IsByteDeterministicFor100Runs(t *testing.T) {
	evidence := passingEvidenceV1(t, false)
	var expectedBytes []byte
	var expectedSHA string
	for run := 0; run < 100; run++ {
		audit, err := RecomputeVerdictV1(evidence)
		if err != nil {
			t.Fatalf("run %d recompute: %v", run, err)
		}
		encoded, digest, err := CanonicalAuditV1(audit)
		if err != nil {
			t.Fatalf("run %d canonical audit: %v", run, err)
		}
		if run == 0 {
			expectedBytes = encoded
			expectedSHA = digest
			continue
		}
		if digest != expectedSHA || !bytes.Equal(encoded, expectedBytes) {
			t.Fatalf("run %d changed audit bytes/hash: %s/%s", run, digest, expectedSHA)
		}
	}
}

func TestRecomputeVerdictV1PublishedImpliesAllGatesPassFor10000Combinations(t *testing.T) {
	for combination := 0; combination < 10000; combination++ {
		evidence := passingEvidenceV1(t, combination%2 == 0)
		value := combination
		gatePointers := []*GateEvidenceV1{
			&evidence.SpecLint,
			&evidence.SampleOutputBinding,
			&evidence.OracleDifferential,
			&evidence.Sanitizer,
			&evidence.BoundaryCoverage,
			&evidence.TestManifest,
		}
		for gateIndex, gate := range gatePointers {
			state := value % 3
			value /= 3
			switch state {
			case 1:
				*gate = blockedGateV1(fmt.Sprintf("gate%d.blocked", gateIndex))
			case 2:
				*gate = checkFailedGateV1(fmt.Sprintf("cas://gate-%d", gateIndex))
			}
		}
		switch value % 3 {
		case 1:
			evidence.Dedup.Status = GateStatusBlocked
			evidence.Dedup.Blockers = []ReviewBlockerV1{fixtureBlockerV1("dedup.duplicate", "cas://statement")}
		case 2:
			evidence.Dedup.Status = GateStatusCheckFailed
			evidence.Dedup.NeighborCount = nil
		}
		value /= 3
		switch value % 3 {
		case 1:
			evidence.HiddenRegression.Status = GateStatusBlocked
			evidence.HiddenRegression.Blockers = []ReviewBlockerV1{fixtureBlockerV1("hidden.mutant_survived", "cas://solution")}
		case 2:
			evidence.HiddenRegression.Status = GateStatusCheckFailed
		}
		value /= 3
		if value%2 == 1 {
			evidence.HiddenRegression.ExposureDetected = true
		}
		value /= 2
		if value%2 == 1 {
			evidence.Reviewer.Assessment.Dimensions.TestCoverage.Score = ScoreV1(6)
			bindReviewAssessmentV1(t, &evidence)
		}

		audit, err := RecomputeVerdictV1(evidence)
		if err != nil {
			t.Fatalf("combination %d: %v", combination, err)
		}
		if audit.Decision != DecisionPass {
			continue
		}
		if !audit.DeterministicGatesPassed || !audit.ReviewerAdvisoryApproved || len(audit.BlockingIssues) != 0 {
			t.Fatalf("combination %d published without all computed gates: %+v", combination, audit)
		}
		for _, result := range audit.GateResults {
			if result.Status != GateStatusPass {
				t.Fatalf("combination %d published with gate %+v", combination, result)
			}
		}
	}
}

func passingEvidenceV1(t *testing.T, declaredApproved bool) S3EvidenceV1 {
	t.Helper()
	zero := 0
	evidence := S3EvidenceV1{
		SchemaVersion:       S3EvidenceSchemaVersionV1,
		SubjectID:           "s3-fixture",
		SubjectRevision:     strings.Repeat("1", 64),
		SpecLint:            passingGateV1("cas://spec-lint"),
		SampleOutputBinding: passingGateV1("cas://sample-output"),
		OracleDifferential:  passingGateV1("cas://oracle"),
		Sanitizer:           passingGateV1("cas://sanitizer"),
		BoundaryCoverage:    passingGateV1("cas://boundaries"),
		TestManifest:        passingGateV1("cas://test-manifest"),
		Reviewer: ReviewerEvidenceV1{
			Evidence:   fixtureAssetV1("cas://review"),
			Assessment: passingReviewAssessmentV1(declaredApproved),
		},
		Dedup: DedupEvidenceV1{
			Status:        GateStatusPass,
			Evidence:      fixtureAssetV1("cas://dedup"),
			NeighborCount: &zero,
			Blockers:      []ReviewBlockerV1{},
		},
		HiddenRegression: HiddenRegressionEvidenceV1{
			Status:   GateStatusPass,
			Evidence: fixtureAssetV1("cas://hidden-result"),
			Blockers: []ReviewBlockerV1{},
		},
	}
	bindReviewAssessmentV1(t, &evidence)
	return evidence
}

func passingReviewAssessmentV1(declaredApproved bool) ReviewAssessmentV1 {
	return ReviewAssessmentV1{
		SchemaVersion: ReviewAssessmentSchemaVersionV1,
		Approved:      BoolV1(declaredApproved),
		Dimensions: &ReviewDimensionsV1{
			Clarity:               &ReviewDimensionV1{Score: ScoreV1(8), Notes: "clear"},
			Correctness:           &ReviewDimensionV1{Score: ScoreV1(8), Notes: "correct"},
			TestCoverage:          &ReviewDimensionV1{Score: ScoreV1(8), Notes: "covered"},
			DifficultyCalibration: &ReviewDimensionV1{Score: ScoreV1(8), Notes: "calibrated"},
			TagAccuracy:           &ReviewDimensionV1{Score: ScoreV1(8), Notes: "accurate"},
		},
		Blockers: []ReviewBlockerV1{},
	}
}

func passingGateV1(ref string) GateEvidenceV1 {
	return GateEvidenceV1{
		Status:   GateStatusPass,
		Evidence: fixtureAssetV1(ref),
		Blockers: []ReviewBlockerV1{},
	}
}

func blockedGateV1(code string) GateEvidenceV1 {
	return GateEvidenceV1{
		Status:   GateStatusBlocked,
		Evidence: fixtureAssetV1("cas://" + code),
		Blockers: []ReviewBlockerV1{fixtureBlockerV1(code, "cas://responsible-asset")},
	}
}

func checkFailedGateV1(ref string) GateEvidenceV1 {
	return GateEvidenceV1{
		Status:   GateStatusCheckFailed,
		Evidence: fixtureAssetV1(ref),
		Blockers: []ReviewBlockerV1{},
	}
}

func fixtureAssetV1(ref string) EvidenceAssetV1 {
	return EvidenceAssetV1{Ref: ref, SHA256: strings.Repeat("a", 64)}
}

func fixtureBlockerV1(code, responsibleAsset string) ReviewBlockerV1 {
	return ReviewBlockerV1{
		Code:             code,
		ResponsibleAsset: responsibleAsset,
		Witness: ExecutableWitnessV1{
			Runner:     "fixture.runner",
			FixtureRef: "cas://witness/" + code,
			Assertion:  "fixture assertion must pass",
		},
	}
}

func bindReviewAssessmentV1(t *testing.T, evidence *S3EvidenceV1) {
	t.Helper()
	_, digest, err := CanonicalReviewAssessmentV1(evidence.Reviewer.Assessment)
	if err != nil {
		t.Fatalf("bind review assessment: %v", err)
	}
	evidence.Reviewer.AssessmentSHA256 = digest
}

func hasIssueCodeV1(audit AuditV1, code string) bool {
	for _, issue := range audit.BlockingIssues {
		if issue.Code == code {
			return true
		}
	}
	return false
}
