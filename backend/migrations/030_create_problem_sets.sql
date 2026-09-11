-- Contest problem sets, their ordered mixed items, and append-only diversity ledger.
CREATE EXTENSION IF NOT EXISTS "pgcrypto";

CREATE TABLE IF NOT EXISTS problem_sets (
    id                     UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    code                   VARCHAR(64) NOT NULL UNIQUE,
    title                  VARCHAR(255) NOT NULL,
    description            TEXT NOT NULL DEFAULT '',
    kind                   VARCHAR(32) NOT NULL DEFAULT 'contest'
                           CHECK (kind IN ('contest', 'homework', 'curriculum', 'mock_exam')),
    visibility             VARCHAR(16) NOT NULL DEFAULT 'private'
                           CHECK (visibility IN ('public', 'private')),
    subject                VARCHAR(128) NOT NULL DEFAULT '',
    tags                   TEXT[] NOT NULL DEFAULT '{}',
    style_prompt           TEXT NOT NULL DEFAULT '',
    difficulty_prompt      TEXT NOT NULL DEFAULT '',
    generated_prompt       TEXT NOT NULL DEFAULT '',
    generated_prompt_model VARCHAR(512) NOT NULL DEFAULT '',
    generated_prompt_at    TIMESTAMPTZ,
    desired_item_count     INT NOT NULL DEFAULT 0
                           CHECK (desired_item_count >= 0 AND desired_item_count <= 20),
    min_item_count         INT NOT NULL DEFAULT 10
                           CHECK (min_item_count >= 1 AND min_item_count <= 20),
    max_item_count         INT NOT NULL DEFAULT 20
                           CHECK (max_item_count >= 1 AND max_item_count <= 20),
    cooldown_sets          INT NOT NULL DEFAULT 2
                           CHECK (cooldown_sets >= 0 AND cooldown_sets <= 50),
    total_score            INT NOT NULL DEFAULT 0 CHECK (total_score >= 0),
    status                 VARCHAR(20) NOT NULL DEFAULT 'draft'
                           CHECK (status IN ('draft', 'ready', 'exported')),
    created_by             VARCHAR(255) NOT NULL DEFAULT 'api',
    created_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (min_item_count <= max_item_count),
    CHECK (desired_item_count = 0 OR
           (desired_item_count >= min_item_count AND desired_item_count <= max_item_count))
);

CREATE INDEX IF NOT EXISTS idx_problem_sets_status ON problem_sets(status);
CREATE INDEX IF NOT EXISTS idx_problem_sets_kind ON problem_sets(kind);
CREATE INDEX IF NOT EXISTS idx_problem_sets_created_at ON problem_sets(created_at DESC);

CREATE TABLE IF NOT EXISTS problem_set_items (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    set_id      UUID NOT NULL REFERENCES problem_sets(id) ON DELETE CASCADE,
    problem_id  UUID NULL REFERENCES problems(id) ON DELETE RESTRICT,
    quiz_id     UUID NULL REFERENCES quiz_problems(id) ON DELETE RESTRICT,
    position    INT NOT NULL CHECK (position > 0),
    score       INT NOT NULL DEFAULT 0 CHECK (score >= 0),
    section     VARCHAR(128) NOT NULL DEFAULT '',
    notes       TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK ((problem_id IS NOT NULL)::int + (quiz_id IS NOT NULL)::int = 1),
    UNIQUE (set_id, position),
    UNIQUE (set_id, problem_id),
    UNIQUE (set_id, quiz_id)
);

CREATE INDEX IF NOT EXISTS idx_problem_set_items_set_position
    ON problem_set_items(set_id, position);

CREATE TABLE IF NOT EXISTS problem_set_ledger_entries (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    set_id                UUID NOT NULL REFERENCES problem_sets(id) ON DELETE RESTRICT,
    revision              INT NOT NULL CHECK (revision > 0),
    event_type            VARCHAR(24) NOT NULL
                          CHECK (event_type IN ('validated', 'exported')),
    snapshot_sha256       CHAR(64) NOT NULL,
    knowledge_point_keys  TEXT[] NOT NULL DEFAULT '{}',
    item_fingerprints     TEXT[] NOT NULL DEFAULT '{}',
    overlap_report        JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (set_id, revision, event_type)
);

CREATE INDEX IF NOT EXISTS idx_problem_set_ledger_recent
    ON problem_set_ledger_entries(event_type, created_at DESC);

DROP TRIGGER IF EXISTS trg_problem_sets_updated_at ON problem_sets;
CREATE TRIGGER trg_problem_sets_updated_at
    BEFORE UPDATE ON problem_sets
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();

DROP TRIGGER IF EXISTS trg_problem_set_items_updated_at ON problem_set_items;
CREATE TRIGGER trg_problem_set_items_updated_at
    BEFORE UPDATE ON problem_set_items
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();

COMMENT ON TABLE problem_sets IS
    'Contest/homework sets with natural-language style and difficulty constraints';
COMMENT ON TABLE problem_set_ledger_entries IS
    'Append-only diversity ledger preventing repeated item/knowledge-point coverage across recent sets';
