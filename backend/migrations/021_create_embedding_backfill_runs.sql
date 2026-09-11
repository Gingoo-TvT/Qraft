-- QE::T11 foundation: resumable embedding shadow backfill ledger.

CREATE TABLE IF NOT EXISTS embedding_backfill_runs (
    run_key               TEXT PRIMARY KEY CHECK (length(trim(run_key)) > 0),
    from_model_version_id  UUID REFERENCES embedding_model_versions(id) ON DELETE RESTRICT,
    to_model_version_id    UUID NOT NULL REFERENCES embedding_model_versions(id) ON DELETE RESTRICT,
    embedding_kind         TEXT NOT NULL CHECK (embedding_kind IN ('statement', 'solution')),
    all_stale              BOOLEAN NOT NULL DEFAULT FALSE,
    dry_run                BOOLEAN NOT NULL DEFAULT FALSE,
    status                 TEXT NOT NULL CHECK (status IN ('planned', 'running', 'completed', 'failed')),
    limit_rows             INT CHECK (limit_rows IS NULL OR limit_rows >= 0),
    request_sha256         TEXT NOT NULL CHECK (request_sha256 ~ '^[0-9a-f]{64}$'),
    last_problem_id        UUID,
    scanned_count          BIGINT NOT NULL DEFAULT 0 CHECK (scanned_count >= 0),
    embedded_count         BIGINT NOT NULL DEFAULT 0 CHECK (embedded_count >= 0),
    skipped_count          BIGINT NOT NULL DEFAULT 0 CHECK (skipped_count >= 0),
    failed_count           BIGINT NOT NULL DEFAULT 0 CHECK (failed_count >= 0),
    error_message          TEXT,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at           TIMESTAMPTZ
);

DROP TRIGGER IF EXISTS trg_embedding_backfill_runs_updated_at ON embedding_backfill_runs;

CREATE TRIGGER trg_embedding_backfill_runs_updated_at
    BEFORE UPDATE ON embedding_backfill_runs
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();

CREATE TABLE IF NOT EXISTS embedding_backfill_failures (
    run_key         TEXT NOT NULL REFERENCES embedding_backfill_runs(run_key) ON DELETE CASCADE,
    problem_id      UUID NOT NULL REFERENCES problems(id) ON DELETE CASCADE,
    content_hash    TEXT NOT NULL CHECK (content_hash ~ '^[0-9a-f]{64}$'),
    embedding_kind  TEXT NOT NULL CHECK (embedding_kind IN ('statement', 'solution')),
    error_message   TEXT NOT NULL CHECK (length(trim(error_message)) > 0),
    attempts        INT NOT NULL DEFAULT 1 CHECK (attempts > 0),
    first_failed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_failed_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (run_key, problem_id, content_hash, embedding_kind)
);

CREATE INDEX IF NOT EXISTS idx_embedding_backfill_runs_target
    ON embedding_backfill_runs (to_model_version_id, embedding_kind, status);

CREATE INDEX IF NOT EXISTS idx_embedding_backfill_failures_problem
    ON embedding_backfill_failures (problem_id, embedding_kind);
