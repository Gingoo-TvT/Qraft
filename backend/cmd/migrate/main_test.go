package main

import (
	"bytes"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/migrate"
)

func TestDatabaseURLFromEnvironment(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("POSTGRES_HOST", "::1")
	t.Setenv("POSTGRES_PORT", "5433")
	t.Setenv("POSTGRES_USER", "user name")
	t.Setenv("POSTGRES_PASSWORD", "p@ss word")
	t.Setenv("POSTGRES_DB", "algo forge")
	t.Setenv("POSTGRES_SSLMODE", "require")

	dsn, err := databaseURLFromEnvironment()
	if err != nil {
		t.Fatalf("databaseURLFromEnvironment() error = %v", err)
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}
	if u.Host != "[::1]:5433" || u.Path != "/algo forge" {
		t.Fatalf("database URL host/path = %q/%q", u.Host, u.Path)
	}
	if u.User.Username() != "user name" {
		t.Fatalf("database URL user = %q", u.User.Username())
	}
	password, ok := u.User.Password()
	if !ok || password != "p@ss word" {
		t.Fatalf("database URL password = %q, %v", password, ok)
	}
	if u.Query().Get("sslmode") != "require" {
		t.Fatalf("database URL sslmode = %q", u.Query().Get("sslmode"))
	}
}

func TestDatabaseURLFromEnvironmentRejectsInvalidPort(t *testing.T) {
	t.Setenv("POSTGRES_PORT", "not-a-port")
	if _, err := databaseURLFromEnvironment(); err == nil {
		t.Fatal("databaseURLFromEnvironment() error = nil, want invalid port error")
	}
}

func TestRunBaselineRequiresExplicitOptionsBeforeConnecting(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "001_initial.sql"), []byte("SELECT 1;"), 0o600); err != nil {
		t.Fatalf("write migration: %v", err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"baseline", "-dir", dir}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("run() code = %d, want 2; stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "--through must be positive") {
		t.Fatalf("stderr = %q, want baseline option error", stderr.String())
	}
}

func TestSplitCommandSupportsCommandFirstAndLegacyFlagFirst(t *testing.T) {
	command, args := splitCommand([]string{"catalog-manifest", "-dir", "migrations"})
	if command != "catalog-manifest" || len(args) != 2 {
		t.Fatalf("splitCommand(command first) = %q, %#v", command, args)
	}
	command, args = splitCommand([]string{"-dir", "migrations", "status"})
	if command != "" || len(args) != 3 {
		t.Fatalf("splitCommand(flag first) = %q, %#v", command, args)
	}
}

func TestIsCommand(t *testing.T) {
	for _, command := range []string{"up", "status", "catalog-hash", "catalog-manifest", "baseline"} {
		if !isCommand(command) {
			t.Errorf("isCommand(%q) = false, want true", command)
		}
	}
	if isCommand("unknown") {
		t.Error("isCommand(unknown) = true, want false")
	}
}

func TestRunUsageIncludesCatalogManifest(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(nil, &stdout, &stderr); code != 2 {
		t.Fatalf("run() code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "catalog-hash|catalog-manifest") {
		t.Fatalf("usage = %q, want catalog-manifest", stderr.String())
	}
}

func TestPrintCatalogManifestWritesOnlyDiagnosticFields(t *testing.T) {
	entries := []migrate.CatalogManifestEntry{{
		Kind:             "function",
		Identity:         "public.calculate(integer)",
		DefinitionSHA256: strings.Repeat("a", 64),
	}}
	var output bytes.Buffer
	if err := printCatalogManifest(&output, entries); err != nil {
		t.Fatalf("printCatalogManifest() error = %v", err)
	}

	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("JSONL line count = %d, want 1", len(lines))
	}
	var decoded map[string]string
	if err := json.Unmarshal([]byte(lines[0]), &decoded); err != nil {
		t.Fatalf("decode JSONL: %v", err)
	}
	if len(decoded) != 3 || decoded["kind"] != entries[0].Kind ||
		decoded["identity"] != entries[0].Identity ||
		decoded["definition_sha256"] != entries[0].DefinitionSHA256 {
		t.Fatalf("JSONL entry = %#v", decoded)
	}
}
