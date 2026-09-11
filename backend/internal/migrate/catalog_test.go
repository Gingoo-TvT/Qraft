package migrate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestHashCatalogObjectsIsOrderIndependentAndDefinitionSensitive(t *testing.T) {
	objects := []catalogObject{
		{Kind: "type", Identity: "public.level", Definition: "enum={a,b}"},
		{Kind: "column", Identity: "public.items.1.id", Definition: "type=uuid"},
	}
	reversed := []catalogObject{objects[1], objects[0]}

	first := hashCatalogObjects(objects)
	second := hashCatalogObjects(reversed)
	if first != second {
		t.Fatalf("catalog hash changed with input order: %#v != %#v", first, second)
	}
	changed := append([]catalogObject(nil), objects...)
	changed[0].Definition = "enum={a,b,c}"
	if hashCatalogObjects(changed).SHA256 == first.SHA256 {
		t.Fatal("catalog hash did not change with object definition")
	}
}

func TestRunnerCatalogHashUsesMigrationLock(t *testing.T) {
	db, state := newFakeDatabase(t)
	runner := newRunnerWithDatabase(t, db, "SELECT 1;")

	result, err := runner.CatalogHash(context.Background())
	if err != nil {
		t.Fatalf("CatalogHash() error = %v", err)
	}
	expected := hashCatalogObjects(state.catalog)
	if result != expected {
		t.Fatalf("CatalogHash() = %#v, want %#v", result, expected)
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.lockCount != 1 || state.unlockCount != 1 {
		t.Fatalf("lock/unlock = %d/%d, want 1/1", state.lockCount, state.unlockCount)
	}
}

func TestManifestCatalogObjectsIsSortedAndDefinitionHashed(t *testing.T) {
	objects := []catalogObject{
		{Kind: "type", Identity: "public.level", Definition: "enum={a,b}"},
		{Kind: "column", Identity: "public.items.id", Definition: "type=uuid"},
		{Kind: "column", Identity: "public.items.id", Definition: "not_null=true"},
	}

	manifest := manifestCatalogObjects(objects)
	wantDefinitions := []string{"not_null=true", "type=uuid", "enum={a,b}"}
	if len(manifest) != len(wantDefinitions) {
		t.Fatalf("manifest length = %d, want %d", len(manifest), len(wantDefinitions))
	}
	for i, definition := range wantDefinitions {
		digest := sha256.Sum256([]byte(definition))
		wantSHA256 := hex.EncodeToString(digest[:])
		if manifest[i].DefinitionSHA256 != wantSHA256 {
			t.Errorf("manifest[%d].DefinitionSHA256 = %q, want %q", i, manifest[i].DefinitionSHA256, wantSHA256)
		}
	}
	if manifest[0].Kind != "column" || manifest[0].Identity != "public.items.id" ||
		manifest[1].Kind != "column" || manifest[1].Identity != "public.items.id" ||
		manifest[2].Kind != "type" || manifest[2].Identity != "public.level" {
		t.Fatalf("manifest order = %#v", manifest)
	}
}

func TestRunnerCatalogManifestUsesMigrationLock(t *testing.T) {
	db, state := newFakeDatabase(t)
	runner := newRunnerWithDatabase(t, db, "SELECT 1;")

	result, err := runner.CatalogManifest(context.Background())
	if err != nil {
		t.Fatalf("CatalogManifest() error = %v", err)
	}
	expected := manifestCatalogObjects(state.catalog)
	if len(result) != len(expected) {
		t.Fatalf("CatalogManifest() length = %d, want %d", len(result), len(expected))
	}
	for i := range expected {
		if result[i] != expected[i] {
			t.Fatalf("CatalogManifest()[%d] = %#v, want %#v", i, result[i], expected[i])
		}
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.lockCount != 1 || state.unlockCount != 1 {
		t.Fatalf("lock/unlock = %d/%d, want 1/1", state.lockCount, state.unlockCount)
	}
}
