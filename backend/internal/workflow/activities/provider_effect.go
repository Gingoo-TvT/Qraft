package activities

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
)

const (
	llmProviderEffectType           = "llm_completion/v1"
	embeddingProviderEffectType     = "problem_embedding/v1"
	providerEffectBusyErrorType     = "ProviderEffectBusy"
	providerEffectLeaseRetryBuffer  = time.Second
	providerEffectMinimumRetryDelay = time.Millisecond
)

type providerEffectInvocation struct {
	Key           string
	Type          string
	RequestSHA256 string
}

type providerInvocationError struct {
	cause error
}

func (e *providerInvocationError) Error() string { return e.cause.Error() }
func (e *providerInvocationError) Unwrap() error { return e.cause }

func isProviderInvocationError(err error) bool {
	var invocationErr *providerInvocationError
	return errors.As(err, &invocationErr)
}

func wrapRequiredProviderEffectError(operation string, err error) error {
	var applicationErr *temporal.ApplicationError
	if errors.As(err, &applicationErr) {
		return err
	}
	return fmt.Errorf("%s: %w", operation, err)
}

type problemEmbeddingRequest struct {
	SchemaVersion  int    `json:"schema_version"`
	Provider       string `json:"provider"`
	Model          string `json:"model"`
	ModelVersionID string `json:"model_version_id"`
	Text           string `json:"text"`
}

func temporalProviderEffectKey(ctx context.Context, logicalSubstep string) (string, error) {
	logicalSubstep = strings.TrimSpace(logicalSubstep)
	if logicalSubstep == "" || len(logicalSubstep) > 120 {
		return "", fmt.Errorf("provider effect logical substep is empty or too long")
	}
	if !activity.IsActivity(ctx) {
		return "", fmt.Errorf("stable Temporal provider effect key requires activity context")
	}
	info := activity.GetInfo(ctx)
	if info.WorkflowExecution.ID == "" || info.WorkflowExecution.RunID == "" || info.ActivityID == "" {
		return "", fmt.Errorf("Temporal activity identity is incomplete")
	}
	identity := strings.Join([]string{
		"algoforge:provider-effect:v1",
		info.WorkflowNamespace,
		info.WorkflowExecution.ID,
		info.WorkflowExecution.RunID,
		info.ActivityID,
		logicalSubstep,
	}, "\x00")
	return "temporal:" + sha256Bytes([]byte(identity)), nil
}

func namedProviderEffectKey(scope, stableID string) (string, error) {
	if strings.TrimSpace(scope) == "" || strings.TrimSpace(stableID) == "" {
		return "", fmt.Errorf("provider effect scope and stable ID are required")
	}
	identity := strings.Join([]string{"algoforge:provider-effect:v1", scope, stableID}, "\x00")
	return "operation:" + sha256Bytes([]byte(identity)), nil
}

func providerRequestSHA256(value interface{}) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode provider effect request identity: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

// cachedProviderEffect is single-flight while a lease is live and returns a
// committed provider result on ordinary Temporal activity redelivery.
func (a *Activities) cachedProviderEffect(
	ctx context.Context,
	effect providerEffectInvocation,
	invoke func() (json.RawMessage, error),
) (json.RawMessage, error) {
	if a.deps == nil || a.deps.ProviderEffects == nil {
		return invoke()
	}
	if a.deps.ProviderEffectLease <= 0 {
		return nil, fmt.Errorf("provider effect lease is not configured")
	}
	claim, err := a.deps.ProviderEffects.Acquire(
		ctx, effect.Key, effect.Type, effect.RequestSHA256, a.deps.ProviderEffectLease,
	)
	if err != nil {
		return nil, err
	}
	switch claim.State {
	case repository.ProviderEffectCompleted:
		if len(claim.ResultJSON) == 0 || !json.Valid(claim.ResultJSON) {
			return nil, fmt.Errorf("cached provider effect %q has invalid result JSON", effect.Key)
		}
		return claim.ResultJSON, nil
	case repository.ProviderEffectBusy:
		retryDelay := time.Until(claim.LeasedUntil) + providerEffectLeaseRetryBuffer
		if retryDelay <= 0 {
			retryDelay = providerEffectMinimumRetryDelay
		}
		return nil, temporal.NewApplicationErrorWithOptions(
			fmt.Sprintf("provider effect %q is leased until %s", effect.Key, claim.LeasedUntil.UTC().Format(time.RFC3339Nano)),
			providerEffectBusyErrorType,
			temporal.ApplicationErrorOptions{NextRetryDelay: retryDelay},
		)
	case repository.ProviderEffectAcquired:
	default:
		return nil, fmt.Errorf("provider effect %q returned unknown claim state %q", effect.Key, claim.State)
	}

	result, invokeErr := invoke()
	if invokeErr != nil {
		a.releaseProviderEffect(ctx, effect, claim, invokeErr)
		return nil, invokeErr
	}
	if len(result) == 0 || !json.Valid(result) {
		err := fmt.Errorf("provider effect %q returned invalid result JSON", effect.Key)
		a.releaseProviderEffect(ctx, effect, claim, err)
		return nil, err
	}
	if err := a.deps.ProviderEffects.Complete(
		ctx, effect.Key, effect.Type, effect.RequestSHA256, claim.LeaseToken, result,
	); err != nil {
		a.releaseProviderEffect(ctx, effect, claim, err)
		return nil, err
	}
	return result, nil
}

func (a *Activities) releaseProviderEffect(
	ctx context.Context,
	effect providerEffectInvocation,
	claim repository.ProviderEffectClaim,
	failure error,
) {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := a.deps.ProviderEffects.Fail(
		cleanupCtx,
		effect.Key,
		effect.Type,
		effect.RequestSHA256,
		claim.LeaseToken,
		failure.Error(),
	); err != nil && activity.IsActivity(ctx) {
		activity.GetLogger(ctx).Error("failed to release provider effect lease", "effect_key", effect.Key, "error", err)
	}
}

func (a *Activities) cachedProblemEmbedding(
	ctx context.Context,
	effectKey, text string,
) ([]float32, error) {
	if a == nil || a.deps == nil || a.deps.Embedding == nil {
		return nil, fmt.Errorf("embedding client is not configured")
	}
	if activity.IsActivity(ctx) && a.deps.ProviderEffects == nil {
		return nil, fmt.Errorf("provider effect store is required for Temporal embedding activities")
	}
	if strings.TrimSpace(a.deps.EmbeddingProvider) == "" || strings.TrimSpace(a.deps.EmbeddingModel) == "" {
		return nil, fmt.Errorf("embedding provider and model identities are required for provider effect caching")
	}
	modelVersionID, err := a.deps.configuredStatementModelVersion()
	if err != nil {
		return nil, err
	}

	requestSHA256, err := providerRequestSHA256(problemEmbeddingRequest{
		SchemaVersion:  2,
		Provider:       a.deps.EmbeddingProvider,
		Model:          a.deps.EmbeddingModel,
		ModelVersionID: modelVersionID.String(),
		Text:           text,
	})
	if err != nil {
		return nil, err
	}
	encoded, err := a.cachedProviderEffect(ctx, providerEffectInvocation{
		Key: effectKey, Type: embeddingProviderEffectType, RequestSHA256: requestSHA256,
	}, func() (json.RawMessage, error) {
		embedding, err := a.deps.Embedding.Embed(ctx, text)
		if err != nil {
			return nil, &providerInvocationError{cause: err}
		}
		return json.Marshal(embedding)
	})
	if err != nil {
		return nil, err
	}
	var embedding []float32
	if err := json.Unmarshal(encoded, &embedding); err != nil {
		return nil, fmt.Errorf("decode cached problem embedding: %w", err)
	}
	if len(embedding) == 0 {
		return nil, fmt.Errorf("cached problem embedding is empty")
	}
	return embedding, nil
}

func (a *Activities) cachedTemporalProblemEmbedding(
	ctx context.Context,
	logicalSubstep, text string,
) ([]float32, error) {
	if !activity.IsActivity(ctx) {
		if a == nil || a.deps == nil || a.deps.Embedding == nil {
			return nil, fmt.Errorf("embedding client is not configured")
		}
		return a.deps.Embedding.Embed(ctx, text)
	}
	if a == nil || a.deps == nil || a.deps.ProviderEffects == nil {
		return nil, fmt.Errorf("provider effect store is required for Temporal embedding activities")
	}
	effectKey, err := temporalProviderEffectKey(ctx, "embedding:"+logicalSubstep)
	if err != nil {
		return nil, err
	}
	return a.cachedProblemEmbedding(ctx, effectKey, text)
}
