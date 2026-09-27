-- Ownership is immutable across Temporal retries, resets and uncertain starts.
-- Historical workflows intentionally have no owner and remain administrator-only.
CREATE TABLE IF NOT EXISTS workflow_ownership (
 workflow_id TEXT PRIMARY KEY,
 owner_user_id TEXT NOT NULL CHECK (length(owner_user_id) > 0),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS workflow_ownership_user ON workflow_ownership(owner_user_id);
ALTER TABLE problem_sets ADD COLUMN IF NOT EXISTS owner_user_id TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS problem_sets_owner ON problem_sets(owner_user_id);
CREATE TABLE IF NOT EXISTS quiz_workflow_ownership (
 quiz_id UUID PRIMARY KEY REFERENCES quiz_problems(id) ON DELETE CASCADE,
 workflow_id TEXT NOT NULL
);
