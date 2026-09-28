-- Human release decisions supplement, rather than rewrite, automatic evidence.
CREATE TABLE problem_manual_release_approvals (
    approval_id UUID PRIMARY KEY,
    problem_id UUID NOT NULL, -- logical reference: retain audit history after deletion
    subject_sha256 TEXT NOT NULL CHECK (subject_sha256 ~ '^[0-9a-f]{64}$'),
    approved_by TEXT NOT NULL CHECK (btrim(approved_by) <> ''),
    approved_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    note TEXT NOT NULL DEFAULT '',
    overridden_checks JSONB NOT NULL CHECK (jsonb_typeof(overridden_checks) = 'array'),
    UNIQUE (problem_id, subject_sha256)
);
CREATE TRIGGER trg_prevent_manual_release_approval_mutation
    BEFORE UPDATE OR DELETE ON problem_manual_release_approvals
    FOR EACH ROW EXECUTE FUNCTION prevent_provenance_history_delete();

-- Bind approval to actual content, artifacts and the exact review history.
-- Publication audit timestamps do not change what was reviewed.
CREATE FUNCTION problem_manual_release_subject(p problems)
RETURNS TEXT LANGUAGE SQL STABLE AS $$
    SELECT encode(digest(jsonb_build_object(
        'problem', jsonb_build_array(p.id, p.created_at, p.title, p.statement, p.level,
            p.one_line_hint, p.detailed_solution, p.tags, p.time_limit,
            p.memory_limit, p.source, p.metadata_json->>'stale'),
        'solutions', COALESCE((SELECT jsonb_agg(jsonb_build_array(
            s.id, s.solution_type, s.language, s.source_code, s.compile_status,
            s.metadata_json) ORDER BY s.id) FROM solutions s WHERE s.problem_id=p.id), '[]'::jsonb),
        'tests', COALESCE((SELECT jsonb_agg(jsonb_build_array(
            t.id, t.test_index, t.group_id, t.is_sample, t.input_path,
            t.output_path, t.score, t.metadata_json) ORDER BY t.id)
            FROM testcases t WHERE t.problem_id=p.id), '[]'::jsonb),
        'reviews', COALESCE((SELECT jsonb_agg(q.quarantine_id ORDER BY q.quarantine_id)
            FROM problem_quarantine_records q WHERE q.problem_id=p.id), '[]'::jsonb),
        'definition', (SELECT b.artifact_id FROM provenance_artifact_bindings b
            WHERE b.subject_type='problem' AND b.subject_id=p.id::text
              AND b.artifact_role='definition' AND b.is_current)
    )::text, 'sha256'), 'hex');
$$;

CREATE FUNCTION problem_manual_release_approved(p problems)
RETURNS BOOLEAN LANGUAGE SQL STABLE AS $$
    SELECT EXISTS (SELECT 1 FROM problem_manual_release_approvals a
        WHERE a.problem_id=p.id AND a.subject_sha256=problem_manual_release_subject(p));
$$;
CREATE FUNCTION problem_manual_release_approved(problem UUID)
RETURNS BOOLEAN LANGUAGE SQL STABLE AS $$
    SELECT COALESCE((SELECT problem_manual_release_approved(p)
        FROM problems p WHERE p.id=problem), FALSE);
$$;

CREATE OR REPLACE FUNCTION prevent_problem_review_quarantine_release()
RETURNS TRIGGER AS $$
BEGIN
    IF NEW.status IS DISTINCT FROM 'quarantined'
       AND EXISTS (SELECT 1 FROM problem_quarantine_records WHERE problem_id=NEW.id)
       AND NOT problem_manual_release_approved(NEW) THEN
        RAISE EXCEPTION USING ERRCODE='23514',
            CONSTRAINT='problem_review_quarantine_status_immutable',
            MESSAGE='problem with automated-review quarantine requires a current human approval';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- Editing a manually released problem returns it to review. An old human
-- decision cannot silently approve changed content or newly added evidence.
CREATE FUNCTION invalidate_problem_manual_release_content()
RETURNS TRIGGER AS $$
BEGIN
    IF OLD.status='published' AND NEW.status IN ('published','draft')
       AND EXISTS (SELECT 1 FROM problem_manual_release_approvals WHERE problem_id=NEW.id)
       AND NOT problem_manual_release_approved(NEW) THEN
        NEW.status='quarantined';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER trg_invalidate_problem_manual_release_content
    BEFORE UPDATE ON problems FOR EACH ROW
    EXECUTE FUNCTION invalidate_problem_manual_release_content();

CREATE FUNCTION invalidate_problem_manual_release_artifacts()
RETURNS TRIGGER AS $$
DECLARE problem UUID;
BEGIN
    problem := CASE WHEN TG_OP='DELETE' THEN OLD.problem_id ELSE NEW.problem_id END;
    UPDATE problems p SET status='quarantined', updated_at=NOW()
    WHERE p.id=problem AND p.status='published'
      AND EXISTS (SELECT 1 FROM problem_manual_release_approvals a WHERE a.problem_id=p.id)
      AND NOT problem_manual_release_approved(p);
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER trg_invalidate_manual_release_solution
    AFTER INSERT OR UPDATE OR DELETE ON solutions FOR EACH ROW
    EXECUTE FUNCTION invalidate_problem_manual_release_artifacts();
CREATE TRIGGER trg_invalidate_manual_release_testcase
    AFTER INSERT OR UPDATE OR DELETE ON testcases FOR EACH ROW
    EXECUTE FUNCTION invalidate_problem_manual_release_artifacts();

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
            AND NOT problem_manual_release_approved(p.id)
      )
      AND 1 - (pe.embedding <=> query_embedding) >= similarity_threshold
    ORDER BY pe.embedding <=> query_embedding
    LIMIT max_results;
END;
$$ LANGUAGE plpgsql;

-- Publication bookkeeping is not a content edit. Otherwise writing the
-- approval itself would create a new unknown-rights definition immediately.
CREATE OR REPLACE FUNCTION update_problem_provenance()
RETURNS TRIGGER AS $$
DECLARE old_content problems; new_content problems;
BEGIN
    old_content := OLD;
    new_content := NEW;
    old_content.metadata_json := COALESCE(OLD.metadata_json,'{}'::jsonb)
        - ARRAY['publication_gate_version','publication_policy_version',
          'publication_gate_status','publication_quarantine_reason','manual_release_approval'];
    new_content.metadata_json := COALESCE(NEW.metadata_json,'{}'::jsonb)
        - ARRAY['publication_gate_version','publication_policy_version',
          'publication_gate_status','publication_quarantine_reason','manual_release_approval'];
    IF provenance_problem_content_hash(new_content) IS DISTINCT FROM provenance_problem_content_hash(old_content) THEN
        PERFORM quarantine_bound_artifact('problem',NEW.id::text,'definition',
            provenance_problem_content_hash(NEW),'algoforge://problems/' || NEW.id::text,
            'problem statement changed',jsonb_build_object('status',NEW.status));
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
