-- QE::T02 read-path convergence: keep the legacy SQL function name but route
-- it through the active statement embedding pointer and versioned rows.

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
    WITH active_statement AS (
        SELECT model_version_id
        FROM embedding_active_pointers
        WHERE embedding_kind = 'statement'
    )
    SELECT
        p.id AS problem_id,
        p.title,
        p.level,
        p.difficulty,
        p.tags,
        (1 - (pe.embedding <=> query_embedding))::FLOAT AS similarity
    FROM active_statement active
    INNER JOIN problem_embeddings pe
        ON pe.model_version_id = active.model_version_id
       AND pe.embedding_kind = 'statement'
    INNER JOIN problems p ON p.id = pe.problem_id
    WHERE (exclude_id IS NULL OR p.id != exclude_id)
      AND p.status <> 'quarantined'
      AND COALESCE(p.metadata_json ->> 'stale', 'false') <> 'true'
      AND COALESCE(pe.metadata_json ->> 'stale', 'false') <> 'true'
      AND pe.content_hash = embedding_statement_content_hash(p.title, p.statement, p.one_line_hint)
      AND NOT EXISTS (
          SELECT 1
          FROM problem_quarantine_records quarantine
          WHERE quarantine.problem_id = p.id
      )
      AND 1 - (pe.embedding <=> query_embedding) >= similarity_threshold
    ORDER BY pe.embedding <=> query_embedding
    LIMIT max_results;
END;
$$ LANGUAGE plpgsql;
