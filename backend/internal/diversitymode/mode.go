// Package diversitymode resolves the independently reversible S5 product
// surface without changing the stable single-candidate Job API.
package diversitymode

import (
	"fmt"
	"strings"
)

const (
	ModeEnv         = "ALGOFORGE_S5_DIVERSITY_MODE"
	ModeDiversityV1 = "diversity-v1"
	ModeLegacyOnly  = "legacy-only"
)

type Audit struct {
	FlagName               string `json:"flag_name"`
	RequestedValue         string `json:"requested_value"`
	EffectiveMode          string `json:"effective_mode"`
	Source                 string `json:"source"`
	MicroBatchRouteEnabled bool   `json:"micro_batch_route_enabled"`
	LegacyJobAPIBehavior   string `json:"legacy_job_api_behavior"`
}

// ResolveMode defaults to the additive S5 surface, supports one explicit
// rollback, and fails closed for blank or unknown explicit configuration.
func ResolveMode(explicitValue string, explicitSet bool, lookupEnv func(string) (string, bool)) (Audit, error) {
	requested := explicitValue
	source := "flag"
	if !explicitSet {
		requested = ModeDiversityV1
		source = "default"
		if lookupEnv != nil {
			if value, ok := lookupEnv(ModeEnv); ok {
				requested = value
				source = "environment"
			}
		}
	}
	normalized := strings.ToLower(strings.TrimSpace(requested))
	switch normalized {
	case ModeDiversityV1:
		return Audit{
			FlagName: ModeEnv, RequestedValue: requested, EffectiveMode: ModeDiversityV1,
			Source: source, MicroBatchRouteEnabled: true, LegacyJobAPIBehavior: "unchanged",
		}, nil
	case ModeLegacyOnly:
		return Audit{
			FlagName: ModeEnv, RequestedValue: requested, EffectiveMode: ModeLegacyOnly,
			Source: source, MicroBatchRouteEnabled: false, LegacyJobAPIBehavior: "unchanged",
		}, nil
	default:
		return Audit{}, fmt.Errorf("S5 diversity mode rejected: unsupported value %q", normalized)
	}
}

func ValidateAudit(audit Audit) error {
	if audit.FlagName != ModeEnv || audit.LegacyJobAPIBehavior != "unchanged" {
		return errorsForAudit("flag name or legacy Job API behavior", audit)
	}
	switch audit.EffectiveMode {
	case ModeDiversityV1:
		if !audit.MicroBatchRouteEnabled {
			return errorsForAudit("diversity-v1 route state", audit)
		}
	case ModeLegacyOnly:
		if audit.MicroBatchRouteEnabled {
			return errorsForAudit("legacy-only route state", audit)
		}
	default:
		return errorsForAudit("effective mode", audit)
	}
	if audit.Source != "default" && audit.Source != "environment" && audit.Source != "flag" {
		return errorsForAudit("source", audit)
	}
	return nil
}

func errorsForAudit(field string, audit Audit) error {
	return fmt.Errorf("S5 diversity mode audit has invalid %s: %+v", field, audit)
}
