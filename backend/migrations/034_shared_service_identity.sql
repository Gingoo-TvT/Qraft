-- Single shared workspace: local accounts, revocable sessions, one-use invitations.
CREATE TABLE IF NOT EXISTS qraft_users (
 id UUID PRIMARY KEY,
 email TEXT NOT NULL UNIQUE CHECK (email = lower(email) AND length(email) <= 254),
 display_name TEXT NOT NULL DEFAULT '',
 role TEXT NOT NULL CHECK (role IN ('admin','member')),
 disabled BOOLEAN NOT NULL DEFAULT false,
 password_hash BYTEA NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS qraft_sessions (
 id UUID PRIMARY KEY,
 token_hash CHAR(64) NOT NULL UNIQUE,
 user_id UUID NOT NULL REFERENCES qraft_users(id),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 expires_at TIMESTAMPTZ NOT NULL,
 revoked_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS qraft_sessions_user ON qraft_sessions(user_id);
CREATE TABLE IF NOT EXISTS qraft_account_invitations (
 id UUID PRIMARY KEY,
 token_hash CHAR(64) NOT NULL UNIQUE,
 email TEXT NOT NULL CHECK (email = lower(email)),
 kind TEXT NOT NULL CHECK (kind IN ('register','reset')),
 created_by UUID NOT NULL REFERENCES qraft_users(id),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 expires_at TIMESTAMPTZ NOT NULL,
 used_at TIMESTAMPTZ,
 revoked_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS qraft_account_invitations_email ON qraft_account_invitations(email,kind);
CREATE TABLE IF NOT EXISTS qraft_identity_audit (
 id BIGSERIAL PRIMARY KEY,
 actor_id UUID REFERENCES qraft_users(id),
 action TEXT NOT NULL,
 subject_id UUID,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
