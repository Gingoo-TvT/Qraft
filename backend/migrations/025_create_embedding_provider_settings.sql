-- Durable encrypted credential for the embedding runtime selected in the Web
-- deployment flow. Vector-space lifecycle metadata remains in
-- embedding_model_versions and embedding_active_pointers.

CREATE TABLE embedding_provider_settings (
    setting_key        VARCHAR(32) PRIMARY KEY
                       CHECK (setting_key = 'runtime'),
    base_url           VARCHAR(512) NOT NULL,
    model              VARCHAR(512) NOT NULL,
    dimensions         INTEGER NOT NULL CHECK (dimensions = 1536),
    timeout_sec        INTEGER NOT NULL DEFAULT 30 CHECK (timeout_sec > 0),
    encrypted_api_key  TEXT NOT NULL CHECK (length(encrypted_api_key) > 0),
    updated_by         VARCHAR(255) NOT NULL DEFAULT 'embedding-web-admin',
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

COMMENT ON TABLE embedding_provider_settings IS
    'Encrypted persistent credential and endpoint identity for the active embedding runtime';
