package repository

import (
	"strings"
	"testing"
)

func TestProviderEffectIdentityValidation(t *testing.T) {
	hash := strings.Repeat("a", 64)
	if err := validateProviderEffectIdentity("temporal:fixture", "llm_completion/v1", hash); err != nil {
		t.Fatalf("valid identity rejected: %v", err)
	}
	for name, values := range map[string][3]string{
		"empty key":      {"", "llm", hash},
		"empty type":     {"key", "", hash},
		"short hash":     {"key", "llm", "abc"},
		"uppercase hash": {"key", "llm", strings.Repeat("A", 64)},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateProviderEffectIdentity(values[0], values[1], values[2]); err == nil {
				t.Fatal("invalid provider effect identity accepted")
			}
		})
	}
}
