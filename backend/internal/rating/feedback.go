package rating

import (
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
)

// latestFeedback is stable under input ordering. Revisions from one invited
// reviewer never count as additional people or as independent observations.
func latestFeedback(items []Feedback) []Feedback {
	latest := map[string]Feedback{}
	for _, f := range items {
		if f.ReviewerID == uuid.Nil {
			continue
		}
		key := f.ProblemID.String() + "/" + f.SubjectHash + "/" + f.ReviewerID.String()
		old, ok := latest[key]
		if !ok || f.Revision > old.Revision || f.Revision == old.Revision && (f.UpdatedAt.After(old.UpdatedAt) || f.UpdatedAt.Equal(old.UpdatedAt) && string(StableJSON(f)) > string(StableJSON(old))) {
			latest[key] = f
		}
	}
	keys := make([]string, 0, len(latest))
	for k := range latest {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]Feedback, 0, len(keys))
	for _, k := range keys {
		out = append(out, latest[k])
	}
	return out
}
func FeedbackSnapshotHash(items []Feedback) string {
	// Only current evidence enters the snapshot. Recomputing has no new prior,
	// mutable clock, accumulated counters or randomized rounding.
	return Digest(StableJSON(struct {
		Rule     string
		Feedback []Feedback
	}{RuleVersion, latestFeedback(items)}))
}

func windowOutcome(f Feedback) string {
	if f.Outcome == "not_attempted" || f.SeenBefore || f.WindowMinutes <= 0 || f.ElapsedMinutes < 0 || f.IndependentMinutes < 0 {
		return "censored"
	}
	assistanceAt := int(^uint(0) >> 1)
	if len(f.Assistance) > 0 {
		if f.AssistanceAfterMinutes == nil {
			assistanceAt = 0
		} else {
			assistanceAt = *f.AssistanceAfterMinutes
		}
	}
	if f.Outcome == "solved" && f.ElapsedMinutes > 0 && f.ElapsedMinutes <= f.WindowMinutes &&
		f.IndependentMinutes >= f.ElapsedMinutes && assistanceAt >= f.ElapsedMinutes {
		return "solved"
	}
	// A self-reported failure is usable for a window only if that full window
	// was observed independently. Leaving early or still trying is not failure.
	if (f.Outcome == "unsolved" || f.Outcome == "solved" || f.Outcome == "stopped") &&
		f.ObservedFullWindow && f.ElapsedMinutes >= f.WindowMinutes &&
		f.IndependentMinutes >= f.WindowMinutes && assistanceAt >= f.WindowMinutes {
		return "failure"
	}
	return "censored"
}
func SummarizeFeedback(items []Feedback) HumanSummary {
	out := HumanSummary{ReviewThreshold: ReviewThreshold, Groups: []FeedbackGroup{}, Limitations: []string{
		"邀请身份去重不自动证明现实中的独立人数；需管理员一人一份发放。",
		"CF 水平均为自报，完成结果未由本模块核验；不把成功者用时映射成整体难度。",
	}}
	groups := map[string]FeedbackGroup{}
	subjects := map[string]bool{}
	for _, f := range latestFeedback(items) {
		subjects[f.ProblemID.String()+"/"+f.SubjectHash] = true
		out.TotalReviewers++
		if f.Outcome != "not_attempted" && (f.Outcome == "solved" || f.Outcome == "unsolved" || f.Outcome == "in_progress" || f.Outcome == "stopped") {
			out.EffectiveReviewers++
		}
		if len(f.Assistance) > 0 {
			out.Assisted++
		}
		if f.SeenBefore {
			out.SeenBefore++
		}
		ability := "unknown"
		if f.CFRating != nil {
			ability = fmt.Sprintf("self_report_%d-%d", (*f.CFRating/400)*400, (*f.CFRating/400)*400+399)
		}
		first := "first"
		if f.SeenBefore {
			first = "repeat"
		}
		aid := "independent"
		if len(f.Assistance) > 0 {
			aid = "assisted"
		}
		key := fmt.Sprintf("%s|T=%d|%s|%s|%s", f.Context, f.WindowMinutes, first, aid, ability)
		g := groups[key]
		g.Key = key
		g.Count++
		switch windowOutcome(f) {
		case "solved":
			out.IndependentSolved++
			g.IndependentSolved++
		case "failure":
			out.WindowFailures++
			g.WindowFailures++
		default:
			out.Censored++
			g.Censored++
		}
		groups[key] = g
	}
	for _, k := range sortedKeys(groups) {
		out.Groups = append(out.Groups, groups[k])
	}
	out.ReviewTriggered = out.EffectiveReviewers >= ReviewThreshold
	if len(subjects) > 1 {
		// Callers normally filter first. Defense in depth prevents accidental
		// pooling of revisions into a threshold on a different current version.
		out.ReviewTriggered = false
		out.EffectiveReviewers = 0
		out.Limitations = append(out.Limitations, "包含多个题目版本；不能合并触发当前版本校准。")
	}
	if out.Assisted > 0 || out.SeenBefore > 0 {
		out.Limitations = append(out.Limitations, "受助与复习作答单独分组，不计为首次独立成功。")
	}
	if out.IndependentSolved == 0 || out.WindowFailures == 0 {
		out.Limitations = append(out.Limitations, "结果单侧或缺少完整观察；不能据此识别题目难度。")
	}
	return out
}
func BuildCalibration(problemID uuid.UUID, subjectHash string, items []Feedback) Calibration {
	filtered := make([]Feedback, 0, len(items))
	for _, f := range items {
		if f.ProblemID == problemID && f.SubjectHash == subjectHash {
			filtered = append(filtered, f)
		}
	}
	c := Calibration{ProblemID: problemID, SubjectHash: subjectHash, FeedbackHash: FeedbackSnapshotHash(filtered), RuleVersion: RuleVersion, Summary: SummarizeFeedback(filtered), Status: "insufficient_evidence",
		Reasons: []string{"首期先报告按能力、辅助、首次作答和观察窗口分组的实测结果。", "缺少经审核的共同锚点作答和能力校准；自由练习的完成率不能直接换算 CF 分数。"}}
	if c.Summary.ReviewTriggered {
		c.Status = "needs_review"
		c.Reasons = append(c.Reasons, "已达到 30 个有效受邀身份的试点复核门槛；不表示数值调整的充分条件。")
	}
	// Intentionally nil: fitting a numerical 1PL estimate from self-selected
	// free-practice feedback without shared anchor attempts is unidentified.
	return c
}

// FeedbackSignals reuses real attempts as hypotheses for targeted analysis.
// Ratings, skill estimates, reviewer IDs and invitation data are not exposed.
func FeedbackSignals(items []Feedback) []FeedbackSignal {
	current := latestFeedback(items)
	sort.SliceStable(current, func(i, j int) bool { return current[i].UpdatedAt.After(current[j].UpdatedAt) })
	out := []FeedbackSignal{}
	bounded := func(value string) string {
		r := []rune(strings.TrimSpace(value))
		if len(r) > 1000 {
			r = r[:1000]
		}
		return string(r)
	}
	for _, f := range current {
		if f.SeenBefore || (f.ElapsedMinutes <= 0 && f.IndependentMinutes <= 0) {
			continue
		}
		switch f.Outcome {
		case "solved", "unsolved", "in_progress", "stopped":
		default:
			continue
		}
		signal := FeedbackSignal{Source: "human_self_report", Verification: "unverified", FirstRoute: bounded(f.FirstRoute), FinalRoute: bounded(f.FinalRoute), Blockers: bounded(f.Blockers), ReportedOutcome: f.Outcome, Assisted: len(f.Assistance) > 0}
		if signal.FirstRoute == "" && signal.FinalRoute == "" && signal.Blockers == "" {
			continue
		}
		out = append(out, signal)
		if len(out) == 12 {
			break
		}
	}
	return out
}
