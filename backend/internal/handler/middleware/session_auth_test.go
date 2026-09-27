package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/access"
	"github.com/Gingoo-TvT/Qraft/backend/internal/identity"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

type sessionAuthFake struct {
	session identity.Session
	err     error
	calls   int
}

func (s *sessionAuthFake) Authenticate(_ context.Context, token string) (identity.Session, error) {
	s.calls++
	if token != "opaque-session" {
		return identity.Session{}, identity.ErrUnauthorized
	}
	return s.session, s.err
}
func TestSessionAuthEnforcesIdentityCSRFAndExactPublicPaths(t *testing.T) {
	uid := uuid.New()
	tests := []struct {
		name, method, path, cookie, bearer, csrf, client string
		want                                             int
	}{
		{"private anonymous", "GET", "/api/v1/protected", "", "", "", "", 401},
		{"legacy JWT refused", "GET", "/api/v1/protected", "", "Bearer signed-but-untracked", "", "", 401},
		{"cookie authenticates", "GET", "/api/v1/protected", "opaque-session", "", "", "", 200},
		{"write needs csrf", "POST", "/api/v1/protected", "opaque-session", "", "", "", 403},
		{"wrong csrf", "POST", "/api/v1/protected", "opaque-session", "", "wrong", "", 403},
		{"write allowed", "POST", "/api/v1/protected", "opaque-session", "", "csrf-bound-to-session", "", 200},
		{"login needs client marker", "POST", "/api/v1/auth/login", "", "", "", "", 403},
		{"login marker allowed", "POST", "/api/v1/auth/login", "", "", "", "1", 200},
		{"no broad public prefix", "GET", "/api/v1/public/anything", "", "", "", "", 401},
		{"no broad health prefix", "GET", "/healthz-sensitive", "", "", "", "", 401},
		{"no auth namespace bypass", "POST", "/api/v1/auth/password", "", "", "", "1", 401},
		{"session discovery public", "GET", "/api/v1/auth/session", "", "", "", "", 200},
		{"invitation discovery public", "GET", "/api/v1/auth/invitation", "", "", "", "", 200},
		{"review external authority", "POST", "/api/v1/public/rating/review", "opaque-session", "", "", "", 200},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &sessionAuthFake{session: identity.Session{User: identity.User{ID: uid, Role: "member"}, CSRFToken: "csrf-bound-to-session", ExpiresAt: time.Now().Add(time.Hour)}}
			e := echo.New()
			e.Use(SessionAuth(fake, SessionAuthOptions{}))
			e.Add(tt.method, tt.path, func(c echo.Context) error {
				if tt.cookie != "" && tt.path != "/api/v1/public/rating/review" {
					claims := GetClaims(c)
					p, ok := access.FromContext(c.Request().Context())
					if claims == nil || claims.UserID != uid.String() || claims.Role != "member" || !ok || p.UserID != uid.String() {
						t.Fatal("missing trusted principal")
					}
				}
				if tt.path == "/api/v1/public/rating/review" && GetClaims(c) != nil {
					t.Fatal("account leaked into review authority")
				}
				return c.NoContent(http.StatusOK)
			})
			req := httptest.NewRequest(tt.method, tt.path, nil)
			if tt.cookie != "" {
				req.AddCookie(&http.Cookie{Name: identity.SessionCookieName, Value: tt.cookie})
			}
			req.Header.Set("Authorization", tt.bearer)
			req.Header.Set("X-CSRF-Token", tt.csrf)
			req.Header.Set("X-Qraft-Client", tt.client)
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			if rec.Code != tt.want {
				t.Fatalf("status %d want %d: %s", rec.Code, tt.want, rec.Body.String())
			}
			if tt.path == "/api/v1/public/rating/review" && fake.calls != 0 {
				t.Fatal("review must not consult account session")
			}
		})
	}
}
func TestSessionAuthRevocationAndStoreFailureFailClosed(t *testing.T) {
	for _, tt := range []struct {
		err  error
		want int
	}{{identity.ErrUnauthorized, 401}, {errors.New("database unavailable"), 503}} {
		fake := &sessionAuthFake{err: tt.err}
		e := echo.New()
		e.Use(SessionAuth(fake, SessionAuthOptions{}))
		e.GET("/private", func(c echo.Context) error { t.Fatal("unavailable or revoked session reached handler"); return nil })
		req := httptest.NewRequest("GET", "/private", nil)
		req.AddCookie(&http.Cookie{Name: identity.SessionCookieName, Value: "opaque-session"})
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Code != tt.want {
			t.Fatalf("status %d want %d", rec.Code, tt.want)
		}
	}
}
func TestSessionAuthLocalAdminIsExplicitAndReviewIsSeparate(t *testing.T) {
	e := echo.New()
	e.Use(SessionAuth(nil, SessionAuthOptions{DevMode: true}))
	e.GET("/private", func(c echo.Context) error {
		claims := GetClaims(c)
		p, ok := access.FromContext(c.Request().Context())
		if claims == nil || claims.UserID != "local-admin" || claims.Role != "admin" || !ok || !p.IsAdmin() {
			t.Fatal("explicit local principal missing")
		}
		return c.NoContent(200)
	})
	e.GET("/api/v1/public/rating/review", func(c echo.Context) error {
		if GetClaims(c) != nil {
			t.Fatal("local principal leaks into reviewer endpoint")
		}
		return c.NoContent(200)
	})
	for _, path := range []string{"/private", "/api/v1/public/rating/review"} {
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != 200 {
			t.Fatal(rec.Code)
		}
	}
}

type streamAuthenticator struct {
	calls   atomic.Int32
	session identity.Session
	mode    string
	entered chan struct{}
}

func (a *streamAuthenticator) Authenticate(ctx context.Context, _ string) (identity.Session, error) {
	if a.calls.Add(1) == 1 {
		return a.session, nil
	}
	switch a.mode {
	case "revoked":
		return identity.Session{}, identity.ErrUnauthorized
	case "role":
		changed := a.session
		changed.User.Role = "member"
		return changed, nil
	case "failure":
		return identity.Session{}, errors.New("store unavailable")
	case "wait":
		if a.entered != nil {
			close(a.entered)
		}
		<-ctx.Done()
		return identity.Session{}, ctx.Err()
	}
	return a.session, nil
}
func TestSessionAuthClosesActiveStreamsOnRevocation(t *testing.T) {
	for _, path := range []string{"/api/v1/workflows/:id/events", "/api/v1/generation/jobs/:id/events"} {
		for _, mode := range []string{"revoked", "role", "failure"} {
			t.Run(path+"/"+mode, func(t *testing.T) {
				fake := &streamAuthenticator{mode: mode, session: identity.Session{ID: uuid.New(), User: identity.User{ID: uuid.New(), Role: "admin"}}}
				e := echo.New()
				e.Use(SessionAuth(fake, SessionAuthOptions{SessionCheckInterval: time.Millisecond}))
				e.GET(path, func(c echo.Context) error {
					select {
					case <-c.Request().Context().Done():
						return c.NoContent(200)
					case <-time.After(time.Second):
						t.Fatal("revoked stream not cancelled")
						return nil
					}
				})
				req := httptest.NewRequest("GET", strings.Replace(path, ":id", "synthetic", 1), nil)
				req.AddCookie(&http.Cookie{Name: identity.SessionCookieName, Value: "opaque"})
				rec := httptest.NewRecorder()
				e.ServeHTTP(rec, req)
				if rec.Code != 200 || fake.calls.Load() != 2 {
					t.Fatal("unexpected stream lifecycle", rec.Code, fake.calls.Load())
				}
			})
		}
	}
}
func TestSessionAuthStopsStreamCheckerWhenHandlerReturns(t *testing.T) {
	fake := &streamAuthenticator{session: identity.Session{ID: uuid.New(), User: identity.User{ID: uuid.New(), Role: "admin"}}}
	e := echo.New()
	e.Use(SessionAuth(fake, SessionAuthOptions{SessionCheckInterval: time.Hour}))
	e.GET("/api/v1/workflows/:id/events", func(c echo.Context) error { return c.NoContent(200) })
	req := httptest.NewRequest("GET", "/api/v1/workflows/synthetic/events", nil)
	req.AddCookie(&http.Cookie{Name: identity.SessionCookieName, Value: "opaque"})
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	// ServeHTTP only returns after its request-owned checker has joined.
	if rec.Code != 200 || fake.calls.Load() != 1 {
		t.Fatal("checker outlived short response")
	}
}

func TestSessionAuthCancelsInFlightStreamCheckOnReturn(t *testing.T) {
	fake := &streamAuthenticator{mode: "wait", entered: make(chan struct{}), session: identity.Session{ID: uuid.New(), User: identity.User{ID: uuid.New(), Role: "admin"}}}
	e := echo.New()
	e.Use(SessionAuth(fake, SessionAuthOptions{SessionCheckInterval: time.Millisecond}))
	e.GET("/api/v1/workflows/:id/events", func(c echo.Context) error {
		select {
		case <-fake.entered:
			return c.NoContent(200)
		case <-time.After(time.Second):
			t.Fatal("checker never started")
			return nil
		}
	})
	req := httptest.NewRequest("GET", "/api/v1/workflows/synthetic/events", nil)
	req.AddCookie(&http.Cookie{Name: identity.SessionCookieName, Value: "opaque"})
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != 200 || fake.calls.Load() != 2 {
		t.Fatal("in-flight check not joined")
	}
}
