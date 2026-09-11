-- Store problem embeddings in the table used by VectorRepository.
-- Older migrations added problems.embedding directly; keep that column for
-- compatibility and backfill any existing vectors into this table.

CREATE TABLE IF NOT EXISTS problem_embeddings (
    problem_id UUID PRIMARY KEY REFERENCES problems(id) ON DELETE CASCADE,
    embedding  vector(1536) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

DROP TRIGGER IF EXISTS trg_problem_embeddings_updated_at ON problem_embeddings;

CREATE TRIGGER trg_problem_embeddings_updated_at
    BEFORE UPDATE ON problem_embeddings
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();

INSERT INTO problem_embeddings (problem_id, embedding, created_at, updated_at)
SELECT id, embedding, created_at, updated_at
FROM problems
WHERE embedding IS NOT NULL
ON CONFLICT (problem_id)
DO UPDATE SET
    embedding = EXCLUDED.embedding,
    updated_at = EXCLUDED.updated_at;

CREATE INDEX IF NOT EXISTS idx_problem_embeddings_embedding
    ON problem_embeddings
    USING ivfflat (embedding vector_cosine_ops)
    WITH (lists = 100);
