package qualitygate

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

const (
	S3EvidenceSchemaVersionV1        = "algoforge.s3-evidence.v1"
	PreReviewEvidenceSchemaVersionV1 = "algoforge.s3-pre-review-evidence.v1"
	AuditSchemaVersionV1             = "algoforge.s3-quality-audit.v1"
	RuleVersionV1                    = "algoforge.s3-quality-gate.v1"

	GateStatusPass        = "pass"
	GateStatusBlocked     = "blocked"
	GateStatusCheckFailed = "check_failed"

	DecisionPass       = "pass"
	DecisionQuarantine = "quarantine"

	IssueSourceDeterministic = "deterministic"
	IssueSourceReviewer      = "reviewer"
)

const (
	GateSpecLint              = "spec_lint"
	GateSampleOutputBinding   = "sample_output_binding"
	GateOracleDifferential    = "oracle_differential"
	GateSanitizer             = "sanitizer"
	GateBoundaryCoverage      = "boundary_coverage"
	GateTestManifest          = "test_manifest"
	GateReviewerSchemaVerdict = "reviewer_schema_verdict"
	GateDedup                 = "dedup"
	GateHiddenRegression      = "hidden_regression"
)

var lowerSHA256PatternV1 = regexp.MustCompile(`^[a-f0-9]{64}$`)

var gateOrderV1 = []string{
	GateSpecLint,
	GateSampleOutputBinding,
	GateOracleDifferential,
	GateSanitizer,
	GateBoundaryCoverage,
	GateTestManifest,
	GateReviewerSchemaVerdict,
	GateDedup,
	GateHiddenRegression,
}

type EvidenceAssetV1 struct {
	Ref    string `json:"ref"`
	SHA256 string `json:"sha256"`
}

type GateEvidenceV1 struct {
	Status   string            `json:"status"`
	Evidence EvidenceAssetV1   `json:"evidence"`
	Blockers []ReviewBlockerV1 `json:"blockers"`
}

type ReviewerEvidenceV1 struct {
	Evidence         EvidenceAssetV1    `json:"evidence"`
	AssessmentSHA256 string             `json:"assessment_sha256"`
	Assessment       ReviewAssessmentV1 `json:"assessment"`
}

type DedupEvidenceV1 struct {
	Status        string            `json:"status"`
	Evidence      EvidenceAssetV1   `json:"evidence"`
	NeighborCount *int              `json:"neighbor_count,omitempty"`
	Blockers      []ReviewBlockerV1 `json:"blockers"`
}

type HiddenRegressionEvidenceV1 struct {
	Status           string            `json:"status"`
	Evidence         EvidenceAssetV1   `json:"evidence"`
	ExposureDetected bool              `json:"exposure_detected"`
	Blockers         []ReviewBlockerV1 `json:"blockers"`
}

// S3EvidenceV1 names all nine hard gates explicitly. Hidden fixture contents
// are intentionally absent: this contract carries only an opaque evidence ref
// and the minimal executor result.
type S3EvidenceV1 struct {
	SchemaVersion       string                     `json:"schema_version"`
	SubjectID           string                     `json:"subject_id"`
	SubjectRevision     string                     `json:"subject_revision"`
	SpecLint            GateEvidenceV1             `json:"spec_lint"`
	SampleOutputBinding GateEvidenceV1             `json:"sample_output_binding"`
	OracleDifferential  GateEvidenceV1             `json:"oracle_differential"`
	Sanitizer           GateEvidenceV1             `json:"sanitizer"`
	BoundaryCoverage    GateEvidenceV1             `json:"boundary_coverage"`
	TestManifest        GateEvidenceV1             `json:"test_manifest"`
	Reviewer            ReviewerEvidenceV1         `json:"reviewer"`
	Dedup               DedupEvidenceV1            `json:"dedup"`
	HiddenRegression    HiddenRegressionEvidenceV1 `json:"hidden_regression"`
}

// PreReviewEvidenceV1 is intentionally independent of reviewer and hidden
// results. It lets orchestration stop a known deterministic defect without
// first manufacturing or calling a reviewer assessment.
type PreReviewEvidenceV1 struct {
	SchemaVersion       string          `json:"schema_version"`
	SubjectID           string          `json:"subject_id"`
	SubjectRevision     string          `json:"subject_revision"`
	SpecLint            GateEvidenceV1  `json:"spec_lint"`
	SampleOutputBinding GateEvidenceV1  `json:"sample_output_binding"`
	OracleDifferential  GateEvidenceV1  `json:"oracle_differential"`
	Sanitizer           GateEvidenceV1  `json:"sanitizer"`
	BoundaryCoverage    GateEvidenceV1  `json:"boundary_coverage"`
	TestManifest        GateEvidenceV1  `json:"test_manifest"`
	Dedup               DedupEvidenceV1 `json:"dedup"`
}

type BlockingIssueV1 struct {
	Source           string              `json:"source"`
	Gate             string              `json:"gate"`
	Code             string              `json:"code"`
	ResponsibleAsset string              `json:"responsible_asset"`
	Witness          ExecutableWitnessV1 `json:"witness"`
}

type GateResultV1 struct {
	Gate   string `json:"gate"`
	Status string `json:"status"`
}

type AuditV1 struct {
	SchemaVersion            string            `json:"schema_version"`
	RuleVersion              string            `json:"rule_version"`
	RuleSHA256               string            `json:"rule_sha256"`
	InputSHA256              string            `json:"input_sha256"`
	SubjectID                string            `json:"subject_id"`
	SubjectRevision          string            `json:"subject_revision"`
	Decision                 string            `json:"decision"`
	DeterministicGatesPassed bool              `json:"deterministic_gates_passed"`
	ReviewerDeclaredApproved bool              `json:"reviewer_declared_approved"`
	ReviewerAdvisoryApproved bool              `json:"reviewer_advisory_approved"`
	GateResults              []GateResultV1    `json:"gate_results"`
	BlockingIssues           []BlockingIssueV1 `json:"blocking_issues"`
}

type ruleDescriptorV1 struct {
	RuleVersion                    string   `json:"rule_version"`
	GateOrder                      []string `json:"gate_order"`
	ReviewScoreMinimum             int      `json:"review_score_minimum"`
	ReviewScoreMaximum             int      `json:"review_score_maximum"`
	ReviewPassingScore             int      `json:"review_passing_score"`
	ReviewerApprovedIsAdvisory     bool     `json:"reviewer_approved_is_advisory"`
	CheckFailedDecision            string   `json:"check_failed_decision"`
	HiddenExposureDecision         string   `json:"hidden_exposure_decision"`
	DeterministicBlockersTakeFirst bool     `json:"deterministic_blockers_take_priority"`
	MaxRepairRounds                int      `json:"max_repair_rounds"`
}

func (asset EvidenceAssetV1) Validate() error {
	if strings.TrimSpace(asset.Ref) == "" || len(asset.Ref) > 512 {
		return errors.New("evidence ref is required and must not exceed 512 bytes")
	}
	if !lowerSHA256PatternV1.MatchString(asset.SHA256) {
		return fmt.Errorf("evidence sha256 %q is not canonical lowercase SHA-256", asset.SHA256)
	}
	return nil
}

func (gate GateEvidenceV1) validate(name string) error {
	if err := validateGateStatusV1(gate.Status); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	if err := gate.Evidence.Validate(); err != nil {
		return fmt.Errorf("%s evidence: %w", name, err)
	}
	if gate.Blockers == nil {
		return fmt.Errorf("%s blockers must be an explicit array", name)
	}
	if err := validateGateBlockersV1(name, gate.Status, gate.Blockers); err != nil {
		return err
	}
	return nil
}

func validateGateBlockersV1(name, status string, blockers []ReviewBlockerV1) error {
	switch status {
	case GateStatusPass:
		if len(blockers) != 0 {
			return fmt.Errorf("%s pass evidence must not contain blockers", name)
		}
	case GateStatusBlocked:
		if len(blockers) == 0 {
			return fmt.Errorf("%s blocked evidence requires at least one blocker", name)
		}
	case GateStatusCheckFailed:
		if len(blockers) != 0 {
			return fmt.Errorf("%s check_failed evidence must use the server-generated failure blocker", name)
		}
	}
	for index, blocker := range blockers {
		if err := blocker.Validate(); err != nil {
			return fmt.Errorf("%s blocker %d: %w", name, index, err)
		}
	}
	return nil
}

func validateGateStatusV1(status string) error {
	switch status {
	case GateStatusPass, GateStatusBlocked, GateStatusCheckFailed:
		return nil
	default:
		return fmt.Errorf("unsupported gate status %q", status)
	}
}

func validateSubjectIdentityV1(subjectID, subjectRevision string) error {
	if strings.TrimSpace(subjectID) == "" || len(subjectID) > 256 {
		return errors.New("subject_id is required and must not exceed 256 bytes")
	}
	if !lowerSHA256PatternV1.MatchString(subjectRevision) {
		return fmt.Errorf("subject_revision %q is not canonical lowercase SHA-256", subjectRevision)
	}
	return nil
}

func (evidence S3EvidenceV1) Validate() error {
	if evidence.SchemaVersion != S3EvidenceSchemaVersionV1 {
		return fmt.Errorf("S3 evidence schema_version %q, want %q", evidence.SchemaVersion, S3EvidenceSchemaVersionV1)
	}
	if err := validateSubjectIdentityV1(evidence.SubjectID, evidence.SubjectRevision); err != nil {
		return err
	}
	for _, gate := range []struct {
		name  string
		value GateEvidenceV1
	}{
		{name: GateSpecLint, value: evidence.SpecLint},
		{name: GateSampleOutputBinding, value: evidence.SampleOutputBinding},
		{name: GateOracleDifferential, value: evidence.OracleDifferential},
		{name: GateSanitizer, value: evidence.Sanitizer},
		{name: GateBoundaryCoverage, value: evidence.BoundaryCoverage},
		{name: GateTestManifest, value: evidence.TestManifest},
	} {
		if err := gate.value.validate(gate.name); err != nil {
			return err
		}
	}
	if err := evidence.Reviewer.Evidence.Validate(); err != nil {
		return fmt.Errorf("reviewer evidence: %w", err)
	}
	_, assessmentSHA, err := CanonicalReviewAssessmentV1(evidence.Reviewer.Assessment)
	if err != nil {
		return fmt.Errorf("reviewer assessment: %w", err)
	}
	if evidence.Reviewer.AssessmentSHA256 != assessmentSHA {
		return fmt.Errorf("reviewer assessment_sha256 %q does not match canonical assessment %q", evidence.Reviewer.AssessmentSHA256, assessmentSHA)
	}
	if err := validateDedupEvidenceV1(evidence.Dedup); err != nil {
		return err
	}
	if err := validateGateStatusV1(evidence.HiddenRegression.Status); err != nil {
		return fmt.Errorf("hidden_regression: %w", err)
	}
	if err := evidence.HiddenRegression.Evidence.Validate(); err != nil {
		return fmt.Errorf("hidden_regression evidence: %w", err)
	}
	if evidence.HiddenRegression.Blockers == nil {
		return errors.New("hidden_regression blockers must be an explicit array")
	}
	if err := validateGateBlockersV1(GateHiddenRegression, evidence.HiddenRegression.Status, evidence.HiddenRegression.Blockers); err != nil {
		return err
	}
	return nil
}

func validateDedupEvidenceV1(evidence DedupEvidenceV1) error {
	if err := validateGateStatusV1(evidence.Status); err != nil {
		return fmt.Errorf("dedup: %w", err)
	}
	if err := evidence.Evidence.Validate(); err != nil {
		return fmt.Errorf("dedup evidence: %w", err)
	}
	if evidence.Blockers == nil {
		return errors.New("dedup blockers must be an explicit array")
	}
	if err := validateGateBlockersV1(GateDedup, evidence.Status, evidence.Blockers); err != nil {
		return err
	}
	if evidence.Status == GateStatusCheckFailed {
		if evidence.NeighborCount != nil {
			return errors.New("dedup check_failed must not expose a neighbor_count")
		}
	} else if evidence.NeighborCount == nil || *evidence.NeighborCount < 0 {
		return errors.New("successful dedup check requires a non-negative neighbor_count")
	}
	return nil
}

// ShouldInvokeReviewerV1 is the pre-LLM short-circuit used by the true-defect
// replay. Gates 1-6 and dedup are deterministic prerequisites for reviewer use.
func ShouldInvokeReviewerV1(evidence PreReviewEvidenceV1) (bool, error) {
	if evidence.SchemaVersion != PreReviewEvidenceSchemaVersionV1 {
		return false, fmt.Errorf("pre-review schema_version %q, want %q", evidence.SchemaVersion, PreReviewEvidenceSchemaVersionV1)
	}
	if err := validateSubjectIdentityV1(evidence.SubjectID, evidence.SubjectRevision); err != nil {
		return false, err
	}
	for _, gate := range []struct {
		name  string
		value GateEvidenceV1
	}{
		{name: GateSpecLint, value: evidence.SpecLint},
		{name: GateSampleOutputBinding, value: evidence.SampleOutputBinding},
		{name: GateOracleDifferential, value: evidence.OracleDifferential},
		{name: GateSanitizer, value: evidence.Sanitizer},
		{name: GateBoundaryCoverage, value: evidence.BoundaryCoverage},
		{name: GateTestManifest, value: evidence.TestManifest},
	} {
		if err := gate.value.validate(gate.name); err != nil {
			return false, err
		}
		if gate.value.Status != GateStatusPass {
			return false, nil
		}
	}
	if err := validateDedupEvidenceV1(evidence.Dedup); err != nil {
		return false, err
	}
	return evidence.Dedup.Status == GateStatusPass, nil
}

func PreReviewEvidenceFromS3V1(evidence S3EvidenceV1) PreReviewEvidenceV1 {
	return PreReviewEvidenceV1{
		SchemaVersion:       PreReviewEvidenceSchemaVersionV1,
		SubjectID:           evidence.SubjectID,
		SubjectRevision:     evidence.SubjectRevision,
		SpecLint:            evidence.SpecLint,
		SampleOutputBinding: evidence.SampleOutputBinding,
		OracleDifferential:  evidence.OracleDifferential,
		Sanitizer:           evidence.Sanitizer,
		BoundaryCoverage:    evidence.BoundaryCoverage,
		TestManifest:        evidence.TestManifest,
		Dedup:               evidence.Dedup,
	}
}

func RecomputeVerdictV1(evidence S3EvidenceV1) (AuditV1, error) {
	if err := evidence.Validate(); err != nil {
		return AuditV1{}, err
	}
	_, inputSHA, err := CanonicalInputV1(evidence)
	if err != nil {
		return AuditV1{}, err
	}
	_, ruleSHA, err := CanonicalRuleV1()
	if err != nil {
		return AuditV1{}, err
	}

	issues := make([]BlockingIssueV1, 0)
	gateResults := make([]GateResultV1, 0, len(gateOrderV1))
	deterministicPassed := true
	for _, gate := range []struct {
		name  string
		value GateEvidenceV1
	}{
		{name: GateSpecLint, value: evidence.SpecLint},
		{name: GateSampleOutputBinding, value: evidence.SampleOutputBinding},
		{name: GateOracleDifferential, value: evidence.OracleDifferential},
		{name: GateSanitizer, value: evidence.Sanitizer},
		{name: GateBoundaryCoverage, value: evidence.BoundaryCoverage},
		{name: GateTestManifest, value: evidence.TestManifest},
	} {
		gateResults = append(gateResults, GateResultV1{Gate: gate.name, Status: gate.value.Status})
		if gate.value.Status != GateStatusPass {
			deterministicPassed = false
			issues = append(issues, issuesForGateV1(gate.name, gate.value.Status, gate.value.Evidence, gate.value.Blockers)...)
		}
	}

	reviewerAdvisory := reviewAdvisoryApprovedV1(evidence.Reviewer.Assessment)
	reviewerStatus := GateStatusPass
	if !reviewerAdvisory {
		reviewerStatus = GateStatusBlocked
		issues = append(issues, reviewerIssuesV1(evidence.Reviewer)...)
	}
	gateResults = append(gateResults, GateResultV1{Gate: GateReviewerSchemaVerdict, Status: reviewerStatus})

	gateResults = append(gateResults, GateResultV1{Gate: GateDedup, Status: evidence.Dedup.Status})
	if evidence.Dedup.Status != GateStatusPass {
		deterministicPassed = false
		issues = append(issues, issuesForGateV1(GateDedup, evidence.Dedup.Status, evidence.Dedup.Evidence, evidence.Dedup.Blockers)...)
	}

	hiddenStatus := evidence.HiddenRegression.Status
	if evidence.HiddenRegression.ExposureDetected {
		hiddenStatus = GateStatusBlocked
		deterministicPassed = false
		issues = append(issues, BlockingIssueV1{
			Source:           IssueSourceDeterministic,
			Gate:             GateHiddenRegression,
			Code:             "hidden.exposure_detected",
			ResponsibleAsset: evidence.HiddenRegression.Evidence.Ref,
			Witness:          serverWitnessV1(evidence.HiddenRegression.Evidence.Ref, "hidden executor output must not contain seed, input, expected output, or mutant material"),
		})
	}
	gateResults = append(gateResults, GateResultV1{Gate: GateHiddenRegression, Status: hiddenStatus})
	if evidence.HiddenRegression.Status != GateStatusPass {
		deterministicPassed = false
		issues = append(issues, issuesForGateV1(GateHiddenRegression, evidence.HiddenRegression.Status, evidence.HiddenRegression.Evidence, evidence.HiddenRegression.Blockers)...)
	}

	sortBlockingIssuesV1(issues)
	decision := DecisionPass
	if len(issues) > 0 {
		decision = DecisionQuarantine
	}
	return AuditV1{
		SchemaVersion:            AuditSchemaVersionV1,
		RuleVersion:              RuleVersionV1,
		RuleSHA256:               ruleSHA,
		InputSHA256:              inputSHA,
		SubjectID:                evidence.SubjectID,
		SubjectRevision:          evidence.SubjectRevision,
		Decision:                 decision,
		DeterministicGatesPassed: deterministicPassed,
		ReviewerDeclaredApproved: *evidence.Reviewer.Assessment.Approved,
		ReviewerAdvisoryApproved: reviewerAdvisory,
		GateResults:              gateResults,
		BlockingIssues:           issues,
	}, nil
}

func CanonicalInputV1(evidence S3EvidenceV1) ([]byte, string, error) {
	if err := evidence.Validate(); err != nil {
		return nil, "", err
	}
	canonical := normalizeEvidenceV1(evidence)
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return nil, "", fmt.Errorf("encode canonical S3 evidence: %w", err)
	}
	return encoded, sha256HexV1(encoded), nil
}

func CanonicalRuleV1() ([]byte, string, error) {
	descriptor := ruleDescriptorV1{
		RuleVersion:                    RuleVersionV1,
		GateOrder:                      append([]string(nil), gateOrderV1...),
		ReviewScoreMinimum:             ReviewScoreMinimumV1,
		ReviewScoreMaximum:             ReviewScoreMaximumV1,
		ReviewPassingScore:             ReviewPassingScoreV1,
		ReviewerApprovedIsAdvisory:     true,
		CheckFailedDecision:            DecisionQuarantine,
		HiddenExposureDecision:         DecisionQuarantine,
		DeterministicBlockersTakeFirst: true,
		MaxRepairRounds:                MaxRepairRoundsV1,
	}
	encoded, err := json.Marshal(descriptor)
	if err != nil {
		return nil, "", fmt.Errorf("encode quality-gate rule: %w", err)
	}
	return encoded, sha256HexV1(encoded), nil
}

func CanonicalAuditV1(audit AuditV1) ([]byte, string, error) {
	if audit.SchemaVersion != AuditSchemaVersionV1 || audit.RuleVersion != RuleVersionV1 {
		return nil, "", errors.New("audit schema_version or rule_version is invalid")
	}
	if !lowerSHA256PatternV1.MatchString(audit.RuleSHA256) || !lowerSHA256PatternV1.MatchString(audit.InputSHA256) {
		return nil, "", errors.New("audit rule_sha256 and input_sha256 must be canonical")
	}
	if audit.Decision != DecisionPass && audit.Decision != DecisionQuarantine {
		return nil, "", fmt.Errorf("unsupported audit decision %q", audit.Decision)
	}
	canonical := audit
	canonical.GateResults = append([]GateResultV1(nil), audit.GateResults...)
	canonical.BlockingIssues = append([]BlockingIssueV1{}, audit.BlockingIssues...)
	sort.Slice(canonical.GateResults, func(i, j int) bool {
		return gateRankV1(canonical.GateResults[i].Gate) < gateRankV1(canonical.GateResults[j].Gate)
	})
	sortBlockingIssuesV1(canonical.BlockingIssues)
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return nil, "", fmt.Errorf("encode canonical audit: %w", err)
	}
	return encoded, sha256HexV1(encoded), nil
}

func normalizeEvidenceV1(evidence S3EvidenceV1) S3EvidenceV1 {
	canonical := evidence
	canonical.SpecLint.Blockers = sortedReviewBlockersV1(evidence.SpecLint.Blockers)
	canonical.SampleOutputBinding.Blockers = sortedReviewBlockersV1(evidence.SampleOutputBinding.Blockers)
	canonical.OracleDifferential.Blockers = sortedReviewBlockersV1(evidence.OracleDifferential.Blockers)
	canonical.Sanitizer.Blockers = sortedReviewBlockersV1(evidence.Sanitizer.Blockers)
	canonical.BoundaryCoverage.Blockers = sortedReviewBlockersV1(evidence.BoundaryCoverage.Blockers)
	canonical.TestManifest.Blockers = sortedReviewBlockersV1(evidence.TestManifest.Blockers)
	canonical.Reviewer.Assessment.Blockers = sortedReviewBlockersV1(evidence.Reviewer.Assessment.Blockers)
	canonical.Dedup.Blockers = sortedReviewBlockersV1(evidence.Dedup.Blockers)
	canonical.HiddenRegression.Blockers = sortedReviewBlockersV1(evidence.HiddenRegression.Blockers)
	return canonical
}

func sortedReviewBlockersV1(blockers []ReviewBlockerV1) []ReviewBlockerV1 {
	result := append([]ReviewBlockerV1(nil), blockers...)
	if result == nil {
		result = []ReviewBlockerV1{}
	}
	sort.Slice(result, func(i, j int) bool {
		return reviewBlockerSortKeyV1(result[i]) < reviewBlockerSortKeyV1(result[j])
	})
	return result
}

func reviewAdvisoryApprovedV1(assessment ReviewAssessmentV1) bool {
	if len(assessment.Blockers) > 0 {
		return false
	}
	for _, dimension := range reviewDimensionsInOrderV1(assessment) {
		if dimension.value == nil || dimension.value.Score == nil || *dimension.value.Score < ReviewPassingScoreV1 {
			return false
		}
	}
	return true
}

func reviewerIssuesV1(reviewer ReviewerEvidenceV1) []BlockingIssueV1 {
	issues := make([]BlockingIssueV1, 0, len(reviewer.Assessment.Blockers)+5)
	for _, dimension := range reviewDimensionsInOrderV1(reviewer.Assessment) {
		if dimension.value != nil && dimension.value.Score != nil && *dimension.value.Score < ReviewPassingScoreV1 {
			issues = append(issues, BlockingIssueV1{
				Source:           IssueSourceReviewer,
				Gate:             GateReviewerSchemaVerdict,
				Code:             "review.score_below_threshold." + dimension.name,
				ResponsibleAsset: reviewer.Evidence.Ref,
				Witness:          serverWitnessV1(reviewer.Evidence.Ref, fmt.Sprintf("%s score must be at least %d", dimension.name, ReviewPassingScoreV1)),
			})
		}
	}
	for _, blocker := range reviewer.Assessment.Blockers {
		issues = append(issues, BlockingIssueV1{
			Source:           IssueSourceReviewer,
			Gate:             GateReviewerSchemaVerdict,
			Code:             blocker.Code,
			ResponsibleAsset: blocker.ResponsibleAsset,
			Witness:          blocker.Witness,
		})
	}
	return issues
}

func reviewDimensionsInOrderV1(assessment ReviewAssessmentV1) []struct {
	name  string
	value *ReviewDimensionV1
} {
	if assessment.Dimensions == nil {
		return nil
	}
	return []struct {
		name  string
		value *ReviewDimensionV1
	}{
		{name: "clarity", value: assessment.Dimensions.Clarity},
		{name: "correctness", value: assessment.Dimensions.Correctness},
		{name: "test_coverage", value: assessment.Dimensions.TestCoverage},
		{name: "difficulty_calibration", value: assessment.Dimensions.DifficultyCalibration},
		{name: "tag_accuracy", value: assessment.Dimensions.TagAccuracy},
	}
}

func issuesForGateV1(name, status string, asset EvidenceAssetV1, blockers []ReviewBlockerV1) []BlockingIssueV1 {
	if status == GateStatusCheckFailed {
		return []BlockingIssueV1{{
			Source:           IssueSourceDeterministic,
			Gate:             name,
			Code:             name + ".check_failed",
			ResponsibleAsset: asset.Ref,
			Witness:          serverWitnessV1(asset.Ref, "gate evidence must be recomputed successfully"),
		}}
	}
	issues := make([]BlockingIssueV1, 0, len(blockers))
	for _, blocker := range blockers {
		issues = append(issues, BlockingIssueV1{
			Source:           IssueSourceDeterministic,
			Gate:             name,
			Code:             blocker.Code,
			ResponsibleAsset: blocker.ResponsibleAsset,
			Witness:          blocker.Witness,
		})
	}
	return issues
}

func serverWitnessV1(ref, assertion string) ExecutableWitnessV1 {
	return ExecutableWitnessV1{
		Runner:     "algoforge.qualitygate.v1",
		FixtureRef: ref,
		Assertion:  assertion,
	}
}

func sortBlockingIssuesV1(issues []BlockingIssueV1) {
	sort.Slice(issues, func(i, j int) bool {
		leftPriority := 0
		if issues[i].Source == IssueSourceReviewer {
			leftPriority = 1
		}
		rightPriority := 0
		if issues[j].Source == IssueSourceReviewer {
			rightPriority = 1
		}
		if leftPriority != rightPriority {
			return leftPriority < rightPriority
		}
		leftRank, rightRank := gateRankV1(issues[i].Gate), gateRankV1(issues[j].Gate)
		if leftRank != rightRank {
			return leftRank < rightRank
		}
		return blockingIssueSortKeyV1(issues[i]) < blockingIssueSortKeyV1(issues[j])
	})
}

func blockingIssueSortKeyV1(issue BlockingIssueV1) string {
	return strings.Join([]string{
		issue.Source,
		issue.Gate,
		issue.Code,
		issue.ResponsibleAsset,
		issue.Witness.Runner,
		issue.Witness.FixtureRef,
		issue.Witness.Assertion,
	}, "\x00")
}

func gateRankV1(gate string) int {
	for index, candidate := range gateOrderV1 {
		if gate == candidate {
			return index
		}
	}
	return len(gateOrderV1)
}
