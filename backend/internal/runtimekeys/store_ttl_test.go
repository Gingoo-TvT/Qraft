package runtimekeys

import (
	"context"
	"testing"
	"time"
)

func TestStorePutWithTTLAllowsOnlyBoundedJobRetention(t *testing.T) {
	redisClient := &fakeRedis{}
	store := NewStore(redisClient, 6*time.Hour)

	if _, err := store.PutWithTTL(context.Background(), "job-secret", maxExtendedTTL); err != nil {
		t.Fatalf("PutWithTTL(max) error = %v", err)
	}
	if redisClient.ttl != 24*time.Hour+5*time.Minute {
		t.Fatalf("extended ttl = %s, want 24h5m", redisClient.ttl)
	}
	if _, err := store.PutWithTTL(context.Background(), "job-secret", maxExtendedTTL+time.Nanosecond); err == nil {
		t.Fatal("PutWithTTL accepted retention beyond the job maximum")
	}
	if _, err := store.PutWithTTL(context.Background(), "job-secret", 0); err == nil {
		t.Fatal("PutWithTTL accepted a non-positive ttl")
	}

	if _, err := store.Put(context.Background(), "legacy-secret"); err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	if redisClient.ttl != 6*time.Hour {
		t.Fatalf("default ttl = %s, want unchanged 6h", redisClient.ttl)
	}
}
