package handler

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/rating"
	"github.com/Gingoo-TvT/Qraft/backend/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type ratingRuntimeHTTPStore struct {
	rating.Store
	current     rating.Assessment
	currentHash string
	updates     int
	events      []string
}

func (s *ratingRuntimeHTTPStore) Workspace(context.Context, uuid.UUID) (rating.Workspace, error) {
	s.events = append(s.events, "workspace")
	return rating.Workspace{Subject: rating.Subject{Hash: s.currentHash}, Assessments: []rating.Assessment{s.current}}, nil
}
func (s *ratingRuntimeHTTPStore) GetAssessment(context.Context, uuid.UUID) (rating.Assessment, error) {
	return s.current, nil
}
func (s *ratingRuntimeHTTPStore) CaptureSubject(context.Context, uuid.UUID) (rating.Subject, error) {
	return rating.Subject{Hash: s.currentHash}, nil
}
func (s *ratingRuntimeHTTPStore) CreateAssessment(context.Context, uuid.UUID, string) (rating.Assessment, error) {
	s.events = append(s.events, "create")
	if s.current.Status == "running" || s.current.Status == "pending" {
		return rating.Assessment{}, rating.ErrConflict
	}
	s.current = rating.Assessment{ID: uuid.New(), Status: "pending", Subject: rating.Subject{Hash: s.currentHash}}
	return s.current, nil
}
func (s *ratingRuntimeHTTPStore) UpdateAssessment(_ context.Context, _ uuid.UUID, status, phase string, report *rating.Report, message string) error {
	s.updates++
	s.current.Status = status
	return nil
}
func TestRatingHTTPUncertainStartPreservesReservation(t *testing.T) {
	store := &ratingRuntimeHTTPStore{currentHash: strings.Repeat("a", 64)}
	h := NewRatingHandler(store, RatingHandlerOptions{DevMode: true, StartAssessment: func(context.Context, uuid.UUID) error { return service.ErrRatingStartUncertain }})
	c, rec := ratingTestContext(http.MethodPost, "/api/v1/rating/problems/p/assessments", "")
	require.NoError(t, h.HandleCreateAssessment(c))
	require.Equal(t, 201, rec.Code)
	require.Zero(t, store.updates)
	require.Equal(t, "pending", store.current.Status)
	require.Contains(t, rec.Body.String(), `"status":"pending"`)
}
func TestRatingHTTPReconcilesBeforeCreatingAgain(t *testing.T) {
	store := &ratingRuntimeHTTPStore{currentHash: strings.Repeat("a", 64), current: rating.Assessment{ID: uuid.New(), Status: "running"}}
	h := NewRatingHandler(store, RatingHandlerOptions{DevMode: true, ReconcileAssessment: func(context.Context, uuid.UUID) error {
		store.events = append(store.events, "reconcile")
		store.current.Status = "failed"
		return nil
	}, StartAssessment: func(context.Context, uuid.UUID) error { store.events = append(store.events, "start"); return nil }})
	c, rec := ratingTestContext(http.MethodPost, "/api/v1/rating/problems/p/assessments", "")
	require.NoError(t, h.HandleCreateAssessment(c))
	require.Equal(t, 201, rec.Code)
	require.Equal(t, []string{"workspace", "reconcile", "create", "start"}, store.events)
}
func TestRatingHTTPGetAndWorkspaceReturnReconciledStatus(t *testing.T) {
	for _, method := range []string{"get", "workspace"} {
		t.Run(method, func(t *testing.T) {
			store := &ratingRuntimeHTTPStore{currentHash: strings.Repeat("a", 64), current: rating.Assessment{ID: uuid.New(), Status: "running", Subject: rating.Subject{Hash: "older-version"}}}
			h := NewRatingHandler(store, RatingHandlerOptions{DevMode: true, ReconcileAssessment: func(context.Context, uuid.UUID) error { store.current.Status = "failed"; return nil }})
			c, rec := ratingTestContext(http.MethodGet, "/api/v1/rating/assessments/a", "")
			if method == "get" {
				require.NoError(t, h.HandleGetAssessment(c))
			} else {
				require.NoError(t, h.HandleWorkspace(c))
			}
			require.Equal(t, 200, rec.Code)
			require.Contains(t, rec.Body.String(), `"status":"failed"`)
			require.Contains(t, rec.Body.String(), `"stale":true`)
		})
	}
}
func TestRatingHTTPCancelDoesNotOverwriteReconciledFailure(t *testing.T) {
	store := &ratingRuntimeHTTPStore{current: rating.Assessment{ID: uuid.New(), Status: "running"}}
	h := NewRatingHandler(store, RatingHandlerOptions{DevMode: true, CancelAssessment: func(context.Context, uuid.UUID) error { store.current.Status = "failed"; return nil }})
	c, rec := ratingTestContext(http.MethodDelete, "/api/v1/rating/assessments/a", "")
	require.NoError(t, h.HandleCancelAssessment(c))
	require.Equal(t, 204, rec.Code)
	require.Equal(t, "failed", store.current.Status)
	require.Zero(t, store.updates)
}
