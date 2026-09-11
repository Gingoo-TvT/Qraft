package handler

import (
	"encoding/json"
	"github.com/Gingoo-TvT/Qraft/backend/internal/service"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/diversitymode"
	"github.com/Gingoo-TvT/Qraft/backend/internal/exportmode"
	"github.com/Gingoo-TvT/Qraft/backend/internal/generationapi"
	"github.com/Gingoo-TvT/Qraft/backend/internal/qualitymode"
	"github.com/labstack/echo/v4"
)

func TestIntegrationCapabilitiesDefaultAndRollbackModes(t *testing.T) {
	tests := []struct {
		name         string
		generation   string
		exports      string
		quality      string
		diversity    string
		wantJobs     bool
		wantBatches  bool
		wantQuality  bool
		wantEvidence []string
	}{
		{
			name: "all v1", generation: generationapi.ModeJobsV1,
			exports: exportmode.ModeQG15V1, quality: qualitymode.ModeQualityV1, diversity: diversitymode.ModeDiversityV1,
			wantJobs: true, wantBatches: true, wantQuality: true,
			wantEvidence: []string{generationapi.EvidenceMinimal, generationapi.EvidenceStandard, generationapi.EvidenceAudit},
		},
		{
			name: "independent export rollback", generation: generationapi.ModeJobsV1,
			exports: exportmode.ModeLegacyOnly, quality: qualitymode.ModeQualityV1, diversity: diversitymode.ModeDiversityV1,
			wantJobs: true, wantBatches: true, wantQuality: true,
			wantEvidence: []string{generationapi.EvidenceMinimal, generationapi.EvidenceStandard, generationapi.EvidenceAudit},
		},
		{
			name: "independent quality rollback", generation: generationapi.ModeJobsV1,
			exports: exportmode.ModeQG15V1, quality: qualitymode.ModeLegacyOnly, diversity: diversitymode.ModeDiversityV1,
			wantJobs: true, wantBatches: true,
			wantEvidence: []string{generationapi.EvidenceMinimal},
		},
		{
			name: "independent diversity rollback", generation: generationapi.ModeJobsV1,
			exports: exportmode.ModeQG15V1, quality: qualitymode.ModeQualityV1, diversity: diversitymode.ModeLegacyOnly,
			wantJobs: true, wantQuality: true,
			wantEvidence: []string{generationapi.EvidenceMinimal, generationapi.EvidenceStandard, generationapi.EvidenceAudit},
		},
		{
			name: "all rollback", generation: generationapi.ModeLegacyOnly,
			exports: exportmode.ModeLegacyOnly, quality: qualitymode.ModeLegacyOnly, diversity: diversitymode.ModeLegacyOnly,
			wantEvidence: []string{},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			generationAudit, err := generationapi.ResolveMode(test.generation, true, nil)
			if err != nil {
				t.Fatal(err)
			}
			exportAudit, err := exportmode.ResolveMode(test.exports, true, nil)
			if err != nil {
				t.Fatal(err)
			}
			qualityAudit, err := qualitymode.ResolveMode(test.quality, true, nil)
			if err != nil {
				t.Fatal(err)
			}
			diversityAudit, err := diversitymode.ResolveMode(test.diversity, true, nil)
			if err != nil {
				t.Fatal(err)
			}
			handler, err := NewIntegrationCapabilitiesHandler(generationAudit, exportAudit, qualityAudit, diversityAudit)
			if err != nil {
				t.Fatal(err)
			}

			e := echo.New()
			request := httptest.NewRequest(http.MethodGet, "/api/v1/integration/capabilities", nil)
			first := httptest.NewRecorder()
			if err := handler.HandleGet(e.NewContext(request, first)); err != nil {
				t.Fatal(err)
			}
			second := httptest.NewRecorder()
			if err := handler.HandleGet(e.NewContext(request, second)); err != nil {
				t.Fatal(err)
			}
			if first.Code != http.StatusOK || first.Body.String() != second.Body.String() {
				t.Fatalf("response is not stable: %d %q / %d %q", first.Code, first.Body.String(), second.Code, second.Body.String())
			}
			if first.Header().Get(echo.HeaderCacheControl) != "no-store" {
				t.Fatalf("cache control = %q", first.Header().Get(echo.HeaderCacheControl))
			}

			var response integrationCapabilitiesResponse
			if err := json.Unmarshal(first.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if response.SchemaVersion != integrationCapabilitiesSchemaV1 ||
				response.GenerationJobs.Enabled != test.wantJobs ||
				response.GenerationBatches.Enabled != test.wantBatches ||
				!response.Exports.PortableSetsEnabled ||
				response.QualityLayer.Enabled != test.wantQuality ||
				!reflect.DeepEqual(response.EvidenceLevels, test.wantEvidence) {
				t.Fatalf("response = %+v", response)
			}
			if response.GenerationBatches.Mode != test.diversity ||
				response.GenerationBatches.FlagName != diversitymode.ModeEnv ||
				response.GenerationBatches.RollbackMode != diversitymode.ModeLegacyOnly ||
				!response.GenerationBatches.RollbackRequiresRestart {
				t.Fatalf("diversity capability audit = %+v", response.GenerationBatches)
			}
			if test.wantBatches && len(response.GenerationBatches.Routes) != 4 {
				t.Fatalf("enabled diversity routes = %#v", response.GenerationBatches.Routes)
			}
			if !test.wantBatches && len(response.GenerationBatches.Routes) != 0 {
				t.Fatalf("rollback diversity routes = %#v", response.GenerationBatches.Routes)
			}
			if response.HydroPhase2 {
				t.Fatalf("external boundary drifted: %+v", response)
			}
		})
	}
}

func TestIntegrationCapabilitiesRejectsForgedAudits(t *testing.T) {
	generationAudit, _ := generationapi.ResolveMode(generationapi.ModeJobsV1, true, nil)
	exportAudit, _ := exportmode.ResolveMode(exportmode.ModeQG15V1, true, nil)
	qualityAudit, _ := qualitymode.ResolveMode(qualitymode.ModeQualityV1, true, nil)
	diversityAudit, _ := diversitymode.ResolveMode(diversitymode.ModeDiversityV1, true, nil)

	badGeneration := generationAudit
	badGeneration.ProductRouteEnabled = false
	if _, err := NewIntegrationCapabilitiesHandler(badGeneration, exportAudit, qualityAudit, diversityAudit); err == nil {
		t.Fatal("forged generation audit accepted")
	}
	badExport := exportAudit
	badExport.QG15ProductRoutes = false
	if _, err := NewIntegrationCapabilitiesHandler(generationAudit, badExport, qualityAudit, diversityAudit); err == nil {
		t.Fatal("forged export audit accepted")
	}
	badQuality := qualityAudit
	badQuality.ExtendedEvidenceLevels = false
	if _, err := NewIntegrationCapabilitiesHandler(generationAudit, exportAudit, badQuality, diversityAudit); err == nil {
		t.Fatal("forged quality audit accepted")
	}
	badDiversity := diversityAudit
	badDiversity.MicroBatchRouteEnabled = false
	if _, err := NewIntegrationCapabilitiesHandler(generationAudit, exportAudit, qualityAudit, badDiversity); err == nil {
		t.Fatal("forged diversity audit accepted")
	}
}

func TestIntegrationCapabilitiesAcceptsEveryIndependentRollbackCombination(t *testing.T) {
	for _, generationMode := range []string{generationapi.ModeJobsV1, generationapi.ModeLegacyOnly} {
		for _, exportMode := range []string{exportmode.ModeQG15V1, exportmode.ModeLegacyOnly} {
			for _, qualityMode := range []string{qualitymode.ModeQualityV1, qualitymode.ModeLegacyOnly} {
				for _, diversityModeValue := range []string{diversitymode.ModeDiversityV1, diversitymode.ModeLegacyOnly} {
					generationAudit, err := generationapi.ResolveMode(generationMode, true, nil)
					if err != nil {
						t.Fatal(err)
					}
					exportAudit, err := exportmode.ResolveMode(exportMode, true, nil)
					if err != nil {
						t.Fatal(err)
					}
					qualityAudit, err := qualitymode.ResolveMode(qualityMode, true, nil)
					if err != nil {
						t.Fatal(err)
					}
					diversityAudit, err := diversitymode.ResolveMode(diversityModeValue, true, nil)
					if err != nil {
						t.Fatal(err)
					}
					handler, err := NewIntegrationCapabilitiesHandler(generationAudit, exportAudit, qualityAudit, diversityAudit)
					if err != nil {
						t.Fatalf("modes %s/%s/%s/%s: %v", generationMode, exportMode, qualityMode, diversityModeValue, err)
					}
					e := echo.New()
					recorder := httptest.NewRecorder()
					request := httptest.NewRequest(http.MethodGet, "/api/v1/integration/capabilities", nil)
					if err := handler.HandleGet(e.NewContext(request, recorder)); err != nil {
						t.Fatal(err)
					}
					var response integrationCapabilitiesResponse
					if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
						t.Fatal(err)
					}
					if response.GenerationJobs.Enabled != (generationMode == generationapi.ModeJobsV1) ||
						!response.Exports.PortableSetsEnabled ||
						response.QualityLayer.Enabled != (qualityMode == qualitymode.ModeQualityV1) ||
						response.GenerationBatches.Enabled != (diversityModeValue == diversitymode.ModeDiversityV1) {
						t.Fatalf("mode independence drifted for %s/%s/%s/%s: %+v", generationMode, exportMode, qualityMode, diversityModeValue, response)
					}
					sets := response.ProblemSets
					if response.ReleaseVersion != "2.1.0" || !sets.Enabled || sets.MinItemCount != 1 || sets.MaxItemCount != 1000 ||
						len(sets.SupportedTypes) != 4 || !sets.Assembly.Enabled || sets.Assembly.RequiresModel || !sets.Export.ScoresIncluded ||
						sets.Generation.Enabled != response.GenerationJobs.Enabled || sets.Export.Enabled != response.Exports.PortableSetsEnabled {
						t.Fatalf("set capability drifted: %+v", response)
					}
					if sets.Generation.Enabled != (len(sets.Generation.Routes) == 3) || sets.Export.Enabled != (len(sets.Export.Routes) == 1) {
						t.Fatalf("disabled set operations must not advertise available routes: %+v", sets)
					}
					setHandler := NewProblemSetHandler(nil)
					if sets.Generation.Enabled {
						setHandler.SetGenerationService(new(service.ProblemSetGenerationService))
					}
					RegisterProblemSetRoutes(e.Group("/api/v1"), setHandler)
					registered := map[string]bool{}
					for _, route := range e.Routes() {
						registered[route.Method+" "+route.Path] = true
					}
					for _, routes := range [][]string{sets.Routes, sets.Generation.Routes, sets.Assembly.Routes, sets.Export.Routes} {
						for _, route := range routes {
							if !registered[route] || !strings.Contains(route, "/api/v1/problem-sets") {
								t.Fatalf("unregistered advertised route: %s", route)
							}
						}
					}
					if response.HydroPhase2 {
						t.Fatalf("external boundary drifted for %s/%s/%s/%s", generationMode, exportMode, qualityMode, diversityModeValue)
					}
				}
			}
		}
	}
}
