-- Create ivfflat index on problems(embedding) for cosine similarity search
-- NOTE: This index requires at least some rows to exist before creation.
-- For initial setup with an empty table, consider creating this index after
-- inserting the first batch of problems, or use HNSW as an alternative.
-- We use lists = 100 as a reasonable default; tune based on dataset size.
CREATE INDEX idx_problems_embedding ON problems
    USING ivfflat (embedding vector_cosine_ops)
    WITH (lists = 100);

-- Function for finding similar problems by cosine similarity
CREATE OR REPLACE FUNCTION find_similar_problems(
    query_embedding vector(1536),
    similarity_threshold FLOAT DEFAULT 0.85,
    max_results INT DEFAULT 10,
    exclude_id UUID DEFAULT NULL
)
RETURNS TABLE (
    problem_id UUID,
    title VARCHAR(200),
    level problem_level,
    difficulty INT,
    tags TEXT[],
    similarity FLOAT
) AS $$
BEGIN
    RETURN QUERY
    SELECT
        p.id AS problem_id,
        p.title,
        p.level,
        p.difficulty,
        p.tags,
        1 - (p.embedding <=> query_embedding) AS similarity
    FROM problems p
    WHERE p.embedding IS NOT NULL
      AND (exclude_id IS NULL OR p.id != exclude_id)
      AND 1 - (p.embedding <=> query_embedding) >= similarity_threshold
    ORDER BY p.embedding <=> query_embedding
    LIMIT max_results;
END;
$$ LANGUAGE plpgsql;
