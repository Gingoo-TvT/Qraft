package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type fakeRunner struct {
	calls []string
	fn    func(...string) (string, error)
}

func (f *fakeRunner) Run(ctx context.Context, dir string, args ...string) (string, error) {
	f.calls = append(f.calls, strings.Join(args, " "))
	if f.fn != nil {
		return f.fn(args...)
	}
	switch args[0] {
	case "context":
		return "unix:///var/run/docker.sock", nil
	case "info":
		return "linux x86_64 2", nil
	case "compose":
		return "2.39.0", nil
	case "ps", "volume", "network":
		return "", nil
	}
	return "", fmt.Errorf("unexpected command")
}
func fixtureManifest() RuntimeManifest {
	d := strings.Repeat("a", 64)
	images := []Image{}
	for _, name := range []string{"backend", "frontend", "sandbox", "migrations", "postgresql"} {
		images = append(images, Image{Tag: "qraft-desktop/" + name + ":" + BackendVersion, ID: "sha256:" + d})
	}
	return RuntimeManifest{Version: BackendVersion, SourceRevision: strings.Repeat("b", 40), BundleSHA256: d, Images: images, SandboxImage: "sha256:" + d, ToolchainDigest: "sha256:" + d, SeccompDigest: "sha256:" + d}
}
func TestPreparePreservesKeysAndLoopbackContract(t *testing.T) {
	m := NewManager(t.TempDir(), &fakeRunner{}, fixtureManifest())
	if e := m.prepare(18080); e != nil {
		t.Fatal(e)
	}
	before, e := readEnv(filepath.Join(m.runtimeDir(), ".env"))
	if e != nil {
		t.Fatal(e)
	}
	if e = m.prepare(18081); e != nil {
		t.Fatal(e)
	}
	after, _ := readEnv(filepath.Join(m.runtimeDir(), ".env"))
	for k, v := range before {
		if k != "LOCAL_PORT" && after[k] != v {
			t.Fatalf("replaced durable %s", k)
		}
	}
	if after["LOCAL_PORT"] != "18081" {
		t.Fatal(after["LOCAL_PORT"])
	}
	if len(after["JWT_SECRET"]) != 64 || after["JWT_SECRET"] == after["POSTGRES_PASSWORD"] {
		t.Fatal("invalid independent credentials")
	}
	compose, e := os.ReadFile(filepath.Join(m.runtimeDir(), "compose.yml"))
	if e != nil {
		t.Fatal(e)
	}
	text := string(compose)
	if !strings.Contains(text, "127.0.0.1:") || strings.Contains(text, "build:") || strings.Contains(text, "temporal-ui:") || !strings.Contains(text, "stop_grace_period: 40m") {
		t.Fatal("runtime contract changed")
	}
	if !strings.Contains(text, "${ALGOFORGE_EMBEDDING_ENABLED:-false}") {
		t.Fatal("fresh runtime must allow API setup before embedding configuration")
	}
	if strings.Count(text, "ports:") != 1 {
		t.Fatal("internal service port exposed")
	}
}
func TestDamagedEnvironmentIsNotReplaced(t *testing.T) {
	m := NewManager(t.TempDir(), &fakeRunner{}, fixtureManifest())
	os.MkdirAll(m.runtimeDir(), 0700)
	path := filepath.Join(m.runtimeDir(), ".env")
	original := []byte("JWT_SECRET=existing\n")
	os.WriteFile(path, original, 0600)
	if e := m.prepare(18080); e == nil {
		t.Fatal("accepted damaged existing deployment")
	}
	b, _ := os.ReadFile(path)
	if string(b) != string(original) {
		t.Fatal("overwrote credentials")
	}
}
func TestBundleChecksumStopsDockerLoad(t *testing.T) {
	f := &fakeRunner{}
	m := NewManager(t.TempDir(), f, fixtureManifest())
	path := filepath.Join(t.TempDir(), "wrong.tar.gz")
	os.WriteFile(path, []byte("wrong"), 0600)
	if _, e := m.importImages(context.Background(), path); e == nil {
		t.Fatal("accepted wrong bundle")
	}
	if len(f.calls) != 0 {
		t.Fatalf("called Docker before validating: %v", f.calls)
	}
}
func TestBundleRequiresExpectedImageIDs(t *testing.T) {
	b := []byte("test archive")
	h := sha256.Sum256(b)
	manifest := fixtureManifest()
	manifest.BundleSHA256 = hex.EncodeToString(h[:])
	f := &fakeRunner{fn: func(args ...string) (string, error) {
		if args[0] == "load" {
			return "loaded", nil
		}
		return "sha256:" + strings.Repeat("f", 64), nil
	}}
	m := NewManager(t.TempDir(), f, manifest)
	path := filepath.Join(t.TempDir(), "backend.tar.gz")
	os.WriteFile(path, b, 0600)
	if _, e := m.importImages(context.Background(), path); e == nil {
		t.Fatal("accepted wrong loaded image ID")
	}
}
func TestForeignProjectCannotBeStopped(t *testing.T) {
	f := &fakeRunner{fn: func(args ...string) (string, error) {
		switch args[0] {
		case "context":
			return "npipe://docker", nil
		case "info":
			return "linux amd64 2", nil
		case "compose":
			return "2.39.0", nil
		case "ps":
			return "foreign-container", nil
		case "container":
			return "{\"io.qraft.desktop.owner\":\"foreign-owner\"}", nil
		}
		t.Fatal(args)
		return "", nil
	}}
	m := NewManager(t.TempDir(), f, fixtureManifest())
	if _, e := m.Run(context.Background(), "stop", ""); e == nil {
		t.Fatal("stopped foreign project")
	}
	for _, c := range f.calls {
		if strings.Contains(c, " stop") {
			t.Fatal(c)
		}
	}
}
func TestRemoteDockerAndV1CgroupsAreRejected(t *testing.T) {
	for _, tc := range []struct{ endpoint, info string }{{"ssh://host", "linux x86_64 2"}, {"unix:///var/run/docker.sock", "linux x86_64 1"}} {
		f := &fakeRunner{fn: func(args ...string) (string, error) {
			if args[0] == "context" {
				return tc.endpoint, nil
			}
			return tc.info, nil
		}}
		m := NewManager(t.TempDir(), f, fixtureManifest())
		if e := m.prerequisites(context.Background()); e == nil {
			t.Fatal(tc)
		}
	}
}
func TestLogsHideGeneratedSecrets(t *testing.T) {
	m := NewManager(t.TempDir(), &fakeRunner{}, fixtureManifest())
	if e := m.prepare(18080); e != nil {
		t.Fatal(e)
	}
	env, _ := readEnv(filepath.Join(m.runtimeDir(), ".env"))
	if strings.Contains(m.redact("key="+env["JWT_SECRET"]), env["JWT_SECRET"]) {
		t.Fatal("secret escaped")
	}
}

func TestRuntimeMountReadableWithoutExposingSecrets(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX bind-mount permissions")
	}
	m := NewManager(t.TempDir(), &fakeRunner{}, fixtureManifest())
	if err := m.prepare(18080); err != nil {
		t.Fatal(err)
	}
	initDir := filepath.Join(m.runtimeDir(), "deploy", "initdb")
	sql := filepath.Join(initDir, "00-extensions.sql")
	// Also repair an interrupted installation created with overly private modes.
	os.Chmod(initDir, 0700)
	os.Chmod(sql, 0600)
	if err := m.prepare(18080); err != nil {
		t.Fatal(err)
	}
	for path, mode := range map[string]os.FileMode{initDir: 0755, sql: 0644, filepath.Join(m.runtimeDir(), ".env"): 0600} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != mode {
			t.Fatalf("%s: %v %v", path, info, err)
		}
	}
}

func TestManifestPinsBackendIndependentlyOfClient(t *testing.T) {
	m := fixtureManifest()
	if e := m.Validate(); e != nil {
		t.Fatal(e)
	}
	m.Version = "99.0.0"
	if e := m.Validate(); e == nil {
		t.Fatal("unverified backend version accepted")
	}
}
