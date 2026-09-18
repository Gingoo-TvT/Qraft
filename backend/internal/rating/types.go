// Package rating stores evidence separately from the author's target difficulty.
package rating

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

const RuleVersion = "kc-rating-pilot-v1"
const ReviewThreshold = 30

var (
	ErrNotFound     = errors.New("rating record not found")
	ErrConflict     = errors.New("rating evidence changed; refresh before continuing")
	ErrInvalid      = errors.New("invalid rating input")
	ErrUnauthorized = errors.New("review invitation is invalid, expired, or revoked")
)

type TestArtifact struct {
	ID           string `json:"id"`
	InputPath    string `json:"input_path"`
	OutputPath   string `json:"output_path"`
	InputSHA256  string `json:"input_sha256"`
	OutputSHA256 string `json:"output_sha256"`
	IsSample     bool   `json:"is_sample"`
	Input        string `json:"input,omitempty"`
	Output       string `json:"output,omitempty"`
}
type Subject struct {
	JudgeMode string `json:"judge_mode"`

	ProblemID        uuid.UUID       `json:"problem_id"`
	Hash             string          `json:"hash"`
	Title            string          `json:"title"`
	Statement        string          `json:"statement"`
	TimeLimit        int             `json:"time_limit"`
	MemoryLimit      int             `json:"memory_limit"`
	TargetDifficulty int             `json:"target_difficulty"`
	ExpectedTags     []string        `json:"expected_tags"`
	OfficialSolution string          `json:"official_solution,omitempty"`
	Metadata         json.RawMessage `json:"metadata,omitempty"`
	Tests            []TestArtifact  `json:"tests"`
	CapturedAt       time.Time       `json:"captured_at"`
}
type KC struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Definition  string   `json:"definition"`
	Conditions  string   `json:"conditions"`
	RelatedTags []string `json:"related_tags"`
	Status      string   `json:"status"` // candidate; never silently joins the reviewed catalog
}
type Evidence struct {
	ID          string          `json:"id"`
	Kind        string          `json:"kind"`
	Status      string          `json:"status"`
	Summary     string          `json:"summary"`
	ArtifactRef string          `json:"artifact_ref,omitempty"`
	Details     json.RawMessage `json:"details,omitempty"`
}
type Path struct {
	ConstraintScope      string           `json:"constraint_scope"`
	SemanticReview       string           `json:"semantic_review"`
	SemanticReviewReason string           `json:"semantic_review_reason"`
	Counterexamples      []Counterexample `json:"counterexamples"`

	ID                string     `json:"id"`
	Name              string     `json:"name"`
	Kind              string     `json:"kind"` // intended, alternative, misleading
	Summary           string     `json:"summary"`
	Proof             string     `json:"proof"`
	Complexity        string     `json:"complexity"`
	Language          string     `json:"language"`
	Code              string     `json:"code,omitempty"`
	KCIDs             []string   `json:"kc_ids"`
	Bypasses          []string   `json:"bypasses"`
	Evidence          []Evidence `json:"evidence"`
	Validation        string     `json:"validation"`
	HumanObservations int        `json:"human_observations"`
}
type Anchor struct {
	ID               uuid.UUID `json:"id"`
	Title            string    `json:"title"`
	SourceURL        string    `json:"source_url"`
	Rating           int       `json:"rating"`
	RatingSource     string    `json:"rating_source"`
	RetrievedAt      time.Time `json:"retrieved_at"`
	StatementSummary string    `json:"statement_summary"`
	SolutionSummary  string    `json:"solution_summary"`
	KCIDs            []string  `json:"kc_ids"`
	Population       string    `json:"population"`
	Family           string    `json:"family"`
	ReviewedBy       string    `json:"reviewed_by"`
	ReviewedAt       time.Time `json:"reviewed_at"`
	SourceConfirmed  bool      `json:"source_confirmed"`
}
type AnchorComparison struct {
	AnchorID     uuid.UUID `json:"anchor_id"`
	AnchorRating int       `json:"anchor_rating"`
	Relation     string    `json:"relation"` // easier, similar, harder, incomparable
	Reason       string    `json:"reason"`
}
type ReferenceEstimate struct {
	Status         string   `json:"status"`
	Lower          *int     `json:"lower,omitempty"`
	Upper          *int     `json:"upper,omitempty"`
	Representative *int     `json:"representative,omitempty"`
	Notes          []string `json:"notes"`
}
type ModelRun struct {
	RequestedModel string `json:"requested_model"`
	ReturnedModel  string `json:"returned_model"`
	EndpointID     string `json:"endpoint_id"`

	Role       string `json:"role"`
	Model      string `json:"model"`
	Provider   string `json:"provider"`
	EvidenceID string `json:"evidence_id,omitempty"`
	Summary    string `json:"summary"`
}
type Report struct {
	SnapshotHash     string `json:"snapshot_hash"`
	AdditionalRounds int    `json:"additional_rounds"`
	ModelDiversity   string `json:"model_diversity"`

	RuleVersion   string             `json:"rule_version"`
	Summary       string             `json:"summary"`
	Validity      string             `json:"validity"`
	KCs           []KC               `json:"kcs"`
	Paths         []Path             `json:"paths"`
	Evidence      []Evidence         `json:"evidence"`
	Anchors       []Anchor           `json:"anchors"`
	Comparisons   []AnchorComparison `json:"comparisons"`
	Estimate      ReferenceEstimate  `json:"estimate"`
	Models        []ModelRun         `json:"models"`
	Disagreements []string           `json:"disagreements"`
	Limitations   []string           `json:"limitations"`
}
type Assessment struct {
	ID          uuid.UUID `json:"id"`
	ProblemID   uuid.UUID `json:"problem_id"`
	Subject     Subject   `json:"subject"`
	Status      string    `json:"status"`
	Phase       string    `json:"phase"`
	WorkflowID  string    `json:"workflow_id"`
	RuleVersion string    `json:"rule_version"`
	Report      *Report   `json:"report,omitempty"`
	Error       string    `json:"error,omitempty"`
	CreatedBy   string    `json:"created_by"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	Stale       bool      `json:"stale"`
}
type InvitationRequest struct {
	ReviewerKey   string `json:"reviewer_key"`
	WindowMinutes int    `json:"window_minutes"`
	Context       string `json:"context"` // practice or contest
	ExpiresInDays int    `json:"expires_in_days"`
}
type Invitation struct {
	ID            uuid.UUID  `json:"id"`
	ProblemID     uuid.UUID  `json:"problem_id"`
	SubjectHash   string     `json:"subject_hash"`
	ReviewerID    uuid.UUID  `json:"reviewer_id"`
	ReviewerKey   string     `json:"reviewer_key"`
	WindowMinutes int        `json:"window_minutes"`
	Context       string     `json:"context"`
	ExpiresAt     time.Time  `json:"expires_at"`
	RevokedAt     *time.Time `json:"revoked_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}
type IssuedInvitation struct {
	Invitation Invitation `json:"invitation"`
	Token      string     `json:"token"` // returned once; database stores only its SHA-256 digest
}
type FeedbackInput struct {
	Outcome                string     `json:"outcome"` // solved, unsolved, in_progress, not_attempted, stopped
	SeenBefore             bool       `json:"seen_before"`
	Assistance             []string   `json:"assistance"` // hint, editorial, tags, ai, discussion
	AssistanceAfterMinutes *int       `json:"assistance_after_minutes,omitempty"`
	IndependentMinutes     int        `json:"independent_minutes"`
	ElapsedMinutes         int        `json:"elapsed_minutes"`
	ObservedFullWindow     bool       `json:"observed_full_window"`
	FirstRoute             string     `json:"first_route"`
	FinalRoute             string     `json:"final_route"`
	Blockers               string     `json:"blockers"`
	SubjectiveRating       *int       `json:"subjective_rating,omitempty"`
	CFRating               *int       `json:"cf_rating,omitempty"`
	CFRatingAt             *time.Time `json:"cf_rating_at,omitempty"`
	Code                   string     `json:"code,omitempty"`
	ResultSource           string     `json:"result_source"` // self_report or external_link; never automatically verified
	ResultURL              string     `json:"result_url,omitempty"`
	Notes                  string     `json:"notes"`
}
type Feedback struct {
	ID            uuid.UUID `json:"id"`
	ProblemID     uuid.UUID `json:"problem_id"`
	SubjectHash   string    `json:"subject_hash"`
	ReviewerID    uuid.UUID `json:"reviewer_id"`
	Revision      int       `json:"revision"`
	WindowMinutes int       `json:"window_minutes"`
	Context       string    `json:"context"`
	FeedbackInput
	UpdatedAt time.Time `json:"updated_at"`
}
type ReviewTask struct {
	Title         string         `json:"title"`
	Statement     string         `json:"statement"`
	TimeLimit     int            `json:"time_limit"`
	MemoryLimit   int            `json:"memory_limit"`
	Samples       []ReviewSample `json:"samples"`
	SubjectHash   string         `json:"subject_hash"`
	WindowMinutes int            `json:"window_minutes"`
	Context       string         `json:"context"`
	ExpiresAt     time.Time      `json:"expires_at"`
	Feedback      *FeedbackInput `json:"feedback,omitempty"`
}
type ReviewSample struct {
	Input  string `json:"input"`
	Output string `json:"output"`
}
type FeedbackGroup struct {
	Key               string `json:"key"`
	Count             int    `json:"count"`
	IndependentSolved int    `json:"independent_solved"`
	WindowFailures    int    `json:"window_failures"`
	Censored          int    `json:"censored"`
}
type HumanSummary struct {
	TotalReviewers     int             `json:"total_reviewers"`
	EffectiveReviewers int             `json:"effective_reviewers"`
	IndependentSolved  int             `json:"independent_solved"`
	WindowFailures     int             `json:"window_failures"`
	Censored           int             `json:"censored"`
	Assisted           int             `json:"assisted"`
	SeenBefore         int             `json:"seen_before"`
	ReviewThreshold    int             `json:"review_threshold"`
	ReviewTriggered    bool            `json:"review_triggered"`
	Groups             []FeedbackGroup `json:"groups"`
	Limitations        []string        `json:"limitations"`
}
type Calibration struct {
	ID              uuid.UUID    `json:"id"`
	ProblemID       uuid.UUID    `json:"problem_id"`
	SubjectHash     string       `json:"subject_hash"`
	FeedbackHash    string       `json:"feedback_hash"`
	RuleVersion     string       `json:"rule_version"`
	Summary         HumanSummary `json:"summary"`
	SuggestedRating *int         `json:"suggested_rating,omitempty"`
	Status          string       `json:"status"`
	Reasons         []string     `json:"reasons"`
	CreatedAt       time.Time    `json:"created_at"`
}
type DecisionInput struct {
	ExpectedDecisionID *uuid.UUID `json:"expected_decision_id,omitempty"`

	SubjectHash   string     `json:"subject_hash"`
	FeedbackHash  string     `json:"feedback_hash"`
	AssessmentID  *uuid.UUID `json:"assessment_id,omitempty"`
	CalibrationID *uuid.UUID `json:"calibration_id,omitempty"`
	Action        string     `json:"action"` // accept, modify, reject, defer
	Rating        *int       `json:"rating,omitempty"`
	Reason        string     `json:"reason"`
}
type Decision struct {
	ID        uuid.UUID `json:"id"`
	ProblemID uuid.UUID `json:"problem_id"`
	DecisionInput
	PreviousRating *int      `json:"previous_rating,omitempty"`
	Actor          string    `json:"actor"`
	CreatedAt      time.Time `json:"created_at"`
}
type OfficialRating struct {
	Rating      int       `json:"rating"`
	SubjectHash string    `json:"subject_hash"`
	DecisionID  uuid.UUID `json:"decision_id"`
	Stale       bool      `json:"stale"`
	UpdatedAt   time.Time `json:"updated_at"`
}
type Workspace struct {
	Subject      Subject         `json:"subject"`
	Official     *OfficialRating `json:"official,omitempty"`
	Assessments  []Assessment    `json:"assessments"`
	Feedback     []Feedback      `json:"feedback"`
	FeedbackHash string          `json:"feedback_hash"`
	Human        HumanSummary    `json:"human"`
	Calibrations []Calibration   `json:"calibrations"`
	Decisions    []Decision      `json:"decisions"`
}

// Store is shared by HTTP handlers and workers; no Temporal dependency.
type Store interface {
	CaptureSubject(context.Context, uuid.UUID) (Subject, error)
	Workspace(context.Context, uuid.UUID) (Workspace, error)
	CreateAssessment(context.Context, uuid.UUID, string) (Assessment, error)
	GetAssessment(context.Context, uuid.UUID) (Assessment, error)
	UpdateAssessment(context.Context, uuid.UUID, string, string, *Report, string) error
	ListAnchors(context.Context) ([]Anchor, error)
	CreateAnchor(context.Context, Anchor, string) (Anchor, error)
	IssueInvitation(context.Context, uuid.UUID, InvitationRequest, string) (IssuedInvitation, error)
	ListInvitations(context.Context, uuid.UUID) ([]Invitation, error)
	RevokeInvitation(context.Context, uuid.UUID, uuid.UUID) error
	ReviewTask(context.Context, string) (ReviewTask, error)
	SubmitFeedback(context.Context, string, FeedbackInput) (Feedback, error)
	ListFeedback(context.Context, uuid.UUID, string) ([]Feedback, error)
	SaveCalibration(context.Context, Calibration) (Calibration, error)
	Decide(context.Context, uuid.UUID, DecisionInput, string) (Decision, error)
}
