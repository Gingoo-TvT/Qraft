//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestProblemImportExactDedupConcurrentIntegration(t *testing.T) {
	if os.Getenv("ALGOFORGE_INTEGRATION_DISPOSABLE_DB") != "true" || os.Getenv("DATABASE_URL") == "" {
		t.Skip("requires disposable migrated database")
	}
	ctx := context.Background()
	pool, e := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	require.NoError(t, e)
	defer pool.Close()
	repo := NewProblemRepository(pool)
	source := domain.SourceProblem{ItemID: "fixture", Title: "Same title", Statement: "Synthetic source " + uuid.NewString()}
	evidence := domain.ImportSourceEvidence{Original: source, OriginalSHA256: source.Hash(), FinalSHA256: source.Hash()}
	operationKey := "import-fixture-" + uuid.NewString()
	_, e = pool.Exec(ctx, `INSERT INTO workflow_operations(operation_key,operation_type,payload_sha256) VALUES($1,'store_problem/v1',$2)`, operationKey, strings.Repeat("a", 64))
	require.NoError(t, e)
	defer pool.Exec(ctx, "DELETE FROM workflow_operations WHERE operation_key=$1", operationKey)
	workflowID := "synthetic-import-workflow"
	build := func() *domain.Problem {
		meta, _ := json.Marshal(map[string]any{"import_source": evidence, "store_idempotency_key": operationKey})
		return &domain.Problem{ID: uuid.New(), Title: source.Title, Statement: source.Statement, Level: domain.LevelAlgorithm, Difficulty: 1500, TimeLimit: 2000, MemoryLimit: 256, Status: domain.ProblemStatusDraft, Source: "qraft_import", WorkflowID: &workflowID, Tags: nil, MetadataJSON: meta, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	}
	first, second := build(), build()
	defer pool.Exec(ctx, "DELETE FROM problems WHERE id=ANY($1)", []uuid.UUID{first.ID, second.ID})
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, p := range []*domain.Problem{first, second} {
		wg.Add(1)
		go func(p *domain.Problem) {
			defer wg.Done()
			<-start
			errs <- repo.CreateImportedProblem(ctx, p, evidence)
		}(p)
	}
	close(start)
	wg.Wait()
	close(errs)
	ok, duplicates := 0, 0
	for e := range errs {
		if e == nil {
			ok++
		} else {
			var duplicate *ImportedIncompleteError
			require.True(t, errors.As(e, &duplicate), "unexpected insert error: %v", e)
			duplicates++
		}
	}
	require.Equal(t, 1, ok)
	require.Equal(t, 1, duplicates)
	found, e := repo.FindImportedDuplicate(ctx, source.Statement, source.Hash())
	var incomplete *ImportedIncompleteError
	require.True(t, errors.As(e, &incomplete))
	require.Equal(t, workflowID, incomplete.WorkflowID)
	_, e = pool.Exec(ctx, `UPDATE workflow_operations SET status='completed',completed_at=NOW(),result_json='{}'::jsonb WHERE operation_key=$1`, operationKey)
	require.NoError(t, e)
	found, e = repo.FindImportedDuplicate(ctx, source.Statement, source.Hash())
	require.NoError(t, e)
	loser := build()
	e = repo.CreateImportedProblem(ctx, loser, evidence)
	var duplicate *ImportedDuplicateError
	require.True(t, errors.As(e, &duplicate))
	require.NotEqual(t, uuid.Nil, found)
	source.Statement += " another mathematical task"
	evidence.Original = source
	evidence.OriginalSHA256 = source.Hash()
	evidence.FinalSHA256 = source.Hash()
	different := build()
	defer pool.Exec(ctx, "DELETE FROM problems WHERE id=$1", different.ID)
	require.NoError(t, repo.CreateImportedProblem(ctx, different, evidence), "same title must not imply duplicate")

	// Old records may have NULL source and no workflow ledger. They are still
	// usable duplicates, not incomplete imports or NULL-to-bool scan errors.
	_, e = pool.Exec(ctx, "UPDATE problems SET source=NULL WHERE id=$1", different.ID)
	require.NoError(t, e)
	found, e = repo.FindImportedDuplicate(ctx, source.Statement, source.Hash())
	require.NoError(t, e)
	require.Equal(t, different.ID, found)
	legacyDuplicate := build()
	e = repo.CreateImportedProblem(ctx, legacyDuplicate, evidence)
	require.True(t, errors.As(e, &duplicate), "NULL source duplicate should remain usable: %v", e)
	require.Equal(t, different.ID, duplicate.ProblemID)
}

func TestProblemImportFiftyItemCollectionIntegration(t *testing.T) {
	if os.Getenv("ALGOFORGE_INTEGRATION_DISPOSABLE_DB") != "true" || os.Getenv("DATABASE_URL") == "" {
		t.Skip("requires disposable migrated database")
	}
	ctx := context.Background()
	pool, e := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	require.NoError(t, e)
	defer pool.Close()
	repo := NewProblemRepository(pool)
	sets := NewProblemSetRepository(pool)
	ids := []uuid.UUID{}
	setID := uuid.New()
	defer func() {
		_, e := pool.Exec(ctx, "DELETE FROM problem_sets WHERE id=$1", setID)
		require.NoError(t, e)
		_, e = pool.Exec(ctx, "DELETE FROM problems WHERE id=ANY($1)", ids)
		require.NoError(t, e)
	}()
	set := domain.ProblemSet{ID: setID, Code: "fixture-" + setID.String(), Title: strings.Repeat("集", 255), Kind: domain.ProblemSetKindContest, Visibility: domain.ProblemSetVisibilityPrivate, Status: domain.ProblemSetStatusDraft, DesiredItemCount: 50, MinItemCount: 50, MaxItemCount: 50, Tags: []string{}}
	require.NoError(t, sets.Create(ctx, &set))
	for i := 0; i < 50; i++ {
		p := domain.Problem{ID: uuid.New(), Title: fmt.Sprintf("Synthetic item %d", i), Statement: "Synthetic statement", Level: domain.LevelAlgorithm, Difficulty: 1500, TimeLimit: 2000, MemoryLimit: 256, Status: domain.ProblemStatusDraft, Tags: []string{}, MetadataJSON: json.RawMessage(`{}`), CreatedAt: time.Now(), UpdatedAt: time.Now()}
		require.NoError(t, repo.Create(ctx, &p))
		ids = append(ids, p.ID)
		require.NoError(t, sets.AddItem(ctx, &domain.ProblemSetItem{ID: uuid.New(), SetID: setID, ProblemID: &p.ID, Position: i + 1, Score: 100, Section: "导入题目"}))
	}
	saved, e := sets.GetByID(ctx, setID)
	require.NoError(t, e)
	require.Len(t, saved.Items, 50)
	require.Equal(t, 50, saved.DesiredItemCount)
	require.Equal(t, strings.Repeat("集", 255), saved.Title)
}
