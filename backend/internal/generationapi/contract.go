// Package generationapi contains the stable product-facing generation-jobs contract.
package generationapi

import (
	"fmt"
	"strings"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
)

const (
	RequestSchemaVersion = "algoforge.product.custom-generation.request.v1"
	MaxCandidateCount    = 1
	MaxCustomPromptBytes = 16 << 10
)

const (
	ProblemTypeStandard = "standard"
	ProblemTypeSubtask  = "subtask"

	EvidenceMinimal  = "minimal"
	EvidenceStandard = "standard"
	EvidenceAudit    = "audit"
)

const (
	JobStatusAccepted              = "accepted"
	JobStatusQueued                = "queued"
	JobStatusRunning               = "running"
	JobStatusSucceeded             = "succeeded"
	JobStatusQuarantined           = "quarantined"
	JobStatusFailed                = "failed"
	JobStatusCancelled             = "cancelled"
	JobStatusCancellationRequested = "cancellation_requested"

	JobPhaseQueued     = "queued"
	JobPhaseGenerating = "generating"
	JobPhaseValidating = "validating"
	JobPhaseReviewing  = "reviewing"
	JobPhaseStoring    = "storing"
	JobPhaseCompleted  = "completed"
)

// OutcomeCategory separates the Stage 0 denominators without asserting that
// a reviewer verdict is content truth. It is intentionally narrower than the
// product error code: a quality_not_met result can originate from either a
// deterministic content gate or a review gate.
type OutcomeCategory string

const (
	OutcomeCategoryTechnical              OutcomeCategory = "technical"
	OutcomeCategoryContent                OutcomeCategory = "content"
	OutcomeCategoryReview                 OutcomeCategory = "review"
	OutcomeCategoryPublicationEligibility OutcomeCategory = "publication_eligibility"
)

// Request is the versioned product contract preview for customizable,
// high-quality competitive-programming problem generation.
type Request struct {
	SchemaVersion      string           `json:"schema_version"`
	Domain             DomainTarget     `json:"domain"`
	Difficulty         DifficultyTarget `json:"difficulty"`
	ProblemType        string           `json:"problem_type"`
	Constraints        ConstraintTarget `json:"constraints"`
	Quality            QualityTarget    `json:"quality"`
	CandidateCount     int              `json:"candidate_count"`
	RandomSeed         *int64           `json:"random_seed,omitempty"`
	Output             OutputTarget     `json:"output"`
	Locale             string           `json:"locale"`
	Languages          []string         `json:"languages"`
	ContestStyle       string           `json:"contest_style,omitempty"`
	CustomRequirements string           `json:"custom_requirements,omitempty"`
	Runtime            RuntimeSelection `json:"runtime,omitempty"`
}

type DomainTarget struct {
	Name            string                    `json:"name"`
	Level           string                    `json:"level"`
	KnowledgePoints []string                  `json:"knowledge_points"`
	Combination     KnowledgePointCombination `json:"combination"`
}

type KnowledgePointCombination struct {
	Mode        string `json:"mode"`
	MaxConcepts int    `json:"max_concepts,omitempty"`
}

type DifficultyTarget struct {
	Rating             int    `json:"rating"`
	Tolerance          int    `json:"tolerance,omitempty"`
	CalibrationProfile string `json:"calibration_profile,omitempty"`
}

type ConstraintTarget struct {
	TimeLimitMS      int `json:"time_limit_ms"`
	MemoryLimitMB    int `json:"memory_limit_mb"`
	TestCaseCount    int `json:"test_case_count"`
	TestCaseCountMin int `json:"test_case_count_min,omitempty"`
	TestCaseCountMax int `json:"test_case_count_max,omitempty"`
	SampleCount      int `json:"sample_count"`
	// AutoCaseCount is a deprecated v1.3.1 compatibility alias. New clients
	// select adaptive mode with test_case_count=0 and the explicit bounds.
	AutoCaseCount  bool `json:"auto_case_count,omitempty"`
	MaxInputBytes  int  `json:"max_input_bytes,omitempty"`
	MaxOutputBytes int  `json:"max_output_bytes,omitempty"`
}

type QualityTarget struct {
	Strategy      string        `json:"strategy"`
	DedupMode     string        `json:"dedup_mode"`
	SimilarLimit  int           `json:"similar_limit,omitempty"`
	Budget        QualityBudget `json:"budget"`
	AuditProfiles []string      `json:"audit_profiles,omitempty"`
}

type QualityBudget struct {
	MaxLLMCalls        int `json:"max_llm_calls,omitempty"`
	MaxTokens          int `json:"max_tokens,omitempty"`
	MaxWallTimeSeconds int `json:"max_wall_time_seconds,omitempty"`
	MaxRegenerations   int `json:"max_regenerations,omitempty"`
}

type OutputTarget struct {
	EvidenceLevel    string   `json:"evidence_level"`
	Formats          []string `json:"formats"`
	IncludeEditorial bool     `json:"include_editorial"`
	IncludeSolutions bool     `json:"include_solutions"`
	IncludeTestData  bool     `json:"include_test_data"`
}

// RuntimeSelection names server-managed profiles. It deliberately does not
// carry API keys or endpoint secrets.
type RuntimeSelection struct {
	StatementProfile    string `json:"statement_profile,omitempty"`
	VerificationProfile string `json:"verification_profile,omitempty"`
}

func (request Request) ValidatePreview() error {
	if request.SchemaVersion != RequestSchemaVersion {
		return fmt.Errorf(
			"schema_version %q, require %q",
			request.SchemaVersion,
			RequestSchemaVersion,
		)
	}
	if err := request.Domain.validate(); err != nil {
		return err
	}
	if err := request.Difficulty.validate(); err != nil {
		return err
	}
	switch request.ProblemType {
	case ProblemTypeStandard, ProblemTypeSubtask:
	default:
		return fmt.Errorf("unsupported problem_type %q in contract v0", request.ProblemType)
	}
	if err := request.Constraints.validate(); err != nil {
		return err
	}
	if err := request.Quality.validate(); err != nil {
		return err
	}
	if request.CandidateCount < 1 || request.CandidateCount > MaxCandidateCount {
		return fmt.Errorf(
			"candidate_count must be in [1,%d], got %d",
			MaxCandidateCount,
			request.CandidateCount,
		)
	}
	if request.Locale != "en" && request.Locale != "zh" {
		return fmt.Errorf("locale must be en or zh")
	}
	if err := validateUniqueNonEmpty("languages", request.Languages); err != nil {
		return err
	}
	if len(request.CustomRequirements) > MaxCustomPromptBytes {
		return fmt.Errorf(
			"custom_requirements exceeds %d bytes",
			MaxCustomPromptBytes,
		)
	}
	if err := request.Output.validate(); err != nil {
		return err
	}
	return nil
}

func (target DomainTarget) validate() error {
	if strings.TrimSpace(target.Name) == "" {
		return fmt.Errorf("domain.name is required")
	}
	if err := validateUniqueNonEmpty(
		"domain.knowledge_points",
		target.KnowledgePoints,
	); err != nil {
		return err
	}
	switch target.Combination.Mode {
	case "single":
		if len(target.KnowledgePoints) != 1 {
			return fmt.Errorf("single combination requires exactly one knowledge point")
		}
	case "mixed":
		if len(target.KnowledgePoints) < 2 {
			return fmt.Errorf("mixed combination requires a primary knowledge point and at least one auxiliary knowledge point")
		}
	case "set", "sequence":
	default:
		return fmt.Errorf(
			"unsupported domain.combination.mode %q",
			target.Combination.Mode,
		)
	}
	if target.Combination.MaxConcepts < 0 {
		return fmt.Errorf("domain.combination.max_concepts must not be negative")
	}
	if target.Combination.MaxConcepts > 0 &&
		target.Combination.MaxConcepts < len(target.KnowledgePoints) {
		return fmt.Errorf(
			"domain.combination.max_concepts is below the requested knowledge point count",
		)
	}
	return nil
}

func (target DifficultyTarget) validate() error {
	if target.Rating < 800 || target.Rating > 3500 || target.Rating%100 != 0 {
		return fmt.Errorf("difficulty.rating must be 800..3500 in steps of 100")
	}
	if target.Tolerance < 0 || target.Tolerance > 1000 {
		return fmt.Errorf("difficulty.tolerance must be in [0,1000]")
	}
	return nil
}

func (target ConstraintTarget) validate() error {
	if target.TimeLimitMS <= 0 {
		return fmt.Errorf("constraints.time_limit_ms must be positive")
	}
	if target.MemoryLimitMB <= 0 {
		return fmt.Errorf("constraints.memory_limit_mb must be positive")
	}
	if target.TestCaseCount == 0 {
		minCases, maxCases := target.TestCaseCountMin, target.TestCaseCountMax
		if minCases == 0 {
			minCases = domain.MinAdaptiveTestCases
		}
		if maxCases == 0 {
			maxCases = domain.MaxAdaptiveTestCases
		}
		if minCases < domain.MinAdaptiveTestCases ||
			maxCases > domain.MaxAdaptiveTestCases ||
			minCases > maxCases {
			return fmt.Errorf("constraints adaptive test-case range must be within [%d,%d]", domain.MinAdaptiveTestCases, domain.MaxAdaptiveTestCases)
		}
		if target.SampleCount < 0 || target.SampleCount > maxCases {
			return fmt.Errorf("constraints.sample_count must be in [0,adaptive_max_test_cases]")
		}
	} else {
		if target.TestCaseCount < 1 || target.TestCaseCount > domain.MaxGeneratedTestCases {
			return fmt.Errorf("constraints.test_case_count must be in [1,%d] for v0", domain.MaxGeneratedTestCases)
		}
		if target.TestCaseCountMin != 0 || target.TestCaseCountMax != 0 {
			return fmt.Errorf("explicit test_case_count must not carry adaptive bounds")
		}
		if target.AutoCaseCount && (target.TestCaseCount < domain.MinAdaptiveTestCases || target.TestCaseCount > domain.MaxAdaptiveTestCases) {
			return fmt.Errorf("constraints.test_case_count must be in [10,20] when auto_case_count is enabled")
		}
		if target.SampleCount < 0 || target.SampleCount > target.TestCaseCount {
			return fmt.Errorf("constraints.sample_count must be in [0,test_case_count]")
		}
		if target.AutoCaseCount && target.SampleCount > domain.MaxAdaptiveTestCases {
			return fmt.Errorf("constraints.sample_count must not exceed 20 when auto_case_count is enabled")
		}
	}
	if target.MaxInputBytes < 0 || target.MaxOutputBytes < 0 {
		return fmt.Errorf("constraints byte limits must not be negative")
	}
	return nil
}

func (target QualityTarget) validate() error {
	switch target.Strategy {
	case "baseline", "balanced", "strict":
	default:
		return fmt.Errorf("unsupported quality.strategy %q", target.Strategy)
	}
	switch target.DedupMode {
	case "off", "warn", "reject":
	default:
		return fmt.Errorf("unsupported quality.dedup_mode %q", target.DedupMode)
	}
	if target.SimilarLimit < 0 {
		return fmt.Errorf("quality.similar_limit must not be negative")
	}
	if target.Budget.MaxLLMCalls < 0 || target.Budget.MaxTokens < 0 ||
		target.Budget.MaxWallTimeSeconds < 0 ||
		target.Budget.MaxRegenerations < 0 {
		return fmt.Errorf("quality budget values must not be negative")
	}
	if len(target.AuditProfiles) == 0 {
		return nil
	}
	return validateUniqueNonEmpty("quality.audit_profiles", target.AuditProfiles)
}

func (target OutputTarget) validate() error {
	switch target.EvidenceLevel {
	case EvidenceMinimal, EvidenceStandard, EvidenceAudit:
	default:
		return fmt.Errorf("unsupported output.evidence_level %q", target.EvidenceLevel)
	}
	if err := validateUniqueNonEmpty("output.formats", target.Formats); err != nil {
		return err
	}
	for _, format := range target.Formats {
		switch format {
		case "algoforge", "hydro":
		default:
			return fmt.Errorf("unsupported output format %q", format)
		}
	}
	return nil
}

func validateUniqueNonEmpty(path string, values []string) error {
	if len(values) == 0 {
		return fmt.Errorf("%s must not be empty", path)
	}
	seen := make(map[string]struct{}, len(values))
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		if value == "" {
			return fmt.Errorf("%s contains an empty value", path)
		}
		if _, ok := seen[value]; ok {
			return fmt.Errorf("%s contains duplicate value %q", path, value)
		}
		seen[value] = struct{}{}
	}
	return nil
}

type JobLinks struct {
	Status string `json:"status"`
	Result string `json:"result"`
	Cancel string `json:"cancel"`
	Events string `json:"events,omitempty"`
}

type JobAccepted struct {
	ContractVersion  string   `json:"contract_version"`
	JobID            string   `json:"job_id"`
	Status           string   `json:"status"`
	IdempotentReplay bool     `json:"idempotent_replay"`
	Links            JobLinks `json:"links"`
}

type JobError struct {
	Code            ErrorCode       `json:"code"`
	Message         string          `json:"message"`
	Retryable       bool            `json:"retryable"`
	OutcomeCategory OutcomeCategory `json:"outcome_category"`
}

type JobStatus struct {
	ContractVersion string    `json:"contract_version"`
	JobID           string    `json:"job_id"`
	Status          string    `json:"status"`
	Phase           string    `json:"phase"`
	Progress        int       `json:"progress"`
	ResultAvailable bool      `json:"result_available"`
	Error           *JobError `json:"error,omitempty"`
	Links           JobLinks  `json:"links"`
}

type JobResult struct {
	ContractVersion  string                  `json:"contract_version"`
	JobID            string                  `json:"job_id"`
	Status           string                  `json:"status"`
	EvidenceLevel    string                  `json:"evidence_level,omitempty"`
	Candidates       []CandidateResult       `json:"candidates"`
	Evidence         []EvidenceRef           `json:"evidence,omitempty"`
	EvidenceIdentity *EvidenceBundleIdentity `json:"evidence_identity,omitempty"`
	Error            *JobError               `json:"error,omitempty"`
}

type CandidateResult struct {
	CandidateID      string          `json:"candidate_id"`
	ProblemID        string          `json:"problem_id,omitempty"`
	Status           string          `json:"status"`
	OutcomeCategory  OutcomeCategory `json:"outcome_category,omitempty"`
	QuarantineReason string          `json:"quarantine_reason,omitempty"`
	ProblemURL       string          `json:"problem_url,omitempty"`
	QualityReportURI string          `json:"quality_report_uri,omitempty"`
	HydroURL         string          `json:"hydro_url,omitempty"`
}

type EvidenceRef struct {
	Kind   string `json:"kind"`
	SHA256 string `json:"sha256"`
	URI    string `json:"uri,omitempty"`
}

type JobCancellation struct {
	ContractVersion string   `json:"contract_version"`
	JobID           string   `json:"job_id"`
	Status          string   `json:"status"`
	Links           JobLinks `json:"links"`
}

type JobEvent struct {
	ContractVersion string    `json:"contract_version"`
	EventID         string    `json:"event_id"`
	Type            string    `json:"type"`
	JobID           string    `json:"job_id"`
	Status          string    `json:"status"`
	Phase           string    `json:"phase"`
	Progress        int       `json:"progress"`
	Error           *JobError `json:"error,omitempty"`
	OccurredAt      string    `json:"occurred_at"`
}
