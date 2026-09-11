-- S2.6: problem edits invalidate release-critical dependents until the
-- regeneration/revalidation/republication gate refreshes them.

ALTER TABLE solutions
    ADD COLUMN IF NOT EXISTS metadata_json JSONB NOT NULL DEFAULT '{}'::jsonb;

ALTER TABLE testcases
    ADD COLUMN IF NOT EXISTS metadata_json JSONB NOT NULL DEFAULT '{}'::jsonb;

CREATE INDEX IF NOT EXISTS idx_solutions_problem_stale
    ON solutions (problem_id)
    WHERE COALESCE(metadata_json ->> 'stale', 'false') = 'true';

CREATE INDEX IF NOT EXISTS idx_testcases_problem_stale
    ON testcases (problem_id)
    WHERE COALESCE(metadata_json ->> 'stale', 'false') = 'true';

CREATE INDEX IF NOT EXISTS idx_problem_embeddings_problem_stale
    ON problem_embeddings (problem_id, embedding_kind, model_version_id)
    WHERE COALESCE(metadata_json ->> 'stale', 'false') = 'true';
