package handler

import (
	"fmt"
	"net/http"

	"github.com/Gingoo-TvT/Qraft/backend/internal/diversitymode"
	"github.com/Gingoo-TvT/Qraft/backend/internal/exportmode"
	"github.com/Gingoo-TvT/Qraft/backend/internal/generationapi"
	"github.com/Gingoo-TvT/Qraft/backend/internal/qualitymode"
	"github.com/labstack/echo/v4"
)

const (
	integrationCapabilitiesSchemaV1 = "algoforge.integration-capabilities.v1"
)

type rolloutCapability struct {
	Mode                    string   `json:"mode"`
	Enabled                 bool     `json:"enabled"`
	FlagName                string   `json:"flag_name"`
	RollbackMode            string   `json:"rollback_mode"`
	RollbackRequiresRestart bool     `json:"rollback_requires_restart"`
	Routes                  []string `json:"routes"`
}

type exportCapability struct {
	Mode                    string   `json:"mode"`
	FlagName                string   `json:"flag_name"`
	RollbackMode            string   `json:"rollback_mode"`
	RollbackRequiresRestart bool     `json:"rollback_requires_restart"`
	HydroRoutesEnabled      bool     `json:"hydro_routes_enabled"`
	PortableSetsEnabled     bool     `json:"portable_sets_enabled"`
	Routes                  []string `json:"routes"`
}

type integrationCapabilitiesResponse struct {
	ReleaseVersion    string               `json:"release_version"`
	ProblemSets       problemSetCapability `json:"problem_sets"`
	SchemaVersion     string               `json:"schema_version"`
	GenerationJobs    rolloutCapability    `json:"generation_jobs"`
	GenerationBatches rolloutCapability    `json:"generation_micro_batches"`
	QualityLayer      rolloutCapability    `json:"quality_layer"`
	Exports           exportCapability     `json:"exports"`
	EvidenceLevels    []string             `json:"generation_evidence_levels"`
	HydroPhase2       bool                 `json:"hydro_phase2_enabled"`
}

// IntegrationCapabilitiesHandler exposes the startup-resolved integration
// surface. It reports actual route state and never upgrades local validation
// into a claim that a third-party OJ accepted an import.
type IntegrationCapabilitiesHandler struct {
	response integrationCapabilitiesResponse
}

func NewIntegrationCapabilitiesHandler(
	generation generationapi.ModeAudit,
	exports exportmode.Audit,
	quality qualitymode.Audit,
	diversityMode diversitymode.Audit,
) (*IntegrationCapabilitiesHandler, error) {
	if err := validateGenerationModeAudit(generation); err != nil {
		return nil, err
	}
	if err := validateExportModeAudit(exports); err != nil {
		return nil, err
	}
	if err := qualitymode.ValidateAudit(quality); err != nil {
		return nil, fmt.Errorf("invalid quality mode audit: %w", err)
	}
	if err := diversitymode.ValidateAudit(diversityMode); err != nil {
		return nil, fmt.Errorf("invalid S5 diversity mode audit: %w", err)
	}

	generationRoutes := []string{}
	evidenceLevels := []string{}
	if generation.ProductRouteEnabled {
		generationRoutes = []string{
			"POST /api/v1/generation/jobs",
			"GET /api/v1/generation/jobs/:id",
			"GET /api/v1/generation/jobs/:id/result",
			"GET /api/v1/generation/jobs/:id/events",
			"DELETE /api/v1/generation/jobs/:id",
		}
		evidenceLevels = []string{generationapi.EvidenceMinimal}
		if quality.ExtendedEvidenceLevels {
			evidenceLevels = append(evidenceLevels, generationapi.EvidenceStandard, generationapi.EvidenceAudit)
		}
	}

	qualityRoutes := []string{}
	if quality.ExtendedEvidenceLevels {
		qualityRoutes = []string{
			"GET /api/v1/problems/:id/quality",
			"GET /api/v1/problems/:id/quality/audit",
			"POST /api/v1/problems/quality/batch-manifest",
		}
	}

	diversityRoutes := []string{}
	if diversityMode.MicroBatchRouteEnabled {
		diversityRoutes = []string{
			"POST /api/v1/generation/micro-batches",
			"GET /api/v1/generation/micro-batches/:id",
			"GET /api/v1/generation/micro-batches/:id/result",
			"DELETE /api/v1/generation/micro-batches/:id",
		}
	}

	exportRoutes := []string{
		"GET /api/v1/problems/hydro.zip",
		"POST /api/v1/problems/hydro/validate",
		"GET /api/v1/problems/:id/hydro.zip",
	}
	exportRoutes = append(exportRoutes,
		"GET /api/v1/problem-sets/:id/export.zip",
		"GET /api/v1/quizzes/export",
		"GET /api/v1/quizzes/template.xlsx",
	)

	return &IntegrationCapabilitiesHandler{response: integrationCapabilitiesResponse{
		SchemaVersion:  integrationCapabilitiesSchemaV1,
		ReleaseVersion: integrationReleaseVersion,
		ProblemSets:    problemSetCapabilities(generation.ProductRouteEnabled, true, generation.FlagName, ""),
		GenerationJobs: rolloutCapability{
			Mode: generation.EffectiveMode, Enabled: generation.ProductRouteEnabled,
			FlagName: generation.FlagName, RollbackMode: generationapi.ModeLegacyOnly,
			RollbackRequiresRestart: true, Routes: generationRoutes,
		},
		GenerationBatches: rolloutCapability{
			Mode: diversityMode.EffectiveMode, Enabled: diversityMode.MicroBatchRouteEnabled,
			FlagName: diversityMode.FlagName, RollbackMode: diversitymode.ModeLegacyOnly,
			RollbackRequiresRestart: true, Routes: diversityRoutes,
		},
		QualityLayer: rolloutCapability{
			Mode: quality.EffectiveMode, Enabled: quality.ExtendedEvidenceLevels,
			FlagName: quality.FlagName, RollbackMode: qualitymode.ModeLegacyOnly,
			RollbackRequiresRestart: true, Routes: qualityRoutes,
		},
		Exports: exportCapability{
			Mode: "hydro-and-algoforge-v1", FlagName: "",
			RollbackMode: "", RollbackRequiresRestart: false,
			HydroRoutesEnabled: true, PortableSetsEnabled: true,
			Routes: exportRoutes,
		},
		EvidenceLevels: evidenceLevels,
		HydroPhase2:    false,
	}}, nil
}

func validateGenerationModeAudit(audit generationapi.ModeAudit) error {
	if audit.FlagName != generationapi.ProductAPIModeEnv || audit.LegacyBehavior != "unchanged" {
		return fmt.Errorf("invalid generation mode audit")
	}
	switch audit.EffectiveMode {
	case generationapi.ModeJobsV1:
		if !audit.ProductRouteEnabled || audit.ContractPreview {
			return fmt.Errorf("invalid jobs-v1 generation mode audit")
		}
	case generationapi.ModeContractPreview:
		if audit.ProductRouteEnabled || !audit.ContractPreview {
			return fmt.Errorf("invalid contract-preview generation mode audit")
		}
	case generationapi.ModeLegacyOnly:
		if audit.ProductRouteEnabled || audit.ContractPreview {
			return fmt.Errorf("invalid legacy generation mode audit")
		}
	default:
		return fmt.Errorf("invalid generation mode %q", audit.EffectiveMode)
	}
	return nil
}

func validateExportModeAudit(audit exportmode.Audit) error {
	if audit.FlagName != exportmode.QG15ExportModeEnv {
		return fmt.Errorf("invalid export mode audit")
	}
	switch audit.EffectiveMode {
	case exportmode.ModeQG15V1:
		if !audit.QG15ProductRoutes || !audit.HydroS3BindingEnabled {
			return fmt.Errorf("invalid qg15-v1 export mode audit")
		}
	case exportmode.ModeLegacyOnly:
		if audit.QG15ProductRoutes || audit.HydroS3BindingEnabled {
			return fmt.Errorf("invalid legacy export mode audit")
		}
	default:
		return fmt.Errorf("invalid export mode %q", audit.EffectiveMode)
	}
	return nil
}

func (handler *IntegrationCapabilitiesHandler) HandleGet(c echo.Context) error {
	c.Response().Header().Set(echo.HeaderCacheControl, "no-store")
	return c.JSON(http.StatusOK, handler.response)
}
