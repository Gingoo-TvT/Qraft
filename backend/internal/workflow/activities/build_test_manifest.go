package activities

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"go.temporal.io/sdk/temporal"
)

const (
	TestManifestSchemaVersion  = "algoforge.test-manifest.v1"
	TestManifestComparisonMode = "trim-trailing-whitespace-v1"
)

// BuildTestManifestInput contains only already generated and validated data.
// The activity resolves durable artifacts so the manifest hashes the exact
// bytes that StoreProblem will persist.
type BuildTestManifestInput struct {
	PayloadVersion   int                   `json:"payload_version"`
	TestCases        []TestCaseData        `json:"test_cases"`
	MainOutput       SandboxResult         `json:"main_output"`
	BruteOutput      SandboxResult         `json:"brute_output"`
	BruteIndices     []int                 `json:"brute_indices"`
	MainSolution     domain.Solution       `json:"main_solution"`
	BruteSolution    domain.Solution       `json:"brute_solution"`
	GeneratorSHA256  string                `json:"generator_sha256,omitempty"`
	GeneratorBatches []GeneratorBatchAudit `json:"generator_batches,omitempty"`
}

// TestManifestV1 is the compact, stable quality evidence for generated tests.
type TestManifestV1 struct {
	SchemaVersion            string                       `json:"schema_version"`
	ComparisonMode           string                       `json:"comparison_mode"`
	TestCount                int                          `json:"test_count"`
	DifferentialCheckedCount int                          `json:"differential_checked_count"`
	GeneratorSHA256          string                       `json:"generator_sha256,omitempty"`
	GeneratorBatches         []TestManifestGeneratorBatch `json:"generator_batches,omitempty"`
	MainSolutionSHA256       string                       `json:"main_solution_sha256"`
	BruteSolutionSHA256      string                       `json:"brute_solution_sha256"`
	MainSandbox              TestManifestSandboxIdentity  `json:"main_sandbox"`
	BruteSandbox             TestManifestSandboxIdentity  `json:"brute_sandbox"`
	Cases                    []TestManifestCase           `json:"cases"`
}

type TestManifestGeneratorBatch struct {
	BatchIndex           int                  `json:"batch_index"`
	TestIndexes          []int                `json:"test_indexes"`
	GeneratorCaseIndexes []int                `json:"generator_case_indexes"`
	Audit                SandboxAuditMetadata `json:"audit"`
}

type TestManifestSandboxIdentity struct {
	ManifestDigest          string `json:"manifest_digest"`
	ImageDigest             string `json:"image_digest"`
	ToolchainManifestDigest string `json:"toolchain_manifest_digest"`
	SeccompPolicyDigest     string `json:"seccomp_policy_digest"`
	LimitProfile            string `json:"limit_profile"`
	Seed                    int64  `json:"seed"`
}

type TestManifestCase struct {
	TestIndex            int            `json:"test_index"`
	GroupID              int            `json:"group_id"`
	IsSample             bool           `json:"is_sample"`
	Purpose              string         `json:"purpose"`
	Coverage             []string       `json:"coverage,omitempty"`
	Origin               TestCaseOrigin `json:"origin"`
	GeneratorSeed        int64          `json:"generator_seed,omitempty"`
	GeneratorCaseIndex   *int           `json:"generator_case_index,omitempty"`
	GeneratorBatchIndex  *int           `json:"generator_batch_index,omitempty"`
	InputSHA256          string         `json:"input_sha256"`
	ExpectedOutputSHA256 string         `json:"expected_output_sha256"`
	DifferentialChecked  bool           `json:"differential_checked"`
	DifferentialMatch    bool           `json:"differential_match,omitempty"`
	BruteOutputSHA256    string         `json:"brute_output_sha256,omitempty"`
}

func (a *Activities) BuildTestManifestActivity(ctx context.Context, input BuildTestManifestInput) (*TestManifestV1, error) {
	if input.PayloadVersion != ActivityPayloadVersion {
		return nil, testManifestQualityError("unsupported test-manifest payload version %d", input.PayloadVersion)
	}
	if input.MainOutput.PayloadVersion != ActivityPayloadVersion || input.BruteOutput.PayloadVersion != ActivityPayloadVersion {
		return nil, testManifestQualityError("test manifest requires versioned main and brute sandbox outputs")
	}
	if sandboxResultLength(input.MainOutput) != len(input.TestCases) {
		return nil, testManifestQualityError("main sandbox produced %d outputs for %d tests", sandboxResultLength(input.MainOutput), len(input.TestCases))
	}
	if sandboxResultLength(input.BruteOutput) != len(input.BruteIndices) {
		return nil, testManifestQualityError("brute sandbox produced %d outputs for %d selected tests", sandboxResultLength(input.BruteOutput), len(input.BruteIndices))
	}
	if len(input.BruteIndices) > MaxReferenceDifferentialCases {
		return nil, testManifestQualityError("brute differential selection contains %d cases, maximum is %d", len(input.BruteIndices), MaxReferenceDifferentialCases)
	}

	brutePositions := make(map[int]int, len(input.BruteIndices))
	for position, testIndex := range input.BruteIndices {
		if testIndex < 0 || testIndex >= len(input.TestCases) {
			return nil, testManifestQualityError("brute test index %d is outside the generated test set", testIndex)
		}
		if _, exists := brutePositions[testIndex]; exists {
			return nil, testManifestQualityError("brute test index %d is duplicated", testIndex)
		}
		brutePositions[testIndex] = position
	}

	manifest := &TestManifestV1{
		SchemaVersion:            TestManifestSchemaVersion,
		ComparisonMode:           TestManifestComparisonMode,
		TestCount:                len(input.TestCases),
		DifferentialCheckedCount: len(input.BruteIndices),
		GeneratorSHA256:          input.GeneratorSHA256,
		GeneratorBatches:         testManifestGeneratorBatches(input.GeneratorBatches),
		MainSolutionSHA256:       manifestSolutionSHA256(input.MainSolution),
		BruteSolutionSHA256:      manifestSolutionSHA256(input.BruteSolution),
		MainSandbox:              testManifestSandboxIdentity(input.MainOutput.Audit),
		BruteSandbox:             testManifestSandboxIdentity(input.BruteOutput.Audit),
		Cases:                    make([]TestManifestCase, 0, len(input.TestCases)),
	}
	for testIndex, testCase := range input.TestCases {
		inputBytes, err := a.resolveTestManifestInput(ctx, testCase)
		if err != nil {
			return nil, testManifestQualityError("resolve test %d input: %v", testIndex, err)
		}
		mainBytes, err := a.resolveTestManifestOutput(ctx, input.MainOutput, testIndex)
		if err != nil {
			return nil, testManifestQualityError("resolve test %d main output: %v", testIndex, err)
		}
		entry := TestManifestCase{
			TestIndex:            testIndex,
			GroupID:              testCase.GroupID,
			IsSample:             testCase.IsSample,
			Purpose:              strings.TrimSpace(testCase.Description),
			Coverage:             normalizeCoverageTags(testCase.Coverage),
			Origin:               testCase.Origin,
			GeneratorSeed:        testCase.GeneratorSeed,
			GeneratorCaseIndex:   testCase.GeneratorCaseIndex,
			GeneratorBatchIndex:  testCase.GeneratorBatchIndex,
			InputSHA256:          manifestSHA256(inputBytes),
			ExpectedOutputSHA256: manifestSHA256(mainBytes),
		}
		if brutePosition, checked := brutePositions[testIndex]; checked {
			bruteBytes, err := a.resolveTestManifestOutput(ctx, input.BruteOutput, brutePosition)
			if err != nil {
				return nil, testManifestQualityError("resolve test %d brute output: %v", testIndex, err)
			}
			if normalizeOutput(string(mainBytes)) != normalizeOutput(string(bruteBytes)) {
				return nil, testManifestQualityError("test %d main and brute outputs do not match under %s", testIndex, TestManifestComparisonMode)
			}
			entry.DifferentialChecked = true
			entry.DifferentialMatch = true
			entry.BruteOutputSHA256 = manifestSHA256(bruteBytes)
		}
		manifest.Cases = append(manifest.Cases, entry)
	}
	if err := manifest.Validate(len(input.TestCases)); err != nil {
		return nil, testManifestQualityError("invalid test manifest: %v", err)
	}
	return manifest, nil
}

func (manifest TestManifestV1) Validate(expectedTests int) error {
	if manifest.SchemaVersion != TestManifestSchemaVersion {
		return fmt.Errorf("unsupported schema %q", manifest.SchemaVersion)
	}
	if manifest.ComparisonMode != TestManifestComparisonMode {
		return fmt.Errorf("unsupported comparison mode %q", manifest.ComparisonMode)
	}
	if !isManifestSHA256(manifest.MainSolutionSHA256) || !isManifestSHA256(manifest.BruteSolutionSHA256) {
		return fmt.Errorf("solution identity is not a canonical SHA-256 digest")
	}
	if manifest.GeneratorSHA256 != "" && !isManifestSHA256(manifest.GeneratorSHA256) {
		return fmt.Errorf("generator identity is not a canonical SHA-256 digest")
	}
	if err := validateTestManifestSandboxIdentity("main", manifest.MainSandbox); err != nil {
		return err
	}
	if err := validateTestManifestSandboxIdentity("brute", manifest.BruteSandbox); err != nil {
		return err
	}
	if manifest.TestCount != expectedTests || len(manifest.Cases) != expectedTests {
		return fmt.Errorf("test count is %d with %d entries, want %d", manifest.TestCount, len(manifest.Cases), expectedTests)
	}
	generatorCoverage, err := manifest.validateGeneratorBatches()
	if err != nil {
		return err
	}
	checked := 0
	hasGeneratorCase := false
	for index, entry := range manifest.Cases {
		if entry.TestIndex != index {
			return fmt.Errorf("entry %d records test index %d", index, entry.TestIndex)
		}
		if entry.Purpose == "" || entry.Purpose != strings.TrimSpace(entry.Purpose) {
			return fmt.Errorf("entry %d purpose is empty or not canonical", index)
		}
		if !coverageTagsCanonical(entry.Coverage) {
			return fmt.Errorf("entry %d coverage labels are not canonical", index)
		}
		if !isManifestSHA256(entry.InputSHA256) || !isManifestSHA256(entry.ExpectedOutputSHA256) {
			return fmt.Errorf("entry %d has a non-canonical input or expected-output digest", index)
		}
		switch entry.Origin {
		case TestCaseOriginLLMInline, TestCaseOriginCustom:
			if entry.GeneratorSeed != 0 || entry.GeneratorCaseIndex != nil || entry.GeneratorBatchIndex != nil || generatorCoverage[index] != 0 {
				return fmt.Errorf("entry %d has generator replay metadata for origin %q", index, entry.Origin)
			}
		case TestCaseOriginGenerator:
			hasGeneratorCase = true
			if entry.GeneratorSeed == 0 {
				return fmt.Errorf("entry %d is generator-backed without a seed", index)
			}
			if entry.GeneratorCaseIndex == nil || *entry.GeneratorCaseIndex < 0 {
				return fmt.Errorf("entry %d is generator-backed without an original case index", index)
			}
			if entry.GeneratorBatchIndex == nil || *entry.GeneratorBatchIndex < 0 {
				return fmt.Errorf("entry %d is generator-backed without a batch index", index)
			}
			if generatorCoverage[index] != 1 {
				return fmt.Errorf("entry %d is generator-backed without exactly one batch audit", index)
			}
		default:
			return fmt.Errorf("entry %d has unsupported origin %q", index, entry.Origin)
		}
		if entry.DifferentialChecked {
			checked++
			if !entry.DifferentialMatch || !isManifestSHA256(entry.BruteOutputSHA256) {
				return fmt.Errorf("entry %d is differential-checked without a verified match and canonical brute digest", index)
			}
		} else if entry.DifferentialMatch || entry.BruteOutputSHA256 != "" {
			return fmt.Errorf("entry %d records differential evidence without differential coverage", index)
		}
	}
	if hasGeneratorCase && manifest.GeneratorSHA256 == "" {
		return fmt.Errorf("generator-backed cases require generator identity")
	}
	if checked != manifest.DifferentialCheckedCount {
		return fmt.Errorf("differential count is %d, entries record %d", manifest.DifferentialCheckedCount, checked)
	}
	return nil
}

func (manifest TestManifestV1) validateGeneratorBatches() (map[int]int, error) {
	coverage := make(map[int]int)
	for batchPosition, batch := range manifest.GeneratorBatches {
		if batch.BatchIndex != batchPosition {
			return nil, fmt.Errorf("generator batch %d records batch index %d", batchPosition, batch.BatchIndex)
		}
		if len(batch.TestIndexes) == 0 || len(batch.TestIndexes) != len(batch.GeneratorCaseIndexes) {
			return nil, fmt.Errorf("generator batch %d has incomplete case mapping", batchPosition)
		}
		if strings.TrimSpace(batch.Audit.RunID) == "" {
			return nil, fmt.Errorf("generator batch %d run id is empty", batchPosition)
		}
		if batch.Audit.Seed == 0 {
			return nil, fmt.Errorf("generator batch %d seed is empty", batchPosition)
		}
		if err := validateTestManifestSandboxIdentity(fmt.Sprintf("generator batch %d", batchPosition), testManifestSandboxIdentity(batch.Audit)); err != nil {
			return nil, err
		}
		for mappingIndex, testIndex := range batch.TestIndexes {
			if testIndex < 0 || testIndex >= len(manifest.Cases) {
				return nil, fmt.Errorf("generator batch %d test index %d is outside the manifest", batchPosition, testIndex)
			}
			entry := manifest.Cases[testIndex]
			if entry.Origin != TestCaseOriginGenerator {
				return nil, fmt.Errorf("generator batch %d maps non-generator test %d", batchPosition, testIndex)
			}
			if entry.GeneratorCaseIndex == nil || *entry.GeneratorCaseIndex != batch.GeneratorCaseIndexes[mappingIndex] {
				return nil, fmt.Errorf("generator batch %d original case index does not match test %d", batchPosition, testIndex)
			}
			if entry.GeneratorBatchIndex == nil || *entry.GeneratorBatchIndex != batch.BatchIndex {
				return nil, fmt.Errorf("generator batch %d is not bound to test %d", batchPosition, testIndex)
			}
			coverage[testIndex]++
			if coverage[testIndex] != 1 {
				return nil, fmt.Errorf("generator test %d is mapped by more than one batch", testIndex)
			}
		}
	}
	return coverage, nil
}

func testManifestGeneratorBatches(batches []GeneratorBatchAudit) []TestManifestGeneratorBatch {
	result := make([]TestManifestGeneratorBatch, 0, len(batches))
	for _, batch := range batches {
		result = append(result, TestManifestGeneratorBatch{
			BatchIndex:           batch.BatchIndex,
			TestIndexes:          append([]int(nil), batch.TestIndexes...),
			GeneratorCaseIndexes: append([]int(nil), batch.GeneratorCaseIndexes...),
			Audit:                batch.Audit,
		})
	}
	return result
}

func CanonicalTestManifestJSON(manifest TestManifestV1) (json.RawMessage, string, error) {
	if err := manifest.Validate(manifest.TestCount); err != nil {
		return nil, "", err
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return nil, "", fmt.Errorf("encoding test manifest: %w", err)
	}
	return encoded, manifestSHA256(encoded), nil
}

// ParseTestManifestJSON decodes the immutable v1 manifest published with a
// generated problem.  Validation is deliberately performed here, at the
// activity boundary, so a validation workflow never silently falls back to
// running the brute/reference program on the full official suite when the
// persisted differential selection is corrupt or tampered with.
//
// The bytes must be the canonical JSON emitted by CanonicalTestManifestJSON;
// accepting semantically equivalent but differently encoded JSON would make
// the metadata SHA256 binding ambiguous.
func ParseTestManifestJSON(data []byte) (*TestManifestV1, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var manifest TestManifestV1
	if err := decoder.Decode(&manifest); err != nil {
		return nil, err
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return nil, err
	}
	if err := manifest.Validate(manifest.TestCount); err != nil {
		return nil, err
	}
	canonical, _, err := CanonicalTestManifestJSON(manifest)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(data, canonical) {
		return nil, fmt.Errorf("TestManifest v1 bytes are not canonical")
	}
	return &manifest, nil
}

func (a *Activities) resolveTestManifestInput(ctx context.Context, testCase TestCaseData) ([]byte, error) {
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

func (a *Activities) resolveTestManifestOutput(ctx context.Context, result SandboxResult, index int) ([]byte, error) {
	switch {
	case index < len(result.OutputArtifacts) && result.OutputArtifacts[index] != nil:
		return a.getArtifact(ctx, result.OutputArtifacts[index])
	case index < len(result.OutputRefs) && result.OutputRefs[index] != "":
		return os.ReadFile(result.OutputRefs[index])
	case index < len(result.Outputs):
		return []byte(result.Outputs[index]), nil
	default:
		return nil, fmt.Errorf("sandbox output is missing")
	}
}

func sandboxResultLength(result SandboxResult) int {
	length := len(result.Outputs)
	if len(result.OutputArtifacts) > length {
		length = len(result.OutputArtifacts)
	}
	if len(result.OutputRefs) > length {
		length = len(result.OutputRefs)
	}
	return length
}

func testManifestSandboxIdentity(audit SandboxAuditMetadata) TestManifestSandboxIdentity {
	return TestManifestSandboxIdentity{
		ManifestDigest:          audit.ManifestDigest,
		ImageDigest:             audit.ImageDigest,
		ToolchainManifestDigest: audit.ToolchainManifestDigest,
		SeccompPolicyDigest:     audit.SeccompPolicyDigest,
		LimitProfile:            audit.LimitProfile,
		Seed:                    audit.Seed,
	}
}

func validateTestManifestSandboxIdentity(label string, identity TestManifestSandboxIdentity) error {
	for field, value := range map[string]string{
		"manifest_digest":           identity.ManifestDigest,
		"image_digest":              identity.ImageDigest,
		"toolchain_manifest_digest": identity.ToolchainManifestDigest,
		"seccomp_policy_digest":     identity.SeccompPolicyDigest,
	} {
		if !isSandboxSHA256(value) {
			return fmt.Errorf("%s sandbox %s is not a canonical SHA-256 identity", label, field)
		}
	}
	if strings.TrimSpace(identity.LimitProfile) == "" {
		return fmt.Errorf("%s sandbox limit profile is empty", label)
	}
	return nil
}

func manifestSolutionSHA256(solution domain.Solution) string {
	encoded, _ := json.Marshal(struct {
		Language   string `json:"language"`
		SourceCode string `json:"source_code"`
	}{
		Language:   solution.Language,
		SourceCode: solution.SourceCode,
	})
	return manifestSHA256(encoded)
}

func isManifestSHA256(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func isSandboxSHA256(value string) bool {
	if strings.HasPrefix(value, "sha256:") {
		value = strings.TrimPrefix(value, "sha256:")
	}
	return isManifestSHA256(value)
}

func manifestSHA256(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func testManifestQualityError(format string, args ...interface{}) error {
	return temporal.NewNonRetryableApplicationError(fmt.Sprintf(format, args...), "QualityNotMet", nil)
}
