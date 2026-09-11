package handler

import (
	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
	"github.com/labstack/echo/v4"
)

// ---------------------------------------------------------------------------
// StatsHandler
// ---------------------------------------------------------------------------

// StatsHandler exposes the dashboard statistics endpoint. It queries the
// problem repository to compute aggregate counts by status, level, and
// difficulty.
type StatsHandler struct {
	problemRepo *repository.ProblemRepository
}

// NewStatsHandler creates a new StatsHandler with the given problem repository.
func NewStatsHandler(problemRepo *repository.ProblemRepository) *StatsHandler {
	return &StatsHandler{problemRepo: problemRepo}
}

// ---------------------------------------------------------------------------
// HandleStats: GET /stats
// ---------------------------------------------------------------------------

// statsResponse contains aggregate dashboard statistics for the problem bank.
// Field names match the frontend DashboardStats TypeScript interface.
type statsResponse struct {
	TotalProblems       int            `json:"total_problems"`
	PublishedProblems   int            `json:"published_problems"`
	DraftProblems       int            `json:"draft_problems"`
	GeneratingProblems  int            `json:"generating_problems"`
	ReviewProblems      int            `json:"review_problems"`
	RejectedProblems    int            `json:"rejected_problems"`
	QuarantinedProblems int            `json:"quarantined_problems"`
	TotalWorkflows      int            `json:"total_workflows"`
	ActiveWorkflows     int            `json:"active_workflows"`
	SuccessRate         float64        `json:"success_rate"`
	ProblemsByLevel     map[string]int `json:"problems_by_level"`
	ProblemsByDiff      map[string]int `json:"problems_by_difficulty"`
	RecentActivity      []interface{}  `json:"recent_activity"`
}

// HandleStats computes and returns dashboard statistics. It queries the
// problem repository with filters for each dimension and assembles the
// aggregate counts.
func (h *StatsHandler) HandleStats(c echo.Context) error {
	ctx := c.Request().Context()

	// Get total count by listing without filters.
	_, total, err := h.problemRepo.List(ctx, repository.ProblemFilter{
		Limit: 1,
	})
	if err != nil {
		return internalError(c, "failed to compute stats: "+err.Error())
	}

	// Count by status.
	statuses := []string{
		string(domain.ProblemStatusDraft),
		string(domain.ProblemStatusGenerating),
		string(domain.ProblemStatusReview),
		string(domain.ProblemStatusPublished),
		string(domain.ProblemStatusRejected),
		string(domain.ProblemStatusQuarantined),
	}
	byStatus := make(map[string]int, len(statuses))
	for _, s := range statuses {
		status := s
		_, count, err := h.problemRepo.List(ctx, repository.ProblemFilter{
			Status: &status,
			Limit:  1,
		})
		if err != nil {
			return internalError(c, "failed to compute stats by status: "+err.Error())
		}
		byStatus[s] = count
	}

	// Count by level.
	levels := []domain.ProblemLevel{domain.LevelSyntax, domain.LevelAlgorithm}
	byLevel := make(map[string]int, len(levels))
	for _, l := range levels {
		level := l
		_, count, err := h.problemRepo.List(ctx, repository.ProblemFilter{
			Level: &level,
			Limit: 1,
		})
		if err != nil {
			return internalError(c, "failed to compute stats by level: "+err.Error())
		}
		byLevel[string(l)] = count
	}

	// Count by difficulty range buckets.
	diffBuckets := map[string][2]int{
		"800-1200":  {800, 1200},
		"1300-1800": {1300, 1800},
		"1900-2400": {1900, 2400},
		"2500-3500": {2500, 3500},
	}
	byDifficulty := make(map[string]int, len(diffBuckets))
	for label, bounds := range diffBuckets {
		minD := bounds[0]
		maxD := bounds[1]
		_, count, err := h.problemRepo.List(ctx, repository.ProblemFilter{
			MinDiff: &minD,
			MaxDiff: &maxD,
			Limit:   1,
		})
		if err != nil {
			return internalError(c, "failed to compute stats by difficulty: "+err.Error())
		}
		byDifficulty[label] = count
	}

	// Compute success rate (published / total, avoid division by zero).
	var successRate float64
	if total > 0 {
		successRate = float64(byStatus[string(domain.ProblemStatusPublished)]) / float64(total)
	}

	resp := statsResponse{
		TotalProblems:       total,
		PublishedProblems:   byStatus[string(domain.ProblemStatusPublished)],
		DraftProblems:       byStatus[string(domain.ProblemStatusDraft)],
		GeneratingProblems:  byStatus[string(domain.ProblemStatusGenerating)],
		ReviewProblems:      byStatus[string(domain.ProblemStatusReview)],
		RejectedProblems:    byStatus[string(domain.ProblemStatusRejected)],
		QuarantinedProblems: byStatus[string(domain.ProblemStatusQuarantined)],
		TotalWorkflows:      0,
		ActiveWorkflows:     0,
		SuccessRate:         successRate,
		ProblemsByLevel:     byLevel,
		ProblemsByDiff:      byDifficulty,
		RecentActivity:      []interface{}{},
	}

	return ok(c, resp)
}
