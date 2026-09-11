package workflow

import (
	"fmt"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/google/uuid"
	"go.temporal.io/sdk/workflow"
)

// GPLTBatchGenerationWorkflow orchestrates the one-click generation of a full
// 团体程序设计天梯赛 problem set (15 problems: 8×L1 + 4×L2 + 3×L3).
//
// It does NOT reimplement the generation pipeline. Instead it launches one
// ProblemGenerationWorkflow per problem as a child workflow, with a fixed
// concurrency cap so that the LLM proxy and Temporal worker are not
// overwhelmed. Each child receives tailored ProblemGenParams (tier-specific
// level, difficulty, test-data config with per-group scoring) plus a
// MetadataExtras map carrying {gplt_batch_id, gplt_tier} so that downstream
// storage tags each problem with its batch provenance.
//
// A single child failure does not abort the batch; results are collected and
// reported in the returned GPLTBatchState.
func GPLTBatchGenerationWorkflow(ctx workflow.Context, params domain.GPLTBatchParams) (*domain.GPLTBatchState, error) {
	logger := workflow.GetLogger(ctx)

	if err := params.Validate(); err != nil {
		return nil, fmt.Errorf("invalid batch params: %w", err)
	}

	if params.Locale == "" {
		params.Locale = "zh"
	}
	if params.BatchID == "" {
		// The workflow is deterministic, so generate via SideEffect.
		var bid string
		if err := workflow.SideEffect(ctx, func(workflow.Context) interface{} {
			return uuid.New().String()
		}).Get(&bid); err != nil {
			return nil, fmt.Errorf("generating batch id: %w", err)
		}
		params.BatchID = bid
	}

	logger.Info("GPLTBatchGenerationWorkflow started",
		"batch_id", params.BatchID,
		"total_problems", 15,
	)

	// Build the 15 problem specs (8 L1, 4 L2, 3 L3).
	specs := buildGPLTSpecs(params)

	// Controlled concurrency: at most 3 child workflows in flight.
	const maxConcurrency = 3

	results := make([]domain.GPLTProblemResult, len(specs))
	selector := workflow.NewSelector(ctx)

	type future struct {
		idx    int
		future workflow.ChildWorkflowFuture
	}

	var (
		next      int
		inFlight  int
		completed int
		succeeded int
		failed    int
	)

	launch := func(i int) {
		spec := specs[i]
		childID := fmt.Sprintf("gplt-%s-%d-%s", params.BatchID, i, spec.Tier)
		cwOpts := workflow.ChildWorkflowOptions{
			WorkflowID:               childID,
			WorkflowExecutionTimeout: 2 * time.Hour,
			WorkflowRunTimeout:       2 * time.Hour,
			WorkflowTaskTimeout:      time.Minute,
		}
		childCtx := workflow.WithChildOptions(ctx, cwOpts)
		fut := workflow.ExecuteChildWorkflow(childCtx, "ProblemGenerationWorkflow", spec.Params)

		results[i] = domain.GPLTProblemResult{
			Tier:       spec.Tier,
			Level:      spec.Params.Level,
			Difficulty: spec.Params.Difficulty,
			Status:     domain.WorkflowStatusRunning,
			WorkflowID: childID,
		}

		selector.AddFuture(fut, func(f workflow.Future) {
			var state domain.WorkflowState
			err := f.Get(ctx, &state)
			completed++
			inFlight--
			if err != nil {
				failed++
				results[i].Status = domain.WorkflowStatusFailed
				results[i].Error = err.Error()
				logger.Warn("GPLT child workflow failed",
					"index", i, "tier", spec.Tier, "error", err)
				return
			}
			succeeded++
			results[i].Status = state.Status
			if state.Error != "" {
				results[i].Error = state.Error
			}
		})
	}

	// Prime the pipeline up to maxConcurrency.
	for next < len(specs) && inFlight < maxConcurrency {
		launch(next)
		inFlight++
		next++
	}

	// Drain: whenever a child completes, launch the next until all done.
	for completed < len(specs) {
		selector.Select(ctx)
		for next < len(specs) && inFlight < maxConcurrency {
			launch(next)
			inFlight++
			next++
		}
	}

	logger.Info("GPLTBatchGenerationWorkflow finished",
		"batch_id", params.BatchID,
		"succeeded", succeeded,
		"failed", failed,
	)

	return &domain.GPLTBatchState{
		BatchID:   params.BatchID,
		Total:     len(specs),
		Succeeded: succeeded,
		Failed:    failed,
		Results:   results,
	}, nil
}

// gpltSpec bundles a tier with its concrete ProblemGenParams.
type gpltSpec struct {
	Tier   domain.GPLTTier
	Score  int
	Params domain.ProblemGenParams
}

// buildGPLTSpecs constructs the 15 problem specs following the canonical
// PAT-GPLT distribution:
//   - L1: 8 problems with scores 5/5/10/10/15/15/20/20 (total 100)
//   - L2: 4 problems × 25 points (total 100)
//   - L3: 3 problems × 30 points (total 90)
//
// Difficulty is chosen based on each problem's score (for L1) or spread across
// the tier range (for L2/L3). L2 difficulty deliberately overlaps with L1 —
// real 天梯赛 L2 problems can be implementation-lighter than a heavy L1
// simulation; the distinction is conceptual (data structures / algorithms)
// rather than strictly harder.
func buildGPLTSpecs(batch domain.GPLTBatchParams) []gpltSpec {
	tiers := []struct {
		tier  domain.GPLTTier
		count int
	}{
		{domain.GPLTTierL1, domain.GPLTStandardDistribution[domain.GPLTTierL1]},
		{domain.GPLTTierL2, domain.GPLTStandardDistribution[domain.GPLTTierL2]},
		{domain.GPLTTierL3, domain.GPLTStandardDistribution[domain.GPLTTierL3]},
	}

	total := 0
	for _, t := range tiers {
		total += t.count
	}

	specs := make([]gpltSpec, 0, total)
	for _, t := range tiers {
		scores := domain.ScoresForTier(t.tier)
		minD, maxD := domain.DifficultyRange(t.tier.Level())
		for i := 0; i < t.count; i++ {
			score := 0
			if i < len(scores) {
				score = scores[i]
			}

			var diff int
			if t.tier == domain.GPLTTierL1 {
				// L1 difficulty tracks the problem's score band.
				diff = domain.DefaultDifficultyForL1Score(score)
			} else {
				diff = spreadDifficulty(minD, maxD, i, t.count)
			}

			extras := map[string]interface{}{
				"gplt_batch_id":   batch.BatchID,
				"gplt_tier":       string(t.tier),
				"gplt_tier_index": i + 1,
				"gplt_score":      score,
			}

			p := domain.ProblemGenParams{
				Level:             t.tier.Level(),
				Difficulty:        diff,
				Tags:              nil,
				ContestStyle:      "gplt",
				TimeLimit:         batch.TimeLimit,
				MemoryLimit:       batch.MemoryLimit,
				TestDataConfig:    t.tier.TestDataConfig(score),
				RequireReview:     batch.RequireReview,
				GenerateEditorial: batch.GenerateEditorial,
				Languages:         batch.Languages,
				Locale:            batch.Locale,
				ProviderConfig:    batch.ProviderConfig,
				SimilarLimit:      batch.SimilarLimit,
				CustomPrompt:      batch.CustomPrompt,
				MetadataExtras:    extras,
			}
			specs = append(specs, gpltSpec{Tier: t.tier, Score: score, Params: p})
		}
	}
	return specs
}

// spreadDifficulty picks a difficulty inside [min, max] (step 100) such that n
// problems in a tier cover the range evenly. Index is 0-based.
func spreadDifficulty(min, max, idx, n int) int {
	if n <= 1 {
		// Use the tier's mid-band.
		mid := (min + max) / 2
		return roundToHundred(mid, min, max)
	}
	step := (max - min) / (n - 1)
	d := min + step*idx
	return roundToHundred(d, min, max)
}

func roundToHundred(d, min, max int) int {
	d = (d / 100) * 100
	if d < min {
		d = ((min + 99) / 100) * 100
	}
	if d > max {
		d = (max / 100) * 100
	}
	return d
}
