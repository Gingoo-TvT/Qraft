package service

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/llm"
	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
	"github.com/google/uuid"
)

const (
	problemSetPromptMaxChars = 12000
	problemSetPromptRetries  = 2
	problemSetDefaultMin     = 10
	problemSetDefaultMax     = 20
)

type problemSetStore interface {
	Create(context.Context, *domain.ProblemSet) error
	GetByID(context.Context, uuid.UUID) (*domain.ProblemSet, error)
	List(context.Context, repository.ProblemSetListFilter) ([]*domain.ProblemSet, int, error)
	Update(context.Context, *domain.ProblemSet) error
	UpdateGeneratedPrompt(context.Context, uuid.UUID, string, string, time.Time) error
	UpdateStatusAndScore(context.Context, uuid.UUID, domain.ProblemSetStatus, int) error
	Delete(context.Context, uuid.UUID) error
	ListItems(context.Context, uuid.UUID) ([]domain.ProblemSetItem, error)
	AddItem(context.Context, *domain.ProblemSetItem) error
	RemoveItem(context.Context, uuid.UUID, uuid.UUID) error
	ReorderItems(context.Context, uuid.UUID, []uuid.UUID) error
	CreateLedgerEntry(context.Context, *domain.ProblemSetLedgerEntry) error
	ListRecentLedger(context.Context, int) ([]domain.ProblemSetLedgerEntry, error)
}

type problemSetProblemReader interface {
	GetProblem(context.Context, uuid.UUID) (*domain.Problem, error)
}

type problemSetQuizReader interface {
	Get(context.Context, uuid.UUID) (*domain.QuizProblem, error)
}

type problemSetPackageBuilder interface {
	BuildProblemPackage(context.Context, uuid.UUID) (*HydroPackage, error)
}

type problemSetLLM interface {
	CompleteWithRetry(context.Context, *llm.Request, int) (*llm.Response, error)
}

// ProblemSetLLMRuntimeResolver resolves the durable statement-model identity.
// It must return a runtime reference, never a raw API key.
type ProblemSetLLMRuntimeResolver func(context.Context) (*domain.LLMRuntimeConfig, error)

type ProblemSetService struct {
	repo       problemSetStore
	problems   problemSetProblemReader
	quizzes    problemSetQuizReader
	packageSvc problemSetPackageBuilder
	llm        problemSetLLM
	resolveLLM ProblemSetLLMRuntimeResolver
}

func NewProblemSetService(
	repo *repository.ProblemSetRepository,
	problems *ProblemService,
	quizzes *QuizService,
	packageSvc *HydroExportService,
	llmClient *llm.Client,
) *ProblemSetService {
	return NewProblemSetServiceWithDeps(repo, problems, quizzes, packageSvc, llmClient)
}

func NewProblemSetServiceWithDeps(
	repo problemSetStore,
	problems problemSetProblemReader,
	quizzes problemSetQuizReader,
	packageSvc problemSetPackageBuilder,
	llmClient problemSetLLM,
) *ProblemSetService {
	return &ProblemSetService{repo: repo, problems: problems, quizzes: quizzes, packageSvc: packageSvc, llm: llmClient}
}

func (s *ProblemSetService) SetLLMRuntimeResolver(resolver ProblemSetLLMRuntimeResolver) {
	if s != nil {
		s.resolveLLM = resolver
	}
}

type ProblemSetCreateRequest struct {
	StartGeneration  bool                               `json:"start_generation"`
	Code             string                             `json:"code"`
	Title            string                             `json:"title"`
	Description      string                             `json:"description"`
	Kind             domain.ProblemSetKind              `json:"kind"`
	Visibility       domain.ProblemSetVisibility        `json:"visibility"`
	Subject          string                             `json:"subject"`
	Tags             []string                           `json:"tags"`
	StylePrompt      string                             `json:"style_prompt"`
	DifficultyPrompt string                             `json:"difficulty_prompt"`
	DesiredItemCount int                                `json:"desired_item_count"`
	MinItemCount     int                                `json:"min_item_count"`
	MaxItemCount     int                                `json:"max_item_count"`
	CooldownSets     int                                `json:"cooldown_sets"`
	GenerationConfig *domain.ProblemSetGenerationConfig `json:"generation_config,omitempty"`
	GeneratePrompt   bool                               `json:"generate_prompt"`
	CreatedBy        string                             `json:"created_by,omitempty"`
}

type ProblemSetUpdateRequest struct {
	Code             *string                            `json:"code,omitempty"`
	Title            *string                            `json:"title,omitempty"`
	Description      *string                            `json:"description,omitempty"`
	Kind             *domain.ProblemSetKind             `json:"kind,omitempty"`
	Visibility       *domain.ProblemSetVisibility       `json:"visibility,omitempty"`
	Subject          *string                            `json:"subject,omitempty"`
	Tags             *[]string                          `json:"tags,omitempty"`
	StylePrompt      *string                            `json:"style_prompt,omitempty"`
	DifficultyPrompt *string                            `json:"difficulty_prompt,omitempty"`
	DesiredItemCount *int                               `json:"desired_item_count,omitempty"`
	MinItemCount     *int                               `json:"min_item_count,omitempty"`
	MaxItemCount     *int                               `json:"max_item_count,omitempty"`
	CooldownSets     *int                               `json:"cooldown_sets,omitempty"`
	GenerationConfig *domain.ProblemSetGenerationConfig `json:"generation_config,omitempty"`
	GeneratePrompt   bool                               `json:"generate_prompt"`
}

type ProblemSetAddItemRequest struct {
	ProblemID *uuid.UUID `json:"problem_id,omitempty"`
	QuizID    *uuid.UUID `json:"quiz_id,omitempty"`
	Position  int        `json:"position,omitempty"`
	Score     int        `json:"score,omitempty"`
	Section   string     `json:"section,omitempty"`
	Notes     string     `json:"notes,omitempty"`
}

type ProblemSetReorderRequest struct {
	ItemIDs []uuid.UUID `json:"item_ids"`
}

type ProblemSetExportResult struct {
	Package *ProblemSetPackage            `json:"-"`
	Quality *domain.ProblemSetQuality     `json:"quality"`
	Ledger  *domain.ProblemSetLedgerEntry `json:"ledger,omitempty"`
}

func (s *ProblemSetService) Create(ctx context.Context, req ProblemSetCreateRequest) (*domain.ProblemSet, error) {
	if s == nil || s.repo == nil {
		return nil, fmt.Errorf("problem set service is unavailable")
	}
	id := uuid.New()
	code := strings.TrimSpace(req.Code)
	if code == "" {
		code = "SET-" + strings.ToUpper(id.String()[:8])
	}
	set := &domain.ProblemSet{
		ID: id, Code: code, Title: req.Title, Description: req.Description, GenerationConfig: req.GenerationConfig,
		Kind: req.Kind, Visibility: req.Visibility, Subject: req.Subject, Tags: req.Tags,
		StylePrompt: req.StylePrompt, DifficultyPrompt: req.DifficultyPrompt,
		DesiredItemCount: req.DesiredItemCount, MinItemCount: req.MinItemCount,
		MaxItemCount: req.MaxItemCount, CooldownSets: req.CooldownSets,
		CreatedBy: strings.TrimSpace(req.CreatedBy), Status: domain.ProblemSetStatusDraft,
	}
	if set.Kind == "" {
		set.Kind = domain.ProblemSetKindContest
	}
	if set.Visibility == "" {
		set.Visibility = domain.ProblemSetVisibilityPrivate
	}
	if set.CreatedBy == "" {
		set.CreatedBy = "api"
	}
	if err := set.NormalizeProblemSet(); err != nil {
		return nil, fmt.Errorf("validation: %w", err)
	}
	if err := set.GenerationConfig.Validate(set.DesiredItemCount); err != nil {
		return nil, fmt.Errorf("validation: %w", err)
	}
	if err := s.repo.Create(ctx, set); err != nil {
		return nil, err
	}
	if req.GeneratePrompt {
		if _, err := s.GeneratePrompt(ctx, set.ID); err != nil {
			return set, err
		}
	}
	return s.Get(ctx, set.ID)
}

func (s *ProblemSetService) Get(ctx context.Context, id uuid.UUID) (*domain.ProblemSet, error) {
	if s == nil || s.repo == nil {
		return nil, fmt.Errorf("problem set service is unavailable")
	}
	set, err := s.repo.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if err := s.hydrateItems(ctx, set); err != nil {
		return nil, err
	}
	quality, err := s.quality(ctx, set, false)
	if err != nil {
		return nil, err
	}
	set.Quality = quality
	return set, nil
}

func (s *ProblemSetService) List(ctx context.Context, filter repository.ProblemSetListFilter) ([]*domain.ProblemSet, int, error) {
	if s == nil || s.repo == nil {
		return nil, 0, fmt.Errorf("problem set service is unavailable")
	}
	return s.repo.List(ctx, filter)
}

func (s *ProblemSetService) Update(ctx context.Context, id uuid.UUID, req ProblemSetUpdateRequest) (*domain.ProblemSet, error) {
	set, err := s.repo.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if set.Generation.Active() {
		return nil, fmt.Errorf("%w: %s", ErrConflict, repository.ErrProblemSetGenerationActive)
	}
	if req.GenerationConfig != nil {
		set.GenerationConfig = req.GenerationConfig
	}
	if req.Code != nil {
		set.Code = *req.Code
	}
	if req.Title != nil {
		set.Title = *req.Title
	}
	if req.Description != nil {
		set.Description = *req.Description
	}
	if req.Kind != nil {
		set.Kind = *req.Kind
	}
	if req.Visibility != nil {
		set.Visibility = *req.Visibility
	}
	if req.Subject != nil {
		set.Subject = *req.Subject
	}
	if req.Tags != nil {
		set.Tags = *req.Tags
	}
	if req.StylePrompt != nil {
		set.StylePrompt = *req.StylePrompt
	}
	if req.DifficultyPrompt != nil {
		set.DifficultyPrompt = *req.DifficultyPrompt
	}
	desiredCountChanged := req.DesiredItemCount != nil
	if req.DesiredItemCount != nil {
		set.DesiredItemCount = *req.DesiredItemCount
	}
	if req.MinItemCount != nil {
		set.MinItemCount = *req.MinItemCount
	}
	if req.MaxItemCount != nil {
		set.MaxItemCount = *req.MaxItemCount
	}
	// The current API exposes one direct item-count field. When it is edited
	// without legacy range fields, collapse the compatibility bounds as well.
	if desiredCountChanged && req.MinItemCount == nil && req.MaxItemCount == nil &&
		set.DesiredItemCount > 0 {
		set.MinItemCount = set.DesiredItemCount
		set.MaxItemCount = set.DesiredItemCount
	}
	if req.CooldownSets != nil {
		set.CooldownSets = *req.CooldownSets
	}
	set.Status = domain.ProblemSetStatusDraft
	if err := set.NormalizeProblemSet(); err != nil {
		return nil, fmt.Errorf("validation: %w", err)
	}
	if err := set.GenerationConfig.Validate(set.DesiredItemCount); err != nil {
		return nil, fmt.Errorf("validation: %w", err)
	}
	if err := s.repo.Update(ctx, set); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	promptInputsChanged := req.StylePrompt != nil || req.DifficultyPrompt != nil ||
		req.Tags != nil || req.Subject != nil || req.DesiredItemCount != nil ||
		req.MinItemCount != nil || req.MaxItemCount != nil || req.GenerationConfig != nil || req.Title != nil || req.Description != nil || req.CooldownSets != nil
	// Style/difficulty changes invalidate the previous LLM brief. The database
	// update intentionally clears it so stale instructions cannot be exported.
	if promptInputsChanged {
		if err := s.repo.UpdateGeneratedPrompt(ctx, id, "", "", time.Time{}); err != nil {
			return nil, err
		}
	}
	// An explicit generate_prompt=true always means regenerate, even when the
	// caller only changed the title or is using the button as a manual refresh.
	if req.GeneratePrompt {
		if _, err := s.GeneratePrompt(ctx, id); err != nil {
			return nil, err
		}
	}
	return s.Get(ctx, id)
}

func (s *ProblemSetService) Delete(ctx context.Context, id uuid.UUID) error {
	if err := s.repo.Delete(ctx, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	return nil
}

func (s *ProblemSetService) AddItem(ctx context.Context, setID uuid.UUID, req ProblemSetAddItemRequest) (*domain.ProblemSet, error) {
	if (req.ProblemID == nil) == (req.QuizID == nil) {
		return nil, fmt.Errorf("validation: exactly one of problem_id or quiz_id is required")
	}
	set, err := s.repo.GetByID(ctx, setID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if req.Score < 0 {
		return nil, fmt.Errorf("validation: score must not be negative")
	}
	if req.ProblemID != nil && s.problems != nil {
		if _, err := s.problems.GetProblem(ctx, *req.ProblemID); err != nil {
			return nil, err
		}
	}
	if req.QuizID != nil && s.quizzes != nil {
		if _, err := s.quizzes.Get(ctx, *req.QuizID); err != nil {
			return nil, err
		}
	}
	item := &domain.ProblemSetItem{SetID: setID, ProblemID: req.ProblemID, QuizID: req.QuizID,
		Position: req.Position, Score: req.Score, Section: strings.TrimSpace(req.Section), Notes: strings.TrimSpace(req.Notes)}
	if err := s.repo.AddItem(ctx, item); err != nil {
		return nil, mapProblemSetPersistenceError(err)
	}
	if err := s.syncScore(ctx, set, setID); err != nil {
		return nil, err
	}
	return s.Get(ctx, setID)
}

func (s *ProblemSetService) RemoveItem(ctx context.Context, setID, itemID uuid.UUID) (*domain.ProblemSet, error) {
	if err := s.repo.RemoveItem(ctx, setID, itemID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	set, err := s.repo.GetByID(ctx, setID)
	if err != nil {
		return nil, err
	}
	if err := s.syncScore(ctx, set, setID); err != nil {
		return nil, err
	}
	return s.Get(ctx, setID)
}

func (s *ProblemSetService) Reorder(ctx context.Context, setID uuid.UUID, itemIDs []uuid.UUID) (*domain.ProblemSet, error) {
	if len(itemIDs) == 0 {
		return nil, fmt.Errorf("validation: item_ids must not be empty")
	}
	if err := s.repo.ReorderItems(ctx, setID, itemIDs); err != nil {
		return nil, mapProblemSetPersistenceError(err)
	}
	return s.Get(ctx, setID)
}

func (s *ProblemSetService) GeneratePrompt(ctx context.Context, id uuid.UUID) (*domain.ProblemSet, error) {
	if s == nil || s.llm == nil || s.resolveLLM == nil {
		return nil, fmt.Errorf("LLM problem-set prompt generation is not configured")
	}
	set, err := s.repo.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if set.Generation.Active() {
		return nil, repository.ErrProblemSetGenerationActive
	}
	// When a set already contains items, resolve their current metadata so the
	// generated brief can identify coverage gaps instead of seeing UUIDs only.
	if len(set.Items) > 0 {
		if err := s.hydrateItems(ctx, set); err != nil {
			return nil, fmt.Errorf("resolving selected problem-set items: %w", err)
		}
	}
	runtime, err := s.resolveLLM(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolving saved statement LLM configuration: %w", err)
	}
	if runtime == nil || strings.TrimSpace(runtime.Model) == "" {
		return nil, fmt.Errorf("saved statement LLM model is required")
	}
	request := &llm.Request{
		Model: runtime.Model, MaxTokens: 1800, Temperature: floatPtr(0.2),
		System:   problemSetPromptSystem,
		Messages: []llm.Message{{Role: "user", Content: s.buildPromptInput(ctx, set)}},
		Runtime:  &llm.RuntimeConfig{APIKeyRef: strings.TrimSpace(runtime.APIKeyRef), BaseURL: strings.TrimSpace(runtime.BaseURL), Provider: strings.TrimSpace(runtime.Provider), Protocol: strings.TrimSpace(runtime.Protocol)},
	}
	response, err := s.llm.CompleteWithRetry(ctx, request, problemSetPromptRetries)
	if err != nil {
		return nil, fmt.Errorf("generating problem-set prompt: %w", err)
	}
	if response == nil || strings.TrimSpace(response.Text()) == "" {
		return nil, fmt.Errorf("generating problem-set prompt returned empty content")
	}
	prompt := cleanPrompt(response.Text())
	if prompt == "" {
		return nil, fmt.Errorf("generating problem-set prompt returned empty content")
	}
	now := time.Now().UTC()
	if err := s.repo.UpdateGeneratedPrompt(ctx, id, prompt, response.Model, now); err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

func (s *ProblemSetService) Quality(ctx context.Context, id uuid.UUID) (*domain.ProblemSetQuality, error) {
	set, err := s.repo.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if err := s.hydrateItems(ctx, set); err != nil {
		return nil, err
	}
	return s.quality(ctx, set, false)
}

func (s *ProblemSetService) Export(ctx context.Context, id uuid.UUID, allowReuse bool) (*ProblemSetExportResult, error) {
	if s == nil || s.repo == nil {
		return nil, fmt.Errorf("problem set service is unavailable")
	}
	set, err := s.repo.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if err := s.hydrateItems(ctx, set); err != nil {
		return nil, err
	}
	quality, err := s.quality(ctx, set, allowReuse)
	if err != nil {
		return nil, err
	}
	if !quality.Valid {
		return nil, fmt.Errorf("validation: %s", strings.Join(quality.BlockingIssues, "; "))
	}
	if len(quality.ReusedKnowledgePoints) > 0 || len(quality.ReusedItems) > 0 {
		if !allowReuse {
			return nil, fmt.Errorf("%w: recent problem sets reuse knowledge points or items; pass allow_reuse=true only for an intentional exception", ErrConflict)
		}
	}
	pkg, err := buildProblemSetPackage(ctx, set, s.packageSvc)
	if err != nil {
		return nil, err
	}
	entry := &domain.ProblemSetLedgerEntry{SetID: set.ID, EventType: "exported", SnapshotSHA256: problemSetSnapshotHash(set), KnowledgePointKeys: qualityKeys(set), ItemFingerprints: itemFingerprints(set), OverlapReport: map[string]interface{}{"quality": quality, "allow_reuse": allowReuse}}
	if err := s.repo.CreateLedgerEntry(ctx, entry); err != nil {
		return nil, err
	}
	if err := s.repo.UpdateStatusAndScore(ctx, set.ID, domain.ProblemSetStatusExported, sumItemScores(set.Items)); err != nil {
		return nil, err
	}
	return &ProblemSetExportResult{Package: pkg, Quality: quality, Ledger: entry}, nil
}

func (s *ProblemSetService) hydrateItems(ctx context.Context, set *domain.ProblemSet) error {
	for index := range set.Items {
		item := &set.Items[index]
		switch {
		case item.ProblemID != nil:
			if s.problems == nil {
				return fmt.Errorf("problem source is unavailable")
			}
			problem, err := s.problems.GetProblem(ctx, *item.ProblemID)
			if err != nil {
				if errors.Is(err, ErrNotFound) {
					return ErrNotFound
				}
				return err
			}
			item.Problem = problem
		case item.QuizID != nil:
			if s.quizzes == nil {
				return fmt.Errorf("quiz source is unavailable")
			}
			quiz, err := s.quizzes.Get(ctx, *item.QuizID)
			if err != nil {
				if errors.Is(err, ErrNotFound) {
					return ErrNotFound
				}
				return err
			}
			item.Quiz = quiz
		default:
			return fmt.Errorf("validation: item %s has neither problem_id nor quiz_id", item.ID)
		}
		item.KnowledgePointKeys = itemKnowledgeKeys(*item)
		item.Fingerprint = itemSourceFingerprint(*item)
	}
	return nil
}

func (s *ProblemSetService) quality(ctx context.Context, set *domain.ProblemSet, allowReuse bool) (*domain.ProblemSetQuality, error) {
	q := &domain.ProblemSetQuality{Valid: true, ReadyForExport: true, ItemCount: len(set.Items), RecommendedItemCount: recommendProblemSetItemCount(set), GeneratedAt: time.Now().UTC()}
	keySet := make(map[string]struct{})
	for _, item := range set.Items {
		for _, key := range item.KnowledgePointKeys {
			keySet[key] = struct{}{}
		}
	}
	q.KnowledgePointCount = len(keySet)
	if len(set.Items) == 0 {
		q.Valid = false
		q.ReadyForExport = false
		q.BlockingIssues = append(q.BlockingIssues, "problem set must contain at least one item")
	}
	if len(set.Items) > 0 {
		if problemSetHasExactItemCount(set) {
			if len(set.Items) != set.DesiredItemCount {
				q.Valid = false
				q.ReadyForExport = false
				q.BlockingIssues = append(q.BlockingIssues, fmt.Sprintf("当前 %d 道题，必须恰好编排 %d 道才能导出", len(set.Items), set.DesiredItemCount))
			}
		} else if len(set.Items) < set.MinItemCount || len(set.Items) > set.MaxItemCount {
			q.Valid = false
			q.ReadyForExport = false
			q.BlockingIssues = append(q.BlockingIssues, fmt.Sprintf("当前 %d 道题，旧记录要求在 %d-%d 道之间才能导出", len(set.Items), set.MinItemCount, set.MaxItemCount))
		}
	}
	if set.GenerationConfig != nil {
		counts := map[domain.QuizType]int{}
		for _, item := range set.Items {
			if item.ProblemID != nil {
				counts[domain.QuizTypeProgramming]++
			} else if item.Quiz != nil {
				counts[item.Quiz.Type]++
			}
		}
		for _, quota := range set.GenerationConfig.Distribution {
			if counts[quota.Type] != quota.Count {
				q.Valid = false
				q.ReadyForExport = false
				q.BlockingIssues = append(q.BlockingIssues, fmt.Sprintf("%s 当前 %d 道，要求 %d 道", quota.Type.ToExcel(), counts[quota.Type], quota.Count))
			}
			delete(counts, quota.Type)
		}
		for typ, count := range counts {
			if count > 0 {
				q.Valid = false
				q.ReadyForExport = false
				q.BlockingIssues = append(q.BlockingIssues, fmt.Sprintf("存在未要求的题型：%s", typ.ToExcel()))
			}
		}
	}
	if set.Generation.Active() {
		q.Valid = false
		q.ReadyForExport = false
		q.BlockingIssues = append(q.BlockingIssues, "题集仍在自动生成")
	}
	if set.CooldownSets > 0 {
		recent, err := s.repo.ListRecentLedger(ctx, set.CooldownSets)
		if err != nil {
			return nil, err
		}
		currentKeys := keySet
		currentItems := make(map[string]struct{})
		for _, item := range set.Items {
			currentItems[item.Fingerprint] = struct{}{}
		}
		for _, entry := range recent {
			if entry.SetID == set.ID {
				continue
			}
			q.OverlapSetIDs = append(q.OverlapSetIDs, entry.SetID.String())
			for _, key := range entry.KnowledgePointKeys {
				if _, ok := currentKeys[key]; ok {
					q.ReusedKnowledgePoints = append(q.ReusedKnowledgePoints, key)
				}
			}
			for _, fp := range entry.ItemFingerprints {
				if _, ok := currentItems[fp]; ok {
					q.ReusedItems = append(q.ReusedItems, fp)
				}
			}
		}
	}
	q.OverlapSetIDs = uniqueSorted(q.OverlapSetIDs)
	q.ReusedKnowledgePoints = uniqueSorted(q.ReusedKnowledgePoints)
	q.ReusedItems = uniqueSorted(q.ReusedItems)
	if len(q.ReusedKnowledgePoints) > 0 || len(q.ReusedItems) > 0 {
		if allowReuse {
			q.Warnings = append(q.Warnings, "已明确允许复用近期知识点/题目")
		} else {
			q.ReadyForExport = false
		}
	}
	if len(q.BlockingIssues) > 0 {
		q.Valid = false
	}
	return q, nil
}

func (s *ProblemSetService) syncScore(ctx context.Context, set *domain.ProblemSet, id uuid.UUID) error {
	items, err := s.repo.ListItems(ctx, id)
	if err != nil {
		return err
	}
	return s.repo.UpdateStatusAndScore(ctx, set.ID, domain.ProblemSetStatusDraft, sumItemScores(items))
}

func (s *ProblemSetService) buildPromptInput(ctx context.Context, set *domain.ProblemSet) string {
	recentSummary := "暂无近期比赛台账"
	if set.CooldownSets > 0 {
		if entries, err := s.repo.ListRecentLedger(ctx, set.CooldownSets); err == nil && len(entries) > 0 {
			parts := make([]string, 0, len(entries))
			for _, entry := range entries {
				parts = append(parts, fmt.Sprintf("set=%s kp=%s", entry.SetID, strings.Join(entry.KnowledgePointKeys, ",")))
			}
			recentSummary = strings.Join(parts, "\n")
		}
	}
	selectedSummary := "尚未编排题目；请根据覆盖轴设计候选题位"
	if len(set.Items) > 0 {
		lines := make([]string, 0, len(set.Items))
		for index, item := range set.Items {
			title := "未解析来源"
			source := ""
			switch {
			case item.Problem != nil:
				title = item.Problem.Title
				source = fmt.Sprintf("编程题 %s", item.Problem.SerialNumber)
			case item.Quiz != nil:
				title = item.Quiz.Title
				source = fmt.Sprintf("客观题 %s", item.Quiz.Code)
			}
			keys := strings.Join(item.KnowledgePointKeys, ",")
			lines = append(lines, fmt.Sprintf("%d. %s | %s | 知识点=%s", index+1, source, title, keys))
		}
		selectedSummary = strings.Join(lines, "\n")
	}
	configText := ""
	if set.GenerationConfig != nil {
		raw, _ := json.Marshal(set.GenerationConfig)
		configText = "\n题型配额与整套需求（严格遵守）：" + string(raw)
	}
	return configText + "\n" + fmt.Sprintf("题集：%s\n类型：%s\n学科：%s\n标签：%s\n风格要求：%s\n自然语言难度要求：%s\n%s\n当前已编排题目（仅供内部规划）：\n%s\n近 %d 场台账（不得重复知识点与题目，除非确有必要并说明理由）：\n%s",
		set.Title, set.Kind, set.Subject, strings.Join(set.Tags, ","), set.StylePrompt, set.DifficultyPrompt,
		itemCountPromptText(set), selectedSummary, set.CooldownSets, recentSummary)
}

const problemSetPromptSystem = `你是 Qraft 的资深竞赛出题负责人。根据用户给出的题集要求，输出一份可直接交给出题工作流使用的中文提示词，不要输出解释、评价或 JSON。必须明确：题集风格、难度梯度、用户直接填写的准确题数、每题应覆盖的不同知识点、与近期台账去重、编程题的最大规模随机数据和边界数据覆盖；如果已有编排题目，指出应补齐的覆盖缺口。这里只写整套覆盖与分区策略、各区难度和约束，后续编排会分批细化题位；大题集不要在这里逐一罗列所有题目。题数是题集层面的固定数量，不要把“10-20”当成题数选择范围；只有编程题内部的测试点数量由生题流程按 corner case 自适应选择 10-20 个。若有题型配额，必须分别规划编程、选择、填空、判断题，客观题需明确考查目标、答案唯一性或多选规则、干扰项依据；不得将客观题误写成需要标准输入输出的编程题。严禁把“提示”写进参赛者可见题面；提示只供内部出题工作流使用。不得虚构已经存在的题目或引用台账中未提供的事实。`

func cleanPrompt(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "```text")
	value = strings.TrimPrefix(value, "```markdown")
	value = strings.TrimSuffix(value, "```")
	value = strings.TrimSpace(value)
	if len([]rune(value)) > problemSetPromptMaxChars {
		value = string([]rune(value)[:problemSetPromptMaxChars])
	}
	return value
}

func floatPtr(value float64) *float64 { return &value }

func desiredCountText(set *domain.ProblemSet) string {
	if set.DesiredItemCount > 0 {
		return strconv.Itoa(set.DesiredItemCount)
	}
	return "未指定"
}

func problemSetHasExactItemCount(set *domain.ProblemSet) bool {
	return set != nil && set.DesiredItemCount > 0 &&
		set.MinItemCount == set.DesiredItemCount &&
		set.MaxItemCount == set.DesiredItemCount
}

func itemCountPromptText(set *domain.ProblemSet) string {
	if problemSetHasExactItemCount(set) {
		return fmt.Sprintf("题数：%d（必须恰好编排 %d 道）", set.DesiredItemCount, set.DesiredItemCount)
	}
	return fmt.Sprintf("题数：%s（兼容旧记录范围 %d-%d）",
		desiredCountText(set), set.MinItemCount, set.MaxItemCount)
}

var problemSetDigitsRE = regexp.MustCompile(`[0-9]+`)

func problemSetOJCode(problem *domain.Problem, id *uuid.UUID) string {
	if problem != nil {
		if match := problemSetDigitsRE.FindString(problem.SerialNumber); match != "" {
			n, err := strconv.Atoi(match)
			if err == nil {
				if n < 1000 {
					n += 1000
				}
				return fmt.Sprintf("C%d", n)
			}
		}
	}
	seed := "problem-set"
	if id != nil {
		seed = id.String()
	}
	digest := sha256.Sum256([]byte(seed))
	n := int(digest[0])<<16 | int(digest[1])<<8 | int(digest[2])
	return fmt.Sprintf("C%d", 1000+n%899000)
}

func problemDifficultyToExcel(value int) string {
	switch {
	case value < 1000:
		return "入门"
	case value < 1400:
		return "简单"
	case value < 2000:
		return "困难"
	case value < 2600:
		return "挑战"
	default:
		return "地狱"
	}
}

func safeSetFilename(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "problem-set"
	}
	var b strings.Builder
	for _, r := range value {
		if r == '-' || r == '_' || r >= '0' && r <= '9' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "problem-set"
	}
	return b.String()
}

func itemSourceKeys(item domain.ProblemSetItem) []string {
	keys := make([]string, 0)
	if item.Problem != nil {
		if subject := strings.TrimSpace(string(item.Problem.Level)); subject != "" {
			keys = append(keys, "level:"+strings.ToLower(subject))
		}
		for _, tag := range item.Problem.Tags {
			if tag = strings.TrimSpace(strings.ToLower(tag)); tag != "" {
				keys = append(keys, "tag:"+tag)
			}
		}
		if item.Problem.ID != uuid.Nil {
			keys = append(keys, "problem:"+item.Problem.ID.String())
		}
	}
	if item.Quiz != nil {
		if item.Quiz.Subject != "" {
			keys = append(keys, "subject:"+strings.ToLower(strings.TrimSpace(item.Quiz.Subject)))
		}
		for _, tag := range item.Quiz.Tags {
			if tag = strings.TrimSpace(strings.ToLower(tag)); tag != "" {
				keys = append(keys, "tag:"+tag)
			}
		}
		for _, kp := range item.Quiz.KnowledgePointIDs {
			keys = append(keys, "kp:"+kp.String())
		}
		if item.Quiz.ID != uuid.Nil {
			keys = append(keys, "quiz:"+item.Quiz.ID.String())
		}
	}
	return uniqueSorted(keys)
}

// itemKnowledgeKeys deliberately excludes source IDs. IDs are used by the
// fingerprint below to catch exact item reuse, while the ledger's knowledge
// coverage should count only actual level/tag/knowledge-point axes.
func itemKnowledgeKeys(item domain.ProblemSetItem) []string {
	keys := itemSourceKeys(item)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		if strings.HasPrefix(key, "problem:") || strings.HasPrefix(key, "quiz:") {
			continue
		}
		out = append(out, key)
	}
	return out
}

func itemSourceFingerprint(item domain.ProblemSetItem) string {
	parts := itemSourceKeys(item)
	hash := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(hash[:])
}

func qualityKeys(set *domain.ProblemSet) []string {
	keys := make([]string, 0)
	for _, item := range set.Items {
		keys = append(keys, item.KnowledgePointKeys...)
	}
	return uniqueSorted(keys)
}

func itemFingerprints(set *domain.ProblemSet) []string {
	values := make([]string, 0, len(set.Items))
	for _, item := range set.Items {
		if item.Fingerprint != "" {
			values = append(values, item.Fingerprint)
		}
	}
	return uniqueSorted(values)
}

func problemSetSnapshotHash(set *domain.ProblemSet) string {
	type snapshotItem struct {
		Position  int        `json:"position"`
		ProblemID *uuid.UUID `json:"problem_id,omitempty"`
		QuizID    *uuid.UUID `json:"quiz_id,omitempty"`
		Score     int        `json:"score"`
	}
	items := make([]snapshotItem, 0, len(set.Items))
	for _, item := range set.Items {
		items = append(items, snapshotItem{item.Position, item.ProblemID, item.QuizID, item.Score})
	}
	payload, _ := json.Marshal(struct {
		ID    uuid.UUID      `json:"id"`
		Code  string         `json:"code"`
		Items []snapshotItem `json:"items"`
	}{set.ID, set.Code, items})
	hash := sha256.Sum256(payload)
	return hex.EncodeToString(hash[:])
}

func sumItemScores(items []domain.ProblemSetItem) int {
	total := 0
	for _, item := range items {
		total += item.Score
	}
	return total
}

func recommendProblemSetItemCount(set *domain.ProblemSet) int {
	if set.DesiredItemCount > 0 {
		return set.DesiredItemCount
	}
	minCount, maxCount := set.MinItemCount, set.MaxItemCount
	if minCount <= 0 {
		minCount = problemSetDefaultMin
	}
	if maxCount <= 0 {
		maxCount = problemSetDefaultMax
	}
	text := strings.ToLower(strings.Join(append(append([]string{}, set.Tags...), set.StylePrompt, set.DifficultyPrompt), " "))
	axes := []string{"dp", "图", "树", "字符串", "数论", "几何", "贪心", "数据结构", "构造", "搜索", "模拟", "随机"}
	count := 0
	for _, axis := range axes {
		if strings.Contains(text, axis) {
			count++
		}
	}
	value := 10 + count
	if strings.Contains(text, "最大") || strings.Contains(text, "边界") || strings.Contains(text, "corner") {
		value += 2
	}
	if value < minCount {
		value = minCount
	}
	if value > maxCount {
		value = maxCount
	}
	return value
}

func uniqueSorted(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func mapProblemSetPersistenceError(err error) error {
	if err == nil {
		return nil
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "duplicate") || strings.Contains(message, "unique") || strings.Contains(message, "constraint") {
		return fmt.Errorf("%w: %v", ErrConflict, err)
	}
	return err
}
