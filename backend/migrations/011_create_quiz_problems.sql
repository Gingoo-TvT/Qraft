CREATE EXTENSION IF NOT EXISTS "pgcrypto";

CREATE TABLE IF NOT EXISTS knowledge_points (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    subject     VARCHAR(64)  NOT NULL,
    code        VARCHAR(128) NOT NULL UNIQUE,
    name        VARCHAR(255) NOT NULL,
    parent_id   UUID NULL REFERENCES knowledge_points(id) ON DELETE SET NULL,
    sort_order  INT NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_knowledge_points_subject ON knowledge_points(subject);

CREATE TABLE IF NOT EXISTS quiz_problems (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    code         VARCHAR(64) NOT NULL UNIQUE,
    title        VARCHAR(255) NOT NULL DEFAULT '',
    statement    TEXT NOT NULL,
    type         VARCHAR(32) NOT NULL,
    code_id      INT NULL,
    code_hint    TEXT NULL,
    options      JSONB NULL,
    answers      TEXT[] NOT NULL DEFAULT '{}',
    difficulty   VARCHAR(16) NOT NULL,
    visibility   VARCHAR(16) NOT NULL,
    is_vip       BOOLEAN NOT NULL DEFAULT FALSE,
    tags         TEXT[] NOT NULL DEFAULT '{}',
    langs        INT[]  NOT NULL DEFAULT '{}',
    explanation  TEXT NULL,
    subject      VARCHAR(64) NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_quiz_problems_type       ON quiz_problems(type);
CREATE INDEX IF NOT EXISTS idx_quiz_problems_subject    ON quiz_problems(subject);
CREATE INDEX IF NOT EXISTS idx_quiz_problems_difficulty ON quiz_problems(difficulty);
CREATE INDEX IF NOT EXISTS idx_quiz_problems_tags       ON quiz_problems USING GIN(tags);

CREATE TABLE IF NOT EXISTS quiz_knowledge_points (
    quiz_id            UUID NOT NULL REFERENCES quiz_problems(id) ON DELETE CASCADE,
    knowledge_point_id UUID NOT NULL REFERENCES knowledge_points(id) ON DELETE CASCADE,
    PRIMARY KEY (quiz_id, knowledge_point_id)
);
CREATE INDEX IF NOT EXISTS idx_qkp_kp ON quiz_knowledge_points(knowledge_point_id);
