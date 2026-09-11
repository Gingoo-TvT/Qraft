package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBakedRevisionMustMatchRuntimeRevision(t *testing.T) {
	revision := strings.Repeat("a", 64)
	if err := validateBakedRevision(revision, revision); err != nil {
		t.Fatalf("matching revision rejected: %v", err)
	}
	if err := validateBakedRevision(strings.Repeat("b", 64), revision); err == nil {
		t.Fatal("runtime revision override was accepted")
	}
	if err := validateBakedRevision(revision, ""); err == nil {
		t.Fatal("empty baked revision was accepted")
	}
}

func TestReadBakedRevision(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source-revision.txt")
	revision := strings.Repeat("c", 64)
	if err := os.WriteFile(path, []byte(revision+"\n"), 0o444); err != nil {
		t.Fatal(err)
	}
	got, err := readBakedRevision(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != revision {
		t.Fatalf("baked revision = %q, want %q", got, revision)
	}
}
