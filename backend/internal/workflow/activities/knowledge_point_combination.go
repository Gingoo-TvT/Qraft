package activities

import (
	"fmt"
	"strings"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"go.temporal.io/sdk/temporal"
)

const (
	knowledgePointPromptStatement = "statement"
	knowledgePointPromptSolution  = "solution"
	knowledgePointPromptTestData  = "testdata"
	knowledgePointPromptReview    = "review"
)

func knowledgePointCombinationPrompt(params domain.ProblemGenParams, consumer string) string {
	contract := params.KnowledgePointCombination
	if contract == nil {
		return ""
	}

	var semantics string
	switch contract.Mode {
	case domain.KnowledgePointCombinationSingle:
		semantics = "The one requested slug is the sole intended core technique."
	case domain.KnowledgePointCombinationSet:
		semantics = "The requested slugs are an unordered set; every slug must be necessary, not decorative."
	case domain.KnowledgePointCombinationSequence:
		semantics = "The requested slug order is binding and must match the intended solution stages."
	case domain.KnowledgePointCombinationMixed:
		semantics = "The first slug is the primary/modeling technique; the remaining slugs are an unordered auxiliary set and every one must be necessary."
	}

	var taskRule string
	switch consumer {
	case knowledgePointPromptStatement:
		taskRule = "Return the `tags` array with exactly these slugs under the mode ordering rule. Design the statement so removing any requested slug makes the intended solution invalid or substantially weaker."
	case knowledgePointPromptSolution:
		taskRule = "In the explanation, identify the concrete role of every requested slug and respect the mode ordering rule. Do not replace the requested combination with an easier unrelated approach."
	case knowledgePointPromptTestData:
		taskRule = "Include adversarial cases that expose solutions which omit any requested slug or violate the required stage order/primary-role semantics."
	case knowledgePointPromptReview:
		taskRule = "Reject the candidate unless its tags exactly match the requested combination and the statement plus model solution make every requested slug genuinely necessary under the mode semantics."
	}

	return fmt.Sprintf(
		"\n### Knowledge-point combination contract\n- Schema: %s\n- Mode: %s\n- Required slugs: %s\n- Maximum concepts: %d\n- Semantics: %s\n- Task rule: %s\n",
		contract.SchemaVersion,
		contract.Mode,
		strings.Join(params.Tags, ", "),
		contract.MaxConcepts,
		semantics,
		taskRule,
	)
}

func conformStatementKnowledgePoints(params domain.ProblemGenParams, observed []string) ([]string, error) {
	if params.KnowledgePointCombination == nil {
		return append([]string(nil), observed...), nil
	}
	canonical, err := params.KnowledgePointCombination.Conform(params.Tags, observed)
	if err != nil {
		return nil, temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("knowledge-point combination quality gate failed: %v", err),
			"QualityNotMet",
			nil,
		)
	}
	return canonical, nil
}

func knowledgePointConformanceMetadata(params domain.ProblemGenParams, persistedTags []string) map[string]interface{} {
	contract := params.KnowledgePointCombination
	if contract == nil {
		return nil
	}
	return map[string]interface{}{
		"schema_version":           domain.KnowledgePointConformanceSchemaV1,
		"combination_schema":       contract.SchemaVersion,
		"mode":                     contract.Mode,
		"max_concepts":             contract.MaxConcepts,
		"required_slugs":           append([]string(nil), params.Tags...),
		"persisted_statement_tags": append([]string(nil), persistedTags...),
		"decision":                 "pass",
	}
}
