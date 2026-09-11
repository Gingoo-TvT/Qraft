package activities

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
	"go.temporal.io/sdk/activity"
)

type OperationLedger interface {
	Begin(context.Context, string, string, string) (repository.WorkflowOperation, error)
	Complete(context.Context, string, string, json.RawMessage) error
	Fail(context.Context, string, string, string) error
	StepCompleted(context.Context, string, string, string) (bool, error)
	CompleteStep(context.Context, string, string, string) error
}

func (a *Activities) beginOperation(
	ctx context.Context,
	key, operationType, payloadSHA256 string,
) (repository.WorkflowOperation, error) {
	if a.deps == nil || a.deps.OperationLedger == nil {
		return repository.WorkflowOperation{}, fmt.Errorf("workflow operation ledger is not configured")
	}
	return a.deps.OperationLedger.Begin(ctx, key, operationType, payloadSHA256)
}

func (a *Activities) recordOperationFailure(ctx context.Context, key, payloadSHA256 string, failure error) {
	if failure == nil || a.deps == nil || a.deps.OperationLedger == nil {
		return
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := a.deps.OperationLedger.Fail(cleanupCtx, key, payloadSHA256, failure.Error()); err != nil {
		activity.GetLogger(ctx).Error("failed to record workflow operation failure", "operation_key", key, "error", err)
	}
}

func (a *Activities) completeOperation(ctx context.Context, key, payloadSHA256 string, result interface{}) error {
	encoded, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("encoding workflow operation result: %w", err)
	}
	if err := a.deps.OperationLedger.Complete(ctx, key, payloadSHA256, encoded); err != nil {
		return fmt.Errorf("completing workflow operation: %w", err)
	}
	return nil
}

// runOperationStep skips a completed stable assignment and records its content
// hash after success. External effects using this helper must themselves accept
// a stable key or be an idempotent assignment, since a crash can occur between
// the remote success and the step record commit.
func (a *Activities) runOperationStep(
	ctx context.Context,
	operationKey, stepKey, effectSHA256 string,
	effect func() error,
) error {
	if a.deps == nil || a.deps.OperationLedger == nil {
		return fmt.Errorf("workflow operation ledger is not configured")
	}
	completed, err := a.deps.OperationLedger.StepCompleted(ctx, operationKey, stepKey, effectSHA256)
	if err != nil {
		return err
	}
	if completed {
		return nil
	}
	if err := effect(); err != nil {
		return err
	}
	return a.deps.OperationLedger.CompleteStep(ctx, operationKey, stepKey, effectSHA256)
}

func sha256Bytes(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

func sha256JSON(value interface{}) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return sha256Bytes(encoded), nil
}
