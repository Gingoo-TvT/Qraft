package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/workflow/activities"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const (
	problemGenerationStatementRepairV1ChangeID  = "problem-generation-statement-repair-v1"
	problemGenerationStatementRepairMaxRoundsV1 = 2
)

var statementRepairLeadingLineNumberV1 = regexp.MustCompile(`(?i)^line\s+\d+\s+`)

func repairGeneratedStatementV1(
	ctx workflow.Context,
	llmCtx workflow.Context,
	candidate activities.StatementResult,
	params domain.ProblemGenParams,
	useStructuredSamples bool,
) (activities.StatementResult, error) {
	return repairGeneratedStatementWithQualityV1(
		ctx,
		llmCtx,
		candidate,
		params,
		useStructuredSamples,
		false,
	)
}

// repairGeneratedStatementWithQualityV1 is the version-aware entry point used
// by the live generation workflow. The wrapper above deliberately preserves
// the old helper contract for replay tests and older histories.
func repairGeneratedStatementWithQualityV1(
	ctx workflow.Context,
	llmCtx workflow.Context,
	candidate activities.StatementResult,
	params domain.ProblemGenParams,
	useStructuredSamples bool,
	strictMarkdownQuality bool,
) (activities.StatementResult, error) {
	if len(candidate.MarkdownDiagnostics) == 0 {
		return candidate, nil
	}

	lastRepairFingerprint := ""
	for repairAttempt := 1; repairAttempt <= problemGenerationStatementRepairMaxRoundsV1; repairAttempt++ {
		input := activities.RepairStatementInputV1{
			PayloadVersion:        activities.ActivityPayloadVersion,
			Candidate:             candidate,
			Params:                params,
			UseStructuredSamples:  useStructuredSamples,
			StrictMarkdownQuality: strictMarkdownQuality,
			RepairAttempt:         repairAttempt,
			Diagnostics:           append([]activities.StatementMarkdownDiagnosticV1(nil), candidate.MarkdownDiagnostics...),
		}

		var repaired activities.StatementResult
		if err := workflow.ExecuteActivity(llmCtx, "RepairStatementActivityV1", input).Get(ctx, &repaired); err != nil {
			return candidate, fmt.Errorf("targeted statement repair attempt %d: %w", repairAttempt, err)
		}
		if err := validateStatementRepairIdentityV1(candidate, repaired); err != nil {
			return candidate, statementRepairQualityErrorV1("targeted statement repair changed frozen identity: " + err.Error())
		}

		candidate = repaired
		if len(candidate.MarkdownDiagnostics) == 0 {
			return candidate, nil
		}

		fingerprint := statementRepairDiagnosticsFingerprintV1(candidate.MarkdownDiagnostics)
		if lastRepairFingerprint != "" && fingerprint == lastRepairFingerprint {
			return candidate, statementRepairQualityErrorV1(
				"targeted statement repair repeated the same diagnostics in two consecutive repair rounds: " +
					statementRepairDiagnosticSummaryV1(candidate.MarkdownDiagnostics),
			)
		}
		lastRepairFingerprint = fingerprint
	}

	return candidate, statementRepairQualityErrorV1(
		fmt.Sprintf(
			"targeted statement repair still has quality defects after %d rounds: %s",
			problemGenerationStatementRepairMaxRoundsV1,
			statementRepairDiagnosticSummaryV1(candidate.MarkdownDiagnostics),
		),
	)
}

func validateStatementRepairIdentityV1(before, after activities.StatementResult) error {
	if before.Title != after.Title {
		return fmt.Errorf("title changed")
	}
	if !reflect.DeepEqual(before.Tags, after.Tags) {
		return fmt.Errorf("tags changed")
	}
	if before.OneLineHint != after.OneLineHint {
		return fmt.Errorf("one-line hint changed")
	}
	if before.DifficultyJustification != after.DifficultyJustification {
		return fmt.Errorf("difficulty justification changed")
	}
	if strings.TrimSpace(after.Statement) == "" {
		return fmt.Errorf("statement became empty")
	}
	return nil
}

func statementRepairDiagnosticsFingerprintV1(diagnostics []activities.StatementMarkdownDiagnosticV1) string {
	canonical := make([]string, 0, len(diagnostics))
	for _, diagnostic := range diagnostics {
		message := strings.ToLower(strings.Join(strings.Fields(diagnostic.Message), " "))
		message = statementRepairLeadingLineNumberV1.ReplaceAllString(message, "")
		canonical = append(canonical, fmt.Sprintf(
			"%s|%s",
			strings.ToLower(strings.TrimSpace(diagnostic.Code)),
			message,
		))
	}
	sort.Strings(canonical)
	encoded, _ := json.Marshal(canonical)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func statementRepairDiagnosticSummaryV1(diagnostics []activities.StatementMarkdownDiagnosticV1) string {
	if len(diagnostics) == 0 {
		return "no diagnostic was returned"
	}
	parts := make([]string, 0, len(diagnostics))
	for _, diagnostic := range diagnostics {
		message := strings.Join(strings.Fields(diagnostic.Message), " ")
		if len([]rune(message)) > 300 {
			message = string([]rune(message)[:300]) + "..."
		}
		parts = append(parts, fmt.Sprintf("[%s] %s", diagnostic.Code, message))
		if len(parts) >= 5 {
			break
		}
	}
	return strings.Join(parts, "; ")
}

func statementRepairQualityErrorV1(message string) error {
	return temporal.NewNonRetryableApplicationError(message, "QualityNotMet", nil)
}
