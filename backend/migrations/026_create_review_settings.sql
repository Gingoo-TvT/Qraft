-- Local operator setting for test-time automatic public-release approval.
-- This does not alter any automated quality gate; the worker only consults it
-- after the candidate has passed every gate except provenance approval.

CREATE TABLE review_settings (
    setting_key                    VARCHAR(32) PRIMARY KEY
                                   CHECK (setting_key = 'global'),
    auto_approve_public_release    BOOLEAN NOT NULL DEFAULT FALSE,
    updated_by                     VARCHAR(255) NOT NULL DEFAULT 'migration',
    created_at                     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at                     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO review_settings (setting_key, auto_approve_public_release)
VALUES ('global', FALSE)
ON CONFLICT (setting_key) DO NOTHING;

COMMENT ON TABLE review_settings IS
    'Persistent operator controls for human-review workflow behavior';
