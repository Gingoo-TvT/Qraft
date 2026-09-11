package diversitymode

import "testing"

func TestResolveModeDefaultsEnabledAndSupportsRollback(t *testing.T) {
	tests := []struct {
		name     string
		explicit string
		set      bool
		env      map[string]string
		wantMode string
		wantOn   bool
	}{
		{name: "default", wantMode: ModeDiversityV1, wantOn: true},
		{name: "environment enabled", env: map[string]string{ModeEnv: " DIVERSITY-V1 "}, wantMode: ModeDiversityV1, wantOn: true},
		{name: "environment rollback", env: map[string]string{ModeEnv: ModeLegacyOnly}, wantMode: ModeLegacyOnly},
		{name: "explicit rollback wins", explicit: ModeLegacyOnly, set: true, env: map[string]string{ModeEnv: ModeDiversityV1}, wantMode: ModeLegacyOnly},
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
			if audit.EffectiveMode != test.wantMode || audit.MicroBatchRouteEnabled != test.wantOn ||
				audit.LegacyJobAPIBehavior != "unchanged" {
				t.Fatalf("audit=%+v", audit)
			}
			if err := ValidateAudit(audit); err != nil {
				t.Fatalf("ValidateAudit: %v", err)
			}
		})
	}
}

func TestResolveModeRejectsUnknownAndBlank(t *testing.T) {
	for _, value := range []string{"", "   ", "off", "false", "s5-v1", "jobs-v1"} {
		t.Run("explicit_"+value, func(t *testing.T) {
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
		{FlagName: ModeEnv, EffectiveMode: ModeDiversityV1, LegacyJobAPIBehavior: "unchanged"},
		{FlagName: ModeEnv, EffectiveMode: ModeLegacyOnly, MicroBatchRouteEnabled: true, LegacyJobAPIBehavior: "unchanged"},
		{FlagName: "OTHER", EffectiveMode: ModeDiversityV1, MicroBatchRouteEnabled: true, LegacyJobAPIBehavior: "unchanged"},
		{FlagName: ModeEnv, EffectiveMode: ModeDiversityV1, MicroBatchRouteEnabled: true, LegacyJobAPIBehavior: "changed", Source: "default"},
	} {
		if err := ValidateAudit(audit); err == nil {
			t.Fatalf("ValidateAudit accepted %+v", audit)
		}
	}
}
