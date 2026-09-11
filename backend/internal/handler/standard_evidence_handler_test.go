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
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

func standardEvidenceTestContract(t *testing.T) *domain.GenerationEvidenceContract {
	t.Helper()
	body := generationJobTestRequestBody(t)
	var request generationapi.Request
	if err := json.Unmarshal([]byte(body), &request); err != nil {
		t.Fatal(err)
	}
	request.Output.EvidenceLevel = generationapi.EvidenceStandard
	contract := request.ToProblemGenParams().GenerationEvidence
	if contract == nil {
		t.Fatal("standard fixture did not produce evidence contract")
	}
	return contract
}

func TestProblemStandardEvidenceReferenceDistinguishesMinimalFromBrokenStandard(t *testing.T) {
	minimal := &domain.Problem{ID: uuid.New(), MetadataJSON: json.RawMessage(`{}`)}
	if _, ok, err := problemStandardEvidenceReference(minimal, nil); err != nil || ok {
		t.Fatalf("minimal standard evidence reference = ok:%t err:%v", ok, err)
	}

	problemID := uuid.New()
	body := []byte(`{"schema_version":"algoforge.generation-standard-evidence.v1"}`)
	digest := sha256.Sum256(body)
	ref := domain.GenerationStandardEvidenceReference{
		SchemaVersion: domain.GenerationStandardEvidenceSchemaV1,
		SHA256:        hex.EncodeToString(digest[:]),
		Path:          "problems/" + problemID.String() + "/generation_standard_evidence.v1.json",
	}
	metadata, err := json.Marshal(map[string]interface{}{
		domain.GenerationStandardEvidenceRequestMetaKey: standardEvidenceTestContract(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	problem := &domain.Problem{ID: problemID, MetadataJSON: metadata}
	binding := &domain.GenerationStandardEvidenceBinding{
		Reference:        ref,
		FinalStatus:      domain.ProblemStatusQuarantined,
		QuarantineReason: "quality review denied",
		OutcomeCategory:  string(generationapi.OutcomeCategoryReview),
		OutcomeKind:      "review_result",
		OutcomeSHA256:    strings.Repeat("b", 64),
	}
	got, ok, err := problemStandardEvidenceReference(problem, binding)
	if err != nil || !ok || got != ref {
		t.Fatalf("standard evidence reference = %+v ok:%t err:%v", got, ok, err)
	}
	if !standardEvidenceBytesMatch(got, body) || standardEvidenceBytesMatch(got, []byte(`{}`)) {
		t.Fatal("standard evidence body hash binding is incorrect")
	}

	if _, _, err := problemStandardEvidenceReference(problem, nil); err == nil {
		t.Fatal("standard request without binding was accepted")
	}
	if _, _, err := problemStandardEvidenceReference(minimal, binding); err == nil {
		t.Fatal("binding without standard request was accepted")
	}
	brokenBinding := *binding
	brokenBinding.Reference.SHA256 = strings.Repeat("A", 64)
	if _, _, err := problemStandardEvidenceReference(problem, &brokenBinding); err == nil {
		t.Fatal("invalid standard evidence binding was accepted")
	}
}

func TestHandleGetStandardEvidenceHTTPContract(t *testing.T) {
	problemID := uuid.New()
	body := []byte(`{"schema_version":"algoforge.generation-standard-evidence.v1","final_outcome":{"job_status":"succeeded"}}`)
	digest := sha256.Sum256(body)
	path := "problems/" + problemID.String() + "/generation_standard_evidence.v1.json"
	metadata, err := json.Marshal(map[string]interface{}{
		domain.GenerationStandardEvidenceRequestMetaKey: standardEvidenceTestContract(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	problem := &domain.Problem{ID: problemID, MetadataJSON: metadata}
	binding := domain.GenerationStandardEvidenceBinding{
		Reference: domain.GenerationStandardEvidenceReference{
			SchemaVersion: domain.GenerationStandardEvidenceSchemaV1,
			SHA256:        hex.EncodeToString(digest[:]),
			Path:          path,
		},
		FinalStatus:     domain.ProblemStatusPublished,
		OutcomeCategory: string(generationapi.OutcomeCategoryPublicationEligibility),
		OutcomeKind:     "publication_decision",
		OutcomeSHA256:   strings.Repeat("b", 64),
	}

	for _, test := range []struct {
		name       string
		download   downloadStandardEvidenceFunc
		wantStatus int
		wantBody   []byte
	}{
		{
			name: "returns exact receipt bytes",
			download: func(_ context.Context, gotPath string) ([]byte, error) {
				if gotPath != path {
					t.Fatalf("download path = %q, want %q", gotPath, path)
				}
				return body, nil
			},
			wantStatus: http.StatusOK,
			wantBody:   body,
		},
		{name: "rejects sha mismatch", download: func(context.Context, string) ([]byte, error) { return []byte(`{}`), nil }, wantStatus: http.StatusInternalServerError},
		{name: "reports download failure", download: func(context.Context, string) ([]byte, error) { return nil, errors.New("object unavailable") }, wantStatus: http.StatusInternalServerError},
	} {
		t.Run(test.name, func(t *testing.T) {
			e := echo.New()
			req := httptest.NewRequest(http.MethodGet, "/api/v1/problems/"+problemID.String()+"/standard-evidence", nil)
			recorder := httptest.NewRecorder()
			ctx := e.NewContext(req, recorder)
			ctx.SetPath("/api/v1/problems/:id/standard-evidence")
			ctx.SetParamNames("id")
			ctx.SetParamValues(problemID.String())
			getProblem := func(context.Context, uuid.UUID) (*domain.Problem, error) { return problem, nil }
			getBinding := func(context.Context, uuid.UUID) (domain.GenerationStandardEvidenceBinding, bool, error) {
				return binding, true, nil
			}
			if err := handleGetStandardEvidence(ctx, getProblem, getBinding, test.download); err != nil {
				t.Fatal(err)
			}
			if recorder.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, test.wantStatus, recorder.Body.String())
			}
			if test.wantStatus == http.StatusOK && recorder.Body.String() != string(test.wantBody) {
				t.Fatalf("body = %q, want %q", recorder.Body.Bytes(), test.wantBody)
			}
		})
	}
}

func mustJSON(t *testing.T, value interface{}) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
