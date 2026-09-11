//go:build integration

package service

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

type assemblyProblemReader struct{ repo *repository.ProblemRepository }

func (r assemblyProblemReader) GetProblem(ctx context.Context, id uuid.UUID) (*domain.Problem, error) {
	return r.repo.GetByID(ctx, id)
}

func TestAssemblyBankFilteringSaveAndExportIntegration(t *testing.T) {
	if os.Getenv("ALGOFORGE_INTEGRATION_DISPOSABLE_DB") != "true" {
		t.Skip("requires disposable migrated DATABASE_URL")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	require.NoError(t, err)
	defer pool.Close()
	repo := repository.NewProblemSetRepository(pool)
	qr := repository.NewQuizRepository(pool)
	quizzes := NewQuizService(qr, nil, nil, "")
	svc := NewProblemSetServiceWithDeps(repo, assemblyProblemReader{repository.NewProblemRepository(pool)}, quizzes, &HydroExportService{}, nil)
	marker := "assembly-" + uuid.NewString()
	// Include a published question, a duplicate statement, an out-of-range
	// published question, and a draft which must never enter the preview.
	var published uuid.UUID
	for i, spec := range []struct {
		status string
		rating int
		text   string
	}{
		{"published", 1200, "求和"}, {"published", 1200, "求和"}, {"published", 2500, "最短路"}, {"draft", 1200, "遍历"},
	} {
		id := uuid.New()
		if i == 0 {
			published = id
		}
		_, err = pool.Exec(ctx, `INSERT INTO problems(id,title,statement,level,difficulty,tags,status,time_limit,memory_limit) VALUES($1,$2,$3,'algorithm',$4,$5,$6,1000,256)`, id, marker, marker+spec.text, spec.rating, []string{marker}, spec.status)
		require.NoError(t, err)
	}
	for _, typ := range []domain.QuizType{domain.QuizTypeChoice, domain.QuizTypeFillBlank, domain.QuizTypeJudge} {
		code, err := qr.NextCodeForType(ctx, typ)
		require.NoError(t, err)
		q := &domain.QuizProblem{ID: uuid.New(), Code: code, Title: marker, Statement: marker + " 一棵含一个顶点的树有几条边？", Type: typ,
			Answers: []string{"0"}, Difficulty: domain.QuizDifficultyMedium, Visibility: domain.QuizVisibilityPrivate, Tags: []string{marker}, Explanation: "单顶点树没有边。", Subject: domain.QuizSubjectDataStructureAlgorithm}
		if typ == domain.QuizTypeChoice {
			q.Options = []domain.QuizOption{{Label: "A", Content: "0"}, {Label: "B", Content: "1"}}
			q.Answers = []string{"A"}
		}
		if typ == domain.QuizTypeJudge {
			q.Statement = marker + " 单顶点无边的图是一棵树。"
			q.Answers = []string{"对"}
		}
		if typ == domain.QuizTypeFillBlank {
			q.Statement = marker + " 单顶点树的边数为 ___。"
		}
		_, err = quizzes.Create(ctx, q)
		require.NoError(t, err)
		if typ == domain.QuizTypeJudge {
			kpID := uuid.New()
			_, err = pool.Exec(ctx, `INSERT INTO knowledge_points(id,subject,code,name) VALUES($1,'data_structure_algorithm',$2,$3)`, kpID, marker, marker+"-知识点")
			require.NoError(t, err)
			_, err = pool.Exec(ctx, `INSERT INTO quiz_knowledge_points(quiz_id,knowledge_point_id) VALUES($1,$2)`, q.ID, kpID)
			require.NoError(t, err)
		}
	}
	req := ProblemSetAssemblyRequest{Config: domain.ProblemSetGenerationConfig{Mode: "mixed", Distribution: []domain.ProblemSetTypeQuota{
		{Type: domain.QuizTypeProgramming, Count: 2, Score: 50}, {Type: domain.QuizTypeChoice, Count: 1, Score: 2},
		{Type: domain.QuizTypeFillBlank, Count: 1, Score: 5}, {Type: domain.QuizTypeJudge, Count: 1, Score: 2},
	}}, Filter: domain.ProblemSetAssemblyFilter{Tags: []string{marker}, MinDifficulty: 1000, MaxDifficulty: 1600, Seed: "integration"}}
	preview, err := svc.PreviewAssembly(ctx, req)
	require.NoError(t, err)
	require.Len(t, preview.Items, 4)
	require.Equal(t, 1, preview.MissingCount)
	require.Equal(t, 1, preview.Distribution[0].Available, "same statements should count once")
	require.Equal(t, 59, preview.TotalScore)
	refs := []domain.ProblemSetAssemblyRef{}
	for _, item := range preview.Items {
		refs = append(refs, item.ProblemSetAssemblyRef)
	}
	assembled, err := svc.Assemble(ctx, ProblemSetAssembleRequest{ProblemSetAssemblyRequest: req, Title: marker, Items: refs})
	require.NoError(t, err)
	require.Len(t, assembled.Items, 4)
	require.Equal(t, 59, assembled.TotalScore)
	require.False(t, assembled.Quality.ReadyForExport, "shortage must remain visible after saving")
	require.Nil(t, assembled.Generation, "assembly must not start generation")
	require.NotNil(t, assembled.GenerationConfig.Assembly)
	// Replay source refs with a source revision changed between preview and save.
	_, err = pool.Exec(ctx, `UPDATE problems SET title=title || ' changed' WHERE id=$1`, refs[0].ID)
	require.NoError(t, err)
	_, err = svc.Assemble(ctx, ProblemSetAssembleRequest{ProblemSetAssemblyRequest: req, Title: marker + " stale", Items: refs})
	require.ErrorIs(t, err, ErrConflict)
	req.Filter.ExcludeRecentSets = 1
	avoided, err := svc.PreviewAssembly(ctx, req)
	require.NoError(t, err)
	require.Empty(t, avoided.Items, "also exclude the unselected duplicate of a recently used statement")
	// Knowledge point names and objective difficulty are real SQL filters.
	kpReq := ProblemSetAssemblyRequest{Config: domain.ProblemSetGenerationConfig{Mode: "mixed", Distribution: []domain.ProblemSetTypeQuota{{Type: domain.QuizTypeJudge, Count: 1, Score: 3}}}, Filter: domain.ProblemSetAssemblyFilter{Tags: []string{marker + "-知识点"}, QuizDifficulty: domain.QuizDifficultyMedium}}
	kpPreview, err := svc.PreviewAssembly(ctx, kpReq)
	require.NoError(t, err)
	require.Len(t, kpPreview.Items, 1)
	kpReq.Filter.QuizDifficulty = domain.QuizDifficultyHard
	none, err := svc.PreviewAssembly(ctx, kpReq)
	require.NoError(t, err)
	require.Empty(t, none.Items)
	kpReq.Filter.QuizDifficulty = domain.QuizDifficultyMedium
	kpReq.Filter.Keyword = "not-present"
	none, err = svc.PreviewAssembly(ctx, kpReq)
	require.NoError(t, err)
	require.Empty(t, none.Items)
	kpReq.Filter.Keyword = ""
	// Export an objective-only paper through the normal fixed OJ package builder.
	paper, err := svc.Assemble(ctx, ProblemSetAssembleRequest{ProblemSetAssemblyRequest: kpReq, Title: marker + " objective", Items: []domain.ProblemSetAssemblyRef{kpPreview.Items[0].ProblemSetAssemblyRef}})
	require.NoError(t, err)
	require.True(t, paper.Quality.ReadyForExport)
	exported, err := svc.Export(ctx, paper.ID, false)
	require.NoError(t, err)
	require.NotEmpty(t, exported.Package.Content)
	// Atomic repository failure must not persist the parent either.
	pending := &domain.ProblemSet{ID: uuid.New(), Code: "FAIL-" + uuid.NewString(), Title: "rollback", Kind: domain.ProblemSetKindContest, Visibility: domain.ProblemSetVisibilityPrivate, Status: domain.ProblemSetStatusDraft, CreatedBy: "test", DesiredItemCount: 1}
	require.NoError(t, pending.NormalizeProblemSet())
	var revision time.Time
	require.NoError(t, pool.QueryRow(ctx, `SELECT updated_at FROM problems WHERE id=$1`, published).Scan(&revision))
	pending.Items = []domain.ProblemSetItem{{ProblemID: &published, Position: 0, Score: 1}} // violates position check
	err = repo.CreateAssembled(ctx, pending, []domain.ProblemSetAssemblyRef{{ID: published, Type: domain.QuizTypeProgramming, UpdatedAt: revision}})
	require.Error(t, err)
	_, err = repo.GetByID(ctx, pending.ID)
	require.ErrorIs(t, err, sql.ErrNoRows)
	// Missing source is rejected before the parent row is inserted.
	unknown := uuid.New()
	pending.Items[0].Position = 1
	err = repo.CreateAssembled(ctx, pending, []domain.ProblemSetAssemblyRef{{ID: unknown, Type: domain.QuizTypeProgramming, UpdatedAt: revision}})
	require.ErrorIs(t, err, repository.ErrProblemSetAssemblyChanged)
	_, err = repo.GetByID(ctx, pending.ID)
	require.ErrorIs(t, err, sql.ErrNoRows)
	fmt.Println("assembly integration: mixed quotas, filters, duplicates, shortages, recent exclusions, stale refs, atomic rollback and objective OJ ZIP passed")
}
