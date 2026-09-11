package activities

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/llm"
)

type ProblemSetQuizReviewInput struct {
	Type    domain.QuizType          `json:"type"`
	Draft   QuizDraft                `json:"draft"`
	Runtime *domain.LLMRuntimeConfig `json:"runtime"`
}
type ProblemSetQuizReviewResult struct {
	Approved        bool           `json:"approved"`
	Feedback        string         `json:"feedback"`
	SourceArtifacts []*ArtifactRef `json:"source_artifacts,omitempty"`
}
type setQuizIndependentAnswer struct {
	Answers   []string `json:"answers"`
	Ambiguous *bool    `json:"ambiguous"`
	Reason    string   `json:"reason"`
}

// The verifier solves from the question and options only. Generated answers and
// explanations are excluded to avoid turning the check into agreement by anchoring.
func (a *Activities) ReviewProblemSetQuizActivity(ctx context.Context, in ProblemSetQuizReviewInput) (*ProblemSetQuizReviewResult, error) {
	if err := validateSetQuizDraft(in.Type, in.Draft); err != nil {
		return &ProblemSetQuizReviewResult{Feedback: err.Error()}, nil
	}
	question, _ := json.Marshal(struct {
		Type      domain.QuizType     `json:"type"`
		Statement string              `json:"statement"`
		Options   []domain.QuizOption `json:"options,omitempty"`
	}{in.Type, in.Draft.Statement, in.Draft.Options})
	req := &llm.Request{MaxTokens: 4096, System: `独立解答下面的客观题。检查题意是否充分、是否存在歧义或未定义行为、选择题选项是否有多解或无解。选择题返回所有正确选项的大写标签；判断题仅返回“对”或“错”；填空题按 ___ 出现顺序返回简洁、可精确比较的答案。不得猜测缺失条件。仅输出 JSON：{"answers":["..."],"ambiguous":false,"reason":"简明推导与检查依据"}。`, Messages: []llm.Message{{Role: "user", Content: string(question)}}}
	applyLLMRuntime(req, in.Runtime)
	stop := heartbeatWhile(ctx, "independent objective answer check", 15*time.Second)
	defer stop()
	resp, artifact, err := a.completeLLMWithProvenance(ctx, "quiz_verification", req, 2)
	if err != nil {
		return nil, wrapRequiredProviderEffectError("independent quiz review", err)
	}
	var answer setQuizIndependentAnswer
	text := strings.TrimSpace(resp.Text())
	if block := extractJSONBlock(text); block != "" {
		text = block
	}
	if err = json.Unmarshal([]byte(text), &answer); err != nil {
		return nil, fmt.Errorf("客观题复核输出无效: %w", err)
	}
	if answer.Ambiguous == nil || strings.TrimSpace(answer.Reason) == "" || len(answer.Answers) == 0 {
		return nil, fmt.Errorf("客观题复核缺少答案或依据")
	}
	out := &ProblemSetQuizReviewResult{SourceArtifacts: []*ArtifactRef{artifact}}
	if *answer.Ambiguous {
		out.Feedback = "题面存在歧义：" + answer.Reason
		return out, nil
	}
	if !sameSetQuizAnswers(in.Type, in.Draft.Answers, answer.Answers) {
		out.Feedback = "独立解答与生成答案不一致：" + answer.Reason
		return out, nil
	}
	out.Approved = true
	return out, nil
}
func validateSetQuizDraft(typ domain.QuizType, d QuizDraft) error {
	if strings.TrimSpace(d.Title) == "" || strings.TrimSpace(d.Statement) == "" || strings.TrimSpace(d.Explanation) == "" || len(d.Answers) == 0 {
		return fmt.Errorf("题目、答案或解析不完整")
	}
	for _, a := range d.Answers {
		if strings.TrimSpace(a) == "" {
			return fmt.Errorf("答案不能为空")
		}
	}
	switch typ {
	case domain.QuizTypeChoice:
		if len(d.Options) < 3 || len(d.Options) > 5 {
			return fmt.Errorf("选择题应有 3-5 个选项")
		}
		labels := map[string]bool{}
		contents := map[string]bool{}
		for _, o := range d.Options {
			key := strings.Join(strings.Fields(o.Content), "")
			if len(o.Label) != 1 || o.Label < "A" || o.Label > "E" || labels[o.Label] || key == "" || contents[key] {
				return fmt.Errorf("选择题选项无效或重复")
			}
			labels[o.Label] = true
			contents[key] = true
		}
		seen := map[string]bool{}
		for _, a := range d.Answers {
			if !labels[a] || seen[a] {
				return fmt.Errorf("选择题答案标签无效或重复")
			}
			seen[a] = true
		}
	case domain.QuizTypeFillBlank:
		if len(d.Options) > 0 || strings.Count(d.Statement, "___") != len(d.Answers) {
			return fmt.Errorf("填空数量与答案不一致")
		}
	case domain.QuizTypeJudge:
		if len(d.Options) > 0 || len(d.Answers) != 1 || (d.Answers[0] != "对" && d.Answers[0] != "错") {
			return fmt.Errorf("判断题答案必须为对或错")
		}
	default:
		return fmt.Errorf("不支持的客观题型")
	}
	return nil
}
func sameSetQuizAnswers(typ domain.QuizType, a, b []string) bool {
	normalize := func(in []string) []string {
		out := make([]string, len(in))
		for i, v := range in {
			out[i] = strings.TrimSpace(v)
		}
		if typ == domain.QuizTypeChoice {
			sort.Strings(out)
		}
		return out
	}
	return reflect.DeepEqual(normalize(a), normalize(b))
}
