package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/generationapi"
	"github.com/Gingoo-TvT/Qraft/backend/internal/workflow/activities"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

func TestProblemTestManifestReferenceDistinguishesLegacyAbsentFromBrokenV3(t *testing.T) {
	legacy := &domain.Problem{ID: uuid.New(), MetadataJSON: json.RawMessage(`{}`)}
	if _, ok, err := problemTestManifestReference(legacy); err != nil || ok {
		t.Fatalf("legacy manifest reference = ok:%t err:%v", ok, err)
	}

	problemID := uuid.New()
	manifestBody := []byte(`{"schema_version":"algoforge.test-manifest.v1"}`)
	digest := sha256.Sum256(manifestBody)
	ref := testManifestReference{
		SchemaVersion: storedTestManifestSchemaVersion,
		SHA256:        hex.EncodeToString(digest[:]),
		Path:          "problems/" + problemID.String() + "/test_manifest.v1.json",
	}
	metadata, err := json.Marshal(map[string]interface{}{
		"activity_payload_version": storedTestManifestPayloadVersion,
		"test_manifest":            ref,
	})
	if err != nil {
		t.Fatal(err)
	}
	problem := &domain.Problem{ID: problemID, MetadataJSON: metadata}
	got, ok, err := problemTestManifestReference(problem)
	if err != nil || !ok || got != ref {
		t.Fatalf("v3 manifest reference = %+v ok:%t err:%v", got, ok, err)
	}
	if !testManifestBytesMatch(got, manifestBody) || testManifestBytesMatch(got, []byte(`{}`)) {
		t.Fatal("manifest body hash binding is incorrect")
	}

	for name, metadata := range map[string]json.RawMessage{
		"missing":    json.RawMessage(`{"activity_payload_version":3}`),
		"bad schema": json.RawMessage(`{"activity_payload_version":3,"test_manifest":{"schema_version":"wrong","sha256":"` + strings.Repeat("a", 64) + `","path":"problems/` + problemID.String() + `/test_manifest.v1.json"}}`),
		"bad sha":    json.RawMessage(`{"activity_payload_version":3,"test_manifest":{"schema_version":"algoforge.test-manifest.v1","sha256":"short","path":"problems/` + problemID.String() + `/test_manifest.v1.json"}}`),
		"bad path":   json.RawMessage(`{"activity_payload_version":3,"test_manifest":{"schema_version":"algoforge.test-manifest.v1","sha256":"` + strings.Repeat("a", 64) + `","path":"problems/other/test_manifest.v1.json"}}`),
	} {
		t.Run(name, func(t *testing.T) {
			broken := &domain.Problem{ID: problemID, MetadataJSON: metadata}
			if _, _, err := problemTestManifestReference(broken); err == nil {
				t.Fatal("broken v3 manifest metadata was silently hidden")
			}
		})
	}
}

func TestCanonicalSHA256Validation(t *testing.T) {
	if !isCanonicalSHA256(strings.Repeat("a", 64)) {
		t.Fatal("canonical digest rejected")
	}
	for _, value := range []string{strings.Repeat("A", 64), strings.Repeat("g", 64), "short"} {
		if isCanonicalSHA256(value) {
			t.Fatalf("non-canonical digest accepted: %q", value)
		}
	}
}

func TestProblemTestManifestReferencePreservesS3V2ArtifactRef(t *testing.T) {
	problemID := uuid.New()
	workflowID := generationapi.JobIDPrefix + strings.Repeat("c", 64)
	digest := strings.Repeat("d", 64)
	artifact := activities.ArtifactRef{
		SchemaVersion:  activities.ArtifactRefSchemaVersion,
		PayloadVersion: activities.ActivityPayloadVersion,
		Bucket:         "quality-artifacts",
		Key:            "workflow-artifacts/v1/sha256/dd/" + digest,
		SHA256:         digest,
		SizeBytes:      123,
		ContentType:    "application/json",
		Producer:       "BuildS3TestManifestActivityV1",
		Provider:       "algoforge",
		Model:          "not_applicable",
		ModelRevision:  "not_applicable",
		WorkflowID:     workflowID,
	}
	ref := testManifestReference{
		SchemaVersion: storedTestManifestV2SchemaVersion,
		SHA256:        digest,
		Path:          "problems/" + problemID.String() + "/test_manifest.v2.json",
		Artifact:      &artifact,
	}
	metadata, err := json.Marshal(map[string]interface{}{
		"activity_payload_version": storedTestManifestV2PayloadVersion,
		"test_manifest":            ref,
	})
	if err != nil {
		t.Fatal(err)
	}
	problem := &domain.Problem{ID: problemID, WorkflowID: &workflowID, MetadataJSON: metadata}

	got, ok, err := problemTestManifestReference(problem)
	if err != nil || !ok || got.Artifact == nil || *got.Artifact != artifact {
		t.Fatalf("S3 TestManifest v2 reference = %+v ok:%t err:%v", got, ok, err)
	}

	for name, mutate := range map[string]func(*testManifestReference){
		"missing artifact": func(value *testManifestReference) { value.Artifact = nil },
		"wrong producer":   func(value *testManifestReference) { value.Artifact.Producer = "legacy" },
		"wrong workflow": func(value *testManifestReference) {
			value.Artifact.WorkflowID = generationapi.JobIDPrefix + strings.Repeat("e", 64)
		},
		"wrong digest": func(value *testManifestReference) { value.Artifact.SHA256 = strings.Repeat("f", 64) },
	} {
		t.Run(name, func(t *testing.T) {
			broken := ref
			artifactCopy := artifact
			broken.Artifact = &artifactCopy
			mutate(&broken)
			brokenMetadata, marshalErr := json.Marshal(map[string]interface{}{
				"activity_payload_version": storedTestManifestV2PayloadVersion,
				"test_manifest":            broken,
			})
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			brokenProblem := &domain.Problem{ID: problemID, WorkflowID: &workflowID, MetadataJSON: brokenMetadata}
			if _, _, refErr := problemTestManifestReference(brokenProblem); refErr == nil {
				t.Fatal("broken S3 TestManifest v2 reference was accepted")
			}
		})
	}
}

func TestHandleGetTestManifestHTTPContract(t *testing.T) {
	problemID := uuid.New()
	manifestBody := []byte(`{"schema_version":"algoforge.test-manifest.v1","test_count":1}`)
	digest := sha256.Sum256(manifestBody)
	manifestPath := "problems/" + problemID.String() + "/test_manifest.v1.json"
	metadata, err := json.Marshal(map[string]interface{}{
		"activity_payload_version": storedTestManifestPayloadVersion,
		"test_manifest": testManifestReference{
			SchemaVersion: storedTestManifestSchemaVersion,
			SHA256:        hex.EncodeToString(digest[:]),
			Path:          manifestPath,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	problem := &domain.Problem{ID: problemID, MetadataJSON: metadata}

	tests := []struct {
		name       string
		download   downloadTestManifestFunc
		wantStatus int
		wantBody   []byte
	}{
		{
			name: "returns exact canonical bytes",
			download: func(_ context.Context, path string) ([]byte, error) {
				if path != manifestPath {
					t.Fatalf("download path = %q, want %q", path, manifestPath)
				}
				return manifestBody, nil
			},
			wantStatus: http.StatusOK,
			wantBody:   manifestBody,
		},
		{
			name: "rejects sha mismatch",
			download: func(context.Context, string) ([]byte, error) {
				return []byte(`{}`), nil
			},
			wantStatus: http.StatusInternalServerError,
		},
		{
			name: "reports download failure",
			download: func(context.Context, string) ([]byte, error) {
				return nil, errors.New("object unavailable")
			},
			wantStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := echo.New()
			req := httptest.NewRequest(http.MethodGet, "/api/v1/problems/"+problemID.String()+"/test-manifest", nil)
			recorder := httptest.NewRecorder()
			ctx := e.NewContext(req, recorder)
			ctx.SetPath("/api/v1/problems/:id/test-manifest")
			ctx.SetParamNames("id")
			ctx.SetParamValues(problemID.String())

			getProblem := func(_ context.Context, id uuid.UUID) (*domain.Problem, error) {
				if id != problemID {
					t.Fatalf("problem id = %s, want %s", id, problemID)
				}
				return problem, nil
			}
			if err := handleGetTestManifest(ctx, getProblem, tt.download); err != nil {
				t.Fatalf("handle test manifest: %v", err)
			}
			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, tt.wantStatus, recorder.Body.String())
			}

			if tt.wantStatus == http.StatusOK {
				if got := recorder.Header().Get(echo.HeaderContentType); got != "application/json" {
					t.Fatalf("content type = %q, want application/json", got)
				}
				if recorder.Body.String() != string(tt.wantBody) {
					t.Fatalf("response body = %q, want exact %q", recorder.Body.Bytes(), tt.wantBody)
				}
				return
			}

			var response APIResponse
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatalf("decode error response: %v", err)
			}
			if response.Success || response.Error == nil || response.Error.Code != "INTERNAL_ERROR" {
				t.Fatalf("error response = %+v", response)
			}
		})
	}
}
