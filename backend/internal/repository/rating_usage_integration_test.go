//go:build integration

package repository_test

import (
	"context"
	"os"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/rating"
	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
	"github.com/Gingoo-TvT/Qraft/backend/internal/service"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

type ratingUsageProblemReader struct{ repo *repository.ProblemRepository }

func (r ratingUsageProblemReader) GetProblem(ctx context.Context, id uuid.UUID) (*domain.Problem, error) {
	return r.repo.GetByID(ctx, id)
}
func TestRatingSearchAndAssemblyRespectOfficialBasisIntegration(t *testing.T) {
	if os.Getenv("ALGOFORGE_INTEGRATION_DISPOSABLE_DB") != "true" {
		t.Skip("requires explicitly disposable migrated DATABASE_URL")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	require.NoError(t, err)
	defer pool.Close()
	id := uuid.New()
	title := "Rating usage fixture " + id.String()
	_, err = pool.Exec(ctx, "INSERT INTO problems(id,title,statement,level,difficulty,tags,status) VALUES($1,$2,'Compute a+b. 0<=a,b<=100.','algorithm',1900,ARRAY['math'],'published')", id, title)
	require.NoError(t, err)
	defer pool.Exec(ctx, "DELETE FROM problems WHERE id=$1", id)
	_, err = pool.Exec(ctx, "INSERT INTO testcases(id,problem_id,test_index,is_sample,input_path,output_path) VALUES($1,$2,0,true,'usage.in','usage.out')", uuid.New(), id)
	require.NoError(t, err)
	output := "3"
	r := repository.NewRatingRepository(pool)
	r.SetArtifactReader(func(_ context.Context, path string) ([]byte, error) {
		if path == "usage.in" {
			return []byte("1 2"), nil
		}
		return []byte(output), nil
	})
	assessment, err := r.CreateAssessment(ctx, id, "fixture-admin")
	require.NoError(t, err)
	proposed := 1400
	require.NoError(t, r.UpdateAssessment(ctx, assessment.ID, "completed", "complete", &rating.Report{SnapshotHash: assessment.Subject.Hash, Validity: "tested_candidates", Estimate: rating.ReferenceEstimate{Representative: &proposed}}, ""))
	input := rating.DecisionInput{SubjectHash: assessment.Subject.Hash, FeedbackHash: rating.FeedbackSnapshotHash(nil), AssessmentID: &assessment.ID, Action: "accept", Reason: "Synthetic basis fixture"}
	first, err := r.Decide(ctx, id, input, "fixture-admin")
	require.NoError(t, err)
	searches := repository.NewQuestionSearchRepository(pool)
	searches.SetRatingRepository(r)
	min, max := 1300, 1500
	filter := domain.QuestionSearchFilter{Keyword: title, Type: domain.QuizTypeProgramming, MinDifficulty: &min, MaxDifficulty: &max, RatingBasis: "official", Page: 1, Size: 20}
	found, err := searches.Search(ctx, filter)
	require.NoError(t, err)
	require.Equal(t, 1, found.Total)
	require.Len(t, found.Items, 1)
	require.Equal(t, 1400, *found.Items[0].Difficulty)
	require.Equal(t, "official", found.Items[0].RatingBasis)
	filter.RatingBasis = "target"
	found, err = searches.Search(ctx, filter)
	require.NoError(t, err)
	require.Zero(t, found.Total)
	min, max = 1800, 2000
	found, err = searches.Search(ctx, filter)
	require.NoError(t, err)
	require.Equal(t, 1, found.Total)
	require.Equal(t, 1900, *found.Items[0].Difficulty)
	require.Equal(t, "target", found.Items[0].RatingBasis)
	filter.RatingBasis = "official"
	found, err = searches.Search(ctx, filter)
	require.NoError(t, err)
	require.Zero(t, found.Total)
	sets := repository.NewProblemSetRepository(pool)
	sets.SetRatingRepository(r)
	setSvc := service.NewProblemSetServiceWithDeps(sets, ratingUsageProblemReader{repository.NewProblemRepository(pool)}, nil, nil, nil)
	request := service.ProblemSetAssemblyRequest{
		Config: domain.ProblemSetGenerationConfig{Mode: "programming", Distribution: []domain.ProblemSetTypeQuota{{Type: domain.QuizTypeProgramming, Count: 1, Score: 100}}},
		Filter: domain.ProblemSetAssemblyFilter{RatingBasis: "official", Keyword: title, MinDifficulty: 1300, MaxDifficulty: 1500, Seed: "fixture"},
	}
	preview, err := setSvc.PreviewAssembly(ctx, request)
	require.NoError(t, err)
	require.Len(t, preview.Items, 1)
	oldRef := preview.Items[0].ProblemSetAssemblyRef
	require.Equal(t, first.ID, *oldRef.RatingDecisionID)
	saved, err := setSvc.Assemble(ctx, service.ProblemSetAssembleRequest{ProblemSetAssemblyRequest: request, Title: "Synthetic saved rating snapshot", Items: []domain.ProblemSetAssemblyRef{oldRef}, CreatedBy: "fixture-admin"})
	require.NoError(t, err)
	defer pool.Exec(ctx, "DELETE FROM problem_sets WHERE id=$1", saved.ID)
	require.Len(t, saved.GenerationConfig.Assembly.RatingSnapshots, 1)
	require.Equal(t, 1400, saved.GenerationConfig.Assembly.RatingSnapshots[0].Rating)
	require.Equal(t, first.ID, saved.GenerationConfig.Assembly.RatingSnapshots[0].DecisionID)
	// Re-confirming the same numeric value still changes the evidence decision.
	input.ExpectedDecisionID = &first.ID
	next, err := r.Decide(ctx, id, input, "fixture-admin")
	require.NoError(t, err)
	require.NotEqual(t, first.ID, next.ID)
	_, err = setSvc.Assemble(ctx, service.ProblemSetAssembleRequest{ProblemSetAssemblyRequest: request, Title: "Should reject outdated decision", Items: []domain.ProblemSetAssemblyRef{oldRef}})
	require.ErrorIs(t, err, service.ErrConflict)
	fresh, err := setSvc.PreviewAssembly(ctx, request)
	require.NoError(t, err)
	require.Len(t, fresh.Items, 1)
	require.Equal(t, next.ID, *fresh.Items[0].RatingDecisionID)
	// Same storage path, same problem updated_at, but changed output bytes.
	output = "4"
	min, max = 1300, 1500
	found, err = searches.Search(ctx, filter)
	require.NoError(t, err)
	require.Zero(t, found.Total)
	excluded, err := setSvc.PreviewAssembly(ctx, request)
	require.NoError(t, err)
	require.Empty(t, excluded.Items)
	_, err = setSvc.Assemble(ctx, service.ProblemSetAssembleRequest{ProblemSetAssemblyRequest: request, Title: "Should reject stale bytes", Items: []domain.ProblemSetAssemblyRef{fresh.Items[0].ProblemSetAssemblyRef}})
	require.ErrorIs(t, err, service.ErrConflict)
	// The previously saved paper keeps its historical official evidence.
	historical, err := sets.GetByID(ctx, saved.ID)
	require.NoError(t, err)
	require.Equal(t, first.ID, historical.GenerationConfig.Assembly.RatingSnapshots[0].DecisionID)
	require.Equal(t, 1400, historical.GenerationConfig.Assembly.RatingSnapshots[0].Rating)
	renamedTitle := "Renamed assembled fixture"
	renamed, err := setSvc.Update(ctx, saved.ID, service.ProblemSetUpdateRequest{Title: &renamedTitle})
	require.NoError(t, err)
	require.Equal(t, renamedTitle, renamed.Title)
	require.Equal(t, historical.GenerationConfig.Assembly.RatingSnapshots, renamed.GenerationConfig.Assembly.RatingSnapshots)
	forged := domain.ProblemSetGenerationConfig{
		Mode: "programming", Distribution: []domain.ProblemSetTypeQuota{{Type: domain.QuizTypeProgramming, Count: 1, Score: 100}},
		Assembly: &domain.ProblemSetAssemblyFilter{RatingBasis: "official", MinDifficulty: 1300, MaxDifficulty: 1500, RatingSnapshots: []domain.ProblemSetAssemblyRatingSnapshot{{ProblemID: id, Rating: 3500, SubjectHash: "forged", DecisionID: uuid.New()}}},
	}
	edited, err := setSvc.Update(ctx, saved.ID, service.ProblemSetUpdateRequest{GenerationConfig: &forged})
	require.NoError(t, err)
	require.Equal(t, historical.GenerationConfig.Assembly.RatingSnapshots, edited.GenerationConfig.Assembly.RatingSnapshots, "incoming configuration cannot replace review history")
	absent := domain.ProblemSetGenerationConfig{Mode: "programming", Distribution: []domain.ProblemSetTypeQuota{{Type: domain.QuizTypeProgramming, Count: 1, Score: 100}}}
	edited, err = setSvc.Update(ctx, saved.ID, service.ProblemSetUpdateRequest{GenerationConfig: &absent})
	require.NoError(t, err)
	require.NotNil(t, edited.GenerationConfig.Assembly)
	require.Equal(t, historical.GenerationConfig.Assembly.RatingSnapshots, edited.GenerationConfig.Assembly.RatingSnapshots, "omitted assembly preserves historical rating evidence")

}
