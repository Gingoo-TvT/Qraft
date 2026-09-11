package domain

import (
	"encoding/json"
	"testing"
)

func TestReviewSignalJSONContract(t *testing.T) {
	original := ReviewSignal{Token: "opaque-review-token", Decision: ReviewDecision{Approved: true, Feedback: "ok"}}
	encoded, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal review signal: %v", err)
	}

	var decoded ReviewSignal
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal review signal: %v", err)
	}
	if decoded.Token != original.Token || !decoded.Decision.Approved || decoded.Decision.Feedback != "ok" {
		t.Fatalf("unexpected decoded signal: %+v", decoded)
	}
}
