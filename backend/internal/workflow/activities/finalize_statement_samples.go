package activities

import (
	"context"
	"fmt"
	"os"
	"strings"

	"go.temporal.io/sdk/temporal"
)

type statementSample struct {
	input  string
	output string
}

// FinalizeStatementSamplesActivity replaces the statement's sample marker
// with the exact public sample inputs and the outputs produced by the main
// solution after those cases passed differential validation.
func (a *Activities) FinalizeStatementSamplesActivity(
	ctx context.Context,
	input FinalizeStatementSamplesInput,
) (*StatementResult, error) {
	if input.PayloadVersion != ActivityPayloadVersion {
		return nil, sampleQualityError("unsupported structured-sample payload version %d", input.PayloadVersion)
	}
	if err := validateActivityPayloadVersion(input.SandboxOutput.PayloadVersion); err != nil {
		return nil, sampleQualityError("sandbox output: %v", err)
	}
	input.Statement.Statement = normalizeGeneratedStatementMarkdown(input.Statement.Statement)
	input.Statement.OneLineHint = NormalizeOneLineHintV1(input.Statement.OneLineHint)
	var surfaceErr error
	if input.StrictMarkdownQuality {
		surfaceErr = ValidateGeneratedStatementMarkdownStrictV1(input.Statement.Statement, true)
	} else {
		surfaceErr = validateStatementMarkdownSurface(input.Statement.Statement)
	}
	if surfaceErr != nil {
		return nil, sampleQualityError("statement markdown quality: %v", surfaceErr)
	}
	if strings.Count(input.Statement.Statement, StatementSamplesPlaceholder) != 1 {
		return nil, sampleQualityError("statement must contain exactly one structured sample placeholder")
	}
	if input.EnforceSampleCount && input.ExpectedSampleCount < 0 {
		return nil, sampleQualityError("expected sample count must not be negative")
	}

	samples := make([]statementSample, 0)
	for index, testCase := range input.TestCases {
		if !testCase.IsSample {
			continue
		}
		inputBytes, err := a.resolveStatementSampleInput(ctx, testCase)
		if err != nil {
			return nil, sampleQualityError("resolve sample %d input: %v", index, err)
		}
		outputBytes, err := a.resolveStatementSampleOutput(ctx, input.SandboxOutput, index)
		if err != nil {
			return nil, sampleQualityError("resolve sample %d output: %v", index, err)
		}
		samples = append(samples, statementSample{input: string(inputBytes), output: string(outputBytes)})
	}
	if !input.EnforceSampleCount && len(samples) == 0 {
		return nil, sampleQualityError("structured statement requested samples but no sample test case was generated")
	}
	if input.EnforceSampleCount && len(samples) != input.ExpectedSampleCount {
		return nil, sampleQualityError("structured statement has %d sample tests, want exactly %d", len(samples), input.ExpectedSampleCount)
	}

	result := input.Statement
	replacement := ""
	if len(samples) > 0 {
		replacement = renderStatementSamples(samples, input.Locale)
	}
	result.Statement = strings.Replace(
		result.Statement,
		StatementSamplesPlaceholder,
		replacement,
		1,
	)
	result.Statement = strings.TrimSpace(result.Statement)
	if strings.Contains(result.Statement, StatementSamplesPlaceholder) {
		return nil, sampleQualityError("final statement still contains a structured sample placeholder")
	}
	if input.StrictMarkdownQuality {
		if err := ValidateGeneratedStatementMarkdownStrictV1(result.Statement, false); err != nil {
			return nil, sampleQualityError("final statement markdown quality: %v", err)
		}
	} else if err := validateStatementMarkdownSurface(result.Statement); err != nil {
		return nil, sampleQualityError("final statement markdown quality: %v", err)
	}
	return &result, nil
}

func (a *Activities) resolveStatementSampleInput(ctx context.Context, testCase TestCaseData) ([]byte, error) {
	switch {
	case testCase.InputArtifact != nil:
		return a.getArtifact(ctx, testCase.InputArtifact)
	case testCase.InputRef != "":
		return os.ReadFile(testCase.InputRef)
	case testCase.Input != "":
		return []byte(testCase.Input), nil
	default:
		return nil, fmt.Errorf("input is empty")
	}
}

func (a *Activities) resolveStatementSampleOutput(ctx context.Context, result SandboxResult, index int) ([]byte, error) {
	switch {
	case index < len(result.OutputArtifacts) && result.OutputArtifacts[index] != nil:
		return a.getArtifact(ctx, result.OutputArtifacts[index])
	case index < len(result.OutputRefs) && result.OutputRefs[index] != "":
		return os.ReadFile(result.OutputRefs[index])
	case index < len(result.Outputs):
		return []byte(result.Outputs[index]), nil
	default:
		return nil, fmt.Errorf("main sandbox output is missing")
	}
}

func renderStatementSamples(samples []statementSample, locale string) string {
	type headings struct {
		section string
		sample  string
		input   string
		output  string
	}
	labels := headings{section: "Samples", sample: "Sample", input: "Input", output: "Output"}
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(locale)), "zh") {
		labels = headings{section: "样例", sample: "样例", input: "输入", output: "输出"}
	}

	var builder strings.Builder
	builder.WriteString("### ")
	builder.WriteString(labels.section)
	builder.WriteString("\n\n")
	for i, sample := range samples {
		builder.WriteString("#### ")
		builder.WriteString(labels.sample)
		builder.WriteString(fmt.Sprintf(" %d\n\n", i+1))
		appendStatementSampleBlock(&builder, labels.input, sample.input)
		builder.WriteString("\n")
		appendStatementSampleBlock(&builder, labels.output, sample.output)
		if i+1 < len(samples) {
			builder.WriteString("\n")
		}
	}
	return strings.TrimRight(builder.String(), "\n")
}

func appendStatementSampleBlock(builder *strings.Builder, label, value string) {
	builder.WriteString("**")
	builder.WriteString(label)
	fence := statementSampleFence(value)
	builder.WriteString("**\n\n")
	builder.WriteString(fence)
	builder.WriteString("text\n")
	builder.WriteString(value)
	if !strings.HasSuffix(value, "\n") {
		builder.WriteString("\n")
	}
	builder.WriteString(fence)
	builder.WriteString("\n")
}

func statementSampleFence(value string) string {
	longestRun := 0
	currentRun := 0
	for _, character := range value {
		if character != '`' {
			currentRun = 0
			continue
		}
		currentRun++
		if currentRun > longestRun {
			longestRun = currentRun
		}
	}
	fenceLength := 3
	if longestRun >= fenceLength {
		fenceLength = longestRun + 1
	}
	return strings.Repeat("`", fenceLength)
}

func sampleQualityError(format string, args ...interface{}) error {
	return temporal.NewNonRetryableApplicationError(
		fmt.Sprintf(format, args...),
		"QualityNotMet",
		nil,
	)
}
