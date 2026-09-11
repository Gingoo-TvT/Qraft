package service

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
	"github.com/google/uuid"
)

type problemSetAssemblyStore interface {
	ListAssemblyCandidates(context.Context, domain.ProblemSetAssemblyFilter, []domain.QuizType) ([]domain.ProblemSetAssemblyCandidate, error)
	CreateAssembled(context.Context, *domain.ProblemSet, []domain.ProblemSetAssemblyRef) error
}
type ProblemSetAssemblyRequest struct {
	Config domain.ProblemSetGenerationConfig `json:"config"`
	Filter domain.ProblemSetAssemblyFilter   `json:"filter"`
}
type ProblemSetAssembleRequest struct {
	ProblemSetAssemblyRequest
	Title       string                         `json:"title"`
	Code        string                         `json:"code"`
	Description string                         `json:"description"`
	Kind        domain.ProblemSetKind          `json:"kind"`
	Subject     string                         `json:"subject"`
	Items       []domain.ProblemSetAssemblyRef `json:"items"`
	CreatedBy   string                         `json:"-"`
}

func normalizeAssemblyRequest(req *ProblemSetAssemblyRequest) (int, []domain.QuizType, error) {
	total := 0
	types := []domain.QuizType{}
	for _, q := range req.Config.Distribution {
		total += q.Count
		if q.Count > 0 {
			types = append(types, q.Type)
		}
	}
	if err := req.Config.Validate(total); err != nil {
		return 0, nil, fmt.Errorf("validation: %w", err)
	}
	if err := req.Filter.Validate(); err != nil {
		return 0, nil, fmt.Errorf("validation: %w", err)
	}
	if req.Filter.Seed == "" {
		req.Filter.Seed = uuid.NewString()
	}
	req.Config.Assembly = &req.Filter
	return total, types, nil
}

func (s *ProblemSetService) PreviewAssembly(ctx context.Context, req ProblemSetAssemblyRequest) (*domain.ProblemSetAssemblyPreview, error) {
	_, types, err := normalizeAssemblyRequest(&req)
	if err != nil {
		return nil, err
	}
	store, ok := s.repo.(problemSetAssemblyStore)
	if !ok {
		return nil, fmt.Errorf("problem set assembly is unavailable")
	}
	candidates, err := store.ListAssemblyCandidates(ctx, req.Filter, types)
	if err != nil {
		return nil, err
	}
	return selectAssemblyCandidates(req, candidates), nil
}

// Shuffle once, prefer new coverage axes, then fill remaining slots. This is
// O(N log N + N*tags), independent of the requested number of questions.
func selectAssemblyCandidates(req ProblemSetAssemblyRequest, candidates []domain.ProblemSetAssemblyCandidate) *domain.ProblemSetAssemblyPreview {
	ranked := append([]domain.ProblemSetAssemblyCandidate(nil), candidates...)
	ranks := map[string]string{}
	key := func(c domain.ProblemSetAssemblyCandidate) string { return string(c.Type) + ":" + c.ID.String() }
	for _, c := range ranked {
		h := sha256.Sum256([]byte(req.Filter.Seed + key(c)))
		ranks[key(c)] = string(h[:])
	}
	sort.Slice(ranked, func(i, j int) bool { return ranks[key(ranked[i])] < ranks[key(ranked[j])] })
	out := &domain.ProblemSetAssemblyPreview{Filter: req.Filter, Items: []domain.ProblemSetAssemblyCandidate{}, Distribution: []domain.ProblemSetAssemblyQuotaResult{}}
	coverage := map[string]bool{}
	for _, quota := range req.Config.Distribution {
		if quota.Count == 0 {
			continue
		}
		pool := []domain.ProblemSetAssemblyCandidate{}
		seen := map[string]bool{}
		for _, c := range ranked {
			if c.Type != quota.Type {
				continue
			}
			fp := c.Fingerprint
			if fp == "" {
				fp = c.ID.String()
			}
			if seen[fp] {
				continue
			}
			seen[fp] = true
			pool = append(pool, c)
		}
		chosen := []domain.ProblemSetAssemblyCandidate{}
		chosenIDs := map[uuid.UUID]bool{}
		take := func(c domain.ProblemSetAssemblyCandidate) {
			c.Score = quota.Score
			chosen = append(chosen, c)
			chosenIDs[c.ID] = true
			for _, tag := range c.Tags {
				coverage[strings.ToLower(strings.TrimSpace(tag))] = true
			}
		}
		for _, c := range pool {
			if len(chosen) >= quota.Count {
				break
			}
			novel := false
			for _, tag := range c.Tags {
				tag = strings.ToLower(strings.TrimSpace(tag))
				if tag != "" && !coverage[tag] {
					novel = true
					break
				}
			}
			if novel {
				take(c)
			}
		}
		for _, c := range pool {
			if len(chosen) >= quota.Count {
				break
			}
			if !chosenIDs[c.ID] {
				take(c)
			}
		}
		sort.SliceStable(chosen, func(i, j int) bool {
			if quota.Type == domain.QuizTypeProgramming {
				return chosen[i].Difficulty < chosen[j].Difficulty
			}
			return chosen[i].QuizDifficulty.Score() < chosen[j].QuizDifficulty.Score()
		})
		missing := quota.Count - len(chosen)
		out.Items = append(out.Items, chosen...)
		out.Distribution = append(out.Distribution, domain.ProblemSetAssemblyQuotaResult{Type: quota.Type, Requested: quota.Count, Available: len(pool), Selected: len(chosen), Missing: missing})
		out.MissingCount += missing
		out.TotalScore += len(chosen) * quota.Score
	}
	return out
}

func (s *ProblemSetService) Assemble(ctx context.Context, req ProblemSetAssembleRequest) (*domain.ProblemSet, error) {
	total, types, err := normalizeAssemblyRequest(&req.ProblemSetAssemblyRequest)
	if err != nil {
		return nil, err
	}
	if len(req.Items) == 0 || len(req.Items) > total {
		return nil, fmt.Errorf("validation: 请先预览并选择 1-%d 道题；库存不足可保存已有题目", total)
	}
	store, ok := s.repo.(problemSetAssemblyStore)
	if !ok {
		return nil, fmt.Errorf("problem set assembly is unavailable")
	}
	id := uuid.New()
	code := strings.TrimSpace(req.Code)
	if code == "" {
		code = "SET-" + strings.ToUpper(id.String()[:8])
	}
	set := &domain.ProblemSet{ID: id, Code: code, Title: req.Title, Description: req.Description, Kind: req.Kind,
		Visibility: domain.ProblemSetVisibilityPrivate, Subject: req.Subject, Tags: req.Filter.Tags,
		DesiredItemCount: total, Status: domain.ProblemSetStatusDraft, CreatedBy: req.CreatedBy, GenerationConfig: &req.Config}
	if set.Kind == "" {
		set.Kind = domain.ProblemSetKindMockExam
	}
	if set.CreatedBy == "" {
		set.CreatedBy = "api"
	}
	if err := set.NormalizeProblemSet(); err != nil {
		return nil, fmt.Errorf("validation: %w", err)
	}
	candidates, err := store.ListAssemblyCandidates(ctx, req.Filter, types)
	if err != nil {
		return nil, err
	}
	byID := map[string]domain.ProblemSetAssemblyCandidate{}
	for _, c := range candidates {
		byID[string(c.Type)+":"+c.ID.String()] = c
	}
	quotas := map[domain.QuizType]domain.ProblemSetTypeQuota{}
	for _, q := range req.Config.Distribution {
		quotas[q.Type] = q
	}
	counts := map[domain.QuizType]int{}
	seenIDs, seenContent := map[string]bool{}, map[string]bool{}
	for i, ref := range req.Items {
		key := string(ref.Type) + ":" + ref.ID.String()
		c, exists := byID[key]
		if !exists || ref.UpdatedAt.IsZero() || !ref.UpdatedAt.Equal(c.UpdatedAt) {
			return nil, fmt.Errorf("%w: %s", ErrConflict, repository.ErrProblemSetAssemblyChanged)
		}
		fp := string(ref.Type) + ":" + c.Fingerprint
		if c.Fingerprint == "" {
			fp = key
		}
		counts[ref.Type]++
		if seenIDs[key] || seenContent[fp] || counts[ref.Type] > quotas[ref.Type].Count {
			return nil, fmt.Errorf("validation: 选题重复或超出题型配额")
		}
		seenIDs[key], seenContent[fp] = true, true
		item := domain.ProblemSetItem{Position: i + 1, Score: quotas[ref.Type].Score, Section: ref.Type.ToExcel()}
		sourceID := ref.ID
		if ref.Type == domain.QuizTypeProgramming {
			item.ProblemID = &sourceID
		} else {
			item.QuizID = &sourceID
		}
		set.Items = append(set.Items, item)
		set.TotalScore += item.Score
	}
	if err := store.CreateAssembled(ctx, set, req.Items); err != nil {
		return nil, mapProblemSetPersistenceError(err)
	}
	return s.Get(ctx, set.ID)
}
