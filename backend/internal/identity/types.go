// Package identity implements accounts for a single shared Qraft workspace.
package identity

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	ErrUnauthorized = errors.New("invalid credentials or invitation")
	ErrForbidden    = errors.New("operation not permitted")
	ErrInvalid      = errors.New("invalid account input")
	ErrConflict     = errors.New("account state changed or conflicts")
	ErrRateLimited  = errors.New("too many account requests")
	ErrUnavailable  = errors.New("account service unavailable")
)

const (
	SessionCookieName = "qraft_session"
	SessionLifetime   = 7 * 24 * time.Hour
)

type User struct {
	ID          uuid.UUID "json:\"id\""
	Email       string    "json:\"email\""
	DisplayName string    "json:\"display_name\""
	Role        string    "json:\"role\""
	Disabled    bool      "json:\"disabled\""
	CreatedAt   time.Time "json:\"created_at\""
}
type UserCredential struct {
	User         User
	PasswordHash []byte
}
type Session struct {
	ID        uuid.UUID
	User      User
	CSRFToken string
	ExpiresAt time.Time
}
type SessionIssue struct {
	Session Session
	Token   string
}
type Invitation struct {
	ID        uuid.UUID  "json:\"id\""
	Email     string     "json:\"email\""
	Kind      string     "json:\"kind\""
	ExpiresAt time.Time  "json:\"expires_at\""
	UsedAt    *time.Time "json:\"used_at,omitempty\""
	RevokedAt *time.Time "json:\"revoked_at,omitempty\""
	CreatedAt time.Time  "json:\"created_at\""
}
type InvitationIssue struct {
	Invitation
	Token string "json:\"token\""
}
type UserPatch struct {
	Role     *string "json:\"role,omitempty\""
	Disabled *bool   "json:\"disabled,omitempty\""
}

type Repository interface {
	SetupRequired(context.Context) (bool, error)
	Bootstrap(context.Context, User, []byte) (User, error)
	Credential(context.Context, string) (UserCredential, error)
	CreateSession(context.Context, string, UserCredential, time.Time) (Session, error)
	Authenticate(context.Context, string) (Session, error)
	RevokeSession(context.Context, string) error
	ChangePassword(context.Context, uuid.UUID, []byte, []byte) error
	ListUsers(context.Context) ([]User, error)
	UpdateUser(context.Context, uuid.UUID, uuid.UUID, UserPatch) (User, error)
	ListInvitations(context.Context) ([]Invitation, error)
	IssueInvitation(context.Context, uuid.UUID, Invitation, string) (Invitation, error)
	Invitation(context.Context, string) (Invitation, error)
	ConsumeInvitation(context.Context, string, string, []byte, string) (User, error)
	RevokeInvitation(context.Context, uuid.UUID, uuid.UUID) error
}
