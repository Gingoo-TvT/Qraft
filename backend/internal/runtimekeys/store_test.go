package runtimekeys

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

type fakeRedis struct {
	values map[string]string
	ttl    time.Duration
}

func (f *fakeRedis) Set(_ context.Context, key string, value interface{}, ttl time.Duration) *redis.StatusCmd {
	if f.values == nil {
		f.values = make(map[string]string)
	}
	f.values[key] = value.(string)
	f.ttl = ttl
	return redis.NewStatusResult("OK", nil)
}

func (f *fakeRedis) Get(_ context.Context, key string) *redis.StringCmd {
	value, ok := f.values[key]
	if !ok {
		return redis.NewStringResult("", redis.Nil)
	}
	return redis.NewStringResult(value, nil)
}

func TestStorePutReturnsReferenceAndResolvesKey(t *testing.T) {
	redisClient := &fakeRedis{}
	store := NewStore(redisClient, time.Hour)

	ref, err := store.Put(context.Background(), "  sk-test  ")
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	if !strings.HasPrefix(ref, RefPrefix) {
		t.Fatalf("ref = %q, want %q prefix", ref, RefPrefix)
	}
	if redisClient.ttl != time.Hour {
		t.Fatalf("ttl = %s, want 1h", redisClient.ttl)
	}

	value, err := store.ResolveAPIKey(context.Background(), ref)
	if err != nil {
		t.Fatalf("ResolveAPIKey() error = %v", err)
	}
	if value != "sk-test" {
		t.Fatalf("resolved key = %q, want sk-test", value)
	}
}

func TestStoreRejectsInvalidKeysAndRefs(t *testing.T) {
	store := NewStore(&fakeRedis{}, time.Hour)
	if _, err := store.Put(context.Background(), ""); err == nil {
		t.Fatal("empty key unexpectedly accepted")
	}
	if _, err := store.Put(context.Background(), "sk\nsecret"); err == nil {
		t.Fatal("key with control character unexpectedly accepted")
	}
	if _, err := store.ResolveAPIKey(context.Background(), "env:KEY"); err == nil {
		t.Fatal("non-runtime ref unexpectedly accepted")
	}
	if _, err := store.ResolveAPIKey(context.Background(), "runtime:bad/token"); err == nil {
		t.Fatal("invalid token unexpectedly accepted")
	}
}
