package activities

import (
	"strings"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/llm"
)

func applyStatementLLMRuntime(request *llm.Request, params domain.ProblemGenParams) {
	if params.ProviderConfig == nil {
		return
	}
	applyLLMRuntime(request, params.ProviderConfig.Statement)
}

func applyVerificationLLMRuntime(request *llm.Request, params domain.ProblemGenParams) {
	if params.ProviderConfig == nil {
		return
	}
	applyLLMRuntime(request, params.ProviderConfig.Verification)
}

// applyReviewLLMRuntime routes the final model review independently from the
// verification/oracle role. A nil review config retains the historical
// verification fallback for workflows created before the additive R field.
func applyReviewLLMRuntime(request *llm.Request, params domain.ProblemGenParams) {
	if params.ProviderConfig == nil {
		return
	}
	cfg := params.ProviderConfig.Review
	if cfg == nil {
		cfg = params.ProviderConfig.Verification
	}
	applyLLMRuntime(request, cfg)
}

func applyLLMRuntime(request *llm.Request, cfg *domain.LLMRuntimeConfig) {
	if request == nil || cfg == nil {
		return
	}
	if model := strings.TrimSpace(cfg.Model); model != "" {
		request.Model = model
	}
	if strings.TrimSpace(cfg.APIKeyRef) == "" &&
		strings.TrimSpace(cfg.BaseURL) == "" &&
		strings.TrimSpace(cfg.Provider) == "" &&
		strings.TrimSpace(cfg.Protocol) == "" &&
		strings.TrimSpace(cfg.ReasoningEffort) == "" {
		return
	}
	request.Runtime = &llm.RuntimeConfig{
		APIKeyRef:       strings.TrimSpace(cfg.APIKeyRef),
		BaseURL:         strings.TrimSpace(cfg.BaseURL),
		Provider:        strings.TrimSpace(cfg.Provider),
		Protocol:        strings.TrimSpace(cfg.Protocol),
		ReasoningEffort: strings.ToLower(strings.TrimSpace(cfg.ReasoningEffort)),
	}
}
