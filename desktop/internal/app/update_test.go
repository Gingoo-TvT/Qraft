package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type updateTransport func(*http.Request) (*http.Response, error)

func (f updateTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func syntheticHash(b string) string                                         { h := sha256.Sum256([]byte(b)); return hex.EncodeToString(h[:]) }
func updateFixture(t *testing.T, modify func(*clientRelease, map[string]string)) (*Manager, *atomic.Int32) {
	t.Helper()
	release := clientRelease{Tag: "v999.0.0"}
	files := map[string]string{
		"Qraft-999.0.0-windows-x64.exe":       "MZ synthetic new executable",
		"Qraft-999.0.0-windows-x64-setup.exe": "MZ synthetic new installer",
	}
	sums := ""
	for name, b := range files {
		sums += syntheticHash(b) + "  " + name + "\n"
	}
	files["SHA256SUMS.txt"] = sums
	for name, b := range files {
		release.Assets = append(release.Assets, releaseAsset{Name: name, URL: releaseRoot + "download/" + release.Tag + "/" + name, Size: int64(len(b)), Digest: "sha256:" + syntheticHash(b)})
	}
	if modify != nil {
		modify(&release, files)
	}
	calls := &atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path == "/repos/Gingoo-TvT/Qraft/releases/latest" {
			_ = json.NewEncoder(w).Encode(release)
			return
		}
		b, ok := files[filepath.Base(r.URL.Path)]
		if !ok {
			http.NotFound(w, r)
			return
		}
		io.WriteString(w, b)
	}))
	t.Cleanup(server.Close)
	m := NewManager(t.TempDir(), nil, RuntimeManifest{})
	target, _ := url.Parse(server.URL)
	m.updateHTTP = &http.Client{Transport: updateTransport(func(r *http.Request) (*http.Response, error) {
		clone := r.Clone(r.Context())
		u := *r.URL
		u.Scheme = target.Scheme
		u.Host = target.Host
		clone.URL = &u
		return http.DefaultTransport.RoundTrip(clone)
	})}
	return m, calls
}
func waitUpdate(t *testing.T, m *Manager) ClientUpdate {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		m.mu.Lock()
		u, busy := m.update, m.op.Busy
		m.mu.Unlock()
		if !busy {
			return u
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("client update did not finish")
	return ClientUpdate{}
}
func TestClientUpdateRequiresGestureAndPreservesData(t *testing.T) {
	m, calls := updateFixture(t, nil)
	if _, e := m.Snapshot(); e != nil {
		t.Fatal(e)
	}
	if calls.Load() != 0 {
		t.Fatal("fresh client contacted GitHub")
	}
	config := `{"synthetic":"existing settings"}`
	if e := os.WriteFile(filepath.Join(m.dir, "settings.json"), []byte(config), 0600); e != nil {
		t.Fatal(e)
	}
	if e := m.BeginClientUpdate("check", nil); e != nil {
		t.Fatal(e)
	}
	u := waitUpdate(t, m)
	if u.Status != "available" || u.LatestVersion != "999.0.0" {
		t.Fatalf("%+v", u)
	}
	if e := m.BeginClientUpdate("download", nil); e != nil {
		t.Fatal(e)
	}
	u = waitUpdate(t, m)
	if u.Status != "ready" || u.Bytes != u.Total || u.Bytes == 0 {
		t.Fatalf("%+v", u)
	}
	b, _ := os.ReadFile(filepath.Join(m.dir, "settings.json"))
	if string(b) != config {
		t.Fatal("update touched settings")
	}
	actual, e := fileSHA256(m.updatePayload)
	if e != nil || actual != m.updateRelease.ExecutableSHA256 {
		t.Fatal("verified payload not persisted", e)
	}
}
func TestClientUpdateRejectsIncompleteOrUntrustedReleases(t *testing.T) {
	for name, change := range map[string]func(*clientRelease, map[string]string){
		"prerelease": func(r *clientRelease, _ map[string]string) { r.Prerelease = true },
		"foreign-download": func(r *clientRelease, _ map[string]string) {
			for i := range r.Assets {
				if strings.HasSuffix(r.Assets[i].Name, "x64.exe") {
					r.Assets[i].URL = "https://example.com/payload.exe"
				}
			}
		},
		"missing-checksum": func(r *clientRelease, _ map[string]string) {
			for i, a := range r.Assets {
				if a.Name == "SHA256SUMS.txt" {
					r.Assets = append(r.Assets[:i], r.Assets[i+1:]...)
					break
				}
			}
		},
		"digest-disagreement": func(r *clientRelease, _ map[string]string) {
			for i := range r.Assets {
				if strings.HasSuffix(r.Assets[i].Name, "x64.exe") {
					r.Assets[i].Digest = "sha256:" + strings.Repeat("0", 64)
				}
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			m, _ := updateFixture(t, change)
			if _, e := m.latestClient(context.Background(), "standalone"); e == nil {
				t.Fatal("unsafe release accepted")
			}
		})
	}
	m, _ := updateFixture(t, func(r *clientRelease, _ map[string]string) { r.Tag = "v" + Version })
	r, e := m.latestClient(context.Background(), "standalone")
	if e != nil || r.Version != Version {
		t.Fatalf("current release: %+v %v", r, e)
	}
}
func TestClientUpdateMissingReleaseAndRateLimit(t *testing.T) {
	for _, status := range []int{404, 403, 429, 500} {
		m := NewManager(t.TempDir(), nil, RuntimeManifest{})
		m.updateHTTP = &http.Client{Transport: updateTransport(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader("{}")), Header: http.Header{}}, nil
		})}
		if e := m.BeginClientUpdate("check", nil); e != nil {
			t.Fatal(e)
		}
		u := waitUpdate(t, m)
		if status == 404 {
			if u.Status != "no_release" || u.Error != "" {
				t.Fatalf("%+v", u)
			}
		} else if u.Status != "error" || u.Error == "" {
			t.Fatalf("%d %+v", status, u)
		}
	}
}
func TestClientUpdateTamperAndCancellationDiscardPartial(t *testing.T) {
	m, _ := updateFixture(t, nil)
	release, e := m.latestClient(context.Background(), "standalone")
	if e != nil {
		t.Fatal(e)
	}
	m.updateRelease = release
	m.updateHTTP = &http.Client{Transport: updateTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(strings.Repeat("X", int(release.Asset.Size)))), Header: http.Header{}}, nil
	})}
	if e = m.BeginClientUpdate("download", nil); e != nil {
		t.Fatal(e)
	}
	if u := waitUpdate(t, m); u.Status != "error" || !strings.Contains(u.Error, "SHA256") {
		t.Fatalf("%+v", u)
	}
	remaining, _ := os.ReadDir(filepath.Join(m.dir, "updates"))
	if len(remaining) != 0 {
		t.Fatal("tampered partial remains")
	}
	started := make(chan struct{})
	m.updateHTTP = &http.Client{Transport: updateTransport(func(r *http.Request) (*http.Response, error) {
		close(started)
		<-r.Context().Done()
		return nil, r.Context().Err()
	})}
	if e = m.BeginClientUpdate("download", nil); e != nil {
		t.Fatal(e)
	}
	<-started
	if e = m.BeginClientUpdate("check", nil); e == nil {
		t.Fatal("concurrent operation admitted")
	}
	c := DefaultConfig()
	c.ServerURL = "https://example.com"
	if e = m.Save(c); e == nil {
		t.Fatal("config changed while updating")
	}
	if e = m.BeginClientUpdate("cancel", nil); e != nil {
		t.Fatal(e)
	}
	if u := waitUpdate(t, m); u.Status != "available" || u.Error != "" {
		t.Fatalf("%+v", u)
	}
	remaining, _ = os.ReadDir(filepath.Join(m.dir, "updates"))
	if len(remaining) != 0 {
		t.Fatal("cancelled partial remains")
	}
}
func TestClientUpdateVersionAndURLBoundaries(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{{"2.10.0", "2.9.9", 1}, {"2.1.0", "2.1.0", 0}, {"v2.1.0", "2.2.0", -1}, {"3.0.0", "2.99.99", 1}} {
		got, e := compareVersions(c.a, c.b)
		if e != nil || got != c.want {
			t.Fatal(c, got, e)
		}
	}
	for _, v := range []string{"2.1", "02.1.0", "2.1.0-beta.1", "2.1.0+dev", "2.1.0/../../evil"} {
		if _, e := compareVersions(v, "2.1.0"); e == nil {
			t.Fatal(v)
		}
	}
	for _, u := range []string{"http://github.com/Gingoo-TvT/Qraft/releases/download/v3.0.0/x.exe", "https://github.com.evil.test/x", "https://user@github.com/Gingoo-TvT/Qraft/releases/download/x", "https://github.com:443/Gingoo-TvT/Qraft/releases/download/x", "file:///tmp/x"} {
		if updateURLAllowed(u, true) {
			t.Fatal("unsafe URL", u)
		}
	}
	client := NewManager(t.TempDir(), nil, RuntimeManifest{}).updateClient()
	req, _ := http.NewRequest("GET", "https://evil.test/payload", nil)
	if e := client.CheckRedirect(req, []*http.Request{{}}); e == nil {
		t.Fatal("foreign redirect accepted")
	}
}
func TestClientUpdateInstalledUsesSetupAndExecutableHash(t *testing.T) {
	m, _ := updateFixture(t, nil)
	release, e := m.latestClient(context.Background(), "installed")
	if e != nil {
		t.Fatal(e)
	}
	if !strings.HasSuffix(release.Asset.Name, "-setup.exe") || release.SHA256 == release.ExecutableSHA256 {
		t.Fatal("installer identity missing")
	}
}
func TestApplyClientUpdatePreservesPortableSettingsAndRestoresOnMismatch(t *testing.T) {
	for _, bad := range []bool{false, true} {
		t.Run(fmt.Sprint(bad), func(t *testing.T) {
			root := t.TempDir()
			exe := filepath.Join(root, "renamed Qraft.exe")
			stage := filepath.Join(root, "data-qraft", "updates", "pending-test")
			if e := os.MkdirAll(stage, 0700); e != nil {
				t.Fatal(e)
			}
			payload := filepath.Join(stage, "new.exe")
			settings := filepath.Join(root, "data-qraft", "settings.json")
			for path, b := range map[string]string{exe: "old executable", payload: "new executable", settings: "preserved data"} {
				if e := os.WriteFile(path, []byte(b), 0600); e != nil {
					t.Fatal(e)
				}
			}
			p := clientUpdatePlan{Executable: exe, Payload: payload, OriginalSHA256: syntheticHash("old executable"), PayloadSHA256: syntheticHash("new executable"), ExecutableSHA256: syntheticHash("new executable"), Kind: "portable"}
			if bad {
				p.ExecutableSHA256 = syntheticHash("different executable")
			}
			e := applyClientUpdate(p)
			actual, _ := os.ReadFile(exe)
			if bad {
				if e == nil || string(actual) != "old executable" {
					t.Fatal("rollback failed", e)
				}
			} else if e != nil || string(actual) != "new executable" {
				t.Fatal("replacement failed", e)
			}
			data, _ := os.ReadFile(settings)
			if string(data) != "preserved data" {
				t.Fatal("data changed")
			}
			backup, _ := os.ReadFile(filepath.Join(stage, "previous-client.exe"))
			if string(backup) != "old executable" {
				t.Fatal("backup missing")
			}
		})
	}
}
func TestClientUpdateRejectsPlanOutsideOwnStaging(t *testing.T) {
	data := t.TempDir()
	stage := filepath.Join(data, "updates", "pending-abc")
	p := clientUpdatePlan{PID: 1, ProcessIdentity: 1, Version: "3.0.0", DataDir: data, Executable: filepath.Join(data, "Qraft.exe"), Payload: filepath.Join(stage, "new.exe"), Kind: "portable", OriginalSHA256: syntheticHash("old"), PayloadSHA256: syntheticHash("new"), ExecutableSHA256: syntheticHash("new")}
	if e := p.validate(filepath.Join(stage, "plan.json")); e != nil {
		t.Fatal(e)
	}
	p.Payload = filepath.Join(data, "unrelated.exe")
	if e := p.validate(filepath.Join(stage, "plan.json")); e == nil {
		t.Fatal("out-of-stage payload accepted")
	}
}
