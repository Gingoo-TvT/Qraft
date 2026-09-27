package handler

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/identity"
	"github.com/labstack/echo/v4"
)

func TestAuthAttemptAddressRequiresExplicitPrivateProxy(t *testing.T) {
	for _, tt := range []struct {
		name   string
		trust  bool
		peer   string
		header []string
		want   string
	}{
		{"default ignores header", false, "10.0.0.2:1234", []string{"198.51.100.8"}, "10.0.0.2"},
		{"private proxy", true, "10.0.0.2:1234", []string{"198.51.100.8"}, "198.51.100.8"},
		{"loopback proxy", true, "127.0.0.1:1234", []string{"198.51.100.8"}, "198.51.100.8"},
		{"IPv6 loopback proxy", true, "[::1]:1234", []string{"2001:db8::8"}, "2001:db8::8"},
		{"IPv6 private proxy", true, "[fd00::2]:1234", []string{"2001:db8::8"}, "2001:db8::8"},
		{"mapped private proxy", true, "[::ffff:10.0.0.2]:1234", []string{"::ffff:198.51.100.8"}, "198.51.100.8"},
		{"public peer cannot spoof", true, "198.51.100.7:1234", []string{"198.51.100.8"}, "198.51.100.7"},
		{"public IPv6 cannot spoof", true, "[2001:db8::7]:1234", []string{"198.51.100.8"}, "2001:db8::7"},
		{"mapped public peer cannot spoof", true, "[::ffff:198.51.100.7]:1234", []string{"198.51.100.8"}, "198.51.100.7"},
		{"missing header", true, "10.0.0.2:1234", nil, "10.0.0.2"},
		{"empty header", true, "10.0.0.2:1234", []string{""}, "10.0.0.2"},
		{"invalid header", true, "10.0.0.2:1234", []string{"client.example"}, "10.0.0.2"},
		{"address with port", true, "10.0.0.2:1234", []string{"198.51.100.8:80"}, "10.0.0.2"},
		{"comma separated", true, "10.0.0.2:1234", []string{"198.51.100.8, 198.51.100.9"}, "10.0.0.2"},
		{"duplicate header", true, "10.0.0.2:1234", []string{"198.51.100.8", "198.51.100.9"}, "10.0.0.2"},
		{"scoped address", true, "10.0.0.2:1234", []string{"fe80::8%eth0"}, "10.0.0.2"},
		{"unspecified address", true, "10.0.0.2:1234", []string{"0.0.0.0"}, "10.0.0.2"},
		{"multicast address", true, "10.0.0.2:1234", []string{"ff02::1"}, "10.0.0.2"},
		{"mapped unspecified address", true, "10.0.0.2:1234", []string{"::ffff:0.0.0.0"}, "10.0.0.2"},
		{"invalid peer", true, "private-proxy", []string{"198.51.100.8"}, "private-proxy"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
			r.RemoteAddr = tt.peer
			for _, value := range tt.header {
				r.Header.Add("X-Real-IP", value)
			}
			r.Header.Set("X-Forwarded-For", "203.0.113.99")
			if got := authAttemptAddress(r, tt.trust); got != tt.want {
				t.Fatalf("attempt address = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAuthPublicAttemptTrustedProxyLimitsEachClientSeparately(t *testing.T) {
	h := NewAuthHandler(nil, AuthHandlerOptions{TrustProxy: true})
	e := echo.New()
	attempt := func(realIP string) error {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
		r.RemoteAddr = "172.18.0.2:50000"
		r.Header.Set("X-Real-IP", realIP)
		return h.publicAttempt(e.NewContext(r, httptest.NewRecorder()))
	}
	for i := 0; i < 100; i++ {
		if err := attempt("198.51.100.8"); err != nil {
			t.Fatalf("first client's attempt %d: %v", i+1, err)
		}
	}
	if err := attempt("198.51.100.8"); !errors.Is(err, identity.ErrRateLimited) {
		t.Fatalf("exhausted client should be limited, got %v", err)
	}
	if err := attempt("::ffff:198.51.100.8"); !errors.Is(err, identity.ErrRateLimited) {
		t.Fatalf("alternate IP spelling bypassed limit: %v", err)
	}
	if err := attempt("198.51.100.9"); err != nil {
		t.Fatalf("second client inherited first client's limit: %v", err)
	}
}

func TestAuthPublicAttemptUntrustedHeadersCannotRotateLimitKey(t *testing.T) {
	for _, tt := range []struct {
		name  string
		trust bool
		peer  string
	}{
		{"default private peer", false, "10.0.0.2:50000"},
		{"public peer despite flag", true, "198.51.100.7:50000"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := NewAuthHandler(nil, AuthHandlerOptions{TrustProxy: tt.trust})
			e := echo.New()
			for i := 0; i <= 100; i++ {
				r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
				r.RemoteAddr = tt.peer
				r.Header.Set("X-Real-IP", fmt.Sprintf("203.0.113.%d", i+1))
				r.Header.Set("X-Forwarded-For", fmt.Sprintf("192.0.2.%d", i+1))
				err := h.publicAttempt(e.NewContext(r, httptest.NewRecorder()))
				if i < 100 && err != nil {
					t.Fatalf("attempt %d: %v", i+1, err)
				}
				if i == 100 && !errors.Is(err, identity.ErrRateLimited) {
					t.Fatalf("rotating headers bypassed peer limit: %v", err)
				}
			}
		})
	}
}
