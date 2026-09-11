package activities

import (
	"context"
	"fmt"

	"go.temporal.io/sdk/activity"
)

// PostStatementSimilarityActivity checks generated statement text against
// stored problem embeddings and flags near-duplicates.
func (a *Activities) PostStatementSimilarityActivity(
	ctx context.Context,
	stmt StatementResult,
) (*PostStatementSimilarityResult, error) {
	logger := activity.GetLogger(ctx)
	text := buildEmbeddingText(stmt.Title, stmt.Statement, stmt.OneLineHint)
	report := newPostStatementDedupReport(stmt)
	out := &PostStatementSimilarityResult{Report: report}

	if a == nil || a.deps == nil {
		return nil, newDedupDependencyError(
			report,
			DedupFailureVectorQueryUnavailable,
			dedupVectorQueryUnavailableErrorType,
			"required post-statement duplicate index is unavailable",
			fmt.Errorf("problem repository is not configured"),
		)
	}
	titleLookup := a.deps.ProblemTitleLookup
	if titleLookup == nil {
		titleLookup = a.deps.ProblemRepo
	}
	if titleLookup == nil {
		return nil, newDedupDependencyError(
			report,
			DedupFailureVectorQueryUnavailable,
			dedupVectorQueryUnavailableErrorType,
			"required post-statement duplicate index is unavailable",
			fmt.Errorf("problem title lookup is not configured"),
		)
	}
	titleMatches, err := titleLookup.FindByTitle(ctx, stmt.Title, 5)
	if err != nil {
		return nil, newDedupDependencyError(
			report,
			DedupFailureVectorQueryUnavailable,
			dedupVectorQueryUnavailableErrorType,
			"required post-statement duplicate index is unavailable",
			err,
		)
	}
	for _, p := range titleMatches {
		out.Neighbors = append(out.Neighbors, NeighborInfo{
			ID:          p.ID,
			Title:       p.Title,
			OneLineHint: p.OneLineHint,
			Tags:        p.Tags,
			Similarity:  1,
		})
	}
	if len(titleMatches) > 0 {
		modelVersionID, modelErr := a.deps.configuredStatementModelVersion()
		if modelErr != nil {
			return nil, newDedupDependencyError(
				report,
				DedupFailureActiveModelUnavailable,
				dedupActiveModelUnavailableErrorType,
				"configured statement embedding model is unavailable",
				modelErr,
			)
		}
		report.ModelVersion = modelVersionID.String()
		out.MaxSimilarity = 1
		out.HardReject = true
		report.Decision = DedupDecisionRejected
		setDedupNeighborCount(report, len(out.Neighbors))
		report.MaxSimilarity = out.MaxSimilarity
		report.Reason = "duplicate_title"
		logger.Warn("duplicate problem title detected",
			"title", stmt.Title,
			"matches", len(titleMatches),
		)
		return out, nil
	}

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

	vec, err := a.cachedTemporalProblemEmbedding(ctx, "post-statement-similarity", text)
	if err != nil {
		return nil, newDedupDependencyError(
			report,
			DedupFailureEmbeddingUnavailable,
			dedupEmbeddingUnavailableErrorType,
			"generating required post-statement similarity embedding is unavailable",
			err,
		)
	}

	if a.deps.VectorRepo == nil {
		return nil, newDedupDependencyError(
			report,
			DedupFailureVectorQueryUnavailable,
			dedupVectorQueryUnavailableErrorType,
			"required post-statement similarity index is unavailable",
			fmt.Errorf("vector repository is not configured"),
		)
	}
	problems, scores, err := a.deps.VectorRepo.FindSimilarForVersion(
		ctx, vec, modelVersionID, report.Kind, SimilarityStatementTopK, SimilarityStatementWarnThreshold,
	)
	if err != nil {
		return nil, newDedupDependencyError(
			report,
			DedupFailureVectorQueryUnavailable,
			dedupVectorQueryUnavailableErrorType,
			"required post-statement similarity index is unavailable",
			err,
		)
	}

	for i, p := range problems {
		out.Neighbors = append(out.Neighbors, NeighborInfo{
			ID:          p.ID,
			Title:       p.Title,
			OneLineHint: p.OneLineHint,
			Tags:        p.Tags,
			Similarity:  scores[i],
		})
		if scores[i] > out.MaxSimilarity {
			out.MaxSimilarity = scores[i]
		}
	}

	if out.MaxSimilarity >= SimilarityStatementRejectThreshold {
		out.HardReject = true
		report.Decision = DedupDecisionRejected
	} else if out.MaxSimilarity >= SimilarityStatementWarnThreshold {
		out.Warning = true
		report.Decision = DedupDecisionWarn
	}
	setDedupNeighborCount(report, len(out.Neighbors))
	report.MaxSimilarity = out.MaxSimilarity

	logger.Info("post-statement similarity check",
		"max_similarity", out.MaxSimilarity,
		"neighbors", len(out.Neighbors),
		"hard_reject", out.HardReject,
	)
	return out, nil
}
