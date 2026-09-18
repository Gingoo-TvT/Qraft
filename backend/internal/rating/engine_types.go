package rating

import (
	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/google/uuid"
)

type WorkflowInput struct {
	AssessmentID uuid.UUID                `json:"assessment_id"`
	ProblemID    uuid.UUID                `json:"problem_id"`
	SnapshotHash string                   `json:"snapshot_hash"`
	BlindA       *domain.LLMRuntimeConfig `json:"blind_a,omitempty"`
	BlindB       *domain.LLMRuntimeConfig `json:"blind_b,omitempty"`
	Review       *domain.LLMRuntimeConfig `json:"review,omitempty"`
}
type Counterexample struct {
	Input            string `json:"input"`
	LegalityArgument string `json:"legality_argument"`
	FailureReason    string `json:"failure_reason"`
}

// BlindSubject is an allowlist, never a redacted serialization of Subject.
// Official solutions, expected tags, metadata and target difficulty cannot
// accidentally travel through newly added Subject fields.
type BlindSubject struct {
	Title       string         `json:"title"`
	Statement   string         `json:"statement"`
	TimeLimit   int            `json:"time_limit"`
	MemoryLimit int            `json:"memory_limit"`
	Samples     []ReviewSample `json:"samples"`
}
type BlindSolution struct {
	Name            string   `json:"name"`
	Summary         string   `json:"summary"`
	Proof           string   `json:"proof"`
	Complexity      string   `json:"complexity"`
	ConstraintScope string   `json:"constraint_scope"`
	Language        string   `json:"language"`
	Code            string   `json:"code"`
	Uncertainties   []string `json:"uncertainties"`
}
type Analysis struct {
	Summary       string             `json:"summary"`
	KCs           []KC               `json:"kcs"`
	Paths         []Path             `json:"paths"`
	Comparisons   []AnchorComparison `json:"comparisons"`
	Disagreements []string           `json:"disagreements"`
	Limitations   []string           `json:"limitations"`
}

// FeedbackSignal is a bounded, anonymous clue. It does not assert verified
// success, independence, KC attribution, or a measured number of observations.
type FeedbackSignal struct {
	Source          string `json:"source"`
	Verification    string `json:"verification"`
	FirstRoute      string `json:"first_route"`
	FinalRoute      string `json:"final_route"`
	Blockers        string `json:"blockers"`
	ReportedOutcome string `json:"reported_outcome"`
	Assisted        bool   `json:"assisted"`
}
