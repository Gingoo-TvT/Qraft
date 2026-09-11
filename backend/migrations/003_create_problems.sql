-- Problems table
CREATE TABLE problems (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    serial_number     SERIAL,
    title             VARCHAR(200) NOT NULL,
    statement         TEXT NOT NULL,                -- Markdown/LaTeX 题面
    level             problem_level NOT NULL,       -- 语法/算法层级
    difficulty        INT NOT NULL CHECK (difficulty >= 800 AND difficulty <= 3500 AND difficulty % 100 = 0),
    tags              TEXT[] NOT NULL,
    one_line_hint     TEXT,                         -- 一句话题解
    detailed_solution TEXT,                         -- 详细题解（MD，可选）
    time_limit        INT NOT NULL DEFAULT 2000,   -- ms
    memory_limit      INT NOT NULL DEFAULT 256,    -- MB
    source            VARCHAR(200),
    status            VARCHAR(20) NOT NULL DEFAULT 'draft',  -- draft/generating/review/published/rejected
    workflow_id       VARCHAR(200),
    metadata_json     JSONB,                        -- {编号, 名称, 层级, tags} JSON
    embedding         vector(1536),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Indexes
CREATE INDEX idx_problems_level ON problems(level);
CREATE INDEX idx_problems_difficulty ON problems(difficulty);
CREATE INDEX idx_problems_status ON problems(status);
CREATE INDEX idx_problems_tags ON problems USING GIN(tags);

-- Auto-update updated_at trigger
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_problems_updated_at
    BEFORE UPDATE ON problems
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();
