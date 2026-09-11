package domain

import (
	"strings"
	"testing"
)

func TestDefaultProblemGenParamsDisablesDeprecatedReviewPause(t *testing.T) {
	if DefaultProblemGenParams().RequireReview {
		t.Fatal("RequireReview product default must remain false; it is an operational break-glass switch")
	}
}

func TestProblemGenParamsRejectsTestCountAboveSandboxBatchContract(t *testing.T) {
	params := DefaultProblemGenParams()
	params.TestDataConfig.NumTestCases = MaxGeneratedTestCases
	if err := params.Validate(); err != nil {
		t.Fatalf("maximum supported test count rejected: %v", err)
	}

	params.TestDataConfig.NumTestCases++
	if err := params.Validate(); err == nil || !strings.Contains(err.Error(), "must not exceed 32") {
		t.Fatalf("oversized test count error = %v", err)
	}
}

func TestProblemGenParamsAcceptsRuntimeProviderEnvRefs(t *testing.T) {
	params := DefaultProblemGenParams()
	params.ProviderConfig = &ProviderRuntimeConfig{
		Statement: &LLMRuntimeConfig{
			Model:     "statement-model",
			APIKeyRef: "env:ALGOFORGE_STATEMENT_KEY",
		},
		Verification: &LLMRuntimeConfig{
			Model:     "review-model",
			APIKeyRef: "env:ALGOFORGE_REVIEW_KEY",
		},
	}

	if err := params.Validate(); err != nil {
		t.Fatalf("valid runtime provider config rejected: %v", err)
	}
}

func TestProblemGenParamsAcceptsRuntimeProviderTokenRefs(t *testing.T) {
	params := DefaultProblemGenParams()
	params.ProviderConfig = &ProviderRuntimeConfig{
		Statement: &LLMRuntimeConfig{
			Model:     "statement-model",
			APIKeyRef: "runtime:abc_DEF-123",
		},
		Verification: &LLMRuntimeConfig{
			Model:     "review-model",
			APIKeyRef: "runtime:review_DEF-456",
		},
	}

	if err := params.Validate(); err != nil {
		t.Fatalf("valid runtime token config rejected: %v", err)
	}
}

func TestProblemGenParamsAcceptsRuntimeProviderBaseURLs(t *testing.T) {
	params := DefaultProblemGenParams()
	params.ProviderConfig = &ProviderRuntimeConfig{
		Statement: &LLMRuntimeConfig{
			Model:     "statement-model",
			APIKeyRef: "env:ALGOFORGE_STATEMENT_KEY",
			BaseURL:   "https://llm.example.com",
			Provider:  "anthropic-compatible",
		},
		Verification: &LLMRuntimeConfig{
			Model:     "review-model",
			APIKeyRef: "runtime:review_DEF-456",
			BaseURL:   "http://127.0.0.1:18080/v1/messages",
			Provider:  "local-review",
		},
		Review: &LLMRuntimeConfig{
			Model:     "reviewer-model",
			APIKeyRef: "runtime:reviewer_GHI-789",
			BaseURL:   "https://review.example.com/v1",
			Provider:  "review-provider",
		},
	}

	if err := params.Validate(); err != nil {
		t.Fatalf("valid runtime base_url config rejected: %v", err)
	}
}

func TestProblemGenParamsRejectsCredentialBearingOrMutableRuntimeURLs(t *testing.T) {
	for _, baseURL := range []string{
		"https://user:secret@llm.example.com/v1",
		"https://llm.example.com/v1?token=secret",
		"https://llm.example.com/v1#fragment",
	} {
		t.Run(baseURL, func(t *testing.T) {
			params := DefaultProblemGenParams()
			params.ProviderConfig = &ProviderRuntimeConfig{
				Review: &LLMRuntimeConfig{
					Model: "review-model", BaseURL: baseURL, Provider: "review-provider",
				},
			}
			if err := params.Validate(); err == nil {
				t.Fatalf("unsafe base_url %q was accepted", baseURL)
			}
		})
	}
}

func TestProblemGenParamsRequiresRuntimeProviderForBaseURL(t *testing.T) {
	params := DefaultProblemGenParams()
	params.ProviderConfig = &ProviderRuntimeConfig{
		Statement: &LLMRuntimeConfig{
			BaseURL: "https://llm.example.com",
		},
	}

	if err := params.Validate(); err == nil || !strings.Contains(err.Error(), "provider is required") {
		t.Fatalf("base_url without provider validation error = %v", err)
	}
}

func TestProblemGenParamsRejectsRawRuntimeProviderKeys(t *testing.T) {
	params := DefaultProblemGenParams()
	params.ProviderConfig = &ProviderRuntimeConfig{
		Statement: &LLMRuntimeConfig{
			Model:  "statement-model",
			APIKey: "sk-raw-secret",
		},
	}

	if err := params.Validate(); err == nil || !strings.Contains(err.Error(), "raw secrets") {
		t.Fatalf("raw key validation error = %v", err)
	}
}

func TestProblemGenParamsRejectsNonEnvRuntimeProviderKeyRefs(t *testing.T) {
	params := DefaultProblemGenParams()
	params.ProviderConfig = &ProviderRuntimeConfig{
		Verification: &LLMRuntimeConfig{
			Model:     "review-model",
			APIKeyRef: "sk-raw-secret",
		},
	}

	if err := params.Validate(); err == nil || !strings.Contains(err.Error(), "env:NAME or runtime:<token>") {
		t.Fatalf("raw key ref validation error = %v", err)
	}
}

func TestKnowledgePointCombinationConformance(t *testing.T) {
	tests := []struct {
		name     string
		mode     string
		required []string
		observed []string
		want     []string
		wantErr  bool
	}{
		{name: "single", mode: KnowledgePointCombinationSingle, required: []string{"dp"}, observed: []string{" DP "}, want: []string{"dp"}},
		{name: "set order is canonical", mode: KnowledgePointCombinationSet, required: []string{"dp", "prefix-sum"}, observed: []string{"prefix-sum", "DP"}, want: []string{"dp", "prefix-sum"}},
		{name: "sequence order is binding", mode: KnowledgePointCombinationSequence, required: []string{"sorting", "two-pointers"}, observed: []string{"two-pointers", "sorting"}, wantErr: true},
		{name: "mixed primary is binding and tail is a set", mode: KnowledgePointCombinationMixed, required: []string{"graph", "dfs", "greedy"}, observed: []string{"graph", "greedy", "dfs"}, want: []string{"graph", "dfs", "greedy"}},
		{name: "mixed primary drift", mode: KnowledgePointCombinationMixed, required: []string{"graph", "dfs", "greedy"}, observed: []string{"dfs", "graph", "greedy"}, wantErr: true},
		{name: "extra model tag", mode: KnowledgePointCombinationSet, required: []string{"dp"}, observed: []string{"dp", "greedy"}, wantErr: true},
		{name: "duplicate model tag", mode: KnowledgePointCombinationSet, required: []string{"dp"}, observed: []string{"dp", "DP"}, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			contract := &KnowledgePointCombinationContract{
				SchemaVersion: KnowledgePointCombinationSchemaV1,
				Mode:          test.mode,
				MaxConcepts:   len(test.required),
			}
			got, err := contract.Conform(test.required, test.observed)
			if test.wantErr {
				if err == nil {
					t.Fatalf("Conform(%v) = %v, want error", test.observed, got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if strings.Join(got, ",") != strings.Join(test.want, ",") {
				t.Fatalf("canonical tags = %v, want %v", got, test.want)
			}
		})
	}
}

func TestProblemGenParamsKnowledgePointCombinationIsOptionalForLegacy(t *testing.T) {
	legacy := DefaultProblemGenParams()
	legacy.Tags = []string{"Unnormalized Legacy Tag"}
	if err := legacy.Validate(); err != nil {
		t.Fatalf("legacy nil combination contract changed behavior: %v", err)
	}

	versioned := DefaultProblemGenParams()
	versioned.Tags = []string{"prefix-sum", "dp"}
	versioned.KnowledgePointCombination = &KnowledgePointCombinationContract{
		SchemaVersion: KnowledgePointCombinationSchemaV1,
		Mode:          KnowledgePointCombinationSet,
		MaxConcepts:   2,
	}
	if err := versioned.Validate(); err == nil || !strings.Contains(err.Error(), "not canonical") {
		t.Fatalf("non-canonical versioned tags validation error = %v", err)
	}
}

func TestProblemGenParamsGenerationEvidenceIsOptionalAndFailClosed(t *testing.T) {
	legacy := DefaultProblemGenParams()
	if err := legacy.Validate(); err != nil {
		t.Fatalf("legacy nil generation evidence changed behavior: %v", err)
	}

	valid := DefaultProblemGenParams()
	valid.GenerationEvidence = &GenerationEvidenceContract{
		SchemaVersion:                   GenerationEvidenceContractSchemaV1,
		EvidenceLevel:                   GenerationStandardEvidenceLevel,
		JobContractVersion:              "algoforge.generation-job.v1",
		GenerationArm:                   "A",
		GenerationArmMode:               "baseline_passthrough",
		GenerationBehavior:              "unchanged",
		ReviewerProfile:                 "algoforge.reviewer.v1",
		ReviewerProfileDescriptorSHA256: strings.Repeat("a", 64),
		EvidenceProfile:                 "algoforge.review-evidence.v0",
		EvidenceProfileDescriptorSHA256: strings.Repeat("b", 64),
		OutcomeTaxonomy:                 "algoforge.generation-outcome-taxonomy.v1",
		IncludeEditorial:                true,
		IncludeSolutions:                true,
		IncludeTestData:                 true,
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid standard evidence contract rejected: %v", err)
	}

	invalid := *valid.GenerationEvidence
	invalid.IncludeTestData = false
	valid.GenerationEvidence = &invalid
	if err := valid.Validate(); err == nil || !strings.Contains(err.Error(), "requires editorial, solutions, and test data") {
		t.Fatalf("partial standard evidence contract error = %v", err)
	}
}
