-- QE::T02 foundation: versioned embedding registry, active pointer, and
-- version/kind/content-addressed problem embedding rows.

CREATE TABLE IF NOT EXISTS embedding_model_versions (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    model_id             TEXT NOT NULL CHECK (length(trim(model_id)) > 0),
    provider             TEXT NOT NULL CHECK (length(trim(provider)) > 0),
    revision             TEXT NOT NULL CHECK (length(trim(revision)) > 0),
    weights_hash         TEXT NOT NULL CHECK (weights_hash ~ '^[0-9a-f]{64}$'),
    dimensions           INT NOT NULL CHECK (dimensions > 0),
    normalization        TEXT NOT NULL CHECK (length(trim(normalization)) > 0),
    quantization         TEXT NOT NULL CHECK (length(trim(quantization)) > 0),
    instruction_template TEXT NOT NULL DEFAULT '',
    index_params         JSONB NOT NULL DEFAULT '{}'::jsonb,
    status               TEXT NOT NULL CHECK (status IN ('shadow', 'active', 'retired')),
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (provider, model_id, revision, weights_hash, dimensions, normalization, quantization)
);

DROP TRIGGER IF EXISTS trg_embedding_model_versions_updated_at ON embedding_model_versions;

CREATE TRIGGER trg_embedding_model_versions_updated_at
    BEFORE UPDATE ON embedding_model_versions
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();

CREATE TABLE IF NOT EXISTS embedding_active_pointers (
    embedding_kind    TEXT PRIMARY KEY CHECK (embedding_kind IN ('statement', 'solution')),
    model_version_id  UUID NOT NULL REFERENCES embedding_model_versions(id) ON DELETE RESTRICT,
    expected_dimensions INT NOT NULL CHECK (expected_dimensions > 0),
    updated_by        TEXT NOT NULL DEFAULT 'system',
    reason            TEXT NOT NULL DEFAULT '',
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE OR REPLACE FUNCTION embedding_statement_content_hash(
    title TEXT,
    statement TEXT,
    one_line_hint TEXT
)
RETURNS TEXT AS $$
    SELECT encode(digest(convert_to(
        COALESCE(title, '') || E'\n\n' || COALESCE(statement, '') || E'\n\n' || COALESCE(one_line_hint, ''),
        'UTF8'
    ), 'sha256'), 'hex');
$$ LANGUAGE sql IMMUTABLE;

WITH legacy_model AS (
    INSERT INTO embedding_model_versions (
        id,
        model_id,
        provider,
        revision,
        weights_hash,
        dimensions,
        normalization,
        quantization,
        instruction_template,
        index_params,
        status
    )
    SELECT
        uuid_generate_v5(uuid_ns_url(), 'algoforge:embedding-model-version:legacy-openai-compatible-1536'),
        'legacy-openai-compatible-1536',
        'openai-compatible',
        'pre-t02-legacy',
        '20b8b24a0f7d07f2e63bfb99022b90751e13e811b2f6680e97d07bafdcae876e',
        1536,
        'provider-default',
        'float32',
        'title\\n\\nstatement\\n\\none_line_hint',
        jsonb_build_object('index', 'ivfflat', 'lists', 100, 'distance', 'cosine'),
        'active'
    WHERE EXISTS (SELECT 1 FROM problem_embeddings)
    ON CONFLICT (provider, model_id, revision, weights_hash, dimensions, normalization, quantization)
    DO UPDATE SET
        status = 'active',
        updated_at = NOW()
    RETURNING id, dimensions
)
INSERT INTO embedding_active_pointers (
    embedding_kind,
    model_version_id,
    expected_dimensions,
    updated_by,
    reason
)
SELECT 'statement', id, dimensions, 'migration-019', 'seed legacy statement active pointer'
FROM legacy_model
ON CONFLICT (embedding_kind)
DO UPDATE SET
    model_version_id = EXCLUDED.model_version_id,
    expected_dimensions = EXCLUDED.expected_dimensions,
    updated_by = EXCLUDED.updated_by,
    reason = EXCLUDED.reason,
    updated_at = NOW();

WITH legacy_model AS (
    SELECT id, dimensions
    FROM embedding_model_versions
    WHERE provider = 'openai-compatible'
      AND model_id = 'legacy-openai-compatible-1536'
      AND revision = 'pre-t02-legacy'
      AND weights_hash = '20b8b24a0f7d07f2e63bfb99022b90751e13e811b2f6680e97d07bafdcae876e'
)
INSERT INTO embedding_active_pointers (
    embedding_kind,
    model_version_id,
    expected_dimensions,
    updated_by,
    reason
)
SELECT 'solution', id, dimensions, 'migration-019', 'seed legacy solution active pointer'
FROM legacy_model
ON CONFLICT (embedding_kind)
DO UPDATE SET
    model_version_id = EXCLUDED.model_version_id,
    expected_dimensions = EXCLUDED.expected_dimensions,
    updated_by = EXCLUDED.updated_by,
    reason = EXCLUDED.reason,
    updated_at = NOW();

ALTER TABLE problem_embeddings
    ADD COLUMN IF NOT EXISTS model_version_id UUID,
    ADD COLUMN IF NOT EXISTS embedding_kind TEXT,
    ADD COLUMN IF NOT EXISTS content_hash TEXT,
    ADD COLUMN IF NOT EXISTS metadata_json JSONB NOT NULL DEFAULT '{}'::jsonb;

UPDATE problem_embeddings pe
SET
    model_version_id = legacy.id,
    embedding_kind = 'statement',
    content_hash = embedding_statement_content_hash(p.title, p.statement, p.one_line_hint)
FROM problems p
CROSS JOIN (
    SELECT id
    FROM embedding_model_versions
    WHERE provider = 'openai-compatible'
      AND model_id = 'legacy-openai-compatible-1536'
      AND revision = 'pre-t02-legacy'
      AND weights_hash = '20b8b24a0f7d07f2e63bfb99022b90751e13e811b2f6680e97d07bafdcae876e'
) legacy
WHERE pe.problem_id = p.id
  AND (pe.model_version_id IS NULL OR pe.embedding_kind IS NULL OR pe.content_hash IS NULL);

ALTER TABLE problem_embeddings
    ALTER COLUMN model_version_id SET NOT NULL,
    ALTER COLUMN embedding_kind SET NOT NULL,
    ALTER COLUMN content_hash SET NOT NULL,
    ADD CONSTRAINT problem_embeddings_kind_check CHECK (embedding_kind IN ('statement', 'solution')),
    ADD CONSTRAINT problem_embeddings_content_hash_check CHECK (content_hash ~ '^[0-9a-f]{64}$');

ALTER TABLE problem_embeddings
    DROP CONSTRAINT IF EXISTS problem_embeddings_pkey;

ALTER TABLE problem_embeddings
    ADD CONSTRAINT problem_embeddings_identity_key
    UNIQUE (problem_id, model_version_id, embedding_kind, content_hash);

ALTER TABLE problem_embeddings
    ADD CONSTRAINT problem_embeddings_model_version_fk
    FOREIGN KEY (model_version_id) REFERENCES embedding_model_versions(id) ON DELETE RESTRICT;

CREATE OR REPLACE FUNCTION validate_problem_embedding_version()
RETURNS TRIGGER AS $$
DECLARE
    model_dimensions INT;
BEGIN
    SELECT dimensions
    INTO model_dimensions
    FROM embedding_model_versions
    WHERE id = NEW.model_version_id;

    IF model_dimensions IS NULL THEN
        RAISE EXCEPTION 'embedding model version % does not exist', NEW.model_version_id;
    END IF;

    IF vector_dims(NEW.embedding) <> model_dimensions THEN
        RAISE EXCEPTION 'embedding dimension mismatch for model %, got %, want %',
            NEW.model_version_id, vector_dims(NEW.embedding), model_dimensions;
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_validate_problem_embedding_version ON problem_embeddings;

CREATE TRIGGER trg_validate_problem_embedding_version
    BEFORE INSERT OR UPDATE OF model_version_id, embedding
    ON problem_embeddings
    FOR EACH ROW
    EXECUTE FUNCTION validate_problem_embedding_version();

CREATE OR REPLACE FUNCTION validate_embedding_active_pointer()
RETURNS TRIGGER AS $$
DECLARE
    model_status TEXT;
    model_dimensions INT;
BEGIN
    SELECT status, dimensions
    INTO model_status, model_dimensions
    FROM embedding_model_versions
    WHERE id = NEW.model_version_id;

    IF model_status IS NULL THEN
        RAISE EXCEPTION 'active embedding model version % does not exist', NEW.model_version_id;
    END IF;

    IF model_status <> 'active' THEN
        RAISE EXCEPTION 'active pointer requires an active model version, got status %', model_status;
    END IF;

    IF NEW.expected_dimensions <> model_dimensions THEN
        RAISE EXCEPTION 'active pointer dimension mismatch for %, got %, want %',
            NEW.embedding_kind, NEW.expected_dimensions, model_dimensions;
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_validate_embedding_active_pointer ON embedding_active_pointers;

CREATE TRIGGER trg_validate_embedding_active_pointer
    BEFORE INSERT OR UPDATE OF model_version_id, expected_dimensions
    ON embedding_active_pointers
    FOR EACH ROW
    EXECUTE FUNCTION validate_embedding_active_pointer();

DROP INDEX IF EXISTS idx_problem_embeddings_embedding;

CREATE INDEX IF NOT EXISTS idx_problem_embeddings_lookup
    ON problem_embeddings (model_version_id, embedding_kind, problem_id, content_hash);

CREATE INDEX IF NOT EXISTS idx_problem_embeddings_content_hash
    ON problem_embeddings (content_hash);

CREATE INDEX IF NOT EXISTS idx_problem_embeddings_statement_embedding
    ON problem_embeddings
    USING ivfflat (embedding vector_cosine_ops)
    WITH (lists = 100)
    WHERE embedding_kind = 'statement';

CREATE INDEX IF NOT EXISTS idx_problem_embeddings_solution_embedding
    ON problem_embeddings
    USING ivfflat (embedding vector_cosine_ops)
    WITH (lists = 100)
    WHERE embedding_kind = 'solution';
