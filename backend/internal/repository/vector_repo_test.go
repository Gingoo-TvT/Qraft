package repository

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestS5OriginalityQueryV1SQLBindsCorpusAndGlobalOrder(t *testing.T) {
	query := s5OriginalityQueryV1SQL()
	for _, required := range []string{
		"pe.model_version_id = $2",
		"pe.embedding_kind = $3",
		"embedding_statement_content_hash",
		"ANY(COALESCE($4::uuid[], ARRAY[]::uuid[]))",
		"p.status IN ('quarantined', 'rejected')",
		"p.status = 'published'",
		"'selection_candidate'",
		"'quarantine_advisory'",
		"'historical_advisory'",
		"problem_quarantine_records",
		"string_agg(",
		"vector_sha256",
		"ORDER BY similarity DESC, problem_id ASC",
		"LEFT JOIN ranked ON TRUE",
	} {
		if !strings.Contains(query, required) {
			t.Fatalf("S5 originality SQL is missing %q:\n%s", required, query)
		}
	}
	if strings.Contains(query, "PARTITION BY") {
		t.Fatalf("S5 originality Top-K must be global, not tier-partitioned:\n%s", query)
	}
}

func TestNormalizeS5OriginalityQueryV1(t *testing.T) {
	modelVersionID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	first := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	second := uuid.MustParse("00000000-0000-0000-0000-000000000002")
	normalized, err := normalizeS5OriginalityQueryV1(S5OriginalityQueryV1{
		ModelVersionID:     modelVersionID,
		Kind:               " STATEMENT ",
		Embedding:          []float32{1, 0},
		TopK:               8,
		ExcludedProblemIDs: []uuid.UUID{second, first},
	})
	if err != nil {
		t.Fatal(err)
	}
	if normalized.Kind != EmbeddingKindStatement || len(normalized.ExcludedProblemIDs) != 2 ||
		normalized.ExcludedProblemIDs[0] != first || normalized.ExcludedProblemIDs[1] != second {
		t.Fatalf("normalized query = %+v", normalized)
	}
	if normalized.ExcludedProblemIDs == nil {
		t.Fatal("normalized exclusion must encode an explicit empty array rather than SQL NULL")
	}

	invalid := []struct {
		name    string
		mutate  func(*S5OriginalityQueryV1)
		wantErr string
	}{
		{"missing model", func(q *S5OriginalityQueryV1) { q.ModelVersionID = uuid.Nil }, "model_version_id"},
		{"wrong kind", func(q *S5OriginalityQueryV1) { q.Kind = EmbeddingKindSolution }, "requires embedding_kind"},
		{"empty vector", func(q *S5OriginalityQueryV1) { q.Embedding = nil }, "vector is required"},
		{"zero vector", func(q *S5OriginalityQueryV1) { q.Embedding = []float32{0, 0} }, "must be non-zero"},
		{"non-finite vector", func(q *S5OriginalityQueryV1) { q.Embedding = []float32{float32(math.NaN())} }, "must be finite"},
		{"bad top K", func(q *S5OriginalityQueryV1) { q.TopK = MaxS5OriginalityTopKV1 + 1 }, "top_k"},
		{"duplicate lineage", func(q *S5OriginalityQueryV1) { q.ExcludedProblemIDs = []uuid.UUID{first, first} }, "duplicated"},
	}
	for _, test := range invalid {
		t.Run(test.name, func(t *testing.T) {
			query := S5OriginalityQueryV1{
				ModelVersionID: modelVersionID,
				Kind:           EmbeddingKindStatement,
				Embedding:      []float32{1},
				TopK:           8,
			}
			test.mutate(&query)
			_, err := normalizeS5OriginalityQueryV1(query)
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("normalize error = %v, want containing %q", err, test.wantErr)
			}
		})
	}
}

func TestScanS5OriginalityRowsV1PreservesExplicitEmptyCorpus(t *testing.T) {
	revision := strings.Repeat("a", 64)
	rows := &s5OriginalityTestRows{rows: []s5OriginalityTestRow{{
		corpusRevision: revision,
		corpusSize:     0,
		hasNeighbor:    false,
	}}}
	result, err := scanS5OriginalityRowsV1(rows, S5OriginalityQueryV1{TopK: 8})
	if err != nil {
		t.Fatal(err)
	}
	if result.CorpusRevision != revision || result.CorpusSize != 0 || result.NeighborCount != 0 || len(result.Neighbors) != 0 {
		t.Fatalf("empty-corpus result = %+v", result)
	}
}

func TestScanS5OriginalityRowsV1KeepsGlobalTopKOrderAndTiers(t *testing.T) {
	revision := strings.Repeat("b", 64)
	first := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	second := uuid.MustParse("00000000-0000-0000-0000-000000000002")
	rows := &s5OriginalityTestRows{rows: []s5OriginalityTestRow{
		{revision, 3, true, first, strings.Repeat("c", 64), "historical_advisory", 0.91},
		{revision, 3, true, second, strings.Repeat("d", 64), "quarantine_advisory", 0.91},
	}}
	result, err := scanS5OriginalityRowsV1(rows, S5OriginalityQueryV1{TopK: 2})
	if err != nil {
		t.Fatal(err)
	}
	if result.NeighborCount != 2 || result.Neighbors[0].ProblemID != first || result.Neighbors[1].ProblemID != second {
		t.Fatalf("ordered result = %+v", result)
	}

	reversed := &s5OriginalityTestRows{rows: []s5OriginalityTestRow{
		{revision, 3, true, second, strings.Repeat("d", 64), "quarantine_advisory", 0.91},
		{revision, 3, true, first, strings.Repeat("c", 64), "historical_advisory", 0.91},
	}}
	if _, err := scanS5OriginalityRowsV1(reversed, S5OriginalityQueryV1{TopK: 2}); err == nil || !strings.Contains(err.Error(), "not ordered") {
		t.Fatalf("reversed tie order error = %v", err)
	}
}

type s5OriginalityTestRow struct {
	corpusRevision string
	corpusSize     int64
	hasNeighbor    bool
	problemID      uuid.UUID
	contentHash    string
	corpusTier     string
	similarity     float64
}

type s5OriginalityTestRows struct {
	rows  []s5OriginalityTestRow
	index int
	err   error
}

func (rows *s5OriginalityTestRows) Next() bool {
	if rows.index >= len(rows.rows) {
		return false
	}
	rows.index++
	return true
}

func (rows *s5OriginalityTestRows) Scan(dest ...interface{}) error {
	if rows.index == 0 || rows.index > len(rows.rows) || len(dest) != 7 {
		return fmt.Errorf("invalid test scan")
	}
	row := rows.rows[rows.index-1]
	*dest[0].(*string) = row.corpusRevision
	*dest[1].(*int64) = row.corpusSize
	*dest[2].(*bool) = row.hasNeighbor
	*dest[3].(*uuid.UUID) = row.problemID
	*dest[4].(*string) = row.contentHash
	*dest[5].(*string) = row.corpusTier
	*dest[6].(*float64) = row.similarity
	return nil
}

func (rows *s5OriginalityTestRows) Err() error { return rows.err }

func TestVersionedSimilarityRequiresModelVersion(t *testing.T) {
	repo := &VectorRepository{}
	_, _, err := repo.FindSimilarForVersion(
		context.Background(),
		[]float32{1, 0, 0},
		uuid.Nil,
		EmbeddingKindStatement,
		10,
		0.7,
	)
	if err == nil || !strings.Contains(err.Error(), "model_version_id is required") {
		t.Fatalf("FindSimilarForVersion() error = %v, want model_version_id requirement", err)
	}
}

func TestVerifyActiveModelVersionAllowsEmptyDeploymentPin(t *testing.T) {
	var repo *VectorRepository
	if err := repo.VerifyActiveModelVersion(context.Background(), EmbeddingKindStatement, " "); err != nil {
		t.Fatalf("empty deployment pin should be a no-op: %v", err)
	}
}

func TestVerifyActiveModelVersionRejectsInvalidDeploymentPin(t *testing.T) {
	var repo *VectorRepository
	err := repo.VerifyActiveModelVersion(context.Background(), EmbeddingKindStatement, "not-a-uuid")
	if err == nil || !strings.Contains(err.Error(), "must be a UUID") {
		t.Fatalf("invalid deployment pin error = %v, want UUID error", err)
	}
}

func TestStatementModelVersionBindingIsConfigurationOwned(t *testing.T) {
	configuredID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	repo := NewVectorRepository(nil)
	bound, err := repo.BindStatementModelVersion(configuredID)
	if err != nil {
		t.Fatalf("bind statement model version: %v", err)
	}
	got, err := bound.StatementModelVersion(context.Background())
	if err != nil {
		t.Fatalf("resolve bound statement model version: %v", err)
	}
	if got != configuredID {
		t.Fatalf("bound statement model version = %s, want %s", got, configuredID)
	}
	if repo.statementModelVersionBound {
		t.Fatal("binding mutated the administration repository")
	}
}

func TestRequiredStatementModelVersionDoesNotFallBackToActivePointer(t *testing.T) {
	repo := NewVectorRepository(nil).RequireStatementModelVersion()
	_, err := repo.GetEmbedding(context.Background(), uuid.New())
	if !errors.Is(err, ErrConfiguredStatementModelVersionRequired) {
		t.Fatalf("unconfigured runtime GetEmbedding() error = %v", err)
	}
}

func TestVersionedEmbeddingWriteValidation(t *testing.T) {
	problemID := uuid.New()
	modelVersionID := uuid.New()

	tests := []struct {
		name    string
		input   EmbeddingWrite
		wantErr string
	}{
		{
			name: "missing problem",
			input: EmbeddingWrite{
				ModelVersionID: modelVersionID,
				Kind:           EmbeddingKindStatement,
				ContentHash:    strings.Repeat("a", 64),
				Embedding:      []float32{1},
			},
			wantErr: "problem ID is required",
		},
		{
			name: "missing version",
			input: EmbeddingWrite{
				ProblemID:   problemID,
				Kind:        EmbeddingKindStatement,
				ContentHash: strings.Repeat("a", 64),
				Embedding:   []float32{1},
			},
			wantErr: "model_version_id is required",
		},
		{
			name: "unsupported kind",
			input: EmbeddingWrite{
				ProblemID:      problemID,
				ModelVersionID: modelVersionID,
				Kind:           "quiz",
				ContentHash:    strings.Repeat("a", 64),
				Embedding:      []float32{1},
			},
			wantErr: "unsupported embedding_kind",
		},
		{
			name: "bad content hash",
			input: EmbeddingWrite{
				ProblemID:      problemID,
				ModelVersionID: modelVersionID,
				Kind:           EmbeddingKindStatement,
				ContentHash:    strings.Repeat("A", 64),
				Embedding:      []float32{1},
			},
			wantErr: "content_hash",
		},
		{
			name: "empty embedding",
			input: EmbeddingWrite{
				ProblemID:      problemID,
				ModelVersionID: modelVersionID,
				Kind:           EmbeddingKindStatement,
				ContentHash:    strings.Repeat("a", 64),
			},
			wantErr: "embedding vector is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateEmbeddingWrite(tt.input, normalizeEmbeddingKind(tt.input.Kind))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("validateEmbeddingWrite() error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestVersionedSimilarityQueriesFilterVersionAndKind(t *testing.T) {
	for name, query := range map[string]string{
		"find similar":        findSimilarQuery(),
		"find similar except": findSimilarExceptQuery(),
	} {
		t.Run(name, func(t *testing.T) {
			for _, required := range []string{
				"pe.model_version_id = $2",
				"pe.embedding_kind = $3",
				"problem_embeddings pe",
				"metadata_json ->> 'stale'",
				"p.status <> 'rejected'",
				"COALESCE(pe.metadata_json ->> 'stale', 'false') <> 'true'",
				"embedding_statement_content_hash",
			} {
				if !strings.Contains(query, required) {
					t.Fatalf("query is missing %q:\n%s", required, query)
				}
			}
		})
	}
}

func TestLegacyEmbeddingContentHashIsStableSHA256(t *testing.T) {
	problemID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	first := legacyEmbeddingContentHash(problemID, []float32{1, 2, 3})
	second := legacyEmbeddingContentHash(problemID, []float32{1, 2, 3})
	changed := legacyEmbeddingContentHash(problemID, []float32{1, 2, 4})

	if len(first) != 64 {
		t.Fatalf("hash length = %d, want 64", len(first))
	}
	if first != second {
		t.Fatalf("hash is not stable: %s != %s", first, second)
	}
	if first == changed {
		t.Fatal("hash did not change when embedding changed")
	}
}

func TestEmbeddingIntegrityBlockingIssues(t *testing.T) {
	counts := EmbeddingIntegrityViolationCounts{
		MissingStatementActivePointer:                 1,
		MissingSolutionActivePointer:                  1,
		ActivePointerInactiveModels:                   1,
		ActivePointerDimensionMismatches:              1,
		OrphanProblemEmbeddings:                       1,
		OrphanModelEmbeddings:                         1,
		EmbeddingDimensionMismatches:                  1,
		ActiveStatementDuplicateCurrentRows:           1,
		PublishedMissingActiveStatementVector:         1,
		LegacyProjectionWithoutCurrentStatementVector: 1,
		ActiveStatementStaleRows:                      99,
		LegacyProjectionPresent:                       99,
	}
	if got, want := counts.BlockingIssues(), int64(10); got != want {
		t.Fatalf("BlockingIssues() = %d, want %d", got, want)
	}
}

func TestEmbeddingIntegritySQLUsesVersionedActiveStatementSpace(t *testing.T) {
	sql := scanEmbeddingIntegritySQL()
	for _, required := range []string{
		"embedding_active_pointers",
		"problem_embeddings pe",
		"embedding_statement_content_hash",
		"vector_dims(pe.embedding)",
		"p.status = 'published'",
		"p.legacy_embedding IS NOT NULL",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("integrity SQL is missing %q:\n%s", required, sql)
		}
	}
	if strings.Contains(sql, "find_similar_problems") {
		t.Fatalf("integrity SQL should not depend on the legacy SQL function:\n%s", sql)
	}
}

func TestEmbeddingBackfillOptionValidation(t *testing.T) {
	toModel := uuid.New()
	tests := []struct {
		name    string
		options EmbeddingBackfillPlanOptions
		wantErr string
	}{
		{
			name:    "missing target",
			options: EmbeddingBackfillPlanOptions{Kind: EmbeddingKindStatement},
			wantErr: "to_model_version_id is required",
		},
		{
			name:    "bad kind",
			options: EmbeddingBackfillPlanOptions{ToModelVersionID: toModel, Kind: "quiz"},
			wantErr: "unsupported embedding_kind",
		},
		{
			name:    "negative limit",
			options: EmbeddingBackfillPlanOptions{ToModelVersionID: toModel, Kind: EmbeddingKindStatement, Limit: -1},
			wantErr: "limit must be non-negative",
		},
		{
			name: "same source and target",
			options: EmbeddingBackfillPlanOptions{
				FromModelVersionID: &toModel,
				ToModelVersionID:   toModel,
				Kind:               EmbeddingKindStatement,
			},
			wantErr: "must differ",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateEmbeddingBackfillOptions(tt.options, normalizeEmbeddingKind(tt.options.Kind))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("validateEmbeddingBackfillOptions() error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestEmbeddingBackfillRunValidation(t *testing.T) {
	toModel := uuid.New()
	tests := []struct {
		name    string
		input   EmbeddingBackfillRunInput
		wantErr string
	}{
		{
			name: "missing run key",
			input: EmbeddingBackfillRunInput{
				ToModelVersionID: toModel,
				Kind:             EmbeddingKindStatement,
				RequestSHA256:    strings.Repeat("a", 64),
			},
			wantErr: "run_key is required",
		},
		{
			name: "bad request hash",
			input: EmbeddingBackfillRunInput{
				RunKey:           "run-1",
				ToModelVersionID: toModel,
				Kind:             EmbeddingKindStatement,
				RequestSHA256:    "not-a-hash",
			},
			wantErr: "request_sha256",
		},
		{
			name: "same source and target",
			input: EmbeddingBackfillRunInput{
				RunKey:             "run-1",
				FromModelVersionID: &toModel,
				ToModelVersionID:   toModel,
				Kind:               EmbeddingKindStatement,
				RequestSHA256:      strings.Repeat("a", 64),
			},
			wantErr: "must differ",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateEmbeddingBackfillRunInput(tt.input, normalizeEmbeddingKind(tt.input.Kind))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("validateEmbeddingBackfillRunInput() error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestStatementBackfillCandidatesSQLIsContentAddressed(t *testing.T) {
	sql := statementBackfillCandidatesSQL()
	for _, required := range []string{
		"embedding_statement_content_hash",
		"problem_embeddings source",
		"problem_embeddings target_any",
		"problem_embeddings target_current",
		"target_current.content_hash = current_statement.content_hash",
		"NOT has_target_current",
		"NOT has_target_any",
		"problem_quarantine_records",
		"$5::uuid IS NULL OR p.id > $5::uuid",
		"LIMIT $6",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("backfill SQL is missing %q:\n%s", required, sql)
		}
	}
}

func TestSolutionBackfillCandidatesSQLIsContentAddressed(t *testing.T) {
	sql := solutionBackfillCandidatesSQL()
	for _, required := range []string{
		"FROM solutions s",
		"s.solution_type = 'main'",
		"s.compile_status = 'success'",
		"btrim(s.source_code) <> ''",
		"digest(convert_to",
		"s.solution_type || E'\\n\\n' || s.language || E'\\n\\n' || s.source_code",
		"problem_embeddings source",
		"problem_embeddings target_any",
		"problem_embeddings target_current",
		"target_current.content_hash = current_solution.content_hash",
		"NOT has_target_current",
		"NOT has_target_any",
		"problem_quarantine_records",
		"$5::uuid IS NULL OR p.id > $5::uuid",
		"LIMIT $6",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("solution backfill SQL is missing %q:\n%s", required, sql)
		}
	}
}

func TestBackfillCandidatesSQLSupportsStatementAndSolution(t *testing.T) {
	for _, kind := range []string{EmbeddingKindStatement, EmbeddingKindSolution} {
		sql, err := backfillCandidatesSQL(kind)
		if err != nil {
			t.Fatalf("backfillCandidatesSQL(%q) error = %v", kind, err)
		}
		if !strings.Contains(sql, "problem_embeddings target_current") {
			t.Fatalf("backfillCandidatesSQL(%q) returned unexpected SQL:\n%s", kind, sql)
		}
	}
	if _, err := backfillCandidatesSQL("quiz"); err == nil || !strings.Contains(err.Error(), "unsupported embedding_kind") {
		t.Fatalf("unsupported kind error = %v", err)
	}
}
