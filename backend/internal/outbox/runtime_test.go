package outbox

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
	"github.com/google/uuid"
)

type runtimeQueue struct {
	mu       sync.Mutex
	events   []repository.OutboxEvent
	stats    repository.OutboxBacklogStats
	statsErr error
}

func (q *runtimeQueue) ClaimBatch(_ context.Context, limit int, _ time.Duration) ([]repository.OutboxEvent, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if limit > len(q.events) {
		limit = len(q.events)
	}
	claimed := append([]repository.OutboxEvent(nil), q.events[:limit]...)
	q.events = q.events[limit:]
	return claimed, nil
}

func (q *runtimeQueue) MarkDelivered(_ context.Context, _ uuid.UUID, _ uuid.UUID) error {
	return nil
}

func (q *runtimeQueue) MarkFailed(_ context.Context, _ uuid.UUID, _ uuid.UUID, _ string, _ time.Time, _ int) error {
	return nil
}

func (q *runtimeQueue) BacklogStats(_ context.Context) (repository.OutboxBacklogStats, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.stats, q.statsErr
}

type runtimePublisher struct {
	err error
}

func (p runtimePublisher) Publish(context.Context, repository.OutboxEvent) error {
	return p.err
}

func runtimeOptions(enabled bool) RuntimeOptions {
	return RuntimeOptions{
		Enabled:      enabled,
		PollInterval: time.Hour,
		Dispatch: DispatchOptions{
			BatchSize:  10,
			Lease:      time.Minute,
			MaxAttempt: 3,
		},
		MaxQueued: 10,
		MaxAge:    time.Minute,
	}
}

func runRuntimeOnce(t *testing.T, runtime *Runtime) RuntimeStatus {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		runtime.Run(ctx)
		close(done)
	}()
	deadline := time.Now().Add(time.Second)
	for runtime.Snapshot().CheckedAt.IsZero() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-done
	return runtime.Snapshot()
}

func TestDisabledRuntimeStaysReadyWhenBacklogExists(t *testing.T) {
	queue := &runtimeQueue{stats: repository.OutboxBacklogStats{Pending: 1}}
	runtime, err := NewRuntime(queue, nil, runtimeOptions(false))
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	status := runRuntimeOnce(t, runtime)
	if !status.Ready || status.Reason != "" || status.Backlog.Pending != 1 {
		t.Fatalf("status = %+v", status)
	}

	request := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	response := httptest.NewRecorder()
	runtime.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("ready status = %d, body=%s", response.Code, response.Body.String())
	}
}

func TestDisabledRuntimeIsReadyOnlyWithEmptyBacklog(t *testing.T) {
	runtime, err := NewRuntime(&runtimeQueue{}, nil, runtimeOptions(false))
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	if status := runRuntimeOnce(t, runtime); !status.Ready {
		t.Fatalf("status = %+v", status)
	}
}

func TestRuntimeReadinessCoversFailuresDeadEventsAndThresholds(t *testing.T) {
	event := testEvent()
	event.LockToken = uuid.New()
	tests := []struct {
		name      string
		queue     *runtimeQueue
		publisher Publisher
		want      string
	}{
		{
			name:      "delivery failure",
			queue:     &runtimeQueue{events: []repository.OutboxEvent{event}, stats: repository.OutboxBacklogStats{Retry: 1}},
			publisher: runtimePublisher{err: errors.New("remote unavailable")},
			want:      "awaiting retry",
		},
		{
			name:      "queue depth",
			queue:     &runtimeQueue{stats: repository.OutboxBacklogStats{Pending: 11}},
			publisher: runtimePublisher{},
			want:      "exceeds threshold",
		},
		{
			name:      "dead event",
			queue:     &runtimeQueue{stats: repository.OutboxBacklogStats{Dead: 1}},
			publisher: runtimePublisher{},
			want:      "dead events",
		},
		{
			name:      "old event",
			queue:     &runtimeQueue{stats: repository.OutboxBacklogStats{Pending: 1, OldestUndeliveredAge: 2 * time.Minute}},
			publisher: runtimePublisher{},
			want:      "oldest outbox event",
		},
		{
			name:      "database failure",
			queue:     &runtimeQueue{statsErr: errors.New("database unavailable")},
			publisher: runtimePublisher{},
			want:      "database unavailable",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runtime, err := NewRuntime(tt.queue, tt.publisher, runtimeOptions(true))
			if err != nil {
				t.Fatalf("new runtime: %v", err)
			}
			status := runRuntimeOnce(t, runtime)
			if status.Ready || !strings.Contains(status.Reason, tt.want) {
				t.Fatalf("status = %+v, want reason %q", status, tt.want)
			}
		})
	}
}
