package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	algoworkflow "github.com/Gingoo-TvT/Qraft/backend/internal/workflow"
	"github.com/google/uuid"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
)

type problemSetGenerationStore interface {
	ReserveGeneration(context.Context, uuid.UUID, time.Time, *domain.ProblemSetGenerationState) (*domain.ProblemSetGenerationState, error)
	ChangeGeneration(context.Context, domain.ProblemSetGenerationRef, func(*domain.ProblemSetGenerationState) error) error
	CompleteGenerationSlot(context.Context, domain.ProblemSetGenerationRef, domain.ProblemSetGenerationSlot) error
}
type setTagReader interface {
	GetAll(context.Context) ([]domain.TagCategory, error)
}
type ProblemSetGenerationService struct {
	sets     *ProblemSetService
	repo     problemSetGenerationStore
	problems *ProblemService
	tags     setTagReader
	temporal client.Client
	queue    string
	resolve  ProblemSetProviderResolver
}

func NewProblemSetGenerationService(sets *ProblemSetService, repo problemSetGenerationStore, problems *ProblemService, tags setTagReader, temporalClient client.Client, queue string, resolve ProblemSetProviderResolver) *ProblemSetGenerationService {
	return &ProblemSetGenerationService{sets: sets, repo: repo, problems: problems, tags: tags, temporal: temporalClient, queue: queue, resolve: resolve}
}
func setWorkflowID(ref domain.ProblemSetGenerationRef) string {
	return "problem-set-generation-" + ref.SetID.String() + "-" + ref.RunID
}
func (s *ProblemSetGenerationService) Start(ctx context.Context, id uuid.UUID) (*domain.ProblemSetGenerationState, error) {
	if s == nil || s.temporal == nil || s.resolve == nil {
		return nil, fmt.Errorf("problem-set generation is unavailable")
	}
	set, err := s.sets.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if set.GenerationConfig == nil {
		return nil, fmt.Errorf("validation: 请先保存题型配额和整套需求")
	}
	if err = set.GenerationConfig.Validate(set.DesiredItemCount); err != nil {
		return nil, fmt.Errorf("validation: %w", err)
	}
	if _, err = s.resolve(ctx); err != nil {
		return nil, err
	}
	next := set.Generation
	if !next.Active() {
		next, err = buildSetGenerationState(set)
		if err != nil {
			return nil, err
		}
		next, err = s.repo.ReserveGeneration(ctx, id, set.UpdatedAt, next)
		if err != nil {
			return nil, err
		}
	}
	if next.Status != "queued" {
		return next, nil
	}
	ref := domain.ProblemSetGenerationRef{SetID: id, RunID: next.ID}
	_, err = s.temporal.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID: setWorkflowID(ref), TaskQueue: s.queue,
		WorkflowIDReusePolicy:    enumspb.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
		WorkflowIDConflictPolicy: enumspb.WORKFLOW_ID_CONFLICT_POLICY_USE_EXISTING,
	}, algoworkflow.ProblemSetGenerationWorkflow, ref)
	var already *serviceerror.WorkflowExecutionAlreadyStarted
	if err != nil && !errors.As(err, &already) {
		// An uncertain start keeps its reservation. Repeating it uses the same ID.
		return nil, fmt.Errorf("generation start unavailable: 启动尚未确认，请在题集页继续启动: %w", err)
	}
	return next, nil
}
func (s *ProblemSetGenerationService) Status(ctx context.Context, id uuid.UUID) (*domain.ProblemSetGenerationState, error) {
	set, err := s.sets.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	state := set.Generation
	if !state.Active() {
		return state, nil
	}
	ref := domain.ProblemSetGenerationRef{SetID: id, RunID: state.ID}
	desc, err := s.temporal.DescribeWorkflowExecution(ctx, setWorkflowID(ref), "")
	if err != nil {
		var missing *serviceerror.NotFound
		if errors.As(err, &missing) && state.Status == "queued" {
			return state, nil
		}
		return nil, fmt.Errorf("generation status unavailable: %w", err)
	}
	if desc.WorkflowExecutionInfo == nil {
		return nil, fmt.Errorf("generation status unavailable")
	}
	switch desc.WorkflowExecutionInfo.Status {
	case enumspb.WORKFLOW_EXECUTION_STATUS_FAILED, enumspb.WORKFLOW_EXECUTION_STATUS_TIMED_OUT, enumspb.WORKFLOW_EXECUTION_STATUS_TERMINATED, enumspb.WORKFLOW_EXECUTION_STATUS_CANCELED:
		err = s.repo.ChangeGeneration(ctx, ref, func(current *domain.ProblemSetGenerationState) error {
			if !current.Active() {
				return nil
			}
			current.Status = "failed"
			current.Error = "生成任务已中断，可以重试未完成题目"
			if desc.WorkflowExecutionInfo.Status == enumspb.WORKFLOW_EXECUTION_STATUS_CANCELED {
				current.Status = "cancelled"
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		set, err = s.sets.repo.GetByID(ctx, id)
		if err != nil {
			return nil, err
		}
		state = set.Generation
	}
	return state, nil
}
func (s *ProblemSetGenerationService) Cancel(ctx context.Context, id uuid.UUID) error {
	set, err := s.sets.repo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if !set.Generation.Active() {
		return nil
	}
	ref := domain.ProblemSetGenerationRef{SetID: id, RunID: set.Generation.ID}
	err = s.temporal.CancelWorkflow(ctx, setWorkflowID(ref), "")
	var missing *serviceerror.NotFound
	if err != nil && !errors.As(err, &missing) {
		return err
	}
	if errors.As(err, &missing) {
		return s.repo.ChangeGeneration(ctx, ref, func(state *domain.ProblemSetGenerationState) error {
			state.Status = "cancelled"
			state.Error = "已停止"
			return nil
		})
	}
	return nil
}
func setConfigFingerprint(set *domain.ProblemSet) string {
	raw, _ := json.Marshal(struct {
		Title, Description, Subject, Style, Difficulty string
		Tags                                           []string
		Count, Cooldown                                int
		Config                                         *domain.ProblemSetGenerationConfig
	}{set.Title, set.Description, set.Subject, set.StylePrompt, set.DifficultyPrompt, set.Tags, set.DesiredItemCount, set.CooldownSets, set.GenerationConfig})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func buildSetGenerationState(set *domain.ProblemSet) (*domain.ProblemSetGenerationState, error) {
	now := time.Now().UTC()
	state := &domain.ProblemSetGenerationState{ID: uuid.NewString(), Status: "queued", ConfigFingerprint: setConfigFingerprint(set), StartedAt: now, UpdatedAt: now}
	remaining := map[domain.QuizType]int{}
	scores := map[domain.QuizType]int{}
	for _, q := range set.GenerationConfig.Distribution {
		remaining[q.Type] = q.Count
		scores[q.Type] = q.Score
	}
	used := map[int]bool{}
	for _, item := range set.Items {
		if item.Position < 1 || item.Position > set.DesiredItemCount || used[item.Position] {
			return nil, fmt.Errorf("validation: 请先将已有题目排序到 1-%d 范围内", set.DesiredItemCount)
		}
		typ := domain.QuizTypeProgramming
		title, statement := "", ""
		if item.Problem != nil {
			title = item.Problem.Title
			statement = item.Problem.Statement
		} else if item.Quiz != nil {
			typ = item.Quiz.Type
			title = item.Quiz.Title
			statement = item.Quiz.Statement
		} else {
			return nil, fmt.Errorf("validation: 已有题目来源不可用")
		}
		remaining[typ]--
		if remaining[typ] < 0 {
			return nil, fmt.Errorf("validation: 已有%s数量超过配额，请调整题型数量", typ.ToExcel())
		}
		used[item.Position] = true
		state.Slots = append(state.Slots, domain.ProblemSetGenerationSlot{Position: item.Position, Type: typ, Score: item.Score, Title: title, Status: "succeeded", ProblemID: item.ProblemID, QuizID: item.QuizID, Fingerprint: setContentFingerprint(statement)})
	}
	old := set.Generation
	if old != nil && old.ConfigFingerprint == state.ConfigFingerprint {
		state.Brief = old.Brief
		for _, slot := range old.Slots {
			if slot.Status == "succeeded" || used[slot.Position] || remaining[slot.Type] <= 0 || slot.Position < 1 || slot.Position > set.DesiredItemCount {
				continue
			}
			slot.Status = "pending"
			if slot.Regenerate {
				slot.ProblemID, slot.QuizID = nil, nil
				slot.ChildID = ""
				slot.Regenerate = false
			}
			slot.Attempt++
			used[slot.Position] = true
			remaining[slot.Type]--
			state.Slots = append(state.Slots, slot)
		}
	}
	for _, quota := range set.GenerationConfig.Distribution {
		for pos := 1; remaining[quota.Type] > 0 && pos <= set.DesiredItemCount; pos++ {
			if used[pos] {
				continue
			}
			used[pos] = true
			remaining[quota.Type]--
			state.Slots = append(state.Slots, domain.ProblemSetGenerationSlot{Position: pos, Type: quota.Type, Score: scores[quota.Type], Status: "pending", Attempt: 1})
		}
	}
	sort.Slice(state.Slots, func(i, j int) bool { return state.Slots[i].Position < state.Slots[j].Position })
	pending := 0
	for _, slot := range state.Slots {
		if slot.Status == "pending" {
			pending++
		}
	}
	if pending == 0 {
		return nil, fmt.Errorf("validation: 题型数量已满足需求，无需重复生成")
	}
	return state, nil
}
func setContentFingerprint(statement string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.Join(strings.Fields(statement), ""))))
	return hex.EncodeToString(sum[:])
}
func (s *ProblemSetGenerationService) FinishProblemSetGenerationActivity(ctx context.Context, in algoworkflow.ProblemSetFinishInput) (bool, error) {
	more := false
	err := s.repo.ChangeGeneration(ctx, in.Ref, func(state *domain.ProblemSetGenerationState) error {
		if !state.Active() {
			return nil
		}
		if in.Cancelled {
			state.Status = "cancelled"
			state.Error = "已停止；已完成题目保留"
			return nil
		}
		if in.Error != "" {
			state.Status = "failed"
			state.Error = boundedSetError(in.Error)
			return nil
		}
		failed := 0
		for _, slot := range state.Slots {
			if slot.Status == "pending" {
				more = true
			}
			if slot.Status != "succeeded" {
				failed++
			}
		}
		if more {
			state.Status = "planning"
			return nil
		}
		state.Status = "completed"
		state.Error = ""
		if failed > 0 {
			state.Status = "partial"
			state.Error = fmt.Sprintf("%d 道题未完成，可重试这些题目", failed)
		}
		return nil
	})
	return more, err
}
func boundedSetError(value string) string {
	chars := []rune(strings.TrimSpace(value))
	if len(chars) > 500 {
		chars = chars[:500]
	}
	return string(chars)
}
