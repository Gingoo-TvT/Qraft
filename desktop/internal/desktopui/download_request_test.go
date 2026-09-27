package desktopui

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestNativeTemplateDownloadForwardsBodySessionAndCSRF(t *testing.T) {
	payload := json.RawMessage(`{"tag_catalog":{"activeTagsTree":[]}}`)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("X-CSRF-Token") != "test-csrf" {
			t.Errorf("wrong request %s %v", r.Method, r.Header)
		}
		if c, e := r.Cookie("qraft_session"); e != nil || c.Value != "sample-session" {
			t.Error("session lost")
		}
		data, _ := io.ReadAll(r.Body)
		if string(data) != string(payload) {
			t.Error("body changed")
		}
		w.Header().Set("Content-Type", "application/zip")
		io.WriteString(w, "test-zip")
	}))
	defer backend.Close()
	dest := filepath.Join(t.TempDir(), "set.zip")
	s, _ := workbench(t, backend.URL, func(string) (string, error) { return dest, nil })
	target, err := s.backend()
	if err != nil {
		t.Fatal(err)
	}
	s.jar(target).SetCookies(target, []*http.Cookie{{Name: "qraft_session", Value: "sample-session"}})
	result, err := s.downloadRequest(context.Background(), "/api/v1/problem-sets/test/export.zip?mode=testing&format=generic", "set.zip", "", payload, "test-csrf")
	if err != nil || result.Status != "completed" {
		t.Fatalf("%+v %v", result, err)
	}
	data, _ := os.ReadFile(dest)
	if string(data) != "test-zip" {
		t.Fatal("download corrupted")
	}
	for _, path := range []string{"/api/v1/admin/users", "/api/v1/problem-sets/test/export.zip?mode=publication", "/api/v1/problem-sets/test/export.zip?mode=testing&format=hydro"} {
		if _, err := s.downloadRequest(context.Background(), path, "bad.zip", "", payload, "test-csrf"); err == nil {
			t.Fatal("arbitrary POST allowed")
		}
	}
}
