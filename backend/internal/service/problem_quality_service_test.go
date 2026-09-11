package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/qualityapi"
	"github.com/google/uuid"
)

type missingProblemQualitySource struct{}

func (missingProblemQualitySource) GetProblem(context.Context, uuid.UUID) (*domain.Problem, error) {
	return nil, ErrNotFound
}

func (missingProblemQualitySource) GetTestCases(context.Context, uuid.UUID) ([]*domain.TestCase, error) {
	return nil, ErrNotFound
}

func TestProblemQualityServiceCanonicalReportAuditAndPublicationLayer(t *testing.T) {
	fixture := qg15AS3ExportFixture(t)
	fixture.request.Problem.Difficulty = 1500
	fixture.request.Problem.Tags = []string{"Graphs", "dp"}
	source := &qg15AHydroProblemSource{problem: fixture.request.Problem, testCases: fixture.request.TestCases}
	quality := NewProblemQualityService(source, fixture.reader)

	first, err := quality.GetProblemQuality(context.Background(), fixture.request.Problem.ID)
	if err != nil {
		t.Fatalf("first quality report: %v", err)
	}
	second, err := quality.GetProblemQuality(context.Background(), fixture.request.Problem.ID)
	if err != nil {
		t.Fatalf("second quality report: %v", err)
	}
	if !bytes.Equal(first.Bytes, second.Bytes) || first.SHA256 != second.SHA256 || first.SHA256 != qualityapi.SHA256Hex(first.Bytes) {
		t.Fatal("identical persisted evidence did not produce identical report bytes/hash")
	}
	report := first.Report
	if report.SchemaVersion != qualityapi.ProblemQualityReportSchemaV1 || len(report.Gates) != 9 ||
		report.Layers[0].Name != qualityapi.LayerStandard || report.Layers[1].Name != qualityapi.LayerVerified ||
		report.Layers[2].Status != qualityapi.LayerStatusPendingExplicitApproval {
		t.Fatalf("quality report layers/gates = %+v", report)
	}
	if report.ConceptSignature.SHA256 == "" || strings.Join(report.ConceptSignature.Values, ",") != "dp,graphs" ||
		report.StructuralSignature.SemanticSpecSHA256 != fixture.manifest.SemanticSpecSHA256 {
		t.Fatalf("quality signatures = concept:%+v structural:%+v", report.ConceptSignature, report.StructuralSignature)
	}
	if report.Difficulty.GenerationTarget != 1500 || report.Difficulty.Calibrated || report.Cost.Status != qualityapi.CostStatusUnavailable || report.ExternalOJ.ImportVerified {
		t.Fatalf("advisory/boundary fields = difficulty:%+v cost:%+v external:%+v", report.Difficulty, report.Cost, report.ExternalOJ)
	}

	audit, err := quality.GetProblemQualityAudit(context.Background(), fixture.request.Problem.ID)
	if err != nil {
		t.Fatalf("quality audit: %v", err)
	}
	wantAudit := fixture.reader.data[fixture.evidence.AuditArtifact.Key]
	if !bytes.Equal(audit.Bytes, wantAudit) || audit.SHA256 != fixture.evidence.AuditArtifact.SHA256 || audit.SHA256 != qualityapi.SHA256Hex(audit.Bytes) {
		t.Fatal("audit endpoint document is not the exact CAS-bound canonical bytes")
	}

	fixture.request.Problem.Status = domain.ProblemStatusPublished
	published, err := quality.GetProblemQuality(context.Background(), fixture.request.Problem.ID)
	if err != nil {
		t.Fatalf("published quality report: %v", err)
	}
	if published.Report.Layers[2].Status != qualityapi.LayerStatusReady {
		t.Fatalf("published publication_ready layer = %+v", published.Report.Layers[2])
	}
}

func TestProblemQualityServiceBatchManifestDeterministicAndBound(t *testing.T) {
	fixture := qg15AS3ExportFixture(t)
	fixture.request.Problem.Difficulty = 1500
	fixture.request.Problem.Tags = []string{"graphs", "dp"}
	source := &qg15AHydroProblemSource{problem: fixture.request.Problem, testCases: fixture.request.TestCases}
	quality := NewProblemQualityService(source, fixture.reader)
	request := qualityapi.ProblemQualityBatchRequestV1{
		SchemaVersion: qualityapi.ProblemQualityBatchRequestSchemaV1,
		ProblemIDs:    []string{fixture.request.Problem.ID.String()},
	}
	first, err := quality.BuildProblemQualityBatchManifest(context.Background(), request)
	if err != nil {
		t.Fatalf("first batch manifest: %v", err)
	}
	second, err := quality.BuildProblemQualityBatchManifest(context.Background(), request)
	if err != nil {
		t.Fatalf("second batch manifest: %v", err)
	}
	if !bytes.Equal(first.Bytes, second.Bytes) || first.SHA256 != second.SHA256 || len(first.Manifest.Items) != 1 {
		t.Fatal("quality batch manifest is not deterministic")
	}
	report, err := quality.GetProblemQuality(context.Background(), fixture.request.Problem.ID)
	if err != nil {
		t.Fatal(err)
	}
	item := first.Manifest.Items[0]
	if item.QualityReportSHA256 != report.SHA256 || item.AuditSHA256 != report.Report.Evidence.Audit.SHA256 ||
		item.TestManifestSHA256 != report.Report.Evidence.TestManifest.SHA256 || first.Manifest.ExternalOJImportVerified {
		t.Fatalf("batch item evidence binding = %+v", item)
	}
}

func TestProblemQualityServiceRejectsTamperedMissingLegacyAndBrokenProjection(t *testing.T) {
	t.Run("tampered audit bytes", func(t *testing.T) {
		fixture := qg15AS3ExportFixture(t)
		fixture.request.Problem.Difficulty = 1500
		fixture.request.Problem.Tags = []string{"dp"}
		fixture.reader.data[fixture.evidence.AuditArtifact.Key] = []byte(`{}`)
		quality := NewProblemQualityService(&qg15AHydroProblemSource{problem: fixture.request.Problem, testCases: fixture.request.TestCases}, fixture.reader)
		if _, err := quality.GetProblemQuality(context.Background(), fixture.request.Problem.ID); err == nil || !errors.Is(err, ErrConflict) {
			t.Fatalf("tampered audit error = %v", err)
		}
	})

	t.Run("legacy metadata", func(t *testing.T) {
		fixture := qg15AS3ExportFixture(t)
		fixture.request.Problem.Difficulty = 1500
		fixture.request.Problem.Tags = []string{"dp"}
		fixture.request.Problem.MetadataJSON = json.RawMessage(`{"publication_gate_status":"published"}`)
		quality := NewProblemQualityService(&qg15AHydroProblemSource{problem: fixture.request.Problem, testCases: fixture.request.TestCases}, fixture.reader)
		if _, err := quality.GetProblemQuality(context.Background(), fixture.request.Problem.ID); err == nil || !errors.Is(err, ErrConflict) {
			t.Fatalf("legacy metadata error = %v", err)
		}
	})

	t.Run("missing problem", func(t *testing.T) {
		quality := NewProblemQualityService(missingProblemQualitySource{}, &qg15AHydroCountingReader{data: map[string][]byte{}, calls: map[string]int{}})
		if _, err := quality.GetProblemQuality(context.Background(), uuid.New()); !errors.Is(err, ErrNotFound) {
			t.Fatalf("missing problem error = %v", err)
		}
	})

	t.Run("fact manifest hash drift", func(t *testing.T) {
		fixture := qg15AS3ExportFixture(t)
		fixture.request.Problem.Difficulty = 1500
		fixture.request.Problem.Tags = []string{"dp"}
		binding, err := loadS3ExportBindingV1(context.Background(), fixture.request.Problem, fixture.request.TestCases, fixture.reader)
		if err != nil {
			t.Fatal(err)
		}
		binding.StatementDraftV1.FactManifestSHA256 = strings.Repeat("f", 64)
		if _, err := buildProblemQualityReportV1(fixture.request.Problem, binding); err == nil {
			t.Fatal("fact manifest drift was accepted")
		}
	})

	t.Run("persisted tags drift from authoring CAS", func(t *testing.T) {
		fixture := qg15AS3ExportFixture(t)
		fixture.request.Problem.Tags = []string{"greedy"}
		quality := NewProblemQualityService(&qg15AHydroProblemSource{problem: fixture.request.Problem, testCases: fixture.request.TestCases}, fixture.reader)
		if _, err := quality.GetProblemQuality(context.Background(), fixture.request.Problem.ID); err == nil || !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "tags drifted") {
			t.Fatalf("persisted tag drift error = %v", err)
		}
	})

	t.Run("persisted difficulty drifts from authoring CAS", func(t *testing.T) {
		fixture := qg15AS3ExportFixture(t)
		fixture.request.Problem.Difficulty = 1600
		quality := NewProblemQualityService(&qg15AHydroProblemSource{problem: fixture.request.Problem, testCases: fixture.request.TestCases}, fixture.reader)
		if _, err := quality.GetProblemQuality(context.Background(), fixture.request.Problem.ID); err == nil || !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "generation target drifted") {
			t.Fatalf("persisted difficulty drift error = %v", err)
		}
	})
}
