package workflow

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/workflow/activities"
	"github.com/stretchr/testify/require"
)

func TestPrepareProblemGenerationReviewRepairV1PreservesBasePrompt(t *testing.T) {
	params := domain.ProblemGenParams{
		CustomPrompt: "Keep the graph topic.",
		MetadataExtras: map[string]interface{}{
			"batch_id": "batch-7",
		},
	}
	review := activities.ReviewResult{
		Issues:              []string{"The invariant is ambiguous."},
		Suggestions:         []string{"Define the invariant before the input section."},
		EstimatedDifficulty: 1900,
	}

	next, decision, err := prepareProblemGenerationReviewRepairV1(params, review)
	require.NoError(t, err)
	require.True(t, decision.Continue)
	require.Equal(t, 1, decision.RepairRound)
	require.Len(t, decision.FeedbackSHA256, 64)
	require.Contains(t, next.CustomPrompt, "Keep the graph topic.")
	require.Contains(t, next.CustomPrompt, "bounded review repair 1/3")
	require.Contains(t, next.CustomPrompt, "The invariant is ambiguous.")
	require.Equal(t, "batch-7", next.MetadataExtras["batch_id"])

	state, found, err := loadProblemGenerationReviewRepairStateV1(next)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "Keep the graph topic.", state.BaseCustomPrompt)
	require.Equal(t, 1, state.RepairRounds)
	require.Equal(t, []string{decision.FeedbackSHA256}, state.FeedbackSHA256)
	require.Equal(t, 1, state.ConsecutiveSameFeedback)
}

func TestPrepareProblemGenerationReviewRepairV1StopsOnRepeatedFeedback(t *testing.T) {
	params := domain.ProblemGenParams{CustomPrompt: "Keep the requested topic."}
	firstReview := activities.ReviewResult{
		Issues:              []string{"Missing edge case", "Ambiguous bound"},
		Suggestions:         []string{"Add the zero case"},
		EstimatedDifficulty: 1700,
	}
	var firstDecision problemGenerationReviewRepairDecisionV1
	var err error
	params, firstDecision, err = prepareProblemGenerationReviewRepairV1(params, firstReview)
	require.NoError(t, err)
	require.True(t, firstDecision.Continue)

	secondReview := activities.ReviewResult{
		Issues:              []string{"  ambiguous   BOUND ", "missing EDGE case"},
		Suggestions:         []string{"add the ZERO case"},
		EstimatedDifficulty: 1700,
	}
	params, secondDecision, err := prepareProblemGenerationReviewRepairV1(params, secondReview)
	require.NoError(t, err)
	require.False(t, secondDecision.Continue)
	require.Equal(t, 1, secondDecision.RepairRound)
	require.Equal(t, "same_feedback_repeated", secondDecision.StopReason)
	require.Equal(t, firstDecision.FeedbackSHA256, secondDecision.FeedbackSHA256)

	stored, err := problemGenerationReviewRepairStoreParamsV1(params, false)
	require.NoError(t, err)
	require.Equal(t, "Keep the requested topic.", stored.CustomPrompt)
	require.NotContains(t, stored.MetadataExtras, problemGenerationReviewRepairStateKeyV1)
	summary, ok := stored.MetadataExtras[problemGenerationReviewRepairSummaryKeyV1].(map[string]interface{})
	require.True(t, ok)
	require.Equal(t, "repair_exhausted", summary["outcome"])
	require.Equal(t, "same_feedback_repeated", summary["stop_reason"])
	require.Equal(t, 1, summary["repair_rounds"])
	require.Equal(t, 2, summary["consecutive_same_feedback"])
}

func TestPrepareProblemGenerationReviewRepairV1StopsAfterThreeRepairRounds(t *testing.T) {
	params := domain.ProblemGenParams{}
	for round := 1; round <= problemGenerationReviewRepairMaxRoundsV1; round++ {
		var decision problemGenerationReviewRepairDecisionV1
		var err error
		params, decision, err = prepareProblemGenerationReviewRepairV1(params, activities.ReviewResult{
			Issues: []string{fmt.Sprintf("distinct defect %d", round)},
		})
		require.NoError(t, err)
		require.True(t, decision.Continue)
		require.Equal(t, round, decision.RepairRound)
	}

	params, decision, err := prepareProblemGenerationReviewRepairV1(params, activities.ReviewResult{
		Issues: []string{"distinct final defect"},
	})
	require.NoError(t, err)
	require.False(t, decision.Continue)
	require.Equal(t, problemGenerationReviewRepairMaxRoundsV1, decision.RepairRound)
	require.Equal(t, "max_repair_rounds_exhausted", decision.StopReason)

	stored, err := problemGenerationReviewRepairStoreParamsV1(params, false)
	require.NoError(t, err)
	summary := stored.MetadataExtras[problemGenerationReviewRepairSummaryKeyV1].(map[string]interface{})
	require.Equal(t, problemGenerationReviewRepairMaxRoundsV1, summary["repair_rounds"])
	require.Equal(t, "max_repair_rounds_exhausted", summary["stop_reason"])
	require.Len(t, summary["feedback_sha256"], problemGenerationReviewRepairMaxRoundsV1+1)
}

func TestProblemGenerationReviewRepairStoreParamsV1RecordsApproval(t *testing.T) {
	params, _, err := prepareProblemGenerationReviewRepairV1(domain.ProblemGenParams{
		CustomPrompt: "Original request",
	}, activities.ReviewResult{Issues: []string{"wrong complexity"}})
	require.NoError(t, err)

	stored, err := problemGenerationReviewRepairStoreParamsV1(params, true)
	require.NoError(t, err)
	require.Equal(t, "Original request", stored.CustomPrompt)
	summary := stored.MetadataExtras[problemGenerationReviewRepairSummaryKeyV1].(map[string]interface{})
	require.Equal(t, "approved_after_repair", summary["outcome"])
	require.Empty(t, summary["stop_reason"])
}

func TestProblemGenerationReviewRepairPromptIsBounded(t *testing.T) {
	longIssue := strings.Repeat("界", problemGenerationReviewRepairPromptRunesV1*2)
	params, _, err := prepareProblemGenerationReviewRepairV1(domain.ProblemGenParams{}, activities.ReviewResult{
		Issues: []string{longIssue},
	})
	require.NoError(t, err)
	require.LessOrEqual(t, len([]rune(params.CustomPrompt)), problemGenerationReviewRepairPromptRunesV1+1)
}

func TestStripProblemGenerationReviewRepairStateV1RejectsCallerOwnedState(t *testing.T) {
	params := domain.ProblemGenParams{MetadataExtras: map[string]interface{}{
		"batch_id":                                "batch-9",
		problemGenerationReviewRepairStateKeyV1:   "forged-state",
		problemGenerationReviewRepairSummaryKeyV1: "forged-summary",
	}}

	StripProblemGenerationReviewRepairStateV1(&params)
	require.Equal(t, map[string]interface{}{"batch_id": "batch-9"}, params.MetadataExtras)
}
