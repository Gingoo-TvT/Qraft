package handler

import "github.com/Gingoo-TvT/Qraft/backend/internal/domain"

const integrationReleaseVersion = "2.2.0"

type setOperationCapability struct {
	Enabled bool     `json:"enabled"`
	Routes  []string `json:"routes"`
}
type setGenerationCapability struct {
	setOperationCapability
	FlagName              string `json:"flag_name"`
	Asynchronous          bool   `json:"asynchronous"`
	RetainsCompletedItems bool   `json:"retains_completed_items"`
}
type setAssemblyCapability struct {
	setOperationCapability
	RequiresModel bool `json:"requires_model"`
}
type setExportCapability struct {
	setOperationCapability
	FlagName       string `json:"flag_name"`
	Format         string `json:"format"`
	ScoresIncluded bool   `json:"scores_included"`
}
type problemSetCapability struct {
	setOperationCapability
	SupportedTypes []domain.QuizType       `json:"supported_types"`
	Modes          []string                `json:"modes"`
	MinItemCount   int                     `json:"min_item_count"`
	MaxItemCount   int                     `json:"max_item_count"`
	Generation     setGenerationCapability `json:"generation"`
	Assembly       setAssemblyCapability   `json:"assembly"`
	Export         setExportCapability     `json:"export"`
}

func problemSetCapabilities(generationEnabled, exportEnabled bool, generationFlag, exportFlag string) problemSetCapability {
	c := problemSetCapability{
		setOperationCapability: setOperationCapability{Enabled: true, Routes: []string{
			"POST /api/v1/problem-sets", "GET /api/v1/problem-sets",
			"GET /api/v1/problem-sets/:id", "PUT /api/v1/problem-sets/:id", "DELETE /api/v1/problem-sets/:id",
			"POST /api/v1/problem-sets/:id/items", "DELETE /api/v1/problem-sets/:id/items/:item_id",
			"PUT /api/v1/problem-sets/:id/items/reorder", "GET /api/v1/problem-sets/:id/quality",
			"POST /api/v1/problem-sets/:id/generate-prompt",
		}},
		SupportedTypes: []domain.QuizType{domain.QuizTypeProgramming, domain.QuizTypeChoice, domain.QuizTypeFillBlank, domain.QuizTypeJudge},
		Modes:          []string{"programming", "mixed"}, MinItemCount: 1, MaxItemCount: domain.MaxProblemSetItemCount,
		Generation: setGenerationCapability{
			setOperationCapability: setOperationCapability{Enabled: generationEnabled, Routes: []string{}},
			FlagName:               generationFlag, Asynchronous: true, RetainsCompletedItems: true,
		},
		Assembly: setAssemblyCapability{setOperationCapability: setOperationCapability{Enabled: true, Routes: []string{
			"POST /api/v1/problem-sets/assembly-preview", "POST /api/v1/problem-sets/assemble",
		}}, RequiresModel: false},
		Export: setExportCapability{
			setOperationCapability: setOperationCapability{Enabled: exportEnabled, Routes: []string{}},
			FlagName:               exportFlag, Format: "algoforge.problem-set.v1", ScoresIncluded: true,
		},
	}
	if generationEnabled {
		c.Generation.Routes = []string{"POST /api/v1/problem-sets/:id/generation", "GET /api/v1/problem-sets/:id/generation", "POST /api/v1/problem-sets/:id/generation/cancel"}
	}
	if exportEnabled {
		c.Export.Routes = []string{"GET /api/v1/problem-sets/:id/export.zip"}
	}
	return c
}
