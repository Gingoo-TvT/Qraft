package workflow

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/generationapi"
	qualitygate "github.com/Gingoo-TvT/Qraft/backend/internal/qualitygate/v1"
	"github.com/Gingoo-TvT/Qraft/backend/internal/workflow/activities"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const (
	ProblemGenerationQualityPayloadVersionV1 = 1
	problemQualityContractErrorTypeV1        = "ProblemGenerationQualityContractError"
)

// ProblemGenerationQualityInputV1 contains only raw authoring intent and fixed
// execution parameters. The hidden-suite reference is resolved server-side. It
// deliberately has no gate statuses, receipts, case plans, SandboxResult,
// manifest, or reviewer result for a caller to self-sign.
type ProblemGenerationQualityInputV1 struct {
	PayloadVersion int                                        `json:"payload_version"`
	SubjectID      string                                     `json:"subject_id"`
	Language       string                                     `json:"language"`
	FrozenConcept  string                                     `json:"frozen_concept"`
	RequiredFacts  []activities.CanonicalAuthoringBriefFactV1 `json:"required_facts"`
	Params         domain.ProblemGenParams                    `json:"params"`
}

type ProblemGenerationQualityResultV1 struct {
	PayloadVersion          int                        `json:"payload_version"`
	Decision                string                     `json:"decision"`
	StoppedGate             string                     `json:"stopped_gate,omitempty"`
	SubjectRevision         string                     `json:"subject_revision,omitempty"`
	ReviewerInvoked         bool                       `json:"reviewer_invoked"`
	HiddenInvoked           bool                       `json:"hidden_invoked"`
	RepairInvoked           bool                       `json:"repair_invoked"`
	RepairRounds            int                        `json:"repair_rounds"`
	FinalStatementArtifact  *activities.ArtifactRef    `json:"final_statement_artifact,omitempty"`
	AuthoringBundleArtifact *activities.ArtifactRef    `json:"authoring_bundle_artifact,omitempty"`
	StatementDraftArtifact  *activities.ArtifactRef    `json:"statement_draft_artifact,omitempty"`
	MainProgramArtifact     *activities.ArtifactRef    `json:"main_program_artifact,omitempty"`
	OracleProgramArtifact   *activities.ArtifactRef    `json:"oracle_program_artifact,omitempty"`
	OracleReceiptArtifact   *activities.ArtifactRef    `json:"oracle_receipt_artifact,omitempty"`
	TestManifestArtifact    *activities.ArtifactRef    `json:"test_manifest_artifact,omitempty"`
	Audit                   *qualitygate.AuditV1       `json:"audit,omitempty"`
	AuditArtifact           *activities.ArtifactRef    `json:"audit_artifact,omitempty"`
	LastRepairArtifact      *activities.ArtifactRef    `json:"last_repair_artifact,omitempty"`
	RepairState             *qualitygate.RepairStateV1 `json:"repair_state,omitempty"`
	StoredProblemID         string                     `json:"stored_problem_id,omitempty"`
	StoredProblemStatus     domain.ProblemStatus       `json:"stored_problem_status,omitempty"`
}

type problemQualityRevisionOutcomeV1 struct {
	result   *ProblemGenerationQualityResultV1
	blockers []qualitygate.ReviewBlockerV1
	assets   map[string]problemQualityRepairAssetV1
}

type problemQualityRepairAssetV1 struct {
	role string
	ref  activities.ArtifactRef
}

// ProblemGenerationQualityWorkflowV1 is the S3 source-of-truth pipeline. A
// non-product subject remains an acceptance/replay execution with no database
// side effects. A stable generation-job subject is fail-closed: quarantine is
// returned as QualityNotMet, while a nine-gate PASS is materialized exactly
// once as an editable draft by the D-side S4 store activity.
func ProblemGenerationQualityWorkflowV1(ctx workflow.Context, in ProblemGenerationQualityInputV1) (*ProblemGenerationQualityResultV1, error) {
	if err := validateProblemQualityInputV1(in); err != nil {
		return nil, temporal.NewNonRetryableApplicationError(err.Error(), problemQualityContractErrorTypeV1, err)
	}
	now := workflow.Now(ctx)
	state := domain.WorkflowState{
		CurrentStep: domain.StepGenerateStatement,
		Status:      domain.WorkflowStatusRunning,
		Progress:    1,
		StartedAt:   &now,
	}
	if err := workflow.SetQueryHandler(ctx, domain.WorkflowStateQueryName, func() (domain.WorkflowStateQuery, error) {
		return domain.WorkflowStateQuery{State: state}, nil
	}); err != nil {
		return nil, fmt.Errorf("register quality workflow state query: %w", err)
	}
	opts := workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Minute,
		HeartbeatTimeout:    60 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval: 2 * time.Second, BackoffCoefficient: 2, MaximumInterval: 2 * time.Minute, MaximumAttempts: 3,
			NonRetryableErrorTypes: []string{"InvalidParameterError", "S3QualityContractError", "QualityNotMet", "ValidatedSampleCandidatesContractError", "StageAuthoringStatementSamplesContractError", "FinalizeAuthoringStatementSamplesContractError"},
		},
	}
	activityCtx := workflow.WithActivityOptions(ctx, opts)
	finish := func(result *ProblemGenerationQualityResultV1) (*ProblemGenerationQualityResultV1, error) {
		return finalizeProblemQualityProductResultV1(ctx, activityCtx, in, result, &state)
	}
	var repairPolicy activities.S3RepairPolicyResultV1
	if err := workflow.ExecuteActivity(activityCtx, "ResolveS3RepairPolicyActivityV1").Get(ctx, &repairPolicy); err != nil {
		return nil, fmt.Errorf("resolve server-owned S3 repair policy: %w", err)
	}
	if repairPolicy.PayloadVersion != activities.S3QualityPayloadVersionV1 || repairPolicy.MaxRounds != qualitygate.MaxRepairRoundsV1 || strings.TrimSpace(repairPolicy.ReasonCode) == "" {
		return nil, temporal.NewNonRetryableApplicationError("server-owned S3 repair policy is invalid", problemQualityContractErrorTypeV1, nil)
	}
	currentAuthoringSource := activities.BuildCanonicalAuthoringBriefInput{
		PayloadVersion: activities.BuildCanonicalAuthoringBriefPayloadVersion,
		FrozenConcept:  in.FrozenConcept,
		RequiredFacts:  append([]activities.CanonicalAuthoringBriefFactV1(nil), in.RequiredFacts...),
	}
	seenBriefs := map[string]bool{}
	expectedRepairSHA := ""
	pendingRepairParent := ""
	var repairState *qualitygate.RepairStateV1
	repairCount := 0
	var lastRepair *activities.ArtifactRef
	for revision := 0; revision <= qualitygate.MaxRepairRoundsV1; revision++ {
		var canonicalBrief activities.BuildCanonicalAuthoringBriefResult
		if err := workflow.ExecuteActivity(activityCtx, "BuildCanonicalAuthoringBriefActivityV1", currentAuthoringSource).Get(ctx, &canonicalBrief); err != nil {
			return nil, fmt.Errorf("build canonical S3 authoring brief: %w", err)
		}
		if canonicalBrief.PayloadVersion != activities.BuildCanonicalAuthoringBriefPayloadVersion ||
			!authoringWorkflowIsSHA256V1(canonicalBrief.CanonicalBriefSHA256) ||
			strings.TrimSpace(canonicalBrief.CanonicalBrief) == "" ||
			(expectedRepairSHA != "" && canonicalBrief.CanonicalBriefSHA256 != expectedRepairSHA) {
			return nil, temporal.NewNonRetryableApplicationError("canonical authoring brief builder returned an invalid or spliced revision", problemQualityContractErrorTypeV1, nil)
		}
		if seenBriefs[canonicalBrief.CanonicalBriefSHA256] {
			return nil, temporal.NewNonRetryableApplicationError("repair revisited a previously evaluated canonical brief", problemQualityContractErrorTypeV1, nil)
		}
		seenBriefs[canonicalBrief.CanonicalBriefSHA256] = true
		authoringInput := activities.GenerateAuthoringPlanInput{PayloadVersion: activities.GenerateAuthoringPlanPayloadVersion, Params: in.Params, CanonicalBrief: canonicalBrief.CanonicalBrief, CanonicalBriefSHA256: canonicalBrief.CanonicalBriefSHA256}
		if err := validateAuthoringWorkflowInputV1(authoringInput); err != nil {
			return nil, temporal.NewNonRetryableApplicationError(err.Error(), problemQualityContractErrorTypeV1, err)
		}
		outcome, err := runProblemQualityRevisionV1(ctx, activityCtx, in, authoringInput)
		if err != nil {
			return nil, err
		}
		outcome.result.RepairInvoked = repairCount > 0
		outcome.result.RepairRounds = repairCount
		outcome.result.LastRepairArtifact = lastRepair
		blockerCodes := problemQualityBlockerCodesV1(outcome.blockers)
		if repairState != nil {
			if outcome.result.Decision != qualitygate.DecisionPass && len(blockerCodes) == 0 {
				return nil, temporal.NewNonRetryableApplicationError("failed repaired revision returned no canonical blocker codes", problemQualityContractErrorTypeV1, nil)
			}
			next, applyErr := qualitygate.ApplyRepairRoundV1(*repairState, qualitygate.RepairRoundInputV1{ParentSHA256: pendingRepairParent, ResultSHA256: canonicalBrief.CanonicalBriefSHA256, BlockerCodes: blockerCodes})
			if applyErr != nil {
				return nil, temporal.NewNonRetryableApplicationError(fmt.Sprintf("record S3 repair round: %v", applyErr), problemQualityContractErrorTypeV1, applyErr)
			}
			repairState = &next
			stateCopy := next
			outcome.result.RepairState = &stateCopy
			if next.Decision != qualitygate.RepairDecisionActive {
				return finish(outcome.result)
			}
		}
		if outcome.result.Decision == qualitygate.DecisionPass || !repairPolicy.Enabled || revision >= qualitygate.MaxRepairRoundsV1 || len(outcome.blockers) == 0 {
			return finish(outcome.result)
		}
		if repairState == nil {
			initial, stateErr := qualitygate.NewRepairStateV1(canonicalBrief.CanonicalBriefSHA256, blockerCodes)
			if stateErr != nil {
				return nil, temporal.NewNonRetryableApplicationError(fmt.Sprintf("initialize S3 repair state: %v", stateErr), problemQualityContractErrorTypeV1, stateErr)
			}
			repairState = &initial
		}
		repairInput, ok := buildProblemQualityRepairInputV1(repairState.CurrentSHA256, len(repairState.Rounds)+1, outcome.blockers, outcome.assets)
		if !ok {
			return finish(outcome.result)
		}
		var repaired activities.S3RepairRevisionResultV1
		if err := workflow.ExecuteActivity(activityCtx, "S3RepairRevisionActivityV1", repairInput).Get(ctx, &repaired); err != nil {
			return nil, fmt.Errorf("repair S3 revision: %w", err)
		}
		if repaired.PayloadVersion != activities.S3QualityPayloadVersionV1 ||
			repaired.ParentRevisionSHA256 != repairState.CurrentSHA256 ||
			repaired.NewRevisionArtifact == nil ||
			repaired.NewRevisionArtifact.SHA256 != repaired.NewRevisionSHA256 ||
			repaired.NewRevisionSHA256 == repairInput.ParentRevisionSHA256 ||
			!authoringWorkflowIsSHA256V1(repaired.NewRevisionSHA256) ||
			repaired.NewAuthoringInput.PayloadVersion != activities.BuildCanonicalAuthoringBriefPayloadVersion ||
			strings.TrimSpace(repaired.NewCanonicalBrief) == "" {
			return nil, temporal.NewNonRetryableApplicationError("repair activity returned an invalid immutable revision", problemQualityContractErrorTypeV1, nil)
		}
		if seenBriefs[repaired.NewRevisionSHA256] {
			return nil, temporal.NewNonRetryableApplicationError("repair returned a previously evaluated canonical brief", problemQualityContractErrorTypeV1, nil)
		}
		pendingRepairParent = repairState.CurrentSHA256
		currentAuthoringSource = repaired.NewAuthoringInput
		expectedRepairSHA = repaired.NewRevisionSHA256
		repairCount++
		lastRepair = repaired.NewRevisionArtifact
	}
	return nil, temporal.NewNonRetryableApplicationError("unreachable S3 repair state", problemQualityContractErrorTypeV1, nil)
}

func finalizeProblemQualityProductResultV1(
	ctx workflow.Context,
	activityCtx workflow.Context,
	in ProblemGenerationQualityInputV1,
	result *ProblemGenerationQualityResultV1,
	state *domain.WorkflowState,
) (*ProblemGenerationQualityResultV1, error) {
	if !generationapi.IsJobID(in.SubjectID) {
		return result, nil
	}
	if result == nil || result.Decision != qualitygate.DecisionPass {
		return nil, temporal.NewNonRetryableApplicationError(
			"candidate did not satisfy all nine quality gates",
			"QualityNotMet",
			nil,
		)
	}
	if result.Audit == nil || result.AuditArtifact == nil ||
		result.AuthoringBundleArtifact == nil || result.StatementDraftArtifact == nil ||
		result.FinalStatementArtifact == nil || result.MainProgramArtifact == nil ||
		result.OracleProgramArtifact == nil || result.OracleReceiptArtifact == nil ||
		result.TestManifestArtifact == nil {
		return nil, temporal.NewNonRetryableApplicationError(
			"quality PASS is missing materialization-critical server artifacts",
			problemQualityContractErrorTypeV1,
			nil,
		)
	}
	if state != nil {
		state.CurrentStep = domain.StepStore
		state.Status = domain.WorkflowStatusRunning
		state.Progress = 95
	}
	evidenceLevel, err := generationapi.QualityEvidenceLevelFromParams(in.Params)
	if err != nil {
		return nil, temporal.NewNonRetryableApplicationError(
			"quality evidence profile is invalid: "+err.Error(),
			problemQualityContractErrorTypeV1,
			err,
		)
	}
	var stored activities.StoreS3QualityDraftResultV1
	storeInput := activities.StoreS3QualityDraftInputV1{
		PayloadVersion:          activities.StoreS3QualityDraftPayloadVersionV1,
		WorkflowID:              in.SubjectID,
		SubjectRevision:         result.SubjectRevision,
		FrozenConcept:           in.FrozenConcept,
		Language:                in.Language,
		EvidenceLevel:           evidenceLevel,
		Params:                  in.Params,
		AuthoringBundleArtifact: *result.AuthoringBundleArtifact,
		StatementDraftArtifact:  *result.StatementDraftArtifact,
		FinalStatementArtifact:  *result.FinalStatementArtifact,
		MainProgramArtifact:     *result.MainProgramArtifact,
		OracleProgramArtifact:   *result.OracleProgramArtifact,
		OracleReceiptArtifact:   *result.OracleReceiptArtifact,
		TestManifestArtifact:    *result.TestManifestArtifact,
		AuditArtifact:           *result.AuditArtifact,
	}
	if err := workflow.ExecuteActivity(activityCtx, "StoreS3QualityDraftActivityV1", storeInput).Get(ctx, &stored); err != nil {
		return nil, fmt.Errorf("materialize S3 quality PASS as product draft: %w", err)
	}
	if stored.PayloadVersion != activities.StoreS3QualityDraftPayloadVersionV1 ||
		stored.ProblemID == "" || stored.Status != domain.ProblemStatusDraft ||
		stored.AuditSHA256 != result.AuditArtifact.SHA256 ||
		stored.TestManifestSHA256 != result.TestManifestArtifact.SHA256 {
		return nil, temporal.NewNonRetryableApplicationError(
			"quality draft materializer returned an invalid result binding",
			problemQualityContractErrorTypeV1,
			nil,
		)
	}
	result.StoredProblemID = stored.ProblemID
	result.StoredProblemStatus = stored.Status
	if state != nil {
		state.MarkCompletedAt(workflow.Now(ctx))
	}
	return result, nil
}

func runProblemQualityRevisionV1(ctx, activityCtx workflow.Context, root ProblemGenerationQualityInputV1, authoringInput activities.GenerateAuthoringPlanInput) (*problemQualityRevisionOutcomeV1, error) {
	quarantine := func(gate, revision string, reviewer, hidden bool, refs ...*activities.ArtifactRef) *problemQualityRevisionOutcomeV1 {
		result := &ProblemGenerationQualityResultV1{PayloadVersion: ProblemGenerationQualityPayloadVersionV1, Decision: qualitygate.DecisionQuarantine, StoppedGate: gate, SubjectRevision: revision, ReviewerInvoked: reviewer, HiddenInvoked: hidden}
		if len(refs) > 0 {
			result.FinalStatementArtifact = refs[0]
		}
		if len(refs) > 1 {
			result.MainProgramArtifact = refs[1]
		}
		if len(refs) > 2 {
			result.TestManifestArtifact = refs[2]
		}
		return &problemQualityRevisionOutcomeV1{result: result, assets: map[string]problemQualityRepairAssetV1{}}
	}

	var authoring activities.GenerateAuthoringPlanResult
	if err := workflow.ExecuteActivity(activityCtx, "GenerateAuthoringPlanActivity", authoringInput).Get(ctx, &authoring); err != nil {
		return nil, fmt.Errorf("generate S3 authoring plan: %w", err)
	}
	if err := validateAuthoringWorkflowResultV1(authoringInput, &authoring); err != nil {
		return nil, temporal.NewNonRetryableApplicationError(err.Error(), problemQualityContractErrorTypeV1, err)
	}
	if authoring.Decision != activities.AuthoringPlanDecisionAccepted {
		return quarantine(qualitygate.GateSpecLint, authoring.BundleSHA256, false, false), nil
	}
	bundleRef := *authoring.BundleArtifact
	var specLintGate activities.BuildS3SpecLintGateResultV1
	specLintInput := activities.BuildS3SpecLintGateInputV1{
		PayloadVersion: activities.S3QualityPayloadVersionV1, AuthoringBundleArtifact: bundleRef,
		ExpectedAuthoringBundleSHA256: authoring.BundleSHA256, ExpectedAuthoringInputSHA256: authoring.InputSHA256,
		ExpectedBriefSHA256: authoring.BriefSHA256, ExpectedSemanticSpecSHA256: authoring.SemanticSpecSHA256,
		ExpectedDifficulty: authoringInput.Params.Difficulty, RequiredKnowledgePoints: append([]string(nil), authoringInput.Params.Tags...),
	}
	if err := workflow.ExecuteActivity(activityCtx, "BuildS3SpecLintGateActivityV1", specLintInput).Get(ctx, &specLintGate); err != nil {
		return nil, fmt.Errorf("build server-recomputed S3 spec-lint gate: %w", err)
	}
	if specLintGate.Gate.Status != qualitygate.GateStatusPass || specLintGate.ReceiptArtifact == nil {
		out := quarantine(qualitygate.GateSpecLint, authoring.BundleSHA256, false, false)
		out.blockers = append([]qualitygate.ReviewBlockerV1(nil), specLintGate.Gate.Blockers...)
		return out, nil
	}
	locale, err := authoringStatementLocaleV1(authoringInput.Params.Locale)
	if err != nil {
		return nil, err
	}
	renderInput := activities.RenderStatementFromAuthoringBundleInput{PayloadVersion: activities.RenderStatementFromAuthoringBundlePayloadVersion, BundleArtifact: bundleRef, ExpectedBundleSHA256: authoring.BundleSHA256, ExpectedAuthoringInputSHA256: authoring.InputSHA256, ExpectedBriefSHA256: authoring.BriefSHA256, ExpectedSemanticSpecSHA256: authoring.SemanticSpecSHA256, ExpectedDifficulty: authoringInput.Params.Difficulty, RequiredKnowledgePoints: append([]string(nil), authoringInput.Params.Tags...), PresentationLocale: locale, StatementRuntime: copyAuthoringStatementRuntimeV1(authoringInput.Params.ProviderConfig)}
	var statement activities.RenderStatementFromAuthoringBundleResult
	if err := workflow.ExecuteActivity(activityCtx, "RenderStatementFromAuthoringBundleActivityV1", renderInput).Get(ctx, &statement); err != nil {
		return nil, fmt.Errorf("render S3 statement shell: %w", err)
	}
	if err := validateAuthoringStatementWorkflowResultV1(renderInput, &statement); err != nil {
		return nil, temporal.NewNonRetryableApplicationError(err.Error(), problemQualityContractErrorTypeV1, err)
	}
	draftRef := *statement.StatementDraftArtifact
	validateSamplesInput := activities.ValidateAuthoringSampleCandidatesInput{PayloadVersion: activities.ValidateAuthoringSampleCandidatesPayloadVersion, BundleArtifact: bundleRef, ExpectedBundleSHA256: authoring.BundleSHA256, ExpectedAuthoringInputSHA256: authoring.InputSHA256, ExpectedBriefSHA256: authoring.BriefSHA256, ExpectedSemanticSpecSHA256: authoring.SemanticSpecSHA256, ExpectedDifficulty: authoringInput.Params.Difficulty, RequiredKnowledgePoints: append([]string(nil), authoringInput.Params.Tags...), ExpectedSampleCount: authoringInput.Params.TestDataConfig.NumSamples}
	extractInput := activities.ExtractSemanticSpecArtifactInputV1{PayloadVersion: activities.S3QualityPayloadVersionV1, AuthoringBundleArtifact: bundleRef, ExpectedAuthoringBundleSHA: authoring.BundleSHA256, ExpectedAuthoringInputSHA: authoring.InputSHA256, ExpectedBriefSHA: authoring.BriefSHA256, ExpectedSemanticSpecSHA: authoring.SemanticSpecSHA256}

	// These two reads validate independent projections of the immutable bundle.
	// Schedule both before waiting so a slow artifact store or parser does not
	// serialize the whole quality run.
	validatedSamplesFuture := workflow.ExecuteActivity(activityCtx, "ValidateAuthoringSampleCandidatesActivityV1", validateSamplesInput)
	semanticFuture := workflow.ExecuteActivity(activityCtx, "ExtractSemanticSpecArtifactActivityV1", extractInput)

	var validatedSamples activities.ValidateAuthoringSampleCandidatesResult
	if err := validatedSamplesFuture.Get(ctx, &validatedSamples); err != nil {
		return nil, fmt.Errorf("validate S3 sample candidates: %w", err)
	}
	if validatedSamples.ValidatedBundleArtifact == nil || validatedSamples.Status != activities.ValidatedSampleCandidatesStatusInputsOnlyV1 || validatedSamples.SemanticSpecSHA256 != authoring.SemanticSpecSHA256 {
		return nil, temporal.NewNonRetryableApplicationError("validated sample result binding mismatch", problemQualityContractErrorTypeV1, nil)
	}
	validatedRef := *validatedSamples.ValidatedBundleArtifact

	var semantic activities.ExtractSemanticSpecArtifactResultV1
	if err := semanticFuture.Get(ctx, &semantic); err != nil {
		return nil, fmt.Errorf("extract standalone SemanticSpec: %w", err)
	}
	if semantic.SemanticSpecArtifact == nil || semantic.SemanticSpecSHA256 != authoring.SemanticSpecSHA256 {
		return nil, temporal.NewNonRetryableApplicationError("standalone SemanticSpec binding mismatch", problemQualityContractErrorTypeV1, nil)
	}
	semanticRef := *semantic.SemanticSpecArtifact

	var testData activities.TestDataResult
	testDataInput := activities.GenerateS3TestDataInputV1{PayloadVersion: activities.S3QualityPayloadVersionV1, AuthoringBundleArtifact: bundleRef, ExpectedAuthoringBundleSHA: authoring.BundleSHA256, StatementDraftArtifact: draftRef, ExpectedStatementDraftSHA: statement.StatementDraftSHA256, ExpectedSemanticSpecSHA: authoring.SemanticSpecSHA256, Config: authoringInput.Params.TestDataConfig, Params: authoringInput.Params}
	mainInput := activities.GenerateMainSolutionInputV1{PayloadVersion: activities.S3QualityPayloadVersionV1, AuthoringBundleArtifact: bundleRef, ExpectedAuthoringBundleSHA: authoring.BundleSHA256, StatementDraftArtifact: draftRef, ExpectedStatementDraftSHA: statement.StatementDraftSHA256, ExpectedSemanticSpecSHA: authoring.SemanticSpecSHA256, Language: root.Language, MainRuntime: copyAuthoringStatementRuntimeV1(authoringInput.Params.ProviderConfig)}
	oracleInput := activities.GenerateOracleCandidateInputV1{PayloadVersion: activities.OracleCandidatePayloadVersionV1, SemanticSpecArtifact: semanticRef, ExpectedSemanticSpecSHA256: authoring.SemanticSpecSHA256, Language: root.Language, VerificationRuntime: copyProblemQualityRuntimeV1(authoringInput.Params.ProviderConfig, "verification")}

	// Test-data generation, main implementation, and independent oracle
	// generation have no dependency on one another after the immutable
	// projections above are available. Keep their model calls in flight
	// together, then materialize the case plan from the returned test data.
	testDataFuture := workflow.ExecuteActivity(activityCtx, "GenerateS3TestDataActivityV1", testDataInput)
	mainFuture := workflow.ExecuteActivity(activityCtx, "GenerateMainSolutionActivityV1", mainInput)
	oracleFuture := workflow.ExecuteActivity(activityCtx, "GenerateOracleCandidateActivityV1", oracleInput)

	if err := testDataFuture.Get(ctx, &testData); err != nil {
		return nil, fmt.Errorf("generate S3 test data: %w", err)
	}
	casePlanFuture := workflow.ExecuteActivity(activityCtx, "MaterializeS3CasePlanActivityV1", activities.MaterializeS3CasePlanInputV1{PayloadVersion: activities.S3QualityPayloadVersionV1, AuthoringBundleArtifact: bundleRef, ExpectedAuthoringBundleSHA: authoring.BundleSHA256, TestData: testData})

	var main activities.GenerateMainSolutionResultV1
	if err := mainFuture.Get(ctx, &main); err != nil {
		return nil, fmt.Errorf("generate S3 main solution: %w", err)
	}
	if main.CandidateSourceArtifact == nil {
		return nil, temporal.NewNonRetryableApplicationError("main solution has no CAS source", problemQualityContractErrorTypeV1, nil)
	}
	mainRef := *main.CandidateSourceArtifact
	var oracle activities.GenerateOracleCandidateResultV1
	if err := oracleFuture.Get(ctx, &oracle); err != nil {
		return nil, fmt.Errorf("generate independent S3 oracle: %w", err)
	}
	var casePlan activities.MaterializeS3CasePlanResultV1
	if err := casePlanFuture.Get(ctx, &casePlan); err != nil {
		return nil, fmt.Errorf("materialize S3 case plan: %w", err)
	}
	if casePlan.CasePlanArtifact == nil || casePlan.TestCount == 0 {
		return nil, temporal.NewNonRetryableApplicationError("case-plan activity returned no cases", problemQualityContractErrorTypeV1, nil)
	}
	casePlanRef := *casePlan.CasePlanArtifact
	limits := activities.ExecutionLimits{TimeLimitMs: authoringInput.Params.TimeLimit, MemoryLimitMB: authoringInput.Params.MemoryLimit}
	var oracleGate activities.S3OracleGateResultV1
	if err := workflow.ExecuteActivity(activityCtx, "VerifyProgramAgainstIndependentOracleActivityV1", activities.S3OracleGateInputV1{PayloadVersion: activities.S3QualityPayloadVersionV1, ExpectedSemanticSpecSHA256: authoring.SemanticSpecSHA256, MainCandidate: main, OracleCandidate: oracle, CasePlanArtifact: casePlanRef, Limits: limits}).Get(ctx, &oracleGate); err != nil {
		return nil, fmt.Errorf("verify main against independent oracle: %w", err)
	}
	baseAssets := map[string]problemQualityRepairAssetV1{}
	addProblemQualityAssetV1(baseAssets, "semantic_spec", semanticRef)
	addProblemQualityAssetV1(baseAssets, "statement_draft", draftRef)
	addProblemQualityAssetV1(baseAssets, "main_solution", mainRef)
	if oracleGate.Gate.Status != qualitygate.GateStatusPass || oracle.CandidateSourceArtifact == nil || oracleGate.VerifiedProgramReceiptArtifact == nil || oracleGate.ReceiptArtifact == nil || oracleGate.MainRun == nil {
		out := quarantine(qualitygate.GateOracleDifferential, main.CandidateSourceSHA256, false, false, nil, &mainRef)
		out.blockers, out.assets = oracleGate.Gate.Blockers, baseAssets
		return out, nil
	}
	oracleRef := *oracle.CandidateSourceArtifact
	verifiedRef := *oracleGate.VerifiedProgramReceiptArtifact
	oracleReceiptRef := *oracleGate.ReceiptArtifact
	addProblemQualityAssetV1(baseAssets, "oracle_promotion", oracleReceiptRef)
	var hiddenResolution activities.ResolveS3HiddenSuiteResultV1
	if err := workflow.ExecuteActivity(activityCtx, "ResolveS3HiddenSuiteActivityV1", activities.ResolveS3HiddenSuiteInputV1{PayloadVersion: activities.S3QualityPayloadVersionV1, SubjectID: root.SubjectID, SemanticSpecSHA256: authoring.SemanticSpecSHA256}).Get(ctx, &hiddenResolution); err != nil {
		return nil, fmt.Errorf("resolve server-owned opaque hidden suite: %w", err)
	}
	if hiddenResolution.PayloadVersion != activities.S3QualityPayloadVersionV1 {
		return nil, temporal.NewNonRetryableApplicationError("hidden-suite resolver returned an invalid result", problemQualityContractErrorTypeV1, nil)
	}

	var firstRun activities.AuthoringSampleSandboxRunV1
	if err := workflow.ExecuteActivity(activityCtx, "RunVerifiedAuthoringSamplesActivityV1", activities.RunVerifiedAuthoringSamplesInputV1{PayloadVersion: activities.S3QualityPayloadVersionV1, Phase: activities.AuthoringSampleSandboxRunPhaseFirst, ProgramArtifact: mainRef, Language: root.Language, VerifiedProgramReceiptArtifact: verifiedRef, SourceArtifact: validatedRef, Limits: limits}).Get(ctx, &firstRun); err != nil {
		return nil, fmt.Errorf("run first verified authoring samples: %w", err)
	}
	var staged activities.StageAuthoringStatementSamplesResult
	stageInput := activities.StageAuthoringStatementSamplesInput{PayloadVersion: activities.StageAuthoringStatementSamplesPayloadVersion, AuthoringBundleArtifact: bundleRef, ExpectedAuthoringBundleSHA256: authoring.BundleSHA256, StatementDraftArtifact: draftRef, ExpectedStatementDraftSHA256: statement.StatementDraftSHA256, ValidatedSamplesArtifact: validatedRef, ExpectedValidatedSamplesSHA256: validatedSamples.ValidatedBundleSHA256, VerifiedProgramReceiptArtifact: verifiedRef, ExpectedVerifiedProgramReceiptSHA256: oracleGate.VerifiedProgramReceiptSHA256, FirstRun: firstRun}
	if err := workflow.ExecuteActivity(activityCtx, "StageAuthoringStatementSamplesActivityV1", stageInput).Get(ctx, &staged); err != nil {
		return nil, fmt.Errorf("stage verified statement samples: %w", err)
	}
	if staged.StagedBundleArtifact == nil {
		return nil, temporal.NewNonRetryableApplicationError("sample stage returned no CAS bundle", problemQualityContractErrorTypeV1, nil)
	}
	stagedRef := *staged.StagedBundleArtifact
	var secondRun activities.AuthoringSampleSandboxRunV1
	if err := workflow.ExecuteActivity(activityCtx, "RunVerifiedAuthoringSamplesActivityV1", activities.RunVerifiedAuthoringSamplesInputV1{PayloadVersion: activities.S3QualityPayloadVersionV1, Phase: activities.AuthoringSampleSandboxRunPhaseReparse, ProgramArtifact: mainRef, Language: root.Language, VerifiedProgramReceiptArtifact: verifiedRef, SourceArtifact: stagedRef, Limits: limits}).Get(ctx, &secondRun); err != nil {
		return nil, fmt.Errorf("run reparsed verified authoring samples: %w", err)
	}
	var finalStatement activities.FinalizeAuthoringStatementSamplesResult
	if err := workflow.ExecuteActivity(activityCtx, "FinalizeAuthoringStatementSamplesActivityV1", activities.FinalizeAuthoringStatementSamplesInput{PayloadVersion: activities.FinalizeAuthoringStatementSamplesPayloadVersion, StagedBundleArtifact: stagedRef, ExpectedStagedBundleSHA256: staged.StagedBundleSHA256, SecondRun: secondRun}).Get(ctx, &finalStatement); err != nil {
		return nil, fmt.Errorf("finalize verified statement samples: %w", err)
	}
	if finalStatement.FinalBundleArtifact == nil {
		return nil, temporal.NewNonRetryableApplicationError("sample finalizer returned no CAS bundle", problemQualityContractErrorTypeV1, nil)
	}
	finalRef := *finalStatement.FinalBundleArtifact
	addProblemQualityAssetV1(baseAssets, "final_statement", finalRef)
	var sampleOutputGate activities.BuildS3SampleOutputGateResultV1
	sampleOutputInput := activities.BuildS3SampleOutputGateInputV1{
		PayloadVersion:         activities.S3QualityPayloadVersionV1,
		FinalStatementArtifact: finalRef, ExpectedFinalStatementSHA256: finalStatement.FinalBundleSHA256,
		ExpectedAuthoringBundleSHA256: authoring.BundleSHA256, ExpectedStatementDraftSHA256: statement.StatementDraftSHA256,
		ExpectedSemanticSpecSHA256:     authoring.SemanticSpecSHA256,
		VerifiedProgramReceiptArtifact: verifiedRef, ExpectedVerifiedProgramReceiptSHA256: oracleGate.VerifiedProgramReceiptSHA256,
		OracleReceiptArtifact: oracleReceiptRef, ExpectedOracleReceiptSHA256: oracleGate.ReceiptSHA256,
	}
	if err := workflow.ExecuteActivity(activityCtx, "BuildS3SampleOutputGateActivityV1", sampleOutputInput).Get(ctx, &sampleOutputGate); err != nil {
		return nil, fmt.Errorf("build server-recomputed S3 sample-output gate: %w", err)
	}
	if sampleOutputGate.Gate.Status != qualitygate.GateStatusPass || sampleOutputGate.ReceiptArtifact == nil {
		out := quarantine(qualitygate.GateSampleOutputBinding, finalStatement.FinalBundleSHA256, false, false, &finalRef, &mainRef)
		out.blockers, out.assets = append([]qualitygate.ReviewBlockerV1(nil), sampleOutputGate.Gate.Blockers...), baseAssets
		return out, nil
	}

	var sanitizer activities.S3SanitizerGateResultV1
	if err := workflow.ExecuteActivity(activityCtx, "RunSanitizerGateActivityV1", activities.S3SanitizerGateInputV1{PayloadVersion: activities.S3QualityPayloadVersionV1, CandidateArtifact: mainRef, Language: root.Language, VerifiedProgramReceiptArtifact: verifiedRef, CasePlanArtifact: casePlanRef, ExpectedSmallSuiteSHA256: casePlan.SanitizerSuiteSHA256, Limits: limits}).Get(ctx, &sanitizer); err != nil {
		return nil, fmt.Errorf("run S3 sanitizer gate: %w", err)
	}
	if sanitizer.ReceiptArtifact != nil {
		addProblemQualityAssetV1(baseAssets, "sanitizer", *sanitizer.ReceiptArtifact)
	}
	if sanitizer.Gate.Status != qualitygate.GateStatusPass {
		out := quarantine(qualitygate.GateSanitizer, finalStatement.FinalBundleSHA256, false, false, &finalRef, &mainRef)
		out.blockers, out.assets = sanitizer.Gate.Blockers, baseAssets
		return out, nil
	}

	var manifest activities.S3ManifestGateResultV1
	manifestInput := activities.S3ManifestGateInputV1{PayloadVersion: activities.S3QualityPayloadVersionV1, SemanticSpecArtifact: semanticRef, AuthoringBundleArtifact: bundleRef, CasePlanArtifact: casePlanRef, MainRun: *oracleGate.MainRun, OracleReceiptArtifact: oracleReceiptRef, VerifiedProgramReceiptArtifact: verifiedRef, SanitizerReceiptArtifact: *sanitizer.ReceiptArtifact}
	if err := workflow.ExecuteActivity(activityCtx, "BuildS3TestManifestActivityV1", manifestInput).Get(ctx, &manifest); err != nil {
		return nil, fmt.Errorf("build S3 TestManifest v2: %w", err)
	}
	if manifest.TestManifestArtifact == nil || manifest.BoundaryReceiptArtifact == nil {
		return nil, temporal.NewNonRetryableApplicationError("manifest builder returned incomplete CAS evidence", problemQualityContractErrorTypeV1, nil)
	}
	manifestRef := *manifest.TestManifestArtifact
	boundaryRef := *manifest.BoundaryReceiptArtifact
	addProblemQualityAssetV1(baseAssets, "test_manifest", manifestRef)
	addProblemQualityAssetV1(baseAssets, "boundary_coverage", boundaryRef)

	var dedup activities.S3DedupGateResultV1
	if err := workflow.ExecuteActivity(activityCtx, "S3DedupGateActivityV1", activities.S3DedupGateInputV1{PayloadVersion: activities.S3QualityPayloadVersionV1, FinalStatementArtifact: finalRef, ExpectedFinalStatementSHA256: finalStatement.FinalBundleSHA256, StatementDraftArtifact: draftRef, ExpectedStatementDraftSHA256: statement.StatementDraftSHA256, ExpectedMarkdownSHA256: finalStatement.MarkdownSHA256}).Get(ctx, &dedup); err != nil {
		return nil, fmt.Errorf("run S3 dedup gate: %w", err)
	}
	pre := qualitygate.PreReviewEvidenceV1{SchemaVersion: qualitygate.PreReviewEvidenceSchemaVersionV1, SubjectID: root.SubjectID, SubjectRevision: finalStatement.FinalBundleSHA256, SpecLint: specLintGate.Gate, SampleOutputBinding: sampleOutputGate.Gate, OracleDifferential: oracleGate.Gate, Sanitizer: sanitizer.Gate, BoundaryCoverage: manifest.BoundaryCoverageGate, TestManifest: manifest.TestManifestGate, Dedup: dedup.Gate}
	shouldReview, err := qualitygate.ShouldInvokeReviewerV1(pre)
	if err != nil {
		return nil, temporal.NewNonRetryableApplicationError(fmt.Sprintf("validate S3 pre-review evidence: %v", err), problemQualityContractErrorTypeV1, err)
	}
	if !shouldReview {
		out := quarantine(qualitygate.GateDedup, finalStatement.FinalBundleSHA256, false, false, &finalRef, &mainRef, &manifestRef)
		out.assets = baseAssets
		out.blockers = append([]qualitygate.ReviewBlockerV1(nil), dedup.Gate.Blockers...)
		return out, nil
	}

	visible := []activities.S3ReviewerAssetV1{{Role: "semantic_spec", Artifact: semanticRef, ExpectedSHA256: semanticRef.SHA256}, {Role: "final_statement", Artifact: finalRef, ExpectedSHA256: finalRef.SHA256}, {Role: "oracle_promotion", Artifact: oracleReceiptRef, ExpectedSHA256: oracleReceiptRef.SHA256}, {Role: "sanitizer", Artifact: *sanitizer.ReceiptArtifact, ExpectedSHA256: sanitizer.ReceiptSHA256}, {Role: "boundary_coverage", Artifact: boundaryRef, ExpectedSHA256: boundaryRef.SHA256}}
	var reviewer activities.S3StrictReviewerResultV1
	if err := workflow.ExecuteActivity(activityCtx, "S3StrictReviewerActivityV1", activities.S3StrictReviewerInputV1{PayloadVersion: activities.S3QualityPayloadVersionV1, SubjectID: root.SubjectID, SubjectRevision: finalStatement.FinalBundleSHA256, TestManifestArtifact: manifestRef, VisibleAssets: visible, ReviewRuntime: copyProblemQualityRuntimeV1(authoringInput.Params.ProviderConfig, "review")}).Get(ctx, &reviewer); err != nil {
		return nil, fmt.Errorf("run strict S3 reviewer: %w", err)
	}
	if !problemQualityReviewerPassV1(reviewer.Reviewer.Assessment) {
		out := quarantine(qualitygate.GateReviewerSchemaVerdict, finalStatement.FinalBundleSHA256, true, false, &finalRef, &mainRef, &manifestRef)
		out.assets, out.blockers = baseAssets, append([]qualitygate.ReviewBlockerV1(nil), reviewer.Reviewer.Assessment.Blockers...)
		return out, nil
	}

	var hidden activities.S3HiddenGateResultV1
	if err := workflow.ExecuteActivity(activityCtx, "S3HiddenRegressionActivityV1", activities.S3HiddenGateInputV1{PayloadVersion: activities.S3QualityPayloadVersionV1, Resolution: hiddenResolution, CandidateArtifact: mainRef}).Get(ctx, &hidden); err != nil {
		return nil, fmt.Errorf("run opaque S3 hidden regression: %w", err)
	}
	evidence := qualitygate.S3EvidenceV1{SchemaVersion: qualitygate.S3EvidenceSchemaVersionV1, SubjectID: root.SubjectID, SubjectRevision: finalStatement.FinalBundleSHA256, SpecLint: specLintGate.Gate, SampleOutputBinding: sampleOutputGate.Gate, OracleDifferential: oracleGate.Gate, Sanitizer: sanitizer.Gate, BoundaryCoverage: manifest.BoundaryCoverageGate, TestManifest: manifest.TestManifestGate, Reviewer: reviewer.Reviewer, Dedup: dedup.Gate, HiddenRegression: hidden.Gate}
	var verdict activities.S3VerdictResultV1
	if err := workflow.ExecuteActivity(activityCtx, "RecomputeS3VerdictActivityV1", activities.S3VerdictInputV1{PayloadVersion: activities.S3QualityPayloadVersionV1, Evidence: evidence}).Get(ctx, &verdict); err != nil {
		return nil, fmt.Errorf("server-recompute S3 verdict: %w", err)
	}
	result := &ProblemGenerationQualityResultV1{
		PayloadVersion: ProblemGenerationQualityPayloadVersionV1,
		Decision:       verdict.Audit.Decision, SubjectRevision: finalStatement.FinalBundleSHA256,
		ReviewerInvoked: true, HiddenInvoked: true,
		AuthoringBundleArtifact: &bundleRef, StatementDraftArtifact: &draftRef,
		FinalStatementArtifact: &finalRef, MainProgramArtifact: &mainRef,
		OracleProgramArtifact: &oracleRef, OracleReceiptArtifact: &oracleReceiptRef,
		TestManifestArtifact: &manifestRef, Audit: &verdict.Audit, AuditArtifact: verdict.AuditArtifact,
	}
	if verdict.Audit.Decision != qualitygate.DecisionPass {
		result.StoppedGate = firstProblemQualityFailedGateV1(verdict.Audit)
	}
	out := &problemQualityRevisionOutcomeV1{result: result, assets: baseAssets}
	for _, issue := range verdict.Audit.BlockingIssues {
		out.blockers = append(out.blockers, qualitygate.ReviewBlockerV1{Code: issue.Code, ResponsibleAsset: issue.ResponsibleAsset, Witness: issue.Witness})
	}
	return out, nil
}

func validateProblemQualityInputV1(in ProblemGenerationQualityInputV1) error {
	if in.PayloadVersion != ProblemGenerationQualityPayloadVersionV1 || strings.TrimSpace(in.SubjectID) == "" || len(in.SubjectID) > 128 || in.SubjectID != strings.TrimSpace(in.SubjectID) || strings.TrimSpace(in.Language) == "" {
		return fmt.Errorf("invalid S3 workflow identity")
	}
	if !utf8.ValidString(in.FrozenConcept) || strings.TrimSpace(in.FrozenConcept) == "" || len(in.FrozenConcept) > 64<<10 || len(in.RequiredFacts) > 128 {
		return fmt.Errorf("invalid frozen S3 concept/fact input")
	}
	params := in.Params
	if err := params.Validate(); err != nil {
		return err
	}
	if _, err := generationapi.QualityEvidenceLevelFromParams(params); err != nil {
		return err
	}
	if in.Params.TestDataConfig.NumSamples <= 0 {
		return fmt.Errorf("S3 requires at least one public sample candidate")
	}
	foundLanguage := false
	for _, language := range in.Params.Languages {
		foundLanguage = foundLanguage || language == in.Language
	}
	if len(in.Params.Languages) > 0 && !foundLanguage {
		return fmt.Errorf("S3 language is not present in authoring languages")
	}
	return nil
}

func problemQualityBlockerCodesV1(blockers []qualitygate.ReviewBlockerV1) []string {
	codes := make([]string, 0, len(blockers))
	for _, blocker := range blockers {
		codes = append(codes, blocker.Code)
	}
	sort.Strings(codes)
	compact := codes[:0]
	for _, code := range codes {
		if len(compact) == 0 || compact[len(compact)-1] != code {
			compact = append(compact, code)
		}
	}
	if compact == nil {
		return []string{}
	}
	return compact
}

func copyProblemQualityRuntimeV1(config *domain.ProviderRuntimeConfig, role string) *domain.LLMRuntimeConfig {
	if config == nil {
		return nil
	}
	var source *domain.LLMRuntimeConfig
	switch role {
	case "verification":
		source = config.Verification
	case "review":
		source = config.Review
	}
	if source == nil {
		return nil
	}
	copyValue := *source
	return &copyValue
}

func problemQualityEvidenceAssetV1(ref activities.ArtifactRef) qualitygate.EvidenceAssetV1 {
	return qualitygate.EvidenceAssetV1{Ref: "cas://" + ref.Bucket + "/" + ref.Key, SHA256: ref.SHA256}
}

func problemQualityReviewerPassV1(assessment qualitygate.ReviewAssessmentV1) bool {
	if assessment.Dimensions == nil || len(assessment.Blockers) != 0 {
		return false
	}
	dimensions := []*qualitygate.ReviewDimensionV1{assessment.Dimensions.Clarity, assessment.Dimensions.Correctness, assessment.Dimensions.TestCoverage, assessment.Dimensions.DifficultyCalibration, assessment.Dimensions.TagAccuracy}
	for _, dimension := range dimensions {
		if dimension == nil || dimension.Score == nil || *dimension.Score < qualitygate.ReviewPassingScoreV1 {
			return false
		}
	}
	return true
}

func firstProblemQualityFailedGateV1(audit qualitygate.AuditV1) string {
	for _, gate := range audit.GateResults {
		if gate.Status != qualitygate.GateStatusPass {
			return gate.Gate
		}
	}
	return ""
}

func addProblemQualityAssetV1(target map[string]problemQualityRepairAssetV1, role string, ref activities.ArtifactRef) {
	target[problemQualityEvidenceAssetV1(ref).Ref] = problemQualityRepairAssetV1{role: role, ref: ref}
}

func buildProblemQualityRepairInputV1(parent string, round int, blockers []qualitygate.ReviewBlockerV1, assets map[string]problemQualityRepairAssetV1) (activities.S3RepairRevisionInputV1, bool) {
	if !authoringWorkflowIsSHA256V1(parent) || len(blockers) == 0 {
		return activities.S3RepairRevisionInputV1{}, false
	}
	codes := make([]string, 0, len(blockers))
	selected := map[string]problemQualityRepairAssetV1{}
	for _, blocker := range blockers {
		asset, exists := assets[blocker.ResponsibleAsset]
		// Until T34 freezes stage-specific patch semantics, R may only revise
		// the structured authoring source for semantic/spec blockers. Program,
		// statement, manifest, sanitizer, dedup, and hidden assets quarantine.
		if !exists || asset.role != "semantic_spec" {
			return activities.S3RepairRevisionInputV1{}, false
		}
		codes = append(codes, blocker.Code)
		selected[blocker.ResponsibleAsset] = asset
	}
	sort.Strings(codes)
	compact := codes[:0]
	for _, code := range codes {
		if len(compact) == 0 || compact[len(compact)-1] != code {
			compact = append(compact, code)
		}
	}
	refs := make([]string, 0, len(selected))
	for ref := range selected {
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	responsible := make([]activities.RepairResponsibleAssetV1, 0, len(refs))
	for _, ref := range refs {
		asset := selected[ref]
		responsible = append(responsible, activities.RepairResponsibleAssetV1{Role: asset.role, Artifact: asset.ref})
	}
	return activities.S3RepairRevisionInputV1{PayloadVersion: activities.S3QualityPayloadVersionV1, ParentRevisionSHA256: parent, Round: round, BlockerCodes: append([]string(nil), compact...), Blockers: append([]qualitygate.ReviewBlockerV1(nil), blockers...), ResponsibleAssets: responsible}, true
}
