package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/Gingoo-TvT/Qraft/backend/internal/handler"
	"github.com/Gingoo-TvT/Qraft/backend/internal/service"
	minioclient "github.com/Gingoo-TvT/Qraft/backend/pkg/minio"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/config"
	"github.com/Gingoo-TvT/Qraft/backend/internal/embeddingruntime"
	provenancegov "github.com/Gingoo-TvT/Qraft/backend/internal/governance/provenance"
	"github.com/Gingoo-TvT/Qraft/backend/internal/llm"
	"github.com/Gingoo-TvT/Qraft/backend/internal/outbox"
	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
	"github.com/Gingoo-TvT/Qraft/backend/internal/runtimekeys"
	"github.com/Gingoo-TvT/Qraft/backend/internal/secureconfig"
	"github.com/Gingoo-TvT/Qraft/backend/internal/workflow"
	"github.com/Gingoo-TvT/Qraft/backend/internal/workflow/activities"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/worker"
)

func main() {
	// Configure structured logging.
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	// Load application configuration.
	cfg, err := config.Load()
	if err != nil {
		log.Fatal().Err(err).Msg("failed to load configuration")
	}
	if err := validateWorkerConfiguration(cfg); err != nil {
		log.Fatal().Err(err).Msg("worker configuration preflight failed")
	}

	setLogLevel(cfg.App.LogLevel)

	log.Info().
		Str("temporal_host", cfg.Temporal.Host).
		Str("task_queue", cfg.Temporal.TaskQueue).
		Msg("starting algoforge temporal worker")

	// Create PostgreSQL connection pool.
	dbPool, err := pgxpool.New(context.Background(), cfg.Database.DSN())
	if err != nil {
		log.Fatal().Err(err).Msg("failed to connect to database")
	}
	defer dbPool.Close()

	if err := dbPool.Ping(context.Background()); err != nil {
		log.Fatal().Err(err).Msg("failed to ping database")
	}
	if err := repository.VerifyWorkflowDurabilitySchema(context.Background(), dbPool); err != nil {
		log.Fatal().Err(err).Msg("workflow durability schema preflight failed")
	}
	log.Info().Msg("database connection established")

	// Redis backs request-scoped runtime key references issued by the API.
	rdb := redis.NewClient(&redis.Options{
		Addr:     cfg.Redis.Addr,
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
	})
	defer rdb.Close()
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		log.Fatal().Err(err).Msg("failed to ping redis")
	}
	runtimeKeyStore := runtimekeys.NewStore(rdb, 6*time.Hour)
	log.Info().Str("addr", cfg.Redis.Addr).Msg("redis runtime key resolver configured")

	// Create MinIO client for object storage.
	minioClient, err := minio.New(cfg.MinIO.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.MinIO.AccessKey, cfg.MinIO.SecretKey, ""),
		Secure: cfg.MinIO.UseSSL,
	})
	if err != nil {
		log.Fatal().Err(err).Msg("failed to create minio client")
	}
	log.Info().Str("endpoint", cfg.MinIO.Endpoint).Msg("minio client created")

	// Create LLM client for Anthropic API.
	llmClient := llm.NewClient(cfg.Anthropic)
	llmClient.SetAPIKeyResolver(runtimeKeyStore)
	log.Info().Str("provider", cfg.Anthropic.ProviderID()).Str("model", cfg.Anthropic.Model).Msg("llm client created")

	settingsSecret, settingsSecretSource, err := secureconfig.ResolveSettingsSecret(
		cfg.App.SettingsEncryptionKey, cfg.App.JWTSecret, cfg.App.DevMode,
	)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to resolve permanent settings encryption key")
	}
	settingsCipher, err := secureconfig.NewCipher(settingsSecret)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to configure permanent embedding settings encryption")
	}
	if settingsSecretSource != "explicit" {
		log.Warn().Str("source", settingsSecretSource).Msg("ALGOFORGE_SETTINGS_ENCRYPTION_KEY is unset; using a compatibility fallback")
	}
	embeddingSettingsRepo := repository.NewEmbeddingProviderSettingsRepository(dbPool)
	embeddingKeyResolver := embeddingruntime.NewAPIKeyResolver(embeddingSettingsRepo, settingsCipher, cfg.Embedding)

	// Problem storage requires an embedding, so disabled embedding is rejected
	// by the worker preflight before any infrastructure clients are created.
	embedder, err := llm.NewEmbeddingClientWithResolver(cfg.Embedding, embeddingKeyResolver)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to create embedding client")
	}
	log.Info().
		Str("model", cfg.Embedding.Model).
		Int("dimensions", cfg.Embedding.Dimensions).
		Msg("embedding client created")

	// Create Temporal client.
	temporalClient, err := client.Dial(client.Options{
		HostPort:  cfg.Temporal.Host,
		Namespace: cfg.Temporal.Namespace,
	})
	if err != nil {
		log.Fatal().Err(err).Msg("failed to create temporal client")
	}
	defer temporalClient.Close()
	log.Info().Msg("temporal client connected")

	// Initialize repositories.
	problemRepo := repository.NewProblemRepository(dbPool)
	reviewSettingsRepo := repository.NewReviewSettingsRepository(dbPool)
	testCaseRepo := repository.NewTestCaseRepository(dbPool)
	vectorRepo := repository.NewVectorRepository(dbPool)
	quizRepo := repository.NewQuizRepository(dbPool)
	kpRepo := repository.NewKnowledgePointRepository(dbPool)
	operationLedger := repository.NewWorkflowOperationRepository(dbPool)
	providerEffects := repository.NewProviderEffectRepository(dbPool)
	outboxRepo := repository.NewOutboxRepository(dbPool)
	provenanceRecorder := repository.NewProvenanceArtifactRepository(dbPool)
	configuredStatementModel, err := vectorRepo.ResolveConfiguredModelVersion(
		context.Background(),
		cfg.Embedding.ExpectedStatementModelVersionID,
		cfg.Embedding.ProviderID(),
		cfg.Embedding.Model,
		cfg.Embedding.Dimensions,
	)
	if err != nil {
		log.Fatal().Err(err).Msg("configured statement embedding preflight failed")
	}
	runtimeVectorRepo, err := vectorRepo.BindStatementModelVersion(configuredStatementModel.ID)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to bind configured statement embedding model")
	}
	if activeID, activeErr := vectorRepo.ActiveModelVersion(context.Background(), repository.EmbeddingKindStatement); activeErr != nil {
		log.Warn().Err(activeErr).Msg("statement embedding active pointer could not be read")
	} else if activeID != configuredStatementModel.ID {
		log.Warn().Str("configured_model_version_id", configuredStatementModel.ID.String()).Str("active_model_version_id", activeID.String()).Msg("statement embedding config and active pointer differ; runtime remains pinned to configuration")
	}

	var outboxPublisher outbox.Publisher
	if cfg.Outbox.Enabled {
		outboxPublisher, err = outbox.NewHTTPPublisher(outbox.HTTPPublisherOptions{
			Endpoint:         cfg.Outbox.Endpoint,
			BearerToken:      cfg.Outbox.BearerToken,
			HMACSecret:       cfg.Outbox.HMACSecret,
			AllowHTTP:        cfg.App.DevMode,
			ConnectTimeout:   cfg.Outbox.ConnectTimeout,
			RequestTimeout:   cfg.Outbox.RequestTimeout,
			MaxRequestBytes:  cfg.Outbox.MaxRequestBytes,
			MaxResponseBytes: cfg.Outbox.MaxResponseBytes,
		})
		if err != nil {
			log.Fatal().Err(err).Msg("failed to configure outbox publisher")
		}
	}
	outboxRuntime, err := outbox.NewRuntime(outboxRepo, outboxPublisher, outbox.RuntimeOptions{
		Enabled:      cfg.Outbox.Enabled,
		PollInterval: cfg.Outbox.PollInterval,
		Dispatch: outbox.DispatchOptions{
			BatchSize:  cfg.Outbox.BatchSize,
			Lease:      cfg.Outbox.LeaseDuration,
			MaxAttempt: cfg.Outbox.MaxAttempts,
		},
		MaxQueued:     cfg.Outbox.ReadinessMaxQueued,
		MaxAge:        cfg.Outbox.ReadinessMaxAge,
		ObserveStatus: newOutboxStatusObserver(),
	})
	if err != nil {
		log.Fatal().Err(err).Msg("failed to configure outbox runtime")
	}
	retentionScheduler, err := provenancegov.NewRetentionScheduler(provenanceRecorder, provenancegov.RetentionSchedulerOptions{
		Enabled:    cfg.Provenance.RetentionEnabled,
		Interval:   cfg.Provenance.RetentionInterval,
		BatchSize:  cfg.Provenance.RetentionBatchSize,
		MaxBatches: cfg.Provenance.RetentionMaxBatches,
		Observe:    newRetentionStatusObserver(),
	})
	if err != nil {
		log.Fatal().Err(err).Msg("failed to configure provenance retention scheduler")
	}

	// Initialize activity dependencies.
	actDeps := &activities.Dependencies{
		LLM:                    llmClient,
		Embedding:              embedder,
		MinIO:                  minioClient,
		MinioBucket:            cfg.MinIO.Bucket,
		ProblemRepo:            problemRepo,
		ProblemEditRefreshRepo: problemRepo,
		ReviewSettings:         reviewSettingsRepo,
		TestCaseRepo:           testCaseRepo,
		VectorRepo:             runtimeVectorRepo,
		QuizRepo:               quizRepo,
		KPRepo:                 kpRepo,
		OperationLedger:        operationLedger,
		OutboxQueue:            outboxRepo,
		OutboxPublisher:        outboxPublisher,
		OutboxDispatch: outbox.DispatchOptions{
			BatchSize:  cfg.Outbox.BatchSize,
			Lease:      cfg.Outbox.LeaseDuration,
			MaxAttempt: cfg.Outbox.MaxAttempts,
		},
		ProvenanceRecorder: provenanceRecorder,
		SandboxCfg:         cfg.Sandbox,
	}
	if err := configureProviderEffectDependencies(actDeps, providerEffects, cfg, configuredStatementModel.ID); err != nil {
		log.Fatal().Err(err).Msg("provider effect dependency preflight failed")
	}
	if err := configureS3QualityDependencies(actDeps, cfg.App.DevMode); err != nil {
		log.Fatal().Err(err).Msg("S3 quality dependency preflight failed")
	}

	// Create and configure the Temporal worker.
	w := worker.New(temporalClient, cfg.Temporal.TaskQueue, worker.Options{
		MaxConcurrentActivityExecutionSize:     cfg.Temporal.WorkerConcurrency,
		MaxConcurrentWorkflowTaskExecutionSize: cfg.Temporal.WorkerConcurrency,
		BuildID:                                cfg.Temporal.WorkerBuildID,
		UseBuildIDForVersioning:                cfg.Temporal.UseBuildIDForVersioning,
		Interceptors:                           []interceptor.WorkerInterceptor{workflow.NewHistoryPayloadGuard(0)},
	})

	// Reuse the product services for set planning and verified item admission.
	setRepo := repository.NewProblemSetRepository(dbPool)
	setTags := repository.NewTagRepository(dbPool)
	setProblems := service.NewProblemService(problemRepo, testCaseRepo, setTags, runtimeVectorRepo, temporalClient, cfg.Temporal.TaskQueue)
	setQuizzes := service.NewQuizService(quizRepo, kpRepo, temporalClient, cfg.Temporal.TaskQueue)
	setObjects, err := minioclient.NewMinIOClient(cfg.MinIO, log.Logger)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to configure set object reader")
	}
	setService := service.NewProblemSetService(setRepo, setProblems, setQuizzes, service.NewHydroExportService(setProblems, setObjects), llmClient)
	setSettings := handler.NewLLMSettingsHandler(repository.NewLLMProviderSettingsRepository(dbPool), settingsCipher, cfg.Anthropic)
	setGeneration := service.NewProblemSetGenerationService(setService, setRepo, setProblems, setTags, temporalClient, cfg.Temporal.TaskQueue, service.NewProblemSetProviderResolver(setSettings, runtimeKeyStore))
	w.RegisterActivity(setGeneration.PrepareProblemSetBatchActivity)
	w.RegisterActivity(setGeneration.RecordProblemSetSlotActivity)
	w.RegisterActivity(setGeneration.AttachProblemSetSlotActivity)
	w.RegisterActivity(setGeneration.FinishProblemSetGenerationActivity)
	w.RegisterWorkflow(workflow.ProblemSetGenerationWorkflow)
	w.RegisterWorkflow(workflow.ProblemSetQuizGenerationWorkflow)

	// Register workflows.
	w.RegisterWorkflow(workflow.ProblemGenerationWorkflow)
	w.RegisterWorkflow(workflow.ProblemGenerationStandardEvidenceWorkflowV1)
	w.RegisterWorkflow(workflow.ProblemGenerationAuthoringWorkflowV1)
	w.RegisterWorkflow(workflow.ProblemGenerationAuthoringStatementWorkflowV1)
	w.RegisterWorkflow(workflow.ProblemGenerationQualityWorkflowV1)
	w.RegisterWorkflow(workflow.S5MicroBatchGenerationWorkflowV1)
	w.RegisterWorkflow(workflow.ProblemValidationWorkflow)
	w.RegisterWorkflow(workflow.GPLTBatchGenerationWorkflow)
	w.RegisterWorkflow(workflow.QuizGenerationWorkflow)

	// Register activities.
	acts := activities.New(actDeps)
	w.RegisterActivity(acts.SimilarityCheckActivity)
	w.RegisterActivity(acts.FetchProblemDataActivity)
	w.RegisterActivity(acts.RefreshEditedProblemActivity)
	w.RegisterActivity(acts.GenerateAuthoringPlanActivity)
	w.RegisterActivity(acts.BuildCanonicalAuthoringBriefActivityV1)
	w.RegisterActivity(acts.RenderStatementFromAuthoringBundleActivityV1)
	w.RegisterActivity(acts.ValidateAuthoringSampleCandidatesActivityV1)
	w.RegisterActivity(acts.ExtractSemanticSpecArtifactActivityV1)
	w.RegisterActivity(acts.BuildS3SpecLintGateActivityV1)
	w.RegisterActivity(acts.GenerateS3TestDataActivityV1)
	w.RegisterActivity(acts.MaterializeS3CasePlanActivityV1)
	w.RegisterActivity(acts.GenerateMainSolutionActivityV1)
	w.RegisterActivity(acts.GenerateOracleCandidateActivityV1)
	w.RegisterActivity(acts.VerifyProgramAgainstIndependentOracleActivityV1)
	w.RegisterActivity(acts.ResolveS3HiddenSuiteActivityV1)
	w.RegisterActivity(acts.RunVerifiedAuthoringSamplesActivityV1)
	w.RegisterActivity(acts.StageAuthoringStatementSamplesActivityV1)
	w.RegisterActivity(acts.FinalizeAuthoringStatementSamplesActivityV1)
	w.RegisterActivity(acts.BuildS3SampleOutputGateActivityV1)
	w.RegisterActivity(acts.RunSanitizerGateActivityV1)
	w.RegisterActivity(acts.BuildS3TestManifestActivityV1)
	w.RegisterActivity(acts.S3DedupGateActivityV1)
	w.RegisterActivity(acts.S3StrictReviewerActivityV1)
	w.RegisterActivity(acts.S3HiddenRegressionActivityV1)
	w.RegisterActivity(acts.ResolveS3RepairPolicyActivityV1)
	w.RegisterActivity(acts.S3RepairRevisionActivityV1)
	w.RegisterActivity(acts.RecomputeS3VerdictActivityV1)
	w.RegisterActivity(acts.StoreS3QualityDraftActivityV1)
	w.RegisterActivity(acts.GenerateS5ConceptAttemptActivityV1)
	w.RegisterActivity(acts.NormalizeS5ConceptPoolActivityV1)
	w.RegisterActivity(acts.ObserveS5ConceptDedupBatchV1)
	w.RegisterActivity(acts.StoreS5ProvisionalReservationActivityV1)
	w.RegisterActivity(acts.GenerateStatementActivity)
	w.RegisterActivity(acts.RepairStatementActivityV1)
	w.RegisterActivity(acts.CleanStatementActivity)
	w.RegisterActivity(acts.PostStatementSimilarityActivity)
	w.RegisterActivity(acts.GenerateSolutionActivity)
	w.RegisterActivity(acts.RepairSolutionActivity)
	w.RegisterActivity(acts.CompileCheckActivity)
	w.RegisterActivity(acts.GenerateTestDataActivity)
	w.RegisterActivity(acts.RunSandboxActivity)
	w.RegisterActivity(acts.ValidateActivity)
	w.RegisterActivity(acts.BuildTestManifestActivity)
	w.RegisterActivity(acts.FinalizeStatementSamplesActivity)
	w.RegisterActivity(acts.AssessProblemFeasibilityActivity)
	w.RegisterActivity(acts.LLMReviewActivity)
	w.RegisterActivity(acts.StoreProblemActivity)
	w.RegisterActivity(acts.GenerateEditorialActivity)
	w.RegisterActivity(acts.ReviewProblemSetQuizActivity)
	w.RegisterActivity(acts.GenerateQuizActivity)
	w.RegisterActivity(acts.StoreQuizActivity)
	w.RegisterActivity(acts.DispatchOutboxActivity)

	// The outbox runtime is independent of Temporal scheduling so publication
	// continues even when no workflow invokes a dispatcher activity.
	runtimeCtx, cancelRuntime := context.WithCancel(context.Background())
	defer cancelRuntime()
	go outboxRuntime.Run(runtimeCtx)
	go retentionScheduler.Run(runtimeCtx)

	healthServer := &http.Server{
		Addr:              cfg.Outbox.HealthAddr,
		Handler:           outboxRuntime.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      5 * time.Second,
		IdleTimeout:       30 * time.Second,
	}

	type serviceError struct {
		component string
		err       error
	}
	errCh := make(chan serviceError, 2)
	go func() {
		errCh <- serviceError{component: "temporal worker", err: w.Run(worker.InterruptCh())}
	}()
	go func() {
		err := healthServer.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		errCh <- serviceError{component: "outbox health server", err: err}
	}()

	log.Info().
		Str("task_queue", cfg.Temporal.TaskQueue).
		Str("build_id", cfg.Temporal.WorkerBuildID).
		Bool("build_id_versioning", cfg.Temporal.UseBuildIDForVersioning).
		Bool("outbox_enabled", cfg.Outbox.Enabled).
		Bool("provenance_retention_enabled", cfg.Provenance.RetentionEnabled).
		Str("outbox_health_addr", cfg.Outbox.HealthAddr).
		Msg("temporal worker started and listening for tasks")

	// Wait for shutdown signal or worker error.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	var serviceFailure serviceError
	select {
	case sig := <-sigCh:
		log.Info().Str("signal", sig.String()).Msg("received shutdown signal, draining worker")
	case serviceFailure = <-errCh:
		if serviceFailure.err != nil {
			log.Error().Err(serviceFailure.err).Str("component", serviceFailure.component).Msg("worker service exited with error")
		}
	}

	cancelRuntime()
	w.Stop()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if err := healthServer.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error().Err(err).Msg("outbox health server shutdown failed")
	}
	if serviceFailure.err != nil {
		log.Fatal().Err(serviceFailure.err).Str("component", serviceFailure.component).Msg("worker stopped after service failure")
	}

	log.Info().Msg("temporal worker shut down gracefully")
}

func configureProviderEffectDependencies(
	deps *activities.Dependencies,
	store activities.ProviderEffectStore,
	cfg *config.Config,
	statementModelVersionID uuid.UUID,
) error {
	if deps == nil || cfg == nil {
		return fmt.Errorf("activity dependencies and config are required")
	}
	if err := validateWorkerConfiguration(cfg); err != nil {
		return err
	}
	deps.LLMProvider = cfg.Anthropic.ProviderID()
	deps.LLMModel = cfg.Anthropic.Model
	deps.LLMBaseURL = cfg.Anthropic.BaseURL
	deps.EmbeddingEnabled = cfg.Embedding.Enabled
	deps.EmbeddingProvider = cfg.Embedding.ProviderID()
	deps.EmbeddingModel = cfg.Embedding.Model
	deps.EmbeddingModelVersionID = statementModelVersionID
	deps.ProviderEffects = store
	deps.ProviderEffectLease = cfg.Temporal.ProviderEffectLease
	return deps.ValidateProviderEffectConfiguration()
}

func validateWorkerConfiguration(cfg *config.Config) error {
	if cfg == nil {
		return fmt.Errorf("worker config is required")
	}
	if !cfg.Embedding.Enabled {
		return fmt.Errorf("embedding must be enabled because StoreProblemActivity requires a durable problem embedding")
	}
	return nil
}

func newOutboxStatusObserver() func(outbox.RuntimeStatus) {
	var lastReason string
	var lastAlert time.Time
	return func(status outbox.RuntimeStatus) {
		if status.Ready {
			if lastReason != "" {
				log.Info().Msg("outbox runtime recovered readiness")
			}
			lastReason = ""
			return
		}
		now := time.Now()
		if status.Reason != lastReason || now.Sub(lastAlert) >= 30*time.Second {
			log.Error().
				Str("reason", status.Reason).
				Int64("queued", status.Backlog.Queued()).
				Int64("dead", status.Backlog.Dead).
				Dur("oldest_age", status.Backlog.OldestUndeliveredAge).
				Msg("outbox runtime is not ready")
			lastReason = status.Reason
			lastAlert = now
		}
	}
}

func newRetentionStatusObserver() func(provenancegov.RetentionRunStatus) {
	return func(status provenancegov.RetentionRunStatus) {
		switch {
		case !status.Enabled:
			log.Warn().Msg("provenance retention scheduler is disabled")
		case status.Error != "":
			log.Error().
				Str("error", status.Error).
				Int64("expired", status.Expired).
				Int("batches", status.Batches).
				Msg("provenance retention audit failed")
		case status.Exhausted:
			log.Error().
				Int64("expired", status.Expired).
				Int("batches", status.Batches).
				Msg("provenance retention audit exhausted its batch budget; backlog may remain")
		case status.Expired > 0:
			log.Info().
				Int64("expired", status.Expired).
				Int("batches", status.Batches).
				Msg("provenance retention audit completed")
		default:
			log.Debug().Msg("provenance retention audit found no expired artifacts")
		}
	}
}

// setLogLevel configures the global zerolog level from a string.
func setLogLevel(level string) {
	switch level {
	case "debug":
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	case "info":
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	case "warn":
		zerolog.SetGlobalLevel(zerolog.WarnLevel)
	case "error":
		zerolog.SetGlobalLevel(zerolog.ErrorLevel)
	default:
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	}
}
