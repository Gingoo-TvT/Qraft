package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/sources"
	"github.com/labstack/echo/v4"
)

type fakeSourceFetcher struct {
	called   int
	document *sources.Document
	err      error
}

func (f *fakeSourceFetcher) Fetch(context.Context, string) (*sources.Document, error) {
	f.called++
	return f.document, f.err
}

func TestSourcePreviewUsesStandardEnvelopeWithoutImporting(t *testing.T) {
	fetcher := &fakeSourceFetcher{document: &sources.Document{URL: "https://fixture.example/p", FinalURL: "https://fixture.example/p", Title: "Fixture", Kind: sources.KindProblem, Items: []sources.Item{{ID: "one", URL: "https://fixture.example/p", Title: "Fixture", Statement: "Compute a+b."}}}}
	e := echo.New()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sources/preview", strings.NewReader(`{"url":"https://fixture.example/p"}`))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	if err := NewSourceHandler(fetcher).HandlePreview(e.NewContext(req, rec)); err != nil {
		t.Fatal(err)
	}
	var response struct {
		Success bool             `json:"success"`
		Data    sources.Document `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusOK || !response.Success || len(response.Data.Items) != 1 || fetcher.called != 1 {
		t.Fatalf("response=%s calls=%d", rec.Body.String(), fetcher.called)
	}
}

func TestSourcePreviewErrorsDoNotBecomeAccountAuthenticationFailures(t *testing.T) {
	fetcher := &fakeSourceFetcher{err: &sources.Error{Code: "SOURCE_ACCESS_DENIED", Message: "来源要求登录；也可以粘贴题面继续。"}}
	e := echo.New()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sources/preview", strings.NewReader(`{"url":"https://fixture.example/p"}`))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	if err := NewSourceHandler(fetcher).HandlePreview(e.NewContext(req, rec)); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "SOURCE_ACCESS_DENIED") || !strings.Contains(rec.Body.String(), "粘贴题面") {
		t.Fatalf("response=%s", rec.Body.String())
	}
}

func TestSourcePreviewRejectsOversizedAndEmptyRequestsBeforeFetch(t *testing.T) {
	for _, body := range []string{`{"url":""}`, `{"url":"` + strings.Repeat("x", 17<<10) + `"}`} {
		fetcher := &fakeSourceFetcher{}
		e := echo.New()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/sources/preview", strings.NewReader(body))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		if err := NewSourceHandler(fetcher).HandlePreview(e.NewContext(req, rec)); err != nil {
			t.Fatal(err)
		}
		if rec.Code != http.StatusBadRequest || fetcher.called != 0 {
			t.Fatalf("status=%d calls=%d", rec.Code, fetcher.called)
		}
	}
}
