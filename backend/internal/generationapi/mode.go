package generationapi

import (
	"fmt"
	"strings"
)

const (
	ProductAPIModeEnv   = "ALGOFORGE_CUSTOM_GENERATION_API_MODE"
	ModeLegacyOnly      = "legacy-only"
	ModeContractPreview = "contract-preview"
	ModeJobsV1          = "jobs-v1"
)

type ModeAudit struct {
	FlagName            string `json:"flag_name"`
	RequestedValue      string `json:"requested_value"`
	EffectiveMode       string `json:"effective_mode"`
	Source              string `json:"source"`
	ContractPreview     bool   `json:"contract_preview"`
	ProductRouteEnabled bool   `json:"product_route_enabled"`
	LegacyBehavior      string `json:"legacy_behavior"`
}

// ResolveMode keeps the additive jobs-v1 route independently reversible while
// rejecting unknown values at startup.
func ResolveMode(
	explicitValue string,
	explicitSet bool,
	lookupEnv func(string) (string, bool),
) (ModeAudit, error) {
	requested := explicitValue
	source := "flag"
	if !explicitSet {
		requested = ModeJobsV1
		source = "default"
		if lookupEnv != nil {
			if value, ok := lookupEnv(ProductAPIModeEnv); ok {
				requested = value
				source = "environment"
			}
		}
	}
	normalized := strings.ToLower(strings.TrimSpace(requested))
	if normalized == "" || normalized == "off" || normalized == "false" ||
		normalized == "0" {
		normalized = ModeLegacyOnly
		requested = ModeLegacyOnly
	}
	switch normalized {
	case ModeLegacyOnly:
		return ModeAudit{
			FlagName:            ProductAPIModeEnv,
			RequestedValue:      requested,
			EffectiveMode:       ModeLegacyOnly,
			Source:              source,
			ContractPreview:     false,
			ProductRouteEnabled: false,
			LegacyBehavior:      "unchanged",
		}, nil
	case ModeContractPreview:
		return ModeAudit{
			FlagName:            ProductAPIModeEnv,
			RequestedValue:      requested,
			EffectiveMode:       ModeContractPreview,
			Source:              source,
			ContractPreview:     true,
			ProductRouteEnabled: false,
			LegacyBehavior:      "unchanged",
		}, nil
	case ModeJobsV1:
		return ModeAudit{
			FlagName:            ProductAPIModeEnv,
			RequestedValue:      requested,
			EffectiveMode:       ModeJobsV1,
			Source:              source,
			ContractPreview:     false,
			ProductRouteEnabled: true,
			LegacyBehavior:      "unchanged",
		}, nil
	default:
		return ModeAudit{}, fmt.Errorf(
			"custom generation API mode rejected: unsupported value %q",
			normalized,
		)
	}
}
