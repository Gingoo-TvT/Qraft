package workflow

import (
	"fmt"
	"strings"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/workflow/activities"
	"github.com/google/uuid"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const (
	problemValidationBruteMemoryV1ChangeID        = "problem-validation-brute-memory-v1"
	problemValidationDifferentialSubsetV1ChangeID = "problem-validation-differential-subset-v1"
	// New validation runs use the same semantic-safe legacy fallback as new
	// generation runs. Existing histories without this marker retain their
	// recorded selection for replay compatibility.
	problemValidationBruteSelectionV2ChangeID = "problem-validation-brute-selection-v2"
	problemValidationMemoryFloorV1ChangeID    = "problem-validation-memory-floor-v1"
)

// ProblemValidationWorkflow re-validates an existing problem by fetching its
// solutions and test data from the database, re-running the solutions in the
// sandbox, and comparing outputs. This is triggered by the "validate" button
// on the problem detail page.
func ProblemValidationWorkflow(ctx workflow.Context, problemID uuid.UUID) (*domain.WorkflowState, error) {
	logger := workflow.GetLogger(ctx)
	payloadPatchVersion := workflow.GetVersion(
		ctx,
		"problem-validation-activity-payload-v1",
		workflow.DefaultVersion,
		activities.ActivityPayloadVersion,
	)
	statePayloadPatchVersion := workflow.GetVersion(
		ctx,
		"workflow-state-step-output-compaction-v1",
		workflow.DefaultVersion,
		1,
	)
	editRefreshPatchVersion := workflow.GetVersion(
		ctx,
		"problem-validation-edit-refresh-v1",
		workflow.DefaultVersion,
		1,
	)
	bruteMemoryPatchVersion := workflow.GetVersion(
		ctx,
		problemValidationBruteMemoryV1ChangeID,
		workflow.DefaultVersion,
		1,
	)
	differentialSubsetPatchVersion := workflow.GetVersion(
		ctx,
		problemValidationDifferentialSubsetV1ChangeID,
		workflow.DefaultVersion,
		1,
	)
	memoryFloorPatchVersion := workflow.GetVersion(
		ctx,
		problemValidationMemoryFloorV1ChangeID,
		workflow.DefaultVersion,
		1,
	)
	bruteSelectionVersion := workflow.DefaultVersion
	if differentialSubsetPatchVersion >= 1 {
		bruteSelectionVersion = workflow.GetVersion(
			ctx,
			problemValidationBruteSelectionV2ChangeID,
			workflow.DefaultVersion,
			1,
		)
	}
	logger.Info("ProblemValidationWorkflow started", "problem_id", problemID)

	now := workflow.Now(ctx)
	state := &domain.WorkflowState{
		Status:    domain.WorkflowStatusRunning,
		Progress:  0,
		StartedAt: &now,
	}
	if err := workflow.SetQueryHandler(ctx, domain.WorkflowStateQueryName, func() (domain.WorkflowStateQuery, error) {
		return domain.WorkflowStateQuery{State: *state}, nil
	}); err != nil {
		state.MarkFailedAt(fmt.Sprintf("register workflow state query: %v", err), workflow.Now(ctx))
		return state, fmt.Errorf("register workflow state query: %w", err)
	}
	markFailed := func(reason string) {
		if payloadPatchVersion >= activities.ActivityPayloadVersion {
			state.MarkFailedAt(reason, workflow.Now(ctx))
			return
		}
		state.MarkFailed(reason)
	}
	markCompleted := func() {
		if payloadPatchVersion >= activities.ActivityPayloadVersion {
			state.MarkCompletedAt(workflow.Now(ctx))
			return
		}
		state.MarkCompleted()
	}

	// Activity options.
	fetchOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    2 * time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    30 * time.Second,
			MaximumAttempts:    3,
		},
	}

	sandboxOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 15 * time.Minute,
		HeartbeatTimeout:    30 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    2 * time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    2 * time.Minute,
			MaximumAttempts:    2,
		},
	}

	fastOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    1 * time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    30 * time.Second,
			MaximumAttempts:    3,
		},
	}

	fetchCtx := workflow.WithActivityOptions(ctx, fetchOpts)
	sandboxCtx := workflow.WithActivityOptions(ctx, sandboxOpts)
	fastCtx := workflow.WithActivityOptions(ctx, fastOpts)
	// A reference-program runtime verdict is a deterministic defect in the
	// generated brute solution, not a transient activity failure.  Do not
	// spend another remote run on the same bad oracle before reporting it.
	referenceSandboxOpts := sandboxOpts
	referenceSandboxOpts.RetryPolicy = &temporal.RetryPolicy{MaximumAttempts: 1}
	referenceSandboxCtx := workflow.WithActivityOptions(ctx, referenceSandboxOpts)

	// Step 1: Fetch problem data.
	state.CurrentStep = "fetch_data"
	stepStart := workflow.Now(ctx)

	var fetchResult activities.FetchProblemDataResult
	if err := workflow.ExecuteActivity(fetchCtx, "FetchProblemDataActivity", problemID).Get(ctx, &fetchResult); err != nil {
		state.RecordStep(failedStep("fetch_data", stepStart, workflow.Now(ctx), err))
		markFailed(fmt.Sprintf("failed to fetch problem data: %v", err))
		return state, err
	}

	state.RecordStep(completedStep("fetch_data", stepStart, workflow.Now(ctx),
		fmt.Sprintf("fetched %d test cases", len(fetchResult.TestCases))))
	logger.Info("problem data fetched",
		"title", fetchResult.Problem.Title,
		"test_cases", len(fetchResult.TestCases),
	)

	// Step 2: Compile check.
	state.CurrentStep = domain.StepCompileCheck
	stepStart = workflow.Now(ctx)

	solutions := []domain.Solution{fetchResult.MainSolution}
	if fetchResult.BruteSolution.SourceCode != "" {
		solutions = append(solutions, fetchResult.BruteSolution)
	}

	var compileResult activities.CompileCheckResult
	if err := workflow.ExecuteActivity(sandboxCtx, "CompileCheckActivity", solutions).Get(ctx, &compileResult); err != nil {
		state.RecordStep(failedStep(domain.StepCompileCheck, stepStart, workflow.Now(ctx), err))
		markFailed(fmt.Sprintf("compile check failed: %v", err))
		return state, err
	}

	if !compileResult.AllCompiled {
		compileErr := fmt.Errorf("%s", compileResult.ErrorDetail)
		state.RecordStep(failedStep(domain.StepCompileCheck, stepStart, workflow.Now(ctx), compileErr))
		if activities.IsReferenceSolutionFailureText(compileResult.ErrorDetail) {
			errMsg := fmt.Sprintf("reference/brute solution program failure during compile; the main solution was not rejected: %s", compileResult.ErrorDetail)
			markFailed(errMsg)
			return state, temporal.NewNonRetryableApplicationError(errMsg, "ReferenceSolutionFailure", nil)
		}
		markFailed(fmt.Sprintf("compilation failed: %s", compileResult.ErrorDetail))
		return state, temporal.NewNonRetryableApplicationError(
			compileResult.ErrorDetail, "CompilationError", nil)
	}

	state.RecordStep(completedStep(domain.StepCompileCheck, stepStart, workflow.Now(ctx), compileResult))
	logger.Info("compilation check passed")

	// Step 3: Run main solution in sandbox.
	state.CurrentStep = domain.StepRunSandbox
	stepStart = workflow.Now(ctx)

	limits := activities.ExecutionLimits{
		TimeLimitMs:   fetchResult.Problem.TimeLimit,
		MemoryLimitMB: fetchResult.Problem.MemoryLimit,
	}
	if limits.TimeLimitMs == 0 {
		limits.TimeLimitMs = 2000
	}
	if limits.MemoryLimitMB == 0 {
		limits.MemoryLimitMB = 256
	}
	if memoryFloorPatchVersion >= 1 && limits.MemoryLimitMB < problemResourceMinimumMemoryMBV1 {
		limits.MemoryLimitMB = problemResourceMinimumMemoryMBV1
	}

	var mainSandboxResult activities.SandboxResult
	validationMode := "stored_outputs"
	var referenceAudit *activities.SandboxAuditMetadata
	bruteBudgetVersion := workflow.DefaultVersion
	bruteBudgetVersionResolved := false
	resolveBruteBudgetVersion := func() workflow.Version {
		if !bruteBudgetVersionResolved {
			bruteBudgetVersion = workflow.GetVersion(
				ctx,
				problemBruteCorrectnessBudgetV1ChangeID,
				workflow.DefaultVersion,
				1,
			)
			bruteBudgetVersionResolved = true
		}
		return bruteBudgetVersion
	}
	if err := workflow.ExecuteActivity(sandboxCtx, "RunSandboxActivity",
		fetchResult.MainSolution, fetchResult.TestCases, limits,
	).Get(ctx, &mainSandboxResult); err != nil {
		state.RecordStep(failedStep(domain.StepRunSandbox, stepStart, workflow.Now(ctx), err))
		markFailed(fmt.Sprintf("main solution sandbox failed: %v", err))
		return state, err
	}

	if differentialSubsetPatchVersion >= 1 {
		// The submitted/model solution is always judged against the complete
		// official suite and its persisted expected outputs first.  The brute
		// program is not an oracle for large cases and is never involved in this
		// full-suite gate.
		state.RecordStep(completedStep(domain.StepRunSandbox, stepStart, workflow.Now(ctx),
			fmt.Sprintf("main solution sandbox completed on %d official test cases", len(fetchResult.TestCases))))

		state.CurrentStep = domain.StepValidate
		stepStart = workflow.Now(ctx)
		storedResult := activities.SandboxResult{
			Outputs:         fetchResult.Outputs,
			OutputArtifacts: fetchResult.OutputArtifacts,
		}
		if payloadPatchVersion >= activities.ActivityPayloadVersion {
			storedResult.PayloadVersion = activities.ActivityPayloadVersion
		}
		var storedValidation activities.ValidationResult
		if err := workflow.ExecuteActivity(fastCtx, "ValidateActivity",
			mainSandboxResult, storedResult,
		).Get(ctx, &storedValidation); err != nil {
			state.RecordStep(failedStep(domain.StepValidate, stepStart, workflow.Now(ctx), err))
			markFailed(fmt.Sprintf("stored-output validation activity error: %v", err))
			return state, err
		}
		if !storedValidation.AllPassed {
			errMsg := fmt.Sprintf("main solution validation failed against stored outputs: %d mismatches found", len(storedValidation.Mismatches))
			state.RecordStep(failedStepWithOutput(domain.StepValidate, stepStart, workflow.Now(ctx), fmt.Errorf("%s", errMsg), storedValidation))
			markFailed(errMsg)
			return state, temporal.NewNonRetryableApplicationError(errMsg, "ValidationFailed", nil)
		}
		state.RecordStep(completedStepVersioned(statePayloadPatchVersion, domain.StepValidate, stepStart, workflow.Now(ctx), storedValidation))
		logger.Info("main solution passed full official-suite validation")

		// Only the immutable v1 manifest-selected subset is sent to brute.  A
		// v2 independent-oracle manifest deliberately disables this legacy
		// comparison, and pre-manifest problems use a sample-only fallback rather
		// than risking a full-suite reference run.
		var bruteCases []activities.TestCaseData
		var bruteIndices []int
		var selectionErr error
		if bruteSelectionVersion >= 1 {
			bruteCases, bruteIndices, selectionErr = validationBruteCasesV2(fetchResult)
		} else {
			bruteCases, bruteIndices, selectionErr = validationBruteCasesV1(fetchResult)
		}
		if selectionErr != nil {
			state.CurrentStep = domain.StepValidate
			selectionStepStart := workflow.Now(ctx)
			state.RecordStep(failedStep(domain.StepValidate, selectionStepStart, workflow.Now(ctx), selectionErr))
			msg := fmt.Sprintf("differential test selection is invalid: %v", selectionErr)
			markFailed(msg)
			return state, temporal.NewNonRetryableApplicationError(msg, "DifferentialSelectionError", nil)
		}
		if fetchResult.BruteSolution.SourceCode != "" && len(bruteCases) > 0 {
			state.CurrentStep = domain.StepRunSandbox
			bruteStepStart := workflow.Now(ctx)
			bruteLimits := activities.ExecutionLimits{
				TimeLimitMs:   limits.TimeLimitMs * 3,
				MemoryLimitMB: limits.MemoryLimitMB * 2,
			}
			if resolveBruteBudgetVersion() >= 1 {
				bruteLimits = problemResourceBruteLimitsV1(limits)
			} else if bruteMemoryPatchVersion >= 1 {
				// Replay of a history that predates the correctness-only budget
				// marker retains its already-recorded relaxed time but fixed memory.
				bruteLimits.MemoryLimitMB = problemResourceBruteMemoryLimitMBV1
			}

			var bruteSandboxResult activities.SandboxResult
			if err := workflow.ExecuteActivity(referenceSandboxCtx, "RunSandboxActivity",
				fetchResult.BruteSolution, bruteCases, bruteLimits,
			).Get(ctx, &bruteSandboxResult); err != nil {
				if activities.IsReferenceSolutionFailure(err) {
					errMsg := fmt.Sprintf("reference/brute solution program failure during differential check; the main solution was not rejected by this runtime verdict: %v", err)
					state.RecordStep(failedStep(domain.StepRunSandbox, bruteStepStart, workflow.Now(ctx), fmt.Errorf("%s", errMsg)))
					markFailed(errMsg)
					return state, temporal.NewNonRetryableApplicationError(errMsg, "ReferenceSolutionFailure", nil)
				}
				errMsg := fmt.Sprintf("reference sandbox execution unavailable while checking %d selected cases: %v", len(bruteCases), err)
				state.RecordStep(failedStep(domain.StepRunSandbox, bruteStepStart, workflow.Now(ctx), fmt.Errorf("%s", errMsg)))
				markFailed(errMsg)
				return state, err
			}
			validationMode = "differential"
			audit := bruteSandboxResult.Audit
			referenceAudit = &audit
			state.RecordStep(completedStep(domain.StepRunSandbox, bruteStepStart, workflow.Now(ctx),
				fmt.Sprintf("reference/brute checked %d selected cases; main covered %d official cases", len(bruteCases), len(fetchResult.TestCases))))

			state.CurrentStep = domain.StepValidate
			stepStart = workflow.Now(ctx)
			mainSubsetResult := pickMainOutputsForBruteSubset(mainSandboxResult, bruteIndices)
			var differentialValidation activities.ValidationResult
			if err := workflow.ExecuteActivity(fastCtx, "ValidateActivity",
				mainSubsetResult, bruteSandboxResult,
			).Get(ctx, &differentialValidation); err != nil {
				state.RecordStep(failedStep(domain.StepValidate, stepStart, workflow.Now(ctx), err))
				markFailed(fmt.Sprintf("differential validation activity error: %v", err))
				return state, err
			}
			if !differentialValidation.AllPassed {
				errMsg := fmt.Sprintf("differential check found %d mismatches between main and reference outputs", len(differentialValidation.Mismatches))
				state.RecordStep(failedStepWithOutput(domain.StepValidate, stepStart, workflow.Now(ctx), fmt.Errorf("%s", errMsg), differentialValidation))
				markFailed(errMsg)
				return state, temporal.NewNonRetryableApplicationError(errMsg, "DifferentialMismatch", nil)
			}
			state.RecordStep(completedStepVersioned(statePayloadPatchVersion, domain.StepValidate, stepStart, workflow.Now(ctx), differentialValidation))
			logger.Info("differential validation passed on bounded reference subset",
				"brute_cases", len(bruteCases), "official_cases", len(fetchResult.TestCases))
		} else if fetchResult.BruteSolution.SourceCode != "" {
			logger.Info("skipping legacy brute execution because no manifest-selected subset is available",
				"manifest_schema", fetchResult.TestManifestSchema,
				"official_cases", len(fetchResult.TestCases))
		}
	} else {
		// Legacy histories retain their original all-testcase brute behavior for
		// Temporal replay compatibility.  New executions never enter this path.
		if fetchResult.BruteSolution.SourceCode != "" {
			bruteLimits := activities.ExecutionLimits{
				TimeLimitMs:   limits.TimeLimitMs * 3,
				MemoryLimitMB: limits.MemoryLimitMB * 2,
			}
			if bruteMemoryPatchVersion >= 1 {
				bruteLimits.MemoryLimitMB = problemResourceBruteMemoryLimitMBV1
			}

			var bruteSandboxResult activities.SandboxResult
			if err := workflow.ExecuteActivity(sandboxCtx, "RunSandboxActivity",
				fetchResult.BruteSolution, fetchResult.TestCases, bruteLimits,
			).Get(ctx, &bruteSandboxResult); err != nil {
				state.RecordStep(failedStep(domain.StepRunSandbox, stepStart, workflow.Now(ctx), err))
				markFailed(fmt.Sprintf("brute solution sandbox failed: %v", err))
				return state, err
			}
			validationMode = "differential"
			audit := bruteSandboxResult.Audit
			referenceAudit = &audit

			state.RecordStep(completedStep(domain.StepRunSandbox, stepStart, workflow.Now(ctx), "sandbox runs completed"))

			// Step 4: Validate main vs brute outputs.
			state.CurrentStep = domain.StepValidate
			stepStart = workflow.Now(ctx)

			var validationResult activities.ValidationResult
			if err := workflow.ExecuteActivity(fastCtx, "ValidateActivity",
				mainSandboxResult, bruteSandboxResult,
			).Get(ctx, &validationResult); err != nil {
				state.RecordStep(failedStep(domain.StepValidate, stepStart, workflow.Now(ctx), err))
				markFailed(fmt.Sprintf("validation activity error: %v", err))
				return state, err
			}

			if !validationResult.AllPassed {
				errMsg := fmt.Sprintf("validation failed: %d mismatches found", len(validationResult.Mismatches))
				state.RecordStep(failedStep(domain.StepValidate, stepStart, workflow.Now(ctx), fmt.Errorf("%s", errMsg)))
				markFailed(errMsg)
				return state, temporal.NewNonRetryableApplicationError(errMsg, "ValidationFailed", nil)
			}

			state.RecordStep(completedStepVersioned(statePayloadPatchVersion, domain.StepValidate, stepStart, workflow.Now(ctx), validationResult))
			logger.Info("validation passed: main and brute outputs match")
		} else {
			// Legacy no-brute path: compare main outputs against stored outputs.
			state.RecordStep(completedStep(domain.StepRunSandbox, stepStart, workflow.Now(ctx), "main solution sandbox completed"))

			state.CurrentStep = domain.StepValidate
			stepStart = workflow.Now(ctx)

			var validationResult activities.ValidationResult
			storedResult := activities.SandboxResult{
				Outputs:         fetchResult.Outputs,
				OutputArtifacts: fetchResult.OutputArtifacts,
			}
			if payloadPatchVersion >= activities.ActivityPayloadVersion {
				storedResult.PayloadVersion = activities.ActivityPayloadVersion
			}
			if err := workflow.ExecuteActivity(fastCtx, "ValidateActivity",
				mainSandboxResult, storedResult,
			).Get(ctx, &validationResult); err != nil {
				state.RecordStep(failedStep(domain.StepValidate, stepStart, workflow.Now(ctx), err))
				markFailed(fmt.Sprintf("validation activity error: %v", err))
				return state, err
			}

			if !validationResult.AllPassed {
				errMsg := fmt.Sprintf("validation failed: %d mismatches vs stored outputs", len(validationResult.Mismatches))
				state.RecordStep(failedStep(domain.StepValidate, stepStart, workflow.Now(ctx), fmt.Errorf("%s", errMsg)))
				markFailed(errMsg)
				return state, temporal.NewNonRetryableApplicationError(errMsg, "ValidationFailed", nil)
			}

			state.RecordStep(completedStepVersioned(statePayloadPatchVersion, domain.StepValidate, stepStart, workflow.Now(ctx), validationResult))
			logger.Info("validation passed: main outputs match stored outputs")
		}
	}

	// An edited problem is not publishable until the exact revision that was
	// validated has been re-embedded and the edit-refresh gate has completed.
	// Older histories skip this newly versioned activity during replay.
	if editRefreshPatchVersion >= 1 && activities.ProblemRequiresEditRefresh(fetchResult.Problem) {
		const refreshStep = domain.WorkflowStep("edit_refresh")
		state.CurrentStep = refreshStep
		stepStart := workflow.Now(ctx)
		refreshInput := activities.RefreshEditedProblemInput{
			ProblemID:           problemID,
			ExpectedUpdatedAt:   fetchResult.Problem.UpdatedAt,
			ExpectedContentHash: activities.ProblemEmbeddingContentHash(fetchResult.Problem),
			ValidationMode:      validationMode,
			TestCaseCount:       len(fetchResult.TestCases),
			MainAudit:           mainSandboxResult.Audit,
			ReferenceAudit:      referenceAudit,
		}
		var refreshResult activities.RefreshEditedProblemResult
		if err := workflow.ExecuteActivity(fastCtx, "RefreshEditedProblemActivity", refreshInput).Get(ctx, &refreshResult); err != nil {
			state.RecordStep(failedStep(refreshStep, stepStart, workflow.Now(ctx), err))
			markFailed(fmt.Sprintf("edited problem refresh failed: %v", err))
			return state, err
		}

		if refreshResult.Report.Decision != "go" && len(refreshResult.Report.BlockingIssues) > 0 {
			errMsg := fmt.Sprintf("edited problem refresh blocked: %s", strings.Join(refreshResult.Report.BlockingIssues, "; "))
			state.RecordStep(failedStepWithOutput(refreshStep, stepStart, workflow.Now(ctx), fmt.Errorf("%s", errMsg), refreshResult.Report))
			markFailed(errMsg)
			return state, temporal.NewNonRetryableApplicationError(errMsg, "ProblemEditRefreshBlocked", nil)
		}
		state.RecordStep(completedStepVersioned(statePayloadPatchVersion, refreshStep, stepStart, workflow.Now(ctx), refreshResult.Report))
		logger.Info("edited problem re-embedded and edit-refresh gate completed",
			"problem_id", problemID,
			"decision", refreshResult.Report.Decision,
			"release_status", refreshResult.Report.ReleaseStatus,
		)
	}

	markCompleted()
	logger.Info("ProblemValidationWorkflow completed successfully", "problem_id", problemID)

	return state, nil
}

const (
	// Legacy problems have no persisted differential manifest.  Keep their
	// fallback deliberately small: samples plus a handful of genuinely inline
	// tiny cases.  In particular, an artifact-backed case is never inferred to
	// be brute-safe from its position in the official suite.
	validationInlineBruteInputLimitV1  = 4096
	validationLegacyBruteCaseLimitV1   = 8
	validationManifestBruteCaseLimitV1 = activities.MaxReferenceDifferentialCases
)

// validationBruteCasesV1 resolves the only cases that may be sent to the
// legacy brute/reference program during a new validation run.
func validationBruteCasesV1(fetch activities.FetchProblemDataResult) ([]activities.TestCaseData, []int, error) {
	switch fetch.TestManifestSchema {
	case activities.TestManifestSchemaVersion:
		if len(fetch.BruteIndices) > validationManifestBruteCaseLimitV1 {
			return nil, nil, fmt.Errorf("manifest selects %d brute cases, maximum is %d", len(fetch.BruteIndices), validationManifestBruteCaseLimitV1)
		}
		return pickTestCasesForIndicesV1(fetch.TestCases, fetch.BruteIndices)
	case activities.TestManifestSchemaVersionV2:
		// Independent-oracle quality jobs do not use the legacy brute protocol.
		return nil, nil, nil
	case "":
		return selectLegacyValidationBruteCasesV1(fetch.TestCases)
	default:
		return nil, nil, fmt.Errorf("unsupported fetched test manifest schema %q", fetch.TestManifestSchema)
	}
}

// validationBruteCasesV2 keeps persisted manifests authoritative while using
// the semantic-safe sample-only fallback for legacy problems that have no
// manifest. A manifest is already evidence that its exact subset was executed
// during generation, so its indexes must not be silently rewritten here.
func validationBruteCasesV2(fetch activities.FetchProblemDataResult) ([]activities.TestCaseData, []int, error) {
	switch fetch.TestManifestSchema {
	case activities.TestManifestSchemaVersion, activities.TestManifestSchemaVersionV2:
		return validationBruteCasesV1(fetch)
	case "":
		return selectLegacyValidationBruteCasesV2(fetch.TestCases)
	default:
		return nil, nil, fmt.Errorf("unsupported fetched test manifest schema %q", fetch.TestManifestSchema)
	}
}

// selectLegacyValidationBruteCasesV1 is a conservative compatibility path for
// problems created before TestManifest v1.  It must never turn a missing
// manifest into an instruction to run brute over every official test.
func selectLegacyValidationBruteCasesV1(cases []activities.TestCaseData) ([]activities.TestCaseData, []int, error) {
	selected := make([]activities.TestCaseData, 0, validationLegacyBruteCaseLimitV1)
	indices := make([]int, 0, validationLegacyBruteCaseLimitV1)
	for index, testCase := range cases {
		if len(selected) >= validationLegacyBruteCaseLimitV1 {
			break
		}
		if (testCase.IsSample || (testCase.InputArtifact == nil && testCase.InputRef == "" && len(testCase.Input) <= validationInlineBruteInputLimitV1)) && referenceCaseFitsBruteBudgetV1(testCase) {
			selected = append(selected, testCase)
			indices = append(indices, index)
		}
	}
	return selected, indices, nil
}

// selectLegacyValidationBruteCasesV2 deliberately admits only public samples
// when no manifest exists. The old fallback also admitted any short inline
// string, which is unsafe for problems whose input is a compact scalar bound
// (for example, a 15-digit N that an enumerating oracle cannot process).
func selectLegacyValidationBruteCasesV2(cases []activities.TestCaseData) ([]activities.TestCaseData, []int, error) {
	selected := make([]activities.TestCaseData, 0, validationLegacyBruteCaseLimitV1)
	indices := make([]int, 0, validationLegacyBruteCaseLimitV1)
	for index, testCase := range cases {
		if len(selected) >= validationLegacyBruteCaseLimitV1 {
			break
		}
		if testCase.IsSample && referenceCaseFitsBruteBudgetV1(testCase) {
			selected = append(selected, testCase)
			indices = append(indices, index)
		}
	}
	return selected, indices, nil
}

// pickTestCasesForIndicesV1 keeps the fetched test cases in the exact order
// recorded by the manifest and rejects malformed/duplicated indexes instead
// of silently broadening the differential run.
func pickTestCasesForIndicesV1(cases []activities.TestCaseData, indices []int) ([]activities.TestCaseData, []int, error) {
	selected := make([]activities.TestCaseData, 0, len(indices))
	canonical := make([]int, 0, len(indices))
	seen := make(map[int]struct{}, len(indices))
	for position, index := range indices {
		if index < 0 || index >= len(cases) {
			return nil, nil, fmt.Errorf("manifest brute index %d at position %d is outside %d test cases", index, position, len(cases))
		}
		if _, exists := seen[index]; exists {
			return nil, nil, fmt.Errorf("manifest brute index %d is duplicated", index)
		}
		seen[index] = struct{}{}
		selected = append(selected, cases[index])
		canonical = append(canonical, index)
	}
	return selected, canonical, nil
}
