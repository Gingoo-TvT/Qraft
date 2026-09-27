package middleware

import (
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
)

func TestRegisteredRoutesHaveExplicitPermissions(t *testing.T) {
	// Read the actual API registration sites, so a new business endpoint cannot
	// silently fall outside the role matrix. Runtime denial still fails closed.
	paths, err := filepath.Glob("../*.go")
	if err != nil {
		t.Fatal(err)
	}
	paths = append(paths, "../../../cmd/api/main.go")
	found := 0
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, data, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || len(call.Args) < 2 {
				return true
			}
			method, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			switch method.Sel.Name {
			case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD":
			default:
				return true
			}
			arg, ok := call.Args[0].(*ast.BasicLit)
			if !ok || arg.Kind != token.STRING {
				return true
			}
			pattern, _ := strconv.Unquote(arg.Value)
			if !strings.HasPrefix(pattern, "/") {
				return true
			}
			if pattern != "/health" {
				pattern = "/api/v1" + pattern
			}
			found++
			if _, known := PermissionForRoute(method.Sel.Name, pattern); !known {
				t.Errorf("%s: unclassified %s %s", path, method.Sel.Name, pattern)
			}
			return true
		})
	}
	if found < 100 {
		t.Fatalf("route discovery unexpectedly found only %d routes", found)
	}
}

func TestRouteAuthorizationMatrix(t *testing.T) {
	for key, permission := range routePermissions {
		bits := strings.SplitN(key, " ", 2)
		for _, role := range []string{"", "member", "admin", "invented-role"} {
			t.Run(key+"/"+role, func(t *testing.T) {
				e := echo.New()
				e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
					return func(c echo.Context) error {
						if role != "" {
							c.Set(contextKeyUser, &JWTClaims{UserID: "user-test", Role: role})
						}
						return next(c)
					}
				})
				e.Use(RouteAuthorization())
				e.Add(bits[0], bits[1], func(c echo.Context) error { return c.NoContent(http.StatusNoContent) })
				path := strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(bits[1], ":invitation_id", "test"), ":item_id", "test"), ":id", "test")
				path = strings.ReplaceAll(strings.ReplaceAll(path, ":purpose", "statement"), ":tid", "test")
				rec := httptest.NewRecorder()
				e.ServeHTTP(rec, httptest.NewRequest(bits[0], path, nil))
				expected := http.StatusNoContent
				if permission != PermissionPublic {
					switch {
					case role == "":
						expected = 401
					case role == "invented-role":
						expected = 403
					case role == "member" && permission == PermissionAdmin:
						expected = 403
					}
				}
				if rec.Code != expected {
					t.Fatalf("got %d want %d: %s", rec.Code, expected, rec.Body.String())
				}
			})
		}
	}
}

func TestUnclassifiedRouteDeniedEvenToAdministrator(t *testing.T) {
	e := echo.New()
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			c.Set(contextKeyUser, &JWTClaims{UserID: "admin-test", Role: "admin"})
			return next(c)
		}
	})
	e.Use(RouteAuthorization())
	e.GET("/api/v1/new-sensitive-route", func(c echo.Context) error { return c.NoContent(204) })
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/new-sensitive-route", nil))
	if rec.Code != 403 {
		t.Fatalf("unclassified route status %d", rec.Code)
	}
}

func TestCORSDefaultDoesNotTrustOtherOrigins(t *testing.T) {
	e := echo.New()
	e.Use(CORS(DefaultCORSConfig()))
	e.GET("/api/v1/auth/session", func(c echo.Context) error { return c.NoContent(204) })
	req := httptest.NewRequest("OPTIONS", "/api/v1/auth/session", nil)
	req.Header.Set("Origin", "https://untrusted.invalid")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "X-Qraft-Client")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("default CORS trusted external origin")
	}
}
