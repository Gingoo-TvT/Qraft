package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNewRemoteLimitsUsesTwoMiBOutputLimit(t *testing.T) {
	limits := NewRemoteLimits(1000, 256)
	if limits.OutputLimitBytes != 2<<20 {
		t.Fatalf("output limit=%d want=%d", limits.OutputLimitBytes, 2<<20)
	}
	if limits.MaxProcesses != defaultMaxProcesses {
		t.Fatalf("max processes=%d want=%d", limits.MaxProcesses, defaultMaxProcesses)
	}
}

const validAuditJSON = `{"run_id":"run_0123456789abcdef0123456789abcdef","manifest_digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","seed":0,"limit_profile":"execute-v1:test","image_digest":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","toolchain_manifest_digest":"sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","seccomp_policy_digest":"sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"}`

func TestHTTPClientCompileAndExecute(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/compile":
			var req remoteCompileRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode compile request: %v", err)
			}
			if req.Version != ProtocolVersion || req.Source != "source" {
				t.Fatalf("unexpected compile request: %+v", req)
			}
			_, _ = fmt.Fprint(w, `{"version":"v1","language":"cpp","success":true,"stdout":"","stderr":"","duration_ms":2,"toolchain":"g++ 13.3","sandbox_revision":"sha256:test","audit":`+validAuditJSON+`}`)
		case "/v1/execute":
			var req remoteExecuteRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode execute request: %v", err)
			}
			if len(req.Inputs) != 2 || req.Limits.OutputLimitBytes == 0 {
				t.Fatalf("unexpected execute request: %+v", req)
			}
			_, _ = fmt.Fprint(w, `{"version":"v1","compile":{"version":"v1","language":"cpp","success":true,"stdout":"","stderr":"","duration_ms":2,"toolchain":"g++ 13.3","sandbox_revision":"sha256:test","audit":`+validAuditJSON+`},"results":[{"index":0,"verdict":"OK","stdout":"a","stderr":"","exit_code":0,"time_ms":1,"memory_bytes":1024},{"index":1,"verdict":"OK","stdout":"b","stderr":"","exit_code":0,"time_ms":2,"memory_bytes":2048}],"audit":`+validAuditJSON+`}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewHTTPClient(server.URL, time.Second)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	compile, err := client.Compile(context.Background(), "c++", "source")
	if err != nil || !compile.Success || compile.Language != "cpp" {
		t.Fatalf("compile result=%+v err=%v", compile, err)
	}
	execute, err := client.Execute(context.Background(), "cpp", "source", []string{"a", "b"}, NewRemoteLimits(1000, 64))
	if err != nil || len(execute.Results) != 2 || execute.Results[1].Stdout != "b" {
		t.Fatalf("execute result=%+v err=%v", execute, err)
	}
}

func TestHTTPClientForwardsSeedAndRejectsMismatchedAudit(t *testing.T) {
	const seed int64 = 424242
	seededAudit := strings.ReplaceAll(validAuditJSON, `"seed":0`, `"seed":424242`)
	tests := []struct {
		name      string
		auditJSON string
		wantError bool
	}{
		{name: "matching", auditJSON: seededAudit},
		{name: "mismatched", auditJSON: validAuditJSON, wantError: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req remoteExecuteRequest
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Fatalf("decode request: %v", err)
				}
				if req.Seed != seed {
					t.Fatalf("seed=%d want=%d", req.Seed, seed)
				}
				_, _ = fmt.Fprint(w, `{"version":"v1","compile":{"version":"v1","language":"cpp","success":true,"stdout":"","stderr":"","duration_ms":1,"toolchain":"g++","sandbox_revision":"test","audit":`+tt.auditJSON+`},"results":[{"index":0,"verdict":"OK","stdout":"x","stderr":"","exit_code":0,"time_ms":1,"memory_bytes":1}],"audit":`+tt.auditJSON+`}`)
			}))
			defer server.Close()

			client, _ := NewHTTPClient(server.URL, time.Second)
			limits := NewRemoteLimits(1000, 64)
			limits.Seed = seed
			_, err := client.Execute(context.Background(), "cpp", "source", []string{""}, limits)
			if (err != nil) != tt.wantError {
				t.Fatalf("err=%v wantError=%v", err, tt.wantError)
			}
		})
	}
}

func TestHTTPClientPropagatesStructuredError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = fmt.Fprint(w, `{"version":"v1","error":{"code":"toolchain_unavailable","message":"javac missing"}}`)
	}))
	defer server.Close()
	client, _ := NewHTTPClient(server.URL, time.Second)
	_, err := client.Compile(context.Background(), "java", "source")
	var remoteErr *RemoteError
	if !errors.As(err, &remoteErr) || remoteErr.Code != "toolchain_unavailable" {
		t.Fatalf("expected structured remote error, got %v", err)
	}
}

func TestHTTPClientRejectsMalformedResponses(t *testing.T) {
	validCompile := `{"version":"v1","language":"cpp","success":true,"stdout":"","stderr":"","duration_ms":1,"toolchain":"g++","sandbox_revision":"test","audit":` + validAuditJSON + `}`
	tests := []struct {
		name string
		body string
	}{
		{name: "invalid json", body: `{`},
		{name: "unknown field", body: strings.TrimSuffix(validCompile, "}") + `,"binary_path":"/tmp/pwn"}`},
		{name: "wrong version", body: strings.Replace(validCompile, `"v1"`, `"v2"`, 1)},
		{name: "wrong language", body: strings.Replace(validCompile, `"cpp"`, `"java"`, 1)},
		{name: "negative duration", body: strings.Replace(validCompile, `"duration_ms":1`, `"duration_ms":-1`, 1)},
		{name: "missing revision", body: strings.Replace(validCompile, `"sandbox_revision":"test"`, `"sandbox_revision":""`, 1)},
		{name: "trailing json", body: validCompile + `{}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = fmt.Fprint(w, tt.body) }))
			defer server.Close()
			client, _ := NewHTTPClient(server.URL, time.Second)
			if _, err := client.Compile(context.Background(), "cpp", "source"); err == nil {
				t.Fatal("expected malformed response error")
			}
		})
	}
}

func TestHTTPClientRejectsInvalidExecuteSchema(t *testing.T) {
	compile := `"compile":{"version":"v1","language":"cpp","success":true,"stdout":"","stderr":"","duration_ms":1,"toolchain":"g++","sandbox_revision":"test","audit":` + validAuditJSON + `}`
	tests := []struct {
		name    string
		results string
	}{
		{name: "wrong count", results: `[]`},
		{name: "wrong index", results: `[{"index":1,"verdict":"OK","stdout":"","stderr":"","exit_code":0,"time_ms":1,"memory_bytes":0}]`},
		{name: "unknown verdict", results: `[{"index":0,"verdict":"AC","stdout":"","stderr":"","exit_code":0,"time_ms":1,"memory_bytes":0}]`},
		{name: "ok nonzero", results: `[{"index":0,"verdict":"OK","stdout":"","stderr":"","exit_code":1,"time_ms":1,"memory_bytes":0}]`},
		{name: "negative metric", results: `[{"index":0,"verdict":"OK","stdout":"","stderr":"","exit_code":0,"time_ms":-1,"memory_bytes":0}]`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := `{"version":"v1",` + compile + `,"results":` + tt.results + `,"audit":` + validAuditJSON + `}`
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = fmt.Fprint(w, body) }))
			defer server.Close()
			client, _ := NewHTTPClient(server.URL, time.Second)
			if _, err := client.Execute(context.Background(), "cpp", "source", []string{""}, NewRemoteLimits(1000, 64)); err == nil {
				t.Fatal("expected invalid execute response error")
			}
		})
	}
}

func TestHTTPClientRejectsInconsistentExecuteAuditIdentity(t *testing.T) {
	topAudit := strings.Replace(
		validAuditJSON,
		"run_0123456789abcdef0123456789abcdef",
		"run_fedcba9876543210fedcba9876543210",
		1,
	)
	body := `{"version":"v1","compile":{"version":"v1","language":"cpp","success":true,"stdout":"","stderr":"","duration_ms":1,"toolchain":"g++","sandbox_revision":"test","audit":` + validAuditJSON + `},"results":[{"index":0,"verdict":"OK","stdout":"","stderr":"","exit_code":0,"time_ms":1,"memory_bytes":0}],"audit":` + topAudit + `}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, body)
	}))
	defer server.Close()

	client, _ := NewHTTPClient(server.URL, time.Second)
	if _, err := client.Execute(context.Background(), "cpp", "source", []string{""}, NewRemoteLimits(1000, 64)); err == nil || !strings.Contains(err.Error(), "run_id") {
		t.Fatalf("inconsistent audit identity error = %v", err)
	}
}

func TestHTTPClientFailsOnTimeoutAndConnectionRefused(t *testing.T) {
	t.Run("timeout", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { time.Sleep(100 * time.Millisecond) }))
		defer server.Close()
		client, _ := NewHTTPClient(server.URL, 5*time.Millisecond)
		if _, err := client.Compile(context.Background(), "cpp", "source"); err == nil {
			t.Fatal("expected timeout")
		}
	})
	t.Run("connection refused", func(t *testing.T) {
		client, _ := NewHTTPClient("http://127.0.0.1:1", 100*time.Millisecond)
		if _, err := client.Compile(context.Background(), "cpp", "source"); err == nil {
			t.Fatal("expected connection error")
		}
	})
}

func TestHTTPClientRejectsOversizedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, strings.Repeat("x", maxHTTPResponseBytes+1))
	}))
	defer server.Close()
	client, _ := NewHTTPClient(server.URL, 10*time.Second)
	if _, err := client.Compile(context.Background(), "cpp", "source"); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("expected oversized response error, got %v", err)
	}
}

func TestNewHTTPClientValidatesURL(t *testing.T) {
	for _, value := range []string{"", "sandbox:8090", "ftp://sandbox", "http://user:pass@sandbox", "http://sandbox?q=x"} {
		if _, err := NewHTTPClient(value, time.Second); err == nil {
			t.Fatalf("expected URL %q to be rejected", value)
		}
	}
}
