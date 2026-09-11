package service

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/Gingoo-TvT/Qraft/backend/internal/workflow/activities"
	"github.com/google/uuid"
)

const hydroTestManifestPressureScoreV2 = 30

type hydroManifestBoundaryKeyV2 struct {
	constraintRegion string
	boundaryRefsKey  string
}

// buildHydroSubtasksForTestManifestV2 is additive: nil preserves the legacy
// GroupID/Score mapper byte-for-byte. A present manifest is the sole scoring
// source and is bound to the exact bytes that will be written into the ZIP.
func buildHydroSubtasksForTestManifestV2(
	problemID uuid.UUID,
	cases []hydroExportCase,
	manifest *activities.TestManifestV2,
) ([]hydroSubtask, string, error) {
	if manifest == nil {
		return buildHydroSubtasks(cases), "", nil
	}
	if problemID == uuid.Nil {
		return nil, "", fmt.Errorf("validation: TestManifest v2 Hydro export requires a persisted problem ID")
	}
	if err := manifest.Validate(); err != nil {
		return nil, "", fmt.Errorf("validation: invalid TestManifest v2: %w", err)
	}
	_, manifestSHA256, err := activities.CanonicalTestManifestV2JSON(*manifest)
	if err != nil {
		return nil, "", fmt.Errorf("validation: canonicalizing TestManifest v2: %w", err)
	}
	if len(cases) != len(manifest.Cases) {
		return nil, "", fmt.Errorf("validation: TestManifest v2 records %d cases, persisted problem has %d", len(manifest.Cases), len(cases))
	}

	sampleCases := make([]int, 0)
	sumRegions := make(map[string][]int)
	sumRegionHasPrimary := make(map[string]bool)
	metamorphicRegions := make(map[string]bool)
	boundaryGroups := make(map[hydroManifestBoundaryKeyV2][]int)
	pressureGroups := make(map[string][]int)

	for index := range cases {
		exportCase := &cases[index]
		manifestCase := manifest.Cases[index]
		if exportCase.tc.TestIndex != index || manifestCase.TestIndex != index {
			return nil, "", fmt.Errorf("validation: TestManifest v2 case %d does not bind to persisted test_index=%d", index, exportCase.tc.TestIndex)
		}
		if exportCase.tc.ProblemID != problemID {
			return nil, "", fmt.Errorf("validation: persisted testcase %d belongs to problem %s, want %s", index, exportCase.tc.ProblemID, problemID)
		}
		isSample := manifestCase.Purpose == activities.TestManifestPurposeSample
		if exportCase.tc.IsSample != isSample {
			return nil, "", fmt.Errorf("validation: TestManifest v2 case %q sample purpose does not match persisted is_sample=%t", manifestCase.TestID, exportCase.tc.IsSample)
		}
		if exportCase.inputSHA256 != manifestCase.InputSHA256 || exportCase.outputSHA256 != manifestCase.OutputSHA256 {
			return nil, "", fmt.Errorf("validation: TestManifest v2 case %q input/output SHA-256 does not match persisted assets", manifestCase.TestID)
		}
		if int64(len(exportCase.inputData)) != manifestCase.InputArtifact.SizeBytes || int64(len(exportCase.outputData)) != manifestCase.OutputArtifact.SizeBytes {
			return nil, "", fmt.Errorf("validation: TestManifest v2 case %q input/output size does not match persisted assets", manifestCase.TestID)
		}

		exportCase.score = 0
		exportCase.testID = manifestCase.TestID
		exportCase.purpose = manifestCase.Purpose
		exportCase.constraintRegion = manifestCase.ConstraintRegion
		exportCase.boundaryRefs = append([]string(nil), manifestCase.BoundaryRefs...)

		switch manifestCase.Purpose {
		case activities.TestManifestPurposeSample:
			sampleCases = append(sampleCases, index)
		case activities.TestManifestPurposeTiny, activities.TestManifestPurposeRandom:
			sumRegions[manifestCase.ConstraintRegion] = append(sumRegions[manifestCase.ConstraintRegion], index)
			sumRegionHasPrimary[manifestCase.ConstraintRegion] = true
		case activities.TestManifestPurposeMetamorphic:
			sumRegions[manifestCase.ConstraintRegion] = append(sumRegions[manifestCase.ConstraintRegion], index)
			metamorphicRegions[manifestCase.ConstraintRegion] = true
		case activities.TestManifestPurposeBoundary:
			key := hydroManifestBoundaryKeyV2{
				constraintRegion: manifestCase.ConstraintRegion,
				boundaryRefsKey:  encodeHydroManifestBoundaryRefsV2(manifestCase.BoundaryRefs),
			}
			boundaryGroups[key] = append(boundaryGroups[key], index)
		case activities.TestManifestPurposeExtreme, activities.TestManifestPurposeComplexity:
			pressureGroups[manifestCase.Purpose] = append(pressureGroups[manifestCase.Purpose], index)
		default:
			return nil, "", fmt.Errorf("validation: unsupported TestManifest v2 purpose %q", manifestCase.Purpose)
		}
	}
	for region := range metamorphicRegions {
		if !sumRegionHasPrimary[region] {
			return nil, "", fmt.Errorf("validation: metamorphic constraint_region %q has no corresponding tiny/random case", region)
		}
	}
	sumRegionKeys := sortedHydroManifestKeysV2(sumRegions)
	boundaryKeys := sortedHydroManifestBoundaryKeysV2(boundaryGroups)
	baseUnitCount := len(sumRegionKeys) + len(boundaryKeys)
	if baseUnitCount == 0 && len(pressureGroups) == 0 {
		return nil, "", fmt.Errorf("validation: TestManifest v2 Hydro mapping cannot score a sample-only manifest")
	}
	baseBudget := 0
	if baseUnitCount > 0 {
		baseBudget = 100
		if len(pressureGroups) > 0 {
			baseBudget -= hydroTestManifestPressureScoreV2
		}
		if baseUnitCount > baseBudget {
			return nil, "", fmt.Errorf("validation: TestManifest v2 has %d base scoring classes, exceeds integer score budget %d", baseUnitCount, baseBudget)
		}
	}
	pressureBudget := 100 - baseBudget
	baseScores := allocateHydroManifestScoresV2(baseBudget, baseUnitCount)

	regionScores := make(map[string]int, len(sumRegionKeys))
	boundaryScores := make(map[hydroManifestBoundaryKeyV2]int, len(boundaryKeys))
	for index, key := range sumRegionKeys {
		regionScores[key] = baseScores[index]
	}
	for index, key := range boundaryKeys {
		boundaryScores[key] = baseScores[len(sumRegionKeys)+index]
	}

	pressureOrder := make([]string, 0, 2)
	for _, purpose := range []string{activities.TestManifestPurposeExtreme, activities.TestManifestPurposeComplexity} {
		if len(pressureGroups[purpose]) > 0 {
			pressureOrder = append(pressureOrder, purpose)
		}
	}
	pressureScores := allocateHydroManifestScoresV2(pressureBudget, len(pressureOrder))

	subtasks := make([]hydroSubtask, 0, 2+len(boundaryKeys)+len(pressureOrder))
	appendSubtask := func(kind string, score int, indexes []int, perCaseScores map[int]int) {
		ordered := append([]int(nil), indexes...)
		sort.Ints(ordered)
		subtask := hydroSubtask{ID: len(subtasks) + 1, Score: score, Type: kind, Cases: make([]hydroCase, 0, len(ordered))}
		for _, caseIndex := range ordered {
			caseScore := perCaseScores[caseIndex]
			cases[caseIndex].score = caseScore
			subtask.Cases = append(subtask.Cases, hydroCase{
				Input:  cases[caseIndex].inputFile,
				Output: cases[caseIndex].outputFile,
				Score:  caseScore,
			})
		}
		subtasks = append(subtasks, subtask)
	}

	if len(sampleCases) > 0 {
		appendSubtask("sum", 0, sampleCases, map[int]int{})
	}
	if len(sumRegionKeys) > 0 {
		sumCases := make([]int, 0)
		sumCaseScores := make(map[int]int)
		sumScore := 0
		for _, region := range sumRegionKeys {
			indexes := sumRegions[region]
			caseScores := allocateHydroManifestScoresV2(regionScores[region], len(indexes))
			for index, caseIndex := range indexes {
				sumCases = append(sumCases, caseIndex)
				sumCaseScores[caseIndex] = caseScores[index]
			}
			sumScore += regionScores[region]
		}
		appendSubtask("sum", sumScore, sumCases, sumCaseScores)
	}
	for _, key := range boundaryKeys {
		appendSubtask("min", boundaryScores[key], boundaryGroups[key], map[int]int{})
	}
	pressureScore := 0
	for index, purpose := range pressureOrder {
		pressureScore += pressureScores[index]
		appendSubtask("min", pressureScores[index], pressureGroups[purpose], map[int]int{})
	}

	totalScore := 0
	for _, subtask := range subtasks {
		totalScore += subtask.Score
	}
	if totalScore != 100 || (len(pressureGroups) > 0 && pressureScore < hydroTestManifestPressureScoreV2) {
		return nil, "", fmt.Errorf("validation: deterministic Hydro score table totals %d with pressure score %d", totalScore, pressureScore)
	}
	return subtasks, manifestSHA256, nil
}

func sortedHydroManifestKeysV2[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func encodeHydroManifestBoundaryRefsV2(refs []string) string {
	var encoded strings.Builder
	for _, ref := range refs {
		encoded.WriteString(strconv.Itoa(len(ref)))
		encoded.WriteByte(':')
		encoded.WriteString(ref)
	}
	return encoded.String()
}

func sortedHydroManifestBoundaryKeysV2(values map[hydroManifestBoundaryKeyV2][]int) []hydroManifestBoundaryKeyV2 {
	keys := make([]hydroManifestBoundaryKeyV2, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].constraintRegion != keys[j].constraintRegion {
			return keys[i].constraintRegion < keys[j].constraintRegion
		}
		return keys[i].boundaryRefsKey < keys[j].boundaryRefsKey
	})
	return keys
}

func allocateHydroManifestScoresV2(total, count int) []int {
	if count <= 0 {
		return nil
	}
	scores := make([]int, count)
	base := total / count
	for index := range scores {
		scores[index] = base
	}
	scores[len(scores)-1] += total - base*count
	return scores
}
