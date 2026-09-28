package repository

import (
	"context"
	"encoding/json"
	"github.com/Gingoo-TvT/Qraft/backend/internal/rating"
	"github.com/google/uuid"
	"net/url"
	"strings"
	"time"
)

// Only shared problems with an independently sourced score or a current admin
// decision can teach later runs. Model estimates and private tasks are excluded.
func (r *RatingRepository) ListReferenceAnchors(ctx context.Context) ([]rating.Anchor, error) {
	rows, err := r.db.Query(ctx, `SELECT p.id,p.title,p.statement,COALESCE(p.detailed_solution,''),p.metadata_json,
 COALESCE(o.rating,0),COALESCE(o.subject_hash,''),o.decision_id,o.updated_at
 FROM problems p LEFT JOIN rating_official o ON o.problem_id=p.id
 WHERE p.status='published' AND (o.problem_id IS NOT NULL OR p.metadata_json->'import_difficulty'->'source_reference'->>'status'='verified')
 ORDER BY p.updated_at DESC,p.id LIMIT 64`)
	if err != nil {
		return nil, err
	}
	type candidate struct {
		id                         uuid.UUID
		title, statement, solution string
		metadata                   json.RawMessage
		official                   int
		hash                       string
		decision                   *uuid.UUID
		updated                    *time.Time
	}
	candidates := []candidate{}
	for rows.Next() {
		var c candidate
		if err = rows.Scan(&c.id, &c.title, &c.statement, &c.solution, &c.metadata, &c.official, &c.hash, &c.decision, &c.updated); err != nil {
			rows.Close()
			return nil, err
		}
		candidates = append(candidates, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	result := []rating.Anchor{}
	for _, c := range candidates {
		source := rating.SourceFromMetadata(c.metadata, c.statement)
		a := rating.Anchor{ID: c.id, Title: c.title, StatementSummary: referenceExcerpt(c.statement), SolutionSummary: referenceExcerpt(c.solution), Population: "竞赛练习参考", Family: c.id.String()}
		// Prefer a current explicit human decision; never overwrite it with an
		// upstream snapshot, including when an upstream rating changes later.
		if c.decision != nil && c.updated != nil {
			subject, e := r.CaptureSubject(ctx, c.id)
			if e != nil {
				return nil, e
			}
			if subject.Hash == c.hash {
				a.Rating, a.Basis, a.SubjectHash, a.DecisionID = c.official, "admin_decision", c.hash, *c.decision
				a.SourceURL, a.ReviewedBy, a.ReviewedAt = "qraft://problems/"+c.id.String(), "admin-decision:"+c.decision.String(), *c.updated
				a.RatingSource, a.RetrievedAt, a.SourceConfirmed = "Qraft 管理员正式决定", *c.updated, true
				if source != nil && source.Status == "verified" {
					a.Family = sourceFamily(source.Difficulty.SourceURL)
					a.SourceReference = source
				}
				result = append(result, a)
				continue
			}
		}
		if n, ok := rating.NativeRating(source); ok {
			a.Rating, a.Basis, a.SourceReference = n, "external_source", source
			a.SourceURL, a.RetrievedAt, a.Family = source.Difficulty.SourceURL, source.Difficulty.FetchedAt, sourceFamily(source.Difficulty.SourceURL)
			a.RatingSource, a.SourceConfirmed = source.Difficulty.Label, true
			result = append(result, a)
		}
	}
	return result, nil
}
func referenceExcerpt(s string) string {
	r := []rune(s)
	if len(r) > 2000 {
		return string(r[:2000]) + "\n[题面/解法节选：若缺少关键约束或论证，请标记为无法比较。]"
	}
	return string(r)
}
func sourceFamily(raw string) string {
	u, e := url.Parse(raw)
	if e != nil {
		return raw
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if strings.Contains(u.Hostname(), "codeforces.com") {
		if len(parts) >= 2 && (parts[0] == "contest" || parts[0] == "gym") {
			return "codeforces:" + parts[1]
		}
		if len(parts) >= 3 && parts[0] == "problemset" && parts[1] == "problem" {
			return "codeforces:" + parts[2]
		}
	}
	return raw
}
