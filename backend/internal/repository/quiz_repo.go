package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type QuizRepository struct {
	db *pgxpool.Pool
}

func NewQuizRepository(db *pgxpool.Pool) *QuizRepository {
	return &QuizRepository{db: db}
}

type QuizListFilter struct {
	Type             *domain.QuizType
	Subject          *string
	Difficulty       *domain.QuizDifficulty
	Tag              *string
	KnowledgePointID *uuid.UUID
}

func (r *QuizRepository) Create(ctx context.Context, q *domain.QuizProblem) error {
	if q.ID == uuid.Nil {
		q.ID = uuid.New()
	}
	now := time.Now()
	if q.CreatedAt.IsZero() {
		q.CreatedAt = now
	}
	if q.UpdatedAt.IsZero() {
		q.UpdatedAt = now
	}
	options, err := json.Marshal(q.Options)
	if err != nil {
		return fmt.Errorf("marshalling quiz options: %w", err)
	}
	query := `
		INSERT INTO quiz_problems (
			id, code, title, statement, type, code_id, code_hint, options,
			answers, difficulty, visibility, is_vip, tags, langs, explanation,
			subject, created_at, updated_at
		) VALUES (
			$1,$2,$3,$4,$5,$6,$7,$8,
			$9,$10,$11,$12,$13,$14,$15,
			$16,$17,$18
		)`
	if _, err := r.db.Exec(ctx, query,
		q.ID, q.Code, q.Title, q.Statement, q.Type, q.CodeID, q.CodeHint, options,
		quizStringArray(q.Answers), q.Difficulty, q.Visibility, q.IsVIP, quizStringArray(q.Tags), quizIntArray(q.Langs), q.Explanation,
		q.Subject, q.CreatedAt, q.UpdatedAt,
	); err != nil {
		return fmt.Errorf("inserting quiz %s: %w", q.Code, err)
	}
	return nil
}

func (r *QuizRepository) BulkCreate(ctx context.Context, qs []*domain.QuizProblem, onConflict string) (inserted, skipped int, err error) {
	if len(qs) == 0 {
		return 0, 0, nil
	}
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("begin quiz bulk create: %w", err)
	}
	defer tx.Rollback(ctx)

	for _, q := range qs {
		affected, err := insertQuizTx(ctx, tx, q, onConflict)
		if err != nil {
			return 0, 0, err
		}
		if affected {
			inserted++
		} else {
			skipped++
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, 0, fmt.Errorf("commit quiz bulk create: %w", err)
	}
	return inserted, skipped, nil
}

// CreateGeneratedBatch serializes code allocation per quiz type and commits
// quizzes plus knowledge-point links atomically. Stable quiz IDs make a
// duplicate activity delivery return the original batch instead of allocating
// a second code range.
func (r *QuizRepository) CreateGeneratedBatch(
	ctx context.Context,
	operationKey string,
	typ domain.QuizType,
	quizzes []*domain.QuizProblem,
	kpIDs []uuid.UUID,
) error {
	const maxGeneratedQuizBatchSize = 20
	if len(quizzes) == 0 {
		return nil
	}
	if operationKey == "" {
		return fmt.Errorf("generated quiz operation key is required")
	}
	if len(quizzes) > maxGeneratedQuizBatchSize {
		return fmt.Errorf("generated quiz batch size %d exceeds %d", len(quizzes), maxGeneratedQuizBatchSize)
	}
	prefix := typ.CodePrefix()
	if prefix == "" {
		return fmt.Errorf("invalid quiz type: %s", typ)
	}

	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin generated quiz batch: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback on committed tx is a no-op

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "quiz-code:"+prefix); err != nil {
		return fmt.Errorf("locking quiz code allocator: %w", err)
	}

	existingCount := 0
	for _, quiz := range quizzes {
		rows, err := tx.Query(ctx, quizSelectBase()+` WHERE id = $1`, quiz.ID)
		if err != nil {
			return fmt.Errorf("checking generated quiz %s: %w", quiz.ID, err)
		}
		existing, scanErr := scanSingleQuiz(rows, fmt.Sprintf("generated quiz %s", quiz.ID))
		rows.Close()
		switch {
		case scanErr == nil:
			if !sameGeneratedQuiz(existing, quiz) {
				return fmt.Errorf("generated quiz id %s was already used with a different payload", quiz.ID)
			}
			matches, err := quizKnowledgePointsMatch(ctx, tx, quiz.ID, kpIDs)
			if err != nil {
				return err
			}
			if !matches {
				return fmt.Errorf("generated quiz id %s was already used with different knowledge points", quiz.ID)
			}
			quiz.Code = existing.Code
			existingCount++
		case errors.Is(scanErr, sql.ErrNoRows):
			// This stable ID has not been committed yet.
		default:
			return scanErr
		}
	}
	allExisting := existingCount == len(quizzes)
	if allExisting {
		for i := len(quizzes); i < maxGeneratedQuizBatchSize; i++ {
			extraID := uuid.NewSHA1(uuid.NameSpaceURL, []byte(fmt.Sprintf("algoforge:store-quiz:%s:%d", operationKey, i)))
			var exists bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM quiz_problems WHERE id=$1)`, extraID).Scan(&exists); err != nil {
				return fmt.Errorf("checking generated quiz batch size: %w", err)
			}
			if exists {
				return fmt.Errorf("generated quiz operation %q was already used with a larger batch", operationKey)
			}
		}
	} else if existingCount != 0 {
		return fmt.Errorf("incomplete generated quiz batch: found %d of %d stable ids", existingCount, len(quizzes))
	} else {
		var maxNum sql.NullInt64
		query := `SELECT MAX((substring(code from 2))::int) FROM quiz_problems WHERE code LIKE $1 AND substring(code from 2) ~ '^[0-9]+$'`
		if err := tx.QueryRow(ctx, query, prefix+"%").Scan(&maxNum); err != nil {
			return fmt.Errorf("allocating generated quiz codes: %w", err)
		}
		start := 1000
		if maxNum.Valid && int(maxNum.Int64) >= start {
			start = int(maxNum.Int64) + 1
		}

		for i, quiz := range quizzes {
			quiz.Code = fmt.Sprintf("%s%d", prefix, start+i)
			if _, err := insertQuizTx(ctx, tx, quiz, "error"); err != nil {
				return err
			}
			for _, kpID := range kpIDs {
				if _, err := tx.Exec(ctx, `INSERT INTO quiz_knowledge_points (quiz_id, knowledge_point_id) VALUES ($1,$2)`, quiz.ID, kpID); err != nil {
					return fmt.Errorf("linking generated quiz %s to knowledge point %s: %w", quiz.ID, kpID, err)
				}
			}
		}
	}
	quizIDs := make([]string, 0, len(quizzes))
	codes := make([]string, 0, len(quizzes))
	for _, quiz := range quizzes {
		quizIDs = append(quizIDs, quiz.ID.String())
		codes = append(codes, quiz.Code)
	}
	outboxPayload, err := json.Marshal(map[string]interface{}{
		"quiz_ids": quizIDs,
		"codes":    codes,
		"type":     typ,
	})
	if err != nil {
		return fmt.Errorf("encoding generated quiz outbox payload: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO workflow_outbox (
			operation_key, event_type, aggregate_type, aggregate_id, payload_json
		) VALUES ($1, 'quiz.batch_stored', 'quiz_batch', $1, $2::jsonb)
		ON CONFLICT (operation_key, event_type) DO NOTHING`, operationKey, string(outboxPayload)); err != nil {
		return fmt.Errorf("enqueueing generated quiz outbox event: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit generated quiz batch: %w", err)
	}
	return nil
}

func quizKnowledgePointsMatch(ctx context.Context, tx pgx.Tx, quizID uuid.UUID, expected []uuid.UUID) (bool, error) {
	rows, err := tx.Query(ctx, `SELECT knowledge_point_id FROM quiz_knowledge_points WHERE quiz_id=$1 ORDER BY knowledge_point_id`, quizID)
	if err != nil {
		return false, fmt.Errorf("querying generated quiz knowledge points: %w", err)
	}
	defer rows.Close()

	actual := make([]string, 0, len(expected))
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return false, fmt.Errorf("scanning generated quiz knowledge point: %w", err)
		}
		actual = append(actual, id.String())
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("iterating generated quiz knowledge points: %w", err)
	}
	want := make([]string, 0, len(expected))
	for _, id := range expected {
		want = append(want, id.String())
	}
	slices.Sort(actual)
	slices.Sort(want)
	return slices.Equal(actual, want), nil
}

func sameGeneratedQuiz(existing, expected *domain.QuizProblem) bool {
	return existing.Title == expected.Title &&
		existing.Statement == expected.Statement &&
		existing.Type == expected.Type &&
		slices.Equal(existing.Options, expected.Options) &&
		slices.Equal(existing.Answers, expected.Answers) &&
		existing.Difficulty == expected.Difficulty &&
		existing.Visibility == expected.Visibility &&
		existing.IsVIP == expected.IsVIP &&
		slices.Equal(existing.Tags, expected.Tags) &&
		existing.Explanation == expected.Explanation &&
		existing.Subject == expected.Subject
}

func (r *QuizRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.QuizProblem, error) {
	query := quizSelectBase() + ` WHERE id = $1`
	rows, err := r.db.Query(ctx, query, id)
	if err != nil {
		return nil, fmt.Errorf("querying quiz %s: %w", id, err)
	}
	defer rows.Close()
	return scanSingleQuiz(rows, fmt.Sprintf("quiz %s", id))
}

func (r *QuizRepository) GetByCode(ctx context.Context, code string) (*domain.QuizProblem, error) {
	query := quizSelectBase() + ` WHERE code = $1`
	rows, err := r.db.Query(ctx, query, code)
	if err != nil {
		return nil, fmt.Errorf("querying quiz %s: %w", code, err)
	}
	defer rows.Close()
	return scanSingleQuiz(rows, fmt.Sprintf("quiz %s", code))
}

func (r *QuizRepository) List(ctx context.Context, filter QuizListFilter, page, pageSize int) ([]*domain.QuizProblem, int, error) {
	where, args := buildQuizWhere(filter)
	countQuery := "SELECT COUNT(DISTINCT qp.id) FROM quiz_problems qp" + where
	var total int
	if err := r.db.QueryRow(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("counting quizzes: %w", err)
	}
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 500 {
		pageSize = 20
	}
	args = append(args, pageSize, (page-1)*pageSize)
	query := quizSelectBaseWithAlias() + where + fmt.Sprintf(" ORDER BY qp.created_at DESC LIMIT $%d OFFSET $%d", len(args)-1, len(args))
	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("listing quizzes: %w", err)
	}
	defer rows.Close()
	items, err := scanQuizzes(rows)
	return items, total, err
}

func (r *QuizRepository) Update(ctx context.Context, q *domain.QuizProblem) error {
	options, err := json.Marshal(q.Options)
	if err != nil {
		return fmt.Errorf("marshalling quiz options: %w", err)
	}
	query := `
		UPDATE quiz_problems SET
			code=$2, title=$3, statement=$4, type=$5, code_id=$6, code_hint=$7,
			options=$8, answers=$9, difficulty=$10, visibility=$11, is_vip=$12,
			tags=$13, langs=$14, explanation=$15, subject=$16, updated_at=NOW()
		WHERE id=$1`
	tag, err := r.db.Exec(ctx, query,
		q.ID, q.Code, q.Title, q.Statement, q.Type, q.CodeID, q.CodeHint,
		options, quizStringArray(q.Answers), q.Difficulty, q.Visibility, q.IsVIP,
		quizStringArray(q.Tags), quizIntArray(q.Langs), q.Explanation, q.Subject,
	)
	if err != nil {
		return fmt.Errorf("updating quiz %s: %w", q.ID, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("quiz %s not found: %w", q.ID, sql.ErrNoRows)
	}
	return nil
}

func (r *QuizRepository) Delete(ctx context.Context, id uuid.UUID) error {
	tag, err := r.db.Exec(ctx, `DELETE FROM quiz_problems WHERE id=$1`, id)
	if err != nil {
		return fmt.Errorf("deleting quiz %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("quiz %s not found: %w", id, sql.ErrNoRows)
	}
	return nil
}

func (r *QuizRepository) LinkKnowledgePoints(ctx context.Context, quizID uuid.UUID, kpIDs []uuid.UUID) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin link knowledge points: %w", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM quiz_knowledge_points WHERE quiz_id=$1`, quizID); err != nil {
		return fmt.Errorf("clearing quiz knowledge points: %w", err)
	}
	for _, kpID := range kpIDs {
		if _, err := tx.Exec(ctx, `INSERT INTO quiz_knowledge_points (quiz_id, knowledge_point_id) VALUES ($1,$2)`, quizID, kpID); err != nil {
			return fmt.Errorf("linking knowledge point %s: %w", kpID, err)
		}
	}
	return tx.Commit(ctx)
}

func (r *QuizRepository) NextCodeForType(ctx context.Context, typ domain.QuizType) (string, error) {
	prefix := typ.CodePrefix()
	if prefix == "" {
		return "", fmt.Errorf("invalid quiz type: %s", typ)
	}
	var maxNum sql.NullInt64
	query := `SELECT MAX((substring(code from 2))::int) FROM quiz_problems WHERE code LIKE $1 AND substring(code from 2) ~ '^[0-9]+$'`
	if err := r.db.QueryRow(ctx, query, prefix+"%").Scan(&maxNum); err != nil {
		return "", fmt.Errorf("querying next quiz code: %w", err)
	}
	next := 1000
	if maxNum.Valid && int(maxNum.Int64) >= next {
		next = int(maxNum.Int64) + 1
	}
	return fmt.Sprintf("%s%d", prefix, next), nil
}

func insertQuizTx(ctx context.Context, tx pgx.Tx, q *domain.QuizProblem, onConflict string) (bool, error) {
	if q.ID == uuid.Nil {
		q.ID = uuid.New()
	}
	now := time.Now()
	if q.CreatedAt.IsZero() {
		q.CreatedAt = now
	}
	if q.UpdatedAt.IsZero() {
		q.UpdatedAt = now
	}
	options, err := json.Marshal(q.Options)
	if err != nil {
		return false, fmt.Errorf("marshalling quiz options: %w", err)
	}
	conflict := ""
	switch onConflict {
	case "", "error":
	case "skip":
		conflict = " ON CONFLICT (code) DO NOTHING"
	case "update":
		conflict = ` ON CONFLICT (code) DO UPDATE SET
			title=EXCLUDED.title, statement=EXCLUDED.statement, type=EXCLUDED.type,
			code_id=EXCLUDED.code_id, code_hint=EXCLUDED.code_hint, options=EXCLUDED.options,
			answers=EXCLUDED.answers, difficulty=EXCLUDED.difficulty, visibility=EXCLUDED.visibility,
			is_vip=EXCLUDED.is_vip, tags=EXCLUDED.tags, langs=EXCLUDED.langs,
			explanation=EXCLUDED.explanation, subject=EXCLUDED.subject, updated_at=NOW()`
	default:
		return false, fmt.Errorf("unsupported on_conflict: %s", onConflict)
	}
	query := `
		INSERT INTO quiz_problems (
			id, code, title, statement, type, code_id, code_hint, options,
			answers, difficulty, visibility, is_vip, tags, langs, explanation,
			subject, created_at, updated_at
		) VALUES (
			$1,$2,$3,$4,$5,$6,$7,$8,
			$9,$10,$11,$12,$13,$14,$15,
			$16,$17,$18
		)` + conflict
	tag, err := tx.Exec(ctx, query,
		q.ID, q.Code, q.Title, q.Statement, q.Type, q.CodeID, q.CodeHint, options,
		quizStringArray(q.Answers), q.Difficulty, q.Visibility, q.IsVIP, quizStringArray(q.Tags), quizIntArray(q.Langs), q.Explanation,
		q.Subject, q.CreatedAt, q.UpdatedAt,
	)
	if err != nil {
		return false, fmt.Errorf("bulk inserting quiz %s: %w", q.Code, err)
	}
	return tag.RowsAffected() > 0, nil
}

func quizStringArray(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func quizIntArray(values []int) []int {
	if values == nil {
		return []int{}
	}
	return values
}

func quizSelectBase() string {
	return `SELECT id, code, title, statement, type, code_id, code_hint, options,
		answers, difficulty, visibility, is_vip, tags, langs, explanation,
		subject, created_at, updated_at FROM quiz_problems`
}

func quizSelectBaseWithAlias() string {
	return `SELECT qp.id, qp.code, qp.title, qp.statement, qp.type, qp.code_id, qp.code_hint, qp.options,
		qp.answers, qp.difficulty, qp.visibility, qp.is_vip, qp.tags, qp.langs, qp.explanation,
		qp.subject, qp.created_at, qp.updated_at FROM quiz_problems qp`
}

func buildQuizWhere(filter QuizListFilter) (string, []interface{}) {
	var conditions []string
	var args []interface{}
	if filter.KnowledgePointID != nil {
		conditions = append(conditions, "EXISTS (SELECT 1 FROM quiz_knowledge_points qkp WHERE qkp.quiz_id = qp.id AND qkp.knowledge_point_id = $"+fmt.Sprint(len(args)+1)+")")
		args = append(args, *filter.KnowledgePointID)
	}
	if filter.Type != nil {
		conditions = append(conditions, fmt.Sprintf("qp.type = $%d", len(args)+1))
		args = append(args, *filter.Type)
	}
	if filter.Subject != nil {
		conditions = append(conditions, fmt.Sprintf("qp.subject = $%d", len(args)+1))
		args = append(args, *filter.Subject)
	}
	if filter.Difficulty != nil {
		conditions = append(conditions, fmt.Sprintf("qp.difficulty = $%d", len(args)+1))
		args = append(args, *filter.Difficulty)
	}
	if filter.Tag != nil {
		conditions = append(conditions, fmt.Sprintf("$%d = ANY(qp.tags)", len(args)+1))
		args = append(args, *filter.Tag)
	}
	if len(conditions) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(conditions, " AND "), args
}

func scanSingleQuiz(rows pgx.Rows, label string) (*domain.QuizProblem, error) {
	items, err := scanQuizzes(rows)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("%s not found: %w", label, sql.ErrNoRows)
	}
	return items[0], nil
}

func scanQuizzes(rows pgx.Rows) ([]*domain.QuizProblem, error) {
	var out []*domain.QuizProblem
	for rows.Next() {
		q := &domain.QuizProblem{}
		var optionsJSON []byte
		if err := rows.Scan(
			&q.ID, &q.Code, &q.Title, &q.Statement, &q.Type, &q.CodeID, &q.CodeHint, &optionsJSON,
			&q.Answers, &q.Difficulty, &q.Visibility, &q.IsVIP, &q.Tags, &q.Langs, &q.Explanation,
			&q.Subject, &q.CreatedAt, &q.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning quiz: %w", err)
		}
		if len(optionsJSON) > 0 && string(optionsJSON) != "null" {
			if err := json.Unmarshal(optionsJSON, &q.Options); err != nil {
				return nil, fmt.Errorf("unmarshalling quiz options: %w", err)
			}
		}
		out = append(out, q)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating quizzes: %w", err)
	}
	return out, nil
}
