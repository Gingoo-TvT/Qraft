package service

import (
	"context"
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/google/uuid"
)

func TestHydroExportServiceQG15EnabledAndLegacyRollback(t *testing.T) {
	t.Run("qg15 accepts S3-bound trusted draft", func(t *testing.T) {
		fixture := qg15AS3ExportFixture(t)
		service := &HydroExportService{
			problems: &qg15AHydroProblemSource{problem: fixture.request.Problem, testCases: fixture.request.TestCases},
			objects:  fixture.reader,
		}
		if _, err := service.BuildProblemPackage(context.Background(), fixture.request.Problem.ID); err != nil {
			t.Fatalf("QG15 problem export: %v", err)
		}
	})

	t.Run("legacy rejects draft and accepts published without S3 binding", func(t *testing.T) {
		fixture := qg15AS3ExportFixture(t)
		service := &HydroExportService{
			problems: &qg15AHydroProblemSource{problem: fixture.request.Problem, testCases: fixture.request.TestCases},
			objects:  fixture.reader,
		}
		service.SetQG15ExportEnabled(false)
		if _, err := service.BuildProblemPackage(context.Background(), fixture.request.Problem.ID); err == nil || !strings.Contains(err.Error(), "requires published status") {
			t.Fatalf("legacy draft error=%v", err)
		}

		legacy, _, reader := qg15AHydroManifestFixture(t)
		legacy.Problem.Status = domain.ProblemStatusPublished
		legacy.Problem.MetadataJSON = nil
		service = &HydroExportService{
			problems: &qg15AHydroProblemSource{problem: legacy.Problem, testCases: legacy.TestCases},
			objects:  reader,
		}
		service.SetQG15ExportEnabled(false)
		if _, err := service.BuildProblemPackage(context.Background(), legacy.Problem.ID); err != nil {
			t.Fatalf("legacy problem export: %v", err)
		}
		if _, err := service.BuildBatchPackage(context.Background(), []uuid.UUID{legacy.Problem.ID}); err != nil {
			t.Fatalf("legacy batch export: %v", err)
		}
	})
}
