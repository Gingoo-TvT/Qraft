package main

import (
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/workflow/activities"
)

func clearS3WorkerEnvironmentV1(t *testing.T) {
	t.Helper()
	for _, name := range []string{s3SandboxImageDigestEnv, s3SandboxToolchainDigestEnv, s3SandboxSeccompDigestEnv, s3HiddenRunnerURLEnv, s3HiddenRunnerTokenEnv} {
		t.Setenv(name, "")
	}
}

func TestConfigureS3QualityDependenciesDefaultsDormantAndFailClosed(t *testing.T) {
	clearS3WorkerEnvironmentV1(t)
	deps := &activities.Dependencies{}
	if err := configureS3QualityDependencies(deps, false); err != nil {
		t.Fatalf("configure dormant S3: %v", err)
	}
	if deps.S3SandboxIdentityPolicy != nil || deps.HiddenSuiteResolver != nil || deps.HiddenRegressionExecutor != nil {
		t.Fatalf("unconfigured S3 dependencies were enabled: %+v", deps)
	}
}

func TestConfigureS3QualityDependenciesRejectsPartialIdentity(t *testing.T) {
	clearS3WorkerEnvironmentV1(t)
	t.Setenv(s3SandboxImageDigestEnv, "sha256:"+strings.Repeat("a", 64))
	if err := configureS3QualityDependencies(&activities.Dependencies{}, false); err == nil || !strings.Contains(err.Error(), "together") {
		t.Fatalf("partial S3 identity error = %v", err)
	}
}

func TestConfigureS3QualityDependenciesFreezesIdentityAndWiresHiddenHTTP(t *testing.T) {
	clearS3WorkerEnvironmentV1(t)
	t.Setenv(s3SandboxImageDigestEnv, "sha256:"+strings.Repeat("a", 64))
	t.Setenv(s3SandboxToolchainDigestEnv, "sha256:"+strings.Repeat("b", 64))
	t.Setenv(s3SandboxSeccompDigestEnv, "sha256:"+strings.Repeat("c", 64))
	t.Setenv(s3HiddenRunnerURLEnv, "http://127.0.0.1:18089")
	t.Setenv(s3HiddenRunnerTokenEnv, "worker-token")
	deps := &activities.Dependencies{}
	if err := configureS3QualityDependencies(deps, true); err != nil {
		t.Fatalf("configure S3 production adapters: %v", err)
	}
	if deps.S3SandboxIdentityPolicy == nil || deps.S3SandboxIdentityPolicy.ImageDigest != "sha256:"+strings.Repeat("a", 64) || deps.HiddenSuiteResolver == nil || deps.HiddenRegressionExecutor == nil {
		t.Fatalf("S3 production wiring = %+v", deps)
	}
}

func TestConfigureS3QualityDependenciesRejectsHiddenTokenWithoutEndpoint(t *testing.T) {
	clearS3WorkerEnvironmentV1(t)
	t.Setenv(s3HiddenRunnerTokenEnv, "orphan-token")
	if err := configureS3QualityDependencies(&activities.Dependencies{}, false); err == nil || !strings.Contains(err.Error(), "without a runner URL") {
		t.Fatalf("orphan hidden token error = %v", err)
	}
}
