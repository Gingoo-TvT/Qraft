package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

var imageDigestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

const bakedRevisionPath = "/sandbox/app/source-revision.txt"

func validateDeploymentIdentity(revision, imageDigest string) error {
	revision = strings.TrimSpace(revision)
	switch strings.ToLower(revision) {
	case "", "dev", "unknown", "unset", "latest":
		return fmt.Errorf("SANDBOX_REVISION must identify an immutable source revision")
	}
	if len(revision) < 12 {
		return fmt.Errorf("SANDBOX_REVISION is too short to be auditable")
	}
	if !imageDigestPattern.MatchString(strings.TrimSpace(imageDigest)) {
		return fmt.Errorf("SANDBOX_IMAGE_DIGEST must be a sha256 OCI image ID")
	}
	return nil
}

func readBakedRevision(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read baked sandbox source revision: %w", err)
	}
	revision := strings.TrimSpace(string(data))
	if revision == "" {
		return "", fmt.Errorf("baked sandbox source revision is empty")
	}
	return revision, nil
}

func validateBakedRevision(runtimeRevision, bakedRevision string) error {
	runtimeRevision = strings.TrimSpace(runtimeRevision)
	bakedRevision = strings.TrimSpace(bakedRevision)
	if bakedRevision == "" {
		return fmt.Errorf("baked sandbox source revision is required")
	}
	if runtimeRevision != bakedRevision {
		return fmt.Errorf("SANDBOX_REVISION does not match the revision baked into the image")
	}
	return nil
}

func sha256File(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}
