-- Provider billing-effect lease/result cache and retention audit scheduling support.

CREATE TABLE provider_effects (
    effect_key       VARCHAR(160) PRIMARY KEY,
    effect_type      VARCHAR(80) NOT NULL,
    request_sha256   CHAR(64) NOT NULL CHECK (request_sha256 ~ '^[0-9a-f]{64}$'),
    status           VARCHAR(20) NOT NULL DEFAULT 'in_progress'
                     CHECK (status IN ('in_progress', 'completed')),
    lease_token      UUID,
    leased_until     TIMESTAMPTZ,
    result_json      JSONB,
    result_sha256    CHAR(64) CHECK (result_sha256 ~ '^[0-9a-f]{64}$'),
    attempt_count    INTEGER NOT NULL DEFAULT 1 CHECK (attempt_count > 0),
    last_error       TEXT,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at     TIMESTAMPTZ,
    CHECK ((status = 'in_progress') = (lease_token IS NOT NULL)),
    CHECK ((status = 'in_progress') = (leased_until IS NOT NULL)),
    CHECK ((status = 'completed') = (result_json IS NOT NULL)),
    CHECK ((status = 'completed') = (result_sha256 IS NOT NULL)),
    CHECK ((status = 'completed') = (completed_at IS NOT NULL)),
    CHECK (
        status <> 'completed'
        OR result_sha256 = encode(
            digest(convert_to(result_json::text, 'UTF8'), 'sha256'),
            'hex'
        )
    )
);

CREATE TRIGGER trg_provider_effects_updated_at
    BEFORE UPDATE ON provider_effects
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE INDEX idx_provider_effects_expired_lease
    ON provider_effects (leased_until, effect_key)
    WHERE status = 'in_progress';

CREATE INDEX idx_provenance_artifacts_retention_due
    ON provenance_artifacts (retention_expires_at, artifact_id)
    WHERE retention_expires_at IS NOT NULL
      AND takedown_status NOT IN ('removed', 'revoked', 'takedown_requested');
