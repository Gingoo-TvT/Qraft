package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type WorkflowArtifactProvenance struct {
	ArtifactType   string
	SourceType     string
	ContentHash    string
	SourceURI      string
	SourceRevision string
	Creator        string
	Provider       string
	Model          string
	ModelRevision  string
	GeneratedAt    time.Time
	WorkflowID     string
	ArtifactRole   string
	RetentionClass string
	Metadata       json.RawMessage
}

type ProvenanceArtifactRepository struct {
	db *pgxpool.Pool
}

func NewProvenanceArtifactRepository(db *pgxpool.Pool) *ProvenanceArtifactRepository {
	return &ProvenanceArtifactRepository{db: db}
}

func (r *ProvenanceArtifactRepository) RecordWorkflowArtifact(
	ctx context.Context,
	record WorkflowArtifactProvenance,
) (uuid.UUID, error) {
	if err := validateWorkflowArtifactProvenance(record); err != nil {
		return uuid.Nil, err
	}
	artifactID := uuid.NewSHA1(
		uuid.NameSpaceURL,
		[]byte(strings.Join([]string{
			"algoforge:workflow-artifact", record.WorkflowID, record.ArtifactRole,
			record.ContentHash, record.SourceURI,
		}, "\x00")),
	)
	unknownTermsDigest := sha256.Sum256([]byte("algoforge:unknown-terms:v1"))
	unknownTermsHash := hex.EncodeToString(unknownTermsDigest[:])

	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return uuid.Nil, fmt.Errorf("begin workflow artifact provenance: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback after commit is a no-op

	var policyVersion string
	if err := tx.QueryRow(ctx, `
		SELECT policy_version FROM governance_policy_versions
		WHERE status='active'
		ORDER BY created_at DESC, policy_version DESC
		LIMIT 1`).Scan(&policyVersion); err != nil {
		return uuid.Nil, fmt.Errorf("selecting active provenance policy: %w", err)
	}
	metadata := record.Metadata
	if len(metadata) == 0 {
		metadata = json.RawMessage(`{}`)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO provenance_artifacts (
			artifact_id, artifact_type, content_hash, source_type, source_uri,
			source_revision, creator, provider, model, model_revision, generated_at,
			license_basis, terms_snapshot_hash, retention_class, takedown_status,
			policy_version, internal_eval_allowed, private_training_allowed,
			public_release_allowed, metadata
		) VALUES (
			$1,$2,$3,$4,$5,
			$6,$7,$8,$9,$10,$11,
			'unknown',$12,$13,'quarantined_unknown',
			$14,FALSE,FALSE,FALSE,$15::jsonb
		)
		ON CONFLICT (artifact_id) DO NOTHING`,
		artifactID, record.ArtifactType, record.ContentHash, record.SourceType, record.SourceURI,
		record.SourceRevision, record.Creator, record.Provider, record.Model,
		record.ModelRevision, record.GeneratedAt, unknownTermsHash, record.RetentionClass, policyVersion, string(metadata)); err != nil {
		return uuid.Nil, fmt.Errorf("recording workflow artifact provenance: %w", err)
	}

	var storedType, storedSourceType, storedHash, storedURI, storedCreator string
	var storedProvider, storedModel, storedModelRevision string
	if err := tx.QueryRow(ctx, `
		SELECT artifact_type, source_type, content_hash, source_uri, creator,
		       provider, model, model_revision
		FROM provenance_artifacts WHERE artifact_id=$1
		FOR SHARE`, artifactID).Scan(
		&storedType, &storedSourceType, &storedHash, &storedURI, &storedCreator,
		&storedProvider, &storedModel, &storedModelRevision,
	); err != nil {
		return uuid.Nil, fmt.Errorf("verifying workflow artifact provenance: %w", err)
	}
	if storedType != record.ArtifactType || storedSourceType != record.SourceType ||
		storedHash != record.ContentHash || storedURI != record.SourceURI ||
		storedCreator != record.Creator || storedProvider != record.Provider ||
		storedModel != record.Model || storedModelRevision != record.ModelRevision {
		return uuid.Nil, fmt.Errorf("workflow artifact provenance identity collision for %s", artifactID)
	}

	var currentArtifactID uuid.UUID
	var currentRevision int64
	err = tx.QueryRow(ctx, `
		SELECT artifact_id, binding_revision
		FROM provenance_artifact_bindings
		WHERE subject_type='workflow_execution'
		  AND subject_id=$1
		  AND artifact_role=$2
		  AND is_current
		FOR UPDATE`, record.WorkflowID, record.ArtifactRole).Scan(
		&currentArtifactID, &currentRevision,
	)
	bindingChanged := false
	supersededArtifactID := uuid.Nil
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		if _, err := tx.Exec(ctx, `
			INSERT INTO provenance_artifact_bindings (
				artifact_id, subject_type, subject_id, artifact_role, binding_revision
			) VALUES ($1,'workflow_execution',$2,$3,1)`,
			artifactID, record.WorkflowID, record.ArtifactRole); err != nil {
			return uuid.Nil, fmt.Errorf("binding workflow artifact provenance: %w", err)
		}
		currentRevision = 1
		bindingChanged = true
	case err != nil:
		return uuid.Nil, fmt.Errorf("reading current workflow artifact binding: %w", err)
	case currentArtifactID == artifactID:
		// Exact activity redelivery: the current binding is already correct.
	default:
		supersededArtifactID = currentArtifactID
		if _, err := tx.Exec(ctx, `
			UPDATE provenance_artifact_bindings
			SET is_current=FALSE, superseded_at=clock_timestamp()
			WHERE artifact_id=$1
			  AND subject_type='workflow_execution'
			  AND subject_id=$2
			  AND artifact_role=$3
			  AND is_current`, currentArtifactID, record.WorkflowID, record.ArtifactRole); err != nil {
			return uuid.Nil, fmt.Errorf("superseding workflow artifact binding: %w", err)
		}
		currentRevision++
		if _, err := tx.Exec(ctx, `
			INSERT INTO provenance_artifact_bindings (
				artifact_id, subject_type, subject_id, artifact_role, binding_revision
			) VALUES ($1,'workflow_execution',$2,$3,$4)`,
			artifactID, record.WorkflowID, record.ArtifactRole, currentRevision); err != nil {
			return uuid.Nil, fmt.Errorf("binding replacement workflow artifact provenance: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO provenance_artifact_ancestry (
				child_artifact_id, parent_artifact_id, relationship
			) VALUES ($1,$2,'supersedes')
			ON CONFLICT DO NOTHING`, artifactID, currentArtifactID); err != nil {
			return uuid.Nil, fmt.Errorf("recording workflow artifact supersession: %w", err)
		}
		bindingChanged = true
	}
	if bindingChanged {
		details := map[string]interface{}{
			"workflow_id":      record.WorkflowID,
			"artifact_role":    record.ArtifactRole,
			"binding_revision": currentRevision,
		}
		eventType := "workflow_artifact_recorded"
		reason := "workflow CAS artifact recorded before downstream processing"
		if supersededArtifactID != uuid.Nil {
			eventType = "workflow_artifact_superseded"
			reason = "workflow provider retry replaced the current artifact binding"
			details["superseded_artifact_id"] = supersededArtifactID.String()
		}
		detailsJSON, err := json.Marshal(details)
		if err != nil {
			return uuid.Nil, fmt.Errorf("encoding workflow artifact audit details: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO provenance_audit_events (
				artifact_id, event_type, actor, reason, policy_version, details
			) VALUES ($1,$2,'workflow_activity',$3,$4,$5::jsonb)`,
			artifactID, eventType, reason, policyVersion, string(detailsJSON)); err != nil {
			return uuid.Nil, fmt.Errorf("auditing workflow artifact provenance: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, fmt.Errorf("commit workflow artifact provenance: %w", err)
	}
	return artifactID, nil
}

// ExpireRetentionBatch transitions expired artifacts to removed and clears all
// permissions. Existing governance triggers invalidate affected manifests and
// append the detailed before/after audit event.
func (r *ProvenanceArtifactRepository) ExpireRetentionBatch(ctx context.Context, limit int) (int64, error) {
	if limit <= 0 || limit > 10_000 {
		return 0, fmt.Errorf("retention audit limit must be in [1,10000]")
	}
	tag, err := r.db.Exec(ctx, `
		WITH expired AS (
			SELECT artifact_id FROM provenance_artifacts
			WHERE retention_expires_at IS NOT NULL
			  AND retention_expires_at <= NOW()
			  AND takedown_status NOT IN ('removed','revoked','takedown_requested')
			ORDER BY retention_expires_at, artifact_id
			FOR UPDATE SKIP LOCKED
			LIMIT $1
		)
		UPDATE provenance_artifacts artifact
		SET takedown_status='removed',
		    internal_eval_allowed=FALSE,
		    private_training_allowed=FALSE,
		    public_release_allowed=FALSE
		FROM expired WHERE artifact.artifact_id=expired.artifact_id`, limit)
	if err != nil {
		return 0, fmt.Errorf("expiring retained provenance artifacts: %w", err)
	}
	return tag.RowsAffected(), nil
}

func validateWorkflowArtifactProvenance(record WorkflowArtifactProvenance) error {
	for name, value := range map[string]string{
		"artifact_type":   record.ArtifactType,
		"source_type":     record.SourceType,
		"source_uri":      record.SourceURI,
		"source_revision": record.SourceRevision,
		"creator":         record.Creator,
		"provider":        record.Provider,
		"model":           record.Model,
		"model_revision":  record.ModelRevision,
		"workflow_id":     record.WorkflowID,
		"artifact_role":   record.ArtifactRole,
		"retention_class": record.RetentionClass,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("workflow artifact provenance %s is required", name)
		}
	}
	if !isLowerHexSHA256(record.ContentHash) {
		return fmt.Errorf("workflow artifact provenance content hash is invalid")
	}
	if record.GeneratedAt.IsZero() {
		return fmt.Errorf("workflow artifact provenance generated_at is required")
	}
	if len(record.Metadata) > 0 && !json.Valid(record.Metadata) {
		return fmt.Errorf("workflow artifact provenance metadata is invalid JSON")
	}
	return nil
}
