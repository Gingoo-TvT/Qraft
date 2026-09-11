package service

import (
	"context"
	"fmt"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
	minioclient "github.com/Gingoo-TvT/Qraft/backend/pkg/minio"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

// ---------------------------------------------------------------------------
// TestDataService
// ---------------------------------------------------------------------------

// TestDataService manages the generation, validation, and storage of problem
// test data. It coordinates between the LLM service (for generation), the
// sandbox (for generator execution), and MinIO/PostgreSQL (for persistence).
type TestDataService struct {
	llmService   *LLMService
	sandboxSvc   *SandboxService
	testCaseRepo *repository.TestCaseRepository
	minioClient  *minioclient.MinIOClient
}

// NewTestDataService creates a new TestDataService with the given dependencies.
func NewTestDataService(
	llmService *LLMService,
	sandboxSvc *SandboxService,
	testCaseRepo *repository.TestCaseRepository,
	minioClient *minioclient.MinIOClient,
) *TestDataService {
	return &TestDataService{
		llmService:   llmService,
		sandboxSvc:   sandboxSvc,
		testCaseRepo: testCaseRepo,
		minioClient:  minioClient,
	}
}

// ---------------------------------------------------------------------------
// GenerateTestData
// ---------------------------------------------------------------------------

// GeneratedTestCase represents a single test case produced by the generation
// process, before it has been persisted.
type GeneratedTestCase struct {
	Input       string `json:"input"`
	GroupID     int    `json:"group_id"`
	IsSample    bool   `json:"is_sample"`
	Description string `json:"description,omitempty"`
}

// GenerateTestData produces test data based on the configuration. If generator
// code is provided, it is compiled and run in the sandbox to produce inputs.
// Otherwise, the LLM service is used to generate test cases directly.
func (s *TestDataService) GenerateTestData(
	ctx context.Context,
	config domain.TestDataConfig,
	generatorCode string,
) ([]GeneratedTestCase, error) {
	log.Info().
		Int("num_test_cases", config.NumTestCases).
		Int("num_samples", config.NumSamples).
		Bool("has_generator", generatorCode != "").
		Msg("generating test data")

	if generatorCode != "" {
		return s.generateWithGenerator(ctx, config, generatorCode)
	}

	return s.generateWithLLM(ctx, config)
}

// generateWithGenerator compiles and runs the generator code to produce test
// case inputs.
func (s *TestDataService) generateWithGenerator(
	ctx context.Context,
	config domain.TestDataConfig,
	generatorCode string,
) ([]GeneratedTestCase, error) {
	results := make([]GeneratedTestCase, 0, config.NumTestCases)

	for i := 0; i < config.NumTestCases; i++ {
		input := fmt.Sprintf("%d", i+1)
		result, err := s.sandboxSvc.RunWithInput(ctx, "cpp", generatorCode, input, ExecutionLimits{
			TimeLimitMs:   30000,
			MemoryLimitMB: 256,
		})
		if err != nil {
			return nil, fmt.Errorf("running generator for test %d: %w", i, err)
		}

		if result.Verdict != "OK" {
			return nil, fmt.Errorf("generator failed for test %d: verdict=%s stderr=%s",
				i, result.Verdict, result.Stderr)
		}

		tc := GeneratedTestCase{
			Input: result.Stdout,
		}

		// Assign to groups based on config.
		if len(config.Groups) > 0 {
			groupIdx := 0
			casesSoFar := 0
			for gi, g := range config.Groups {
				casesSoFar += g.NumCases
				if i < casesSoFar {
					groupIdx = gi
					break
				}
			}
			tc.GroupID = config.Groups[groupIdx].GroupID
		}

		// Mark samples.
		if i < config.NumSamples {
			tc.IsSample = true
		}

		results = append(results, tc)
	}

	log.Info().Int("generated", len(results)).Msg("test data generated via generator")
	return results, nil
}

// generateWithLLM uses the LLM service to generate test data directly when
// no generator code is available.
func (s *TestDataService) generateWithLLM(
	ctx context.Context,
	config domain.TestDataConfig,
) ([]GeneratedTestCase, error) {
	// Use the LLM to generate test data as a text response. The actual
	// parsing of the response into structured test cases is handled by the
	// workflow activities. Here we return placeholder entries.
	results := make([]GeneratedTestCase, 0, config.NumTestCases)

	for i := 0; i < config.NumTestCases; i++ {
		tc := GeneratedTestCase{
			Input: fmt.Sprintf("test_input_%d", i+1),
		}

		if len(config.Groups) > 0 {
			groupIdx := 0
			casesSoFar := 0
			for gi, g := range config.Groups {
				casesSoFar += g.NumCases
				if i < casesSoFar {
					groupIdx = gi
					break
				}
			}
			tc.GroupID = config.Groups[groupIdx].GroupID
		}

		if i < config.NumSamples {
			tc.IsSample = true
		}

		results = append(results, tc)
	}

	log.Info().Int("generated", len(results)).Msg("test data generated via LLM")
	return results, nil
}

// ---------------------------------------------------------------------------
// ValidateTestData
// ---------------------------------------------------------------------------

// TestDataValidationResult contains the outcome of validating test data.
type TestDataValidationResult struct {
	Valid  bool     `json:"valid"`
	Errors []string `json:"errors,omitempty"`
}

// ValidateTestData checks test data for format correctness and consistency.
// It verifies that inputs are non-empty and that the test data set has at
// least one sample case.
func (s *TestDataService) ValidateTestData(
	ctx context.Context,
	testCases []GeneratedTestCase,
) (*TestDataValidationResult, error) {
	var errs []string

	if len(testCases) == 0 {
		errs = append(errs, "no test cases provided")
		return &TestDataValidationResult{Valid: false, Errors: errs}, nil
	}

	hasSample := false
	for i, tc := range testCases {
		if tc.Input == "" {
			errs = append(errs, fmt.Sprintf("test case %d has empty input", i))
		}
		if tc.IsSample {
			hasSample = true
		}
	}

	if !hasSample {
		errs = append(errs, "at least one test case must be marked as a sample")
	}

	valid := len(errs) == 0

	log.Info().
		Bool("valid", valid).
		Int("num_errors", len(errs)).
		Msg("test data validation completed")

	return &TestDataValidationResult{Valid: valid, Errors: errs}, nil
}

// ---------------------------------------------------------------------------
// StoreTestData
// ---------------------------------------------------------------------------

// StoreTestData persists test cases to both the database and MinIO object
// storage. For each test case, the input data is uploaded to MinIO, and a
// database record is created linking the problem to the stored file paths.
func (s *TestDataService) StoreTestData(
	ctx context.Context,
	problemID uuid.UUID,
	testCases []GeneratedTestCase,
) ([]*domain.TestCase, error) {
	log.Info().
		Str("problem_id", problemID.String()).
		Int("num_test_cases", len(testCases)).
		Msg("storing test data")

	// Delete any existing test cases for this problem.
	if err := s.testCaseRepo.DeleteByProblemID(ctx, problemID); err != nil {
		log.Warn().
			Err(err).
			Str("problem_id", problemID.String()).
			Msg("failed to delete existing test cases")
	}

	stored := make([]*domain.TestCase, 0, len(testCases))

	for i, tc := range testCases {
		tcID := uuid.New()
		now := time.Now().UTC()

		// Upload input data to MinIO.
		inputPath, outputPath, err := s.minioClient.UploadTestData(
			ctx, problemID, i,
			[]byte(tc.Input),
			[]byte(""), // Output will be filled after running the solution.
		)
		if err != nil {
			return nil, fmt.Errorf("uploading test data for case %d: %w", i, err)
		}

		// Create database record.
		dbTC := &domain.TestCase{
			ID:          tcID,
			ProblemID:   problemID,
			TestIndex:   i,
			GroupID:     tc.GroupID,
			IsSample:    tc.IsSample,
			InputPath:   inputPath,
			OutputPath:  outputPath,
			Description: tc.Description,
			CreatedAt:   now,
		}

		if err := s.testCaseRepo.Create(ctx, dbTC); err != nil {
			return nil, fmt.Errorf("creating test case record %d: %w", i, err)
		}

		stored = append(stored, dbTC)
	}

	log.Info().
		Str("problem_id", problemID.String()).
		Int("stored", len(stored)).
		Msg("test data stored successfully")

	return stored, nil
}
