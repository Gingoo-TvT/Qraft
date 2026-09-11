package activities

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
	"github.com/google/uuid"
	"go.temporal.io/sdk/testsuite"
)

type dedupVectorStore struct {
	problems []*domain.Problem
	scores   []float64
	err      error
}

func (s *dedupVectorStore) FindSimilarForVersion(
	_ context.Context,
	_ []float32,
	_ uuid.UUID,
	_ string,
	_ int,
	_ float64,
) ([]*domain.Problem, []float64, error) {
	if s.err != nil {
		return nil, nil, s.err
	}
	return s.problems, s.scores, nil
}

func (s *dedupVectorStore) UpdateEmbeddingForVersion(
	_ context.Context,
	_ repository.EmbeddingWrite,
) error {
	return nil
}

func TestDedupSuccessMakesRealZeroNeighborsExplicit(t *testing.T) {
	modelVersionID := uuid.New()
	providerEffects := &fakeProviderEffectStore{claim: repository.ProviderEffectClaim{
		State: repository.ProviderEffectAcquired, LeaseToken: uuid.New(),
	}}
	acts := New(&Dependencies{
		Embedding:               &recordingEmbedder{result: []float32{1, 0}},
		EmbeddingProvider:       "fixture-provider",
		EmbeddingModel:          "fixture-model",
		EmbeddingModelVersionID: modelVersionID,
		ProblemRepo:             repository.NewProblemRepository(nil),
		VectorRepo:              &dedupVectorStore{},
		ProviderEffects:         providerEffects,
		ProviderEffectLease:     time.Minute,
	})
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(acts.SimilarityCheckActivity)
	env.RegisterActivity(acts.PostStatementSimilarityActivity)

	preValue, err := env.ExecuteActivity(acts.SimilarityCheckActivity, domain.ProblemGenParams{SimilarLimit: 1})
	if err != nil {
		t.Fatalf("pre-generation similarity: %v", err)
	}
	var pre SimilarityCheckResult
	if err := preValue.Get(&pre); err != nil {
		t.Fatalf("decode pre-generation result: %v", err)
	}
	postValue, err := env.ExecuteActivity(acts.PostStatementSimilarityActivity, StatementResult{
		Statement: "fixture statement", OneLineHint: "fixture hint",
	})
	if err != nil {
		t.Fatalf("post-statement similarity: %v", err)
	}
	var post PostStatementSimilarityResult
	if err := postValue.Get(&post); err != nil {
		t.Fatalf("decode post-statement result: %v", err)
	}

	for name, report := range map[string]*DedupReport{
		"pre-generation": pre.Report,
		"post-statement": post.Report,
	} {
		if report == nil || report.Decision != DedupDecisionPass {
			t.Fatalf("%s report = %+v, want pass", name, report)
		}
		if report.NeighborCount == nil || *report.NeighborCount != 0 {
			t.Fatalf("%s neighbor count = %v, want explicit zero", name, report.NeighborCount)
		}
		encoded, marshalErr := json.Marshal(report)
		if marshalErr != nil {
			t.Fatalf("marshal %s report: %v", name, marshalErr)
		}
		if !strings.Contains(string(encoded), `"neighbor_count":0`) {
			t.Fatalf("%s report did not encode a checked zero: %s", name, encoded)
		}
	}
}

func TestPreGenerationDedupDependencyFailuresAreStructuredAndSecretFree(t *testing.T) {
	secret := "sk-must-not-appear"
	modelVersionID := uuid.New()
	tests := []struct {
		name       string
		deps       *Dependencies
		wantReason string
	}{
		{
			name: "active model unavailable",
			deps: &Dependencies{
				Embedding:         &recordingEmbedder{result: []float32{1}},
				EmbeddingProvider: "fixture-provider",
				EmbeddingModel:    "fixture-model",
				VectorRepo:        &dedupVectorStore{},
			},
			wantReason: DedupFailureActiveModelUnavailable,
		},
		{
			name: "embedding unavailable or invalid",
			deps: &Dependencies{
				Embedding:               &recordingEmbedder{err: errors.New("provider rejected " + secret)},
				EmbeddingProvider:       "fixture-provider",
				EmbeddingModel:          "fixture-model",
				EmbeddingModelVersionID: modelVersionID,
				VectorRepo:              &dedupVectorStore{},
			},
			wantReason: DedupFailureEmbeddingUnavailable,
		},
		{
			name: "vector query unavailable",
			deps: &Dependencies{
				Embedding:               &recordingEmbedder{result: []float32{1}},
				EmbeddingProvider:       "fixture-provider",
				EmbeddingModel:          "fixture-model",
				EmbeddingModelVersionID: modelVersionID,
				VectorRepo:              &dedupVectorStore{err: errors.New("pgvector failed " + secret)},
			},
			wantReason: DedupFailureVectorQueryUnavailable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.deps.ProviderEffects = &fakeProviderEffectStore{claim: repository.ProviderEffectClaim{
				State: repository.ProviderEffectAcquired, LeaseToken: uuid.New(),
			}}
			tt.deps.ProviderEffectLease = time.Minute
			acts := New(tt.deps)
			var suite testsuite.WorkflowTestSuite
			env := suite.NewTestActivityEnvironment()
			env.RegisterActivity(acts.SimilarityCheckActivity)
			_, err := env.ExecuteActivity(acts.SimilarityCheckActivity, domain.ProblemGenParams{SimilarLimit: 1})
			if err == nil {
				t.Fatal("dependency failure returned success")
			}
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("error leaked provider material: %v", err)
			}
			report, ok := DedupCheckFailedReportFromError(err)
			if !ok {
				t.Fatalf("error has no structured check_failed report: %T %v", err, err)
			}
			if report.Decision != DedupDecisionCheckFailed || report.Reason != tt.wantReason {
				t.Fatalf("report = %+v, want check_failed/%s", report, tt.wantReason)
			}
			if report.NeighborCount != nil {
				t.Fatalf("failed check exposed neighbor count %v", *report.NeighborCount)
			}
			encoded, marshalErr := json.Marshal(report)
			if marshalErr != nil {
				t.Fatalf("marshal report: %v", marshalErr)
			}
			if strings.Contains(string(encoded), "neighbor_count") || strings.Contains(string(encoded), secret) {
				t.Fatalf("failed report leaked count or secret: %s", encoded)
			}
		})
	}
}
