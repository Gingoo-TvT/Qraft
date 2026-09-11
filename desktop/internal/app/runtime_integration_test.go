//go:build integration

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// This opt-in test creates and removes only a previously absent desktop project.
// It never adopts an existing project, volume, or client configuration.
func TestPackagedRuntimeIntegration(t *testing.T) {
	if os.Getenv("ALGOFORGE_DESKTOP_INTEGRATION") != "disposable" {
		t.Skip("requires explicit disposable runtime opt-in")
	}
	reportPath := os.Getenv("ALGOFORGE_DESKTOP_TEST_REPORT")
	manifestPath := os.Getenv("ALGOFORGE_DESKTOP_MANIFEST")
	if reportPath == "" || manifestPath == "" {
		t.Fatal("manifest and report paths are required")
	}
	b, e := os.ReadFile(manifestPath)
	if e != nil {
		t.Fatal(e)
	}
	var manifest RuntimeManifest
	if e = json.Unmarshal(b, &manifest); e != nil {
		t.Fatal(e)
	}
	if e = manifest.Validate(); e != nil {
		t.Fatal(e)
	}
	runner := CommandRunner{}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	for _, args := range [][]string{
		{"ps", "-a", "--filter", "label=com.docker.compose.project=" + Project, "--format", "{{.ID}}"},
		{"volume", "ls", "--filter", "label=com.docker.compose.project=" + Project, "--format", "{{.Name}}"},
		{"network", "ls", "--filter", "label=com.docker.compose.project=" + Project, "--format", "{{.ID}}"},
	} {
		out, e := runner.Run(ctx, "", args...)
		if e != nil {
			t.Fatal(e)
		}
		if strings.TrimSpace(out) != "" {
			t.Fatal("refusing to adopt existing desktop resources", out)
		}
	}
	dir := t.TempDir()
	m := NewManager(dir, runner, manifest)
	c := DefaultConfig()
	c.Mode = "local"
	c.LocalPort = 18200
	if e = SaveConfig(dir, c); e != nil {
		t.Fatal(e)
	}
	report := map[string]any{"pid": os.Getpid(), "project": Project, "client_dir": dir, "started": time.Now().Format(time.RFC3339), "source_revision": manifest.SourceRevision, "cleanup": "owned project only; stop worker, stop services, down --volumes"}
	saveReport := func() {
		b, _ := json.MarshalIndent(report, "", "  ")
		if e := os.WriteFile(reportPath, b, 0600); e != nil {
			t.Error(e)
		}
	}
	saveReport()
	defer func() {
		cleanCtx, cleanCancel := context.WithTimeout(context.Background(), 45*time.Minute)
		defer cleanCancel()
		// prepare may not have run if prerequisites failed.
		if _, e := os.Stat(filepath.Join(m.runtimeDir(), ".env")); os.IsNotExist(e) {
			return
		}
		if e := m.ownership(cleanCtx); e != nil {
			t.Error("cleanup ownership", e)
			report["cleanup_error"] = e.Error()
			saveReport()
			return
		}
		if out, e := m.Run(cleanCtx, "stop", ""); e != nil {
			t.Error("stop", e, out)
			return
		}
		if out, e := m.compose(cleanCtx, "down", "--volumes", "--remove-orphans"); e != nil {
			t.Error("cleanup", e, out)
			return
		}
		for _, kind := range []string{"ps", "volume", "network"} {
			args := []string{kind, "ls"}
			if kind == "ps" {
				args = []string{"ps", "-a"}
			}
			args = append(args, "--filter", "label=com.docker.compose.project="+Project, "--format", "{{.ID}}")
			if kind == "volume" {
				args[len(args)-1] = "{{.Name}}"
			}
			out, e := runner.Run(cleanCtx, "", args...)
			if e != nil || strings.TrimSpace(out) != "" {
				t.Error("cleanup remaining", kind, out, e)
				return
			}
		}
		report["cleanup_verified"] = true
		saveReport()
	}()
	out, e := m.Run(ctx, "start", "")
	if e != nil {
		diagnostics, _ := m.compose(ctx, "logs", "--no-color", "--tail", "100", "postgresql", "migrations", "worker")
		t.Fatalf("start: %v\n%s\n%s", e, out, m.redact(diagnostics))
	}
	conn, e := CheckConnection(ctx, "http://localhost:18200")
	if e != nil || conn.ReleaseVersion != manifest.Version {
		t.Fatalf("connection %+v %v", conn, e)
	}
	report["connection"] = conn
	report["first_start_configuration_ready"] = strings.Contains(out, "生成 worker 尚未启动")
	if report["first_start_configuration_ready"] != true {
		t.Fatal("fresh instance should expose setup before accepting generation")
	}
	gateway, e := runner.Run(ctx, "", "network", "inspect", Project+"_qraft", "--format", "{{(index .IPAM.Config 0).Gateway}}")
	if e != nil {
		t.Fatal(e)
	}
	listener, e := net.Listen("tcp", net.JoinHostPort(strings.TrimSpace(gateway), "0"))
	if e != nil {
		t.Fatal(e)
	}
	mock := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			http.NotFound(w, r)
			return
		}
		vector := make([]float64, 1536)
		vector[0] = 1
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"embedding": vector, "index": 0, "object": "embedding"}}, "model": "desktop-integration-fixture", "object": "list"})
	}))
	mock.Listener = listener
	mock.Start()
	defer mock.Close()
	report["embedding_test_fixture"] = map[string]any{"url": mock.URL, "purpose": "deterministic protocol fixture; no semantic quality claim", "cleanup": "httptest Close"}
	saveReport()
	payload, _ := json.Marshal(map[string]any{"endpoint": map[string]any{"base_url": mock.URL + "/v1", "model": "desktop-integration-fixture", "dimensions": 1536, "api_key": "desktop-fixture-key", "timeout_sec": 30}, "embedding_kinds": []string{"statement"}, "activate": false, "actor": "desktop-integration-test"})
	request, e := http.NewRequestWithContext(ctx, http.MethodPost, "http://localhost:18200/api/v1/embedding/local/deploy", bytes.NewReader(payload))
	if e != nil {
		t.Fatal(e)
	}
	request.Header.Set("Content-Type", "application/json")
	response, e := (&http.Client{Timeout: 45 * time.Second}).Do(request)
	if e != nil {
		t.Fatal(e)
	}
	var deployed map[string]any
	e = json.NewDecoder(response.Body).Decode(&deployed)
	response.Body.Close()
	if e != nil || response.StatusCode != 200 {
		t.Fatal("embedding fixture deployment", deployed, e)
	}
	if out, e = m.Run(ctx, "start", ""); e != nil {
		t.Fatalf("configured startup: %v %s", e, out)
	}
	report["configured_worker_started"] = true

	health, e := m.compose(ctx, "exec", "-T", "worker", "wget", "-qO-", "http://sandbox:8090/health")
	if e != nil {
		t.Fatal(health, e)
	}
	var status map[string]any
	if e = json.Unmarshal([]byte(health), &status); e != nil {
		t.Fatal(e)
	}
	if status["ready"] != true || status["image_digest"] != manifest.SandboxImage || status["toolchain_manifest_digest"] != manifest.ToolchainDigest || status["seccomp_policy_digest"] != manifest.SeccompDigest {
		t.Fatal("sandbox identity or readiness mismatch", status)
	}
	report["sandbox"] = status
	recipePath := filepath.Join("..", "..", "..", "backend", "internal", "testdatagen", "examples", "array-sum.json")
	recipe, e := os.ReadFile(recipePath)
	if e != nil {
		t.Fatal(e)
	}
	args := []string{"compose", "--project-name", Project, "--env-file", filepath.Join(m.runtimeDir(), ".env"), "--file", filepath.Join(m.runtimeDir(), "compose.yml"), "exec", "-T", "worker", "./testdata-tool"}
	generate := func() []byte {
		cmd := exec.CommandContext(ctx, "docker", args...)
		cmd.Stdin = bytes.NewReader(recipe)
		configureProcess(cmd)
		var errOut bytes.Buffer
		cmd.Stderr = &errOut
		out, e := cmd.Output()
		if e != nil {
			t.Fatalf("packaged data tool: %v %s", e, errOut.String())
		}
		return out
	}
	first, second := generate(), generate()
	// Audits have distinct run IDs, so compare the generated inputs and seeds.
	var firstDoc, secondDoc map[string]any
	if e = json.Unmarshal(first, &firstDoc); e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(second, &secondDoc); e != nil {
		t.Fatal(e)
	}
	firstCases, ok := firstDoc["cases"].([]any)
	if !ok || len(firstCases) != 10 {
		t.Fatal("expected ten generated cases")
	}
	secondCases := secondDoc["cases"].([]any)
	for i, item := range firstCases {
		a, b := item.(map[string]any), secondCases[i].(map[string]any)
		if a["input"] != b["input"] || a["seed"] != b["seed"] {
			t.Fatalf("case %d replay differs", i)
		}
	}
	report["data_tool_cases"] = len(firstCases)
	report["data_tool_replay"] = true
	report["ready_for_ui"] = true
	saveReport()
	t.Log("Isolated packaged runtime ready at http://localhost:18200")
	if hold := os.Getenv("ALGOFORGE_DESKTOP_UI_HOLD"); hold != "" {
		deadline := time.NewTimer(15 * time.Minute)
		defer deadline.Stop()
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			if _, e := os.Stat(hold); os.IsNotExist(e) {
				break
			}
			select {
			case <-deadline.C:
				t.Fatal("UI hold expired; cleaning owned services")
			case <-ticker.C:
			}
		}
	}
	report["completed"] = time.Now().Format(time.RFC3339)
	saveReport()
	fmt.Println("Packaged runtime and sandbox data generation passed")
}
