package repository

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestWorkflowOperationIdentityValidation(t *testing.T) {
	hash := strings.Repeat("a", 64)
	if err := validateOperationIdentity("workflow/store/v1", "store_problem/v1", hash); err != nil {
		t.Fatalf("valid identity rejected: %v", err)
	}
	for name, values := range map[string][3]string{
		"empty key":      {"", "store", hash},
		"empty type":     {"key", "", hash},
		"short hash":     {"key", "store", "abc"},
		"uppercase hash": {"key", "store", strings.Repeat("A", 64)},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateOperationIdentity(values[0], values[1], values[2]); err == nil {
				t.Fatal("invalid identity accepted")
			}
		})
	}
}

func TestWorkflowOperationResultComparisonUsesJSONSemantics(t *testing.T) {
	left := json.RawMessage(`{"a":1,"b":[2,3]}`)
	right := json.RawMessage(`{"b":[2,3],"a":1}`)
	if !jsonEqual(left, right) {
		t.Fatal("equivalent JSON results were treated as different")
	}
	if jsonEqual(left, json.RawMessage(`{"a":2,"b":[2,3]}`)) {
		t.Fatal("different JSON results were treated as equal")
	}
}
