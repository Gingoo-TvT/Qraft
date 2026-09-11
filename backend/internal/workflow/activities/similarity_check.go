package activities

import (
	"context"
	"fmt"
	"strings"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"go.temporal.io/sdk/activity"
)

// SimilarityCheckActivity checks whether the proposed problem is too similar
// to existing problems in the database. It generates an embedding for the
// problem description and queries the vector store for near-duplicates.
func (a *Activities) SimilarityCheckActivity(ctx context.Context, params domain.ProblemGenParams) (*SimilarityCheckResult, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("starting similarity check",
		"level", params.Level,
		"difficulty", params.Difficulty,
		"tags", params.Tags,
	)

	queryText := buildSimilarityQuery(params)
	report := newPreGenerationDedupReport(params)

	modelVersionID, err := a.deps.configuredStatementModelVersion()
	if err != nil {
		return nil, newDedupDependencyError(
			report,
			DedupFailureActiveModelUnavailable,
			dedupActiveModelUnavailableErrorType,
			"configured statement embedding model is unavailable",
			err,
		)
	}
	report.ModelVersion = modelVersionID.String()

	activity.RecordHeartbeat(ctx, "generating embedding")
	vec, err := a.cachedTemporalProblemEmbedding(ctx, "similarity-check", queryText)
	if err != nil {
		return nil, newDedupDependencyError(
			report,
			DedupFailureEmbeddingUnavailable,
			dedupEmbeddingUnavailableErrorType,
			"generating required pre-generation similarity embedding is unavailable",
			err,
		)
	}

	activity.RecordHeartbeat(ctx, "querying pgvector")
	if a.deps.VectorRepo == nil {
		return nil, newDedupDependencyError(
			report,
			DedupFailureVectorQueryUnavailable,
			dedupVectorQueryUnavailableErrorType,
			"required pre-generation similarity index is unavailable",
			fmt.Errorf("vector repository is not configured"),
		)
	}
	similarProblems, scores, err := a.deps.VectorRepo.FindSimilarForVersion(
		ctx, vec, modelVersionID, report.Kind, report.TopK, SimilarityParamLevelThreshold,
	)
	if err != nil {
		return nil, newDedupDependencyError(
			report,
			DedupFailureVectorQueryUnavailable,
			dedupVectorQueryUnavailableErrorType,
			"required pre-generation similarity index is unavailable",
			err,
		)
	}

	result := &SimilarityCheckResult{
		TooSimilar:      false,
		SimilarProblems: make([]SimilarProblem, 0, len(similarProblems)),
		Neighbors:       make([]NeighborInfo, 0, len(similarProblems)),
	}

	for i, p := range similarProblems {
		result.SimilarProblems = append(result.SimilarProblems, SimilarProblem{
			ID:         p.ID,
			Title:      p.Title,
			Similarity: scores[i],
		})
		result.Neighbors = append(result.Neighbors, NeighborInfo{
			ID:          p.ID,
			Title:       p.Title,
			OneLineHint: p.OneLineHint,
			Tags:        p.Tags,
			Similarity:  scores[i],
		})
	}
	setDedupNeighborCount(report, len(result.Neighbors))
	if len(scores) > 0 {
		report.MaxSimilarity = scores[0]
	}

	if params.SimilarLimit > 0 && len(result.SimilarProblems) > params.SimilarLimit {
		result.TooSimilar = true
		report.Decision = DedupDecisionRejected
	}
	result.Report = report

	logger.Info("similarity check completed",
		"neighbors", len(result.Neighbors),
		"too_similar", result.TooSimilar,
	)

	return result, nil
}

// buildSimilarityQuery constructs a textual query from generation parameters
// that can be used for similarity searching.
func buildSimilarityQuery(params domain.ProblemGenParams) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Competitive programming problem at %s level, ", params.Level))
	sb.WriteString(fmt.Sprintf("difficulty rating %d. ", params.Difficulty))

	if len(params.Tags) > 0 {
		sb.WriteString(fmt.Sprintf("Topics: %s. ", strings.Join(params.Tags, ", ")))
	}
	if contract := params.KnowledgePointCombination; contract != nil {
		sb.WriteString(fmt.Sprintf("Knowledge-point combination %s uses %s mode with ordered canonical slugs [%s] and max_concepts %d. ",
			contract.SchemaVersion, contract.Mode, strings.Join(params.Tags, ", "), contract.MaxConcepts))
	}
	if params.ContestStyle != "" {
		sb.WriteString(fmt.Sprintf("Contest style: %s. ", params.ContestStyle))
	}
	if params.CustomPrompt != "" {
		sb.WriteString(fmt.Sprintf("Additional context: %s", params.CustomPrompt))
	}

	return sb.String()
}
