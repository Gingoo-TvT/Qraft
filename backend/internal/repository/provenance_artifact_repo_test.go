package repository

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestWorkflowArtifactProvenanceValidation(t *testing.T) {
	valid := WorkflowArtifactProvenance{
		ArtifactType:   "normalized_llm_response",
		SourceType:     "model_provider_response",
		ContentHash:    strings.Repeat("a", 64),
		SourceURI:      "minio://bucket/key",
		SourceRevision: "artifact-ref/v1;payload/v1",
		Creator:        "GenerateStatementActivity",
		Provider:       "anthropic-compatible",
		Model:          "model",
		ModelRevision:  "model-revision",
		GeneratedAt:    time.Now(),
		WorkflowID:     "workflow-id",
		ArtifactRole:   "normalized_llm_response:activity:attempt:1",
		RetentionClass: "provider_response_unreviewed",
		Metadata:       json.RawMessage(`{"request_sha256":"fixture"}`),
	}
	if err := validateWorkflowArtifactProvenance(valid); err != nil {
		t.Fatalf("valid provenance rejected: %v", err)
	}
	invalid := valid
	invalid.WorkflowID = ""
	if err := validateWorkflowArtifactProvenance(invalid); err == nil {
		t.Fatal("missing workflow ID was accepted")
	}
	invalid = valid
	invalid.Metadata = json.RawMessage(`{`)
	if err := validateWorkflowArtifactProvenance(invalid); err == nil {
		t.Fatal("invalid metadata JSON was accepted")
	}
}
