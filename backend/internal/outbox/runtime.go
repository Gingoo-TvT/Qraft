package outbox

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
)

type RuntimeOptions struct {
	Enabled       bool
	PollInterval  time.Duration
	Dispatch      DispatchOptions
	MaxQueued     int64
	MaxAge        time.Duration
	ObserveStatus func(RuntimeStatus)
}

type RuntimeStatus struct {
	Enabled      bool                          `json:"enabled"`
	Ready        bool                          `json:"ready"`
	Reason       string                        `json:"reason,omitempty"`
	CheckedAt    time.Time                     `json:"checked_at,omitempty"`
	LastDispatch DispatchResult                `json:"last_dispatch"`
	Backlog      repository.OutboxBacklogStats `json:"backlog"`
}

type Runtime struct {
	queue     BacklogQueue
	publisher Publisher
	options   RuntimeOptions

	mu     sync.RWMutex
	status RuntimeStatus
}

func NewRuntime(queue BacklogQueue, publisher Publisher, options RuntimeOptions) (*Runtime, error) {
	if queue == nil {
		return nil, fmt.Errorf("outbox queue must be configured")
	}
	if options.Enabled && publisher == nil {
		return nil, fmt.Errorf("enabled outbox runtime requires a publisher")
	}
	if options.PollInterval <= 0 || options.MaxQueued < 0 || options.MaxAge <= 0 {
		return nil, fmt.Errorf("invalid outbox runtime thresholds")
	}
	if options.Enabled {
		if options.Dispatch.BatchSize <= 0 || options.Dispatch.Lease <= 0 || options.Dispatch.MaxAttempt <= 0 {
			return nil, fmt.Errorf("invalid outbox dispatch options")
		}
	}
	return &Runtime{
		queue:     queue,
		publisher: publisher,
		options:   options,
		status: RuntimeStatus{
			Enabled: options.Enabled,
			Ready:   false,
			Reason:  "outbox backlog has not been checked",
		},
	}, nil
}

func (r *Runtime) Run(ctx context.Context) {
	r.runOnce(ctx)
	ticker := time.NewTicker(r.options.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.runOnce(ctx)
		}
	}
}

func (r *Runtime) Snapshot() RuntimeStatus {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.status
}

func (r *Runtime) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeStatusJSON(w, http.StatusOK, map[string]bool{"alive": true})
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		status := r.Snapshot()
		code := http.StatusOK
		if !status.Ready {
			code = http.StatusServiceUnavailable
		}
		writeStatusJSON(w, code, status)
	})
	return mux
}

func (r *Runtime) runOnce(ctx context.Context) {
	status := RuntimeStatus{Enabled: r.options.Enabled, CheckedAt: time.Now().UTC()}
	if r.options.Enabled {
		result, err := Dispatch(ctx, r.queue, r.publisher, r.options.Dispatch)
		if result != nil {
			status.LastDispatch = *result
		}
		if err != nil {
			status.Reason = err.Error()
		} else if result != nil && result.Dead > 0 {
			status.Reason = fmt.Sprintf("%d outbox deliveries exhausted their attempt budget", result.Dead)
		} else if result != nil && result.Retried > 0 {
			status.Reason = fmt.Sprintf("%d outbox deliveries failed and were scheduled for retry", result.Retried)
		}
	}

	stats, err := r.queue.BacklogStats(ctx)
	if err != nil {
		if status.Reason == "" {
			status.Reason = err.Error()
		} else {
			status.Reason += "; " + err.Error()
		}
		r.update(status)
		return
	}
	status.Backlog = stats

	switch {
	case !r.options.Enabled:
		// Local mode retains undelivered events without making Worker readiness depend on an intentionally disabled publisher.
	case stats.Dead > 0:
		status.Reason = fmt.Sprintf("outbox contains %d dead events", stats.Dead)
	case stats.Retry > 0:
		status.Reason = fmt.Sprintf("outbox contains %d events awaiting retry", stats.Retry)
	case stats.Queued() > r.options.MaxQueued:
		status.Reason = fmt.Sprintf("outbox queued backlog %d exceeds threshold %d", stats.Queued(), r.options.MaxQueued)
	case stats.OldestUndeliveredAge > r.options.MaxAge:
		status.Reason = fmt.Sprintf("oldest outbox event age %s exceeds threshold %s", stats.OldestUndeliveredAge.Round(time.Second), r.options.MaxAge)
	}
	status.Ready = status.Reason == ""
	r.update(status)
}

func (r *Runtime) update(status RuntimeStatus) {
	r.mu.Lock()
	r.status = status
	r.mu.Unlock()
	if r.options.ObserveStatus != nil {
		r.options.ObserveStatus(status)
	}
}

func writeStatusJSON(w http.ResponseWriter, status int, value interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
