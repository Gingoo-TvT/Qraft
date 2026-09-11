package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/qualityapi"
	"github.com/Gingoo-TvT/Qraft/backend/internal/service"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

type fakeProblemQualityReader struct {
	report      *service.ProblemQualityDocumentV1
	audit       *service.ProblemQualityAuditDocumentV1
	batch       *service.ProblemQualityBatchDocumentV1
	err         error
	reportCalls int
	auditCalls  int
	batchCalls  int
	gotID       uuid.UUID
	gotBatch    qualityapi.ProblemQualityBatchRequestV1
}

func (fake *fakeProblemQualityReader) GetProblemQuality(_ context.Context, id uuid.UUID) (*service.ProblemQualityDocumentV1, error) {
	fake.reportCalls++
	fake.gotID = id
	return fake.report, fake.err
}

func (fake *fakeProblemQualityReader) GetProblemQualityAudit(_ context.Context, id uuid.UUID) (*service.ProblemQualityAuditDocumentV1, error) {
	fake.auditCalls++
	fake.gotID = id
	return fake.audit, fake.err
}

func (fake *fakeProblemQualityReader) BuildProblemQualityBatchManifest(_ context.Context, request qualityapi.ProblemQualityBatchRequestV1) (*service.ProblemQualityBatchDocumentV1, error) {
	fake.batchCalls++
	fake.gotBatch = request
	return fake.batch, fake.err
}

func TestProblemQualityHandlerReturnsCanonicalReportAuditAndBatchBytes(t *testing.T) {
	problemID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	auditSHA := strings.Repeat("a", 64)
	manifestSHA := strings.Repeat("b", 64)
	reportBytes := []byte(`{"schema_version":"algoforge.problem-quality-report.v1"}`)
	auditBytes := []byte(`{"schema_version":"algoforge.s3-quality-audit.v1"}`)
	batchBytes := []byte(`{"schema_version":"algoforge.problem-quality-batch-manifest.v1"}`)
	fake := &fakeProblemQualityReader{
		report: &service.ProblemQualityDocumentV1{
			Bytes: reportBytes, SHA256: qualityapi.SHA256Hex(reportBytes),
			Report: qualityapi.ProblemQualityReportV1{Evidence: qualityapi.QualityEvidenceV1{
				Audit: qualityapi.EvidenceBindingV1{SHA256: auditSHA}, TestManifest: qualityapi.EvidenceBindingV1{SHA256: manifestSHA},
			}},
		},
		audit: &service.ProblemQualityAuditDocumentV1{Bytes: auditBytes, SHA256: auditSHA},
		batch: &service.ProblemQualityBatchDocumentV1{Bytes: batchBytes, SHA256: qualityapi.SHA256Hex(batchBytes)},
	}
	handler := NewProblemQualityHandler(fake)

	t.Run("quality", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		ctx := qualityHandlerContext(http.MethodGet, "/api/v1/problems/"+problemID.String()+"/quality", nil, recorder, problemID)
		if err := handler.HandleGetQuality(ctx); err != nil {
			t.Fatal(err)
		}
		if recorder.Code != http.StatusOK || recorder.Body.String() != string(reportBytes) || fake.gotID != problemID {
			t.Fatalf("quality response = %d %q id=%s", recorder.Code, recorder.Body.String(), fake.gotID)
		}
		if recorder.Header().Get("X-AlgoForge-Quality-Report-SHA256") != qualityapi.SHA256Hex(reportBytes) ||
			recorder.Header().Get("X-AlgoForge-Quality-Audit-SHA256") != auditSHA ||
			recorder.Header().Get("X-AlgoForge-Test-Manifest-SHA256") != manifestSHA ||
			recorder.Header().Get("X-AlgoForge-External-OJ-Import-Verified") != "false" {
			t.Fatalf("quality response headers = %+v", recorder.Header())
		}
	})

	t.Run("audit", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		ctx := qualityHandlerContext(http.MethodGet, "/api/v1/problems/"+problemID.String()+"/quality/audit", nil, recorder, problemID)
		if err := handler.HandleGetAudit(ctx); err != nil {
			t.Fatal(err)
		}
		if recorder.Code != http.StatusOK || recorder.Body.String() != string(auditBytes) || recorder.Header().Get("X-AlgoForge-Quality-Audit-SHA256") != auditSHA {
			t.Fatalf("audit response = %d %q headers=%+v", recorder.Code, recorder.Body.String(), recorder.Header())
		}
	})

	t.Run("batch", func(t *testing.T) {
		body := `{"schema_version":"` + qualityapi.ProblemQualityBatchRequestSchemaV1 + `","problem_ids":["` + problemID.String() + `"]}`
		recorder := httptest.NewRecorder()
		ctx := qualityHandlerContext(http.MethodPost, "/api/v1/problems/quality/batch-manifest", strings.NewReader(body), recorder, uuid.Nil)
		if err := handler.HandleBuildBatchManifest(ctx); err != nil {
			t.Fatal(err)
		}
		if recorder.Code != http.StatusOK || recorder.Body.String() != string(batchBytes) || fake.batchCalls != 1 ||
			fake.gotBatch.SchemaVersion != qualityapi.ProblemQualityBatchRequestSchemaV1 || len(fake.gotBatch.ProblemIDs) != 1 {
			t.Fatalf("batch response=%d %q calls=%d request=%+v", recorder.Code, recorder.Body.String(), fake.batchCalls, fake.gotBatch)
		}
		if recorder.Header().Get("X-AlgoForge-Quality-Batch-Manifest-SHA256") != qualityapi.SHA256Hex(batchBytes) ||
			recorder.Header().Get("X-AlgoForge-External-OJ-Import-Verified") != "false" {
			t.Fatalf("batch headers = %+v", recorder.Header())
		}
	})
}

func TestProblemQualityBatchHandlerRejectsUnknownDuplicateTrailingUnorderedAndOversize(t *testing.T) {
	firstID := "11111111-1111-1111-1111-111111111111"
	secondID := "22222222-2222-2222-2222-222222222222"
	prefix := `{"schema_version":"` + qualityapi.ProblemQualityBatchRequestSchemaV1 + `",`
	tests := map[string]string{
		"missing schema": `{ "problem_ids":["` + firstID + `"]}`,
		"wrong schema":   `{"schema_version":"wrong","problem_ids":["` + firstID + `"]}`,
		"unknown":        prefix + `"problem_ids":["` + firstID + `"],"unknown":true}`,
		"duplicate key":  prefix + `"problem_ids":["` + firstID + `"],"problem_ids":["` + secondID + `"]}`,
		"trailing":       prefix + `"problem_ids":["` + firstID + `"]} {}`,
		"duplicate id":   prefix + `"problem_ids":["` + firstID + `","` + firstID + `"]}`,
		"unordered":      prefix + `"problem_ids":["` + secondID + `","` + firstID + `"]}`,
		"oversize":       prefix + `"problem_ids":["` + firstID + `"],"` + strings.Repeat("x", qualityapi.MaxBatchRequestBytesV1) + `":0}`,
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			fake := &fakeProblemQualityReader{}
			recorder := httptest.NewRecorder()
			ctx := qualityHandlerContext(http.MethodPost, "/api/v1/problems/quality/batch-manifest", strings.NewReader(body), recorder, uuid.Nil)
			if err := NewProblemQualityHandler(fake).HandleBuildBatchManifest(ctx); err != nil {
				t.Fatal(err)
			}
			if recorder.Code != http.StatusBadRequest || fake.batchCalls != 0 {
				t.Fatalf("invalid request response=%d body=%s calls=%d", recorder.Code, recorder.Body.String(), fake.batchCalls)
			}
		})
	}
}

func TestProblemQualityHandlerMapsNotFoundConflictAndInternal(t *testing.T) {
	problemID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	for _, test := range []struct {
		name string
		err  error
		want int
	}{
		{"not found", service.ErrNotFound, http.StatusNotFound},
		{"conflict", service.ErrConflict, http.StatusConflict},
		{"internal", errors.New("object unavailable"), http.StatusInternalServerError},
	} {
		t.Run(test.name, func(t *testing.T) {
			fake := &fakeProblemQualityReader{err: test.err}
			recorder := httptest.NewRecorder()
			ctx := qualityHandlerContext(http.MethodGet, "/api/v1/problems/"+problemID.String()+"/quality", nil, recorder, problemID)
			if err := NewProblemQualityHandler(fake).HandleGetQuality(ctx); err != nil {
				t.Fatal(err)
			}
			if recorder.Code != test.want {
				t.Fatalf("status=%d want=%d body=%s", recorder.Code, test.want, recorder.Body.String())
			}
		})
	}
}

func TestRegisterProblemQualityRoutes(t *testing.T) {
	e := echo.New()
	RegisterProblemQualityRoutes(e.Group("/api/v1"), NewProblemQualityHandler(&fakeProblemQualityReader{}))
	want := map[string]bool{
		http.MethodPost + " /api/v1/problems/quality/batch-manifest": false,
		http.MethodGet + " /api/v1/problems/:id/quality/audit":       false,
		http.MethodGet + " /api/v1/problems/:id/quality":             false,
	}
	for _, route := range e.Routes() {
		key := route.Method + " " + route.Path
		if _, ok := want[key]; ok {
			want[key] = true
		}
	}
	for route, found := range want {
		if !found {
			t.Fatalf("route %s was not registered", route)
		}
	}
}

func qualityHandlerContext(method, target string, body *strings.Reader, recorder *httptest.ResponseRecorder, problemID uuid.UUID) echo.Context {
	e := echo.New()
	var request *http.Request
	if body == nil {
		request = httptest.NewRequest(method, target, nil)
	} else {
		request = httptest.NewRequest(method, target, body)
		request.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	}
	ctx := e.NewContext(request, recorder)
	if problemID != uuid.Nil {
		ctx.SetParamNames("id")
		ctx.SetParamValues(problemID.String())
	}
	return ctx
}
