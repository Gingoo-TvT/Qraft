package activities

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

const (
	VerifiedProgramReceiptSchemaV1        = "algoforge.verified-program-receipt.v1"
	VerifiedProgramReceiptStatusV1        = "verified_against_independent_oracle"
	VerifiedProgramReceiptProducerV1      = "VerifyProgramAgainstIndependentOracleActivityV1"
	AuthoringSampleSandboxRunSchemaV1     = "algoforge.authoring-sample-sandbox-run.v1"
	AuthoringSampleSandboxRunPhaseFirst   = "first_output_run"
	AuthoringSampleSandboxRunPhaseReparse = "reparsed_input_run"
	maxVerifiedProgramReceiptBytesV1      = 64 << 10
)

// VerifiedProgramReceiptV1 is deliberately an input from an independent
// verification stage. The sample activities validate and consume it but never
// create it, so they cannot self-sign program correctness.
type VerifiedProgramReceiptV1 struct {
	SchemaVersion                  string `json:"schema_version"`
	Status                         string `json:"status"`
	ProgramSHA256                  string `json:"program_sha256"`
	IndependentOracleReceiptSHA256 string `json:"independent_oracle_receipt_sha256"`
	SandboxImageDigest             string `json:"sandbox_image_digest"`
	ToolchainManifestDigest        string `json:"toolchain_manifest_digest"`
	SeccompPolicyDigest            string `json:"seccomp_policy_digest"`
	LimitProfile                   string `json:"limit_profile"`
}

type AuthoringSampleSandboxRunV1 struct {
	SchemaVersion                  string        `json:"schema_version"`
	Phase                          string        `json:"phase"`
	VerifiedProgramReceiptSHA256   string        `json:"verified_program_receipt_sha256"`
	ProgramSHA256                  string        `json:"program_sha256"`
	IndependentOracleReceiptSHA256 string        `json:"independent_oracle_receipt_sha256"`
	InputSHA256                    []string      `json:"input_sha256"`
	SandboxOutput                  SandboxResult `json:"sandbox_output"`
}

func decodeCanonicalVerifiedProgramReceiptV1(data []byte) (*VerifiedProgramReceiptV1, error) {
	var receipt VerifiedProgramReceiptV1
	if err := decodeCanonicalSampleClosureJSONV1(data, &receipt); err != nil {
		return nil, err
	}
	if err := validateVerifiedProgramReceiptV1(receipt); err != nil {
		return nil, err
	}
	return &receipt, nil
}

func validateVerifiedProgramReceiptV1(receipt VerifiedProgramReceiptV1) error {
	if receipt.SchemaVersion != VerifiedProgramReceiptSchemaV1 || receipt.Status != VerifiedProgramReceiptStatusV1 {
		return fmt.Errorf("unsupported verified-program receipt schema or status")
	}
	if !statementDraftIsSHA256V1(receipt.ProgramSHA256) ||
		!statementDraftIsSHA256V1(receipt.IndependentOracleReceiptSHA256) {
		return fmt.Errorf("verified-program receipt program/oracle SHA-256 is invalid")
	}
	for name, value := range map[string]string{
		"sandbox image digest":      receipt.SandboxImageDigest,
		"toolchain manifest digest": receipt.ToolchainManifestDigest,
		"seccomp policy digest":     receipt.SeccompPolicyDigest,
		"limit profile":             receipt.LimitProfile,
	} {
		if value == "" || value != strings.TrimSpace(value) || len(value) > 512 {
			return fmt.Errorf("verified-program %s is invalid", name)
		}
	}
	return nil
}

func validateAuthoringSampleSandboxRunV1(
	run AuthoringSampleSandboxRunV1,
	expectedPhase string,
	receipt VerifiedProgramReceiptV1,
	receiptSHA string,
	expectedInputSHA []string,
) error {
	if run.SchemaVersion != AuthoringSampleSandboxRunSchemaV1 || run.Phase != expectedPhase {
		return fmt.Errorf("unsupported authoring sample sandbox-run schema or phase")
	}
	if run.VerifiedProgramReceiptSHA256 != receiptSHA ||
		run.ProgramSHA256 != receipt.ProgramSHA256 ||
		run.IndependentOracleReceiptSHA256 != receipt.IndependentOracleReceiptSHA256 {
		return fmt.Errorf("sample sandbox run program/oracle receipt binding mismatch")
	}
	if len(run.InputSHA256) != len(expectedInputSHA) {
		return fmt.Errorf("sample sandbox run has %d inputs, want %d", len(run.InputSHA256), len(expectedInputSHA))
	}
	for index := range expectedInputSHA {
		if !statementDraftIsSHA256V1(run.InputSHA256[index]) || run.InputSHA256[index] != expectedInputSHA[index] {
			return fmt.Errorf("sample sandbox run input %d SHA-256/order mismatch", index)
		}
	}
	output := run.SandboxOutput
	if output.PayloadVersion != ActivityPayloadVersion {
		return fmt.Errorf("sample sandbox output payload version mismatch")
	}
	if len(output.OutputRefs) != 0 {
		return fmt.Errorf("legacy sandbox output refs are forbidden")
	}
	if len(output.Outputs) != len(expectedInputSHA) ||
		len(output.TimeTaken) != len(expectedInputSHA) ||
		len(output.MemoryUsed) != len(expectedInputSHA) {
		return fmt.Errorf("sample sandbox output count does not match input count")
	}
	if len(output.OutputArtifacts) != 0 && len(output.OutputArtifacts) != len(expectedInputSHA) {
		return fmt.Errorf("sample sandbox output artifact count does not match input count")
	}
	for index := range output.TimeTaken {
		if output.TimeTaken[index] < 0 || output.MemoryUsed[index] < 0 {
			return fmt.Errorf("sample sandbox resource receipt %d is negative", index)
		}
	}
	if output.Audit.RunID == "" || output.Audit.RunID != strings.TrimSpace(output.Audit.RunID) ||
		output.Audit.ManifestDigest == "" || output.Audit.ManifestDigest != strings.TrimSpace(output.Audit.ManifestDigest) {
		return fmt.Errorf("sample sandbox run lacks immutable run/manifest identity")
	}
	if output.Audit.ImageDigest != receipt.SandboxImageDigest ||
		output.Audit.ToolchainManifestDigest != receipt.ToolchainManifestDigest ||
		output.Audit.SeccompPolicyDigest != receipt.SeccompPolicyDigest ||
		output.Audit.LimitProfile != receipt.LimitProfile {
		return fmt.Errorf("sample sandbox environment does not match verified-program receipt")
	}
	return nil
}

func decodeCanonicalSampleClosureJSONV1[T any](data []byte, target *T) error {
	if target == nil {
		return fmt.Errorf("canonical JSON target is nil")
	}
	targetType := reflect.TypeOf(*target)
	if err := validateExactJSONKeysV1(string(data), targetType); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return err
	}
	canonical, err := json.Marshal(target)
	if err != nil {
		return err
	}
	if !bytes.Equal(canonical, data) {
		return fmt.Errorf("artifact bytes are not canonical JSON")
	}
	return nil
}
