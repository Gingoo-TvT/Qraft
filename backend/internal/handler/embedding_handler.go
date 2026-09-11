package handler

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/config"
	"github.com/Gingoo-TvT/Qraft/backend/internal/llm"
	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

const embeddingUIAllowPublicEnv = "ALGOFORGE_EMBEDDING_UI_ALLOW_PUBLIC"

type embeddingStatusStore interface {
	ActiveEmbeddingModelStatuses(ctx context.Context) ([]repository.ActiveEmbeddingModelStatus, error)
}

type embeddingAdminStore interface {
	RegisterEmbeddingModelVersion(ctx context.Context, input repository.EmbeddingModelVersionRegistration) (repository.EmbeddingModelVersionRecord, error)
	GetEmbeddingModelVersion(ctx context.Context, id uuid.UUID) (repository.EmbeddingModelVersionRecord, error)
	ListEmbeddingModelVersions(ctx context.Context, limit int) ([]repository.EmbeddingModelVersionRecord, error)
	PlanEmbeddingBackfill(ctx context.Context, options repository.EmbeddingBackfillPlanOptions) (repository.EmbeddingBackfillPlanReport, error)
	UpdateEmbeddingForVersion(ctx context.Context, input repository.EmbeddingWrite) error
	SwitchActiveEmbeddingPointer(ctx context.Context, options repository.ActivePointerSwitchOptions) (repository.ActivePointerSwitchReport, error)
}

type embeddingProviderSettingsStore interface {
	GetEmbeddingProviderSetting(ctx context.Context) (repository.EmbeddingProviderSettingRecord, error)
	UpsertEmbeddingProviderSetting(ctx context.Context, input repository.EmbeddingProviderSettingUpsert) (repository.EmbeddingProviderSettingRecord, error)
}

// EmbeddingHandler exposes read-only embedding runtime status endpoints.
type EmbeddingHandler struct {
	vectorRepo     embeddingStatusStore
	adminRepo      embeddingAdminStore
	runtimeConfig  config.EmbeddingConfig
	hasRuntimeInfo bool
	settingsStore  embeddingProviderSettingsStore
	settingsCipher settingsSecretCipher
}

// SetPersistentRuntimeSettings enables encrypted credential persistence for
// successful Web deployments. The worker reads the same store at call time.
func (h *EmbeddingHandler) SetPersistentRuntimeSettings(
	store embeddingProviderSettingsStore,
	cipher settingsSecretCipher,
) {
	if h == nil {
		return
	}
	h.settingsStore = store
	h.settingsCipher = cipher
}

// NewEmbeddingHandler creates a handler for active embedding status queries.
func NewEmbeddingHandler(vectorRepo embeddingStatusStore, runtimeConfig ...config.EmbeddingConfig) *EmbeddingHandler {
	adminRepo, _ := vectorRepo.(embeddingAdminStore)
	h := &EmbeddingHandler{vectorRepo: vectorRepo, adminRepo: adminRepo}
	if len(runtimeConfig) > 0 {
		h.runtimeConfig = runtimeConfig[0]
		h.hasRuntimeInfo = true
	}
	return h
}

// HandleStatus: GET /embedding/status
func (h *EmbeddingHandler) HandleStatus(c echo.Context) error {
	if h == nil || h.vectorRepo == nil {
		return internalError(c, "embedding status store is unavailable")
	}
	statuses, err := h.vectorRepo.ActiveEmbeddingModelStatuses(c.Request().Context())
	if err != nil {
		return internalError(c, "failed to read active embedding status: "+err.Error())
	}
	return ok(c, statuses)
}

// HandleModelVersions returns persisted shadow and active versions so the
// activation flow survives browser refreshes.
func (h *EmbeddingHandler) HandleModelVersions(c echo.Context) error {
	if h == nil || h.adminRepo == nil {
		return internalError(c, "embedding admin store is unavailable")
	}
	records, err := h.adminRepo.ListEmbeddingModelVersions(c.Request().Context(), 100)
	if err != nil {
		return internalError(c, "failed to list embedding model versions: "+err.Error())
	}
	return ok(c, records)
}

type embeddingRuntimeSettingsView struct {
	BaseURL                 string     `json:"base_url"`
	ProviderID              string     `json:"provider_id"`
	Model                   string     `json:"model"`
	Dimensions              int        `json:"dimensions"`
	StatementModelVersionID string     `json:"statement_model_version_id,omitempty"`
	TimeoutSec              int        `json:"timeout_sec"`
	APIKeyConfigured        bool       `json:"api_key_configured"`
	Source                  string     `json:"source"`
	UpdatedBy               string     `json:"updated_by,omitempty"`
	UpdatedAt               *time.Time `json:"updated_at,omitempty"`
}

// HandleRuntimeSettings returns credential-free effective embedding settings
// for page reloads. API keys are never returned.
func (h *EmbeddingHandler) HandleRuntimeSettings(c echo.Context) error {
	if h == nil {
		return internalError(c, "embedding runtime settings are unavailable")
	}
	timeoutSec := h.runtimeConfig.TimeoutSec
	if timeoutSec <= 0 {
		timeoutSec = 30
	}
	view := embeddingRuntimeSettingsView{
		BaseURL:                 normalizeRuntimeEmbeddingBaseURL(h.runtimeConfig.BaseURL),
		ProviderID:              h.runtimeConfig.ProviderID(),
		Model:                   strings.TrimSpace(h.runtimeConfig.Model),
		Dimensions:              h.runtimeConfig.Dimensions,
		StatementModelVersionID: strings.TrimSpace(h.runtimeConfig.ExpectedStatementModelVersionID),
		TimeoutSec:              timeoutSec,
		APIKeyConfigured:        strings.TrimSpace(h.runtimeConfig.APIKey) != "",
		Source:                  "deployment",
	}
	if h.settingsStore != nil {
		record, err := h.settingsStore.GetEmbeddingProviderSetting(c.Request().Context())
		if err == nil {
			if embeddingProviderSettingMatchesConfig(record, h.runtimeConfig) && strings.TrimSpace(record.EncryptedAPIKey) != "" {
				updatedAt := record.UpdatedAt
				view.APIKeyConfigured = true
				view.UpdatedBy = record.UpdatedBy
				view.UpdatedAt = &updatedAt
			}
		} else if !errors.Is(err, repository.ErrEmbeddingProviderSettingNotFound) {
			return internalError(c, "failed to read embedding runtime settings: "+err.Error())
		}
	}
	return ok(c, view)
}

type localEmbeddingEndpointConfig struct {
	BaseURL    string `json:"base_url"`
	Model      string `json:"model"`
	APIKey     string `json:"api_key"`
	Dimensions int    `json:"dimensions"`
	TimeoutSec int    `json:"timeout_sec"`
	SampleText string `json:"sample_text"`
}

type localEmbeddingTestResult struct {
	OK           bool    `json:"ok"`
	ProviderID   string  `json:"provider_id"`
	Model        string  `json:"model"`
	BaseURL      string  `json:"base_url"`
	Dimensions   int     `json:"dimensions"`
	LatencyMS    int64   `json:"latency_ms"`
	VectorNorm   float64 `json:"vector_norm"`
	SampleSHA256 string  `json:"sample_sha256"`
}

type localEmbeddingDeployRequest struct {
	Endpoint            localEmbeddingEndpointConfig `json:"endpoint"`
	Provider            string                       `json:"provider"`
	ModelID             string                       `json:"model_id"`
	Revision            string                       `json:"revision"`
	WeightsHash         string                       `json:"weights_hash"`
	Normalization       string                       `json:"normalization"`
	Quantization        string                       `json:"quantization"`
	InstructionTemplate string                       `json:"instruction_template"`
	IndexParams         json.RawMessage              `json:"index_params"`
	EmbeddingKinds      []string                     `json:"embedding_kinds"`
	Actor               string                       `json:"actor"`
	Reason              string                       `json:"reason"`
	DatasetReportSHA256 string                       `json:"dataset_report_sha256"`
	Activate            bool                         `json:"activate"`
	DryRun              bool                         `json:"dry_run"`
	BackfillPlanLimit   int                          `json:"backfill_plan_limit"`
}

type localEmbeddingDeployResult struct {
	Test                    localEmbeddingTestResult                 `json:"test"`
	ModelVersion            repository.EmbeddingModelVersionRecord   `json:"model_version"`
	BackfillPlans           []repository.EmbeddingBackfillPlanReport `json:"backfill_plans,omitempty"`
	ActivationReports       []repository.ActivePointerSwitchReport   `json:"activation_reports,omitempty"`
	RuntimeMatches          bool                                     `json:"runtime_matches"`
	RequiresRuntimeRestart  bool                                     `json:"requires_runtime_restart"`
	ActivationBlockedReason string                                   `json:"activation_blocked_reason,omitempty"`
	RuntimeSettingsSaved    bool                                     `json:"runtime_settings_saved"`
	Env                     map[string]string                        `json:"env"`
}

type localEmbeddingBackfillRequest struct {
	Endpoint       localEmbeddingEndpointConfig `json:"endpoint"`
	ModelVersionID string                       `json:"model_version_id"`
	EmbeddingKind  string                       `json:"embedding_kind"`
	Limit          int                          `json:"limit"`
	AllStale       bool                         `json:"all_stale"`
	DryRun         bool                         `json:"dry_run"`
}

type localEmbeddingBackfillFailure struct {
	ProblemID   string `json:"problem_id"`
	ContentHash string `json:"content_hash"`
	Error       string `json:"error"`
}

type localEmbeddingBackfillResult struct {
	Plan          repository.EmbeddingBackfillPlanReport `json:"plan"`
	DryRun        bool                                   `json:"dry_run"`
	EmbeddedCount int                                    `json:"embedded_count"`
	FailedCount   int                                    `json:"failed_count"`
	ReportSHA256  string                                 `json:"report_sha256"`
	Failures      []localEmbeddingBackfillFailure        `json:"failures,omitempty"`
}

type localEmbeddingActivateRequest struct {
	ModelVersionID       string   `json:"model_version_id"`
	EmbeddingKinds       []string `json:"embedding_kinds"`
	Actor                string   `json:"actor"`
	Reason               string   `json:"reason"`
	DatasetReportSHA256  string   `json:"dataset_report_sha256"`
	DryRun               bool     `json:"dry_run"`
	EndpointBaseURL      string   `json:"endpoint_base_url"`
	EndpointModel        string   `json:"endpoint_model"`
	EndpointDimensions   int      `json:"endpoint_dimensions"`
	AllowRuntimeMismatch bool     `json:"allow_runtime_mismatch"`
}

type localEmbeddingActivateResult struct {
	Reports                 []repository.ActivePointerSwitchReport `json:"reports"`
	RuntimeMatches          bool                                   `json:"runtime_matches"`
	RequiresRuntimeRestart  bool                                   `json:"requires_runtime_restart"`
	ActivationBlockedReason string                                 `json:"activation_blocked_reason,omitempty"`
}

// HandleTestLocal verifies a local OpenAI-compatible embedding endpoint without
// persisting keys or model metadata.
func (h *EmbeddingHandler) HandleTestLocal(c echo.Context) error {
	var req localEmbeddingEndpointConfig
	if err := c.Bind(&req); err != nil {
		return badRequest(c, "INVALID_BODY", "failed to parse request body: "+err.Error())
	}
	endpoint, err := h.endpointWithPersistentKey(c.Request().Context(), req)
	if err != nil {
		return internalError(c, "failed to resolve embedding runtime key: "+err.Error())
	}
	result, err := testLocalEmbeddingEndpoint(c.Request().Context(), endpoint)
	if err != nil {
		return badRequest(c, "EMBEDDING_TEST_FAILED", err.Error())
	}
	return ok(c, result)
}

// HandleDeployLocal tests a local endpoint, registers its immutable model
// version, plans missing backfill rows, and optionally attempts active pointer
// cutover.
func (h *EmbeddingHandler) HandleDeployLocal(c echo.Context) error {
	if h == nil || h.adminRepo == nil {
		return internalError(c, "embedding admin store is unavailable")
	}
	var req localEmbeddingDeployRequest
	if err := c.Bind(&req); err != nil {
		return badRequest(c, "INVALID_BODY", "failed to parse request body: "+err.Error())
	}
	endpoint, err := h.endpointWithPersistentKey(c.Request().Context(), req.Endpoint)
	if err != nil {
		return internalError(c, "failed to resolve embedding runtime key: "+err.Error())
	}
	req.Endpoint = endpoint

	test, err := testLocalEmbeddingEndpoint(c.Request().Context(), req.Endpoint)
	if err != nil {
		return badRequest(c, "EMBEDDING_TEST_FAILED", err.Error())
	}
	if err := validateLocalDeploymentIdentity(req, test); err != nil {
		return badRequest(c, "EMBEDDING_IDENTITY_MISMATCH", err.Error())
	}
	registration := normalizeLocalDeploymentRegistration(req, test)
	modelVersion, err := h.adminRepo.RegisterEmbeddingModelVersion(c.Request().Context(), registration)
	if err != nil {
		return badRequest(c, "EMBEDDING_MODEL_REGISTER_FAILED", err.Error())
	}
	runtimeSettingsSaved, err := h.persistRuntimeSettings(c.Request().Context(), req)
	if err != nil {
		return internalError(c, "failed to persist embedding runtime settings: "+err.Error())
	}

	kinds := normalizeEmbeddingKinds(req.EmbeddingKinds)
	planLimit := req.BackfillPlanLimit
	if planLimit <= 0 {
		planLimit = 20
	}
	if planLimit > 100 {
		planLimit = 100
	}
	var plans []repository.EmbeddingBackfillPlanReport
	for _, kind := range kinds {
		plan, err := h.adminRepo.PlanEmbeddingBackfill(c.Request().Context(), repository.EmbeddingBackfillPlanOptions{
			ToModelVersionID: modelVersion.ID,
			Kind:             kind,
			Limit:            planLimit,
		})
		if err != nil {
			return internalError(c, "failed to plan embedding backfill: "+err.Error())
		}
		plans = append(plans, plan)
	}

	runtimeMatches := h.embeddingModelVersionMatchesRuntime(modelVersion)
	result := localEmbeddingDeployResult{
		Test:                   test,
		ModelVersion:           modelVersion,
		BackfillPlans:          plans,
		RuntimeMatches:         runtimeMatches,
		RequiresRuntimeRestart: !runtimeMatches,
		RuntimeSettingsSaved:   runtimeSettingsSaved,
		Env:                    localEmbeddingEnv(req.Endpoint, modelVersion.ID),
	}
	if req.Activate {
		if !runtimeMatches && !req.DryRun {
			result.ActivationBlockedReason = "current API/worker embedding runtime does not match this endpoint; save the three-field configuration and restart runtime before activation"
			return ok(c, result)
		}
		reports, err := h.switchLocalEmbeddingPointers(
			c.Request().Context(),
			modelVersion.ID,
			kinds,
			req.Actor,
			req.Reason,
			req.DatasetReportSHA256,
			req.DryRun,
			req.Endpoint,
		)
		if err != nil {
			return badRequest(c, "EMBEDDING_ACTIVATION_FAILED", err.Error())
		}
		result.ActivationReports = reports
	}
	return ok(c, result)
}

func (h *EmbeddingHandler) persistRuntimeSettings(
	ctx context.Context,
	req localEmbeddingDeployRequest,
) (bool, error) {
	if h == nil || h.settingsStore == nil || h.settingsCipher == nil {
		return false, nil
	}
	_, runtimeConfig, err := newLocalEmbeddingClient(req.Endpoint)
	if err != nil {
		return false, err
	}
	if len(runtimeConfig.APIKey) > 8192 {
		return false, fmt.Errorf("api_key is too long")
	}
	if containsControlRune(runtimeConfig.APIKey) {
		return false, fmt.Errorf("api_key contains control characters")
	}
	sealed, err := h.settingsCipher.Seal(runtimeConfig.APIKey)
	if err != nil {
		return false, err
	}
	actor := strings.TrimSpace(req.Actor)
	if actor == "" {
		actor = "embedding-web-admin"
	}
	_, err = h.settingsStore.UpsertEmbeddingProviderSetting(ctx, repository.EmbeddingProviderSettingUpsert{
		BaseURL:         runtimeConfig.BaseURL,
		Model:           runtimeConfig.Model,
		Dimensions:      runtimeConfig.Dimensions,
		TimeoutSec:      runtimeConfig.TimeoutSec,
		EncryptedAPIKey: sealed,
		UpdatedBy:       actor,
	})
	if err != nil {
		return false, err
	}
	return true, nil
}

// HandleBackfillLocal embeds missing current rows for a registered local model
// version. It is intentionally bounded and synchronous for the Web UI.
func (h *EmbeddingHandler) HandleBackfillLocal(c echo.Context) error {
	if h == nil || h.adminRepo == nil {
		return internalError(c, "embedding admin store is unavailable")
	}
	var req localEmbeddingBackfillRequest
	if err := c.Bind(&req); err != nil {
		return badRequest(c, "INVALID_BODY", "failed to parse request body: "+err.Error())
	}
	modelVersionID, err := uuid.Parse(strings.TrimSpace(req.ModelVersionID))
	if err != nil {
		return badRequest(c, "INVALID_MODEL_VERSION", "model_version_id must be a UUID")
	}
	kind := strings.TrimSpace(req.EmbeddingKind)
	if kind == "" {
		kind = repository.EmbeddingKindStatement
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	endpoint, err := h.endpointWithPersistentKey(c.Request().Context(), req.Endpoint)
	if err != nil {
		return internalError(c, "failed to resolve embedding runtime key: "+err.Error())
	}
	modelVersion, err := h.adminRepo.GetEmbeddingModelVersion(c.Request().Context(), modelVersionID)
	if errors.Is(err, sql.ErrNoRows) {
		return badRequest(c, "INVALID_MODEL_VERSION", "model_version_id does not reference a registered embedding model version")
	}
	if err != nil {
		return internalError(c, "failed to read embedding model version: "+err.Error())
	}
	client, endpointConfig, err := newLocalEmbeddingClient(endpoint)
	if err != nil {
		return badRequest(c, "INVALID_ENDPOINT", err.Error())
	}
	if !embeddingModelVersionMatchesIdentity(
		modelVersion,
		endpointConfig.ProviderID(),
		endpointConfig.Model,
		endpointConfig.Dimensions,
	) {
		return badRequest(c, "EMBEDDING_IDENTITY_MISMATCH", "registered model version does not match the configured endpoint provider, model, and dimensions")
	}
	plan, err := h.adminRepo.PlanEmbeddingBackfill(c.Request().Context(), repository.EmbeddingBackfillPlanOptions{
		ToModelVersionID: modelVersionID,
		Kind:             kind,
		AllStale:         req.AllStale,
		Limit:            limit,
	})
	if err != nil {
		return badRequest(c, "BACKFILL_PLAN_FAILED", err.Error())
	}
	result := localEmbeddingBackfillResult{Plan: plan, DryRun: req.DryRun}
	if req.DryRun {
		result.ReportSHA256 = stableJSONSHA256(result)
		return ok(c, result)
	}

	for _, candidate := range plan.Candidates {
		vec, err := client.Embed(c.Request().Context(), candidate.Text)
		if err != nil {
			result.FailedCount++
			result.Failures = append(result.Failures, localEmbeddingBackfillFailure{
				ProblemID:   candidate.ProblemID.String(),
				ContentHash: candidate.ContentHash,
				Error:       err.Error(),
			})
			continue
		}
		if err := h.adminRepo.UpdateEmbeddingForVersion(c.Request().Context(), repository.EmbeddingWrite{
			ProblemID:      candidate.ProblemID,
			ModelVersionID: modelVersionID,
			Kind:           kind,
			ContentHash:    candidate.ContentHash,
			Embedding:      vec,
		}); err != nil {
			result.FailedCount++
			result.Failures = append(result.Failures, localEmbeddingBackfillFailure{
				ProblemID:   candidate.ProblemID.String(),
				ContentHash: candidate.ContentHash,
				Error:       err.Error(),
			})
			continue
		}
		result.EmbeddedCount++
	}
	result.ReportSHA256 = stableJSONSHA256(result)
	return ok(c, result)
}

func (h *EmbeddingHandler) endpointWithPersistentKey(
	ctx context.Context,
	endpoint localEmbeddingEndpointConfig,
) (localEmbeddingEndpointConfig, error) {
	if strings.TrimSpace(endpoint.APIKey) != "" || h == nil {
		return endpoint, nil
	}
	if h.settingsStore != nil && h.settingsCipher != nil {
		record, err := h.settingsStore.GetEmbeddingProviderSetting(ctx)
		if err != nil && !errors.Is(err, repository.ErrEmbeddingProviderSettingNotFound) {
			return endpoint, err
		}
		if err == nil {
			baseURL, err := normalizeLocalEmbeddingBaseURL(endpoint.BaseURL)
			if err != nil {
				return endpoint, err
			}
			dimensions := endpoint.Dimensions
			if dimensions <= 0 {
				dimensions = 1536
			}
			if strings.TrimRight(strings.TrimSpace(record.BaseURL), "/") == baseURL &&
				strings.TrimSpace(record.Model) == strings.TrimSpace(endpoint.Model) &&
				record.Dimensions == dimensions {
				key, err := h.settingsCipher.Open(record.EncryptedAPIKey)
				if err != nil {
					return endpoint, err
				}
				if strings.TrimSpace(key) != "" {
					endpoint.APIKey = key
					return endpoint, nil
				}
			}
		}
	}
	if h.localEndpointMatchesRuntime(endpoint) && strings.TrimSpace(h.runtimeConfig.APIKey) != "" {
		endpoint.APIKey = h.runtimeConfig.APIKey
	}
	return endpoint, nil
}

// HandleActivateLocal commits or dry-runs active pointer cutover for an already
// registered local embedding model version.
func (h *EmbeddingHandler) HandleActivateLocal(c echo.Context) error {
	if h == nil || h.adminRepo == nil {
		return internalError(c, "embedding admin store is unavailable")
	}
	var req localEmbeddingActivateRequest
	if err := c.Bind(&req); err != nil {
		return badRequest(c, "INVALID_BODY", "failed to parse request body: "+err.Error())
	}
	modelVersionID, err := uuid.Parse(strings.TrimSpace(req.ModelVersionID))
	if err != nil {
		return badRequest(c, "INVALID_MODEL_VERSION", "model_version_id must be a UUID")
	}
	modelVersion, err := h.adminRepo.GetEmbeddingModelVersion(c.Request().Context(), modelVersionID)
	if errors.Is(err, sql.ErrNoRows) {
		return badRequest(c, "INVALID_MODEL_VERSION", "model_version_id does not reference a registered embedding model version")
	}
	if err != nil {
		return internalError(c, "failed to read embedding model version: "+err.Error())
	}
	runtimeMatches := h.embeddingModelVersionMatchesRuntime(modelVersion)
	result := localEmbeddingActivateResult{
		Reports:                []repository.ActivePointerSwitchReport{},
		RuntimeMatches:         runtimeMatches,
		RequiresRuntimeRestart: !runtimeMatches,
	}
	if !runtimeMatches && !req.DryRun {
		result.ActivationBlockedReason = "current API/worker embedding runtime identity does not match this registered model version; save the three-field configuration and restart runtime before activation"
		return ok(c, result)
	}
	endpoint := localEmbeddingEndpointConfig{
		BaseURL:    strings.TrimPrefix(modelVersion.Provider, "openai-compatible:"),
		Model:      modelVersion.ModelID,
		Dimensions: modelVersion.Dimensions,
	}
	kinds := normalizeEmbeddingKinds(req.EmbeddingKinds)
	reports, err := h.switchLocalEmbeddingPointers(
		c.Request().Context(),
		modelVersionID,
		kinds,
		req.Actor,
		req.Reason,
		req.DatasetReportSHA256,
		req.DryRun,
		endpoint,
	)
	if err != nil {
		return badRequest(c, "EMBEDDING_ACTIVATION_FAILED", err.Error())
	}
	result.Reports = reports
	return ok(c, result)
}

func testLocalEmbeddingEndpoint(ctx context.Context, req localEmbeddingEndpointConfig) (localEmbeddingTestResult, error) {
	client, cfg, err := newLocalEmbeddingClient(req)
	if err != nil {
		return localEmbeddingTestResult{}, err
	}
	sample := strings.TrimSpace(req.SampleText)
	if sample == "" {
		sample = "Qraft embedding readiness check"
	}
	start := time.Now()
	vec, err := client.Embed(ctx, sample)
	if err != nil {
		return localEmbeddingTestResult{}, err
	}
	var norm float64
	for _, v := range vec {
		norm += float64(v) * float64(v)
	}
	return localEmbeddingTestResult{
		OK:           true,
		ProviderID:   cfg.ProviderID(),
		Model:        cfg.Model,
		BaseURL:      cfg.BaseURL,
		Dimensions:   len(vec),
		LatencyMS:    time.Since(start).Milliseconds(),
		VectorNorm:   math.Sqrt(norm),
		SampleSHA256: sha256Hex(sample),
	}, nil
}

func newLocalEmbeddingClient(req localEmbeddingEndpointConfig) (*llm.EmbeddingClient, config.EmbeddingConfig, error) {
	baseURL, err := normalizeLocalEmbeddingBaseURL(req.BaseURL)
	if err != nil {
		return nil, config.EmbeddingConfig{}, err
	}
	model := strings.TrimSpace(req.Model)
	if model == "" {
		return nil, config.EmbeddingConfig{}, fmt.Errorf("model is required")
	}
	dimensions := req.Dimensions
	if dimensions <= 0 {
		dimensions = 1536
	}
	timeoutSec := req.TimeoutSec
	if timeoutSec <= 0 {
		timeoutSec = 30
	}
	apiKey := strings.TrimSpace(req.APIKey)
	if apiKey == "" {
		apiKey = "local-placeholder"
	}
	cfg := config.EmbeddingConfig{
		APIKey:     apiKey,
		BaseURL:    baseURL,
		Model:      model,
		Dimensions: dimensions,
		TimeoutSec: timeoutSec,
		Enabled:    true,
	}
	client, err := llm.NewEmbeddingClient(cfg)
	if err != nil {
		return nil, config.EmbeddingConfig{}, err
	}
	return client, cfg, nil
}

func normalizeLocalEmbeddingBaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("base_url is required")
	}
	u, err := url.ParseRequestURI(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("base_url must be an absolute URL")
	}
	if u.User != nil {
		return "", fmt.Errorf("base_url must not contain credentials")
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return "", fmt.Errorf("base_url scheme must be http or https")
	}
	host := strings.ToLower(u.Hostname())
	allowPublic := strings.EqualFold(strings.TrimSpace(os.Getenv(embeddingUIAllowPublicEnv)), "true")
	if !isAllowedLocalEmbeddingHost(host) && !allowPublic {
		return "", fmt.Errorf("base_url host %q is not local/private; public embedding endpoints require explicit operator deployment outside this UI", host)
	}
	u.RawQuery = ""
	u.ForceQuery = false
	u.Fragment = ""
	return strings.TrimRight(u.String(), "/"), nil
}

func isAllowedLocalEmbeddingHost(host string) bool {
	if host == "localhost" || host == "host.docker.internal" {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()
}

func normalizeLocalDeploymentRegistration(req localEmbeddingDeployRequest, test localEmbeddingTestResult) repository.EmbeddingModelVersionRegistration {
	revision := strings.TrimSpace(req.Revision)
	if revision == "" {
		revision = "local-runtime"
	}
	weightsHash := strings.TrimSpace(req.WeightsHash)
	if weightsHash == "" {
		weightsHash = sha256Hex(strings.Join([]string{test.BaseURL, test.Model, revision, fmt.Sprint(test.Dimensions)}, "\n"))
	}
	normalization := strings.TrimSpace(req.Normalization)
	if normalization == "" {
		normalization = "mrl-1536+l2"
	}
	quantization := strings.TrimSpace(req.Quantization)
	if quantization == "" {
		quantization = "float32"
	}
	instructionTemplate := strings.TrimSpace(req.InstructionTemplate)
	if instructionTemplate == "" {
		instructionTemplate = "title\\n\\nstatement\\n\\none_line_hint"
	}
	indexParams := req.IndexParams
	if strings.TrimSpace(string(indexParams)) == "" {
		indexParams = json.RawMessage(`{"index":"ivfflat","lists":100,"distance":"cosine"}`)
	}
	return repository.EmbeddingModelVersionRegistration{
		Provider:            test.ProviderID,
		ModelID:             test.Model,
		Revision:            revision,
		WeightsHash:         weightsHash,
		Dimensions:          test.Dimensions,
		Normalization:       normalization,
		Quantization:        quantization,
		InstructionTemplate: instructionTemplate,
		IndexParams:         indexParams,
	}
}

func validateLocalDeploymentIdentity(req localEmbeddingDeployRequest, test localEmbeddingTestResult) error {
	if provider := strings.TrimSpace(req.Provider); provider != "" && provider != test.ProviderID {
		return fmt.Errorf("provider must match the tested endpoint identity")
	}
	if modelID := strings.TrimSpace(req.ModelID); modelID != "" && modelID != test.Model {
		return fmt.Errorf("model_id must match the tested endpoint model")
	}
	return nil
}

func normalizeRuntimeEmbeddingBaseURL(value string) string {
	return strings.TrimRight(strings.TrimSpace(value), "/")
}

func embeddingProviderSettingMatchesConfig(record repository.EmbeddingProviderSettingRecord, runtimeConfig config.EmbeddingConfig) bool {
	recordProviderID := (config.EmbeddingConfig{BaseURL: record.BaseURL}).ProviderID()
	return recordProviderID != "" &&
		recordProviderID == runtimeConfig.ProviderID() &&
		strings.TrimSpace(record.Model) == strings.TrimSpace(runtimeConfig.Model) &&
		record.Dimensions == runtimeConfig.Dimensions
}

func embeddingModelVersionMatchesIdentity(record repository.EmbeddingModelVersionRecord, provider, model string, dimensions int) bool {
	return strings.TrimSpace(record.Provider) == strings.TrimSpace(provider) &&
		strings.TrimSpace(record.ModelID) == strings.TrimSpace(model) &&
		record.Dimensions == dimensions
}

func (h *EmbeddingHandler) embeddingModelVersionMatchesRuntime(record repository.EmbeddingModelVersionRecord) bool {
	if h == nil || !h.hasRuntimeInfo || !h.runtimeConfig.Enabled {
		return false
	}
	expectedID, err := uuid.Parse(strings.TrimSpace(h.runtimeConfig.ExpectedStatementModelVersionID))
	if err != nil || expectedID != record.ID {
		return false
	}
	return embeddingModelVersionMatchesIdentity(
		record,
		h.runtimeConfig.ProviderID(),
		h.runtimeConfig.Model,
		h.runtimeConfig.Dimensions,
	)
}

func (h *EmbeddingHandler) switchLocalEmbeddingPointers(
	ctx context.Context,
	modelVersionID uuid.UUID,
	kinds []string,
	actor string,
	reason string,
	datasetReportSHA256 string,
	dryRun bool,
	endpoint localEmbeddingEndpointConfig,
) ([]repository.ActivePointerSwitchReport, error) {
	actor = strings.TrimSpace(actor)
	if actor == "" {
		actor = "embedding-web-admin"
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "local embedding model deployment"
	}
	datasetReportSHA256 = strings.TrimSpace(datasetReportSHA256)
	if datasetReportSHA256 == "" {
		datasetReportSHA256 = sha256Hex(strings.Join([]string{
			modelVersionID.String(),
			endpoint.BaseURL,
			endpoint.Model,
			fmt.Sprint(endpoint.Dimensions),
			reason,
		}, "\n"))
	}
	var reports []repository.ActivePointerSwitchReport
	for _, kind := range kinds {
		report, err := h.adminRepo.SwitchActiveEmbeddingPointer(ctx, repository.ActivePointerSwitchOptions{
			Kind:                kind,
			Operation:           repository.EmbeddingSwitchOperationCutover,
			ToModelVersionID:    modelVersionID,
			Actor:               actor,
			Reason:              reason,
			DatasetReportSHA256: datasetReportSHA256,
			DryRun:              dryRun,
		})
		if err != nil {
			return reports, err
		}
		reports = append(reports, report)
	}
	return reports, nil
}

func (h *EmbeddingHandler) localEndpointMatchesRuntime(endpoint localEmbeddingEndpointConfig) bool {
	if h == nil || !h.hasRuntimeInfo || !h.runtimeConfig.Enabled {
		return false
	}
	baseURL, err := normalizeLocalEmbeddingBaseURL(endpoint.BaseURL)
	if err != nil {
		return false
	}
	dimensions := endpoint.Dimensions
	if dimensions <= 0 {
		dimensions = 1536
	}
	return strings.TrimRight(strings.TrimSpace(h.runtimeConfig.BaseURL), "/") == baseURL &&
		strings.TrimSpace(h.runtimeConfig.Model) == strings.TrimSpace(endpoint.Model) &&
		h.runtimeConfig.Dimensions == dimensions
}

func localEmbeddingEnv(endpoint localEmbeddingEndpointConfig, modelVersionID uuid.UUID) map[string]string {
	dimensions := endpoint.Dimensions
	if dimensions <= 0 {
		dimensions = 1536
	}
	return map[string]string{
		"ALGOFORGE_EMBEDDING_BASE_URL":                            strings.TrimRight(strings.TrimSpace(endpoint.BaseURL), "/"),
		"ALGOFORGE_EMBEDDING_MODEL":                               strings.TrimSpace(endpoint.Model),
		"ALGOFORGE_EMBEDDING_DIMENSIONS":                          fmt.Sprint(dimensions),
		"ALGOFORGE_EMBEDDING_ENABLED":                             "true",
		"ALGOFORGE_EMBEDDING_EXPECTED_STATEMENT_MODEL_VERSION_ID": modelVersionID.String(),
	}
}

func normalizeEmbeddingKinds(kinds []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, kind := range kinds {
		switch strings.ToLower(strings.TrimSpace(kind)) {
		case repository.EmbeddingKindStatement:
			if !seen[repository.EmbeddingKindStatement] {
				out = append(out, repository.EmbeddingKindStatement)
				seen[repository.EmbeddingKindStatement] = true
			}
		case repository.EmbeddingKindSolution:
			if !seen[repository.EmbeddingKindSolution] {
				out = append(out, repository.EmbeddingKindSolution)
				seen[repository.EmbeddingKindSolution] = true
			}
		}
	}
	if len(out) == 0 {
		return []string{repository.EmbeddingKindStatement}
	}
	return out
}

func sha256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func stableJSONSHA256(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return sha256Hex(fmt.Sprint(value))
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}
