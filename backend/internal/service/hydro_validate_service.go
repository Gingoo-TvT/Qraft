package service

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"path"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	hydroMaxPackageBytes = 128 << 20
	hydroMaxTextBytes    = 2 << 20
)

type HydroValidationService struct{}

type HydroValidationReport struct {
	Phase        string                  `json:"phase"`
	Valid        bool                    `json:"valid"`
	Mode         string                  `json:"mode"`
	ProblemCount int                     `json:"problem_count"`
	SuccessCount int                     `json:"success_count"`
	FailedCount  int                     `json:"failed_count"`
	Problems     []HydroValidatedProblem `json:"problems"`
	Errors       []HydroValidationIssue  `json:"errors,omitempty"`
}

type HydroValidatedProblem struct {
	Path           string                 `json:"path"`
	PID            string                 `json:"pid"`
	Title          string                 `json:"title"`
	StatementFiles []string               `json:"statement_files"`
	TestDataFiles  int                    `json:"testdata_files"`
	CaseCount      int                    `json:"case_count"`
	ConfigMode     string                 `json:"config_mode"`
	Valid          bool                   `json:"valid"`
	Errors         []HydroValidationIssue `json:"errors,omitempty"`
	Warnings       []HydroValidationIssue `json:"warnings,omitempty"`
	Unsupported    []HydroValidationIssue `json:"unsupported,omitempty"`
}

type HydroValidationIssue struct {
	Path    string `json:"path,omitempty"`
	Field   string `json:"field,omitempty"`
	Message string `json:"message"`
}

type hydroPackageEntries map[string]*zip.File

type hydroParsedProblemYAML struct {
	PID   string   `yaml:"pid"`
	Title string   `yaml:"title"`
	Tag   []string `yaml:"tag,omitempty"`
}

type hydroParsedConfig struct {
	Type        string               `yaml:"type"`
	Time        string               `yaml:"time"`
	Memory      string               `yaml:"memory"`
	Filename    string               `yaml:"filename"`
	CheckerType string               `yaml:"checker_type"`
	Detail      string               `yaml:"detail"`
	Cases       []hydroParsedCase    `yaml:"cases"`
	Subtasks    []hydroParsedSubtask `yaml:"subtasks"`
}

type hydroParsedCase struct {
	Input  string `yaml:"input"`
	Output string `yaml:"output"`
	Score  *int   `yaml:"score"`
	Time   string `yaml:"time"`
	Memory string `yaml:"memory"`
}

type hydroParsedSubtask struct {
	ID     int               `yaml:"id"`
	Score  *int              `yaml:"score"`
	Type   string            `yaml:"type"`
	Time   string            `yaml:"time"`
	Memory string            `yaml:"memory"`
	Cases  []hydroParsedCase `yaml:"cases"`
}

func NewHydroValidationService() *HydroValidationService {
	return &HydroValidationService{}
}

func (s *HydroValidationService) ValidatePackage(ctx context.Context, reader io.Reader) (*HydroValidationReport, error) {
	if reader == nil {
		return nil, fmt.Errorf("file is required")
	}
	data, err := readHydroPackageBytes(reader)
	if err != nil {
		return nil, err
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("opening hydro zip: %w", err)
	}
	entries, topErrors := collectHydroEntries(zr)
	roots, mode, rootErrors := detectHydroProblemRoots(entries)

	report := &HydroValidationReport{
		Phase:  hydroPhaseOne,
		Mode:   mode,
		Errors: append(topErrors, rootErrors...),
	}
	if len(rootErrors) > 0 {
		report.Valid = false
		return report, nil
	}

	pidFirstPath := make(map[string]string, len(roots))
	for _, root := range roots {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		problem := validateHydroProblemRoot(entries, root)
		if problem.PID != "" {
			if firstPath, exists := pidFirstPath[problem.PID]; exists {
				issue := HydroValidationIssue{
					Path:    problem.Path,
					Field:   "problem.yaml.pid",
					Message: fmt.Sprintf("duplicate pid %q also appears at %s", problem.PID, firstPath),
				}
				problem.Errors = append(problem.Errors, issue)
				report.Errors = append(report.Errors, issue)
			} else {
				pidFirstPath[problem.PID] = problem.Path
			}
		}
		problem.Valid = len(problem.Errors) == 0 && len(problem.Unsupported) == 0
		if problem.Valid {
			report.SuccessCount++
		} else {
			report.FailedCount++
		}
		report.Problems = append(report.Problems, problem)
	}
	report.ProblemCount = len(report.Problems)
	report.Valid = len(report.Errors) == 0 && report.FailedCount == 0 && len(topErrors) == 0
	return report, nil
}

func readHydroPackageBytes(reader io.Reader) ([]byte, error) {
	limited := io.LimitReader(reader, hydroMaxPackageBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("reading hydro upload: %w", err)
	}
	if len(data) > hydroMaxPackageBytes {
		return nil, fmt.Errorf("hydro zip exceeds %d bytes", hydroMaxPackageBytes)
	}
	return data, nil
}

func collectHydroEntries(zr *zip.Reader) (hydroPackageEntries, []HydroValidationIssue) {
	entries := make(hydroPackageEntries, len(zr.File))
	var issues []HydroValidationIssue
	for _, file := range zr.File {
		clean, ok := cleanHydroEntryName(file.Name)
		if !ok {
			issues = append(issues, HydroValidationIssue{Path: file.Name, Message: "unsafe zip path"})
			continue
		}
		if file.FileInfo().IsDir() {
			continue
		}
		if _, exists := entries[clean]; exists {
			issues = append(issues, HydroValidationIssue{Path: clean, Message: "duplicate zip entry"})
			continue
		}
		entries[clean] = file
	}
	return entries, issues
}

func cleanHydroEntryName(name string) (string, bool) {
	name = strings.ReplaceAll(name, "\\", "/")
	if strings.HasPrefix(name, "/") || strings.Contains(name, ":") {
		return "", false
	}
	clean := path.Clean(name)
	if clean == "." || strings.HasPrefix(clean, "../") || clean == ".." || strings.Contains(clean, "/../") {
		return "", false
	}
	return clean, true
}

func detectHydroProblemRoots(entries hydroPackageEntries) ([]string, string, []HydroValidationIssue) {
	if _, ok := entries["problem.yaml"]; ok {
		return []string{""}, "single", nil
	}

	rootSet := make(map[string]bool)
	for name := range entries {
		dir, file := path.Split(name)
		if file != "problem.yaml" {
			continue
		}
		root := strings.TrimSuffix(dir, "/")
		if root == "" || strings.Contains(root, "/") {
			continue
		}
		rootSet[root] = true
	}
	if len(rootSet) == 0 {
		return nil, "unknown", []HydroValidationIssue{{Field: "problem.yaml", Message: "zip must contain problem.yaml at root or inside direct problem directories"}}
	}
	roots := make([]string, 0, len(rootSet))
	for root := range rootSet {
		roots = append(roots, root)
	}
	sort.Strings(roots)
	return roots, "batch", nil
}

func validateHydroProblemRoot(entries hydroPackageEntries, root string) HydroValidatedProblem {
	prefix := cleanZipPrefix(root)
	displayPath := "."
	if root != "" {
		displayPath = root
	}
	result := HydroValidatedProblem{Path: displayPath}

	problemFile := prefix + "problem.yaml"
	rawProblem, err := readHydroTextEntry(entries, problemFile)
	if err != nil {
		result.Errors = append(result.Errors, HydroValidationIssue{Path: problemFile, Field: "problem.yaml", Message: err.Error()})
		return result
	}
	var problemYAML hydroParsedProblemYAML
	if err := yaml.Unmarshal(rawProblem, &problemYAML); err != nil {
		result.Errors = append(result.Errors, HydroValidationIssue{Path: problemFile, Field: "problem.yaml", Message: "invalid YAML: " + err.Error()})
		return result
	}
	result.PID = sanitizeHydroPID(problemYAML.PID)
	result.Title = strings.TrimSpace(problemYAML.Title)
	if result.PID == "" {
		result.Errors = append(result.Errors, HydroValidationIssue{Path: problemFile, Field: "pid", Message: "pid is required"})
	}
	if result.Title == "" {
		result.Errors = append(result.Errors, HydroValidationIssue{Path: problemFile, Field: "title", Message: "title is required"})
	}

	result.StatementFiles = directHydroStatementFiles(entries, prefix)
	if len(result.StatementFiles) == 0 {
		result.Errors = append(result.Errors, HydroValidationIssue{Path: displayPath, Field: "problem_*.md", Message: "at least one statement markdown file is required"})
	}

	testDataFiles := directHydroTestDataFiles(entries, prefix)
	result.TestDataFiles = len(testDataFiles)
	if len(testDataFiles) == 0 {
		result.Errors = append(result.Errors, HydroValidationIssue{Path: prefix + "testdata", Field: "testdata", Message: "testdata directory must contain formal test files"})
	}

	configPath := prefix + "testdata/config.yaml"
	if _, ok := entries[configPath]; ok {
		validateHydroConfig(entries, prefix, configPath, &result)
	} else {
		result.ConfigMode = "auto_detect"
		result.CaseCount = validateHydroAutoCases(entries, prefix, &result)
	}
	return result
}

func readHydroTextEntry(entries hydroPackageEntries, name string) ([]byte, error) {
	file, ok := entries[name]
	if !ok {
		return nil, fmt.Errorf("missing required file")
	}
	if file.UncompressedSize64 > hydroMaxTextBytes {
		return nil, fmt.Errorf("file exceeds %d bytes", hydroMaxTextBytes)
	}
	rc, err := file.Open()
	if err != nil {
		return nil, fmt.Errorf("open failed: %w", err)
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, hydroMaxTextBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read failed: %w", err)
	}
	if len(data) > hydroMaxTextBytes {
		return nil, fmt.Errorf("file exceeds %d bytes", hydroMaxTextBytes)
	}
	return data, nil
}

func directHydroStatementFiles(entries hydroPackageEntries, prefix string) []string {
	files := make([]string, 0)
	for name := range entries {
		rel, ok := strings.CutPrefix(name, prefix)
		if !ok || strings.Contains(rel, "/") {
			continue
		}
		if strings.HasPrefix(rel, "problem_") && strings.HasSuffix(rel, ".md") {
			files = append(files, rel)
		}
	}
	sort.Strings(files)
	return files
}

func directHydroTestDataFiles(entries hydroPackageEntries, prefix string) []string {
	files := make([]string, 0)
	testPrefix := prefix + "testdata/"
	for name := range entries {
		rel, ok := strings.CutPrefix(name, testPrefix)
		if !ok || rel == "" || strings.Contains(rel, "/") || rel == "config.yaml" {
			continue
		}
		files = append(files, rel)
	}
	sort.Strings(files)
	return files
}

func validateHydroConfig(entries hydroPackageEntries, prefix, configPath string, result *HydroValidatedProblem) {
	raw, err := readHydroTextEntry(entries, configPath)
	if err != nil {
		result.Errors = append(result.Errors, HydroValidationIssue{Path: configPath, Field: "testdata/config.yaml", Message: err.Error()})
		return
	}

	var top map[string]interface{}
	if err := yaml.Unmarshal(raw, &top); err != nil {
		result.Errors = append(result.Errors, HydroValidationIssue{Path: configPath, Field: "testdata/config.yaml", Message: "invalid YAML: " + err.Error()})
		return
	}
	for _, key := range sortedMapKeys(top) {
		if !hydroPhaseOneTopKeys[key] {
			result.Unsupported = append(result.Unsupported, HydroValidationIssue{Path: configPath, Field: key, Message: "Hydro phase1 does not support this top-level config field"})
		}
	}

	var cfg hydroParsedConfig
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		result.Errors = append(result.Errors, HydroValidationIssue{Path: configPath, Field: "testdata/config.yaml", Message: "invalid config structure: " + err.Error()})
		return
	}
	typ := strings.TrimSpace(cfg.Type)
	if typ != "" && typ != "default" {
		result.Unsupported = append(result.Unsupported, HydroValidationIssue{Path: configPath, Field: "type", Message: fmt.Sprintf("unsupported Hydro type %q in phase1", typ)})
	}
	checkerType := strings.TrimSpace(cfg.CheckerType)
	if checkerType != "" && checkerType != "default" {
		result.Unsupported = append(result.Unsupported, HydroValidationIssue{Path: configPath, Field: "checker_type", Message: fmt.Sprintf("unsupported checker_type %q in phase1", checkerType)})
	}

	if _, hasCases := top["cases"]; hasCases {
		result.ConfigMode = "cases"
		result.CaseCount = validateHydroCases(entries, prefix, configPath, "cases", cfg.Cases, result)
		if _, hasSubtasks := top["subtasks"]; hasSubtasks {
			result.Warnings = append(result.Warnings, HydroValidationIssue{Path: configPath, Field: "subtasks", Message: "Hydro gives cases priority; subtasks will be ignored"})
		}
		return
	}
	if _, hasSubtasks := top["subtasks"]; hasSubtasks {
		result.ConfigMode = "subtasks"
		result.CaseCount = validateHydroSubtasks(entries, prefix, configPath, raw, cfg.Subtasks, result)
		return
	}

	result.ConfigMode = "auto_detect"
	result.CaseCount = validateHydroAutoCases(entries, prefix, result)
}

var hydroPhaseOneTopKeys = map[string]bool{
	"type":         true,
	"time":         true,
	"memory":       true,
	"filename":     true,
	"checker_type": true,
	"cases":        true,
	"subtasks":     true,
	"detail":       true,
}

func validateHydroCases(entries hydroPackageEntries, prefix, configPath, fieldPrefix string, cases []hydroParsedCase, result *HydroValidatedProblem) int {
	if len(cases) == 0 {
		result.Errors = append(result.Errors, HydroValidationIssue{Path: configPath, Field: fieldPrefix, Message: "at least one case is required"})
		return 0
	}
	for i, c := range cases {
		validateHydroCase(entries, prefix, configPath, fmt.Sprintf("%s[%d]", fieldPrefix, i), c, result)
	}
	return len(cases)
}

func validateHydroSubtasks(entries hydroPackageEntries, prefix, configPath string, raw []byte, subtasks []hydroParsedSubtask, result *HydroValidatedProblem) int {
	if len(subtasks) == 0 {
		result.Errors = append(result.Errors, HydroValidationIssue{Path: configPath, Field: "subtasks", Message: "at least one subtask is required"})
		return 0
	}
	var rawConfig map[string][]map[string]interface{}
	_ = yaml.Unmarshal(raw, &rawConfig)
	caseCount := 0
	for i, st := range subtasks {
		field := fmt.Sprintf("subtasks[%d]", i)
		rawSubtask := map[string]interface{}{}
		if len(rawConfig["subtasks"]) > i {
			rawSubtask = rawConfig["subtasks"][i]
		}
		for _, key := range sortedMapKeys(rawSubtask) {
			if !hydroPhaseOneSubtaskKeys[key] {
				result.Unsupported = append(result.Unsupported, HydroValidationIssue{Path: configPath, Field: field + "." + key, Message: "Hydro phase1 does not support this subtask field"})
			}
		}
		if _, hasIf := rawSubtask["if"]; hasIf {
			result.Unsupported = append(result.Unsupported, HydroValidationIssue{Path: configPath, Field: field + ".if", Message: "subtask dependencies are reserved for phase2"})
		}
		typ := strings.TrimSpace(st.Type)
		if typ == "" {
			typ = "min"
		}
		if typ != "sum" && typ != "min" {
			result.Unsupported = append(result.Unsupported, HydroValidationIssue{Path: configPath, Field: field + ".type", Message: fmt.Sprintf("unsupported subtask type %q in phase1", typ)})
		}
		caseCount += validateHydroCases(entries, prefix, configPath, field+".cases", st.Cases, result)
	}
	return caseCount
}

var hydroPhaseOneSubtaskKeys = map[string]bool{
	"id":     true,
	"score":  true,
	"type":   true,
	"time":   true,
	"memory": true,
	"cases":  true,
	"if":     true,
}

func validateHydroCase(entries hydroPackageEntries, prefix, configPath, field string, c hydroParsedCase, result *HydroValidatedProblem) {
	input := strings.TrimSpace(c.Input)
	output := strings.TrimSpace(c.Output)
	if input == "" {
		result.Errors = append(result.Errors, HydroValidationIssue{Path: configPath, Field: field + ".input", Message: "case input is required"})
		return
	}
	if !hydroTestDataFileExists(entries, prefix, input) {
		result.Errors = append(result.Errors, HydroValidationIssue{Path: configPath, Field: field + ".input", Message: fmt.Sprintf("input file %q does not exist", input)})
	}
	if output == "" {
		result.Warnings = append(result.Warnings, HydroValidationIssue{Path: configPath, Field: field + ".output", Message: "missing output uses Hydro /dev/null behavior"})
		return
	}
	if output == "/dev/null" {
		return
	}
	if !hydroTestDataFileExists(entries, prefix, output) {
		result.Errors = append(result.Errors, HydroValidationIssue{Path: configPath, Field: field + ".output", Message: fmt.Sprintf("output file %q does not exist", output)})
	}
}

func hydroTestDataFileExists(entries hydroPackageEntries, prefix, rel string) bool {
	rel = strings.TrimSpace(strings.ReplaceAll(rel, "\\", "/"))
	if rel == "" || strings.HasPrefix(rel, "/") || strings.Contains(rel, "..") {
		return false
	}
	_, ok := entries[prefix+"testdata/"+path.Clean(rel)]
	return ok
}

var (
	hydroAutoNumberRe = regexp.MustCompile(`[0-9]`)
	hydroInputTxtRe   = regexp.MustCompile(`^input([0-9]+)\.txt$`)
)

func validateHydroAutoCases(entries hydroPackageEntries, prefix string, result *HydroValidatedProblem) int {
	testFiles := directHydroTestDataFiles(entries, prefix)
	fileSet := make(map[string]bool, len(testFiles))
	for _, file := range testFiles {
		fileSet[file] = true
	}
	var paired int
	var skipped []string
	for _, file := range testFiles {
		if match := hydroInputTxtRe.FindStringSubmatch(file); len(match) == 2 {
			if fileSet["output"+match[1]+".txt"] {
				paired++
			} else {
				skipped = append(skipped, file)
			}
			continue
		}
		if !strings.HasSuffix(file, ".in") {
			continue
		}
		stem := strings.TrimSuffix(file, ".in")
		if !hydroAutoNumberRe.MatchString(stem) {
			skipped = append(skipped, file)
			continue
		}
		if fileSet[stem+".out"] || fileSet[stem+".ans"] {
			paired++
		} else {
			skipped = append(skipped, file)
		}
	}
	if paired == 0 {
		result.Errors = append(result.Errors, HydroValidationIssue{Path: prefix + "testdata", Field: "testdata", Message: "auto-detect found no formal input/output pairs"})
	}
	for _, file := range skipped {
		result.Warnings = append(result.Warnings, HydroValidationIssue{Path: prefix + "testdata/" + file, Field: "testdata", Message: "file was not recognized as a formal auto-detected test pair"})
	}
	return paired
}

func sortedMapKeys(values map[string]interface{}) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
