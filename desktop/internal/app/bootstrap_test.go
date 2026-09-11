package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestSavedSelectionAndDurableConfiguration(t *testing.T) {
	selection := savedEmbeddingSelection{Configured: true, BaseURL: "https://embedding.example/v1", Model: "text-embedding-test", Dimensions: 1536, TimeoutSec: 30, ModelVersionID: "11111111-2222-4333-8444-555555555555"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/embedding/saved-runtime-settings" {
			t.Error(r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]any{"data": selection})
	}))
	defer server.Close()
	got, e := savedSelection(context.Background(), server.URL, "")
	if e != nil || got != selection {
		t.Fatal(got, e)
	}
	m := NewManager(t.TempDir(), &fakeRunner{}, fixtureManifest())
	if e = m.prepare(18080); e != nil {
		t.Fatal(e)
	}
	before, _ := readEnv(filepath.Join(m.runtimeDir(), ".env"))
	if e = m.writeSelection(got); e != nil {
		t.Fatal(e)
	}
	if e = m.prepare(18080); e != nil {
		t.Fatal(e)
	}
	after, _ := readEnv(filepath.Join(m.runtimeDir(), ".env"))
	if before["JWT_SECRET"] != after["JWT_SECRET"] || after["ALGOFORGE_EMBEDDING_EXPECTED_STATEMENT_MODEL_VERSION_ID"] != selection.ModelVersionID || after["ALGOFORGE_EMBEDDING_BASE_URL"] != selection.BaseURL {
		t.Fatal("selection or secrets lost on prepare")
	}
	for _, bad := range []savedEmbeddingSelection{
		{Configured: true, BaseURL: "file:///secret", Model: "x", Dimensions: 1536, TimeoutSec: 30, ModelVersionID: selection.ModelVersionID},
		{Configured: true, BaseURL: selection.BaseURL, Model: "x\nJWT_SECRET=overwrite", Dimensions: 1536, TimeoutSec: 30, ModelVersionID: selection.ModelVersionID},
	} {
		if e = m.writeSelection(bad); e == nil {
			t.Fatal("invalid identity entered environment")
		}
	}
	unchanged, _ := readEnv(filepath.Join(m.runtimeDir(), ".env"))
	if after["JWT_SECRET"] != unchanged["JWT_SECRET"] {
		t.Fatal("invalid selection changed credentials")
	}
}

// A real HTTP configuration API combined with recorded Compose calls verifies
// the first-start sequence without launching Docker services.
func TestFreshLocalStartConfiguresEmbeddingBeforeWorker(t *testing.T) {
	for _, configured := range []bool{false, true} {
		t.Run(strconv.FormatBool(configured), func(t *testing.T) {
			selection := savedEmbeddingSelection{Configured: configured, BaseURL: "https://embedding.example/v1", Model: "test-model", Dimensions: 1536, TimeoutSec: 30, ModelVersionID: "11111111-2222-4333-8444-555555555555"}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v1/integration/capabilities":
					json.NewEncoder(w).Encode(map[string]any{"schema_version": "algoforge.integration-capabilities.v1", "release_version": Version, "problem_sets": map[string]bool{"enabled": true}})
				case "/api/v1/embedding/saved-runtime-settings":
					json.NewEncoder(w).Encode(map[string]any{"data": selection})
				default:
					t.Errorf("unexpected configuration request %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()
			address, _ := url.Parse(srv.URL)
			port, _ := strconv.Atoi(address.Port())
			f := &fakeRunner{}
			m := NewManager(t.TempDir(), f, fixtureManifest())
			c := DefaultConfig()
			c.Mode = "local"
			c.LocalPort = port
			if err := m.Save(c); err != nil {
				t.Fatal(err)
			}
			if err := m.prepare(port); err != nil {
				t.Fatal(err)
			}
			env, _ := readEnv(filepath.Join(m.runtimeDir(), ".env"))
			if env["ALGOFORGE_EMBEDDING_ENABLED"] != "false" {
				t.Fatal("fresh API enabled an unconfigured provider")
			}
			var stages []string
			f.fn = func(args ...string) (string, error) {
				switch args[0] {
				case "ps":
					return "gateway-id", nil
				case "volume", "network":
					return "", nil
				case "image":
					return fixtureManifest().Images[0].ID, nil
				case "container":
					labels := map[string]string{"io.qraft.desktop.owner": env["QRAFT_DESKTOP_OWNER"]}
					var data any = labels
					if strings.Contains(strings.Join(args, " "), "ports") {
						data = map[string]any{"labels": labels, "ports": map[string]any{"80/tcp": []map[string]string{{"HostIP": "127.0.0.1", "HostPort": address.Port()}}}}
					}
					b, _ := json.Marshal(data)
					return string(b), nil
				case "compose":
					joined := strings.Join(args, " ")
					if strings.Contains(joined, " up ") {
						current, _ := readEnv(filepath.Join(m.runtimeDir(), ".env"))
						stages = append(stages, current["ALGOFORGE_EMBEDDING_ENABLED"])
						if strings.Contains(joined, "--remove-orphans") && current["ALGOFORGE_EMBEDDING_EXPECTED_STATEMENT_MODEL_VERSION_ID"] != selection.ModelVersionID {
							t.Fatal("full API/worker reconciliation preceded the saved runtime identity")
						}
					}
					return "", nil
				}
				return "", nil
			}
			message, err := m.startLocal(context.Background(), "", false)
			if err != nil {
				t.Fatal(err)
			}
			if len(stages) == 0 || stages[0] != "false" {
				t.Fatalf("configuration API did not start disabled: %v", stages)
			}
			after, _ := readEnv(filepath.Join(m.runtimeDir(), ".env"))
			if configured {
				if len(stages) != 2 || stages[1] != "true" || after["ALGOFORGE_EMBEDDING_ENABLED"] != "true" {
					t.Fatalf("saved selection was not applied to the full stack: %v", stages)
				}
				if err := m.prepare(port); err != nil {
					t.Fatal(err)
				}
				retained, _ := readEnv(filepath.Join(m.runtimeDir(), ".env"))
				if retained["ALGOFORGE_EMBEDDING_ENABLED"] != "true" {
					t.Fatal("repeated prepare disabled a configured backend")
				}
			} else {
				if len(stages) != 1 || after["ALGOFORGE_EMBEDDING_ENABLED"] != "false" || !strings.Contains(message, "配置工作区已启动") {
					t.Fatalf("fresh worker started prematurely: %v %s", stages, message)
				}
			}
		})
	}
}
