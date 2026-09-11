package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var ErrProblemSetDuplicateContent = errors.New("题目内容与本题集已有题目重复")

var ErrProblemSetGenerationActive = errors.New("题集正在自动生成，请先停止生成再修改")
var ErrProblemSetGenerationChanged = errors.New("题集或生成任务已经变化，请刷新后重试")

func lockSetGeneration(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.ProblemSetGenerationState, time.Time, error) {
	var data []byte
	var updated time.Time
	if err := tx.QueryRow(ctx, `SELECT generation_state,updated_at FROM problem_sets WHERE id=$1 FOR UPDATE`, id).Scan(&data, &updated); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, updated, sql.ErrNoRows
		}
		return nil, updated, err
	}
	var state *domain.ProblemSetGenerationState
	if len(data) > 0 {
		if err := json.Unmarshal(data, &state); err != nil {
			return nil, updated, err
		}
	}
	return state, updated, nil
}
func lockEditableProblemSet(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	state, _, err := lockSetGeneration(ctx, tx, id)
	if err != nil {
		return err
	}
	if state.Active() {
		return ErrProblemSetGenerationActive
	}
	return nil
}
func saveSetGeneration(ctx context.Context, tx pgx.Tx, id uuid.UUID, state *domain.ProblemSetGenerationState) error {
	state.UpdatedAt = time.Now().UTC()
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE problem_sets SET generation_state=$2,updated_at=NOW() WHERE id=$1`, id, raw)
	return err
}

// ReserveGeneration serializes repeated clicks with edits and with another starter.
func (r *ProblemSetRepository) ReserveGeneration(ctx context.Context, id uuid.UUID, expected time.Time, next *domain.ProblemSetGenerationState) (*domain.ProblemSetGenerationState, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	old, updated, err := lockSetGeneration(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if old.Active() {
		return old, tx.Commit(ctx)
	}
	if !updated.Equal(expected) {
		return nil, ErrProblemSetGenerationChanged
	}
	if err = saveSetGeneration(ctx, tx, id, next); err != nil {
		return nil, err
	}
	return next, tx.Commit(ctx)
}

// ChangeGeneration keeps concurrent slot completions from overwriting each other.
func (r *ProblemSetRepository) ChangeGeneration(ctx context.Context, ref domain.ProblemSetGenerationRef, change func(*domain.ProblemSetGenerationState) error) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	state, _, err := lockSetGeneration(ctx, tx, ref.SetID)
	if err != nil {
		return err
	}
	if state == nil || state.ID != ref.RunID {
		return ErrProblemSetGenerationChanged
	}
	if err = change(state); err != nil {
		return err
	}
	if err = saveSetGeneration(ctx, tx, ref.SetID, state); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// CompleteGenerationSlot commits the item and its completion marker together.
// The source identity is stable across activity retries; an occupied slot is never replaced.
func (r *ProblemSetRepository) CompleteGenerationSlot(ctx context.Context, ref domain.ProblemSetGenerationRef, result domain.ProblemSetGenerationSlot) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	state, _, err := lockSetGeneration(ctx, tx, ref.SetID)
	if err != nil {
		return err
	}
	if state == nil || state.ID != ref.RunID {
		return ErrProblemSetGenerationChanged
	}
	slot := state.FindSlot(result.Position)
	if slot == nil || slot.Type != result.Type || slot.ChildID != result.ChildID {
		return ErrProblemSetGenerationChanged
	}
	if slot.Status == "succeeded" {
		return tx.Commit(ctx)
	}
	if !state.Active() {
		return ErrProblemSetGenerationChanged
	}
	if (result.ProblemID == nil) == (result.QuizID == nil) {
		return fmt.Errorf("exactly one generated source is required")
	}
	if (slot.Type == domain.QuizTypeProgramming) != (result.ProblemID != nil) {
		return fmt.Errorf("generated source type does not match slot")
	}
	if result.Fingerprint == "" {
		return fmt.Errorf("generated content identity is missing")
	}
	for _, other := range state.Slots {
		if other.Status == "succeeded" && other.Position != slot.Position && other.Fingerprint == result.Fingerprint {
			return ErrProblemSetDuplicateContent
		}
	}
	itemID := uuid.NewSHA1(uuid.NameSpaceURL, []byte(fmt.Sprintf("algoforge:set:%s:slot:%d", ref.SetID, slot.Position)))
	_, err = tx.Exec(ctx, `INSERT INTO problem_set_items(id,set_id,problem_id,quiz_id,position,score,section,notes)
 VALUES($1,$2,$3,$4,$5,$6,$7,'')`, itemID, ref.SetID, nullableUUID(result.ProblemID), nullableUUID(result.QuizID), slot.Position, slot.Score, slot.Type.ToExcel())
	if err != nil {
		return fmt.Errorf("自动加入题集: %w", err)
	}
	slot.Fingerprint = result.Fingerprint
	slot.ProblemID = result.ProblemID
	slot.QuizID = result.QuizID
	slot.Status = "succeeded"
	slot.Error = ""
	if err = saveSetGeneration(ctx, tx, ref.SetID, state); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE problem_sets SET total_score=(SELECT COALESCE(SUM(score),0) FROM problem_set_items WHERE set_id=$1),status='draft' WHERE id=$1`, ref.SetID)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
