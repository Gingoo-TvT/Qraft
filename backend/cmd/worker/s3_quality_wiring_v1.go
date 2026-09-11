package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/workflow/activities"
)

const (
	s3SandboxImageDigestEnv     = "ALGOFORGE_S3_SANDBOX_IMAGE_DIGEST"
	s3SandboxToolchainDigestEnv = "ALGOFORGE_S3_SANDBOX_TOOLCHAIN_MANIFEST_DIGEST"
	s3SandboxSeccompDigestEnv   = "ALGOFORGE_S3_SANDBOX_SECCOMP_POLICY_DIGEST"
	s3HiddenRunnerURLEnv        = "ALGOFORGE_S3_HIDDEN_RUNNER_URL"
	s3HiddenRunnerTokenEnv      = "ALGOFORGE_S3_HIDDEN_RUNNER_BEARER_TOKEN"
)

// configureS3QualityDependencies is additive: a legacy worker with no S3
// configuration still starts, while the dormant S3 workflow fails closed at
// its source activities. Partial configuration is rejected at startup.
func configureS3QualityDependencies(deps *activities.Dependencies, allowHTTP bool) error {
	if deps == nil {
		return fmt.Errorf("S3 activity dependencies are required")
	}
	image := strings.TrimSpace(os.Getenv(s3SandboxImageDigestEnv))
	toolchain := strings.TrimSpace(os.Getenv(s3SandboxToolchainDigestEnv))
	seccomp := strings.TrimSpace(os.Getenv(s3SandboxSeccompDigestEnv))
	configuredDigests := 0
	for _, value := range []string{image, toolchain, seccomp} {
		if value != "" {
			configuredDigests++
		}
	}
	if configuredDigests != 0 && configuredDigests != 3 {
		return fmt.Errorf("S3 sandbox identity requires image, toolchain, and seccomp digests together")
	}
	if configuredDigests == 3 {
		for name, value := range map[string]string{"image": image, "toolchain": toolchain, "seccomp": seccomp} {
			if !validWorkerS3DigestV1(value) {
				return fmt.Errorf("S3 sandbox %s digest must be sha256:<64 lowercase hex>", name)
			}
		}
		deps.S3SandboxIdentityPolicy = &activities.S3SandboxIdentityPolicyV1{ImageDigest: image, ToolchainManifestDigest: toolchain, SeccompPolicyDigest: seccomp}
	}

	runnerURL := strings.TrimSpace(os.Getenv(s3HiddenRunnerURLEnv))
	runnerToken := os.Getenv(s3HiddenRunnerTokenEnv)
	if runnerURL == "" {
		if runnerToken != "" {
			return fmt.Errorf("S3 hidden runner token is set without a runner URL")
		}
		return nil
	}
	client, err := activities.NewHTTPHiddenQualityClientV1(runnerURL, runnerToken, 10*time.Minute, allowHTTP)
	if err != nil {
		return fmt.Errorf("configure S3 hidden registry/runner: %w", err)
	}
	deps.HiddenSuiteResolver = client
	deps.HiddenRegressionExecutor = client
	return nil
}

func validWorkerS3DigestV1(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, character := range strings.TrimPrefix(value, "sha256:") {
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f')) {
			return false
		}
	}
	return true
}
