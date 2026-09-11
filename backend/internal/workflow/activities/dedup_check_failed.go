package activities

import (
	"errors"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
	"go.temporal.io/sdk/temporal"
)

const (
	DedupFailureEmbeddingUnavailable   = "embedding_unavailable"
	DedupFailureActiveModelUnavailable = "active_model_unavailable"
	DedupFailureVectorQueryUnavailable = "vector_query_unavailable"
	DedupFailureDependencyUnavailable  = "dependency_unavailable"

	dedupEmbeddingUnavailableErrorType   = "DedupEmbeddingUnavailable"
	dedupActiveModelUnavailableErrorType = "DedupActiveModelUnavailable"
	dedupVectorQueryUnavailableErrorType = "DedupVectorQueryUnavailable"
)

func newPreGenerationDedupReport(params domain.ProblemGenParams) *DedupReport {
	queryText := buildSimilarityQuery(params)
	topK := SimilarityParamTopK
	if params.SimilarLimit+5 > topK {
		topK = params.SimilarLimit + 5
	}
	return &DedupReport{
		SchemaVersion: DedupReportSchemaVersion,
		Stage:         DedupStagePreGeneration,
		Kind:          repository.EmbeddingKindStatement,
		ContentHash:   sha256Bytes([]byte(queryText)),
		TopK:          topK,
		Threshold:     SimilarityParamLevelThreshold,
		Decision:      DedupDecisionPass,
		SimilarLimit:  params.SimilarLimit,
	}
}

func newPostStatementDedupReport(stmt StatementResult) *DedupReport {
	text := buildEmbeddingText(stmt.Title, stmt.Statement, stmt.OneLineHint)
	return &DedupReport{
		SchemaVersion:   DedupReportSchemaVersion,
		Stage:           DedupStagePostStatement,
		Kind:            repository.EmbeddingKindStatement,
		ContentHash:     sha256Bytes([]byte(text)),
		TopK:            SimilarityStatementTopK,
		Threshold:       SimilarityStatementWarnThreshold,
		RejectThreshold: SimilarityStatementRejectThreshold,
		Decision:        DedupDecisionPass,
	}
}

func setDedupNeighborCount(report *DedupReport, count int) {
	if report == nil {
		return
	}
	report.NeighborCount = &count
}

func markDedupCheckFailed(report *DedupReport, reason string) {
	if report == nil {
		return
	}
	report.Decision = DedupDecisionCheckFailed
	report.NeighborCount = nil
	report.MaxSimilarity = 0
	report.Reason = reason
}

// newDedupDependencyError preserves provider-effect lease retries, but turns
// every terminal dependency failure into a stable, secret-free application
// error with the structured report attached as details.
func newDedupDependencyError(
	report *DedupReport,
	reason string,
	errorType string,
	message string,
	cause error,
) error {
	var applicationErr *temporal.ApplicationError
	if errors.As(cause, &applicationErr) && applicationErr.Type() == providerEffectBusyErrorType {
		return cause
	}
	markDedupCheckFailed(report, reason)
	if report == nil {
		return temporal.NewApplicationError(message, errorType)
	}
	return temporal.NewApplicationError(message, errorType, *report)
}

// DedupCheckFailedReportFromError extracts the structured failure report from
// a Temporal activity error. It deliberately never copies the provider error
// message into the report.
func DedupCheckFailedReportFromError(err error) (*DedupReport, bool) {
	var applicationErr *temporal.ApplicationError
	if !errors.As(err, &applicationErr) {
		return nil, false
	}
	reason := dedupFailureReasonForErrorType(applicationErr.Type())
	if reason == "" {
		return nil, false
	}
	var report DedupReport
	if detailsErr := applicationErr.Details(&report); detailsErr == nil {
		markDedupCheckFailed(&report, reason)
		return &report, true
	}
	return &DedupReport{Decision: DedupDecisionCheckFailed, Reason: reason}, true
}

func dedupFailureReasonForErrorType(errorType string) string {
	switch errorType {
	case dedupEmbeddingUnavailableErrorType:
		return DedupFailureEmbeddingUnavailable
	case dedupActiveModelUnavailableErrorType:
		return DedupFailureActiveModelUnavailable
	case dedupVectorQueryUnavailableErrorType:
		return DedupFailureVectorQueryUnavailable
	default:
		return ""
	}
}

func DedupReportCheckFailed(report *DedupReport) bool {
	return report != nil && report.Decision == DedupDecisionCheckFailed
}

// NewPreGenerationDedupCheckFailedResult builds the failed workflow-step
// output. Old/raw activity errors receive a generic stable reason; new QG-14
// errors retain their typed report details.
func NewPreGenerationDedupCheckFailedResult(
	params domain.ProblemGenParams,
	err error,
) SimilarityCheckResult {
	report, ok := DedupCheckFailedReportFromError(err)
	if !ok {
		report = newPreGenerationDedupReport(params)
		markDedupCheckFailed(report, DedupFailureDependencyUnavailable)
	}
	report.Stage = DedupStagePreGeneration
	return SimilarityCheckResult{Report: report}
}

func NewPostStatementDedupCheckFailedResult(
	stmt StatementResult,
	err error,
) PostStatementSimilarityResult {
	report, ok := DedupCheckFailedReportFromError(err)
	if !ok {
		report = newPostStatementDedupReport(stmt)
		markDedupCheckFailed(report, DedupFailureDependencyUnavailable)
	}
	report.Stage = DedupStagePostStatement
	return PostStatementSimilarityResult{Report: report}
}
