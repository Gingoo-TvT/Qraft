package desktopui

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Gingoo-TvT/Qraft/desktop/internal/app"
)

func workbench(t *testing.T, backend string, choose func(string) (string, error)) (*Server, *app.Manager) {
	t.Helper()
	dir := t.TempDir()
	c := app.DefaultConfig()
	c.ServerURL = backend
	if e := app.SaveConfig(dir, c); e != nil {
		t.Fatal(e)
	}
	m := app.NewManager(dir, app.CommandRunner{}, app.RuntimeManifest{})
	s, e := Start(Options{Manager: m, Assets: fstest.MapFS{
		"index.html":     &fstest.MapFile{Data: []byte("<html><head><!--ALGOFORGE_BOOTSTRAP--></head><body><div id=root>workbench</div></body></html>")},
		"assets/main.js": &fstest.MapFile{Data: []byte("window.workbench=true;")},
	}, ChooseSave: choose})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, m
}
func read(t *testing.T, url string) (int, string, http.Header) {
	t.Helper()
	res, e := http.Get(url)
	if e != nil {
		t.Fatal(e)
	}
	defer res.Body.Close()
	b, e := io.ReadAll(res.Body)
	if e != nil {
		t.Fatal(e)
	}
	return res.StatusCode, string(b), res.Header
}
func TestOfflineWorkbenchDeepLinkAndLifecycle(t *testing.T) {
	s, m := workbench(t, "http://127.0.0.1:1", nil)
	prefs := app.DefaultPreferences()
	prefs.Theme = "dark"
	prefs.LastPath = "/problem-sets/new"
	if e := m.SavePreferences(prefs); e != nil {
		t.Fatal(e)
	}
	status, body, headers := read(t, s.URL()+"problem-sets/new")
	if status != 200 || !strings.Contains(body, "window.__ALGOFORGE_DESKTOP__=") || !strings.Contains(body, `"theme":"dark"`) || !strings.Contains(body, s.prefix+"/ui/") {
		t.Fatalf("offline entry: %d %s", status, body)
	}
	if !strings.Contains(headers.Get("Content-Security-Policy"), "frame-src 'none'") {
		t.Fatal("bundled entry policy missing")
	}
	status, body, _ = read(t, s.URL()+"assets/main.js")
	if status != 200 || body != "window.workbench=true;" {
		t.Fatalf("asset: %d %s", status, body)
	}
	status, _, _ = read(t, s.URL()+"assets/missing.js")
	if status != 404 {
		t.Fatal("missing script must not return the SPA")
	}
	req, _ := http.NewRequest("POST", s.origin+s.prefix+"/native/preferences", strings.NewReader("{}"))
	req.Header.Set("Origin", "https://unrelated.example")
	res, e := http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	res.Body.Close()
	if res.StatusCode != 403 {
		t.Fatal("unrelated browser origin reached desktop controls")
	}
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
	conn, e := net.DialTimeout("tcp", s.listener.Addr().String(), time.Second)
	if e == nil {
		conn.Close()
		t.Fatal("listener survived window shutdown")
	}
}
func TestProxyUploadSSEAndServiceCookieIsolation(t *testing.T) {
	release := make(chan struct{})
	flushed := make(chan struct{})
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/login":
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "first", Path: "/"})
			io.WriteString(w, "ok")
		case "/api/v1/upload":
			if r.Header.Get("Authorization") != "Bearer sample" {
				t.Error("upload authorization missing")
			}
			if e := r.ParseMultipartForm(1 << 20); e != nil {
				t.Error(e)
			}
			f, _, e := r.FormFile("file")
			if e != nil {
				t.Error(e)
				return
			}
			defer f.Close()
			b, _ := io.ReadAll(f)
			io.WriteString(w, r.FormValue("label")+":"+string(b)+":"+r.URL.Query().Get("format"))
		case "/api/v1/events":
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, "event: step\ndata: first\n\n")
			w.(http.Flusher).Flush()
			close(flushed)
			select {
			case <-release:
			case <-r.Context().Done():
			}
		default:
			c, _ := r.Cookie("session")
			if c != nil {
				io.WriteString(w, c.Value)
			}
		}
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "" {
			t.Error("cookie leaked to another service")
		}
		io.WriteString(w, "second")
	}))
	defer second.Close()
	s, m := workbench(t, first.URL, nil)
	_, _, headers := read(t, s.origin+s.prefix+"/bridge/api/v1/login")
	if headers.Get("Set-Cookie") != "" {
		t.Fatal("backend cookie escaped into the desktop browser")
	}
	_, body, _ := read(t, s.origin+s.prefix+"/bridge/api/v1/me")
	if body != "first" {
		t.Fatalf("cookie session lost: %q", body)
	}
	// A real multipart body and query must arrive unchanged through the adapter.
	payload := "--boundary\r\nContent-Disposition: form-data; name=\"label\"\r\n\r\nfixture\r\n--boundary\r\nContent-Disposition: form-data; name=\"file\"; filename=\"test.txt\"\r\nContent-Type: text/plain\r\n\r\ncontent\r\n--boundary--\r\n"
	req, _ := http.NewRequest("POST", s.origin+s.prefix+"/bridge/api/v1/upload?format=zip", strings.NewReader(payload))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=boundary")
	req.Header.Set("Authorization", "Bearer sample")
	res, e := http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if string(b) != "fixture:content:zip" {
		t.Fatalf("upload changed: %s", b)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ = http.NewRequestWithContext(ctx, "GET", s.origin+s.prefix+"/bridge/api/v1/events", nil)
	res, e = http.DefaultClient.Do(req)
	if e != nil {
		close(release)
		t.Fatal(e)
	}
	line, e := bufio.NewReader(res.Body).ReadString('\n')
	close(release)
	res.Body.Close()
	if e != nil || line != "event: step\n" {
		t.Fatalf("SSE did not arrive while stream open: %s %v", line, e)
	}
	<-flushed
	c := app.DefaultConfig()
	c.ServerURL = second.URL
	if e = m.Save(c); e != nil {
		t.Fatal(e)
	}
	_, body, _ = read(t, s.origin+s.prefix+"/bridge/api/v1/me")
	if body != "second" {
		t.Fatal("service selection did not take effect")
	}
	c.ServerURL = first.URL
	if e = m.Save(c); e != nil {
		t.Fatal(e)
	}
	_, body, _ = read(t, s.origin+s.prefix+"/bridge/api/v1/me")
	if body != "first" {
		t.Fatal("returning to a service lost its own session")
	}
}
func TestNativeDownloadIntegrityFailureAndCancel(t *testing.T) {
	payload := strings.Repeat("test archive bytes\n", 2000)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sample" {
			t.Error("native export authorization missing")
		}
		if r.URL.Query().Get("fail") == "yes" {
			w.WriteHeader(422)
			io.WriteString(w, `{"error":{"message":"题集尚未就绪"}}`)
			return
		}
		io.WriteString(w, payload)
	}))
	defer backend.Close()
	dir := t.TempDir()
	dest := filepath.Join(dir, "混合题集.zip")
	s, _ := workbench(t, backend.URL, func(string) (string, error) { return dest, nil })
	if e := os.WriteFile(dest, []byte("previous selected file"), 0600); e != nil {
		t.Fatal(e)
	}
	d, e := s.download(context.Background(), "/api/v1/problem-sets/fixture/export?format=zip", "set.zip", "Bearer sample")
	if e != nil || d.Status != "completed" || d.Bytes != int64(len(payload)) {
		t.Fatalf("export failed: %+v %v", d, e)
	}
	actual, _ := os.ReadFile(dest)
	if string(actual) != payload {
		t.Fatal("export bytes changed")
	}
	d, e = s.download(context.Background(), "/api/v1/problem-sets/fixture/export?fail=yes", "set.zip", "Bearer sample")
	if e == nil || d.Status != "failed" || !strings.Contains(e.Error(), "题集尚未就绪") {
		t.Fatalf("upstream failure hidden: %+v %v", d, e)
	}
	actual, _ = os.ReadFile(dest)
	if string(actual) != payload {
		t.Fatal("failed export replaced previous file")
	}
	partials, _ := filepath.Glob(filepath.Join(dir, "*.part"))
	if len(partials) != 0 {
		t.Fatal("partial export left behind")
	}
	s.options.ChooseSave = func(string) (string, error) { return "", nil }
	d, e = s.download(context.Background(), "/api/v1/export", "set.zip", "Bearer sample")
	if e != nil || d.Status != "cancelled" {
		t.Fatalf("cancel: %+v %v", d, e)
	}
	_, data, _ := read(t, s.origin+s.prefix+"/native/downloads")
	var result struct{ Data []Download }
	if e = json.Unmarshal([]byte(data), &result); e != nil || len(result.Data) != 2 {
		t.Fatalf("export history: %s %v", data, e)
	}
}

func TestLocalTextExportWorksWithoutBackend(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "配置.json")
	s, _ := workbench(t, "http://127.0.0.1:1", func(name string) (string, error) {
		if name != "testdata-config.json" {
			t.Errorf("suggested filename: %q", name)
		}
		return dest, nil
	})
	content := "{\"adaptive_count\":true,\"说明\":\"边界覆盖\"}"
	saved, e := s.saveText("testdata-config.json", content)
	if e != nil || saved.Status != "completed" {
		t.Fatalf("local export: %+v %v", saved, e)
	}
	b, e := os.ReadFile(dest)
	if e != nil || string(b) != content {
		t.Fatalf("local config changed: %q %v", b, e)
	}
}

func TestUnconfiguredWorkbenchDoesNotProxyOrProbe(t *testing.T) {
	calls := 0
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(200) }))
	defer backend.Close()
	m := app.NewManager(t.TempDir(), app.CommandRunner{}, app.RuntimeManifest{})
	s, err := Start(Options{Manager: m, Assets: fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html><head><!--ALGOFORGE_BOOTSTRAP--></head><body>Qraft</body></html>")}}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	status, body, _ := read(t, s.URL())
	if status != 200 || !strings.Contains(body, `"configured":false`) || !strings.Contains(body, `"service_url":""`) {
		t.Fatalf("%d %s", status, body)
	}
	status, _, _ = read(t, s.origin+s.prefix+"/bridge/api/v1/problems")
	if status != 503 {
		t.Fatal("unconfigured proxy was not blocked", status)
	}
	payload, _ := json.Marshal(map[string]string{"url": backend.URL})
	res, err := http.Post(s.origin+s.prefix+"/native/probe", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 400 || calls != 0 {
		t.Fatalf("unsaved probe contacted backend: status=%d calls=%d", res.StatusCode, calls)
	}
}
