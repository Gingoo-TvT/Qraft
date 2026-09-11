package handler

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
	"github.com/labstack/echo/v4"
)

type fakeReviewSettingsStore struct {
	record      repository.ReviewSettingsRecord
	updatedBy   string
	updateCalls int
}

func (s *fakeReviewSettingsStore) GetReviewSettings(
	_ context.Context,
) (repository.ReviewSettingsRecord, error) {
	return s.record, nil
}

func (s *fakeReviewSettingsStore) UpdateReviewSettings(
	_ context.Context,
	autoApprovePublicRelease bool,
	actor string,
) (repository.ReviewSettingsRecord, error) {
	s.updateCalls++
	s.updatedBy = actor
	s.record.AutoApprovePublicRelease = autoApprovePublicRelease
	s.record.UpdatedBy = actor
	s.record.UpdatedAt = time.Now().UTC()
	return s.record, nil
}

func TestReviewSettingsGetAndUpdate(t *testing.T) {
	store := &fakeReviewSettingsStore{record: repository.ReviewSettingsRecord{
		AutoApprovePublicRelease: false,
		UpdatedBy:                "migration",
		UpdatedAt:                time.Now().UTC(),
	}}
	h := NewReviewSettingsHandler(store)
	e := echo.New()

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/settings/review", nil)
	getRec := httptest.NewRecorder()
	if err := h.HandleGet(e.NewContext(getReq, getRec)); err != nil {
		t.Fatalf("HandleGet() error = %v", err)
	}
	if getRec.Code != http.StatusOK ||
		!strings.Contains(getRec.Body.String(), `"auto_approve_public_release":false`) ||
		!strings.Contains(getRec.Body.String(), `"scope":"future_eligible_problems"`) {
		t.Fatalf("unexpected GET response: status=%d body=%s", getRec.Code, getRec.Body.String())
	}

	putReq := httptest.NewRequest(
		http.MethodPut,
		"/api/v1/settings/review",
		bytes.NewBufferString(`{"auto_approve_public_release":true}`),
	)
	putReq.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	putRec := httptest.NewRecorder()
	if err := h.HandleUpdate(e.NewContext(putReq, putRec)); err != nil {
		t.Fatalf("HandleUpdate() error = %v", err)
	}
	if putRec.Code != http.StatusOK ||
		!strings.Contains(putRec.Body.String(), `"auto_approve_public_release":true`) {
		t.Fatalf("unexpected PUT response: status=%d body=%s", putRec.Code, putRec.Body.String())
	}
	if store.updateCalls != 1 || store.updatedBy != "local-admin" {
		t.Fatalf("update calls=%d actor=%q", store.updateCalls, store.updatedBy)
	}
}

func TestReviewSettingsUpdateRequiresExplicitBoolean(t *testing.T) {
	store := &fakeReviewSettingsStore{}
	h := NewReviewSettingsHandler(store)
	e := echo.New()
	req := httptest.NewRequest(
		http.MethodPut,
		"/api/v1/settings/review",
		bytes.NewBufferString(`{}`),
	)
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()

	if err := h.HandleUpdate(e.NewContext(req, rec)); err != nil {
		t.Fatalf("HandleUpdate() error = %v", err)
	}
	if rec.Code != http.StatusBadRequest ||
		!strings.Contains(rec.Body.String(), `"code":"AUTO_APPROVE_REQUIRED"`) {
		t.Fatalf("unexpected response: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if store.updateCalls != 0 {
		t.Fatalf("unexpected update calls: %d", store.updateCalls)
	}
}
