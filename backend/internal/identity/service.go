package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

const passwordCost = 12

type Options struct{ BootstrapToken string }
type Service struct {
	store         Repository
	options       Options
	passwordSlots chan struct{}
	dummyHash     []byte
	attempts      *AttemptLimiter
}

func NewService(store Repository, options Options) *Service {
	dummy, _ := bcrypt.GenerateFromPassword([]byte("qraft-login-timing-only-not-a-user-password"), passwordCost)
	return &Service{store: store, options: options, passwordSlots: make(chan struct{}, 4), dummyHash: dummy, attempts: NewAttemptLimiter(4096)}
}
func normalizeEmail(raw string) (string, error) {
	if strings.ContainsAny(raw, "\r\n\x00") {
		return "", ErrInvalid
	}
	value := strings.ToLower(strings.TrimSpace(raw))
	if len(value) > 254 || strings.ContainsAny(value, "\r\n") {
		return "", ErrInvalid
	}
	parsed, err := mail.ParseAddress(value)
	if err != nil || parsed.Address != value || !strings.Contains(value, "@") {
		return "", ErrInvalid
	}
	return value, nil
}
func validateName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if !utf8.ValidString(name) || utf8.RuneCountInString(name) > 80 || strings.ContainsAny(name, "\r\n\x00") {
		return "", ErrInvalid
	}
	return name, nil
}
func validatePassword(password string) error {
	if !utf8.ValidString(password) || utf8.RuneCountInString(password) < 12 || len(password) > 72 {
		return fmt.Errorf("%w: password must contain at least 12 characters and at most 72 UTF-8 bytes", ErrInvalid)
	}
	return nil
}
func randomToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}
func validToken(token string) bool {
	if len(token) != 43 {
		return false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	return err == nil && len(decoded) == 32
}
func tokenHash(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}
func csrfToken(token string) string {
	digest := sha256.Sum256([]byte("qraft-session-csrf-v1\x00" + token))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}
func (s *Service) ready() error {
	if s == nil || s.store == nil {
		return ErrUnavailable
	}
	return nil
}
func (s *Service) SetupRequired(ctx context.Context) (bool, error) {
	if err := s.ready(); err != nil {
		return false, err
	}
	return s.store.SetupRequired(ctx)
}
func (s *Service) passwordWork(ctx context.Context, work func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case s.passwordSlots <- struct{}{}:
		defer func() { <-s.passwordSlots }()
		return work()
	default:
		return ErrRateLimited
	}
}
func (s *Service) hashPassword(ctx context.Context, password string) ([]byte, error) {
	if err := validatePassword(password); err != nil {
		return nil, err
	}
	var hash []byte
	err := s.passwordWork(ctx, func() error {
		var err error
		hash, err = bcrypt.GenerateFromPassword([]byte(password), passwordCost)
		return err
	})
	return hash, err
}
func (s *Service) Login(ctx context.Context, email, password string) (SessionIssue, error) {
	if err := s.ready(); err != nil {
		return SessionIssue{}, err
	}
	normalized, err := normalizeEmail(email)
	if err != nil || len(password) > 72 || password == "" {
		return SessionIssue{}, ErrUnauthorized
	}
	if !s.attempts.Allow("login:"+normalized, 12, 15*time.Minute) {
		return SessionIssue{}, ErrRateLimited
	}
	credential, lookupErr := s.store.Credential(ctx, normalized)
	if lookupErr != nil && !errors.Is(lookupErr, ErrUnauthorized) {
		return SessionIssue{}, lookupErr
	}
	hash := credential.PasswordHash
	if lookupErr != nil || credential.User.Disabled {
		hash = s.dummyHash
	}
	compareErr := s.passwordWork(ctx, func() error { return bcrypt.CompareHashAndPassword(hash, []byte(password)) })
	if errors.Is(compareErr, ErrRateLimited) {
		return SessionIssue{}, compareErr
	}
	if compareErr != nil || lookupErr != nil || credential.User.Disabled {
		return SessionIssue{}, ErrUnauthorized
	}
	return s.issueSession(ctx, credential)
}
func (s *Service) issueSession(ctx context.Context, credential UserCredential) (SessionIssue, error) {
	token, err := randomToken()
	if err != nil {
		return SessionIssue{}, err
	}
	session, err := s.store.CreateSession(ctx, tokenHash(token), credential, time.Now().UTC().Add(SessionLifetime))
	if err != nil {
		return SessionIssue{}, err
	}
	session.CSRFToken = csrfToken(token)
	return SessionIssue{Session: session, Token: token}, nil
}
func (s *Service) Authenticate(ctx context.Context, token string) (Session, error) {
	if err := s.ready(); err != nil {
		return Session{}, err
	}
	if !validToken(token) {
		return Session{}, ErrUnauthorized
	}
	session, err := s.store.Authenticate(ctx, tokenHash(token))
	if err != nil {
		return Session{}, err
	}
	if session.User.ID == uuid.Nil || session.User.Disabled || (session.User.Role != "admin" && session.User.Role != "member") {
		return Session{}, ErrUnauthorized
	}
	session.CSRFToken = csrfToken(token)
	return session, nil
}
func (s *Service) Logout(ctx context.Context, token string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if !validToken(token) {
		return nil
	}
	return s.store.RevokeSession(ctx, tokenHash(token))
}
func (s *Service) Bootstrap(ctx context.Context, token, email, password, name string) (SessionIssue, error) {
	if err := s.ready(); err != nil {
		return SessionIssue{}, err
	}
	if len(s.options.BootstrapToken) < 24 || len(token) > 512 {
		return SessionIssue{}, ErrUnauthorized
	}
	expected, actual := sha256.Sum256([]byte(s.options.BootstrapToken)), sha256.Sum256([]byte(token))
	if subtle.ConstantTimeCompare(expected[:], actual[:]) != 1 {
		return SessionIssue{}, ErrUnauthorized
	}
	required, err := s.store.SetupRequired(ctx)
	if err != nil {
		return SessionIssue{}, err
	}
	if !required {
		return SessionIssue{}, ErrConflict
	}
	email, err = normalizeEmail(email)
	if err != nil {
		return SessionIssue{}, err
	}
	name, err = validateName(name)
	if err != nil {
		return SessionIssue{}, err
	}
	hash, err := s.hashPassword(ctx, password)
	if err != nil {
		return SessionIssue{}, err
	}
	user, err := s.store.Bootstrap(ctx, User{ID: uuid.New(), Email: email, DisplayName: name, Role: "admin"}, hash)
	if err != nil {
		return SessionIssue{}, err
	}
	return s.issueSession(ctx, UserCredential{User: user, PasswordHash: hash})
}
func (s *Service) Register(ctx context.Context, token, password, name string) (SessionIssue, error) {
	user, hash, err := s.consume(ctx, token, "register", password, name)
	if err != nil {
		return SessionIssue{}, err
	}
	return s.issueSession(ctx, UserCredential{User: user, PasswordHash: hash})
}
func (s *Service) ResetPassword(ctx context.Context, token, password string) error {
	_, _, err := s.consume(ctx, token, "reset", password, "")
	return err
}
func (s *Service) consume(ctx context.Context, token, kind, password, name string) (User, []byte, error) {
	if err := s.ready(); err != nil {
		return User{}, nil, err
	}
	if !validToken(token) {
		return User{}, nil, ErrUnauthorized
	}
	invitation, err := s.store.Invitation(ctx, tokenHash(token))
	if err != nil {
		return User{}, nil, err
	}
	if invitation.Kind != kind {
		return User{}, nil, ErrUnauthorized
	}
	name, err = validateName(name)
	if err != nil {
		return User{}, nil, err
	}
	hash, err := s.hashPassword(ctx, password)
	if err != nil {
		return User{}, nil, err
	}
	user, err := s.store.ConsumeInvitation(ctx, tokenHash(token), kind, hash, name)
	return user, hash, err
}
func (s *Service) ChangePassword(ctx context.Context, user User, current, next string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if !s.attempts.Allow("password:"+user.ID.String(), 12, 15*time.Minute) {
		return ErrRateLimited
	}
	credential, err := s.store.Credential(ctx, user.Email)
	if err != nil {
		return err
	}
	if credential.User.ID != user.ID || credential.User.Disabled || len(current) > 72 {
		return ErrUnauthorized
	}
	err = s.passwordWork(ctx, func() error { return bcrypt.CompareHashAndPassword(credential.PasswordHash, []byte(current)) })
	if errors.Is(err, ErrRateLimited) {
		return err
	}
	if err != nil {
		return ErrUnauthorized
	}
	hash, err := s.hashPassword(ctx, next)
	if err != nil {
		return err
	}
	return s.store.ChangePassword(ctx, user.ID, credential.PasswordHash, hash)
}
func (s *Service) ListUsers(ctx context.Context) ([]User, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	return s.store.ListUsers(ctx)
}
func (s *Service) UpdateUser(ctx context.Context, actor, id uuid.UUID, patch UserPatch) (User, error) {
	if err := s.ready(); err != nil {
		return User{}, err
	}
	if patch.Role == nil && patch.Disabled == nil || patch.Role != nil && *patch.Role != "admin" && *patch.Role != "member" {
		return User{}, ErrInvalid
	}
	return s.store.UpdateUser(ctx, actor, id, patch)
}
func (s *Service) ListInvitations(ctx context.Context) ([]Invitation, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	return s.store.ListInvitations(ctx)
}
func (s *Service) IssueInvitation(ctx context.Context, actor uuid.UUID, email, kind string) (InvitationIssue, error) {
	if err := s.ready(); err != nil {
		return InvitationIssue{}, err
	}
	email, err := normalizeEmail(email)
	if err != nil {
		return InvitationIssue{}, err
	}
	lifetime := 7 * 24 * time.Hour
	switch kind {
	case "register":
	case "reset":
		lifetime = time.Hour
	default:
		return InvitationIssue{}, ErrInvalid
	}
	token, err := randomToken()
	if err != nil {
		return InvitationIssue{}, err
	}
	invitation, err := s.store.IssueInvitation(ctx, actor, Invitation{ID: uuid.New(), Email: email, Kind: kind, ExpiresAt: time.Now().UTC().Add(lifetime)}, tokenHash(token))
	if err != nil {
		return InvitationIssue{}, err
	}
	return InvitationIssue{Invitation: invitation, Token: token}, nil
}
func (s *Service) Invitation(ctx context.Context, token string) (Invitation, error) {
	if err := s.ready(); err != nil {
		return Invitation{}, err
	}
	if !validToken(token) {
		return Invitation{}, ErrUnauthorized
	}
	return s.store.Invitation(ctx, tokenHash(token))
}
func (s *Service) RevokeInvitation(ctx context.Context, actor, id uuid.UUID) error {
	if err := s.ready(); err != nil {
		return err
	}
	return s.store.RevokeInvitation(ctx, actor, id)
}

// AttemptLimiter is a bounded process-local abuse guard. Full capacity rejects
// new keys rather than evicting live counters. The proxy supplies an additional
// network-level limit when deploying more than one API process.
type attempt struct {
	Count int
	Until time.Time
}
type AttemptLimiter struct {
	mu       sync.Mutex
	entries  map[string]attempt
	capacity int
	now      func() time.Time
}

func NewAttemptLimiter(capacity int) *AttemptLimiter {
	if capacity < 1 {
		capacity = 1
	}
	return &AttemptLimiter{entries: make(map[string]attempt), capacity: capacity, now: time.Now}
}
func (l *AttemptLimiter) Allow(key string, limit int, window time.Duration) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if len(key) > 512 || limit < 1 || window <= 0 {
		return false
	}
	entry, exists := l.entries[key]
	if exists && !entry.Until.After(now) {
		delete(l.entries, key)
		exists = false
	}
	if !exists {
		if len(l.entries) >= l.capacity {
			for k, v := range l.entries {
				if !v.Until.After(now) {
					delete(l.entries, k)
				}
			}
		}
		if len(l.entries) >= l.capacity {
			return false
		}
		entry = attempt{Until: now.Add(window)}
	}
	if entry.Count >= limit {
		return false
	}
	entry.Count++
	l.entries[key] = entry
	return true
}
