//go:build integration

package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
	"github.com/Gingoo-TvT/Qraft/backend/internal/service"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/mocks"
)

func TestProblemSetCreateStartRecoveryHTTPIntegration(t *testing.T) {
	if os.Getenv("ALGOFORGE_INTEGRATION_DISPOSABLE_DB") != "true" {
		t.Skip("requires a disposable migrated DATABASE_URL")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	require.NoError(t, err)
	defer pool.Close()
	repo := repository.NewProblemSetRepository(pool)
	sets := service.NewProblemSetServiceWithDeps(repo, nil, nil, nil, nil)
	temporalClient := &mocks.Client{}
	ids := []string{}
	opts := mock.MatchedBy(func(o client.StartWorkflowOptions) bool {
		ids = append(ids, o.ID)
		return o.TaskQueue == "set-test-queue"
	})
	temporalClient.On("ExecuteWorkflow", mock.Anything, opts, mock.Anything, mock.Anything).Return(nil, errors.New("fixture transport unavailable")).Once()
	temporalClient.On("ExecuteWorkflow", mock.Anything, opts, mock.Anything, mock.Anything).Return(nil, nil).Once()
	resolver := func(context.Context) (*domain.ProviderRuntimeConfig, error) {
		return &domain.ProviderRuntimeConfig{}, nil
	}
	generation := service.NewProblemSetGenerationService(sets, repo, nil, nil, temporalClient, "set-test-queue", resolver)
	h := NewProblemSetHandler(sets)
	h.SetGenerationService(generation)
	e := echo.New()
	RegisterProblemSetRoutes(e.Group("/api/v1"), h)
	request := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		out := httptest.NewRecorder()
		e.ServeHTTP(out, req)
		return out
	}
	created := request(http.MethodPost, "/api/v1/problem-sets", `{"title":"HTTP orchestration fixture","desired_item_count":2,"start_generation":true,"generate_prompt":true,"generation_config":{"mode":"mixed","requirements":"完整测试","distribution":[{"type":"judge","count":1,"score":2},{"type":"programming","count":1,"score":100}]}}`)
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	var response struct {
		Data domain.ProblemSet `json:"data"`
	}
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &response))
	require.NotEqual(t, uuid.Nil, response.Data.ID)
	require.NotEmpty(t, response.Data.GenerationError)
	id := response.Data.ID
	saved, err := repo.GetByID(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "queued", saved.Generation.Status)
	retry := request(http.MethodPost, "/api/v1/problem-sets/"+id.String()+"/generation", "")
	require.Equal(t, http.StatusAccepted, retry.Code, retry.Body.String())
	require.GreaterOrEqual(t, len(ids), 2)
	for _, identity := range ids {
		require.Equal(t, ids[0], identity, "uncertain start retries must use the same reservation")
	}
	edited := request(http.MethodPut, "/api/v1/problem-sets/"+id.String(), `{"title":"cannot edit while running"}`)
	require.Equal(t, http.StatusConflict, edited.Code)
	missing := request(http.MethodGet, "/api/v1/problem-sets/"+uuid.NewString()+"/generation", "")
	require.Equal(t, http.StatusNotFound, missing.Code)
	temporalClient.AssertExpectations(t)
}

func TestProblemSetLegacySaveStillWorksWithoutGenerationConfigHTTPIntegration(t *testing.T) {
	if os.Getenv("ALGOFORGE_INTEGRATION_DISPOSABLE_DB") != "true" {
		t.Skip("requires a disposable migrated DATABASE_URL")
	}
	pool, err := pgxpool.New(context.Background(), os.Getenv("DATABASE_URL"))
	require.NoError(t, err)
	defer pool.Close()
	h := NewProblemSetHandler(service.NewProblemSetServiceWithDeps(repository.NewProblemSetRepository(pool), nil, nil, nil, nil))
	e := echo.New()
	RegisterProblemSetRoutes(e.Group("/api/v1"), h)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/problem-sets", strings.NewReader(`{"title":"legacy manual set","desired_item_count":3}`))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	out := httptest.NewRecorder()
	e.ServeHTTP(out, req)
	require.Equal(t, http.StatusCreated, out.Code, out.Body.String())
	var data struct {
		Data domain.ProblemSet `json:"data"`
	}
	require.NoError(t, json.Unmarshal(out.Body.Bytes(), &data))
	require.Nil(t, data.Data.GenerationConfig)
	require.Nil(t, data.Data.Generation)
}
