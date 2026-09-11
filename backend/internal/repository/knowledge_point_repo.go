package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/jackc/pgx/v5/pgxpool"
)

type KnowledgePointRepository struct {
	db *pgxpool.Pool
}

func NewKnowledgePointRepository(db *pgxpool.Pool) *KnowledgePointRepository {
	return &KnowledgePointRepository{db: db}
}

func (r *KnowledgePointRepository) ListBySubject(ctx context.Context, subject string) ([]*domain.KnowledgePoint, error) {
	query := `
		SELECT id, subject, code, name, parent_id, sort_order, created_at
		FROM knowledge_points
		WHERE ($1 = '' OR subject = $1)
		ORDER BY subject ASC, sort_order ASC, name ASC`
	rows, err := r.db.Query(ctx, query, subject)
	if err != nil {
		return nil, fmt.Errorf("querying knowledge points: %w", err)
	}
	defer rows.Close()
	return scanKnowledgePoints(rows)
}

func (r *KnowledgePointRepository) GetByCode(ctx context.Context, code string) (*domain.KnowledgePoint, error) {
	query := `
		SELECT id, subject, code, name, parent_id, sort_order, created_at
		FROM knowledge_points
		WHERE code = $1`
	var kp domain.KnowledgePoint
	err := r.db.QueryRow(ctx, query, code).Scan(
		&kp.ID, &kp.Subject, &kp.Code, &kp.Name, &kp.ParentID, &kp.SortOrder, &kp.CreatedAt,
	)
	if err != nil {
		if err.Error() == "no rows in result set" {
			return nil, fmt.Errorf("knowledge point %q not found: %w", code, sql.ErrNoRows)
		}
		return nil, fmt.Errorf("querying knowledge point %q: %w", code, err)
	}
	return &kp, nil
}

func (r *KnowledgePointRepository) GetByCodes(ctx context.Context, codes []string) ([]*domain.KnowledgePoint, error) {
	if len(codes) == 0 {
		return nil, nil
	}
	placeholders := make([]string, len(codes))
	args := make([]interface{}, len(codes))
	for i, code := range codes {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = code
	}
	query := fmt.Sprintf(`
		SELECT id, subject, code, name, parent_id, sort_order, created_at
		FROM knowledge_points
		WHERE code IN (%s)
		ORDER BY sort_order ASC, name ASC`, strings.Join(placeholders, ","))
	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying knowledge points by codes: %w", err)
	}
	defer rows.Close()
	points, err := scanKnowledgePoints(rows)
	if err != nil {
		return nil, err
	}
	if len(points) != len(codes) {
		found := make(map[string]struct{}, len(points))
		for _, kp := range points {
			found[kp.Code] = struct{}{}
		}
		var missing []string
		for _, code := range codes {
			if _, ok := found[code]; !ok {
				missing = append(missing, code)
			}
		}
		return nil, fmt.Errorf("knowledge points not found: %s", strings.Join(missing, ", "))
	}
	return points, nil
}

type knowledgePointRows interface {
	Next() bool
	Scan(dest ...interface{}) error
	Err() error
}

func scanKnowledgePoints(rows knowledgePointRows) ([]*domain.KnowledgePoint, error) {
	var out []*domain.KnowledgePoint
	for rows.Next() {
		var kp domain.KnowledgePoint
		if err := rows.Scan(&kp.ID, &kp.Subject, &kp.Code, &kp.Name, &kp.ParentID, &kp.SortOrder, &kp.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning knowledge point: %w", err)
		}
		out = append(out, &kp)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating knowledge points: %w", err)
	}
	return out, nil
}
