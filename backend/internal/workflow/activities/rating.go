package activities

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/llm"
	"github.com/Gingoo-TvT/Qraft/backend/internal/rating"
	"github.com/google/uuid"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
)

type RatingSnapshot struct {
	RuleVersion          string                  `json:"rule_version,omitempty"`
	FeedbackSignals      []rating.FeedbackSignal `json:"feedback_signals,omitempty"`
	FeedbackSnapshotHash string                  `json:"feedback_snapshot_hash,omitempty"`
	Subject              rating.Subject          `json:"subject"`
	Anchors              []rating.Anchor         `json:"anchors"`
}
type RatingBlindInput struct {
	Snapshot ArtifactRef              `json:"snapshot"`
	Role     string                   `json:"role"`
	Runtime  *domain.LLMRuntimeConfig `json:"runtime,omitempty"`
}
type RatingBlindResult struct {
	Solution rating.BlindSolution `json:"solution"`
	Model    rating.ModelRun      `json:"model"`
	Evidence rating.Evidence      `json:"evidence"`
}
type RatingAnalyzeInput struct {
	Snapshot ArtifactRef              `json:"snapshot"`
	Blinds   []ArtifactRef            `json:"blinds"`
	Previous *ArtifactRef             `json:"previous,omitempty"`
	Round    int                      `json:"round"`
	Runtime  *domain.LLMRuntimeConfig `json:"runtime,omitempty"`
}
type RatingVerifyInput struct {
	Snapshot ArtifactRef `json:"snapshot"`
	Report   ArtifactRef `json:"report"`
}
type RatingReportResult struct {
	Report               ArtifactRef `json:"report"`
	NeedsAdditionalRound bool        `json:"needs_additional_round"`
}
type RatingFinalizeInput struct {
	AssessmentID uuid.UUID   `json:"assessment_id"`
	Report       ArtifactRef `json:"report"`
}
type RatingStateInput struct {
	AssessmentID uuid.UUID `json:"assessment_id"`
	Status       string    `json:"status"`
	Phase        string    `json:"phase"`
	Error        string    `json:"error,omitempty"`
}

func ratingInvalid(err error) error {
	return temporal.NewNonRetryableApplicationError(err.Error(), "InvalidParameterError", err)
}
func (a *Activities) ratingRead(ctx context.Context, ref ArtifactRef, out any) error {
	data, err := a.getArtifact(ctx, &ref)
	if err != nil {
		return err
	}
	if len(data) > 8<<20 {
		return fmt.Errorf("rating artifact exceeds 8 MiB")
	}
	if rating.Digest(data) != ref.SHA256 {
		return fmt.Errorf("rating artifact digest mismatch")
	}
	return json.Unmarshal(data, out)
}
func (a *Activities) ratingWrite(ctx context.Context, value any, kind string) (*ArtifactRef, error) {
	b, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if len(b) > 8<<20 {
		return nil, fmt.Errorf("rating artifact exceeds bounded budget")
	}
	m := artifactMetadataFromActivity(ctx)
	m.ArtifactType = "rating_" + kind
	m.SourceType = "rating_candidate_evidence"
	m.RetentionClass = "workflow_cas_unreviewed"
	return a.putArtifactWithMetadata(ctx, b, "application/json", m)
}
func (a *Activities) RatingStateActivity(ctx context.Context, in RatingStateInput) error {
	if a.deps == nil || a.deps.RatingStore == nil {
		return fmt.Errorf("rating store is not configured")
	}
	if in.Error != "" {
		in.Error = "评估未完成，请检查当前阶段的服务配置和执行证据。"
	}
	return a.deps.RatingStore.UpdateAssessment(ctx, in.AssessmentID, in.Status, in.Phase, nil, in.Error)
}

// RatingLoadActivity freezes the selected anchor set with the already captured
// subject. Only CAS references, never test bytes or editorial text, enter history.
func (a *Activities) RatingLoadActivity(ctx context.Context, in rating.WorkflowInput) (*ArtifactRef, error) {
	if a.deps == nil || a.deps.RatingStore == nil {
		return nil, fmt.Errorf("rating store is not configured")
	}
	item, err := a.deps.RatingStore.GetAssessment(ctx, in.AssessmentID)
	if err != nil {
		return nil, err
	}
	if item.ProblemID != in.ProblemID || item.Subject.Hash != in.SnapshotHash {
		return nil, ratingInvalid(fmt.Errorf("assessment subject mismatch"))
	}
	if item.RuleVersion != rating.RuleVersion && item.RuleVersion != rating.LegacyRuleVersion {
		return nil, ratingInvalid(fmt.Errorf("unsupported rating rules"))
	}
	if item.RuleVersion == rating.LegacyRuleVersion {
		anchors, e := a.deps.RatingStore.ListAnchors(ctx)
		if e != nil {
			return nil, e
		}
		feedback, e := a.deps.RatingStore.ListFeedback(ctx, item.ProblemID, item.Subject.Hash)
		if e != nil {
			return nil, e
		}
		for i := range item.Subject.Tests {
			test := &item.Subject.Tests[i]
			if test.IsSample {
				test.Input, test.Output, e = a.ratingTestBytes(ctx, *test)
				if e != nil {
					return nil, e
				}
			}
		}
		if e = a.deps.RatingStore.UpdateAssessment(ctx, in.AssessmentID, "running", "blind_solving", nil, ""); e != nil {
			return nil, e
		}
		return a.ratingWrite(ctx, RatingSnapshot{RuleVersion: rating.LegacyRuleVersion, Subject: item.Subject, Anchors: rating.SelectAnchors(anchors, "", 6), FeedbackSignals: rating.FeedbackSignals(feedback), FeedbackSnapshotHash: rating.FeedbackSnapshotHash(feedback)}, "snapshot")
	}
	source := rating.SourceFromMetadata(item.Subject.Metadata, item.Subject.Statement)
	if source == nil || source.Status != "verified" {
		source = a.resolveSourceDifficulty(ctx, item.Subject.Metadata, item.Subject.Statement)
	}
	item.Subject.SourceReference = source
	excluded := ""
	if source != nil && source.Difficulty != nil {
		excluded = source.Difficulty.SourceURL
	}
	reviewed, err := a.difficultyAnchors(ctx, excluded)
	if err != nil {
		return nil, err
	}
	kept := reviewed[:0]
	for _, anchor := range reviewed {
		if anchor.ID != item.ProblemID {
			kept = append(kept, anchor)
		}
	}
	reviewed = kept
	feedback, err := a.deps.RatingStore.ListFeedback(ctx, item.ProblemID, item.Subject.Hash)
	if err != nil {
		return nil, err
	}
	// Samples are materialized only from the frozen artifacts, with hash checks.
	for i := range item.Subject.Tests {
		t := &item.Subject.Tests[i]
		if !t.IsSample {
			continue
		}
		input, output, err := a.ratingTestBytes(ctx, *t)
		if err != nil {
			return nil, err
		}
		t.Input = input
		t.Output = output
	}
	if err := a.deps.RatingStore.UpdateAssessment(ctx, in.AssessmentID, "running", "blind_solving", nil, ""); err != nil {
		return nil, err
	}
	return a.ratingWrite(ctx, RatingSnapshot{RuleVersion: item.RuleVersion, Subject: item.Subject, Anchors: reviewed, FeedbackSignals: rating.FeedbackSignals(feedback), FeedbackSnapshotHash: rating.FeedbackSnapshotHash(feedback)}, "snapshot")
}
func (a *Activities) ratingTestBytes(ctx context.Context, t rating.TestArtifact) (string, string, error) {
	input, output := t.Input, t.Output
	var err error
	if input == "" && t.InputPath != "" {
		if a.deps.MinIO == nil {
			return "", "", fmt.Errorf("test artifact storage unavailable")
		}
		input, err = a.downloadFromMinIO(ctx, t.InputPath)
		if err != nil {
			return "", "", err
		}
	}
	if output == "" && t.OutputPath != "" {
		if a.deps.MinIO == nil {
			return "", "", fmt.Errorf("test artifact storage unavailable")
		}
		output, err = a.downloadFromMinIO(ctx, t.OutputPath)
		if err != nil {
			return "", "", err
		}
	}
	if t.InputSHA256 == "" || t.OutputSHA256 == "" || rating.Digest([]byte(input)) != t.InputSHA256 || rating.Digest([]byte(output)) != t.OutputSHA256 {
		return "", "", fmt.Errorf("frozen test %s changed or lacks content digest", t.ID)
	}
	return input, output, nil
}
func ratingDecode(text string, target any) error {
	// A single uncertainty is semantically the same as a one-item list.
	// Normalize only this blind-solve field; keep the strict decoder below
	// for all other field types, unknown keys, and trailing JSON values.
	if _, blind := target.(*rating.BlindSolution); blind {
		var object map[string]json.RawMessage
		if json.Unmarshal([]byte(text), &object) == nil {
			raw := object["uncertainties"]
			if strings.HasPrefix(strings.TrimSpace(string(raw)), "\"") {
				var value string
				if err := json.Unmarshal(raw, &value); err != nil {
					return err
				}
				values := []string{}
				if strings.TrimSpace(value) != "" {
					values = append(values, value)
				}
				object["uncertainties"], _ = json.Marshal(values)
				normalized, err := json.Marshal(object)
				if err != nil {
					return err
				}
				text = string(normalized)
			}
		}
	}
	if _, analysis := target.(*rating.Analysis); analysis {
		var object map[string]json.RawMessage
		if json.Unmarshal([]byte(text), &object) == nil {
			if raw, present := object["summary"]; present {
				summary, err := ratingAnalysisSummaryText(raw)
				if err != nil {
					return err
				}
				object["summary"], _ = json.Marshal(summary)
				normalized, err := json.Marshal(object)
				if err != nil {
					return err
				}
				text = string(normalized)
			}
		}
	}
	decoder := json.NewDecoder(strings.NewReader(strings.TrimSpace(text)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("expected exactly one JSON object")
	}
	return nil
}
func (a *Activities) ratingComplete(ctx context.Context, role, system string, payload any, runtime *domain.LLMRuntimeConfig, target any) (rating.ModelRun, rating.Evidence, error) {
	req := &llm.Request{MaxTokens: 18000, System: system, Messages: []llm.Message{{Role: "user", Content: string(rating.StableJSON(payload))}}, PromptID: "rating_" + role, PromptVersion: rating.ModelPromptVersion}
	applyLLMRuntime(req, runtime)
	stop := func() {}
	if activity.IsActivity(ctx) {
		stop = heartbeatWhile(ctx, "rating "+role, 15*time.Second)
	}
	defer stop()
	response, ref, err := a.completeLLMWithProvenance(ctx, "rating_"+role, req, 1)
	if err != nil {
		if ctx.Err() != nil {
			return rating.ModelRun{}, rating.Evidence{}, ctx.Err()
		}
		return rating.ModelRun{}, rating.Evidence{}, fmt.Errorf("rating model request failed; check provider configuration or retry")
	}
	if response == nil || ref == nil || ref.LLMCallReceipt == nil {
		return rating.ModelRun{}, rating.Evidence{}, fmt.Errorf("rating response lacks call receipt")
	}
	if response.StopReason == "max_tokens" {
		return rating.ModelRun{}, rating.Evidence{}, ratingInvalid(fmt.Errorf("rating response truncated"))
	}
	if err := ratingDecode(response.Text(), target); err != nil {
		return rating.ModelRun{}, rating.Evidence{}, ratingInvalid(fmt.Errorf("rating model returned an invalid structured response"))
	}
	receipt := ref.LLMCallReceipt
	ev := rating.Evidence{ID: ref.SHA256, Kind: "llm_call", Status: "recorded", Summary: role + " independent provider call", ArtifactRef: ref.Key, Details: rating.StableJSON(ref)}
	model := rating.ModelRun{Role: role, Provider: receipt.Provider, Model: receipt.RequestedModel, RequestedModel: receipt.RequestedModel, ReturnedModel: receipt.ReturnedModel, EndpointID: receipt.EndpointID, EvidenceID: ev.ID, Summary: "配置模型身份；返回模型和调用哈希见证据。"}
	return model, ev, nil
}
func (a *Activities) RatingBlindSolveActivity(ctx context.Context, in RatingBlindInput) (*ArtifactRef, error) {
	if in.Role != "blind_a" && in.Role != "blind_b" {
		return nil, ratingInvalid(fmt.Errorf("invalid blind role"))
	}
	var snap RatingSnapshot
	if err := a.ratingRead(ctx, in.Snapshot, &snap); err != nil {
		return nil, err
	}
	result := RatingBlindResult{}
	m, e, err := a.ratingComplete(ctx, in.Role, rating.BlindSystemPrompt, rating.BlindInput(snap.Subject), in.Runtime, &result.Solution)
	if err != nil {
		return nil, err
	}
	if err := rating.ValidateBlind(result.Solution); err != nil {
		return nil, ratingInvalid(err)
	}
	result.Model = m
	result.Evidence = e
	return a.ratingWrite(ctx, result, in.Role)
}
func (a *Activities) RatingAnalyzeActivity(ctx context.Context, in RatingAnalyzeInput) (*ArtifactRef, error) {
	if in.Round < 0 || in.Round > 1 || len(in.Blinds) != 2 {
		return nil, ratingInvalid(fmt.Errorf("rating requires two blind results and at most one additional round"))
	}
	var snap RatingSnapshot
	if err := a.ratingRead(ctx, in.Snapshot, &snap); err != nil {
		return nil, err
	}
	blinds := make([]RatingBlindResult, 2)
	for i, ref := range in.Blinds {
		if err := a.ratingRead(ctx, ref, &blinds[i]); err != nil {
			return nil, err
		}
	}
	var previous *rating.Report
	if in.Previous != nil {
		previous = &rating.Report{}
		if err := a.ratingRead(ctx, *in.Previous, previous); err != nil {
			return nil, err
		}
	}
	payload := struct {
		Problem          rating.BlindSubject     `json:"problem"`
		ExpectedTags     []string                `json:"expected_tags"`
		OfficialSolution string                  `json:"official_solution"`
		Blinds           []RatingBlindResult     `json:"blinds"`
		Anchors          []rating.Anchor         `json:"reviewed_anchors"`
		FeedbackSignals  []rating.FeedbackSignal `json:"human_feedback_signals,omitempty"`
		Previous         *rating.Report          `json:"previous_report,omitempty"`
		Round            int                     `json:"round"`
	}{rating.BlindInput(snap.Subject), snap.Subject.ExpectedTags, snap.Subject.OfficialSolution, blinds, snap.Anchors, snap.FeedbackSignals, previous, in.Round}
	var analysis rating.Analysis
	m, e, err := a.ratingComplete(ctx, fmt.Sprintf("analysis_%d", in.Round), rating.AnalysisSystemPrompt, payload, in.Runtime, &analysis)
	if err != nil {
		return nil, err
	}
	if err := rating.NormalizeAnalysis(&analysis); err != nil {
		return nil, ratingInvalid(err)
	}
	version := snap.RuleVersion
	if version == "" {
		version = rating.LegacyRuleVersion
	}
	report := rating.Report{SourceReference: snap.Subject.SourceReference, RuleVersion: version, SnapshotHash: snap.Subject.Hash, Summary: analysis.Summary, KCs: analysis.KCs, Paths: analysis.Paths, Anchors: snap.Anchors, Comparisons: analysis.Comparisons, Disagreements: analysis.Disagreements, Limitations: analysis.Limitations, Models: []rating.ModelRun{}, Evidence: []rating.Evidence{}, AdditionalRounds: in.Round}
	for _, b := range blinds {
		report.Models = append(report.Models, b.Model)
		report.Evidence = append(report.Evidence, b.Evidence)
		role := b.Model.Role
		found := -1
		for i, p := range report.Paths {
			if p.ID == role {
				found = i
				break
			}
		}
		reviewScope := "uncertain"
		reviewProof := "未提供独立路径论证"
		reviewComplexity := "未提供独立复杂度核对"
		if found >= 0 {
			reviewScope = report.Paths[found].ConstraintScope
			reviewProof = report.Paths[found].Proof
			reviewComplexity = report.Paths[found].Complexity
		}
		if found < 0 {
			report.Paths = append([]rating.Path{{ID: role, Name: b.Solution.Name, Kind: "alternative", SemanticReview: "needs_review"}}, report.Paths...)
			found = 0
		}
		p := &report.Paths[found]
		p.Code = b.Solution.Code
		p.Language = b.Solution.Language
		p.Summary = b.Solution.Summary
		p.Proof = "盲解论证：" + b.Solution.Proof + "\n独立复核：" + reviewProof
		p.Complexity = "盲解：" + b.Solution.Complexity + "；复核：" + reviewComplexity
		p.ConstraintScope = rating.ConservativeScope(b.Solution.ConstraintScope, reviewScope)
		p.Validation = "candidate"
		report.Disagreements = append(report.Disagreements, b.Solution.Uncertainties...)
	}
	if len(report.Paths) > 5 {
		report.Paths = report.Paths[:5]
		report.Limitations = append(report.Limitations, "本次最多验证五条候选路径。")
	}
	if len(snap.FeedbackSignals) > 0 {
		report.Evidence = append(report.Evidence, rating.Evidence{ID: snap.FeedbackSnapshotHash, Kind: "human_feedback_signals", Status: "self_report_unverified", Summary: "已将匿名首次尝试路线作为待检验线索用于定向分析；未自动计为 KC 实际观察人数。", Details: rating.StableJSON(snap.FeedbackSignals)})
		report.Limitations = append(report.Limitations, "最多采用最近十二条自报首次尝试路线作为线索；不包含身份或能力/主观评分字段，不代表人群统计。")
	}
	report.Models = append(report.Models, m)
	report.Evidence = append(report.Evidence, e)
	if previous != nil {
		report.Models = append(report.Models, previous.Models...)
		report.Evidence = append(report.Evidence, previous.Evidence...)
		// Preserve tested evidence only for byte-identical code, language and scope.
		for i := range report.Paths {
			p := &report.Paths[i]
			for _, old := range previous.Paths {
				if p.ID == old.ID && p.Code == old.Code && p.Language == old.Language && p.ConstraintScope == old.ConstraintScope {
					p.Evidence = old.Evidence
					p.Validation = old.Validation
				}
			}
		}
		report.Limitations = append(report.Limitations, "已执行唯一一轮定向复核；未解决争议保留，不继续自动追求共识。")
	}
	// Never trust a model-supplied anchor rating or an invented anchor ID.
	allowed := map[uuid.UUID]rating.Anchor{}
	for _, x := range snap.Anchors {
		allowed[x.ID] = x
	}
	comps := make([]rating.AnchorComparison, 0, len(report.Comparisons))
	for _, c := range report.Comparisons {
		if anchor, ok := allowed[c.AnchorID]; ok {
			c.AnchorRating = anchor.Rating
			comps = append(comps, c)
		} else {
			report.Disagreements = append(report.Disagreements, "模型引用了未冻结的锚点，已排除。")
		}
	}
	report.Comparisons = comps
	report.Limitations = append(report.Limitations, "锚点按已审核难度范围确定性分层选取最多六道，再逐个判断可比性；未将原标签与 KC ID 视为同一目录。")
	rating.NormalizeReport(&report)
	return a.ratingWrite(ctx, report, "analysis")
}
func (a *Activities) RatingFinalizeActivity(ctx context.Context, in RatingFinalizeInput) error {
	var report rating.Report
	if err := a.ratingRead(ctx, in.Report, &report); err != nil {
		return err
	}
	item, err := a.deps.RatingStore.GetAssessment(ctx, in.AssessmentID)
	if err != nil {
		return err
	}
	if item.Subject.Hash != report.SnapshotHash {
		return ratingInvalid(fmt.Errorf("report subject mismatch"))
	}
	rating.NormalizeReport(&report)
	return a.deps.RatingStore.UpdateAssessment(ctx, in.AssessmentID, "completed", "completed", &report, "")
}

// Summary is display text only. Preserve every label/value when a model uses
// a small flat object; do not reinterpret any of its claims as rating evidence.
func ratingAnalysisSummaryText(raw json.RawMessage) (string, error) {
	trimmed := strings.TrimSpace(string(raw))
	if strings.HasPrefix(trimmed, `"`) {
		var text string
		err := json.Unmarshal(raw, &text)
		return text, err
	}
	if !strings.HasPrefix(trimmed, "{") {
		return "", fmt.Errorf("analysis summary must be text or a flat text object")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return "", err
	}
	if len(fields) == 0 || len(fields) > 8 {
		return "", fmt.Errorf("analysis summary object requires one to eight text fields")
	}
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var builder strings.Builder
	for i, key := range keys {
		valueRaw := fields[key]
		if !strings.HasPrefix(strings.TrimSpace(string(valueRaw)), `"`) {
			return "", fmt.Errorf("analysis summary object values must be strings")
		}
		var value string
		if err := json.Unmarshal(valueRaw, &value); err != nil {
			return "", err
		}
		if i > 0 {
			builder.WriteByte('\n')
		}
		builder.WriteString(key)
		builder.WriteString(": ")
		builder.WriteString(value)
		if builder.Len() > 16<<10 {
			return "", fmt.Errorf("analysis summary object exceeds 16 KiB text budget")
		}
	}
	return builder.String(), nil
}
