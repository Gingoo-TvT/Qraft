//go:build integration

package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	authmw "github.com/Gingoo-TvT/Qraft/backend/internal/handler/middleware"
	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
	"github.com/Gingoo-TvT/Qraft/backend/internal/service"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/require"
)

func TestProblemManualReleaseHTTPContractIntegration(t *testing.T) {
	if os.Getenv("ALGOFORGE_INTEGRATION_DISPOSABLE_DB") != "true" {
		t.Skip("requires disposable migrated database")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	require.NoError(t, err)
	defer pool.Close()
	repo := repository.NewProblemRepository(pool)
	p := &domain.Problem{ID: uuid.New(), Title: "Synthetic manual HTTP", Statement: "Print an integer.", Level: domain.LevelAlgorithm, Difficulty: 1000, Tags: []string{}, TimeLimit: 1000, MemoryLimit: 128, Status: domain.ProblemStatusQuarantined, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	require.NoError(t, repo.Create(ctx, p))
	p, err = repo.GetByID(ctx, p.ID)
	require.NoError(t, err)
	h := NewProblemHandler(service.NewProblemService(repo, nil, nil, nil, nil, ""), nil)
	call := func(role string, payload map[string]any) *httptest.ResponseRecorder {
		raw, e := json.Marshal(payload)
		require.NoError(t, e)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/problems/"+p.ID.String()+"/public-release-approval", strings.NewReader(string(raw)))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		server := echo.New()
		c := server.NewContext(req, rec)
		c.SetParamNames("id")
		c.SetParamValues(p.ID.String())
		c.Set("user", &authmw.JWTClaims{Role: role, UserID: "synthetic-" + role, Email: role + "@example.invalid"})
		if e := h.HandleApprovePublicRelease(c); e != nil {
			server.HTTPErrorHandler(e, c)
		}
		return rec
	}
	payload := map[string]any{"approved": true, "override_quality": true, "expected_updated_at": p.UpdatedAt, "note": "Reviewed synthetic content", "actor": "forged-client-actor"}
	require.Equal(t, http.StatusForbidden, call("member", payload).Code)
	require.Equal(t, http.StatusBadRequest, call("admin", map[string]any{"approved": false}).Code)
	require.Equal(t, http.StatusBadRequest, call("admin", map[string]any{"approved": true, "override_quality": true}).Code)
	require.Equal(t, http.StatusConflict, call("admin", map[string]any{"approved": true}).Code)
	response := call("admin", payload)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var result struct {
		Data repository.PublicReleaseApprovalReport `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
	require.Equal(t, domain.ProblemStatusPublished, result.Data.ReleaseStatus)
	require.NotNil(t, result.Data.ManualReview)
	require.Equal(t, "admin@example.invalid", result.Data.ManualReview.ApprovedBy)
	require.NotContains(t, response.Body.String(), "forged-client-actor")
	require.Contains(t, result.Data.ManualReview.OverriddenChecks, "missing detailed solution")
	require.Equal(t, http.StatusConflict, call("admin", payload).Code, "stale page must not approve a different revision")
	require.NoError(t, repo.Delete(ctx, p.ID), "manual audit alone does not ban ordinary deletion")
	var count int
	require.NoError(t, pool.QueryRow(ctx, `SELECT COUNT(*) FROM problem_manual_release_approvals WHERE problem_id=$1`, p.ID).Scan(&count))
	require.Equal(t, 1, count)
}
