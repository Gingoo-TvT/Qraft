-- Testcases table
CREATE TABLE testcases (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    problem_id   UUID NOT NULL REFERENCES problems(id) ON DELETE CASCADE,
    test_index   INT NOT NULL,
    group_id     INT DEFAULT 0,
    is_sample    BOOLEAN DEFAULT FALSE,
    input_path   TEXT NOT NULL,              -- MinIO path
    output_path  TEXT NOT NULL,              -- MinIO path
    score        INT DEFAULT 0,
    description  TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Unique index: each problem has unique test indices
CREATE UNIQUE INDEX idx_testcases_problem_test ON testcases(problem_id, test_index);
-- Index on problem_id for fast lookup
CREATE INDEX idx_testcases_problem_id ON testcases(problem_id);
