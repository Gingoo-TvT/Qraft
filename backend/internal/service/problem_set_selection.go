package service

import (
	"context"
	"fmt"
	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/google/uuid"
	"strings"
)

// Manual selection accepts visible draft/review items. Publication and ZIP
// export keep their own asset/quality checks; adding an item never publishes it.
func (s *ProblemSetService) prepareSelectedItems(ctx context.Context, requests []ProblemSetAddItemRequest) ([]domain.ProblemSetItem, error) {
	if len(requests) == 0 || len(requests) > domain.MaxProblemSetItemCount {
		return nil, fmt.Errorf("validation: 每次请选择 1 到 %d 道题目", domain.MaxProblemSetItemCount)
	}
	seen := map[string]bool{}
	items := make([]domain.ProblemSetItem, 0, len(requests))
	for _, req := range requests {
		if (req.ProblemID == nil) == (req.QuizID == nil) || req.Score < 0 {
			return nil, fmt.Errorf("validation: 每项需要一道题目，分值不能为负数")
		}
		var key string
		if req.ProblemID != nil {
			key = "problem:" + req.ProblemID.String()
			if s.problems == nil {
				return nil, fmt.Errorf("题库服务不可用")
			}
			if _, err := s.problems.GetProblem(ctx, *req.ProblemID); err != nil {
				return nil, err
			}
		} else {
			key = "quiz:" + req.QuizID.String()
			if s.quizzes == nil {
				return nil, fmt.Errorf("客观题服务不可用")
			}
			if _, err := s.quizzes.Get(ctx, *req.QuizID); err != nil {
				return nil, err
			}
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		items = append(items, domain.ProblemSetItem{ProblemID: req.ProblemID, QuizID: req.QuizID, Score: req.Score,
			Section: strings.TrimSpace(req.Section), Notes: strings.TrimSpace(req.Notes)})
	}
	return items, nil
}

func (s *ProblemSetService) AddItems(ctx context.Context, setID uuid.UUID, requests []ProblemSetAddItemRequest) (*domain.ProblemSet, error) {
	if err := s.requireSetAccess(ctx, setID, true); err != nil {
		return nil, err
	}
	items, err := s.prepareSelectedItems(ctx, requests)
	if err != nil {
		return nil, err
	}
	store, ok := s.repo.(interface {
		AddItems(context.Context, uuid.UUID, []domain.ProblemSetItem) error
	})
	if !ok {
		return nil, fmt.Errorf("题集批量保存服务不可用")
	}
	if err := store.AddItems(ctx, setID, items); err != nil {
		return nil, mapProblemSetPersistenceError(err)
	}
	return s.Get(ctx, setID)
}
