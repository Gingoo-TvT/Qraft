package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TagRepository provides data access operations for the tag taxonomy.
type TagRepository struct {
	db *pgxpool.Pool
}

// ErrTagsNotValidForLevel distinguishes a contradictory product request from
// a repository outage while keeping the detailed invalid tags in the wrapper.
var ErrTagsNotValidForLevel = errors.New("tags are not valid for problem level")

// NewTagRepository creates a new TagRepository backed by the given connection
// pool.
func NewTagRepository(db *pgxpool.Pool) *TagRepository {
	return &TagRepository{db: db}
}

// GetAll retrieves every tag category ordered by sort_order.
func (r *TagRepository) GetAll(ctx context.Context) ([]domain.TagCategory, error) {
	query := `
		SELECT id, level, tag_name, display_name, description, sort_order, min_difficulty, max_difficulty
		FROM tag_categories
		ORDER BY sort_order ASC`

	rows, err := r.db.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("querying all tag categories: %w", err)
	}
	defer rows.Close()

	return scanTagCategories(rows)
}

// GetByLevel retrieves tag categories applicable to the specified problem level,
// ordered by sort_order.
func (r *TagRepository) GetByLevel(ctx context.Context, level domain.ProblemLevel) ([]domain.TagCategory, error) {
	query := `
		SELECT id, level, tag_name, display_name, description, sort_order, min_difficulty, max_difficulty
		FROM tag_categories
		WHERE level = $1
		ORDER BY sort_order ASC`

	rows, err := r.db.Query(ctx, query, level)
	if err != nil {
		return nil, fmt.Errorf("querying tag categories for level %s: %w", level, err)
	}
	defer rows.Close()

	return scanTagCategories(rows)
}

// GetByTagName looks up a single tag category by its tag_name. Returns
// sql.ErrNoRows wrapped in a descriptive error if the tag does not exist.
func (r *TagRepository) GetByTagName(ctx context.Context, name string) (*domain.TagCategory, error) {
	query := `
		SELECT id, level, tag_name, display_name, description, sort_order, min_difficulty, max_difficulty
		FROM tag_categories
		WHERE tag_name = $1`

	var cat domain.TagCategory
	err := r.db.QueryRow(ctx, query, name).Scan(
		&cat.ID,
		&cat.Level,
		&cat.TagName,
		&cat.DisplayName,
		&cat.Description,
		&cat.SortOrder,
		&cat.MinDifficulty,
		&cat.MaxDifficulty,
	)
	if err != nil {
		if err.Error() == "no rows in result set" {
			return nil, fmt.Errorf("tag %q not found: %w", name, sql.ErrNoRows)
		}
		return nil, fmt.Errorf("querying tag %q: %w", name, err)
	}

	return &cat, nil
}

// ValidateTagsForLevel checks that every tag in the provided slice exists in
// the database and is applicable to the given problem level. It returns a
// descriptive error listing any invalid tags.
func (r *TagRepository) ValidateTagsForLevel(ctx context.Context, level domain.ProblemLevel, tags []string) error {
	if len(tags) == 0 {
		return nil
	}

	placeholders := make([]string, len(tags))
	args := make([]interface{}, 0, len(tags)+1)
	args = append(args, level)

	for i, tag := range tags {
		placeholders[i] = fmt.Sprintf("$%d", i+2)
		args = append(args, tag)
	}

	query := fmt.Sprintf(`
		SELECT tag_name
		FROM tag_categories
		WHERE level = $1
		  AND tag_name IN (%s)`,
		strings.Join(placeholders, ", "))

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("validating tags for level %s: %w", level, err)
	}
	defer rows.Close()

	validTags := make(map[string]struct{})
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return fmt.Errorf("scanning valid tag name: %w", err)
		}
		validTags[name] = struct{}{}
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterating valid tags: %w", err)
	}

	var invalid []string
	for _, tag := range tags {
		if _, ok := validTags[tag]; !ok {
			invalid = append(invalid, tag)
		}
	}

	if len(invalid) > 0 {
		return fmt.Errorf(
			"%w %s: %s",
			ErrTagsNotValidForLevel,
			level,
			strings.Join(invalid, ", "),
		)
	}

	return nil
}

// GetByLevelAndDifficulty retrieves tag categories for the given level that are
// appropriate for the specified difficulty rating.
func (r *TagRepository) GetByLevelAndDifficulty(ctx context.Context, level domain.ProblemLevel, difficulty int) ([]domain.TagCategory, error) {
	query := `
		SELECT id, level, tag_name, display_name, description, sort_order, min_difficulty, max_difficulty
		FROM tag_categories
		WHERE level = $1
		  AND min_difficulty <= $2
		  AND max_difficulty >= $2
		ORDER BY sort_order ASC`

	rows, err := r.db.Query(ctx, query, level, difficulty)
	if err != nil {
		return nil, fmt.Errorf("querying tags for level %s difficulty %d: %w", level, difficulty, err)
	}
	defer rows.Close()

	return scanTagCategories(rows)
}

// pgxRows is a minimal interface satisfied by pgx query result rows.
type pgxRows interface {
	Next() bool
	Scan(dest ...interface{}) error
	Err() error
}

// scanTagCategories scans rows into a slice of TagCategory.
func scanTagCategories(rows pgxRows) ([]domain.TagCategory, error) {
	var categories []domain.TagCategory

	for rows.Next() {
		var cat domain.TagCategory
		if err := rows.Scan(
			&cat.ID,
			&cat.Level,
			&cat.TagName,
			&cat.DisplayName,
			&cat.Description,
			&cat.SortOrder,
			&cat.MinDifficulty,
			&cat.MaxDifficulty,
		); err != nil {
			return nil, fmt.Errorf("scanning tag category row: %w", err)
		}
		categories = append(categories, cat)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating tag category rows: %w", err)
	}

	return categories, nil
}
