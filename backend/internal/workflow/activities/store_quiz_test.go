package activities

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
)

func TestQuizStableIDsAreDeterministicAndPositionScoped(t *testing.T) {
	key := "workflow/store-quiz/v1"
	idFor := func(index int) uuid.UUID {
		return uuid.NewSHA1(uuid.NameSpaceURL, []byte(fmt.Sprintf("algoforge:store-quiz:%s:%d", key, index)))
	}

	if idFor(0) != idFor(0) {
		t.Fatal("same operation and position produced different IDs")
	}
	if idFor(0) == idFor(1) {
		t.Fatal("different positions produced the same ID")
	}
}

// A small ledger double keeps the real StoreQuizActivity control flow, including
// its cached-result branch, without creating a database or storing fixtures.
type quizOwnerLedger struct {
	OperationLedger
	result  json.RawMessage
	begins  int
	onBegin func()
}

func (l *quizOwnerLedger) Begin(_ context.Context, _, _, _ string) (repository.WorkflowOperation, error) {
	l.begins++
	if l.onBegin != nil {
		l.onBegin()
	}
	if l.result != nil {
		return repository.WorkflowOperation{Status: repository.OperationStatusCompleted, ResultJSON: l.result}, nil
	}
	return repository.WorkflowOperation{}, nil
}
func (l *quizOwnerLedger) Complete(_ context.Context, _, _ string, result json.RawMessage) error {
	l.result = append(json.RawMessage(nil), result...)
	return nil
}
func (l *quizOwnerLedger) Fail(context.Context, string, string, string) error { return nil }

func TestStoreQuizPersistsTrustedOwnerBeforeFreshAndCachedResults(t *testing.T) {
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestActivityEnvironment()
	calls := 0
	ledger := &quizOwnerLedger{}
	ledger.onBegin = func() {
		require.Equal(t, ledger.begins, calls, "owner must be durable before consulting the write ledger")
	}
	acts := New(&Dependencies{
		QuizRepo: &repository.QuizRepository{}, OperationLedger: ledger,
		PersistWorkflowOwner: func(ctx context.Context, id string) error {
			calls++
			require.NotEmpty(t, id)
			require.Equal(t, activity.GetInfo(ctx).WorkflowExecution.ID, id)
			require.NotEqual(t, "untrusted-operation-owner", id)
			return nil
		},
	})
	env.RegisterActivity(acts.StoreQuizActivity)
	// An empty synthetic batch needs no database but goes through the actual
	// fresh-write/ledger-completion path on the first invocation.
	input := QuizStoreInput{PayloadVersion: ActivityPayloadVersion, IdempotencyKey: "untrusted-operation-owner/store-quiz/v1"}
	for i := 0; i < 2; i++ {
		value, err := env.ExecuteActivity(acts.StoreQuizActivity, input)
		require.NoError(t, err)
		var result QuizStoreResult
		require.NoError(t, value.Get(&result))
		require.Empty(t, result.InsertedIDs)
	}
	require.Equal(t, 2, calls)
	require.Equal(t, 2, ledger.begins)
	require.NotNil(t, ledger.result)
}

func TestStoreQuizOwnerFailureStopsFreshAndCachedResults(t *testing.T) {
	for _, cached := range []bool{false, true} {
		t.Run(fmt.Sprint(cached), func(t *testing.T) {
			suite := &testsuite.WorkflowTestSuite{}
			env := suite.NewTestActivityEnvironment()
			ledger := &quizOwnerLedger{}
			if cached {
				ledger.result = json.RawMessage(`{"inserted_ids":[]}`)
			}
			acts := New(&Dependencies{OperationLedger: ledger, PersistWorkflowOwner: func(context.Context, string) error { return errors.New("owner store unavailable") }})
			env.RegisterActivity(acts.StoreQuizActivity)
			_, err := env.ExecuteActivity(acts.StoreQuizActivity, QuizStoreInput{PayloadVersion: ActivityPayloadVersion, IdempotencyKey: "fixture/store-quiz/v1"})
			require.ErrorContains(t, err, "owner store unavailable")
			require.Zero(t, ledger.begins, "ownership failure must prevent a write or cached success")
		})
	}
}
