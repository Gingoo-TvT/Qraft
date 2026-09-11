package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	ProtocolVersion           = "v1"
	SanitizerCAndCPPProfileV1 = "sanitizer-c-cpp-v1"
	maxHTTPResponseBytes      = 64 << 20
	defaultOutputLimit        = 2 << 20
	defaultMaxProcesses       = 64
)

type RemoteExecutor interface {
	Compile(context.Context, string, string) (*RemoteCompileResult, error)
	Execute(context.Context, string, string, []string, RemoteLimits) (*RemoteExecuteResult, error)
}

type RemoteLimits struct {
	TimeLimitMS      int    `json:"time_limit_ms"`
	MemoryLimitMB    int    `json:"memory_limit_mb"`
	OutputLimitBytes int64  `json:"output_limit_bytes"`
	MaxProcesses     int    `json:"max_processes"`
	Seed             int64  `json:"-"`
	Profile          string `json:"-"`
}

func NewRemoteLimits(timeLimitMS, memoryLimitMB int) RemoteLimits {
	return RemoteLimits{
		TimeLimitMS:      timeLimitMS,
		MemoryLimitMB:    memoryLimitMB,
		OutputLimitBytes: defaultOutputLimit,
		MaxProcesses:     defaultMaxProcesses,
	}
}

type RemoteCompileResult struct {
	Version         string              `json:"version"`
	Language        string              `json:"language"`
	Success         bool                `json:"success"`
	Stdout          string              `json:"stdout"`
	Stderr          string              `json:"stderr"`
	DurationMS      int64               `json:"duration_ms"`
	Toolchain       string              `json:"toolchain"`
	SandboxRevision string              `json:"sandbox_revision"`
	Audit           RemoteAuditMetadata `json:"audit"`
}

type RemoteAuditMetadata struct {
	RunID                   string `json:"run_id"`
	ManifestDigest          string `json:"manifest_digest"`
	Seed                    int64  `json:"seed"`
	LimitProfile            string `json:"limit_profile"`
	Profile                 string `json:"profile,omitempty"`
	ImageDigest             string `json:"image_digest"`
	ToolchainManifestDigest string `json:"toolchain_manifest_digest"`
	SeccompPolicyDigest     string `json:"seccomp_policy_digest"`
}

type RemoteCaseResult struct {
	Index       int     `json:"index"`
	Verdict     Verdict `json:"verdict"`
	Stdout      string  `json:"stdout"`
	Stderr      string  `json:"stderr"`
	ExitCode    int     `json:"exit_code"`
	TimeMS      int64   `json:"time_ms"`
	MemoryBytes int64   `json:"memory_bytes"`
	Signal      string  `json:"signal,omitempty"`
}

type RemoteExecuteResult struct {
	Version string              `json:"version"`
	Compile RemoteCompileResult `json:"compile"`
	Results []RemoteCaseResult  `json:"results"`
	Audit   RemoteAuditMetadata `json:"audit"`
}

type RemoteError struct {
	StatusCode int
	Code       string
	Message    string
}

func (e *RemoteError) Error() string {
	return fmt.Sprintf("sandbox HTTP %d (%s): %s", e.StatusCode, e.Code, e.Message)
}

type HTTPClient struct {
	baseURL    string
	httpClient *http.Client
}

func NewHTTPClient(baseURL string, timeout time.Duration) (*HTTPClient, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return nil, fmt.Errorf("parsing sandbox URL: %w", err)
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, fmt.Errorf("sandbox URL must be an absolute http(s) URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("sandbox URL must not contain credentials, query, or fragment")
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &HTTPClient{
		baseURL: strings.TrimRight(parsed.String(), "/"),
		httpClient: &http.Client{
			Timeout: timeout,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return errors.New("sandbox redirects are not allowed")
			},
		},
	}, nil
}

type remoteCompileRequest struct {
	Version  string `json:"version"`
	Language string `json:"language"`
	Source   string `json:"source"`
	Profile  string `json:"profile,omitempty"`
}

type remoteExecuteRequest struct {
	Version  string       `json:"version"`
	Language string       `json:"language"`
	Source   string       `json:"source"`
	Profile  string       `json:"profile,omitempty"`
	Inputs   []string     `json:"inputs"`
	Limits   RemoteLimits `json:"limits"`
	Seed     int64        `json:"seed"`
}

type remoteErrorEnvelope struct {
	Version string `json:"version"`
	Error   struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (c *HTTPClient) Compile(ctx context.Context, language, source string) (*RemoteCompileResult, error) {
	return c.CompileWithProfile(ctx, language, source, "")
}

// CompileWithProfile is additive; callers using RemoteExecutor keep the exact
// legacy request bytes because the empty profile is omitted from JSON.
func (c *HTTPClient) CompileWithProfile(ctx context.Context, language, source, profile string) (*RemoteCompileResult, error) {
	canonical, err := canonicalRemoteLanguage(language)
	if err != nil {
		return nil, err
	}
	if err := validateRemoteExecutionProfile(profile, canonical); err != nil {
		return nil, err
	}
	req := remoteCompileRequest{Version: ProtocolVersion, Language: language, Source: source, Profile: profile}
	var result RemoteCompileResult
	if err := c.post(ctx, "/v1/compile", req, &result); err != nil {
		return nil, err
	}
	if err := validateRemoteCompile(result, canonical, 0, profile); err != nil {
		return nil, fmt.Errorf("invalid sandbox compile response: %w", err)
	}
	return &result, nil
}

func (c *HTTPClient) Execute(ctx context.Context, language, source string, inputs []string, limits RemoteLimits) (*RemoteExecuteResult, error) {
	canonical, err := canonicalRemoteLanguage(language)
	if err != nil {
		return nil, err
	}
	if err := validateRemoteExecutionProfile(limits.Profile, canonical); err != nil {
		return nil, err
	}
	req := remoteExecuteRequest{
		Version: ProtocolVersion, Language: language, Source: source,
		Inputs: inputs, Limits: limits, Seed: limits.Seed, Profile: limits.Profile,
	}
	var result RemoteExecuteResult
	if err := c.post(ctx, "/v1/execute", req, &result); err != nil {
		return nil, err
	}
	if result.Version != ProtocolVersion {
		return nil, fmt.Errorf("invalid sandbox execute response: version %q, want %q", result.Version, ProtocolVersion)
	}
	if err := validateRemoteAudit(result.Audit, limits.Seed, limits.Profile); err != nil {
		return nil, fmt.Errorf("invalid sandbox execute audit: %w", err)
	}
	if err := validateRemoteCompile(result.Compile, canonical, limits.Seed, limits.Profile); err != nil {
		return nil, fmt.Errorf("invalid sandbox execute compile result: %w", err)
	}
	if err := validateExecuteAuditConsistency(result.Audit, result.Compile.Audit); err != nil {
		return nil, fmt.Errorf("invalid sandbox execute audit consistency: %w", err)
	}
	if !result.Compile.Success {
		if len(result.Results) != 0 {
			return nil, fmt.Errorf("invalid sandbox execute response: compile failed but returned %d cases", len(result.Results))
		}
		return &result, nil
	}
	if len(result.Results) != len(inputs) {
		return nil, fmt.Errorf("invalid sandbox execute response: got %d cases, want %d", len(result.Results), len(inputs))
	}
	for i, item := range result.Results {
		if item.Index != i {
			return nil, fmt.Errorf("invalid sandbox execute response: result %d has index %d", i, item.Index)
		}
		if !isRemoteVerdict(item.Verdict) {
			return nil, fmt.Errorf("invalid sandbox execute response: result %d has verdict %q", i, item.Verdict)
		}
		if item.TimeMS < 0 || item.MemoryBytes < 0 {
			return nil, fmt.Errorf("invalid sandbox execute response: result %d has negative resource usage", i)
		}
		if item.Verdict == VerdictOK && item.ExitCode != 0 {
			return nil, fmt.Errorf("invalid sandbox execute response: OK result %d has exit code %d", i, item.ExitCode)
		}
	}
	return &result, nil
}

func validateExecuteAuditConsistency(executeAudit, compileAudit RemoteAuditMetadata) error {
	if executeAudit.RunID != compileAudit.RunID {
		return fmt.Errorf("execute run_id %q does not match compile run_id %q", executeAudit.RunID, compileAudit.RunID)
	}
	for name, pair := range map[string][2]string{
		"image_digest":              {executeAudit.ImageDigest, compileAudit.ImageDigest},
		"toolchain_manifest_digest": {executeAudit.ToolchainManifestDigest, compileAudit.ToolchainManifestDigest},
		"seccomp_policy_digest":     {executeAudit.SeccompPolicyDigest, compileAudit.SeccompPolicyDigest},
		"profile":                   {executeAudit.Profile, compileAudit.Profile},
	} {
		if pair[0] != pair[1] {
			return fmt.Errorf("execute %s does not match compile %s", name, name)
		}
	}
	return nil
}

func (c *HTTPClient) post(ctx context.Context, path string, requestBody, responseBody any) error {
	body, err := json.Marshal(requestBody)
	if err != nil {
		return fmt.Errorf("encoding sandbox request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("creating sandbox request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("calling sandbox %s: %w", path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxHTTPResponseBytes+1))
	if err != nil {
		return fmt.Errorf("reading sandbox response: %w", err)
	}
	if len(data) > maxHTTPResponseBytes {
		return fmt.Errorf("sandbox response exceeds %d bytes", maxHTTPResponseBytes)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var envelope remoteErrorEnvelope
		if err := strictUnmarshal(data, &envelope); err != nil {
			return fmt.Errorf("sandbox returned HTTP %d with malformed error response: %w", resp.StatusCode, err)
		}
		if envelope.Version != ProtocolVersion || envelope.Error.Code == "" || envelope.Error.Message == "" {
			return fmt.Errorf("sandbox returned HTTP %d with invalid error schema", resp.StatusCode)
		}
		return &RemoteError{StatusCode: resp.StatusCode, Code: envelope.Error.Code, Message: envelope.Error.Message}
	}
	if err := strictUnmarshal(data, responseBody); err != nil {
		return fmt.Errorf("decoding sandbox success response: %w", err)
	}
	return nil
}

func strictUnmarshal(data []byte, dst any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return fmt.Errorf("trailing JSON data: %w", err)
	}
	return nil
}

func validateRemoteCompile(result RemoteCompileResult, language string, expectedSeed int64, profiles ...string) error {
	if result.Version != ProtocolVersion {
		return fmt.Errorf("version %q, want %q", result.Version, ProtocolVersion)
	}
	if result.Language != language {
		return fmt.Errorf("language %q, want %q", result.Language, language)
	}
	if result.DurationMS < 0 {
		return fmt.Errorf("negative duration_ms")
	}
	if result.Toolchain == "" || result.SandboxRevision == "" {
		return fmt.Errorf("missing toolchain or sandbox revision")
	}
	if err := validateRemoteAudit(result.Audit, expectedSeed, profiles...); err != nil {
		return fmt.Errorf("invalid audit metadata: %w", err)
	}
	return nil
}

func validateRemoteAudit(audit RemoteAuditMetadata, expectedSeed int64, profiles ...string) error {
	expectedProfile := ""
	if len(profiles) > 0 {
		expectedProfile = profiles[0]
	}
	if !strings.HasPrefix(audit.RunID, "run_") || len(audit.RunID) != len("run_")+32 {
		return fmt.Errorf("invalid run_id %q", audit.RunID)
	}
	for name, value := range map[string]string{
		"manifest_digest":           audit.ManifestDigest,
		"image_digest":              audit.ImageDigest,
		"toolchain_manifest_digest": audit.ToolchainManifestDigest,
		"seccomp_policy_digest":     audit.SeccompPolicyDigest,
	} {
		if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
			return fmt.Errorf("invalid %s", name)
		}
	}
	if strings.TrimSpace(audit.LimitProfile) == "" {
		return fmt.Errorf("missing limit_profile")
	}
	if audit.Seed != expectedSeed {
		return fmt.Errorf("seed %d, want %d", audit.Seed, expectedSeed)
	}
	if audit.Profile != expectedProfile {
		return fmt.Errorf("profile %q, want %q", audit.Profile, expectedProfile)
	}
	if expectedProfile != "" && !strings.Contains(audit.LimitProfile, "profile="+expectedProfile) {
		return fmt.Errorf("limit_profile does not bind execution profile %q", expectedProfile)
	}
	return nil
}

func validateRemoteExecutionProfile(profile, language string) error {
	if profile == "" {
		return nil
	}
	if profile != strings.TrimSpace(profile) {
		return fmt.Errorf("execution profile must be canonical")
	}
	if profile != SanitizerCAndCPPProfileV1 {
		return fmt.Errorf("unsupported execution profile %q", profile)
	}
	if language != "c" && language != "cpp" {
		return fmt.Errorf("%s supports only C and C++", SanitizerCAndCPPProfileV1)
	}
	return nil
}

func canonicalRemoteLanguage(language string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(language)) {
	case "c":
		return "c", nil
	case "cpp", "c++", "cc":
		return "cpp", nil
	case "python3", "python", "py":
		return "python3", nil
	case "java":
		return "java", nil
	case "go", "golang":
		return "go", nil
	default:
		return "", fmt.Errorf("unsupported language %q", language)
	}
}

func isRemoteVerdict(verdict Verdict) bool {
	switch verdict {
	case VerdictOK, VerdictRE, VerdictTLE, VerdictMLE, Verdict("OLE"):
		return true
	default:
		return false
	}
}
