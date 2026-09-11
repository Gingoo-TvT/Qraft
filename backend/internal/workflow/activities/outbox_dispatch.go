package activities

import (
	"context"
	"fmt"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/outbox"
)

type OutboxQueue = outbox.Queue
type OutboxPublisher = outbox.Publisher
type OutboxDispatchOptions = outbox.DispatchOptions

type DispatchOutboxInput struct {
	BatchSize  int           `json:"batch_size"`
	Lease      time.Duration `json:"lease"`
	MaxAttempt int           `json:"max_attempt"`
}

type DispatchOutboxResult = outbox.DispatchResult

func (a *Activities) DispatchOutboxActivity(ctx context.Context, _ DispatchOutboxInput) (*DispatchOutboxResult, error) {
	if a.deps == nil || a.deps.OutboxQueue == nil {
		return nil, fmt.Errorf("outbox queue must be configured")
	}
	// Disabled workers keep the activity registered for compatibility, but do
	// not claim events that they cannot publish. Runtime readiness still fails
	// whenever a durable backlog exists.
	if a.deps.OutboxPublisher == nil {
		return &outbox.DispatchResult{Disabled: true}, nil
	}
	return outbox.Dispatch(ctx, a.deps.OutboxQueue, a.deps.OutboxPublisher, a.deps.OutboxDispatch)
}
