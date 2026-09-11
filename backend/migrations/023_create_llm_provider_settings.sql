-- Durable, operator-managed defaults for problem generation providers.
-- API keys are encrypted by the API process before they reach this table.

CREATE TABLE llm_provider_settings (
    purpose             VARCHAR(32) PRIMARY KEY
                        CHECK (purpose IN ('statement', 'verification')),
    model               VARCHAR(512) NOT NULL,
    base_url            VARCHAR(512) NOT NULL DEFAULT '',
    provider            VARCHAR(512) NOT NULL,
    api_key_source      VARCHAR(24) NOT NULL DEFAULT 'environment'
                        CHECK (api_key_source IN ('environment', 'stored')),
    encrypted_api_key   TEXT,
    updated_by          VARCHAR(255) NOT NULL DEFAULT 'api',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (
        (api_key_source = 'environment' AND encrypted_api_key IS NULL)
        OR
        (api_key_source = 'stored' AND encrypted_api_key IS NOT NULL AND length(encrypted_api_key) > 0)
    )
);

COMMENT ON TABLE llm_provider_settings IS
    'Encrypted persistent defaults for statement generation and verification providers';
