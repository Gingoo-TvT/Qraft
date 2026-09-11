package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestConfigRoundTripAndInvalidConfigPreserved(t *testing.T) {
	dir := t.TempDir()
	c, err := LoadConfig(dir)
	if err != nil || c != DefaultConfig() {
		t.Fatalf("default=%+v %v", c, err)
	}
	c.ServerURL = "https://oj.example.edu/"
	if err := SaveConfig(dir, c); err != nil {
		t.Fatal(err)
	}
	got, err := LoadConfig(dir)
	if err != nil || got.ServerURL != "https://oj.example.edu" {
		t.Fatalf("%+v %v", got, err)
	}
	c.LocalPort = 18090
	if err := SaveConfig(dir, c); err != nil {
		t.Fatal(err)
	}
	got, err = LoadConfig(dir)
	if err != nil || got.LocalPort != 18090 {
		t.Fatalf("replacement failed: %+v %v", got, err)
	}
	path := filepath.Join(dir, "settings.json")
	original := []byte("{broken user config")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(dir); err == nil {
		t.Fatal("corruption silently reset")
	}
	preserved, _ := os.ReadFile(path)
	if string(preserved) != string(original) {
		t.Fatal("corrupted config overwritten")
	}
}

func TestURLValidation(t *testing.T) {
	for _, s := range []string{"", "file:///C:/secret", "javascript:alert(1)", "localhost:18080", "https://user:secret@oj.example", "https://oj.example?key=secret", "https://oj.example/#secret", "http://localhost:65536", "http://localhost:0", "https://oj.example/forge"} {
		if _, err := NormalizeURL(s); err == nil {
			t.Errorf("accepted %q", s)
		}
	}
	for _, s := range []string{"http://localhost:18080", "http://[::1]:18080"} {
		if got, err := NormalizeURL(s); err != nil || got != s {
			t.Errorf("%q: %s %v", s, got, err)
		}
	}
}

func TestConnectionProbesActualCapabilitiesAndDoesNotFollowRedirect(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   any
		ok     bool
	}{
		{"ready", 200, map[string]any{"schema_version": "algoforge.integration-capabilities.v1", "release_version": "1.6.0", "problem_sets": map[string]bool{"enabled": true}}, true},
		{"unrelated", 200, map[string]string{"status": "ok"}, false},
		{"login", 401, map[string]string{}, false},
		{"redirect", 302, map[string]string{}, false},
		{"unavailable", 503, map[string]string{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			redirected := false
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/login" {
					redirected = true
					return
				}
				if r.URL.Path != "/api/v1/integration/capabilities" {
					t.Errorf("path=%s", r.URL.Path)
				}
				w.Header().Set("Location", "/login")
				w.WriteHeader(tc.status)
				_ = json.NewEncoder(w).Encode(tc.body)
			}))
			defer srv.Close()
			result, err := CheckConnection(context.Background(), srv.URL)
			if (err == nil) != tc.ok || result.Ready != tc.ok {
				t.Fatalf("%+v %v", result, err)
			}
			if redirected {
				t.Fatal("login redirect followed as if it were a capability response")
			}
		})
	}
}
