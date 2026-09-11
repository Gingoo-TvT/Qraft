package provenance

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

type retentionFixture struct {
	mu     sync.Mutex
	counts []int64
	err    error
	calls  int
	limits []int
}

func (f *retentionFixture) ExpireRetentionBatch(_ context.Context, limit int) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.limits = append(f.limits, limit)
	if f.err != nil {
		return 0, f.err
	}
	if len(f.counts) == 0 {
		return 0, nil
	}
	count := f.counts[0]
	f.counts = f.counts[1:]
	return count, nil
}

func retentionOptions() RetentionSchedulerOptions {
	return RetentionSchedulerOptions{
		Enabled: true, Interval: time.Hour, BatchSize: 2, MaxBatches: 3,
	}
}

func TestRetentionSchedulerDrainsUntilPartialBatch(t *testing.T) {
	fixture := &retentionFixture{counts: []int64{2, 2, 1}}
	scheduler, err := NewRetentionScheduler(fixture, retentionOptions())
	if err != nil {
		t.Fatal(err)
	}
	status := scheduler.runOnce(context.Background())
	if status.Error != "" || status.Exhausted || status.Expired != 5 || status.Batches != 3 {
		t.Fatalf("status = %+v", status)
	}
	if fixture.calls != 3 {
		t.Fatalf("calls = %d, want 3", fixture.calls)
	}
}

func TestRetentionSchedulerReportsExhaustionAndErrors(t *testing.T) {
	full := &retentionFixture{counts: []int64{2, 2, 2}}
	scheduler, err := NewRetentionScheduler(full, retentionOptions())
	if err != nil {
		t.Fatal(err)
	}
	if status := scheduler.runOnce(context.Background()); !status.Exhausted || status.Expired != 6 {
		t.Fatalf("exhausted status = %+v", status)
	}

	failed := &retentionFixture{err: errors.New("database unavailable")}
	scheduler, err = NewRetentionScheduler(failed, retentionOptions())
	if err != nil {
		t.Fatal(err)
	}
	if status := scheduler.runOnce(context.Background()); !strings.Contains(status.Error, "database unavailable") || status.Batches != 1 {
		t.Fatalf("failure status = %+v", status)
	}
}

func TestRetentionSchedulerRunsImmediatelyAndObservesStatus(t *testing.T) {
	fixture := &retentionFixture{}
	observed := make(chan RetentionRunStatus, 1)
	options := retentionOptions()
	options.Observe = func(status RetentionRunStatus) { observed <- status }
	scheduler, err := NewRetentionScheduler(fixture, options)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		scheduler.Run(ctx)
		close(done)
	}()
	select {
	case status := <-observed:
		if !status.Enabled || status.Batches != 1 || status.StartedAt.IsZero() || status.FinishedAt.IsZero() {
			t.Fatalf("observed status = %+v", status)
		}
	case <-time.After(time.Second):
		t.Fatal("scheduler did not run immediately")
	}
	cancel()
	<-done
}

func TestRetentionSchedulerValidatesBounds(t *testing.T) {
	fixture := &retentionFixture{}
	options := retentionOptions()
	options.BatchSize = 0
	if _, err := NewRetentionScheduler(fixture, options); err == nil {
		t.Fatal("zero batch size accepted")
	}
	options = retentionOptions()
	options.MaxBatches = 0
	if _, err := NewRetentionScheduler(fixture, options); err == nil {
		t.Fatal("zero max batches accepted")
	}
}
