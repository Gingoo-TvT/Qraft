package generationapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
)

const (
	JobContractVersion = "algoforge.generation-job.v1"
	JobIDPrefix        = "generation-job-v1-"
	MaxRequestBytes    = 64 << 10
	MaxWallTimeSeconds = 24 * 60 * 60

	MemoContractVersionKey        = "generation_job_contract_version"
	MemoPayloadSHA256Key          = "generation_job_payload_sha256"
	MemoPrincipalScopeKey         = "generation_job_principal_scope_sha256"
	MemoGenerationArmKey          = "generation_job_generation_arm"
	MemoReviewerProfileKey        = "generation_job_reviewer_profile"
	MemoEvidenceProfileKey        = "generation_job_evidence_profile"
	MemoOutcomeTaxonomyKey        = "generation_job_outcome_taxonomy"
	MemoReviewerDescriptorKey     = "generation_job_reviewer_profile_descriptor_sha256"
	MemoEvidenceDescriptorKey     = "generation_job_evidence_profile_descriptor_sha256"
	MemoKnowledgePointContractKey = "generation_job_knowledge_point_combination_schema"
	MemoEvidenceLevelKey          = "generation_job_evidence_level"

	StandardEvidenceWorkflowTypeV1 = "ProblemGenerationStandardEvidenceWorkflowV1"
	QualityWorkflowTypeV1          = "ProblemGenerationQualityWorkflowV1"

	GenerationAuditMetadataKey           = "generation_contract_audit"
	QualityEvidenceLevelMetadataKey      = "generation_quality_evidence_level"
	KnowledgePointCombinationMetadataKey = "knowledge_point_combination"
	S3QualityMaterializationMetadataKey  = "s3_quality_v1"
	S3QualityMaterializationSchemaV1     = "algoforge.s3-quality-draft-materialization.v1"
	GenerationAuditSchemaV1              = "algoforge.generation-contract-audit.v1"
	GenerationArmBaseline                = "A"
	GenerationArmModeBaseline            = "baseline_passthrough"
	ReviewerProfileV0                    = "algoforge.reviewer.v0"
	ReviewerProfileV1                    = "algoforge.reviewer.v1"
	EvidenceProfileV0                    = "algoforge.review-evidence.v0"
	OutcomeTaxonomyVersion               = "algoforge.generation-outcome-taxonomy.v1"
	// The reviewer prompt contract was rotated in August 2026 so the reviewer
	// receives only contestant-visible statement content for clarity/difficulty
	// judgments. Keep the prior digests below as accepted legacy identities so
	// already-issued standard receipts remain verifiable.
	ReviewerProfileDescriptorSHA256         = "1e67944bfa3d8f4fa7d1d745e2153c43226fa109e4a5f6c96ec016d151162468"
	ReviewerProfileV1DescriptorSHA256       = "1422b3da999b425e6093f1d2a5cd3be0335d8f700b8bf3d9bb41aa9531c082bd"
	ReviewerProfileLegacyDescriptorSHA256   = "d437fb973e27c680cc5f1fabced1243f055314e3c1f3cfd9adb2b7fecb1d1118"
	ReviewerProfileV1LegacyDescriptorSHA256 = "e3c6cb027744d3b9edcc49d87cfcd2f722e62bb9489fec6b4671d29c2f9e53ee"
	EvidenceProfileDescriptorSHA256         = "5c0ee4e19117c59e65aed3d81b2f7ea087b1727e3c9011f1ce3f7018aa78160b"
)

type ErrorCode string

const (
	ErrorBudgetExhausted       ErrorCode = "budget_exhausted"
	ErrorQualityNotMet         ErrorCode = "quality_not_met"
	ErrorUnsupportedConstraint ErrorCode = "unsupported_constraint"
	ErrorUnsatisfiableSpec     ErrorCode = "unsatisfiable_spec"
	ErrorInternal              ErrorCode = "internal_error"
	ErrorUnauthorized          ErrorCode = "unauthorized"
	ErrorIdempotencyConflict   ErrorCode = "idempotency_conflict"
	ErrorJobNotReady           ErrorCode = "job_not_ready"
	ErrorNotFound              ErrorCode = "not_found"
)

type ContractError struct {
	Code    ErrorCode
	Path    string
	Message string
}

func (e *ContractError) Error() string {
	if e == nil {
		return ""
	}
	if e.Path == "" {
		return e.Message
	}
	return fmt.Sprintf("%s: %s", e.Path, e.Message)
}

func contractError(code ErrorCode, path, message string) error {
	return &ContractError{Code: code, Path: path, Message: message}
}

func ErrorCodeOf(err error) ErrorCode {
	var typed *ContractError
	if errors.As(err, &typed) {
		return typed.Code
	}
	return ErrorUnsatisfiableSpec
}

// ValidateV1 validates only the capabilities that the first stable job API can
// actually honor. Unsupported controls are rejected instead of being ignored.
func (request Request) ValidateV1() error {
	request = request.Canonical()
	if request.SchemaVersion != RequestSchemaVersion {
		return contractError(ErrorUnsupportedConstraint, "schema_version", "unsupported request schema")
	}
	if request.CandidateCount != 1 {
		return contractError(ErrorUnsupportedConstraint, "candidate_count", "generation jobs v1 supports exactly one candidate")
	}
	if request.ProblemType != "" && request.ProblemType != ProblemTypeStandard {
		return contractError(ErrorUnsupportedConstraint, "problem_type", "generation jobs v1 supports only standard problems")
	}
	if len(request.Output.Formats) != 1 || request.Output.Formats[0] != "algoforge" {
		return contractError(ErrorUnsupportedConstraint, "output.formats", "generation jobs v1 supports exactly the algoforge output format")
	}
	if len(request.Languages) != 1 {
		return contractError(ErrorUnsupportedConstraint, "languages", "generation jobs v1 supports exactly one solution language")
	}
	if language := request.Languages[0]; language != "" && !isGenerationJobLanguage(language) {
		return contractError(ErrorUnsupportedConstraint, "languages", fmt.Sprintf("solution language %q is not supported", language))
	}
	if err := request.ValidatePreview(); err != nil {
		return contractError(ErrorUnsatisfiableSpec, "request", err.Error())
	}
	if request.Domain.Name != "competitive_programming" {
		return contractError(ErrorUnsupportedConstraint, "domain.name", "only competitive_programming is supported in v1")
	}
	level := domain.ProblemLevel(request.Domain.Level)
	if level != domain.LevelSyntax && level != domain.LevelAlgorithm {
		return contractError(ErrorUnsupportedConstraint, "domain.level", "v1 supports syntax or algorithm levels")
	}
	minDifficulty, maxDifficulty := domain.DifficultyRange(level)
	if request.Difficulty.Rating < minDifficulty || request.Difficulty.Rating > maxDifficulty {
		return contractError(
			ErrorUnsatisfiableSpec,
			"difficulty.rating",
			fmt.Sprintf("rating is outside level %s range [%d,%d]", level, minDifficulty, maxDifficulty),
		)
	}
	switch request.Domain.Combination.Mode {
	case domain.KnowledgePointCombinationSingle,
		domain.KnowledgePointCombinationSet,
		domain.KnowledgePointCombinationSequence,
		domain.KnowledgePointCombinationMixed:
	default:
		return contractError(ErrorUnsupportedConstraint, "domain.combination.mode", "unsupported knowledge-point combination mode")
	}
	if request.Difficulty.Tolerance != 0 {
		return contractError(ErrorUnsupportedConstraint, "difficulty.tolerance", "difficulty tolerance is not enforced in v1")
	}
	if request.Difficulty.CalibrationProfile != "" && request.Difficulty.CalibrationProfile != "default" {
		return contractError(ErrorUnsupportedConstraint, "difficulty.calibration_profile", "only the default calibration profile is available")
	}
	if request.Constraints.MaxInputBytes != 0 || request.Constraints.MaxOutputBytes != 0 {
		return contractError(ErrorUnsupportedConstraint, "constraints", "per-request byte limits are not available in v1")
	}
	if request.Quality.Strategy != "baseline" {
		return contractError(ErrorUnsupportedConstraint, "quality.strategy", "only baseline quality orchestration is available in v1")
	}
	if request.Quality.DedupMode != "reject" {
		return contractError(ErrorUnsupportedConstraint, "quality.dedup_mode", "v1 always fails closed on duplicate candidates")
	}
	if request.Quality.Budget.MaxLLMCalls != 0 || request.Quality.Budget.MaxTokens != 0 || request.Quality.Budget.MaxRegenerations != 0 {
		return contractError(ErrorUnsupportedConstraint, "quality.budget", "call, token, and regeneration budgets are not enforced in v1")
	}
	if request.Quality.Budget.MaxWallTimeSeconds < 60 || request.Quality.Budget.MaxWallTimeSeconds > MaxWallTimeSeconds {
		return contractError(ErrorUnsatisfiableSpec, "quality.budget.max_wall_time_seconds", fmt.Sprintf("wall-time budget must be in [60,%d]", MaxWallTimeSeconds))
	}
	if len(request.Quality.AuditProfiles) != 0 {
		return contractError(ErrorUnsupportedConstraint, "quality.audit_profiles", "audit profiles are not wired into generation jobs v1")
	}
	if request.RandomSeed != nil {
		return contractError(ErrorUnsupportedConstraint, "random_seed", "end-to-end deterministic seeds are not available in v1")
	}
	switch request.Output.EvidenceLevel {
	case EvidenceMinimal, EvidenceStandard, EvidenceAudit:
	default:
		return contractError(ErrorUnsupportedConstraint, "output.evidence_level", "unsupported evidence level")
	}
	if !request.Output.IncludeEditorial || !request.Output.IncludeSolutions || !request.Output.IncludeTestData {
		return contractError(ErrorUnsupportedConstraint, "output", "v1 always generates editorials, solutions, and test data")
	}
	if !isDefaultProfile(request.Runtime.StatementProfile, "default-statement") {
		return contractError(ErrorUnsupportedConstraint, "runtime.statement_profile", "only the server default statement profile is available")
	}
	if !isDefaultProfile(request.Runtime.VerificationProfile, "default-verification") {
		return contractError(ErrorUnsupportedConstraint, "runtime.verification_profile", "only the server default verification profile is available")
	}
	return nil
}

func isGenerationJobLanguage(value string) bool {
	switch value {
	case "c", "cpp", "python3", "java", "go":
		return true
	default:
		return false
	}
}

func isDefaultProfile(value, namedDefault string) bool {
	return value == "" || value == namedDefault || value == "default"
}

// Canonical normalizes semantically unordered fields before hashing them for
// idempotency binding. Sequence knowledge points retain their caller order.
func (request Request) Canonical() Request {
	request.SchemaVersion = strings.TrimSpace(request.SchemaVersion)
	request.Domain.Name = strings.ToLower(strings.TrimSpace(request.Domain.Name))
	request.Domain.Level = strings.ToLower(strings.TrimSpace(request.Domain.Level))
	request.Domain.Combination.Mode = strings.ToLower(strings.TrimSpace(request.Domain.Combination.Mode))
	request.Difficulty.CalibrationProfile = strings.ToLower(strings.TrimSpace(request.Difficulty.CalibrationProfile))
	request.ProblemType = strings.ToLower(strings.TrimSpace(request.ProblemType))
	request.Quality.Strategy = strings.ToLower(strings.TrimSpace(request.Quality.Strategy))
	request.Quality.DedupMode = strings.ToLower(strings.TrimSpace(request.Quality.DedupMode))
	request.Output.EvidenceLevel = strings.ToLower(strings.TrimSpace(request.Output.EvidenceLevel))
	request.Locale = strings.ToLower(strings.TrimSpace(request.Locale))
	request.ContestStyle = strings.ToLower(strings.TrimSpace(request.ContestStyle))
	request.CustomRequirements = strings.TrimSpace(request.CustomRequirements)
	request.Runtime.StatementProfile = strings.TrimSpace(request.Runtime.StatementProfile)
	request.Runtime.VerificationProfile = strings.TrimSpace(request.Runtime.VerificationProfile)
	request.Domain.KnowledgePoints = normalizedKnowledgePoints(request.Domain.KnowledgePoints, request.Domain.Combination.Mode)
	if request.Domain.Combination.MaxConcepts == 0 && len(request.Domain.KnowledgePoints) > 0 {
		request.Domain.Combination.MaxConcepts = len(request.Domain.KnowledgePoints)
	}
	request.Languages = normalizedStrings(request.Languages, true)
	request.Output.Formats = normalizedStrings(request.Output.Formats, true)
	request.Quality.AuditProfiles = normalizedStrings(request.Quality.AuditProfiles, true)
	if request.Constraints.TestCaseCount == 0 {
		if request.Constraints.TestCaseCountMin == 0 {
			request.Constraints.TestCaseCountMin = domain.MinAdaptiveTestCases
		}
		if request.Constraints.TestCaseCountMax == 0 {
			request.Constraints.TestCaseCountMax = domain.MaxAdaptiveTestCases
		}
	}
	for i := range request.Languages {
		request.Languages[i] = strings.ToLower(request.Languages[i])
	}
	for i := range request.Output.Formats {
		request.Output.Formats[i] = strings.ToLower(request.Output.Formats[i])
	}
	return request
}

func normalizedKnowledgePoints(values []string, mode string) []string {
	if values == nil {
		return nil
	}
	result := make([]string, len(values))
	for i, value := range values {
		result[i] = strings.ToLower(strings.TrimSpace(value))
	}
	switch mode {
	case domain.KnowledgePointCombinationSequence:
		// Caller order is the stage order.
	case domain.KnowledgePointCombinationMixed:
		// The first slug is primary; the auxiliary tail is an unordered set.
		if len(result) > 1 {
			sort.Strings(result[1:])
		}
	default:
		sort.Strings(result)
	}
	return result
}

func normalizedStrings(values []string, sortValues bool) []string {
	if values == nil {
		return nil
	}
	result := make([]string, len(values))
	for i, value := range values {
		result[i] = strings.TrimSpace(value)
	}
	if sortValues {
		sort.Strings(result)
	}
	return result
}

func (request Request) CanonicalPayload() ([]byte, string, error) {
	request = request.Canonical()
	if err := request.ValidateV1(); err != nil {
		return nil, "", err
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, "", fmt.Errorf("encode canonical generation request: %w", err)
	}
	digest := sha256.Sum256(payload)
	return payload, hex.EncodeToString(digest[:]), nil
}

func JobIDForIdempotencyKey(principalScope, key string) (string, string, error) {
	principalScope = strings.TrimSpace(principalScope)
	if principalScope == "" {
		return "", "", fmt.Errorf("principal scope is required")
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return "", "", fmt.Errorf("idempotency key is required")
	}
	if len(key) > 256 {
		return "", "", fmt.Errorf("idempotency key exceeds 256 bytes")
	}
	for _, r := range key {
		if r < 0x20 || r == 0x7f {
			return "", "", fmt.Errorf("idempotency key contains control characters")
		}
	}
	scopeDigest := sha256.Sum256([]byte(principalScope))
	jobDigest := sha256.Sum256([]byte(principalScope + "\x00" + key))
	return JobIDPrefix + hex.EncodeToString(jobDigest[:]), hex.EncodeToString(scopeDigest[:]), nil
}

func IsJobID(value string) bool {
	if !strings.HasPrefix(value, JobIDPrefix) {
		return false
	}
	digest := strings.TrimPrefix(value, JobIDPrefix)
	if len(digest) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(digest)
	return err == nil
}

func (request Request) WorkflowTimeout() time.Duration {
	if request.Quality.Budget.MaxWallTimeSeconds <= 0 {
		return 0
	}
	return time.Duration(request.Quality.Budget.MaxWallTimeSeconds) * time.Second
}

func (request Request) ToProblemGenParams() domain.ProblemGenParams {
	request = request.Canonical()
	maxConcepts := request.Domain.Combination.MaxConcepts
	if maxConcepts == 0 {
		maxConcepts = len(request.Domain.KnowledgePoints)
	}
	combination := &domain.KnowledgePointCombinationContract{
		SchemaVersion: domain.KnowledgePointCombinationSchemaV1,
		Mode:          request.Domain.Combination.Mode,
		MaxConcepts:   maxConcepts,
	}
	level := domain.ProblemLevel(request.Domain.Level)
	config := domain.DefaultTestDataConfig()
	config.NumSamples = request.Constraints.SampleCount
	if request.Constraints.TestCaseCount > 0 {
		config.NumTestCases = request.Constraints.TestCaseCount
		config.MinTestCases = 0
		config.MaxTestCases = 0
		config.AutoCaseCount = request.Constraints.AutoCaseCount
		config.Groups = []domain.TestGroup{{
			GroupID:  1,
			NumCases: request.Constraints.TestCaseCount,
			Score:    100,
		}}
	} else {
		config.NumTestCases = 0
		config.MinTestCases = request.Constraints.TestCaseCountMin
		config.MaxTestCases = request.Constraints.TestCaseCountMax
		config.Groups = []domain.TestGroup{{
			GroupID:     1,
			NumCases:    0,
			Score:       100,
			Description: "由模型按 corner case 自主决定 10-20 个测试点",
		}}
	}
	var generationEvidence *domain.GenerationEvidenceContract
	if request.Output.EvidenceLevel == EvidenceStandard {
		generationEvidence = &domain.GenerationEvidenceContract{
			SchemaVersion:                   domain.GenerationEvidenceContractSchemaV1,
			EvidenceLevel:                   domain.GenerationStandardEvidenceLevel,
			JobContractVersion:              JobContractVersion,
			GenerationArm:                   GenerationArmBaseline,
			GenerationArmMode:               GenerationArmModeBaseline,
			GenerationBehavior:              "unchanged",
			ReviewerProfile:                 ReviewerProfileV1,
			ReviewerProfileDescriptorSHA256: ReviewerProfileV1DescriptorSHA256,
			EvidenceProfile:                 EvidenceProfileV0,
			EvidenceProfileDescriptorSHA256: EvidenceProfileDescriptorSHA256,
			OutcomeTaxonomy:                 OutcomeTaxonomyVersion,
			IncludeEditorial:                request.Output.IncludeEditorial,
			IncludeSolutions:                request.Output.IncludeSolutions,
			IncludeTestData:                 request.Output.IncludeTestData,
		}
	}
	return domain.ProblemGenParams{
		Level:                     level,
		Difficulty:                request.Difficulty.Rating,
		Tags:                      append([]string(nil), request.Domain.KnowledgePoints...),
		KnowledgePointCombination: combination,
		GenerationEvidence:        generationEvidence,
		ContestStyle:              request.ContestStyle,
		TimeLimit:                 request.Constraints.TimeLimitMS,
		MemoryLimit:               request.Constraints.MemoryLimitMB,
		TestDataConfig:            config,
		RequireReview:             false,
		GenerateEditorial:         request.Output.IncludeEditorial,
		CustomPrompt:              request.CustomRequirements,
		Languages:                 append([]string(nil), request.Languages...),
		Locale:                    request.Locale,
		SimilarLimit:              request.Quality.SimilarLimit,
		MetadataExtras: map[string]interface{}{
			QualityEvidenceLevelMetadataKey: request.Output.EvidenceLevel,
			KnowledgePointCombinationMetadataKey: map[string]interface{}{
				"schema_version": domain.KnowledgePointCombinationSchemaV1,
				"mode":           combination.Mode,
				"max_concepts":   combination.MaxConcepts,
				"required_slugs": append([]string(nil), request.Domain.KnowledgePoints...),
			},
			GenerationAuditMetadataKey: map[string]interface{}{
				"schema_version":                     GenerationAuditSchemaV1,
				"generation_arm":                     GenerationArmBaseline,
				"generation_arm_mode":                GenerationArmModeBaseline,
				"generation_behavior":                "unchanged",
				"qg02_plus_enabled":                  false,
				"reviewer_profile":                   ReviewerProfileV1,
				"reviewer_profile_descriptor_sha256": ReviewerProfileV1DescriptorSHA256,
				"evidence_profile":                   EvidenceProfileV0,
				"evidence_profile_descriptor_sha256": EvidenceProfileDescriptorSHA256,
				"outcome_taxonomy":                   OutcomeTaxonomyVersion,
				"knowledge_point_combination_schema": domain.KnowledgePointCombinationSchemaV1,
				"requested_evidence_level":           request.Output.EvidenceLevel,
			},
		},
	}
}

// QualityEvidenceLevelFromParams recovers the server-authored product profile
// carried into the quality workflow. Missing metadata is interpreted only for
// histories created before the additive field: a legacy standard receipt
// contract maps to standard, and all older minimal jobs map to minimal.
func QualityEvidenceLevelFromParams(params domain.ProblemGenParams) (string, error) {
	level := ""
	if params.MetadataExtras != nil {
		if raw, ok := params.MetadataExtras[QualityEvidenceLevelMetadataKey]; ok {
			value, ok := raw.(string)
			if !ok {
				return "", fmt.Errorf("%s must be a string", QualityEvidenceLevelMetadataKey)
			}
			level = strings.ToLower(strings.TrimSpace(value))
			if level == "" {
				return "", fmt.Errorf("%s must not be blank", QualityEvidenceLevelMetadataKey)
			}
		}
	}
	if level == "" {
		if params.GenerationEvidence != nil {
			level = EvidenceStandard
		} else {
			level = EvidenceMinimal
		}
	}
	if level != EvidenceMinimal && level != EvidenceStandard && level != EvidenceAudit {
		return "", fmt.Errorf("unsupported quality evidence level %q", level)
	}
	if params.GenerationEvidence != nil && level != EvidenceStandard {
		return "", fmt.Errorf("legacy standard evidence contract conflicts with quality evidence level %q", level)
	}
	return level, nil
}
