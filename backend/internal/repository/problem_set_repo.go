package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ProblemSetRepository persists contest sets, ordered mixed items, and the
// append-only diversity ledger used by the quality gate.
type ProblemSetRepository struct {
	db *pgxpool.Pool
}

func NewProblemSetRepository(db *pgxpool.Pool) *ProblemSetRepository {
	return &ProblemSetRepository{db: db}
}

func (r *ProblemSetRepository) Create(ctx context.Context, set *domain.ProblemSet) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("problem set database is required")
	}
	return createProblemSet(ctx, r.db, set)
}

type problemSetInserter interface {
	Exec(context.Context, string, ...interface{}) (pgconn.CommandTag, error)
}

func createProblemSet(ctx context.Context, db problemSetInserter, set *domain.ProblemSet) error {
	if set.ID == uuid.Nil {
		set.ID = uuid.New()
	}
	now := time.Now().UTC()
	if set.CreatedAt.IsZero() {
		set.CreatedAt = now
	}
	if set.UpdatedAt.IsZero() {
		set.UpdatedAt = now
	}
	_, err := db.Exec(ctx, `
		INSERT INTO problem_sets (
			id, code, title, description, kind, visibility, subject, tags,
			style_prompt, difficulty_prompt, generated_prompt,
			generated_prompt_model, generated_prompt_at, desired_item_count,
			min_item_count, max_item_count, cooldown_sets, total_score, status,
			created_by, created_at, updated_at, generation_config
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23)`,
		set.ID, set.Code, set.Title, set.Description, set.Kind, set.Visibility,
		set.Subject, set.Tags, set.StylePrompt, set.DifficultyPrompt,
		set.GeneratedPrompt, set.GeneratedPromptModel, nullableTime(set.GeneratedPromptAt), set.DesiredItemCount,
		set.MinItemCount, set.MaxItemCount, set.CooldownSets, set.TotalScore,
		set.Status, set.CreatedBy, set.CreatedAt, set.UpdatedAt, set.GenerationConfig,
	)
	if err != nil {
		return fmt.Errorf("creating problem set: %w", err)
	}
	return nil
}

func (r *ProblemSetRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.ProblemSet, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("problem set database is required")
	}
	var set domain.ProblemSet
	if err := scanProblemSet(r.db.QueryRow(ctx, problemSetSelectSQL+` WHERE id=$1`, id), &set); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("problem set %s not found: %w", id, sql.ErrNoRows)
		}
		return nil, fmt.Errorf("querying problem set %s: %w", id, err)
	}
	items, err := r.ListItems(ctx, id)
	if err != nil {
		return nil, err
	}
	set.Items = items
	return &set, nil
}

func (r *ProblemSetRepository) GetByCode(ctx context.Context, code string) (*domain.ProblemSet, error) {
	var set domain.ProblemSet
	if err := scanProblemSet(r.db.QueryRow(ctx, problemSetSelectSQL+` WHERE code=$1`, code), &set); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("problem set %s not found: %w", code, sql.ErrNoRows)
		}
		return nil, fmt.Errorf("querying problem set %s: %w", code, err)
	}
	items, err := r.ListItems(ctx, set.ID)
	if err != nil {
		return nil, err
	}
	set.Items = items
	return &set, nil
}

type ProblemSetListFilter struct {
	Kind   *domain.ProblemSetKind
	Status *domain.ProblemSetStatus
	Search string
	Page   int
	Size   int
}

func (r *ProblemSetRepository) List(ctx context.Context, filter ProblemSetListFilter) ([]*domain.ProblemSet, int, error) {
	if filter.Page < 1 {
		filter.Page = 1
	}
	if filter.Size < 1 || filter.Size > 100 {
		filter.Size = 20
	}
	var conditions []string
	args := make([]interface{}, 0, 4)
	if filter.Kind != nil {
		conditions = append(conditions, fmt.Sprintf("kind=$%d", len(args)+1))
		args = append(args, *filter.Kind)
	}
	if filter.Status != nil {
		conditions = append(conditions, fmt.Sprintf("status=$%d", len(args)+1))
		args = append(args, *filter.Status)
	}
	if strings.TrimSpace(filter.Search) != "" {
		placeholder := len(args) + 1
		conditions = append(conditions, fmt.Sprintf("(title ILIKE $%d OR code ILIKE $%d)", placeholder, placeholder))
		args = append(args, "%"+strings.TrimSpace(filter.Search)+"%")
	}
	where := ""
	if len(conditions) > 0 {
		where = " WHERE " + strings.Join(conditions, " AND ")
	}
	var total int
	if err := r.db.QueryRow(ctx, `SELECT COUNT(*) FROM problem_sets`+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("counting problem sets: %w", err)
	}
	args = append(args, filter.Size, (filter.Page-1)*filter.Size)
	query := problemSetSelectSQL + where + fmt.Sprintf(" ORDER BY created_at DESC, id DESC LIMIT $%d OFFSET $%d", len(args)-1, len(args))
	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("listing problem sets: %w", err)
	}
	defer rows.Close()
	out := make([]*domain.ProblemSet, 0)
	for rows.Next() {
		set := &domain.ProblemSet{}
		if err := scanProblemSet(rows, set); err != nil {
			return nil, 0, fmt.Errorf("scanning problem set: %w", err)
		}
		out = append(out, set)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterating problem sets: %w", err)
	}
	return out, total, nil
}

func (r *ProblemSetRepository) Update(ctx context.Context, set *domain.ProblemSet) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	state, updated, err := lockSetGeneration(ctx, tx, set.ID)
	if err != nil {
		return err
	}
	if state.Active() {
		return ErrProblemSetGenerationActive
	}
	if !updated.Equal(set.UpdatedAt) {
		return ErrProblemSetGenerationChanged
	}
	tag, err := tx.Exec(ctx, `
		UPDATE problem_sets SET code=$2,title=$3,description=$4,kind=$5,visibility=$6,
		subject=$7,tags=$8,style_prompt=$9,difficulty_prompt=$10,desired_item_count=$11,
		min_item_count=$12,max_item_count=$13,cooldown_sets=$14,total_score=$15,status=$16,
		generation_config=$17,updated_at=NOW() WHERE id=$1`,
		set.ID, set.Code, set.Title, set.Description, set.Kind, set.Visibility,
		set.Subject, set.Tags, set.StylePrompt, set.DifficultyPrompt, set.DesiredItemCount,
		set.MinItemCount, set.MaxItemCount, set.CooldownSets, set.TotalScore, set.Status, set.GenerationConfig,
	)
	if err != nil {
		return fmt.Errorf("updating problem set: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("problem set %s not found: %w", set.ID, sql.ErrNoRows)
	}
	return tx.Commit(ctx)
}

func (r *ProblemSetRepository) UpdateGeneratedPrompt(ctx context.Context, id uuid.UUID, prompt, model string, generatedAt time.Time) error {
	var generatedAtValue interface{}
	if !generatedAt.IsZero() {
		generatedAtValue = generatedAt
	}
	tag, err := r.db.Exec(ctx, `
		UPDATE problem_sets SET generated_prompt=$2, generated_prompt_model=$3,
		generated_prompt_at=$4, updated_at=NOW() WHERE id=$1`, id, prompt, model, generatedAtValue)
	if err != nil {
		return fmt.Errorf("updating generated problem-set prompt: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("problem set %s not found: %w", id, sql.ErrNoRows)
	}
	return nil
}

func (r *ProblemSetRepository) UpdateStatusAndScore(ctx context.Context, id uuid.UUID, status domain.ProblemSetStatus, score int) error {
	tag, err := r.db.Exec(ctx, `UPDATE problem_sets SET status=$2,total_score=$3,updated_at=NOW() WHERE id=$1`, id, status, score)
	if err != nil {
		return fmt.Errorf("updating problem set status: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("problem set %s not found: %w", id, sql.ErrNoRows)
	}
	return nil
}

func (r *ProblemSetRepository) Delete(ctx context.Context, id uuid.UUID) error {
	tx, beginErr := r.db.Begin(ctx)
	if beginErr != nil {
		return beginErr
	}
	defer tx.Rollback(ctx)
	if err := lockEditableProblemSet(ctx, tx, id); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `DELETE FROM problem_sets WHERE id=$1`, id)
	if err != nil {
		return fmt.Errorf("deleting problem set: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("problem set %s not found: %w", id, sql.ErrNoRows)
	}
	return tx.Commit(ctx)
}

func (r *ProblemSetRepository) ListItems(ctx context.Context, setID uuid.UUID) ([]domain.ProblemSetItem, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id,set_id,problem_id,quiz_id,position,score,section,notes,created_at,updated_at
		FROM problem_set_items WHERE set_id=$1 ORDER BY position ASC,id ASC`, setID)
	if err != nil {
		return nil, fmt.Errorf("listing problem set items: %w", err)
	}
	defer rows.Close()
	out := make([]domain.ProblemSetItem, 0)
	for rows.Next() {
		var item domain.ProblemSetItem
		var problemID, quizID uuid.NullUUID
		if err := rows.Scan(&item.ID, &item.SetID, &problemID, &quizID, &item.Position, &item.Score, &item.Section, &item.Notes, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scanning problem set item: %w", err)
		}
		if problemID.Valid {
			id := problemID.UUID
			item.ProblemID = &id
		}
		if quizID.Valid {
			id := quizID.UUID
			item.QuizID = &id
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating problem set items: %w", err)
	}
	return out, nil
}

func (r *ProblemSetRepository) AddItem(ctx context.Context, item *domain.ProblemSetItem) error {
	tx, beginErr := r.db.Begin(ctx)
	if beginErr != nil {
		return beginErr
	}
	defer tx.Rollback(ctx)
	if err := lockEditableProblemSet(ctx, tx, item.SetID); err != nil {
		return err
	}
	if item.ID == uuid.Nil {
		item.ID = uuid.New()
	}
	if item.Position <= 0 {
		var max int
		if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(position),0) FROM problem_set_items WHERE set_id=$1`, item.SetID).Scan(&max); err != nil {
			return fmt.Errorf("allocating problem set position: %w", err)
		}
		item.Position = max + 1
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO problem_set_items (id,set_id,problem_id,quiz_id,position,score,section,notes)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		item.ID, item.SetID, nullableUUID(item.ProblemID), nullableUUID(item.QuizID),
		item.Position, item.Score, item.Section, item.Notes)
	if err != nil {
		return fmt.Errorf("adding problem set item: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE problem_sets SET updated_at=NOW(),status='draft',total_score=(SELECT COALESCE(SUM(score),0) FROM problem_set_items WHERE set_id=$1) WHERE id=$1`, item.SetID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *ProblemSetRepository) RemoveItem(ctx context.Context, setID, itemID uuid.UUID) error {
	tx, beginErr := r.db.Begin(ctx)
	if beginErr != nil {
		return beginErr
	}
	defer tx.Rollback(ctx)
	if err := lockEditableProblemSet(ctx, tx, setID); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `DELETE FROM problem_set_items WHERE set_id=$1 AND id=$2`, setID, itemID)
	if err != nil {
		return fmt.Errorf("removing problem set item: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("problem set item %s not found: %w", itemID, sql.ErrNoRows)
	}
	if _, err := tx.Exec(ctx, `UPDATE problem_sets SET updated_at=NOW(),status='draft',total_score=(SELECT COALESCE(SUM(score),0) FROM problem_set_items WHERE set_id=$1) WHERE id=$1`, setID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *ProblemSetRepository) ReorderItems(ctx context.Context, setID uuid.UUID, itemIDs []uuid.UUID) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin problem set reorder: %w", err)
	}
	defer tx.Rollback(ctx)
	if err := lockEditableProblemSet(ctx, tx, setID); err != nil {
		return err
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM problem_set_items WHERE set_id=$1`, setID).Scan(&count); err != nil {
		return fmt.Errorf("counting problem set items: %w", err)
	}
	if count != len(itemIDs) {
		return fmt.Errorf("reorder must include every item exactly once")
	}
	if _, err := tx.Exec(ctx, `UPDATE problem_set_items SET position=position+1000000 WHERE set_id=$1`, setID); err != nil {
		return fmt.Errorf("temporarily shifting problem set positions: %w", err)
	}
	seen := make(map[uuid.UUID]struct{}, len(itemIDs))
	for index, id := range itemIDs {
		if _, ok := seen[id]; ok {
			return fmt.Errorf("reorder contains duplicate item %s", id)
		}
		seen[id] = struct{}{}
		tag, err := tx.Exec(ctx, `UPDATE problem_set_items SET position=$3,updated_at=NOW() WHERE set_id=$1 AND id=$2`, setID, id, index+1)
		if err != nil {
			return fmt.Errorf("setting problem set item position: %w", err)
		}
		if tag.RowsAffected() != 1 {
			return fmt.Errorf("reorder contains an unknown item %s", id)
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE problem_sets SET updated_at=NOW(),status='draft' WHERE id=$1`, setID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit problem set reorder: %w", err)
	}
	return nil
}

func (r *ProblemSetRepository) CreateLedgerEntry(ctx context.Context, entry *domain.ProblemSetLedgerEntry) error {
	if entry.ID == uuid.Nil {
		entry.ID = uuid.New()
	}
	if entry.Revision <= 0 {
		if err := r.db.QueryRow(ctx, `SELECT COALESCE(MAX(revision),0)+1 FROM problem_set_ledger_entries WHERE set_id=$1`, entry.SetID).Scan(&entry.Revision); err != nil {
			return fmt.Errorf("allocating problem set ledger revision: %w", err)
		}
	}
	report, err := json.Marshal(entry.OverlapReport)
	if err != nil {
		return fmt.Errorf("encoding problem set ledger report: %w", err)
	}
	_, err = r.db.Exec(ctx, `
		INSERT INTO problem_set_ledger_entries
		(id,set_id,revision,event_type,snapshot_sha256,knowledge_point_keys,item_fingerprints,overlap_report)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8::jsonb)`,
		entry.ID, entry.SetID, entry.Revision, entry.EventType, entry.SnapshotSHA256,
		entry.KnowledgePointKeys, entry.ItemFingerprints, string(report))
	if err != nil {
		return fmt.Errorf("creating problem set ledger entry: %w", err)
	}
	return nil
}

func (r *ProblemSetRepository) ListRecentLedger(ctx context.Context, limit int) ([]domain.ProblemSetLedgerEntry, error) {
	if limit <= 0 {
		return nil, nil
	}
	if limit > 50 {
		limit = 50
	}
	rows, err := r.db.Query(ctx, `
		SELECT id,set_id,revision,event_type,snapshot_sha256,knowledge_point_keys,
		       item_fingerprints,overlap_report,created_at
		FROM (
			SELECT DISTINCT ON (set_id)
			       id,set_id,revision,event_type,snapshot_sha256,knowledge_point_keys,
			       item_fingerprints,overlap_report,created_at
			FROM problem_set_ledger_entries
			WHERE event_type='exported'
			ORDER BY set_id,created_at DESC,id DESC
		) recent
		ORDER BY created_at DESC,id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("listing recent problem set ledger: %w", err)
	}
	defer rows.Close()
	out := make([]domain.ProblemSetLedgerEntry, 0)
	for rows.Next() {
		var entry domain.ProblemSetLedgerEntry
		var report []byte
		if err := rows.Scan(&entry.ID, &entry.SetID, &entry.Revision, &entry.EventType, &entry.SnapshotSHA256, &entry.KnowledgePointKeys, &entry.ItemFingerprints, &report, &entry.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning problem set ledger: %w", err)
		}
		entry.OverlapReport = map[string]interface{}{}
		if len(report) > 0 {
			_ = json.Unmarshal(report, &entry.OverlapReport)
		}
		out = append(out, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating problem set ledger: %w", err)
	}
	return out, nil
}

const problemSetSelectSQL = `
SELECT id,code,title,description,kind,visibility,subject,tags,style_prompt,
       difficulty_prompt,generated_prompt,generated_prompt_model,generated_prompt_at,
       desired_item_count,min_item_count,max_item_count,cooldown_sets,total_score,
       status,created_by,created_at,updated_at,generation_config,generation_state FROM problem_sets`

type problemSetRowScanner interface {
	Scan(dest ...interface{}) error
}

func scanProblemSet(row problemSetRowScanner, set *domain.ProblemSet) error {
	return row.Scan(&set.ID, &set.Code, &set.Title, &set.Description, &set.Kind, &set.Visibility,
		&set.Subject, &set.Tags, &set.StylePrompt, &set.DifficultyPrompt, &set.GeneratedPrompt,
		&set.GeneratedPromptModel, &set.GeneratedPromptAt, &set.DesiredItemCount, &set.MinItemCount,
		&set.MaxItemCount, &set.CooldownSets, &set.TotalScore, &set.Status, &set.CreatedBy,
		&set.CreatedAt, &set.UpdatedAt, &set.GenerationConfig, &set.Generation)
}

func nullableUUID(id *uuid.UUID) interface{} {
	if id == nil {
		return nil
	}
	return *id
}

func nullableTime(value *time.Time) interface{} {
	if value == nil {
		return nil
	}
	return *value
}
