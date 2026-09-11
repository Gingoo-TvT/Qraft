package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
	algoworkflow "github.com/Gingoo-TvT/Qraft/backend/internal/workflow"
	"github.com/google/uuid"
	"go.temporal.io/sdk/temporal"
)

func (s *ProblemSetGenerationService) recoverSetSlot(ctx context.Context, slot *domain.ProblemSetGenerationSlot) error {
	if slot.Type == domain.QuizTypeProgramming {
		p, err := s.problems.GetProblemByWorkflowID(ctx, slot.ChildID)
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if p.Status == domain.ProblemStatusDraft || p.Status == domain.ProblemStatusPublished {
			slot.ProblemID = &p.ID
		}
	} else {
		id := uuid.NewSHA1(uuid.NameSpaceURL, []byte(fmt.Sprintf("algoforge:store-quiz:%s/store-quiz/v1:0", slot.ChildID)))
		_, err := s.sets.quizzes.Get(ctx, id)
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		slot.QuizID = &id
	}
	return nil
}
func (s *ProblemSetGenerationService) RecordProblemSetSlotActivity(ctx context.Context, in algoworkflow.ProblemSetSlotInput) error {
	return s.repo.ChangeGeneration(ctx, in.Ref, func(state *domain.ProblemSetGenerationState) error {
		if !state.Active() {
			return repository.ErrProblemSetGenerationChanged
		}
		slot := state.FindSlot(in.Slot.Position)
		if slot == nil || slot.Type != in.Slot.Type || slot.ChildID != in.Slot.ChildID {
			return repository.ErrProblemSetGenerationChanged
		}
		if slot.Status == "succeeded" {
			return nil
		}
		switch in.Slot.Status {
		case "running", "generated", "failed":
		default:
			return fmt.Errorf("invalid slot transition")
		}
		slot.Regenerate = in.Slot.Regenerate
		slot.Status = in.Slot.Status
		slot.Error = boundedSetError(in.Slot.Error)
		if in.Slot.ProblemID != nil {
			slot.ProblemID = in.Slot.ProblemID
		}
		if in.Slot.QuizID != nil {
			slot.QuizID = in.Slot.QuizID
		}
		return nil
	})
}
func (s *ProblemSetGenerationService) AttachProblemSetSlotActivity(ctx context.Context, in algoworkflow.ProblemSetSlotInput) error {
	slot := in.Slot
	if slot.Type == domain.QuizTypeProgramming {
		if slot.ProblemID == nil {
			return fmt.Errorf("programming result is missing")
		}
		p, err := s.sets.problems.GetProblem(ctx, *slot.ProblemID)
		if err != nil {
			return err
		}
		if s.sets.packageSvc == nil {
			return fmt.Errorf("programming asset verification is unavailable")
		}
		_, err = s.sets.packageSvc.BuildProblemPackage(ctx, p.ID)
		if err != nil {
			return fmt.Errorf("编程题资产未通过导出校验: %w", err)
		}
		slot.Fingerprint = setContentFingerprint(p.Statement)
	} else {
		if slot.QuizID == nil {
			return fmt.Errorf("quiz result is missing")
		}
		q, err := s.sets.quizzes.Get(ctx, *slot.QuizID)
		if err != nil {
			return err
		}
		if q.Type != slot.Type {
			return fmt.Errorf("客观题类型与配额不一致")
		}
		quiz := *q
		quiz.KnowledgePointIDs = nil
		if strings.TrimSpace(quiz.Explanation) == "" {
			return fmt.Errorf("客观题缺少解析")
		}
		if err = validateQuizRow(quiz, 1); err != nil {
			return fmt.Errorf("客观题字段校验失败: %w", err)
		}
		slot.Fingerprint = setContentFingerprint(q.Statement)
	}
	err := s.repo.CompleteGenerationSlot(ctx, in.Ref, slot)
	if errors.Is(err, repository.ErrProblemSetDuplicateContent) {
		return temporal.NewNonRetryableApplicationError(err.Error(), "ProblemSetSlotRejected", err)
	}
	return err
}
