package workflow

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/diversity"
	"github.com/Gingoo-TvT/Qraft/backend/internal/diversityapi"
	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	qualitygate "github.com/Gingoo-TvT/Qraft/backend/internal/qualitygate/v1"
	"github.com/Gingoo-TvT/Qraft/backend/internal/workflow/activities"
	"github.com/google/uuid"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const (
	S5MicroBatchPayloadVersionV1 = 1
	S5MicroBatchStateQueryName   = "s5-micro-batch-state"

	S5MicroBatchPhaseCreative  = "creative"
	S5MicroBatchPhaseNormalize = "normalize"
	S5MicroBatchPhaseDedup     = "dedup"
	S5MicroBatchPhaseReserve   = "reserve"
	S5MicroBatchPhaseChildren  = "children"
	S5MicroBatchPhaseComplete  = "complete"
	S5MicroBatchPhaseFailed    = "failed"

	s5MicroBatchContractErrorTypeV1 = "S5MicroBatchContractError"
	s5ReservedChildMetadataSchemaV1 = "algoforge.s5-reserved-child.v1"
)

// S5MicroBatchInputV1 is an additive parent contract. Every slot is still one
// validated generation-job projection; the existing single-candidate API is
// not widened.
type S5MicroBatchInputV1 struct {
	PayloadVersion            int                               `json:"payload_version"`
	BatchID                   string                            `json:"batch_id"`
	Slots                     []ProblemGenerationQualityInputV1 `json:"slots"`
	ConceptBudget             diversity.ConceptBudgetV1         `json:"concept_budget"`
	ConceptNetworkRetryBudget int                               `json:"concept_network_retry_budget"`
	DedupTopK                 int                               `json:"dedup_top_k"`
	DedupThreshold            float64                           `json:"dedup_threshold"`
	ExcludedLineageIDs        []string                          `json:"excluded_lineage_ids,omitempty"`
	ChildWorkflowTimeout      time.Duration                     `json:"child_workflow_timeout"`
}

type S5MicroBatchChildStateV1 struct {
	SlotIndex       int                   `json:"slot_index"`
	SlotID          string                `json:"slot_id"`
	JobID           string                `json:"job_id"`
	ConceptID       string                `json:"concept_id,omitempty"`
	Status          domain.WorkflowStatus `json:"status"`
	Decision        string                `json:"decision,omitempty"`
	StoredProblemID string                `json:"stored_problem_id,omitempty"`
}

// S5MicroBatchStateV1 is a compact independent query surface for the parent.
// It never aliases the state query of an individual nine-gate child.
type S5MicroBatchStateV1 struct {
	PayloadVersion    int                        `json:"payload_version"`
	BatchID           string                     `json:"batch_id"`
	Phase             string                     `json:"phase"`
	Status            domain.WorkflowStatus      `json:"status"`
	Progress          int                        `json:"progress"`
	CorpusRevision    string                     `json:"corpus_revision,omitempty"`
	ReservationSHA256 string                     `json:"reservation_sha256,omitempty"`
	ManifestSHA256    string                     `json:"manifest_sha256,omitempty"`
	ChildJobIDs       []string                   `json:"child_job_ids"`
	Children          []S5MicroBatchChildStateV1 `json:"children"`
	Error             string                     `json:"error,omitempty"`
}

type S5MicroBatchChildResultV1 struct {
	SlotIndex           int                  `json:"slot_index"`
	SlotID              string               `json:"slot_id"`
	JobID               string               `json:"job_id"`
	ConceptID           string               `json:"concept_id"`
	Decision            string               `json:"decision"`
	StoredProblemID     string               `json:"stored_problem_id"`
	StoredProblemStatus domain.ProblemStatus `json:"stored_problem_status"`
}

type S5MicroBatchResultV1 struct {
	PayloadVersion    int                                `json:"payload_version"`
	BatchID           string                             `json:"batch_id"`
	CorpusRevision    string                             `json:"corpus_revision"`
	Pools             []diversity.ConceptPoolV1          `json:"pools"`
	DedupObservations []diversity.DedupObservationV1     `json:"dedup_observations"`
	Reservation       diversity.ProvisionalReservationV1 `json:"reservation"`
	ReservationSHA256 string                             `json:"reservation_sha256"`
	ManifestSHA256    string                             `json:"manifest_sha256"`
	ManifestArtifact  *activities.ArtifactRef            `json:"manifest_artifact"`
	ChildJobIDs       []string                           `json:"child_job_ids"`
	Children          []S5MicroBatchChildResultV1        `json:"children"`
}

type s5ConceptCoordinateV1 struct {
	slotIndex    int
	attemptIndex int
	conceptIndex int
	card         diversity.ConceptCardV1
}

// S5MicroBatchGenerationWorkflowV1 freezes all 12 concepts before observing
// one corpus revision. Selection is advisory with respect to near-neighbor
// warnings: only unavailable or internally inconsistent observation evidence
// stops the parent before reservation.
func S5MicroBatchGenerationWorkflowV1(ctx workflow.Context, input S5MicroBatchInputV1) (*S5MicroBatchResultV1, error) {
	prepared, childJobIDs, err := validateS5MicroBatchInputV1(input)
	if err != nil {
		return nil, s5MicroBatchContractErrorV1(err.Error(), err)
	}
	input = prepared

	state := S5MicroBatchStateV1{
		PayloadVersion: S5MicroBatchPayloadVersionV1,
		BatchID:        input.BatchID,
		Phase:          S5MicroBatchPhaseCreative,
		Status:         domain.WorkflowStatusRunning,
		Progress:       5,
		ChildJobIDs:    append([]string(nil), childJobIDs...),
		Children:       make([]S5MicroBatchChildStateV1, diversityapi.SlotCountV1),
	}
	for slotIndex := range state.Children {
		slotID, _ := activities.S5SlotIDV1(input.BatchID, slotIndex)
		state.Children[slotIndex] = S5MicroBatchChildStateV1{
			SlotIndex: slotIndex,
			SlotID:    slotID,
			JobID:     childJobIDs[slotIndex],
			Status:    domain.WorkflowStatusPending,
		}
	}
	if err := workflow.SetQueryHandler(ctx, S5MicroBatchStateQueryName, func() (S5MicroBatchStateV1, error) {
		return cloneS5MicroBatchStateV1(state), nil
	}); err != nil {
		return nil, fmt.Errorf("register S5 micro-batch state query: %w", err)
	}
	fail := func(message string, cause error) (*S5MicroBatchResultV1, error) {
		state.Phase = S5MicroBatchPhaseFailed
		state.Status = domain.WorkflowStatusFailed
		state.Error = message
		return nil, s5MicroBatchContractErrorV1(message, cause)
	}

	activityContext := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts: 1,
		},
	})

	// Schedule every creative invocation before awaiting any one of them.
	// This is the first hard barrier: normalization cannot begin until all six
	// logical attempts have returned auditable receipts and usage.
	type creativeFutureV1 struct {
		slotIndex    int
		attemptIndex int
		future       workflow.Future
	}
	creativeFutures := make([]creativeFutureV1, 0, diversityapi.SlotCountV1*2)
	canonicalBriefs := make([]string, diversityapi.SlotCountV1)
	briefSHA256s := make([]string, diversityapi.SlotCountV1)
	for slotIndex := range input.Slots {
		canonicalBrief, briefSHA256, canonicalErr := activities.CanonicalS5BriefV1(input.Slots[slotIndex].FrozenConcept)
		if canonicalErr != nil {
			return fail(fmt.Sprintf("slot %d concept brief is invalid", slotIndex), canonicalErr)
		}
		canonicalBriefs[slotIndex], briefSHA256s[slotIndex] = canonicalBrief, briefSHA256
		slotID, _ := activities.S5SlotIDV1(input.BatchID, slotIndex)
		for attemptIndex := 0; attemptIndex < 2; attemptIndex++ {
			attemptID, _ := activities.S5LogicalAttemptIDV1(input.BatchID, slotIndex, attemptIndex)
			future := workflow.ExecuteActivity(activityContext, "GenerateS5ConceptAttemptActivityV1", activities.GenerateS5ConceptAttemptInputV1{
				PayloadVersion:     activities.GenerateS5ConceptAttemptPayloadVersionV1,
				BatchID:            input.BatchID,
				SlotIndex:          slotIndex,
				SlotID:             slotID,
				AttemptIndex:       attemptIndex,
				LogicalAttemptID:   attemptID,
				CanonicalBrief:     canonicalBrief,
				BriefSHA256:        briefSHA256,
				Params:             input.Slots[slotIndex].Params,
				Budget:             input.ConceptBudget,
				NetworkRetryBudget: input.ConceptNetworkRetryBudget,
			})
			creativeFutures = append(creativeFutures, creativeFutureV1{slotIndex: slotIndex, attemptIndex: attemptIndex, future: future})
		}
	}
	attemptsBySlot := make([][]diversity.ConceptAttemptV1, diversityapi.SlotCountV1)
	for _, scheduled := range creativeFutures {
		var result activities.GenerateS5ConceptAttemptResultV1
		if err := scheduled.future.Get(ctx, &result); err != nil {
			return fail(fmt.Sprintf("slot %d creative attempt %d failed", scheduled.slotIndex, scheduled.attemptIndex), err)
		}
		if err := validateS5CreativeResultV1(input, scheduled.slotIndex, scheduled.attemptIndex, briefSHA256s[scheduled.slotIndex], result); err != nil {
			return fail(fmt.Sprintf("slot %d creative attempt %d returned an invalid contract", scheduled.slotIndex, scheduled.attemptIndex), err)
		}
		attemptsBySlot[scheduled.slotIndex] = append(attemptsBySlot[scheduled.slotIndex], result.Attempt)
	}

	state.Phase, state.Progress = S5MicroBatchPhaseNormalize, 30
	// Schedule the three low-temperature normalizers before awaiting one.
	normalizeFutures := make([]workflow.Future, diversityapi.SlotCountV1)
	for slotIndex := range input.Slots {
		slotID, _ := activities.S5SlotIDV1(input.BatchID, slotIndex)
		normalizeFutures[slotIndex] = workflow.ExecuteActivity(activityContext, "NormalizeS5ConceptPoolActivityV1", activities.NormalizeS5ConceptPoolInputV1{
			PayloadVersion:     activities.NormalizeS5ConceptPoolPayloadVersionV1,
			BatchID:            input.BatchID,
			SlotIndex:          slotIndex,
			SlotID:             slotID,
			BriefSHA256:        briefSHA256s[slotIndex],
			Params:             input.Slots[slotIndex].Params,
			Budget:             input.ConceptBudget,
			Attempts:           append([]diversity.ConceptAttemptV1(nil), attemptsBySlot[slotIndex]...),
			NormalizerVersion:  activities.S5ConceptNormalizerVersionV1,
			NetworkRetryBudget: input.ConceptNetworkRetryBudget,
		})
	}
	drafts := make([]activities.S5NormalizedConceptPoolDraftV1, diversityapi.SlotCountV1)
	for slotIndex, future := range normalizeFutures {
		var result activities.NormalizeS5ConceptPoolResultV1
		if err := future.Get(ctx, &result); err != nil {
			return fail(fmt.Sprintf("slot %d concept normalization failed", slotIndex), err)
		}
		if err := validateS5NormalizeResultV1(input, slotIndex, briefSHA256s[slotIndex], result); err != nil {
			return fail(fmt.Sprintf("slot %d concept normalization returned an invalid contract", slotIndex), err)
		}
		drafts[slotIndex] = result.Draft
	}

	coordinates := flattenS5ConceptsV1(drafts)
	if len(coordinates) != activities.MaxS5ConceptsPerDedupBatchV1 {
		return fail("normalized concept batch does not contain exactly 12 concepts", nil)
	}
	conceptSpecs := make([]diversity.ConceptSpecV1, len(coordinates))
	for index := range coordinates {
		conceptSpecs[index] = coordinates[index].card.Spec
	}

	state.Phase, state.Progress = S5MicroBatchPhaseDedup, 50
	var dedup activities.S5DedupObservationBatchResultV1
	if err := workflow.ExecuteActivity(activityContext, "ObserveS5ConceptDedupBatchV1", activities.S5DedupObservationBatchInputV1{
		PayloadVersion:     activities.S5DedupObservationBatchPayloadVersionV1,
		Concepts:           conceptSpecs,
		RequestedTopK:      input.DedupTopK,
		Threshold:          input.DedupThreshold,
		ExcludedLineageIDs: append([]string(nil), input.ExcludedLineageIDs...),
	}).Get(ctx, &dedup); err != nil {
		return fail("S5 concept dedup observation failed", err)
	}
	observations, err := validateS5DedupResultV1(input, coordinates, dedup)
	if err != nil {
		return fail("S5 concept dedup observation is unavailable or inconsistent", err)
	}
	state.CorpusRevision = dedup.CorpusRevision

	pools := make([]diversity.ConceptPoolV1, diversityapi.SlotCountV1)
	for slotIndex := range drafts {
		pool, _, _, finalizeErr := activities.FinalizeS5ConceptPoolV1(drafts[slotIndex], dedup.CorpusRevision)
		if finalizeErr != nil {
			return fail(fmt.Sprintf("slot %d concept pool finalization failed", slotIndex), finalizeErr)
		}
		pools[slotIndex] = pool
	}
	reservation, _, reservationSHA256, err := diversity.SelectMicroBatchV1(pools)
	if err != nil {
		return fail("S5 deterministic micro-batch selection failed", err)
	}
	state.ReservationSHA256 = reservationSHA256

	bindings := make([]activities.S5ConceptDedupBindingV1, len(coordinates))
	for index, coordinate := range coordinates {
		bindings[index] = activities.S5ConceptDedupBindingV1{
			SlotIndex:    coordinate.slotIndex,
			AttemptIndex: coordinate.attemptIndex,
			ConceptIndex: coordinate.conceptIndex,
			ConceptID:    coordinate.card.ConceptID,
			Observation:  observations[index],
		}
	}
	manifest := activities.S5ProvisionalReservationManifestV1{
		SchemaVersion:     activities.S5ProvisionalReservationManifestSchemaV1,
		BatchID:           input.BatchID,
		CorpusRevision:    dedup.CorpusRevision,
		ReservationSHA256: reservationSHA256,
		Reservation:       reservation,
		Pools:             append([]diversity.ConceptPoolV1(nil), pools...),
		DedupBindings:     bindings,
		ChildJobIDs:       append([]string(nil), childJobIDs...),
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		return fail("encode canonical S5 provisional reservation manifest", err)
	}
	expectedManifestSHA256 := diversity.SHA256Hex(manifestBytes)

	state.Phase, state.Progress = S5MicroBatchPhaseReserve, 65
	var stored activities.StoreS5ProvisionalReservationResultV1
	if err := workflow.ExecuteActivity(activityContext, "StoreS5ProvisionalReservationActivityV1", activities.StoreS5ProvisionalReservationInputV1{
		PayloadVersion: activities.StoreS5ProvisionalReservationPayloadVersionV1,
		Manifest:       manifest,
	}).Get(ctx, &stored); err != nil {
		return fail("store S5 provisional reservation manifest failed", err)
	}
	if stored.PayloadVersion != activities.StoreS5ProvisionalReservationPayloadVersionV1 ||
		stored.ManifestArtifact == nil || stored.ManifestSHA256 != expectedManifestSHA256 ||
		stored.ManifestArtifact.SHA256 != expectedManifestSHA256 {
		return fail("stored S5 provisional reservation manifest has an invalid CAS identity", nil)
	}
	state.ManifestSHA256 = stored.ManifestSHA256

	childInputs := make([]ProblemGenerationQualityInputV1, diversityapi.SlotCountV1)
	for slotIndex := range input.Slots {
		winner, ok := findS5ConceptCardV1(pools[slotIndex], reservation.Slots[slotIndex].Winner.ConceptID)
		if !ok {
			return fail(fmt.Sprintf("slot %d reserved winner is absent from its concept pool", slotIndex), nil)
		}
		childInput := input.Slots[slotIndex]
		childInput.FrozenConcept = winner.OneParagraphPitch
		childInput.Params.CustomPrompt = winner.OneParagraphPitch
		childInput.Params.MetadataExtras = cloneS5MetadataV1(childInput.Params.MetadataExtras)
		childInput.Params.MetadataExtras["s5_micro_batch_v1"] = map[string]interface{}{
			"schema_version":     s5ReservedChildMetadataSchemaV1,
			"selection_state":    "reserved",
			"parent_batch_id":    input.BatchID,
			"slot_index":         slotIndex,
			"slot_id":            reservation.Slots[slotIndex].SlotID,
			"pool_sha256":        reservation.Slots[slotIndex].PoolSHA256,
			"corpus_revision":    dedup.CorpusRevision,
			"reservation_sha256": reservationSHA256,
			"manifest_sha256":    stored.ManifestSHA256,
			"concept_id":         winner.ConceptID,
			"concept_signature":  reservation.Slots[slotIndex].Winner.StructuralSignatureSHA,
		}
		if err := validateProblemQualityInputV1(childInput); err != nil {
			return fail(fmt.Sprintf("slot %d reserved child input is invalid", slotIndex), err)
		}
		childInputs[slotIndex] = childInput
		state.Children[slotIndex].ConceptID = winner.ConceptID
		state.Children[slotIndex].Status = domain.WorkflowStatusRunning
	}

	// The CAS result above is the release point. Only now are all three
	// existing nine-gate children scheduled, again before awaiting any one.
	state.Phase, state.Progress = S5MicroBatchPhaseChildren, 75
	childFutures := make([]workflow.ChildWorkflowFuture, diversityapi.SlotCountV1)
	for slotIndex := range childInputs {
		childContext := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
			WorkflowID:               childJobIDs[slotIndex],
			WorkflowExecutionTimeout: input.ChildWorkflowTimeout,
			WorkflowRunTimeout:       input.ChildWorkflowTimeout,
			WorkflowTaskTimeout:      time.Minute,
			WorkflowIDReusePolicy:    enumspb.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
		})
		childFutures[slotIndex] = workflow.ExecuteChildWorkflow(childContext, ProblemGenerationQualityWorkflowV1, childInputs[slotIndex])
	}
	children := make([]S5MicroBatchChildResultV1, diversityapi.SlotCountV1)
	childErrors := make([]string, 0)
	for slotIndex, future := range childFutures {
		var result ProblemGenerationQualityResultV1
		if err := future.Get(ctx, &result); err != nil {
			state.Children[slotIndex].Status = domain.WorkflowStatusFailed
			childErrors = append(childErrors, fmt.Sprintf("slot %d: %v", slotIndex, err))
			continue
		}
		if result.PayloadVersion != ProblemGenerationQualityPayloadVersionV1 ||
			result.Decision != qualitygate.DecisionPass || strings.TrimSpace(result.StoredProblemID) == "" ||
			result.StoredProblemStatus != domain.ProblemStatusDraft {
			state.Children[slotIndex].Status = domain.WorkflowStatusFailed
			childErrors = append(childErrors, fmt.Sprintf("slot %d: invalid nine-gate child result", slotIndex))
			continue
		}
		state.Children[slotIndex].Status = domain.WorkflowStatusCompleted
		state.Children[slotIndex].Decision = result.Decision
		state.Children[slotIndex].StoredProblemID = result.StoredProblemID
		children[slotIndex] = S5MicroBatchChildResultV1{
			SlotIndex:           slotIndex,
			SlotID:              reservation.Slots[slotIndex].SlotID,
			JobID:               childJobIDs[slotIndex],
			ConceptID:           reservation.Slots[slotIndex].Winner.ConceptID,
			Decision:            result.Decision,
			StoredProblemID:     result.StoredProblemID,
			StoredProblemStatus: result.StoredProblemStatus,
		}
	}
	if len(childErrors) != 0 {
		return fail("one or more reserved nine-gate child workflows failed", fmt.Errorf("%s", strings.Join(childErrors, "; ")))
	}

	state.Phase = S5MicroBatchPhaseComplete
	state.Status = domain.WorkflowStatusCompleted
	state.Progress = 100
	return &S5MicroBatchResultV1{
		PayloadVersion:    S5MicroBatchPayloadVersionV1,
		BatchID:           input.BatchID,
		CorpusRevision:    dedup.CorpusRevision,
		Pools:             pools,
		DedupObservations: observations,
		Reservation:       reservation,
		ReservationSHA256: reservationSHA256,
		ManifestSHA256:    stored.ManifestSHA256,
		ManifestArtifact:  stored.ManifestArtifact,
		ChildJobIDs:       append([]string(nil), childJobIDs...),
		Children:          children,
	}, nil
}

func validateS5MicroBatchInputV1(input S5MicroBatchInputV1) (S5MicroBatchInputV1, []string, error) {
	result := input
	if result.PayloadVersion != S5MicroBatchPayloadVersionV1 || !diversityapi.IsBatchID(result.BatchID) {
		return S5MicroBatchInputV1{}, nil, fmt.Errorf("invalid S5 micro-batch identity")
	}
	if len(result.Slots) != diversityapi.SlotCountV1 {
		return S5MicroBatchInputV1{}, nil, fmt.Errorf("S5 micro-batch requires exactly %d slots", diversityapi.SlotCountV1)
	}
	if err := result.ConceptBudget.Validate(); err != nil {
		return S5MicroBatchInputV1{}, nil, fmt.Errorf("invalid S5 concept budget: %w", err)
	}
	if result.ConceptNetworkRetryBudget < 0 ||
		3*result.ConceptNetworkRetryBudget > result.ConceptBudget.MaxNetworkRetries ||
		3*(1+result.ConceptNetworkRetryBudget) > result.ConceptBudget.MaxModelCalls {
		return S5MicroBatchInputV1{}, nil, fmt.Errorf("S5 concept retry allowance exceeds its frozen pool budget")
	}
	if result.DedupTopK <= 0 || result.DedupTopK > 100 || math.IsNaN(result.DedupThreshold) ||
		math.IsInf(result.DedupThreshold, 0) || result.DedupThreshold < 0 || result.DedupThreshold > 1 {
		return S5MicroBatchInputV1{}, nil, fmt.Errorf("invalid S5 dedup configuration")
	}
	if result.ChildWorkflowTimeout <= 0 {
		return S5MicroBatchInputV1{}, nil, fmt.Errorf("S5 child workflow timeout must be positive")
	}

	result.Slots = append([]ProblemGenerationQualityInputV1(nil), result.Slots...)
	childJobIDs := make([]string, diversityapi.SlotCountV1)
	for slotIndex := range result.Slots {
		expectedID, err := diversityapi.ChildJobID(result.BatchID, slotIndex)
		if err != nil || result.Slots[slotIndex].SubjectID != expectedID {
			return S5MicroBatchInputV1{}, nil, fmt.Errorf("slot %d subject_id is not server-derived", slotIndex)
		}
		if err := validateProblemQualityInputV1(result.Slots[slotIndex]); err != nil {
			return S5MicroBatchInputV1{}, nil, fmt.Errorf("slot %d: %w", slotIndex, err)
		}
		childJobIDs[slotIndex] = expectedID
	}

	seenLineage := make(map[string]struct{}, len(result.ExcludedLineageIDs))
	result.ExcludedLineageIDs = append([]string(nil), result.ExcludedLineageIDs...)
	for index, rawID := range result.ExcludedLineageIDs {
		parsed, err := uuid.Parse(strings.TrimSpace(rawID))
		if err != nil || parsed == uuid.Nil {
			return S5MicroBatchInputV1{}, nil, fmt.Errorf("excluded_lineage_ids[%d] must be a UUID", index)
		}
		canonical := parsed.String()
		if _, duplicate := seenLineage[canonical]; duplicate {
			return S5MicroBatchInputV1{}, nil, fmt.Errorf("excluded lineage ID %s is duplicated", canonical)
		}
		seenLineage[canonical] = struct{}{}
		result.ExcludedLineageIDs[index] = canonical
	}
	sort.Strings(result.ExcludedLineageIDs)
	return result, childJobIDs, nil
}

func validateS5CreativeResultV1(input S5MicroBatchInputV1, slotIndex, attemptIndex int, briefSHA256 string, result activities.GenerateS5ConceptAttemptResultV1) error {
	slotID, _ := activities.S5SlotIDV1(input.BatchID, slotIndex)
	attemptID, _ := activities.S5LogicalAttemptIDV1(input.BatchID, slotIndex, attemptIndex)
	if result.PayloadVersion != activities.GenerateS5ConceptAttemptPayloadVersionV1 || result.BatchID != input.BatchID ||
		result.SlotIndex != slotIndex || result.SlotID != slotID || result.AttemptIndex != attemptIndex || result.BriefSHA256 != briefSHA256 ||
		result.Attempt.AttemptIndex != attemptIndex || result.Attempt.LogicalAttemptID != attemptID || len(result.Attempt.Concepts) != 2 {
		return fmt.Errorf("creative attempt identity mismatch")
	}
	for conceptIndex, card := range result.Attempt.Concepts {
		expectedID, _ := activities.S5ConceptIDV1(input.BatchID, slotIndex, attemptIndex, conceptIndex)
		if card.ConceptIndex != conceptIndex || card.ConceptID != expectedID {
			return fmt.Errorf("creative concept %d identity mismatch", conceptIndex)
		}
	}
	return nil
}

func validateS5NormalizeResultV1(input S5MicroBatchInputV1, slotIndex int, briefSHA256 string, result activities.NormalizeS5ConceptPoolResultV1) error {
	slotID, _ := activities.S5SlotIDV1(input.BatchID, slotIndex)
	if result.PayloadVersion != activities.NormalizeS5ConceptPoolPayloadVersionV1 || result.Draft.SchemaVersion != activities.S5NormalizedConceptPoolDraftSchemaV1 ||
		result.Draft.BatchID != input.BatchID || result.Draft.SlotIndex != slotIndex || result.Draft.SlotID != slotID || result.Draft.BriefSHA256 != briefSHA256 ||
		result.Draft.Budget != input.ConceptBudget || result.Draft.Normalization.NormalizerVersion != activities.S5ConceptNormalizerVersionV1 {
		return fmt.Errorf("normalized concept draft identity mismatch")
	}
	encoded, err := json.Marshal(result.Draft)
	if err != nil || result.DraftSHA256 != diversity.SHA256Hex(encoded) {
		return fmt.Errorf("normalized concept draft digest mismatch")
	}
	// Finalization supplies a temporary valid revision only to invoke the
	// activity package's complete canonical validator without mutating history.
	if _, _, _, err := activities.FinalizeS5ConceptPoolV1(result.Draft, strings.Repeat("0", 64)); err != nil {
		return err
	}
	return nil
}

func flattenS5ConceptsV1(drafts []activities.S5NormalizedConceptPoolDraftV1) []s5ConceptCoordinateV1 {
	result := make([]s5ConceptCoordinateV1, 0, activities.MaxS5ConceptsPerDedupBatchV1)
	for slotIndex, draft := range drafts {
		for attemptIndex, attempt := range draft.Attempts {
			for conceptIndex, card := range attempt.Concepts {
				result = append(result, s5ConceptCoordinateV1{slotIndex: slotIndex, attemptIndex: attemptIndex, conceptIndex: conceptIndex, card: card})
			}
		}
	}
	return result
}

func validateS5DedupResultV1(input S5MicroBatchInputV1, concepts []s5ConceptCoordinateV1, result activities.S5DedupObservationBatchResultV1) ([]diversity.DedupObservationV1, error) {
	if result.PayloadVersion != activities.S5DedupObservationBatchPayloadVersionV1 || !isS5SHA256V1(result.CorpusRevision) || len(result.Observations) != len(concepts) {
		return nil, fmt.Errorf("dedup batch shape, version, or corpus revision is invalid")
	}
	canonical := make([]diversity.DedupObservationV1, len(result.Observations))
	modelVersion := ""
	for index, observation := range result.Observations {
		encoded, _, err := diversity.CanonicalDedupObservationV1(observation)
		if err != nil {
			return nil, fmt.Errorf("observation %d: %w", index, err)
		}
		if err := json.Unmarshal(encoded, &canonical[index]); err != nil {
			return nil, fmt.Errorf("decode canonical observation %d: %w", index, err)
		}
		expectedHash, err := activities.S5ConceptContentHashV1(concepts[index].card.Spec)
		if err != nil {
			return nil, fmt.Errorf("concept %d content hash: %w", index, err)
		}
		observation = canonical[index]
		if observation.Stage != diversity.DedupStageConceptSelection || observation.Kind != diversity.DedupKindStructure ||
			observation.ContentHash != expectedHash || observation.CorpusRevision != result.CorpusRevision ||
			observation.RequestedTopK != input.DedupTopK || observation.Threshold != input.DedupThreshold ||
			!equalS5StringsV1(observation.ExcludedLineageIDs, input.ExcludedLineageIDs) {
			return nil, fmt.Errorf("observation %d is not bound to its concept or request", index)
		}
		// A structure neighbor is an advisory warning, never a tenth quality
		// gate. The activity contract for this stage only emits pass or warn.
		if observation.Decision != diversity.DedupDecisionPass && observation.Decision != diversity.DedupDecisionWarn {
			return nil, fmt.Errorf("observation %d has unavailable or unsupported decision %q", index, observation.Decision)
		}
		if strings.TrimSpace(observation.ModelVersion) == "" {
			return nil, fmt.Errorf("observation %d has empty model version", index)
		}
		if modelVersion == "" {
			modelVersion = observation.ModelVersion
		} else if observation.ModelVersion != modelVersion {
			return nil, fmt.Errorf("observation %d uses model version %q instead of frozen version %q", index, observation.ModelVersion, modelVersion)
		}
		if observation.Decision == diversity.DedupDecisionWarn &&
			(len(observation.Neighbors) == 0 || observation.Neighbors[0].Similarity < observation.Threshold) {
			return nil, fmt.Errorf("observation %d warning is not supported by a threshold neighbor", index)
		}
		if observation.Decision == diversity.DedupDecisionPass && len(observation.Neighbors) > 0 &&
			observation.Neighbors[0].Similarity >= observation.Threshold {
			return nil, fmt.Errorf("observation %d pass contradicts its threshold neighbor", index)
		}
	}
	return canonical, nil
}

func findS5ConceptCardV1(pool diversity.ConceptPoolV1, conceptID string) (diversity.ConceptCardV1, bool) {
	for _, attempt := range pool.Attempts {
		for _, card := range attempt.Concepts {
			if card.ConceptID == conceptID {
				return card, true
			}
		}
	}
	return diversity.ConceptCardV1{}, false
}

func cloneS5MetadataV1(source map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{}, len(source)+1)
	for key, value := range source {
		result[key] = value
	}
	return result
}

func cloneS5MicroBatchStateV1(source S5MicroBatchStateV1) S5MicroBatchStateV1 {
	result := source
	result.ChildJobIDs = append([]string(nil), source.ChildJobIDs...)
	result.Children = append([]S5MicroBatchChildStateV1(nil), source.Children...)
	return result
}

func isS5SHA256V1(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func equalS5StringsV1(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func s5MicroBatchContractErrorV1(message string, cause error) error {
	return temporal.NewNonRetryableApplicationError(message, s5MicroBatchContractErrorTypeV1, cause)
}
