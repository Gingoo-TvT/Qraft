package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/workflow/activities"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const (
	problemGenerationReviewGateV1ChangeID   = "problem-generation-review-gate-v1"
	problemGenerationReviewGateV2ChangeID   = "problem-generation-review-gate-v2"
	problemGenerationDifferentialV1ChangeID = "problem-generation-differential-validation-v1"
	// The first differential-validation implementation treated every short
	// serialized input as brute-safe.  That is unsound for scalar bounds such
	// as N=10^15, whose input is only a few bytes.  New histories use the
	// sample/explicit-BruteCheck selection policy behind this marker; histories
	// that already recorded the old activity arguments keep the legacy branch
	// for Temporal replay compatibility.
	problemGenerationBruteSelectionV2ChangeID           = "problem-generation-brute-selection-v2"
	problemGenerationStructuredSamplesV1ChangeID        = "problem-generation-structured-samples-v1"
	problemGenerationStructuredSamplesV2ChangeID        = "problem-generation-structured-samples-v2"
	problemGenerationAdaptiveTestDataV1ChangeID         = "problem-generation-adaptive-test-data-v1"
	problemGenerationTestManifestV1ChangeID             = "problem-generation-test-manifest-v1"
	problemGenerationFinalStatementSimilarityV1ChangeID = "problem-generation-final-statement-similarity-v1"
	problemGenerationStatementQualityGateV1ChangeID     = "problem-generation-statement-quality-gate-v1"
	problemGenerationStatementMarkdownStrictV1ChangeID  = "problem-generation-statement-markdown-strict-v1"
	// Resource calibration changes the public limits after the statement has
	// already been generated.  This marker gates the deterministic statement
	// synchronization so histories that predate it replay their exact payloads.
	problemGenerationStatementResourceSyncV1ChangeID = "problem-generation-statement-resource-sync-v1"
	reviewQuarantineReason                           = "automated LLM review rejected candidate"
)

type reviewTokenMaterialV1 struct {
	Approved            bool     `json:"approved"`
	Issues              []string `json:"issues,omitempty"`
	Suggestions         []string `json:"suggestions,omitempty"`
	Confidence          float64  `json:"confidence"`
	EstimatedDifficulty int      `json:"estimated_difficulty,omitempty"`
}

func diagnoseStatementMarkdownForWorkflowV1(
	statement string,
	expectStructuredSamples bool,
	strict bool,
) []activities.StatementMarkdownDiagnosticV1 {
	if strict {
		return activities.DiagnoseGeneratedStatementMarkdownStrictV1(statement, expectStructuredSamples)
	}
	return activities.DiagnoseGeneratedStatementMarkdownV1(statement, expectStructuredSamples)
}

// ProblemGenerationStandardEvidenceWorkflowV1 gives standard-evidence jobs a
// workflow type that old workers cannot decode as the legacy workflow and
// silently downgrade to minimal evidence. Once a compatible worker handles the
// task, the underlying deterministic pipeline remains identical.
func ProblemGenerationStandardEvidenceWorkflowV1(
	ctx workflow.Context,
	params domain.ProblemGenParams,
) (*domain.WorkflowState, error) {
	if params.GenerationEvidence == nil {
		return nil, fmt.Errorf("standard evidence workflow requires generation evidence contract")
	}
	return ProblemGenerationWorkflow(ctx, params)
}

// ProblemGenerationWorkflow orchestrates the end-to-end generation of a
// competitive-programming problem. It executes a pipeline that generates,
// validates, reviews, and stores a problem along with its solutions and
// test data. Validation failures trigger an automatic retry loop: if the
// problem itself is assessed as infeasible the statement is regenerated
// (outer loop); if only the solution code is buggy the solutions are
// regenerated while reusing the same test-case inputs (inner loop).
func ProblemGenerationWorkflow(ctx workflow.Context, params domain.ProblemGenParams) (*domain.WorkflowState, error) {
	logger := workflow.GetLogger(ctx)
	payloadPatchVersion := workflow.GetVersion(
		ctx,
		"problem-generation-activity-payload-v1",
		workflow.DefaultVersion,
		activities.ActivityPayloadVersion,
	)
	statePayloadPatchVersion := workflow.GetVersion(
		ctx,
		"workflow-state-step-output-compaction-v1",
		workflow.DefaultVersion,
		1,
	)
	// Pin the v2 decision in the first workflow task. Histories that started
	// before v2 have no marker here and therefore remain on their v0/v1 path,
	// even if they had not reached the review step when the worker was upgraded.
	reviewGateV2Version := workflow.GetVersion(
		ctx,
		problemGenerationReviewGateV2ChangeID,
		workflow.DefaultVersion,
		2,
	)
	differentialValidationVersion := workflow.GetVersion(
		ctx,
		problemGenerationDifferentialV1ChangeID,
		workflow.DefaultVersion,
		1,
	)
	structuredSamplesVersion := workflow.GetVersion(
		ctx,
		problemGenerationStructuredSamplesV1ChangeID,
		workflow.DefaultVersion,
		1,
	)
	structuredSamplesV2Version := workflow.GetVersion(
		ctx,
		problemGenerationStructuredSamplesV2ChangeID,
		workflow.DefaultVersion,
		1,
	)
	adaptiveTestDataVersion := workflow.GetVersion(
		ctx,
		problemGenerationAdaptiveTestDataV1ChangeID,
		workflow.DefaultVersion,
		1,
	)
	testManifestVersion := workflow.GetVersion(
		ctx,
		problemGenerationTestManifestV1ChangeID,
		workflow.DefaultVersion,
		1,
	)
	finalStatementSimilarityVersion := workflow.GetVersion(
		ctx,
		problemGenerationFinalStatementSimilarityV1ChangeID,
		workflow.DefaultVersion,
		1,
	)
	reviewRepairVersion := workflow.GetVersion(
		ctx,
		problemGenerationReviewRepairV1ChangeID,
		workflow.DefaultVersion,
		1,
	)
	resourceCalibrationVersion := workflow.GetVersion(
		ctx,
		problemGenerationResourceCalibrationV1ChangeID,
		workflow.DefaultVersion,
		1,
	)
	statementRepairVersion := workflow.GetVersion(
		ctx,
		problemGenerationStatementRepairV1ChangeID,
		workflow.DefaultVersion,
		1,
	)
	statementQualityGateVersion := workflow.GetVersion(
		ctx,
		problemGenerationStatementQualityGateV1ChangeID,
		workflow.DefaultVersion,
		1,
	)
	// Strict Markdown checks are introduced behind their own marker. Only
	// histories already opted into the contextual statement-repair path call
	// this marker; legacy histories therefore replay the exact old command
	// sequence and validator behavior.
	statementMarkdownStrictVersion := workflow.DefaultVersion
	if statementRepairVersion >= 1 && statementQualityGateVersion >= 1 {
		statementMarkdownStrictVersion = workflow.GetVersion(
			ctx,
			problemGenerationStatementMarkdownStrictV1ChangeID,
			workflow.DefaultVersion,
			1,
		)
	}
	strictStatementQuality := statementMarkdownStrictVersion >= 1
	logger.Info("ProblemGenerationWorkflow started",
		"level", params.Level,
		"difficulty", params.Difficulty,
		"tags", params.Tags,
	)

	// Initialize workflow state.
	now := workflow.Now(ctx)
	state := &domain.WorkflowState{
		Status:    domain.WorkflowStatusRunning,
		Progress:  0,
		StartedAt: &now,
	}
	var reviewRequest *domain.ReviewRequest
	if err := workflow.SetQueryHandler(ctx, domain.WorkflowStateQueryName, func() (domain.WorkflowStateQuery, error) {
		return domain.WorkflowStateQuery{State: *state, ReviewRequest: reviewRequest}, nil
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

	// Validate input parameters.
	if err := params.Validate(); err != nil {
		markFailed(fmt.Sprintf("invalid parameters: %v", err))
		return state, fmt.Errorf("invalid parameters: %w", err)
	}

	// -----------------------------------------------------------------------
	// Activity option presets
	// -----------------------------------------------------------------------

	// Default options for LLM-based activities (generous timeouts for Opus model).
	llmActivityOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 35 * time.Minute,
		HeartbeatTimeout:    60 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    10 * time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    5 * time.Minute,
			MaximumAttempts:    3,
			NonRetryableErrorTypes: []string{
				"InvalidParameterError",
			},
		},
	}

	// Options for sandbox activities (CPU-bound).
	sandboxActivityOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 15 * time.Minute,
		HeartbeatTimeout:    30 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    2 * time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    2 * time.Minute,
			MaximumAttempts:    2,
			NonRetryableErrorTypes: []string{
				"CompilationError",
			},
		},
	}

	// Options for fast, non-LLM activities (validation, storage).
	fastActivityOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    1 * time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    30 * time.Second,
			MaximumAttempts:    5,
		},
	}

	// Options for storage activities (database + MinIO, moderate retries).
	storeActivityOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    2 * time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    1 * time.Minute,
			MaximumAttempts:    5,
		},
	}

	llmCtx := workflow.WithActivityOptions(ctx, llmActivityOpts)
	sandboxCtx := workflow.WithActivityOptions(ctx, sandboxActivityOpts)
	fastCtx := workflow.WithActivityOptions(ctx, fastActivityOpts)
	storeCtx := workflow.WithActivityOptions(ctx, storeActivityOpts)

	var err error
	var (
		paramNeighbors []activities.NeighborInfo
		stmtNeighbors  []activities.NeighborInfo
		dedupReports   []activities.DedupReport
	)

	// -----------------------------------------------------------------------
	// Step 1: Similarity Check (skipped when SimilarLimit == 0)
	// -----------------------------------------------------------------------

	state.CurrentStep = domain.StepSimilarityCheck
	stepStart := workflow.Now(ctx)

	if params.SimilarLimit <= 0 {
		paramNeighbors = nil
		state.RecordStep(completedStep(domain.StepSimilarityCheck, stepStart, workflow.Now(ctx), "skipped (disabled)"))
		logger.Info("similarity check skipped (similar_limit=0)")
	} else {
		var similarityResult activities.SimilarityCheckResult
		err = workflow.ExecuteActivity(llmCtx, "SimilarityCheckActivity", params).Get(ctx, &similarityResult)
		if err != nil {
			failedResult := activities.NewPreGenerationDedupCheckFailedResult(params, err)
			state.RecordStep(failedStepWithOutput(domain.StepSimilarityCheck, stepStart, workflow.Now(ctx), err, failedResult))
			markFailed(fmt.Sprintf("similarity check failed: %v", err))
			return state, err
		}
		if activities.DedupReportCheckFailed(similarityResult.Report) {
			checkErr := dedupCheckFailedWorkflowError("pre-generation")
			state.RecordStep(failedStepWithOutput(domain.StepSimilarityCheck, stepStart, workflow.Now(ctx), checkErr, similarityResult))
			markFailed(checkErr.Error())
			return state, checkErr
		}

		if similarityResult.TooSimilar {
			if similarityResult.Report != nil {
				dedupReports = append(dedupReports, *similarityResult.Report)
			}
			errMsg := fmt.Sprintf("problem is too similar to %d existing problems (limit: %d)",
				len(similarityResult.SimilarProblems), params.SimilarLimit)
			state.RecordStep(failedStepWithOutput(domain.StepSimilarityCheck, stepStart, workflow.Now(ctx), fmt.Errorf("%s", errMsg), similarityResult))
			markFailed(errMsg)
			return state, temporal.NewNonRetryableApplicationError(errMsg, "TooSimilarError", nil)
		}

		paramNeighbors = similarityResult.Neighbors
		if similarityResult.Report != nil {
			dedupReports = append(dedupReports, *similarityResult.Report)
		}
		state.RecordStep(completedStepVersioned(statePayloadPatchVersion, domain.StepSimilarityCheck, stepStart, workflow.Now(ctx), similarityResult))
		logger.Info("similarity check passed", "similar_count", len(similarityResult.SimilarProblems))
	}

	// -----------------------------------------------------------------------
	// Steps 2–7: Statement → Solution → Validate (with auto-retry)
	//
	// Surface/Markdown defects are repaired in place first (max 2 bounded
	// attempts). The outer loop regenerates statement + test-case inputs only
	// for downstream feasibility, similarity, or solution-pipeline failures
	// (max 3 attempts).
	// The cleaned statement is re-diagnosed at a final quality gate; test-data
	// generation is not scheduled until that gate is clean.
	// Inner loop: regenerates solutions only, reusing the same inputs (max 3).
	//
	// On each validation mismatch:
	//   • AssessProblemFeasibility is called to decide the retry strategy.
	//   • Infeasible → break inner loop; outer loop regenerates statement.
	//   • Feasible   → continue inner loop; only solutions are regenerated.
	// -----------------------------------------------------------------------

	const maxStatementAttempts = 3
	const maxSolutionAttempts = 3

	var (
		statementResult     activities.StatementResult
		solutionResult      activities.SolutionResult
		testDataResult      activities.TestDataResult
		mainSandboxResult   activities.SandboxResult
		bruteSandboxResult  activities.SandboxResult
		validationResult    activities.ValidationResult
		bruteCases          []activities.TestCaseData
		bruteIndices        []int
		sourceArtifacts     []*activities.ArtifactRef
		testManifest        *activities.TestManifestV1
		resourceCalibration *activities.ResourceCalibrationV1
	)
	// Resolved only for calibrated candidates.  Keeping the marker lazy avoids
	// adding a command to histories that never enter the resource-calibration
	// path.
	statementResourceSyncVersion := workflow.DefaultVersion

	validated := false
	var statementRetryFeedback *activities.StatementRetryFeedbackV1
	var testDataRetryFeedback *activities.StatementRetryFeedbackV1
	var lastSolutionRetryFeedback *activities.SolutionRetryFeedbackV1
	var bruteBudgetVersion workflow.Version
	bruteBudgetVersionResolved := false
	var bruteSelectionVersion workflow.Version
	bruteSelectionVersionResolved := false
	retryFeedbackVersion := workflow.DefaultVersion
	retryFeedbackVersionResolved := false
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
	resolveBruteSelectionVersion := func() workflow.Version {
		if !bruteSelectionVersionResolved {
			bruteSelectionVersion = workflow.GetVersion(
				ctx,
				problemGenerationBruteSelectionV2ChangeID,
				workflow.DefaultVersion,
				1,
			)
			bruteSelectionVersionResolved = true
		}
		return bruteSelectionVersion
	}
	resolveRetryFeedbackVersion := func() workflow.Version {
		if !retryFeedbackVersionResolved {
			retryFeedbackVersion = workflow.GetVersion(
				ctx,
				problemGenerationRetryFeedbackV1ChangeID,
				workflow.DefaultVersion,
				1,
			)
			retryFeedbackVersionResolved = true
		}
		return retryFeedbackVersion
	}

	for stmtAttempt := 0; stmtAttempt < maxStatementAttempts && !validated; stmtAttempt++ {
		if stmtAttempt > 0 {
			logger.Warn("regenerating problem statement",
				"stmt_attempt", stmtAttempt+1,
				"max", maxStatementAttempts,
			)
		}

		// -- Step 2: Generate Statement ------------------------------------

		state.CurrentStep = domain.StepGenerateStatement
		stepStart := workflow.Now(ctx)

		useStructuredSamples := structuredSamplesEnabled(
			structuredSamplesVersion,
			structuredSamplesV2Version,
			params.TestDataConfig.NumSamples,
		)
		statementInput := activities.GenerateStatementInput{
			Params:                params,
			Neighbors:             paramNeighbors,
			UseStructuredSamples:  useStructuredSamples,
			EnableStatementRepair: statementRepairVersion >= 1,
			StrictMarkdownQuality: strictStatementQuality,
		}
		if stmtAttempt > 0 && statementRetryFeedback != nil && resolveRetryFeedbackVersion() >= 1 {
			statementInput.RetryFeedback = statementRetryFeedback
		}

		if err := workflow.ExecuteActivity(llmCtx, "GenerateStatementActivity", statementInput).Get(ctx, &statementResult); err != nil {
			state.RecordStep(failedStep(domain.StepGenerateStatement, stepStart, workflow.Now(ctx), err))
			statementRetryFeedback = newStatementRetryFeedbackV1(
				stmtAttempt+1,
				statementResult.Title,
				"generate_statement",
				err.Error(),
				"",
				nil,
			)
			if stmtAttempt < maxStatementAttempts-1 {
				logger.Warn("statement generation failed, retrying", "error", err)
				continue
			}
			markFailed(fmt.Sprintf("statement generation failed after %d attempts: %v", maxStatementAttempts, err))
			return state, err
		}
		if statementRepairVersion >= 1 && len(statementResult.MarkdownDiagnostics) > 0 {
			initialDiagnosticCount := len(statementResult.MarkdownDiagnostics)
			repairedStatement, repairErr := repairGeneratedStatementWithQualityV1(
				ctx,
				llmCtx,
				statementResult,
				params,
				useStructuredSamples,
				strictStatementQuality,
			)
			if repairErr != nil {
				state.RecordStep(failedStep(domain.StepGenerateStatement, stepStart, workflow.Now(ctx), repairErr))
				statementRetryFeedback = newStatementRetryFeedbackV1(
					stmtAttempt+1,
					statementResult.Title,
					"statement_repair",
					repairErr.Error(),
					"",
					nil,
				)
				if stmtAttempt < maxStatementAttempts-1 {
					// A bounded repair failure is candidate-local. Give the outer
					// statement attempt a fresh candidate while carrying the exact
					// failure context forward, instead of terminating the whole job.
					logger.Warn("targeted statement repair failed, regenerating statement", "error", repairErr)
					continue
				}
				markFailed(fmt.Sprintf("targeted statement repair failed after %d attempts: %v", maxStatementAttempts, repairErr))
				return state, repairErr
			}
			statementResult = repairedStatement
			logger.Info(
				"statement quality defects repaired",
				"initial_diagnostics", initialDiagnosticCount,
			)
		}

		state.RecordStep(completedStepVersioned(statePayloadPatchVersion, domain.StepGenerateStatement, stepStart, workflow.Now(ctx), statementResult))
		sourceArtifacts = appendArtifactRefsUnique(sourceArtifacts, statementResult.SourceArtifacts...)
		logger.Info("statement generated", "title", statementResult.Title, "stmt_attempt", stmtAttempt+1)

		// -- Step 2.5: Clean Statement -------------------------------------

		var cleanedStatement activities.StatementResult
		if err := workflow.ExecuteActivity(llmCtx, "CleanStatementActivity", statementResult, params).Get(ctx, &cleanedStatement); err != nil {
			// The cleaner performs its own surface validation and can therefore
			// reject a malformed candidate before it gets a chance to clean it.
			// For new histories, turn that deterministic defect into the same
			// contextual repair path rather than sending the candidate straight
			// to a fresh test-data attempt (or failing without a repair).
			if statementQualityGateVersion >= 1 {
				statementResult.MarkdownDiagnostics = diagnoseStatementMarkdownForWorkflowV1(
					statementResult.Statement,
					useStructuredSamples,
					strictStatementQuality,
				)
				if len(statementResult.MarkdownDiagnostics) > 0 {
					repairedStatement, repairErr := repairGeneratedStatementWithQualityV1(
						ctx,
						llmCtx,
						statementResult,
						params,
						useStructuredSamples,
						strictStatementQuality,
					)
					if repairErr == nil {
						statementResult = repairedStatement
						cleanedStatement = repairedStatement
						sourceArtifacts = appendArtifactRefsUnique(sourceArtifacts, repairedStatement.SourceArtifacts...)
						logger.Info("statement quality repaired after cleaner rejection")
					} else {
						err = fmt.Errorf("statement cleaning and targeted repair failed: %w", repairErr)
					}
				}
			}
			if cleanedStatement.Statement == "" {
				logger.Warn("statement cleaning failed, regenerating statement", "error", err)
				statementRetryFeedback = newStatementRetryFeedbackV1(
					stmtAttempt+1,
					statementResult.Title,
					"clean_statement",
					err.Error(),
					"",
					nil,
				)
				if stmtAttempt < maxStatementAttempts-1 {
					continue
				}
				markFailed(fmt.Sprintf("statement cleaning failed: %v", err))
				return state, err
			}
		} else {
			statementResult = cleanedStatement
			sourceArtifacts = appendArtifactRefsUnique(sourceArtifacts, cleanedStatement.SourceArtifacts...)
		}
		if statementQualityGateVersion >= 1 {
			// The cleaning activity is an external boundary.  Re-check its
			// returned body and repair any defect it introduced or failed to
			// remove.  This is the final gate before GenerateTestDataActivity.
			statementResult.MarkdownDiagnostics = diagnoseStatementMarkdownForWorkflowV1(
				statementResult.Statement,
				useStructuredSamples,
				strictStatementQuality,
			)
			if len(statementResult.MarkdownDiagnostics) > 0 {
				initialDiagnosticCount := len(statementResult.MarkdownDiagnostics)
				repairedStatement, repairErr := repairGeneratedStatementWithQualityV1(
					ctx,
					llmCtx,
					statementResult,
					params,
					useStructuredSamples,
					strictStatementQuality,
				)
				if repairErr != nil {
					state.RecordStep(failedStep(domain.StepGenerateStatement, stepStart, workflow.Now(ctx), repairErr))
					statementRetryFeedback = newStatementRetryFeedbackV1(
						stmtAttempt+1,
						statementResult.Title,
						"statement_quality_gate",
						repairErr.Error(),
						"",
						nil,
					)
					if stmtAttempt < maxStatementAttempts-1 {
						logger.Warn("statement quality gate failed, regenerating statement", "error", repairErr)
						continue
					}
					markFailed(fmt.Sprintf("statement quality gate failed after repair: %v", repairErr))
					return state, repairErr
				}
				statementResult = repairedStatement
				sourceArtifacts = appendArtifactRefsUnique(sourceArtifacts, repairedStatement.SourceArtifacts...)
				logger.Info("statement quality gate repaired candidate before test data", "initial_diagnostics", initialDiagnosticCount)
			}
			if remaining := diagnoseStatementMarkdownForWorkflowV1(statementResult.Statement, useStructuredSamples, strictStatementQuality); len(remaining) > 0 {
				errMsg := fmt.Sprintf("statement quality gate left %d diagnostics before test-data generation", len(remaining))
				gateErr := temporal.NewNonRetryableApplicationError(errMsg, "QualityNotMet", nil)
				state.RecordStep(failedStep(domain.StepGenerateStatement, stepStart, workflow.Now(ctx), gateErr))
				markFailed(errMsg)
				return state, gateErr
			}
			statementResult.MarkdownDiagnostics = nil
		}
		if structuredSamplesV2Version >= 1 && strings.Count(statementResult.Statement, activities.StatementSamplesPlaceholder) != 1 {
			errMsg := "cleaned statement does not contain exactly one structured sample placeholder"
			statementRetryFeedback = newStatementRetryFeedbackV1(
				stmtAttempt+1,
				statementResult.Title,
				"structured_sample_placeholder",
				errMsg,
				"",
				nil,
			)
			if stmtAttempt < maxStatementAttempts-1 {
				logger.Warn("structured sample placeholder was not preserved, regenerating statement",
					"stmt_attempt", stmtAttempt+1,
				)
				continue
			}
			markFailed(errMsg)
			return state, temporal.NewNonRetryableApplicationError(errMsg, "QualityNotMet", nil)
		}
		// Legacy histories must keep the pre-finalization similarity command at
		// this exact location. New histories run the authoritative check after
		// validated samples have replaced the structured-sample placeholder.
		if finalStatementSimilarityVersion < 1 {
			// -- Step 2.6: Post-Statement Similarity Check ----------------------

			state.CurrentStep = domain.StepPostStatementSimilarity
			stepStart = workflow.Now(ctx)

			var postSim activities.PostStatementSimilarityResult
			psErr := workflow.ExecuteActivity(llmCtx, "PostStatementSimilarityActivity", statementResult).Get(ctx, &postSim)
			if psErr != nil {
				failedResult := activities.NewPostStatementDedupCheckFailedResult(statementResult, psErr)
				state.RecordStep(failedStepWithOutput(domain.StepPostStatementSimilarity, stepStart, workflow.Now(ctx), psErr, failedResult))
				markFailed(fmt.Sprintf("post-statement similarity check failed: %v", psErr))
				return state, psErr
			}
			if activities.DedupReportCheckFailed(postSim.Report) {
				checkErr := dedupCheckFailedWorkflowError("post-statement")
				state.RecordStep(failedStepWithOutput(domain.StepPostStatementSimilarity, stepStart, workflow.Now(ctx), checkErr, postSim))
				markFailed(checkErr.Error())
				return state, checkErr
			}
			state.RecordStep(completedStepVersioned(statePayloadPatchVersion, domain.StepPostStatementSimilarity, stepStart, workflow.Now(ctx), postSim))
			if postSim.Report != nil {
				dedupReports = append(dedupReports, *postSim.Report)
			}
			stmtNeighbors = postSim.Neighbors
			if postSim.HardReject {
				logger.Warn("statement too similar to existing problem, regenerating",
					"max_similarity", postSim.MaxSimilarity,
				)
				paramNeighbors = mergeNeighbors(paramNeighbors, postSim.Neighbors)
				statementRetryFeedback = newStatementRetryFeedbackV1(
					stmtAttempt+1,
					statementResult.Title,
					"statement_similarity",
					fmt.Sprintf("post-statement similarity %.6f triggered hard rejection", postSim.MaxSimilarity),
					"",
					nil,
				)
				if stmtAttempt < maxStatementAttempts-1 {
					continue
				}
				errMsg := "statement too similar to existing problem after max attempts"
				markFailed(errMsg)
				return state, temporal.NewNonRetryableApplicationError(
					errMsg, "StatementTooSimilar", nil,
				)
			}
		}

		// -- Step 5: Generate Test Data ------------------------------------
		// A malformed generator is a defect in the candidate artifact, not in
		// the already-valid statement.  New histories get one focused retry on
		// the same statement with the exact sandbox diagnostic; legacy histories
		// retain the original outer statement-retry command sequence.
		testDataReady := false
		for testDataAttempt := 0; !testDataReady; testDataAttempt++ {
			state.CurrentStep = domain.StepGenerateTestdata
			stepStart = workflow.Now(ctx)
			testDataParams := params
			if testDataRetryFeedback != nil && resolveRetryFeedbackVersion() >= 1 {
				testDataParams = problemGenerationTestDataParamsWithRetryFeedbackV1(testDataParams, testDataRetryFeedback)
			}

			if err := workflow.ExecuteActivity(llmCtx, "GenerateTestDataActivity",
				statementResult.Statement, testDataParams.TestDataConfig, testDataParams,
			).Get(ctx, &testDataResult); err != nil {
				state.RecordStep(failedStep(domain.StepGenerateTestdata, stepStart, workflow.Now(ctx), err))
				testDataRetryFeedback = newStatementRetryFeedbackV1(
					stmtAttempt+1,
					statementResult.Title,
					"generate_testdata",
					err.Error(),
					"",
					nil,
				)
				if testDataAttempt == 0 && resolveRetryFeedbackVersion() >= 1 && testDataFailureCanReuseStatementV1(err) {
					logger.Warn("test data candidate failed, retrying same statement",
						"error", err,
						"test_data_attempt", testDataAttempt+1,
						"max_same_statement_attempts", 2,
					)
					continue
				}
				if stmtAttempt < maxStatementAttempts-1 {
					logger.Warn("test data generation failed, retrying statement", "error", err)
					break
				}
				markFailed(fmt.Sprintf("test data generation failed: %v", err))
				return state, err
			}
			if payloadPatchVersion >= activities.ActivityPayloadVersion {
				if testDataResult.PayloadVersion != activities.ActivityPayloadVersion {
					err := fmt.Errorf("unsupported test data payload version %d", testDataResult.PayloadVersion)
					state.RecordStep(failedStep(domain.StepGenerateTestdata, stepStart, workflow.Now(ctx), err))
					markFailed(err.Error())
					return state, temporal.NewNonRetryableApplicationError(err.Error(), "UnsupportedPayloadVersion", nil)
				}
				for i, testCase := range testDataResult.TestCases {
					if testCase.InputRef != "" {
						err := fmt.Errorf("test data payload contains worker-local input ref at index %d", i)
						state.RecordStep(failedStep(domain.StepGenerateTestdata, stepStart, workflow.Now(ctx), err))
						markFailed(err.Error())
						return state, temporal.NewNonRetryableApplicationError(err.Error(), "WorkerLocalArtifactRef", nil)
					}
				}
			}
			if structuredSamplesV2Version >= 1 {
				actualSamples := countSampleTestCases(testDataResult.TestCases)
				if actualSamples != params.TestDataConfig.NumSamples {
					errMsg := fmt.Sprintf("generated %d sample tests, want exactly %d", actualSamples, params.TestDataConfig.NumSamples)
					gateErr := temporal.NewNonRetryableApplicationError(errMsg, "QualityNotMet", nil)
					state.RecordStep(failedStep(domain.StepGenerateTestdata, stepStart, workflow.Now(ctx), gateErr))
					testDataRetryFeedback = newStatementRetryFeedbackV1(
						stmtAttempt+1,
						statementResult.Title,
						"structured_sample_gate",
						errMsg,
						"",
						nil,
					)
					if testDataAttempt == 0 && resolveRetryFeedbackVersion() >= 1 {
						logger.Warn("sample count quality gate failed, retrying same statement",
							"error", errMsg,
							"test_data_attempt", testDataAttempt+1,
							"max_same_statement_attempts", 2,
						)
						continue
					}
					if stmtAttempt < maxStatementAttempts-1 {
						logger.Warn("sample count quality gate failed, regenerating statement", "error", errMsg)
						break
					}
					markFailed(errMsg)
					return state, gateErr
				}
			}
			// A successful generator result clears only generator-specific feedback;
			// solution/statement retry context remains independently scoped.
			testDataRetryFeedback = nil
			sourceArtifacts = appendArtifactRefsUnique(sourceArtifacts, testDataResult.SourceArtifacts...)

			state.RecordStep(completedStep(domain.StepGenerateTestdata, stepStart, workflow.Now(ctx),
				fmt.Sprintf("generated %d test cases", len(testDataResult.TestCases))))
			logger.Info("test data generated", "count", len(testDataResult.TestCases), "attempt", testDataAttempt+1)
			testDataReady = true
		}
		if !testDataReady {
			continue
		}
		if adaptiveTestDataVersion >= 1 {
			if err := params.TestDataConfig.ValidateGeneratedTestCaseCount(len(testDataResult.TestCases)); err != nil {
				errMsg := fmt.Sprintf("generated test-data count quality gate failed: %v", err)
				gateErr := temporal.NewNonRetryableApplicationError(errMsg, "QualityNotMet", nil)
				state.RecordStep(failedStep(domain.StepGenerateTestdata, stepStart, workflow.Now(ctx), gateErr))
				if stmtAttempt < maxStatementAttempts-1 {
					logger.Warn("test-data count quality gate failed, regenerating statement", "error", errMsg)
					continue
				}
				markFailed(errMsg)
				return state, gateErr
			}
		}

		if differentialValidationVersion >= 1 {
			if resolveBruteSelectionVersion() >= 1 {
				bruteCases, bruteIndices = selectBruteCheckCasesV2(
					testDataResult.TestCases,
					params.TestDataConfig,
				)
			} else {
				bruteCases, bruteIndices = selectBruteCheckCasesV1(
					testDataResult.TestCases,
					params.TestDataConfig,
				)
			}
			if len(bruteCases) == 0 {
				errMsg := fmt.Sprintf("quality gate selected no test cases for main/brute differential validation (provide a small sample or BruteCheck case no larger than %d bytes)", activities.MaxReferenceDifferentialInputBytes)
				gateErr := temporal.NewNonRetryableApplicationError(errMsg, "QualityNotMet", nil)
				state.CurrentStep = domain.StepValidate
				gateStart := workflow.Now(ctx)
				state.RecordStep(failedStep(domain.StepValidate, gateStart, workflow.Now(ctx), gateErr))
				markFailed(errMsg)
				return state, gateErr
			}
		}

		// -- Inner loop: regenerate solutions ------------------------------

		// Failures from a previous statement attempt must not be allowed to
		// determine the terminal error type for this attempt.  In particular, a
		// brute runtime failure belongs to the reference program that produced it
		// and is only terminally classified as such when this statement's bounded
		// solution loop ends on the same failure stage.
		lastSolutionRetryFeedback = nil
		var solutionRetryFeedback *activities.SolutionRetryFeedbackV1
		lastFailureFingerprint := ""
		consecutiveSameFailure := 0
		recordSolutionFailure := func(feedback *activities.SolutionRetryFeedbackV1) bool {
			solutionRetryFeedback = feedback
			lastSolutionRetryFeedback = feedback
			fingerprint := solutionRetryFingerprintV1(feedback)
			if fingerprint != "" && fingerprint == lastFailureFingerprint {
				consecutiveSameFailure++
			} else {
				lastFailureFingerprint = fingerprint
				consecutiveSameFailure = 1
			}
			if consecutiveSameFailure >= 2 {
				logger.Warn("same solution failure repeated; abandoning statement early",
					"stage", feedback.FailureStage,
					"consecutive", consecutiveSameFailure,
					"stmt_attempt", stmtAttempt+1,
				)
				return true
			}
			return false
		}

		for solAttempt := 0; solAttempt < maxSolutionAttempts && !validated; solAttempt++ {
			resourceCalibration = nil
			if solAttempt > 0 {
				logger.Warn("regenerating solutions only (test inputs reused)",
					"sol_attempt", solAttempt+1,
					"max", maxSolutionAttempts,
				)
			}

			// Step 3: Generate Solutions

			state.CurrentStep = domain.StepGenerateSolution
			stepStart = workflow.Now(ctx)

			var solutionErr error
			if solAttempt > 0 && solutionRetryFeedback != nil && resolveRetryFeedbackVersion() >= 1 {
				solutionErr = workflow.ExecuteActivity(llmCtx, "RepairSolutionActivity",
					activities.GenerateSolutionRepairInput{
						PayloadVersion: activities.ActivityPayloadVersion,
						Statement:      statementResult.Statement,
						Params:         params,
						Feedback:       *solutionRetryFeedback,
					},
				).Get(ctx, &solutionResult)
			} else {
				solutionErr = workflow.ExecuteActivity(llmCtx, "GenerateSolutionActivity",
					statementResult.Statement, params,
				).Get(ctx, &solutionResult)
			}
			if solutionErr != nil {
				err := solutionErr
				state.RecordStep(failedStep(domain.StepGenerateSolution, stepStart, workflow.Now(ctx), err))
				logger.Warn("solution generation failed", "sol_attempt", solAttempt+1, "error", err)
				abandonStatement := recordSolutionFailure(newSolutionRetryFeedbackV1(
					stmtAttempt+1,
					solAttempt+1,
					"generate_solution",
					err.Error(),
					solutionResult,
					nil,
					nil,
					"",
				))
				if solAttempt < maxSolutionAttempts-1 && !abandonStatement {
					continue
				}
				logger.Warn("solution generation failed after all sol attempts, regenerating statement")
				break
			}

			state.RecordStep(completedStep(domain.StepGenerateSolution, stepStart, workflow.Now(ctx), "solutions generated"))
			sourceArtifacts = appendArtifactRefsUnique(sourceArtifacts, solutionResult.SourceArtifacts...)
			logger.Info("solutions generated",
				"main_lang", solutionResult.MainSolution.Language,
				"brute_lang", solutionResult.BruteSolution.Language,
			)

			// Step 4: Compile Check

			state.CurrentStep = domain.StepCompileCheck
			stepStart = workflow.Now(ctx)

			var compileResult activities.CompileCheckResult
			if err := workflow.ExecuteActivity(sandboxCtx, "CompileCheckActivity",
				[]domain.Solution{solutionResult.MainSolution, solutionResult.BruteSolution},
			).Get(ctx, &compileResult); err != nil {
				state.RecordStep(failedStep(domain.StepCompileCheck, stepStart, workflow.Now(ctx), err))
				logger.Warn("compile check activity error, retrying solutions", "error", err, "sol_attempt", solAttempt+1)
				abandonStatement := recordSolutionFailure(newSolutionRetryFeedbackV1(
					stmtAttempt+1, solAttempt+1, "compile_activity", err.Error(),
					solutionResult, nil, nil, "",
				))
				if solAttempt < maxSolutionAttempts-1 && !abandonStatement {
					continue
				}
				break
			}

			if !compileResult.AllCompiled {
				state.RecordStep(failedStep(domain.StepCompileCheck, stepStart, workflow.Now(ctx),
					fmt.Errorf("%s", compileResult.ErrorDetail)))
				failureStage := "compile"
				diagnostic := compileResult.ErrorDetail
				if activities.IsReferenceSolutionFailureText(diagnostic) {
					failureStage = "brute_program_compile"
					diagnostic = "reference/brute program failed to compile; regenerate the brute implementation (this is not a main-solution verdict): " + diagnostic
				}
				logger.Warn("compilation failed, regenerating solutions",
					"detail", compileResult.ErrorDetail,
					"sol_attempt", solAttempt+1,
				)
				abandonStatement := recordSolutionFailure(newSolutionRetryFeedbackV1(
					stmtAttempt+1, solAttempt+1, failureStage, diagnostic,
					solutionResult, nil, nil, "",
				))
				if solAttempt < maxSolutionAttempts-1 && !abandonStatement {
					continue
				}
				break
			}

			state.RecordStep(completedStep(domain.StepCompileCheck, stepStart, workflow.Now(ctx), compileResult))
			logger.Info("compilation check passed")

			// Step 6: Run Sandbox

			state.CurrentStep = domain.StepRunSandbox
			stepStart = workflow.Now(ctx)

			sandboxLimits := activities.ExecutionLimits{
				TimeLimitMs:   params.TimeLimit,
				MemoryLimitMB: params.MemoryLimit,
			}
			if resourceCalibrationVersion >= 1 {
				benchmarkLimits := problemResourceBenchmarkLimitsV1()
				var benchmarkResult activities.SandboxResult
				if err := workflow.ExecuteActivity(sandboxCtx, "RunSandboxActivity",
					solutionResult.MainSolution, testDataResult.TestCases, benchmarkLimits,
				).Get(ctx, &benchmarkResult); err != nil {
					state.RecordStep(failedStep(domain.StepRunSandbox, stepStart, workflow.Now(ctx), err))
					logger.Warn("resource benchmark failed for main solution", "sol_attempt", solAttempt+1, "error", err)
					abandonStatement := recordSolutionFailure(newSolutionRetryFeedbackV1(
						stmtAttempt+1, solAttempt+1, "resource_benchmark", err.Error(),
						solutionResult, nil, nil, "",
					))
					if solAttempt < maxSolutionAttempts-1 && !abandonStatement {
						continue
					}
					break
				}

				calibration, calibrationErr := deriveProblemResourceCalibrationV1(benchmarkResult, len(testDataResult.TestCases))
				if calibrationErr != nil {
					state.RecordStep(failedStep(domain.StepRunSandbox, stepStart, workflow.Now(ctx), calibrationErr))
					logger.Warn("resource calibration rejected model solution", "sol_attempt", solAttempt+1, "error", calibrationErr)
					abandonStatement := recordSolutionFailure(newSolutionRetryFeedbackV1(
						stmtAttempt+1, solAttempt+1, "resource_calibration", calibrationErr.Error(),
						solutionResult, nil, nil, "",
					))
					if solAttempt < maxSolutionAttempts-1 && !abandonStatement {
						continue
					}
					break
				}

				sandboxLimits = calibration.FinalLimits
				if err := workflow.ExecuteActivity(sandboxCtx, "RunSandboxActivity",
					solutionResult.MainSolution, testDataResult.TestCases, sandboxLimits,
				).Get(ctx, &mainSandboxResult); err != nil {
					state.RecordStep(failedStep(domain.StepRunSandbox, stepStart, workflow.Now(ctx), err))
					logger.Warn("main solution failed under derived final limits", "sol_attempt", solAttempt+1, "error", err)
					abandonStatement := recordSolutionFailure(newSolutionRetryFeedbackV1(
						stmtAttempt+1, solAttempt+1, "main_sandbox", err.Error(),
						solutionResult, nil, nil, "",
					))
					if solAttempt < maxSolutionAttempts-1 && !abandonStatement {
						continue
					}
					break
				}

				calibration, calibrationErr = finalizeProblemResourceCalibrationV1(calibration, mainSandboxResult)
				if calibrationErr != nil {
					state.RecordStep(failedStep(domain.StepRunSandbox, stepStart, workflow.Now(ctx), calibrationErr))
					logger.Warn("final resource calibration receipt is invalid", "sol_attempt", solAttempt+1, "error", calibrationErr)
					abandonStatement := recordSolutionFailure(newSolutionRetryFeedbackV1(
						stmtAttempt+1, solAttempt+1, "resource_calibration_receipt", calibrationErr.Error(),
						solutionResult, nil, nil, "",
					))
					if solAttempt < maxSolutionAttempts-1 && !abandonStatement {
						continue
					}
					break
				}
				resourceCalibration = &calibration
			} else if err := workflow.ExecuteActivity(sandboxCtx, "RunSandboxActivity",
				solutionResult.MainSolution, testDataResult.TestCases, sandboxLimits,
			).Get(ctx, &mainSandboxResult); err != nil {
				state.RecordStep(failedStep(domain.StepRunSandbox, stepStart, workflow.Now(ctx), err))
				logger.Warn("sandbox failed for main solution", "sol_attempt", solAttempt+1, "error", err)
				abandonStatement := recordSolutionFailure(newSolutionRetryFeedbackV1(
					stmtAttempt+1, solAttempt+1, "main_sandbox", err.Error(),
					solutionResult, nil, nil, "",
				))
				if solAttempt < maxSolutionAttempts-1 && !abandonStatement {
					continue
				}
				break
			}

			bruteLimits := activities.ExecutionLimits{
				TimeLimitMs:   params.TimeLimit * 3,
				MemoryLimitMB: params.MemoryLimit * 2,
			}
			if resourceCalibrationVersion >= 1 {
				if resolveBruteBudgetVersion() >= 1 {
					bruteLimits = problemResourceBruteLimitsV1(sandboxLimits)
				} else {
					bruteLimits = problemResourceBruteLimitsLegacyV0(sandboxLimits)
				}
			} else if differentialValidationVersion >= 1 && resolveBruteBudgetVersion() >= 1 {
				// New differential histories without resource calibration still
				// use the same correctness-only oracle contract.
				bruteLimits = problemResourceBruteLimitsV1(sandboxLimits)
			}

			// New histories pin their bounded differential subset immediately
			// after test-data generation. Legacy histories select here to keep
			// their pre-v1 command order and selection behavior replay-safe.
			if differentialValidationVersion < 1 {
				bruteCases, bruteIndices = selectBruteCheckCases(
					testDataResult.TestCases,
					params.TestDataConfig,
				)
			}

			if err := workflow.ExecuteActivity(sandboxCtx, "RunSandboxActivity",
				solutionResult.BruteSolution, bruteCases, bruteLimits,
			).Get(ctx, &bruteSandboxResult); err != nil {
				state.RecordStep(failedStep(domain.StepRunSandbox, stepStart, workflow.Now(ctx), err))
				failureStage := "brute_sandbox"
				diagnostic := err.Error()
				if activities.IsReferenceSolutionFailure(err) {
					failureStage = "brute_program_runtime"
					diagnostic = "reference/brute program failed; regenerate the brute implementation (this is not a main-solution verdict): " + diagnostic
				}
				logger.Warn("sandbox failed for brute solution", "sol_attempt", solAttempt+1, "failure_stage", failureStage, "error", diagnostic)
				abandonStatement := recordSolutionFailure(newSolutionRetryFeedbackV1(
					stmtAttempt+1, solAttempt+1, failureStage, diagnostic,
					solutionResult, nil, nil, "",
				))
				if solAttempt < maxSolutionAttempts-1 && !abandonStatement {
					continue
				}
				break
			}

			runSandboxOutput := interface{}("sandbox runs completed")
			if resourceCalibration != nil {
				runSandboxOutput = *resourceCalibration
			}
			state.RecordStep(completedStep(domain.StepRunSandbox, stepStart, workflow.Now(ctx), runSandboxOutput))
			logger.Info("sandbox execution completed",
				"main_outputs", len(mainSandboxResult.Outputs),
				"brute_outputs", len(bruteSandboxResult.Outputs),
				"brute_cases", len(bruteCases),
				"total_cases", len(testDataResult.TestCases),
			)

			// Step 7: Validate (only on brute-check subset)

			state.CurrentStep = domain.StepValidate
			stepStart = workflow.Now(ctx)

			mainSubsetResult := pickMainOutputsForBruteSubset(mainSandboxResult, bruteIndices)

			// ValidationResult.Mismatches is omitempty on the wire. Decode into a
			// fresh value on every attempt so a successful response that omits the
			// empty field cannot inherit mismatches from the preceding attempt.
			var currentValidationResult activities.ValidationResult
			if err := workflow.ExecuteActivity(fastCtx, "ValidateActivity",
				mainSubsetResult, bruteSandboxResult,
			).Get(ctx, &currentValidationResult); err != nil {
				state.RecordStep(failedStep(domain.StepValidate, stepStart, workflow.Now(ctx), err))
				logger.Warn("validate activity error", "sol_attempt", solAttempt+1, "error", err)
				abandonStatement := recordSolutionFailure(newSolutionRetryFeedbackV1(
					stmtAttempt+1, solAttempt+1, "validate_activity", err.Error(),
					solutionResult, nil, nil, "",
				))
				if solAttempt < maxSolutionAttempts-1 && !abandonStatement {
					continue
				}
				break
			}
			validationResult = currentValidationResult

			if validationResult.AllPassed {
				state.RecordStep(completedStepVersioned(statePayloadPatchVersion, domain.StepValidate, stepStart, workflow.Now(ctx), validationResult))
				logger.Info("validation passed, all outputs match")
				validated = true
				break
			}

			// Mismatch found — record failure then assess whether the problem
			// or the solution code is at fault.
			state.RecordStep(failedStep(domain.StepValidate, stepStart, workflow.Now(ctx),
				fmt.Errorf("validation failed: %d mismatches found", len(validationResult.Mismatches))))
			logger.Warn("validation mismatch detected, assessing problem feasibility",
				"mismatches", len(validationResult.Mismatches),
				"stmt_attempt", stmtAttempt+1,
				"sol_attempt", solAttempt+1,
			)

			state.CurrentStep = domain.StepAssessFeasibility
			stepStart = workflow.Now(ctx)

			var feasibility activities.FeasibilityResult
			if err := workflow.ExecuteActivity(llmCtx, "AssessProblemFeasibilityActivity",
				statementResult, validationResult.Mismatches, params,
			).Get(ctx, &feasibility); err != nil {
				logger.Warn("feasibility assessment failed, treating problem as infeasible", "error", err)
				feasibility.Feasible = false
				feasibility.Reason = fmt.Sprintf("assessment error: %v", err)
			}

			state.RecordStep(completedStepVersioned(statePayloadPatchVersion, domain.StepAssessFeasibility, stepStart, workflow.Now(ctx), feasibility))
			sourceArtifacts = appendArtifactRefsUnique(sourceArtifacts, feasibility.SourceArtifacts...)

			failingCases := failingCasesForMismatchesV1(validationResult.Mismatches, bruteCases)
			abandonStatement := recordSolutionFailure(newSolutionRetryFeedbackV1(
				stmtAttempt+1,
				solAttempt+1,
				"differential_validation",
				fmt.Sprintf("main and brute outputs differed on %d compared cases", len(validationResult.Mismatches)),
				solutionResult,
				validationResult.Mismatches,
				failingCases,
				feasibility.Reason,
			))

			if !feasibility.Feasible {
				logger.Warn("problem assessed as infeasible, regenerating statement",
					"reason", feasibility.Reason,
					"stmt_attempt", stmtAttempt+1,
				)
				break // exit inner loop → outer loop regenerates statement
			}

			logger.Warn("problem is feasible, solution code has bugs, regenerating solutions",
				"reason", feasibility.Reason,
				"sol_attempt", solAttempt+1,
			)
			if abandonStatement {
				break
			}
			// continue inner loop: regenerate solutions only
		}

		if !validated {
			statementRetryFeedback = statementRetryFeedbackFromSolutionV1(
				stmtAttempt+1,
				statementResult.Title,
				solutionRetryFeedback,
			)
		}

		if validated && finalStatementSimilarityVersion >= 1 {
			if structuredSamplesEnabled(structuredSamplesVersion, structuredSamplesV2Version, params.TestDataConfig.NumSamples) {
				var finalizedStatement activities.StatementResult
				finalizeInput := activities.FinalizeStatementSamplesInput{
					PayloadVersion:        activities.ActivityPayloadVersion,
					Statement:             statementResult,
					TestCases:             testDataResult.TestCases,
					SandboxOutput:         mainSandboxResult,
					Locale:                params.Locale,
					StrictMarkdownQuality: strictStatementQuality,
				}
				if structuredSamplesV2Version >= 1 {
					finalizeInput.ExpectedSampleCount = params.TestDataConfig.NumSamples
					finalizeInput.EnforceSampleCount = true
				}
				err = workflow.ExecuteActivity(fastCtx, "FinalizeStatementSamplesActivity",
					finalizeInput,
				).Get(ctx, &finalizedStatement)
				if err != nil {
					markFailed(fmt.Sprintf("finalizing validated statement samples: %v", err))
					return state, err
				}
				statementResult = finalizedStatement
				logger.Info("validated statement samples finalized",
					"sample_count", countSampleTestCases(testDataResult.TestCases),
				)
			}

			state.CurrentStep = domain.StepPostStatementSimilarity
			stepStart = workflow.Now(ctx)

			var postSim activities.PostStatementSimilarityResult
			psErr := workflow.ExecuteActivity(llmCtx, "PostStatementSimilarityActivity", statementResult).Get(ctx, &postSim)
			if psErr != nil {
				failedResult := activities.NewPostStatementDedupCheckFailedResult(statementResult, psErr)
				state.RecordStep(failedStepWithOutput(domain.StepPostStatementSimilarity, stepStart, workflow.Now(ctx), psErr, failedResult))
				markFailed(fmt.Sprintf("post-statement similarity check failed: %v", psErr))
				return state, psErr
			}
			if activities.DedupReportCheckFailed(postSim.Report) {
				checkErr := dedupCheckFailedWorkflowError("post-statement")
				state.RecordStep(failedStepWithOutput(domain.StepPostStatementSimilarity, stepStart, workflow.Now(ctx), checkErr, postSim))
				markFailed(checkErr.Error())
				return state, checkErr
			}
			state.RecordStep(completedStepVersioned(statePayloadPatchVersion, domain.StepPostStatementSimilarity, stepStart, workflow.Now(ctx), postSim))
			if postSim.Report != nil {
				dedupReports = append(dedupReports, *postSim.Report)
			}
			stmtNeighbors = postSim.Neighbors
			if postSim.HardReject {
				logger.Warn("final statement too similar to existing problem, regenerating",
					"max_similarity", postSim.MaxSimilarity,
				)
				paramNeighbors = mergeNeighbors(paramNeighbors, postSim.Neighbors)
				validated = false
				statementRetryFeedback = newStatementRetryFeedbackV1(
					stmtAttempt+1,
					statementResult.Title,
					"final_statement_similarity",
					fmt.Sprintf("final statement similarity %.6f triggered hard rejection", postSim.MaxSimilarity),
					"",
					nil,
				)
				if stmtAttempt < maxStatementAttempts-1 {
					continue
				}
				errMsg := "final statement too similar to existing problem after max attempts"
				markFailed(errMsg)
				return state, temporal.NewNonRetryableApplicationError(
					errMsg, "StatementTooSimilar", nil,
				)
			}
		}
	}

	if !validated {
		errMsg := fmt.Sprintf("failed to produce valid solutions after up to %d statement attempts × %d solution attempts",
			maxStatementAttempts, maxSolutionAttempts)
		if lastSolutionRetryFeedback != nil {
			errMsg += fmt.Sprintf("; last failure at %s: %s",
				lastSolutionRetryFeedback.FailureStage,
				boundedGenerationRetryTextV1(lastSolutionRetryFeedback.Diagnostic, 500),
			)
			if isReferenceSolutionFailureStageV1(lastSolutionRetryFeedback.FailureStage) {
				refErrMsg := "reference/brute solution failed during its bounded correctness check; the main solution was not rejected: " +
					boundedGenerationRetryTextV1(lastSolutionRetryFeedback.Diagnostic, 1000)
				markFailed(refErrMsg)
				return state, temporal.NewNonRetryableApplicationError(refErrMsg, "ReferenceSolutionFailure", nil)
			}
		} else if statementRetryFeedback != nil {
			errMsg += fmt.Sprintf("; last failure at %s: %s",
				statementRetryFeedback.FailureStage,
				boundedGenerationRetryTextV1(statementRetryFeedback.Diagnostic, 500),
			)
		}
		markFailed(errMsg)
		return state, temporal.NewNonRetryableApplicationError(errMsg, "MaxRetriesExceeded", nil)
	}
	if resourceCalibration != nil {
		params = applyProblemResourceCalibrationV1(params, *resourceCalibration)
		statementResourceSyncVersion = workflow.GetVersion(
			ctx,
			problemGenerationStatementResourceSyncV1ChangeID,
			workflow.DefaultVersion,
			1,
		)
	}

	if testManifestVersion >= 1 {
		var manifest activities.TestManifestV1
		err = workflow.ExecuteActivity(fastCtx, "BuildTestManifestActivity",
			activities.BuildTestManifestInput{
				PayloadVersion:   activities.ActivityPayloadVersion,
				TestCases:        testDataResult.TestCases,
				MainOutput:       mainSandboxResult,
				BruteOutput:      bruteSandboxResult,
				BruteIndices:     bruteIndices,
				MainSolution:     solutionResult.MainSolution,
				BruteSolution:    solutionResult.BruteSolution,
				GeneratorSHA256:  testDataResult.GeneratorSHA256,
				GeneratorBatches: testDataResult.GeneratorBatches,
			},
		).Get(ctx, &manifest)
		if err != nil {
			markFailed(fmt.Sprintf("building test manifest: %v", err))
			return state, err
		}
		testManifest = &manifest
		logger.Info("test manifest built",
			"test_count", manifest.TestCount,
			"differential_checked_count", manifest.DifferentialCheckedCount,
		)
	}

	if finalStatementSimilarityVersion < 1 &&
		structuredSamplesEnabled(structuredSamplesVersion, structuredSamplesV2Version, params.TestDataConfig.NumSamples) {
		var finalizedStatement activities.StatementResult
		finalizeInput := activities.FinalizeStatementSamplesInput{
			PayloadVersion:        activities.ActivityPayloadVersion,
			Statement:             statementResult,
			TestCases:             testDataResult.TestCases,
			SandboxOutput:         mainSandboxResult,
			Locale:                params.Locale,
			StrictMarkdownQuality: strictStatementQuality,
		}
		if structuredSamplesV2Version >= 1 {
			finalizeInput.ExpectedSampleCount = params.TestDataConfig.NumSamples
			finalizeInput.EnforceSampleCount = true
		}
		err = workflow.ExecuteActivity(fastCtx, "FinalizeStatementSamplesActivity",
			finalizeInput,
		).Get(ctx, &finalizedStatement)
		if err != nil {
			markFailed(fmt.Sprintf("finalizing validated statement samples: %v", err))
			return state, err
		}
		statementResult = finalizedStatement
		logger.Info("validated statement samples finalized",
			"sample_count", countSampleTestCases(testDataResult.TestCases),
		)
	}

	// The standard-solution benchmark is authoritative for the final public
	// limits.  Synchronize the already validated statement before review,
	// editorial generation, and storage so no artifact can advertise a stale
	// provisional limit (for example, 2000 ms while the enforced limit is
	// 1000 ms).  The edit is limited to explicit resource-limit declarations and
	// is deterministic; the version marker above preserves replay of older
	// histories.
	if resourceCalibration != nil && statementResourceSyncVersion >= 1 {
		before := statementResult.Statement
		statementResult.Statement = activities.SynchronizeStatementResourceLimitsV1(
			statementResult.Statement,
			params.TimeLimit,
			params.MemoryLimit,
		)
		if before != statementResult.Statement {
			logger.Info("synchronized statement resource limits with calibrated sandbox",
				"time_limit_ms", params.TimeLimit,
				"memory_limit_mb", params.MemoryLimit,
			)
		}
	}

	// -----------------------------------------------------------------------
	// Step 8: LLM Review
	// -----------------------------------------------------------------------

	state.CurrentStep = domain.StepLLMReview
	stepStart = workflow.Now(ctx)

	var reviewResult activities.ReviewResult
	err = workflow.ExecuteActivity(llmCtx, "LLMReviewActivity",
		activities.LLMReviewInput{
			Statement:           statementResult,
			Solutions:           solutionResult,
			TestCases:           testDataResult.TestCases,
			Params:              params,
			Neighbors:           stmtNeighbors,
			ResourceCalibration: resourceCalibration,
		},
	).Get(ctx, &reviewResult)
	if err != nil {
		state.RecordStep(failedStep(domain.StepLLMReview, stepStart, workflow.Now(ctx), err))
		markFailed(fmt.Sprintf("llm review failed: %v", err))
		return state, err
	}

	if reviewRepairVersion < 1 && reviewResult.IsDuplicate {
		errMsg := fmt.Sprintf("LLM reviewer judged as duplicate of %q: %s",
			reviewResult.DuplicateOf, reviewResult.DuplicateReason)
		markFailed(errMsg)
		return state, temporal.NewNonRetryableApplicationError(errMsg, "DuplicateProblem", nil)
	}
	if reviewRepairVersion >= 1 && reviewResult.IsDuplicate {
		reviewResult.Approved = false
	}
	reviewGateVersion := problemGenerationReviewGateVersion(ctx, reviewGateV2Version)

	if !reviewResult.Approved {
		if reviewGateVersion >= 2 && !params.RequireReview {
			logger.Warn("llm review did not approve the problem; candidate will be quarantined",
				"issues", reviewResult.Issues,
				"confidence", reviewResult.Confidence,
			)
		} else if reviewGateVersion >= 1 {
			logger.Warn("llm review did not approve the problem; human review required",
				"issues", reviewResult.Issues,
				"confidence", reviewResult.Confidence,
			)
		} else {
			logger.Warn("llm review did not approve the problem, continuing anyway",
				"issues", reviewResult.Issues,
				"confidence", reviewResult.Confidence,
			)
		}
	}

	// Difficulty deviation check: warn if estimated difficulty is far from target.
	if reviewResult.EstimatedDifficulty > 0 {
		deviation := reviewResult.EstimatedDifficulty - params.Difficulty
		if deviation < 0 {
			deviation = -deviation
		}
		if deviation > 200 {
			diffWarning := fmt.Sprintf(
				"difficulty mismatch: target=%d, estimated=%d (deviation=%d)",
				params.Difficulty, reviewResult.EstimatedDifficulty, deviation,
			)
			logger.Warn("difficulty calibration mismatch detected",
				"target", params.Difficulty,
				"estimated", reviewResult.EstimatedDifficulty,
				"deviation", deviation,
			)
			reviewResult.Issues = append(reviewResult.Issues, diffWarning)
			if reviewRepairVersion >= 1 {
				reviewResult.Approved = false
			}
		}
	}

	state.RecordStep(completedStepVersioned(statePayloadPatchVersion, domain.StepLLMReview, stepStart, workflow.Now(ctx), reviewResult))
	sourceArtifacts = appendArtifactRefsUnique(sourceArtifacts, reviewResult.SourceArtifacts...)
	if reviewResult.Approved {
		logger.Info("llm review passed",
			"confidence", reviewResult.Confidence,
			"estimated_difficulty", reviewResult.EstimatedDifficulty,
		)
	} else if reviewGateVersion >= 2 && !params.RequireReview {
		logger.Info("llm review recorded for automated quarantine",
			"confidence", reviewResult.Confidence,
			"estimated_difficulty", reviewResult.EstimatedDifficulty,
		)
	} else if reviewGateVersion >= 1 {
		logger.Info("llm review recorded for pending human decision",
			"confidence", reviewResult.Confidence,
			"estimated_difficulty", reviewResult.EstimatedDifficulty,
		)
	}

	if reviewRepairVersion >= 1 && !reviewResult.Approved {
		nextParams, decision, repairErr := prepareProblemGenerationReviewRepairV1(params, reviewResult)
		if repairErr != nil {
			errMsg := fmt.Sprintf("preparing review-driven repair: %v", repairErr)
			markFailed(errMsg)
			return state, temporal.NewNonRetryableApplicationError(errMsg, "ReviewRepairContract", repairErr)
		}
		params = nextParams
		if decision.Continue {
			logger.Info("automated review repair scheduled",
				"repair_round", decision.RepairRound,
				"max_repair_rounds", problemGenerationReviewRepairMaxRoundsV1,
				"feedback_sha256", decision.FeedbackSHA256,
			)
			return nil, workflow.NewContinueAsNewError(ctx, ProblemGenerationWorkflow, nextParams)
		}
		logger.Warn("automated review repair stopped",
			"repair_rounds", decision.RepairRound,
			"stop_reason", decision.StopReason,
			"feedback_sha256", decision.FeedbackSHA256,
		)
	}

	if reviewResult.IsDuplicate {
		errMsg := fmt.Sprintf("LLM reviewer judged as duplicate of %q after bounded repair: %s",
			reviewResult.DuplicateOf, reviewResult.DuplicateReason)
		markFailed(errMsg)
		return state, temporal.NewNonRetryableApplicationError(errMsg, "DuplicateProblem", nil)
	}

	// -----------------------------------------------------------------------
	// Step 9: Human Review (conditional)
	// -----------------------------------------------------------------------

	state.CurrentStep = domain.StepHumanReview
	stepStart = workflow.Now(ctx)

	shouldWaitForReview := params.RequireReview
	if reviewGateVersion == 1 {
		shouldWaitForReview = params.RequireReview || !reviewResult.Approved
	}
	shouldQuarantineReview := reviewGateVersion >= 2 && !reviewResult.Approved && !shouldWaitForReview

	if shouldWaitForReview {
		reviewAttempt := 0
		if reviewGateVersion >= 1 {
			reviewAttempt = 1
		}
		var err error
		reviewRequest, err = buildReviewRequest(ctx, reviewAttempt, reviewGateVersion >= 1, reviewResult)
		if err != nil {
			state.RecordStep(failedStep(domain.StepHumanReview, stepStart, workflow.Now(ctx), err))
			markFailed(fmt.Sprintf("creating human review request: %v", err))
			return state, err
		}
		state.Status = domain.WorkflowStatusWaitingReview
		logger.Info("waiting for human review signal", "review_attempt", reviewAttempt)

		var decision *domain.ReviewDecision
		if reviewGateVersion >= 1 {
			decision, err = waitForReviewSignal(ctx, 72*time.Hour, reviewRequest.Token)
		} else {
			decision, err = waitForLegacyReviewSignal(ctx, 72*time.Hour)
		}
		if err != nil {
			state.RecordStep(failedStep(domain.StepHumanReview, stepStart, workflow.Now(ctx), err))
			markFailed(fmt.Sprintf("human review failed: %v", err))
			return state, err
		}

		state.Status = domain.WorkflowStatusRunning
		reviewRequest.Decision = decision

		if !decision.Approved {
			errMsg := fmt.Sprintf("human reviewer rejected: %s", decision.Feedback)
			state.RecordStep(failedStep(domain.StepHumanReview, stepStart, workflow.Now(ctx), fmt.Errorf("%s", errMsg)))
			markFailed(errMsg)
			return state, temporal.NewNonRetryableApplicationError(errMsg, "HumanReviewRejection", nil)
		}

		state.RecordStep(completedStep(domain.StepHumanReview, stepStart, workflow.Now(ctx), decision))
		logger.Info("human review approved", "feedback", decision.Feedback)
	} else {
		stepOutput := "skipped"
		if shouldQuarantineReview {
			stepOutput = "automated rejection routed to quarantine"
		}
		state.RecordStep(completedStep(domain.StepHumanReview, stepStart, workflow.Now(ctx), stepOutput))
		logger.Info("human review skipped", "automated_quarantine", shouldQuarantineReview)
	}

	// -----------------------------------------------------------------------
	// Optional: Generate Editorial
	// -----------------------------------------------------------------------

	var editorialText string
	if params.GenerateEditorial {
		var editorialResult activities.EditorialResult
		err = workflow.ExecuteActivity(llmCtx, "GenerateEditorialActivity",
			statementResult, solutionResult.MainSolution, params,
		).Get(ctx, &editorialResult)
		if err != nil {
			// Editorial generation failure is non-fatal; log and continue.
			logger.Warn("editorial generation failed, continuing without editorial", "error", err)
		} else {
			editorialText = editorialResult.Editorial
			sourceArtifacts = appendArtifactRefsUnique(sourceArtifacts, editorialResult.SourceArtifacts...)
			logger.Info("editorial generated")
		}
	}

	// -----------------------------------------------------------------------
	// Step 10: Store Problem
	// -----------------------------------------------------------------------

	state.CurrentStep = domain.StepStore
	stepStart = workflow.Now(ctx)

	storeParams := params
	if reviewRepairVersion >= 1 {
		storeParams, err = problemGenerationReviewRepairStoreParamsV1(params, reviewResult.Approved)
		if err != nil {
			state.RecordStep(failedStep(domain.StepStore, stepStart, workflow.Now(ctx), err))
			errMsg := fmt.Sprintf("preparing review repair evidence for storage: %v", err)
			markFailed(errMsg)
			return state, temporal.NewNonRetryableApplicationError(errMsg, "ReviewRepairContract", err)
		}
	}
	storeInput := activities.StoreInput{
		Statement:     statementResult,
		Solutions:     solutionResult,
		TestCases:     testDataResult.TestCases,
		SandboxOutput: mainSandboxResult,
		Params:        storeParams,
		Editorial:     editorialText,
		DedupReports:  dedupReports,
		TestManifest:  testManifest,
	}
	storeInput.SourceArtifacts = appendArtifactRefsUnique(storeInput.SourceArtifacts, sourceArtifacts...)
	if payloadPatchVersion >= activities.ActivityPayloadVersion {
		storeInput.PayloadVersion = activities.ActivityPayloadVersion
		storeInput.WorkflowID = workflow.GetInfo(ctx).WorkflowExecution.ID
		storeInput.IdempotencyKey = storeInput.WorkflowID + "/store-problem/v1"
	}
	if reviewGateVersion >= 2 {
		storeInput.PayloadVersion = activities.StoreProblemPayloadVersion
	}
	if testManifestVersion >= 1 {
		storeInput.PayloadVersion = activities.StoreProblemTestManifestPayloadVersion
	}
	if params.KnowledgePointCombination != nil {
		storeInput.PayloadVersion = activities.StoreProblemKnowledgePointCombinationPayloadVersion
	}
	if params.GenerationEvidence != nil {
		storeInput.PayloadVersion = activities.StoreProblemStandardEvidencePayloadVersion
	}
	if shouldQuarantineReview {
		_, reviewHash, err := activities.CanonicalReviewResultJSON(reviewResult)
		if err != nil {
			state.RecordStep(failedStep(domain.StepStore, stepStart, workflow.Now(ctx), err))
			markFailed(fmt.Sprintf("encoding review quarantine evidence: %v", err))
			return state, err
		}
		storeInput.ReviewQuarantine = &activities.ReviewQuarantineEvidence{
			Reason:             reviewQuarantineReason,
			WorkflowRunID:      workflow.GetInfo(ctx).WorkflowExecution.RunID,
			ReviewGateChangeID: problemGenerationReviewGateV2ChangeID,
			ReviewGateVersion:  int(reviewGateVersion),
			ReviewResultSHA256: reviewHash,
			ReviewResult:       reviewResult,
			SourceAncestry:     reviewResult.SourceArtifacts,
		}
	}

	var storeResult activities.StoreResult
	err = workflow.ExecuteActivity(storeCtx, "StoreProblemActivity", storeInput).Get(ctx, &storeResult)
	if err != nil {
		state.RecordStep(failedStep(domain.StepStore, stepStart, workflow.Now(ctx), err))
		markFailed(fmt.Sprintf("problem storage failed: %v", err))
		return state, err
	}
	if shouldQuarantineReview && (storeResult.Status != domain.ProblemStatusQuarantined || storeResult.QuarantineReason == "") {
		err = fmt.Errorf("review-rejected candidate Store result is not quarantined with a reason: status=%q reason=%q", storeResult.Status, storeResult.QuarantineReason)
		state.RecordStep(failedStep(domain.StepStore, stepStart, workflow.Now(ctx), err))
		markFailed(err.Error())
		return state, err
	}
	if params.GenerationEvidence != nil {
		expectedPath := fmt.Sprintf("problems/%s/generation_standard_evidence.v1.json", storeResult.ProblemID.String())
		if storeResult.StandardEvidence == nil {
			err = fmt.Errorf("standard evidence Store result is missing its receipt reference")
		} else if validateErr := storeResult.StandardEvidence.Validate(expectedPath); validateErr != nil {
			err = fmt.Errorf("standard evidence Store result is invalid: %w", validateErr)
		}
		if err != nil {
			state.RecordStep(failedStep(domain.StepStore, stepStart, workflow.Now(ctx), err))
			markFailed(err.Error())
			return state, err
		}
	}

	state.RecordStep(completedStepVersioned(statePayloadPatchVersion, domain.StepStore, stepStart, workflow.Now(ctx), storeResult))
	logger.Info("problem storage completed",
		"problem_id", storeResult.ProblemID,
		"status", storeResult.Status,
		"quarantine_reason", storeResult.QuarantineReason,
	)

	// -----------------------------------------------------------------------
	// Complete
	// -----------------------------------------------------------------------

	if shouldQuarantineReview {
		state.MarkRejectedQuarantinedAt(workflow.Now(ctx))
	} else {
		markCompleted()
	}
	logger.Info("ProblemGenerationWorkflow completed successfully",
		"problem_id", storeResult.ProblemID,
		"serial_number", storeResult.SerialNumber,
		"problem_status", storeResult.Status,
	)

	return state, nil
}

func isReferenceSolutionFailureStageV1(stage string) bool {
	stage = strings.ToLower(strings.TrimSpace(stage))
	return strings.HasPrefix(stage, "brute_program_")
}

func problemGenerationReviewGateVersion(ctx workflow.Context, v2Version workflow.Version) workflow.Version {
	if v2Version >= 2 {
		return v2Version
	}
	legacyVersion := workflow.GetVersion(
		ctx,
		problemGenerationReviewGateV1ChangeID,
		workflow.DefaultVersion,
		1,
	)
	if legacyVersion < 1 {
		return workflow.DefaultVersion
	}
	return 1
}

func buildReviewRequest(
	ctx workflow.Context,
	reviewAttempt int,
	tokenRequired bool,
	reviewResult activities.ReviewResult,
) (*domain.ReviewRequest, error) {
	summary := domain.LLMReviewSummary{
		Approved:            reviewResult.Approved,
		Issues:              reviewResult.Issues,
		Suggestions:         reviewResult.Suggestions,
		Confidence:          reviewResult.Confidence,
		EstimatedDifficulty: reviewResult.EstimatedDifficulty,
	}
	runID := workflow.GetInfo(ctx).WorkflowExecution.RunID
	token, reviewResultHash, err := deriveReviewTokenV1(runID, reviewAttempt, reviewResult)
	if err != nil {
		return nil, err
	}

	return &domain.ReviewRequest{
		Token:            token,
		RunID:            runID,
		ReviewAttempt:    reviewAttempt,
		ReviewResultHash: reviewResultHash,
		TokenRequired:    tokenRequired,
		ReviewResult:     summary,
	}, nil
}

func deriveReviewTokenV1(runID string, reviewAttempt int, reviewResult activities.ReviewResult) (string, string, error) {
	material := reviewTokenMaterialV1{
		Approved:            reviewResult.Approved,
		Issues:              reviewResult.Issues,
		Suggestions:         reviewResult.Suggestions,
		Confidence:          reviewResult.Confidence,
		EstimatedDifficulty: reviewResult.EstimatedDifficulty,
	}
	encoded, err := json.Marshal(material)
	if err != nil {
		return "", "", fmt.Errorf("encoding review result: %w", err)
	}
	reviewDigest := sha256.Sum256(encoded)
	reviewResultHash := hex.EncodeToString(reviewDigest[:])
	tokenDigest := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%s", runID, reviewAttempt, reviewResultHash)))
	return hex.EncodeToString(tokenDigest[:]), reviewResultHash, nil
}

// completedStep builds a StepResult for a successfully completed step.
func completedStep(step domain.WorkflowStep, start, end time.Time, output interface{}) domain.StepResult {
	return domain.StepResult{
		Step:     step,
		Status:   domain.WorkflowStatusCompleted,
		Duration: end.Sub(start),
		Output:   output,
	}
}

func completedStepVersioned(version workflow.Version, step domain.WorkflowStep, start, end time.Time, output interface{}) domain.StepResult {
	if version >= 1 {
		output = compactStepOutput(output)
	}
	return completedStep(step, start, end, output)
}

const maxStepOutputBytes = 64 << 10

type compactedStepOutput struct {
	Compacted     bool   `json:"compacted"`
	OriginalBytes int    `json:"original_bytes"`
	SHA256        string `json:"sha256"`
}

func compactStepOutput(output interface{}) interface{} {
	encoded, err := json.Marshal(output)
	if err != nil || len(encoded) <= maxStepOutputBytes {
		return output
	}
	digest := sha256.Sum256(encoded)
	return compactedStepOutput{
		Compacted:     true,
		OriginalBytes: len(encoded),
		SHA256:        hex.EncodeToString(digest[:]),
	}
}

// failedStep builds a StepResult for a failed step.
func failedStep(step domain.WorkflowStep, start, end time.Time, err error) domain.StepResult {
	return domain.StepResult{
		Step:     step,
		Status:   domain.WorkflowStatusFailed,
		Duration: end.Sub(start),
		Error:    err.Error(),
	}
}

func failedStepWithOutput(step domain.WorkflowStep, start, end time.Time, err error, output interface{}) domain.StepResult {
	result := failedStep(step, start, end, err)
	result.Output = output
	return result
}

func dedupCheckFailedWorkflowError(stage string) error {
	return temporal.NewNonRetryableApplicationError(
		stage+" dedup check returned check_failed",
		"DedupCheckFailed",
		nil,
	)
}

// selectBruteCheckCases preserves the selector used before the differential
// validation v1 gate. Explicit brute groups select only their own cases; when
// no group is marked, samples and small inline cases are selected. Keep this
// behavior unchanged for Temporal replay compatibility.
func selectBruteCheckCases(
	cases []activities.TestCaseData,
	cfg domain.TestDataConfig,
) ([]activities.TestCaseData, []int) {
	bruteGroups := cfg.BruteCheckGroupIDs()
	if bruteGroups == nil {
		const inlineBruteInputLimit = 4096
		subset := make([]activities.TestCaseData, 0, len(cases))
		indices := make([]int, 0, len(cases))
		for i, tc := range cases {
			if tc.IsSample || (tc.InputArtifact == nil && tc.InputRef == "" && len(tc.Input) <= inlineBruteInputLimit) {
				subset = append(subset, tc)
				indices = append(indices, i)
			}
		}
		return subset, indices
	}

	subset := make([]activities.TestCaseData, 0, len(cases))
	indices := make([]int, 0, len(cases))
	for i, tc := range cases {
		if bruteGroups[tc.GroupID] {
			subset = append(subset, tc)
			indices = append(indices, i)
		}
	}
	return subset, indices
}

// selectBruteCheckCasesV1 is the pre-v2 policy retained only for replay of
// histories that already recorded its activity arguments. Do not use it for
// new workflows: its serialized-size heuristic can admit a semantically huge
// scalar input to a naive reference implementation.
func selectBruteCheckCasesV1(
	cases []activities.TestCaseData,
	cfg domain.TestDataConfig,
) ([]activities.TestCaseData, []int) {
	bruteGroups := cfg.BruteCheckGroupIDs()

	subset := make([]activities.TestCaseData, 0, len(cases))
	indices := make([]int, 0, len(cases))
	for i, tc := range cases {
		candidate := false
		if bruteGroups == nil {
			// Without an explicit BruteCheck group, retain the conservative
			// samples/tiny-inline fallback, but do not let a sample that was
			// materialized as a maximum-scale artifact reach the naive oracle.
			candidate = tc.IsSample || (tc.InputArtifact == nil && tc.InputRef == "" && len(tc.Input) <= validationInlineBruteInputLimitV1)
		} else {
			// Explicit groups opt in to differential checking, but the group
			// annotation is not permission to run a brute program on a giant
			// generated artifact.  Such a case remains main-only; the quality
			// gate below requires at least one genuinely small oracle case.
			candidate = tc.IsSample || bruteGroups[tc.GroupID]
		}
		if candidate && referenceCaseFitsBruteBudgetV1(tc) {
			subset = append(subset, tc)
			indices = append(indices, i)
		}
	}
	return capBruteCheckCasesV1(subset, indices)
}

// selectBruteCheckCasesV2 is the safe policy for new generation histories.
// A serialized-byte bound cannot describe semantic size: the input
// "100000000000000\n" is tiny but can make an enumerating oracle run for an
// effectively unbounded amount of time. With no explicit BruteCheck group,
// only public samples are used as the oracle witness; once groups are
// configured, only cases in groups explicitly marked BruteCheck are admitted.
// The generator prompt is responsible for keeping those selected cases
// genuinely small under the problem's own constraints.
func selectBruteCheckCasesV2(
	cases []activities.TestCaseData,
	cfg domain.TestDataConfig,
) ([]activities.TestCaseData, []int) {
	bruteGroups := cfg.BruteCheckGroupIDs()
	subset := make([]activities.TestCaseData, 0, len(cases))
	indices := make([]int, 0, len(cases))
	for i, tc := range cases {
		candidate := tc.IsSample
		if bruteGroups != nil {
			candidate = bruteGroups[tc.GroupID]
		}
		if candidate && referenceCaseFitsBruteBudgetV1(tc) {
			subset = append(subset, tc)
			indices = append(indices, i)
		}
	}
	return capBruteCheckCasesV1(subset, indices)
}

// referenceCaseFitsBruteBudgetV1 is deliberately conservative for new
// generation histories.  A byte bound cannot prove that an arbitrary naive
// algorithm is fast, so RunSandboxActivity still enforces the real problem
// time limit; this admission check only prevents obvious maximum-scale inputs
// (usually artifact-backed samples) from being submitted to the oracle.
func referenceCaseFitsBruteBudgetV1(testCase activities.TestCaseData) bool {
	var size int64
	switch {
	case testCase.InputArtifact != nil:
		size = testCase.InputArtifact.SizeBytes
	case testCase.Input != "":
		size = int64(len(testCase.Input))
	default:
		// Worker-local InputRef values and empty placeholders have no durable
		// size identity and are never admitted to a new differential subset.
		return false
	}
	return size > 0 && size <= activities.MaxReferenceDifferentialInputBytes
}

// capBruteCheckCasesV1 keeps the correctness oracle bounded even when a
// caller marks an accidentally large group as BruteCheck. Samples are retained
// first (up to the hard cap), then the remaining candidates keep their source
// order. The official suite is still executed by the main solution in full;
// only the legacy oracle workload is capped.
func capBruteCheckCasesV1(
	cases []activities.TestCaseData,
	indices []int,
) ([]activities.TestCaseData, []int) {
	if len(cases) <= activities.MaxReferenceDifferentialCases {
		return cases, indices
	}
	keep := make([]bool, len(cases))
	remaining := activities.MaxReferenceDifferentialCases
	for i, testCase := range cases {
		if remaining == 0 {
			break
		}
		if testCase.IsSample {
			keep[i] = true
			remaining--
		}
	}
	for i := range cases {
		if remaining == 0 {
			break
		}
		if !keep[i] {
			keep[i] = true
			remaining--
		}
	}
	selected := make([]activities.TestCaseData, 0, activities.MaxReferenceDifferentialCases)
	selectedIndices := make([]int, 0, activities.MaxReferenceDifferentialCases)
	for i, testCase := range cases {
		if !keep[i] {
			continue
		}
		selected = append(selected, testCase)
		selectedIndices = append(selectedIndices, indices[i])
	}
	return selected, selectedIndices
}

func structuredSamplesEnabled(v1Version, v2Version workflow.Version, sampleCount int) bool {
	return v2Version >= 1 || (v1Version >= 1 && sampleCount > 0)
}

func countSampleTestCases(testCases []activities.TestCaseData) int {
	count := 0
	for _, testCase := range testCases {
		if testCase.IsSample {
			count++
		}
	}
	return count
}

// pickMainOutputsForBruteSubset rearranges a full-length main sandbox result
// so that its outputs line up positionally with the brute sandbox result,
// which only ran on the brute-check subset. This lets ValidateActivity do a
// simple index-by-index comparison without teaching it about subsets.
func pickMainOutputsForBruteSubset(
	main activities.SandboxResult,
	bruteIndices []int,
) activities.SandboxResult {
	out := activities.SandboxResult{
		PayloadVersion: main.PayloadVersion,
		Outputs:        make([]string, 0, len(bruteIndices)),
		TimeTaken:      make([]time.Duration, 0, len(bruteIndices)),
		MemoryUsed:     make([]int64, 0, len(bruteIndices)),
	}
	if len(main.OutputArtifacts) > 0 {
		out.OutputArtifacts = make([]*activities.ArtifactRef, 0, len(bruteIndices))
	}
	if len(main.OutputRefs) > 0 {
		out.OutputRefs = make([]string, 0, len(bruteIndices))
	}
	for _, i := range bruteIndices {
		if i < len(main.Outputs) {
			out.Outputs = append(out.Outputs, main.Outputs[i])
		} else {
			out.Outputs = append(out.Outputs, "")
		}
		if i < len(main.OutputRefs) {
			out.OutputRefs = append(out.OutputRefs, main.OutputRefs[i])
		}
		if i < len(main.OutputArtifacts) {
			out.OutputArtifacts = append(out.OutputArtifacts, main.OutputArtifacts[i])
		}
		if i < len(main.TimeTaken) {
			out.TimeTaken = append(out.TimeTaken, main.TimeTaken[i])
		}
		if i < len(main.MemoryUsed) {
			out.MemoryUsed = append(out.MemoryUsed, main.MemoryUsed[i])
		}
	}
	return out
}

func mergeNeighbors(existing, added []activities.NeighborInfo) []activities.NeighborInfo {
	if len(added) == 0 {
		return existing
	}

	seen := make(map[string]bool, len(existing)+len(added))
	merged := make([]activities.NeighborInfo, 0, len(existing)+len(added))
	for _, n := range existing {
		key := n.ID.String()
		seen[key] = true
		merged = append(merged, n)
	}
	for _, n := range added {
		key := n.ID.String()
		if seen[key] {
			continue
		}
		seen[key] = true
		merged = append(merged, n)
	}
	return merged
}

func appendArtifactRefsUnique(existing []*activities.ArtifactRef, added ...*activities.ArtifactRef) []*activities.ArtifactRef {
	seen := make(map[string]struct{}, len(existing)+len(added))
	for _, ref := range existing {
		if ref != nil {
			seen[ref.WorkflowID+"\x00"+ref.Producer+"\x00"+ref.Key] = struct{}{}
		}
	}
	for _, ref := range added {
		if ref == nil {
			continue
		}
		key := ref.WorkflowID + "\x00" + ref.Producer + "\x00" + ref.Key
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		existing = append(existing, ref)
	}
	return existing
}
