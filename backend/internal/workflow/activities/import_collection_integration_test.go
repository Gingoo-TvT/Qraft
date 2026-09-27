//go:build integration

package activities

import (
	"context"
	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
	"os"
	"testing"
)

func TestImportedCollectionResumeKeepsMembersAndRestoresOrderIntegration(t *testing.T) {
	if os.Getenv("ALGOFORGE_INTEGRATION_DISPOSABLE_DB") != "true" || os.Getenv("DATABASE_URL") == "" {
		t.Skip("requires disposable migrated database")
	}
	ctx := context.Background()
	db, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	require.NoError(t, err)
	defer db.Close()
	repo := repository.NewProblemSetRepository(db)
	ids := []uuid.UUID{uuid.New(), uuid.New(), uuid.New(), uuid.New()}
	for _, id := range ids {
		_, err = db.Exec(ctx, `INSERT INTO problems(id,title,statement,level,difficulty,tags) VALUES($1,'Synthetic import','Compute a synthetic value','algorithm',800,ARRAY[]::text[])`, id)
		require.NoError(t, err)
	}
	workflowID := "synthetic-import-collection-" + uuid.NewString()
	setID := uuid.NewSHA1(uuid.NameSpaceURL, []byte("qraft-import-set:"+workflowID))
	defer func() {
		_, err := db.Exec(ctx, `DELETE FROM problem_sets WHERE id=$1`, setID)
		require.NoError(t, err)
		_, err = db.Exec(ctx, `DELETE FROM problems WHERE id=ANY($1)`, ids)
		require.NoError(t, err)
	}()
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestActivityEnvironment()
	acts := &Activities{deps: &Dependencies{ProblemSetRepo: repo}}
	env.RegisterActivity(acts.CreateImportedProblemSetActivity)
	invoke := func(problems []uuid.UUID) {
		result, err := env.ExecuteActivity(acts.CreateImportedProblemSetActivity, CreateImportedProblemSetInput{WorkflowID: workflowID, Title: "Synthetic resumed collection", ProblemIDs: problems})
		require.NoError(t, err)
		var got string
		require.NoError(t, result.Get(&got))
		require.Equal(t, setID.String(), got)
	}
	invoke([]uuid.UUID{ids[0], ids[2]})
	first, err := repo.GetByID(ctx, setID)
	require.NoError(t, err)
	retained := map[uuid.UUID]uuid.UUID{ids[0]: first.Items[0].ID, ids[2]: first.Items[1].ID}
	extra := domain.ProblemSetItem{ID: uuid.New(), SetID: setID, ProblemID: &ids[3], Score: 42, Notes: "preserve user-added item"}
	require.NoError(t, repo.AddItem(ctx, &extra))
	for attempt := 0; attempt < 2; attempt++ {
		invoke(ids[:3])
		got, err := repo.GetByID(ctx, setID)
		require.NoError(t, err)
		require.Len(t, got.Items, 4)
		require.Equal(t, 4, got.DesiredItemCount)
		require.Equal(t, 4, got.MinItemCount)
		require.Equal(t, 4, got.MaxItemCount)
		for i, item := range got.Items {
			require.Equal(t, ids[i], *item.ProblemID)
			require.Equal(t, i+1, item.Position)
		}
		require.Equal(t, retained[ids[0]], got.Items[0].ID)
		require.Equal(t, retained[ids[2]], got.Items[2].ID)
		require.Equal(t, extra.ID, got.Items[3].ID)
		require.Equal(t, 42, got.Items[3].Score)
		require.Equal(t, extra.Notes, got.Items[3].Notes)
	}
}
