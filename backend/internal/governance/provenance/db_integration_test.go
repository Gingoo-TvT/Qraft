//go:build integration

package provenance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func integrationPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		t.Fatal("DATABASE_URL is required for provenance integration tests")
	}
	pool, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		t.Fatalf("create database pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func createEligibleArtifact(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	id := uuid.New()
	termsDigest := sha256.Sum256([]byte("terms:" + id.String()))
	termsHash := hex.EncodeToString(termsDigest[:])
	contentDigest := sha256.Sum256([]byte("content:" + id.String()))
	contentHash := hex.EncodeToString(contentDigest[:])

	_, err := pool.Exec(ctx, `
		INSERT INTO provenance_terms_snapshots (
			snapshot_hash, source, snapshot_uri, captured_at, reviewer,
			review_status, metadata
		) VALUES ($1, 'integration_fixture', $2, NOW(), 'pending', 'unreviewed', '{}');
		`, termsHash, "algoforge://terms/integration/"+id.String())
	if err != nil {
		t.Fatalf("create terms snapshot: %v", err)
	}
	_, err = pool.Exec(ctx, `UPDATE provenance_terms_snapshots
		SET review_status = 'approved', reviewer = 'integration-reviewer'
		WHERE snapshot_hash = $1`, termsHash)
	if err != nil {
		t.Fatalf("approve terms snapshot: %v", err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO provenance_artifacts (
			artifact_id, artifact_type, content_hash, source_type, source_uri,
			source_revision, creator, provider, model, model_revision,
			generated_at, license_basis, terms_snapshot_hash, retention_class,
			takedown_status, policy_version, internal_eval_allowed,
			private_training_allowed, public_release_allowed, metadata
		) VALUES (
			$1, 'integration_fixture', $2, 'integration_fixture', $3,
			'v1', 'integration-test', 'internal', 'not_applicable',
			'not_applicable', NOW(), 'project_owned', $4, 'integration_fixture',
			'active', '2026-07-13.v1', TRUE, TRUE, TRUE,
			'{"ownership_record":"integration-fixture"}'::jsonb
		)`, id, contentHash, "algoforge://artifact/integration/"+id.String(), termsHash)
	if err != nil {
		t.Fatalf("create eligible artifact: %v", err)
	}
	return id
}

func TestManifestIssueSerializesConcurrentItemInsert(t *testing.T) {
	pool := integrationPool(t)
	ctx := context.Background()
	first := createEligibleArtifact(t, pool)
	late := createEligibleArtifact(t, pool)
	manifestID := uuid.New()

	_, err := pool.Exec(ctx, `
		INSERT INTO provenance_manifests (manifest_id, policy_version, purpose)
		VALUES ($1, '2026-07-13.v1', 'public_release')`, manifestID)
	if err != nil {
		t.Fatalf("create manifest: %v", err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO provenance_manifest_artifacts (manifest_id, artifact_id, content_hash)
		SELECT $1, artifact_id, content_hash
		FROM provenance_artifacts WHERE artifact_id = $2`, manifestID, first)
	if err != nil {
		t.Fatalf("prepare manifest: %v", err)
	}

	issueTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer issueTx.Rollback(ctx)
	if _, err := issueTx.Exec(ctx, `SELECT issue_provenance_manifest($1)`, manifestID); err != nil {
		t.Fatalf("issue manifest: %v", err)
	}

	insertResult := make(chan error, 1)
	go func() {
		_, err := pool.Exec(context.Background(), `
			INSERT INTO provenance_manifest_artifacts (manifest_id, artifact_id, content_hash)
			SELECT $1, artifact_id, content_hash
			FROM provenance_artifacts WHERE artifact_id = $2`, manifestID, late)
		insertResult <- err
	}()

	select {
	case err := <-insertResult:
		t.Fatalf("concurrent insert did not wait for manifest row lock: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	if err := issueTx.Commit(ctx); err != nil {
		t.Fatalf("commit issue: %v", err)
	}

	select {
	case err := <-insertResult:
		if err == nil || !strings.Contains(err.Error(), "draft") {
			t.Fatalf("late item was not rejected after issue: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("late item insert stayed blocked after issue committed")
	}

	var itemCount, distributable int
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT COUNT(*) FROM provenance_manifest_artifacts WHERE manifest_id = $1),
			(SELECT COUNT(*) FROM provenance_distributable_manifests WHERE manifest_id = $1)`,
		manifestID).Scan(&itemCount, &distributable); err != nil {
		t.Fatal(err)
	}
	if itemCount != 1 || distributable != 1 {
		t.Fatalf("manifest changed after concurrent insert: items=%d distributable=%d", itemCount, distributable)
	}
}

func TestAncestryWritesSerializeAndRejectReciprocalCycle(t *testing.T) {
	pool := integrationPool(t)
	ctx := context.Background()
	a := createEligibleArtifact(t, pool)
	b := createEligibleArtifact(t, pool)

	firstTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer firstTx.Rollback(ctx)
	if _, err := firstTx.Exec(ctx, `
		INSERT INTO provenance_artifact_ancestry (
			child_artifact_id, parent_artifact_id, relationship
		) VALUES ($1, $2, 'derived_from')`, a, b); err != nil {
		t.Fatalf("insert first ancestry edge: %v", err)
	}

	cycleResult := make(chan error, 1)
	go func() {
		_, err := pool.Exec(context.Background(), `
			INSERT INTO provenance_artifact_ancestry (
				child_artifact_id, parent_artifact_id, relationship
			) VALUES ($1, $2, 'derived_from')`, b, a)
		cycleResult <- err
	}()

	select {
	case err := <-cycleResult:
		t.Fatalf("reciprocal insert did not wait for ancestry lock: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	if err := firstTx.Commit(ctx); err != nil {
		t.Fatalf("commit first ancestry edge: %v", err)
	}

	select {
	case err := <-cycleResult:
		if err == nil || !strings.Contains(err.Error(), "cycle") {
			t.Fatalf("reciprocal cycle was not rejected: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reciprocal ancestry insert stayed blocked after commit")
	}
}

func TestExpireRetentionBatchRemovesExpiredArtifactIdempotently(t *testing.T) {
	pool := integrationPool(t)
	ctx := context.Background()
	artifactID := createEligibleArtifact(t, pool)
	if _, err := pool.Exec(ctx, `
		UPDATE provenance_artifacts
		SET retention_expires_at=generated_at + INTERVAL '1 microsecond',
		    internal_eval_allowed=FALSE,
		    private_training_allowed=FALSE,
		    public_release_allowed=FALSE
		WHERE artifact_id=$1`, artifactID); err != nil {
		t.Fatalf("make retention fixture overdue: %v", err)
	}

	repo := repository.NewProvenanceArtifactRepository(pool)
	count, err := repo.ExpireRetentionBatch(ctx, 100)
	if err != nil || count < 1 {
		t.Fatalf("expire batch count=%d err=%v", count, err)
	}
	var status string
	var internalEval, privateTraining, publicRelease bool
	if err := pool.QueryRow(ctx, `
		SELECT takedown_status, internal_eval_allowed,
		       private_training_allowed, public_release_allowed
		FROM provenance_artifacts WHERE artifact_id=$1`, artifactID).Scan(
		&status, &internalEval, &privateTraining, &publicRelease,
	); err != nil {
		t.Fatal(err)
	}
	if status != "removed" || internalEval || privateTraining || publicRelease {
		t.Fatalf("expired artifact status=%s permissions=%v/%v/%v", status, internalEval, privateTraining, publicRelease)
	}

	count, err = repo.ExpireRetentionBatch(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	var stillEligible int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM provenance_artifacts
		WHERE artifact_id=$1
		  AND takedown_status NOT IN ('removed','revoked','takedown_requested')`, artifactID).Scan(&stillEligible); err != nil {
		t.Fatal(err)
	}
	if stillEligible != 0 {
		t.Fatalf("expired artifact remained eligible after rerun; second batch count=%d", count)
	}
}
