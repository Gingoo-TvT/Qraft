-- The standard generation receipt is produced only after the publication gate.
-- Keep its immutable binding outside problems.metadata_json because metadata is
-- part of the governed problem content identity.

CREATE TABLE problem_generation_standard_evidence (
    problem_id          UUID PRIMARY KEY REFERENCES problems(id) ON DELETE CASCADE,
    schema_version      TEXT NOT NULL
                        CHECK (schema_version = 'algoforge.generation-standard-evidence.v1'),
    sha256              CHAR(64) NOT NULL
                        CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    object_path         TEXT NOT NULL,
    final_status        VARCHAR(32) NOT NULL
                        CHECK (final_status IN ('published', 'quarantined')),
    quarantine_reason   TEXT NOT NULL DEFAULT '',
    outcome_category    VARCHAR(64) NOT NULL
                        CHECK (outcome_category IN ('review', 'publication_eligibility')),
    outcome_kind        VARCHAR(64) NOT NULL
                        CHECK (outcome_kind IN ('review_result', 'publication_decision')),
    outcome_sha256      CHAR(64) NOT NULL
                        CHECK (outcome_sha256 ~ '^[0-9a-f]{64}$'),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (
        (final_status = 'published' AND quarantine_reason = '') OR
        (final_status = 'quarantined' AND BTRIM(quarantine_reason) <> '')
    ),
    CHECK (
        (outcome_category = 'review' AND final_status = 'quarantined' AND outcome_kind = 'review_result') OR
        (outcome_category = 'publication_eligibility' AND outcome_kind = 'publication_decision')
    )
);

COMMENT ON TABLE problem_generation_standard_evidence IS
    'Immutable post-publication-gate binding for generation standard evidence receipts';
