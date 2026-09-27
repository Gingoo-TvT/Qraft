package identity

import (
	"bytes"
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func storeError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrUnauthorized
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23505" {
		return ErrConflict
	}
	return err
}
func scanUser(row pgx.Row) (User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Email, &u.DisplayName, &u.Role, &u.Disabled, &u.CreatedAt)
	return u, storeError(err)
}

const userColumns = "id,email,display_name,role,disabled,created_at"

func (s *Store) SetupRequired(ctx context.Context) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM qraft_users)").Scan(&exists)
	return !exists, err
}

// Serialize account/invitation mutations, including bootstrap and the last-admin
// check. Password hashing and network work always happen before this short lock.
func (s *Store) mutation(ctx context.Context) (pgx.Tx, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(723714982)")
	if err != nil {
		_ = tx.Rollback(ctx)
		return nil, err
	}
	return tx, nil
}
func requireAdmin(ctx context.Context, tx pgx.Tx, actor uuid.UUID) error {
	var allowed bool
	err := tx.QueryRow(ctx, "SELECT role='admin' AND NOT disabled FROM qraft_users WHERE id=$1 FOR SHARE", actor).Scan(&allowed)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && !allowed {
		return ErrForbidden
	}
	return err
}
func audit(ctx context.Context, tx pgx.Tx, actor uuid.UUID, action string, subject uuid.UUID) error {
	_, err := tx.Exec(ctx, "INSERT INTO qraft_identity_audit(actor_id,action,subject_id) VALUES($1,$2,$3)", actor, action, subject)
	return err
}
func (s *Store) Bootstrap(ctx context.Context, u User, hash []byte) (User, error) {
	tx, err := s.mutation(ctx)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback(ctx)
	var exists bool
	if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM qraft_users)").Scan(&exists); err != nil {
		return User{}, err
	}
	if exists {
		return User{}, ErrConflict
	}
	u, err = scanUser(tx.QueryRow(ctx, "INSERT INTO qraft_users(id,email,display_name,role,password_hash) VALUES($1,$2,$3,'admin',$4) RETURNING "+userColumns, u.ID, u.Email, u.DisplayName, hash))
	if err != nil {
		return User{}, err
	}
	if err = audit(ctx, tx, u.ID, "bootstrap", u.ID); err != nil {
		return User{}, err
	}
	return u, tx.Commit(ctx)
}
func (s *Store) Credential(ctx context.Context, email string) (UserCredential, error) {
	var c UserCredential
	err := s.pool.QueryRow(ctx, "SELECT "+userColumns+",password_hash FROM qraft_users WHERE email=$1", email).Scan(&c.User.ID, &c.User.Email, &c.User.DisplayName, &c.User.Role, &c.User.Disabled, &c.User.CreatedAt, &c.PasswordHash)
	return c, storeError(err)
}
func (s *Store) CreateSession(ctx context.Context, hash string, credential UserCredential, expires time.Time) (Session, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Session{}, err
	}
	defer tx.Rollback(ctx)
	var currentHash []byte
	var disabled bool
	if err = tx.QueryRow(ctx, "SELECT password_hash,disabled FROM qraft_users WHERE id=$1 FOR SHARE", credential.User.ID).Scan(&currentHash, &disabled); err != nil {
		return Session{}, storeError(err)
	}
	if disabled || !bytes.Equal(currentHash, credential.PasswordHash) {
		return Session{}, ErrUnauthorized
	}
	// Return current role/name; a concurrent role change must not be revived by login.
	u, err := scanUser(tx.QueryRow(ctx, "SELECT "+userColumns+" FROM qraft_users WHERE id=$1", credential.User.ID))
	if err != nil {
		return Session{}, err
	}
	session := Session{ID: uuid.New(), User: u, ExpiresAt: expires}
	_, err = tx.Exec(ctx, "INSERT INTO qraft_sessions(id,token_hash,user_id,expires_at) VALUES($1,$2,$3,$4)", session.ID, hash, u.ID, expires)
	if err != nil {
		return Session{}, err
	}
	_, err = tx.Exec(ctx, "DELETE FROM qraft_sessions WHERE id IN (SELECT id FROM qraft_sessions WHERE expires_at<now()-interval '7 days' OR revoked_at<now()-interval '7 days' LIMIT 100)")
	if err != nil {
		return Session{}, err
	}
	return session, tx.Commit(ctx)
}
func (s *Store) Authenticate(ctx context.Context, hash string) (Session, error) {
	var result Session
	err := s.pool.QueryRow(ctx, "SELECT s.id,s.expires_at,u.id,u.email,u.display_name,u.role,u.disabled,u.created_at FROM qraft_sessions s JOIN qraft_users u ON u.id=s.user_id WHERE s.token_hash=$1 AND s.revoked_at IS NULL AND s.expires_at>now() AND NOT u.disabled", hash).Scan(&result.ID, &result.ExpiresAt, &result.User.ID, &result.User.Email, &result.User.DisplayName, &result.User.Role, &result.User.Disabled, &result.User.CreatedAt)
	return result, storeError(err)
}
func (s *Store) RevokeSession(ctx context.Context, hash string) error {
	_, err := s.pool.Exec(ctx, "UPDATE qraft_sessions SET revoked_at=COALESCE(revoked_at,now()) WHERE token_hash=$1", hash)
	return err
}
func (s *Store) ChangePassword(ctx context.Context, id uuid.UUID, oldHash, newHash []byte) error {
	tx, err := s.mutation(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	result, err := tx.Exec(ctx, "UPDATE qraft_users SET password_hash=$3,updated_at=now() WHERE id=$1 AND password_hash=$2 AND NOT disabled", id, oldHash, newHash)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrUnauthorized
	}
	if _, err = tx.Exec(ctx, "UPDATE qraft_sessions SET revoked_at=COALESCE(revoked_at,now()) WHERE user_id=$1", id); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "UPDATE qraft_account_invitations SET revoked_at=COALESCE(revoked_at,now()) WHERE email=(SELECT email FROM qraft_users WHERE id=$1) AND kind='reset' AND used_at IS NULL", id); err != nil {
		return err
	}
	if err = audit(ctx, tx, id, "password_changed", id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Store) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.pool.Query(ctx, "SELECT "+userColumns+" FROM qraft_users ORDER BY created_at,id LIMIT 1000")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, u)
	}
	return result, rows.Err()
}
func (s *Store) UpdateUser(ctx context.Context, actor, id uuid.UUID, patch UserPatch) (User, error) {
	tx, err := s.mutation(ctx)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback(ctx)
	if err = requireAdmin(ctx, tx, actor); err != nil {
		return User{}, err
	}
	before, err := scanUser(tx.QueryRow(ctx, "SELECT "+userColumns+" FROM qraft_users WHERE id=$1 FOR UPDATE", id))
	if err != nil {
		return User{}, err
	}
	after := before
	if patch.Role != nil {
		after.Role = *patch.Role
	}
	if patch.Disabled != nil {
		after.Disabled = *patch.Disabled
	}
	if after.Role != "admin" && after.Role != "member" {
		return User{}, ErrInvalid
	}
	if before.Role == "admin" && !before.Disabled && (after.Role != "admin" || after.Disabled) {
		var count int
		if err = tx.QueryRow(ctx, "SELECT count(*) FROM qraft_users WHERE role='admin' AND NOT disabled").Scan(&count); err != nil {
			return User{}, err
		}
		if count <= 1 {
			return User{}, ErrConflict
		}
	}
	u, err := scanUser(tx.QueryRow(ctx, "UPDATE qraft_users SET role=$2,disabled=$3,updated_at=now() WHERE id=$1 RETURNING "+userColumns, id, after.Role, after.Disabled))
	if err != nil {
		return User{}, err
	}
	if before.Role != after.Role || before.Disabled != after.Disabled {
		if _, err = tx.Exec(ctx, "UPDATE qraft_sessions SET revoked_at=COALESCE(revoked_at,now()) WHERE user_id=$1", id); err != nil {
			return User{}, err
		}
	}
	if after.Disabled {
		if _, err = tx.Exec(ctx, "UPDATE qraft_account_invitations SET revoked_at=COALESCE(revoked_at,now()) WHERE email=$1 AND used_at IS NULL", after.Email); err != nil {
			return User{}, err
		}
	}
	if err = audit(ctx, tx, actor, "user_updated", id); err != nil {
		return User{}, err
	}
	return u, tx.Commit(ctx)
}
func scanInvitation(row pgx.Row) (Invitation, error) {
	var i Invitation
	err := row.Scan(&i.ID, &i.Email, &i.Kind, &i.ExpiresAt, &i.UsedAt, &i.RevokedAt, &i.CreatedAt)
	return i, storeError(err)
}

const invitationColumns = "id,email,kind,expires_at,used_at,revoked_at,created_at"

func (s *Store) ListInvitations(ctx context.Context) ([]Invitation, error) {
	rows, err := s.pool.Query(ctx, "SELECT "+invitationColumns+" FROM qraft_account_invitations ORDER BY created_at DESC,id LIMIT 200")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Invitation{}
	for rows.Next() {
		i, err := scanInvitation(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, i)
	}
	return result, rows.Err()
}
func (s *Store) IssueInvitation(ctx context.Context, actor uuid.UUID, i Invitation, hash string) (Invitation, error) {
	tx, err := s.mutation(ctx)
	if err != nil {
		return Invitation{}, err
	}
	defer tx.Rollback(ctx)
	if err = requireAdmin(ctx, tx, actor); err != nil {
		return Invitation{}, err
	}
	var exists, disabled bool
	err = tx.QueryRow(ctx, "SELECT disabled FROM qraft_users WHERE email=$1", i.Email).Scan(&disabled)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Invitation{}, err
	}
	exists = err == nil
	if i.Kind == "register" && exists || i.Kind == "reset" && (!exists || disabled) {
		return Invitation{}, ErrConflict
	}
	if _, err = tx.Exec(ctx, "UPDATE qraft_account_invitations SET revoked_at=COALESCE(revoked_at,now()) WHERE email=$1 AND kind=$2 AND used_at IS NULL", i.Email, i.Kind); err != nil {
		return Invitation{}, err
	}
	i, err = scanInvitation(tx.QueryRow(ctx, "INSERT INTO qraft_account_invitations(id,token_hash,email,kind,created_by,expires_at) VALUES($1,$2,$3,$4,$5,$6) RETURNING "+invitationColumns, i.ID, hash, i.Email, i.Kind, actor, i.ExpiresAt))
	if err != nil {
		return Invitation{}, err
	}
	if err = audit(ctx, tx, actor, "invitation_created", i.ID); err != nil {
		return Invitation{}, err
	}
	return i, tx.Commit(ctx)
}
func (s *Store) Invitation(ctx context.Context, hash string) (Invitation, error) {
	return scanInvitation(s.pool.QueryRow(ctx, "SELECT "+invitationColumns+" FROM qraft_account_invitations WHERE token_hash=$1 AND used_at IS NULL AND revoked_at IS NULL AND expires_at>now()", hash))
}
func (s *Store) ConsumeInvitation(ctx context.Context, hash, kind string, password []byte, name string) (User, error) {
	tx, err := s.mutation(ctx)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback(ctx)
	i, err := scanInvitation(tx.QueryRow(ctx, "SELECT "+invitationColumns+" FROM qraft_account_invitations WHERE token_hash=$1 AND kind=$2 AND used_at IS NULL AND revoked_at IS NULL AND expires_at>now() FOR UPDATE", hash, kind))
	if err != nil {
		return User{}, err
	}
	var u User
	if kind == "register" {
		u, err = scanUser(tx.QueryRow(ctx, "INSERT INTO qraft_users(id,email,display_name,role,password_hash) VALUES($1,$2,$3,'member',$4) RETURNING "+userColumns, uuid.New(), i.Email, name, password))
	} else if kind == "reset" {
		u, err = scanUser(tx.QueryRow(ctx, "UPDATE qraft_users SET password_hash=$2,updated_at=now() WHERE email=$1 AND NOT disabled RETURNING "+userColumns, i.Email, password))
	} else {
		return User{}, ErrInvalid
	}
	if err != nil {
		return User{}, err
	}
	if _, err = tx.Exec(ctx, "UPDATE qraft_account_invitations SET used_at=now() WHERE id=$1", i.ID); err != nil {
		return User{}, err
	}
	if _, err = tx.Exec(ctx, "UPDATE qraft_sessions SET revoked_at=COALESCE(revoked_at,now()) WHERE user_id=$1", u.ID); err != nil {
		return User{}, err
	}
	if err = audit(ctx, tx, u.ID, "invitation_consumed_"+kind, i.ID); err != nil {
		return User{}, err
	}
	return u, tx.Commit(ctx)
}
func (s *Store) RevokeInvitation(ctx context.Context, actor, id uuid.UUID) error {
	tx, err := s.mutation(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = requireAdmin(ctx, tx, actor); err != nil {
		return err
	}
	result, err := tx.Exec(ctx, "UPDATE qraft_account_invitations SET revoked_at=COALESCE(revoked_at,now()) WHERE id=$1", id)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrConflict
	}
	if err = audit(ctx, tx, actor, "invitation_revoked", id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
