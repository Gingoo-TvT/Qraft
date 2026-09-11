package service

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
	algoworkflow "github.com/Gingoo-TvT/Qraft/backend/internal/workflow"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
)

// ---------------------------------------------------------------------------
// ProblemService
// ---------------------------------------------------------------------------

// ProblemService implements the business logic for problem CRUD operations and
// orchestrates generation workflows via Temporal.
type ProblemService struct {
	problemRepo  *repository.ProblemRepository
	testCaseRepo *repository.TestCaseRepository
	tagRepo      *repository.TagRepository
	vectorRepo   *repository.VectorRepository
	temporal     client.Client
	taskQueue    string
}

// NewProblemService creates a new ProblemService with the given dependencies.
func NewProblemService(
	problemRepo *repository.ProblemRepository,
	testCaseRepo *repository.TestCaseRepository,
	tagRepo *repository.TagRepository,
	vectorRepo *repository.VectorRepository,
	temporal client.Client,
	taskQueue string,
) *ProblemService {
	return &ProblemService{
		problemRepo:  problemRepo,
		testCaseRepo: testCaseRepo,
		tagRepo:      tagRepo,
		vectorRepo:   vectorRepo,
		temporal:     temporal,
		taskQueue:    taskQueue,
	}
}

// ---------------------------------------------------------------------------
// CRUD operations
// ---------------------------------------------------------------------------

// CreateProblem persists a new problem. It validates the input, assigns an ID,
// and stores the record.
func (s *ProblemService) CreateProblem(ctx context.Context, p *domain.Problem) error {
	if p.ID == uuid.Nil {
		p.ID = uuid.New()
	}
	now := time.Now().UTC()
	p.CreatedAt = now
	p.UpdatedAt = now

	if p.Status == "" {
		p.Status = domain.ProblemStatusDraft
	}

	if err := p.Validate(); err != nil {
		return fmt.Errorf("validation: %w", err)
	}

	if err := s.problemRepo.Create(ctx, p); err != nil {
		return fmt.Errorf("creating problem: %w", err)
	}

	log.Info().
		Str("problem_id", p.ID.String()).
		Str("title", p.Title).
		Msg("problem created")

	return nil
}

// GetProblem retrieves a single problem by ID.
func (s *ProblemService) GetProblem(ctx context.Context, id uuid.UUID) (*domain.Problem, error) {
	p, err := s.problemRepo.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("getting problem: %w", err)
	}
	// detailed_solution is a Markdown field.  Older generation runs could
	// persist a provider JSON envelope, so normalize it at the read boundary as
	// well as at write time.  This keeps existing records usable without a
	// destructive database rewrite.
	p.DetailedSolution = domain.NormalizeEditorialMarkdownV1(p.DetailedSolution)
	return p, nil
}

// GetGenerationStandardEvidence returns the immutable post-gate receipt
// binding without projecting it into governed problem metadata.
func (s *ProblemService) GetGenerationStandardEvidence(
	ctx context.Context,
	id uuid.UUID,
) (domain.GenerationStandardEvidenceBinding, bool, error) {
	binding, ok, err := s.problemRepo.GetGenerationStandardEvidence(ctx, id)
	if err != nil {
		return domain.GenerationStandardEvidenceBinding{}, false, fmt.Errorf("getting generation standard evidence: %w", err)
	}
	return binding, ok, nil
}

// ListProblemsFilter encapsulates query parameters for listing problems.
type ListProblemsFilter struct {
	Level         *domain.ProblemLevel
	MinDifficulty *int
	MaxDifficulty *int
	Tags          []string
	Status        *string
	Page          int
	PageSize      int
}

// ListProblemsResult contains the paginated result of a ListProblems call.
type ListProblemsResult struct {
	Problems []*domain.Problem
	Total    int
	Page     int
	PageSize int
}

// ProblemUpdateInput is the service-level S2.6 edit DTO. It intentionally
// excludes status, source and workflow_id because those are system-managed.
type ProblemUpdateInput struct {
	ExpectedUpdatedAt time.Time
	Actor             string

	Title            *string
	Statement        *string
	Level            *domain.ProblemLevel
	Difficulty       *int
	OneLineHint      *string
	DetailedSolution *string
	Tags             *[]string
	TimeLimit        *int
	MemoryLimit      *int
	MetadataJSON     *json.RawMessage
}

// ProblemEditRefreshInput completes the S2.6 re-embed/revalidate/release gate
// after an edited problem has fresh release-critical artifacts.
type ProblemEditRefreshInput struct {
	OperationKey           string
	Actor                  string
	ValidationReportSHA256 string
}

// PublicReleaseApprovalInput is the explicit administrator decision. Actor is
// derived from authenticated context and Approved must be true.
type PublicReleaseApprovalInput struct {
	Approved bool
	Actor    string
}

// ListProblems returns a paginated, filtered list of problems.
func (s *ProblemService) ListProblems(ctx context.Context, filter ListProblemsFilter) (*ListProblemsResult, error) {
	page := filter.Page
	if page < 1 {
		page = 1
	}
	pageSize := filter.PageSize
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize

	repoFilter := problemRepositoryListFilter(filter, pageSize, offset)

	problems, total, err := s.problemRepo.List(ctx, repoFilter)
	if err != nil {
		return nil, fmt.Errorf("listing problems: %w", err)
	}

	return &ListProblemsResult{
		Problems: problems,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	}, nil
}

func problemRepositoryListFilter(filter ListProblemsFilter, pageSize, offset int) repository.ProblemFilter {
	return repository.ProblemFilter{
		Level:              filter.Level,
		MinDiff:            filter.MinDifficulty,
		MaxDiff:            filter.MaxDifficulty,
		Tags:               filter.Tags,
		Status:             filter.Status,
		ExcludeQuarantined: filter.Status == nil,
		ExcludeRejected:    filter.Status == nil,
		Limit:              pageSize,
		Offset:             offset,
	}
}

// UpdateProblem updates the mutable fields of an existing problem.
func (s *ProblemService) UpdateProblem(ctx context.Context, id uuid.UUID, input ProblemUpdateInput) (*domain.Problem, error) {
	patch := repository.ProblemEditPatch{
		ProblemID:         id,
		ExpectedUpdatedAt: input.ExpectedUpdatedAt,
		Actor:             input.Actor,
		Title:             input.Title,
		Statement:         input.Statement,
		Level:             input.Level,
		Difficulty:        input.Difficulty,
		OneLineHint:       input.OneLineHint,
		DetailedSolution:  input.DetailedSolution,
		Tags:              input.Tags,
		TimeLimit:         input.TimeLimit,
		MemoryLimit:       input.MemoryLimit,
		MetadataJSON:      input.MetadataJSON,
	}
	updated, err := s.problemRepo.Edit(ctx, patch)
	if err != nil {
		if errors.Is(err, repository.ErrReviewQuarantineProtected) {
			return nil, ErrProtected
		}
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		if errors.Is(err, repository.ErrConcurrentProblemEdit) ||
			errors.Is(err, repository.ErrProblemStatusNotEditable) {
			return nil, ErrConflict
		}
		if errors.Is(err, repository.ErrProblemEditNoMutableFields) ||
			strings.HasPrefix(err.Error(), "validation: ") {
			return nil, fmt.Errorf("validation: %w", err)
		}
		return nil, fmt.Errorf("updating problem: %w", err)
	}

	log.Info().
		Str("problem_id", updated.ID.String()).
		Msg("problem updated")

	return updated, nil
}

// CompleteProblemEditRefresh clears stale markers and re-enters the publication
// gate once a problem edit has been re-embedded and revalidated.
func (s *ProblemService) CompleteProblemEditRefresh(
	ctx context.Context,
	id uuid.UUID,
	input ProblemEditRefreshInput,
) (repository.ProblemEditRefreshReport, error) {
	operationKey := strings.TrimSpace(input.OperationKey)
	validationHash := strings.TrimSpace(input.ValidationReportSHA256)
	if operationKey == "" && validationHash != "" {
		suffix := validationHash
		if len(suffix) > 16 {
			suffix = suffix[:16]
		}
		operationKey = "problem-edit-refresh:" + id.String() + ":" + suffix
	}
	actor := strings.TrimSpace(input.Actor)
	if actor == "" {
		actor = "api"
	}

	report, err := s.problemRepo.CompleteProblemEditRefresh(ctx, repository.ProblemEditRefreshOptions{
		ProblemID:              id,
		OperationKey:           operationKey,
		Actor:                  actor,
		ValidationReportSHA256: validationHash,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return report, ErrNotFound
		}
		if errors.Is(err, repository.ErrConcurrentProblemEdit) {
			return report, ErrConflict
		}
		if strings.Contains(err.Error(), "validation_report_sha256") ||
			strings.Contains(err.Error(), "operation key") ||
			strings.Contains(err.Error(), "actor") {
			return report, fmt.Errorf("validation: %w", err)
		}
		return report, fmt.Errorf("completing problem edit refresh: %w", err)
	}
	return report, nil
}

// ApprovePublicRelease records an administrator approval and re-enters the
// ordinary publication gate. It does not bypass quality prerequisites.
func (s *ProblemService) ApprovePublicRelease(
	ctx context.Context,
	id uuid.UUID,
	input PublicReleaseApprovalInput,
) (repository.PublicReleaseApprovalReport, error) {
	if !input.Approved {
		return repository.PublicReleaseApprovalReport{}, fmt.Errorf("validation: explicit approval is required")
	}
	actor := strings.TrimSpace(input.Actor)
	if actor == "" {
		return repository.PublicReleaseApprovalReport{}, fmt.Errorf("validation: approval actor is required")
	}

	report, err := s.problemRepo.ApprovePublicRelease(ctx, id, actor)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return report, ErrNotFound
		}
		if errors.Is(err, repository.ErrReviewQuarantineProtected) {
			return report, ErrProtected
		}
		if errors.Is(err, repository.ErrPublicReleaseApprovalConflict) {
			return report, fmt.Errorf("%w: %s", ErrConflict, err.Error())
		}
		return report, fmt.Errorf("approving public release: %w", err)
	}

	log.Info().
		Str("problem_id", id.String()).
		Str("actor", actor).
		Str("status", string(report.ReleaseStatus)).
		Msg("public release approved")
	return report, nil
}

// DeleteProblem removes a problem and lets the database apply the child-row
// CASCADEs atomically. Review-quarantine evidence uses RESTRICT and therefore
// protects the entire aggregate from deletion.
func (s *ProblemService) DeleteProblem(ctx context.Context, id uuid.UUID) error {
	protected, err := s.problemRepo.HasReviewQuarantine(ctx, id)
	if err != nil {
		return fmt.Errorf("checking problem deletion protection: %w", err)
	}
	if protected {
		return ErrProtected
	}

	if err := s.problemRepo.Delete(ctx, id); err != nil {
		if errors.Is(err, repository.ErrReviewQuarantineProtected) {
			return ErrProtected
		}
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("deleting problem: %w", err)
	}

	log.Info().Str("problem_id", id.String()).Msg("problem deleted")
	return nil
}

// ---------------------------------------------------------------------------
// Workflow operations
// ---------------------------------------------------------------------------

// TriggerGeneration starts a Temporal problem-generation workflow with the
// given parameters. It returns the Temporal workflow run so callers can
// track progress.
func (s *ProblemService) TriggerGeneration(ctx context.Context, params domain.ProblemGenParams) (client.WorkflowRun, error) {
	algoworkflow.StripProblemGenerationReviewRepairStateV1(&params)
	if err := params.Validate(); err != nil {
		return nil, fmt.Errorf("validation: %w", err)
	}

	// Populate per-tag difficulty ranges so activities can include them in prompts.
	if len(params.Tags) > 0 && params.TagRanges == nil {
		params.TagRanges = make(map[string][2]int, len(params.Tags))
		for _, tagName := range params.Tags {
			cat, err := s.tagRepo.GetByTagName(ctx, tagName)
			if err != nil {
				log.Warn().Err(err).Str("tag", tagName).Msg("could not look up tag difficulty range")
				continue
			}
			params.TagRanges[tagName] = [2]int{cat.MinDifficulty, cat.MaxDifficulty}
		}
	}

	workflowID := fmt.Sprintf("problem-gen-%s", uuid.New().String())

	opts := client.StartWorkflowOptions{
		ID:        workflowID,
		TaskQueue: s.taskQueue,
	}

	run, err := s.temporal.ExecuteWorkflow(ctx, opts, "ProblemGenerationWorkflow", params)
	if err != nil {
		return nil, fmt.Errorf("starting workflow: %w", err)
	}

	log.Info().
		Str("workflow_id", run.GetID()).
		Str("run_id", run.GetRunID()).
		Msg("problem generation workflow started")

	return run, nil
}

// TriggerGPLTBatchGeneration starts a GPLTBatchGenerationWorkflow that
// orchestrates one-click generation of 15 天梯赛 problems (8×L1 + 4×L2 + 3×L3)
// via child ProblemGenerationWorkflows. It returns the parent workflow run so
// callers can poll status; the same batch_id is embedded in each generated
// problem's metadata_json for later cross-referencing.
func (s *ProblemService) TriggerGPLTBatchGeneration(ctx context.Context, params domain.GPLTBatchParams) (client.WorkflowRun, error) {
	if err := params.Validate(); err != nil {
		return nil, fmt.Errorf("validation: %w", err)
	}

	if params.BatchID == "" {
		params.BatchID = uuid.New().String()
	}

	workflowID := fmt.Sprintf("gplt-batch-%s", params.BatchID)

	opts := client.StartWorkflowOptions{
		ID:        workflowID,
		TaskQueue: s.taskQueue,
	}

	run, err := s.temporal.ExecuteWorkflow(ctx, opts, "GPLTBatchGenerationWorkflow", params)
	if err != nil {
		return nil, fmt.Errorf("starting GPLT batch workflow: %w", err)
	}

	log.Info().
		Str("workflow_id", run.GetID()).
		Str("run_id", run.GetRunID()).
		Str("batch_id", params.BatchID).
		Msg("GPLT batch generation workflow started")

	return run, nil
}

// ValidateProblem triggers a validation-only run for an existing problem,
// re-running sandbox execution and output comparison.
func (s *ProblemService) ValidateProblem(ctx context.Context, id uuid.UUID) (client.WorkflowRun, error) {
	// Verify the problem exists.
	problem, err := s.problemRepo.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("getting problem: %w", err)
	}

	workflowID := problemValidationWorkflowID(id, problem.UpdatedAt)

	opts := client.StartWorkflowOptions{
		ID:                                       workflowID,
		TaskQueue:                                s.taskQueue,
		WorkflowIDReusePolicy:                    enumspb.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE,
		WorkflowIDConflictPolicy:                 enumspb.WORKFLOW_ID_CONFLICT_POLICY_FAIL,
		WorkflowExecutionErrorWhenAlreadyStarted: true,
	}

	run, err := s.temporal.ExecuteWorkflow(ctx, opts, "ProblemValidationWorkflow", id)
	if err != nil {
		var alreadyStarted *serviceerror.WorkflowExecutionAlreadyStarted
		if errors.As(err, &alreadyStarted) {
			// A second click for the same problem revision must attach to the
			// existing run instead of launching a duplicate sandbox workload.
			if existing := s.temporal.GetWorkflow(ctx, workflowID, ""); existing != nil {
				log.Info().Str("workflow_id", workflowID).Str("problem_id", id.String()).Msg("problem validation already running; reusing existing workflow")
				return existing, nil
			}
		}
		return nil, fmt.Errorf("starting validation workflow: %w", err)
	}

	log.Info().
		Str("workflow_id", run.GetID()).
		Str("problem_id", id.String()).
		Msg("problem validation workflow started")

	return run, nil
}

// problemValidationWorkflowID is stable for one persisted problem revision.
// It prevents duplicate validation runs from repeated UI clicks while a new
// revision naturally receives a new workflow identity.
func problemValidationWorkflowID(id uuid.UUID, updatedAt time.Time) string {
	identity := id.String() + "\x00" + updatedAt.UTC().Format(time.RFC3339Nano)
	digest := sha256.Sum256([]byte(identity))
	return fmt.Sprintf("problem-validate-%s-%s", id.String(), hex.EncodeToString(digest[:8]))
}

// ---------------------------------------------------------------------------
// Metadata & editorial helpers
// ---------------------------------------------------------------------------

// GetMetadata returns the lightweight JSON metadata for a problem.
func (s *ProblemService) GetMetadata(ctx context.Context, id uuid.UUID) (*domain.ProblemMetadata, error) {
	p, err := s.problemRepo.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("getting problem: %w", err)
	}
	meta := p.Metadata()
	return &meta, nil
}

// GetEditorial returns the detailed solution / editorial text for a problem.
func (s *ProblemService) GetEditorial(ctx context.Context, id uuid.UUID) (string, error) {
	p, err := s.problemRepo.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("getting problem: %w", err)
	}
	return domain.NormalizeEditorialMarkdownV1(p.DetailedSolution), nil
}

// GetTestCases returns all test cases for a problem.
func (s *ProblemService) GetTestCases(ctx context.Context, problemID uuid.UUID) ([]*domain.TestCase, error) {
	// Verify the problem exists.
	_, err := s.problemRepo.GetByID(ctx, problemID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("getting problem: %w", err)
	}

	tcs, err := s.testCaseRepo.GetByProblemID(ctx, problemID)
	if err != nil {
		return nil, fmt.Errorf("listing test cases: %w", err)
	}
	return tcs, nil
}

// GetSolutions returns all source artefacts for a problem. Keeping this
// separate from the editorial endpoint prevents callers from confusing the
// human-readable Markdown explanation with the executable standard solution.
func (s *ProblemService) GetSolutions(ctx context.Context, problemID uuid.UUID) ([]*domain.Solution, error) {
	if _, err := s.problemRepo.GetByID(ctx, problemID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("getting problem: %w", err)
	}

	solutions, err := s.problemRepo.ListSolutionsByProblemID(ctx, problemID)
	if err != nil {
		return nil, fmt.Errorf("listing solutions: %w", err)
	}
	return solutions, nil
}

// GetTestCase returns a single test case by ID.
func (s *ProblemService) GetTestCase(ctx context.Context, id uuid.UUID) (*domain.TestCase, error) {
	tc, err := s.testCaseRepo.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("getting test case: %w", err)
	}
	return tc, nil
}

// FindSimilarProblems searches for problems similar to the given embedding
// vector. Returns problems and their similarity scores.
func (s *ProblemService) FindSimilarProblems(
	ctx context.Context,
	embedding []float32,
	limit int,
	threshold float64,
) ([]*domain.Problem, []float64, error) {
	if limit <= 0 {
		limit = 10
	}
	if threshold <= 0 {
		threshold = 0.7
	}

	problems, scores, err := s.vectorRepo.FindSimilar(ctx, embedding, limit, threshold)
	if err != nil {
		return nil, nil, fmt.Errorf("finding similar problems: %w", err)
	}
	return problems, scores, nil
}

// FindSimilarByProblemID finds problems similar to the specified problem by
// first retrieving the problem's embedding and then performing a vector search.
func (s *ProblemService) FindSimilarByProblemID(ctx context.Context, problemID uuid.UUID, limit int) ([]*domain.Problem, []float64, error) {
	if _, err := s.problemRepo.GetByID(ctx, problemID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, ErrNotFound
		}
		return nil, nil, fmt.Errorf("getting problem: %w", err)
	}

	embedding, err := s.vectorRepo.GetEmbedding(ctx, problemID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, fmt.Errorf("problem %s has no embedding", problemID)
		}
		return nil, nil, fmt.Errorf("getting problem embedding: %w", err)
	}
	if len(embedding) == 0 {
		return nil, nil, fmt.Errorf("problem %s has no embedding", problemID)
	}

	if limit <= 0 {
		limit = 10
	}
	problems, scores, err := s.vectorRepo.FindSimilarExcept(ctx, embedding, problemID, limit, 0.7)
	if err != nil {
		return nil, nil, fmt.Errorf("finding similar problems: %w", err)
	}
	return problems, scores, nil
}

// ---------------------------------------------------------------------------
// Sentinel errors
// ---------------------------------------------------------------------------

// ErrNotFound indicates the requested resource does not exist.
var ErrNotFound = errors.New("not found")

// ErrProtected indicates that an immutable governance record prevents deletion.
var ErrProtected = errors.New("protected by immutable governance evidence")

// ErrConflict indicates optimistic concurrency or state-machine conflict.
var ErrConflict = errors.New("conflict")
