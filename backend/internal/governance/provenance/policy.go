package provenance

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Purpose is a separately governed use of an artifact.
type Purpose string

const (
	PurposeInternalEval    Purpose = "internal_eval"
	PurposePrivateTraining Purpose = "private_training"
	PurposePublicRelease   Purpose = "public_release"
)

var purposes = []Purpose{
	PurposeInternalEval,
	PurposePrivateTraining,
	PurposePublicRelease,
}

// PermissionSet records the direct decision for each governed purpose. The
// zero value denies every purpose.
type PermissionSet struct {
	InternalEval    bool `json:"internal_eval_allowed"`
	PrivateTraining bool `json:"private_training_allowed"`
	PublicRelease   bool `json:"public_release_allowed"`
}

func (p PermissionSet) Allows(purpose Purpose) bool {
	switch purpose {
	case PurposeInternalEval:
		return p.InternalEval
	case PurposePrivateTraining:
		return p.PrivateTraining
	case PurposePublicRelease:
		return p.PublicRelease
	default:
		return false
	}
}

// Artifact is the minimum provenance record required before an artifact can
// leave quarantine. Fields that do not apply must use an explicit value such
// as "not_applicable" rather than being omitted.
type Artifact struct {
	ArtifactID        string        `json:"artifact_id"`
	ArtifactType      string        `json:"artifact_type"`
	ContentHash       string        `json:"content_hash"`
	SourceType        string        `json:"source_type"`
	SourceURI         string        `json:"source_uri"`
	SourceRevision    string        `json:"source_revision"`
	Creator           string        `json:"creator"`
	Provider          string        `json:"provider"`
	Model             string        `json:"model"`
	ModelRevision     string        `json:"model_revision"`
	GeneratedAt       time.Time     `json:"generated_at"`
	LicenseBasis      string        `json:"license_basis"`
	TermsSnapshotHash string        `json:"terms_snapshot_hash"`
	AncestryIDs       []string      `json:"ancestry_ids"`
	RetentionClass    string        `json:"retention_class"`
	RetentionExpires  *time.Time    `json:"retention_expires_at,omitempty"`
	TakedownStatus    string        `json:"takedown_status"`
	PolicyVersion     string        `json:"policy_version"`
	Permissions       PermissionSet `json:"permissions"`
}

// Decision is the effective result after applying direct permissions,
// retention/takedown state, and every transitive ancestor restriction.
type Decision struct {
	Allowed         bool     `json:"allowed"`
	Purpose         Purpose  `json:"purpose"`
	PolicyVersion   string   `json:"policy_version"`
	BlockingIDs     []string `json:"blocking_artifact_ids,omitempty"`
	BlockingReasons []string `json:"blocking_reasons,omitempty"`
}

// Evaluator applies a single immutable policy version to an artifact graph.
type Evaluator struct {
	PolicyVersion string
	Artifacts     map[string]Artifact
	Now           func() time.Time
}

func (e Evaluator) Evaluate(artifactID string, purpose Purpose) Decision {
	result := Decision{Purpose: purpose, PolicyVersion: e.PolicyVersion}
	if !validPurpose(purpose) {
		result.BlockingReasons = []string{"unknown purpose"}
		return result
	}
	if strings.TrimSpace(e.PolicyVersion) == "" {
		result.BlockingReasons = []string{"missing evaluator policy version"}
		return result
	}

	now := time.Now().UTC()
	if e.Now != nil {
		now = e.Now().UTC()
	}

	visiting := make(map[string]bool)
	visited := make(map[string]bool)
	var blockingIDs []string
	var reasons []string
	var visit func(string)
	visit = func(id string) {
		if visited[id] {
			return
		}
		if visiting[id] {
			blockingIDs = append(blockingIDs, id)
			reasons = append(reasons, fmt.Sprintf("%s: ancestry cycle", id))
			return
		}
		artifact, ok := e.Artifacts[id]
		if !ok {
			blockingIDs = append(blockingIDs, id)
			reasons = append(reasons, fmt.Sprintf("%s: missing provenance record", id))
			return
		}

		visiting[id] = true
		for _, reason := range validateArtifact(artifact, e.PolicyVersion, purpose, now) {
			blockingIDs = append(blockingIDs, id)
			reasons = append(reasons, fmt.Sprintf("%s: %s", id, reason))
		}
		for _, parentID := range artifact.AncestryIDs {
			visit(parentID)
		}
		visiting[id] = false
		visited[id] = true
	}

	visit(artifactID)
	result.BlockingIDs = uniqueSorted(blockingIDs)
	result.BlockingReasons = uniqueSorted(reasons)
	result.Allowed = len(result.BlockingReasons) == 0
	return result
}

func validateArtifact(a Artifact, policyVersion string, purpose Purpose, now time.Time) []string {
	var reasons []string
	required := map[string]string{
		"artifact_id":         a.ArtifactID,
		"artifact_type":       a.ArtifactType,
		"content_hash":        a.ContentHash,
		"source_type":         a.SourceType,
		"source_uri":          a.SourceURI,
		"source_revision":     a.SourceRevision,
		"creator":             a.Creator,
		"provider":            a.Provider,
		"model":               a.Model,
		"model_revision":      a.ModelRevision,
		"license_basis":       a.LicenseBasis,
		"terms_snapshot_hash": a.TermsSnapshotHash,
		"retention_class":     a.RetentionClass,
		"takedown_status":     a.TakedownStatus,
		"policy_version":      a.PolicyVersion,
	}
	for field, value := range required {
		if strings.TrimSpace(value) == "" {
			reasons = append(reasons, "missing "+field)
		}
	}
	if a.GeneratedAt.IsZero() {
		reasons = append(reasons, "missing generated_at")
	}
	for field, value := range map[string]string{
		"content_hash":        a.ContentHash,
		"terms_snapshot_hash": a.TermsSnapshotHash,
	} {
		if value != "" && !isSHA256(value) {
			reasons = append(reasons, field+" must be a lowercase SHA-256 hex digest")
		}
	}
	if a.PolicyVersion != "" && a.PolicyVersion != policyVersion {
		reasons = append(reasons, "policy version mismatch")
	}
	if a.TakedownStatus != "active" {
		reasons = append(reasons, "takedown status is not active")
	}
	if a.RetentionExpires != nil && !a.RetentionExpires.After(now) {
		reasons = append(reasons, "retention expired")
	}
	if !a.Permissions.Allows(purpose) {
		reasons = append(reasons, fmt.Sprintf("%s is not allowed", purpose))
	}
	return reasons
}

func validPurpose(p Purpose) bool {
	for _, candidate := range purposes {
		if p == candidate {
			return true
		}
	}
	return false
}

func isSHA256(value string) bool {
	if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func uniqueSorted(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

// Manifest is a deterministic, machine-readable record of an approved use.
type Manifest struct {
	SchemaVersion  string     `json:"schema_version"`
	PolicyVersion  string     `json:"policy_version"`
	Purpose        Purpose    `json:"purpose"`
	CreatedAt      time.Time  `json:"created_at"`
	Artifacts      []Artifact `json:"artifacts"`
	ManifestSHA256 string     `json:"manifest_sha256"`
}

// BuildManifest refuses the complete build when any requested artifact or
// ancestor is not eligible for the requested purpose.
func (e Evaluator) BuildManifest(artifactIDs []string, purpose Purpose) (Manifest, error) {
	if len(artifactIDs) == 0 {
		return Manifest{}, errors.New("manifest must contain at least one artifact")
	}
	ids := uniqueSorted(artifactIDs)
	artifacts := make([]Artifact, 0, len(ids))
	for _, id := range ids {
		decision := e.Evaluate(id, purpose)
		if !decision.Allowed {
			return Manifest{}, fmt.Errorf("artifact %s is not eligible for %s: %s", id, purpose, strings.Join(decision.BlockingReasons, "; "))
		}
		artifacts = append(artifacts, e.Artifacts[id])
	}
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].ArtifactID < artifacts[j].ArtifactID })

	now := time.Now().UTC()
	if e.Now != nil {
		now = e.Now().UTC()
	}
	manifest := Manifest{
		SchemaVersion: "1.0.0",
		PolicyVersion: e.PolicyVersion,
		Purpose:       purpose,
		CreatedAt:     now,
		Artifacts:     artifacts,
	}
	canonical, err := json.Marshal(manifest)
	if err != nil {
		return Manifest{}, fmt.Errorf("marshal manifest: %w", err)
	}
	digest := sha256.Sum256(canonical)
	manifest.ManifestSHA256 = hex.EncodeToString(digest[:])
	return manifest, nil
}
