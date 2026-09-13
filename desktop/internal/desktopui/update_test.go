package desktopui

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestUpdateAndExportAreMutuallyExclusive(t *testing.T) {
	s, m := workbench(t, "http://127.0.0.1:1", func(string) (string, error) { t.Fatal("save dialog opened during update"); return "", nil })
	s.mu.Lock()
	s.downloading = true
	s.mu.Unlock()
	res, e := http.Post(s.origin+s.prefix+"/native/client-update", "application/json", strings.NewReader(`{"action":"check"}`))
	if e != nil {
		t.Fatal(e)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 400 || !strings.Contains(string(b), "导出") {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	if m.Busy() {
		t.Fatal("export conflict started updater")
	}
	s.mu.Lock()
	s.downloading = false
	s.mu.Unlock()
	// Invalid updates are rejected without network access or exit callbacks.
	res, e = http.Post(s.origin+s.prefix+"/native/client-update", "application/json", strings.NewReader(`{"action":"install"}`))
	if e != nil {
		t.Fatal(e)
	}
	res.Body.Close()
	if res.StatusCode != 400 {
		t.Fatal("unprepared install accepted")
	}
	res, e = http.Post(s.origin+s.prefix+"/native/client-update", "application/json", strings.NewReader(`{"action":"check","url":"https://untrusted.example"}`))
	if e != nil {
		t.Fatal(e)
	}
	res.Body.Close()
	if res.StatusCode != 400 {
		t.Fatal("caller can override official source")
	}
}
