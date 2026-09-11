package domain

import (
	"fmt"
	"net/url"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// WorkflowStatus enum
// ---------------------------------------------------------------------------

// WorkflowStatus represents the high-level state of a problem-generation
// workflow.
type WorkflowStatus string

const (
	// WorkflowStatusPending means the workflow has been created but not yet
	// started.
	WorkflowStatusPending WorkflowStatus = "pending"
	// WorkflowStatusRunning means the workflow is actively executing steps.
	WorkflowStatusRunning WorkflowStatus = "running"
	// WorkflowStatusWaitingReview means the workflow is paused, awaiting a
	// human review decision.
	WorkflowStatusWaitingReview WorkflowStatus = "waiting_review"
	// WorkflowStatusCompleted means the workflow finished successfully.
	WorkflowStatusCompleted WorkflowStatus = "completed"
	// WorkflowStatusRejectedQuarantined means the workflow completed normally,
	// but its generated candidate was isolated after an automated review denial.
	WorkflowStatusRejectedQuarantined WorkflowStatus = "rejected_quarantined"
	// WorkflowStatusFailed means the workflow terminated due to an error.
	WorkflowStatusFailed WorkflowStatus = "failed"
	// WorkflowStatusCancelled means the workflow was explicitly cancelled by
	// a user or system action.
	WorkflowStatusCancelled WorkflowStatus = "cancelled"
)

const (
	// ReviewSignalChannelName is shared by Temporal workflows and API clients.
	ReviewSignalChannelName = "human-review-decision"
	// WorkflowStateQueryName exposes the logical state of a running workflow.
	WorkflowStateQueryName = "workflow-state"
)

// ReviewSignal is the shared payload contract for human review signals.
type ReviewSignal struct {
	Token    string         `json:"token,omitempty"`
	Decision ReviewDecision `json:"decision"`
}

// LLMReviewSummary is the bounded subset of the model review exposed to
// operators while a workflow is waiting for a human decision.
type LLMReviewSummary struct {
	Approved            bool     `json:"approved"`
	Issues              []string `json:"issues,omitempty"`
	Suggestions         []string `json:"suggestions,omitempty"`
	Confidence          float64  `json:"confidence"`
	EstimatedDifficulty int      `json:"estimated_difficulty,omitempty"`
}

// ReviewRequest identifies one human-review gate within a workflow run.
type ReviewRequest struct {
	Token            string           `json:"token"`
	RunID            string           `json:"run_id"`
	ReviewAttempt    int              `json:"review_attempt"`
	ReviewResultHash string           `json:"review_result_hash"`
	TokenRequired    bool             `json:"token_required"`
	ReviewResult     LLMReviewSummary `json:"review_result"`
	Decision         *ReviewDecision  `json:"decision,omitempty"`
}

// ProviderRuntimeConfig lets API callers choose distinct runtime provider
// settings for selected LLM steps without placing raw secrets in workflow
// history. APIKeyRef supports env:NAME references resolved by the worker
// process and runtime:<token> references issued by the API service.
type ProviderRuntimeConfig struct {
	Statement    *LLMRuntimeConfig `json:"statement,omitempty"`
	Verification *LLMRuntimeConfig `json:"verification,omitempty"`
	Review       *LLMRuntimeConfig `json:"review,omitempty"`
}

// LLMRuntimeConfig describes one caller-selected LLM endpoint/model.
type LLMRuntimeConfig struct {
	Model           string `json:"model,omitempty"`
	APIKeyRef       string `json:"api_key_ref,omitempty"`
	BaseURL         string `json:"base_url,omitempty"`
	Provider        string `json:"provider,omitempty"`
	Protocol        string `json:"protocol,omitempty"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`

	// APIKey is accepted only so validation can fail closed if a client sends
	// a raw secret. Use APIKeyRef with env:NAME instead.
	APIKey string `json:"api_key,omitempty"`
}

// Validate checks runtime provider overrides for secret safety and stable
// provenance identity.
func (c *ProviderRuntimeConfig) Validate() error {
	if c == nil {
		return nil
	}
	if err := c.Statement.Validate("provider_config.statement"); err != nil {
		return err
	}
	if err := c.Verification.Validate("provider_config.verification"); err != nil {
		return err
	}
	if err := c.Review.Validate("provider_config.review"); err != nil {
		return err
	}
	return nil
}

// Validate checks a single runtime provider override.
func (c *LLMRuntimeConfig) Validate(path string) error {
	if c == nil {
		return nil
	}
	c.Model = strings.TrimSpace(c.Model)
	c.APIKeyRef = strings.TrimSpace(c.APIKeyRef)
	c.BaseURL = strings.TrimSpace(c.BaseURL)
	c.Provider = strings.TrimSpace(c.Provider)
	c.Protocol = strings.ToLower(strings.TrimSpace(c.Protocol))
	c.ReasoningEffort = strings.ToLower(strings.TrimSpace(c.ReasoningEffort))

	if strings.TrimSpace(c.APIKey) != "" {
		return fmt.Errorf("%s.api_key must not contain raw secrets; use api_key_ref with env:NAME or runtime:<token>", path)
	}
	for field, value := range map[string]string{
		"model":            c.Model,
		"api_key_ref":      c.APIKeyRef,
		"base_url":         c.BaseURL,
		"provider":         c.Provider,
		"protocol":         c.Protocol,
		"reasoning_effort": c.ReasoningEffort,
	} {
		if len(value) > 512 {
			return fmt.Errorf("%s.%s is too long", path, field)
		}
		if hasControlRune(value) {
			return fmt.Errorf("%s.%s contains control characters", path, field)
		}
	}
	if !isSupportedReasoningEffort(c.ReasoningEffort) {
		return fmt.Errorf("%s.reasoning_effort %q is unsupported; use none, minimal, low, medium, high, xhigh, or max", path, c.ReasoningEffort)
	}
	if c.APIKeyRef != "" {
		switch {
		case strings.HasPrefix(c.APIKeyRef, "env:"):
			if !isEnvRefName(strings.TrimPrefix(c.APIKeyRef, "env:")) {
				return fmt.Errorf("%s.api_key_ref must use a valid environment variable name", path)
			}
		case strings.HasPrefix(c.APIKeyRef, "runtime:"):
			if !isRuntimeRefToken(strings.TrimPrefix(c.APIKeyRef, "runtime:")) {
				return fmt.Errorf("%s.api_key_ref must use a valid runtime token", path)
			}
		default:
			return fmt.Errorf("%s.api_key_ref must be env:NAME or runtime:<token>; raw API keys are not accepted", path)
		}
	}
	if c.BaseURL != "" {
		if strings.Contains(c.BaseURL, "#") {
			return fmt.Errorf("%s.base_url must not contain a fragment", path)
		}
		parsed, err := url.ParseRequestURI(c.BaseURL)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return fmt.Errorf("%s.base_url must be an absolute http(s) URL", path)
		}
		scheme := strings.ToLower(parsed.Scheme)
		if scheme != "https" && scheme != "http" {
			return fmt.Errorf("%s.base_url must use http or https", path)
		}
		if parsed.User != nil {
			return fmt.Errorf("%s.base_url must not contain credentials", path)
		}
		if parsed.RawQuery != "" || parsed.ForceQuery {
			return fmt.Errorf("%s.base_url must not contain a query", path)
		}
		if parsed.Fragment != "" {
			return fmt.Errorf("%s.base_url must not contain a fragment", path)
		}
		if c.Provider == "" {
			return fmt.Errorf("%s.provider is required when base_url is set", path)
		}
	}
	if !isSupportedLLMProtocol(c.Protocol) {
		return fmt.Errorf("%s.protocol %q is unsupported", path, c.Protocol)
	}
	return nil
}

// isSupportedReasoningEffort is intentionally a closed set. An empty value
// means that no reasoning field is sent and the provider is not given an
// application-selected effort; it is not an AlgoForge fallback value.
func isSupportedReasoningEffort(value string) bool {
	switch value {
	case "", "none", "minimal", "low", "medium", "high", "xhigh", "max":
		return true
	default:
		return false
	}
}

func isSupportedLLMProtocol(protocol string) bool {
	switch protocol {
	case "", "auto", "anthropic-messages", "anthropic", "messages", "claude",
		"gemini-native", "gemini", "google", "openai-responses", "responses", "openai",
		"openai-chat", "chat-completions", "openai-compatible":
		return true
	default:
		return false
	}
}

func hasControlRune(value string) bool {
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

func isRuntimeRefToken(token string) bool {
	if token == "" {
		return false
	}
	for _, r := range token {
		if r != '-' && r != '_' && (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

func isEnvRefName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		if i == 0 {
			if r != '_' && (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') {
				return false
			}
			continue
		}
		if r != '_' && (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

// IsValid reports whether the status is one of the known values.
func (ws WorkflowStatus) IsValid() bool {
	switch ws {
	case WorkflowStatusPending, WorkflowStatusRunning, WorkflowStatusWaitingReview,
		WorkflowStatusCompleted, WorkflowStatusRejectedQuarantined,
		WorkflowStatusFailed, WorkflowStatusCancelled:
		return true
	default:
		return false
	}
}

// IsTerminal reports whether the status represents a final state from which
// no further transitions are expected.
func (ws WorkflowStatus) IsTerminal() bool {
	switch ws {
	case WorkflowStatusCompleted, WorkflowStatusRejectedQuarantined,
		WorkflowStatusFailed, WorkflowStatusCancelled:
		return true
	default:
		return false
	}
}

// ---------------------------------------------------------------------------
// WorkflowStep enum
// ---------------------------------------------------------------------------

// WorkflowStep identifies a discrete phase within the generation pipeline.
// Steps are executed in the order defined by their numeric constants.
type WorkflowStep string

const (
	// StepSimilarityCheck searches the existing problem bank for duplicates.
	StepSimilarityCheck WorkflowStep = "similarity_check"
	// StepGenerateStatement asks the LLM to produce a problem statement.
	StepGenerateStatement WorkflowStep = "generate_statement"
	// StepPostStatementSimilarity checks generated statement text against the
	// active statement embedding index. Versioned workflows check the finalized
	// statement after validated samples are bound; legacy histories retain the
	// original pre-test position.
	StepPostStatementSimilarity WorkflowStep = "post_statement_similarity"
	// StepGenerateSolution asks the LLM to produce model & brute solutions.
	StepGenerateSolution WorkflowStep = "generate_solution"
	// StepCompileCheck compiles all generated source files in the sandbox.
	StepCompileCheck WorkflowStep = "compile_check"
	// StepGenerateTestdata generates or runs the test-data generator.
	StepGenerateTestdata WorkflowStep = "generate_testdata"
	// StepRunSandbox executes the solutions against the test data.
	StepRunSandbox WorkflowStep = "run_sandbox"
	// StepValidate cross-checks outputs between model and brute solutions.
	StepValidate WorkflowStep = "validate"
	// StepAssessFeasibility asks the LLM whether a validation mismatch is due
	// to a flawed problem or buggy solution code.
	StepAssessFeasibility WorkflowStep = "assess_feasibility"
	// StepLLMReview asks the LLM to review the generated problem for quality.
	StepLLMReview WorkflowStep = "llm_review"
	// StepHumanReview pauses the workflow for a human reviewer.
	StepHumanReview WorkflowStep = "human_review"
	// StepStore persists the finalised problem and its artefacts.
	StepStore WorkflowStep = "store"
)

// AllWorkflowSteps returns the ordered sequence of steps in the generation
// pipeline.
func AllWorkflowSteps() []WorkflowStep {
	return []WorkflowStep{
		StepSimilarityCheck,
		StepGenerateStatement,
		StepPostStatementSimilarity,
		StepGenerateSolution,
		StepCompileCheck,
		StepGenerateTestdata,
		StepRunSandbox,
		StepValidate,
		StepAssessFeasibility,
		StepLLMReview,
		StepHumanReview,
		StepStore,
	}
}

// IsValid reports whether the step is one of the known values.
func (ws WorkflowStep) IsValid() bool {
	for _, s := range AllWorkflowSteps() {
		if s == ws {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// StepResult
// ---------------------------------------------------------------------------

// StepResult captures the outcome of a single workflow step execution.
type StepResult struct {
	// Which step this result belongs to.
	Step WorkflowStep `json:"step"`

	// Status of the step (reuses WorkflowStatus for simplicity: running,
	// completed, failed).
	Status WorkflowStatus `json:"status"`

	// Wall-clock duration the step took to execute.
	Duration time.Duration `json:"duration"`

	// Arbitrary output produced by the step (JSON-friendly).
	Output interface{} `json:"output,omitempty"`

	// Error message if the step failed.
	Error string `json:"error,omitempty"`
}

// ---------------------------------------------------------------------------
// WorkflowState
// ---------------------------------------------------------------------------

// WorkflowState is the runtime state of a problem-generation workflow.  It is
// typically serialised to JSON and stored alongside the workflow record so that
// the orchestrator can resume after interruptions.
type WorkflowState struct {
	// The step currently being executed (or the last completed step).
	CurrentStep WorkflowStep `json:"current_step"`

	// Overall workflow status.
	Status WorkflowStatus `json:"status"`

	// Progress percentage (0-100).
	Progress int `json:"progress"`

	// Results collected so far, one per executed step.
	Steps []StepResult `json:"steps,omitempty"`

	// Top-level error description when Status == WorkflowStatusFailed.
	Error string `json:"error,omitempty"`

	// When the workflow started executing.
	StartedAt *time.Time `json:"started_at,omitempty"`

	// When the workflow reached a terminal state.
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

// WorkflowStateQuery is returned by the read-only workflow-state query.
type WorkflowStateQuery struct {
	State         WorkflowState  `json:"state"`
	ReviewRequest *ReviewRequest `json:"review_request,omitempty"`
}

// RecordStep appends a StepResult and updates CurrentStep accordingly.
func (ws *WorkflowState) RecordStep(result StepResult) {
	ws.Steps = append(ws.Steps, result)
	ws.CurrentStep = result.Step

	// Recalculate progress based on the number of steps completed.
	allSteps := AllWorkflowSteps()
	if len(allSteps) > 0 {
		ws.Progress = (len(ws.Steps) * 100) / len(allSteps)
	}
}

// MarkFailed transitions the workflow to the failed state with the given
// reason.
func (ws *WorkflowState) MarkFailed(reason string) {
	ws.MarkFailedAt(reason, time.Now())
}

// MarkFailedAt is the deterministic workflow-safe form of MarkFailed. Temporal
// workflow code must pass workflow.Now(ctx) rather than reading wall time.
func (ws *WorkflowState) MarkFailedAt(reason string, completedAt time.Time) {
	ws.Status = WorkflowStatusFailed
	ws.Error = reason
	ws.CompletedAt = &completedAt
}

// MarkCompleted transitions the workflow to the completed state.
func (ws *WorkflowState) MarkCompleted() {
	ws.MarkCompletedAt(time.Now())
}

// MarkCompletedAt is the deterministic workflow-safe form of MarkCompleted.
func (ws *WorkflowState) MarkCompletedAt(completedAt time.Time) {
	ws.Status = WorkflowStatusCompleted
	ws.Progress = 100
	ws.CompletedAt = &completedAt
}

// MarkRejectedQuarantinedAt records a successful Temporal completion whose
// product outcome is an isolated, non-publishable candidate.
func (ws *WorkflowState) MarkRejectedQuarantinedAt(completedAt time.Time) {
	ws.Status = WorkflowStatusRejectedQuarantined
	ws.Progress = 100
	ws.CompletedAt = &completedAt
}

// ---------------------------------------------------------------------------
// ProblemGenParams
// ---------------------------------------------------------------------------

// ProblemGenParams bundles every configurable parameter that controls the
// problem-generation workflow.  It is supplied by the API caller (or a
// scheduler) and threaded through every step.
type ProblemGenParams struct {
	// Two-tier classification.
	Level ProblemLevel `json:"level"`

	// Target difficulty rating (800-3500, step 100).
	Difficulty int `json:"difficulty"`

	// Desired tags (must be valid for Level).
	Tags []string `json:"tags"`

	// KnowledgePointCombination carries the versioned product semantics for
	// jobs v1. Nil is the replay-compatible legacy behavior.
	KnowledgePointCombination *KnowledgePointCombinationContract `json:"knowledge_point_combination,omitempty"`

	// GenerationEvidence requests a versioned, downloadable standard evidence
	// receipt. Nil preserves legacy and minimal jobs behavior.
	GenerationEvidence *GenerationEvidenceContract `json:"generation_evidence,omitempty"`

	// Contest style hint (e.g. "codeforces", "icpc", "ioi").
	ContestStyle string `json:"contest_style,omitempty"`

	// Execution time limit in milliseconds.
	TimeLimit int `json:"time_limit"`

	// Memory limit in megabytes.
	MemoryLimit int `json:"memory_limit"`

	// Test-data generation configuration.
	TestDataConfig TestDataConfig `json:"test_data_config"`

	// Deprecated: operational break-glass switch for pausing a workflow for
	// human review. Product callers must leave this false.
	RequireReview bool `json:"require_review"`

	// Whether to generate an editorial / detailed solution write-up.
	GenerateEditorial bool `json:"generate_editorial"`

	// Per-tag difficulty ranges keyed by tag name. Each value is [min, max].
	// Populated by the service layer before starting the workflow.
	TagRanges map[string][2]int `json:"tag_ranges,omitempty"`

	// Free-form prompt appended to the LLM generation request.
	CustomPrompt string `json:"custom_prompt,omitempty"`

	// Languages to generate solutions in (e.g. ["cpp", "python3"]).
	Languages []string `json:"languages,omitempty"`

	// Natural language for generated content ("en" or "zh"). Default: "en".
	Locale string `json:"locale,omitempty"`

	// Optional request-scoped LLM runtime settings. Raw API keys are rejected;
	// callers should pass key references such as env:ALGOFORGE_LLM_API_KEY or
	// API-issued runtime:<token> references.
	ProviderConfig *ProviderRuntimeConfig `json:"provider_config,omitempty"`

	// Maximum number of similar existing problems allowed before aborting.
	SimilarLimit int `json:"similar_limit"`

	// MetadataExtras are arbitrary key/value pairs merged into the stored
	// Problem.metadata_json. Used by batch orchestrators (e.g. GPLT) to tag
	// generated problems with batch identifiers and tier information without
	// polluting the core ProblemGenParams schema.
	MetadataExtras map[string]interface{} `json:"metadata_extras,omitempty"`
}

// Validate checks that the generation parameters are internally consistent.
func (p *ProblemGenParams) Validate() error {
	if !p.Level.IsValid() {
		return fmt.Errorf("invalid level: %q", p.Level)
	}

	minD, maxD := DifficultyRange(p.Level)
	if p.Difficulty < minD || p.Difficulty > maxD {
		return fmt.Errorf("difficulty %d out of range [%d, %d] for level %q",
			p.Difficulty, minD, maxD, p.Level)
	}
	if p.Difficulty%100 != 0 {
		return fmt.Errorf("difficulty %d must be a multiple of 100", p.Difficulty)
	}
	if err := p.KnowledgePointCombination.Validate(p.Tags); err != nil {
		return fmt.Errorf("knowledge_point_combination: %w", err)
	}
	if err := p.GenerationEvidence.Validate(); err != nil {
		return fmt.Errorf("generation_evidence: %w", err)
	}

	if p.TimeLimit <= 0 {
		return fmt.Errorf("time_limit must be positive, got %d", p.TimeLimit)
	}
	if p.MemoryLimit <= 0 {
		return fmt.Errorf("memory_limit must be positive, got %d", p.MemoryLimit)
	}

	// Validate a copy so normalizing adaptive defaults does not mutate workflow
	// input (which would change replay payloads). Zero selects the product's
	// bounded 10-20 adaptive mode; positive values remain legacy exact counts.
	testDataConfig := p.TestDataConfig
	if err := testDataConfig.NormalizeForGeneration(); err != nil {
		return fmt.Errorf("test_data_config: %w", err)
	}

	if len(p.Languages) == 0 {
		return fmt.Errorf("at least one language must be specified")
	}
	if err := p.ProviderConfig.Validate(); err != nil {
		return err
	}

	return nil
}

// DefaultProblemGenParams returns a ProblemGenParams with sensible defaults
// for quick experimentation.
func DefaultProblemGenParams() ProblemGenParams {
	return ProblemGenParams{
		Level:             LevelAlgorithm,
		Difficulty:        1500,
		TimeLimit:         2000, // 2 seconds
		MemoryLimit:       256,  // 256 MB
		TestDataConfig:    DefaultTestDataConfig(),
		RequireReview:     false,
		GenerateEditorial: true,
		Languages:         []string{"cpp"},
		SimilarLimit:      3,
	}
}

// ---------------------------------------------------------------------------
// ReviewDecision
// ---------------------------------------------------------------------------

// ReviewDecision captures a human reviewer's verdict on a generated problem.
type ReviewDecision struct {
	// Whether the problem is approved for publication.
	Approved bool `json:"approved"`

	// Free-form feedback from the reviewer (mandatory when rejecting).
	Feedback string `json:"feedback,omitempty"`
}

// Validate checks the review decision for consistency.
func (rd *ReviewDecision) Validate() error {
	if !rd.Approved && rd.Feedback == "" {
		return fmt.Errorf("feedback is required when rejecting a problem")
	}
	return nil
}
