package activities

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"sync"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/llm"
	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
	"github.com/google/uuid"
	"go.temporal.io/sdk/testsuite"
)

type overlappingSolutionLLM struct {
	mu          sync.Mutex
	started     int
	active      int
	maxActive   int
	bothStarted chan struct{}
	once        sync.Once
}

func newOverlappingSolutionLLM() *overlappingSolutionLLM {
	return &overlappingSolutionLLM{bothStarted: make(chan struct{})}
}

func (l *overlappingSolutionLLM) CompleteWithRetry(ctx context.Context, request *llm.Request, _ int) (*llm.Response, error) {
	l.mu.Lock()
	l.started++
	l.active++
	if l.active > l.maxActive {
		l.maxActive = l.active
	}
	if l.started == 2 {
		l.once.Do(func() { close(l.bothStarted) })
	}
	l.mu.Unlock()
	defer func() {
		l.mu.Lock()
		l.active--
		l.mu.Unlock()
	}()

	select {
	case <-l.bothStarted:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	isBrute := len(request.Messages) > 0 && strings.Contains(request.Messages[0].Content, "brute-force")
	code := "int main(){return 0;}"
	if isBrute {
		code = "int main(){return 1;}"
	}
	return &llm.Response{
		Model:         "fixture-model",
		ModelObserved: true,
		StopReason:    "end_turn",
		Content: []llm.ContentBlock{{
			Type: "text",
			Text: `{"source_code":"` + code + `","language":"cpp","complexity_time":"O(1)","complexity_space":"O(1)","explanation":"fixture"}`,
		}},
	}, nil
}

type immutableSolutionArtifactStore struct{}

func (immutableSolutionArtifactStore) Put(_ context.Context, data []byte, contentType string, metadata ArtifactMetadata) (ArtifactRef, error) {
	digest := sha256.Sum256(data)
	digestHex := hex.EncodeToString(digest[:])
	return ArtifactRef{
		SchemaVersion:  ArtifactRefSchemaVersion,
		PayloadVersion: ActivityPayloadVersion,
		Bucket:         "fixture",
		Key:            artifactKey(digestHex),
		SHA256:         digestHex,
		SizeBytes:      int64(len(data)),
		ContentType:    contentType,
		Producer:       metadata.Producer,
		Provider:       metadata.Provider,
		Model:          metadata.Model,
		ModelRevision:  metadata.ModelRevision,
		WorkflowID:     metadata.WorkflowID,
	}, nil
}

func (immutableSolutionArtifactStore) Get(context.Context, ArtifactRef) ([]byte, error) {
	return nil, nil
}

type noopSolutionProvenanceRecorder struct{}

func (noopSolutionProvenanceRecorder) RecordWorkflowArtifact(context.Context, repository.WorkflowArtifactProvenance) (uuid.UUID, error) {
	return uuid.New(), nil
}

func TestGenerateSolutionPairOverlapsMainAndBruteProviderCalls(t *testing.T) {
	provider := newOverlappingSolutionLLM()
	acts := New(&Dependencies{
		LLM:                provider,
		LLMProvider:        "fixture-provider",
		LLMModel:           "fixture-model",
		ArtifactStore:      immutableSolutionArtifactStore{},
		ProvenanceRecorder: noopSolutionProvenanceRecorder{},
	})

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(acts.GenerateSolutionActivity)
	encoded, err := env.ExecuteActivity(
		acts.GenerateSolutionActivity,
		"Read one integer and print it.",
		domain.DefaultProblemGenParams(),
	)
	if err != nil {
		t.Fatalf("GenerateSolutionActivity returned error: %v", err)
	}
	var result SolutionResult
	if err := encoded.Get(&result); err != nil {
		t.Fatalf("decode solution result: %v", err)
	}
	if result.MainSolution.SourceCode == "" || result.BruteSolution.SourceCode == "" {
		t.Fatalf("incomplete solution result: %+v", result)
	}
	if len(result.SourceArtifacts) != 2 || result.SourceArtifacts[0] == nil || result.SourceArtifacts[1] == nil {
		t.Fatalf("solution artifact order/result = %+v", result.SourceArtifacts)
	}
	provider.mu.Lock()
	started, maxActive := provider.started, provider.maxActive
	provider.mu.Unlock()
	if started != 2 {
		t.Fatalf("provider calls = %d, want 2", started)
	}
	if maxActive < 2 {
		t.Fatalf("provider calls did not overlap; max active = %d", maxActive)
	}
}
