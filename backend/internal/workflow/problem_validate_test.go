package workflow

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/workflow/activities"
)

func TestValidationBruteCasesV1UsesManifestIndexesOnly(t *testing.T) {
	large := activities.TestCaseData{InputArtifact: &activities.ArtifactRef{SizeBytes: 128 << 10}, Description: "maximum official case"}
	fetch := activities.FetchProblemDataResult{
		TestManifestSchema: activities.TestManifestSchemaVersion,
		BruteIndices:       []int{0, 2},
		TestCases: []activities.TestCaseData{
			{Input: "small-a\n", IsSample: true},
			large,
			{Input: "small-b\n"},
		},
	}
	selected, indices, err := validationBruteCasesV1(fetch)
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 2 || len(indices) != 2 || indices[0] != 0 || indices[1] != 2 ||
		selected[0].Input != "small-a\n" || selected[1].Input != "small-b\n" {
		t.Fatalf("selected=%+v indices=%v", selected, indices)
	}
}

func TestValidationBruteCasesV1NeverExpandsLegacyFallbackToArtifactSuite(t *testing.T) {
	cases := make([]activities.TestCaseData, 0, validationLegacyBruteCaseLimitV1+3)
	cases = append(cases, activities.TestCaseData{Input: "sample\n", IsSample: true})
	for i := 0; i < validationLegacyBruteCaseLimitV1+2; i++ {
		cases = append(cases, activities.TestCaseData{InputArtifact: &activities.ArtifactRef{SizeBytes: int64(1 << 20)}, Description: "large official case"})
	}
	fetch := activities.FetchProblemDataResult{TestCases: cases}
	selected, indices, err := validationBruteCasesV1(fetch)
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 1 || len(indices) != 1 || indices[0] != 0 {
		t.Fatalf("legacy selection expanded unexpectedly: selected=%d indices=%v", len(selected), indices)
	}
}

func TestValidationBruteCasesV1SkipsOversizedLegacySample(t *testing.T) {
	cases := []activities.TestCaseData{
		{InputArtifact: &activities.ArtifactRef{SizeBytes: activities.MaxReferenceDifferentialInputBytes + 1}, IsSample: true},
		{Input: "tiny\n", IsSample: true},
	}
	selected, indices, err := validationBruteCasesV1(activities.FetchProblemDataResult{TestCases: cases})
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 1 || len(indices) != 1 || indices[0] != 1 {
		t.Fatalf("oversized legacy sample was admitted: selected=%+v indices=%v", selected, indices)
	}
}

func TestValidationBruteCasesV1RejectsOverbroadManifest(t *testing.T) {
	indices := make([]int, validationManifestBruteCaseLimitV1+1)
	for i := range indices {
		indices[i] = i
	}
	_, _, err := validationBruteCasesV1(activities.FetchProblemDataResult{
		TestManifestSchema: activities.TestManifestSchemaVersion,
		BruteIndices:       indices,
		TestCases:          make([]activities.TestCaseData, len(indices)),
	})
	if err == nil || !strings.Contains(err.Error(), "maximum") {
		t.Fatalf("overbroad manifest error = %v", err)
	}
}

func TestValidationBruteCasesV1SkipsIndependentOracleManifest(t *testing.T) {
	selected, indices, err := validationBruteCasesV1(activities.FetchProblemDataResult{
		TestManifestSchema: activities.TestManifestSchemaVersionV2,
		BruteIndices:       []int{0},
		TestCases:          []activities.TestCaseData{{Input: "must not run"}},
	})
	if err != nil || len(selected) != 0 || len(indices) != 0 {
		t.Fatalf("v2 selection selected=%+v indices=%v err=%v", selected, indices, err)
	}
}

func TestValidationBruteCasesV2LegacyFallbackUsesSamplesOnly(t *testing.T) {
	cases := []activities.TestCaseData{
		{Input: "1\n", IsSample: true},
		// A compact scalar is not evidence that the brute implementation can
		// enumerate it within the problem time limit.
		{Input: "100000000000000\n", Description: "large scalar official case"},
		{Input: "tiny-inline\n", Description: "non-sample inline case"},
	}
	selected, indices, err := validationBruteCasesV2(activities.FetchProblemDataResult{TestCases: cases})
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(indices) != "[0]" || len(selected) != 1 || selected[0].Input != "1\n" {
		t.Fatalf("legacy v2 fallback admitted non-sample cases: indices=%v selected=%+v", indices, selected)
	}
}
