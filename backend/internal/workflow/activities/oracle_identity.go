package activities

import "strings"

const (
	OracleIdentityCrossModelIndependent = "cross_model_independent"
	OracleIdentityCorrelated            = "correlated_oracle"
)

// OracleIndependenceReceipt records whether the main-solution (G) and oracle
// (V) calls actually returned identities from different models and backends.
// Any missing or shared identity fails closed to correlated_oracle.
type OracleIndependenceReceipt struct {
	SchemaVersion       int    `json:"schema_version"`
	Status              string `json:"status"`
	Reason              string `json:"reason"`
	MainReturnedModel   string `json:"main_returned_model,omitempty"`
	OracleReturnedModel string `json:"oracle_returned_model,omitempty"`
	MainProvider        string `json:"main_provider,omitempty"`
	OracleProvider      string `json:"oracle_provider,omitempty"`
	MainEndpointID      string `json:"main_endpoint_id,omitempty"`
	OracleEndpointID    string `json:"oracle_endpoint_id,omitempty"`
}

func assessOracleIndependence(mainArtifact, oracleArtifact *ArtifactRef) *OracleIndependenceReceipt {
	receipt := &OracleIndependenceReceipt{
		SchemaVersion: 1,
		Status:        OracleIdentityCorrelated,
	}
	if mainArtifact == nil || oracleArtifact == nil || mainArtifact.LLMCallReceipt == nil || oracleArtifact.LLMCallReceipt == nil {
		receipt.Reason = "missing_call_receipt"
		return receipt
	}

	mainCall := mainArtifact.LLMCallReceipt
	oracleCall := oracleArtifact.LLMCallReceipt
	receipt.MainReturnedModel = strings.TrimSpace(mainCall.ReturnedModel)
	receipt.OracleReturnedModel = strings.TrimSpace(oracleCall.ReturnedModel)
	receipt.MainProvider = strings.TrimSpace(mainCall.Provider)
	receipt.OracleProvider = strings.TrimSpace(oracleCall.Provider)
	receipt.MainEndpointID = strings.TrimSpace(mainCall.EndpointID)
	receipt.OracleEndpointID = strings.TrimSpace(oracleCall.EndpointID)

	if receipt.MainReturnedModel == "" || receipt.OracleReturnedModel == "" ||
		receipt.MainEndpointID == "" || receipt.OracleEndpointID == "" {
		receipt.Reason = "missing_returned_identity"
		return receipt
	}
	modelsDiffer := !strings.EqualFold(receipt.MainReturnedModel, receipt.OracleReturnedModel)
	endpointsDiffer := receipt.MainEndpointID != receipt.OracleEndpointID
	if modelsDiffer && endpointsDiffer {
		receipt.Status = OracleIdentityCrossModelIndependent
		receipt.Reason = "different_returned_model_and_endpoint"
		return receipt
	}
	switch {
	case !modelsDiffer && !endpointsDiffer:
		receipt.Reason = "same_returned_model_and_endpoint"
	case !modelsDiffer:
		receipt.Reason = "same_returned_model"
	default:
		receipt.Reason = "same_endpoint"
	}
	return receipt
}
