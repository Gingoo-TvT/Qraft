package workflow

import (
	"context"
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/generationapi"
	"github.com/Gingoo-TvT/Qraft/backend/internal/workflow/activities"
	"github.com/google/uuid"
	"go.temporal.io/sdk/activity"
)

func TestProblemGenerationQualityWorkflowV1ProductJobStoresOnlyAfterNineGatePass(t *testing.T) {
	for index, level := range []string{generationapi.EvidenceMinimal, generationapi.EvidenceStandard, generationapi.EvidenceAudit} {
		t.Run(level, func(t *testing.T) {
			h := newFixed20Harness(t, 71+index, fixed20Scenario{})
			h.input.SubjectID = generationapi.JobIDPrefix + strings.Repeat(string(rune('7'+index)), 64)
			h.input.Params.MetadataExtras = map[string]interface{}{generationapi.QualityEvidenceLevelMetadataKey: level}
			storeCalls := 0
			h.env.RegisterActivityWithOptions(
				func(_ context.Context, input activities.StoreS3QualityDraftInputV1) (*activities.StoreS3QualityDraftResultV1, error) {
					storeCalls++
					if input.WorkflowID != h.input.SubjectID || input.EvidenceLevel != level || input.SubjectRevision == "" ||
						input.AuditArtifact.SHA256 == "" || input.TestManifestArtifact.SHA256 == "" ||
						input.FinalStatementArtifact.SHA256 == "" || input.MainProgramArtifact.SHA256 == "" {
						t.Fatalf("incomplete quality draft store input: %+v", input)
					}
					return &activities.StoreS3QualityDraftResultV1{
						PayloadVersion:     activities.StoreS3QualityDraftPayloadVersionV1,
						ProblemID:          uuid.NewSHA1(uuid.NameSpaceURL, []byte(input.WorkflowID)).String(),
						Status:             domain.ProblemStatusDraft,
						AuditSHA256:        input.AuditArtifact.SHA256,
						TestManifestSHA256: input.TestManifestArtifact.SHA256,
					}, nil
				},
				activity.RegisterOptions{Name: "StoreS3QualityDraftActivityV1"},
			)

			result, err := h.execute(t)
			if err != nil {
				t.Fatal(err)
			}
			if storeCalls != 1 || result.StoredProblemID == "" || result.StoredProblemStatus != domain.ProblemStatusDraft {
				t.Fatalf("quality job materialization = calls:%d result:%+v", storeCalls, result)
			}
			if result.Audit == nil || len(result.Audit.GateResults) != 9 {
				t.Fatalf("stored result has no nine-gate audit: %+v", result)
			}
			queryValue, err := h.env.QueryWorkflow(domain.WorkflowStateQueryName)
			if err != nil {
				t.Fatalf("query completed quality workflow: %v", err)
			}
			var query domain.WorkflowStateQuery
			if err := queryValue.Get(&query); err != nil {
				t.Fatal(err)
			}
			if query.State.Status != domain.WorkflowStatusCompleted || query.State.CurrentStep != domain.StepStore || query.State.Progress != 100 {
				t.Fatalf("quality workflow query = %+v", query.State)
			}
		})
	}
}

func TestProblemGenerationQualityWorkflowV1ProductJobQualityFailureDoesNotStore(t *testing.T) {
	h := newFixed20Harness(t, 72, fixed20Scenario{whitespaceMismatch: true})
	h.input.SubjectID = generationapi.JobIDPrefix + strings.Repeat("8", 64)
	storeCalls := 0
	h.env.RegisterActivityWithOptions(
		func(context.Context, activities.StoreS3QualityDraftInputV1) (*activities.StoreS3QualityDraftResultV1, error) {
			storeCalls++
			return nil, nil
		},
		activity.RegisterOptions{Name: "StoreS3QualityDraftActivityV1"},
	)

	_, err := h.execute(t)
	if err == nil || !strings.Contains(err.Error(), "QualityNotMet") {
		t.Fatalf("quality failure error = %v", err)
	}
	if storeCalls != 0 {
		t.Fatalf("quality failure called Store %d times", storeCalls)
	}
}
