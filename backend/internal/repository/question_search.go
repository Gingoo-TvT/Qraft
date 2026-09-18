package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/jackc/pgx/v5/pgxpool"
)

type QuestionSearchRepository struct {
	db      *pgxpool.Pool
	ratings *RatingRepository
}

func NewQuestionSearchRepository(db *pgxpool.Pool) *QuestionSearchRepository {
	return &QuestionSearchRepository{db: db}
}

// One statement gives the total and page the same database snapshot, including
// empty pages. Only searchable metadata is read; statements and answers are not
// sent through this endpoint. No extra search index or service is required.
const questionSearchQuery = `
WITH candidates AS (
    SELECT p.id, 'problem' AS source, 'programming' AS type,
        p.serial_number::text AS code, p.title, COALESCE(p.tags, '{}'::text[]) AS tags,
        ARRAY(
            SELECT DISTINCT COALESCE(NULLIF(tc.display_name, ''), tag.name)
            FROM unnest(COALESCE(p.tags, '{}'::text[])) AS tag(name)
            LEFT JOIN tag_categories tc ON lower(tc.tag_name)=lower(tag.name)
            ORDER BY 1
        ) AS knowledge_points,
        COALESCE(p.tags, '{}'::text[]) AS knowledge_codes,
        p.difficulty, '' AS quiz_difficulty, p.level::text AS level, p.status, p.updated_at
    FROM problems p
    WHERE p.status NOT IN ('quarantined','rejected')
        AND NOT EXISTS (SELECT 1 FROM problem_quarantine_records qr WHERE qr.problem_id=p.id)
    UNION ALL
    SELECT q.id, 'quiz', q.type, q.code, q.title, COALESCE(q.tags, '{}'::text[]),
        ARRAY(
            SELECT kp.name FROM quiz_knowledge_points qkp
            JOIN knowledge_points kp ON kp.id=qkp.knowledge_point_id
            WHERE qkp.quiz_id=q.id ORDER BY kp.name, kp.id
        ),
        ARRAY(
            SELECT kp.code FROM quiz_knowledge_points qkp
            JOIN knowledge_points kp ON kp.id=qkp.knowledge_point_id
            WHERE qkp.quiz_id=q.id ORDER BY kp.code, kp.id
        ),
        NULL::integer, q.difficulty, '', '', q.updated_at
    FROM quiz_problems q
    WHERE q.type IN ('programming','choice','fill_blank','judge')
), matched AS (
    SELECT c.* FROM candidates c
    WHERE ($1='' OR strpos(lower(c.id::text),lower($1))>0
        OR strpos(lower(c.code),lower($1))>0
        OR strpos(lower(c.title),lower($1))>0
        OR EXISTS (SELECT 1 FROM unnest(c.tags || c.knowledge_points || c.knowledge_codes) term
                   WHERE strpos(lower(term),lower($1))>0))
      AND ($2='' OR c.type=$2)
      AND ($3='' OR EXISTS (
          SELECT 1 FROM unnest(c.tags || CASE WHEN c.source='problem' THEN c.knowledge_points ELSE '{}'::text[] END) tag
          WHERE strpos(lower(tag),lower($3))>0))
      AND ($4='' OR EXISTS (
          SELECT 1 FROM unnest(c.knowledge_points || c.knowledge_codes) point
          WHERE strpos(lower(point),lower($4))>0))
      AND ($5::integer IS NULL OR c.difficulty >= $5)
      AND ($6::integer IS NULL OR c.difficulty <= $6)
      AND ($7='' OR c.quiz_difficulty=$7)
), page AS (
    SELECT id,source,type,code,title,tags,knowledge_points,difficulty,
        quiz_difficulty,level,status,updated_at
    FROM matched ORDER BY updated_at DESC,source,id LIMIT $8 OFFSET $9
)
SELECT COALESCE(
    (SELECT jsonb_agg(page ORDER BY updated_at DESC,source,id) FROM page), '[]'::jsonb
), (SELECT count(*) FROM matched)
`

func (r *QuestionSearchRepository) Search(ctx context.Context, filter domain.QuestionSearchFilter) (domain.QuestionSearchResult, error) {
	filter, err := filter.Normalize()
	if err != nil {
		return domain.QuestionSearchResult{}, err
	}
	result := domain.QuestionSearchResult{
		Items: make([]domain.QuestionSearchItem, 0), Page: filter.Page, Size: filter.Size,
	}
	var data []byte
	query := questionSearchQuery
	var limit any = filter.Size
	offset := (filter.Page - 1) * filter.Size
	if filter.RatingBasis == "official" {
		if r.ratings == nil {
			return result, fmt.Errorf("正式评级暂不可用")
		}
		query = strings.Replace(query, "p.difficulty,", "ro.rating AS difficulty,", 1)
		query = strings.Replace(query, "FROM problems p", "FROM problems p JOIN rating_official ro ON ro.problem_id=p.id", 1)
		// Verify candidates before counting/pagination; stale rows must not produce
		// phantom totals or sparse pages. SQL has already applied numeric filters.
		limit = 201
		offset = 0
	}
	err = r.db.QueryRow(ctx, query,
		filter.Keyword, string(filter.Type), filter.Tag, filter.KnowledgePoint,
		filter.MinDifficulty, filter.MaxDifficulty, string(filter.QuizDifficulty),
		limit, offset,
	).Scan(&data, &result.Total)
	if err != nil {
		return result, fmt.Errorf("searching questions: %w", err)
	}
	if err := json.Unmarshal(data, &result.Items); err != nil {
		return result, fmt.Errorf("decoding question search: %w", err)
	}
	if filter.RatingBasis == "official" {
		if result.Total > 200 {
			return result, fmt.Errorf("正式评级候选超过 200 道，请增加关键词、标签或缩小分数范围后再搜索")
		}
		verified := make([]domain.QuestionSearchItem, 0, len(result.Items))
		for _, item := range result.Items {
			if item.Source == "problem" {
				official, err := r.ratings.CurrentOfficial(ctx, item.ID)
				if err != nil {
					return result, err
				}
				if official == nil || (filter.MinDifficulty != nil && official.Rating < *filter.MinDifficulty) || (filter.MaxDifficulty != nil && official.Rating > *filter.MaxDifficulty) {
					continue
				}
				value := official.Rating
				item.Difficulty = &value
				item.RatingBasis = "official"
			}
			verified = append(verified, item)
		}
		result.Total = len(verified)
		start := (filter.Page - 1) * filter.Size
		if start > len(verified) {
			start = len(verified)
		}
		end := start + filter.Size
		if end > len(verified) {
			end = len(verified)
		}
		result.Items = verified[start:end]
	} else {
		for i := range result.Items {
			if result.Items[i].Source == "problem" {
				result.Items[i].RatingBasis = "target"
			}
		}
	}
	return result, nil
}

func (r *QuestionSearchRepository) SetRatingRepository(ratings *RatingRepository) {
	r.ratings = ratings
}
