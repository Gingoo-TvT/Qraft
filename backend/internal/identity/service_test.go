package identity

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

func TestIdentityLimiterBoundsAndWindow(t *testing.T) {
	limiter := NewAttemptLimiter(2)
	now := time.Unix(1000, 0)
	limiter.now = func() time.Time { return now }
	if !limiter.Allow("a", 2, time.Minute) || !limiter.Allow("a", 2, time.Minute) || limiter.Allow("a", 2, time.Minute) {
		t.Fatal("attempt limit failed")
	}
	if !limiter.Allow("b", 1, time.Minute) || limiter.Allow("c", 1, time.Minute) {
		t.Fatal("capacity must fail closed")
	}
	now = now.Add(time.Minute)
	if !limiter.Allow("c", 1, time.Minute) || !limiter.Allow("a", 1, time.Minute) {
		t.Fatal("expired keys must be reclaimable")
	}
}
func TestIdentityPasswordAndEmailValidation(t *testing.T) {
	for _, password := range []string{"", "short", strings.Repeat("x", 73), strings.Repeat("题", 25)} {
		if validatePassword(password) == nil {
			t.Fatal("accepted invalid password")
		}
	}
	if validatePassword("这是一个长度充分的中文测试密码") != nil {
		t.Fatal("reasonable unicode password rejected")
	}
	for _, email := range []string{"x", "Name <a@example.test>", "a@example.test\n", "a@@b.test"} {
		if _, err := normalizeEmail(email); err == nil {
			t.Fatalf("accepted %q", email)
		}
	}
	normalized, err := normalizeEmail(" User@Example.test ")
	if err != nil || normalized != "user@example.test" {
		t.Fatal(normalized, err)
	}
}
func TestIdentityTokenAndCSRFSeparated(t *testing.T) {
	a, err := randomToken()
	if err != nil {
		t.Fatal(err)
	}
	b, err := randomToken()
	if err != nil {
		t.Fatal(err)
	}
	if a == b || !validToken(a) || len(tokenHash(a)) != 64 || tokenHash(a) == a || csrfToken(a) == a || csrfToken(a) == csrfToken(b) {
		t.Fatal("token separation failed")
	}
	if validToken(strings.Repeat("a", 42)) || validToken(strings.Repeat("!", 43)) {
		t.Fatal("invalid token accepted")
	}
}

type credentialRepository struct {
	Repository
	credential  UserCredential
	sessionHash string
	revoked     string
	lookupErr   error
}

func (r *credentialRepository) Credential(context.Context, string) (UserCredential, error) {
	return r.credential, r.lookupErr
}
func (r *credentialRepository) CreateSession(_ context.Context, hash string, c UserCredential, expiry time.Time) (Session, error) {
	r.sessionHash = hash
	return Session{ID: uuid.New(), User: c.User, ExpiresAt: expiry}, nil
}
func (r *credentialRepository) Authenticate(_ context.Context, hash string) (Session, error) {
	if hash != r.sessionHash || hash == r.revoked {
		return Session{}, ErrUnauthorized
	}
	return Session{User: r.credential.User, ExpiresAt: time.Now().Add(time.Hour)}, nil
}
func (r *credentialRepository) RevokeSession(_ context.Context, hash string) error {
	r.revoked = hash
	return nil
}
func TestIdentityLoginStoresOnlyHashAndRevocationIsImmediate(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("synthetic-test-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	repo := &credentialRepository{credential: UserCredential{User: User{ID: uuid.New(), Email: "one@example.test", Role: "member"}, PasswordHash: hash}}
	s := NewService(repo, Options{})
	if _, err = s.Login(context.Background(), "one@example.test", "incorrect"); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("wrong password accepted", err)
	}
	issue, err := s.Login(context.Background(), "ONE@example.test", "synthetic-test-password")
	if err != nil {
		t.Fatal(err)
	}
	if repo.sessionHash == issue.Token || repo.sessionHash != tokenHash(issue.Token) {
		t.Fatal("raw token stored")
	}
	session, err := s.Authenticate(context.Background(), issue.Token)
	if err != nil || session.CSRFToken != issue.Session.CSRFToken {
		t.Fatal("session csrf not stable", err)
	}
	if err = s.Logout(context.Background(), issue.Token); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Authenticate(context.Background(), issue.Token); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("revoked session remained valid", err)
	}
}
func TestIdentityPasswordConcurrencyIsBounded(t *testing.T) {
	s := &Service{passwordSlots: make(chan struct{}, 1)}
	s.passwordSlots <- struct{}{}
	called := false
	err := s.passwordWork(context.Background(), func() error { called = true; return nil })
	if !errors.Is(err, ErrRateLimited) || called {
		t.Fatal("saturated password worker should reject")
	}
}
