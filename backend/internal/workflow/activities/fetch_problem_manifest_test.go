package activities

import (
	"bytes"
	"strings"
	"testing"
)

func TestParseTestManifestJSONReturnsPersistedDifferentialIndexes(t *testing.T) {
	manifest := testManifestParserFixture()
	encoded, digest, err := CanonicalTestManifestJSON(manifest)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseTestManifestJSON(encoded)
	if err != nil {
		t.Fatalf("ParseTestManifestJSON() error = %v", err)
	}
	if parsed.TestCount != 3 || parsed.DifferentialCheckedCount != 2 ||
		!parsed.Cases[0].DifferentialChecked || parsed.Cases[1].DifferentialChecked ||
		!parsed.Cases[2].DifferentialChecked {
		t.Fatalf("parsed manifest = %+v", parsed)
	}
	if got := manifestSHA256(encoded); got != digest {
		t.Fatalf("digest = %s, canonical digest = %s", got, digest)
	}
}

func TestParseTestManifestJSONRejectsNonCanonicalOrUnknownBytes(t *testing.T) {
	manifest := testManifestParserFixture()
	encoded, _, err := CanonicalTestManifestJSON(manifest)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		data []byte
	}{
		{name: "trailing json", data: append(append([]byte(nil), encoded...), []byte(" \n")...)},
		{name: "unknown field", data: bytes.Replace(encoded, []byte(`"cases":[`), []byte(`"unexpected":true,"cases":[`), 1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ParseTestManifestJSON(test.data); err == nil {
				t.Fatalf("ParseTestManifestJSON accepted invalid %s", test.name)
			}
		})
	}
}

func testManifestParserFixture() TestManifestV1 {
	digest := strings.Repeat("a", 64)
	sandbox := TestManifestSandboxIdentity{
		ManifestDigest:          "sha256:" + digest,
		ImageDigest:             "sha256:" + digest,
		ToolchainManifestDigest: "sha256:" + digest,
		SeccompPolicyDigest:     "sha256:" + digest,
		LimitProfile:            "fixture",
	}
	return TestManifestV1{
		SchemaVersion:            TestManifestSchemaVersion,
		ComparisonMode:           TestManifestComparisonMode,
		TestCount:                3,
		DifferentialCheckedCount: 2,
		MainSolutionSHA256:       digest,
		BruteSolutionSHA256:      digest,
		MainSandbox:              sandbox,
		BruteSandbox:             sandbox,
		Cases: []TestManifestCase{
			{TestIndex: 0, GroupID: 1, IsSample: true, Purpose: "sample", Origin: TestCaseOriginLLMInline, InputSHA256: digest, ExpectedOutputSHA256: digest, DifferentialChecked: true, DifferentialMatch: true, BruteOutputSHA256: digest},
			{TestIndex: 1, GroupID: 1, Purpose: "large main-only", Origin: TestCaseOriginCustom, InputSHA256: digest, ExpectedOutputSHA256: digest},
			{TestIndex: 2, GroupID: 1, Purpose: "tiny oracle", Origin: TestCaseOriginCustom, InputSHA256: digest, ExpectedOutputSHA256: digest, DifferentialChecked: true, DifferentialMatch: true, BruteOutputSHA256: digest},
		},
	}
}
