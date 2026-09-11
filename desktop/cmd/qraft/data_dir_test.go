package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Gingoo-TvT/Qraft/desktop/internal/app"
)

func TestNewBrandDoesNotReadLegacyClientData(t *testing.T) {
	base := t.TempDir()
	old := filepath.Join(base, "AlgoForge")
	if err := os.MkdirAll(old, 0700); err != nil {
		t.Fatal(err)
	}
	original := []byte(`{"schema_version":1,"mode":"remote","server_url":"http://localhost:18080","local_port":18080}`)
	if err := os.WriteFile(filepath.Join(old, "settings.json"), original, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LOCALAPPDATA", base)
	oldData, oldPortable := *dataDir, *portable
	*dataDir, *portable = "", false
	defer func() { *dataDir, *portable = oldData, oldPortable }()
	dir, err := resolveDataDir()
	if err != nil || dir != filepath.Join(base, "Qraft") {
		t.Fatalf("%s %v", dir, err)
	}
	c, err := app.LoadConfig(dir)
	if err != nil || c.ServerURL != "" {
		t.Fatalf("inherited legacy service: %+v %v", c, err)
	}
	after, _ := os.ReadFile(filepath.Join(old, "settings.json"))
	if string(after) != string(original) {
		t.Fatal("legacy client data changed")
	}
	*portable = true
	exe, _ := os.Executable()
	dir, err = resolveDataDir()
	if err != nil || dir != filepath.Join(filepath.Dir(exe), "data-qraft") {
		t.Fatalf("portable directory=%s %v", dir, err)
	}
}
