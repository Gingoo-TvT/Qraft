package service

import (
	"context"
	"fmt"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/diversity"
	"github.com/Gingoo-TvT/Qraft/backend/internal/diversityapi"
	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	algoworkflow "github.com/Gingoo-TvT/Qraft/backend/internal/workflow"
	"github.com/rs/zerolog/log"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
)

const (
	s5ConceptNetworkRetryBudgetV1 = 2
	s5DedupRequestedTopKV1        = 8
	s5DedupThresholdV1            = 0.90
	s5ConceptOverheadV1           = 30 * time.Minute
)

func defaultS5ConceptBudgetV1() diversity.ConceptBudgetV1 {
	return diversity.ConceptBudgetV1{
		MaxCreativeAttempts: 2,
		MaxConcepts:         4,
		// Two creative calls plus one normalizer, each with at most two
		// network retries. Actual calls/retries are recorded separately.
		MaxModelCalls:       9,
		MaxNetworkRetries:   6,
		MaxTokens:           64_000,
		MaxWallMilliseconds: int64((20 * time.Minute) / time.Millisecond),
	}
}

// TriggerS5MicroBatch starts the additive three-slot S5 parent workflow. It
// never widens the stable generation-jobs v1 request: each slot remains one
// validated candidate and is later materialized by the existing S3 workflow.
func (s *ProblemService) TriggerS5MicroBatch(
	ctx context.Context,
	batchID string,
	payloadSHA256 string,
	principalScopeSHA256 string,
	childWorkflowTimeout time.Duration,
	params []domain.ProblemGenParams,
) (client.WorkflowRun, error) {
	if !diversityapi.IsBatchID(batchID) {
		return nil, fmt.Errorf("validation: invalid S5 micro-batch id")
	}
	if !isGenerationJobSHA256(payloadSHA256) || !isGenerationJobSHA256(principalScopeSHA256) {
		return nil, fmt.Errorf("validation: invalid S5 micro-batch identity hash")
	}
	if len(params) != diversityapi.SlotCountV1 {
		return nil, fmt.Errorf("validation: S5 micro-batch requires exactly %d slots", diversityapi.SlotCountV1)
	}
	prepared := append([]domain.ProblemGenParams(nil), params...)
	for index := range prepared {
		if err := s.prepareGenerationJobParams(ctx, &prepared[index]); err != nil {
			return nil, fmt.Errorf("slot %d: %w", index, err)
		}
	}
	input, err := buildS5MicroBatchWorkflowInputV1(batchID, childWorkflowTimeout, prepared)
	if err != nil {
		return nil, err
	}
	opts := s5MicroBatchStartOptionsV1(
		batchID,
		s.taskQueue,
		payloadSHA256,
		principalScopeSHA256,
		childWorkflowTimeout,
	)
	run, err := s.temporal.ExecuteWorkflow(ctx, opts, diversityapi.WorkflowTypeV1, input)
	if err != nil {
		return nil, fmt.Errorf("starting S5 micro-batch workflow: %w", err)
	}
	log.Info().Str("batch_id", run.GetID()).Msg("S5 micro-batch workflow started")
	return run, nil
}

func buildS5MicroBatchWorkflowInputV1(
	batchID string,
	childWorkflowTimeout time.Duration,
	params []domain.ProblemGenParams,
) (algoworkflow.S5MicroBatchInputV1, error) {
	if !diversityapi.IsBatchID(batchID) || len(params) != diversityapi.SlotCountV1 {
		return algoworkflow.S5MicroBatchInputV1{}, fmt.Errorf("validation: invalid S5 micro-batch shape")
	}
	if childWorkflowTimeout <= 0 {
		return algoworkflow.S5MicroBatchInputV1{}, fmt.Errorf("validation: S5 child workflow timeout must be positive")
	}
	slots := make([]algoworkflow.ProblemGenerationQualityInputV1, diversityapi.SlotCountV1)
	for slotIndex := range params {
		childID, err := diversityapi.ChildJobID(batchID, slotIndex)
		if err != nil {
			return algoworkflow.S5MicroBatchInputV1{}, fmt.Errorf("validation: derive slot %d child id: %w", slotIndex, err)
		}
		if params[slotIndex].MetadataExtras == nil {
			params[slotIndex].MetadataExtras = make(map[string]interface{})
		}
		params[slotIndex].MetadataExtras["s5_micro_batch_v1"] = map[string]interface{}{
			"batch_id":        batchID,
			"slot_index":      slotIndex,
			"selection_state": "pending_parent_reservation",
		}
		qualityInput, err := generationJobQualityInput(childID, params[slotIndex])
		if err != nil {
			return algoworkflow.S5MicroBatchInputV1{}, fmt.Errorf("slot %d: %w", slotIndex, err)
		}
		slots[slotIndex] = qualityInput
	}
	return algoworkflow.S5MicroBatchInputV1{
		PayloadVersion:            algoworkflow.S5MicroBatchPayloadVersionV1,
		BatchID:                   batchID,
		Slots:                     slots,
		ConceptBudget:             defaultS5ConceptBudgetV1(),
		ConceptNetworkRetryBudget: s5ConceptNetworkRetryBudgetV1,
		DedupTopK:                 s5DedupRequestedTopKV1,
		DedupThreshold:            s5DedupThresholdV1,
		ExcludedLineageIDs:        []string{},
		ChildWorkflowTimeout:      childWorkflowTimeout,
	}, nil
}

func s5MicroBatchStartOptionsV1(
	batchID string,
	taskQueue string,
	payloadSHA256 string,
	principalScopeSHA256 string,
	childWorkflowTimeout time.Duration,
) client.StartWorkflowOptions {
	opts := client.StartWorkflowOptions{
		ID:                                       batchID,
		TaskQueue:                                taskQueue,
		WorkflowIDReusePolicy:                    enumspb.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
		WorkflowIDConflictPolicy:                 enumspb.WORKFLOW_ID_CONFLICT_POLICY_FAIL,
		WorkflowExecutionErrorWhenAlreadyStarted: true,
		Memo: map[string]interface{}{
			diversityapi.MemoContractVersionKey: diversityapi.ContractVersion,
			diversityapi.MemoPayloadSHA256Key:   payloadSHA256,
			diversityapi.MemoPrincipalScopeKey:  principalScopeSHA256,
		},
	}
	if childWorkflowTimeout > 0 {
		opts.WorkflowExecutionTimeout = childWorkflowTimeout + s5ConceptOverheadV1
		opts.WorkflowRunTimeout = childWorkflowTimeout + s5ConceptOverheadV1
	}
	return opts
}
