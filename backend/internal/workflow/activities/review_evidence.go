package activities

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

type canonicalReviewResult struct {
	SourceArtifacts     []*ArtifactRef  `json:"source_artifacts"`
	Approved            bool            `json:"approved"`
	Issues              []string        `json:"issues"`
	Suggestions         []string        `json:"suggestions"`
	Confidence          float64         `json:"confidence"`
	EstimatedDifficulty int             `json:"estimated_difficulty"`
	IsDuplicate         bool            `json:"is_duplicate"`
	DuplicateOf         string          `json:"duplicate_of"`
	DuplicateReason     string          `json:"duplicate_reason"`
	ReviewDetails       json.RawMessage `json:"review_details,omitempty"`
	FullText            string          `json:"full_text"`
}

// CanonicalReviewResultJSON returns the complete, stable representation used
// by the quarantine ledger and its content hash.
func CanonicalReviewResultJSON(result ReviewResult) ([]byte, string, error) {
	sourceArtifacts := result.SourceArtifacts
	if sourceArtifacts == nil {
		sourceArtifacts = []*ArtifactRef{}
	}
	issues := result.Issues
	if issues == nil {
		issues = []string{}
	}
	suggestions := result.Suggestions
	if suggestions == nil {
		suggestions = []string{}
	}

	encoded, err := json.Marshal(canonicalReviewResult{
		SourceArtifacts:     sourceArtifacts,
		Approved:            result.Approved,
		Issues:              issues,
		Suggestions:         suggestions,
		Confidence:          result.Confidence,
		EstimatedDifficulty: result.EstimatedDifficulty,
		IsDuplicate:         result.IsDuplicate,
		DuplicateOf:         result.DuplicateOf,
		DuplicateReason:     result.DuplicateReason,
		ReviewDetails:       result.ReviewDetails,
		FullText:            result.FullText,
	})
	if err != nil {
		return nil, "", fmt.Errorf("encoding canonical review result: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return encoded, hex.EncodeToString(digest[:]), nil
}
