package service

import (
	"strings"
	"testing"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/generationapi"
	enumspb "go.temporal.io/api/enums/v1"
)

func TestGenerationJobStartOptionsBindOnlyStableHashes(t *testing.T) {
	jobID := generationapi.JobIDPrefix + strings.Repeat("a", 64)
	payloadSHA256 := strings.Repeat("b", 64)
	principalSHA256 := strings.Repeat("c", 64)
	timeout := 90 * time.Second

	opts := generationJobStartOptions(jobID, "quality-queue", payloadSHA256, principalSHA256, timeout, generationapi.EvidenceStandard)
	if opts.ID != jobID || opts.TaskQueue != "quality-queue" {
		t.Fatalf("identity options = %+v", opts)
	}
	if opts.WorkflowIDReusePolicy != enumspb.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE {
		t.Fatalf("reuse policy = %v", opts.WorkflowIDReusePolicy)
	}
	if opts.WorkflowIDConflictPolicy != enumspb.WORKFLOW_ID_CONFLICT_POLICY_FAIL || !opts.WorkflowExecutionErrorWhenAlreadyStarted {
		t.Fatalf("conflict options = %+v", opts)
	}
	if opts.WorkflowExecutionTimeout != timeout || opts.WorkflowRunTimeout != timeout {
		t.Fatalf("timeouts = %v / %v", opts.WorkflowExecutionTimeout, opts.WorkflowRunTimeout)
	}
	if len(opts.Memo) != 11 ||
		opts.Memo[generationapi.MemoContractVersionKey] != generationapi.JobContractVersion ||
		opts.Memo[generationapi.MemoPayloadSHA256Key] != payloadSHA256 ||
		opts.Memo[generationapi.MemoPrincipalScopeKey] != principalSHA256 ||
		opts.Memo[generationapi.MemoGenerationArmKey] != generationapi.GenerationArmBaseline ||
		opts.Memo[generationapi.MemoReviewerProfileKey] != generationapi.ReviewerProfileV1 ||
		opts.Memo[generationapi.MemoEvidenceProfileKey] != generationapi.EvidenceProfileV0 ||
		opts.Memo[generationapi.MemoOutcomeTaxonomyKey] != generationapi.OutcomeTaxonomyVersion ||
		opts.Memo[generationapi.MemoReviewerDescriptorKey] != generationapi.ReviewerProfileV1DescriptorSHA256 ||
		opts.Memo[generationapi.MemoEvidenceDescriptorKey] != generationapi.EvidenceProfileDescriptorSHA256 {
		t.Fatalf("memo = %#v", opts.Memo)
	}
	if opts.Memo[generationapi.MemoKnowledgePointContractKey] != domain.KnowledgePointCombinationSchemaV1 {
		t.Fatalf("knowledge-point contract memo = %#v", opts.Memo)
	}
	if opts.Memo[generationapi.MemoEvidenceLevelKey] != generationapi.EvidenceStandard {
		t.Fatalf("evidence level memo = %#v", opts.Memo)
	}
	encoded := ""
	for key, value := range opts.Memo {
		encoded += key + "=" + value.(string) + ";"
	}
	for _, forbidden := range []string{"retry-key", "custom_requirements", "api_key"} {
		if strings.Contains(encoded, forbidden) {
			t.Fatalf("memo leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestGenerationJobRoutesAllNewEvidenceLevelsThroughQualityWorkflow(t *testing.T) {
	minimal := domain.ProblemGenParams{}
	if got, err := generationJobEvidenceLevel(minimal); err != nil || generationJobWorkflowType(minimal) != generationapi.QualityWorkflowTypeV1 || got != generationapi.EvidenceMinimal {
		t.Fatalf("minimal routing = %q / %q / %v", generationJobWorkflowType(minimal), got, err)
	}
	standard := domain.ProblemGenParams{
		GenerationEvidence: &domain.GenerationEvidenceContract{},
		MetadataExtras:     map[string]interface{}{generationapi.QualityEvidenceLevelMetadataKey: generationapi.EvidenceStandard},
	}
	if got, err := generationJobEvidenceLevel(standard); err != nil || generationJobWorkflowType(standard) != generationapi.QualityWorkflowTypeV1 || got != generationapi.EvidenceStandard {
		t.Fatalf("standard routing = %q / %q / %v", generationJobWorkflowType(standard), got, err)
	}
	audit := domain.ProblemGenParams{MetadataExtras: map[string]interface{}{generationapi.QualityEvidenceLevelMetadataKey: generationapi.EvidenceAudit}}
	if got, err := generationJobEvidenceLevel(audit); err != nil || generationJobWorkflowType(audit) != generationapi.QualityWorkflowTypeV1 || got != generationapi.EvidenceAudit {
		t.Fatalf("audit routing = %q / %q / %v", generationJobWorkflowType(audit), got, err)
	}
}

func TestGenerationJobQualityInputIsServerDerivedAndSingleCandidate(t *testing.T) {
	params := domain.DefaultProblemGenParams()
	params.Tags = []string{"prefix-sum"}
	params.Locale = "zh"
	params.CustomPrompt = "Design a deterministic range-sum problem."
	params.GenerationEvidence = &domain.GenerationEvidenceContract{
		SchemaVersion: domain.GenerationEvidenceContractSchemaV1, EvidenceLevel: domain.GenerationStandardEvidenceLevel,
		JobContractVersion: generationapi.JobContractVersion, GenerationArm: generationapi.GenerationArmBaseline,
		GenerationArmMode: generationapi.GenerationArmModeBaseline, GenerationBehavior: "unchanged",
		ReviewerProfile: generationapi.ReviewerProfileV1, ReviewerProfileDescriptorSHA256: generationapi.ReviewerProfileV1DescriptorSHA256,
		EvidenceProfile: generationapi.EvidenceProfileV0, EvidenceProfileDescriptorSHA256: generationapi.EvidenceProfileDescriptorSHA256,
		OutcomeTaxonomy: generationapi.OutcomeTaxonomyVersion, IncludeEditorial: true, IncludeSolutions: true, IncludeTestData: true,
	}
	params.MetadataExtras = map[string]interface{}{generationapi.QualityEvidenceLevelMetadataKey: generationapi.EvidenceStandard}
	jobID := generationapi.JobIDPrefix + strings.Repeat("d", 64)

	input, err := generationJobQualityInput(jobID, params)
	if err != nil {
		t.Fatal(err)
	}
	if input.SubjectID != jobID || input.Language != "cpp" || input.FrozenConcept != params.CustomPrompt {
		t.Fatalf("quality input identity = %+v", input)
	}
	if input.Params.RequireReview || len(input.Params.Languages) != 1 {
		t.Fatalf("quality params are not the stable single-candidate product shape: %+v", input.Params)
	}
	if input.Params.GenerationEvidence != nil {
		t.Fatalf("quality input retained historical standard receipt contract: %+v", input.Params.GenerationEvidence)
	}
	facts := make(map[string]string, len(input.RequiredFacts))
	for _, fact := range input.RequiredFacts {
		facts[fact.Key] = fact.Value
	}
	for _, key := range []string{"difficulty_rating", "knowledge_points", "level", "locale", "memory_limit_mb", "sample_count", "test_case_count", "time_limit_ms"} {
		if facts[key] == "" {
			t.Fatalf("server-derived fact %q is missing: %#v", key, facts)
		}
	}
}

func TestGenerationJobSHA256Validation(t *testing.T) {
	if !isGenerationJobSHA256(strings.Repeat("a", 64)) {
		t.Fatal("valid sha256 was rejected")
	}
	for _, invalid := range []string{"", strings.Repeat("a", 63), strings.Repeat("z", 64)} {
		if isGenerationJobSHA256(invalid) {
			t.Fatalf("invalid sha256 was accepted: %q", invalid)
		}
	}
}
