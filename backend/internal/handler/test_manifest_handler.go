package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/service"
	"github.com/Gingoo-TvT/Qraft/backend/internal/workflow/activities"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

const (
	storedTestManifestPayloadVersion   = 3
	storedTestManifestSchemaVersion    = "algoforge.test-manifest.v1"
	storedTestManifestV2PayloadVersion = activities.StoreProblemS3QualityDraftPayloadVersion
	storedTestManifestV2SchemaVersion  = activities.TestManifestSchemaVersionV2
)

type testManifestReference struct {
	SchemaVersion string                  `json:"schema_version"`
	SHA256        string                  `json:"sha256"`
	Path          string                  `json:"path"`
	Artifact      *activities.ArtifactRef `json:"artifact,omitempty"`
}

type getTestManifestProblemFunc func(context.Context, uuid.UUID) (*domain.Problem, error)
type downloadTestManifestFunc func(context.Context, string) ([]byte, error)

func problemTestManifestReference(problem *domain.Problem) (testManifestReference, bool, error) {
	if problem == nil || len(problem.MetadataJSON) == 0 {
		return testManifestReference{}, false, nil
	}
	var metadata struct {
		ActivityPayloadVersion int                    `json:"activity_payload_version"`
		TestManifest           *testManifestReference `json:"test_manifest"`
	}
	if err := json.Unmarshal(problem.MetadataJSON, &metadata); err != nil {
		return testManifestReference{}, false, errors.New("problem metadata is not valid JSON")
	}
	if metadata.TestManifest == nil {
		if metadata.ActivityPayloadVersion >= storedTestManifestPayloadVersion {
			return testManifestReference{}, false, errors.New("versioned problem metadata is missing test_manifest")
		}
		return testManifestReference{}, false, nil
	}
	ref := *metadata.TestManifest
	if metadata.ActivityPayloadVersion < storedTestManifestPayloadVersion || !isCanonicalSHA256(ref.SHA256) {
		return testManifestReference{}, false, errors.New("problem test_manifest reference is inconsistent")
	}
	switch ref.SchemaVersion {
	case storedTestManifestSchemaVersion:
		expectedPath := "problems/" + problem.ID.String() + "/test_manifest.v1.json"
		if metadata.ActivityPayloadVersion >= storedTestManifestV2PayloadVersion || ref.Path != expectedPath || ref.Artifact != nil {
			return testManifestReference{}, false, errors.New("problem test_manifest v1 reference is inconsistent")
		}
	case storedTestManifestV2SchemaVersion:
		expectedPath := "problems/" + problem.ID.String() + "/test_manifest.v2.json"
		if metadata.ActivityPayloadVersion != storedTestManifestV2PayloadVersion || ref.Path != expectedPath || ref.Artifact == nil ||
			ref.Artifact.SHA256 != ref.SHA256 || ref.Artifact.Producer != "BuildS3TestManifestActivityV1" ||
			problem.WorkflowID == nil || ref.Artifact.WorkflowID != *problem.WorkflowID || ref.Artifact.Validate(ref.Artifact.Bucket) != nil {
			return testManifestReference{}, false, errors.New("problem test_manifest v2 reference is inconsistent")
		}
	default:
		return testManifestReference{}, false, errors.New("problem test_manifest schema is unsupported")
	}
	return ref, true, nil
}

func isCanonicalSHA256(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

func testManifestBytesMatch(ref testManifestReference, data []byte) bool {
	digest := sha256.Sum256(data)
	return ref.SHA256 == hex.EncodeToString(digest[:])
}

// HandleGetTestManifest returns the exact immutable manifest bytes referenced
// by problem metadata, so its response body hashes to the EvidenceRef SHA256.
func (h *ProblemHandler) HandleGetTestManifest(c echo.Context) error {
	return handleGetTestManifest(
		c,
		func(ctx context.Context, id uuid.UUID) (*domain.Problem, error) {
			return h.problemService.GetProblem(ctx, id)
		},
		func(ctx context.Context, path string) ([]byte, error) {
			return h.minioClient.DownloadFile(ctx, path)
		},
	)
}

func handleGetTestManifest(
	c echo.Context,
	getProblem getTestManifestProblemFunc,
	download downloadTestManifestFunc,
) error {
	id, err := parseUUID(c, "id")
	if err != nil {
		return err
	}
	problem, err := getProblem(c.Request().Context(), id)
	if err != nil {
		if errors.Is(err, service.ErrNotFound) {
			return notFound(c, "problem not found")
		}
		return internalError(c, "failed to get problem: "+err.Error())
	}
	ref, ok, refErr := problemTestManifestReference(problem)
	if refErr != nil {
		return internalError(c, "stored test manifest reference is inconsistent")
	}
	if !ok {
		return notFound(c, "test manifest not found")
	}
	data, err := download(c.Request().Context(), ref.Path)
	if err != nil {
		return internalError(c, "failed to download test manifest: "+err.Error())
	}
	if !testManifestBytesMatch(ref, data) {
		return internalError(c, "stored test manifest content does not match metadata")
	}
	return c.Blob(http.StatusOK, "application/json", data)
}
