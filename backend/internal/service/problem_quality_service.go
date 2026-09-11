package service

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/qualityapi"
	speccontract "github.com/Gingoo-TvT/Qraft/backend/internal/spec"
	"github.com/Gingoo-TvT/Qraft/backend/internal/workflow/activities"
	"github.com/google/uuid"
)

type QualityProblemSource interface {
	GetProblem(context.Context, uuid.UUID) (*domain.Problem, error)
	GetTestCases(context.Context, uuid.UUID) ([]*domain.TestCase, error)
}

type ProblemQualityService struct {
	problems QualityProblemSource
	objects  HydroObjectReader
}

type ProblemQualityDocumentV1 struct {
	Report qualityapi.ProblemQualityReportV1
	Bytes  []byte
	SHA256 string
}

type ProblemQualityAuditDocumentV1 struct {
	Bytes  []byte
	SHA256 string
}

type ProblemQualityBatchDocumentV1 struct {
	Manifest qualityapi.ProblemQualityBatchManifestV1
	Bytes    []byte
	SHA256   string
}

func NewProblemQualityService(problems QualityProblemSource, objects HydroObjectReader) *ProblemQualityService {
	return &ProblemQualityService{problems: problems, objects: objects}
}

// GetProblemQuality reopens every S3 artifact through the same strong binding
// used by product exports, then emits a deterministic projection. The report
// is not a new evidence artifact: its SHA-256 is a reproducible response
// identity, while the audit and TestManifest remain the authoritative CAS
// evidence.
func (s *ProblemQualityService) GetProblemQuality(ctx context.Context, problemID uuid.UUID) (*ProblemQualityDocumentV1, error) {
	problem, binding, err := s.loadBinding(ctx, problemID)
	if err != nil {
		return nil, err
	}
	report, err := buildProblemQualityReportV1(problem, binding)
	if err != nil {
		return nil, fmt.Errorf("%w: V1 quality projection: %v", ErrConflict, err)
	}
	canonical, digest, err := qualityapi.CanonicalProblemQualityReportV1(report)
	if err != nil {
		return nil, fmt.Errorf("%w: V1 quality report is not canonical: %v", ErrConflict, err)
	}
	return &ProblemQualityDocumentV1{Report: report, Bytes: canonical, SHA256: digest}, nil
}

// GetProblemQualityAudit returns the exact canonical audit bytes after the
// entire problem/testcase/CAS binding has been revalidated. It never repairs,
// reformats, or substitutes persisted bytes.
func (s *ProblemQualityService) GetProblemQualityAudit(ctx context.Context, problemID uuid.UUID) (*ProblemQualityAuditDocumentV1, error) {
	_, binding, err := s.loadBinding(ctx, problemID)
	if err != nil {
		return nil, err
	}
	return &ProblemQualityAuditDocumentV1{
		Bytes:  append([]byte(nil), binding.QualityAuditBytes...),
		SHA256: binding.QualityAuditRef.SHA256,
	}, nil
}

func (s *ProblemQualityService) BuildProblemQualityBatchManifest(
	ctx context.Context,
	request qualityapi.ProblemQualityBatchRequestV1,
) (*ProblemQualityBatchDocumentV1, error) {
	_, requestSHA, err := qualityapi.CanonicalProblemQualityBatchRequestV1(request)
	if err != nil {
		return nil, fmt.Errorf("validation: %w", err)
	}
	items := make([]qualityapi.ProblemQualityBatchItemV1, len(request.ProblemIDs))
	for index, rawID := range request.ProblemIDs {
		problemID, parseErr := uuid.Parse(rawID)
		if parseErr != nil {
			return nil, fmt.Errorf("validation: problem_ids[%d] is invalid", index)
		}
		document, getErr := s.GetProblemQuality(ctx, problemID)
		if getErr != nil {
			return nil, getErr
		}
		prefix := "/api/v1/problems/" + rawID
		items[index] = qualityapi.ProblemQualityBatchItemV1{
			ProblemID:           rawID,
			QualityReportSHA256: document.SHA256,
			AuditSHA256:         document.Report.Evidence.Audit.SHA256,
			TestManifestSHA256:  document.Report.Evidence.TestManifest.SHA256,
			QualityURI:          prefix + "/quality",
			AuditURI:            prefix + "/quality/audit",
			TestManifestURI:     prefix + "/test-manifest",
		}
	}
	manifest := qualityapi.ProblemQualityBatchManifestV1{
		SchemaVersion:            qualityapi.ProblemQualityBatchManifestSchemaV1,
		RequestSchemaVersion:     qualityapi.ProblemQualityBatchRequestSchemaV1,
		RequestSHA256:            requestSHA,
		Items:                    items,
		ExternalOJImportVerified: false,
	}
	canonical, digest, err := qualityapi.CanonicalProblemQualityBatchManifestV1(manifest)
	if err != nil {
		return nil, fmt.Errorf("%w: V1 quality batch manifest is not canonical: %v", ErrConflict, err)
	}
	return &ProblemQualityBatchDocumentV1{Manifest: manifest, Bytes: canonical, SHA256: digest}, nil
}

func (s *ProblemQualityService) loadBinding(
	ctx context.Context,
	problemID uuid.UUID,
) (*domain.Problem, *s3ExportBindingV1, error) {
	if s == nil || s.problems == nil || s.objects == nil {
		return nil, nil, fmt.Errorf("quality service dependencies are not configured")
	}
	problem, err := s.problems.GetProblem(ctx, problemID)
	if err != nil {
		return nil, nil, err
	}
	if problem == nil || problem.ID != problemID {
		return nil, nil, fmt.Errorf("%w: quality source returned the wrong problem", ErrConflict)
	}
	testCases, err := s.problems.GetTestCases(ctx, problemID)
	if err != nil {
		return nil, nil, err
	}
	binding, err := loadS3ExportBindingV1(ctx, problem, testCases, s.objects)
	if err != nil {
		return nil, nil, err
	}
	authoringBytes, err := readS3ExportArtifactV1(ctx, s.objects, binding.AuthoringBundleRef)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: V1 quality authoring bundle: %v", ErrConflict, err)
	}
	var authoring activities.AuthoringPlanBundleV1
	if err := decodeCanonicalS3ExportArtifactJSONV1(authoringBytes, &authoring); err != nil {
		return nil, nil, fmt.Errorf("%w: V1 quality authoring bundle is not canonical: %v", ErrConflict, err)
	}
	binding.AuthoringBundleV1 = &authoring
	return problem, binding, nil
}

func buildProblemQualityReportV1(problem *domain.Problem, binding *s3ExportBindingV1) (qualityapi.ProblemQualityReportV1, error) {
	if problem == nil || binding == nil || binding.TestManifestV2 == nil || binding.StatementDraftV1 == nil || binding.AuthoringBundleV1 == nil {
		return qualityapi.ProblemQualityReportV1{}, fmt.Errorf("strong binding projection is incomplete")
	}
	draft := binding.StatementDraftV1
	manifest := binding.TestManifestV2
	authoring := binding.AuthoringBundleV1
	if authoring.SchemaVersion != activities.AuthoringPlanBundleSchemaV1 ||
		authoring.ModelDecision != activities.AuthoringPlanDecisionAccepted || authoring.Decision != activities.AuthoringPlanDecisionAccepted ||
		authoring.GateReasonCode != "" || authoring.RejectionReason != "" || authoring.SemanticSpec == nil || authoring.AuthoringPlan == nil {
		return qualityapi.ProblemQualityReportV1{}, fmt.Errorf("authoring bundle is not an accepted typed contract")
	}
	semanticSHA := speccontract.SemanticSpecSHA256V1(*authoring.SemanticSpec)
	planBytes, err := json.Marshal(authoring.AuthoringPlan)
	if err != nil {
		return qualityapi.ProblemQualityReportV1{}, fmt.Errorf("encode authoring plan: %w", err)
	}
	planSHA := qualityapi.SHA256Hex(planBytes)
	if semanticSHA != manifest.SemanticSpecSHA256 || authoring.AuthoringPlan.SemanticSpecSHA256 != semanticSHA || planSHA != manifest.AuthoringPlanSHA256 {
		return qualityapi.ProblemQualityReportV1{}, fmt.Errorf("authoring concept/difficulty contract is not bound to TestManifest v2")
	}
	if authoring.AuthoringPlan.TargetDifficulty != problem.Difficulty {
		return qualityapi.ProblemQualityReportV1{}, fmt.Errorf("persisted generation target drifted from the CAS-bound authoring plan")
	}
	if draft.FactManifest.SchemaVersion != activities.StatementFactManifestSchemaV1 {
		return qualityapi.ProblemQualityReportV1{}, fmt.Errorf("statement fact manifest schema is invalid")
	}
	factBytes, err := json.Marshal(draft.FactManifest)
	if err != nil {
		return qualityapi.ProblemQualityReportV1{}, fmt.Errorf("encode statement fact manifest: %w", err)
	}
	factSHA := qualityapi.SHA256Hex(factBytes)
	if draft.FactManifestSHA256 != factSHA || draft.SemanticSpecSHA256 != manifest.SemanticSpecSHA256 {
		return qualityapi.ProblemQualityReportV1{}, fmt.Errorf("statement fact manifest identity is not bound to TestManifest v2")
	}
	concepts := make([]string, len(authoring.AuthoringPlan.ConceptRoles))
	for index, role := range authoring.AuthoringPlan.ConceptRoles {
		concepts[index] = role.Slug
	}
	conceptSignature, err := qualityapi.NewConceptSignatureV1(concepts)
	if err != nil {
		return qualityapi.ProblemQualityReportV1{}, err
	}
	persistedConceptSignature, err := qualityapi.NewConceptSignatureV1(problem.Tags)
	if err != nil || persistedConceptSignature.SHA256 != conceptSignature.SHA256 {
		return qualityapi.ProblemQualityReportV1{}, fmt.Errorf("persisted problem tags drifted from the CAS-bound authoring concepts")
	}
	structuralSignature, err := qualityapi.NewStructuralSignatureV1(
		manifest.SemanticSpecSHA256,
		manifest.AuthoringPlanSHA256,
		factSHA,
	)
	if err != nil {
		return qualityapi.ProblemQualityReportV1{}, err
	}

	gates := make([]qualityapi.QualityGateV1, len(binding.QualityAuditV1.GateResults))
	for index, gate := range binding.QualityAuditV1.GateResults {
		gates[index] = qualityapi.QualityGateV1{Name: gate.Gate, Status: gate.Status}
	}
	publicationStatus := qualityapi.LayerStatusPendingExplicitApproval
	publicationBasis := "quality_passed_draft_requires_explicit_public_release_approval"
	if problem.Status == domain.ProblemStatusPublished {
		publicationStatus = qualityapi.LayerStatusReady
		publicationBasis = "published_state_revalidated_against_fresh_s3_binding"
	}
	problemID := problem.ID.String()
	return qualityapi.ProblemQualityReportV1{
		SchemaVersion:             qualityapi.ProblemQualityReportSchemaV1,
		ProblemID:                 problemID,
		WorkflowID:                *problem.WorkflowID,
		SubjectRevision:           binding.SubjectRevision,
		GenerationEvidenceProfile: binding.EvidenceLevel,
		Decision:                  binding.QualityAuditV1.Decision,
		Evidence: qualityapi.QualityEvidenceV1{
			Audit: qualityapi.EvidenceBindingV1{
				Kind: "quality_audit", SchemaVersion: binding.QualityAuditV1.SchemaVersion,
				SHA256: binding.QualityAuditRef.SHA256, SizeBytes: binding.QualityAuditRef.SizeBytes,
				Producer: binding.QualityAuditRef.Producer, URI: "/api/v1/problems/" + problemID + "/quality/audit",
			},
			TestManifest: qualityapi.EvidenceBindingV1{
				Kind: "test_manifest", SchemaVersion: manifest.SchemaVersion,
				SHA256: binding.TestManifestRef.SHA256, SizeBytes: binding.TestManifestRef.SizeBytes,
				Producer: binding.TestManifestRef.Producer, URI: "/api/v1/problems/" + problemID + "/test-manifest",
			},
		},
		Gates: gates,
		Layers: []qualityapi.QualityLayerV1{
			{Name: qualityapi.LayerStandard, Status: qualityapi.LayerStatusPass, Basis: "canonical_nine_gate_audit_pass", EvidenceSHA256: binding.QualityAuditRef.SHA256},
			{Name: qualityapi.LayerVerified, Status: qualityapi.LayerStatusPass, Basis: "cas_and_persisted_testcase_binding_revalidated", EvidenceSHA256: binding.TestManifestRef.SHA256},
			{Name: qualityapi.LayerPublicationReady, Status: publicationStatus, Basis: publicationBasis, EvidenceSHA256: binding.SubjectRevision},
		},
		ConceptSignature:    conceptSignature,
		StructuralSignature: structuralSignature,
		Difficulty: qualityapi.DifficultyAdvisoryV1{
			GenerationTarget: authoring.AuthoringPlan.TargetDifficulty,
			Calibrated:       false,
			Source:           "cas_bound_authoring_plan_generation_target_advisory",
		},
		Cost: qualityapi.CostStatusV1{
			Status: qualityapi.CostStatusUnavailable,
			Source: "not_recomputable_from_persisted_s3_evidence",
		},
		ExternalOJ: qualityapi.ExternalOJBoundaryV1{
			ImportVerified: false,
			Source:         "outside_algoforge_quality_evidence",
		},
	}, nil
}
