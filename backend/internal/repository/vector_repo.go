package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lib/pq"
	"github.com/pgvector/pgvector-go"
)

const (
	EmbeddingKindStatement = "statement"
	EmbeddingKindSolution  = "solution"

	S5OriginalityCorpusSchemaV1 = "algoforge.s5-originality-corpus.v1"
	MaxS5OriginalityTopKV1      = 100
	maxS5ExcludedLineageIDsV1   = 1024
)

var (
	ErrConfiguredStatementModelVersionRequired = errors.New("configured statement model version required")
	ErrS5CorpusRevisionUnavailable             = errors.New("S5 originality corpus revision unavailable")
)

// EmbeddingWrite identifies one content-addressed embedding row in a model
// version/kind vector space.
type EmbeddingWrite struct {
	ProblemID      uuid.UUID
	ModelVersionID uuid.UUID
	Kind           string
	ContentHash    string
	Embedding      []float32
}

// S5OriginalityQueryV1 is an additive, fail-closed query contract for concept
// selection. ExcludedProblemIDs is supplied by the server from the current S5
// lineage; callers must not derive it from model output.
type S5OriginalityQueryV1 struct {
	ModelVersionID     uuid.UUID
	Kind               string
	Embedding          []float32
	TopK               int
	ExcludedProblemIDs []uuid.UUID
}

type S5OriginalityNeighborV1 struct {
	ProblemID   uuid.UUID
	ContentHash string
	CorpusTier  string
	Similarity  float64
}

// S5OriginalityResultV1 distinguishes a successful empty-corpus query from a
// failed query without overloading a nil slice. CorpusRevision binds the exact
// effective corpus (including lineage exclusion) observed by this query.
type S5OriginalityResultV1 struct {
	CorpusRevision string
	CorpusSize     int
	NeighborCount  int
	Neighbors      []S5OriginalityNeighborV1
}

// VectorRepository provides vector similarity search operations backed by
// pgvector. It stores and queries problem embeddings to find semantically
// similar problems, which is used for deduplication checks and "related
// problems" recommendations.
type VectorRepository struct {
	db                         *pgxpool.Pool
	statementModelVersionID    uuid.UUID
	statementModelVersionBound bool
}

// NewVectorRepository creates a new VectorRepository backed by the given
// connection pool. The database must have the pgvector extension installed
// and the versioned problem_embeddings schema created.
func NewVectorRepository(db *pgxpool.Pool) *VectorRepository {
	return &VectorRepository{db: db}
}

// BindStatementModelVersion returns a repository view whose ordinary
// statement reads and writes are pinned to the deployment-configured model
// version. The base repository remains unbound for embedding administration.
func (r *VectorRepository) BindStatementModelVersion(modelVersionID uuid.UUID) (*VectorRepository, error) {
	if r == nil {
		return nil, fmt.Errorf("vector repository is required")
	}
	if modelVersionID == uuid.Nil {
		return nil, ErrConfiguredStatementModelVersionRequired
	}
	bound := *r
	bound.statementModelVersionID = modelVersionID
	bound.statementModelVersionBound = true
	return &bound, nil
}

// RequireStatementModelVersion returns a fail-closed runtime view. It lets the
// API keep embedding administration available when the deployment pin is
// absent without allowing product similarity calls to fall back to the DB
// active pointer.
func (r *VectorRepository) RequireStatementModelVersion() *VectorRepository {
	if r == nil {
		return &VectorRepository{statementModelVersionBound: true}
	}
	required := *r
	required.statementModelVersionID = uuid.Nil
	required.statementModelVersionBound = true
	return &required
}

// StatementModelVersion resolves the runtime statement identity. A bound
// repository always returns its configured version and never re-reads the
// mutable active pointer.
func (r *VectorRepository) StatementModelVersion(ctx context.Context) (uuid.UUID, error) {
	if r != nil && r.statementModelVersionBound {
		if r.statementModelVersionID == uuid.Nil {
			return uuid.Nil, ErrConfiguredStatementModelVersionRequired
		}
		return r.statementModelVersionID, nil
	}
	return r.ActiveModelVersion(ctx, EmbeddingKindStatement)
}

// ActiveModelVersion resolves the DB active pointer for a specific embedding
// kind. The pointer is the compatibility boundary for legacy callers; the
// versioned methods below require callers to pass the resolved version
// explicitly.
func (r *VectorRepository) ActiveModelVersion(ctx context.Context, kind string) (uuid.UUID, error) {
	kind = normalizeEmbeddingKind(kind)
	if err := validateEmbeddingKind(kind); err != nil {
		return uuid.Nil, err
	}

	var modelVersionID uuid.UUID
	query := `
		SELECT model_version_id
		FROM embedding_active_pointers
		WHERE embedding_kind = $1`
	if err := r.db.QueryRow(ctx, query, kind).Scan(&modelVersionID); err != nil {
		if err == sql.ErrNoRows {
			return uuid.Nil, fmt.Errorf("active embedding model for kind %q not found: %w", kind, sql.ErrNoRows)
		}
		return uuid.Nil, fmt.Errorf("querying active embedding model for kind %q: %w", kind, err)
	}
	return modelVersionID, nil
}

// VerifyActiveModelVersion compares a deployment-declared expected model
// version with the DB active pointer. Empty expected values intentionally mean
// "no deployment pin" for local development and shadow work.
func (r *VectorRepository) VerifyActiveModelVersion(ctx context.Context, kind, expected string) error {
	expected = strings.TrimSpace(expected)
	if expected == "" {
		return nil
	}
	expectedID, err := uuid.Parse(expected)
	if err != nil {
		return fmt.Errorf("expected active embedding model version for kind %q must be a UUID: %w", kind, err)
	}
	activeID, err := r.ActiveModelVersion(ctx, kind)
	if err != nil {
		return err
	}
	if activeID != expectedID {
		return fmt.Errorf(
			"active embedding model version mismatch for kind %q: database=%s expected=%s",
			normalizeEmbeddingKind(kind),
			activeID,
			expectedID,
		)
	}
	return nil
}

// UpdateEmbedding stores a statement embedding through the active DB pointer.
// Prefer UpdateStatementEmbedding when the caller has the canonical statement
// content hash; this legacy wrapper derives a deterministic hash from the
// vector for old call sites that only provide the embedding.
func (r *VectorRepository) UpdateEmbedding(ctx context.Context, problemID uuid.UUID, embedding []float32) error {
	return r.UpdateStatementEmbedding(ctx, problemID, legacyEmbeddingContentHash(problemID, embedding), embedding)
}

// UpdateStatementEmbedding stores a canonical statement embedding in the
// currently active statement vector space.
func (r *VectorRepository) UpdateStatementEmbedding(
	ctx context.Context,
	problemID uuid.UUID,
	contentHash string,
	embedding []float32,
) error {
	modelVersionID, err := r.StatementModelVersion(ctx)
	if err != nil {
		return err
	}
	return r.UpdateEmbeddingForVersion(ctx, EmbeddingWrite{
		ProblemID:      problemID,
		ModelVersionID: modelVersionID,
		Kind:           EmbeddingKindStatement,
		ContentHash:    contentHash,
		Embedding:      embedding,
	})
}

// UpdateEmbeddingForVersion stores or replaces a content-addressed vector in
// one explicit model version/kind space.
func (r *VectorRepository) UpdateEmbeddingForVersion(ctx context.Context, input EmbeddingWrite) error {
	kind := normalizeEmbeddingKind(input.Kind)
	if err := validateEmbeddingWrite(input, kind); err != nil {
		return err
	}

	vec := pgvector.NewVector(input.Embedding)
	query := `
		INSERT INTO problem_embeddings (
			problem_id,
			model_version_id,
			embedding_kind,
			content_hash,
			embedding,
			updated_at
		)
		VALUES ($1, $2, $3, $4, $5, NOW())
		ON CONFLICT (problem_id, model_version_id, embedding_kind, content_hash)
		DO UPDATE SET
			embedding = EXCLUDED.embedding,
			updated_at = EXCLUDED.updated_at`

	_, err := r.db.Exec(
		ctx,
		query,
		input.ProblemID,
		input.ModelVersionID,
		kind,
		input.ContentHash,
		vec,
	)
	if err != nil {
		return fmt.Errorf(
			"upserting %s embedding for problem %s model version %s: %w",
			kind,
			input.ProblemID,
			input.ModelVersionID,
			err,
		)
	}

	return nil
}

// GetEmbedding returns the latest statement vector stored for a problem in
// the currently active statement model version.
func (r *VectorRepository) GetEmbedding(ctx context.Context, problemID uuid.UUID) ([]float32, error) {
	modelVersionID, err := r.StatementModelVersion(ctx)
	if err != nil {
		return nil, err
	}
	return r.GetEmbeddingForVersion(ctx, problemID, modelVersionID, EmbeddingKindStatement)
}

// GetEmbeddingForVersion returns the latest vector stored for a problem in one
// explicit model version/kind space.
func (r *VectorRepository) GetEmbeddingForVersion(
	ctx context.Context,
	problemID uuid.UUID,
	modelVersionID uuid.UUID,
	kind string,
) ([]float32, error) {
	kind = normalizeEmbeddingKind(kind)
	if err := validateProblemVersionKind(problemID, modelVersionID, kind); err != nil {
		return nil, err
	}

	var vec pgvector.Vector
	query := `
		SELECT embedding
		FROM problem_embeddings
		WHERE problem_id = $1
		  AND model_version_id = $2
		  AND embedding_kind = $3
		ORDER BY updated_at DESC, created_at DESC, content_hash DESC
		LIMIT 1`
	if err := r.db.QueryRow(ctx, query, problemID, modelVersionID, kind).Scan(&vec); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf(
				"%s embedding for problem %s model version %s not found: %w",
				kind,
				problemID,
				modelVersionID,
				sql.ErrNoRows,
			)
		}
		return nil, fmt.Errorf(
			"querying %s embedding for problem %s model version %s: %w",
			kind,
			problemID,
			modelVersionID,
			err,
		)
	}
	return vec.Slice(), nil
}

// FindSimilarExcept performs the same cosine search as FindSimilar while
// excluding a specific problem ID.
func (r *VectorRepository) FindSimilarExcept(
	ctx context.Context,
	queryVec []float32,
	excludeID uuid.UUID,
	limit int,
	threshold float64,
) ([]*domain.Problem, []float64, error) {
	modelVersionID, err := r.StatementModelVersion(ctx)
	if err != nil {
		return nil, nil, err
	}
	return r.FindSimilarExceptForVersion(
		ctx,
		queryVec,
		modelVersionID,
		EmbeddingKindStatement,
		excludeID,
		limit,
		threshold,
	)
}

// FindSimilarExceptForVersion performs a cosine search in one explicit model
// version/kind space while excluding a problem ID.
func (r *VectorRepository) FindSimilarExceptForVersion(
	ctx context.Context,
	queryVec []float32,
	modelVersionID uuid.UUID,
	kind string,
	excludeID uuid.UUID,
	limit int,
	threshold float64,
) ([]*domain.Problem, []float64, error) {
	if excludeID == uuid.Nil {
		return nil, nil, fmt.Errorf("exclude problem ID is required")
	}
	limit, distanceThreshold, kind, err := validateSimilaritySearch(queryVec, modelVersionID, kind, limit, threshold)
	if err != nil {
		return nil, nil, err
	}

	vec := pgvector.NewVector(queryVec)
	query := findSimilarExceptQuery()

	rows, err := r.db.Query(ctx, query, vec, modelVersionID, kind, excludeID, distanceThreshold, limit)
	if err != nil {
		return nil, nil, fmt.Errorf("querying similar problems: %w", err)
	}
	defer rows.Close()

	return scanSimilarProblemRows(rows)
}

// FindSimilar performs a cosine similarity search against the active statement
// vector space. Results are filtered to only include problems whose cosine
// similarity meets or exceeds the given threshold.
func (r *VectorRepository) FindSimilar(
	ctx context.Context,
	queryVec []float32,
	limit int,
	threshold float64,
) ([]*domain.Problem, []float64, error) {
	modelVersionID, err := r.StatementModelVersion(ctx)
	if err != nil {
		return nil, nil, err
	}
	return r.FindSimilarForVersion(ctx, queryVec, modelVersionID, EmbeddingKindStatement, limit, threshold)
}

// FindSimilarForVersion performs a cosine similarity search in one explicit
// model version/kind space. A nil model version is rejected before any DB work,
// so callers cannot accidentally mix spaces by querying only by dimension.
func (r *VectorRepository) FindSimilarForVersion(
	ctx context.Context,
	queryVec []float32,
	modelVersionID uuid.UUID,
	kind string,
	limit int,
	threshold float64,
) ([]*domain.Problem, []float64, error) {
	limit, distanceThreshold, kind, err := validateSimilaritySearch(queryVec, modelVersionID, kind, limit, threshold)
	if err != nil {
		return nil, nil, err
	}

	vec := pgvector.NewVector(queryVec)
	query := findSimilarQuery()

	rows, err := r.db.Query(ctx, query, vec, modelVersionID, kind, distanceThreshold, limit)
	if err != nil {
		return nil, nil, fmt.Errorf("querying similar problems: %w", err)
	}
	defer rows.Close()

	return scanSimilarProblemRows(rows)
}

// QueryS5OriginalityV1 returns a global Top-K across the published,
// in-flight-candidate, and quarantine advisory tiers. It intentionally does
// not change the filtering or threshold semantics of the legacy similarity
// methods above.
func (r *VectorRepository) QueryS5OriginalityV1(
	ctx context.Context,
	input S5OriginalityQueryV1,
) (S5OriginalityResultV1, error) {
	normalized, err := normalizeS5OriginalityQueryV1(input)
	if err != nil {
		return S5OriginalityResultV1{}, err
	}
	if r == nil || r.db == nil {
		return S5OriginalityResultV1{}, fmt.Errorf("vector repository is required")
	}

	rows, err := r.db.Query(
		ctx,
		s5OriginalityQueryV1SQL(),
		pgvector.NewVector(normalized.Embedding),
		normalized.ModelVersionID,
		normalized.Kind,
		normalized.ExcludedProblemIDs,
		normalized.TopK,
		S5OriginalityCorpusSchemaV1,
	)
	if err != nil {
		return S5OriginalityResultV1{}, fmt.Errorf("querying S5 originality corpus: %w", err)
	}
	defer rows.Close()

	return scanS5OriginalityRowsV1(rows, normalized)
}

func s5OriginalityQueryV1SQL() string {
	return `
		WITH corpus AS MATERIALIZED (
			SELECT
				pe.problem_id,
				pe.content_hash,
				pe.embedding,
				encode(digest(convert_to(pe.embedding::text, 'UTF8'), 'sha256'), 'hex') AS vector_sha256,
				CASE
					WHEN p.status IN ('quarantined', 'rejected')
					  OR COALESCE(p.metadata_json ->> 'stale', 'false') = 'true'
					  OR COALESCE(pe.metadata_json ->> 'stale', 'false') = 'true'
					  OR EXISTS (
						  SELECT 1 FROM problem_quarantine_records quarantine
						  WHERE quarantine.problem_id = p.id
					  )
					THEN 'quarantine_advisory'
					WHEN p.status = 'published' THEN 'historical_advisory'
					ELSE 'selection_candidate'
				END AS corpus_tier
			FROM problem_embeddings pe
			INNER JOIN problems p ON p.id = pe.problem_id
			WHERE pe.model_version_id = $2
			  AND pe.embedding_kind = $3
			  AND pe.content_hash = embedding_statement_content_hash(p.title, p.statement, p.one_line_hint)
			  AND NOT (pe.problem_id = ANY(COALESCE($4::uuid[], ARRAY[]::uuid[])))
		),
		revision AS MATERIALIZED (
			SELECT
				encode(
					digest(
						convert_to(
							$6::text || E'\n' || $2::text || E'\n' || $3::text || E'\n' ||
							COALESCE(
								string_agg(
									problem_id::text || E'\t' || content_hash || E'\t' || corpus_tier || E'\t' || vector_sha256,
									E'\n' ORDER BY problem_id ASC, content_hash ASC, corpus_tier ASC
								),
								''
							),
							'UTF8'
						),
						'sha256'
					),
					'hex'
				) AS corpus_revision,
				COUNT(*)::bigint AS corpus_size
			FROM corpus
		),
		ranked AS MATERIALIZED (
			SELECT
				problem_id,
				content_hash,
				corpus_tier,
				GREATEST(0.0, LEAST(1.0, 1.0 - (embedding <=> $1)))::double precision AS similarity
			FROM corpus
			ORDER BY similarity DESC, problem_id ASC
			LIMIT $5
		)
		SELECT
			revision.corpus_revision,
			revision.corpus_size,
			ranked.problem_id IS NOT NULL AS has_neighbor,
			COALESCE(ranked.problem_id, '00000000-0000-0000-0000-000000000000'::uuid),
			COALESCE(ranked.content_hash, ''),
			COALESCE(ranked.corpus_tier, ''),
			COALESCE(ranked.similarity, 0.0)::double precision
		FROM revision
		LEFT JOIN ranked ON TRUE
		ORDER BY ranked.similarity DESC NULLS LAST, ranked.problem_id ASC`
}

func normalizeS5OriginalityQueryV1(input S5OriginalityQueryV1) (S5OriginalityQueryV1, error) {
	result := input
	result.Kind = normalizeEmbeddingKind(input.Kind)
	result.Embedding = append([]float32(nil), input.Embedding...)
	result.ExcludedProblemIDs = make([]uuid.UUID, len(input.ExcludedProblemIDs))
	copy(result.ExcludedProblemIDs, input.ExcludedProblemIDs)

	if result.ModelVersionID == uuid.Nil {
		return S5OriginalityQueryV1{}, fmt.Errorf("model_version_id is required")
	}
	if result.Kind != EmbeddingKindStatement {
		return S5OriginalityQueryV1{}, fmt.Errorf("S5 originality requires embedding_kind %q", EmbeddingKindStatement)
	}
	if len(result.Embedding) == 0 {
		return S5OriginalityQueryV1{}, fmt.Errorf("query embedding vector is required")
	}
	hasMagnitude := false
	for index, value := range result.Embedding {
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return S5OriginalityQueryV1{}, fmt.Errorf("query embedding value %d must be finite", index)
		}
		if value != 0 {
			hasMagnitude = true
		}
	}
	if !hasMagnitude {
		return S5OriginalityQueryV1{}, fmt.Errorf("query embedding vector must be non-zero")
	}
	if result.TopK <= 0 || result.TopK > MaxS5OriginalityTopKV1 {
		return S5OriginalityQueryV1{}, fmt.Errorf("top_k must be within [1,%d]", MaxS5OriginalityTopKV1)
	}
	if len(result.ExcludedProblemIDs) > maxS5ExcludedLineageIDsV1 {
		return S5OriginalityQueryV1{}, fmt.Errorf("excluded lineage exceeds %d problem IDs", maxS5ExcludedLineageIDsV1)
	}
	sort.Slice(result.ExcludedProblemIDs, func(i, j int) bool {
		return result.ExcludedProblemIDs[i].String() < result.ExcludedProblemIDs[j].String()
	})
	for index, id := range result.ExcludedProblemIDs {
		if id == uuid.Nil {
			return S5OriginalityQueryV1{}, fmt.Errorf("excluded lineage problem ID %d is required", index)
		}
		if index > 0 && id == result.ExcludedProblemIDs[index-1] {
			return S5OriginalityQueryV1{}, fmt.Errorf("excluded lineage problem ID %s is duplicated", id)
		}
	}
	return result, nil
}

func scanS5OriginalityRowsV1(rows interface {
	Next() bool
	Scan(dest ...interface{}) error
	Err() error
}, input S5OriginalityQueryV1) (S5OriginalityResultV1, error) {
	result := S5OriginalityResultV1{}
	excluded := make(map[uuid.UUID]struct{}, len(input.ExcludedProblemIDs))
	for _, id := range input.ExcludedProblemIDs {
		excluded[id] = struct{}{}
	}
	seen := make(map[uuid.UUID]struct{}, input.TopK)
	previousSimilarity := math.Inf(1)
	previousProblemID := ""
	rowCount := 0
	for rows.Next() {
		rowCount++
		var (
			corpusRevision string
			corpusSize     int64
			hasNeighbor    bool
			problemID      uuid.UUID
			contentHash    string
			corpusTier     string
			similarity     float64
		)
		if err := rows.Scan(
			&corpusRevision,
			&corpusSize,
			&hasNeighbor,
			&problemID,
			&contentHash,
			&corpusTier,
			&similarity,
		); err != nil {
			return S5OriginalityResultV1{}, fmt.Errorf("scanning S5 originality row: %w", err)
		}
		if err := validateContentHash(corpusRevision); err != nil {
			return S5OriginalityResultV1{}, fmt.Errorf("%w: invalid revision", ErrS5CorpusRevisionUnavailable)
		}
		if corpusSize < 0 || int64(int(corpusSize)) != corpusSize {
			return S5OriginalityResultV1{}, fmt.Errorf("%w: invalid corpus size", ErrS5CorpusRevisionUnavailable)
		}
		if result.CorpusRevision == "" {
			result.CorpusRevision = corpusRevision
			result.CorpusSize = int(corpusSize)
		} else if result.CorpusRevision != corpusRevision || result.CorpusSize != int(corpusSize) {
			return S5OriginalityResultV1{}, fmt.Errorf("%w: inconsistent rows", ErrS5CorpusRevisionUnavailable)
		}
		if !hasNeighbor {
			if rowCount != 1 || corpusSize != 0 || problemID != uuid.Nil || contentHash != "" || corpusTier != "" || similarity != 0 {
				return S5OriginalityResultV1{}, fmt.Errorf("%w: malformed empty corpus row", ErrS5CorpusRevisionUnavailable)
			}
			continue
		}
		if problemID == uuid.Nil {
			return S5OriginalityResultV1{}, fmt.Errorf("S5 originality neighbor problem ID is required")
		}
		if _, blocked := excluded[problemID]; blocked {
			return S5OriginalityResultV1{}, fmt.Errorf("S5 originality query returned excluded lineage problem %s", problemID)
		}
		if _, duplicate := seen[problemID]; duplicate {
			return S5OriginalityResultV1{}, fmt.Errorf("S5 originality query returned duplicate problem %s", problemID)
		}
		seen[problemID] = struct{}{}
		if err := validateContentHash(contentHash); err != nil {
			return S5OriginalityResultV1{}, fmt.Errorf("S5 originality neighbor %s: %w", problemID, err)
		}
		switch corpusTier {
		case "selection_candidate", "quarantine_advisory", "historical_advisory":
		default:
			return S5OriginalityResultV1{}, fmt.Errorf("unsupported S5 originality corpus tier %q", corpusTier)
		}
		if math.IsNaN(similarity) || math.IsInf(similarity, 0) || similarity < 0 || similarity > 1 {
			return S5OriginalityResultV1{}, fmt.Errorf("S5 originality neighbor %s has invalid similarity", problemID)
		}
		problemIDText := problemID.String()
		if similarity > previousSimilarity || (similarity == previousSimilarity && previousProblemID != "" && problemIDText < previousProblemID) {
			return S5OriginalityResultV1{}, fmt.Errorf("S5 originality neighbors are not ordered by similarity desc, problem_id asc")
		}
		previousSimilarity = similarity
		previousProblemID = problemIDText
		result.Neighbors = append(result.Neighbors, S5OriginalityNeighborV1{
			ProblemID: problemID, ContentHash: contentHash, CorpusTier: corpusTier, Similarity: similarity,
		})
	}
	if err := rows.Err(); err != nil {
		return S5OriginalityResultV1{}, fmt.Errorf("iterating S5 originality rows: %w", err)
	}
	if rowCount == 0 || result.CorpusRevision == "" {
		return S5OriginalityResultV1{}, fmt.Errorf("%w: query returned no revision row", ErrS5CorpusRevisionUnavailable)
	}
	if len(result.Neighbors) > input.TopK || len(result.Neighbors) > result.CorpusSize {
		return S5OriginalityResultV1{}, fmt.Errorf("%w: neighbor count exceeds corpus bounds", ErrS5CorpusRevisionUnavailable)
	}
	if result.CorpusSize > 0 && len(result.Neighbors) == 0 {
		return S5OriginalityResultV1{}, fmt.Errorf("%w: non-empty corpus returned no Top-K rows", ErrS5CorpusRevisionUnavailable)
	}
	result.NeighborCount = len(result.Neighbors)
	return result, nil
}

func findSimilarExceptQuery() string {
	return `
		SELECT
			p.id, p.serial_number, p.title, p.statement, p.level, p.difficulty,
			p.one_line_hint, p.detailed_solution,
			p.tags, p.status, p.metadata_json, p.created_at, p.updated_at,
			(pe.embedding <=> $1) AS distance
		FROM problem_embeddings pe
		INNER JOIN problems p ON p.id = pe.problem_id
		WHERE pe.model_version_id = $2
		  AND pe.embedding_kind = $3
		  AND pe.problem_id <> $4
		  AND p.status <> 'quarantined'
		  AND p.status <> 'rejected'
		  AND COALESCE(p.metadata_json ->> 'stale', 'false') <> 'true'
		  AND COALESCE(pe.metadata_json ->> 'stale', 'false') <> 'true'
		  AND (
		      $3 <> 'statement'
		      OR pe.content_hash = embedding_statement_content_hash(p.title, p.statement, p.one_line_hint)
		  )
		  AND NOT EXISTS (
		      SELECT 1 FROM problem_quarantine_records quarantine
		      WHERE quarantine.problem_id = p.id
		  )
		  AND (pe.embedding <=> $1) <= $5
		ORDER BY distance ASC
		LIMIT $6`
}

func findSimilarQuery() string {
	return `
		SELECT
			p.id, p.serial_number, p.title, p.statement, p.level, p.difficulty,
			p.one_line_hint, p.detailed_solution,
			p.tags, p.status, p.metadata_json, p.created_at, p.updated_at,
			(pe.embedding <=> $1) AS distance
		FROM problem_embeddings pe
		INNER JOIN problems p ON p.id = pe.problem_id
		WHERE pe.model_version_id = $2
		  AND pe.embedding_kind = $3
		  AND p.status <> 'quarantined'
		  AND p.status <> 'rejected'
		  AND COALESCE(p.metadata_json ->> 'stale', 'false') <> 'true'
		  AND COALESCE(pe.metadata_json ->> 'stale', 'false') <> 'true'
		  AND (
		      $3 <> 'statement'
		      OR pe.content_hash = embedding_statement_content_hash(p.title, p.statement, p.one_line_hint)
		  )
		  AND NOT EXISTS (
		      SELECT 1 FROM problem_quarantine_records quarantine
		      WHERE quarantine.problem_id = p.id
		  )
		  AND (pe.embedding <=> $1) <= $4
		ORDER BY distance ASC
		LIMIT $5`
}

func scanSimilarProblemRows(rows interface {
	Next() bool
	Scan(dest ...interface{}) error
	Err() error
}) ([]*domain.Problem, []float64, error) {
	var problems []*domain.Problem
	var scores []float64

	for rows.Next() {
		p := &domain.Problem{}
		var distance float64

		if err := rows.Scan(
			&p.ID,
			&p.SerialNumber,
			&p.Title,
			&p.Statement,
			&p.Level,
			&p.Difficulty,
			&p.OneLineHint,
			&p.DetailedSolution,
			pq.Array(&p.Tags),
			&p.Status,
			&p.MetadataJSON,
			&p.CreatedAt,
			&p.UpdatedAt,
			&distance,
		); err != nil {
			return nil, nil, fmt.Errorf("scanning similar problem row: %w", err)
		}

		// The pgvector <=> operator computes cosine distance.
		similarity := 1.0 - distance
		problems = append(problems, p)
		scores = append(scores, similarity)
	}

	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterating similar problem rows: %w", err)
	}

	return problems, scores, nil
}

// DeleteEmbedding removes all vector embeddings for a problem. This should be
// called when a problem is deleted to keep every embedding space consistent.
func (r *VectorRepository) DeleteEmbedding(ctx context.Context, problemID uuid.UUID) error {
	if problemID == uuid.Nil {
		return fmt.Errorf("problem ID is required")
	}

	query := `DELETE FROM problem_embeddings WHERE problem_id = $1`

	result, err := r.db.Exec(ctx, query, problemID)
	if err != nil {
		return fmt.Errorf("deleting embeddings for problem %s: %w", problemID, err)
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("embedding for problem %s not found: %w", problemID, sql.ErrNoRows)
	}

	return nil
}

func validateEmbeddingWrite(input EmbeddingWrite, kind string) error {
	if err := validateProblemVersionKind(input.ProblemID, input.ModelVersionID, kind); err != nil {
		return err
	}
	if err := validateContentHash(input.ContentHash); err != nil {
		return err
	}
	if len(input.Embedding) == 0 {
		return fmt.Errorf("embedding vector is required")
	}
	return nil
}

func validateProblemVersionKind(problemID, modelVersionID uuid.UUID, kind string) error {
	if problemID == uuid.Nil {
		return fmt.Errorf("problem ID is required")
	}
	if modelVersionID == uuid.Nil {
		return fmt.Errorf("model_version_id is required")
	}
	return validateEmbeddingKind(kind)
}

func validateSimilaritySearch(
	queryVec []float32,
	modelVersionID uuid.UUID,
	kind string,
	limit int,
	threshold float64,
) (int, float64, string, error) {
	kind = normalizeEmbeddingKind(kind)
	if modelVersionID == uuid.Nil {
		return 0, 0, "", fmt.Errorf("model_version_id is required")
	}
	if err := validateEmbeddingKind(kind); err != nil {
		return 0, 0, "", err
	}
	if len(queryVec) == 0 {
		return 0, 0, "", fmt.Errorf("query embedding vector is required")
	}
	if limit <= 0 {
		limit = 10
	}
	if threshold <= 0 {
		threshold = 0.7
	}
	return limit, 1.0 - threshold, kind, nil
}

func normalizeEmbeddingKind(kind string) string {
	return strings.TrimSpace(strings.ToLower(kind))
}

func validateEmbeddingKind(kind string) error {
	switch kind {
	case EmbeddingKindStatement, EmbeddingKindSolution:
		return nil
	default:
		return fmt.Errorf("unsupported embedding_kind %q", kind)
	}
}

func validateContentHash(hash string) error {
	if len(hash) != 64 {
		return fmt.Errorf("content_hash must be a lowercase sha256 hex digest")
	}
	for _, ch := range hash {
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
			return fmt.Errorf("content_hash must be a lowercase sha256 hex digest")
		}
	}
	return nil
}

func legacyEmbeddingContentHash(problemID uuid.UUID, embedding []float32) string {
	hasher := sha256.New()
	hasher.Write([]byte(problemID.String()))
	var buf [4]byte
	for _, value := range embedding {
		binary.LittleEndian.PutUint32(buf[:], math.Float32bits(value))
		hasher.Write(buf[:])
	}
	return hex.EncodeToString(hasher.Sum(nil))
}
