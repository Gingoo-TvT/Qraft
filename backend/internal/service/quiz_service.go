package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
	"github.com/Gingoo-TvT/Qraft/backend/internal/workflow/activities"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"go.temporal.io/sdk/client"
)

// QuizService coordinates quiz CRUD and objective-question generation.
type QuizService struct {
	quizRepo           *repository.QuizRepository
	kpRepo             *repository.KnowledgePointRepository
	temporal           client.Client
	taskQueue          string
	llmRuntimeResolver QuizLLMRuntimeResolver
}

// QuizLLMRuntimeResolver resolves the saved statement-model identity for a
// new objective-question workflow. The returned value must contain only a
// secret reference; raw API keys must never be persisted in Temporal input.
type QuizLLMRuntimeResolver func(context.Context) (*domain.LLMRuntimeConfig, error)

func NewQuizService(
	quizRepo *repository.QuizRepository,
	kpRepo *repository.KnowledgePointRepository,
	temporal client.Client,
	taskQueue string,
) *QuizService {
	return &QuizService{
		quizRepo:  quizRepo,
		kpRepo:    kpRepo,
		temporal:  temporal,
		taskQueue: taskQueue,
	}
}

func (s *QuizService) SetLLMRuntimeResolver(resolver QuizLLMRuntimeResolver) {
	if s != nil {
		s.llmRuntimeResolver = resolver
	}
}

type QuizListRequest struct {
	Filter   repository.QuizListFilter
	Page     int
	PageSize int
}

type QuizListResult struct {
	Quizzes  []*domain.QuizProblem
	Total    int
	Page     int
	PageSize int
}

type QuizGenerateRequest struct {
	Subject             string                `json:"subject"`
	Type                domain.QuizType       `json:"type"`
	Difficulty          domain.QuizDifficulty `json:"difficulty"`
	KnowledgePointCodes []string              `json:"knowledge_point_codes"`
	Count               int                   `json:"count"`
	Tags                []string              `json:"tags,omitempty"`
	Visibility          domain.QuizVisibility `json:"visibility"`
	IsVIP               bool                  `json:"is_vip"`
	CustomPrompt        string                `json:"custom_prompt,omitempty"`
}

func (s *QuizService) Create(ctx context.Context, q *domain.QuizProblem) (*domain.QuizProblem, error) {
	if err := normalizeQuizProblem(q); err != nil {
		return nil, err
	}
	if err := s.quizRepo.Create(ctx, q); err != nil {
		return nil, fmt.Errorf("creating quiz: %w", err)
	}
	if len(q.KnowledgePointIDs) > 0 {
		if err := s.quizRepo.LinkKnowledgePoints(ctx, q.ID, q.KnowledgePointIDs); err != nil {
			return nil, fmt.Errorf("linking quiz knowledge points: %w", err)
		}
	}
	return q, nil
}

func (s *QuizService) Get(ctx context.Context, id uuid.UUID) (*domain.QuizProblem, error) {
	q, err := s.quizRepo.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("getting quiz: %w", err)
	}
	return q, nil
}

func (s *QuizService) List(ctx context.Context, req QuizListRequest) (*QuizListResult, error) {
	page := req.Page
	if page < 1 {
		page = 1
	}
	pageSize := req.PageSize
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	items, total, err := s.quizRepo.List(ctx, req.Filter, page, pageSize)
	if err != nil {
		return nil, fmt.Errorf("listing quizzes: %w", err)
	}
	return &QuizListResult{
		Quizzes:  items,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	}, nil
}

func (s *QuizService) Update(ctx context.Context, q *domain.QuizProblem) error {
	if err := normalizeQuizProblem(q); err != nil {
		return err
	}
	if err := s.quizRepo.Update(ctx, q); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("updating quiz: %w", err)
	}
	if q.KnowledgePointIDs != nil {
		if err := s.quizRepo.LinkKnowledgePoints(ctx, q.ID, q.KnowledgePointIDs); err != nil {
			return fmt.Errorf("linking quiz knowledge points: %w", err)
		}
	}
	return nil
}

func (s *QuizService) Delete(ctx context.Context, id uuid.UUID) error {
	if err := s.quizRepo.Delete(ctx, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("deleting quiz: %w", err)
	}
	return nil
}

func (s *QuizService) Generate(ctx context.Context, req QuizGenerateRequest) (workflowID string, err error) {
	if err := validateQuizGenerateRequest(req); err != nil {
		return "", err
	}
	if s.llmRuntimeResolver == nil {
		return "", fmt.Errorf("saved quiz LLM configuration resolver is unavailable")
	}
	llmRuntime, err := s.llmRuntimeResolver(ctx)
	if err != nil {
		return "", fmt.Errorf("resolving saved quiz LLM configuration: %w", err)
	}
	if err := validateQuizLLMRuntime(llmRuntime); err != nil {
		return "", err
	}

	points, err := s.kpRepo.GetByCodes(ctx, req.KnowledgePointCodes)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("resolving knowledge points: %w", err)
	}

	names := make([]string, 0, len(points))
	ids := make([]uuid.UUID, 0, len(points))
	for _, point := range points {
		if point.Subject != req.Subject {
			return "", fmt.Errorf("knowledge point %s does not belong to subject %s", point.Code, req.Subject)
		}
		names = append(names, point.Name)
		ids = append(ids, point.ID)
	}

	workflowID = fmt.Sprintf("quiz-gen-%s", uuid.New().String())
	input := activities.QuizGenerateInput{
		Subject:             strings.TrimSpace(req.Subject),
		Type:                req.Type,
		Difficulty:          req.Difficulty,
		KnowledgePointCodes: cleanStrings(req.KnowledgePointCodes),
		KnowledgePointNames: names,
		KnowledgePointIDs:   ids,
		Count:               req.Count,
		Tags:                cleanStrings(req.Tags),
		Visibility:          req.Visibility,
		IsVIP:               req.IsVIP,
		CustomPrompt:        strings.TrimSpace(req.CustomPrompt),
		LLMRuntime:          llmRuntime,
	}

	run, err := s.temporal.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        workflowID,
		TaskQueue: s.taskQueue,
	}, "QuizGenerationWorkflow", input)
	if err != nil {
		return "", fmt.Errorf("starting quiz generation workflow: %w", err)
	}

	log.Info().
		Str("workflow_id", run.GetID()).
		Str("run_id", run.GetRunID()).
		Int("count", req.Count).
		Msg("quiz generation workflow started")

	return run.GetID(), nil
}

func validateQuizLLMRuntime(runtime *domain.LLMRuntimeConfig) error {
	if runtime == nil {
		return fmt.Errorf("saved statement LLM configuration is required for quiz generation")
	}
	if err := runtime.Validate("quiz_llm_runtime"); err != nil {
		return fmt.Errorf("invalid saved quiz LLM configuration: %w", err)
	}
	if strings.TrimSpace(runtime.Model) == "" {
		return fmt.Errorf("saved quiz LLM model is required")
	}
	if strings.TrimSpace(runtime.BaseURL) == "" {
		return fmt.Errorf("saved quiz LLM Base URL is required")
	}
	if strings.TrimSpace(runtime.APIKeyRef) == "" {
		return fmt.Errorf("saved quiz LLM API key is required")
	}
	return nil
}

func normalizeQuizProblem(q *domain.QuizProblem) error {
	if q == nil {
		return fmt.Errorf("quiz is required")
	}
	q.Code = strings.TrimSpace(q.Code)
	q.Title = strings.TrimSpace(q.Title)
	q.Statement = strings.TrimSpace(q.Statement)
	q.Subject = strings.TrimSpace(q.Subject)
	q.CodeHint = strings.TrimSpace(q.CodeHint)
	q.Explanation = strings.TrimSpace(q.Explanation)
	q.Tags = cleanStrings(q.Tags)
	q.Answers = cleanStrings(q.Answers)
	for i := range q.Options {
		q.Options[i].Label = strings.ToUpper(strings.TrimSpace(q.Options[i].Label))
		q.Options[i].Content = strings.TrimSpace(q.Options[i].Content)
	}
	if q.ID == uuid.Nil {
		q.ID = uuid.New()
	}
	if q.Code == "" {
		return fmt.Errorf("quiz code is required")
	}
	if q.Statement == "" {
		return fmt.Errorf("quiz statement is required")
	}
	if q.Subject == "" {
		return fmt.Errorf("quiz subject is required")
	}
	if !q.Type.IsValid() {
		return fmt.Errorf("invalid quiz type: %s", q.Type)
	}
	if !q.Difficulty.IsValid() {
		return fmt.Errorf("invalid quiz difficulty: %s", q.Difficulty)
	}
	if !q.Visibility.IsValid() {
		return fmt.Errorf("invalid quiz visibility: %s", q.Visibility)
	}
	return nil
}

func validateQuizGenerateRequest(req QuizGenerateRequest) error {
	if req.Type == domain.QuizTypeProgramming {
		return fmt.Errorf("quiz generation does not support programming type")
	}
	if req.Type != domain.QuizTypeChoice && req.Type != domain.QuizTypeJudge && req.Type != domain.QuizTypeFillBlank {
		return fmt.Errorf("invalid quiz type: %s", req.Type)
	}
	if req.Count < 1 || req.Count > QuizGenerateCountMax {
		return fmt.Errorf("count must be between 1 and %d", QuizGenerateCountMax)
	}
	if strings.TrimSpace(req.Subject) == "" {
		return fmt.Errorf("subject is required")
	}
	if !req.Difficulty.IsValid() {
		return fmt.Errorf("invalid quiz difficulty: %s", req.Difficulty)
	}
	if !req.Visibility.IsValid() {
		return fmt.Errorf("invalid quiz visibility: %s", req.Visibility)
	}
	if len(cleanStrings(req.KnowledgePointCodes)) == 0 {
		return fmt.Errorf("knowledge_point_codes is required")
	}
	return nil
}

func cleanStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
