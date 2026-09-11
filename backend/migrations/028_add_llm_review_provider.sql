-- Add the independent review (R) provider role without changing existing
-- statement (G) or verification/oracle (V) rows.

ALTER TABLE llm_provider_settings
    DROP CONSTRAINT IF EXISTS llm_provider_settings_purpose_check;

ALTER TABLE llm_provider_settings
    ADD CONSTRAINT llm_provider_settings_purpose_check
    CHECK (purpose IN ('statement', 'verification', 'review'));

COMMENT ON TABLE llm_provider_settings IS
    'Encrypted persistent defaults for generation, verification/oracle, and review providers';
