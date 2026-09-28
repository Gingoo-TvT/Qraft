package rating

import (
	"encoding/json"
	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/google/uuid"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// A verified reference is fetched by the worker, matched to the original
// imported statement, and bound to the final stored statement. Client-supplied
// labels never become verified source evidence.
type SourceReference struct {
	Status          string                   `json:"status"`
	Difficulty      *domain.SourceDifficulty `json:"difficulty,omitempty"`
	StatementSHA256 string                   `json:"statement_sha256,omitempty"`
	Reason          string                   `json:"reason,omitempty"`
}

func validSource(s *SourceReference) bool {
	if s == nil || s.Status != "verified" || s.Difficulty == nil || len(s.StatementSHA256) != 64 {
		return false
	}
	d := s.Difficulty
	if d.FetchedAt.IsZero() || d.FetchedAt.After(time.Now().Add(24*time.Hour)) || d.Value == "" {
		return false
	}
	u, err := url.Parse(d.SourceURL)
	if err != nil || u.Scheme != "https" || u.User != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	switch d.Scale {
	case "codeforces_rating":
		return d.Platform == "codeforces" && (host == "codeforces.com" || strings.HasSuffix(host, ".codeforces.com"))
	case "luogu_level":
		return d.Platform == "luogu" && (host == "luogu.com.cn" || host == "www.luogu.com.cn")
	case "nowcoder_level":
		return d.Platform == "nowcoder" && (host == "nowcoder.com" || strings.HasSuffix(host, ".nowcoder.com"))
	}
	return false
}

func NativeRating(s *SourceReference) (int, bool) {
	if !validSource(s) || s.Difficulty.Scale != "codeforces_rating" {
		return 0, false
	}
	n, err := strconv.Atoi(s.Difficulty.Value)
	return n, err == nil && n >= 800 && n <= 3500 && n%100 == 0
}

func SourceFromMetadata(metadata json.RawMessage, statement string) *SourceReference {
	var m struct {
		ImportDifficulty struct {
			Source *SourceReference `json:"source_reference"`
		} `json:"import_difficulty"`
	}
	if json.Unmarshal(metadata, &m) != nil || m.ImportDifficulty.Source == nil {
		return nil
	}
	s := *m.ImportDifficulty.Source
	if s.Status == "verified" && (!validSource(&s) || s.StatementSHA256 != Digest([]byte(statement))) {
		s.Status = "stale"
		s.Reason = "题面已修改或来源凭据不完整，原站难度需重新核对。"
	}
	return &s
}

func ValidAnchor(a Anchor) bool {
	if a.SourceURL == "" || a.Rating < 800 || a.Rating > 3500 || a.Rating%100 != 0 {
		return false
	}
	switch a.Basis {
	case "external_source":
		n, ok := NativeRating(a.SourceReference)
		return ok && n == a.Rating && a.SourceReference.Difficulty.SourceURL == a.SourceURL
	case "admin_decision":
		return a.DecisionID != uuid.Nil && a.SubjectHash != "" && a.ReviewedBy != "" && !a.ReviewedAt.IsZero()
	case "", "manual":
		return a.SourceConfirmed && a.ReviewedBy != "" && !a.ReviewedAt.IsZero()
	default:
		return false
	}
}

// A repeated import or another member of the same source family cannot inflate
// coverage. Selection is deterministic and does not use an LLM's guessed score.
func SelectAnchors(all []Anchor, excludedURL string, limit int) []Anchor {
	items := append([]Anchor(nil), all...)
	sort.Slice(items, func(i, j int) bool {
		if items[i].Rating != items[j].Rating {
			return items[i].Rating < items[j].Rating
		}
		return items[i].ID.String() < items[j].ID.String()
	})
	families, sources := map[string]bool{}, map[string]bool{}
	result := []Anchor{}
	for _, a := range items {
		family := a.Family
		if family == "" {
			family = a.SourceURL
		}
		if !ValidAnchor(a) || (a.SourceURL == excludedURL || (excludedURL != "" && a.SourceReference != nil && a.SourceReference.Difficulty != nil && a.SourceReference.Difficulty.SourceURL == excludedURL)) || families[family] || sources[a.SourceURL] {
			continue
		}
		families[family], sources[a.SourceURL] = true, true
		result = append(result, a)
	}
	if limit <= 0 || len(result) <= limit {
		return result
	}
	if limit == 1 {
		return []Anchor{result[len(result)/2]}
	}
	selected := make([]Anchor, limit)
	for i := range selected {
		selected[i] = result[i*(len(result)-1)/(limit-1)]
	}
	return selected
}

func ApplySourceReference(r *Report) {
	if category := EstimateNativeCategory(r.SourceReference, r.Anchors); category.Representative != nil {
		r.Estimate = category
	}
	if n, ok := NativeRating(r.SourceReference); ok {
		r.Estimate = ReferenceEstimate{Status: "source_reference", Lower: &n, Upper: &n, Representative: &n, Notes: []string{"采用原站题目难度快照；不以模型估分覆盖，也不自动修改管理员正式评级。"}}
	}
}

// A native category acquires a numeric reference only from current independent
// human decisions on that same native category, never from an invented table.
func EstimateNativeCategory(source *SourceReference, anchors []Anchor) ReferenceEstimate {
	out := ReferenceEstimate{Status: "unmapped_native_scale", Notes: []string{"原站等级与 CF 分数不是同一尺度；至少需要三个不同家族的有效人工确认对照。"}}
	if !validSource(source) || source.Difficulty.Scale == "codeforces_rating" {
		return out
	}
	values := []int{}
	families := map[string]bool{}
	urls := map[string]bool{}
	for _, a := range anchors {
		if a.Basis != "admin_decision" || !ValidAnchor(a) || !validSource(a.SourceReference) {
			continue
		}
		d := a.SourceReference.Difficulty
		if d.Platform != source.Difficulty.Platform || d.Scale != source.Difficulty.Scale || d.Value != source.Difficulty.Value || d.SourceURL == source.Difficulty.SourceURL {
			continue
		}
		family := a.Family
		if family == "" {
			family = d.SourceURL
		}
		if families[family] || urls[d.SourceURL] {
			continue
		}
		families[family], urls[d.SourceURL] = true, true
		values = append(values, a.Rating)
	}
	if len(values) < 3 {
		return out
	}
	sort.Ints(values)
	low, high, mid := values[0], values[len(values)-1], values[len(values)/2]
	return ReferenceEstimate{Status: "native_category_reference", Lower: &low, Upper: &high, Representative: &mid, Notes: []string{"同一原站等级的有效人工确认分数范围，仅作经验参照，不是官方换算或统计置信区间。"}}
}
