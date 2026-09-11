package activities

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/generationapi"
	qualitygate "github.com/Gingoo-TvT/Qraft/backend/internal/qualitygate/v1"
	"github.com/google/uuid"
)

const (
	StoreS3QualityDraftPayloadVersionV1 = 1
	S3QualityDraftEvidenceSchemaV1      = generationapi.S3QualityMaterializationSchemaV1
	maxS3QualityDraftSourceBytesV1      = 2 << 20
)

// S3QualityPassDraftEvidenceV1 is persisted in problem metadata. Every
// ArtifactRef remains complete so Hydro/Excel exporters can resolve the exact
// nine-gate source artifacts rather than guessing from a legacy v1 manifest.
type S3QualityPassDraftEvidenceV1 struct {
	SchemaVersion           string      `json:"schema_version"`
	Decision                string      `json:"decision"`
	SubjectRevision         string      `json:"subject_revision"`
	EvidenceLevel           string      `json:"evidence_level"`
	AuditArtifact           ArtifactRef `json:"audit_artifact"`
	AuthoringBundleArtifact ArtifactRef `json:"authoring_bundle_artifact"`
	StatementDraftArtifact  ArtifactRef `json:"statement_draft_artifact"`
	FinalStatementArtifact  ArtifactRef `json:"final_statement_artifact"`
	MainProgramArtifact     ArtifactRef `json:"main_program_artifact"`
	OracleProgramArtifact   ArtifactRef `json:"oracle_program_artifact"`
	OracleReceiptArtifact   ArtifactRef `json:"oracle_receipt_artifact"`
	TestManifestArtifact    ArtifactRef `json:"test_manifest_artifact"`
}

type StoreS3QualityDraftInputV1 struct {
	PayloadVersion          int                     `json:"payload_version"`
	WorkflowID              string                  `json:"workflow_id"`
	SubjectRevision         string                  `json:"subject_revision"`
	FrozenConcept           string                  `json:"frozen_concept"`
	Language                string                  `json:"language"`
	EvidenceLevel           string                  `json:"evidence_level"`
	Params                  domain.ProblemGenParams `json:"params"`
	AuthoringBundleArtifact ArtifactRef             `json:"authoring_bundle_artifact"`
	StatementDraftArtifact  ArtifactRef             `json:"statement_draft_artifact"`
	FinalStatementArtifact  ArtifactRef             `json:"final_statement_artifact"`
	MainProgramArtifact     ArtifactRef             `json:"main_program_artifact"`
	OracleProgramArtifact   ArtifactRef             `json:"oracle_program_artifact"`
	OracleReceiptArtifact   ArtifactRef             `json:"oracle_receipt_artifact"`
	TestManifestArtifact    ArtifactRef             `json:"test_manifest_artifact"`
	AuditArtifact           ArtifactRef             `json:"audit_artifact"`
}

type StoreS3QualityDraftResultV1 struct {
	PayloadVersion     int                  `json:"payload_version"`
	ProblemID          string               `json:"problem_id"`
	Status             domain.ProblemStatus `json:"status"`
	AuditSHA256        string               `json:"audit_sha256"`
	TestManifestSHA256 string               `json:"test_manifest_sha256"`
}

// StoreS3QualityDraftActivityV1 re-opens every materialization-critical CAS
// object and proves their ancestry before delegating to the idempotent Store
// implementation. The v6 Store path persists an editable draft and explicitly
// skips the obsolete publication gate.
func (a *Activities) StoreS3QualityDraftActivityV1(ctx context.Context, in StoreS3QualityDraftInputV1) (*StoreS3QualityDraftResultV1, error) {
	storeInput, err := a.buildS3QualityDraftStoreInputV1(ctx, in)
	if err != nil {
		return nil, s3ContractErrorV1("build quality draft materialization: %v", err)
	}
	stored, err := a.StoreProblemActivity(ctx, *storeInput)
	if err != nil {
		return nil, err
	}
	if stored == nil || stored.ProblemID == uuid.Nil || stored.Status != domain.ProblemStatusDraft {
		return nil, fmt.Errorf("quality draft Store returned an invalid result")
	}
	return &StoreS3QualityDraftResultV1{
		PayloadVersion: StoreS3QualityDraftPayloadVersionV1,
		ProblemID:      stored.ProblemID.String(), Status: stored.Status,
		AuditSHA256:        in.AuditArtifact.SHA256,
		TestManifestSHA256: in.TestManifestArtifact.SHA256,
	}, nil
}

func (a *Activities) buildS3QualityDraftStoreInputV1(ctx context.Context, in StoreS3QualityDraftInputV1) (*StoreInput, error) {
	if a == nil || a.artifacts == nil {
		return nil, fmt.Errorf("artifact store is not configured")
	}
	if in.PayloadVersion != StoreS3QualityDraftPayloadVersionV1 || !generationapi.IsJobID(in.WorkflowID) ||
		!isManifestSHA256(in.SubjectRevision) || strings.TrimSpace(in.FrozenConcept) == "" ||
		(in.EvidenceLevel != generationapi.EvidenceMinimal && in.EvidenceLevel != generationapi.EvidenceStandard && in.EvidenceLevel != generationapi.EvidenceAudit) {
		return nil, fmt.Errorf("invalid quality draft identity")
	}
	if err := in.Params.Validate(); err != nil {
		return nil, err
	}
	requestedEvidenceLevel, err := generationapi.QualityEvidenceLevelFromParams(in.Params)
	if err != nil || requestedEvidenceLevel != in.EvidenceLevel {
		return nil, fmt.Errorf("quality draft evidence level does not match server-authored parameters")
	}
	if len(in.Params.Languages) != 1 || in.Params.Languages[0] != in.Language {
		return nil, fmt.Errorf("quality draft language is not the single product language")
	}

	refs := []struct {
		name     string
		producer string
		ref      ArtifactRef
	}{
		{"authoring bundle", "GenerateAuthoringPlanActivity", in.AuthoringBundleArtifact},
		{"statement draft", "RenderStatementFromAuthoringBundleActivityV1", in.StatementDraftArtifact},
		{"final statement", "FinalizeAuthoringStatementSamplesActivityV1", in.FinalStatementArtifact},
		{"main program", "GenerateMainSolutionActivityV1", in.MainProgramArtifact},
		{"oracle program", "GenerateOracleCandidateActivityV1", in.OracleProgramArtifact},
		{"oracle receipt", VerifiedProgramReceiptProducerV1, in.OracleReceiptArtifact},
		{"test manifest", "BuildS3TestManifestActivityV1", in.TestManifestArtifact},
		{"quality audit", "RecomputeS3VerdictActivityV1", in.AuditArtifact},
	}
	for _, item := range refs {
		if err := validateS3QualityDraftArtifactRefV1(item.name, item.producer, in.WorkflowID, item.ref); err != nil {
			return nil, err
		}
	}

	auditBytes, err := a.artifacts.Get(ctx, in.AuditArtifact)
	if err != nil {
		return nil, fmt.Errorf("read quality audit: %w", err)
	}
	var audit qualitygate.AuditV1
	if err := decodeCanonicalSampleClosureJSONV1(auditBytes, &audit); err != nil {
		return nil, fmt.Errorf("decode canonical quality audit: %w", err)
	}
	canonicalAudit, auditSHA, err := qualitygate.CanonicalAuditV1(audit)
	if err != nil || !bytes.Equal(canonicalAudit, auditBytes) || auditSHA != in.AuditArtifact.SHA256 {
		return nil, fmt.Errorf("quality audit CAS is not canonical")
	}
	if err := validateS3QualityPassAuditV1(audit, in.WorkflowID, in.SubjectRevision); err != nil {
		return nil, err
	}

	draftBytes, err := a.artifacts.Get(ctx, in.StatementDraftArtifact)
	if err != nil {
		return nil, fmt.Errorf("read statement draft: %w", err)
	}
	var draft StatementDraftBundleV1
	if err := decodeCanonicalSampleClosureJSONV1(draftBytes, &draft); err != nil {
		return nil, fmt.Errorf("decode statement draft: %w", err)
	}
	finalBytes, err := a.artifacts.Get(ctx, in.FinalStatementArtifact)
	if err != nil {
		return nil, fmt.Errorf("read final statement: %w", err)
	}
	var final FinalAuthoringStatementSamplesBundleV1
	if err := decodeCanonicalSampleClosureJSONV1(finalBytes, &final); err != nil {
		return nil, fmt.Errorf("decode final statement: %w", err)
	}
	manifestBytes, err := a.artifacts.Get(ctx, in.TestManifestArtifact)
	if err != nil {
		return nil, fmt.Errorf("read TestManifest v2: %w", err)
	}
	manifest, err := ParseTestManifestV2JSON(manifestBytes)
	if err != nil {
		return nil, fmt.Errorf("decode TestManifest v2: %w", err)
	}
	oracleReceiptBytes, err := a.artifacts.Get(ctx, in.OracleReceiptArtifact)
	if err != nil {
		return nil, fmt.Errorf("read oracle receipt: %w", err)
	}
	var oracleReceipt S3OracleGateReceiptV1
	if err := decodeCanonicalSampleClosureJSONV1(oracleReceiptBytes, &oracleReceipt); err != nil {
		return nil, fmt.Errorf("decode oracle receipt: %w", err)
	}
	mainSource, err := a.artifacts.Get(ctx, in.MainProgramArtifact)
	if err != nil {
		return nil, fmt.Errorf("read main program: %w", err)
	}
	oracleSource, err := a.artifacts.Get(ctx, in.OracleProgramArtifact)
	if err != nil {
		return nil, fmt.Errorf("read oracle program: %w", err)
	}
	if len(mainSource) == 0 || len(mainSource) > maxS3QualityDraftSourceBytesV1 || len(oracleSource) == 0 || len(oracleSource) > maxS3QualityDraftSourceBytesV1 {
		return nil, fmt.Errorf("quality draft program source is empty or too large")
	}
	if draft.AuthoringBundleSHA256 != in.AuthoringBundleArtifact.SHA256 ||
		final.AuthoringBundleSHA256 != in.AuthoringBundleArtifact.SHA256 ||
		final.StatementDraftSHA256 != in.StatementDraftArtifact.SHA256 ||
		final.ProgramSHA256 != in.MainProgramArtifact.SHA256 ||
		final.MarkdownSHA256 != sha256Hex([]byte(final.Markdown)) ||
		in.SubjectRevision != in.FinalStatementArtifact.SHA256 ||
		oracleReceipt.Status != qualitygate.GateStatusPass || oracleReceipt.Promotion == nil || !oracleReceipt.Promotion.Promoted ||
		oracleReceipt.CandidateSHA256 != in.MainProgramArtifact.SHA256 ||
		oracleReceipt.Promotion.CandidateSourceSHA256 != in.OracleProgramArtifact.SHA256 ||
		manifest.OraclePromotionReceiptSHA256 != in.OracleReceiptArtifact.SHA256 ||
		manifest.SemanticSpecSHA256 != final.SemanticSpecSHA256 ||
		oracleReceipt.SemanticSpecSHA256 != final.SemanticSpecSHA256 {
		return nil, fmt.Errorf("quality draft artifact ancestry is inconsistent")
	}

	mainSolution := domain.Solution{SolutionType: domain.SolutionTypeMain, Language: in.Language, SourceCode: string(mainSource)}
	oracleSolution := domain.Solution{SolutionType: domain.SolutionTypeBrute, Language: in.Language, SourceCode: string(oracleSource)}
	testCases, outputArtifacts := s3QualityDraftTestCasesV1(manifest)
	sandboxOutput := SandboxResult{
		PayloadVersion:  ActivityPayloadVersion,
		OutputArtifacts: outputArtifacts,
		TimeTaken:       make([]time.Duration, len(outputArtifacts)),
		MemoryUsed:      make([]int64, len(outputArtifacts)),
		Audit:           oracleReceipt.SandboxIdentity,
	}

	qualityEvidence := &S3QualityPassDraftEvidenceV1{
		SchemaVersion: S3QualityDraftEvidenceSchemaV1, Decision: qualitygate.DecisionPass,
		SubjectRevision: in.SubjectRevision, EvidenceLevel: in.EvidenceLevel,
		AuditArtifact: in.AuditArtifact, AuthoringBundleArtifact: in.AuthoringBundleArtifact,
		StatementDraftArtifact: in.StatementDraftArtifact, FinalStatementArtifact: in.FinalStatementArtifact,
		MainProgramArtifact: in.MainProgramArtifact, OracleProgramArtifact: in.OracleProgramArtifact,
		OracleReceiptArtifact: in.OracleReceiptArtifact, TestManifestArtifact: in.TestManifestArtifact,
	}
	storeParams := s3QualityDraftStoreParamsV1(in.Params, in.EvidenceLevel)
	return &StoreInput{
		PayloadVersion: StoreProblemS3QualityDraftPayloadVersion,
		IdempotencyKey: in.WorkflowID + "/store-s3-quality-draft/v1",
		WorkflowID:     in.WorkflowID,
		SourceArtifacts: []*ArtifactRef{
			&in.AuthoringBundleArtifact, &in.StatementDraftArtifact, &in.FinalStatementArtifact,
			&in.MainProgramArtifact, &in.OracleProgramArtifact, &in.OracleReceiptArtifact,
			&in.TestManifestArtifact, &in.AuditArtifact,
		},
		Statement: StatementResult{
			Title: draft.Title, Statement: final.Markdown,
			Tags: append([]string(nil), storeParams.Tags...), OneLineHint: in.FrozenConcept,
			DifficultyJustification: "accepted by AlgoForge S3 nine-gate quality workflow",
		},
		Solutions: SolutionResult{MainSolution: mainSolution, BruteSolution: oracleSolution},
		TestCases: testCases, SandboxOutput: sandboxOutput, Params: storeParams,
		TestManifestV2: manifest, QualityPassDraft: qualityEvidence,
	}, nil
}

// TestManifest v2 is the sole scoring source for S3 drafts. Legacy Store
// group/score fields stay zero so downstream exporters cannot accidentally
// treat the old one-group/100-point defaults as publication semantics.
func s3QualityDraftTestCasesV1(manifest *TestManifestV2) ([]TestCaseData, []*ArtifactRef) {
	testCases := make([]TestCaseData, len(manifest.Cases))
	outputArtifacts := make([]*ArtifactRef, len(manifest.Cases))
	for index, item := range manifest.Cases {
		inputRef := *item.InputArtifact
		outputRef := *item.OutputArtifact
		testCases[index] = TestCaseData{
			InputArtifact: &inputRef, GroupID: 0,
			IsSample:    item.Purpose == TestManifestPurposeSample,
			Description: item.Purpose + ":" + item.ConstraintRegion,
			Origin:      TestCaseOriginCustom,
		}
		outputArtifacts[index] = &outputRef
	}
	return testCases, outputArtifacts
}

func validateS3QualityDraftArtifactRefV1(name, producer, workflowID string, ref ArtifactRef) error {
	if err := ref.Validate(ref.Bucket); err != nil {
		return fmt.Errorf("%s artifact is invalid: %w", name, err)
	}
	if ref.Producer != producer || ref.WorkflowID != workflowID {
		return fmt.Errorf("%s artifact producer/workflow binding is invalid", name)
	}
	return nil
}

func validateS3QualityPassAuditV1(audit qualitygate.AuditV1, workflowID, revision string) error {
	want := []string{
		qualitygate.GateSpecLint, qualitygate.GateSampleOutputBinding,
		qualitygate.GateOracleDifferential, qualitygate.GateSanitizer,
		qualitygate.GateBoundaryCoverage, qualitygate.GateTestManifest,
		qualitygate.GateReviewerSchemaVerdict, qualitygate.GateDedup,
		qualitygate.GateHiddenRegression,
	}
	if audit.SubjectID != workflowID || audit.SubjectRevision != revision ||
		audit.Decision != qualitygate.DecisionPass || !audit.DeterministicGatesPassed ||
		len(audit.BlockingIssues) != 0 || len(audit.GateResults) != len(want) {
		return fmt.Errorf("quality audit is not a complete nine-gate PASS")
	}
	for index, gate := range audit.GateResults {
		if gate.Gate != want[index] || gate.Status != qualitygate.GateStatusPass {
			return fmt.Errorf("quality audit gate %d is not the canonical PASS", index)
		}
	}
	return nil
}

func (a *Activities) validateS3QualityPassDraftStoreInputV1(ctx context.Context, input StoreInput) error {
	if input.PayloadVersion != StoreProblemS3QualityDraftPayloadVersion {
		if input.TestManifestV2 != nil || input.QualityPassDraft != nil {
			return fmt.Errorf("S3 quality draft evidence requires Store payload version %d", StoreProblemS3QualityDraftPayloadVersion)
		}
		return nil
	}
	evidence := input.QualityPassDraft
	if a == nil || a.artifacts == nil || evidence == nil || input.TestManifestV2 == nil || input.TestManifest != nil || input.ReviewQuarantine != nil || input.Params.GenerationEvidence != nil {
		return fmt.Errorf("S3 quality draft Store contract is incomplete or mixed with legacy evidence")
	}
	if evidence.SchemaVersion != S3QualityDraftEvidenceSchemaV1 || evidence.Decision != qualitygate.DecisionPass ||
		evidence.SubjectRevision != evidence.FinalStatementArtifact.SHA256 ||
		(evidence.EvidenceLevel != generationapi.EvidenceMinimal && evidence.EvidenceLevel != generationapi.EvidenceStandard && evidence.EvidenceLevel != generationapi.EvidenceAudit) ||
		!generationapi.IsJobID(input.WorkflowID) {
		return fmt.Errorf("S3 quality draft evidence identity is invalid")
	}
	requestedEvidenceLevel, err := generationapi.QualityEvidenceLevelFromParams(input.Params)
	if err != nil || requestedEvidenceLevel != evidence.EvidenceLevel {
		return fmt.Errorf("S3 quality draft evidence level binding is invalid")
	}
	refs := []struct {
		name     string
		producer string
		ref      ArtifactRef
	}{
		{"authoring bundle", "GenerateAuthoringPlanActivity", evidence.AuthoringBundleArtifact},
		{"statement draft", "RenderStatementFromAuthoringBundleActivityV1", evidence.StatementDraftArtifact},
		{"final statement", "FinalizeAuthoringStatementSamplesActivityV1", evidence.FinalStatementArtifact},
		{"main program", "GenerateMainSolutionActivityV1", evidence.MainProgramArtifact},
		{"oracle program", "GenerateOracleCandidateActivityV1", evidence.OracleProgramArtifact},
		{"oracle receipt", VerifiedProgramReceiptProducerV1, evidence.OracleReceiptArtifact},
		{"test manifest", "BuildS3TestManifestActivityV1", evidence.TestManifestArtifact},
		{"quality audit", "RecomputeS3VerdictActivityV1", evidence.AuditArtifact},
	}
	for _, item := range refs {
		if err := validateS3QualityDraftArtifactRefV1(item.name, item.producer, input.WorkflowID, item.ref); err != nil {
			return err
		}
	}
	manifestBytes, manifestSHA, err := CanonicalTestManifestV2JSON(*input.TestManifestV2)
	if err != nil || len(manifestBytes) == 0 || manifestSHA != evidence.TestManifestArtifact.SHA256 {
		return fmt.Errorf("S3 quality draft TestManifest v2 binding is invalid")
	}
	auditBytes, err := a.artifacts.Get(ctx, evidence.AuditArtifact)
	if err != nil {
		return fmt.Errorf("read S3 quality draft audit: %w", err)
	}
	var audit qualitygate.AuditV1
	if err := decodeCanonicalSampleClosureJSONV1(auditBytes, &audit); err != nil {
		return fmt.Errorf("decode S3 quality draft audit: %w", err)
	}
	canonicalAudit, auditSHA, err := qualitygate.CanonicalAuditV1(audit)
	if err != nil || !bytes.Equal(canonicalAudit, auditBytes) || auditSHA != evidence.AuditArtifact.SHA256 {
		return fmt.Errorf("S3 quality draft audit binding is invalid")
	}
	if err := validateS3QualityPassAuditV1(audit, input.WorkflowID, evidence.SubjectRevision); err != nil {
		return err
	}
	for _, item := range refs {
		found := false
		for _, source := range input.SourceArtifacts {
			if source != nil && source.Equal(item.ref) {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("S3 quality draft source artifacts omit %s", item.name)
		}
	}
	return nil
}

func s3QualityDraftStoreParamsV1(params domain.ProblemGenParams, evidenceLevel string) domain.ProblemGenParams {
	result := params
	result.GenerationEvidence = nil
	result.MetadataExtras = cloneS3QualityMetadataMapV1(params.MetadataExtras)
	result.MetadataExtras[generationapi.QualityEvidenceLevelMetadataKey] = evidenceLevel
	audit, _ := result.MetadataExtras[generationapi.GenerationAuditMetadataKey].(map[string]interface{})
	audit = cloneS3QualityMetadataMapV1(audit)
	audit["qg02_plus_enabled"] = true
	audit["quality_workflow"] = generationapi.QualityWorkflowTypeV1
	audit["requested_evidence_level"] = evidenceLevel
	result.MetadataExtras[generationapi.GenerationAuditMetadataKey] = audit
	return result
}

func cloneS3QualityMetadataMapV1(source map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{}, len(source)+1)
	for key, value := range source {
		result[key] = value
	}
	return result
}
