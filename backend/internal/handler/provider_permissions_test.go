package handler

import (
	"context"
	"github.com/Gingoo-TvT/Qraft/backend/internal/access"
	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"testing"
)

func TestMemberCannotOverrideSavedModelConfiguration(t *testing.T) {
	ctx := access.WithPrincipal(context.Background(), access.Principal{UserID: "member-a", Role: "member"})
	for _, override := range []*domain.ProviderRuntimeConfig{
		{}, {Statement: &domain.LLMRuntimeConfig{Model: "different"}},
		{Verification: &domain.LLMRuntimeConfig{BaseURL: "https://untrusted.invalid", APIKeyRef: "env:POSTGRES_PASSWORD"}},
		{Review: &domain.LLMRuntimeConfig{APIKey: "synthetic-key"}},
	} {
		if err := validateRequestProviderConfig(ctx, override); err == nil {
			t.Fatal("member override accepted")
		}
	}
	if err := validateRequestProviderConfig(ctx, nil); err != nil {
		t.Fatal(err)
	}
}

func TestRequestKeyReferencesAreNeverTrustedEvenForAdmin(t *testing.T) {
	ctx := access.WithPrincipal(context.Background(), access.Principal{UserID: "admin-a", Role: "admin"})
	for _, ref := range []string{"env:POSTGRES_PASSWORD", "env:ANTHROPIC_API_KEY", "runtime:other-session-key"} {
		for _, cfg := range []*domain.ProviderRuntimeConfig{
			{Statement: &domain.LLMRuntimeConfig{APIKeyRef: ref}},
			{Verification: &domain.LLMRuntimeConfig{APIKeyRef: ref}},
			{Review: &domain.LLMRuntimeConfig{APIKeyRef: ref}},
		} {
			if err := validateRequestProviderConfig(ctx, cfg); err == nil {
				t.Fatal("untrusted reference accepted")
			}
		}
	}
	if err := validateRequestProviderConfig(ctx, &domain.ProviderRuntimeConfig{Statement: &domain.LLMRuntimeConfig{Model: "test", APIKey: "admin-supplied-key"}}); err != nil {
		t.Fatal(err)
	}
}
