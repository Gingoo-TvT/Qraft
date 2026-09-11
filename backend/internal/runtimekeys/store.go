package runtimekeys

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	RefPrefix        = "runtime:"
	defaultKeyPrefix = "algoforge:runtime-keys:"
	defaultTTL       = 6 * time.Hour
	maxAPIKeyBytes   = 8192
	maxExtendedTTL   = 24*time.Hour + 5*time.Minute
)

type RedisClient interface {
	Set(context.Context, string, interface{}, time.Duration) *redis.StatusCmd
	Get(context.Context, string) *redis.StringCmd
}

type Store struct {
	redis     RedisClient
	ttl       time.Duration
	keyPrefix string
}

func NewStore(redis RedisClient, ttl time.Duration) *Store {
	if ttl <= 0 {
		ttl = defaultTTL
	}
	return &Store{
		redis:     redis,
		ttl:       ttl,
		keyPrefix: defaultKeyPrefix,
	}
}

func IsRuntimeRef(ref string) bool {
	return strings.HasPrefix(strings.TrimSpace(ref), RefPrefix)
}

func (s *Store) Put(ctx context.Context, apiKey string) (string, error) {
	return s.put(ctx, apiKey, s.ttl)
}

func (s *Store) PutWithTTL(ctx context.Context, apiKey string, ttl time.Duration) (string, error) {
	if ttl <= 0 || ttl > maxExtendedTTL {
		return "", fmt.Errorf("runtime api key ttl must be in (0,%s]", maxExtendedTTL)
	}
	return s.put(ctx, apiKey, ttl)
}

func (s *Store) put(ctx context.Context, apiKey string, ttl time.Duration) (string, error) {
	if s == nil || s.redis == nil {
		return "", fmt.Errorf("runtime key store is not configured")
	}
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return "", fmt.Errorf("runtime api key must not be empty")
	}
	if len(apiKey) > maxAPIKeyBytes {
		return "", fmt.Errorf("runtime api key is too long")
	}
	if hasControlRune(apiKey) {
		return "", fmt.Errorf("runtime api key contains control characters")
	}

	token, err := newToken()
	if err != nil {
		return "", err
	}
	if err := s.redis.Set(ctx, s.storageKey(token), apiKey, ttl).Err(); err != nil {
		return "", fmt.Errorf("storing runtime api key: %w", err)
	}
	return RefPrefix + token, nil
}

func (s *Store) ResolveAPIKey(ctx context.Context, ref string) (string, error) {
	if s == nil || s.redis == nil {
		return "", fmt.Errorf("runtime key store is not configured")
	}
	token, err := parseRef(ref)
	if err != nil {
		return "", err
	}
	value, err := s.redis.Get(ctx, s.storageKey(token)).Result()
	if err != nil {
		if err == redis.Nil {
			return "", fmt.Errorf("runtime api key reference has expired or does not exist")
		}
		return "", fmt.Errorf("resolving runtime api key: %w", err)
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("runtime api key reference resolved to an empty value")
	}
	return value, nil
}

func (s *Store) storageKey(token string) string {
	return s.keyPrefix + token
}

func parseRef(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if !strings.HasPrefix(ref, RefPrefix) {
		return "", fmt.Errorf("runtime api key ref must start with %s", RefPrefix)
	}
	token := strings.TrimPrefix(ref, RefPrefix)
	if token == "" {
		return "", fmt.Errorf("runtime api key ref token must not be empty")
	}
	for _, r := range token {
		if r != '-' && r != '_' && (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return "", fmt.Errorf("runtime api key ref token is invalid")
		}
	}
	return token, nil
}

func newToken() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generating runtime api key token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

func hasControlRune(value string) bool {
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}
