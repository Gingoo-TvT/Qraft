package activities

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
)

// These fields deliberately mirror the small metadata reference written by
// StoreProblemActivity.  The manifest itself remains in MinIO/CAS; only its
// selected case indexes cross the activity boundary.
type storedTestManifestReference struct {
	SchemaVersion string       `json:"schema_version"`
	SHA256        string       `json:"sha256"`
	Path          string       `json:"path"`
	Artifact      *ArtifactRef `json:"artifact,omitempty"`
}

type storedProblemManifestMetadata struct {
	ActivityPayloadVersion int                          `json:"activity_payload_version"`
	TestManifest           *storedTestManifestReference `json:"test_manifest"`
}

// resolveValidationBruteSelection reads and verifies the persisted test
// manifest.  A v1 manifest is the source of truth for the bounded differential
// subset.  A v2 manifest is backed by an independent oracle and intentionally
// does not schedule the legacy brute program.  Pre-manifest problems return an
// empty schema so the workflow can use its conservative samples-only fallback.
func (a *Activities) resolveValidationBruteSelection(
	ctx context.Context,
	problem *domain.Problem,
	testCount int,
) ([]int, string, error) {
	if problem == nil {
		return nil, "", fmt.Errorf("problem is nil")
	}
	if testCount <= 0 {
		return nil, "", fmt.Errorf("test case count must be positive, got %d", testCount)
	}
	if len(problem.MetadataJSON) == 0 {
		return nil, "", nil
	}

	var metadata storedProblemManifestMetadata
	if err := json.Unmarshal(problem.MetadataJSON, &metadata); err != nil {
		return nil, "", fmt.Errorf("problem metadata is not valid JSON: %w", err)
	}
	if metadata.TestManifest == nil {
		if metadata.ActivityPayloadVersion >= StoreProblemTestManifestPayloadVersion {
			return nil, "", fmt.Errorf("versioned problem metadata is missing test_manifest")
		}
		return nil, "", nil
	}
	if metadata.ActivityPayloadVersion < StoreProblemTestManifestPayloadVersion {
		return nil, "", fmt.Errorf("test_manifest is present but activity payload version is %d", metadata.ActivityPayloadVersion)
	}

	ref := metadata.TestManifest
	if !isManifestSHA256(ref.SHA256) {
		return nil, "", fmt.Errorf("test_manifest sha256 is not canonical")
	}
	if ref.Artifact != nil && ref.SchemaVersion == TestManifestSchemaVersion {
		return nil, "", fmt.Errorf("v1 test_manifest must not carry an artifact reference")
	}

	switch ref.SchemaVersion {
	case TestManifestSchemaVersion:
		expectedPath := fmt.Sprintf("problems/%s/test_manifest.v1.json", problem.ID.String())
		if ref.Path != expectedPath {
			return nil, "", fmt.Errorf("v1 test_manifest path %q does not match %q", ref.Path, expectedPath)
		}
		data, err := a.downloadFromMinIO(ctx, ref.Path)
		if err != nil {
			return nil, "", fmt.Errorf("downloading v1 test_manifest: %w", err)
		}
		if digest := manifestSHA256([]byte(data)); digest != ref.SHA256 {
			return nil, "", fmt.Errorf("v1 test_manifest sha256 mismatch: got %s want %s", digest, ref.SHA256)
		}
		manifest, err := ParseTestManifestJSON([]byte(data))
		if err != nil {
			return nil, "", fmt.Errorf("parsing v1 test_manifest: %w", err)
		}
		if manifest.TestCount != testCount {
			return nil, "", fmt.Errorf("v1 test_manifest has %d tests, database has %d", manifest.TestCount, testCount)
		}
		indices := make([]int, 0, manifest.DifferentialCheckedCount)
		for index, item := range manifest.Cases {
			if item.DifferentialChecked {
				indices = append(indices, index)
			}
		}
		return indices, TestManifestSchemaVersion, nil

	case TestManifestSchemaVersionV2:
		expectedPath := fmt.Sprintf("problems/%s/test_manifest.v2.json", problem.ID.String())
		if ref.Path != expectedPath {
			return nil, "", fmt.Errorf("v2 test_manifest path %q does not match %q", ref.Path, expectedPath)
		}
		if ref.Artifact == nil {
			return nil, "", fmt.Errorf("v2 test_manifest is missing its artifact reference")
		}
		data, err := a.downloadFromMinIO(ctx, ref.Path)
		if err != nil {
			return nil, "", fmt.Errorf("downloading v2 test_manifest: %w", err)
		}
		if digest := manifestSHA256([]byte(data)); digest != ref.SHA256 {
			return nil, "", fmt.Errorf("v2 test_manifest sha256 mismatch: got %s want %s", digest, ref.SHA256)
		}
		manifest, err := ParseTestManifestV2JSON([]byte(data))
		if err != nil {
			return nil, "", fmt.Errorf("parsing v2 test_manifest: %w", err)
		}
		if manifest.TestCount != testCount {
			return nil, "", fmt.Errorf("v2 test_manifest has %d tests, database has %d", manifest.TestCount, testCount)
		}
		// v2's independent-oracle receipt is authoritative.  Running a legacy
		// brute implementation here would mix two evidence protocols and could
		// reintroduce the full-suite TLE that this selection is meant to prevent.
		return nil, TestManifestSchemaVersionV2, nil

	default:
		return nil, "", fmt.Errorf("unsupported test_manifest schema %q", ref.SchemaVersion)
	}
}
