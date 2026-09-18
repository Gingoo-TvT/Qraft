package desktopui

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestInvitedRatingProxyDoesNotUseOrModifyAdminSession(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/login":
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "admin", Path: "/"})
		case "/api/v1/public/rating/review":
			if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" || r.Header.Get("X-Actor") != "" {
				t.Error("administrator credentials reached invited review")
			}
			if r.Header.Get("X-Qraft-Review-Token") != "synthetic-invitation" {
				t.Error("invitation missing")
			}
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "reviewer", Path: "/"})
			if r.URL.Query().Get("redirect") == "1" {
				http.Redirect(w, r, "/api/v1/me", http.StatusTemporaryRedirect)
			}
		case "/api/v1/me":
			if r.Header.Get("X-Qraft-Review-Token") != "" {
				t.Error("invitation leaked to another API")
			}
			c, err := r.Cookie("session")
			if err != nil {
				t.Error(err)
			} else {
				io.WriteString(w, c.Value)
			}
		}
	}))
	defer backend.Close()
	s, _ := workbench(t, backend.URL, nil)
	base := s.origin + s.prefix + "/bridge"
	read(t, base+"/api/v1/login")
	for _, query := range []string{"", "?redirect=1"} {
		req, _ := http.NewRequest(http.MethodGet, base+"/api/v1/public/rating/review"+query, nil)
		req.Header.Set("Authorization", "Bearer synthetic-admin")
		req.Header.Set("Cookie", "local=private")
		req.Header.Set("X-Actor", "administrator")
		req.Header.Set("X-Qraft-Review-Token", "synthetic-invitation")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		want := http.StatusOK
		if query != "" {
			want = http.StatusBadGateway
		}
		if res.StatusCode != want {
			t.Fatalf("status %d, want %d", res.StatusCode, want)
		}
		if res.Header.Get("Set-Cookie") != "" {
			t.Fatal("review cookie reached browser")
		}
	}
	req, _ := http.NewRequest(http.MethodGet, base+"/api/v1/me", nil)
	req.Header.Set("X-Qraft-Review-Token", "synthetic-invitation")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if string(body) != "admin" {
		t.Fatalf("anonymous review changed admin session: %s", body)
	}
}
