//go:build integration

package activities

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"go.temporal.io/sdk/testsuite"
)

var errInjectedStoreComplete = errors.New("injected first store ledger completion failure")

type failFirstCompleteLedger struct {
	repository *repository.WorkflowOperationRepository

	mu       sync.Mutex
	failNext bool
}

func (l *failFirstCompleteLedger) Begin(ctx context.Context, key, operationType, payloadSHA256 string) (repository.WorkflowOperation, error) {
	return l.repository.Begin(ctx, key, operationType, payloadSHA256)
}

func (l *failFirstCompleteLedger) Complete(ctx context.Context, key, payloadSHA256 string, result json.RawMessage) error {
	l.mu.Lock()
	if l.failNext {
		l.failNext = false
		l.mu.Unlock()
		return errInjectedStoreComplete
	}
	l.mu.Unlock()
	return l.repository.Complete(ctx, key, payloadSHA256, result)
}

func (l *failFirstCompleteLedger) Fail(ctx context.Context, key, payloadSHA256, failure string) error {
	return l.repository.Fail(ctx, key, payloadSHA256, failure)
}

func (l *failFirstCompleteLedger) StepCompleted(ctx context.Context, operationKey, stepKey, effectSHA256 string) (bool, error) {
	return l.repository.StepCompleted(ctx, operationKey, stepKey, effectSHA256)
}

func (l *failFirstCompleteLedger) CompleteStep(ctx context.Context, operationKey, stepKey, effectSHA256 string) error {
	return l.repository.CompleteStep(ctx, operationKey, stepKey, effectSHA256)
}

type fixedStoreEmbedder struct {
	mu    sync.Mutex
	calls int
}

func (e *fixedStoreEmbedder) Embed(context.Context, string) ([]float32, error) {
	e.mu.Lock()
	e.calls++
	e.mu.Unlock()

	embedding := make([]float32, 1536)
	for i := range embedding {
		embedding[i] = float32(i%17+1) / 17
	}
	return embedding, nil
}

func (e *fixedStoreEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	result := make([][]float32, len(texts))
	for i, text := range texts {
		embedding, err := e.Embed(ctx, text)
		if err != nil {
			return nil, err
		}
		result[i] = embedding
	}
	return result, nil
}

func (e *fixedStoreEmbedder) callCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls
}

type storeS3Recorder struct {
	mu   sync.Mutex
	puts map[string]int
}

func newStoreS3Server(t *testing.T, bucket string) (*httptest.Server, *storeS3Recorder) {
	t.Helper()
	recorder := &storeS3Recorder{puts: make(map[string]int)}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			http.Error(w, "only PUT is supported", http.StatusMethodNotAllowed)
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/"+bucket+"/") {
			http.Error(w, "unexpected bucket path", http.StatusNotFound)
			return
		}
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		recorder.mu.Lock()
		recorder.puts[r.URL.Path]++
		recorder.mu.Unlock()
		w.Header().Set("ETag", `"integration-fixture"`)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)
	return server, recorder
}

func (r *storeS3Recorder) putCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	total := 0
	for _, count := range r.puts {
		total += count
	}
	return total
}

type storeSideEffectCounts struct {
	Problems             int64
	ProblemsByKey        int64
	Solutions            int64
	TestCases            int64
	ProblemEmbeddings    int64
	OutboxEvents         int64
	ProblemArtifacts     int64
	ProblemBindings      int64
	AuditEvents          int64
	ProviderEffects      int64
	OperationSteps       int64
	ProjectionDimensions int
	VectorDimensions     int
}

func TestStoreProblemIdempotencyAfterLedgerCompleteFailureIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := storeIntegrationPool(t)
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("ping integration database: %v", err)
	}

	operationKey := "integration/store-problem/" + uuid.NewString()
	problemID := uuid.NewSHA1(uuid.NameSpaceURL, []byte("algoforge:store-problem:"+operationKey))
	effectKey, err := namedProviderEffectKey("store-problem-embedding", operationKey)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupStoreIntegrationRows(t, pool, operationKey, effectKey)
	})

	const bucket = "store-integration"
	s3Server, s3Recorder := newStoreS3Server(t, bucket)
	minioClient, err := minio.New(strings.TrimPrefix(s3Server.URL, "http://"), &minio.Options{
		Creds:        credentials.NewStaticV4("integration-access", "integration-secret", ""),
		Secure:       false,
		Region:       "us-east-1",
		BucketLookup: minio.BucketLookupPath,
	})
	if err != nil {
		t.Fatalf("create MinIO client: %v", err)
	}

	operationRepository := repository.NewWorkflowOperationRepository(pool)
	ledger := &failFirstCompleteLedger{repository: operationRepository, failNext: true}
	embedder := &fixedStoreEmbedder{}
	vectorRepo := repository.NewVectorRepository(pool)
	embeddingModelVersionID, err := vectorRepo.ActiveModelVersion(ctx, repository.EmbeddingKindStatement)
	if err != nil {
		t.Fatalf("resolve integration statement model version: %v", err)
	}
	activities := New(&Dependencies{
		Embedding:               embedder,
		EmbeddingEnabled:        true,
		EmbeddingProvider:       "integration-provider",
		EmbeddingModel:          "integration-1536",
		EmbeddingModelVersionID: embeddingModelVersionID,
		MinIO:                   minioClient,
		MinioBucket:             bucket,
		ProblemRepo:             repository.NewProblemRepository(pool),
		TestCaseRepo:            repository.NewTestCaseRepository(pool),
		VectorRepo:              vectorRepo,
		OperationLedger:         ledger,
		ProviderEffects:         repository.NewProviderEffectRepository(pool),
		ProviderEffectLease:     time.Minute,
	})
	input := storeIntegrationInput(operationKey)

	var suite testsuite.WorkflowTestSuite
	environment := suite.NewTestActivityEnvironment()
	environment.RegisterActivity(activities.StoreProblemActivity)

	if _, err := environment.ExecuteActivity(activities.StoreProblemActivity, input); err == nil || !strings.Contains(err.Error(), errInjectedStoreComplete.Error()) {
		t.Fatalf("first delivery error = %v, want injected ledger completion failure", err)
	}
	assertStoreOperationState(t, pool, operationKey, repository.OperationStatusFailed, 1)
	afterFailedCompletion := readStoreSideEffects(t, pool, problemID, operationKey, effectKey)
	assertCompleteStoreSideEffects(t, afterFailedCompletion, len(input.TestCases))
	putsAfterFailedCompletion := s3Recorder.putCount()
	if want := 2*len(input.TestCases) + 3; putsAfterFailedCompletion != want {
		t.Fatalf("S3 PUT count after first delivery = %d, want %d", putsAfterFailedCompletion, want)
	}

	second := executeStoreActivity(t, environment, activities, input)
	if second.ProblemID != problemID {
		t.Fatalf("second delivery problem ID = %s, want %s", second.ProblemID, problemID)
	}
	assertStoreOperationState(t, pool, operationKey, repository.OperationStatusCompleted, 2)
	afterRecovery := readStoreSideEffects(t, pool, problemID, operationKey, effectKey)
	if afterRecovery != afterFailedCompletion {
		t.Fatalf("side effects changed on recovery: first=%+v second=%+v", afterFailedCompletion, afterRecovery)
	}
	if got := s3Recorder.putCount(); got != putsAfterFailedCompletion {
		t.Fatalf("S3 PUT count changed on recovery: first=%d second=%d", putsAfterFailedCompletion, got)
	}

	third := executeStoreActivity(t, environment, activities, input)
	if *third != *second {
		t.Fatalf("completed-cache result changed: second=%+v third=%+v", second, third)
	}
	assertStoreOperationState(t, pool, operationKey, repository.OperationStatusCompleted, 2)
	afterCachedRedelivery := readStoreSideEffects(t, pool, problemID, operationKey, effectKey)
	if afterCachedRedelivery != afterRecovery {
		t.Fatalf("side effects changed on completed-cache redelivery: second=%+v third=%+v", afterRecovery, afterCachedRedelivery)
	}
	if got := s3Recorder.putCount(); got != putsAfterFailedCompletion {
		t.Fatalf("S3 PUT count changed on completed-cache redelivery: first=%d third=%d", putsAfterFailedCompletion, got)
	}
	if got := embedder.callCount(); got != 1 {
		t.Fatalf("embedding provider calls = %d, want 1", got)
	}
}

func storeIntegrationPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if os.Getenv("ALGOFORGE_INTEGRATION_DISPOSABLE_DB") != "true" {
		t.Fatal("ALGOFORGE_INTEGRATION_DISPOSABLE_DB=true is required because provenance audit rows are append-only")
	}
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		t.Fatal("DATABASE_URL is required for store problem integration tests")
	}
	pool, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func storeIntegrationInput(operationKey string) StoreInput {
	return StoreInput{
		PayloadVersion: ActivityPayloadVersion,
		IdempotencyKey: operationKey,
		WorkflowID:     "integration-workflow-" + uuid.NewString(),
		Statement: StatementResult{
			Title:       "Store idempotency integration fixture",
			Statement:   "Given an integer n, print n.",
			Tags:        []string{"integration"},
			OneLineHint: "Read and print the value.",
		},
		Solutions: SolutionResult{
			MainSolution: domain.Solution{
				SolutionType: domain.SolutionTypeMain,
				Language:     "cpp",
				SourceCode:   "#include <iostream>\nint main(){int n;std::cin>>n;std::cout<<n;}",
			},
			BruteSolution: domain.Solution{
				SolutionType: domain.SolutionTypeBrute,
				Language:     "cpp",
				SourceCode:   "#include <cstdio>\nint main(){int n;scanf(\"%d\",&n);printf(\"%d\",n);}",
			},
		},
		TestCases: []TestCaseData{
			{Input: "1\n", GroupID: 1, IsSample: true, Description: "sample"},
			{Input: "42\n", GroupID: 1, Description: "ordinary"},
		},
		SandboxOutput: SandboxResult{
			PayloadVersion: ActivityPayloadVersion,
			Outputs:        []string{"1\n", "42\n"},
		},
		Params: domain.ProblemGenParams{
			Level:       domain.LevelAlgorithm,
			Difficulty:  1500,
			Tags:        []string{"integration"},
			TimeLimit:   1000,
			MemoryLimit: 64,
			TestDataConfig: domain.TestDataConfig{
				NumTestCases: 2,
				NumSamples:   1,
				Groups: []domain.TestGroup{{
					GroupID:  1,
					NumCases: 2,
					Score:    100,
				}},
			},
			Languages: []string{"cpp"},
		},
		Editorial: "The answer is the input value.",
	}
}

func executeStoreActivity(t *testing.T, environment *testsuite.TestActivityEnvironment, activities *Activities, input StoreInput) *StoreResult {
	t.Helper()
	encoded, err := environment.ExecuteActivity(activities.StoreProblemActivity, input)
	if err != nil {
		t.Fatalf("store problem activity: %v", err)
	}
	var result StoreResult
	if err := encoded.Get(&result); err != nil {
		t.Fatalf("decode store result: %v", err)
	}
	return &result
}

func readStoreSideEffects(
	t *testing.T,
	pool *pgxpool.Pool,
	problemID uuid.UUID,
	operationKey string,
	effectKey string,
) storeSideEffectCounts {
	t.Helper()
	var counts storeSideEffectCounts
	err := pool.QueryRow(context.Background(), `
			SELECT
				(SELECT COUNT(*) FROM problems WHERE id=$1),
				(SELECT COUNT(*) FROM problems WHERE metadata_json->>'store_idempotency_key'=$2),
				(SELECT COUNT(*) FROM solutions WHERE problem_id=$1),
				(SELECT COUNT(*) FROM testcases WHERE problem_id=$1),
				(SELECT COUNT(*) FROM problem_embeddings WHERE problem_id=$1),
				(SELECT COUNT(*) FROM workflow_outbox WHERE operation_key=$2),
				(SELECT COUNT(DISTINCT artifact.artifact_id)
				 FROM provenance_artifacts artifact
				 JOIN provenance_artifact_bindings binding ON binding.artifact_id=artifact.artifact_id
				 WHERE binding.subject_type='problem' AND binding.subject_id=$1::text),
				(SELECT COUNT(*) FROM provenance_artifact_bindings
				 WHERE subject_type='problem' AND subject_id=$1::text),
				(SELECT COUNT(*) FROM provenance_audit_events WHERE details->>'operation_key'=$2),
				(SELECT COUNT(*) FROM provider_effects WHERE effect_key=$3),
				(SELECT COUNT(*) FROM workflow_operation_steps WHERE operation_key=$2),
				COALESCE((SELECT vector_dims(embedding) FROM problems WHERE id=$1), 0),
				COALESCE((SELECT vector_dims(embedding) FROM problem_embeddings WHERE problem_id=$1), 0)`,
		problemID, operationKey, effectKey,
	).Scan(
		&counts.Problems,
		&counts.ProblemsByKey,
		&counts.Solutions,
		&counts.TestCases,
		&counts.ProblemEmbeddings,
		&counts.OutboxEvents,
		&counts.ProblemArtifacts,
		&counts.ProblemBindings,
		&counts.AuditEvents,
		&counts.ProviderEffects,
		&counts.OperationSteps,
		&counts.ProjectionDimensions,
		&counts.VectorDimensions,
	)
	if err != nil {
		t.Fatalf("read store side effects: %v", err)
	}
	return counts
}

func assertCompleteStoreSideEffects(t *testing.T, counts storeSideEffectCounts, testCaseCount int) {
	t.Helper()
	if counts.Problems != 1 || counts.ProblemsByKey != 1 || counts.Solutions != 2 ||
		counts.TestCases != int64(testCaseCount) || counts.ProblemEmbeddings != 1 || counts.OutboxEvents != 1 ||
		counts.AuditEvents != 1 || counts.ProviderEffects != 1 || counts.OperationSteps == 0 ||
		counts.ProblemArtifacts == 0 || counts.ProblemBindings == 0 {
		t.Fatalf("incomplete first-delivery side effects: %+v", counts)
	}
	if counts.ProjectionDimensions != 0 || counts.VectorDimensions != 1536 {
		t.Fatalf("stored embedding dimensions = projection:%d vector:%d, want 0/1536", counts.ProjectionDimensions, counts.VectorDimensions)
	}
}

func assertStoreOperationState(t *testing.T, pool *pgxpool.Pool, operationKey, wantStatus string, wantAttempts int) {
	t.Helper()
	var status string
	var attempts int
	if err := pool.QueryRow(context.Background(), `
		SELECT status, attempt_count FROM workflow_operations WHERE operation_key=$1`, operationKey,
	).Scan(&status, &attempts); err != nil {
		t.Fatalf("read workflow operation state: %v", err)
	}
	if status != wantStatus || attempts != wantAttempts {
		t.Fatalf("workflow operation state = %s/%d, want %s/%d", status, attempts, wantStatus, wantAttempts)
	}
}

func cleanupStoreIntegrationRows(t *testing.T, pool *pgxpool.Pool, operationKey, effectKey string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	statements := []struct {
		query string
		args  []interface{}
	}{
		{`DELETE FROM workflow_outbox WHERE operation_key=$1`, []interface{}{operationKey}},
		{`DELETE FROM problems WHERE metadata_json->>'store_idempotency_key'=$1`, []interface{}{operationKey}},
		{`DELETE FROM provider_effects WHERE effect_key=$1`, []interface{}{effectKey}},
		{`DELETE FROM workflow_operations WHERE operation_key=$1`, []interface{}{operationKey}},
	}
	for _, statement := range statements {
		if _, err := pool.Exec(ctx, statement.query, statement.args...); err != nil {
			t.Errorf("clean store integration rows with %q: %v", statement.query, err)
		}
	}
}

var _ OperationLedger = (*failFirstCompleteLedger)(nil)
