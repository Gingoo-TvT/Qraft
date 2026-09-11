package activities

import (
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/llm"
)

func TestEnsurePromptIdentityUsesStableLogicalPrefix(t *testing.T) {
	req := &llm.Request{}
	if err := ensurePromptIdentity(req, "s5_concept_attempt_v1:attempt-42"); err != nil {
		t.Fatalf("ensurePromptIdentity: %v", err)
	}
	if req.PromptID != "s5_concept_attempt_v1" || req.PromptVersion != "v1" {
		t.Fatalf("unexpected prompt identity: %+v", req)
	}
}

func TestEnsurePromptIdentityCanonicalizesBuiltinAlias(t *testing.T) {
	req := &llm.Request{PromptID: "review"}
	if err := ensurePromptIdentity(req, ""); err != nil {
		t.Fatalf("ensurePromptIdentity: %v", err)
	}
	if req.PromptID != "llm_review" || req.PromptVersion != "v1" {
		t.Fatalf("unexpected canonical prompt identity: %+v", req)
	}
}

func TestLLMCallReceiptCarriesPromptIdentity(t *testing.T) {
	req := &llm.Request{Model: "model", PromptID: "review", PromptVersion: "v1", Messages: []llm.Message{{Role: "user", Content: "x"}}}
	receipt, err := newLLMCallReceipt("provider", req, nil, strings.Repeat("a", 64), "https://example.com/v1")
	if err != nil {
		t.Fatalf("newLLMCallReceipt: %v", err)
	}
	if receipt.PromptID != "review" || receipt.PromptVersion != "v1" {
		t.Fatalf("prompt identity not preserved: %+v", receipt)
	}
}
