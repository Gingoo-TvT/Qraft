package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var ErrProblemSetAssemblyChanged = errors.New("题库中的题目已发生变化，请重新预览后保存")

// Only lightweight metadata crosses the repository boundary. The same query
// validates submitted preview references, including recent-set exclusions.
func (r *ProblemSetRepository) ListAssemblyCandidates(ctx context.Context, filter domain.ProblemSetAssemblyFilter, types []domain.QuizType) ([]domain.ProblemSetAssemblyCandidate, error) {
	typeNames := make([]string, len(types))
	for i, typ := range types {
		typeNames[i] = string(typ)
	}
	rows, err := r.db.Query(ctx, `
 WITH recent_sets AS (
  SELECT id FROM problem_sets ORDER BY created_at DESC, id DESC LIMIT $7
 ), recent AS (
  SELECT p.id, 'programming' AS type,
   md5(regexp_replace(trim(p.statement), '[[:space:]]+', ' ', 'g')) AS fingerprint
  FROM problem_set_items i JOIN recent_sets s ON s.id=i.set_id JOIN problems p ON p.id=i.problem_id
  UNION ALL
  SELECT q.id, q.type,
   md5(regexp_replace(trim(q.statement), '[[:space:]]+', ' ', 'g') || COALESCE(q.options::text, '[]'))
  FROM problem_set_items i JOIN recent_sets s ON s.id=i.set_id JOIN quiz_problems q ON q.id=i.quiz_id
 ), candidates AS (
  SELECT p.id, 'programming' AS type, p.updated_at, p.serial_number::text AS code, p.title,
   p.difficulty, '' AS quiz_difficulty, COALESCE(p.tags, '{}'::text[]) AS tags,
   md5(regexp_replace(trim(p.statement), '[[:space:]]+', ' ', 'g')) AS fingerprint,
   p.statement
  FROM problems p
  WHERE p.status='published' AND btrim(p.statement) <> ''
   AND NOT EXISTS (SELECT 1 FROM problem_quarantine_records qr WHERE qr.problem_id=p.id)
   AND p.difficulty BETWEEN $4 AND $5
   AND 'programming'=ANY($1::text[])
  UNION ALL
  SELECT q.id, q.type, q.updated_at, q.code, q.title, 0, q.difficulty,
   COALESCE(q.tags, '{}'::text[]) || ARRAY(
    SELECT kp.name FROM quiz_knowledge_points qkp JOIN knowledge_points kp ON kp.id=qkp.knowledge_point_id WHERE qkp.quiz_id=q.id
   ),
   md5(regexp_replace(trim(q.statement), '[[:space:]]+', ' ', 'g') || COALESCE(q.options::text, '[]')),
   q.statement
  FROM quiz_problems q
  WHERE q.type IN ('choice','fill_blank','judge') AND q.type=ANY($1::text[])
   AND btrim(q.statement) <> '' AND cardinality(q.answers)>0
   AND ($6='' OR q.difficulty=$6)
 )
 SELECT c.id,c.type,c.updated_at,c.code,c.title,c.difficulty,c.quiz_difficulty,c.tags,c.fingerprint
 FROM candidates c
 WHERE (cardinality($2::text[])=0 OR EXISTS (SELECT 1 FROM unnest(c.tags) tag WHERE lower(trim(tag))=ANY($2::text[])))
  AND ($3='' OR strpos(lower(c.title || ' ' || c.statement),lower($3))>0)
  AND NOT EXISTS (SELECT 1 FROM recent r WHERE r.type=c.type AND (r.id=c.id OR r.fingerprint=c.fingerprint))
 ORDER BY c.type,c.id`, typeNames, filter.Tags, filter.Keyword, filter.MinDifficulty, filter.MaxDifficulty, filter.QuizDifficulty, filter.ExcludeRecentSets)
	if err != nil {
		return nil, fmt.Errorf("listing assembly candidates: %w", err)
	}
	defer rows.Close()
	out := make([]domain.ProblemSetAssemblyCandidate, 0)
	for rows.Next() {
		var item domain.ProblemSetAssemblyCandidate
		if err := rows.Scan(&item.ID, &item.Type, &item.UpdatedAt, &item.Code, &item.Title, &item.Difficulty, &item.QuizDifficulty, &item.Tags, &item.Fingerprint); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// Source revisions are locked before inserting anything. A stale preview or an
// insertion failure therefore cannot leave a half-created paper.
func (r *ProblemSetRepository) CreateAssembled(ctx context.Context, set *domain.ProblemSet, refs []domain.ProblemSetAssemblyRef) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for _, ref := range refs {
		var id uuid.UUID
		if ref.Type == domain.QuizTypeProgramming {
			err = tx.QueryRow(ctx, `SELECT p.id FROM problems p WHERE p.id=$1 AND p.updated_at=$2 AND p.status='published'
    AND NOT EXISTS (SELECT 1 FROM problem_quarantine_records q WHERE q.problem_id=p.id) FOR SHARE OF p`, ref.ID, ref.UpdatedAt).Scan(&id)
		} else {
			err = tx.QueryRow(ctx, `SELECT id FROM quiz_problems WHERE id=$1 AND updated_at=$2 AND type=$3 FOR SHARE`, ref.ID, ref.UpdatedAt, ref.Type).Scan(&id)
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrProblemSetAssemblyChanged
		}
		if err != nil {
			return err
		}
	}
	if err := createProblemSet(ctx, tx, set); err != nil {
		return err
	}
	for i := range set.Items {
		item := &set.Items[i]
		item.ID, item.SetID = uuid.New(), set.ID
		_, err = tx.Exec(ctx, `INSERT INTO problem_set_items(id,set_id,problem_id,quiz_id,position,score,section,notes) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`,
			item.ID, set.ID, nullableUUID(item.ProblemID), nullableUUID(item.QuizID), item.Position, item.Score, item.Section, item.Notes)
		if err != nil {
			return fmt.Errorf("saving assembled item: %w", err)
		}
	}
	return tx.Commit(ctx)
}
