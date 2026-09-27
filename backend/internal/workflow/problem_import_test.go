package workflow

import (
	"context"
	"fmt"
	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/rating"
	"github.com/Gingoo-TvT/Qraft/backend/internal/workflow/activities"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	tw "go.temporal.io/sdk/workflow"
	"testing"
	"time"
)

func TestProblemImportBatchSingleAndFinalChildComplete(t *testing.T) {
	for _, count := range []int{1, 3, 5} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			var suite testsuite.WorkflowTestSuite
			env := suite.NewTestWorkflowEnvironment()
			active, peak := 0, 0
			env.RegisterWorkflowWithOptions(func(ctx tw.Context, in ImportedProblemInput) (*domain.ProblemImportItemResult, error) {
				require.False(t, in.Params.GenerateEditorial, "preserved imports must not require optional editorial generation")
				active++
				if active > peak {
					peak = active
				}
				defer func() { active-- }()
				if e := tw.Sleep(ctx, time.Second); e != nil {
					return nil, e
				}
				if in.Source.ItemID == "2" {
					return nil, temporal.NewNonRetryableApplicationError("synthetic invalid input", "Fixture", nil)
				}
				return &domain.ProblemImportItemResult{ItemID: in.Source.ItemID, Title: in.Source.Title, Status: "imported", ProblemID: uuid.NewSHA1(uuid.NameSpaceURL, []byte(in.Source.ItemID)).String()}, nil
			}, tw.RegisterOptions{Name: "ImportedProblemWorkflow"})
			env.RegisterActivityWithOptions(func(_ context.Context, in activities.PrepareImportRatingInput) (*rating.Assessment, error) {
				return &rating.Assessment{ID: uuid.NewSHA1(uuid.NameSpaceURL, []byte(in.WorkflowID)), WorkflowID: "rating-" + in.ProblemID.String(), Subject: rating.Subject{Hash: "fixture"}}, nil
			}, activity.RegisterOptions{Name: "PrepareImportRatingActivity"})
			env.RegisterWorkflowWithOptions(func(ctx tw.Context, in rating.WorkflowInput) (*domain.WorkflowState, error) {
				if in.ProblemID == uuid.NewSHA1(uuid.NameSpaceURL, []byte("3")) {
					return nil, temporal.NewNonRetryableApplicationError("fixture rating unavailable", "Fixture", nil)
				}
				return &domain.WorkflowState{Status: domain.WorkflowStatusCompleted}, nil
			}, tw.RegisterOptions{Name: "RatingWorkflow"})
			items := make([]domain.SourceProblem, count)
			for i := range items {
				items[i] = domain.SourceProblem{ItemID: fmt.Sprint(i + 1), Title: "Same title", Statement: fmt.Sprintf("different full statement %d", i)}
			}
			in := domain.ProblemImportInput{Request: domain.ProblemImportRequest{Mode: "preserve_statement", Items: items}, ProviderConfig: &domain.ProviderRuntimeConfig{}}
			env.ExecuteWorkflow(ProblemImportWorkflow, in)
			require.NoError(t, env.GetWorkflowError())
			var out domain.ProblemImportState
			require.NoError(t, env.GetWorkflowResult(&out))
			require.Equal(t, "completed", out.Status)
			require.Equal(t, count, out.Counts.Total)
			require.Zero(t, out.Counts.Pending)
			require.Zero(t, out.Counts.Running)
			require.LessOrEqual(t, peak, 2)
			if count == 1 {
				require.Equal(t, 1, out.Counts.Imported)
			} else {
				require.Equal(t, 1, out.Counts.Failed)
				require.Equal(t, 1, out.Counts.AssessmentFailed)
				require.NotEmpty(t, out.Items[2].ProblemID)
				require.NotEmpty(t, out.Items[2].RatingAssessmentID)
			}
		})
	}
}

func TestProblemImportDuplicatesDoNotStopFollowingItems(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	calls := 0
	env.RegisterWorkflowWithOptions(func(ctx tw.Context, in ImportedProblemInput) (*domain.ProblemImportItemResult, error) {
		calls++
		if e := tw.Sleep(ctx, time.Second); e != nil {
			return nil, e
		}
		return &domain.ProblemImportItemResult{ItemID: in.Source.ItemID, Title: in.Source.Title, Status: "skipped_duplicate", DuplicateOf: "stored-id"}, nil
	}, tw.RegisterOptions{Name: "ImportedProblemWorkflow"})
	in := domain.ProblemImportInput{Request: domain.ProblemImportRequest{Mode: "preserve_statement", Items: []domain.SourceProblem{{ItemID: "a", Title: "T", Statement: "same"}, {ItemID: "b", Title: "T", Statement: "same"}, {ItemID: "c", Title: "T", Statement: "different"}}}, ProviderConfig: &domain.ProviderRuntimeConfig{}}
	env.ExecuteWorkflow(ProblemImportWorkflow, in)
	require.NoError(t, env.GetWorkflowError())
	var out domain.ProblemImportState
	require.NoError(t, env.GetWorkflowResult(&out))
	require.Equal(t, 2, calls)
	require.Equal(t, 3, out.Counts.SkippedDuplicate)
	require.Equal(t, "item:a", out.Items[1].DuplicateOf)
}

func TestImportedProblemUsesFreshDataWithoutOriginalityReviewOrRewriting(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	source := domain.SourceProblem{ItemID: "one", Title: "Preserved", Statement: "Original $a+b$\n```\n1 2\n```\n"}
	params := domain.DefaultProblemGenParams()
	params.GenerateEditorial = false
	env.RegisterActivityWithOptions(func(context.Context, activities.ImportDuplicateInput) (*activities.ImportDuplicateResult, error) {
		return &activities.ImportDuplicateResult{}, nil
	}, activity.RegisterOptions{Name: "CheckImportedDuplicateActivity"})
	env.RegisterActivityWithOptions(func(context.Context, activities.PrepareImportedStatementInput) (*activities.PreparedImportedStatement, error) {
		return &activities.PreparedImportedStatement{Statement: activities.StatementResult{Title: source.Title, Statement: source.Statement}, Evidence: domain.ImportSourceEvidence{Original: source, OriginalSHA256: source.Hash(), FinalSHA256: source.Hash()}, TimeLimit: 2000, MemoryLimit: 256}, nil
	}, activity.RegisterOptions{Name: "PrepareImportedStatementActivity"})
	data := activities.TestDataResult{PayloadVersion: activities.ActivityPayloadVersion, TestCases: make([]activities.TestCaseData, 10)}
	for i := range data.TestCases {
		data.TestCases[i] = activities.TestCaseData{Input: fmt.Sprint(i), IsSample: i < 2, Origin: activities.TestCaseOriginGenerator}
	}
	env.RegisterActivityWithOptions(func(_ context.Context, text string, cfg domain.TestDataConfig, p domain.ProblemGenParams) (*activities.TestDataResult, error) {
		require.Equal(t, source.Statement, text)
		require.Empty(t, cfg.CustomCases)
		return &data, nil
	}, activity.RegisterOptions{Name: "GenerateTestDataActivity"})
	env.RegisterActivityWithOptions(func(_ context.Context, text string, p domain.ProblemGenParams) (*activities.SolutionResult, error) {
		require.Equal(t, source.Statement, text)
		return &activities.SolutionResult{}, nil
	}, activity.RegisterOptions{Name: "GenerateSolutionActivity"})
	env.RegisterActivityWithOptions(func(context.Context, []domain.Solution) (*activities.CompileCheckResult, error) {
		return &activities.CompileCheckResult{AllCompiled: true}, nil
	}, activity.RegisterOptions{Name: "CompileCheckActivity"})
	env.RegisterActivityWithOptions(func(_ context.Context, s domain.Solution, cases []activities.TestCaseData, l activities.ExecutionLimits) (*activities.SandboxResult, error) {
		return &activities.SandboxResult{PayloadVersion: activities.ActivityPayloadVersion, Outputs: make([]string, len(cases))}, nil
	}, activity.RegisterOptions{Name: "RunSandboxActivity"})
	env.RegisterActivityWithOptions(func(context.Context, activities.SandboxResult, activities.SandboxResult) (*activities.ValidationResult, error) {
		return &activities.ValidationResult{AllPassed: true}, nil
	}, activity.RegisterOptions{Name: "ValidateActivity"})
	env.RegisterActivityWithOptions(func(_ context.Context, in activities.BuildTestManifestInput) (*activities.TestManifestV1, error) {
		require.NotEmpty(t, in.BruteIndices)
		return &activities.TestManifestV1{DifferentialCheckedCount: len(in.BruteIndices)}, nil
	}, activity.RegisterOptions{Name: "BuildTestManifestActivity"})
	env.RegisterActivityWithOptions(func(_ context.Context, in activities.StoreInput) (*activities.StoreResult, error) {
		require.Equal(t, source.Statement, in.Statement.Statement)
		require.Nil(t, in.ReviewQuarantine)
		require.Nil(t, in.QualityPassDraft)
		require.NotNil(t, in.ImportSource)
		require.NotNil(t, in.TestManifest)
		return &activities.StoreResult{ProblemID: uuid.New(), Status: domain.ProblemStatusDraft}, nil
	}, activity.RegisterOptions{Name: "StoreProblemActivity"})
	env.ExecuteWorkflow(ImportedProblemWorkflow, ImportedProblemInput{Source: source, Params: params})
	require.NoError(t, env.GetWorkflowError())
	var result domain.ProblemImportItemResult
	require.NoError(t, env.GetWorkflowResult(&result))
	require.Equal(t, "imported", result.Status)
	require.False(t, result.StatementChanged)
	// No activity capable of authoring, cleaning, finalizing or originality
	// reviewing a statement is registered: any such accidental call fails.
}

func TestDirectImportResumeKeepsStoredProblemsAndDoesNotRunKC(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	existingA, existingB, fresh := uuid.New(), uuid.New(), uuid.New()
	childCalls := 0
	env.RegisterWorkflowWithOptions(func(ctx tw.Context, in ImportedProblemInput) (*domain.ProblemImportItemResult, error) {
		childCalls++
		require.Equal(t, "c", in.Source.ItemID)
		return &domain.ProblemImportItemResult{ItemID: "c", Title: "C", Status: "imported", ProblemID: fresh.String()}, nil
	}, tw.RegisterOptions{Name: "ImportedProblemWorkflow"})
	estimates := 0
	env.RegisterActivityWithOptions(func(ctx context.Context, in activities.ImportDifficultyInput) (*activities.ImportDifficultyResult, error) {
		estimates++
		if in.ProblemID == existingB {
			return nil, temporal.NewNonRetryableApplicationError("fixture unavailable", "Fixture", nil)
		}
		return &activities.ImportDifficultyResult{Difficulty: 800, Reason: "simple arithmetic"}, nil
	}, activity.RegisterOptions{Name: "EstimateImportedDifficultyActivity"})
	env.RegisterActivityWithOptions(func(ctx context.Context, in activities.CreateImportedProblemSetInput) (string, error) {
		require.Equal(t, "original-batch", in.WorkflowID)
		require.ElementsMatch(t, []uuid.UUID{existingA, existingB, fresh}, in.ProblemIDs)
		return "retained-set", nil
	}, activity.RegisterOptions{Name: "CreateImportedProblemSetActivity"})
	items := []domain.SourceProblem{{ItemID: "a", Title: "A", Statement: "a"}, {ItemID: "b", Title: "B", Statement: "b"}, {ItemID: "c", Title: "C", Statement: "c"}, {ItemID: "d", Title: "D", Statement: "d"}}
	retained := []domain.ProblemImportItemResult{{ItemID: "a", Title: "A", Status: "imported", ProblemID: existingA.String()}, {ItemID: "b", Title: "B", Status: "assessment_failed", ProblemID: existingB.String(), Error: "old KC error"}, {}, {ItemID: "d", Title: "D", Status: "skipped_duplicate", DuplicateOf: "existing"}}
	env.ExecuteWorkflow(ProblemImportWorkflow, domain.ProblemImportInput{DirectDifficulty: true, CollectionWorkflowID: "original-batch", ResumeItems: retained, Request: domain.ProblemImportRequest{Mode: "preserve_statement", Items: items, CreateSet: true}, ProviderConfig: &domain.ProviderRuntimeConfig{}})
	require.NoError(t, env.GetWorkflowError())
	var out domain.ProblemImportState
	require.NoError(t, env.GetWorkflowResult(&out))
	require.Equal(t, 1, childCalls)
	require.Equal(t, 3, estimates)
	require.Equal(t, 3, out.Counts.Imported)
	require.Equal(t, 1, out.Counts.SkippedDuplicate)
	require.Zero(t, out.Counts.AssessmentFailed)
	require.Zero(t, out.Counts.Failed)
	require.Equal(t, 800, out.Items[0].EstimatedDifficulty)
	require.NotEmpty(t, out.Items[1].Warning)
	require.Empty(t, out.Items[1].Error)
	require.Equal(t, existingB.String(), out.Items[1].ProblemID)
	require.Equal(t, "retained-set", out.ProblemSetID)
	// No KC activity or workflow is registered: an accidental call fails here.
}

func TestImportedProblemChecksNewExampleWithBothSolutions(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	source := domain.SourceProblem{ItemID: "one", Title: "Preserved", Statement: "Original $a+b$\n```\n1 2\n```\n"}
	params := domain.DefaultProblemGenParams()
	params.GenerateEditorial = false
	env.RegisterActivityWithOptions(func(context.Context, activities.ImportDuplicateInput) (*activities.ImportDuplicateResult, error) {
		return &activities.ImportDuplicateResult{}, nil
	}, activity.RegisterOptions{Name: "CheckImportedDuplicateActivity"})
	env.RegisterActivityWithOptions(func(context.Context, activities.PrepareImportedStatementInput) (*activities.PreparedImportedStatement, error) {
		return &activities.PreparedImportedStatement{Statement: activities.StatementResult{Title: source.Title, Statement: source.Statement}, Evidence: domain.ImportSourceEvidence{Original: source, OriginalSHA256: source.Hash(), FinalSHA256: source.Hash()}, Samples: []activities.ImportedSample{{Input: "2", Output: "3", Origin: "generated"}}, TimeLimit: 2000, MemoryLimit: 256}, nil
	}, activity.RegisterOptions{Name: "PrepareImportedStatementActivity"})
	data := activities.TestDataResult{PayloadVersion: activities.ActivityPayloadVersion, TestCases: make([]activities.TestCaseData, 10)}
	for i := range data.TestCases {
		data.TestCases[i] = activities.TestCaseData{Input: fmt.Sprint(i), IsSample: i < 2, Origin: activities.TestCaseOriginGenerator}
	}
	env.RegisterActivityWithOptions(func(_ context.Context, text string, cfg domain.TestDataConfig, p domain.ProblemGenParams) (*activities.TestDataResult, error) {
		require.Equal(t, source.Statement, text)
		require.Empty(t, cfg.CustomCases)
		return &data, nil
	}, activity.RegisterOptions{Name: "GenerateTestDataActivity"})
	env.RegisterActivityWithOptions(func(_ context.Context, text string, p domain.ProblemGenParams) (*activities.SolutionResult, error) {
		require.Equal(t, source.Statement, text)
		return &activities.SolutionResult{MainSolution: domain.Solution{SolutionType: domain.SolutionTypeMain}, BruteSolution: domain.Solution{SolutionType: domain.SolutionTypeBrute}}, nil
	}, activity.RegisterOptions{Name: "GenerateSolutionActivity"})
	env.RegisterActivityWithOptions(func(context.Context, []domain.Solution) (*activities.CompileCheckResult, error) {
		return &activities.CompileCheckResult{AllCompiled: true}, nil
	}, activity.RegisterOptions{Name: "CompileCheckActivity"})
	sampleRuns := map[domain.SolutionType]int{}
	env.RegisterActivityWithOptions(func(_ context.Context, s domain.Solution, cases []activities.TestCaseData, l activities.ExecutionLimits) (*activities.SandboxResult, error) {
		if len(cases) == 1 && cases[0].Origin == activities.TestCaseOriginCustom {
			sampleRuns[s.SolutionType]++
		}
		return &activities.SandboxResult{PayloadVersion: activities.ActivityPayloadVersion, Outputs: make([]string, len(cases))}, nil
	}, activity.RegisterOptions{Name: "RunSandboxActivity"})
	env.RegisterActivityWithOptions(func(context.Context, activities.SandboxResult, activities.SandboxResult) (*activities.ValidationResult, error) {
		return &activities.ValidationResult{AllPassed: true}, nil
	}, activity.RegisterOptions{Name: "ValidateActivity"})
	env.RegisterActivityWithOptions(func(_ context.Context, in activities.BuildTestManifestInput) (*activities.TestManifestV1, error) {
		require.NotEmpty(t, in.BruteIndices)
		return &activities.TestManifestV1{DifferentialCheckedCount: len(in.BruteIndices)}, nil
	}, activity.RegisterOptions{Name: "BuildTestManifestActivity"})
	env.RegisterActivityWithOptions(func(_ context.Context, in activities.StoreInput) (*activities.StoreResult, error) {
		require.Equal(t, source.Statement, in.Statement.Statement)
		require.Nil(t, in.ReviewQuarantine)
		require.Nil(t, in.QualityPassDraft)
		require.NotNil(t, in.ImportSource)
		require.NotNil(t, in.TestManifest)
		return &activities.StoreResult{ProblemID: uuid.New(), Status: domain.ProblemStatusDraft}, nil
	}, activity.RegisterOptions{Name: "StoreProblemActivity"})
	env.ExecuteWorkflow(ImportedProblemWorkflow, ImportedProblemInput{Source: source, Params: params})
	require.NoError(t, env.GetWorkflowError())
	var result domain.ProblemImportItemResult
	require.NoError(t, env.GetWorkflowResult(&result))
	require.Equal(t, "imported", result.Status)
	require.Equal(t, 1, sampleRuns[domain.SolutionTypeMain])
	require.Equal(t, 1, sampleRuns[domain.SolutionTypeBrute])
	require.False(t, result.StatementChanged)
	// No activity capable of authoring, cleaning, finalizing or originality
	// reviewing a statement is registered: any such accidental call fails.
}
