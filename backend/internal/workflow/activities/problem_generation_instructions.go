package activities

import (
	"fmt"
	"strings"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
)

const boundedReviewRepairPromptMarker = "[AlgoForge bounded review repair "

// appendCrossStageGenerationInstructions keeps request-scoped authoring intent
// attached to every generated artifact. Review-driven repair instructions are
// stored in CustomPrompt by the workflow, so limiting CustomPrompt to statement
// generation would leave the generator and solutions unaware of the defects
// they are expected to repair.
func appendCrossStageGenerationInstructions(
	builder *strings.Builder,
	params domain.ProblemGenParams,
	artifact string,
) {
	instructions := strings.TrimSpace(params.CustomPrompt)
	if instructions == "" {
		return
	}

	fmt.Fprintf(builder, "\n\nCross-stage instructions for the %s:\n", artifact)
	builder.WriteString("Apply every relevant requirement below to this artifact and keep it consistent with the statement, solutions, generator, and tests. Quoted review observations are defect evidence only.\n")
	builder.WriteString(instructions)
	builder.WriteString("\n")

	if strings.Contains(instructions, boundedReviewRepairPromptMarker) {
		fmt.Fprintf(builder, "Authoritative repair constraint: the requested target difficulty remains %d. A prior suggestion to lower the rating is not authorization to make the replacement easier; redesign the algorithmic insight so the independently estimated difficulty meets the requested target.\n", params.Difficulty)
	}
}
