package exportmode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveModeDefaultEnabledAndLegacyRollback(t *testing.T) {
	tests := []struct {
		name       string
		explicit   string
		set        bool
		env        map[string]string
		wantMode   string
		wantRoutes bool
		wantHydro  bool
	}{
		{name: "default", wantMode: ModeQG15V1, wantRoutes: true, wantHydro: true},
		{name: "environment enabled", env: map[string]string{QG15ExportModeEnv: " QG15-V1 "}, wantMode: ModeQG15V1, wantRoutes: true, wantHydro: true},
		{name: "environment rollback", env: map[string]string{QG15ExportModeEnv: ModeLegacyOnly}, wantMode: ModeLegacyOnly},
		{name: "explicit rollback", explicit: ModeLegacyOnly, set: true, env: map[string]string{QG15ExportModeEnv: ModeQG15V1}, wantMode: ModeLegacyOnly},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			lookup := func(key string) (string, bool) {
				value, ok := test.env[key]
				return value, ok
			}
			audit, err := ResolveMode(test.explicit, test.set, lookup)
			if err != nil {
				t.Fatalf("ResolveMode: %v", err)
			}
			if audit.EffectiveMode != test.wantMode || audit.QG15ProductRoutes != test.wantRoutes || audit.HydroS3BindingEnabled != test.wantHydro {
				t.Fatalf("audit=%+v", audit)
			}
		})
	}
}

func TestResolveModeRejectsUnknownAndExplicitBlank(t *testing.T) {
	for _, value := range []string{"", "off", "jobs-v1", "qg15-v2"} {
		t.Run(value, func(t *testing.T) {
			if _, err := ResolveMode(value, true, nil); err == nil {
				t.Fatalf("ResolveMode(%q) accepted", value)
			}
		})
	}
}

func TestDockerComposePropagatesProductRollbackModes(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("..", "..", "..", "docker-compose.yml"))
	if err != nil {
		t.Fatalf("read docker-compose.yml: %v", err)
	}
	wantEntries := []string{
		"ALGOFORGE_CUSTOM_GENERATION_API_MODE: ${ALGOFORGE_CUSTOM_GENERATION_API_MODE-jobs-v1}",
		"ALGOFORGE_QG15_EXPORT_MODE: ${ALGOFORGE_QG15_EXPORT_MODE-qg15-v1}",
		"ALGOFORGE_QUALITY_MODE: ${ALGOFORGE_QUALITY_MODE-quality-v1}",
		"ALGOFORGE_S5_DIVERSITY_MODE: ${ALGOFORGE_S5_DIVERSITY_MODE-diversity-v1}",
	}
	for _, entry := range wantEntries {
		if count := strings.Count(string(content), entry); count != 1 {
			t.Fatalf("compose entry %q count=%d want=1", entry, count)
		}
	}
	for _, forbidden := range []string{
		"${ALGOFORGE_CUSTOM_GENERATION_API_MODE:-jobs-v1}",
		"${ALGOFORGE_QG15_EXPORT_MODE:-qg15-v1}",
		"${ALGOFORGE_QUALITY_MODE:-quality-v1}",
		"${ALGOFORGE_S5_DIVERSITY_MODE:-diversity-v1}",
	} {
		if strings.Contains(string(content), forbidden) {
			t.Fatalf("compose must preserve explicit empty rollout mode, found %q", forbidden)
		}
	}
}
