package activities

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/llm"
	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
	"github.com/google/uuid"
	"go.temporal.io/sdk/testsuite"
)

type captureArtifactStore struct {
	data     []byte
	metadata ArtifactMetadata
}

type captureProvenanceRecorder struct {
	records []repository.WorkflowArtifactProvenance
}

type failingLLM struct {
	err error
}

type staticLLM struct {
	response *llm.Response
}

func (s staticLLM) CompleteWithRetry(context.Context, *llm.Request, int) (*llm.Response, error) {
	return s.response, nil
}

func (f failingLLM) CompleteWithRetry(context.Context, *llm.Request, int) (*llm.Response, error) {
	return nil, f.err
}

func (r *captureProvenanceRecorder) RecordWorkflowArtifact(_ context.Context, record repository.WorkflowArtifactProvenance) (uuid.UUID, error) {
	r.records = append(r.records, record)
	return uuid.New(), nil
}

func (s *captureArtifactStore) Put(_ context.Context, data []byte, _ string, metadata ArtifactMetadata) (ArtifactRef, error) {
	s.data = append([]byte(nil), data...)
	s.metadata = metadata
	digest := sha256.Sum256(data)
	digestHex := hex.EncodeToString(digest[:])
	return ArtifactRef{
		SchemaVersion:  ArtifactRefSchemaVersion,
		PayloadVersion: ActivityPayloadVersion,
		Bucket:         "fixture",
		Key:            artifactKey(digestHex),
		SHA256:         digestHex,
		SizeBytes:      int64(len(data)),
		ContentType:    "application/json",
		Producer:       metadata.Producer,
		Provider:       metadata.Provider,
		Model:          metadata.Model,
		ModelRevision:  metadata.ModelRevision,
		WorkflowID:     metadata.WorkflowID,
	}, nil
}

func (s *captureArtifactStore) Get(context.Context, ArtifactRef) ([]byte, error) {
	return append([]byte(nil), s.data...), nil
}

func TestArtifactRefValidate(t *testing.T) {
	digest := sha256.Sum256([]byte("fixture"))
	digestHex := hex.EncodeToString(digest[:])
	ref := ArtifactRef{
		SchemaVersion:  ArtifactRefSchemaVersion,
		PayloadVersion: ActivityPayloadVersion,
		Bucket:         "algoforge",
		Key:            artifactKey(digestHex),
		SHA256:         digestHex,
		SizeBytes:      7,
		ContentType:    "text/plain",
		Producer:       "fixture",
		Provider:       "test",
		Model:          "not_applicable",
		ModelRevision:  "not_applicable",
		WorkflowID:     "fixture-workflow",
	}

	if err := ref.Validate("algoforge"); err != nil {
		t.Fatalf("valid artifact ref rejected: %v", err)
	}
}

func TestArtifactRefEqualUsesValueIdentityAcrossJSONRoundTrip(t *testing.T) {
	digest := strings.Repeat("a", sha256.Size*2)
	original := ArtifactRef{
		SchemaVersion:  ArtifactRefSchemaVersion,
		PayloadVersion: ActivityPayloadVersion,
		Bucket:         "algoforge",
		Key:            artifactKey(digest),
		SHA256:         digest,
		SizeBytes:      17,
		ContentType:    "application/json",
		Producer:       "LLMReviewActivity",
		Provider:       "fixture-provider",
		Model:          "fixture-model",
		ModelRevision:  "fixture-model-r1",
		WorkflowID:     "fixture-workflow",
		LLMCallReceipt: &LLMCallReceipt{
			SchemaVersion:  1,
			RequestedModel: "fixture-model",
			ReturnedModel:  "fixture-model-r1",
			Provider:       "fixture-provider",
			EndpointID:     strings.Repeat("b", sha256.Size*2),
			PromptHash:     strings.Repeat("c", sha256.Size*2),
			RequestSHA256:  strings.Repeat("d", sha256.Size*2),
		},
	}

	encoded, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var roundTripped ArtifactRef
	if err := json.Unmarshal(encoded, &roundTripped); err != nil {
		t.Fatal(err)
	}
	if original.LLMCallReceipt == roundTripped.LLMCallReceipt {
		t.Fatal("JSON round trip unexpectedly preserved the receipt pointer")
	}
	if !original.Equal(roundTripped) {
		t.Fatalf("equal serialized artifact identities differ: original=%+v round_tripped=%+v", original, roundTripped)
	}

	receiptDrift := roundTripped
	driftedReceipt := *roundTripped.LLMCallReceipt
	receiptDrift.LLMCallReceipt = &driftedReceipt
	receiptDrift.LLMCallReceipt.ReturnedModel = "different-model"
	if original.Equal(receiptDrift) {
		t.Fatal("receipt content drift did not change artifact identity")
	}
	metadataDrift := roundTripped
	metadataDrift.ModelRevision = "different-revision"
	if original.Equal(metadataDrift) {
		t.Fatal("artifact metadata drift did not change artifact identity")
	}
	withoutReceipt := roundTripped
	withoutReceipt.LLMCallReceipt = nil
	if original.Equal(withoutReceipt) {
		t.Fatal("missing receipt did not change artifact identity")
	}
}

func TestArtifactRefValidateRejectsTampering(t *testing.T) {
	digest := strings.Repeat("a", sha256.Size*2)
	base := ArtifactRef{
		SchemaVersion:  ArtifactRefSchemaVersion,
		PayloadVersion: ActivityPayloadVersion,
		Bucket:         "algoforge",
		Key:            artifactKey(digest),
		SHA256:         digest,
		SizeBytes:      1,
		ContentType:    "text/plain",
		Producer:       "fixture",
		Provider:       "test",
		Model:          "not_applicable",
		ModelRevision:  "not_applicable",
		WorkflowID:     "fixture-workflow",
	}

	tests := map[string]ArtifactRef{
		"unknown schema":   func() ArtifactRef { r := base; r.SchemaVersion++; return r }(),
		"unknown payload":  func() ArtifactRef { r := base; r.PayloadVersion++; return r }(),
		"wrong bucket":     func() ArtifactRef { r := base; r.Bucket = "other"; return r }(),
		"wrong key":        func() ArtifactRef { r := base; r.Key += "-other"; return r }(),
		"bad digest":       func() ArtifactRef { r := base; r.SHA256 = "not-a-digest"; return r }(),
		"negative size":    func() ArtifactRef { r := base; r.SizeBytes = -1; return r }(),
		"missing type":     func() ArtifactRef { r := base; r.ContentType = ""; return r }(),
		"missing model":    func() ArtifactRef { r := base; r.Model = ""; return r }(),
		"missing workflow": func() ArtifactRef { r := base; r.WorkflowID = ""; return r }(),
	}

	for name, ref := range tests {
		t.Run(name, func(t *testing.T) {
			if err := ref.Validate("algoforge"); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestValidateActivityPayloadVersion(t *testing.T) {
	for _, version := range []int{0, ActivityPayloadVersion} {
		if err := validateActivityPayloadVersion(version); err != nil {
			t.Fatalf("version %d rejected: %v", version, err)
		}
	}
	if err := validateActivityPayloadVersion(ActivityPayloadVersion + 1); err == nil {
		t.Fatal("expected unknown version to be rejected")
	}
}

func TestRecordLLMResponseStoresNormalizedEnvelopeAndRequestHash(t *testing.T) {
	store := &captureArtifactStore{}
	provenance := &captureProvenanceRecorder{}
	activities := &Activities{deps: &Dependencies{ProvenanceRecorder: provenance}, artifacts: store}
	request := &llm.Request{
		Model:     "requested-model",
		MaxTokens: 10,
		Messages:  []llm.Message{{Role: "user", Content: "private prompt fixture"}},
	}
	response := &llm.Response{
		ID:            "response-id",
		Model:         "resolved-model-revision",
		ModelObserved: true,
		Content: []llm.ContentBlock{{
			Type: "text",
			Text: "result",
		}},
	}

	ref, err := activities.recordLLMResponse(context.Background(), request, response)
	if err != nil {
		t.Fatalf("record response: %v", err)
	}
	if ref.Model != response.Model || ref.ModelRevision != response.Model {
		t.Fatalf("response model provenance not preserved: %+v", ref)
	}
	if ref.LLMCallReceipt == nil || ref.LLMCallReceipt.RequestedModel != request.Model ||
		ref.LLMCallReceipt.ReturnedModel != response.Model || ref.LLMCallReceipt.Provider != "unknown_provider" {
		t.Fatalf("call receipt identity not preserved: %+v", ref.LLMCallReceipt)
	}
	if len(ref.LLMCallReceipt.EndpointID) != sha256.Size*2 || len(ref.LLMCallReceipt.PromptHash) != sha256.Size*2 {
		t.Fatalf("call receipt hashes are not SHA-256 identities: %+v", ref.LLMCallReceipt)
	}
	if strings.Contains(string(store.data), "private prompt fixture") {
		t.Fatal("normalized response artifact leaked request content")
	}
	var envelope recordedLLMResponse
	if err := json.Unmarshal(store.data, &envelope); err != nil {
		t.Fatalf("decode recorded envelope: %v", err)
	}
	if len(envelope.RequestSHA256) != sha256.Size*2 || envelope.Response.ID != response.ID {
		t.Fatalf("unexpected response envelope: %+v", envelope)
	}
	if envelope.CallReceipt == nil || *envelope.CallReceipt != *ref.LLMCallReceipt {
		t.Fatalf("response envelope call receipt mismatch: %+v / %+v", envelope.CallReceipt, ref.LLMCallReceipt)
	}
	if len(provenance.records) != 1 {
		t.Fatalf("provenance records = %d, want 1", len(provenance.records))
	}
	record := provenance.records[0]
	if record.ContentHash != ref.SHA256 || record.WorkflowID != ref.WorkflowID || record.ArtifactRole == "" {
		t.Fatalf("normalized response provenance does not match CAS ref: %+v / %+v", record, ref)
	}
}

func TestRecordLLMResponseDoesNotPromoteCompatibilityModelToReturnedIdentity(t *testing.T) {
	store := &captureArtifactStore{}
	activities := &Activities{artifacts: store}
	request := &llm.Request{Model: "requested-model", Messages: []llm.Message{{Role: "user", Content: "fixture"}}}
	response := &llm.Response{Model: "requested-model", ModelObserved: false}

	ref, err := activities.recordLLMResponse(context.Background(), request, response)
	if err != nil {
		t.Fatal(err)
	}
	if ref.Model != "unknown_returned_model" || ref.ModelRevision != "unknown_returned_model" {
		t.Fatalf("unobserved compatibility model entered artifact identity: %+v", ref)
	}
	if ref.LLMCallReceipt == nil || ref.LLMCallReceipt.ReturnedModel != "" {
		t.Fatalf("unobserved compatibility model entered call receipt: %+v", ref.LLMCallReceipt)
	}
	independence := assessOracleIndependence(ref, artifactWithCallReceipt("oracle-model", "oracle-provider", "oracle-endpoint"))
	if independence.Status != OracleIdentityCorrelated || independence.Reason != "missing_returned_identity" {
		t.Fatalf("unobserved model did not fail closed: %+v", independence)
	}
}

func TestProviderFailureStillProducesCASAndProvenance(t *testing.T) {
	store := &captureArtifactStore{}
	provenance := &captureProvenanceRecorder{}
	providerErr := errors.New("injected provider timeout")
	activities := &Activities{
		deps: &Dependencies{
			LLM:                failingLLM{err: providerErr},
			ProvenanceRecorder: provenance,
		},
		artifacts: store,
	}
	request := &llm.Request{Model: "requested-model", Messages: []llm.Message{{Role: "user", Content: "private"}}}
	response, ref, err := activities.completeLLMWithProvenance(context.Background(), "fixture", request, 2)
	if !errors.Is(err, providerErr) || response != nil || ref == nil {
		t.Fatalf("response=%v ref=%v err=%v", response, ref, err)
	}
	if len(provenance.records) != 1 || provenance.records[0].ArtifactType != "normalized_llm_response" {
		t.Fatalf("provider failure provenance = %+v", provenance.records)
	}
	var envelope recordedLLMResponse
	if err := json.Unmarshal(store.data, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Response != nil || envelope.ProviderError == nil || !strings.Contains(envelope.ProviderError.Message, "timeout") {
		t.Fatalf("provider error envelope = %+v", envelope)
	}
	if ref.LLMCallReceipt == nil || envelope.CallReceipt == nil || envelope.CallReceipt.ReturnedModel != "" {
		t.Fatalf("provider failure call receipt = ref:%+v envelope:%+v", ref.LLMCallReceipt, envelope.CallReceipt)
	}
	if ref.Model != "unknown_returned_model" || ref.ModelRevision != "unknown_returned_model" {
		t.Fatalf("provider failure substituted requested model as returned identity: %+v", ref)
	}
	if strings.Contains(string(store.data), "private") {
		t.Fatal("provider error envelope leaked request content")
	}
}

func TestLLMCallReceiptUsesOpaqueStableEndpointAndPromptIdentities(t *testing.T) {
	request := &llm.Request{
		Model:  "requested-model",
		System: "system fixture",
		Messages: []llm.Message{{
			Role: "user", Content: "prompt fixture",
		}},
		Runtime: &llm.RuntimeConfig{
			APIKeyRef: "runtime:secret-reference",
			BaseURL:   "HTTPS://Example.COM/v1/",
			Provider:  "fixture-provider",
			Protocol:  "openai-chat",
		},
	}
	receipt, err := newLLMCallReceipt("fixture-provider", request, &llm.Response{Model: "returned-model", ModelObserved: true}, strings.Repeat("a", sha256.Size*2), "")
	if err != nil {
		t.Fatal(err)
	}
	if receipt.RequestedModel != "requested-model" || receipt.ReturnedModel != "returned-model" || receipt.Provider != "fixture-provider" {
		t.Fatalf("unexpected call receipt: %+v", receipt)
	}
	if strings.Contains(receipt.EndpointID, "example.com") || strings.Contains(receipt.EndpointID, "secret-reference") {
		t.Fatalf("endpoint identity is not opaque: %+v", receipt)
	}

	sameEndpoint := *request
	sameRuntime := *request.Runtime
	sameRuntime.BaseURL = "https://example.com/v1"
	sameRuntime.APIKeyRef = "runtime:different-secret-reference"
	sameEndpoint.Runtime = &sameRuntime
	sameReceipt, err := newLLMCallReceipt("fixture-provider", &sameEndpoint, &llm.Response{Model: "returned-model", ModelObserved: true}, strings.Repeat("b", sha256.Size*2), "")
	if err != nil {
		t.Fatal(err)
	}
	if sameReceipt.EndpointID != receipt.EndpointID || sameReceipt.PromptHash != receipt.PromptHash {
		t.Fatalf("equivalent endpoint or prompt was not stable: %+v / %+v", receipt, sameReceipt)
	}

	changedPrompt := sameEndpoint
	changedPrompt.Messages = []llm.Message{{Role: "user", Content: "different prompt"}}
	changedReceipt, err := newLLMCallReceipt("fixture-provider", &changedPrompt, &llm.Response{Model: "returned-model", ModelObserved: true}, strings.Repeat("c", sha256.Size*2), "")
	if err != nil {
		t.Fatal(err)
	}
	if changedReceipt.EndpointID != receipt.EndpointID || changedReceipt.PromptHash == receipt.PromptHash {
		t.Fatalf("prompt identity did not isolate content from transport: %+v / %+v", receipt, changedReceipt)
	}
}

func TestLLMCallReceiptBindsDeploymentFallbackEndpoint(t *testing.T) {
	request := &llm.Request{Model: "model", Messages: []llm.Message{{Role: "user", Content: "prompt"}}}
	first, err := newLLMCallReceipt("provider", request, &llm.Response{Model: "returned", ModelObserved: true}, strings.Repeat("a", sha256.Size*2), "https://gateway-a.example/v1/")
	if err != nil {
		t.Fatal(err)
	}
	same, err := newLLMCallReceipt("provider", request, &llm.Response{Model: "returned", ModelObserved: true}, strings.Repeat("b", sha256.Size*2), "HTTPS://GATEWAY-A.EXAMPLE/v1")
	if err != nil {
		t.Fatal(err)
	}
	different, err := newLLMCallReceipt("provider", request, &llm.Response{Model: "returned", ModelObserved: true}, strings.Repeat("c", sha256.Size*2), "https://gateway-b.example/v1")
	if err != nil {
		t.Fatal(err)
	}
	if first.EndpointID != same.EndpointID || first.EndpointID == different.EndpointID {
		t.Fatalf("deployment endpoint identities = first:%s same:%s different:%s", first.EndpointID, same.EndpointID, different.EndpointID)
	}
}

func TestProviderDecodeFailurePersistsExactResponseEvidence(t *testing.T) {
	store := &captureArtifactStore{}
	provenance := &captureProvenanceRecorder{}
	body := []byte(`{"content":`)
	providerErr := &llm.ResponseDecodeError{Cause: errors.New("unexpected EOF"), Body: body}
	activities := &Activities{
		deps: &Dependencies{
			LLM:                failingLLM{err: providerErr},
			LLMProvider:        "fixture-provider",
			ProvenanceRecorder: provenance,
		},
		artifacts: store,
	}
	request := &llm.Request{Model: "fixture-model", Messages: []llm.Message{{Role: "user", Content: "private"}}}
	_, ref, err := activities.completeLLMWithProvenance(context.Background(), "fixture", request, 0)
	if !errors.Is(err, providerErr) || ref == nil {
		t.Fatalf("ref=%v err=%v", ref, err)
	}
	var envelope recordedLLMResponse
	if err := json.Unmarshal(store.data, &envelope); err != nil {
		t.Fatal(err)
	}
	got, err := base64.StdEncoding.DecodeString(envelope.ProviderError.ResponseBodyBase64)
	if err != nil || string(got) != string(body) {
		t.Fatalf("decoded evidence=%q err=%v", got, err)
	}
	digest := sha256.Sum256(body)
	if envelope.ProviderError.ResponseBodySHA256 != hex.EncodeToString(digest[:]) || envelope.ProviderError.ResponseEvidenceTruncated {
		t.Fatalf("provider evidence metadata=%+v", envelope.ProviderError)
	}
	if len(provenance.records) != 1 || provenance.records[0].ContentHash != ref.SHA256 {
		t.Fatalf("provenance=%+v ref=%+v", provenance.records, ref)
	}
}

func TestGenericCASPutProducesWorkflowProvenance(t *testing.T) {
	store := &captureArtifactStore{}
	provenance := &captureProvenanceRecorder{}
	activities := &Activities{deps: &Dependencies{ProvenanceRecorder: provenance}, artifacts: store}
	metadata := ArtifactMetadata{
		Producer:       "GenerateTestDataActivity",
		Provider:       "algoforge",
		Model:          "not_applicable",
		ModelRevision:  "not_applicable",
		WorkflowID:     "failed-workflow-fixture",
		PayloadVersion: ActivityPayloadVersion,
	}
	ref, err := activities.putArtifactWithMetadata(context.Background(), []byte("large test input"), "text/plain", metadata)
	if err != nil {
		t.Fatal(err)
	}
	if len(provenance.records) != 1 {
		t.Fatalf("provenance records = %d, want 1", len(provenance.records))
	}
	record := provenance.records[0]
	if record.ArtifactType != "workflow_cas_object" || record.SourceType != "workflow_cas" ||
		record.ContentHash != ref.SHA256 || record.WorkflowID != metadata.WorkflowID || record.RetentionClass == "" {
		t.Fatalf("generic CAS provenance = %+v, ref=%+v", record, ref)
	}
}

func TestParseFailureKeepsNormalizedResponseProvenance(t *testing.T) {
	store := &captureArtifactStore{}
	provenance := &captureProvenanceRecorder{}
	activities := &Activities{
		deps: &Dependencies{
			LLM:                staticLLM{response: &llm.Response{Model: "fixture-model", Content: []llm.ContentBlock{{Type: "text", Text: "not json"}}}},
			LLMProvider:        "fixture-provider",
			ProvenanceRecorder: provenance,
		},
		artifacts: store,
	}
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(activities.GenerateQuizActivity)
	_, err := env.ExecuteActivity(activities.GenerateQuizActivity, QuizGenerateInput{
		Subject: "go", Type: domain.QuizTypeChoice, Difficulty: domain.QuizDifficultyEasy,
		Count: 1, Visibility: domain.QuizVisibilityPrivate,
	})
	if err == nil {
		t.Fatal("invalid provider response unexpectedly parsed")
	}
	if len(provenance.records) != 1 || provenance.records[0].WorkflowID == "" {
		t.Fatalf("parse-failure provenance = %+v", provenance.records)
	}
}

type concurrentCASStore struct {
	mu     sync.RWMutex
	bucket string
	data   map[string][]byte
}

func (s *concurrentCASStore) Put(_ context.Context, data []byte, contentType string, metadata ArtifactMetadata) (ArtifactRef, error) {
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
	s.mu.Lock()
	s.data[ref.Key] = append([]byte(nil), data...)
	s.mu.Unlock()
	return ref, nil
}

func (s *concurrentCASStore) Get(_ context.Context, ref ArtifactRef) ([]byte, error) {
	if err := ref.Validate(s.bucket); err != nil {
		return nil, err
	}
	s.mu.RLock()
	data, ok := s.data[ref.Key]
	data = append([]byte(nil), data...)
	s.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("artifact not found")
	}
	if int64(len(data)) != ref.SizeBytes {
		return nil, fmt.Errorf("artifact size mismatch")
	}
	digest := sha256.Sum256(data)
	if hex.EncodeToString(digest[:]) != ref.SHA256 {
		return nil, fmt.Errorf("artifact sha256 mismatch")
	}
	return data, nil
}

func TestTwoWorkersShareCASAcrossThousandRandomAssignments(t *testing.T) {
	store := &concurrentCASStore{bucket: "algoforge", data: make(map[string][]byte)}
	workers := []*Activities{{artifacts: store}, {artifacts: store}}
	metadata := ArtifactMetadata{
		Producer:       "fixture",
		Provider:       "test",
		Model:          "not_applicable",
		ModelRevision:  "v1",
		WorkflowID:     "cas-concurrency",
		PayloadVersion: ActivityPayloadVersion,
	}

	const assignments = 1000
	errorsCh := make(chan error, assignments)
	refs := make([]*ArtifactRef, assignments)
	var wg sync.WaitGroup
	for i := 0; i < assignments; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			producer := workers[i%len(workers)]
			consumer := workers[(i+1)%len(workers)]
			data := []byte(fmt.Sprintf("artifact-%04d-%s", i, strings.Repeat("x", i%97)))
			ref, err := producer.putArtifactWithMetadata(context.Background(), data, "application/octet-stream", metadata)
			if err != nil {
				errorsCh <- err
				return
			}
			refs[i] = ref
			got, err := consumer.getArtifact(context.Background(), ref)
			if err != nil {
				errorsCh <- err
				return
			}
			if string(got) != string(data) {
				errorsCh <- fmt.Errorf("assignment %d data mismatch", i)
			}
		}(i)
	}
	wg.Wait()
	close(errorsCh)
	for err := range errorsCh {
		t.Error(err)
	}

	tampered := refs[assignments/2]
	if tampered == nil {
		t.Fatal("missing reference for tamper fixture")
	}
	store.mu.Lock()
	store.data[tampered.Key][0] ^= 0xff
	store.mu.Unlock()
	if _, err := workers[0].getArtifact(context.Background(), tampered); err == nil || !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Fatalf("tampered artifact error = %v, want sha256 mismatch", err)
	}
}
