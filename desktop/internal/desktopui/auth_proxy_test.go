package desktopui

import (
	"crypto/tls"
	"crypto/x509"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Gingoo-TvT/Qraft/desktop/internal/app"
)

// Exercise the real bridge, not only request-building helpers.
func TestAccountCookiesCSRFAndAnonymousInvitation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/login":
			if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
				t.Error("previous identity reached login")
			}
			if r.Header.Get("X-Qraft-Client") != "1" {
				t.Error("client request marker lost")
			}
			http.SetCookie(w, &http.Cookie{Name: "qraft_session", Value: "synthetic-session", Path: "/", HttpOnly: true})
			io.WriteString(w, "logged in")
		case "/api/v1/auth/invitation":
			if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" || r.Header.Get("X-CSRF-Token") != "" {
				t.Error("account credentials reached anonymous invitation")
			}
			if r.Header.Get("X-Qraft-Invitation-Token") != "synthetic-invitation" {
				t.Error("invitation identity lost")
			}
			http.SetCookie(w, &http.Cookie{Name: "qraft_session", Value: "poison", Path: "/"})
			io.WriteString(w, "invitation")
		case "/api/v1/admin/users":
			cookie, err := r.Cookie("qraft_session")
			if err != nil || cookie.Value != "synthetic-session" {
				t.Error("session missing or anonymous invitation replaced it")
			}
			if r.Header.Get("X-CSRF-Token") != "synthetic-csrf" {
				t.Error("CSRF header lost")
			}
			if r.Header.Get("X-Qraft-Invitation-Token") != "" {
				t.Error("invitation leaked to an unrelated endpoint")
			}
			io.WriteString(w, "users")
		case "/api/v1/auth/logout":
			io.WriteString(w, "signed out")
		case "/api/v1/auth/session":
			if r.Header.Get("Cookie") != "" {
				t.Error("logout left a cookie in the native jar")
			}
			io.WriteString(w, "anonymous")
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	s, _ := workbench(t, server.URL, nil)
	request := func(path string) {
		req, _ := http.NewRequest(http.MethodPost, s.origin+s.prefix+"/bridge"+path, strings.NewReader("{}"))
		req.Header.Set("X-Qraft-Client", "1")
		req.Header.Set("X-CSRF-Token", "synthetic-csrf")
		req.Header.Set("X-Qraft-Invitation-Token", "synthetic-invitation")
		req.Header.Set("Authorization", "Bearer stale")
		req.Header.Set("Cookie", "browser-cookie=must-not-forward")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		_, _ = io.Copy(io.Discard, res.Body)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("HTTP %d", res.StatusCode)
		}
		if res.Header.Get("Set-Cookie") != "" {
			t.Fatal("native backend cookie escaped to browser")
		}
	}
	request("/api/v1/auth/login")
	request("/api/v1/auth/invitation")
	request("/api/v1/admin/users")
	request("/api/v1/auth/logout")
	request("/api/v1/auth/session")
}

func TestStalePageCannotForwardCSRFToAnotherService(t *testing.T) {
	calls := 0
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(200) }))
	defer backend.Close()
	s, _ := workbench(t, backend.URL, nil)
	req, _ := http.NewRequest(http.MethodPost, s.origin+s.prefix+"/bridge/api/v1/workflows", strings.NewReader("{}"))
	req.Header.Set("X-Qraft-Service", "https://previous-service.invalid")
	req.Header.Set("X-CSRF-Token", "previous-secret")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusConflict || calls != 0 {
		t.Fatalf("stale credential reached selected service: HTTP %d, calls %d", res.StatusCode, calls)
	}
}

func TestHTTPSAccountSessionCookieJarCSRFLogoutAndServiceIsolation(t *testing.T) {
	// Exercise a TLS upstream through the real local HTTP bridge. The proxy's
	// transport trusts only the test certificate roots; certificate verification
	// remains enabled, including the upstream IP/hostname check.
	newService := func(name string) *httptest.Server {
		loggedIn := false
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.TLS == nil {
				t.Error("upstream request did not use TLS")
			}
			cookie, err := r.Cookie("qraft_session")
			switch r.URL.Path {
			case "/api/v1/auth/login":
				if r.Method != http.MethodPost || r.Header.Get("X-Qraft-Client") != "1" {
					t.Error("login method or client marker lost")
				}
				if err == nil || r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
					t.Error("previous identity leaked into login")
				}
				loggedIn = true
				http.SetCookie(w, &http.Cookie{Name: "qraft_session", Value: name + "-session", Path: "/api/v1", HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})
				io.WriteString(w, name+"-csrf")
			case "/api/v1/auth/session":
				if loggedIn {
					if err != nil || cookie.Value != name+"-session" {
						t.Error("wrong or missing HTTPS session cookie", name)
					}
					io.WriteString(w, name)
				} else {
					if err == nil || r.Header.Get("Cookie") != "" {
						t.Error("cookie leaked after logout or to another HTTPS service", name)
					}
					io.WriteString(w, "anonymous")
				}
			case "/api/v1/auth/password", "/api/v1/auth/logout":
				if r.Method != http.MethodPost || !loggedIn || err != nil || cookie.Value != name+"-session" {
					t.Error("authenticated write lost its HTTPS session", name)
				}
				if r.Header.Get("X-CSRF-Token") != name+"-csrf" {
					t.Error("authenticated write lost or mixed its CSRF token", name)
				}
				if r.URL.Path == "/api/v1/auth/logout" {
					loggedIn = false
					http.SetCookie(w, &http.Cookie{Name: "qraft_session", Value: "", Path: "/api/v1", HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode, MaxAge: -1, Expires: time.Unix(1, 0)})
				}
				io.WriteString(w, "ok")
			default:
				t.Errorf("unexpected HTTPS path %s", r.URL.Path)
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		t.Cleanup(server.Close)
		return server
	}
	first, second := newService("first"), newService("second")
	roots := x509.NewCertPool()
	roots.AddCert(first.Certificate())
	roots.AddCert(second.Certificate())
	original := http.DefaultTransport
	transport := original.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = original; transport.CloseIdleConnections() })
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	s, manager := workbench(t, first.URL, nil)
	selected := first.URL
	request := func(method, path, csrf, want string) {
		t.Helper()
		req, err := http.NewRequest(method, s.origin+s.prefix+"/bridge"+path, strings.NewReader("{}"))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Origin", s.origin)
		req.Header.Set("X-Qraft-Service", selected)
		req.Header.Set("X-Qraft-Client", "1")
		req.Header.Set("Cookie", "browser-cookie=must-not-reach-upstream")
		if csrf != "" {
			req.Header.Set("X-CSRF-Token", csrf)
		}
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(res.Body)
		res.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if res.StatusCode != http.StatusOK || string(body) != want {
			t.Fatalf("%s %s: HTTP %d %q, want %q", method, path, res.StatusCode, body, want)
		}
		if res.Header.Get("Set-Cookie") != "" {
			t.Fatal("HTTPS HttpOnly cookie escaped to local browser")
		}
	}
	selectService := func(raw string) {
		t.Helper()
		cfg := app.DefaultConfig()
		cfg.ServerURL = raw
		if err := manager.Save(cfg); err != nil {
			t.Fatal(err)
		}
		selected = raw
	}
	request("POST", "/api/v1/auth/login", "", "first-csrf")
	firstTarget, err := url.Parse(first.URL + "/api/v1/auth/session")
	if err != nil {
		t.Fatal(err)
	}
	if cookies := s.jar(firstTarget).Cookies(firstTarget); len(cookies) != 1 || cookies[0].Value != "first-session" {
		t.Fatalf("HTTPS cookie was not retained in native jar: %v", cookies)
	}
	insecureTarget := *firstTarget
	insecureTarget.Scheme = "http"
	if len(s.jar(firstTarget).Cookies(&insecureTarget)) != 0 {
		t.Fatal("Secure session cookie can be sent over HTTP")
	}
	request("GET", "/api/v1/auth/session", "", "first")
	request("POST", "/api/v1/auth/password", "first-csrf", "ok")
	selectService(second.URL)
	request("GET", "/api/v1/auth/session", "", "anonymous")
	request("POST", "/api/v1/auth/login", "", "second-csrf")
	request("POST", "/api/v1/auth/password", "second-csrf", "ok")
	selectService(first.URL)
	request("GET", "/api/v1/auth/session", "", "first")
	request("POST", "/api/v1/auth/logout", "first-csrf", "ok")
	request("GET", "/api/v1/auth/session", "", "anonymous")
	if len(s.jar(firstTarget).Cookies(firstTarget)) != 0 {
		t.Fatal("logout did not clear the HTTPS cookie jar")
	}
	selectService(second.URL)
	request("GET", "/api/v1/auth/session", "", "second")
	request("POST", "/api/v1/auth/logout", "second-csrf", "ok")
	request("GET", "/api/v1/auth/session", "", "anonymous")
}
