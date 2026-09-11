package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/generationapi"
	"github.com/Gingoo-TvT/Qraft/backend/internal/llm"
	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
	algoworkflow "github.com/Gingoo-TvT/Qraft/backend/internal/workflow"
	"github.com/Gingoo-TvT/Qraft/backend/internal/workflow/activities"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
)

type setPlanEntry struct {
	Position       int                   `json:"position"`
	Type           domain.QuizType       `json:"type"`
	Title          string                `json:"title"`
	Brief          string                `json:"brief"`
	Tags           []string              `json:"tags"`
	Level          domain.ProblemLevel   `json:"level,omitempty"`
	Difficulty     int                   `json:"difficulty,omitempty"`
	QuizDifficulty domain.QuizDifficulty `json:"quiz_difficulty,omitempty"`
}

func (s *ProblemSetGenerationService) PrepareProblemSetBatchActivity(ctx context.Context, ref domain.ProblemSetGenerationRef) (*algoworkflow.ProblemSetBatch, error) {
	stop := setGenerationHeartbeat(ctx)
	defer stop()
	set, err := s.sets.Get(ctx, ref.SetID)
	if err != nil {
		return nil, err
	}
	state := set.Generation
	if state == nil || state.ID != ref.RunID || !state.Active() {
		return nil, temporal.NewNonRetryableApplicationError("生成任务已变更或停止", "SetGenerationChanged", nil)
	}
	if err = s.repo.ChangeGeneration(ctx, ref, func(current *domain.ProblemSetGenerationState) error {
		if !current.Active() {
			return repository.ErrProblemSetGenerationChanged
		}
		current.Status = "planning"
		return nil
	}); err != nil {
		return nil, err
	}
	providers, err := s.resolve(ctx)
	if err != nil {
		return nil, err
	}
	if state.Brief == "" {
		response, err := s.completeSetPlanning(ctx, providers.Statement, problemSetPromptSystem, s.sets.buildPromptInput(ctx, set), 4096)
		if err != nil {
			return nil, err
		}
		state.Brief = cleanPrompt(response)
		if state.Brief == "" {
			return nil, fmt.Errorf("整套规划为空")
		}
		if err = s.repo.ChangeGeneration(ctx, ref, func(current *domain.ProblemSetGenerationState) error { current.Brief = state.Brief; return nil }); err != nil {
			return nil, err
		}
		if err = s.sets.repo.UpdateGeneratedPrompt(ctx, set.ID, state.Brief, providers.Statement.Model, time.Now().UTC()); err != nil {
			return nil, err
		}
	}
	slots := make([]domain.ProblemSetGenerationSlot, 0, algoworkflow.ProblemSetBatchSize)
	for _, slot := range state.Slots {
		if slot.Status == "pending" || slot.Status == "ready" {
			slots = append(slots, slot)
		}
		if len(slots) == algoworkflow.ProblemSetBatchSize {
			break
		}
	}
	if len(slots) == 0 {
		return &algoworkflow.ProblemSetBatch{}, nil
	}
	var catalog []domain.TagCategory
	for _, slot := range slots {
		if slot.Type == domain.QuizTypeProgramming {
			catalog, err = s.tags.GetAll(ctx)
			if err != nil {
				return nil, err
			}
			if len(catalog) == 0 {
				return nil, fmt.Errorf("编程题标签目录为空")
			}
			break
		}
	}
	missing := make([]domain.ProblemSetGenerationSlot, 0)
	for _, slot := range slots {
		if slot.Brief == "" && slot.ProblemID == nil && slot.QuizID == nil {
			missing = append(missing, slot)
		}
	}
	if len(missing) > 0 {
		prompt := setBatchPlanPrompt(set, state, missing, catalog)
		var planned []setPlanEntry
		for attempt := 0; attempt < 2; attempt++ {
			text, callErr := s.completeSetPlanning(ctx, providers.Statement, setBatchPlanSystem, prompt, 12000)
			if callErr != nil {
				return nil, callErr
			}
			text = strings.TrimSpace(text)
			text = strings.TrimPrefix(text, "\x60\x60\x60json")
			text = strings.TrimPrefix(text, "\x60\x60\x60")
			text = strings.TrimSuffix(strings.TrimSpace(text), "\x60\x60\x60")
			err = json.Unmarshal([]byte(text), &planned)
			if err == nil {
				err = validateSetBatchPlan(planned, missing, state.Slots, catalog)
			}
			if err == nil {
				break
			}
			prompt += "\n上一版结构或配额错误，请仅重写本批 JSON：" + err.Error()
		}
		if err != nil {
			return nil, temporal.NewNonRetryableApplicationError("题集规划未通过校验："+err.Error(), "InvalidSetPlan", err)
		}
		byPosition := map[int]setPlanEntry{}
		for _, plan := range planned {
			byPosition[plan.Position] = plan
		}
		for i := range slots {
			if plan, ok := byPosition[slots[i].Position]; ok {
				slots[i].Title = plan.Title
				slots[i].Brief = plan.Brief
				slots[i].Tags = plan.Tags
				slots[i].Level = plan.Level
				slots[i].Difficulty = plan.Difficulty
				slots[i].QuizDifficulty = plan.QuizDifficulty
			}
		}
	}
	batch := &algoworkflow.ProblemSetBatch{Slots: make([]algoworkflow.ProblemSetPreparedSlot, 0, len(slots))}
	for i := range slots {
		slot := &slots[i]
		if slot.ChildID != "" && slot.ProblemID == nil && slot.QuizID == nil {
			if err = s.recoverSetSlot(ctx, slot); err != nil {
				return nil, err
			}
		}
		sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%d", ref.RunID, slot.Position)))
		slot.ChildID = "set-quiz-" + hex.EncodeToString(sum[:])
		if slot.Type == domain.QuizTypeProgramming {
			slot.ChildID = generationapi.JobIDPrefix + hex.EncodeToString(sum[:])
		}
		slot.Status = "ready"
		prepared := algoworkflow.ProblemSetPreparedSlot{Slot: *slot, ReviewRuntime: providers.Verification}
		brief := state.Brief + "\n整套原始需求：" + set.GenerationConfig.Requirements + fmt.Sprintf("\n只生成第 %d 题（%s）：%s\n分题任务：%s", slot.Position, slot.Type.ToExcel(), slot.Title, slot.Brief)
		if slot.Error != "" {
			brief += "\n上次失败反馈（仅修正此题）：" + boundedSetError(slot.Error)
		}
		if slot.ProblemID == nil && slot.QuizID == nil {
			if slot.Type == domain.QuizTypeProgramming {
				params := domain.DefaultProblemGenParams()
				params.Level = slot.Level
				params.Tags = append([]string(nil), slot.Tags...)
				params.Difficulty = slot.Difficulty
				params.Locale = "zh"
				params.CustomPrompt = brief
				params.ProviderConfig = providers
				params.MetadataExtras = map[string]interface{}{"problem_set_id": ref.SetID.String(), "problem_set_position": slot.Position}
				if err = s.problems.prepareGenerationJobParams(ctx, &params); err != nil {
					return nil, err
				}
				quality, err := generationJobQualityInput(slot.ChildID, params)
				if err != nil {
					return nil, err
				}
				prepared.Programming = &quality
			} else {
				subject := set.Subject
				switch strings.TrimSpace(subject) {
				case "数据结构与算法":
					subject = domain.QuizSubjectDataStructureAlgorithm
				case "C语言程序设计", "C 语言程序设计":
					subject = domain.QuizSubjectCLanguage
				}
				prepared.Quiz = &activities.QuizGenerateInput{Subject: subject, Type: slot.Type, Difficulty: slot.QuizDifficulty, Count: 1, KnowledgePointNames: slot.Tags, Tags: slot.Tags, Visibility: domain.QuizVisibilityPrivate, CustomPrompt: brief, LLMRuntime: providers.Statement}
			}
		}
		batch.Slots = append(batch.Slots, prepared)
	}
	if err = s.repo.ChangeGeneration(ctx, ref, func(current *domain.ProblemSetGenerationState) error {
		if !current.Active() {
			return repository.ErrProblemSetGenerationChanged
		}
		current.Status = "generating"
		for _, slot := range slots {
			target := current.FindSlot(slot.Position)
			if target == nil {
				return repository.ErrProblemSetGenerationChanged
			}
			if target.Status != "succeeded" {
				*target = slot
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return batch, nil
}
func (s *ProblemSetGenerationService) completeSetPlanning(ctx context.Context, runtime *domain.LLMRuntimeConfig, system, user string, maxTokens int) (string, error) {
	req := &llm.Request{Model: runtime.Model, MaxTokens: maxTokens, System: system, Messages: []llm.Message{{Role: "user", Content: user}}, Runtime: &llm.RuntimeConfig{APIKeyRef: runtime.APIKeyRef, BaseURL: runtime.BaseURL, Provider: runtime.Provider, Protocol: runtime.Protocol}}
	out, err := s.sets.llm.CompleteWithRetry(ctx, req, 2)
	if err != nil {
		return "", err
	}
	if out == nil {
		return "", fmt.Errorf("规划模型返回为空")
	}
	return out.Text(), nil
}
func setBatchPlanPrompt(set *domain.ProblemSet, state *domain.ProblemSetGenerationState, slots []domain.ProblemSetGenerationSlot, catalog []domain.TagCategory) string {
	targets := make([]map[string]interface{}, 0, len(slots))
	for _, slot := range slots {
		targets = append(targets, map[string]interface{}{"position": slot.Position, "type": slot.Type})
	}
	prior := make([]string, 0)
	for _, slot := range state.Slots {
		if slot.Title != "" {
			prior = append(prior, fmt.Sprintf("%d %s %s | %s", slot.Position, slot.Type, slot.Title, strings.Join(slot.Tags, ",")))
		}
	}
	if len(prior) > 80 {
		prior = append(prior[:20], prior[len(prior)-60:]...)
	}
	choices := make([]map[string]interface{}, 0, len(catalog))
	for _, c := range catalog {
		choices = append(choices, map[string]interface{}{"tag": c.TagName, "name": c.DisplayName, "level": c.Level, "min": c.MinDifficulty, "max": c.MaxDifficulty})
	}
	raw, _ := json.Marshal(map[string]interface{}{"set_brief": state.Brief, "requirements": set.GenerationConfig, "difficulty": set.DifficultyPrompt, "subject": set.Subject, "total": set.DesiredItemCount, "targets": targets, "already_planned": prior, "programming_tag_catalog": choices})
	return string(raw)
}

const setBatchPlanSystem = `你是整套试题的统筹命题人。将整套需求分解为指定 targets 的分题任务，只规划这些位置，不增删位置，不修改题型。输出 JSON 数组，每项字段：
{"position":1,"type":"programming|choice|fill_blank|judge","title":"简洁工作标题","brief":"明确的独立命题任务","tags":["知识点"],"level":"algorithm","difficulty":1500,"quiz_difficulty":"medium"}
编程题：level 与 tags 必须精确取自给定目录且属于同一 level，难度必须为 100 的倍数并在所有所选标签范围内；选择 1-3 个真实需要的知识点，任务写明核心思维、可行解法思路、预期复杂度和边界挑战，避免换皮模板。客观题：不用 level/difficulty，quiz_difficulty 为 easy/medium/hard；任务写清考查目标、迷惑选项依据或填空/判断的唯一性条件，不要求标准输入输出或测试点。每题应有独立价值，不互相泄露答案；对照已经规划的题位避免同构重复，按整套自然语言难度组织合理梯度。标题不超过 80 字，brief 为 100-600 字，不输出题目成品。仅输出数组。`

func validateSetBatchPlan(plans []setPlanEntry, targets, previous []domain.ProblemSetGenerationSlot, catalog []domain.TagCategory) error {
	if len(plans) != len(targets) {
		return fmt.Errorf("本批必须恰好 %d 题", len(targets))
	}
	wanted := map[int]domain.QuizType{}
	for _, target := range targets {
		wanted[target.Position] = target.Type
	}
	tagMap := map[string]domain.TagCategory{}
	for _, tag := range catalog {
		tagMap[tag.TagName] = tag
	}
	titles := map[string]bool{}
	briefs := map[string]bool{}
	for _, slot := range previous {
		if slot.Brief != "" {
			titles[strings.TrimSpace(slot.Title)] = true
			briefs[strings.TrimSpace(slot.Brief)] = true
		}
	}
	for _, plan := range plans {
		typ, ok := wanted[plan.Position]
		if !ok || typ != plan.Type {
			return fmt.Errorf("题位或题型不匹配: %d", plan.Position)
		}
		delete(wanted, plan.Position)
		title, brief := strings.TrimSpace(plan.Title), strings.TrimSpace(plan.Brief)
		if title == "" || len([]rune(title)) > 80 || len([]rune(brief)) < 20 || len([]rune(brief)) > 2000 {
			return fmt.Errorf("第 %d 题任务不完整或过长", plan.Position)
		}
		if titles[title] || briefs[brief] {
			return fmt.Errorf("第 %d 题与其他题位重复", plan.Position)
		}
		titles[title] = true
		briefs[brief] = true
		if len(plan.Tags) < 1 || len(plan.Tags) > 4 {
			return fmt.Errorf("第 %d 题缺少明确知识点", plan.Position)
		}
		if plan.Type == domain.QuizTypeProgramming {
			if !plan.Level.IsValid() || plan.Difficulty%100 != 0 {
				return fmt.Errorf("第 %d 题等级或难度无效", plan.Position)
			}
			for _, tag := range plan.Tags {
				entry, ok := tagMap[tag]
				if !ok || entry.Level != plan.Level || plan.Difficulty < entry.MinDifficulty || plan.Difficulty > entry.MaxDifficulty {
					return fmt.Errorf("第 %d 题标签 %s 与等级/难度不兼容", plan.Position, tag)
				}
			}
		} else if !plan.QuizDifficulty.IsValid() {
			return fmt.Errorf("第 %d 题客观题难度无效", plan.Position)
		}
	}
	return nil
}
func setGenerationHeartbeat(ctx context.Context) func() {
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				activity.RecordHeartbeat(ctx, "planning problem-set batch")
			case <-ctx.Done():
				return
			case <-done:
				return
			}
		}
	}()
	return func() { close(done) }
}
