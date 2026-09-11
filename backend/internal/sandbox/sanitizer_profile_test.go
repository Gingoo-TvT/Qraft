package sandbox

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSanitizerRemoteProtocolOmitsLegacyProfileAndForwardsOptIn(t *testing.T) {
	legacyCompileJSON, err := json.Marshal(remoteCompileRequest{Version: ProtocolVersion, Language: "cpp", Source: "source"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(legacyCompileJSON), `{"version":"v1","language":"cpp","source":"source"}`; got != want {
		t.Fatalf("legacy compile request bytes changed:\ngot  %s\nwant %s", got, want)
	}
	legacyJSON, err := json.Marshal(remoteExecuteRequest{
		Version: ProtocolVersion, Language: "cpp", Source: "source", Inputs: []string{""}, Limits: NewRemoteLimits(1000, 64),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(legacyJSON), `{"version":"v1","language":"cpp","source":"source","inputs":[""],"limits":{"time_limit_ms":1000,"memory_limit_mb":64,"output_limit_bytes":2097152,"max_processes":64},"seed":0}`; got != want {
		t.Fatalf("legacy execute request bytes changed:\ngot  %s\nwant %s", got, want)
	}

	var seenCompile, seenExecute bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		audit := sanitizerRemoteAuditV1(17)
		switch r.URL.Path {
		case "/v1/compile":
			var req remoteCompileRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode compile request: %v", err)
			}
			seenCompile = true
			if req.Profile != SanitizerCAndCPPProfileV1 {
				t.Fatalf("compile profile=%q", req.Profile)
			}
			_ = json.NewEncoder(w).Encode(RemoteCompileResult{
				Version: ProtocolVersion, Language: "cpp", Success: true, DurationMS: 1,
				Toolchain: "g++", SandboxRevision: "revision", Audit: sanitizerRemoteAuditV1(0),
			})
		case "/v1/execute":
			var req remoteExecuteRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode execute request: %v", err)
			}
			seenExecute = true
			if req.Profile != SanitizerCAndCPPProfileV1 || req.Limits.Profile != "" {
				t.Fatalf("execute profile was not top-level-only: %+v", req)
			}
			compile := RemoteCompileResult{
				Version: ProtocolVersion, Language: "cpp", Success: true, DurationMS: 1,
				Toolchain: "g++", SandboxRevision: "revision", Audit: audit,
			}
			_ = json.NewEncoder(w).Encode(RemoteExecuteResult{
				Version: ProtocolVersion, Compile: compile,
				Results: []RemoteCaseResult{{Index: 0, Verdict: VerdictOK, ExitCode: 0}}, Audit: audit,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewHTTPClient(server.URL, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.CompileWithProfile(context.Background(), "c++", "source", SanitizerCAndCPPProfileV1); err != nil {
		t.Fatalf("sanitizer compile protocol failed: %v", err)
	}
	limits := NewRemoteLimits(1000, 64)
	limits.Seed = 17
	limits.Profile = SanitizerCAndCPPProfileV1
	if _, err := client.Execute(context.Background(), "cpp", "source", []string{""}, limits); err != nil {
		t.Fatalf("sanitizer execute protocol failed: %v", err)
	}
	if !seenCompile || !seenExecute {
		t.Fatalf("profile requests not observed: compile=%t execute=%t", seenCompile, seenExecute)
	}
}

func TestSanitizerRemoteProtocolFailsClosed(t *testing.T) {
	for _, test := range []struct {
		profile  string
		language string
	}{
		{SanitizerCAndCPPProfileV1, "c"},
		{SanitizerCAndCPPProfileV1, "cpp"},
	} {
		if err := validateRemoteExecutionProfile(test.profile, test.language); err != nil {
			t.Fatalf("profile=%q language=%q rejected: %v", test.profile, test.language, err)
		}
	}
	for _, test := range []struct {
		profile  string
		language string
	}{
		{SanitizerCAndCPPProfileV1, "python3"},
		{" sanitizer-c-cpp-v1", "cpp"},
		{"sanitizer-v2", "cpp"},
	} {
		if err := validateRemoteExecutionProfile(test.profile, test.language); err == nil {
			t.Fatalf("profile=%q language=%q accepted", test.profile, test.language)
		}
	}

	valid := sanitizerRemoteAuditV1(17)
	if err := validateRemoteAudit(valid, 17, SanitizerCAndCPPProfileV1); err != nil {
		t.Fatalf("valid sanitizer audit rejected: %v", err)
	}
	missingProfile := valid
	missingProfile.Profile = ""
	if err := validateRemoteAudit(missingProfile, 17, SanitizerCAndCPPProfileV1); err == nil {
		t.Fatal("sanitizer audit without profile was accepted")
	}
	missingLimitBinding := valid
	missingLimitBinding.LimitProfile = "execute-v1:time_ms=1000,memory_mb=64,output_bytes=1024,pids=64"
	if err := validateRemoteAudit(missingLimitBinding, 17, SanitizerCAndCPPProfileV1); err == nil {
		t.Fatal("sanitizer audit without limit-profile binding was accepted")
	}
	if err := validateRemoteAudit(valid, 17); err == nil {
		t.Fatal("profiled audit was accepted by a legacy caller")
	}
}

func sanitizerRemoteAuditV1(seed int64) RemoteAuditMetadata {
	return RemoteAuditMetadata{
		RunID: "run_0123456789abcdef0123456789abcdef", ManifestDigest: "sha256:" + strings.Repeat("a", 64), Seed: seed,
		LimitProfile: "execute-v1:time_ms=1000,memory_mb=64,output_bytes=1024,pids=64,profile=" + SanitizerCAndCPPProfileV1,
		Profile:      SanitizerCAndCPPProfileV1, ImageDigest: "sha256:" + strings.Repeat("b", 64),
		ToolchainManifestDigest: "sha256:" + strings.Repeat("c", 64), SeccompPolicyDigest: "sha256:" + strings.Repeat("d", 64),
	}
}
