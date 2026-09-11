-- Durable Temporal activity idempotency and transactional delivery foundation.

CREATE TABLE workflow_operations (
    operation_key   VARCHAR(512) PRIMARY KEY,
    operation_type  VARCHAR(100) NOT NULL,
    payload_sha256  CHAR(64) NOT NULL CHECK (payload_sha256 ~ '^[0-9a-f]{64}$'),
    status          VARCHAR(20) NOT NULL DEFAULT 'in_progress'
                    CHECK (status IN ('in_progress', 'failed', 'completed')),
    result_json     JSONB,
    attempt_count   INTEGER NOT NULL DEFAULT 1 CHECK (attempt_count > 0),
    last_error      TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at    TIMESTAMPTZ,
    CHECK ((status = 'completed') = (completed_at IS NOT NULL)),
    CHECK (status <> 'completed' OR result_json IS NOT NULL)
);

CREATE TRIGGER trg_workflow_operations_updated_at
    BEFORE UPDATE ON workflow_operations
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TABLE workflow_operation_steps (
    operation_key   VARCHAR(512) NOT NULL
                    REFERENCES workflow_operations(operation_key) ON DELETE CASCADE,
    step_key        VARCHAR(768) NOT NULL,
    effect_sha256   CHAR(64) NOT NULL CHECK (effect_sha256 ~ '^[0-9a-f]{64}$'),
    completed_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (operation_key, step_key)
);

CREATE TABLE workflow_outbox (
    event_id        UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    operation_key   VARCHAR(512) NOT NULL,
    event_type      VARCHAR(120) NOT NULL,
    aggregate_type  VARCHAR(80) NOT NULL,
    aggregate_id    VARCHAR(512) NOT NULL,
    payload_json    JSONB NOT NULL,
    status          VARCHAR(20) NOT NULL DEFAULT 'pending'
                    CHECK (status IN ('pending', 'processing', 'retry', 'delivered', 'dead')),
    attempt_count   INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    available_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    lock_token      UUID,
    locked_until    TIMESTAMPTZ,
    last_error      TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    delivered_at    TIMESTAMPTZ,
    UNIQUE (operation_key, event_type),
    CHECK ((status = 'processing') = (lock_token IS NOT NULL)),
    CHECK ((status = 'processing') = (locked_until IS NOT NULL)),
    CHECK (status <> 'delivered' OR delivered_at IS NOT NULL)
);

CREATE INDEX idx_workflow_outbox_ready
    ON workflow_outbox (available_at, created_at)
    WHERE status IN ('pending', 'retry');

CREATE INDEX idx_workflow_outbox_expired_lock
    ON workflow_outbox (locked_until)
    WHERE status = 'processing';
