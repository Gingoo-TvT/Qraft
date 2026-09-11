package exportmode

import (
	"fmt"
	"strings"
)

const (
	QG15ExportModeEnv = "ALGOFORGE_QG15_EXPORT_MODE"
	ModeQG15V1        = "qg15-v1"
	ModeLegacyOnly    = "legacy-only"
)

type Audit struct {
	FlagName              string `json:"flag_name"`
	RequestedValue        string `json:"requested_value"`
	EffectiveMode         string `json:"effective_mode"`
	Source                string `json:"source"`
	QG15ProductRoutes     bool   `json:"qg15_product_routes"`
	HydroS3BindingEnabled bool   `json:"hydro_s3_binding_enabled"`
}

// ResolveMode defaults to QG15 v1, supports one explicit rollback value, and
// rejects every unknown (including explicitly blank) value at startup.
func ResolveMode(explicitValue string, explicitSet bool, lookupEnv func(string) (string, bool)) (Audit, error) {
	requested := explicitValue
	source := "flag"
	if !explicitSet {
		requested = ModeQG15V1
		source = "default"
		if lookupEnv != nil {
			if value, ok := lookupEnv(QG15ExportModeEnv); ok {
				requested = value
				source = "environment"
			}
		}
	}
	normalized := strings.ToLower(strings.TrimSpace(requested))
	switch normalized {
	case ModeQG15V1:
		return Audit{
			FlagName: QG15ExportModeEnv, RequestedValue: requested, EffectiveMode: ModeQG15V1, Source: source,
			QG15ProductRoutes: true, HydroS3BindingEnabled: true,
		}, nil
	case ModeLegacyOnly:
		return Audit{
			FlagName: QG15ExportModeEnv, RequestedValue: requested, EffectiveMode: ModeLegacyOnly, Source: source,
			QG15ProductRoutes: false, HydroS3BindingEnabled: false,
		}, nil
	default:
		return Audit{}, fmt.Errorf("QG15 export mode rejected: unsupported value %q", normalized)
	}
}
