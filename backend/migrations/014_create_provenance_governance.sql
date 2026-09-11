-- P00: provenance, license decisions, ancestry and fail-closed manifests.
-- Existing content is deliberately backfilled as quarantined/unknown. A later
-- reviewed import may create a new policy decision, but must not rewrite the
-- original audit history.

CREATE TABLE governance_policy_versions (
    policy_version      TEXT PRIMARY KEY,
    schema_version      TEXT NOT NULL,
    policy_definition   JSONB NOT NULL,
    policy_hash         TEXT NOT NULL CHECK (policy_hash ~ '^[0-9a-f]{64}$'),
    terms_index_hash    TEXT NOT NULL CHECK (terms_index_hash ~ '^[0-9a-f]{64}$'),
    status              TEXT NOT NULL CHECK (status IN ('draft', 'active', 'retired')),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    retired_at          TIMESTAMPTZ,
    CHECK ((status = 'retired') = (retired_at IS NOT NULL))
);

WITH policy AS (
    SELECT '{
      "schema_version":"1.0.0",
      "policy_version":"2026-07-13.v1",
      "default":{
        "internal_eval_allowed":false,
        "private_training_allowed":false,
        "public_release_allowed":false
      },
      "rules":[
        {"license_basis":"project_owned","internal_eval_allowed":true,"private_training_allowed":true,"public_release_allowed":true,"requires":["ownership_record","terms_snapshot"]},
        {"license_basis":"explicit_contributor_consent_v1","internal_eval_allowed":true,"private_training_allowed":true,"public_release_allowed":true,"requires":["signed_consent_record","terms_snapshot"]},
        {"license_basis":"reviewed_permissive_license","internal_eval_allowed":true,"private_training_allowed":false,"public_release_allowed":false,"requires":["legal_review_id","attribution_record","terms_snapshot"]},
        {"license_basis":"anthropic_generated_unreviewed","internal_eval_allowed":true,"private_training_allowed":false,"public_release_allowed":false,"requires":["provider","model_revision","terms_snapshot"]},
        {"license_basis":"external_oj_unreviewed","internal_eval_allowed":false,"private_training_allowed":false,"public_release_allowed":false,"requires":["source_uri","source_revision","terms_snapshot"]},
        {"license_basis":"unknown","internal_eval_allowed":false,"private_training_allowed":false,"public_release_allowed":false,"requires":[]}
      ],
      "inheritance":"all_derivatives_inherit_the_strictest_transitive_ancestor",
      "unknown_value_behavior":"deny",
      "takedown_behavior":"deny_and_rebuild_affected_manifests"
    }'::jsonb AS definition
)
INSERT INTO governance_policy_versions (
    policy_version, schema_version, policy_definition, policy_hash,
    terms_index_hash, status
)
SELECT
    '2026-07-13.v1',
    '1.0.0',
    definition,
    '5011df966f387e65876f44693d002043dd56a9d203524cae814e5102c76eff17',
    'd8d1518400a3aa07f0fd91f34418de7ac8ba53e02b3dd06d0e904a5fba391cfd',
    'active'
FROM policy;

CREATE TABLE provenance_terms_snapshots (
    snapshot_hash       TEXT PRIMARY KEY CHECK (snapshot_hash ~ '^[0-9a-f]{64}$'),
    source              TEXT NOT NULL,
    snapshot_uri        TEXT NOT NULL,
    effective_at        TIMESTAMPTZ,
    captured_at         TIMESTAMPTZ NOT NULL,
    reviewer            TEXT NOT NULL,
    review_status       TEXT NOT NULL CHECK (review_status IN ('unreviewed', 'approved', 'rejected', 'expired')),
    metadata            JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO provenance_terms_snapshots (
    snapshot_hash, source, snapshot_uri, captured_at, reviewer, review_status,
    metadata
)
VALUES (
    encode(digest('algoforge:unknown-terms:v1', 'sha256'), 'hex'),
    'unknown',
    'algoforge://governance/terms/unknown',
    NOW(),
    'quarantined_unknown',
    'unreviewed',
    '{"reason":"legacy content has no reviewed terms snapshot"}'::jsonb
);

CREATE TABLE provenance_license_rules (
    policy_version              TEXT NOT NULL REFERENCES governance_policy_versions(policy_version),
    license_basis               TEXT NOT NULL,
    internal_eval_allowed       BOOLEAN NOT NULL DEFAULT FALSE,
    private_training_allowed    BOOLEAN NOT NULL DEFAULT FALSE,
    public_release_allowed      BOOLEAN NOT NULL DEFAULT FALSE,
    requirements                JSONB NOT NULL DEFAULT '[]'::jsonb,
    PRIMARY KEY (policy_version, license_basis)
);

INSERT INTO provenance_license_rules (
    policy_version, license_basis, internal_eval_allowed,
    private_training_allowed, public_release_allowed, requirements
)
VALUES
    ('2026-07-13.v1', 'project_owned', TRUE, TRUE, TRUE,
     '["ownership_record","terms_snapshot"]'::jsonb),
    ('2026-07-13.v1', 'explicit_contributor_consent_v1', TRUE, TRUE, TRUE,
     '["signed_consent_record","terms_snapshot"]'::jsonb),
    ('2026-07-13.v1', 'reviewed_permissive_license', TRUE, FALSE, FALSE,
     '["legal_review_id","attribution_record","terms_snapshot"]'::jsonb),
    ('2026-07-13.v1', 'anthropic_generated_unreviewed', TRUE, FALSE, FALSE,
     '["provider","model_revision","terms_snapshot"]'::jsonb),
    ('2026-07-13.v1', 'external_oj_unreviewed', FALSE, FALSE, FALSE,
     '["source_uri","source_revision","terms_snapshot"]'::jsonb),
    ('2026-07-13.v1', 'unknown', FALSE, FALSE, FALSE, '[]'::jsonb);

CREATE TABLE provenance_artifacts (
    artifact_id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    artifact_type               TEXT NOT NULL,
    content_hash                TEXT CHECK (content_hash IS NULL OR content_hash ~ '^[0-9a-f]{64}$'),
    source_type                 TEXT NOT NULL,
    source_uri                  TEXT NOT NULL,
    source_revision             TEXT NOT NULL,
    creator                     TEXT NOT NULL,
    provider                    TEXT NOT NULL,
    model                       TEXT NOT NULL,
    model_revision              TEXT NOT NULL,
    generated_at                TIMESTAMPTZ NOT NULL,
    license_basis               TEXT NOT NULL,
    terms_snapshot_hash         TEXT NOT NULL REFERENCES provenance_terms_snapshots(snapshot_hash),
    retention_class             TEXT NOT NULL,
    retention_expires_at        TIMESTAMPTZ,
    takedown_status             TEXT NOT NULL DEFAULT 'quarantined_unknown'
                                CHECK (takedown_status IN (
                                    'active', 'quarantined_unknown', 'revoked',
                                    'takedown_requested', 'removed'
                                )),
    policy_version              TEXT NOT NULL,
    internal_eval_allowed       BOOLEAN NOT NULL DEFAULT FALSE,
    private_training_allowed    BOOLEAN NOT NULL DEFAULT FALSE,
    public_release_allowed      BOOLEAN NOT NULL DEFAULT FALSE,
    metadata                    JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at                  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at                  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    FOREIGN KEY (policy_version, license_basis)
        REFERENCES provenance_license_rules(policy_version, license_basis),
    CHECK (takedown_status <> 'active' OR content_hash IS NOT NULL),
    CHECK (retention_expires_at IS NULL OR retention_expires_at > generated_at)
);

CREATE INDEX idx_provenance_artifacts_content_hash
    ON provenance_artifacts(content_hash) WHERE content_hash IS NOT NULL;
CREATE INDEX idx_provenance_artifacts_license
    ON provenance_artifacts(policy_version, license_basis);
CREATE INDEX idx_provenance_artifacts_takedown
    ON provenance_artifacts(takedown_status);

CREATE TABLE provenance_artifact_bindings (
    artifact_id     UUID NOT NULL REFERENCES provenance_artifacts(artifact_id) ON DELETE RESTRICT,
    subject_type    TEXT NOT NULL,
    subject_id      TEXT NOT NULL,
    artifact_role   TEXT NOT NULL,
    binding_revision BIGINT NOT NULL DEFAULT 1 CHECK (binding_revision > 0),
    is_current      BOOLEAN NOT NULL DEFAULT TRUE,
    superseded_at   TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (artifact_id, subject_type, subject_id, artifact_role),
    UNIQUE (subject_type, subject_id, artifact_role, binding_revision),
    CHECK (is_current = (superseded_at IS NULL))
);

CREATE INDEX idx_provenance_bindings_subject
    ON provenance_artifact_bindings(subject_type, subject_id);
CREATE UNIQUE INDEX idx_provenance_bindings_current
    ON provenance_artifact_bindings(subject_type, subject_id, artifact_role)
    WHERE is_current;

CREATE TABLE provenance_artifact_ancestry (
    child_artifact_id   UUID NOT NULL REFERENCES provenance_artifacts(artifact_id) ON DELETE RESTRICT,
    parent_artifact_id  UUID NOT NULL REFERENCES provenance_artifacts(artifact_id) ON DELETE RESTRICT,
    relationship        TEXT NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (child_artifact_id, parent_artifact_id, relationship),
    CHECK (child_artifact_id <> parent_artifact_id)
);

CREATE INDEX idx_provenance_ancestry_parent
    ON provenance_artifact_ancestry(parent_artifact_id);

CREATE TABLE provenance_audit_events (
    event_id        BIGSERIAL PRIMARY KEY,
    artifact_id     UUID REFERENCES provenance_artifacts(artifact_id) ON DELETE RESTRICT,
    event_type      TEXT NOT NULL,
    actor           TEXT NOT NULL,
    reason          TEXT NOT NULL,
    policy_version  TEXT REFERENCES governance_policy_versions(policy_version),
    details         JSONB NOT NULL DEFAULT '{}'::jsonb,
    occurred_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE provenance_manifests (
    manifest_id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    schema_version      TEXT NOT NULL DEFAULT '1.0.0',
    policy_version      TEXT NOT NULL REFERENCES governance_policy_versions(policy_version),
    purpose             TEXT NOT NULL CHECK (purpose IN ('internal_eval', 'private_training', 'public_release')),
    manifest_hash       TEXT CHECK (manifest_hash IS NULL OR manifest_hash ~ '^[0-9a-f]{64}$'),
    manifest_document   JSONB,
    state               TEXT NOT NULL DEFAULT 'draft' CHECK (state IN ('draft', 'issued', 'invalidated')),
    invalidation_reason TEXT,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    issued_at           TIMESTAMPTZ,
    invalidated_at      TIMESTAMPTZ,
    CHECK ((state IN ('issued', 'invalidated')) = (issued_at IS NOT NULL AND manifest_hash IS NOT NULL AND manifest_document IS NOT NULL)),
    CHECK ((state = 'invalidated') = (invalidated_at IS NOT NULL AND invalidation_reason IS NOT NULL))
);

CREATE TABLE provenance_manifest_artifacts (
    manifest_id     UUID NOT NULL REFERENCES provenance_manifests(manifest_id) ON DELETE RESTRICT,
    artifact_id     UUID NOT NULL REFERENCES provenance_artifacts(artifact_id) ON DELETE RESTRICT,
    content_hash    TEXT NOT NULL CHECK (content_hash ~ '^[0-9a-f]{64}$'),
    PRIMARY KEY (manifest_id, artifact_id)
);

CREATE OR REPLACE FUNCTION enforce_provenance_permission_ceiling()
RETURNS TRIGGER AS $$
DECLARE
    rule provenance_license_rules%ROWTYPE;
    requirement TEXT;
    terms_approved BOOLEAN;
BEGIN
    SELECT * INTO STRICT rule
    FROM provenance_license_rules
    WHERE policy_version = NEW.policy_version
      AND license_basis = NEW.license_basis;

    IF (NEW.internal_eval_allowed AND NOT rule.internal_eval_allowed)
       OR (NEW.private_training_allowed AND NOT rule.private_training_allowed)
       OR (NEW.public_release_allowed AND NOT rule.public_release_allowed) THEN
        RAISE EXCEPTION 'artifact permission exceeds policy/license ceiling';
    END IF;
    IF NEW.internal_eval_allowed OR NEW.private_training_allowed OR NEW.public_release_allowed THEN
        SELECT review_status = 'approved' INTO terms_approved
        FROM provenance_terms_snapshots
        WHERE snapshot_hash = NEW.terms_snapshot_hash;
        IF NOT COALESCE(terms_approved, FALSE) THEN
            RAISE EXCEPTION 'allowed artifact requires an approved terms snapshot';
        END IF;
        FOR requirement IN
            SELECT jsonb_array_elements_text(rule.requirements)
        LOOP
            IF requirement = 'terms_snapshot' THEN
                CONTINUE;
            ELSIF requirement = 'provider' THEN
                IF btrim(NEW.provider) IN ('', 'not_applicable', 'unknown') THEN
                    RAISE EXCEPTION 'artifact is missing required provider evidence';
                END IF;
            ELSIF requirement = 'model_revision' THEN
                IF btrim(NEW.model_revision) IN ('', 'not_applicable', 'unknown') THEN
                    RAISE EXCEPTION 'artifact is missing required model_revision evidence';
                END IF;
            ELSIF requirement = 'source_uri' THEN
                IF btrim(NEW.source_uri) = '' THEN
                    RAISE EXCEPTION 'artifact is missing required source_uri evidence';
                END IF;
            ELSIF requirement = 'source_revision' THEN
                IF btrim(NEW.source_revision) = '' THEN
                    RAISE EXCEPTION 'artifact is missing required source_revision evidence';
                END IF;
            ELSIF NOT (NEW.metadata ? requirement)
                  OR btrim(COALESCE(NEW.metadata ->> requirement, '')) = '' THEN
                RAISE EXCEPTION 'artifact is missing required evidence %', requirement;
            END IF;
        END LOOP;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_provenance_permission_ceiling
    BEFORE INSERT OR UPDATE ON provenance_artifacts
    FOR EACH ROW EXECUTE FUNCTION enforce_provenance_permission_ceiling();

CREATE OR REPLACE FUNCTION enforce_provenance_artifact_update()
RETURNS TRIGGER AS $$
BEGIN
    IF NEW.artifact_id IS DISTINCT FROM OLD.artifact_id
       OR NEW.artifact_type IS DISTINCT FROM OLD.artifact_type
       OR NEW.content_hash IS DISTINCT FROM OLD.content_hash
       OR NEW.source_type IS DISTINCT FROM OLD.source_type
       OR NEW.source_uri IS DISTINCT FROM OLD.source_uri
       OR NEW.source_revision IS DISTINCT FROM OLD.source_revision
       OR NEW.creator IS DISTINCT FROM OLD.creator
       OR NEW.provider IS DISTINCT FROM OLD.provider
       OR NEW.model IS DISTINCT FROM OLD.model
       OR NEW.model_revision IS DISTINCT FROM OLD.model_revision
       OR NEW.generated_at IS DISTINCT FROM OLD.generated_at
       OR NEW.retention_class IS DISTINCT FROM OLD.retention_class
       OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'artifact identity/content/source fields are immutable; create a new artifact version';
    END IF;

    IF OLD.takedown_status IN ('revoked', 'takedown_requested', 'removed')
       THEN
        IF to_jsonb(NEW) IS DISTINCT FROM to_jsonb(OLD) THEN
            RAISE EXCEPTION 'terminal artifact governance state is immutable';
        END IF;
        RETURN OLD;
    END IF;

    IF OLD.takedown_status = 'active' THEN
        IF NEW.takedown_status NOT IN ('active', 'revoked', 'takedown_requested', 'removed') THEN
            RAISE EXCEPTION 'active artifact cannot return to quarantine';
        END IF;
        IF NEW.license_basis IS DISTINCT FROM OLD.license_basis
           OR NEW.terms_snapshot_hash IS DISTINCT FROM OLD.terms_snapshot_hash
           OR NEW.policy_version IS DISTINCT FROM OLD.policy_version
           OR NEW.metadata IS DISTINCT FROM OLD.metadata
           OR (NEW.internal_eval_allowed AND NOT OLD.internal_eval_allowed)
           OR (NEW.private_training_allowed AND NOT OLD.private_training_allowed)
           OR (NEW.public_release_allowed AND NOT OLD.public_release_allowed) THEN
            RAISE EXCEPTION 'active artifact decisions may only become stricter';
        END IF;
    ELSIF OLD.takedown_status = 'quarantined_unknown'
          AND NEW.takedown_status NOT IN ('quarantined_unknown', 'active', 'removed', 'takedown_requested') THEN
        RAISE EXCEPTION 'invalid quarantine transition to %', NEW.takedown_status;
    END IF;

    IF OLD.retention_expires_at IS NOT NULL
       AND (NEW.retention_expires_at IS NULL OR NEW.retention_expires_at > OLD.retention_expires_at) THEN
        RAISE EXCEPTION 'retention expiry may not be removed or extended in place';
    END IF;

    NEW.updated_at := clock_timestamp();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_enforce_provenance_artifact_update
    BEFORE UPDATE ON provenance_artifacts
    FOR EACH ROW EXECUTE FUNCTION enforce_provenance_artifact_update();

CREATE OR REPLACE FUNCTION prevent_provenance_ancestry_cycle()
RETURNS TRIGGER AS $$
BEGIN
    PERFORM pg_advisory_xact_lock(4717081156417147200);
    IF EXISTS (
        WITH RECURSIVE ancestors(artifact_id) AS (
            SELECT NEW.parent_artifact_id
            UNION
            SELECT edge.parent_artifact_id
            FROM provenance_artifact_ancestry edge
            JOIN ancestors a ON edge.child_artifact_id = a.artifact_id
        )
        SELECT 1 FROM ancestors WHERE artifact_id = NEW.child_artifact_id
    ) THEN
        RAISE EXCEPTION 'provenance ancestry cycle detected';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_prevent_provenance_ancestry_cycle
    BEFORE INSERT ON provenance_artifact_ancestry
    FOR EACH ROW EXECUTE FUNCTION prevent_provenance_ancestry_cycle();

CREATE OR REPLACE FUNCTION prevent_provenance_history_delete()
RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION '% rows are provenance history and cannot be updated/deleted', TG_TABLE_NAME;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_prevent_artifact_delete
    BEFORE DELETE ON provenance_artifacts
    FOR EACH ROW EXECUTE FUNCTION prevent_provenance_history_delete();

CREATE TRIGGER trg_prevent_binding_mutation
    BEFORE DELETE ON provenance_artifact_bindings
    FOR EACH ROW EXECUTE FUNCTION prevent_provenance_history_delete();

CREATE TRIGGER trg_prevent_ancestry_delete
    BEFORE UPDATE OR DELETE ON provenance_artifact_ancestry
    FOR EACH ROW EXECUTE FUNCTION prevent_provenance_history_delete();

CREATE OR REPLACE FUNCTION prevent_active_policy_rule_mutation()
RETURNS TRIGGER AS $$
DECLARE
    version TEXT;
BEGIN
    IF TG_OP = 'INSERT' THEN
        version := NEW.policy_version;
    ELSE
        version := OLD.policy_version;
    END IF;
    IF EXISTS (
        SELECT 1 FROM governance_policy_versions
        WHERE policy_version = version AND status IN ('active', 'retired')
    ) THEN
        RAISE EXCEPTION 'rules for active/retired policy % are immutable; create a new policy version', version;
    END IF;
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_prevent_active_policy_rule_mutation
    BEFORE INSERT OR UPDATE OR DELETE ON provenance_license_rules
    FOR EACH ROW EXECUTE FUNCTION prevent_active_policy_rule_mutation();

CREATE UNIQUE INDEX idx_governance_single_active_policy
    ON governance_policy_versions ((status)) WHERE status = 'active';

CREATE OR REPLACE FUNCTION enforce_policy_version_state()
RETURNS TRIGGER AS $$
DECLARE
    rule_count BIGINT;
    definition_count BIGINT;
    unmatched_count BIGINT;
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'policy versions are immutable history';
    END IF;
    IF TG_OP = 'INSERT' AND NEW.status <> 'draft' THEN
        RAISE EXCEPTION 'new policy versions must start as draft';
    END IF;
    IF TG_OP = 'UPDATE' THEN
        IF OLD.status = 'retired' THEN
            RAISE EXCEPTION 'retired policy versions are immutable';
        ELSIF OLD.status = 'active' THEN
            IF NEW.status <> 'retired'
               OR NEW.policy_version IS DISTINCT FROM OLD.policy_version
               OR NEW.schema_version IS DISTINCT FROM OLD.schema_version
               OR NEW.policy_definition IS DISTINCT FROM OLD.policy_definition
               OR NEW.policy_hash IS DISTINCT FROM OLD.policy_hash
               OR NEW.terms_index_hash IS DISTINCT FROM OLD.terms_index_hash
               OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
                RAISE EXCEPTION 'active policy can only transition to retired without content changes';
            END IF;
        ELSIF OLD.status = 'draft' AND NEW.status NOT IN ('draft', 'active') THEN
            RAISE EXCEPTION 'draft policy can only remain draft or become active';
        END IF;

        IF OLD.status = 'draft' AND NEW.status = 'active' THEN
            SELECT COUNT(*) INTO rule_count
            FROM provenance_license_rules WHERE policy_version = NEW.policy_version;
            definition_count := jsonb_array_length(COALESCE(NEW.policy_definition -> 'rules', '[]'::jsonb));
            SELECT COUNT(*) INTO unmatched_count
            FROM jsonb_array_elements(COALESCE(NEW.policy_definition -> 'rules', '[]'::jsonb)) expected
            WHERE NOT EXISTS (
                SELECT 1 FROM provenance_license_rules actual
                WHERE actual.policy_version = NEW.policy_version
                  AND actual.license_basis = expected ->> 'license_basis'
                  AND actual.internal_eval_allowed = COALESCE((expected ->> 'internal_eval_allowed')::BOOLEAN, FALSE)
                  AND actual.private_training_allowed = COALESCE((expected ->> 'private_training_allowed')::BOOLEAN, FALSE)
                  AND actual.public_release_allowed = COALESCE((expected ->> 'public_release_allowed')::BOOLEAN, FALSE)
                  AND actual.requirements = COALESCE(expected -> 'requires', '[]'::jsonb)
            );
            IF rule_count = 0 OR rule_count <> definition_count OR unmatched_count <> 0 THEN
                RAISE EXCEPTION 'policy definition and license rules do not match';
            END IF;
        END IF;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_enforce_policy_version_state
    BEFORE INSERT OR UPDATE OR DELETE ON governance_policy_versions
    FOR EACH ROW EXECUTE FUNCTION enforce_policy_version_state();

CREATE OR REPLACE FUNCTION enforce_terms_snapshot_state()
RETURNS TRIGGER AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'terms snapshots are immutable history';
    END IF;
    IF TG_OP = 'INSERT' AND NEW.review_status <> 'unreviewed' THEN
        RAISE EXCEPTION 'new terms snapshots must start unreviewed';
    END IF;
    IF TG_OP = 'UPDATE' THEN
        IF NEW.snapshot_hash IS DISTINCT FROM OLD.snapshot_hash
           OR NEW.source IS DISTINCT FROM OLD.source
           OR NEW.snapshot_uri IS DISTINCT FROM OLD.snapshot_uri
           OR NEW.effective_at IS DISTINCT FROM OLD.effective_at
           OR NEW.captured_at IS DISTINCT FROM OLD.captured_at
           OR NEW.metadata IS DISTINCT FROM OLD.metadata
           OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
            RAISE EXCEPTION 'terms snapshot content is immutable; create a new snapshot';
        END IF;
        IF OLD.review_status = 'unreviewed' AND NEW.review_status NOT IN ('approved', 'rejected') THEN
            RAISE EXCEPTION 'unreviewed terms may only become approved or rejected';
        ELSIF OLD.review_status = 'approved' AND NEW.review_status <> 'expired' THEN
            RAISE EXCEPTION 'approved terms may only become expired';
        ELSIF OLD.review_status IN ('rejected', 'expired') THEN
            RAISE EXCEPTION 'rejected/expired terms snapshots are immutable';
        END IF;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_enforce_terms_snapshot_state
    BEFORE INSERT OR UPDATE OR DELETE ON provenance_terms_snapshots
    FOR EACH ROW EXECUTE FUNCTION enforce_terms_snapshot_state();

CREATE OR REPLACE FUNCTION enforce_binding_history_update()
RETURNS TRIGGER AS $$
BEGIN
    IF OLD.artifact_id IS DISTINCT FROM NEW.artifact_id
       OR OLD.subject_type IS DISTINCT FROM NEW.subject_type
       OR OLD.subject_id IS DISTINCT FROM NEW.subject_id
       OR OLD.artifact_role IS DISTINCT FROM NEW.artifact_role
       OR OLD.binding_revision IS DISTINCT FROM NEW.binding_revision
       OR OLD.created_at IS DISTINCT FROM NEW.created_at
       OR NOT OLD.is_current
       OR NEW.is_current
       OR NEW.superseded_at IS NULL THEN
        RAISE EXCEPTION 'binding history only permits current -> superseded transition';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_enforce_binding_history_update
    BEFORE UPDATE ON provenance_artifact_bindings
    FOR EACH ROW EXECUTE FUNCTION enforce_binding_history_update();

CREATE OR REPLACE FUNCTION provenance_artifact_use_allowed(
    root_artifact_id UUID,
    requested_purpose TEXT
)
RETURNS BOOLEAN AS $$
    WITH RECURSIVE lineage AS (
        SELECT a.*, ARRAY[a.artifact_id] AS path, FALSE AS cycle_detected
        FROM provenance_artifacts a
        WHERE a.artifact_id = root_artifact_id
        UNION ALL
        SELECT parent.*, lineage.path || parent.artifact_id,
               parent.artifact_id = ANY(lineage.path) AS cycle_detected
        FROM lineage
        JOIN provenance_artifact_ancestry edge
          ON edge.child_artifact_id = lineage.artifact_id
        JOIN provenance_artifacts parent
          ON parent.artifact_id = edge.parent_artifact_id
        WHERE NOT lineage.cycle_detected
    )
    SELECT COALESCE(
        COUNT(*) > 0
        AND BOOL_AND(
            NOT lineage.cycle_detected
            AND lineage.content_hash IS NOT NULL
            AND btrim(lineage.artifact_type) <> ''
            AND btrim(lineage.source_type) <> ''
            AND btrim(lineage.source_uri) <> ''
            AND btrim(lineage.source_revision) <> ''
            AND btrim(lineage.creator) <> ''
            AND btrim(lineage.provider) <> ''
            AND btrim(lineage.model) <> ''
            AND btrim(lineage.model_revision) <> ''
            AND btrim(lineage.retention_class) <> ''
            AND lineage.takedown_status = 'active'
            AND (lineage.retention_expires_at IS NULL OR lineage.retention_expires_at > NOW())
            AND policy.status = 'active'
            AND terms.review_status = 'approved'
            AND CASE requested_purpose
                WHEN 'internal_eval' THEN lineage.internal_eval_allowed AND rule.internal_eval_allowed
                WHEN 'private_training' THEN lineage.private_training_allowed AND rule.private_training_allowed
                WHEN 'public_release' THEN lineage.public_release_allowed AND rule.public_release_allowed
                ELSE FALSE
            END
        ),
        FALSE
    )
    FROM lineage
    JOIN governance_policy_versions policy
      ON policy.policy_version = lineage.policy_version
    JOIN provenance_license_rules rule
      ON rule.policy_version = lineage.policy_version
     AND rule.license_basis = lineage.license_basis
    JOIN provenance_terms_snapshots terms
      ON terms.snapshot_hash = lineage.terms_snapshot_hash;
$$ LANGUAGE sql STABLE;

CREATE OR REPLACE FUNCTION enforce_manifest_artifact_policy()
RETURNS TRIGGER AS $$
DECLARE
    manifest provenance_manifests%ROWTYPE;
    artifact provenance_artifacts%ROWTYPE;
BEGIN
    SELECT * INTO STRICT manifest
    FROM provenance_manifests WHERE manifest_id = NEW.manifest_id
    FOR UPDATE;
    IF manifest.state <> 'draft' THEN
        RAISE EXCEPTION 'manifest items can only be changed while draft';
    END IF;
    SELECT * INTO STRICT artifact
    FROM provenance_artifacts WHERE artifact_id = NEW.artifact_id;
    IF artifact.policy_version <> manifest.policy_version THEN
        RAISE EXCEPTION 'artifact and manifest policy versions do not match';
    END IF;
    IF NEW.content_hash <> artifact.content_hash THEN
        RAISE EXCEPTION 'manifest content hash does not match artifact';
    END IF;
    IF NOT provenance_artifact_use_allowed(NEW.artifact_id, manifest.purpose) THEN
        RAISE EXCEPTION 'artifact is not eligible for manifest purpose %', manifest.purpose;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_enforce_manifest_artifact_policy
    BEFORE INSERT OR UPDATE ON provenance_manifest_artifacts
    FOR EACH ROW EXECUTE FUNCTION enforce_manifest_artifact_policy();

CREATE OR REPLACE FUNCTION provenance_manifest_canonical_document(target_manifest_id UUID)
RETURNS JSONB AS $$
    SELECT jsonb_build_object(
        'schema_version', manifest.schema_version,
        'policy_version', manifest.policy_version,
        'purpose', manifest.purpose,
        'artifacts', COALESCE(
            jsonb_agg(
                jsonb_build_object(
                    'artifact_id', artifact.artifact_id,
                    'artifact_type', artifact.artifact_type,
                    'content_hash', artifact.content_hash,
                    'source_type', artifact.source_type,
                    'source_uri', artifact.source_uri,
                    'source_revision', artifact.source_revision,
                    'creator', artifact.creator,
                    'provider', artifact.provider,
                    'model', artifact.model,
                    'model_revision', artifact.model_revision,
                    'generated_at', artifact.generated_at,
                    'license_basis', artifact.license_basis,
                    'terms_snapshot_hash', artifact.terms_snapshot_hash,
                    'ancestry_ids', COALESCE((
                        SELECT jsonb_agg(edge.parent_artifact_id ORDER BY edge.parent_artifact_id)
                        FROM provenance_artifact_ancestry edge
                        WHERE edge.child_artifact_id = artifact.artifact_id
                    ), '[]'::jsonb),
                    'retention_class', artifact.retention_class,
                    'retention_expires_at', artifact.retention_expires_at,
                    'takedown_status', artifact.takedown_status,
                    'policy_version', artifact.policy_version,
                    'internal_eval_allowed', artifact.internal_eval_allowed,
                    'private_training_allowed', artifact.private_training_allowed,
                    'public_release_allowed', artifact.public_release_allowed,
                    'metadata', artifact.metadata
                ) ORDER BY artifact.artifact_id
            ) FILTER (WHERE artifact.artifact_id IS NOT NULL),
            '[]'::jsonb
        )
    )
    FROM provenance_manifests manifest
    LEFT JOIN provenance_manifest_artifacts item
      ON item.manifest_id = manifest.manifest_id
    LEFT JOIN provenance_artifacts artifact
      ON artifact.artifact_id = item.artifact_id
    WHERE manifest.manifest_id = target_manifest_id
    GROUP BY manifest.manifest_id, manifest.schema_version,
             manifest.policy_version, manifest.purpose;
$$ LANGUAGE sql STABLE;

CREATE OR REPLACE FUNCTION enforce_manifest_issue()
RETURNS TRIGGER AS $$
DECLARE
    invalid_count BIGINT;
    item_count BIGINT;
    expected_document JSONB;
    expected_hash TEXT;
BEGIN
    IF NEW.manifest_id IS DISTINCT FROM OLD.manifest_id
       OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'manifest identity and creation time are immutable';
    END IF;
    IF NEW.state <> OLD.state
       AND NOT (
           (OLD.state = 'draft' AND NEW.state = 'issued')
           OR (OLD.state = 'issued' AND NEW.state = 'invalidated')
       ) THEN
        RAISE EXCEPTION 'invalid manifest state transition % -> %', OLD.state, NEW.state;
    END IF;
    IF OLD.state IN ('issued', 'invalidated') AND NEW.state = OLD.state
       AND to_jsonb(NEW) IS DISTINCT FROM to_jsonb(OLD) THEN
        RAISE EXCEPTION 'issued/invalidated manifests are immutable';
    END IF;
    IF OLD.state = 'issued' AND NEW.state = 'invalidated'
       AND (to_jsonb(NEW) - 'state' - 'invalidated_at' - 'invalidation_reason')
           IS DISTINCT FROM
           (to_jsonb(OLD) - 'state' - 'invalidated_at' - 'invalidation_reason') THEN
        RAISE EXCEPTION 'invalidation cannot alter issued manifest content';
    END IF;
    IF NEW.state = 'issued' AND OLD.state <> 'issued' THEN
        SELECT COUNT(*), COUNT(*) FILTER (
            WHERE NOT provenance_artifact_use_allowed(item.artifact_id, NEW.purpose)
               OR item.content_hash <> artifact.content_hash
               OR artifact.policy_version <> NEW.policy_version
        ) INTO item_count, invalid_count
        FROM provenance_manifest_artifacts item
        JOIN provenance_artifacts artifact USING (artifact_id)
        WHERE item.manifest_id = NEW.manifest_id;
        IF item_count = 0 OR invalid_count <> 0 THEN
            RAISE EXCEPTION 'manifest cannot be issued: item_count %, invalid_count %', item_count, invalid_count;
        END IF;
        expected_document := provenance_manifest_canonical_document(NEW.manifest_id);
        expected_hash := encode(digest(convert_to(expected_document::text, 'UTF8'), 'sha256'), 'hex');
        IF NEW.manifest_document IS DISTINCT FROM expected_document
           OR NEW.manifest_hash IS DISTINCT FROM expected_hash THEN
            RAISE EXCEPTION 'manifest document/hash is not canonical';
        END IF;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION enforce_manifest_insert()
RETURNS TRIGGER AS $$
BEGIN
    IF NEW.state <> 'draft'
       OR NEW.issued_at IS NOT NULL
       OR NEW.invalidated_at IS NOT NULL
       OR NEW.manifest_hash IS NOT NULL
       OR NEW.manifest_document IS NOT NULL THEN
        RAISE EXCEPTION 'manifests must be inserted as empty drafts';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_enforce_manifest_insert
    BEFORE INSERT ON provenance_manifests
    FOR EACH ROW EXECUTE FUNCTION enforce_manifest_insert();

CREATE TRIGGER trg_enforce_manifest_issue
    BEFORE UPDATE ON provenance_manifests
    FOR EACH ROW EXECUTE FUNCTION enforce_manifest_issue();

CREATE OR REPLACE FUNCTION enforce_manifest_item_delete()
RETURNS TRIGGER AS $$
DECLARE
    manifest_state TEXT;
BEGIN
    SELECT state INTO STRICT manifest_state
    FROM provenance_manifests
    WHERE manifest_id = OLD.manifest_id
    FOR UPDATE;
    IF manifest_state <> 'draft' THEN
        RAISE EXCEPTION 'issued/invalidated manifest items are immutable';
    END IF;
    RETURN OLD;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_enforce_manifest_item_delete
    BEFORE DELETE ON provenance_manifest_artifacts
    FOR EACH ROW EXECUTE FUNCTION enforce_manifest_item_delete();

CREATE OR REPLACE FUNCTION issue_provenance_manifest(target_manifest_id UUID)
RETURNS provenance_manifests AS $$
DECLARE
    current_manifest provenance_manifests%ROWTYPE;
    document JSONB;
    document_hash TEXT;
    issued provenance_manifests%ROWTYPE;
BEGIN
    SELECT * INTO STRICT current_manifest
    FROM provenance_manifests
    WHERE manifest_id = target_manifest_id
    FOR UPDATE;
    IF current_manifest.state <> 'draft' THEN
        RAISE EXCEPTION 'manifest % is not draft', target_manifest_id;
    END IF;
    document := provenance_manifest_canonical_document(target_manifest_id);
    document_hash := encode(digest(convert_to(document::text, 'UTF8'), 'sha256'), 'hex');
    UPDATE provenance_manifests
    SET state = 'issued',
        issued_at = clock_timestamp(),
        manifest_document = document,
        manifest_hash = document_hash
    WHERE manifest_id = target_manifest_id
    RETURNING * INTO issued;
    RETURN issued;
END;
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION invalidate_provenance_manifests(
    changed_artifact_id UUID,
    invalidation TEXT
)
RETURNS BIGINT AS $$
DECLARE
    affected_count BIGINT;
BEGIN
    WITH RECURSIVE affected(artifact_id) AS (
        SELECT changed_artifact_id
        UNION
        SELECT edge.child_artifact_id
        FROM provenance_artifact_ancestry edge
        JOIN affected a ON edge.parent_artifact_id = a.artifact_id
    ), invalidated AS (
        UPDATE provenance_manifests manifest
        SET state = 'invalidated',
            invalidated_at = NOW(),
            invalidation_reason = invalidation
        WHERE manifest.state = 'issued'
          AND EXISTS (
              SELECT 1
              FROM provenance_manifest_artifacts item
              JOIN affected a USING (artifact_id)
              WHERE item.manifest_id = manifest.manifest_id
          )
        RETURNING manifest_id
    )
    SELECT COUNT(*) INTO affected_count FROM invalidated;

    INSERT INTO provenance_audit_events (
        artifact_id, event_type, actor, reason, details
    ) VALUES (
        changed_artifact_id,
        'manifest_invalidation',
        'database_policy_trigger',
        invalidation,
        jsonb_build_object('invalidated_manifest_count', affected_count)
    );
    RETURN affected_count;
END;
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION invalidate_manifests_after_artifact_change()
RETURNS TRIGGER AS $$
BEGIN
    PERFORM invalidate_provenance_manifests(
        NEW.artifact_id,
        'artifact governance state changed'
    );
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_invalidate_manifests_after_artifact_change
    AFTER UPDATE OF content_hash, license_basis, terms_snapshot_hash,
        retention_expires_at, takedown_status, policy_version,
        internal_eval_allowed, private_training_allowed, public_release_allowed
    ON provenance_artifacts
    FOR EACH ROW EXECUTE FUNCTION invalidate_manifests_after_artifact_change();

CREATE OR REPLACE FUNCTION audit_provenance_artifact_governance_change()
RETURNS TRIGGER AS $$
BEGIN
    INSERT INTO provenance_audit_events (
        artifact_id, event_type, actor, reason, policy_version, details
    ) VALUES (
        NEW.artifact_id,
        'artifact_governance_change',
        'database_policy_trigger',
        'artifact governance decision changed',
        NEW.policy_version,
        jsonb_build_object(
            'old', jsonb_build_object(
                'license_basis', OLD.license_basis,
                'terms_snapshot_hash', OLD.terms_snapshot_hash,
                'retention_expires_at', OLD.retention_expires_at,
                'takedown_status', OLD.takedown_status,
                'policy_version', OLD.policy_version,
                'internal_eval_allowed', OLD.internal_eval_allowed,
                'private_training_allowed', OLD.private_training_allowed,
                'public_release_allowed', OLD.public_release_allowed,
                'metadata', OLD.metadata
            ),
            'new', jsonb_build_object(
                'license_basis', NEW.license_basis,
                'terms_snapshot_hash', NEW.terms_snapshot_hash,
                'retention_expires_at', NEW.retention_expires_at,
                'takedown_status', NEW.takedown_status,
                'policy_version', NEW.policy_version,
                'internal_eval_allowed', NEW.internal_eval_allowed,
                'private_training_allowed', NEW.private_training_allowed,
                'public_release_allowed', NEW.public_release_allowed,
                'metadata', NEW.metadata
            )
        )
    );
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_audit_provenance_artifact_governance_change
    AFTER UPDATE OF license_basis, terms_snapshot_hash, retention_expires_at,
        takedown_status, policy_version, internal_eval_allowed,
        private_training_allowed, public_release_allowed, metadata
    ON provenance_artifacts
    FOR EACH ROW EXECUTE FUNCTION audit_provenance_artifact_governance_change();

CREATE OR REPLACE FUNCTION invalidate_manifests_after_ancestry_change()
RETURNS TRIGGER AS $$
BEGIN
    PERFORM invalidate_provenance_manifests(
        NEW.child_artifact_id,
        'artifact ancestry changed'
    );
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_invalidate_manifests_after_ancestry_change
    AFTER INSERT OR UPDATE ON provenance_artifact_ancestry
    FOR EACH ROW EXECUTE FUNCTION invalidate_manifests_after_ancestry_change();

CREATE OR REPLACE FUNCTION invalidate_manifests_after_terms_change()
RETURNS TRIGGER AS $$
DECLARE
    artifact RECORD;
BEGIN
    IF OLD.review_status = 'approved' AND NEW.review_status <> 'approved' THEN
        FOR artifact IN
            SELECT artifact_id FROM provenance_artifacts
            WHERE terms_snapshot_hash = NEW.snapshot_hash
        LOOP
            PERFORM invalidate_provenance_manifests(
                artifact.artifact_id,
                'terms snapshot is no longer approved'
            );
        END LOOP;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_invalidate_manifests_after_terms_change
    AFTER UPDATE OF review_status ON provenance_terms_snapshots
    FOR EACH ROW EXECUTE FUNCTION invalidate_manifests_after_terms_change();

CREATE OR REPLACE FUNCTION invalidate_manifests_after_policy_retirement()
RETURNS TRIGGER AS $$
DECLARE
    affected_count BIGINT;
BEGIN
    IF OLD.status = 'active' AND NEW.status = 'retired' THEN
        WITH invalidated AS (
            UPDATE provenance_manifests
            SET state = 'invalidated',
                invalidated_at = NOW(),
                invalidation_reason = 'governance policy retired'
            WHERE state = 'issued' AND policy_version = NEW.policy_version
            RETURNING manifest_id
        )
        SELECT COUNT(*) INTO affected_count FROM invalidated;

        INSERT INTO provenance_audit_events (
            event_type, actor, reason, policy_version, details
        ) VALUES (
            'policy_retirement', 'database_policy_trigger',
            'governance policy retired', NEW.policy_version,
            jsonb_build_object('invalidated_manifest_count', affected_count)
        );
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_invalidate_manifests_after_policy_retirement
    AFTER UPDATE OF status ON governance_policy_versions
    FOR EACH ROW EXECUTE FUNCTION invalidate_manifests_after_policy_retirement();

CREATE OR REPLACE FUNCTION prevent_provenance_audit_mutation()
RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'provenance audit events are append-only';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_prevent_provenance_audit_mutation
    BEFORE UPDATE OR DELETE ON provenance_audit_events
    FOR EACH ROW EXECUTE FUNCTION prevent_provenance_audit_mutation();

CREATE OR REPLACE FUNCTION provenance_problem_content_hash(value problems)
RETURNS TEXT AS $$
    SELECT encode(digest(convert_to(jsonb_build_object(
        'title', value.title,
        'statement', value.statement,
        'level', value.level::TEXT,
        'difficulty', value.difficulty,
        'tags', value.tags,
        'one_line_hint', value.one_line_hint,
        'detailed_solution', value.detailed_solution,
        'time_limit', value.time_limit,
        'memory_limit', value.memory_limit,
        'source', value.source,
        'metadata', value.metadata_json
    )::TEXT, 'UTF8'), 'sha256'), 'hex');
$$ LANGUAGE sql IMMUTABLE;

CREATE OR REPLACE FUNCTION provenance_solution_content_hash(value solutions)
RETURNS TEXT AS $$
    SELECT encode(digest(convert_to(jsonb_build_object(
        'solution_type', value.solution_type,
        'language', value.language,
        'source_code', value.source_code
    )::TEXT, 'UTF8'), 'sha256'), 'hex');
$$ LANGUAGE sql IMMUTABLE;

CREATE OR REPLACE FUNCTION provenance_quiz_content_hash(value quiz_problems)
RETURNS TEXT AS $$
    SELECT encode(digest(convert_to(jsonb_build_object(
        'code', value.code,
        'title', value.title,
        'statement', value.statement,
        'type', value.type,
        'code_id', value.code_id,
        'code_hint', value.code_hint,
        'options', value.options,
        'answers', value.answers,
        'difficulty', value.difficulty,
        'visibility', value.visibility,
        'is_vip', value.is_vip,
        'tags', value.tags,
        'langs', value.langs,
        'explanation', value.explanation,
        'subject', value.subject
    )::TEXT, 'UTF8'), 'sha256'), 'hex');
$$ LANGUAGE sql IMMUTABLE;

CREATE OR REPLACE FUNCTION register_problem_provenance()
RETURNS TRIGGER AS $$
DECLARE
    artifact UUID := uuid_generate_v5(
        '00000000-0000-0000-0000-000000000001'::uuid,
        'problem:' || NEW.id::text
    );
BEGIN
    INSERT INTO provenance_artifacts (
        artifact_id, artifact_type, content_hash, source_type, source_uri,
        source_revision, creator, provider, model, model_revision, generated_at,
        license_basis, terms_snapshot_hash, retention_class, takedown_status,
        policy_version, metadata
    ) VALUES (
        artifact, 'problem_definition', provenance_problem_content_hash(NEW),
        'unreviewed_application_write',
        'algoforge://problems/' || NEW.id::text,
        'database-insert', 'quarantined_unknown', 'not_applicable',
        'not_applicable', 'not_applicable', NEW.created_at, 'unknown',
        encode(digest('algoforge:unknown-terms:v1', 'sha256'), 'hex'),
        'project_record', 'quarantined_unknown', '2026-07-13.v1',
        jsonb_build_object('status', NEW.status)
    );
    INSERT INTO provenance_artifact_bindings (
        artifact_id, subject_type, subject_id, artifact_role
    ) VALUES (artifact, 'problem', NEW.id::text, 'definition');
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_register_problem_provenance
    AFTER INSERT ON problems
    FOR EACH ROW EXECUTE FUNCTION register_problem_provenance();

CREATE OR REPLACE FUNCTION register_solution_provenance()
RETURNS TRIGGER AS $$
DECLARE
    artifact UUID := uuid_generate_v5(
        '00000000-0000-0000-0000-000000000001'::uuid,
        'solution:' || NEW.id::text
    );
BEGIN
    INSERT INTO provenance_artifacts (
        artifact_id, artifact_type, content_hash, source_type, source_uri,
        source_revision, creator, provider, model, model_revision, generated_at,
        license_basis, terms_snapshot_hash, retention_class, takedown_status,
        policy_version, metadata
    ) VALUES (
        artifact, 'solution_code', provenance_solution_content_hash(NEW),
        'unreviewed_application_write',
        'algoforge://solutions/' || NEW.id::text,
        'database-insert', 'quarantined_unknown', 'not_applicable',
        'not_applicable', 'not_applicable', NEW.created_at, 'unknown',
        encode(digest('algoforge:unknown-terms:v1', 'sha256'), 'hex'),
        'project_record', 'quarantined_unknown', '2026-07-13.v1',
        jsonb_build_object('language', NEW.language, 'solution_type', NEW.solution_type)
    );
    INSERT INTO provenance_artifact_bindings (
        artifact_id, subject_type, subject_id, artifact_role
    ) VALUES (artifact, 'solution', NEW.id::text, 'source_code');
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_register_solution_provenance
    AFTER INSERT ON solutions
    FOR EACH ROW EXECUTE FUNCTION register_solution_provenance();

CREATE OR REPLACE FUNCTION register_testcase_provenance()
RETURNS TRIGGER AS $$
DECLARE
    role TEXT;
    artifact UUID;
    object_uri TEXT;
BEGIN
    FOREACH role IN ARRAY ARRAY['input', 'output'] LOOP
        artifact := uuid_generate_v5(
            '00000000-0000-0000-0000-000000000001'::uuid,
            'testcase:' || NEW.id::text || ':' || role
        );
        object_uri := CASE role WHEN 'input' THEN NEW.input_path ELSE NEW.output_path END;
        INSERT INTO provenance_artifacts (
            artifact_id, artifact_type, content_hash, source_type, source_uri,
            source_revision, creator, provider, model, model_revision, generated_at,
            license_basis, terms_snapshot_hash, retention_class, takedown_status,
            policy_version, metadata
        ) VALUES (
            artifact, 'test_' || role, NULL, 'unreviewed_application_write',
            object_uri, 'database-insert', 'quarantined_unknown', 'not_applicable',
            'not_applicable', 'not_applicable', NEW.created_at, 'unknown',
            encode(digest('algoforge:unknown-terms:v1', 'sha256'), 'hex'),
            'project_record', 'quarantined_unknown', '2026-07-13.v1',
            jsonb_build_object('test_index', NEW.test_index, 'content_hash_status', 'unknown')
        );
        INSERT INTO provenance_artifact_bindings (
            artifact_id, subject_type, subject_id, artifact_role
        ) VALUES (artifact, 'testcase', NEW.id::text, role);
    END LOOP;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_register_testcase_provenance
    AFTER INSERT ON testcases
    FOR EACH ROW EXECUTE FUNCTION register_testcase_provenance();

CREATE OR REPLACE FUNCTION register_quiz_provenance()
RETURNS TRIGGER AS $$
DECLARE
    artifact UUID := uuid_generate_v5(
        '00000000-0000-0000-0000-000000000001'::uuid,
        'quiz:' || NEW.id::text
    );
BEGIN
    INSERT INTO provenance_artifacts (
        artifact_id, artifact_type, content_hash, source_type, source_uri,
        source_revision, creator, provider, model, model_revision, generated_at,
        license_basis, terms_snapshot_hash, retention_class, takedown_status,
        policy_version, metadata
    ) VALUES (
        artifact, 'quiz_problem', provenance_quiz_content_hash(NEW),
        'unreviewed_application_write',
        'algoforge://quizzes/' || NEW.id::text,
        'database-insert', 'quarantined_unknown', 'not_applicable',
        'not_applicable', 'not_applicable', NEW.created_at, 'unknown',
        encode(digest('algoforge:unknown-terms:v1', 'sha256'), 'hex'),
        'project_record', 'quarantined_unknown', '2026-07-13.v1',
        jsonb_build_object('code', NEW.code, 'subject', NEW.subject)
    );
    INSERT INTO provenance_artifact_bindings (
        artifact_id, subject_type, subject_id, artifact_role
    ) VALUES (artifact, 'quiz_problem', NEW.id::text, 'content');
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_register_quiz_provenance
    AFTER INSERT ON quiz_problems
    FOR EACH ROW EXECUTE FUNCTION register_quiz_provenance();

CREATE OR REPLACE FUNCTION quarantine_bound_artifact(
    bound_subject_type TEXT,
    bound_subject_id TEXT,
    bound_artifact_role TEXT,
    new_content_hash TEXT,
    new_source_uri TEXT,
    change_reason TEXT,
    metadata_patch JSONB
)
RETURNS VOID AS $$
DECLARE
    target UUID;
    replacement UUID := gen_random_uuid();
    old_hash TEXT;
    old_type TEXT;
    old_revision BIGINT;
BEGIN
    SELECT binding.artifact_id, artifact.content_hash, artifact.artifact_type,
           binding.binding_revision
    INTO STRICT target, old_hash, old_type, old_revision
    FROM provenance_artifact_bindings binding
    JOIN provenance_artifacts artifact USING (artifact_id)
    WHERE binding.subject_type = bound_subject_type
      AND binding.subject_id = bound_subject_id
      AND binding.artifact_role = bound_artifact_role
      AND binding.is_current
    FOR UPDATE OF binding, artifact;

    INSERT INTO provenance_artifacts (
        artifact_id, artifact_type, content_hash, source_type, source_uri,
        source_revision, creator, provider, model, model_revision, generated_at,
        license_basis, terms_snapshot_hash, retention_class, takedown_status,
        policy_version, metadata
    ) VALUES (
        replacement, old_type, new_content_hash, 'unreviewed_application_write',
        new_source_uri, 'database-update:' || clock_timestamp()::text,
        'quarantined_unknown', 'not_applicable', 'not_applicable',
        'not_applicable', NOW(), 'unknown',
        encode(digest('algoforge:unknown-terms:v1', 'sha256'), 'hex'),
        'project_record', 'quarantined_unknown', '2026-07-13.v1',
        COALESCE(metadata_patch, '{}'::jsonb)
    );

    UPDATE provenance_artifact_bindings
    SET is_current = FALSE, superseded_at = clock_timestamp()
    WHERE artifact_id = target
      AND subject_type = bound_subject_type
      AND subject_id = bound_subject_id
      AND artifact_role = bound_artifact_role;

    INSERT INTO provenance_artifact_bindings (
        artifact_id, subject_type, subject_id, artifact_role, binding_revision
    ) VALUES (
        replacement, bound_subject_type, bound_subject_id,
        bound_artifact_role, old_revision + 1
    );

    INSERT INTO provenance_artifact_ancestry (
        child_artifact_id, parent_artifact_id, relationship
    ) VALUES (replacement, target, 'supersedes');

    INSERT INTO provenance_audit_events (
        artifact_id, event_type, actor, reason, policy_version, details
    ) VALUES (
        replacement, 'bound_content_changed', 'database_policy_trigger',
        change_reason, '2026-07-13.v1',
        jsonb_build_object(
            'superseded_artifact_id', target,
            'old_content_hash', old_hash,
            'new_content_hash', new_content_hash
        )
    );
END;
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION remove_bound_artifact(
    bound_subject_type TEXT,
    bound_subject_id TEXT,
    bound_artifact_role TEXT
)
RETURNS VOID AS $$
DECLARE
    target UUID;
BEGIN
    SELECT artifact_id INTO STRICT target
    FROM provenance_artifact_bindings
    WHERE subject_type = bound_subject_type
      AND subject_id = bound_subject_id
      AND artifact_role = bound_artifact_role
      AND is_current
    FOR UPDATE;

    UPDATE provenance_artifacts
    SET takedown_status = 'removed',
        internal_eval_allowed = FALSE,
        private_training_allowed = FALSE,
        public_release_allowed = FALSE,
        updated_at = NOW()
    WHERE artifact_id = target;

    UPDATE provenance_artifact_bindings
    SET is_current = FALSE, superseded_at = clock_timestamp()
    WHERE artifact_id = target
      AND subject_type = bound_subject_type
      AND subject_id = bound_subject_id
      AND artifact_role = bound_artifact_role;

    INSERT INTO provenance_audit_events (
        artifact_id, event_type, actor, reason, policy_version
    ) VALUES (
        target, 'bound_subject_deleted', 'database_policy_trigger',
        'bound application row was deleted', '2026-07-13.v1'
    );
END;
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION update_problem_provenance()
RETURNS TRIGGER AS $$
BEGIN
    IF provenance_problem_content_hash(NEW) IS DISTINCT FROM provenance_problem_content_hash(OLD) THEN
        PERFORM quarantine_bound_artifact(
            'problem', NEW.id::text, 'definition',
            provenance_problem_content_hash(NEW),
            'algoforge://problems/' || NEW.id::text,
            'problem statement changed',
            jsonb_build_object('status', NEW.status)
        );
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_update_problem_provenance
    AFTER UPDATE OF title, statement, level, difficulty, tags, one_line_hint,
        detailed_solution, time_limit, memory_limit, source, metadata_json
    ON problems
    FOR EACH ROW EXECUTE FUNCTION update_problem_provenance();

CREATE OR REPLACE FUNCTION delete_problem_provenance()
RETURNS TRIGGER AS $$
BEGIN
    PERFORM remove_bound_artifact('problem', OLD.id::text, 'definition');
    RETURN OLD;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_delete_problem_provenance
    AFTER DELETE ON problems
    FOR EACH ROW EXECUTE FUNCTION delete_problem_provenance();

CREATE OR REPLACE FUNCTION update_solution_provenance()
RETURNS TRIGGER AS $$
BEGIN
    IF provenance_solution_content_hash(NEW) IS DISTINCT FROM provenance_solution_content_hash(OLD) THEN
        PERFORM quarantine_bound_artifact(
            'solution', NEW.id::text, 'source_code',
            provenance_solution_content_hash(NEW),
            'algoforge://solutions/' || NEW.id::text,
            'solution source changed',
            jsonb_build_object('language', NEW.language, 'solution_type', NEW.solution_type)
        );
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_update_solution_provenance
    AFTER UPDATE OF solution_type, language, source_code ON solutions
    FOR EACH ROW EXECUTE FUNCTION update_solution_provenance();

CREATE OR REPLACE FUNCTION delete_solution_provenance()
RETURNS TRIGGER AS $$
BEGIN
    PERFORM remove_bound_artifact('solution', OLD.id::text, 'source_code');
    RETURN OLD;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_delete_solution_provenance
    AFTER DELETE ON solutions
    FOR EACH ROW EXECUTE FUNCTION delete_solution_provenance();

CREATE OR REPLACE FUNCTION update_testcase_provenance()
RETURNS TRIGGER AS $$
BEGIN
    IF NEW.input_path IS DISTINCT FROM OLD.input_path THEN
        PERFORM quarantine_bound_artifact(
            'testcase', NEW.id::text, 'input', NULL, NEW.input_path,
            'test input reference changed',
            jsonb_build_object('test_index', NEW.test_index, 'content_hash_status', 'unknown')
        );
    END IF;
    IF NEW.output_path IS DISTINCT FROM OLD.output_path THEN
        PERFORM quarantine_bound_artifact(
            'testcase', NEW.id::text, 'output', NULL, NEW.output_path,
            'test output reference changed',
            jsonb_build_object('test_index', NEW.test_index, 'content_hash_status', 'unknown')
        );
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_update_testcase_provenance
    AFTER UPDATE OF input_path, output_path ON testcases
    FOR EACH ROW EXECUTE FUNCTION update_testcase_provenance();

CREATE OR REPLACE FUNCTION delete_testcase_provenance()
RETURNS TRIGGER AS $$
BEGIN
    PERFORM remove_bound_artifact('testcase', OLD.id::text, 'input');
    PERFORM remove_bound_artifact('testcase', OLD.id::text, 'output');
    RETURN OLD;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_delete_testcase_provenance
    AFTER DELETE ON testcases
    FOR EACH ROW EXECUTE FUNCTION delete_testcase_provenance();

CREATE OR REPLACE FUNCTION update_quiz_provenance()
RETURNS TRIGGER AS $$
BEGIN
    IF provenance_quiz_content_hash(NEW) IS DISTINCT FROM provenance_quiz_content_hash(OLD) THEN
        PERFORM quarantine_bound_artifact(
            'quiz_problem', NEW.id::text, 'content',
            provenance_quiz_content_hash(NEW),
            'algoforge://quizzes/' || NEW.id::text,
            'quiz content changed',
            jsonb_build_object('code', NEW.code, 'subject', NEW.subject)
        );
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_update_quiz_provenance
    AFTER UPDATE OF code, title, statement, type, code_id, code_hint, options,
        answers, difficulty, visibility, is_vip, tags, langs, explanation, subject
    ON quiz_problems
    FOR EACH ROW EXECUTE FUNCTION update_quiz_provenance();

CREATE OR REPLACE FUNCTION delete_quiz_provenance()
RETURNS TRIGGER AS $$
BEGIN
    PERFORM remove_bound_artifact('quiz_problem', OLD.id::text, 'content');
    RETURN OLD;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_delete_quiz_provenance
    AFTER DELETE ON quiz_problems
    FOR EACH ROW EXECUTE FUNCTION delete_quiz_provenance();

-- Legacy problems: source/license/model details are not inferable, so every
-- record is explicit and denied rather than silently treated as project-owned.
INSERT INTO provenance_artifacts (
    artifact_id, artifact_type, content_hash, source_type, source_uri,
    source_revision, creator, provider, model, model_revision, generated_at,
    license_basis, terms_snapshot_hash, retention_class, takedown_status,
    policy_version, metadata
)
SELECT
    uuid_generate_v5('00000000-0000-0000-0000-000000000001'::uuid, 'problem:' || p.id::text),
    'problem_definition',
    provenance_problem_content_hash(p),
    'legacy_unknown',
    'algoforge://legacy/problems/' || p.id::text,
    'migration-014',
    'quarantined_unknown',
    'not_applicable',
    'not_applicable',
    'not_applicable',
    p.created_at,
    'unknown',
    encode(digest('algoforge:unknown-terms:v1', 'sha256'), 'hex'),
    'project_record',
    'quarantined_unknown',
    '2026-07-13.v1',
    jsonb_build_object('legacy_source', p.source)
FROM problems p;

INSERT INTO provenance_artifact_bindings (artifact_id, subject_type, subject_id, artifact_role)
SELECT
    uuid_generate_v5('00000000-0000-0000-0000-000000000001'::uuid, 'problem:' || id::text),
    'problem', id::text, 'definition'
FROM problems;

INSERT INTO provenance_artifacts (
    artifact_id, artifact_type, content_hash, source_type, source_uri,
    source_revision, creator, provider, model, model_revision, generated_at,
    license_basis, terms_snapshot_hash, retention_class, takedown_status,
    policy_version, metadata
)
SELECT
    uuid_generate_v5('00000000-0000-0000-0000-000000000001'::uuid, 'solution:' || s.id::text),
    'solution_code',
    provenance_solution_content_hash(s),
    'legacy_unknown',
    'algoforge://legacy/solutions/' || s.id::text,
    'migration-014',
    'quarantined_unknown',
    'not_applicable',
    'not_applicable',
    'not_applicable',
    s.created_at,
    'unknown',
    encode(digest('algoforge:unknown-terms:v1', 'sha256'), 'hex'),
    'project_record',
    'quarantined_unknown',
    '2026-07-13.v1',
    jsonb_build_object('language', s.language, 'solution_type', s.solution_type)
FROM solutions s;

INSERT INTO provenance_artifact_bindings (artifact_id, subject_type, subject_id, artifact_role)
SELECT
    uuid_generate_v5('00000000-0000-0000-0000-000000000001'::uuid, 'solution:' || id::text),
    'solution', id::text, 'source_code'
FROM solutions;

INSERT INTO provenance_artifacts (
    artifact_id, artifact_type, content_hash, source_type, source_uri,
    source_revision, creator, provider, model, model_revision, generated_at,
    license_basis, terms_snapshot_hash, retention_class, takedown_status,
    policy_version, metadata
)
SELECT
    uuid_generate_v5('00000000-0000-0000-0000-000000000001'::uuid,
        'testcase:' || id::text || ':' || role),
    'test_' || role,
    NULL,
    'legacy_unknown',
    CASE role WHEN 'input' THEN input_path ELSE output_path END,
    'migration-014',
    'quarantined_unknown',
    'not_applicable',
    'not_applicable',
    'not_applicable',
    created_at,
    'unknown',
    encode(digest('algoforge:unknown-terms:v1', 'sha256'), 'hex'),
    'project_record',
    'quarantined_unknown',
    '2026-07-13.v1',
    jsonb_build_object('test_index', test_index, 'content_hash_status', 'unknown')
FROM testcases
CROSS JOIN (VALUES ('input'), ('output')) AS roles(role);

INSERT INTO provenance_artifact_bindings (artifact_id, subject_type, subject_id, artifact_role)
SELECT
    uuid_generate_v5('00000000-0000-0000-0000-000000000001'::uuid,
        'testcase:' || id::text || ':' || role),
    'testcase', id::text, role
FROM testcases
CROSS JOIN (VALUES ('input'), ('output')) AS roles(role);

INSERT INTO provenance_artifacts (
    artifact_id, artifact_type, content_hash, source_type, source_uri,
    source_revision, creator, provider, model, model_revision, generated_at,
    license_basis, terms_snapshot_hash, retention_class, takedown_status,
    policy_version, metadata
)
SELECT
    uuid_generate_v5('00000000-0000-0000-0000-000000000001'::uuid, 'quiz:' || q.id::text),
    'quiz_problem',
    provenance_quiz_content_hash(q),
    'legacy_unknown',
    'algoforge://legacy/quizzes/' || q.id::text,
    'migration-014',
    'quarantined_unknown',
    'not_applicable',
    'not_applicable',
    'not_applicable',
    q.created_at,
    'unknown',
    encode(digest('algoforge:unknown-terms:v1', 'sha256'), 'hex'),
    'project_record',
    'quarantined_unknown',
    '2026-07-13.v1',
    jsonb_build_object('code', q.code, 'subject', q.subject)
FROM quiz_problems q;

INSERT INTO provenance_artifact_bindings (artifact_id, subject_type, subject_id, artifact_role)
SELECT
    uuid_generate_v5('00000000-0000-0000-0000-000000000001'::uuid, 'quiz:' || id::text),
    'quiz_problem', id::text, 'content'
FROM quiz_problems;

CREATE VIEW provenance_coverage_audit AS
WITH expected AS (
    SELECT 'problem'::TEXT AS subject_type, id::TEXT AS subject_id, 'definition'::TEXT AS artifact_role FROM problems
    UNION ALL
    SELECT 'solution', id::TEXT, 'source_code' FROM solutions
    UNION ALL
    SELECT 'testcase', id::TEXT, role
    FROM testcases CROSS JOIN (VALUES ('input'), ('output')) AS roles(role)
    UNION ALL
    SELECT 'quiz_problem', id::TEXT, 'content' FROM quiz_problems
)
SELECT
    expected.subject_type,
    COUNT(*) AS expected_count,
    COUNT(binding.artifact_id) AS registered_count,
    COUNT(*) - COUNT(binding.artifact_id) AS missing_count,
    COUNT(*) FILTER (WHERE artifact.takedown_status = 'quarantined_unknown') AS quarantined_unknown_count
FROM expected
LEFT JOIN provenance_artifact_bindings binding
  ON binding.subject_type = expected.subject_type
 AND binding.subject_id = expected.subject_id
 AND binding.artifact_role = expected.artifact_role
 AND binding.is_current
LEFT JOIN provenance_artifacts artifact USING (artifact_id)
GROUP BY expected.subject_type;

CREATE VIEW provenance_distributable_manifests AS
SELECT manifest.*
FROM provenance_manifests manifest
WHERE manifest.state = 'issued'
  AND EXISTS (
      SELECT 1 FROM provenance_manifest_artifacts item
      WHERE item.manifest_id = manifest.manifest_id
  )
  AND manifest.manifest_document = provenance_manifest_canonical_document(manifest.manifest_id)
  AND manifest.manifest_hash = encode(digest(convert_to(
      provenance_manifest_canonical_document(manifest.manifest_id)::text,
      'UTF8'
  ), 'sha256'), 'hex')
  AND NOT EXISTS (
      SELECT 1
      FROM provenance_manifest_artifacts item
      JOIN provenance_artifacts artifact USING (artifact_id)
      WHERE item.manifest_id = manifest.manifest_id
        AND (
            artifact.policy_version <> manifest.policy_version
            OR
            item.content_hash <> artifact.content_hash
            OR NOT provenance_artifact_use_allowed(item.artifact_id, manifest.purpose)
        )
  );

INSERT INTO provenance_audit_events (
    event_type, actor, reason, policy_version, details
)
SELECT
    'legacy_backfill',
    'migration-014',
    'legacy artifacts were explicitly quarantined pending source review',
    '2026-07-13.v1',
    jsonb_build_object(
        'artifact_count', COUNT(*),
        'quarantined_unknown_count', COUNT(*) FILTER (
            WHERE takedown_status = 'quarantined_unknown'
        )
    )
FROM provenance_artifacts
HAVING COUNT(*) > 0;
