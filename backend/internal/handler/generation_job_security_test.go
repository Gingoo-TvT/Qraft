package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/generationapi"
	authmw "github.com/Gingoo-TvT/Qraft/backend/internal/handler/middleware"
	"github.com/Gingoo-TvT/Qraft/backend/internal/service"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	temporalmocks "go.temporal.io/sdk/mocks"
	"go.temporal.io/sdk/temporal"
)

func TestGenerationJobCreateRejectsAuthenticatedRoleOnlyPrincipal(t *testing.T) {
	temporalClient := temporalmocks.NewClient(t)
	fake := &fakeGenerationJobService{}
	handler := NewGenerationJobHandler(fake, temporalClient, &ProblemHandler{})
	e := echo.New()
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/generation/jobs",
		strings.NewReader(generationJobTestRequestBody(t)),
	)
	request.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	request.Header.Set("Idempotency-Key", "role-only-key")
	recorder := httptest.NewRecorder()
	ctx := e.NewContext(request, recorder)
	ctx.Set("user", &authmw.JWTClaims{Role: "admin"})

	require.NoError(t, handler.HandleCreate(ctx))
	require.Equal(t, http.StatusUnauthorized, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"code":"unauthorized"`)
	require.Zero(t, fake.triggerCalls)
}

func TestGenerationJobStatusFailsClosedWhenTemporalQueryFails(t *testing.T) {
	body := generationJobTestRequestBody(t)
	jobID, payloadSHA256, principalSHA256 := generationJobTestIdentity(t, body, "query-error-key")
	temporalClient := temporalmocks.NewClient(t)
	temporalClient.On("DescribeWorkflowExecution", mock.Anything, jobID, "").
		Return(generationJobTestDescription(t, jobID, enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING, payloadSHA256, principalSHA256), nil).Once()
	temporalClient.On("QueryWorkflow", mock.Anything, jobID, generationJobTestRunID, domain.WorkflowStateQueryName).
		Return(nil, errors.New("temporal query unavailable")).Once()

	handler := NewGenerationJobHandler(&fakeGenerationJobService{}, temporalClient, nil)
	recorder := invokeGenerationJobHandler(t, http.MethodGet, "/api/v1/generation/jobs/"+jobID, jobID, "", "", handler.HandleStatus)
	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"code":"internal_error"`)
	require.NotContains(t, recorder.Body.String(), `"status":"queued"`)
	requireGenerationJobResponseDoesNotLeakTemporal(t, recorder.Body.String())
}

func TestGenerationJobCompletedWithoutStoredProblemIsInternalUnavailable(t *testing.T) {
	body := generationJobTestRequestBody(t)
	jobID, payloadSHA256, principalSHA256 := generationJobTestIdentity(t, body, "missing-result-key")
	temporalClient := temporalmocks.NewClient(t)
	temporalClient.On("DescribeWorkflowExecution", mock.Anything, jobID, "").
		Return(generationJobTestDescription(t, jobID, enumspb.WORKFLOW_EXECUTION_STATUS_COMPLETED, payloadSHA256, principalSHA256), nil).Once()

	handler := NewGenerationJobHandler(&fakeGenerationJobService{problemErr: service.ErrNotFound}, temporalClient, nil)
	recorder := invokeGenerationJobHandler(t, http.MethodGet, "/api/v1/generation/jobs/"+jobID, jobID, "", "", handler.HandleStatus)
	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"code":"internal_error"`)
	require.NotContains(t, recorder.Body.String(), `"code":"not_found"`)
}

func TestGenerationJobFailureClassifierCoversWorkflowQualityErrors(t *testing.T) {
	for _, errorType := range []string{
		"InvalidParameterError",
		"TruncatedLLMResponse",
		"CompilationError",
		"ValidationFailed",
		"HumanReviewRejection",
	} {
		t.Run(errorType, func(t *testing.T) {
			err := temporal.NewNonRetryableApplicationError("private provider detail", errorType, nil)
			classified := classifyGenerationJobFailure(err)
			require.Equal(t, generationapi.ErrorQualityNotMet, classified.Code)
			require.False(t, classified.Retryable)
			require.NotContains(t, classified.Message, "private provider")
		})
	}
}

func TestGenerationJobStoredProviderKeyTTLIncludesMaximumWallBudget(t *testing.T) {
	var productRequest generationapi.Request
	require.NoError(t, json.Unmarshal([]byte(generationJobTestRequestBody(t)), &productRequest))
	productRequest.Quality.Budget.MaxWallTimeSeconds = generationapi.MaxWallTimeSeconds
	bodyBytes, err := json.Marshal(productRequest)
	require.NoError(t, err)
	body := string(bodyBytes)
	jobID, _, _ := generationJobTestIdentity(t, body, "max-wall-key")

	temporalClient := temporalmocks.NewClient(t)
	temporalClient.On("DescribeWorkflowExecution", mock.Anything, jobID, "").
		Return(nil, serviceerror.NewNotFound("absent")).Once()
	keyStore := &fakeRuntimeKeyStore{}
	providerHelper := NewProblemHandler(nil, nil, keyStore)
	providerHelper.SetPermanentProviderSettings(&fakePermanentProviderSettings{
		statement: &domain.LLMRuntimeConfig{
			Model:    "gemini-test",
			APIKey:   "stored-statement-key",
			BaseURL:  "https://llm.example.com",
			Provider: "google",
			Protocol: "gemini-native",
		},
		verification: &domain.LLMRuntimeConfig{
			Model:     "gpt-test",
			APIKeyRef: "env:OPENAI_API_KEY",
			BaseURL:   "https://review.example.com",
			Provider:  "openai",
			Protocol:  "openai-responses",
		},
	}, true)
	fake := &fakeGenerationJobService{}
	fake.trigger = func(
		_ context.Context,
		_ string,
		_ string,
		_ string,
		timeout time.Duration,
		params domain.ProblemGenParams,
	) error {
		require.Equal(t, 24*time.Hour, timeout)
		require.Equal(t, 24*time.Hour+generationJobRuntimeKeyTTLGrace, keyStore.lastTTL)
		require.NotNil(t, params.ProviderConfig)
		require.NotNil(t, params.ProviderConfig.Statement)
		require.Empty(t, params.ProviderConfig.Statement.APIKey)
		require.Equal(t, "runtime:test-token", params.ProviderConfig.Statement.APIKeyRef)
		return nil
	}

	handler := NewGenerationJobHandler(fake, temporalClient, providerHelper)
	recorder := invokeGenerationJobHandler(t, http.MethodPost, "/api/v1/generation/jobs", "", body, "max-wall-key", handler.HandleCreate)
	require.Equal(t, http.StatusCreated, recorder.Code)
	require.Equal(t, 1, fake.triggerCalls)
}
