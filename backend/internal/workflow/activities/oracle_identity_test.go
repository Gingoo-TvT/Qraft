package activities

import "testing"

func TestAssessOracleIndependenceRequiresDifferentReturnedModelAndEndpoint(t *testing.T) {
	main := artifactWithCallReceipt("main-model", "provider-g", "endpoint-g")
	oracle := artifactWithCallReceipt("oracle-model", "provider-v", "endpoint-v")

	got := assessOracleIndependence(main, oracle)
	if got.Status != OracleIdentityCrossModelIndependent || got.Reason != "different_returned_model_and_endpoint" {
		t.Fatalf("independence assessment = %+v", got)
	}
}

func TestAssessOracleIndependenceFailsClosedToCorrelated(t *testing.T) {
	tests := map[string]struct {
		main       *ArtifactRef
		oracle     *ArtifactRef
		wantReason string
	}{
		"missing receipt": {
			main:       &ArtifactRef{},
			oracle:     artifactWithCallReceipt("oracle-model", "provider-v", "endpoint-v"),
			wantReason: "missing_call_receipt",
		},
		"missing returned identity": {
			main:       artifactWithCallReceipt("", "provider-g", "endpoint-g"),
			oracle:     artifactWithCallReceipt("oracle-model", "provider-v", "endpoint-v"),
			wantReason: "missing_returned_identity",
		},
		"same model": {
			main:       artifactWithCallReceipt("shared-model", "provider-g", "endpoint-g"),
			oracle:     artifactWithCallReceipt("SHARED-MODEL", "provider-v", "endpoint-v"),
			wantReason: "same_returned_model",
		},
		"same endpoint": {
			main:       artifactWithCallReceipt("main-model", "provider-g", "shared-endpoint"),
			oracle:     artifactWithCallReceipt("oracle-model", "provider-v", "shared-endpoint"),
			wantReason: "same_endpoint",
		},
		"same model and endpoint": {
			main:       artifactWithCallReceipt("shared-model", "provider-g", "shared-endpoint"),
			oracle:     artifactWithCallReceipt("shared-model", "provider-v", "shared-endpoint"),
			wantReason: "same_returned_model_and_endpoint",
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got := assessOracleIndependence(tc.main, tc.oracle)
			if got.Status != OracleIdentityCorrelated || got.Reason != tc.wantReason {
				t.Fatalf("correlation assessment = %+v", got)
			}
		})
	}
}

func artifactWithCallReceipt(returnedModel, provider, endpointID string) *ArtifactRef {
	return &ArtifactRef{LLMCallReceipt: &LLMCallReceipt{
		SchemaVersion: 1,
		ReturnedModel: returnedModel,
		Provider:      provider,
		EndpointID:    endpointID,
	}}
}
