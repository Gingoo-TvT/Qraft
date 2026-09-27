package config

import (
	"testing"

	"github.com/spf13/viper"
)

func TestAuthTrustProxyDefaultsOffAndUsesExplicitEnv(t *testing.T) {
	for _, tt := range []struct {
		value string
		want  bool
	}{
		{"", false},
		{"true", true},
		{"false", false},
	} {
		t.Run(tt.value, func(t *testing.T) {
			t.Setenv("QRAFT_AUTH_TRUST_PROXY", tt.value)
			v := viper.New()
			setDefaults(v)
			var cfg Config
			if err := v.Unmarshal(&cfg); err != nil {
				t.Fatal(err)
			}
			if cfg.App.AuthTrustProxy != tt.want {
				t.Fatalf("AuthTrustProxy = %v, want %v", cfg.App.AuthTrustProxy, tt.want)
			}
		})
	}
}
