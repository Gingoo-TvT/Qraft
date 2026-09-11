package provenance

import (
	"strings"
	"testing"
	"time"
)

const testHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func completeArtifact(id string, permissions PermissionSet) Artifact {
	return Artifact{
		ArtifactID:        id,
		ArtifactType:      "problem_statement",
		ContentHash:       testHash,
		SourceType:        "generated",
		SourceURI:         "algoforge://workflow/test",
		SourceRevision:    "attempt-1",
		Creator:           "test-suite",
		Provider:          "internal",
		Model:             "not_applicable",
		ModelRevision:     "not_applicable",
		GeneratedAt:       time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC),
		LicenseBasis:      "project_owned",
		TermsSnapshotHash: testHash,
		RetentionClass:    "project_record",
		TakedownStatus:    "active",
		PolicyVersion:     "2026-07-13.v1",
		Permissions:       permissions,
	}
}

func TestEvaluatorKeepsPurposesSeparate(t *testing.T) {
	artifact := completeArtifact("a", PermissionSet{InternalEval: true})
	evaluator := Evaluator{PolicyVersion: artifact.PolicyVersion, Artifacts: map[string]Artifact{"a": artifact}}

	if !evaluator.Evaluate("a", PurposeInternalEval).Allowed {
		t.Fatal("internal evaluation should be allowed")
	}
	if evaluator.Evaluate("a", PurposePrivateTraining).Allowed {
		t.Fatal("private training must default to denied")
	}
	if evaluator.Evaluate("a", PurposePublicRelease).Allowed {
		t.Fatal("public release must default to denied")
	}
}

func TestEvaluatorInheritsStrictestAncestor(t *testing.T) {
	parent := completeArtifact("parent", PermissionSet{InternalEval: true})
	child := completeArtifact("child", PermissionSet{InternalEval: true, PrivateTraining: true, PublicRelease: true})
	child.AncestryIDs = []string{parent.ArtifactID}
	evaluator := Evaluator{
		PolicyVersion: parent.PolicyVersion,
		Artifacts: map[string]Artifact{
			parent.ArtifactID: parent,
			child.ArtifactID:  child,
		},
	}

	decision := evaluator.Evaluate(child.ArtifactID, PurposePublicRelease)
	if decision.Allowed {
		t.Fatal("a derivative cannot be released when an ancestor denies release")
	}
	if len(decision.BlockingIDs) != 1 || decision.BlockingIDs[0] != parent.ArtifactID {
		t.Fatalf("unexpected blocking ancestry: %#v", decision.BlockingIDs)
	}
}

func TestEvaluatorFailsClosedForUnknownAndIncompleteRecords(t *testing.T) {
	evaluator := Evaluator{PolicyVersion: "2026-07-13.v1", Artifacts: map[string]Artifact{}}
	if evaluator.Evaluate("missing", PurposeInternalEval).Allowed {
		t.Fatal("a missing provenance record must be denied")
	}

	incomplete := completeArtifact("incomplete", PermissionSet{InternalEval: true})
	incomplete.TermsSnapshotHash = ""
	evaluator.Artifacts[incomplete.ArtifactID] = incomplete
	decision := evaluator.Evaluate(incomplete.ArtifactID, PurposeInternalEval)
	if decision.Allowed || !strings.Contains(strings.Join(decision.BlockingReasons, " "), "terms_snapshot_hash") {
		t.Fatalf("incomplete record was not rejected: %#v", decision)
	}

	if evaluator.Evaluate(incomplete.ArtifactID, Purpose("new_unreviewed_use")).Allowed {
		t.Fatal("unknown purposes must be denied")
	}
}

func TestEvaluatorRevocationExpiryAndTakedownInvalidateDescendants(t *testing.T) {
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	for name, mutate := range map[string]func(*Artifact){
		"source revoke": func(a *Artifact) { a.TakedownStatus = "revoked" },
		"retention expiry": func(a *Artifact) {
			expired := now.Add(-time.Second)
			a.RetentionExpires = &expired
		},
		"takedown": func(a *Artifact) { a.TakedownStatus = "takedown_requested" },
	} {
		t.Run(name, func(t *testing.T) {
			parent := completeArtifact("parent", PermissionSet{InternalEval: true, PrivateTraining: true, PublicRelease: true})
			mutate(&parent)
			child := completeArtifact("child", parent.Permissions)
			child.AncestryIDs = []string{parent.ArtifactID}
			evaluator := Evaluator{
				PolicyVersion: parent.PolicyVersion,
				Artifacts:     map[string]Artifact{parent.ArtifactID: parent, child.ArtifactID: child},
				Now:           func() time.Time { return now },
			}
			if evaluator.Evaluate(child.ArtifactID, PurposePublicRelease).Allowed {
				t.Fatal("invalid ancestor did not invalidate its descendant")
			}
		})
	}
}

func TestEvaluatorRejectsAncestryCycle(t *testing.T) {
	a := completeArtifact("a", PermissionSet{InternalEval: true})
	b := completeArtifact("b", PermissionSet{InternalEval: true})
	a.AncestryIDs = []string{"b"}
	b.AncestryIDs = []string{"a"}
	evaluator := Evaluator{PolicyVersion: a.PolicyVersion, Artifacts: map[string]Artifact{"a": a, "b": b}}
	decision := evaluator.Evaluate("a", PurposeInternalEval)
	if decision.Allowed || !strings.Contains(strings.Join(decision.BlockingReasons, " "), "cycle") {
		t.Fatalf("ancestry cycle was not rejected: %#v", decision)
	}
}

func TestBuildManifestIsDeterministicAndAtomic(t *testing.T) {
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	permissions := PermissionSet{PublicRelease: true}
	a := completeArtifact("a", permissions)
	b := completeArtifact("b", permissions)
	evaluator := Evaluator{
		PolicyVersion: a.PolicyVersion,
		Artifacts:     map[string]Artifact{"a": a, "b": b},
		Now:           func() time.Time { return now },
	}

	first, err := evaluator.BuildManifest([]string{"b", "a", "a"}, PurposePublicRelease)
	if err != nil {
		t.Fatalf("build manifest: %v", err)
	}
	second, err := evaluator.BuildManifest([]string{"a", "b"}, PurposePublicRelease)
	if err != nil {
		t.Fatalf("build manifest again: %v", err)
	}
	if first.ManifestSHA256 != second.ManifestSHA256 {
		t.Fatalf("manifest is not deterministic: %s != %s", first.ManifestSHA256, second.ManifestSHA256)
	}
	if len(first.Artifacts) != 2 || first.Artifacts[0].ArtifactID != "a" || first.Artifacts[1].ArtifactID != "b" {
		t.Fatalf("manifest ordering is not canonical: %#v", first.Artifacts)
	}

	b.Permissions.PublicRelease = false
	evaluator.Artifacts["b"] = b
	if _, err := evaluator.BuildManifest([]string{"a", "b"}, PurposePublicRelease); err == nil {
		t.Fatal("manifest build must fail atomically when one artifact is denied")
	}
}
