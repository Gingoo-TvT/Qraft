package service

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/workflow/activities"
	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
)

const (
	hydroExportFormat = "algoforge-hydro-export-v1"
	hydroPhaseOne     = "hydro-phase1"
)

type HydroObjectReader interface {
	DownloadFile(ctx context.Context, objectPath string) ([]byte, error)
}

type hydroProblemSource interface {
	GetProblem(ctx context.Context, problemID uuid.UUID) (*domain.Problem, error)
	GetTestCases(ctx context.Context, problemID uuid.UUID) ([]*domain.TestCase, error)
}

type HydroExportService struct {
	problems   hydroProblemSource
	objects    HydroObjectReader
	legacyOnly bool
}

type HydroPackage struct {
	FileName string
	Content  []byte
}

type HydroPackageRequest struct {
	Problem        *domain.Problem
	TestCases      []*domain.TestCase
	ObjectReader   HydroObjectReader
	GeneratedAt    time.Time
	Prefix         string
	TestManifestV2 *activities.TestManifestV2
}

type hydroProblemYAML struct {
	PID   string   `yaml:"pid"`
	Title string   `yaml:"title"`
	Tag   []string `yaml:"tag,omitempty"`
}

type hydroConfigYAML struct {
	Type        string         `yaml:"type"`
	Time        string         `yaml:"time"`
	Memory      string         `yaml:"memory"`
	Filename    string         `yaml:"filename,omitempty"`
	CheckerType string         `yaml:"checker_type"`
	Detail      string         `yaml:"detail,omitempty"`
	Subtasks    []hydroSubtask `yaml:"subtasks"`
}

type hydroSubtask struct {
	ID     int         `yaml:"id"`
	Score  int         `yaml:"score"`
	Type   string      `yaml:"type"`
	Time   string      `yaml:"time,omitempty"`
	Memory string      `yaml:"memory,omitempty"`
	Cases  []hydroCase `yaml:"cases"`
}

type hydroCase struct {
	Input  string `yaml:"input"`
	Output string `yaml:"output"`
	Score  int    `yaml:"score"`
	Time   string `yaml:"time,omitempty"`
	Memory string `yaml:"memory,omitempty"`
}

type hydroMetadata struct {
	PID         string `json:"pid"`
	Filename    string `json:"filename"`
	CheckerType string `json:"checker_type"`
	Detail      string `json:"detail"`
}

type hydroManifest struct {
	Format             string                  `json:"format"`
	HydroPhase         string                  `json:"hydro_phase"`
	ProblemID          string                  `json:"problem_id"`
	SerialNumber       string                  `json:"serial_number"`
	HydroPID           string                  `json:"hydro_pid"`
	Title              string                  `json:"title"`
	WorkflowID         string                  `json:"workflow_id,omitempty"`
	GeneratedAt        string                  `json:"generated_at"`
	StatementSHA256    string                  `json:"statement_sha256"`
	TestManifestSHA256 string                  `json:"test_manifest_sha256,omitempty"`
	Compatibility      hydroCompatibility      `json:"compatibility"`
	TestCases          []hydroManifestTestCase `json:"test_cases"`
}

type hydroCompatibility struct {
	Supported           []string `json:"supported"`
	ReservedUnsupported []string `json:"reserved_unsupported"`
}

type hydroManifestTestCase struct {
	CaseIndex        int      `json:"case_index"`
	GroupID          int      `json:"group_id"`
	IsSample         bool     `json:"is_sample"`
	Score            int      `json:"score"`
	InputFile        string   `json:"input_file"`
	OutputFile       string   `json:"output_file"`
	InputSHA256      string   `json:"input_sha256"`
	OutputSHA256     string   `json:"output_sha256"`
	Description      string   `json:"description,omitempty"`
	TestID           string   `json:"test_id,omitempty"`
	Purpose          string   `json:"purpose,omitempty"`
	ConstraintRegion string   `json:"constraint_region,omitempty"`
	BoundaryRefs     []string `json:"boundary_refs,omitempty"`
}

type hydroExportCase struct {
	tc               *domain.TestCase
	inputFile        string
	outputFile       string
	score            int
	inputSHA256      string
	outputSHA256     string
	inputData        []byte
	outputData       []byte
	testID           string
	purpose          string
	constraintRegion string
	boundaryRefs     []string
}

func NewHydroExportService(problems *ProblemService, objects HydroObjectReader) *HydroExportService {
	service := &HydroExportService{objects: objects}
	if problems != nil {
		service.problems = problems
	}
	return service
}

// SetQG15ExportEnabled selects the additive S3-bound product path. Disabling
// it restores the pre-QG15 published/stale gate and legacy scoring behavior.
func (s *HydroExportService) SetQG15ExportEnabled(enabled bool) {
	if s != nil {
		s.legacyOnly = !enabled
	}
}

func (s *HydroExportService) BuildProblemPackage(ctx context.Context, problemID uuid.UUID) (*HydroPackage, error) {
	if s == nil || s.problems == nil {
		return nil, fmt.Errorf("problem service is required")
	}
	if s.objects == nil {
		return nil, fmt.Errorf("object reader is required")
	}

	problem, err := s.problems.GetProblem(ctx, problemID)
	if err != nil {
		return nil, err
	}
	testCases, err := s.problems.GetTestCases(ctx, problemID)
	if err != nil {
		return nil, err
	}
	var manifest *activities.TestManifestV2
	if s.legacyOnly {
		if err := ensureHydroExportAllowed(problem); err != nil {
			return nil, err
		}
	} else {
		binding, err := loadS3ExportBindingV1(ctx, problem, testCases, s.objects)
		if err != nil {
			return nil, err
		}
		manifest = binding.TestManifestV2
	}

	return BuildHydroProblemPackage(ctx, HydroPackageRequest{
		Problem:        problem,
		TestCases:      testCases,
		ObjectReader:   s.objects,
		GeneratedAt:    resolveHydroGeneratedAt(problem, time.Time{}),
		TestManifestV2: manifest,
	})
}

func (s *HydroExportService) BuildBatchPackage(ctx context.Context, problemIDs []uuid.UUID) (*HydroPackage, error) {
	if len(problemIDs) == 0 {
		return nil, fmt.Errorf("validation: at least one problem id is required")
	}
	if s == nil || s.problems == nil {
		return nil, fmt.Errorf("problem service is required")
	}
	if s.objects == nil {
		return nil, fmt.Errorf("object reader is required")
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	seenDirs := make(map[string]int, len(problemIDs))
	for _, problemID := range problemIDs {
		problem, err := s.problems.GetProblem(ctx, problemID)
		if err != nil {
			_ = zw.Close()
			return nil, err
		}
		testCases, err := s.problems.GetTestCases(ctx, problemID)
		if err != nil {
			_ = zw.Close()
			return nil, err
		}
		var manifest *activities.TestManifestV2
		if s.legacyOnly {
			if err := ensureHydroExportAllowed(problem); err != nil {
				_ = zw.Close()
				return nil, err
			}
		} else {
			binding, err := loadS3ExportBindingV1(ctx, problem, testCases, s.objects)
			if err != nil {
				_ = zw.Close()
				return nil, err
			}
			manifest = binding.TestManifestV2
		}
		pid, _, err := hydroIdentity(problem)
		if err != nil {
			_ = zw.Close()
			return nil, err
		}
		dir := uniqueHydroDir(pid, seenDirs)
		if err := writeHydroProblemEntries(ctx, zw, HydroPackageRequest{
			Problem:        problem,
			TestCases:      testCases,
			ObjectReader:   s.objects,
			GeneratedAt:    resolveHydroGeneratedAt(problem, time.Time{}),
			Prefix:         dir + "/",
			TestManifestV2: manifest,
		}); err != nil {
			_ = zw.Close()
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("finalizing hydro batch zip: %w", err)
	}
	return &HydroPackage{FileName: "algoforge-hydro-batch.zip", Content: buf.Bytes()}, nil
}

func ensureHydroExportAllowed(problem *domain.Problem) error {
	if problem == nil {
		return fmt.Errorf("validation: problem is required")
	}
	metadata, err := hydroExportGateMetadata(problem.MetadataJSON)
	if err != nil {
		return fmt.Errorf("%w: hydro export blocked by invalid publication metadata: %v", ErrConflict, err)
	}

	reason := hydroMetadataString(metadata, "publication_quarantine_reason")
	if problem.Status != domain.ProblemStatusPublished {
		return fmt.Errorf("%w: hydro export requires published status, got %q%s", ErrConflict, problem.Status, hydroReasonSuffix(reason))
	}
	if hydroMetadataBool(metadata, "stale") {
		staleReason := hydroMetadataString(metadata, "stale_reason")
		return fmt.Errorf("%w: hydro export blocked because problem edit refresh is stale%s", ErrConflict, hydroReasonSuffix(staleReason))
	}
	gateStatus := hydroMetadataString(metadata, "publication_gate_status")
	if gateStatus != "" && gateStatus != string(domain.ProblemStatusPublished) {
		return fmt.Errorf("%w: hydro export requires publication_gate_status=published, got %q%s", ErrConflict, gateStatus, hydroReasonSuffix(reason))
	}
	return nil
}

func hydroExportGateMetadata(raw json.RawMessage) (map[string]interface{}, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	var metadata map[string]interface{}
	if err := json.Unmarshal(raw, &metadata); err != nil {
		return nil, err
	}
	return metadata, nil
}

func hydroMetadataString(metadata map[string]interface{}, key string) string {
	if metadata == nil {
		return ""
	}
	value, _ := metadata[key].(string)
	return strings.TrimSpace(value)
}

func hydroMetadataBool(metadata map[string]interface{}, key string) bool {
	if metadata == nil {
		return false
	}
	value, ok := metadata[key].(bool)
	return ok && value
}

func hydroReasonSuffix(reason string) string {
	if reason == "" {
		return ""
	}
	return ": " + reason
}

func BuildHydroProblemPackage(ctx context.Context, req HydroPackageRequest) (*HydroPackage, error) {
	pid, _, err := hydroIdentity(req.Problem)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	if err := writeHydroProblemEntries(ctx, zw, req); err != nil {
		_ = zw.Close()
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("finalizing hydro zip: %w", err)
	}

	return &HydroPackage{
		FileName: fmt.Sprintf("%s-hydro.zip", sanitizeFileName(pid)),
		Content:  buf.Bytes(),
	}, nil
}

func resolveHydroGeneratedAt(problem *domain.Problem, requested time.Time) time.Time {
	if !requested.IsZero() {
		return requested.UTC()
	}
	if problem != nil {
		if !problem.UpdatedAt.IsZero() {
			return problem.UpdatedAt.UTC()
		}
		if !problem.CreatedAt.IsZero() {
			return problem.CreatedAt.UTC()
		}
	}
	return time.Unix(0, 0).UTC()
}

func writeHydroProblemEntries(ctx context.Context, zw *zip.Writer, req HydroPackageRequest) error {
	if req.Problem == nil {
		return fmt.Errorf("validation: problem is required")
	}
	if req.TestManifestV2 != nil {
		if err := validateHydroProductMetadataV2(req.Problem.MetadataJSON); err != nil {
			return err
		}
	}
	if req.ObjectReader == nil {
		return fmt.Errorf("validation: object reader is required")
	}
	if len(req.TestCases) == 0 {
		return fmt.Errorf("validation: hydro export requires at least one test case")
	}
	if strings.TrimSpace(req.Problem.Title) == "" {
		return fmt.Errorf("validation: hydro export requires a non-empty title")
	}
	if strings.TrimSpace(req.Problem.Statement) == "" {
		return fmt.Errorf("validation: hydro export requires a non-empty statement")
	}

	prefix := cleanZipPrefix(req.Prefix)
	pid, hydroMeta, err := hydroIdentity(req.Problem)
	if err != nil {
		return err
	}
	if err := validateHydroMetadata(hydroMeta); err != nil {
		return err
	}
	generatedAt := resolveHydroGeneratedAt(req.Problem, req.GeneratedAt)

	exportCases, err := prepareHydroCases(ctx, req.TestCases, req.ObjectReader)
	if err != nil {
		return err
	}
	subtasks, testManifestSHA256, err := buildHydroSubtasksForTestManifestV2(req.Problem.ID, exportCases, req.TestManifestV2)
	if err != nil {
		return err
	}
	if req.TestManifestV2 != nil {
		if req.Problem.TimeLimit <= 0 || req.Problem.MemoryLimit <= 0 {
			return fmt.Errorf("validation: TestManifest v2 Hydro export requires positive problem time and memory limits")
		}
		inheritHydroLimitsV2(subtasks, hydroTimeLimit(req.Problem.TimeLimit), hydroMemoryLimit(req.Problem.MemoryLimit))
	}

	problemYAML, err := yaml.Marshal(hydroProblemYAML{
		PID:   pid,
		Title: strings.TrimSpace(req.Problem.Title),
		Tag:   compactStrings(req.Problem.Tags),
	})
	if err != nil {
		return fmt.Errorf("marshalling problem.yaml: %w", err)
	}
	if err := writeZipFile(zw, prefix+"problem.yaml", problemYAML); err != nil {
		return err
	}

	statement := buildHydroStatement(req.Problem)
	if err := writeZipFile(zw, prefix+"problem_zh.md", []byte(statement)); err != nil {
		return err
	}

	configYAML, err := yaml.Marshal(hydroConfigYAML{
		Type:        "default",
		Time:        hydroTimeLimit(req.Problem.TimeLimit),
		Memory:      hydroMemoryLimit(req.Problem.MemoryLimit),
		Filename:    hydroMeta.Filename,
		CheckerType: "default",
		Detail:      hydroMeta.Detail,
		Subtasks:    subtasks,
	})
	if err != nil {
		return fmt.Errorf("marshalling testdata/config.yaml: %w", err)
	}
	if err := writeZipFile(zw, prefix+"testdata/config.yaml", configYAML); err != nil {
		return err
	}

	for _, ec := range exportCases {
		if err := writeZipFile(zw, prefix+"testdata/"+ec.inputFile, ec.inputData); err != nil {
			return err
		}
		if err := writeZipFile(zw, prefix+"testdata/"+ec.outputFile, ec.outputData); err != nil {
			return err
		}
	}

	manifestData, err := json.MarshalIndent(buildHydroManifest(req.Problem, pid, generatedAt, statement, testManifestSHA256, exportCases), "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling algoforge manifest: %w", err)
	}
	if err := writeZipFile(zw, prefix+"additional_file/algoforge_manifest.json", append(manifestData, '\n')); err != nil {
		return err
	}
	return nil
}

func hydroIdentity(problem *domain.Problem) (string, hydroMetadata, error) {
	if problem == nil {
		return "", hydroMetadata{}, fmt.Errorf("validation: problem is required")
	}
	meta := parseHydroMetadata(problem.MetadataJSON)
	pid := strings.TrimSpace(meta.PID)
	if pid == "" {
		pid = sanitizeHydroPID(problem.SerialNumber)
	}
	if pid == "" {
		pid = "AF" + strings.ReplaceAll(problem.ID.String()[:8], "-", "")
	}
	return pid, meta, nil
}

func parseHydroMetadata(raw json.RawMessage) hydroMetadata {
	var meta hydroMetadata
	if len(raw) == 0 {
		return meta
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil {
		return meta
	}
	for _, key := range []string{"hydro_pid", "pid"} {
		if v, ok := values[key]; ok && meta.PID == "" {
			_ = json.Unmarshal(v, &meta.PID)
		}
	}
	if v, ok := values["hydro"]; ok {
		_ = json.Unmarshal(v, &meta)
	}
	meta.PID = sanitizeHydroPID(meta.PID)
	meta.Filename = sanitizeHydroFilename(meta.Filename)
	meta.CheckerType = strings.TrimSpace(meta.CheckerType)
	meta.Detail = sanitizeHydroDetail(meta.Detail)
	return meta
}

func validateHydroMetadata(meta hydroMetadata) error {
	if meta.CheckerType != "" && meta.CheckerType != "default" {
		return fmt.Errorf("validation: hydro phase1 export supports only checker_type=default, got %q", meta.CheckerType)
	}
	return nil
}

func validateHydroProductMetadataV2(raw json.RawMessage) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil {
		return fmt.Errorf("validation: invalid problem metadata JSON: %w", err)
	}
	for _, key := range []string{"hydro_pid", "pid"} {
		value, ok := values[key]
		if !ok {
			continue
		}
		var pid string
		if err := json.Unmarshal(value, &pid); err != nil {
			return fmt.Errorf("validation: metadata.%s must be a string: %w", key, err)
		}
		if err := validateExplicitHydroPIDV2("metadata."+key, pid); err != nil {
			return err
		}
	}
	rawHydro, ok := values["hydro"]
	if !ok {
		return nil
	}
	if bytes.Equal(bytes.TrimSpace(rawHydro), []byte("null")) {
		return fmt.Errorf("validation: metadata.hydro must be an object")
	}
	var meta hydroMetadata
	if err := decodeS3ExportStrictJSONV1(rawHydro, &meta); err != nil {
		return fmt.Errorf("validation: metadata.hydro contains unknown, unsupported, or invalid fields: %w", err)
	}
	if err := validateExplicitHydroPIDV2("metadata.hydro.pid", meta.PID); err != nil {
		return err
	}
	if meta.Filename != "" {
		if strings.TrimSpace(meta.Filename) != meta.Filename || sanitizeHydroFilename(meta.Filename) != meta.Filename {
			return fmt.Errorf("validation: metadata.hydro.filename must use only A-Z, a-z, 0-9, underscore, or hyphen without leading or trailing separators")
		}
	}
	if meta.CheckerType != "" && meta.CheckerType != "default" {
		return fmt.Errorf("validation: hydro phase1 export supports only checker_type=default, got %q", meta.CheckerType)
	}
	if meta.Detail != "" && meta.Detail != "full" && meta.Detail != "case" && meta.Detail != "none" {
		return fmt.Errorf("validation: metadata.hydro.detail must be one of full, case, or none, got %q", meta.Detail)
	}
	return nil
}

func validateExplicitHydroPIDV2(field, value string) error {
	if value == "" {
		return nil
	}
	if strings.TrimSpace(value) != value || sanitizeHydroPID(value) != value {
		return fmt.Errorf("validation: %s must use a canonical non-numeric-leading Hydro pid", field)
	}
	return nil
}

func inheritHydroLimitsV2(subtasks []hydroSubtask, timeLimit, memoryLimit string) {
	for subtaskIndex := range subtasks {
		subtasks[subtaskIndex].Time = timeLimit
		subtasks[subtaskIndex].Memory = memoryLimit
		for caseIndex := range subtasks[subtaskIndex].Cases {
			subtasks[subtaskIndex].Cases[caseIndex].Time = timeLimit
			subtasks[subtaskIndex].Cases[caseIndex].Memory = memoryLimit
		}
	}
}

func prepareHydroCases(ctx context.Context, testCases []*domain.TestCase, reader HydroObjectReader) ([]hydroExportCase, error) {
	ordered := append([]*domain.TestCase(nil), testCases...)
	for index, testCase := range ordered {
		if testCase == nil {
			return nil, fmt.Errorf("validation: testcase %d is nil", index)
		}
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].TestIndex == ordered[j].TestIndex {
			return ordered[i].ID.String() < ordered[j].ID.String()
		}
		return ordered[i].TestIndex < ordered[j].TestIndex
	})
	scores := hydroCaseScores(ordered)
	exportCases := make([]hydroExportCase, 0, len(ordered))
	for i, tc := range ordered {
		if strings.TrimSpace(tc.InputPath) == "" {
			return nil, fmt.Errorf("validation: testcase %d has no input path", tc.TestIndex)
		}
		if strings.TrimSpace(tc.OutputPath) == "" {
			return nil, fmt.Errorf("validation: testcase %d has no output path", tc.TestIndex)
		}
		inputData, err := reader.DownloadFile(ctx, tc.InputPath)
		if err != nil {
			return nil, fmt.Errorf("reading hydro input %d: %w", tc.TestIndex, err)
		}
		outputData, err := reader.DownloadFile(ctx, tc.OutputPath)
		if err != nil {
			return nil, fmt.Errorf("reading hydro output %d: %w", tc.TestIndex, err)
		}
		caseNo := i + 1
		exportCases = append(exportCases, hydroExportCase{
			tc:           tc,
			inputFile:    fmt.Sprintf("%d.in", caseNo),
			outputFile:   fmt.Sprintf("%d.out", caseNo),
			score:        scores[i],
			inputSHA256:  sha256Hex(inputData),
			outputSHA256: sha256Hex(outputData),
			inputData:    append([]byte(nil), inputData...),
			outputData:   append([]byte(nil), outputData...),
		})
	}
	return exportCases, nil
}

func hydroCaseScores(testCases []*domain.TestCase) []int {
	scores := make([]int, len(testCases))
	total := 0
	for i, tc := range testCases {
		if tc.Score > 0 {
			scores[i] = tc.Score
			total += tc.Score
		}
	}
	if total > 0 {
		return scores
	}

	targets := make([]int, 0, len(testCases))
	for i, tc := range testCases {
		if !tc.IsSample {
			targets = append(targets, i)
		}
	}
	if len(targets) == 0 {
		for i := range testCases {
			targets = append(targets, i)
		}
	}
	base := 100 / len(targets)
	remainder := 100 - base*len(targets)
	for _, idx := range targets {
		scores[idx] = base
	}
	if remainder > 0 {
		scores[targets[len(targets)-1]] += remainder
	}
	return scores
}

func buildHydroSubtasks(cases []hydroExportCase) []hydroSubtask {
	positiveGroups := make(map[int]bool)
	for _, ec := range cases {
		if ec.score > 0 {
			positiveGroups[ec.tc.GroupID] = true
		}
	}
	if len(positiveGroups) <= 1 {
		return []hydroSubtask{hydroSubtaskFromCases(1, cases)}
	}

	groupIDs := make([]int, 0, len(positiveGroups))
	for groupID := range positiveGroups {
		groupIDs = append(groupIDs, groupID)
	}
	sort.Ints(groupIDs)

	byGroup := make(map[int][]hydroExportCase, len(groupIDs))
	for _, ec := range cases {
		groupID := ec.tc.GroupID
		if !positiveGroups[groupID] {
			groupID = groupIDs[0]
		}
		byGroup[groupID] = append(byGroup[groupID], ec)
	}

	subtasks := make([]hydroSubtask, 0, len(groupIDs))
	for i, groupID := range groupIDs {
		subtasks = append(subtasks, hydroSubtaskFromCases(i+1, byGroup[groupID]))
	}
	return subtasks
}

func hydroSubtaskFromCases(id int, cases []hydroExportCase) hydroSubtask {
	subtask := hydroSubtask{ID: id, Type: "sum", Cases: make([]hydroCase, 0, len(cases))}
	for _, ec := range cases {
		subtask.Score += ec.score
		subtask.Cases = append(subtask.Cases, hydroCase{
			Input:  ec.inputFile,
			Output: ec.outputFile,
			Score:  ec.score,
		})
	}
	return subtask
}

func buildHydroManifest(problem *domain.Problem, pid string, generatedAt time.Time, statement, testManifestSHA256 string, cases []hydroExportCase) hydroManifest {
	manifestCases := make([]hydroManifestTestCase, 0, len(cases))
	for _, ec := range cases {
		manifestCases = append(manifestCases, hydroManifestTestCase{
			CaseIndex:        ec.tc.TestIndex,
			GroupID:          ec.tc.GroupID,
			IsSample:         ec.tc.IsSample,
			Score:            ec.score,
			InputFile:        "testdata/" + ec.inputFile,
			OutputFile:       "testdata/" + ec.outputFile,
			InputSHA256:      ec.inputSHA256,
			OutputSHA256:     ec.outputSHA256,
			Description:      ec.tc.Description,
			TestID:           ec.testID,
			Purpose:          ec.purpose,
			ConstraintRegion: ec.constraintRegion,
			BoundaryRefs:     append([]string(nil), ec.boundaryRefs...),
		})
	}
	workflowID := ""
	if problem.WorkflowID != nil {
		workflowID = *problem.WorkflowID
	}
	return hydroManifest{
		Format:             hydroExportFormat,
		HydroPhase:         hydroPhaseOne,
		ProblemID:          problem.ID.String(),
		SerialNumber:       problem.SerialNumber,
		HydroPID:           pid,
		Title:              problem.Title,
		WorkflowID:         workflowID,
		GeneratedAt:        generatedAt.Format(time.RFC3339),
		StatementSHA256:    sha256Hex([]byte(statement)),
		TestManifestSHA256: testManifestSHA256,
		Compatibility: hydroCompatibility{
			Supported: []string{
				"problem.yaml pid/title/tag",
				"problem_zh.md markdown statement",
				"type=default",
				"checker_type=default",
				"stdio",
				"file IO filename metadata",
				"subtasks type=sum",
				"per-case score/time/memory defaults",
			},
			ReservedUnsupported: []string{
				"checker_type strict/hustoj/lemon/qduoj/syzoj/testlib/kattis",
				"custom checker/SPJ files",
				"interactive",
				"communication",
				"submit_answer",
				"objective",
				"subtasks type=max",
				"subtasks if dependencies",
				"langs/time_limit_rate/memory_limit_rate",
				"user_extra_files/judge_extra_files",
				"multi_pass",
				"remote_judge",
			},
		},
		TestCases: manifestCases,
	}
}

func buildHydroStatement(problem *domain.Problem) string {
	statement := strings.TrimSpace(problem.Statement)
	if statement == "" {
		return ""
	}
	if strings.HasPrefix(statement, "#") {
		return statement + "\n"
	}
	return fmt.Sprintf("# %s\n\n%s\n", strings.TrimSpace(problem.Title), statement)
}

func writeZipFile(zw *zip.Writer, name string, data []byte) error {
	fw, err := zw.Create(path.Clean(name))
	if err != nil {
		return fmt.Errorf("creating zip entry %q: %w", name, err)
	}
	if _, err := fw.Write(data); err != nil {
		return fmt.Errorf("writing zip entry %q: %w", name, err)
	}
	return nil
}

func hydroTimeLimit(ms int) string {
	if ms <= 0 {
		return "1s"
	}
	if ms%1000 == 0 {
		return fmt.Sprintf("%ds", ms/1000)
	}
	return fmt.Sprintf("%dms", ms)
}

func hydroMemoryLimit(mb int) string {
	if mb <= 0 {
		return "256m"
	}
	return fmt.Sprintf("%dm", mb)
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func compactStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

var (
	hydroPIDUnsafe      = regexp.MustCompile(`[^A-Za-z0-9_]+`)
	hydroFilenameUnsafe = regexp.MustCompile(`[^A-Za-z0-9_-]+`)
)

func sanitizeHydroPID(value string) string {
	value = hydroPIDUnsafe.ReplaceAllString(strings.TrimSpace(value), "")
	if value == "" {
		return ""
	}
	if value[0] >= '0' && value[0] <= '9' {
		value = "P" + value
	}
	return value
}

func sanitizeHydroFilename(value string) string {
	value = hydroFilenameUnsafe.ReplaceAllString(strings.TrimSpace(value), "")
	return strings.Trim(value, "_-")
}

func sanitizeHydroDetail(value string) string {
	switch strings.TrimSpace(value) {
	case "full", "case", "none":
		return strings.TrimSpace(value)
	default:
		return ""
	}
}

func sanitizeFileName(value string) string {
	value = hydroFilenameUnsafe.ReplaceAllString(value, "_")
	value = strings.Trim(value, "_-")
	if value == "" {
		return "problem"
	}
	return value
}

func cleanZipPrefix(prefix string) string {
	prefix = strings.TrimSpace(strings.ReplaceAll(prefix, "\\", "/"))
	if prefix == "" {
		return ""
	}
	prefix = strings.Trim(path.Clean(prefix), "/")
	if prefix == "." {
		return ""
	}
	return prefix + "/"
}

func uniqueHydroDir(pid string, seen map[string]int) string {
	base := sanitizeFileName(pid)
	if base == "" {
		base = "problem"
	}
	seen[base]++
	if seen[base] == 1 {
		return base
	}
	return fmt.Sprintf("%s-%d", base, seen[base])
}
