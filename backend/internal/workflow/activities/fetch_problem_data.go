package activities

import (
	"context"
	"fmt"
	"io"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
	"go.temporal.io/sdk/activity"
)

// FetchProblemDataResult contains the problem, its solutions, and test case
// inputs needed to re-validate an existing problem.
type FetchProblemDataResult struct {
	Problem         domain.Problem  `json:"problem"`
	MainSolution    domain.Solution `json:"main_solution"`
	BruteSolution   domain.Solution `json:"brute_solution"`
	TestCases       []TestCaseData  `json:"test_cases"`
	Outputs         []string        `json:"outputs,omitempty"`
	OutputArtifacts []*ArtifactRef  `json:"output_artifacts,omitempty"`
	// BruteIndices is the immutable, persisted v1 differential subset.  It is
	// intentionally separate from TestCases: large official cases must still
	// be run by the main solution, but must never be sent to an O(n^2)/O(n^3)
	// reference implementation merely because they are present in the suite.
	BruteIndices []int `json:"brute_indices,omitempty"`
	// TestManifestSchema records where the selection came from.  An empty value
	// means this is a legacy problem with no manifest; v2 uses an independent
	// oracle and therefore has no brute subset to execute in this workflow.
	TestManifestSchema string `json:"test_manifest_schema,omitempty"`
}

// FetchProblemDataActivity loads an existing problem's solutions and test data
// from the database and MinIO so that the ProblemValidationWorkflow can
// re-run the solutions against the test cases.
func (a *Activities) FetchProblemDataActivity(ctx context.Context, problemID uuid.UUID) (*FetchProblemDataResult, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("fetching problem data for validation", "problem_id", problemID)

	activity.RecordHeartbeat(ctx, "fetching problem from database")

	// 1. Fetch problem.
	problem, err := a.deps.ProblemRepo.GetByID(ctx, problemID)
	if err != nil {
		return nil, fmt.Errorf("fetching problem: %w", err)
	}

	// 2. Fetch solutions.
	activity.RecordHeartbeat(ctx, "fetching solutions")

	var solutions []domain.Solution
	rows, err := a.deps.ProblemRepo.QueryRaw(ctx,
		`SELECT id, problem_id, solution_type, language, source_code, compile_status, created_at
		 FROM solutions WHERE problem_id = $1 ORDER BY solution_type`,
		problemID,
	)
	if err != nil {
		return nil, fmt.Errorf("querying solutions: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var sol domain.Solution
		if err := rows.Scan(
			&sol.ID, &sol.ProblemID, &sol.SolutionType,
			&sol.Language, &sol.SourceCode, &sol.CompileStatus, &sol.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning solution: %w", err)
		}
		solutions = append(solutions, sol)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating solutions: %w", err)
	}

	var mainSol, bruteSol domain.Solution
	for _, sol := range solutions {
		switch sol.SolutionType {
		case domain.SolutionTypeMain:
			mainSol = sol
		case domain.SolutionTypeBrute:
			bruteSol = sol
		}
	}

	if mainSol.SourceCode == "" {
		return nil, fmt.Errorf("no main solution found for problem %s", problemID)
	}

	// 3. Fetch test cases and their data from MinIO.
	activity.RecordHeartbeat(ctx, "fetching test cases")

	testCases, err := a.deps.TestCaseRepo.GetByProblemID(ctx, problemID)
	if err != nil {
		return nil, fmt.Errorf("fetching test cases: %w", err)
	}

	bruteIndices, manifestSchema, err := a.resolveValidationBruteSelection(ctx, problem, len(testCases))
	if err != nil {
		return nil, fmt.Errorf("resolving persisted differential test selection: %w", err)
	}

	result := &FetchProblemDataResult{
		Problem:            *problem,
		MainSolution:       mainSol,
		BruteSolution:      bruteSol,
		TestCases:          make([]TestCaseData, 0, len(testCases)),
		OutputArtifacts:    make([]*ArtifactRef, 0, len(testCases)),
		BruteIndices:       bruteIndices,
		TestManifestSchema: manifestSchema,
	}

	for i, tc := range testCases {
		activity.RecordHeartbeat(ctx, fmt.Sprintf("downloading test case %d/%d", i+1, len(testCases)))

		input, err := a.downloadFromMinIO(ctx, tc.InputPath)
		if err != nil {
			return nil, fmt.Errorf("downloading test input %d: %w", i, err)
		}

		output, err := a.downloadFromMinIO(ctx, tc.OutputPath)
		if err != nil {
			return nil, fmt.Errorf("downloading test output %d: %w", i, err)
		}
		inputArtifact, err := a.putArtifact(ctx, []byte(input), "text/plain")
		if err != nil {
			return nil, fmt.Errorf("externalizing test input %d to CAS: %w", i, err)
		}
		outputArtifact, err := a.putArtifact(ctx, []byte(output), "text/plain")
		if err != nil {
			return nil, fmt.Errorf("externalizing test output %d to CAS: %w", i, err)
		}

		result.TestCases = append(result.TestCases, TestCaseData{
			InputArtifact: inputArtifact,
			GroupID:       tc.GroupID,
			IsSample:      tc.IsSample,
			Description:   tc.Description,
			Origin:        TestCaseOriginCustom,
		})
		result.OutputArtifacts = append(result.OutputArtifacts, outputArtifact)
	}

	logger.Info("problem data fetched",
		"title", problem.Title,
		"solutions", len(solutions),
		"test_cases", len(testCases),
	)

	return result, nil
}

// downloadFromMinIO downloads an object from MinIO and returns its contents
// as a string.
func (a *Activities) downloadFromMinIO(ctx context.Context, objectPath string) (string, error) {
	obj, err := a.deps.MinIO.GetObject(ctx, a.deps.MinioBucket, objectPath, minio.GetObjectOptions{})
	if err != nil {
		return "", fmt.Errorf("getting object %s: %w", objectPath, err)
	}
	defer obj.Close()

	data, err := io.ReadAll(io.LimitReader(obj, maxArtifactSizeBytes+1))
	if err != nil {
		return "", fmt.Errorf("reading object %s: %w", objectPath, err)
	}
	if int64(len(data)) > maxArtifactSizeBytes {
		return "", fmt.Errorf("object %s exceeds artifact size limit %d", objectPath, maxArtifactSizeBytes)
	}

	return string(data), nil
}
