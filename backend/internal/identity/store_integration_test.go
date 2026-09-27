//go:build integration

package identity

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func identityTestStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" || os.Getenv("ALGOFORGE_INTEGRATION_DISPOSABLE_DB") != "true" {
		t.Skip("disposable integration database is required")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := "qraft_identity_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	name := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+name); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		_, dropErr := admin.Exec(context.Background(), "DROP SCHEMA "+name+" CASCADE")
		admin.Close()
		if dropErr != nil {
			t.Error(dropErr)
		}
	})
	migration, err := os.ReadFile(filepath.Join("..", "..", "migrations", "034_shared_service_identity.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, string(migration)); err != nil {
		t.Fatal(err)
	}
	return NewStore(pool)
}
func TestIdentityStoreLifecycleAndConcurrentGuards(t *testing.T) {
	ctx := context.Background()
	store := identityTestStore(t)
	s := NewService(store, Options{BootstrapToken: strings.Repeat("a", 32)})
	const password = "synthetic-strong-password"
	admin, err := s.Bootstrap(ctx, strings.Repeat("a", 32), "ADMIN@example.test", password, "Admin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Bootstrap(ctx, strings.Repeat("a", 32), "second@example.test", password, "Admin2"); !errors.Is(err, ErrConflict) {
		t.Fatal("bootstrap reused", err)
	}
	if _, err = s.IssueInvitation(ctx, uuid.New(), "one@example.test", "register"); !errors.Is(err, ErrForbidden) {
		t.Fatal("untrusted inviter", err)
	}
	invitation, err := s.IssueInvitation(ctx, admin.Session.User.ID, "One@example.test", "register")
	if err != nil {
		t.Fatal(err)
	}
	var storedHash string
	if err = store.pool.QueryRow(ctx, "SELECT token_hash FROM qraft_account_invitations WHERE id=$1", invitation.ID).Scan(&storedHash); err != nil {
		t.Fatal(err)
	}
	if storedHash == invitation.Token || storedHash != tokenHash(invitation.Token) {
		t.Fatal("invitation persisted raw secret")
	}
	type registration struct {
		issue SessionIssue
		err   error
	}
	result := make(chan registration, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			issue, err := s.Register(ctx, invitation.Token, password, "Member")
			result <- registration{issue, err}
		}()
	}
	wg.Wait()
	close(result)
	var member SessionIssue
	success := 0
	for r := range result {
		if r.err == nil {
			member = r.issue
			success++
		} else if !errors.Is(r.err, ErrUnauthorized) {
			t.Fatal(r.err)
		}
	}
	if success != 1 {
		t.Fatalf("invitation consumed %d times", success)
	}
	if _, err = s.Invitation(ctx, invitation.Token); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("used invitation remains valid", err)
	}
	if member.Session.User.Role != "member" || member.Session.User.Email != "one@example.test" {
		t.Fatal("invitation did not bind member/email")
	}
	disabled := true
	if _, err = s.UpdateUser(ctx, admin.Session.User.ID, member.Session.User.ID, UserPatch{Disabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Authenticate(ctx, member.Token); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("disabled session still works", err)
	}
	if _, err = s.Login(ctx, "one@example.test", password); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("disabled login works", err)
	}
	disabled = false
	if _, err = s.UpdateUser(ctx, admin.Session.User.ID, member.Session.User.ID, UserPatch{Disabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	member, err = s.Login(ctx, "one@example.test", password)
	if err != nil {
		t.Fatal(err)
	}
	before, err := store.Credential(ctx, "one@example.test")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ChangePassword(ctx, member.Session.User, password, "synthetic-new-password"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Authenticate(ctx, member.Token); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("password change did not revoke session", err)
	}
	if _, err = store.CreateSession(ctx, tokenHash("synthetic-racing-token"), before, time.Now().Add(time.Hour)); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("stale login race revived password", err)
	}
	if _, err = s.Login(ctx, "one@example.test", password); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("old password works", err)
	}
	member, err = s.Login(ctx, "one@example.test", "synthetic-new-password")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.pool.Exec(ctx, "UPDATE qraft_sessions SET expires_at=now()-interval '1 second' WHERE id=$1", member.Session.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Authenticate(ctx, member.Token); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("expired session works", err)
	}
	member, err = s.Login(ctx, "one@example.test", "synthetic-new-password")
	if err != nil {
		t.Fatal(err)
	}
	reset, err := s.IssueInvitation(ctx, admin.Session.User.ID, "one@example.test", "reset")
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := s.IssueInvitation(ctx, admin.Session.User.ID, "one@example.test", "reset")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ResetPassword(ctx, reset.Token, "another-reset-password"); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("superseded reset works", err)
	}
	if err = s.ResetPassword(ctx, replacement.Token, "another-reset-password"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Authenticate(ctx, member.Token); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("reset did not revoke sessions", err)
	}
	expiring, err := s.IssueInvitation(ctx, admin.Session.User.ID, "expiry@example.test", "register")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.pool.Exec(ctx, "UPDATE qraft_account_invitations SET expires_at=now()-interval '1 second' WHERE id=$1", expiring.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Register(ctx, expiring.Token, password, "Expired"); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("expired invitation works", err)
	}
	revoked, err := s.IssueInvitation(ctx, admin.Session.User.ID, "revoked@example.test", "register")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.RevokeInvitation(ctx, admin.Session.User.ID, revoked.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Invitation(ctx, revoked.Token); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("revoked invitation works", err)
	}
	demoted := "member"
	if _, err = s.UpdateUser(ctx, admin.Session.User.ID, admin.Session.User.ID, UserPatch{Role: &demoted}); !errors.Is(err, ErrConflict) {
		t.Fatal("last admin removed", err)
	}
	promoted := "admin"
	if _, err = s.UpdateUser(ctx, admin.Session.User.ID, member.Session.User.ID, UserPatch{Role: &promoted}); err != nil {
		t.Fatal(err)
	}
	outcomes := make(chan error, 2)
	for _, id := range []uuid.UUID{admin.Session.User.ID, member.Session.User.ID} {
		go func(id uuid.UUID) { _, err := s.UpdateUser(ctx, id, id, UserPatch{Role: &demoted}); outcomes <- err }(id)
	}
	count := 0
	for i := 0; i < 2; i++ {
		err := <-outcomes
		if err == nil {
			count++
		} else if !errors.Is(err, ErrConflict) {
			t.Fatal(err)
		}
	}
	if count != 1 {
		t.Fatal("concurrent last-admin guard", count)
	}
	var admins int
	if err = store.pool.QueryRow(ctx, "SELECT count(*) FROM qraft_users WHERE role='admin' AND NOT disabled").Scan(&admins); err != nil || admins != 1 {
		t.Fatal("administrator invariant failed", admins, err)
	}
}
func TestIdentityBootstrapConcurrentSingleWinner(t *testing.T) {
	store := identityTestStore(t)
	ctx := context.Background()
	results := make(chan error, 2)
	for _, email := range []string{"one@example.test", "two@example.test"} {
		go func(email string) {
			_, err := store.Bootstrap(ctx, User{ID: uuid.New(), Email: email}, []byte("synthetic-hash-unused-for-login"))
			results <- err
		}(email)
	}
	winners := 0
	for i := 0; i < 2; i++ {
		err := <-results
		if err == nil {
			winners++
		} else if !errors.Is(err, ErrConflict) {
			t.Fatal(err)
		}
	}
	if winners != 1 {
		t.Fatalf("bootstrap winners=%d", winners)
	}
}
