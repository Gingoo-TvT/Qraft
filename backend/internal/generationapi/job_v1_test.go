package generationapi

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
)

func TestJobsV1CanonicalPayloadAndPrincipalScopedIdentity(t *testing.T) {
	left := loadFixtureRequest(t)
	right := loadFixtureRequest(t)
	right.Domain.KnowledgePoints = []string{" prefix-sum ", "dp"}
	right.Languages = []string{" cpp "}
	right.Output.Formats = []string{" algoforge "}
	right.Domain.Name = " Competitive_Programming "
	right.Domain.Level = " Algorithm "

	_, leftHash, err := left.CanonicalPayload()
	if err != nil {
		t.Fatal(err)
	}
	_, rightHash, err := right.CanonicalPayload()
	if err != nil {
		t.Fatal(err)
	}
	if leftHash != rightHash {
		t.Fatalf("canonical equivalents produced different hashes: %s != %s", leftHash, rightHash)
	}

	right.Difficulty.Rating = 1900
	_, changedHash, err := right.CanonicalPayload()
	if err != nil {
		t.Fatal(err)
	}
	if changedHash == leftHash {
		t.Fatal("a material request change reused the canonical payload hash")
	}

	jobA, scopeA, err := JobIDForIdempotencyKey("user:a", "retry-key")
	if err != nil {
		t.Fatal(err)
	}
	jobAReplay, scopeAReplay, err := JobIDForIdempotencyKey("user:a", "retry-key")
	if err != nil {
		t.Fatal(err)
	}
	jobB, scopeB, err := JobIDForIdempotencyKey("user:b", "retry-key")
	if err != nil {
		t.Fatal(err)
	}
	if jobA != jobAReplay || scopeA != scopeAReplay {
		t.Fatal("same principal and key did not reproduce the stable identity")
	}
	if jobA == jobB || scopeA == scopeB {
		t.Fatal("different principals shared a generation job identity")
	}
	if !IsJobID(jobA) || strings.Contains(jobA, "retry-key") {
		t.Fatalf("job identity is not opaque: %q", jobA)
	}
}

func TestJobsV1CombinationCanonicalization(t *testing.T) {
	tests := []struct {
		name       string
		mode       string
		left       []string
		right      []string
		equivalent bool
	}{
		{name: "set is unordered", mode: "set", left: []string{"dp", "prefix-sum"}, right: []string{"PREFIX-SUM", " dp "}, equivalent: true},
		{name: "sequence order is binding", mode: "sequence", left: []string{"sorting", "two-pointers"}, right: []string{"two-pointers", "sorting"}, equivalent: false},
		{name: "mixed tail is unordered", mode: "mixed", left: []string{"graph", "dfs", "greedy"}, right: []string{"GRAPH", "greedy", "dfs"}, equivalent: true},
		{name: "mixed primary is binding", mode: "mixed", left: []string{"graph", "dfs", "greedy"}, right: []string{"dfs", "graph", "greedy"}, equivalent: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			left := loadFixtureRequest(t)
			left.Domain.Combination.Mode = test.mode
			left.Domain.Combination.MaxConcepts = len(test.left)
			left.Domain.KnowledgePoints = test.left
			right := left
			right.Domain.KnowledgePoints = test.right
			_, leftHash, err := left.CanonicalPayload()
			if err != nil {
				t.Fatal(err)
			}
			_, rightHash, err := right.CanonicalPayload()
			if err != nil {
				t.Fatal(err)
			}
			if (leftHash == rightHash) != test.equivalent {
				t.Fatalf("hash equality = %v, want %v (%s / %s)", leftHash == rightHash, test.equivalent, leftHash, rightHash)
			}
		})
	}
}

func TestJobsV1CanonicalizesImplicitMaxConcepts(t *testing.T) {
	implicit := loadFixtureRequest(t)
	implicit.Domain.Combination.MaxConcepts = 0
	explicit := loadFixtureRequest(t)
	explicit.Domain.Combination.MaxConcepts = len(explicit.Domain.KnowledgePoints)

	implicitPayload, implicitHash, err := implicit.CanonicalPayload()
	if err != nil {
		t.Fatal(err)
	}
	explicitPayload, explicitHash, err := explicit.CanonicalPayload()
	if err != nil {
		t.Fatal(err)
	}
	if implicitHash != explicitHash || string(implicitPayload) != string(explicitPayload) {
		t.Fatalf("implicit and explicit effective max differ:\n%s\n%s", implicitPayload, explicitPayload)
	}
}

func TestJobsV1SupportsAdaptiveTestCaseCount(t *testing.T) {
	request := loadFixtureRequest(t)
	request.Constraints.TestCaseCount = 20
	request.Constraints.AutoCaseCount = true
	if err := request.ValidateV1(); err != nil {
		t.Fatalf("valid adaptive generation request rejected: %v", err)
	}
	params := request.ToProblemGenParams()
	if !params.TestDataConfig.AutoCaseCount || params.TestDataConfig.NumTestCases != 20 {
		t.Fatalf("adaptive mode was not carried into workflow params: %+v", params.TestDataConfig)
	}
	payload, _, err := request.CanonicalPayload()
	if err != nil || !strings.Contains(string(payload), `"auto_case_count":true`) {
		t.Fatalf("canonical adaptive request omitted mode: %s (%v)", payload, err)
	}

	request.Constraints.TestCaseCount = 9
	if err := request.ValidateV1(); err == nil || !strings.Contains(err.Error(), "[10,20]") {
		t.Fatalf("invalid adaptive capacity error = %v", err)
	}
}

func TestJobsV1SupportsZeroCountAdaptiveTestCaseContract(t *testing.T) {
	request := loadFixtureRequest(t)
	request.Constraints.TestCaseCount = 0
	request.Constraints.TestCaseCountMin = domain.MinAdaptiveTestCases
	request.Constraints.TestCaseCountMax = domain.MaxAdaptiveTestCases
	request.Constraints.SampleCount = 2
	if err := request.ValidateV1(); err != nil {
		t.Fatalf("zero-count adaptive request rejected: %v", err)
	}
	params := request.ToProblemGenParams()
	config := params.TestDataConfig
	if !config.IsAdaptive() || config.NumTestCases != 0 ||
		config.MinTestCases != domain.MinAdaptiveTestCases ||
		config.MaxTestCases != domain.MaxAdaptiveTestCases {
		t.Fatalf("adaptive contract was not mapped into workflow params: %+v", config)
	}
	payload, _, err := request.CanonicalPayload()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(payload), `"test_case_count":0`) ||
		!strings.Contains(string(payload), `"test_case_count_min":10`) ||
		!strings.Contains(string(payload), `"test_case_count_max":20`) {
		t.Fatalf("canonical adaptive bounds missing: %s", payload)
	}
}

func TestJobsV1RejectsUnsupportedAndUnsatisfiableControls(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Request)
		code   ErrorCode
	}{
		{
			name: "multiple candidates",
			mutate: func(request *Request) {
				request.CandidateCount = 2
			},
			code: ErrorUnsupportedConstraint,
		},
		{
			name: "multiple languages",
			mutate: func(request *Request) {
				request.Languages = []string{"cpp", "python"}
			},
			code: ErrorUnsupportedConstraint,
		},
		{
			name: "syntax rating conflict",
			mutate: func(request *Request) {
				request.Domain.Level = "syntax"
				request.Difficulty.Rating = 1800
			},
			code: ErrorUnsatisfiableSpec,
		},
		{
			name: "missing wall budget",
			mutate: func(request *Request) {
				request.Quality.Budget.MaxWallTimeSeconds = 0
			},
			code: ErrorUnsatisfiableSpec,
		},
		{
			name: "seed is not wired",
			mutate: func(request *Request) {
				seed := int64(7)
				request.RandomSeed = &seed
			},
			code: ErrorUnsupportedConstraint,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := loadFixtureRequest(t)
			test.mutate(&request)
			err := request.ValidateV1()
			if err == nil {
				t.Fatal("ValidateV1 accepted an unsupported request")
			}
			if got := ErrorCodeOf(err); got != test.code {
				t.Fatalf("error code = %q, want %q (err=%v)", got, test.code, err)
			}
		})
	}
}

func TestJobsV1MapsExplicitLevelAndDoesNotExposeTemporalFields(t *testing.T) {
	request := loadFixtureRequest(t)
	params := request.ToProblemGenParams()
	if params.Level != domain.LevelAlgorithm || params.Difficulty != 1800 {
		t.Fatalf("params = %+v", params)
	}
	if params.KnowledgePointCombination == nil ||
		params.KnowledgePointCombination.SchemaVersion != domain.KnowledgePointCombinationSchemaV1 ||
		params.KnowledgePointCombination.Mode != domain.KnowledgePointCombinationSet ||
		params.KnowledgePointCombination.MaxConcepts != 3 {
		t.Fatalf("knowledge-point combination = %+v", params.KnowledgePointCombination)
	}
	if params.GenerationEvidence != nil {
		t.Fatalf("minimal request unexpectedly enabled standard evidence: %+v", params.GenerationEvidence)
	}
	combination, ok := params.MetadataExtras[KnowledgePointCombinationMetadataKey].(map[string]interface{})
	if !ok || combination["schema_version"] != domain.KnowledgePointCombinationSchemaV1 || combination["mode"] != "set" {
		t.Fatalf("knowledge-point metadata = %#v", combination)
	}
	audit, ok := params.MetadataExtras[GenerationAuditMetadataKey].(map[string]interface{})
	if !ok {
		t.Fatalf("generation audit metadata = %#v", params.MetadataExtras)
	}
	if audit["generation_arm"] != GenerationArmBaseline ||
		audit["generation_arm_mode"] != GenerationArmModeBaseline ||
		audit["generation_behavior"] != "unchanged" ||
		audit["qg02_plus_enabled"] != false ||
		audit["reviewer_profile"] != ReviewerProfileV1 ||
		audit["reviewer_profile_descriptor_sha256"] != ReviewerProfileV1DescriptorSHA256 ||
		audit["evidence_profile"] != EvidenceProfileV0 ||
		audit["evidence_profile_descriptor_sha256"] != EvidenceProfileDescriptorSHA256 ||
		audit["outcome_taxonomy"] != OutcomeTaxonomyVersion ||
		audit["knowledge_point_combination_schema"] != domain.KnowledgePointCombinationSchemaV1 {
		t.Fatalf("generation audit metadata = %#v", audit)
	}
	request.Domain.Level = "syntax"
	request.Difficulty.Rating = 1000
	if got := request.ToProblemGenParams().Level; got != domain.LevelSyntax {
		t.Fatalf("explicit syntax level mapped to %q", got)
	}

	encoded, err := json.Marshal(JobStatus{
		ContractVersion: JobContractVersion,
		JobID:           JobIDPrefix + strings.Repeat("a", 64),
		Status:          JobStatusRunning,
		Phase:           JobPhaseValidating,
		Progress:        50,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"run_id", "workflow_id", "current_step", "steps", "activity_type", "failure_reason"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("stable DTO leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestJobsV1AllEvidenceLevelsAreCanonicalAndServerAuthored(t *testing.T) {
	minimal := loadFixtureRequest(t)
	_, minimalHash, err := minimal.CanonicalPayload()
	if err != nil {
		t.Fatal(err)
	}

	standard := loadFixtureRequest(t)
	standard.Output.EvidenceLevel = " STANDARD "
	_, standardHash, err := standard.CanonicalPayload()
	if err != nil {
		t.Fatalf("standard evidence rejected: %v", err)
	}
	if standardHash == minimalHash {
		t.Fatal("standard evidence reused the minimal canonical request identity")
	}
	contract := standard.ToProblemGenParams().GenerationEvidence
	if contract == nil || contract.SchemaVersion != domain.GenerationEvidenceContractSchemaV1 ||
		contract.EvidenceLevel != domain.GenerationStandardEvidenceLevel ||
		contract.JobContractVersion != JobContractVersion ||
		contract.ReviewerProfileDescriptorSHA256 != ReviewerProfileV1DescriptorSHA256 ||
		contract.EvidenceProfileDescriptorSHA256 != EvidenceProfileDescriptorSHA256 ||
		!contract.IncludeEditorial || !contract.IncludeSolutions || !contract.IncludeTestData {
		t.Fatalf("standard evidence contract = %+v", contract)
	}
	standardParams := standard.ToProblemGenParams()
	if level, err := QualityEvidenceLevelFromParams(standardParams); err != nil || level != EvidenceStandard {
		t.Fatalf("standard quality level = %q, %v", level, err)
	}

	audit := loadFixtureRequest(t)
	audit.Output.EvidenceLevel = EvidenceAudit
	_, auditHash, err := audit.CanonicalPayload()
	if err != nil {
		t.Fatalf("audit evidence rejected: %v", err)
	}
	if auditHash == minimalHash || auditHash == standardHash {
		t.Fatal("audit evidence reused another canonical request identity")
	}
	auditParams := audit.ToProblemGenParams()
	if auditParams.GenerationEvidence != nil {
		t.Fatalf("audit profile was mixed with the historical standard receipt contract: %+v", auditParams.GenerationEvidence)
	}
	if level, err := QualityEvidenceLevelFromParams(auditParams); err != nil || level != EvidenceAudit {
		t.Fatalf("audit quality level = %q, %v", level, err)
	}
	if got := auditParams.MetadataExtras[QualityEvidenceLevelMetadataKey]; got != EvidenceAudit {
		t.Fatalf("persisted audit metadata = %#v", got)
	}
}

func TestQualityEvidenceLevelFromParamsIsReplayCompatibleAndFailClosed(t *testing.T) {
	minimal := domain.ProblemGenParams{}
	if level, err := QualityEvidenceLevelFromParams(minimal); err != nil || level != EvidenceMinimal {
		t.Fatalf("legacy minimal = %q, %v", level, err)
	}
	standard := domain.ProblemGenParams{GenerationEvidence: &domain.GenerationEvidenceContract{}}
	if level, err := QualityEvidenceLevelFromParams(standard); err != nil || level != EvidenceStandard {
		t.Fatalf("legacy standard = %q, %v", level, err)
	}
	for name, metadata := range map[string]map[string]interface{}{
		"blank":      {QualityEvidenceLevelMetadataKey: " "},
		"unknown":    {QualityEvidenceLevelMetadataKey: "extended"},
		"wrong type": {QualityEvidenceLevelMetadataKey: 1},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := QualityEvidenceLevelFromParams(domain.ProblemGenParams{MetadataExtras: metadata}); err == nil {
				t.Fatalf("metadata %#v was accepted", metadata)
			}
		})
	}
	conflict := domain.ProblemGenParams{
		GenerationEvidence: &domain.GenerationEvidenceContract{},
		MetadataExtras:     map[string]interface{}{QualityEvidenceLevelMetadataKey: EvidenceAudit},
	}
	if _, err := QualityEvidenceLevelFromParams(conflict); err == nil {
		t.Fatal("legacy standard contract mixed with audit profile")
	}
}
