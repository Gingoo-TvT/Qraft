package activities

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"reflect"
	"strings"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/llm"
	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
	"github.com/google/uuid"
	miniogo "github.com/minio/minio-go/v7"
	"go.temporal.io/sdk/activity"
)

const (
	ArtifactRefSchemaVersion = 1
	artifactKeyPrefix        = "workflow-artifacts/v1/sha256"
	defaultArtifactMediaType = "application/octet-stream"
	maxArtifactSizeBytes     = int64(512 << 20)
)

// ArtifactRef is a durable, content-addressed reference passed between
// Temporal activities. It never points at worker-local storage.
type ArtifactRef struct {
	SchemaVersion  int             `json:"schema_version"`
	PayloadVersion int             `json:"payload_version"`
	Bucket         string          `json:"bucket"`
	Key            string          `json:"key"`
	SHA256         string          `json:"sha256"`
	SizeBytes      int64           `json:"size_bytes"`
	ContentType    string          `json:"content_type"`
	Producer       string          `json:"producer"`
	Provider       string          `json:"provider"`
	Model          string          `json:"model"`
	ModelRevision  string          `json:"model_revision"`
	WorkflowID     string          `json:"workflow_id"`
	LLMCallReceipt *LLMCallReceipt `json:"llm_call_receipt,omitempty"`
}

// LLMCallReceipt is a credential-free identity receipt for one provider call.
// ReturnedModel is never inferred from RequestedModel: an empty value means the
// provider did not return an auditable model identity.
type LLMCallReceipt struct {
	SchemaVersion  int    `json:"schema_version"`
	PromptID       string `json:"prompt_id,omitempty"`
	PromptVersion  string `json:"prompt_version,omitempty"`
	RequestedModel string `json:"requested_model"`
	ReturnedModel  string `json:"returned_model"`
	Provider       string `json:"provider"`
	EndpointID     string `json:"endpoint_id"`
	PromptHash     string `json:"prompt_hash"`
	RequestSHA256  string `json:"request_sha256"`
}

// Equal reports whether two references carry the same durable value identity.
// Nested pointers are compared by their pointed-to values so a Temporal JSON
// round trip cannot change equality merely by allocating new Go pointers.
func (r ArtifactRef) Equal(other ArtifactRef) bool {
	return reflect.DeepEqual(r, other)
}

type ArtifactMetadata struct {
	Producer           string
	Provider           string
	Model              string
	ModelRevision      string
	WorkflowID         string
	PayloadVersion     int
	ArtifactType       string
	SourceType         string
	ArtifactRole       string
	RetentionClass     string
	ProvenanceMetadata json.RawMessage
}

type recordedLLMResponse struct {
	SchemaVersion int                    `json:"schema_version"`
	RequestSHA256 string                 `json:"request_sha256"`
	CallReceipt   *LLMCallReceipt        `json:"call_receipt,omitempty"`
	Response      *llm.Response          `json:"response,omitempty"`
	ProviderError *recordedProviderError `json:"provider_error,omitempty"`
}

type recordedProviderError struct {
	Type                      string `json:"type"`
	Message                   string `json:"message"`
	ResponseBodyBase64        string `json:"response_body_base64,omitempty"`
	ResponseBodySHA256        string `json:"response_body_sha256,omitempty"`
	ResponseEvidenceTruncated bool   `json:"response_evidence_truncated,omitempty"`
}

type llmRequestIdentity struct {
	SchemaVersion int                 `json:"schema_version"`
	Provider      string              `json:"provider"`
	PromptID      string              `json:"prompt_id,omitempty"`
	PromptVersion string              `json:"prompt_version,omitempty"`
	Runtime       *llmRuntimeIdentity `json:"runtime,omitempty"`
	Request       *llm.Request        `json:"request"`
}

type llmRuntimeIdentity struct {
	APIKeyRef       string `json:"api_key_ref,omitempty"`
	BaseURL         string `json:"base_url,omitempty"`
	Protocol        string `json:"protocol,omitempty"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
}

// ArtifactStore persists and verifies data exchanged between activities.
type ArtifactStore interface {
	Put(context.Context, []byte, string, ArtifactMetadata) (ArtifactRef, error)
	Get(context.Context, ArtifactRef) ([]byte, error)
}

type ProvenanceArtifactRecorder interface {
	RecordWorkflowArtifact(context.Context, repository.WorkflowArtifactProvenance) (uuid.UUID, error)
}

type minioArtifactStore struct {
	client *miniogo.Client
	bucket string
}

func NewMinIOArtifactStore(client *miniogo.Client, bucket string) ArtifactStore {
	return &minioArtifactStore{client: client, bucket: bucket}
}

func (s *minioArtifactStore) Put(ctx context.Context, data []byte, contentType string, metadata ArtifactMetadata) (ArtifactRef, error) {
	if int64(len(data)) > maxArtifactSizeBytes {
		return ArtifactRef{}, fmt.Errorf("artifact size %d exceeds limit %d", len(data), maxArtifactSizeBytes)
	}
	if contentType == "" {
		contentType = defaultArtifactMediaType
	}

	digest := sha256.Sum256(data)
	digestHex := hex.EncodeToString(digest[:])
	ref := ArtifactRef{
		SchemaVersion:  ArtifactRefSchemaVersion,
		PayloadVersion: metadata.PayloadVersion,
		Bucket:         s.bucket,
		Key:            artifactKey(digestHex),
		SHA256:         digestHex,
		SizeBytes:      int64(len(data)),
		ContentType:    contentType,
		Producer:       metadata.Producer,
		Provider:       metadata.Provider,
		Model:          metadata.Model,
		ModelRevision:  metadata.ModelRevision,
		WorkflowID:     metadata.WorkflowID,
	}
	if err := ref.Validate(s.bucket); err != nil {
		return ArtifactRef{}, err
	}

	_, err := s.client.PutObject(
		ctx,
		s.bucket,
		ref.Key,
		bytes.NewReader(data),
		ref.SizeBytes,
		miniogo.PutObjectOptions{
			ContentType: contentType,
			UserMetadata: map[string]string{
				"sha256":          digestHex,
				"payload-version": fmt.Sprint(metadata.PayloadVersion),
			},
		},
	)
	if err != nil {
		return ArtifactRef{}, fmt.Errorf("put artifact %s: %w", ref.Key, err)
	}

	return ref, nil
}

func (s *minioArtifactStore) Get(ctx context.Context, ref ArtifactRef) ([]byte, error) {
	if err := ref.Validate(s.bucket); err != nil {
		return nil, err
	}

	obj, err := s.client.GetObject(ctx, s.bucket, ref.Key, miniogo.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("get artifact %s: %w", ref.Key, err)
	}
	defer obj.Close()

	data, err := io.ReadAll(io.LimitReader(obj, ref.SizeBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read artifact %s: %w", ref.Key, err)
	}
	if int64(len(data)) != ref.SizeBytes {
		return nil, fmt.Errorf("artifact %s size mismatch: got %d, want %d", ref.Key, len(data), ref.SizeBytes)
	}
	digest := sha256.Sum256(data)
	if actual := hex.EncodeToString(digest[:]); actual != ref.SHA256 {
		return nil, fmt.Errorf("artifact %s sha256 mismatch: got %s, want %s", ref.Key, actual, ref.SHA256)
	}

	return data, nil
}

func (r ArtifactRef) Validate(expectedBucket string) error {
	if r.SchemaVersion != ArtifactRefSchemaVersion {
		return fmt.Errorf("unsupported artifact ref schema version %d", r.SchemaVersion)
	}
	if r.PayloadVersion != ActivityPayloadVersion {
		return fmt.Errorf("unsupported artifact payload version %d", r.PayloadVersion)
	}
	if r.Bucket == "" || r.Bucket != expectedBucket {
		return fmt.Errorf("artifact bucket %q does not match configured bucket %q", r.Bucket, expectedBucket)
	}
	if r.SizeBytes < 0 || r.SizeBytes > maxArtifactSizeBytes {
		return fmt.Errorf("artifact size %d is outside [0,%d]", r.SizeBytes, maxArtifactSizeBytes)
	}
	if len(r.SHA256) != sha256.Size*2 || strings.ToLower(r.SHA256) != r.SHA256 {
		return fmt.Errorf("invalid artifact sha256 %q", r.SHA256)
	}
	if _, err := hex.DecodeString(r.SHA256); err != nil {
		return fmt.Errorf("invalid artifact sha256 %q: %w", r.SHA256, err)
	}
	if r.Key != artifactKey(r.SHA256) {
		return fmt.Errorf("artifact key %q does not match sha256", r.Key)
	}
	if r.ContentType == "" {
		return fmt.Errorf("artifact content type must not be empty")
	}
	for name, value := range map[string]string{
		"producer":       r.Producer,
		"provider":       r.Provider,
		"model":          r.Model,
		"model_revision": r.ModelRevision,
		"workflow_id":    r.WorkflowID,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("artifact %s must not be empty", name)
		}
	}
	return nil
}

func artifactKey(digest string) string {
	shard := "00"
	if len(digest) >= 2 {
		shard = digest[:2]
	}
	return fmt.Sprintf("%s/%s/%s", artifactKeyPrefix, shard, digest)
}

func (a *Activities) putArtifact(ctx context.Context, data []byte, contentType string) (*ArtifactRef, error) {
	metadata := artifactMetadataFromActivity(ctx)
	if metadata.Producer == "RunSandboxActivity" {
		metadata.Provider = "algoforge-sandbox"
		metadata.Model = "sandbox-runner"
		metadata.ModelRevision = "unversioned"
	}
	return a.putArtifactWithMetadata(ctx, data, contentType, metadata)
}

func (a *Activities) putArtifactWithMetadata(ctx context.Context, data []byte, contentType string, metadata ArtifactMetadata) (*ArtifactRef, error) {
	if a.artifacts == nil {
		return nil, fmt.Errorf("artifact store is not configured")
	}
	if len(metadata.ProvenanceMetadata) > 0 && !json.Valid(metadata.ProvenanceMetadata) {
		return nil, fmt.Errorf("artifact provenance metadata is invalid JSON")
	}
	ref, err := a.artifacts.Put(ctx, data, contentType, metadata)
	if err != nil {
		return nil, err
	}
	if err := a.recordCASProvenance(ctx, ref, metadata); err != nil {
		return nil, err
	}
	return &ref, nil
}

func (a *Activities) recordCASProvenance(ctx context.Context, ref ArtifactRef, metadata ArtifactMetadata) error {
	if a.deps == nil || a.deps.ProvenanceRecorder == nil {
		if activity.IsActivity(ctx) {
			return fmt.Errorf("provenance recorder is not configured")
		}
		return nil
	}
	artifactType := metadata.ArtifactType
	if artifactType == "" {
		artifactType = "workflow_cas_object"
	}
	sourceType := metadata.SourceType
	if sourceType == "" {
		sourceType = "workflow_cas"
	}
	retentionClass := metadata.RetentionClass
	if retentionClass == "" {
		retentionClass = "workflow_cas_unreviewed"
	}
	role := metadata.ArtifactRole
	if role == "" {
		role = fmt.Sprintf("%s:%s:%s", artifactType, ref.Producer, ref.SHA256[:16])
		if activity.IsActivity(ctx) {
			info := activity.GetInfo(ctx)
			role = fmt.Sprintf("%s:%s:attempt:%d:%s", artifactType, info.ActivityID, info.Attempt, ref.SHA256[:16])
		}
	}
	provenanceMetadata := map[string]interface{}{
		"artifact_ref_schema_version": ref.SchemaVersion,
		"payload_version":             ref.PayloadVersion,
		"bucket":                      ref.Bucket,
		"key":                         ref.Key,
		"size_bytes":                  ref.SizeBytes,
		"content_type":                ref.ContentType,
	}
	if len(metadata.ProvenanceMetadata) > 0 {
		var extra map[string]interface{}
		if err := json.Unmarshal(metadata.ProvenanceMetadata, &extra); err != nil {
			return fmt.Errorf("decoding artifact provenance metadata: %w", err)
		}
		for key, value := range extra {
			provenanceMetadata[key] = value
		}
	}
	encodedMetadata, err := json.Marshal(provenanceMetadata)
	if err != nil {
		return fmt.Errorf("encoding artifact provenance metadata: %w", err)
	}
	if _, err := a.deps.ProvenanceRecorder.RecordWorkflowArtifact(ctx, repository.WorkflowArtifactProvenance{
		ArtifactType:   artifactType,
		SourceType:     sourceType,
		ContentHash:    ref.SHA256,
		SourceURI:      fmt.Sprintf("minio://%s/%s", ref.Bucket, ref.Key),
		SourceRevision: fmt.Sprintf("artifact-ref/v%d;payload/v%d", ref.SchemaVersion, ref.PayloadVersion),
		Creator:        ref.Producer,
		Provider:       ref.Provider,
		Model:          ref.Model,
		ModelRevision:  ref.ModelRevision,
		GeneratedAt:    time.Now().UTC(),
		WorkflowID:     ref.WorkflowID,
		ArtifactRole:   role,
		RetentionClass: retentionClass,
		Metadata:       encodedMetadata,
	}); err != nil {
		return fmt.Errorf("recording CAS object provenance: %w", err)
	}
	return nil
}

func artifactMetadataFromActivity(ctx context.Context) ArtifactMetadata {
	metadata := ArtifactMetadata{
		Producer:       "unknown_activity",
		Provider:       "algoforge",
		Model:          "not_applicable",
		ModelRevision:  "not_applicable",
		WorkflowID:     "unknown_workflow",
		PayloadVersion: ActivityPayloadVersion,
	}
	if !activity.IsActivity(ctx) {
		return metadata
	}
	info := activity.GetInfo(ctx)
	if info.ActivityType.Name != "" {
		metadata.Producer = info.ActivityType.Name
	}
	if info.WorkflowExecution.ID != "" {
		metadata.WorkflowID = info.WorkflowExecution.ID
	}
	return metadata
}

// recordLLMResponse stores the normalized provider response envelope. The LLM
// client does not retain the raw HTTP body, so callers must not describe this
// artifact as byte-for-byte wire data.
func (a *Activities) recordLLMResponse(ctx context.Context, request *llm.Request, response *llm.Response) (*ArtifactRef, error) {
	return a.recordLLMExchange(ctx, request, response, nil)
}

func (a *Activities) recordLLMExchange(ctx context.Context, request *llm.Request, response *llm.Response, providerErr error) (*ArtifactRef, error) {
	return a.recordLLMExchangeForEffect(ctx, request, response, providerErr, "")
}

func (a *Activities) recordLLMExchangeForEffect(ctx context.Context, request *llm.Request, response *llm.Response, providerErr error, effectKey string) (*ArtifactRef, error) {
	if request == nil || (response == nil && providerErr == nil) {
		return nil, fmt.Errorf("LLM request and response or provider error are required for provenance")
	}
	providerID, err := a.effectiveLLMProvider(request)
	if err != nil {
		return nil, err
	}
	requestJSON, err := json.Marshal(newLLMRequestIdentity(providerID, request))
	if err != nil {
		return nil, fmt.Errorf("marshal LLM request for provenance: %w", err)
	}
	requestDigest := sha256.Sum256(requestJSON)
	requestSHA256 := hex.EncodeToString(requestDigest[:])
	fallbackBaseURL := ""
	if a != nil && a.deps != nil {
		fallbackBaseURL = a.deps.LLMBaseURL
	}
	callReceipt, err := newLLMCallReceipt(providerID, request, response, requestSHA256, fallbackBaseURL)
	if err != nil {
		return nil, err
	}
	envelope := recordedLLMResponse{
		SchemaVersion: 1,
		RequestSHA256: requestSHA256,
		CallReceipt:   callReceipt,
		Response:      response,
	}
	if providerErr != nil {
		envelope.ProviderError = &recordedProviderError{
			Type:    fmt.Sprintf("%T", providerErr),
			Message: truncate(providerErr.Error(), 4096),
		}
		if body, truncated, ok := llm.ResponseEvidence(providerErr); ok {
			digest := sha256.Sum256(body)
			envelope.ProviderError.ResponseBodyBase64 = base64.StdEncoding.EncodeToString(body)
			envelope.ProviderError.ResponseBodySHA256 = hex.EncodeToString(digest[:])
			envelope.ProviderError.ResponseEvidenceTruncated = truncated
		}
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("marshal LLM response provenance: %w", err)
	}

	modelID := "unknown_returned_model"
	if response != nil && response.ModelObserved && strings.TrimSpace(response.Model) != "" {
		modelID = strings.TrimSpace(response.Model)
	}
	metadata := artifactMetadataFromActivity(ctx)
	metadata.Provider = providerID
	metadata.Model = modelID
	metadata.ModelRevision = modelID
	metadata.ArtifactType = "normalized_llm_response"
	metadata.SourceType = "model_provider_response"
	metadata.RetentionClass = "provider_response_unreviewed"
	if effectKey != "" {
		metadata.ArtifactRole = "normalized_llm_response:" + effectKey
	}
	metadata.ProvenanceMetadata, err = json.Marshal(map[string]interface{}{
		"request_sha256": envelope.RequestSHA256,
		"call_receipt":   callReceipt,
		"outcome": func() string {
			if providerErr != nil {
				return "provider_error"
			}
			return "response"
		}(),
	})
	if err != nil {
		return nil, fmt.Errorf("encoding LLM response provenance metadata: %w", err)
	}
	ref, err := a.putArtifactWithMetadata(ctx, encoded, "application/json", metadata)
	if err != nil {
		return nil, err
	}
	ref.LLMCallReceipt = callReceipt
	return ref, nil
}

func newLLMCallReceipt(
	providerID string,
	request *llm.Request,
	response *llm.Response,
	requestSHA256 string,
	fallbackBaseURL string,
) (*LLMCallReceipt, error) {
	promptJSON, err := json.Marshal(struct {
		SchemaVersion int           `json:"schema_version"`
		System        string        `json:"system,omitempty"`
		Messages      []llm.Message `json:"messages"`
	}{
		SchemaVersion: 1,
		System:        request.System,
		Messages:      request.Messages,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal LLM prompt identity: %w", err)
	}
	promptDigest := sha256.Sum256(promptJSON)
	endpointJSON, err := json.Marshal(struct {
		SchemaVersion int    `json:"schema_version"`
		BaseURL       string `json:"base_url"`
	}{
		SchemaVersion: 1,
		BaseURL:       normalizedLLMEndpoint(request, fallbackBaseURL),
	})
	if err != nil {
		return nil, fmt.Errorf("marshal LLM endpoint identity: %w", err)
	}
	endpointDigest := sha256.Sum256(endpointJSON)

	returnedModel := ""
	if response != nil && response.ModelObserved {
		returnedModel = strings.TrimSpace(response.Model)
	}
	return &LLMCallReceipt{
		SchemaVersion:  1,
		PromptID:       strings.TrimSpace(request.PromptID),
		PromptVersion:  strings.TrimSpace(request.PromptVersion),
		RequestedModel: strings.TrimSpace(request.Model),
		ReturnedModel:  returnedModel,
		Provider:       strings.TrimSpace(providerID),
		EndpointID:     hex.EncodeToString(endpointDigest[:]),
		PromptHash:     hex.EncodeToString(promptDigest[:]),
		RequestSHA256:  requestSHA256,
	}, nil
}

func normalizedLLMEndpoint(request *llm.Request, fallbackBaseURL string) string {
	raw := ""
	if request != nil && request.Runtime != nil {
		raw = strings.TrimSpace(request.Runtime.BaseURL)
	}
	if raw == "" {
		raw = strings.TrimSpace(fallbackBaseURL)
	}
	if raw == "" {
		return "https://api.anthropic.com/v1/messages"
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return raw
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	return strings.TrimRight(parsed.String(), "/")
}

func canonicalPromptID(id string) string {
	switch strings.TrimSpace(id) {
	case "statement":
		return "generate_statement"
	case "solution":
		return "generate_solution"
	case "testdata":
		return "generate_testdata"
	case "review":
		return "llm_review"
	case "editorial":
		return "generate_editorial"
	default:
		return strings.TrimSpace(id)
	}
}

func ensurePromptIdentity(request *llm.Request, logicalSubstep string) error {
	if request == nil {
		return fmt.Errorf("LLM request is required")
	}
	if strings.TrimSpace(request.PromptID) == "" {
		logicalSubstep = strings.TrimSpace(logicalSubstep)
		if logicalSubstep == "" {
			return fmt.Errorf("LLM prompt identity is required")
		}
		if separator := strings.IndexByte(logicalSubstep, ':'); separator >= 0 {
			logicalSubstep = logicalSubstep[:separator]
		}
		request.PromptID = canonicalPromptID(logicalSubstep)
	}
	request.PromptID = canonicalPromptID(request.PromptID)
	if strings.TrimSpace(request.PromptID) == "" || len(request.PromptID) > 120 {
		return fmt.Errorf("LLM prompt ID is empty or too long")
	}
	if strings.TrimSpace(request.PromptVersion) == "" {
		request.PromptVersion = "v1"
	}
	if len(request.PromptVersion) > 32 {
		return fmt.Errorf("LLM prompt version is too long")
	}
	request.PromptID = strings.TrimSpace(request.PromptID)
	request.PromptVersion = strings.TrimSpace(request.PromptVersion)
	return nil
}

func (a *Activities) completeLLMWithProvenance(ctx context.Context, logicalSubstep string, request *llm.Request, retries int) (*llm.Response, *ArtifactRef, error) {
	if err := ensurePromptIdentity(request, logicalSubstep); err != nil {
		return nil, nil, err
	}
	if request == nil {
		return nil, nil, fmt.Errorf("LLM request is required")
	}
	if a.deps == nil || a.deps.LLM == nil {
		return nil, nil, fmt.Errorf("LLM client is not configured")
	}
	if request.Model == "" && a.deps != nil {
		request.Model = a.deps.LLMModel
	}
	providerID, err := a.effectiveLLMProvider(request)
	if err != nil {
		return nil, nil, err
	}

	var (
		response    *llm.Response
		providerErr error
		effectKey   string
	)
	if a.deps == nil || a.deps.ProviderEffects == nil || !activity.IsActivity(ctx) {
		response, providerErr = a.deps.LLM.CompleteWithRetry(ctx, request, retries)
	} else {
		var err error
		effectKey, err = temporalProviderEffectKey(ctx, "llm:"+logicalSubstep)
		if err != nil {
			return nil, nil, err
		}
		requestSHA256, err := providerRequestSHA256(struct {
			SchemaVersion int                 `json:"schema_version"`
			Provider      string              `json:"provider"`
			Runtime       *llmRuntimeIdentity `json:"runtime,omitempty"`
			Request       *llm.Request        `json:"request"`
		}{
			SchemaVersion: 1,
			Provider:      providerID,
			Runtime:       runtimeIdentityFromRequest(request),
			Request:       request,
		})
		if err != nil {
			return nil, nil, err
		}
		var invokedResponse *llm.Response
		var invokedProviderErr error
		encoded, err := a.cachedProviderEffect(ctx, providerEffectInvocation{
			Key: effectKey, Type: llmProviderEffectType, RequestSHA256: requestSHA256,
		}, func() (json.RawMessage, error) {
			providerResponse, err := a.deps.LLM.CompleteWithRetry(ctx, request, retries)
			if err != nil {
				invokedProviderErr = err
				return nil, err
			}
			invokedResponse = providerResponse
			return json.Marshal(providerResponse)
		})
		if err != nil {
			if invokedProviderErr != nil {
				providerErr = invokedProviderErr
			} else if invokedResponse != nil {
				ref, provenanceErr := a.recordLLMExchangeForEffect(ctx, request, invokedResponse, nil, effectKey)
				if provenanceErr != nil {
					return nil, nil, provenanceErr
				}
				return nil, ref, fmt.Errorf("persisting LLM provider effect result: %w", err)
			} else {
				return nil, nil, err
			}
		} else if err := json.Unmarshal(encoded, &response); err != nil {
			return nil, nil, fmt.Errorf("decode cached LLM provider effect %q: %w", effectKey, err)
		}
	}

	ref, provenanceErr := a.recordLLMExchangeForEffect(ctx, request, response, providerErr, effectKey)
	if provenanceErr != nil {
		return nil, nil, provenanceErr
	}
	if providerErr != nil {
		return nil, ref, providerErr
	}
	return response, ref, nil
}

func (a *Activities) effectiveLLMProvider(request *llm.Request) (string, error) {
	if request != nil && request.Runtime != nil {
		if provider := strings.TrimSpace(request.Runtime.Provider); provider != "" {
			return provider, nil
		}
		if strings.TrimSpace(request.Runtime.BaseURL) != "" {
			return "", fmt.Errorf("LLM runtime provider identity is required when base_url is set")
		}
	}
	if a != nil && a.deps != nil && strings.TrimSpace(a.deps.LLMProvider) != "" {
		return strings.TrimSpace(a.deps.LLMProvider), nil
	}
	return "unknown_provider", nil
}

func newLLMRequestIdentity(provider string, request *llm.Request) llmRequestIdentity {
	return llmRequestIdentity{
		SchemaVersion: 1,
		Provider:      provider,
		PromptID:      strings.TrimSpace(request.PromptID),
		PromptVersion: strings.TrimSpace(request.PromptVersion),
		Runtime:       runtimeIdentityFromRequest(request),
		Request:       request,
	}
}

func runtimeIdentityFromRequest(request *llm.Request) *llmRuntimeIdentity {
	if request == nil || request.Runtime == nil {
		return nil
	}
	runtime := &llmRuntimeIdentity{
		APIKeyRef:       strings.TrimSpace(request.Runtime.APIKeyRef),
		BaseURL:         strings.TrimSpace(request.Runtime.BaseURL),
		Protocol:        strings.TrimSpace(request.Runtime.Protocol),
		ReasoningEffort: strings.ToLower(strings.TrimSpace(request.Runtime.ReasoningEffort)),
	}
	if runtime.APIKeyRef == "" && runtime.BaseURL == "" && runtime.Protocol == "" && runtime.ReasoningEffort == "" {
		return nil
	}
	return runtime
}

func (a *Activities) getArtifact(ctx context.Context, ref *ArtifactRef) ([]byte, error) {
	if ref == nil {
		return nil, fmt.Errorf("artifact ref is nil")
	}
	if a.artifacts == nil {
		return nil, fmt.Errorf("artifact store is not configured")
	}
	return a.artifacts.Get(ctx, *ref)
}

func validateActivityPayloadVersion(version int) error {
	// Version zero is the legacy payload shape and remains readable solely for
	// replaying workflows that started before payload versioning was introduced.
	if version == 0 || version == ActivityPayloadVersion {
		return nil
	}
	return fmt.Errorf("unsupported activity payload version %d", version)
}
