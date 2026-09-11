package handler

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/config"
	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	authmw "github.com/Gingoo-TvT/Qraft/backend/internal/handler/middleware"
	"github.com/Gingoo-TvT/Qraft/backend/internal/llm"
	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
	"github.com/labstack/echo/v4"
)

type llmProviderSettingsStore interface {
	GetLLMProviderSetting(ctx context.Context, purpose string) (repository.LLMProviderSettingRecord, error)
	UpsertLLMProviderSetting(ctx context.Context, input repository.LLMProviderSettingUpsert) (repository.LLMProviderSettingRecord, error)
	DeleteLLMProviderSetting(ctx context.Context, purpose string) error
}

type settingsSecretCipher interface {
	Seal(plaintext string) (string, error)
	Open(encoded string) (string, error)
}

// LinkAPI Responses calls can take longer than a short health-check window
// before the first response headers arrive. Keep the probe bounded, but align
// it with the provider's documented client timeout so a valid GPT route is not
// rejected merely because model startup exceeds ten seconds.
const llmSettingsConnectionProbeTimeout = 120 * time.Second

var errLLMSettingsEndpointChangeRequiresKey = errors.New(
	"changing base_url requires a new api_key or explicit use_environment_key",
)

type llmProviderConnectionProbe struct {
	Model           string
	BaseURL         string
	Provider        string
	Protocol        string
	ReasoningEffort string
	APIKey          string
}

type llmProviderConnectionProber interface {
	Probe(context.Context, llmProviderConnectionProbe) error
}

type liveLLMProviderConnectionProber struct{}

func (liveLLMProviderConnectionProber) Probe(ctx context.Context, input llmProviderConnectionProbe) error {
	client := llm.NewClient(config.AnthropicConfig{
		APIKey:          input.APIKey,
		BaseURL:         input.BaseURL,
		Model:           input.Model,
		Provider:        input.Provider,
		ReasoningEffort: input.ReasoningEffort,
	})
	_, err := client.Complete(ctx, &llm.Request{
		Model:     input.Model,
		MaxTokens: 1,
		Messages: []llm.Message{{
			Role:    "user",
			Content: "Reply with OK.",
		}},
		Runtime: &llm.RuntimeConfig{
			BaseURL:         input.BaseURL,
			Provider:        input.Provider,
			Protocol:        input.Protocol,
			ReasoningEffort: input.ReasoningEffort,
		},
	})
	if err != nil {
		return fmt.Errorf("provider connection check: %w", err)
	}
	return nil
}

// LLMSettingsHandler manages durable defaults for the three provider roles used
// by programming-problem generation. Secret values are never returned by the
// API and are only decrypted long enough to create a short-lived runtime ref.
type LLMSettingsHandler struct {
	store    llmProviderSettingsStore
	cipher   settingsSecretCipher
	fallback config.AnthropicConfig
	prober   llmProviderConnectionProber
}

func NewLLMSettingsHandler(
	store llmProviderSettingsStore,
	cipher settingsSecretCipher,
	fallback config.AnthropicConfig,
	probers ...llmProviderConnectionProber,
) *LLMSettingsHandler {
	prober := llmProviderConnectionProber(liveLLMProviderConnectionProber{})
	if len(probers) > 0 && probers[0] != nil {
		prober = probers[0]
	}
	return &LLMSettingsHandler{store: store, cipher: cipher, fallback: fallback, prober: prober}
}

type llmProviderSettingView struct {
	Purpose            string     `json:"purpose"`
	Model              string     `json:"model"`
	BaseURL            string     `json:"base_url"`
	Provider           string     `json:"provider"`
	Protocol           string     `json:"protocol"`
	ReasoningEffort    string     `json:"reasoning_effort"`
	APIKeySource       string     `json:"api_key_source"`
	APIKeyConfigured   bool       `json:"api_key_configured"`
	Source             string     `json:"source"`
	OverrideConfigured bool       `json:"override_configured"`
	InheritedFrom      string     `json:"inherited_from,omitempty"`
	UpdatedBy          string     `json:"updated_by,omitempty"`
	UpdatedAt          *time.Time `json:"updated_at,omitempty"`
}

type llmProviderSettingsView struct {
	Statement    llmProviderSettingView `json:"statement"`
	Verification llmProviderSettingView `json:"verification"`
	Review       llmProviderSettingView `json:"review"`
}

type llmProviderSettingUpdate struct {
	Model             string `json:"model"`
	BaseURL           string `json:"base_url"`
	Provider          string `json:"provider"`
	Protocol          string `json:"protocol"`
	ReasoningEffort   string `json:"reasoning_effort"`
	APIKey            string `json:"api_key"`
	UseEnvironmentKey bool   `json:"use_environment_key"`
}

// HandleGet returns credential-free effective settings for all three roles.
// V inherits G and R inherits V when their saved override is absent.
func (h *LLMSettingsHandler) HandleGet(c echo.Context) error {
	if h == nil || h.store == nil {
		return serviceUnavailable(c, "LLM settings store is unavailable")
	}
	settings, err := h.effectiveSettingViews(c.Request().Context())
	if err != nil {
		return internalError(c, "failed to read LLM provider settings: "+err.Error())
	}
	return ok(c, settings)
}

// HandleUpdate persists one provider role. An empty api_key preserves the
// current key source; use_environment_key explicitly switches back to the
// deployment-managed ANTHROPIC_API_KEY.
func (h *LLMSettingsHandler) HandleUpdate(c echo.Context) error {
	if h == nil || h.store == nil || h.cipher == nil || h.prober == nil {
		return serviceUnavailable(c, "LLM settings service is unavailable")
	}
	actor, allowed := llmSettingsAdminActor(c)
	if !allowed {
		return forbidden(c, "administrator role with a stable identity is required")
	}
	purpose, err := normalizeLLMSettingPurpose(c.Param("purpose"))
	if err != nil {
		return badRequest(c, "INVALID_PURPOSE", err.Error())
	}

	var request llmProviderSettingUpdate
	if err := c.Bind(&request); err != nil {
		return badRequest(c, "INVALID_BODY", "failed to parse request body: "+err.Error())
	}
	if err := normalizeLLMProviderSettingUpdate(&request); err != nil {
		return badRequest(c, "INVALID_SETTINGS", err.Error())
	}

	keySource, encryptedKey, err := h.nextKeyMaterial(c.Request().Context(), purpose, request)
	if err != nil {
		if errors.Is(err, errLLMSettingsEndpointChangeRequiresKey) {
			return badRequest(c, "INVALID_SETTINGS", err.Error())
		}
		return internalError(c, "failed to prepare provider key: "+err.Error())
	}
	probeKey, err := h.connectionProbeKey(keySource, encryptedKey, request.APIKey)
	if err != nil {
		return badRequest(c, "LLM_CONNECTION_FAILED", err.Error())
	}
	probeCtx, cancelProbe := context.WithTimeout(c.Request().Context(), llmSettingsConnectionProbeTimeout)
	defer cancelProbe()
	if err := h.prober.Probe(probeCtx, llmProviderConnectionProbe{
		Model: request.Model, BaseURL: request.BaseURL, Provider: request.Provider,
		Protocol: request.Protocol, ReasoningEffort: request.ReasoningEffort, APIKey: probeKey,
	}); err != nil {
		return badRequest(c, "LLM_CONNECTION_FAILED", redactSecret(err.Error(), probeKey))
	}
	record, err := h.store.UpsertLLMProviderSetting(c.Request().Context(), repository.LLMProviderSettingUpsert{
		Purpose:         purpose,
		Model:           request.Model,
		BaseURL:         request.BaseURL,
		Provider:        request.Provider,
		Protocol:        request.Protocol,
		ReasoningEffort: request.ReasoningEffort,
		APIKeySource:    keySource,
		EncryptedAPIKey: encryptedKey,
		UpdatedBy:       actor,
	})
	if err != nil {
		return internalError(c, "failed to save provider settings: "+err.Error())
	}
	return ok(c, h.recordView(record))
}

// HandleDelete clears an optional V/R override and returns the resulting
// credential-free inherited setting. G is the root and cannot be deleted.
func (h *LLMSettingsHandler) HandleDelete(c echo.Context) error {
	if h == nil || h.store == nil {
		return serviceUnavailable(c, "LLM settings store is unavailable")
	}
	if _, allowed := llmSettingsAdminActor(c); !allowed {
		return forbidden(c, "administrator role with a stable identity is required")
	}
	purpose, err := normalizeLLMSettingPurpose(c.Param("purpose"))
	if err != nil {
		return badRequest(c, "INVALID_PURPOSE", err.Error())
	}
	if purpose == repository.LLMProviderPurposeStatement {
		return badRequest(c, "RESET_NOT_ALLOWED", "statement is the root provider and cannot be reset")
	}
	if err := h.store.DeleteLLMProviderSetting(c.Request().Context(), purpose); err != nil {
		return internalError(c, "failed to reset provider settings: "+err.Error())
	}
	settings, err := h.effectiveSettingViews(c.Request().Context())
	if err != nil {
		return internalError(c, "failed to read inherited provider settings: "+err.Error())
	}
	if purpose == repository.LLMProviderPurposeVerification {
		return ok(c, settings.Verification)
	}
	return ok(c, settings.Review)
}

func redactSecret(message, secret string) string {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return message
	}
	return strings.ReplaceAll(message, secret, "[redacted]")
}

func (h *LLMSettingsHandler) connectionProbeKey(
	keySource string,
	encryptedKey *string,
	requestKey string,
) (string, error) {
	switch keySource {
	case repository.LLMAPIKeySourceEnvironment:
		key := strings.TrimSpace(h.fallback.APIKey)
		if key == "" {
			return "", fmt.Errorf("deployment API key is not configured")
		}
		return key, nil
	case repository.LLMAPIKeySourceStored:
		if key := strings.TrimSpace(requestKey); key != "" {
			return key, nil
		}
		if encryptedKey == nil || strings.TrimSpace(*encryptedKey) == "" {
			return "", fmt.Errorf("stored provider key is missing")
		}
		key, err := h.cipher.Open(*encryptedKey)
		if err != nil {
			return "", fmt.Errorf("decrypt stored provider key: %w", err)
		}
		key = strings.TrimSpace(key)
		if key == "" {
			return "", fmt.Errorf("stored provider key is empty")
		}
		return key, nil
	default:
		return "", fmt.Errorf("unsupported provider key source %q", keySource)
	}
}

func (h *LLMSettingsHandler) nextKeyMaterial(
	ctx context.Context,
	purpose string,
	request llmProviderSettingUpdate,
) (string, *string, error) {
	if request.UseEnvironmentKey {
		return repository.LLMAPIKeySourceEnvironment, nil, nil
	}
	if request.APIKey != "" {
		if len(request.APIKey) > 8192 {
			return "", nil, fmt.Errorf("api_key is too long")
		}
		if containsControlRune(request.APIKey) {
			return "", nil, fmt.Errorf("api_key contains control characters")
		}
		sealed, err := h.cipher.Seal(request.APIKey)
		if err != nil {
			return "", nil, err
		}
		return repository.LLMAPIKeySourceStored, &sealed, nil
	}

	current, err := h.store.GetLLMProviderSetting(ctx, purpose)
	if errors.Is(err, repository.ErrLLMProviderSettingNotFound) {
		return repository.LLMAPIKeySourceEnvironment, nil, nil
	}
	if err != nil {
		return "", nil, err
	}
	if !sameLLMSettingsBaseURL(current.BaseURL, request.BaseURL) {
		return "", nil, errLLMSettingsEndpointChangeRequiresKey
	}
	return current.APIKeySource, current.EncryptedAPIKey, nil
}

func sameLLMSettingsBaseURL(left, right string) bool {
	left, leftErr := normalizeLLMSettingsBaseURL(left)
	right, rightErr := normalizeLLMSettingsBaseURL(right)
	return leftErr == nil && rightErr == nil && left == right
}

func normalizeLLMProviderSettingUpdate(request *llmProviderSettingUpdate) error {
	if request == nil {
		return fmt.Errorf("settings are required")
	}
	request.Model = strings.TrimSpace(request.Model)
	request.BaseURL = strings.TrimSpace(request.BaseURL)
	request.Provider = strings.TrimSpace(request.Provider)
	request.Protocol = strings.ToLower(strings.TrimSpace(request.Protocol))
	request.ReasoningEffort = strings.ToLower(strings.TrimSpace(request.ReasoningEffort))
	request.APIKey = strings.TrimSpace(request.APIKey)
	if request.Model == "" {
		return fmt.Errorf("model is required")
	}
	if request.APIKey != "" && request.UseEnvironmentKey {
		return fmt.Errorf("api_key cannot be combined with use_environment_key")
	}

	// Validate the caller-controlled URL before deriving identity from it.
	preflightProvider := request.Provider
	if preflightProvider == "" && request.BaseURL != "" {
		preflightProvider = "endpoint-derived"
	}
	preflightProtocol := request.Protocol
	if preflightProtocol == "" {
		preflightProtocol = "auto"
	}
	preflight := &domain.LLMRuntimeConfig{
		Model:           request.Model,
		APIKeyRef:       "env:ANTHROPIC_API_KEY",
		BaseURL:         request.BaseURL,
		Provider:        preflightProvider,
		Protocol:        preflightProtocol,
		ReasoningEffort: request.ReasoningEffort,
	}
	if err := preflight.Validate("settings"); err != nil {
		return err
	}

	baseURL, err := normalizeLLMSettingsBaseURL(request.BaseURL)
	if err != nil {
		return err
	}
	request.BaseURL = baseURL
	if request.Provider == "" {
		request.Provider = deriveLLMProviderID(request.BaseURL)
	}

	protocolHint := request.Protocol
	if protocolHint == "" || protocolHint == "auto" {
		switch {
		case request.BaseURL == "":
			protocolHint = string(llm.ProtocolAnthropic)
		case hasKnownLLMModelPrefix(request.Model):
			protocolHint = "auto"
		default:
			protocolHint = string(llm.ProtocolOpenAIChat)
		}
	}
	resolvedProtocol, err := llm.ResolveProtocol(&llm.RuntimeConfig{
		Provider: request.Provider,
		Protocol: protocolHint,
	}, request.Model)
	if err != nil {
		return err
	}
	request.Protocol = string(resolvedProtocol)

	configToValidate := &domain.LLMRuntimeConfig{
		Model:           request.Model,
		APIKeyRef:       "env:ANTHROPIC_API_KEY",
		BaseURL:         request.BaseURL,
		Provider:        request.Provider,
		Protocol:        request.Protocol,
		ReasoningEffort: request.ReasoningEffort,
	}
	return configToValidate.Validate("settings")
}

func normalizeLLMSettingsBaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	endpoint, err := url.ParseRequestURI(raw)
	if err != nil || endpoint.Scheme == "" || endpoint.Host == "" {
		return "", fmt.Errorf("settings.base_url must be an absolute http(s) URL")
	}
	endpoint.Scheme = strings.ToLower(endpoint.Scheme)
	endpoint.Host = strings.ToLower(endpoint.Host)
	return strings.TrimRight(endpoint.String(), "/"), nil
}

func deriveLLMProviderID(baseURL string) string {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return "anthropic"
	}
	provider := "llm-endpoint:" + baseURL
	if len(provider) <= 512 {
		return provider
	}
	digest := sha256.Sum256([]byte(baseURL))
	return fmt.Sprintf("llm-endpoint-sha256:%x", digest)
}

func hasKnownLLMModelPrefix(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	return strings.HasPrefix(model, "gemini-") ||
		strings.HasPrefix(model, "gpt-") ||
		strings.HasPrefix(model, "o1") ||
		strings.HasPrefix(model, "o3") ||
		strings.HasPrefix(model, "o4") ||
		strings.HasPrefix(model, "claude-")
}

func normalizeLLMSettingPurpose(purpose string) (string, error) {
	switch strings.TrimSpace(purpose) {
	case repository.LLMProviderPurposeStatement:
		return repository.LLMProviderPurposeStatement, nil
	case repository.LLMProviderPurposeVerification:
		return repository.LLMProviderPurposeVerification, nil
	case repository.LLMProviderPurposeReview:
		return repository.LLMProviderPurposeReview, nil
	default:
		return "", fmt.Errorf("purpose must be statement, verification, or review")
	}
}

func (h *LLMSettingsHandler) effectiveSettingViews(ctx context.Context) (llmProviderSettingsView, error) {
	statement, found, err := h.savedSettingView(ctx, repository.LLMProviderPurposeStatement)
	if err != nil {
		return llmProviderSettingsView{}, fmt.Errorf("statement settings: %w", err)
	}
	if !found {
		statement = unconfiguredSettingView(repository.LLMProviderPurposeStatement)
	}
	verification, found, err := h.savedSettingView(ctx, repository.LLMProviderPurposeVerification)
	if err != nil {
		return llmProviderSettingsView{}, fmt.Errorf("verification settings: %w", err)
	}
	if !found {
		verification = inheritedSettingView(
			repository.LLMProviderPurposeVerification,
			repository.LLMProviderPurposeStatement,
			statement,
		)
	}
	review, found, err := h.savedSettingView(ctx, repository.LLMProviderPurposeReview)
	if err != nil {
		return llmProviderSettingsView{}, fmt.Errorf("review settings: %w", err)
	}
	if !found {
		review = inheritedSettingView(
			repository.LLMProviderPurposeReview,
			repository.LLMProviderPurposeVerification,
			verification,
		)
	}
	return llmProviderSettingsView{
		Statement: statement, Verification: verification, Review: review,
	}, nil
}

func (h *LLMSettingsHandler) savedSettingView(
	ctx context.Context,
	purpose string,
) (llmProviderSettingView, bool, error) {
	record, err := h.store.GetLLMProviderSetting(ctx, purpose)
	if errors.Is(err, repository.ErrLLMProviderSettingNotFound) {
		return llmProviderSettingView{}, false, nil
	}
	if err != nil {
		return llmProviderSettingView{}, false, err
	}
	return h.recordView(record), true, nil
}

func unconfiguredSettingView(purpose string) llmProviderSettingView {
	return llmProviderSettingView{
		Purpose:            purpose,
		Source:             "unconfigured",
		OverrideConfigured: false,
	}
}

func inheritedSettingView(purpose, parent string, source llmProviderSettingView) llmProviderSettingView {
	return llmProviderSettingView{
		Purpose:            purpose,
		Model:              source.Model,
		BaseURL:            source.BaseURL,
		Provider:           source.Provider,
		Protocol:           source.Protocol,
		ReasoningEffort:    source.ReasoningEffort,
		APIKeySource:       source.APIKeySource,
		APIKeyConfigured:   source.APIKeyConfigured,
		Source:             "inherited",
		OverrideConfigured: false,
		InheritedFrom:      parent,
	}
}

func (h *LLMSettingsHandler) recordView(record repository.LLMProviderSettingRecord) llmProviderSettingView {
	configured := record.APIKeySource == repository.LLMAPIKeySourceEnvironment && strings.TrimSpace(h.fallback.APIKey) != ""
	if record.APIKeySource == repository.LLMAPIKeySourceStored {
		configured = record.EncryptedAPIKey != nil && strings.TrimSpace(*record.EncryptedAPIKey) != ""
	}
	updatedAt := record.UpdatedAt
	return llmProviderSettingView{
		Purpose:            record.Purpose,
		Model:              record.Model,
		BaseURL:            record.BaseURL,
		Provider:           record.Provider,
		Protocol:           record.Protocol,
		ReasoningEffort:    record.ReasoningEffort,
		APIKeySource:       record.APIKeySource,
		APIKeyConfigured:   configured,
		Source:             "saved",
		OverrideConfigured: true,
		UpdatedBy:          record.UpdatedBy,
		UpdatedAt:          &updatedAt,
	}
}

// EffectiveRuntimeConfig resolves one role for automatic injection into a new
// workflow. Stored keys are returned only to the ProblemHandler, which
// immediately replaces them with a runtime token before workflow persistence.
func (h *LLMSettingsHandler) EffectiveRuntimeConfig(
	ctx context.Context,
	purpose string,
) (*domain.LLMRuntimeConfig, error) {
	purpose, err := normalizeLLMSettingPurpose(purpose)
	if err != nil {
		return nil, err
	}
	result, found, err := h.SavedRuntimeConfig(ctx, purpose)
	if err != nil || found {
		return result, err
	}
	switch purpose {
	case repository.LLMProviderPurposeVerification:
		return h.EffectiveRuntimeConfig(ctx, repository.LLMProviderPurposeStatement)
	case repository.LLMProviderPurposeReview:
		return h.EffectiveRuntimeConfig(ctx, repository.LLMProviderPurposeVerification)
	default:
		return nil, fmt.Errorf("saved statement LLM configuration is required; configure Base URL, model, and API key in model settings")
	}
}

// SavedRuntimeConfig distinguishes a durable override from an inherited
// effective value so new workflows can apply request-scoped inheritance.
func (h *LLMSettingsHandler) SavedRuntimeConfig(
	ctx context.Context,
	purpose string,
) (*domain.LLMRuntimeConfig, bool, error) {
	purpose, err := normalizeLLMSettingPurpose(purpose)
	if err != nil {
		return nil, false, err
	}
	record, err := h.store.GetLLMProviderSetting(ctx, purpose)
	if errors.Is(err, repository.ErrLLMProviderSettingNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}

	result := &domain.LLMRuntimeConfig{
		Model:           record.Model,
		BaseURL:         record.BaseURL,
		Provider:        record.Provider,
		Protocol:        record.Protocol,
		ReasoningEffort: record.ReasoningEffort,
	}
	switch record.APIKeySource {
	case repository.LLMAPIKeySourceEnvironment:
		result.APIKeyRef = "env:ANTHROPIC_API_KEY"
	case repository.LLMAPIKeySourceStored:
		if record.EncryptedAPIKey == nil || strings.TrimSpace(*record.EncryptedAPIKey) == "" {
			return nil, false, fmt.Errorf("stored provider key is missing")
		}
		apiKey, err := h.cipher.Open(*record.EncryptedAPIKey)
		if err != nil {
			return nil, false, err
		}
		result.APIKey = apiKey
	default:
		return nil, false, fmt.Errorf("unsupported provider key source %q", record.APIKeySource)
	}
	return result, true, nil
}

func llmSettingsAdminActor(c echo.Context) (string, bool) {
	claims := authmw.GetClaims(c)
	if claims == nil {
		return "local-admin", true
	}
	if !strings.EqualFold(strings.TrimSpace(claims.Role), "admin") {
		return "", false
	}
	if userID := strings.TrimSpace(claims.UserID); userID != "" {
		return "user:" + userID, true
	}
	if subject := strings.TrimSpace(claims.Subject); subject != "" {
		return "subject:" + subject, true
	}
	if email := strings.ToLower(strings.TrimSpace(claims.Email)); email != "" {
		return "email:" + email, true
	}
	return "", false
}

func containsControlRune(value string) bool {
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}
