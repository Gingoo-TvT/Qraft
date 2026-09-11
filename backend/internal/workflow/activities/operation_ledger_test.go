package activities

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
)

type memoryOperationLedger struct {
	mu          sync.Mutex
	steps       map[string]string
	checkErr    error
	completeErr error
}

func (m *memoryOperationLedger) Begin(context.Context, string, string, string) (repository.WorkflowOperation, error) {
	return repository.WorkflowOperation{}, nil
}

func (m *memoryOperationLedger) Complete(context.Context, string, string, json.RawMessage) error {
	return nil
}

func (m *memoryOperationLedger) Fail(context.Context, string, string, string) error {
	return nil
}

func (m *memoryOperationLedger) StepCompleted(_ context.Context, operationKey, stepKey, effectHash string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.checkErr != nil {
		return false, m.checkErr
	}
	stored, ok := m.steps[operationKey+"\x00"+stepKey]
	if ok && stored != effectHash {
		return false, errors.New("effect hash conflict")
	}
	return ok, nil
}

func (m *memoryOperationLedger) CompleteStep(_ context.Context, operationKey, stepKey, effectHash string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.completeErr != nil {
		return m.completeErr
	}
	if m.steps == nil {
		m.steps = make(map[string]string)
	}
	m.steps[operationKey+"\x00"+stepKey] = effectHash
	return nil
}

func TestRecoverableSideEffectsRetryThenCache(t *testing.T) {
	for _, step := range []string{"db:projection", "minio:object", "vector:embedding"} {
		t.Run(step, func(t *testing.T) {
			ledger := &memoryOperationLedger{steps: make(map[string]string)}
			activities := New(&Dependencies{OperationLedger: ledger})
			calls := 0
			effect := func() error {
				calls++
				if calls == 1 {
					return errors.New("injected side-effect failure")
				}
				return nil
			}

			if err := activities.runOperationStep(context.Background(), "operation", step, sha256Bytes([]byte("v1")), effect); err == nil {
				t.Fatal("injected failure was not returned")
			}
			if err := activities.runOperationStep(context.Background(), "operation", step, sha256Bytes([]byte("v1")), effect); err != nil {
				t.Fatalf("retry failed: %v", err)
			}
			if err := activities.runOperationStep(context.Background(), "operation", step, sha256Bytes([]byte("v1")), effect); err != nil {
				t.Fatalf("cached retry failed: %v", err)
			}
			if calls != 2 {
				t.Fatalf("effect calls = %d, want 2", calls)
			}
			if err := activities.runOperationStep(context.Background(), "operation", step, sha256Bytes([]byte("changed")), effect); err == nil {
				t.Fatal("completed step accepted different content")
			}
		})
	}
}

func TestRecoverableSideEffectLedgerFailurePreventsRemoteCall(t *testing.T) {
	ledger := &memoryOperationLedger{checkErr: errors.New("database unavailable")}
	activities := New(&Dependencies{OperationLedger: ledger})
	calls := 0
	err := activities.runOperationStep(context.Background(), "operation", "minio:object", sha256Bytes(nil), func() error {
		calls++
		return nil
	})
	if err == nil || calls != 0 {
		t.Fatalf("ledger failure err=%v calls=%d, want error and zero remote calls", err, calls)
	}
}
