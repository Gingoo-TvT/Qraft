//go:build integration

package repository_test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
)

func TestRecordWorkflowArtifactPersistsAuditEvent(t *testing.T) {
	if os.Getenv("ALGOFORGE_INTEGRATION_DISPOSABLE_DB") != "true" {
		t.Skip("set ALGOFORGE_INTEGRATION_DISPOSABLE_DB=true with a disposable migrated DATABASE_URL")
	}
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		t.Fatal("DATABASE_URL is required for provenance artifact integration tests")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect database: %v", err)
	}
	defer pool.Close()

	repo := repository.NewProvenanceArtifactRepository(pool)
	workflowID := "integration-provenance-" + uuid.NewString()
	role := "normalized_llm_response:integration:" + uuid.NewString()
	record := repository.WorkflowArtifactProvenance{
		ArtifactType:   "normalized_llm_response",
		SourceType:     "model_provider_response",
		ContentHash:    strings.Repeat("b", 64),
		SourceURI:      "minio://integration/" + uuid.NewString(),
		SourceRevision: "artifact-ref/v1;payload/v1",
		Creator:        "GenerateStatementActivity",
		Provider:       "integration-provider",
		Model:          "integration-model",
		ModelRevision:  "integration-revision",
		GeneratedAt:    time.Now().UTC(),
		WorkflowID:     workflowID,
		ArtifactRole:   role,
		RetentionClass: "provider_response_unreviewed",
		Metadata:       json.RawMessage(`{"request_sha256":"integration"}`),
	}

	artifactID, err := repo.RecordWorkflowArtifact(ctx, record)
	if err != nil {
		t.Fatalf("record workflow artifact: %v", err)
	}
	replayedID, err := repo.RecordWorkflowArtifact(ctx, record)
	if err != nil {
		t.Fatalf("record workflow artifact replay: %v", err)
	}
	if replayedID != artifactID {
		t.Fatalf("replayed artifact id = %s, want %s", replayedID, artifactID)
	}

	var storedWorkflowID, storedRole string
	if err := pool.QueryRow(ctx, `
		SELECT details->>'workflow_id', details->>'artifact_role'
		FROM provenance_audit_events
		WHERE artifact_id=$1 AND event_type='workflow_artifact_recorded'
		ORDER BY occurred_at DESC, event_id DESC
		LIMIT 1`, artifactID).Scan(&storedWorkflowID, &storedRole); err != nil {
		t.Fatalf("select audit event: %v", err)
	}
	if storedWorkflowID != workflowID || storedRole != role {
		t.Fatalf("audit details = (%q, %q), want (%q, %q)", storedWorkflowID, storedRole, workflowID, role)
	}

	replacement := record
	replacement.ContentHash = strings.Repeat("c", 64)
	replacement.SourceURI = "minio://integration/" + uuid.NewString()
	replacementID, err := repo.RecordWorkflowArtifact(ctx, replacement)
	if err != nil {
		t.Fatalf("replace workflow artifact binding: %v", err)
	}
	if replacementID == artifactID {
		t.Fatal("replacement artifact reused the original artifact ID")
	}
	var currentID uuid.UUID
	var revision int64
	if err := pool.QueryRow(ctx, `
		SELECT artifact_id, binding_revision
		FROM provenance_artifact_bindings
		WHERE subject_type='workflow_execution' AND subject_id=$1
		  AND artifact_role=$2 AND is_current`, workflowID, role).Scan(&currentID, &revision); err != nil {
		t.Fatalf("select replacement binding: %v", err)
	}
	if currentID != replacementID || revision != 2 {
		t.Fatalf("current binding=(%s,%d), want=(%s,2)", currentID, revision, replacementID)
	}
	var ancestryCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM provenance_artifact_ancestry
		WHERE child_artifact_id=$1 AND parent_artifact_id=$2 AND relationship='supersedes'`,
		replacementID, artifactID).Scan(&ancestryCount); err != nil {
		t.Fatalf("select replacement ancestry: %v", err)
	}
	if ancestryCount != 1 {
		t.Fatalf("replacement ancestry count=%d, want=1", ancestryCount)
	}
}
