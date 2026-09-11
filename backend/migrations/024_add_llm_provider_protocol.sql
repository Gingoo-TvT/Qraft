-- Persist the wire protocol separately from the auditable provider identity.
-- Existing rows use auto inference so current Gemini/GPT/Claude settings keep
-- working without operator intervention.

ALTER TABLE llm_provider_settings
    ADD COLUMN protocol VARCHAR(32) NOT NULL DEFAULT 'auto';

ALTER TABLE llm_provider_settings
    ADD CONSTRAINT llm_provider_settings_protocol_check
    CHECK (protocol IN (
        'auto',
        'anthropic-messages',
        'gemini-native',
        'openai-responses',
        'openai-chat'
    ));

COMMENT ON COLUMN llm_provider_settings.protocol IS
    'Wire protocol; auto infers from model/provider while provider remains the provenance identity';
