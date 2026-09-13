package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/config"
	"github.com/Gingoo-TvT/Qraft/backend/internal/diversitymode"
	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/exportmode"
	"github.com/Gingoo-TvT/Qraft/backend/internal/generationapi"
	"github.com/Gingoo-TvT/Qraft/backend/internal/handler"
	"github.com/Gingoo-TvT/Qraft/backend/internal/handler/middleware"
	"github.com/Gingoo-TvT/Qraft/backend/internal/llm"
	"github.com/Gingoo-TvT/Qraft/backend/internal/llm/prompts"
	"github.com/Gingoo-TvT/Qraft/backend/internal/qualitymode"
	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
	"github.com/Gingoo-TvT/Qraft/backend/internal/runtimekeys"
	"github.com/Gingoo-TvT/Qraft/backend/internal/secureconfig"
	"github.com/Gingoo-TvT/Qraft/backend/internal/service"
	"github.com/Gingoo-TvT/Qraft/backend/pkg/minio"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v4"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	temporalclient "go.temporal.io/sdk/client"
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

	setLogLevel(cfg.App.LogLevel)

	generationAPIMode, err := generationapi.ResolveMode("", false, os.LookupEnv)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to resolve custom generation API mode")
	}
	log.Info().
		Str("mode", generationAPIMode.EffectiveMode).
		Bool("route_enabled", generationAPIMode.ProductRouteEnabled).
		Msg("custom generation API mode resolved")

	qg15ExportMode, err := exportmode.ResolveMode("", false, os.LookupEnv)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to resolve QG15 export mode")
	}
	log.Info().
		Str("mode", qg15ExportMode.EffectiveMode).
		Bool("product_routes", qg15ExportMode.QG15ProductRoutes).
		Bool("hydro_s3_binding", qg15ExportMode.HydroS3BindingEnabled).
		Msg("QG15 export mode resolved")

	qualityMode, err := qualitymode.ResolveMode("", false, os.LookupEnv)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to resolve V1 quality mode")
	}
	log.Info().
		Str("mode", qualityMode.EffectiveMode).
		Bool("extended_evidence_levels", qualityMode.ExtendedEvidenceLevels).
		Msg("V1 quality mode resolved")

	s5DiversityMode, err := diversitymode.ResolveMode("", false, os.LookupEnv)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to resolve S5 diversity mode")
	}
	log.Info().
		Str("mode", s5DiversityMode.EffectiveMode).
		Bool("micro_batch_route_enabled", s5DiversityMode.MicroBatchRouteEnabled).
		Msg("S5 diversity mode resolved")

	log.Info().
		Int("port", cfg.App.Port).
		Msg("starting algoforge api server")

	// -----------------------------------------------------------------------
	// Infrastructure clients
	// -----------------------------------------------------------------------

	// PostgreSQL connection pool.
	dbPool, err := pgxpool.New(context.Background(), cfg.Database.DSN())
	if err != nil {
		log.Fatal().Err(err).Msg("failed to connect to database")
	}
	defer dbPool.Close()

	if err := dbPool.Ping(context.Background()); err != nil {
		log.Fatal().Err(err).Msg("failed to ping database")
	}
	log.Info().Msg("database connection established")

	// Redis client.
	rdb := redis.NewClient(&redis.Options{
		Addr:     cfg.Redis.Addr,
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
	})
	defer rdb.Close()

	if err := rdb.Ping(context.Background()).Err(); err != nil {
		log.Fatal().Err(err).Msg("failed to ping redis")
	}
	log.Info().Str("addr", cfg.Redis.Addr).Msg("redis connection established")
	runtimeKeyStore := runtimekeys.NewStore(rdb, 6*time.Hour)

	// MinIO client.
	minioClient, err := minio.NewMinIOClient(cfg.MinIO, log.Logger)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to create minio client")
	}

	if err := minioClient.EnsureDefaultBucket(context.Background()); err != nil {
		log.Fatal().Err(err).Msg("failed to ensure minio bucket")
	}
	log.Info().Str("endpoint", cfg.MinIO.Endpoint).Msg("minio client created")

	// Temporal client.
	temporalClient, err := temporalclient.Dial(temporalclient.Options{
		HostPort:  cfg.Temporal.Host,
		Namespace: cfg.Temporal.Namespace,
	})
	if err != nil {
		log.Fatal().Err(err).Msg("failed to create temporal client")
	}
	defer temporalClient.Close()
	log.Info().Msg("temporal client connected")

	// LLM client.
	llmClient := llm.NewClient(cfg.Anthropic)
	llmClient.SetAPIKeyResolver(runtimeKeyStore)
	log.Info().Str("model", cfg.Anthropic.Model).Msg("llm client created")

	// -----------------------------------------------------------------------
	// Repositories
	// -----------------------------------------------------------------------

	problemRepo := repository.NewProblemRepository(dbPool)
	testCaseRepo := repository.NewTestCaseRepository(dbPool)
	tagRepo := repository.NewTagRepository(dbPool)
	vectorRepo := repository.NewVectorRepository(dbPool)
	quizRepo := repository.NewQuizRepository(dbPool)
	kpRepo := repository.NewKnowledgePointRepository(dbPool)
	problemSetRepo := repository.NewProblemSetRepository(dbPool)
	llmSettingsRepo := repository.NewLLMProviderSettingsRepository(dbPool)
	embeddingSettingsRepo := repository.NewEmbeddingProviderSettingsRepository(dbPool)
	reviewSettingsRepo := repository.NewReviewSettingsRepository(dbPool)
	runtimeVectorRepo := vectorRepo.RequireStatementModelVersion()
	embeddingHandlerRuntimeConfig := cfg.Embedding
	configuredStatementModel, resolveErr := vectorRepo.ResolveConfiguredModelVersion(
		context.Background(),
		strings.TrimSpace(cfg.Embedding.ExpectedStatementModelVersionID),
		cfg.Embedding.ProviderID(),
		cfg.Embedding.Model,
		cfg.Embedding.Dimensions,
	)
	if resolveErr != nil {
		log.Warn().Err(resolveErr).Msg("configured statement embedding is unresolved; embedding administration remains available but product similarity is disabled")
	} else if boundRepo, bindErr := vectorRepo.BindStatementModelVersion(configuredStatementModel.ID); bindErr != nil {
		log.Warn().Err(bindErr).Msg("configured statement embedding could not be bound; embedding administration remains available but product similarity is disabled")
	} else {
		runtimeVectorRepo = boundRepo
		embeddingHandlerRuntimeConfig.ExpectedStatementModelVersionID = configuredStatementModel.ID.String()
		if activeID, activeErr := vectorRepo.ActiveModelVersion(context.Background(), repository.EmbeddingKindStatement); activeErr != nil {
			log.Warn().Err(activeErr).Msg("statement embedding active pointer could not be read")
		} else if activeID != configuredStatementModel.ID {
			log.Warn().Str("configured_model_version_id", configuredStatementModel.ID.String()).Str("active_model_version_id", activeID.String()).Msg("statement embedding config and active pointer differ; API similarity remains pinned to configuration")
		}
	}

	// -----------------------------------------------------------------------
	// Services
	// -----------------------------------------------------------------------

	problemService := service.NewProblemService(
		problemRepo, testCaseRepo, tagRepo, runtimeVectorRepo,
		temporalClient, cfg.Temporal.TaskQueue,
	)
	quizService := service.NewQuizService(quizRepo, kpRepo, temporalClient, cfg.Temporal.TaskQueue)
	quizImportService := service.NewQuizImportService(quizRepo, kpRepo)
	quizExportService := service.NewQuizExportService(quizRepo)
	setHydroExportService := service.NewHydroExportService(problemService, minioClient)
	problemQualityService := service.NewProblemQualityService(problemService, minioClient)

	promptRegistry := prompts.NewRegistry()
	llmService := service.NewLLMService(llmClient, promptRegistry)

	_ = llmService  // referenced by workflow activities, not directly by handlers
	_ = minioClient // used by testdata service
	_ = rdb         // available for caching layers

	// -----------------------------------------------------------------------
	// Handlers
	// -----------------------------------------------------------------------

	settingsSecret, settingsSecretSource, err := secureconfig.ResolveSettingsSecret(
		cfg.App.SettingsEncryptionKey, cfg.App.JWTSecret, cfg.App.DevMode,
	)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to resolve permanent settings encryption key")
	}
	settingsCipher, err := secureconfig.NewCipher(settingsSecret)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to configure permanent LLM settings encryption")
	}
	if settingsSecretSource != "explicit" {
		log.Warn().Str("source", settingsSecretSource).Msg("ALGOFORGE_SETTINGS_ENCRYPTION_KEY is unset; using a compatibility fallback")
	}
	llmSettingsHandler := handler.NewLLMSettingsHandler(llmSettingsRepo, settingsCipher, cfg.Anthropic)
	statementRuntimeResolver := func(ctx context.Context) (*domain.LLMRuntimeConfig, error) {
		runtimeConfig, err := llmSettingsHandler.EffectiveRuntimeConfig(ctx, repository.LLMProviderPurposeStatement)
		if err != nil {
			return nil, err
		}
		if runtimeConfig == nil {
			return nil, fmt.Errorf("saved statement LLM configuration is required")
		}
		prepared := *runtimeConfig
		if rawKey := strings.TrimSpace(prepared.APIKey); rawKey != "" {
			if strings.TrimSpace(prepared.APIKeyRef) != "" {
				return nil, fmt.Errorf("saved statement LLM API key cannot be combined with an API key reference")
			}
			ref, err := runtimeKeyStore.Put(ctx, rawKey)
			if err != nil {
				return nil, fmt.Errorf("preparing saved statement LLM API key: %w", err)
			}
			prepared.APIKey = ""
			prepared.APIKeyRef = ref
		}
		return &prepared, nil
	}
	quizService.SetLLMRuntimeResolver(statementRuntimeResolver)
	problemSetService := service.NewProblemSetService(problemSetRepo, problemService, quizService, setHydroExportService, llmClient)
	problemSetService.SetLLMRuntimeResolver(statementRuntimeResolver)
	problemSetHandler := handler.NewProblemSetHandler(problemSetService)
	if generationAPIMode.ProductRouteEnabled {
		problemSetHandler.SetGenerationService(service.NewProblemSetGenerationService(problemSetService, problemSetRepo, problemService, tagRepo, temporalClient, cfg.Temporal.TaskQueue, service.NewProblemSetProviderResolver(llmSettingsHandler, runtimeKeyStore)))
	}
	reviewSettingsHandler := handler.NewReviewSettingsHandler(reviewSettingsRepo)
	problemHandler := handler.NewProblemHandler(problemService, minioClient, runtimeKeyStore)
	problemHandler.SetPermanentProviderSettings(llmSettingsHandler, true)
	problemHandler.SetQG15ExportEnabled(qg15ExportMode.HydroS3BindingEnabled)
	workflowHandler := handler.NewWorkflowHandler(temporalClient, cfg.Temporal.Namespace)
	generationJobHandler, err := handler.NewGenerationJobHandlerWithQualityMode(
		problemService, temporalClient, problemHandler, qualityMode,
	)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to configure generation Job quality mode")
	}
	s5MicroBatchHandler, err := handler.NewS5MicroBatchHandler(
		problemService, temporalClient, problemHandler, qualityMode,
	)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to configure S5 micro-batch handler")
	}
	problemQualityHandler := handler.NewProblemQualityHandler(problemQualityService)
	integrationCapabilitiesHandler, err := handler.NewIntegrationCapabilitiesHandler(
		generationAPIMode, qg15ExportMode, qualityMode, s5DiversityMode,
	)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to configure integration capabilities")
	}
	tagHandler := handler.NewTagHandler(tagRepo)
	statsHandler := handler.NewStatsHandler(problemRepo)
	quizHandler := handler.NewQuizHandler(quizService, quizImportService, quizExportService, kpRepo)
	questionSearchHandler := handler.NewQuestionSearchHandler(repository.NewQuestionSearchRepository(dbPool))
	embeddingHandler := handler.NewEmbeddingHandler(vectorRepo, embeddingHandlerRuntimeConfig)
	embeddingHandler.SetPersistentRuntimeSettings(embeddingSettingsRepo, settingsCipher)

	// -----------------------------------------------------------------------
	// Echo server
	// -----------------------------------------------------------------------

	e := echo.New()
	e.HideBanner = true
	e.HidePort = true

	// Global middleware.
	e.Use(middleware.Recovery())
	e.Use(middleware.Logger())
	e.Use(middleware.CORS(middleware.DefaultCORSConfig()))
	e.Use(middleware.Auth(cfg.App.JWTSecret, cfg.App.DevMode))

	// Health check (public, bypasses auth via PublicPaths).
	e.GET("/health", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	})

	// -----------------------------------------------------------------------
	// API v1 routes
	// -----------------------------------------------------------------------

	v1 := e.Group("/api/v1")
	v1.GET("/integration/capabilities", integrationCapabilitiesHandler.HandleGet)

	// Problems.
	v1.POST("/problems/generate", problemHandler.HandleGenerate)
	if generationAPIMode.ProductRouteEnabled {
		handler.RegisterGenerationJobRoutes(v1, generationJobHandler)
	}
	if err := handler.RegisterS5MicroBatchRoutes(v1, s5MicroBatchHandler, s5DiversityMode); err != nil {
		log.Fatal().Err(err).Msg("failed to register S5 micro-batch routes")
	}
	v1.POST("/problems/gplt/generate", problemHandler.HandleGPLTGenerate)
	v1.GET("/problems", problemHandler.HandleList)
	v1.GET("/problems/similar", problemHandler.HandleFindSimilar)
	if qualityMode.ExtendedEvidenceLevels {
		handler.RegisterProblemQualityRoutes(v1, problemQualityHandler)
	}
	handler.RegisterExportRoutes(v1, problemHandler, quizHandler, qg15ExportMode.QG15ProductRoutes)
	v1.GET("/problems/:id", problemHandler.HandleGet)
	v1.PUT("/problems/:id", problemHandler.HandleUpdate)
	v1.POST("/problems/:id/edit-refresh", problemHandler.HandleCompleteEditRefresh)
	v1.POST("/problems/:id/public-release-approval", problemHandler.HandleApprovePublicRelease)
	v1.DELETE("/problems/:id", problemHandler.HandleDelete)
	v1.GET("/problems/:id/testcases", problemHandler.HandleGetTestCases)
	v1.GET("/problems/:id/test-manifest", problemHandler.HandleGetTestManifest)
	v1.GET("/problems/:id/standard-evidence", problemHandler.HandleGetStandardEvidence)
	v1.GET("/problems/:id/testcases/:tid/input", problemHandler.HandleGetTestCaseInput)
	v1.GET("/problems/:id/testcases/:tid/output", problemHandler.HandleGetTestCaseOutput)
	v1.GET("/problems/:id/testdata.zip", problemHandler.HandleDownloadAllTestData)
	v1.GET("/problems/:id/metadata", problemHandler.HandleGetMetadata)
	v1.GET("/problems/:id/editorial", problemHandler.HandleGetEditorial)
	v1.GET("/problems/:id/solutions", problemHandler.HandleGetSolutions)
	v1.POST("/problems/:id/validate", problemHandler.HandleValidate)

	// Quizzes.
	v1.POST("/quizzes", quizHandler.HandleCreate)
	v1.GET("/questions/search", questionSearchHandler.HandleSearch)
	v1.GET("/quizzes", quizHandler.HandleList)
	v1.POST("/quizzes/generate", quizHandler.HandleGenerate)
	v1.POST("/quizzes/import", quizHandler.HandleImport)
	v1.GET("/quizzes/export", quizHandler.HandleExport)
	v1.GET("/quizzes/template.xlsx", quizHandler.HandleTemplate)
	v1.GET("/quizzes/:id", quizHandler.HandleGet)
	v1.PUT("/quizzes/:id", quizHandler.HandleUpdate)
	v1.DELETE("/quizzes/:id", quizHandler.HandleDelete)
	v1.GET("/knowledge-points", quizHandler.HandleListKnowledgePoints)

	// Contest/homework collections and their portable AlgoForge package.
	handler.RegisterProblemSetRoutes(v1, problemSetHandler)

	// Tags.
	v1.GET("/tags", tagHandler.HandleList)

	// Workflows.
	v1.GET("/workflows", workflowHandler.HandleList)
	v1.GET("/workflows/:id", workflowHandler.HandleGet)
	v1.GET("/workflows/:id/events", workflowHandler.HandleEvents)
	v1.POST("/workflows/:id/approve", workflowHandler.HandleApprove)
	v1.POST("/workflows/:id/reject", workflowHandler.HandleReject)
	v1.POST("/workflows/:id/retry", workflowHandler.HandleRetry)
	v1.DELETE("/workflows/:id", workflowHandler.HandleCancel)

	// Stats.
	v1.GET("/stats", statsHandler.HandleStats)

	// Durable LLM provider defaults. API keys are write-only and encrypted.
	v1.GET("/settings/llm", llmSettingsHandler.HandleGet)
	v1.PUT("/settings/llm/:purpose", llmSettingsHandler.HandleUpdate)
	v1.DELETE("/settings/llm/:purpose", llmSettingsHandler.HandleDelete)
	v1.GET("/settings/review", reviewSettingsHandler.HandleGet)
	v1.PUT("/settings/review", reviewSettingsHandler.HandleUpdate)

	// Embedding runtime status.
	v1.GET("/embedding/status", embeddingHandler.HandleStatus)
	v1.GET("/embedding/models", embeddingHandler.HandleModelVersions)
	v1.GET("/embedding/runtime-settings", embeddingHandler.HandleRuntimeSettings)
	v1.GET("/embedding/saved-runtime-settings", embeddingHandler.HandleSavedRuntimeSettings)
	v1.POST("/embedding/local/test", embeddingHandler.HandleTestLocal)
	v1.POST("/embedding/local/deploy", embeddingHandler.HandleDeployLocal)
	v1.POST("/embedding/local/backfill", embeddingHandler.HandleBackfillLocal)
	v1.POST("/embedding/local/activate", embeddingHandler.HandleActivateLocal)

	// -----------------------------------------------------------------------
	// Graceful shutdown
	// -----------------------------------------------------------------------

	addr := fmt.Sprintf(":%d", cfg.App.Port)

	go func() {
		log.Info().Str("addr", addr).Msg("http server listening")
		if err := e.Start(addr); err != nil && err != http.ErrServerClosed {
			log.Fatal().Err(err).Msg("http server error")
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit

	log.Info().Str("signal", sig.String()).Msg("received shutdown signal")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := e.Shutdown(ctx); err != nil {
		log.Fatal().Err(err).Msg("http server forced shutdown")
	}

	log.Info().Msg("api server shut down gracefully")
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
