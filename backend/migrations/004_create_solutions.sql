-- Solutions table
CREATE TABLE solutions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    problem_id      UUID NOT NULL REFERENCES problems(id) ON DELETE CASCADE,
    solution_type   VARCHAR(20) NOT NULL,       -- main/brute/generator/checker
    language        VARCHAR(20) NOT NULL,
    source_code     TEXT NOT NULL,
    compile_status  VARCHAR(20) DEFAULT 'pending',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Index on problem_id for fast lookup
CREATE INDEX idx_solutions_problem_id ON solutions(problem_id);
