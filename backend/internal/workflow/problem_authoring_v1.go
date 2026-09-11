package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Gingoo-TvT/Qraft/backend/internal/workflow/activities"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const (
	authoringInputContractErrorTypeV1 = "AuthoringInputContractError"
	authoringGateContractErrorTypeV1  = "AuthoringGateContractError"
	maxAuthoringCanonicalBriefBytesV1 = 64 << 10
)

// ProblemGenerationAuthoringWorkflowV1 is an additive, dormant QG-02B workflow
// type. Production starters do not route to it yet. It executes exactly one G
// formalization activity and terminates with that stage's explicit decision;
// Temporal completion does not mean that a complete problem was generated.
func ProblemGenerationAuthoringWorkflowV1(
	ctx workflow.Context,
	in activities.GenerateAuthoringPlanInput,
) (*activities.GenerateAuthoringPlanResult, error) {
	if err := validateAuthoringWorkflowInputV1(in); err != nil {
		return nil, temporal.NewNonRetryableApplicationError(
			err.Error(),
			authoringInputContractErrorTypeV1,
			err,
		)
	}
	activityOptions := workflow.ActivityOptions{
		StartToCloseTimeout: 20 * time.Minute,
		HeartbeatTimeout:    60 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    10 * time.Second,
			BackoffCoefficient: 2,
			MaximumInterval:    5 * time.Minute,
			MaximumAttempts:    3,
			NonRetryableErrorTypes: []string{
				"InvalidParameterError",
			},
		},
	}
	activityCtx := workflow.WithActivityOptions(ctx, activityOptions)

	var result activities.GenerateAuthoringPlanResult
	if err := workflow.ExecuteActivity(
		activityCtx,
		"GenerateAuthoringPlanActivity",
		in,
	).Get(ctx, &result); err != nil {
		return nil, fmt.Errorf("generate authoring plan: %w", err)
	}
	if err := validateAuthoringWorkflowResultV1(in, &result); err != nil {
		return nil, temporal.NewNonRetryableApplicationError(
			err.Error(),
			authoringGateContractErrorTypeV1,
			err,
		)
	}

	// Both accepted and rejected are terminal stage outcomes. A later workflow
	// version may consume the bundle, but this v1 type must never fall through to
	// the legacy statement pipeline, which does not understand the bound bundle.
	return &result, nil
}

func validateAuthoringWorkflowInputV1(in activities.GenerateAuthoringPlanInput) error {
	if in.PayloadVersion != activities.GenerateAuthoringPlanPayloadVersion {
		return fmt.Errorf("unsupported authoring input payload version %d", in.PayloadVersion)
	}
	if err := in.Params.Validate(); err != nil {
		return fmt.Errorf("invalid authoring generation parameters: %w", err)
	}
	if in.CanonicalBrief == "" || in.CanonicalBrief != strings.TrimSpace(in.CanonicalBrief) {
		return fmt.Errorf("canonical brief must be non-empty and trimmed")
	}
	if !utf8.ValidString(in.CanonicalBrief) {
		return fmt.Errorf("canonical brief must be valid UTF-8")
	}
	if len(in.CanonicalBrief) > maxAuthoringCanonicalBriefBytesV1 {
		return fmt.Errorf("canonical brief exceeds %d bytes", maxAuthoringCanonicalBriefBytesV1)
	}
	briefSHA := authoringWorkflowSHA256HexV1([]byte(in.CanonicalBrief))
	if !authoringWorkflowIsSHA256V1(in.CanonicalBriefSHA256) || in.CanonicalBriefSHA256 != briefSHA {
		return fmt.Errorf("canonical brief SHA-256 mismatch")
	}
	return nil
}

func validateAuthoringWorkflowResultV1(
	in activities.GenerateAuthoringPlanInput,
	result *activities.GenerateAuthoringPlanResult,
) error {
	if result == nil {
		return fmt.Errorf("authoring activity result is required")
	}
	if result.PayloadVersion != activities.GenerateAuthoringPlanPayloadVersion {
		return fmt.Errorf("unsupported authoring result payload version %d", result.PayloadVersion)
	}
	inputSHA, err := authoringWorkflowInputSHA256V1(in)
	if err != nil {
		return fmt.Errorf("hash authoring workflow input: %w", err)
	}
	if result.InputSHA256 != inputSHA {
		return fmt.Errorf("authoring result input SHA-256 mismatch")
	}
	briefSHA := authoringWorkflowSHA256HexV1([]byte(in.CanonicalBrief))
	if in.CanonicalBriefSHA256 != briefSHA || result.BriefSHA256 != briefSHA {
		return fmt.Errorf("authoring result canonical brief SHA-256 mismatch")
	}
	if !authoringWorkflowIsSHA256V1(result.BundleSHA256) {
		return fmt.Errorf("authoring result bundle SHA-256 is invalid")
	}
	if result.BundleArtifact == nil || result.BundleArtifact.SHA256 != result.BundleSHA256 {
		return fmt.Errorf("authoring result bundle artifact identity mismatch")
	}
	if len(result.SourceArtifacts) != 2 || result.SourceArtifacts[0] == nil || result.SourceArtifacts[1] == nil {
		return fmt.Errorf("authoring result requires raw-response and bundle artifacts")
	}
	if !authoringWorkflowIsSHA256V1(result.SourceArtifacts[0].SHA256) ||
		result.SourceArtifacts[0].LLMCallReceipt == nil ||
		!authoringWorkflowIsSHA256V1(result.SourceArtifacts[0].LLMCallReceipt.RequestSHA256) {
		return fmt.Errorf("authoring result raw source artifact lacks immutable request identity")
	}
	if result.SourceArtifacts[1].SHA256 != result.BundleSHA256 || !reflect.DeepEqual(result.SourceArtifacts[1], result.BundleArtifact) {
		return fmt.Errorf("authoring result source artifact list does not end with the bundle")
	}

	switch result.GateReasonCode {
	case "":
		if result.ModelDecision != activities.AuthoringPlanDecisionAccepted ||
			result.Decision != activities.AuthoringPlanDecisionAccepted ||
			result.RejectionReason != "" ||
			!result.LintPassed ||
			result.LintErrorCount != 0 ||
			!authoringWorkflowIsSHA256V1(result.SemanticSpecSHA256) {
			return fmt.Errorf("accepted authoring result violates the gate contract")
		}
	case activities.AuthoringPlanGateReasonModelRejected:
		if result.ModelDecision != activities.AuthoringPlanDecisionRejected ||
			result.Decision != activities.AuthoringPlanDecisionRejected ||
			strings.TrimSpace(result.RejectionReason) == "" ||
			result.RejectionReason != strings.TrimSpace(result.RejectionReason) ||
			result.LintPassed ||
			result.LintErrorCount != 0 ||
			result.SemanticSpecSHA256 != "" {
			return fmt.Errorf("model-rejected authoring result violates the gate contract")
		}
	case activities.AuthoringPlanGateReasonSpecLintFailed:
		if result.ModelDecision != activities.AuthoringPlanDecisionAccepted ||
			result.Decision != activities.AuthoringPlanDecisionRejected ||
			strings.TrimSpace(result.RejectionReason) == "" ||
			result.RejectionReason != strings.TrimSpace(result.RejectionReason) ||
			result.LintPassed ||
			result.LintErrorCount <= 0 ||
			!authoringWorkflowIsSHA256V1(result.SemanticSpecSHA256) {
			return fmt.Errorf("lint-rejected authoring result violates the gate contract")
		}
	default:
		return fmt.Errorf("unsupported authoring gate reason %q", result.GateReasonCode)
	}
	return nil
}

func authoringWorkflowInputSHA256V1(in activities.GenerateAuthoringPlanInput) (string, error) {
	encoded, err := json.Marshal(in)
	if err != nil {
		return "", err
	}
	return authoringWorkflowSHA256HexV1(encoded), nil
}

func authoringWorkflowSHA256HexV1(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func authoringWorkflowIsSHA256V1(value string) bool {
	if len(value) != sha256.Size*2 || value != strings.ToLower(value) {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}
