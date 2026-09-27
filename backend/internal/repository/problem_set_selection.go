package repository

import (
	"context"
	"fmt"
	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (r *ProblemSetRepository) AddItems(ctx context.Context, setID uuid.UUID, items []domain.ProblemSetItem) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = lockEditableProblemSet(ctx, tx, setID); err != nil {
		return err
	}
	if err = appendSelectedItems(ctx, tx, setID, items); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// The set row is locked by the caller (or was just inserted). A failed item
// rolls back the entire batch; repeats are harmless and preserve existing order.
func appendSelectedItems(ctx context.Context, tx pgx.Tx, setID uuid.UUID, items []domain.ProblemSetItem) error {
	if len(items) == 0 || len(items) > domain.MaxProblemSetItemCount {
		return fmt.Errorf("validation: 无效的选题数量")
	}
	var position, count int
	if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(position),0),COUNT(*) FROM problem_set_items WHERE set_id=$1`, setID).Scan(&position, &count); err != nil {
		return err
	}
	for _, item := range items {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM problem_set_items WHERE set_id=$1 AND (problem_id=$2 OR quiz_id=$3))`, setID, nullableUUID(item.ProblemID), nullableUUID(item.QuizID)).Scan(&exists); err != nil {
			return err
		}
		if exists {
			continue
		}
		count++
		if count > domain.MaxProblemSetItemCount {
			return fmt.Errorf("validation: 题集最多容纳 %d 道题目", domain.MaxProblemSetItemCount)
		}
		position++
		if _, err := tx.Exec(ctx, `INSERT INTO problem_set_items(id,set_id,problem_id,quiz_id,position,score,section,notes) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`,
			uuid.New(), setID, nullableUUID(item.ProblemID), nullableUUID(item.QuizID), position, item.Score, item.Section, item.Notes); err != nil {
			return err
		}
	}
	_, err := tx.Exec(ctx, `UPDATE problem_sets SET updated_at=NOW(),status='draft',total_score=(SELECT COALESCE(SUM(score),0) FROM problem_set_items WHERE set_id=$1) WHERE id=$1`, setID)
	return err
}
