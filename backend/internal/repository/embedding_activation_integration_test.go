//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"
	"github.com/stretchr/testify/require"
)

type embeddingActivationFixture struct {
	pool     *pgxpool.Pool
	repo     *VectorRepository
	actor    string
	models   []uuid.UUID
	problems []uuid.UUID
}

func newEmbeddingActivationFixture(t *testing.T) *embeddingActivationFixture {
	t.Helper()
	if os.Getenv("ALGOFORGE_INTEGRATION_DISPOSABLE_DB") != "true" || os.Getenv("DATABASE_URL") == "" {
		t.Skip("requires explicitly disposable migrated DATABASE_URL")
	}
	cfg, err := pgxpool.ParseConfig(os.Getenv("DATABASE_URL"))
	require.NoError(t, err)
	actor := "embedding-activation-test-" + uuid.NewString()
	cfg.ConnConfig.RuntimeParams["application_name"] = actor
	cfg.MaxConns = 8
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	require.NoError(t, err)
	f := &embeddingActivationFixture{pool: pool, repo: NewVectorRepository(pool), actor: actor}
	t.Cleanup(func() {
		ctx := context.Background()
		for _, id := range f.problems {
			_, err := pool.Exec(ctx, "DELETE FROM problems WHERE id=$1", id)
			require.NoError(t, err)
		}
		// Audit rows are append-only by design. Their unique actor isolates each
		// fixture; the explicitly disposable test database owns their lifetime.
		for _, id := range f.models {
			_, err := pool.Exec(ctx, "DELETE FROM embedding_active_pointers WHERE model_version_id=$1", id)
			require.NoError(t, err)
			_, err = pool.Exec(ctx, "DELETE FROM embedding_model_versions WHERE id=$1", id)
			require.NoError(t, err)
		}
		pool.Close()
	})
	var pointers int
	require.NoError(t, pool.QueryRow(context.Background(), "SELECT COUNT(*) FROM embedding_active_pointers").Scan(&pointers))
	require.Zero(t, pointers, "test requires empty disposable pointers, never clears existing instance pointers")
	return f
}

func (f *embeddingActivationFixture) model(t *testing.T, status string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := f.pool.Exec(context.Background(),
		"INSERT INTO embedding_model_versions(id,model_id,provider,revision,weights_hash,dimensions,normalization,quantization,status) VALUES($1,$2,'synthetic','v1',$3,1536,'none','float32',$4)",
		id, id.String(), strings.Repeat("a", 64), status)
	require.NoError(t, err)
	f.models = append(f.models, id)
	return id
}

func (f *embeddingActivationFixture) options(id uuid.UUID) ActivePointerSwitchOptions {
	return ActivePointerSwitchOptions{Kind: EmbeddingKindStatement, Operation: EmbeddingSwitchOperationCutover,
		ToModelVersionID: id, Actor: f.actor, Reason: "synthetic initial activation",
		DatasetReportSHA256: strings.Repeat("a", 64)}
}

func (f *embeddingActivationFixture) untouched(t *testing.T, id uuid.UUID) {
	t.Helper()
	var count int
	require.NoError(t, f.pool.QueryRow(context.Background(), "SELECT COUNT(*) FROM embedding_active_pointers").Scan(&count))
	require.Zero(t, count)
	require.NoError(t, f.pool.QueryRow(context.Background(), "SELECT COUNT(*) FROM provenance_audit_events WHERE actor=$1", f.actor).Scan(&count))
	require.Zero(t, count)
	var status string
	require.NoError(t, f.pool.QueryRow(context.Background(), "SELECT status FROM embedding_model_versions WHERE id=$1", id).Scan(&status))
	require.Equal(t, EmbeddingModelStatusShadow, status)
}

func TestEmbeddingActivationInitialDryRunAndCommitIntegration(t *testing.T) {
	f := newEmbeddingActivationFixture(t)
	ctx := context.Background()
	first := f.model(t, EmbeddingModelStatusShadow)
	options := f.options(first)
	options.DryRun = true
	report, err := f.repo.SwitchActiveEmbeddingPointer(ctx, options)
	require.NoError(t, err)
	require.Equal(t, EmbeddingSwitchDecisionGo, report.Decision)
	require.Nil(t, report.OldModelVersionID)
	require.False(t, report.Committed)
	require.Equal(t, float64(1), report.Preflight.Coverage())
	f.untouched(t, first)
	raw, err := json.Marshal(report)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"old_model_version_id":null`)

	options.DryRun = false
	report, err = f.repo.SwitchActiveEmbeddingPointer(ctx, options)
	require.NoError(t, err)
	require.True(t, report.Committed)
	require.Nil(t, report.OldModelVersionID)
	var pointer uuid.UUID
	var dimensions int
	require.NoError(t, f.pool.QueryRow(ctx, "SELECT model_version_id, expected_dimensions FROM embedding_active_pointers WHERE embedding_kind='statement'").Scan(&pointer, &dimensions))
	require.Equal(t, first, pointer)
	require.Equal(t, 1536, dimensions)
	var audit []byte
	require.NoError(t, f.pool.QueryRow(ctx, "SELECT details FROM provenance_audit_events WHERE event_id=$1", report.AuditEventID).Scan(&audit))
	var details map[string]any
	require.NoError(t, json.Unmarshal(audit, &details))
	require.Contains(t, details, "old_model_version_id")
	require.Nil(t, details["old_model_version_id"])
	require.Equal(t, first.String(), details["new_model_version_id"])
	// The same dimension trigger still protects a newly inserted pointer.
	_, err = f.pool.Exec(ctx, "UPDATE embedding_active_pointers SET expected_dimensions=768 WHERE embedding_kind='statement'")
	require.ErrorContains(t, err, "dimension mismatch")
	second := f.model(t, EmbeddingModelStatusShadow)
	options = f.options(second)
	options.ExpectedFromModelVersionID = &first
	report, err = f.repo.SwitchActiveEmbeddingPointer(ctx, options)
	require.NoError(t, err)
	require.True(t, report.Committed)
	require.NotNil(t, report.OldModelVersionID)
	require.Equal(t, first, *report.OldModelVersionID)
	// Existing compare-and-switch expectations are unchanged.
	_, err = f.repo.SwitchActiveEmbeddingPointer(ctx, options)
	require.ErrorContains(t, err, "expectation mismatch")
}

func TestEmbeddingActivationInitialGuardsIntegration(t *testing.T) {
	for _, mode := range []string{"rollback", "expected source", "unknown target", "retired target", "shadow errors"} {
		t.Run(mode, func(t *testing.T) {
			f := newEmbeddingActivationFixture(t)
			target := f.model(t, EmbeddingModelStatusShadow)
			options := f.options(target)
			switch mode {
			case "rollback":
				options.Operation = EmbeddingSwitchOperationRollback
			case "expected source":
				expected := uuid.New()
				options.ExpectedFromModelVersionID = &expected
			case "unknown target":
				options.ToModelVersionID = uuid.New()
			case "retired target":
				options.ToModelVersionID = f.model(t, EmbeddingModelStatusRetired)
			case "shadow errors":
				options.ShadowReadCount = 1000
				options.ShadowErrorCount = 1
			}
			report, err := f.repo.SwitchActiveEmbeddingPointer(context.Background(), options)
			if mode == "shadow errors" {
				require.NoError(t, err)
				require.Equal(t, EmbeddingSwitchDecisionNoGo, report.Decision)
				require.NotEmpty(t, report.BlockingIssues)
			} else {
				require.Error(t, err)
			}
			f.untouched(t, target)
			if mode == "retired target" {
				var status string
				require.NoError(t, f.pool.QueryRow(context.Background(), "SELECT status FROM embedding_model_versions WHERE id=$1", options.ToModelVersionID).Scan(&status))
				require.Equal(t, EmbeddingModelStatusRetired, status)
			}
		})
	}
}

func TestEmbeddingActivationInitialRequiresCurrentCoverageIntegration(t *testing.T) {
	f := newEmbeddingActivationFixture(t)
	ctx := context.Background()
	target := f.model(t, EmbeddingModelStatusShadow)
	problem := uuid.New()
	_, err := f.pool.Exec(ctx, "INSERT INTO problems(id,title,statement,one_line_hint,level,difficulty,tags,status) VALUES($1,'Synthetic activation fixture','Compute a+b.','addition','algorithm',800,ARRAY['math'],'published')", problem)
	require.NoError(t, err)
	f.problems = append(f.problems, problem)
	options := f.options(target)
	for _, phase := range []string{"missing", "stale"} {
		report, err := f.repo.SwitchActiveEmbeddingPointer(ctx, options)
		require.NoError(t, err)
		require.Equal(t, EmbeddingSwitchDecisionNoGo, report.Decision, phase)
		require.EqualValues(t, 1, report.Preflight.TargetMissingCurrentVectorCount, phase)
		f.untouched(t, target)
		if phase == "missing" {
			vector := make([]float32, 1536)
			vector[0] = 1
			_, err = f.pool.Exec(ctx, "INSERT INTO problem_embeddings(problem_id,embedding,model_version_id,embedding_kind,content_hash) VALUES($1,$2,$3,'statement',$4)", problem, pgvector.NewVector(vector), target, strings.Repeat("b", 64))
			require.NoError(t, err)
		}
	}
	_, err = f.pool.Exec(ctx, "UPDATE problem_embeddings pe SET content_hash=embedding_statement_content_hash(p.title,p.statement,p.one_line_hint) FROM problems p WHERE pe.problem_id=p.id AND p.id=$1", problem)
	require.NoError(t, err)
	report, err := f.repo.SwitchActiveEmbeddingPointer(ctx, options)
	require.NoError(t, err)
	require.True(t, report.Committed)
	require.EqualValues(t, 1, report.Preflight.TargetCurrentVectorCount)
}

func TestEmbeddingActivationConcurrentInitializationIntegration(t *testing.T) {
	f := newEmbeddingActivationFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	first, second := f.model(t, EmbeddingModelStatusShadow), f.model(t, EmbeddingModelStatusShadow)
	blocker, err := f.pool.Begin(ctx)
	require.NoError(t, err)
	defer blocker.Rollback(context.Background())
	// Block target row locks after both serializable transactions read that no
	// pointer exists. This exercises the actual missing-row race deterministically.
	_, err = blocker.Exec(ctx, "LOCK TABLE embedding_model_versions IN EXCLUSIVE MODE")
	require.NoError(t, err)
	type result struct {
		report ActivePointerSwitchReport
		err    error
	}
	done := make(chan result, 2)
	for _, id := range []uuid.UUID{first, second} {
		go func(id uuid.UUID) {
			report, err := f.repo.SwitchActiveEmbeddingPointer(ctx, f.options(id))
			done <- result{report, err}
		}(id)
	}
	require.Eventually(t, func() bool {
		var waiting int
		err := f.pool.QueryRow(ctx, "SELECT COUNT(*) FROM pg_stat_activity WHERE application_name=$1 AND wait_event_type='Lock' AND query LIKE '%SELECT dimensions, status%'", f.actor).Scan(&waiting)
		return err == nil && waiting == 2
	}, 5*time.Second, 20*time.Millisecond)
	require.NoError(t, blocker.Commit(ctx))
	committed := 0
	var winner uuid.UUID
	for range 2 {
		outcome := <-done
		if outcome.err == nil {
			require.True(t, outcome.report.Committed)
			require.Nil(t, outcome.report.OldModelVersionID)
			winner = outcome.report.NewModelVersionID
			committed++
		} else {
			require.False(t, outcome.report.Committed)
		}
	}
	require.Equal(t, 1, committed)
	var pointer uuid.UUID
	require.NoError(t, f.pool.QueryRow(ctx, "SELECT model_version_id FROM embedding_active_pointers WHERE embedding_kind='statement'").Scan(&pointer))
	require.Equal(t, winner, pointer)
	var count int
	require.NoError(t, f.pool.QueryRow(ctx, "SELECT COUNT(*) FROM provenance_audit_events WHERE actor=$1", f.actor).Scan(&count))
	require.Equal(t, 1, count)
	loser := first
	if winner == first {
		loser = second
	}
	var status string
	require.NoError(t, f.pool.QueryRow(ctx, "SELECT status FROM embedding_model_versions WHERE id=$1", loser).Scan(&status))
	require.Equal(t, EmbeddingModelStatusShadow, status)
}
