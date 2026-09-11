package activities

import (
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/llm"
)

func TestApplyLLMRuntimeIncludesProtocol(t *testing.T) {
	request := &llm.Request{}
	applyLLMRuntime(request, &domain.LLMRuntimeConfig{
		Model: "gemini-3.7-flash", APIKeyRef: "runtime:key",
		BaseURL: "https://api.example.com", Provider: "linkapi", Protocol: "gemini-native",
		ReasoningEffort: "high",
	})
	if request.Model != "gemini-3.7-flash" || request.Runtime == nil {
		t.Fatalf("request=%+v", request)
	}
	if request.Runtime.Protocol != "gemini-native" || request.Runtime.Provider != "linkapi" || request.Runtime.ReasoningEffort != "high" {
		t.Fatalf("runtime=%+v", request.Runtime)
	}
}

func TestApplyReviewLLMRuntimeUsesIndependentReviewRole(t *testing.T) {
	request := &llm.Request{}
	params := domain.ProblemGenParams{ProviderConfig: &domain.ProviderRuntimeConfig{
		Verification: &domain.LLMRuntimeConfig{Model: "verification-model", APIKeyRef: "runtime:verification"},
		Review:       &domain.LLMRuntimeConfig{Model: "review-model", APIKeyRef: "runtime:review"},
	}}

	applyReviewLLMRuntime(request, params)
	if request.Model != "review-model" || request.Runtime == nil || request.Runtime.APIKeyRef != "runtime:review" {
		t.Fatalf("review runtime=%+v request=%+v", request.Runtime, request)
	}
}

func TestApplyReviewLLMRuntimeRetainsVerificationFallback(t *testing.T) {
	request := &llm.Request{}
	params := domain.ProblemGenParams{ProviderConfig: &domain.ProviderRuntimeConfig{
		Verification: &domain.LLMRuntimeConfig{Model: "legacy-review-model", APIKeyRef: "runtime:legacy"},
	}}

	applyReviewLLMRuntime(request, params)
	if request.Model != "legacy-review-model" || request.Runtime == nil || request.Runtime.APIKeyRef != "runtime:legacy" {
		t.Fatalf("legacy review runtime=%+v request=%+v", request.Runtime, request)
	}
}
