package activities

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/llm"
	"github.com/Gingoo-TvT/Qraft/backend/internal/llm/prompts"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
)

func (a *Activities) GenerateQuizActivity(ctx context.Context, in QuizGenerateInput) (*QuizGenerateResult, error) {
	logger := activity.GetLogger(ctx)
	if in.Type == domain.QuizTypeProgramming {
		return nil, temporal.NewNonRetryableApplicationError("quiz generation does not support programming type", "InvalidQuizType", nil)
	}
	system, user, err := prompts.RenderQuizPrompt(prompts.QuizPromptInput{
		Type:              string(in.Type),
		TypeChinese:       in.Type.ToExcel(),
		Subject:           in.Subject,
		SubjectChinese:    subjectChinese(in.Subject),
		Difficulty:        string(in.Difficulty),
		DifficultyChinese: in.Difficulty.ToExcel(),
		Count:             in.Count,
		KnowledgePoints:   in.KnowledgePointNames,
		Tags:              in.Tags,
		CustomPrompt:      in.CustomPrompt,
	})
	if err != nil {
		return nil, err
	}
	req := &llm.Request{
		MaxTokens: 4096,
		System:    system,
		Messages:  []llm.Message{{Role: "user", Content: user}},
	}
	applyLLMRuntime(req, in.LLMRuntime)
	stopHB := heartbeatWhile(ctx, "calling LLM to generate quiz", 15*time.Second)
	resp, sourceArtifact, err := a.completeLLMWithProvenance(ctx, "quiz", req, 2)
	stopHB()
	if err != nil {
		return nil, wrapRequiredProviderEffectError("llm call for quiz generation", err)
	}
	drafts, err := parseQuizDrafts(resp.Text())
	if err != nil {
		return nil, err
	}
	if len(drafts) != in.Count {
		logger.Warn("quiz draft count mismatch", "got", len(drafts), "want", in.Count)
		if len(drafts) > in.Count {
			drafts = drafts[:in.Count]
		}
	}
	return &QuizGenerateResult{
		SourceArtifacts: []*ArtifactRef{sourceArtifact},
		Drafts:          drafts,
	}, nil
}

func parseQuizDrafts(text string) ([]QuizDraft, error) {
	text = strings.TrimSpace(text)
	var drafts []QuizDraft
	if err := json.Unmarshal([]byte(text), &drafts); err == nil {
		return drafts, nil
	}
	if block := extractJSONBlock(text); block != "" {
		if err := json.Unmarshal([]byte(block), &drafts); err == nil {
			return drafts, nil
		}
	}
	if outer := extractOutermostJSON(text); outer != "" {
		if err := json.Unmarshal([]byte(outer), &drafts); err == nil {
			return drafts, nil
		}
	}
	return nil, fmt.Errorf("failed to parse quiz drafts JSON")
}

func subjectChinese(subject string) string {
	switch subject {
	case domain.QuizSubjectCLanguage:
		return "C语言程序设计"
	case domain.QuizSubjectDataStructureAlgorithm:
		return "数据结构与算法"
	default:
		return subject
	}
}
