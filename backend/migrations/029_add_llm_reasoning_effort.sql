-- Persist the operator-selected reasoning effort independently for each LLM
-- role. An empty value means that AlgoForge omits the provider field entirely;
-- it is deliberately not a hidden application default.

ALTER TABLE llm_provider_settings
    ADD COLUMN reasoning_effort VARCHAR(32) NOT NULL DEFAULT '';

ALTER TABLE llm_provider_settings
    ADD CONSTRAINT llm_provider_settings_reasoning_effort_check
    CHECK (reasoning_effort IN (
        '',
        'none',
        'minimal',
        'low',
        'medium',
        'high',
        'xhigh',
        'max'
    ));

COMMENT ON COLUMN llm_provider_settings.reasoning_effort IS
    'Explicit provider reasoning effort; empty means no reasoning field is sent';
