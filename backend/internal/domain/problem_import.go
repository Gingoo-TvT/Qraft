package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"
)

const ProblemImportQuery = "problem-import-state"

// SourceProblem is original source material, never an instruction to replace
// the authoring pipeline's system prompt. Statement bytes are kept unchanged.
type SourceProblem struct {
	ItemID    string `json:"item_id"`
	Title     string `json:"title"`
	Statement string `json:"statement"`
	SourceURL string `json:"source_url,omitempty"`
	SourceID  string `json:"source_id,omitempty"`
}

func (s SourceProblem) Hash() string {
	h := sha256.Sum256([]byte(s.Statement))
	return hex.EncodeToString(h[:])
}
func (s SourceProblem) Validate() error {
	if strings.TrimSpace(s.ItemID) == "" || len(s.ItemID) > 128 {
		return fmt.Errorf("item_id is required and must not exceed 128 bytes")
	}
	if strings.TrimSpace(s.Title) == "" || utf8.RuneCountInString(s.Title) > 200 {
		return fmt.Errorf("title is required and must not exceed 200 characters")
	}
	if strings.TrimSpace(s.Statement) == "" || len(s.Statement) > 128*1024 {
		return fmt.Errorf("statement is required and must not exceed 128 KiB")
	}
	if len(s.SourceID) > 512 || len(s.SourceURL) > 4096 {
		return fmt.Errorf("source identity is too long")
	}
	if s.SourceURL != "" {
		u, e := url.Parse(s.SourceURL)
		if e != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil {
			return fmt.Errorf("source_url must be an http(s) URL without credentials")
		}
	}
	return nil
}

type ProblemImportRequest struct {
	Title      string          `json:"title,omitempty"`
	Mode       string          `json:"mode"`
	Items      []SourceProblem `json:"items"`
	CreateSet  bool            `json:"create_set"`
	Difficulty int             `json:"difficulty,omitempty"`
	Language   string          `json:"language,omitempty"`
	Locale     string          `json:"locale,omitempty"`
}

func (r *ProblemImportRequest) Normalize() error {
	if r.Mode != "inspiration" && r.Mode != "preserve_statement" {
		return fmt.Errorf("mode must be inspiration or preserve_statement")
	}
	if len(r.Items) < 1 || len(r.Items) > 50 {
		return fmt.Errorf("items must contain 1–50 problems")
	}
	if utf8.RuneCountInString(r.Title) > 255 {
		return fmt.Errorf("collection title must not exceed 255 characters")
	}
	if r.Difficulty == 0 {
		r.Difficulty = 1500
	}
	if r.Difficulty < 800 || r.Difficulty > 3500 || r.Difficulty%100 != 0 {
		return fmt.Errorf("difficulty must be 800–3500 in steps of 100")
	}
	if r.Language == "" {
		r.Language = "cpp"
	}
	if r.Language != "cpp" && r.Language != "python3" && r.Language != "java" {
		return fmt.Errorf("unsupported language")
	}
	if r.Locale == "" {
		r.Locale = "zh"
	}
	if r.Locale != "zh" && r.Locale != "en" {
		return fmt.Errorf("locale must be zh or en")
	}
	ids := map[string]bool{}
	total := 0
	for i, v := range r.Items {
		if e := v.Validate(); e != nil {
			return fmt.Errorf("items[%d]: %w", i, e)
		}
		if ids[v.ItemID] {
			return fmt.Errorf("item_id must be unique")
		}
		ids[v.ItemID] = true
		total += len(v.Statement)
	}
	if total > 1024*1024 {
		return fmt.Errorf("combined statements must not exceed 1 MiB")
	}
	return nil
}

type ProblemImportInput struct {
	DirectDifficulty     bool                      `json:"direct_difficulty,omitempty"`
	ResumeItems          []ProblemImportItemResult `json:"resume_items,omitempty"`
	CollectionWorkflowID string                    `json:"collection_workflow_id,omitempty"`
	Request              ProblemImportRequest      `json:"request"`
	ProviderConfig       *ProviderRuntimeConfig    `json:"provider_config"`
	OwnerUserID          string                    `json:"owner_user_id"`
}
type ProblemImportItemResult struct {
	EstimatedDifficulty int    `json:"estimated_difficulty,omitempty"`
	DifficultyReason    string `json:"difficulty_reason,omitempty"`
	Warning             string `json:"warning,omitempty"`
	ItemID              string `json:"item_id"`
	Title               string `json:"title"`
	Status              string `json:"status"`
	ProblemID           string `json:"problem_id,omitempty"`
	WorkflowID          string `json:"workflow_id,omitempty"`
	DuplicateOf         string `json:"duplicate_of,omitempty"`
	RatingAssessmentID  string `json:"rating_assessment_id,omitempty"`
	Error               string `json:"error,omitempty"`
	StatementChanged    bool   `json:"statement_changed,omitempty"`
	ClarificationReason string `json:"clarification_reason,omitempty"`
}
type ProblemImportCounts struct {
	Total            int `json:"total"`
	Pending          int `json:"pending"`
	Running          int `json:"running"`
	Imported         int `json:"imported"`
	SkippedDuplicate int `json:"skipped_duplicate"`
	Failed           int `json:"failed"`
	AssessmentFailed int `json:"assessment_failed"`
}
type ProblemImportState struct {
	WorkflowID   string                    `json:"workflow_id"`
	Status       string                    `json:"status"`
	ProblemSetID string                    `json:"problem_set_id,omitempty"`
	Items        []ProblemImportItemResult `json:"items"`
	Counts       ProblemImportCounts       `json:"counts"`
	Error        string                    `json:"error,omitempty"`
}

func (s *ProblemImportState) Recount() {
	s.Counts = ProblemImportCounts{Total: len(s.Items)}
	for _, v := range s.Items {
		switch v.Status {
		case "pending":
			s.Counts.Pending++
		case "running":
			s.Counts.Running++
		case "imported":
			s.Counts.Imported++
		case "skipped_duplicate":
			s.Counts.SkippedDuplicate++
		case "failed":
			s.Counts.Failed++
		case "assessment_failed":
			s.Counts.AssessmentFailed++
		}
	}
}

// ImportSourceEvidence is separate from original-generation quality evidence.
// Importing never grants public-release approval or an originality verdict.
type ImportSourceEvidence struct {
	OJStatement         *OJStatement          `json:"oj_statement,omitempty"`
	Original            SourceProblem         `json:"original"`
	OriginalSHA256      string                `json:"original_sha256"`
	FinalSHA256         string                `json:"final_sha256"`
	ClarificationReason string                `json:"clarification_reason,omitempty"`
	Changes             []ImportClarification `json:"changes,omitempty"`
}
type ImportClarification struct {
	Original    string `json:"original"`
	Replacement string `json:"replacement"`
}
