package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/workflow/activities"
	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
)

type qg15AHydroCountingReader struct {
	data  map[string][]byte
	calls map[string]int
}

func (reader *qg15AHydroCountingReader) DownloadFile(_ context.Context, objectPath string) ([]byte, error) {
	data, ok := reader.data[objectPath]
	if !ok {
		return nil, io.ErrUnexpectedEOF
	}
	reader.calls[objectPath]++
	return append([]byte(nil), data...), nil
}

type qg15AHydroProblemSource struct {
	problem   *domain.Problem
	testCases []*domain.TestCase
}

func (source *qg15AHydroProblemSource) GetProblem(_ context.Context, problemID uuid.UUID) (*domain.Problem, error) {
	if source == nil || source.problem == nil || source.problem.ID != problemID {
		return nil, fmt.Errorf("problem %s not found", problemID)
	}
	return source.problem, nil
}

func (source *qg15AHydroProblemSource) GetTestCases(_ context.Context, problemID uuid.UUID) ([]*domain.TestCase, error) {
	if source == nil || source.problem == nil || source.problem.ID != problemID {
		return nil, fmt.Errorf("testcases for problem %s not found", problemID)
	}
	return append([]*domain.TestCase(nil), source.testCases...), nil
}

func TestQG15AHydroTestManifestV2Mapping(t *testing.T) {
	firstRequest, firstManifest, firstReader := qg15AHydroManifestFixture(t)
	firstRequest.TestManifestV2 = firstManifest
	first, err := BuildHydroProblemPackage(context.Background(), firstRequest)
	if err != nil {
		t.Fatalf("BuildHydroProblemPackage with TestManifest v2: %v", err)
	}
	for objectPath, calls := range firstReader.calls {
		if calls != 1 {
			t.Fatalf("object %q read %d times, want once so verified bytes equal ZIP bytes", objectPath, calls)
		}
	}

	secondRequest, secondManifest, _ := qg15AHydroManifestFixture(t)
	secondRequest.TestManifestV2 = secondManifest
	second, err := BuildHydroProblemPackage(context.Background(), secondRequest)
	if err != nil {
		t.Fatalf("second deterministic build: %v", err)
	}
	if !bytes.Equal(first.Content, second.Content) {
		t.Fatal("TestManifest v2 Hydro package is not byte deterministic")
	}

	entries := unzipEntries(t, first.Content)
	var config hydroConfigYAML
	if err := yaml.Unmarshal(entries["testdata/config.yaml"], &config); err != nil {
		t.Fatalf("decode config.yaml: %v", err)
	}
	if len(config.Subtasks) != 6 {
		t.Fatalf("subtask count = %d, want 6: %+v", len(config.Subtasks), config.Subtasks)
	}
	assertQG15AHydroConfigThreeLevelLimits(t, config, "1s", "128m")
	want := []hydroSubtask{
		{ID: 1, Score: 0, Type: "sum", Cases: []hydroCase{{Input: "1.in", Output: "1.out", Score: 0}}},
		{ID: 2, Score: 34, Type: "sum", Cases: []hydroCase{
			{Input: "2.in", Output: "2.out", Score: 17},
			{Input: "3.in", Output: "3.out", Score: 8},
			{Input: "4.in", Output: "4.out", Score: 9},
		}},
		{ID: 3, Score: 17, Type: "min", Cases: []hydroCase{{Input: "7.in", Output: "7.out", Score: 0}}},
		{ID: 4, Score: 19, Type: "min", Cases: []hydroCase{
			{Input: "5.in", Output: "5.out", Score: 0},
			{Input: "6.in", Output: "6.out", Score: 0},
		}},
		{ID: 5, Score: 15, Type: "min", Cases: []hydroCase{{Input: "8.in", Output: "8.out", Score: 0}}},
		{ID: 6, Score: 15, Type: "min", Cases: []hydroCase{{Input: "9.in", Output: "9.out", Score: 0}}},
	}
	for subtaskIndex := range want {
		want[subtaskIndex].Time = "1s"
		want[subtaskIndex].Memory = "128m"
		for caseIndex := range want[subtaskIndex].Cases {
			want[subtaskIndex].Cases[caseIndex].Time = "1s"
			want[subtaskIndex].Cases[caseIndex].Memory = "128m"
		}
	}
	if !reflect.DeepEqual(config.Subtasks, want) {
		t.Fatalf("deterministic TestManifest score table\ngot:  %+v\nwant: %+v", config.Subtasks, want)
	}
	totalScore := 0
	for _, subtask := range config.Subtasks {
		totalScore += subtask.Score
	}
	if totalScore != 100 || config.Subtasks[0].Score != 0 || config.Subtasks[4].Score+config.Subtasks[5].Score < 30 {
		t.Fatalf("score invariants: total=%d sample=%d pressure=%d", totalScore, config.Subtasks[0].Score, config.Subtasks[4].Score+config.Subtasks[5].Score)
	}

	_, manifestSHA256, err := activities.CanonicalTestManifestV2JSON(*firstManifest)
	if err != nil {
		t.Fatalf("canonical TestManifest v2: %v", err)
	}
	var exportedManifest hydroManifest
	if err := json.Unmarshal(entries["additional_file/algoforge_manifest.json"], &exportedManifest); err != nil {
		t.Fatalf("decode algoforge manifest: %v", err)
	}
	if exportedManifest.TestManifestSHA256 != manifestSHA256 || len(exportedManifest.TestCases) != len(firstManifest.Cases) {
		t.Fatalf("exported TestManifest binding = %+v", exportedManifest)
	}
	for index, manifestCase := range firstManifest.Cases {
		exportedCase := exportedManifest.TestCases[index]
		if exportedCase.TestID != manifestCase.TestID || exportedCase.Purpose != manifestCase.Purpose || exportedCase.ConstraintRegion != manifestCase.ConstraintRegion || !slices.Equal(exportedCase.BoundaryRefs, manifestCase.BoundaryRefs) {
			t.Fatalf("exported manifest case %d = %+v, want %+v", index, exportedCase, manifestCase)
		}
		inputPath := fmt.Sprintf("testdata/%d.in", index+1)
		outputPath := fmt.Sprintf("testdata/%d.out", index+1)
		if sha256Hex(entries[inputPath]) != manifestCase.InputSHA256 || sha256Hex(entries[outputPath]) != manifestCase.OutputSHA256 {
			t.Fatalf("final ZIP case %d bytes are not bound to TestManifest v2", index)
		}
	}

	report, err := NewHydroValidationService().ValidatePackage(context.Background(), bytes.NewReader(first.Content))
	if err != nil {
		t.Fatalf("Hydro round-trip validation: %v", err)
	}
	if !report.Valid || report.ProblemCount != 1 || report.Problems[0].CaseCount != len(firstManifest.Cases) {
		t.Fatalf("Hydro round-trip report = %+v", report)
	}
}

func TestQG15AHydroTestManifestV2RejectsBindingAndMappingViolations(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*HydroPackageRequest, *activities.TestManifestV2, *qg15AHydroCountingReader)
		want   string
	}{
		{
			name: "input bytes splice",
			mutate: func(_ *HydroPackageRequest, _ *activities.TestManifestV2, reader *qg15AHydroCountingReader) {
				reader.data["input/2"] = []byte("spliced input\n")
			},
			want: "input/output SHA-256",
		},
		{
			name: "output bytes splice",
			mutate: func(_ *HydroPackageRequest, _ *activities.TestManifestV2, reader *qg15AHydroCountingReader) {
				reader.data["output/4"] = []byte("spliced output\n")
			},
			want: "input/output SHA-256",
		},
		{
			name: "manifest ordered input binding splice",
			mutate: func(_ *HydroPackageRequest, manifest *activities.TestManifestV2, _ *qg15AHydroCountingReader) {
				manifest.Cases[1].InputArtifact, manifest.Cases[2].InputArtifact = manifest.Cases[2].InputArtifact, manifest.Cases[1].InputArtifact
				manifest.Cases[1].InputSHA256, manifest.Cases[2].InputSHA256 = manifest.Cases[2].InputSHA256, manifest.Cases[1].InputSHA256
			},
			want: "input/output SHA-256",
		},
		{
			name: "artifact size mismatch",
			mutate: func(_ *HydroPackageRequest, manifest *activities.TestManifestV2, _ *qg15AHydroCountingReader) {
				manifest.Cases[1].InputArtifact.SizeBytes++
			},
			want: "input/output size",
		},
		{
			name: "sample flag mismatch",
			mutate: func(request *HydroPackageRequest, _ *activities.TestManifestV2, _ *qg15AHydroCountingReader) {
				request.TestCases[0].IsSample = false
			},
			want: "sample purpose",
		},
		{
			name: "foreign problem testcase",
			mutate: func(request *HydroPackageRequest, _ *activities.TestManifestV2, _ *qg15AHydroCountingReader) {
				request.TestCases[3].ProblemID = uuid.MustParse("15150000-0000-0000-0000-000000009999")
			},
			want: "belongs to problem",
		},
		{
			name: "test index splice",
			mutate: func(request *HydroPackageRequest, _ *activities.TestManifestV2, _ *qg15AHydroCountingReader) {
				request.TestCases[8].TestIndex = 99
			},
			want: "does not bind to persisted test_index",
		},
		{
			name: "orphan metamorphic region",
			mutate: func(_ *HydroPackageRequest, manifest *activities.TestManifestV2, _ *qg15AHydroCountingReader) {
				manifest.Cases[2].Purpose = activities.TestManifestPurposeBoundary
			},
			want: "has no corresponding tiny/random case",
		},
		{
			name: "sample only cannot total 100",
			mutate: func(request *HydroPackageRequest, manifest *activities.TestManifestV2, _ *qg15AHydroCountingReader) {
				request.TestCases = request.TestCases[:1]
				manifest.Cases = manifest.Cases[:1]
				manifest.TestCount = 1
			},
			want: "cannot score a sample-only manifest",
		},
		{
			name: "case count mismatch",
			mutate: func(request *HydroPackageRequest, _ *activities.TestManifestV2, _ *qg15AHydroCountingReader) {
				request.TestCases = request.TestCases[:len(request.TestCases)-1]
			},
			want: "records 9 cases, persisted problem has 8",
		},
		{
			name: "invalid manifest receipt",
			mutate: func(_ *HydroPackageRequest, manifest *activities.TestManifestV2, _ *qg15AHydroCountingReader) {
				manifest.SanitizerReceiptSHA256 = "not-a-sha"
			},
			want: "invalid TestManifest v2",
		},
		{
			name: "non-positive product limits",
			mutate: func(request *HydroPackageRequest, _ *activities.TestManifestV2, _ *qg15AHydroCountingReader) {
				request.Problem.TimeLimit = 0
			},
			want: "requires positive problem time and memory limits",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request, manifest, reader := qg15AHydroManifestFixture(t)
			test.mutate(&request, manifest, reader)
			request.TestManifestV2 = manifest
			_, err := BuildHydroProblemPackage(context.Background(), request)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want fragment %q", err, test.want)
			}
		})
	}
}

func TestQG15AHydroTestManifestV2WithoutPressureUsesAllOneHundredPoints(t *testing.T) {
	request, manifest, _ := qg15AHydroManifestFixture(t)
	manifest.Cases[7].Purpose = activities.TestManifestPurposeRandom
	manifest.Cases[8].Purpose = activities.TestManifestPurposeRandom
	request.TestManifestV2 = manifest
	pkg, err := BuildHydroProblemPackage(context.Background(), request)
	if err != nil {
		t.Fatalf("build no-pressure manifest: %v", err)
	}
	entries := unzipEntries(t, pkg.Content)
	var config hydroConfigYAML
	if err := yaml.Unmarshal(entries["testdata/config.yaml"], &config); err != nil {
		t.Fatalf("decode config.yaml: %v", err)
	}
	total := 0
	for _, subtask := range config.Subtasks {
		total += subtask.Score
	}
	if total != 100 {
		t.Fatalf("no-pressure base score = %d, want 100: %+v", total, config.Subtasks)
	}
}

func TestQG15AHydroTestManifestV2WithOnlyPressureUsesAllOneHundredPoints(t *testing.T) {
	request, manifest, _ := qg15AHydroManifestFixture(t)
	for index := 1; index < len(manifest.Cases); index++ {
		manifest.Cases[index].BoundaryRefs = []string{}
		if index <= 4 {
			manifest.Cases[index].Purpose = activities.TestManifestPurposeExtreme
		} else {
			manifest.Cases[index].Purpose = activities.TestManifestPurposeComplexity
		}
	}
	request.TestManifestV2 = manifest
	pkg, err := BuildHydroProblemPackage(context.Background(), request)
	if err != nil {
		t.Fatalf("build pressure-only manifest: %v", err)
	}
	entries := unzipEntries(t, pkg.Content)
	var config hydroConfigYAML
	if err := yaml.Unmarshal(entries["testdata/config.yaml"], &config); err != nil {
		t.Fatalf("decode config.yaml: %v", err)
	}
	if len(config.Subtasks) != 3 || config.Subtasks[0].Score != 0 || config.Subtasks[1].Score != 50 || config.Subtasks[2].Score != 50 {
		t.Fatalf("pressure-only score table = %+v, want sample=0 extreme=50 complexity=50", config.Subtasks)
	}
}

func TestQG15AHydroTestManifestV2NilUsesLegacyMapper(t *testing.T) {
	cases := []hydroExportCase{
		{tc: &domain.TestCase{GroupID: 1}, inputFile: "1.in", outputFile: "1.out", score: 40},
		{tc: &domain.TestCase{GroupID: 2}, inputFile: "2.in", outputFile: "2.out", score: 60},
	}
	want := buildHydroSubtasks(cases)
	got, manifestSHA256, err := buildHydroSubtasksForTestManifestV2(uuid.Nil, cases, nil)
	if err != nil {
		t.Fatalf("legacy mapper: %v", err)
	}
	if manifestSHA256 != "" || !reflect.DeepEqual(got, want) {
		t.Fatalf("legacy mapper changed: got=%+v sha=%q want=%+v", got, manifestSHA256, want)
	}
}

func TestQG15AHydroTestManifestV2IgnoresLegacyGroupAndScore(t *testing.T) {
	baselineRequest, baselineManifest, _ := qg15AHydroManifestFixture(t)
	baselineRequest.TestManifestV2 = baselineManifest
	baseline, err := BuildHydroProblemPackage(context.Background(), baselineRequest)
	if err != nil {
		t.Fatalf("build baseline manifest package: %v", err)
	}

	legacyRequest, legacyManifest, _ := qg15AHydroManifestFixture(t)
	for index, testCase := range legacyRequest.TestCases {
		testCase.GroupID = 900 - index
		testCase.Score = 70 + index
	}
	legacyRequest.TestManifestV2 = legacyManifest
	legacy, err := BuildHydroProblemPackage(context.Background(), legacyRequest)
	if err != nil {
		t.Fatalf("build package with conflicting legacy scoring metadata: %v", err)
	}

	baselineConfig := unzipEntries(t, baseline.Content)["testdata/config.yaml"]
	legacyConfig := unzipEntries(t, legacy.Content)["testdata/config.yaml"]
	if !bytes.Equal(legacyConfig, baselineConfig) {
		t.Fatalf("legacy GroupID/Score changed TestManifest v2 scoring\nbaseline:\n%s\nlegacy:\n%s", baselineConfig, legacyConfig)
	}
}

func TestQG15AHydroProductionServiceUsesStableProblemTimestamp(t *testing.T) {
	fixture := qg15AS3ExportFixture(t)
	request := fixture.request
	reader := fixture.reader
	stableTime := time.Date(2026, 8, 25, 13, 14, 15, 0, time.FixedZone("fixture", 8*60*60))
	request.Problem.UpdatedAt = stableTime
	source := &qg15AHydroProblemSource{problem: request.Problem, testCases: request.TestCases}
	service := &HydroExportService{problems: source, objects: reader}

	firstProblem, err := service.BuildProblemPackage(context.Background(), request.Problem.ID)
	if err != nil {
		t.Fatalf("first production problem export: %v", err)
	}
	secondProblem, err := service.BuildProblemPackage(context.Background(), request.Problem.ID)
	if err != nil {
		t.Fatalf("second production problem export: %v", err)
	}
	if !bytes.Equal(firstProblem.Content, secondProblem.Content) {
		t.Fatal("production problem export changed across identical calls")
	}
	problemEntries := unzipEntries(t, firstProblem.Content)
	assertQG15AHydroManifestGeneratedAt(t, problemEntries, stableTime.UTC())
	var config hydroConfigYAML
	if err := yaml.Unmarshal(problemEntries["testdata/config.yaml"], &config); err != nil {
		t.Fatalf("decode production config.yaml: %v", err)
	}
	assertQG15AHydroConfigThreeLevelLimits(t, config, "1s", "128m")

	firstBatch, err := service.BuildBatchPackage(context.Background(), []uuid.UUID{request.Problem.ID})
	if err != nil {
		t.Fatalf("first production batch export: %v", err)
	}
	secondBatch, err := service.BuildBatchPackage(context.Background(), []uuid.UUID{request.Problem.ID})
	if err != nil {
		t.Fatalf("second production batch export: %v", err)
	}
	if !bytes.Equal(firstBatch.Content, secondBatch.Content) {
		t.Fatal("production batch export changed across identical calls")
	}
	assertQG15AHydroManifestGeneratedAt(t, unzipEntries(t, firstBatch.Content), stableTime.UTC())
}

func TestQG15AHydroProductionServiceStrictMetadata(t *testing.T) {
	t.Run("canonical supported fields", func(t *testing.T) {
		fixture := qg15AS3ExportFixture(t)
		qg15AAddHydroMetadata(t, fixture.request.Problem, map[string]interface{}{
			"pid": "QG15A_15", "filename": "main_cpp", "checker_type": "default", "detail": "full",
		})
		service := &HydroExportService{
			problems: &qg15AHydroProblemSource{problem: fixture.request.Problem, testCases: fixture.request.TestCases},
			objects:  fixture.reader,
		}
		if _, err := service.BuildProblemPackage(context.Background(), fixture.request.Problem.ID); err != nil {
			t.Fatalf("canonical metadata.hydro fields: %v", err)
		}
	})

	tests := []struct {
		name  string
		hydro map[string]interface{}
		want  string
	}{
		{name: "interactive is unsupported", hydro: map[string]interface{}{"interactive": true}, want: `unknown field "interactive"`},
		{name: "type is unsupported", hydro: map[string]interface{}{"type": "default"}, want: `unknown field "type"`},
		{name: "subtasks are unsupported", hydro: map[string]interface{}{"subtasks": []interface{}{}}, want: `unknown field "subtasks"`},
		{name: "strict is unsupported", hydro: map[string]interface{}{"strict": true}, want: `unknown field "strict"`},
		{name: "invalid filename", hydro: map[string]interface{}{"filename": "../main.cpp"}, want: "metadata.hydro.filename"},
		{name: "invalid detail", hydro: map[string]interface{}{"detail": "verbose"}, want: "metadata.hydro.detail"},
		{name: "invalid pid", hydro: map[string]interface{}{"pid": "123-bad"}, want: "metadata.hydro.pid"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := qg15AS3ExportFixture(t)
			qg15AAddHydroMetadata(t, fixture.request.Problem, test.hydro)
			service := &HydroExportService{
				problems: &qg15AHydroProblemSource{problem: fixture.request.Problem, testCases: fixture.request.TestCases},
				objects:  fixture.reader,
			}
			_, err := service.BuildProblemPackage(context.Background(), fixture.request.Problem.ID)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("single export error=%v, want containing %q", err, test.want)
			}
			_, err = service.BuildBatchPackage(context.Background(), []uuid.UUID{fixture.request.Problem.ID})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("batch export error=%v, want containing %q", err, test.want)
			}
		})
	}
}

func assertQG15AHydroConfigThreeLevelLimits(t *testing.T, config hydroConfigYAML, wantTime, wantMemory string) {
	t.Helper()
	if config.Time != wantTime || config.Memory != wantMemory {
		t.Fatalf("top-level limits time=%q memory=%q, want %q/%q", config.Time, config.Memory, wantTime, wantMemory)
	}
	for subtaskIndex, subtask := range config.Subtasks {
		if subtask.Time != wantTime || subtask.Memory != wantMemory {
			t.Fatalf("subtask %d limits time=%q memory=%q, want %q/%q", subtaskIndex, subtask.Time, subtask.Memory, wantTime, wantMemory)
		}
		for caseIndex, testCase := range subtask.Cases {
			if testCase.Time != wantTime || testCase.Memory != wantMemory {
				t.Fatalf("subtask %d case %d limits time=%q memory=%q, want %q/%q", subtaskIndex, caseIndex, testCase.Time, testCase.Memory, wantTime, wantMemory)
			}
		}
	}
}

func assertQG15AHydroManifestGeneratedAt(t *testing.T, entries map[string][]byte, want time.Time) {
	t.Helper()
	for name, content := range entries {
		if name != "additional_file/algoforge_manifest.json" && !strings.HasSuffix(name, "/additional_file/algoforge_manifest.json") {
			continue
		}
		var manifest hydroManifest
		if err := json.Unmarshal(content, &manifest); err != nil {
			t.Fatalf("decode %s: %v", name, err)
		}
		if manifest.GeneratedAt != want.Format(time.RFC3339) {
			t.Fatalf("%s generated_at=%q, want stable problem timestamp %q", name, manifest.GeneratedAt, want.Format(time.RFC3339))
		}
		return
	}
	t.Fatal("algoforge manifest not found in Hydro package")
}

func qg15AHydroManifestFixture(t *testing.T) (HydroPackageRequest, *activities.TestManifestV2, *qg15AHydroCountingReader) {
	t.Helper()
	problemID := uuid.MustParse("15150000-0000-0000-0000-000000000015")
	purposes := []string{
		activities.TestManifestPurposeSample,
		activities.TestManifestPurposeTiny,
		activities.TestManifestPurposeRandom,
		activities.TestManifestPurposeMetamorphic,
		activities.TestManifestPurposeBoundary,
		activities.TestManifestPurposeBoundary,
		activities.TestManifestPurposeBoundary,
		activities.TestManifestPurposeExtreme,
		activities.TestManifestPurposeComplexity,
	}
	regions := []string{"sample", "small", "medium", "medium", "low", "low", "low", "pressure-extreme", "pressure-complexity"}
	boundaryRefs := [][]string{{}, {}, {}, {}, {"n-min"}, {"n-min"}, {"n-max"}, {}, {}}
	testCases := make([]*domain.TestCase, len(purposes))
	manifestCases := make([]activities.TestManifestCaseV2, len(purposes))
	reader := &qg15AHydroCountingReader{data: make(map[string][]byte, len(purposes)*2), calls: make(map[string]int, len(purposes)*2)}

	for index := range purposes {
		inputPath := fmt.Sprintf("input/%d", index)
		outputPath := fmt.Sprintf("output/%d", index)
		input := []byte(fmt.Sprintf("%d\n", index))
		output := []byte(fmt.Sprintf("%d\n", index*index))
		reader.data[inputPath] = input
		reader.data[outputPath] = output
		testCases[index] = &domain.TestCase{
			ID:         uuid.MustParse(fmt.Sprintf("15150000-0000-0000-0000-%012d", index+100)),
			ProblemID:  problemID,
			TestIndex:  index,
			IsSample:   index == 0,
			InputPath:  inputPath,
			OutputPath: outputPath,
		}
		seed := int64(1500 + index)
		inputRef := qg15AHydroArtifactRef(input, "input", index)
		outputRef := qg15AHydroArtifactRef(output, "output", index)
		manifestCases[index] = activities.TestManifestCaseV2{
			TestIndex:                     index,
			TestID:                        fmt.Sprintf("case-%06d", index),
			Purpose:                       purposes[index],
			ConstraintRegion:              regions[index],
			BoundaryRefs:                  append([]string{}, boundaryRefs[index]...),
			Seed:                          &seed,
			InputArtifact:                 &inputRef,
			InputSHA256:                   inputRef.SHA256,
			OutputArtifact:                &outputRef,
			OutputSHA256:                  outputRef.SHA256,
			KilledWrongIDs:                []string{},
			KilledWrongIDsRetentionReason: "QG-15A deterministic fixture",
		}
	}
	manifest := &activities.TestManifestV2{
		SchemaVersion:                 activities.TestManifestSchemaVersionV2,
		SemanticSpecSHA256:            strings.Repeat("a", 64),
		AuthoringPlanSHA256:           strings.Repeat("b", 64),
		OraclePromotionReceiptSHA256:  strings.Repeat("c", 64),
		SanitizerReceiptSHA256:        strings.Repeat("d", 64),
		BoundaryCoverageReceiptSHA256: strings.Repeat("e", 64),
		TestCount:                     len(manifestCases),
		Cases:                         manifestCases,
	}
	if err := manifest.Validate(); err != nil {
		t.Fatalf("fixture TestManifest v2: %v", err)
	}
	return HydroPackageRequest{
		Problem: &domain.Problem{
			ID:           problemID,
			SerialNumber: "QG15A-15",
			Title:        "TestManifest v2 mapping",
			Statement:    "Deterministic Hydro mapping fixture.",
			TimeLimit:    1000,
			MemoryLimit:  128,
		},
		TestCases:    testCases,
		ObjectReader: reader,
		GeneratedAt:  time.Date(2026, 8, 26, 0, 0, 0, 0, time.UTC),
	}, manifest, reader
}

func qg15AHydroArtifactRef(data []byte, role string, index int) activities.ArtifactRef {
	digest := sha256Hex(data)
	return activities.ArtifactRef{
		SchemaVersion:  activities.ArtifactRefSchemaVersion,
		PayloadVersion: 1,
		Bucket:         "qg15a-fixture",
		Key:            fmt.Sprintf("workflow-artifacts/v1/sha256/%s/%s", digest[:2], digest),
		SHA256:         digest,
		SizeBytes:      int64(len(data)),
		ContentType:    "text/plain; charset=utf-8",
		Producer:       "QG15AHydroTestManifestV2Mapping",
		Provider:       "algoforge-test",
		Model:          "deterministic-fixture",
		ModelRevision:  "v1",
		WorkflowID:     fmt.Sprintf("qg15a-%s-%d", role, index),
	}
}
