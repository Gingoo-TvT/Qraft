package activities

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"go.temporal.io/sdk/temporal"
)

func TestBuildTestManifestRecordsExactHashesAndDifferentialCoverage(t *testing.T) {
	mainAudit := SandboxAuditMetadata{
		ManifestDigest:          "main-request",
		ImageDigest:             "sha256:main-image",
		ToolchainManifestDigest: "main-toolchain",
		SeccompPolicyDigest:     "main-seccomp",
		LimitProfile:            "main-limits",
		Seed:                    11,
	}
	bruteAudit := SandboxAuditMetadata{
		ManifestDigest:          "sha256:" + manifestSHA256([]byte("brute-request")),
		ImageDigest:             "sha256:" + manifestSHA256([]byte("brute-image")),
		ToolchainManifestDigest: "sha256:" + manifestSHA256([]byte("brute-toolchain")),
		SeccompPolicyDigest:     "sha256:" + manifestSHA256([]byte("brute-seccomp")),
		LimitProfile:            "brute-limits",
		Seed:                    22,
	}
	mainAudit.ManifestDigest = "sha256:" + manifestSHA256([]byte("main-request"))
	mainAudit.ImageDigest = "sha256:" + manifestSHA256([]byte("main-image"))
	mainAudit.ToolchainManifestDigest = "sha256:" + manifestSHA256([]byte("main-toolchain"))
	mainAudit.SeccompPolicyDigest = "sha256:" + manifestSHA256([]byte("main-seccomp"))
	generatorAudit := SandboxAuditMetadata{
		RunID:                   "generator-run-0",
		ManifestDigest:          "sha256:" + manifestSHA256([]byte("generator-request")),
		ImageDigest:             "sha256:" + manifestSHA256([]byte("generator-image")),
		ToolchainManifestDigest: "sha256:" + manifestSHA256([]byte("generator-toolchain")),
		SeccompPolicyDigest:     "sha256:" + manifestSHA256([]byte("generator-seccomp")),
		LimitProfile:            "generator-limits",
		Seed:                    33,
	}
	generatorCaseIndex := 0
	generatorBatchIndex := 0
	manifest, err := New(&Dependencies{}).BuildTestManifestActivity(context.Background(), BuildTestManifestInput{
		PayloadVersion: ActivityPayloadVersion,
		TestCases: []TestCaseData{
			{Input: "1 2\n", GroupID: 1, IsSample: true, Description: "public addition sample", Origin: TestCaseOriginLLMInline},
			{Input: "3 4\n", GroupID: 2, Description: "large main-only case", Origin: TestCaseOriginCustom},
			{Input: "10 20\n", GroupID: 1, Description: "small boundary oracle case", Origin: TestCaseOriginGenerator, GeneratorSeed: 123, GeneratorCaseIndex: &generatorCaseIndex, GeneratorBatchIndex: &generatorBatchIndex},
		},
		MainOutput: SandboxResult{
			PayloadVersion: ActivityPayloadVersion,
			Outputs:        []string{"3 \n", "7", "30"},
			Audit:          mainAudit,
		},
		BruteOutput: SandboxResult{
			PayloadVersion: ActivityPayloadVersion,
			Outputs:        []string{"3", "30"},
			Audit:          bruteAudit,
		},
		BruteIndices:    []int{0, 2},
		MainSolution:    domain.Solution{Language: "cpp", SourceCode: "main-source"},
		BruteSolution:   domain.Solution{Language: "cpp", SourceCode: "brute-source"},
		GeneratorSHA256: manifestSHA256([]byte("generator-source")),
		GeneratorBatches: []GeneratorBatchAudit{{
			BatchIndex:           0,
			TestIndexes:          []int{2},
			GeneratorCaseIndexes: []int{0},
			Audit:                generatorAudit,
		}},
	})
	if err != nil {
		t.Fatalf("build test manifest: %v", err)
	}
	if manifest.SchemaVersion != TestManifestSchemaVersion || manifest.ComparisonMode != TestManifestComparisonMode || manifest.TestCount != 3 || manifest.DifferentialCheckedCount != 2 {
		t.Fatalf("manifest summary = %+v", manifest)
	}
	if manifest.MainSolutionSHA256 != manifestSolutionSHA256(domain.Solution{Language: "cpp", SourceCode: "main-source"}) || manifest.BruteSolutionSHA256 != manifestSolutionSHA256(domain.Solution{Language: "cpp", SourceCode: "brute-source"}) {
		t.Fatalf("solution hashes = %s/%s", manifest.MainSolutionSHA256, manifest.BruteSolutionSHA256)
	}
	if manifest.MainSandbox != testManifestSandboxIdentity(mainAudit) || manifest.BruteSandbox != testManifestSandboxIdentity(bruteAudit) {
		t.Fatalf("sandbox identities = %+v / %+v", manifest.MainSandbox, manifest.BruteSandbox)
	}
	if manifest.Cases[0].InputSHA256 != manifestSHA256([]byte("1 2\n")) || manifest.Cases[0].ExpectedOutputSHA256 != manifestSHA256([]byte("3 \n")) {
		t.Fatalf("sample entry hashes = %+v", manifest.Cases[0])
	}
	if !manifest.Cases[0].DifferentialChecked || !manifest.Cases[0].DifferentialMatch || manifest.Cases[0].BruteOutputSHA256 != manifestSHA256([]byte("3")) {
		t.Fatalf("sample oracle coverage = %+v", manifest.Cases[0])
	}
	if manifest.Cases[1].DifferentialChecked || manifest.Cases[1].BruteOutputSHA256 != "" || manifest.Cases[1].Purpose != "large main-only case" {
		t.Fatalf("main-only entry = %+v", manifest.Cases[1])
	}
	if !manifest.Cases[2].DifferentialChecked || !manifest.Cases[2].DifferentialMatch || manifest.Cases[2].GeneratorSeed != 123 ||
		manifest.Cases[2].GeneratorCaseIndex == nil || *manifest.Cases[2].GeneratorCaseIndex != 0 ||
		manifest.Cases[2].GeneratorBatchIndex == nil || *manifest.Cases[2].GeneratorBatchIndex != 0 ||
		manifest.Cases[2].BruteOutputSHA256 != manifestSHA256([]byte("30")) {
		t.Fatalf("second oracle entry = %+v", manifest.Cases[2])
	}
	if len(manifest.GeneratorBatches) != 1 || manifest.GeneratorBatches[0].Audit != generatorAudit {
		t.Fatalf("generator batch evidence = %+v", manifest.GeneratorBatches)
	}

	firstJSON, firstSHA, err := CanonicalTestManifestJSON(*manifest)
	if err != nil {
		t.Fatal(err)
	}
	secondJSON, secondSHA, err := CanonicalTestManifestJSON(*manifest)
	if err != nil {
		t.Fatal(err)
	}
	if string(firstJSON) != string(secondJSON) || firstSHA != secondSHA || firstSHA != manifestSHA256(firstJSON) {
		t.Fatalf("canonical manifest is unstable: %s/%s", firstSHA, secondSHA)
	}
}

func TestBuildTestManifestRejectsInvalidBruteIndexAsQualityNotMet(t *testing.T) {
	_, err := New(&Dependencies{}).BuildTestManifestActivity(context.Background(), BuildTestManifestInput{
		PayloadVersion: ActivityPayloadVersion,
		TestCases:      []TestCaseData{{Input: "1\n"}},
		MainOutput: SandboxResult{
			PayloadVersion: ActivityPayloadVersion,
			Outputs:        []string{"1"},
		},
		BruteOutput: SandboxResult{
			PayloadVersion: ActivityPayloadVersion,
			Outputs:        []string{"1"},
		},
		BruteIndices: []int{4},
	})
	var applicationErr *temporal.ApplicationError
	if !errors.As(err, &applicationErr) || applicationErr.Type() != "QualityNotMet" || !applicationErr.NonRetryable() {
		t.Fatalf("error = %T %v, want non-retryable QualityNotMet", err, err)
	}
}

func TestBuildTestManifestRejectsDifferentialMismatch(t *testing.T) {
	audit := SandboxAuditMetadata{
		ManifestDigest:          "sha256:" + manifestSHA256([]byte("request")),
		ImageDigest:             "sha256:" + manifestSHA256([]byte("image")),
		ToolchainManifestDigest: "sha256:" + manifestSHA256([]byte("toolchain")),
		SeccompPolicyDigest:     "sha256:" + manifestSHA256([]byte("seccomp")),
		LimitProfile:            "fixture-limits",
	}
	_, err := New(&Dependencies{}).BuildTestManifestActivity(context.Background(), BuildTestManifestInput{
		PayloadVersion: ActivityPayloadVersion,
		TestCases: []TestCaseData{{
			Input: "1\n", Origin: TestCaseOriginLLMInline,
		}},
		MainOutput:    SandboxResult{PayloadVersion: ActivityPayloadVersion, Outputs: []string{"1"}, Audit: audit},
		BruteOutput:   SandboxResult{PayloadVersion: ActivityPayloadVersion, Outputs: []string{"2"}, Audit: audit},
		BruteIndices:  []int{0},
		MainSolution:  domain.Solution{Language: "cpp", SourceCode: "main"},
		BruteSolution: domain.Solution{Language: "cpp", SourceCode: "brute"},
	})
	var applicationErr *temporal.ApplicationError
	if !errors.As(err, &applicationErr) || applicationErr.Type() != "QualityNotMet" || !applicationErr.NonRetryable() || !strings.Contains(applicationErr.Error(), TestManifestComparisonMode) {
		t.Fatalf("error = %T %v, want comparison QualityNotMet", err, err)
	}
}

func TestBuildTestManifestRejectsEmptyPurposeAsQualityNotMet(t *testing.T) {
	audit := SandboxAuditMetadata{
		ManifestDigest:          "sha256:" + manifestSHA256([]byte("purpose-request")),
		ImageDigest:             "sha256:" + manifestSHA256([]byte("purpose-image")),
		ToolchainManifestDigest: "sha256:" + manifestSHA256([]byte("purpose-toolchain")),
		SeccompPolicyDigest:     "sha256:" + manifestSHA256([]byte("purpose-seccomp")),
		LimitProfile:            "purpose-limits",
	}
	_, err := New(&Dependencies{}).BuildTestManifestActivity(context.Background(), BuildTestManifestInput{
		PayloadVersion: ActivityPayloadVersion,
		TestCases: []TestCaseData{{
			Input: "1\n", Description: "  ", Origin: TestCaseOriginLLMInline,
		}},
		MainOutput:    SandboxResult{PayloadVersion: ActivityPayloadVersion, Outputs: []string{"1"}, Audit: audit},
		BruteOutput:   SandboxResult{PayloadVersion: ActivityPayloadVersion, Outputs: []string{"1"}, Audit: audit},
		BruteIndices:  []int{0},
		MainSolution:  domain.Solution{Language: "cpp", SourceCode: "main"},
		BruteSolution: domain.Solution{Language: "cpp", SourceCode: "brute"},
	})
	var applicationErr *temporal.ApplicationError
	if !errors.As(err, &applicationErr) || applicationErr.Type() != "QualityNotMet" || !applicationErr.NonRetryable() || !strings.Contains(applicationErr.Error(), "purpose") {
		t.Fatalf("error = %T %v, want purpose QualityNotMet", err, err)
	}
}
