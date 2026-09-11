package activities

import (
	"context"
	"fmt"
	"os"
	"strings"

	"go.temporal.io/sdk/activity"
)

// ValidateActivity compares two already-selected output suites.  The
// generation/validation workflows decide whether the second suite is the
// persisted official output or a bounded brute/reference subset; this activity
// intentionally knows nothing about case selection and never expands a subset
// into the full official suite.
func (a *Activities) ValidateActivity(
	ctx context.Context,
	mainResult SandboxResult,
	bruteResult SandboxResult,
) (*ValidationResult, error) {
	if err := validateActivityPayloadVersion(mainResult.PayloadVersion); err != nil {
		return nil, fmt.Errorf("main sandbox result: %w", err)
	}
	if err := validateActivityPayloadVersion(bruteResult.PayloadVersion); err != nil {
		return nil, fmt.Errorf("brute sandbox result: %w", err)
	}
	if mainResult.PayloadVersion == ActivityPayloadVersion || bruteResult.PayloadVersion == ActivityPayloadVersion {
		if mainResult.PayloadVersion != bruteResult.PayloadVersion {
			return nil, fmt.Errorf("sandbox payload version mismatch: main=%d brute=%d", mainResult.PayloadVersion, bruteResult.PayloadVersion)
		}
		if hasLegacyOutputRef(mainResult.OutputRefs) || hasLegacyOutputRef(bruteResult.OutputRefs) {
			return nil, fmt.Errorf("versioned sandbox result contains a forbidden worker-local output ref")
		}
	}

	logger := activity.GetLogger(ctx)
	logger.Info("validating outputs",
		"main_count", len(mainResult.Outputs),
		"brute_count", len(bruteResult.Outputs),
	)

	activity.RecordHeartbeat(ctx, "resolving outputs")

	mainOutputs, err := a.resolveOutputs(ctx, mainResult)
	if err != nil {
		return nil, fmt.Errorf("resolving main outputs: %w", err)
	}
	bruteOutputs, err := a.resolveOutputs(ctx, bruteResult)
	if err != nil {
		return nil, fmt.Errorf("resolving brute outputs: %w", err)
	}

	activity.RecordHeartbeat(ctx, "comparing outputs")

	if len(mainOutputs) == 0 && len(bruteOutputs) == 0 {
		return &ValidationResult{
			AllPassed: false,
			Mismatches: []Mismatch{
				{
					TestIndex:   -1,
					MainOutput:  "empty comparison: no test cases were compared",
					BruteOutput: "",
				},
			},
		}, nil
	}

	// Ensure both slices have the same length.
	if len(mainOutputs) != len(bruteOutputs) {
		return &ValidationResult{
			AllPassed: false,
			Mismatches: []Mismatch{
				{
					TestIndex:   -1,
					MainOutput:  fmt.Sprintf("count mismatch: main=%d, brute=%d", len(mainOutputs), len(bruteOutputs)),
					BruteOutput: "",
				},
			},
		}, nil
	}

	var mismatches []Mismatch
	for i := 0; i < len(mainOutputs); i++ {
		activity.RecordHeartbeat(ctx, fmt.Sprintf("comparing test case %d/%d", i+1, len(mainOutputs)))

		mainNorm := normalizeOutput(mainOutputs[i])
		bruteNorm := normalizeOutput(bruteOutputs[i])

		if mainNorm != bruteNorm {
			mismatches = append(mismatches, Mismatch{
				TestIndex:   i,
				MainOutput:  truncate(mainOutputs[i], 500),
				BruteOutput: truncate(bruteOutputs[i], 500),
			})
		}
	}

	result := &ValidationResult{
		AllPassed:  len(mismatches) == 0,
		Mismatches: mismatches,
	}

	if result.AllPassed {
		logger.Info("all outputs match")
	} else {
		logger.Warn("output mismatches found", "count", len(mismatches))
	}

	return result, nil
}

func hasLegacyOutputRef(refs []string) bool {
	for _, ref := range refs {
		if ref != "" {
			return true
		}
	}
	return false
}

// resolveOutputs reads durable artifacts when present. OutputRefs is a
// replay-only fallback for workflows started before artifact versioning.
func (a *Activities) resolveOutputs(ctx context.Context, sr SandboxResult) ([]string, error) {
	if len(sr.OutputArtifacts) > 0 {
		outputs := make([]string, len(sr.OutputArtifacts))
		for i, ref := range sr.OutputArtifacts {
			if ref == nil {
				if i < len(sr.Outputs) {
					outputs[i] = sr.Outputs[i]
				}
				continue
			}
			data, err := a.getArtifact(ctx, ref)
			if err != nil {
				return nil, fmt.Errorf("reading output artifact %d: %w", i, err)
			}
			outputs[i] = string(data)
		}
		return outputs, nil
	}
	if len(sr.OutputRefs) == 0 {
		return sr.Outputs, nil
	}
	outputs := make([]string, len(sr.OutputRefs))
	for i, ref := range sr.OutputRefs {
		if ref == "" {
			// Not externalized, use inline.
			if i < len(sr.Outputs) {
				outputs[i] = sr.Outputs[i]
			}
			continue
		}
		data, err := os.ReadFile(ref)
		if err != nil {
			return nil, fmt.Errorf("reading output ref %d from %s: %w", i, ref, err)
		}
		outputs[i] = string(data)
	}
	return outputs, nil
}

// normalizeOutput normalises a solution output string for comparison by
// trimming trailing whitespace from each line and removing trailing newlines.
func normalizeOutput(s string) string {
	lines := strings.Split(s, "\n")
	normalized := make([]string, 0, len(lines))

	for _, line := range lines {
		trimmed := strings.TrimRight(line, " \t\r")
		normalized = append(normalized, trimmed)
	}

	// Remove trailing empty lines.
	for len(normalized) > 0 && normalized[len(normalized)-1] == "" {
		normalized = normalized[:len(normalized)-1]
	}

	return strings.Join(normalized, "\n")
}

// truncate shortens a string to at most maxLen characters, appending "..."
// if truncation occurred.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
