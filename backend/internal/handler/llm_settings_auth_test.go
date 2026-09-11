package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/config"
	authmw "github.com/Gingoo-TvT/Qraft/backend/internal/handler/middleware"
	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/require"
)

const llmSettingsAuthTestBody = `{
	"model":"gemini-test",
	"base_url":"https://llm.example.com",
	"provider":"google",
	"protocol":"gemini-native",
	"use_environment_key":true
}`

func TestLLMSettingsUpdateRequiresAdminWithStableIdentity(t *testing.T) {
	for _, test := range []struct {
		name   string
		claims *authmw.JWTClaims
	}{
		{
			name:   "non-admin",
			claims: &authmw.JWTClaims{Role: "user", UserID: "user-1"},
		},
		{
			name:   "admin without stable identity",
			claims: &authmw.JWTClaims{Role: "admin"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := &fakeLLMSettingsStore{}
			handler := NewLLMSettingsHandler(
				store, &fakeSettingsCipher{}, config.AnthropicConfig{APIKey: "env-key"},
				&fakeLLMSettingsConnectionProber{},
			)
			recorder := invokeLLMSettingsAuthUpdate(t, handler, test.claims, "untrusted-header")
			require.Equal(t, http.StatusForbidden, recorder.Code)
			require.Contains(t, recorder.Body.String(), `"code":"FORBIDDEN"`)
			require.Empty(t, store.records)
		})
	}
}

func TestLLMSettingsUpdateUsesAuthenticatedActorAndIgnoresHeader(t *testing.T) {
	store := &fakeLLMSettingsStore{}
	handler := NewLLMSettingsHandler(
		store, &fakeSettingsCipher{}, config.AnthropicConfig{APIKey: "env-key"},
		&fakeLLMSettingsConnectionProber{},
	)
	recorder := invokeLLMSettingsAuthUpdate(
		t,
		handler,
		&authmw.JWTClaims{Role: "admin", UserID: "admin-1"},
		"attacker-supplied-actor",
	)

	require.Equal(t, http.StatusOK, recorder.Code)
	record, ok := store.records[repository.LLMProviderPurposeStatement]
	require.True(t, ok)
	require.Equal(t, "user:admin-1", record.UpdatedBy)
	require.NotContains(t, recorder.Body.String(), "attacker-supplied-actor")
}

func TestLLMSettingsDeleteRequiresAdminWithStableIdentity(t *testing.T) {
	store := &fakeLLMSettingsStore{records: map[string]repository.LLMProviderSettingRecord{
		repository.LLMProviderPurposeVerification: {
			Purpose: repository.LLMProviderPurposeVerification,
			Model:   "verification-model",
		},
	}}
	handler := NewLLMSettingsHandler(store, &fakeSettingsCipher{}, config.AnthropicConfig{})
	recorder := invokeLLMSettingsDelete(
		t,
		handler,
		repository.LLMProviderPurposeVerification,
		&authmw.JWTClaims{Role: "user", UserID: "user-1"},
	)
	require.Equal(t, http.StatusForbidden, recorder.Code)
	require.Empty(t, store.deleteCalls)
	require.Contains(t, store.records, repository.LLMProviderPurposeVerification)
}

func invokeLLMSettingsAuthUpdate(
	t *testing.T,
	handler *LLMSettingsHandler,
	claims *authmw.JWTClaims,
	actorHeader string,
) *httptest.ResponseRecorder {
	t.Helper()
	e := echo.New()
	request := httptest.NewRequest(
		http.MethodPut,
		"/api/v1/settings/llm/statement",
		strings.NewReader(llmSettingsAuthTestBody),
	)
	request.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	request.Header.Set("X-Actor", actorHeader)
	recorder := httptest.NewRecorder()
	ctx := e.NewContext(request, recorder)
	ctx.SetParamNames("purpose")
	ctx.SetParamValues("statement")
	ctx.Set("user", claims)
	require.NoError(t, handler.HandleUpdate(ctx))
	return recorder
}
