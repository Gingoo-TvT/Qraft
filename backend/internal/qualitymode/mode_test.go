package qualitymode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveModeDefaultsEnabledAndSupportsRollback(t *testing.T) {
	tests := []struct {
		name     string
		explicit string
		set      bool
		env      map[string]string
		wantMode string
		wantOn   bool
	}{
		{name: "default", wantMode: ModeQualityV1, wantOn: true},
		{name: "environment enabled", env: map[string]string{ModeEnv: " QUALITY-V1 "}, wantMode: ModeQualityV1, wantOn: true},
		{name: "environment rollback", env: map[string]string{ModeEnv: ModeLegacyOnly}, wantMode: ModeLegacyOnly},
		{name: "explicit rollback wins", explicit: ModeLegacyOnly, set: true, env: map[string]string{ModeEnv: ModeQualityV1}, wantMode: ModeLegacyOnly},
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
			if audit.EffectiveMode != test.wantMode || audit.ExtendedEvidenceLevels != test.wantOn {
				t.Fatalf("audit=%+v", audit)
			}
			if err := ValidateAudit(audit); err != nil {
				t.Fatalf("ValidateAudit: %v", err)
			}
		})
	}
}

func TestResolveModeRejectsUnknownAndExplicitBlank(t *testing.T) {
	for _, value := range []string{"", "   ", "off", "false", "quality-v2", "jobs-v1"} {
		t.Run(value, func(t *testing.T) {
			if _, err := ResolveMode(value, true, nil); err == nil {
				t.Fatalf("ResolveMode(%q) accepted", value)
			}
		})
	}
	for _, value := range []string{"", "   ", "enabled"} {
		t.Run("environment_"+value, func(t *testing.T) {
			lookup := func(string) (string, bool) { return value, true }
			if _, err := ResolveMode("", false, lookup); err == nil {
				t.Fatalf("environment value %q accepted", value)
			}
		})
	}
}

func TestValidateAuditRejectsForgedOrZeroValue(t *testing.T) {
	for _, audit := range []Audit{
		{},
		{FlagName: ModeEnv, EffectiveMode: ModeQualityV1},
		{FlagName: ModeEnv, EffectiveMode: ModeLegacyOnly, ExtendedEvidenceLevels: true},
		{FlagName: "OTHER", EffectiveMode: ModeQualityV1, ExtendedEvidenceLevels: true},
	} {
		if err := ValidateAudit(audit); err == nil {
			t.Fatalf("ValidateAudit accepted %+v", audit)
		}
	}
}

func TestDockerComposePropagatesQualityModeDefault(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("..", "..", "..", "docker-compose.yml"))
	if err != nil {
		t.Fatalf("read docker-compose.yml: %v", err)
	}
	want := "ALGOFORGE_QUALITY_MODE: ${ALGOFORGE_QUALITY_MODE-quality-v1}"
	if count := strings.Count(string(content), want); count != 1 {
		t.Fatalf("compose entry %q count=%d want=1", want, count)
	}
	if forbidden := "${ALGOFORGE_QUALITY_MODE:-quality-v1}"; strings.Contains(string(content), forbidden) {
		t.Fatalf("compose must preserve explicit empty quality mode, found %q", forbidden)
	}
}
