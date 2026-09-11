CREATE TABLE problem_quarantine_records (
    quarantine_id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    problem_id             UUID NOT NULL REFERENCES problems(id) ON DELETE RESTRICT,
    operation_key          VARCHAR(512) NOT NULL UNIQUE,
    reason                 TEXT NOT NULL CHECK (btrim(reason) <> ''),
    workflow_id            VARCHAR(512) NOT NULL CHECK (btrim(workflow_id) <> ''),
    workflow_run_id        VARCHAR(512) NOT NULL CHECK (btrim(workflow_run_id) <> ''),
    review_gate_change_id  VARCHAR(200) NOT NULL CHECK (btrim(review_gate_change_id) <> ''),
    review_gate_version    INTEGER NOT NULL CHECK (review_gate_version >= 2),
    review_result_sha256   CHAR(64) NOT NULL
                           CHECK (review_result_sha256 ~ '^[0-9a-f]{64}$'),
    review_result          JSONB NOT NULL
                           CHECK (jsonb_typeof(review_result) = 'object')
                           CHECK (review_result ?& ARRAY[
                               'approved', 'issues', 'suggestions', 'confidence',
                               'estimated_difficulty', 'is_duplicate',
                               'duplicate_of', 'duplicate_reason',
                               'source_artifacts', 'full_text'
                           ])
                           CHECK (jsonb_typeof(review_result->'source_artifacts') = 'array')
                           CHECK (jsonb_array_length(review_result->'source_artifacts') > 0),
    review_text            TEXT NOT NULL CHECK (btrim(review_text) <> ''),
    source_ancestry        JSONB NOT NULL DEFAULT '[]'::jsonb
                           CHECK (jsonb_typeof(source_ancestry) = 'array')
                           CHECK (jsonb_array_length(source_ancestry) > 0),
    created_at             TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_problem_quarantine_records_problem_created
    ON problem_quarantine_records (problem_id, created_at DESC);

CREATE OR REPLACE FUNCTION force_problem_review_quarantine_status()
RETURNS TRIGGER AS $$
BEGIN
    UPDATE problems
    SET status = 'quarantined', updated_at = NOW()
    WHERE id = NEW.problem_id;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_force_problem_review_quarantine_status
    AFTER INSERT ON problem_quarantine_records
    FOR EACH ROW EXECUTE FUNCTION force_problem_review_quarantine_status();

CREATE OR REPLACE FUNCTION prevent_problem_review_quarantine_release()
RETURNS TRIGGER AS $$
BEGIN
    IF NEW.status IS DISTINCT FROM 'quarantined'
       AND EXISTS (
           SELECT 1
           FROM problem_quarantine_records
           WHERE problem_id = NEW.id
       ) THEN
        RAISE EXCEPTION USING
            ERRCODE = '23514',
            CONSTRAINT = 'problem_review_quarantine_status_immutable',
            MESSAGE = 'problem with automated-review quarantine evidence cannot leave quarantine';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_prevent_problem_review_quarantine_release
    BEFORE UPDATE OF status ON problems
    FOR EACH ROW EXECUTE FUNCTION prevent_problem_review_quarantine_release();

CREATE TRIGGER trg_prevent_problem_quarantine_record_mutation
    BEFORE UPDATE OR DELETE ON problem_quarantine_records
    FOR EACH ROW EXECUTE FUNCTION prevent_provenance_history_delete();
