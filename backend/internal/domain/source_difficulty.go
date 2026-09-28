package domain

import "time"

// SourceDifficulty preserves the source's scale; a category or contest score
// must never be relabeled as a Codeforces rating.
type SourceDifficulty struct {
	Platform  string    `json:"platform"`
	Scale     string    `json:"scale"`
	Value     string    `json:"value"`
	Label     string    `json:"label"`
	SourceURL string    `json:"source_url"`
	FetchedAt time.Time `json:"fetched_at"`
}
