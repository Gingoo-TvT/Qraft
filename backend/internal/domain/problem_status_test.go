package domain

import "testing"

func TestProblemStatusQuarantinedIsValidAndNotPublished(t *testing.T) {
	if !ProblemStatusQuarantined.IsValid() {
		t.Fatal("quarantined status must be valid")
	}
	if ProblemStatusQuarantined == ProblemStatusPublished {
		t.Fatal("quarantined status must be distinct from published")
	}
}
