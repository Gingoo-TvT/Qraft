//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func operationIntegrationPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		t.Fatal("DATABASE_URL is required for operation integration tests")
	}
	pool, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestWorkflowOperationLedgerAndOutboxIntegration(t *testing.T) {
	ctx := context.Background()
	pool := operationIntegrationPool(t)
	ledger := NewWorkflowOperationRepository(pool)
	key := "integration/" + uuid.NewString()
	hash := strings.Repeat("a", 64)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM workflow_outbox WHERE operation_key=$1`, key)
		_, _ = pool.Exec(context.Background(), `DELETE FROM workflow_operations WHERE operation_key=$1`, key)
	})

	operation, err := ledger.Begin(ctx, key, "integration/v1", hash)
	if err != nil || operation.AttemptCount != 1 {
		t.Fatalf("first begin = %+v, err=%v", operation, err)
	}
	operation, err = ledger.Begin(ctx, key, "integration/v1", hash)
	if err != nil || operation.AttemptCount != 2 {
		t.Fatalf("second begin = %+v, err=%v", operation, err)
	}
	if _, err := ledger.Begin(ctx, key, "integration/v1", strings.Repeat("b", 64)); err == nil {
		t.Fatal("operation key accepted a different payload")
	}
	if err := ledger.CompleteStep(ctx, key, "minio:fixture", hash); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.StepCompleted(ctx, key, "minio:fixture", strings.Repeat("b", 64)); err == nil {
		t.Fatal("operation step accepted different content")
	}
	result := json.RawMessage(`{"status":"ok"}`)
	if err := ledger.Complete(ctx, key, hash, result); err != nil {
		t.Fatal(err)
	}
	operation, err = ledger.Begin(ctx, key, "integration/v1", hash)
	if err != nil || operation.Status != OperationStatusCompleted || !jsonEqual(operation.ResultJSON, result) {
		t.Fatalf("cached operation = %+v, err=%v", operation, err)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO workflow_outbox (
			operation_key,event_type,aggregate_type,aggregate_id,payload_json,created_at
		) VALUES ($1,'integration.callback','fixture',$1,'{}',NOW()-INTERVAL '2 minutes')`, key); err != nil {
		t.Fatal(err)
	}
	outbox := NewOutboxRepository(pool)
	stats, err := outbox.BacklogStats(ctx)
	if err != nil || stats.Pending < 1 || stats.Queued() < 1 || stats.OldestUndeliveredAge < time.Minute {
		t.Fatalf("pending backlog stats = %+v, err=%v", stats, err)
	}
	events, err := outbox.ClaimBatch(ctx, 1, time.Minute)
	if err != nil || len(events) != 1 {
		t.Fatalf("first claim = %+v, err=%v", events, err)
	}
	stats, err = outbox.BacklogStats(ctx)
	if err != nil || stats.Processing < 1 {
		t.Fatalf("processing backlog stats = %+v, err=%v", stats, err)
	}
	if err := outbox.MarkFailed(ctx, events[0].EventID, events[0].LockToken, "timeout", time.Now().Add(-time.Second), 3); err != nil {
		t.Fatal(err)
	}
	stats, err = outbox.BacklogStats(ctx)
	if err != nil || stats.Retry < 1 {
		t.Fatalf("retry backlog stats = %+v, err=%v", stats, err)
	}
	events, err = outbox.ClaimBatch(ctx, 1, time.Minute)
	if err != nil || len(events) != 1 {
		t.Fatalf("retry claim = %+v, err=%v", events, err)
	}
	if err := outbox.MarkDelivered(ctx, events[0].EventID, events[0].LockToken); err != nil {
		t.Fatal(err)
	}
}

func TestProviderEffectLeaseAndResultCacheIntegration(t *testing.T) {
	ctx := context.Background()
	pool := operationIntegrationPool(t)
	repo := NewProviderEffectRepository(pool)
	key := "integration:provider-effect:" + uuid.NewString()
	requestHash := strings.Repeat("c", 64)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM provider_effects WHERE effect_key=$1`, key)
	})

	first, err := repo.Acquire(ctx, key, "integration/v1", requestHash, time.Minute)
	if err != nil || first.State != ProviderEffectAcquired || first.LeaseToken == uuid.Nil {
		t.Fatalf("first claim = %+v, err=%v", first, err)
	}
	busy, err := repo.Acquire(ctx, key, "integration/v1", requestHash, time.Minute)
	if err != nil || busy.State != ProviderEffectBusy || busy.LeaseToken != first.LeaseToken {
		t.Fatalf("busy claim = %+v, err=%v", busy, err)
	}
	if err := repo.Fail(ctx, key, "integration/v1", requestHash, first.LeaseToken, "retryable fixture"); err != nil {
		t.Fatal(err)
	}
	reacquired, err := repo.Acquire(ctx, key, "integration/v1", requestHash, time.Minute)
	if err != nil || reacquired.State != ProviderEffectAcquired || reacquired.LeaseToken == first.LeaseToken {
		t.Fatalf("reacquired claim = %+v, err=%v", reacquired, err)
	}
	result := json.RawMessage(`{"z":2,"a":1}`)
	if err := repo.Complete(ctx, key, "integration/v1", requestHash, first.LeaseToken, result); err == nil {
		t.Fatal("completion with the expired lease token succeeded")
	}
	if err := repo.Complete(ctx, key, "integration/v1", requestHash, reacquired.LeaseToken, result); err != nil {
		t.Fatal(err)
	}
	if err := repo.Complete(ctx, key, "integration/v1", requestHash, reacquired.LeaseToken, json.RawMessage(`{ "a": 1, "z": 2 }`)); err != nil {
		t.Fatalf("semantically identical JSON completion: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE provider_effects SET result_json='{}'::jsonb WHERE effect_key=$1`, key); err == nil {
		t.Fatal("provider effect result was mutated without a matching canonical digest")
	}
	cached, err := repo.Acquire(ctx, key, "integration/v1", requestHash, time.Minute)
	if err != nil || cached.State != ProviderEffectCompleted || !jsonEqual(cached.ResultJSON, result) {
		t.Fatalf("cached claim = %+v, err=%v", cached, err)
	}
	if _, err := repo.Acquire(ctx, key, "integration/v1", strings.Repeat("d", 64), time.Minute); err == nil {
		t.Fatal("provider effect key accepted a different request")
	}
}
