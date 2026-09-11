package service

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/generationapi"
	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
	algoworkflow "github.com/Gingoo-TvT/Qraft/backend/internal/workflow"
	"github.com/Gingoo-TvT/Qraft/backend/internal/workflow/activities"
	"github.com/rs/zerolog/log"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
)

// TriggerGenerationJob starts the stable single-candidate product workflow.
// Its identity and memo contain only hashes; the canonical request and the
// caller's idempotency key never enter Temporal visibility metadata.
func (s *ProblemService) TriggerGenerationJob(
	ctx context.Context,
	workflowID string,
	payloadSHA256 string,
	principalScopeSHA256 string,
	runTimeout time.Duration,
	params domain.ProblemGenParams,
) (client.WorkflowRun, error) {
	if !generationapi.IsJobID(workflowID) {
		return nil, fmt.Errorf("validation: invalid generation job id")
	}
	if !isGenerationJobSHA256(payloadSHA256) || !isGenerationJobSHA256(principalScopeSHA256) {
		return nil, fmt.Errorf("validation: invalid generation job identity hash")
	}
	if err := s.prepareGenerationJobParams(ctx, &params); err != nil {
		return nil, err
	}

	evidenceLevel, err := generationJobEvidenceLevel(params)
	if err != nil {
		return nil, fmt.Errorf("validation: %w", err)
	}
	opts := generationJobStartOptions(
		workflowID,
		s.taskQueue,
		payloadSHA256,
		principalScopeSHA256,
		runTimeout,
		evidenceLevel,
	)
	qualityInput, err := generationJobQualityInput(workflowID, params)
	if err != nil {
		return nil, err
	}
	run, err := s.temporal.ExecuteWorkflow(ctx, opts, generationJobWorkflowType(params), qualityInput)
	if err != nil {
		return nil, fmt.Errorf("starting generation job workflow: %w", err)
	}

	log.Info().
		Str("job_id", run.GetID()).
		Msg("generation job workflow started")
	return run, nil
}

func (s *ProblemService) prepareGenerationJobParams(
	ctx context.Context,
	params *domain.ProblemGenParams,
) error {
	if params == nil {
		return fmt.Errorf("validation: generation parameters are required")
	}
	if err := params.Validate(); err != nil {
		return fmt.Errorf("validation: %w", err)
	}
	if s.tagRepo == nil {
		return fmt.Errorf("validating generation job tags: tag repository is unavailable")
	}
	if err := s.tagRepo.ValidateTagsForLevel(ctx, params.Level, params.Tags); err != nil {
		if errors.Is(err, repository.ErrTagsNotValidForLevel) {
			return fmt.Errorf("validation: %w", err)
		}
		return fmt.Errorf("validating generation job tags: %w", err)
	}

	params.TagRanges = make(map[string][2]int, len(params.Tags))
	for _, tagName := range params.Tags {
		category, err := s.tagRepo.GetByTagName(ctx, tagName)
		if err != nil {
			return fmt.Errorf("loading generation job tag %q: %w", tagName, err)
		}
		if params.Difficulty < category.MinDifficulty || params.Difficulty > category.MaxDifficulty {
			return fmt.Errorf(
				"validation: difficulty %d is outside tag %s range [%d,%d]",
				params.Difficulty,
				tagName,
				category.MinDifficulty,
				category.MaxDifficulty,
			)
		}
		params.TagRanges[tagName] = [2]int{category.MinDifficulty, category.MaxDifficulty}
	}
	return nil
}

func generationJobStartOptions(
	workflowID string,
	taskQueue string,
	payloadSHA256 string,
	principalScopeSHA256 string,
	runTimeout time.Duration,
	evidenceLevel string,
) client.StartWorkflowOptions {
	opts := client.StartWorkflowOptions{
		ID:                                       workflowID,
		TaskQueue:                                taskQueue,
		WorkflowIDReusePolicy:                    enumspb.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
		WorkflowIDConflictPolicy:                 enumspb.WORKFLOW_ID_CONFLICT_POLICY_FAIL,
		WorkflowExecutionErrorWhenAlreadyStarted: true,
		Memo: map[string]interface{}{
			generationapi.MemoContractVersionKey:        generationapi.JobContractVersion,
			generationapi.MemoPayloadSHA256Key:          payloadSHA256,
			generationapi.MemoPrincipalScopeKey:         principalScopeSHA256,
			generationapi.MemoGenerationArmKey:          generationapi.GenerationArmBaseline,
			generationapi.MemoReviewerProfileKey:        generationapi.ReviewerProfileV1,
			generationapi.MemoEvidenceProfileKey:        generationapi.EvidenceProfileV0,
			generationapi.MemoOutcomeTaxonomyKey:        generationapi.OutcomeTaxonomyVersion,
			generationapi.MemoReviewerDescriptorKey:     generationapi.ReviewerProfileV1DescriptorSHA256,
			generationapi.MemoEvidenceDescriptorKey:     generationapi.EvidenceProfileDescriptorSHA256,
			generationapi.MemoKnowledgePointContractKey: domain.KnowledgePointCombinationSchemaV1,
			generationapi.MemoEvidenceLevelKey:          evidenceLevel,
		},
	}
	if runTimeout > 0 {
		opts.WorkflowExecutionTimeout = runTimeout
		opts.WorkflowRunTimeout = runTimeout
	}
	return opts
}

func generationJobEvidenceLevel(params domain.ProblemGenParams) (string, error) {
	return generationapi.QualityEvidenceLevelFromParams(params)
}

func generationJobWorkflowType(params domain.ProblemGenParams) string {
	_ = params
	return generationapi.QualityWorkflowTypeV1
}

// generationJobQualityInput is the only product adapter into the S3 quality
// workflow. It derives the immutable authoring intent from validated server
// parameters; callers cannot submit gate results, artifact refs, hidden-suite
// selectors, or a pre-approved manifest through the stable Job API.
func generationJobQualityInput(workflowID string, params domain.ProblemGenParams) (algoworkflow.ProblemGenerationQualityInputV1, error) {
	if !generationapi.IsJobID(workflowID) {
		return algoworkflow.ProblemGenerationQualityInputV1{}, fmt.Errorf("validation: invalid generation job id")
	}
	if err := params.Validate(); err != nil {
		return algoworkflow.ProblemGenerationQualityInputV1{}, fmt.Errorf("validation: %w", err)
	}
	if len(params.Languages) != 1 {
		return algoworkflow.ProblemGenerationQualityInputV1{}, fmt.Errorf("validation: generation jobs v1 requires exactly one language")
	}
	if _, err := generationJobEvidenceLevel(params); err != nil {
		return algoworkflow.ProblemGenerationQualityInputV1{}, fmt.Errorf("validation: %w", err)
	}
	// GenerationEvidence belongs to the historical standard-receipt workflow.
	// New standard/audit jobs carry their profile in server-authored metadata
	// and enter the same nine-gate quality workflow as minimal jobs.
	qualityParams := params
	qualityParams.GenerationEvidence = nil

	concept := strings.TrimSpace(params.CustomPrompt)
	if concept == "" {
		concept = fmt.Sprintf(
			"Generate one %s competitive-programming problem using the required knowledge points: %s.",
			params.Level,
			strings.Join(params.Tags, ", "),
		)
	}
	facts := []activities.CanonicalAuthoringBriefFactV1{
		{Key: "difficulty_rating", Value: strconv.Itoa(params.Difficulty)},
		{Key: "knowledge_points", Value: strings.Join(params.Tags, ",")},
		{Key: "level", Value: string(params.Level)},
		{Key: "locale", Value: params.Locale},
		{Key: "memory_limit_mb", Value: strconv.Itoa(params.MemoryLimit)},
		{Key: "sample_count", Value: strconv.Itoa(params.TestDataConfig.NumSamples)},
		{Key: "time_limit_ms", Value: strconv.Itoa(params.TimeLimit)},
	}
	if params.TestDataConfig.IsAdaptive() {
		minCases, maxCases, _ := params.TestDataConfig.EffectiveTestCaseRange()
		facts = append(facts,
			activities.CanonicalAuthoringBriefFactV1{Key: "test_case_count", Value: fmt.Sprintf("adaptive[%d,%d]", minCases, maxCases)},
			activities.CanonicalAuthoringBriefFactV1{Key: "test_case_count_min", Value: strconv.Itoa(minCases)},
			activities.CanonicalAuthoringBriefFactV1{Key: "test_case_count_max", Value: strconv.Itoa(maxCases)},
		)
	} else {
		facts = append(facts, activities.CanonicalAuthoringBriefFactV1{Key: "test_case_count", Value: strconv.Itoa(params.TestDataConfig.NumTestCases)})
	}
	if style := strings.TrimSpace(params.ContestStyle); style != "" {
		facts = append(facts, activities.CanonicalAuthoringBriefFactV1{Key: "contest_style", Value: style})
	}
	return algoworkflow.ProblemGenerationQualityInputV1{
		PayloadVersion: algoworkflow.ProblemGenerationQualityPayloadVersionV1,
		SubjectID:      workflowID,
		Language:       params.Languages[0],
		FrozenConcept:  concept,
		RequiredFacts:  facts,
		Params:         qualityParams,
	}, nil
}

func isGenerationJobSHA256(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != 64 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32
}

// GetProblemByWorkflowID projects a product result from durable problem data;
// callers never need to inspect workflow step outputs.
func (s *ProblemService) GetProblemByWorkflowID(
	ctx context.Context,
	workflowID string,
) (*domain.Problem, error) {
	problem, err := s.problemRepo.GetByWorkflowID(ctx, workflowID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("getting generation job result: %w", err)
	}
	return problem, nil
}
