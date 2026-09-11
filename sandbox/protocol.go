package main

const apiVersion = "v1"

const sanitizerCAndCPPProfileV1 = "sanitizer-c-cpp-v1"

const (
	verdictOK  = "OK"
	verdictRE  = "RE"
	verdictTLE = "TLE"
	verdictMLE = "MLE"
	verdictOLE = "OLE"
)

type compileRequest struct {
	Version  string `json:"version"`
	Language string `json:"language"`
	Source   string `json:"source"`
	Profile  string `json:"profile,omitempty"`
	Seed     int64  `json:"seed"`
	RunID    string `json:"-"`
}

type auditMetadata struct {
	RunID                   string `json:"run_id"`
	ManifestDigest          string `json:"manifest_digest"`
	Seed                    int64  `json:"seed"`
	LimitProfile            string `json:"limit_profile"`
	Profile                 string `json:"profile,omitempty"`
	ImageDigest             string `json:"image_digest"`
	ToolchainManifestDigest string `json:"toolchain_manifest_digest"`
	SeccompPolicyDigest     string `json:"seccomp_policy_digest"`
}

type compileResponse struct {
	Version         string        `json:"version"`
	Language        string        `json:"language"`
	Success         bool          `json:"success"`
	Stdout          string        `json:"stdout"`
	Stderr          string        `json:"stderr"`
	DurationMS      int64         `json:"duration_ms"`
	Toolchain       string        `json:"toolchain"`
	SandboxRevision string        `json:"sandbox_revision"`
	Audit           auditMetadata `json:"audit"`
}

type executionLimits struct {
	TimeLimitMS      int   `json:"time_limit_ms"`
	MemoryLimitMB    int   `json:"memory_limit_mb"`
	OutputLimitBytes int64 `json:"output_limit_bytes"`
	MaxProcesses     int   `json:"max_processes"`
}

type executeRequest struct {
	Version  string          `json:"version"`
	Language string          `json:"language"`
	Source   string          `json:"source"`
	Profile  string          `json:"profile,omitempty"`
	Inputs   []string        `json:"inputs"`
	Limits   executionLimits `json:"limits"`
	Seed     int64           `json:"seed"`
	RunID    string          `json:"-"`
}

type caseResult struct {
	Index       int    `json:"index"`
	Verdict     string `json:"verdict"`
	Stdout      string `json:"stdout"`
	Stderr      string `json:"stderr"`
	ExitCode    int    `json:"exit_code"`
	TimeMS      int64  `json:"time_ms"`
	MemoryBytes int64  `json:"memory_bytes"`
	Signal      string `json:"signal,omitempty"`
}

type executeResponse struct {
	Version string          `json:"version"`
	Compile compileResponse `json:"compile"`
	Results []caseResult    `json:"results"`
	Audit   auditMetadata   `json:"audit"`
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type errorResponse struct {
	Version string   `json:"version"`
	Error   apiError `json:"error"`
}
