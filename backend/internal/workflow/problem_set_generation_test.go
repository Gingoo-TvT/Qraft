package workflow

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/workflow/activities"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	tw "go.temporal.io/sdk/workflow"
)

type setWorkflowHarness struct {
	env                                   *testsuite.TestWorkflowEnvironment
	mu                                    sync.Mutex
	statuses                              map[int]string
	attached                              map[int]bool
	programCalls, quizCalls, active, peak int
	finish                                ProblemSetFinishInput
	more                                  bool
}

func newSetWorkflowHarness(t *testing.T, slots []ProblemSetPreparedSlot) *setWorkflowHarness {
	t.Helper()
	suite := &testsuite.WorkflowTestSuite{}
	h := &setWorkflowHarness{env: suite.NewTestWorkflowEnvironment(), statuses: map[int]string{}, attached: map[int]bool{}}
	h.env.RegisterActivityWithOptions(func(context.Context, domain.ProblemSetGenerationRef) (*ProblemSetBatch, error) {
		return &ProblemSetBatch{Slots: slots}, nil
	}, activity.RegisterOptions{Name: "PrepareProblemSetBatchActivity"})
	h.env.RegisterActivityWithOptions(func(_ context.Context, in ProblemSetSlotInput) error {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.statuses[in.Slot.Position] = in.Slot.Status
		return nil
	}, activity.RegisterOptions{Name: "RecordProblemSetSlotActivity"})
	h.env.RegisterActivityWithOptions(func(_ context.Context, in ProblemSetSlotInput) error {
		h.mu.Lock()
		defer h.mu.Unlock()
		if h.attached[in.Slot.Position] {
			return fmt.Errorf("duplicate attachment")
		}
		h.attached[in.Slot.Position] = true
		h.statuses[in.Slot.Position] = "succeeded"
		return nil
	}, activity.RegisterOptions{Name: "AttachProblemSetSlotActivity"})
	h.env.RegisterActivityWithOptions(func(_ context.Context, in ProblemSetFinishInput) (bool, error) {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.finish = in
		return h.more, nil
	}, activity.RegisterOptions{Name: "FinishProblemSetGenerationActivity"})
	enter := func(program bool) {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.active++
		if h.active > h.peak {
			h.peak = h.active
		}
		if program {
			h.programCalls++
		} else {
			h.quizCalls++
		}
	}
	leave := func() { h.mu.Lock(); defer h.mu.Unlock(); h.active-- }
	h.env.RegisterWorkflowWithOptions(func(ctx tw.Context, in ProblemGenerationQualityInputV1) (*ProblemGenerationQualityResultV1, error) {
		enter(true)
		defer leave()
		if err := tw.Sleep(ctx, time.Second); err != nil {
			return nil, err
		}
		if in.SubjectID == "reject" {
			return &ProblemGenerationQualityResultV1{Decision: "quarantine", StoredProblemID: uuid.NewString()}, nil
		}
		return &ProblemGenerationQualityResultV1{Decision: "pass", StoredProblemID: uuid.NewString()}, nil
	}, tw.RegisterOptions{Name: "ProblemGenerationQualityWorkflowV1"})
	h.env.RegisterWorkflowWithOptions(func(ctx tw.Context, in ProblemSetQuizInput) (*activities.QuizStoreResult, error) {
		enter(false)
		defer leave()
		if err := tw.Sleep(ctx, time.Minute); err != nil {
			return nil, err
		}
		return &activities.QuizStoreResult{InsertedIDs: []uuid.UUID{uuid.New()}}, nil
	}, tw.RegisterOptions{Name: "ProblemSetQuizGenerationWorkflow"})
	return h
}

func setWorkflowSlots(count int) []ProblemSetPreparedSlot {
	types := []domain.QuizType{domain.QuizTypeProgramming, domain.QuizTypeChoice, domain.QuizTypeFillBlank, domain.QuizTypeJudge}
	slots := make([]ProblemSetPreparedSlot, count)
	for i := range slots {
		typ := types[i%len(types)]
		slots[i] = ProblemSetPreparedSlot{Slot: domain.ProblemSetGenerationSlot{Position: i + 1, Type: typ, ChildID: fmt.Sprintf("child-%d", i+1)}, Programming: &ProblemGenerationQualityInputV1{SubjectID: fmt.Sprintf("problem-%d", i)}, Quiz: &activities.QuizGenerateInput{Type: typ, Count: 1}}
	}
	return slots
}

func TestProblemSetWorkflowMixedTypesBoundedParallelAndPartialFailure(t *testing.T) {
	slots := setWorkflowSlots(10)
	slots[0].Programming.SubjectID = "reject"
	recovered := uuid.New()
	slots[2].Slot.QuizID = &recovered
	slots[2].Quiz = nil
	h := newSetWorkflowHarness(t, slots)
	h.env.ExecuteWorkflow(ProblemSetGenerationWorkflow, domain.ProblemSetGenerationRef{SetID: uuid.New(), RunID: "run"})
	require.NoError(t, h.env.GetWorkflowError())
	require.Len(t, h.attached, 9)
	require.False(t, h.attached[1], "quarantined programming candidate must never enter the set")
	require.True(t, h.attached[3], "already stored result should be attached without regenerating")
	require.Equal(t, 3, h.programCalls)
	require.Equal(t, 6, h.quizCalls)
	require.Equal(t, "failed", h.statuses[1])
	require.Greater(t, h.peak, 1)
	require.LessOrEqual(t, h.peak, ProblemSetConcurrency)
}

func TestProblemSetWorkflowContinuesWithoutRepeatingCompletedBatch(t *testing.T) {
	h := newSetWorkflowHarness(t, setWorkflowSlots(4))
	h.more = true
	h.env.ExecuteWorkflow(ProblemSetGenerationWorkflow, domain.ProblemSetGenerationRef{SetID: uuid.New(), RunID: "run"})
	require.True(t, tw.IsContinueAsNewError(h.env.GetWorkflowError()))
	require.Len(t, h.attached, 4)
	require.Empty(t, h.finish.Error)
}

func TestProblemSetWorkflowCancelKeepsCompletedItems(t *testing.T) {
	h := newSetWorkflowHarness(t, setWorkflowSlots(8))
	h.env.RegisterDelayedCallback(h.env.CancelWorkflow, 10*time.Second)
	h.env.ExecuteWorkflow(ProblemSetGenerationWorkflow, domain.ProblemSetGenerationRef{SetID: uuid.New(), RunID: "run"})
	require.Error(t, h.env.GetWorkflowError())
	require.True(t, h.finish.Cancelled)
	require.Greater(t, len(h.attached), 0)
	require.Less(t, len(h.attached), 8)
}

func TestProblemSetQuizWorkflowRepairsOnceAndOnlyStoresApprovedDraft(t *testing.T) {
	for _, approvedSecond := range []bool{true, false} {
		t.Run(fmt.Sprint(approvedSecond), func(t *testing.T) {
			suite := &testsuite.WorkflowTestSuite{}
			env := suite.NewTestWorkflowEnvironment()
			calls, stores, reviews := 0, 0, 0
			env.RegisterActivityWithOptions(func(_ context.Context, in activities.QuizGenerateInput) (*activities.QuizGenerateResult, error) {
				calls++
				if calls == 2 {
					require.Contains(t, in.CustomPrompt, "答案不一致")
				}
				return &activities.QuizGenerateResult{Drafts: []activities.QuizDraft{{Title: fmt.Sprint(calls), Statement: "question", Answers: []string{"对"}, Explanation: "reason"}}}, nil
			}, activity.RegisterOptions{Name: "GenerateQuizActivity"})
			env.RegisterActivityWithOptions(func(context.Context, activities.ProblemSetQuizReviewInput) (*activities.ProblemSetQuizReviewResult, error) {
				reviews++
				return &activities.ProblemSetQuizReviewResult{Approved: reviews == 2 && approvedSecond, Feedback: "答案不一致"}, nil
			}, activity.RegisterOptions{Name: "ReviewProblemSetQuizActivity"})
			env.RegisterActivityWithOptions(func(_ context.Context, in activities.QuizStoreInput) (*activities.QuizStoreResult, error) {
				stores++
				require.Equal(t, "2", in.Drafts[0].Title)
				require.Contains(t, in.IdempotencyKey, "/store-quiz/v1")
				return &activities.QuizStoreResult{InsertedIDs: []uuid.UUID{uuid.New()}}, nil
			}, activity.RegisterOptions{Name: "StoreQuizActivity"})
			env.ExecuteWorkflow(ProblemSetQuizGenerationWorkflow, ProblemSetQuizInput{Generate: activities.QuizGenerateInput{Type: domain.QuizTypeJudge, Count: 1}})
			require.Equal(t, 2, calls)
			if approvedSecond {
				require.NoError(t, env.GetWorkflowError())
				require.Equal(t, 1, stores)
			} else {
				var application *temporal.ApplicationError
				require.ErrorAs(t, env.GetWorkflowError(), &application)
				require.Equal(t, "QuizQualityNotMet", application.Type())
				require.Zero(t, stores)
			}
		})
	}
}
