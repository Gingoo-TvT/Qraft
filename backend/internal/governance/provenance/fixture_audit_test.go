package provenance

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	testFixturePolicyPath = "governance/policy.json"
	testTermsSnapshotID   = "project-owned-fixtures-v1"
	testTermsSnapshotPath = "governance/terms/project-owned.json"
)

func TestAuditFixtureCatalogProducesDeterministicThreePurposeReport(t *testing.T) {
	root := t.TempDir()
	parentHash := writeAuditFixture(t, root, "fixtures/parent.json", "{\"fixture\":\"parent\"}\n")
	childHash := writeAuditFixture(t, root, "fixtures/child.json", "{\"fixture\":\"child\"}\n")

	parent := completeArtifact("fixture-parent", PermissionSet{InternalEval: true})
	parent.ContentHash = parentHash
	parent.AncestryIDs = []string{}
	child := completeArtifact("fixture-child", PermissionSet{InternalEval: true, PrivateTraining: true, PublicRelease: true})
	child.ContentHash = childHash
	child.AncestryIDs = []string{parent.ArtifactID}
	catalog := FixtureCatalog{
		SchemaVersion: FixtureCatalogSchemaVersion,
		PolicyVersion: parent.PolicyVersion,
		AsOf:          time.Date(2026, 7, 17, 0, 0, 0, 0, time.UTC),
		FixtureRoots:  []string{"fixtures"},
		Artifacts: []FixtureRecord{
			{Path: "fixtures/child.json", Artifact: child},
			{Path: "fixtures/parent.json", Artifact: parent},
		},
	}
	bindTestGovernance(t, root, &catalog, testFixturePolicy(allPurposes()), testTermsSnapshot("fixtures/**", allPurposes()))
	data := marshalFixtureCatalog(t, catalog)

	first, err := AuditFixtureCatalog(root, data)
	if err != nil {
		t.Fatalf("audit fixture catalog: %v", err)
	}
	second, err := AuditFixtureCatalog(root, data)
	if err != nil {
		t.Fatalf("audit fixture catalog again: %v", err)
	}
	firstJSON, _ := json.Marshal(first)
	secondJSON, _ := json.Marshal(second)
	if string(firstJSON) != string(secondJSON) {
		t.Fatalf("audit report changed across runs:\n%s\n%s", firstJSON, secondJSON)
	}
	if first.ArtifactCount != 2 || first.EligibleCount != 2 || first.EligibilityCoverage != 1 {
		t.Fatalf("unexpected coverage: %+v", first)
	}
	if first.PolicyPath != testFixturePolicyPath || first.PolicySHA256 != catalog.PolicySHA256 {
		t.Fatalf("policy binding missing from report: %+v", first)
	}
	if first.Records[0].ArtifactID != child.ArtifactID || first.Records[1].ArtifactID != parent.ArtifactID {
		t.Fatalf("records are not sorted by artifact ID: %+v", first.Records)
	}
	childDecisions := first.Records[0].Decisions
	if !childDecisions.InternalEval.Allowed || childDecisions.PrivateTraining.Allowed || childDecisions.PublicRelease.Allowed {
		t.Fatalf("strict ancestor decisions were not applied independently: %+v", childDecisions)
	}
}

func TestAuditFixtureCatalogFailsClosed(t *testing.T) {
	type mutation struct {
		name        string
		preserveRaw bool
		apply       func(*testing.T, string, *FixtureCatalog, *[]byte)
	}
	mutations := []mutation{
		{name: "content hash mismatch", apply: func(_ *testing.T, _ string, catalog *FixtureCatalog, _ *[]byte) {
			catalog.Artifacts[0].Artifact.ContentHash = strings.Repeat("0", 64)
		}},
		{name: "terms hash mismatch", apply: func(_ *testing.T, _ string, catalog *FixtureCatalog, _ *[]byte) {
			catalog.Artifacts[0].Artifact.TermsSnapshotHash = strings.Repeat("0", 64)
		}},
		{name: "policy hash mismatch", apply: func(_ *testing.T, _ string, catalog *FixtureCatalog, _ *[]byte) {
			catalog.PolicySHA256 = strings.Repeat("0", 64)
		}},
		{name: "internal eval denied", apply: func(_ *testing.T, _ string, catalog *FixtureCatalog, _ *[]byte) {
			catalog.Artifacts[0].Artifact.Permissions.InternalEval = false
		}},
		{name: "path escape", apply: func(_ *testing.T, _ string, catalog *FixtureCatalog, _ *[]byte) {
			catalog.Artifacts[0].Path = "../outside.json"
		}},
		{name: "uncataloged fixture file", apply: func(t *testing.T, root string, _ *FixtureCatalog, _ *[]byte) {
			writeAuditFixture(t, root, "fixtures/unlisted.json", "{}\n")
		}},
		{name: "fixture root escape", apply: func(_ *testing.T, _ string, catalog *FixtureCatalog, _ *[]byte) {
			catalog.FixtureRoots = []string{"../outside"}
		}},
		{name: "missing explicit purpose", preserveRaw: true, apply: func(t *testing.T, _ string, _ *FixtureCatalog, data *[]byte) {
			mutateCatalogArtifact(t, data, func(artifact map[string]any) {
				delete(artifact["permissions"].(map[string]any), "public_release_allowed")
			})
		}},
		{name: "null purpose", preserveRaw: true, apply: func(t *testing.T, _ string, _ *FixtureCatalog, data *[]byte) {
			mutateCatalogArtifact(t, data, func(artifact map[string]any) {
				artifact["permissions"].(map[string]any)["public_release_allowed"] = nil
			})
		}},
		{name: "missing explicit ancestry", preserveRaw: true, apply: func(t *testing.T, _ string, _ *FixtureCatalog, data *[]byte) {
			mutateCatalogArtifact(t, data, func(artifact map[string]any) { delete(artifact, "ancestry_ids") })
		}},
		{name: "unknown catalog field", preserveRaw: true, apply: func(t *testing.T, _ string, _ *FixtureCatalog, data *[]byte) {
			var raw map[string]any
			mustUnmarshalJSON(t, *data, &raw)
			raw["unexpected"] = true
			*data = mustMarshalJSON(t, raw)
		}},
		{name: "trailing catalog value", preserveRaw: true, apply: func(_ *testing.T, _ string, _ *FixtureCatalog, data *[]byte) {
			*data = append(*data, []byte("\n{}\n")...)
		}},
	}
	for _, test := range mutations {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			catalog := newSingleFixtureCatalog(t, root, "fixtures/fixture.json", "{}\n", PermissionSet{InternalEval: true})
			data := marshalFixtureCatalog(t, catalog)
			test.apply(t, root, &catalog, &data)
			if !test.preserveRaw {
				data = marshalFixtureCatalog(t, catalog)
			}
			if report, err := AuditFixtureCatalog(root, data); err == nil {
				t.Fatalf("audit succeeded with report %+v", report)
			}
		})
	}
}

func TestAuditFixtureCatalogRejectsPolicyAndTermsViolations(t *testing.T) {
	type violation struct {
		name   string
		mutate func(*testing.T, string, *FixtureCatalog, *fixturePolicyDocument, *fixtureTermsSnapshot)
	}
	violations := []violation{
		{name: "policy permission ceiling", mutate: func(t *testing.T, root string, catalog *FixtureCatalog, policy *fixturePolicyDocument, _ *fixtureTermsSnapshot) {
			policy.Rules[0].PublicRelease = boolPointer(false)
			catalog.PolicySHA256 = writeAuditJSONFixture(t, root, testFixturePolicyPath, policy)
		}},
		{name: "snapshot id mismatch", mutate: func(_ *testing.T, _ string, catalog *FixtureCatalog, _ *fixturePolicyDocument, _ *fixtureTermsSnapshot) {
			catalog.Artifacts[0].TermsSnapshotID = "wrong-snapshot"
		}},
		{name: "snapshot license mismatch", mutate: func(t *testing.T, root string, catalog *FixtureCatalog, _ *fixturePolicyDocument, snapshot *fixtureTermsSnapshot) {
			snapshot.LicenseBasis = "unknown"
			rewriteTermsSnapshot(t, root, catalog, snapshot)
		}},
		{name: "snapshot policy mismatch", mutate: func(t *testing.T, root string, catalog *FixtureCatalog, _ *fixturePolicyDocument, snapshot *fixtureTermsSnapshot) {
			snapshot.PolicyVersion = "other-policy"
			rewriteTermsSnapshot(t, root, catalog, snapshot)
		}},
		{name: "snapshot permission ceiling", mutate: func(t *testing.T, root string, catalog *FixtureCatalog, _ *fixturePolicyDocument, snapshot *fixtureTermsSnapshot) {
			snapshot.Permissions.PublicRelease = boolPointer(false)
			rewriteTermsSnapshot(t, root, catalog, snapshot)
		}},
		{name: "snapshot scope mismatch", mutate: func(t *testing.T, root string, catalog *FixtureCatalog, _ *fixturePolicyDocument, snapshot *fixtureTermsSnapshot) {
			snapshot.Scope = []string{"other/**"}
			rewriteTermsSnapshot(t, root, catalog, snapshot)
		}},
		{name: "snapshot contains invalid scope", mutate: func(t *testing.T, root string, catalog *FixtureCatalog, _ *fixturePolicyDocument, snapshot *fixtureTermsSnapshot) {
			snapshot.Scope = append(snapshot.Scope, "../outside/**")
			rewriteTermsSnapshot(t, root, catalog, snapshot)
		}},
		{name: "snapshot not approved", mutate: func(t *testing.T, root string, catalog *FixtureCatalog, _ *fixturePolicyDocument, snapshot *fixtureTermsSnapshot) {
			snapshot.ReviewStatus = "unreviewed"
			rewriteTermsSnapshot(t, root, catalog, snapshot)
		}},
		{name: "missing ownership evidence", mutate: func(t *testing.T, root string, catalog *FixtureCatalog, _ *fixturePolicyDocument, snapshot *fixtureTermsSnapshot) {
			snapshot.ContentOwner = ""
			rewriteTermsSnapshot(t, root, catalog, snapshot)
		}},
		{name: "unknown source revision", mutate: func(_ *testing.T, _ string, catalog *FixtureCatalog, _ *fixturePolicyDocument, _ *fixtureTermsSnapshot) {
			catalog.Artifacts[0].Artifact.SourceRevision = "unknown"
		}},
		{name: "unknown policy field", mutate: func(t *testing.T, root string, catalog *FixtureCatalog, policy *fixturePolicyDocument, _ *fixtureTermsSnapshot) {
			raw := toJSONObject(t, policy)
			raw["unexpected"] = true
			catalog.PolicySHA256 = writeAuditJSONFixture(t, root, testFixturePolicyPath, raw)
		}},
		{name: "unknown snapshot field", mutate: func(t *testing.T, root string, catalog *FixtureCatalog, _ *fixturePolicyDocument, snapshot *fixtureTermsSnapshot) {
			raw := toJSONObject(t, snapshot)
			raw["unexpected"] = true
			hash := writeAuditJSONFixture(t, root, testTermsSnapshotPath, raw)
			catalog.Artifacts[0].Artifact.TermsSnapshotHash = hash
		}},
		{name: "embedded snapshot mismatch", mutate: func(t *testing.T, root string, catalog *FixtureCatalog, _ *fixturePolicyDocument, _ *fixtureTermsSnapshot) {
			content := `{"license":{"snapshot_id":"wrong-snapshot"}}` + "\n"
			catalog.Artifacts[0].Artifact.ContentHash = writeAuditFixture(t, root, catalog.Artifacts[0].Path, content)
		}},
		{name: "embedded terms hash mismatch", mutate: func(t *testing.T, root string, catalog *FixtureCatalog, _ *fixturePolicyDocument, _ *fixtureTermsSnapshot) {
			content := `{"license":{"terms_snapshot_hash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}` + "\n"
			catalog.Artifacts[0].Artifact.ContentHash = writeAuditFixture(t, root, catalog.Artifacts[0].Path, content)
		}},
		{name: "embedded license mismatch", mutate: func(t *testing.T, root string, catalog *FixtureCatalog, _ *fixturePolicyDocument, _ *fixtureTermsSnapshot) {
			content := `{"license":{"license_basis":"unknown"}}` + "\n"
			catalog.Artifacts[0].Artifact.ContentHash = writeAuditFixture(t, root, catalog.Artifacts[0].Path, content)
		}},
	}
	for _, test := range violations {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			catalog := newSingleFixtureCatalog(t, root, "fixtures/fixture.json", "{}\n", allPurposes())
			policy := testFixturePolicy(allPurposes())
			snapshot := testTermsSnapshot("fixtures/**", allPurposes())
			test.mutate(t, root, &catalog, &policy, &snapshot)
			if report, err := AuditFixtureCatalog(root, marshalFixtureCatalog(t, catalog)); err == nil {
				t.Fatalf("audit succeeded with report %+v", report)
			}
		})
	}
}

func TestAuditFixtureCatalogRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.json")
	if err := os.WriteFile(outside, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "fixtures"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "fixtures", "fixture.json")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	catalog := symlinkAuditCatalog(t, root)
	if _, err := AuditFixtureCatalog(root, marshalFixtureCatalog(t, catalog)); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlink was not rejected: %v", err)
	}
}

func TestAuditFixtureCatalogRejectsSymlinkedParentDirectory(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "fixture.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "fixtures")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	catalog := symlinkAuditCatalog(t, root)
	if _, err := AuditFixtureCatalog(root, marshalFixtureCatalog(t, catalog)); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlinked parent directory was not rejected: %v", err)
	}
}

func newSingleFixtureCatalog(t *testing.T, root, relativePath, content string, permissions PermissionSet) FixtureCatalog {
	t.Helper()
	artifact := completeArtifact("fixture", permissions)
	artifact.ContentHash = writeAuditFixture(t, root, relativePath, content)
	artifact.AncestryIDs = []string{}
	catalog := FixtureCatalog{
		SchemaVersion: FixtureCatalogSchemaVersion,
		PolicyVersion: artifact.PolicyVersion,
		AsOf:          time.Date(2026, 7, 17, 0, 0, 0, 0, time.UTC),
		FixtureRoots:  []string{filepath.ToSlash(filepath.Dir(relativePath))},
		Artifacts:     []FixtureRecord{{Path: relativePath, Artifact: artifact}},
	}
	bindTestGovernance(t, root, &catalog, testFixturePolicy(allPurposes()), testTermsSnapshot(filepath.ToSlash(filepath.Dir(relativePath))+"/**", allPurposes()))
	return catalog
}

func symlinkAuditCatalog(t *testing.T, root string) FixtureCatalog {
	t.Helper()
	artifact := completeArtifact("fixture", PermissionSet{InternalEval: true})
	artifact.ContentHash = strings.Repeat("0", 64)
	artifact.AncestryIDs = []string{}
	catalog := FixtureCatalog{
		SchemaVersion: FixtureCatalogSchemaVersion,
		PolicyVersion: artifact.PolicyVersion,
		AsOf:          time.Date(2026, 7, 17, 0, 0, 0, 0, time.UTC),
		FixtureRoots:  []string{"fixtures"},
		Artifacts:     []FixtureRecord{{Path: "fixtures/fixture.json", Artifact: artifact}},
	}
	bindTestGovernance(t, root, &catalog, testFixturePolicy(allPurposes()), testTermsSnapshot("fixtures/**", allPurposes()))
	return catalog
}

func bindTestGovernance(t *testing.T, root string, catalog *FixtureCatalog, policy fixturePolicyDocument, snapshot fixtureTermsSnapshot) {
	t.Helper()
	catalog.PolicyPath = testFixturePolicyPath
	catalog.PolicySHA256 = writeAuditJSONFixture(t, root, testFixturePolicyPath, policy)
	termsHash := writeAuditJSONFixture(t, root, testTermsSnapshotPath, snapshot)
	for i := range catalog.Artifacts {
		catalog.Artifacts[i].TermsSnapshotID = snapshot.SnapshotID
		catalog.Artifacts[i].TermsSnapshotPath = testTermsSnapshotPath
		catalog.Artifacts[i].Artifact.TermsSnapshotHash = termsHash
	}
}

func rewriteTermsSnapshot(t *testing.T, root string, catalog *FixtureCatalog, snapshot *fixtureTermsSnapshot) {
	t.Helper()
	hash := writeAuditJSONFixture(t, root, testTermsSnapshotPath, snapshot)
	for i := range catalog.Artifacts {
		catalog.Artifacts[i].Artifact.TermsSnapshotHash = hash
	}
}

func testFixturePolicy(permissions PermissionSet) fixturePolicyDocument {
	return fixturePolicyDocument{
		SchemaVersion: fixturePolicySchemaVersion,
		PolicyVersion: "2026-07-13.v1",
		Default:       explicitPermissions(PermissionSet{}),
		Rules: []fixturePolicyRule{{
			LicenseBasis:          "project_owned",
			explicitPermissionSet: explicitPermissions(permissions),
			Requires:              []string{"ownership_record", "terms_snapshot"},
		}},
		Inheritance:          "all_derivatives_inherit_the_strictest_transitive_ancestor",
		UnknownValueBehavior: "deny",
		TakedownBehavior:     "deny_and_rebuild_affected_manifests",
	}
}

func testTermsSnapshot(scope string, permissions PermissionSet) fixtureTermsSnapshot {
	return fixtureTermsSnapshot{
		SchemaVersion:    termsSnapshotSchemaVersion,
		SnapshotID:       testTermsSnapshotID,
		Source:           "AlgoForge test fixtures",
		CapturedAt:       time.Date(2026, 7, 17, 0, 0, 0, 0, time.UTC),
		EffectiveAt:      time.Date(2026, 7, 17, 0, 0, 0, 0, time.UTC),
		ContentOwner:     "AlgoForge project",
		LicenseBasis:     "project_owned",
		Scope:            []string{scope},
		Permissions:      explicitPermissions(permissions),
		Reviewer:         "AlgoForge engineering",
		ReviewStatus:     "approved",
		StorageReference: "repository:" + testTermsSnapshotPath,
		PolicyVersion:    "2026-07-13.v1",
	}
}

func explicitPermissions(permissions PermissionSet) explicitPermissionSet {
	return explicitPermissionSet{
		InternalEval:    boolPointer(permissions.InternalEval),
		PrivateTraining: boolPointer(permissions.PrivateTraining),
		PublicRelease:   boolPointer(permissions.PublicRelease),
	}
}

func allPurposes() PermissionSet {
	return PermissionSet{InternalEval: true, PrivateTraining: true, PublicRelease: true}
}

func boolPointer(value bool) *bool { return &value }

func writeAuditFixture(t *testing.T, root, relativePath, content string) string {
	t.Helper()
	path := filepath.Join(root, relativePath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	data := []byte(content)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func writeAuditJSONFixture(t *testing.T, root, relativePath string, value any) string {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return writeAuditFixture(t, root, relativePath, string(append(data, '\n')))
}

func marshalFixtureCatalog(t *testing.T, catalog FixtureCatalog) []byte {
	t.Helper()
	for i := range catalog.Artifacts {
		if catalog.Artifacts[i].Artifact.AncestryIDs == nil {
			catalog.Artifacts[i].Artifact.AncestryIDs = []string{}
		}
	}
	return mustMarshalJSON(t, catalog)
}

func mutateCatalogArtifact(t *testing.T, data *[]byte, mutate func(map[string]any)) {
	t.Helper()
	var raw map[string]any
	mustUnmarshalJSON(t, *data, &raw)
	artifact := raw["artifacts"].([]any)[0].(map[string]any)["artifact"].(map[string]any)
	mutate(artifact)
	*data = mustMarshalJSON(t, raw)
}

func toJSONObject(t *testing.T, value any) map[string]any {
	t.Helper()
	var raw map[string]any
	mustUnmarshalJSON(t, mustMarshalJSON(t, value), &raw)
	return raw
}

func mustMarshalJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func mustUnmarshalJSON(t *testing.T, data []byte, destination any) {
	t.Helper()
	if err := json.Unmarshal(data, destination); err != nil {
		t.Fatal(err)
	}
}
