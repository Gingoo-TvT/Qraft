package outbox

import (
	"context"
	"fmt"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
	"github.com/google/uuid"
)

type Queue interface {
	ClaimBatch(context.Context, int, time.Duration) ([]repository.OutboxEvent, error)
	MarkDelivered(context.Context, uuid.UUID, uuid.UUID) error
	MarkFailed(context.Context, uuid.UUID, uuid.UUID, string, time.Time, int) error
}

type BacklogQueue interface {
	Queue
	BacklogStats(context.Context) (repository.OutboxBacklogStats, error)
}

// Publisher performs an at-least-once remote delivery. Receivers must
// deduplicate using OutboxEvent.EventID.
type Publisher interface {
	Publish(context.Context, repository.OutboxEvent) error
}

type DispatchOptions struct {
	BatchSize  int
	Lease      time.Duration
	MaxAttempt int
}

type DispatchResult struct {
	Claimed   int  `json:"claimed"`
	Delivered int  `json:"delivered"`
	Retried   int  `json:"retried"`
	Dead      int  `json:"dead"`
	Disabled  bool `json:"disabled,omitempty"`
}

func Dispatch(ctx context.Context, queue Queue, publisher Publisher, options DispatchOptions) (*DispatchResult, error) {
	if queue == nil || publisher == nil {
		return nil, fmt.Errorf("outbox queue and publisher must be configured")
	}
	if options.BatchSize <= 0 || options.BatchSize > 1000 {
		return nil, fmt.Errorf("outbox batch size must be in [1,1000]")
	}
	if options.Lease <= 0 {
		return nil, fmt.Errorf("outbox lease must be positive")
	}
	if options.MaxAttempt <= 0 {
		return nil, fmt.Errorf("outbox max attempts must be positive")
	}

	events, err := queue.ClaimBatch(ctx, options.BatchSize, options.Lease)
	if err != nil {
		return nil, err
	}
	result := &DispatchResult{Claimed: len(events)}
	for _, event := range events {
		if err := publisher.Publish(ctx, event); err != nil {
			delay := retryDelay(event.AttemptCount)
			if markErr := queue.MarkFailed(
				ctx, event.EventID, event.LockToken, err.Error(), time.Now().Add(delay), options.MaxAttempt,
			); markErr != nil {
				return nil, fmt.Errorf("publish outbox event %s failed (%v), then failure acknowledgement failed: %w", event.EventID, err, markErr)
			}
			if event.AttemptCount >= options.MaxAttempt {
				result.Dead++
			} else {
				result.Retried++
			}
			continue
		}
		if err := queue.MarkDelivered(ctx, event.EventID, event.LockToken); err != nil {
			return nil, err
		}
		result.Delivered++
	}
	return result, nil
}

func retryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 8 {
		attempt = 8
	}
	return time.Duration(1<<(attempt-1)) * time.Second
}
