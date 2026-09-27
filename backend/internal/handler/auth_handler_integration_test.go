//go:build integration

package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	authmw "github.com/Gingoo-TvT/Qraft/backend/internal/handler/middleware"
	"github.com/Gingoo-TvT/Qraft/backend/internal/identity"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v4"
)

func authHTTPTestService(t *testing.T) *identity.Service {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" || os.Getenv("ALGOFORGE_INTEGRATION_DISPOSABLE_DB") != "true" {
		t.Skip("disposable integration database is required")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := "qraft_auth_http_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	name := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+name); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		_, dropErr := admin.Exec(context.Background(), "DROP SCHEMA "+name+" CASCADE")
		admin.Close()
		if dropErr != nil {
			t.Error(dropErr)
		}
	})
	migration, err := os.ReadFile(filepath.Join("..", "..", "migrations", "034_shared_service_identity.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, string(migration)); err != nil {
		t.Fatal(err)
	}
	return identity.NewService(identity.NewStore(pool), identity.Options{BootstrapToken: strings.Repeat("b", 32)})
}

type authHTTPClient struct {
	cookie *http.Cookie
	csrf   string
}

func TestIdentityHTTPSharedServiceAccountFlow(t *testing.T) {
	service := authHTTPTestService(t)
	e := echo.New()
	e.Use(authmw.SessionAuth(service, authmw.SessionAuthOptions{}))
	e.Use(authmw.RouteAuthorization())
	NewAuthHandler(service, AuthHandlerOptions{SecureCookie: true}).Register(e.Group("/api/v1"))
	e.GET("/api/v1/problems", func(c echo.Context) error { return ok(c, []string{"shared synthetic bank"}) })
	e.GET("/api/v1/unclassified", func(c echo.Context) error { t.Fatal("unclassified route reached"); return nil })
	call := func(client *authHTTPClient, method, path string, body any, clientHeader, csrf bool) (*httptest.ResponseRecorder, map[string]any) {
		t.Helper()
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest(method, path, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		if clientHeader {
			req.Header.Set("X-Qraft-Client", "1")
		}
		if client != nil {
			if client.cookie != nil {
				req.AddCookie(client.cookie)
			}
			if csrf {
				req.Header.Set("X-CSRF-Token", client.csrf)
			}
		}
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		var envelope struct {
			Data map[string]any "json:\"data\""
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &envelope)
		return rec, envelope.Data
	}
	adopt := func(client *authHTTPClient, rec *httptest.ResponseRecorder, data map[string]any) {
		t.Helper()
		for _, cookie := range rec.Result().Cookies() {
			if cookie.Name == identity.SessionCookieName {
				client.cookie = cookie
			}
		}
		if value, ok := data["csrf_token"].(string); ok {
			client.csrf = value
		}
		if client.cookie == nil || !client.cookie.HttpOnly || !client.cookie.Secure || client.cookie.SameSite != http.SameSiteStrictMode || client.cookie.Path != "/api/v1" {
			t.Fatal("unsafe or absent session cookie")
		}
	}
	require := func(rec *httptest.ResponseRecorder, status int) {
		t.Helper()
		if rec.Code != status {
			t.Fatalf("HTTP %d want %d: %s", rec.Code, status, rec.Body.String())
		}
	}
	rec, data := call(nil, "GET", "/api/v1/auth/session", nil, false, false)
	require(rec, 200)
	if data["setup_required"] != true || data["authenticated"] != false {
		t.Fatal(data)
	}
	payload := map[string]any{"token": strings.Repeat("b", 32), "email": "admin@example.test", "password": "http-synthetic-password", "display_name": "Admin"}
	rec, _ = call(nil, "POST", "/api/v1/auth/bootstrap", payload, false, false)
	require(rec, 403)
	admin := &authHTTPClient{}
	rec, data = call(nil, "POST", "/api/v1/auth/bootstrap", payload, true, false)
	require(rec, 200)
	adopt(admin, rec, data)
	if data["authenticated"] != true {
		t.Fatal("bootstrap did not authenticate")
	}
	adminID := data["user"].(map[string]any)["id"].(string)
	rec, _ = call(admin, "POST", "/api/v1/admin/invitations", map[string]any{"email": "member@example.test", "kind": "register"}, false, false)
	require(rec, 403)
	rec, data = call(admin, "POST", "/api/v1/admin/invitations", map[string]any{"email": "member@example.test", "kind": "register"}, false, true)
	require(rec, 201)
	invitation := data["token"].(string)
	body := map[string]any{"token": invitation, "password": "member-synthetic-password", "display_name": "Member", "role": "admin"}
	rec, _ = call(nil, "POST", "/api/v1/auth/register", body, true, false)
	require(rec, 400)
	delete(body, "role")
	rec, data = call(nil, "POST", "/api/v1/auth/register", body, true, false)
	require(rec, 200)
	member := &authHTTPClient{}
	adopt(member, rec, data)
	memberID := data["user"].(map[string]any)["id"].(string)
	if data["user"].(map[string]any)["role"] != "member" {
		t.Fatal("invited member escalated role")
	}
	rec, _ = call(nil, "POST", "/api/v1/auth/register", body, true, false)
	require(rec, 401)
	rec, _ = call(member, "GET", "/api/v1/problems", nil, false, false)
	require(rec, 200)
	rec, _ = call(member, "GET", "/api/v1/admin/users", nil, false, false)
	require(rec, 403)
	rec, _ = call(member, "POST", "/api/v1/admin/invitations", map[string]any{"email": "attacker@example.test", "kind": "register"}, true, true)
	require(rec, 403)
	rec, _ = call(admin, "GET", "/api/v1/unclassified", nil, false, false)
	require(rec, 403)
	// The last active administrator cannot remove their own administrative access.
	rec, _ = call(admin, "PATCH", "/api/v1/admin/users/"+adminID, map[string]any{"disabled": true}, true, true)
	require(rec, 409)
	// Admin account changes invalidate the member's existing browser session.
	rec, _ = call(admin, "PATCH", "/api/v1/admin/users/"+memberID, map[string]any{"disabled": true}, true, true)
	require(rec, 200)
	rec, _ = call(member, "GET", "/api/v1/problems", nil, false, false)
	require(rec, 401)
	rec, _ = call(admin, "PATCH", "/api/v1/admin/users/"+memberID, map[string]any{"disabled": false}, true, true)
	require(rec, 200)
	rec, _ = call(member, "GET", "/api/v1/problems", nil, false, false)
	require(rec, 401)
	rec, data = call(nil, "POST", "/api/v1/auth/login", map[string]any{"email": "member@example.test", "password": "member-synthetic-password"}, true, false)
	require(rec, 200)
	adopt(member, rec, data)
	rec, _ = call(member, "POST", "/api/v1/auth/password", map[string]any{"current_password": "member-synthetic-password", "new_password": "member-replaced-password"}, false, true)
	require(rec, 200)
	rec, _ = call(member, "GET", "/api/v1/problems", nil, false, false)
	require(rec, 401)
	rec, data = call(nil, "POST", "/api/v1/auth/login", map[string]any{"email": "member@example.test", "password": "member-replaced-password"}, true, false)
	require(rec, 200)
	adopt(member, rec, data)
	old := *member
	rec, _ = call(member, "POST", "/api/v1/auth/logout", nil, false, true)
	require(rec, 200)
	rec, _ = call(&old, "GET", "/api/v1/problems", nil, false, false)
	require(rec, 401)
	// Wrong passwords and absent accounts produce the same public error.
	absent, _ := call(nil, "POST", "/api/v1/auth/login", map[string]any{"email": "missing@example.test", "password": "wrong-synthetic-password"}, true, false)
	wrong, _ := call(nil, "POST", "/api/v1/auth/login", map[string]any{"email": "member@example.test", "password": "wrong-synthetic-password"}, true, false)
	require(absent, 401)
	require(wrong, 401)
	if absent.Body.String() != wrong.Body.String() {
		t.Fatal("login discloses account existence")
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("account response cache enabled")
	}
}
