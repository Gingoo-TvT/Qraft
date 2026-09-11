package qualitymode

import (
	"fmt"
	"strings"
)

const (
	ModeEnv        = "ALGOFORGE_QUALITY_MODE"
	ModeQualityV1  = "quality-v1"
	ModeLegacyOnly = "legacy-only"
)

// Audit is the startup-resolved quality profile. It is intentionally small:
// legacy-only keeps the S3 minimal job path available while withdrawing the
// additive standard/audit product profiles.
type Audit struct {
	FlagName               string `json:"flag_name"`
	RequestedValue         string `json:"requested_value"`
	EffectiveMode          string `json:"effective_mode"`
	Source                 string `json:"source"`
	ExtendedEvidenceLevels bool   `json:"extended_evidence_levels"`
}

// ResolveMode defaults to quality-v1, supports one explicit rollback mode,
// and rejects unknown or explicitly blank configuration at startup.
func ResolveMode(explicitValue string, explicitSet bool, lookupEnv func(string) (string, bool)) (Audit, error) {
	requested := explicitValue
	source := "flag"
	if !explicitSet {
		requested = ModeQualityV1
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
	case ModeQualityV1:
		return Audit{
			FlagName: ModeEnv, RequestedValue: requested, EffectiveMode: ModeQualityV1,
			Source: source, ExtendedEvidenceLevels: true,
		}, nil
	case ModeLegacyOnly:
		return Audit{
			FlagName: ModeEnv, RequestedValue: requested, EffectiveMode: ModeLegacyOnly,
			Source: source, ExtendedEvidenceLevels: false,
		}, nil
	default:
		return Audit{}, fmt.Errorf("quality mode rejected: unsupported value %q", normalized)
	}
}

// ValidateAudit prevents a manually assembled or zero-value audit from
// accidentally enabling a partially configured profile.
func ValidateAudit(audit Audit) error {
	if audit.FlagName != ModeEnv {
		return fmt.Errorf("quality mode audit has invalid flag name %q", audit.FlagName)
	}
	switch audit.EffectiveMode {
	case ModeQualityV1:
		if !audit.ExtendedEvidenceLevels {
			return fmt.Errorf("quality-v1 audit must enable extended evidence levels")
		}
	case ModeLegacyOnly:
		if audit.ExtendedEvidenceLevels {
			return fmt.Errorf("legacy-only audit must disable extended evidence levels")
		}
	default:
		return fmt.Errorf("quality mode audit has invalid effective mode %q", audit.EffectiveMode)
	}
	return nil
}
