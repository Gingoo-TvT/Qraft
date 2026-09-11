-- Level enum
CREATE TYPE problem_level AS ENUM ('syntax', 'algorithm');

-- Tag categories table - each tag belongs to exactly one level
CREATE TABLE tag_categories (
    id           SERIAL PRIMARY KEY,
    level        problem_level NOT NULL,
    tag_name     VARCHAR(50) NOT NULL UNIQUE,
    display_name VARCHAR(100) NOT NULL,
    description  TEXT,
    sort_order   INT DEFAULT 0,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Unique index ensuring tag names are globally unique across levels
CREATE UNIQUE INDEX idx_tag_name_unique ON tag_categories(tag_name);
-- Index for filtering by level
CREATE INDEX idx_tag_level ON tag_categories(level);
