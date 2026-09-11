package activities

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
	"github.com/google/uuid"
)

type fakeOutboxQueue struct {
	event     repository.OutboxEvent
	claimErr  error
	delivered int
	failed    int
	ready     bool
}

func (q *fakeOutboxQueue) ClaimBatch(context.Context, int, time.Duration) ([]repository.OutboxEvent, error) {
	if q.claimErr != nil {
		return nil, q.claimErr
	}
	if !q.ready {
		return nil, nil
	}
	q.ready = false
	q.event.AttemptCount++
	q.event.LockToken = uuid.New()
	return []repository.OutboxEvent{q.event}, nil
}

func (q *fakeOutboxQueue) MarkDelivered(context.Context, uuid.UUID, uuid.UUID) error {
	q.delivered++
	return nil
}

func (q *fakeOutboxQueue) MarkFailed(context.Context, uuid.UUID, uuid.UUID, string, time.Time, int) error {
	q.failed++
	q.ready = true
	return nil
}

type failOncePublisher struct {
	calls int
}

func (p *failOncePublisher) Publish(context.Context, repository.OutboxEvent) error {
	p.calls++
	if p.calls == 1 {
		return errors.New("injected callback timeout")
	}
	return nil
}

func TestOutboxCallbackFailureIsRetriedAndAcknowledgedOnce(t *testing.T) {
	queue := &fakeOutboxQueue{
		ready: true,
		event: repository.OutboxEvent{EventID: uuid.New(), OperationKey: "op", EventType: "callback"},
	}
	publisher := &failOncePublisher{}
	activities := New(&Dependencies{
		OutboxQueue:     queue,
		OutboxPublisher: publisher,
		OutboxDispatch: OutboxDispatchOptions{
			BatchSize:  1,
			Lease:      time.Minute,
			MaxAttempt: 3,
		},
	})

	first, err := activities.DispatchOutboxActivity(context.Background(), DispatchOutboxInput{})
	if err != nil {
		t.Fatalf("first dispatch: %v", err)
	}
	if first.Retried != 1 || first.Delivered != 0 || queue.failed != 1 {
		t.Fatalf("unexpected first dispatch: %+v queue=%+v", first, queue)
	}
	second, err := activities.DispatchOutboxActivity(context.Background(), DispatchOutboxInput{})
	if err != nil {
		t.Fatalf("second dispatch: %v", err)
	}
	if second.Delivered != 1 || queue.delivered != 1 || publisher.calls != 2 {
		t.Fatalf("unexpected retry dispatch: %+v queue=%+v calls=%d", second, queue, publisher.calls)
	}
}

func TestOutboxDatabaseFailurePreventsCallback(t *testing.T) {
	queue := &fakeOutboxQueue{claimErr: errors.New("database unavailable")}
	publisher := &failOncePublisher{}
	activities := New(&Dependencies{
		OutboxQueue:     queue,
		OutboxPublisher: publisher,
		OutboxDispatch: OutboxDispatchOptions{
			BatchSize:  1,
			Lease:      time.Minute,
			MaxAttempt: 3,
		},
	})
	if _, err := activities.DispatchOutboxActivity(context.Background(), DispatchOutboxInput{}); err == nil {
		t.Fatal("claim failure was not returned")
	}
	if publisher.calls != 0 {
		t.Fatalf("publisher called %d times after claim failure", publisher.calls)
	}
}

func TestDisabledOutboxActivityIsExplicitNoop(t *testing.T) {
	queue := &fakeOutboxQueue{ready: true}
	result, err := New(&Dependencies{OutboxQueue: queue}).DispatchOutboxActivity(
		context.Background(), DispatchOutboxInput{},
	)
	if err != nil {
		t.Fatalf("disabled dispatch: %v", err)
	}
	if !result.Disabled || !queue.ready {
		t.Fatalf("disabled result = %+v, queue claimed=%v", result, !queue.ready)
	}
}
