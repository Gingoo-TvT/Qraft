package provenance

import (
	"context"
	"fmt"
	"time"
)

type RetentionExpirer interface {
	ExpireRetentionBatch(context.Context, int) (int64, error)
}

type RetentionSchedulerOptions struct {
	Enabled    bool
	Interval   time.Duration
	BatchSize  int
	MaxBatches int
	Observe    func(RetentionRunStatus)
}

type RetentionRunStatus struct {
	Enabled    bool      `json:"enabled"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
	Expired    int64     `json:"expired"`
	Batches    int       `json:"batches"`
	Exhausted  bool      `json:"exhausted"`
	Error      string    `json:"error,omitempty"`
}

// RetentionScheduler runs bounded, non-overlapping expiry scans. The repository
// uses SKIP LOCKED, so multiple worker processes may run this scheduler safely.
type RetentionScheduler struct {
	expirer RetentionExpirer
	options RetentionSchedulerOptions
}

func NewRetentionScheduler(expirer RetentionExpirer, options RetentionSchedulerOptions) (*RetentionScheduler, error) {
	if expirer == nil {
		return nil, fmt.Errorf("retention expirer is required")
	}
	if options.Interval <= 0 {
		return nil, fmt.Errorf("retention interval must be positive")
	}
	if options.BatchSize <= 0 || options.BatchSize > 10_000 {
		return nil, fmt.Errorf("retention batch size must be in [1,10000]")
	}
	if options.MaxBatches <= 0 {
		return nil, fmt.Errorf("retention max batches must be positive")
	}
	return &RetentionScheduler{expirer: expirer, options: options}, nil
}

func (s *RetentionScheduler) Run(ctx context.Context) {
	if !s.options.Enabled {
		s.observe(RetentionRunStatus{Enabled: false})
		return
	}
	s.runOnce(ctx)
	ticker := time.NewTicker(s.options.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.runOnce(ctx)
		}
	}
}

func (s *RetentionScheduler) runOnce(ctx context.Context) (status RetentionRunStatus) {
	status = RetentionRunStatus{Enabled: true, StartedAt: time.Now().UTC()}
	defer func() {
		status.FinishedAt = time.Now().UTC()
		s.observe(status)
	}()

	for batch := 0; batch < s.options.MaxBatches; batch++ {
		count, err := s.expirer.ExpireRetentionBatch(ctx, s.options.BatchSize)
		status.Batches++
		status.Expired += count
		if err != nil {
			status.Error = err.Error()
			return status
		}
		if count < int64(s.options.BatchSize) {
			return status
		}
	}
	status.Exhausted = true
	return status
}

func (s *RetentionScheduler) observe(status RetentionRunStatus) {
	if s.options.Observe != nil {
		s.options.Observe(status)
	}
}
