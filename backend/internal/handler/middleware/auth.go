package middleware

import (
	"context"
	"crypto/subtle"
	"errors"

	"github.com/Gingoo-TvT/Qraft/backend/internal/access"
	"github.com/Gingoo-TvT/Qraft/backend/internal/identity"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/labstack/echo/v4"
)

// JWTClaims represents the claims stored inside an AlgoForge JWT.
type JWTClaims struct {
	UserID string `json:"user_id"`
	Email  string `json:"email"`
	Role   string `json:"role"`
	jwt.RegisteredClaims
}

// contextKey is the key used to store claims in the echo context.
const contextKeyUser = "user"

// PublicPaths returns the set of path prefixes that do not require
// authentication. Requests whose path starts with any of these strings
// bypass the JWT middleware.
func PublicPaths() []string {
	return []string{
		"/health",
		"/api/v1/public",
	}
}

// Auth returns an Echo middleware function that validates JWTs carried in
// the Authorization header. Requests to public paths (see PublicPaths) are
// allowed through without a token.
//
// When devMode is true, all requests bypass authentication entirely.
//
// On success the parsed JWTClaims are stored in the echo.Context under
// the key "user" and can be retrieved with GetClaims.
func Auth(jwtSecret string, devMode bool) echo.MiddlewareFunc {
	if devMode {
		return func(next echo.HandlerFunc) echo.HandlerFunc {
			return func(c echo.Context) error {
				return next(c)
			}
		}
	}

	publicPaths := PublicPaths()

	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			path := c.Request().URL.Path
			if isPublicAnnotationRequest(c.Request().Method, path) {
				return next(c)
			}

			// Skip authentication for public paths.
			for _, pp := range publicPaths {
				if strings.HasPrefix(path, pp) {
					return next(c)
				}
			}

			// Extract the Bearer token from the Authorization header.
			authHeader := c.Request().Header.Get("Authorization")
			if authHeader == "" {
				return c.JSON(http.StatusUnauthorized, map[string]interface{}{
					"success": false,
					"error": map[string]string{
						"code":    "MISSING_TOKEN",
						"message": "authorization header is required",
					},
				})
			}

			parts := strings.SplitN(authHeader, " ", 2)
			if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
				return c.JSON(http.StatusUnauthorized, map[string]interface{}{
					"success": false,
					"error": map[string]string{
						"code":    "INVALID_TOKEN_FORMAT",
						"message": "authorization header must use Bearer scheme",
					},
				})
			}
			tokenString := parts[1]

			// Parse and validate the JWT.
			claims := &JWTClaims{}
			token, err := jwt.ParseWithClaims(tokenString, claims, func(t *jwt.Token) (interface{}, error) {
				// Ensure the signing method is HMAC.
				if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
					return nil, jwt.ErrSignatureInvalid
				}
				return []byte(jwtSecret), nil
			})
			if err != nil || !token.Valid {
				return c.JSON(http.StatusUnauthorized, map[string]interface{}{
					"success": false,
					"error": map[string]string{
						"code":    "INVALID_TOKEN",
						"message": "token is invalid or expired",
					},
				})
			}

			// Store the claims in the context for downstream handlers.
			c.Set(contextKeyUser, claims)

			return next(c)
		}
	}
}

func isPublicAnnotationRequest(method, path string) bool {
	const prefix = "/api/v1/annotate/"
	if !strings.HasPrefix(path, prefix) {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(path, prefix), "/")
	if len(parts) != 2 || parts[0] == "" {
		return false
	}
	return (method == http.MethodGet && parts[1] == "next") ||
		(method == http.MethodPost && parts[1] == "submit")
}

// GetClaims extracts the authenticated user's JWT claims from the echo
// context. Returns nil if the request was not authenticated (e.g. a
// public path).
func GetClaims(c echo.Context) *JWTClaims {
	val := c.Get(contextKeyUser)
	if val == nil {
		return nil
	}
	claims, ok := val.(*JWTClaims)
	if !ok {
		return nil
	}
	return claims
}

// SessionAuthenticator must consult persistent account/session state on every
// request. Signed legacy JWTs are deliberately not accepted in shared mode.
type SessionAuthenticator interface {
	Authenticate(context.Context, string) (identity.Session, error)
}
type SessionAuthOptions struct {
	DevMode              bool
	SessionCheckInterval time.Duration
}

const contextKeySession = "qraft-identity-session"

func GetSession(c echo.Context) *identity.Session {
	session, _ := c.Get(contextKeySession).(*identity.Session)
	return session
}
func sessionAuthError(c echo.Context, status int, code, message string) error {
	c.Response().Header().Set("Cache-Control", "no-store")
	return c.JSON(status, map[string]interface{}{"success": false, "error": map[string]string{"code": code, "message": message}})
}
func IsPublicSessionRoute(method, path string) bool {
	switch method + " " + path {
	case "GET /health", "GET /api/v1/auth/session", "GET /api/v1/auth/invitation",
		"POST /api/v1/auth/login", "POST /api/v1/auth/register", "POST /api/v1/auth/reset-password",
		"POST /api/v1/auth/bootstrap",
		"GET /api/v1/public/rating/review", "POST /api/v1/public/rating/review":
		return true
	}
	return false
}
func anonymousAccountWrite(method, path string) bool {
	return method == http.MethodPost && (path == "/api/v1/auth/login" || path == "/api/v1/auth/register" || path == "/api/v1/auth/reset-password" || path == "/api/v1/auth/bootstrap")
}
func SessionAuth(auth SessionAuthenticator, opts SessionAuthOptions) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			path, method := c.Request().URL.Path, c.Request().Method
			// Reviewer tokens form a separate authority. Never attach account claims,
			// including local-admin, to this otherwise-public endpoint.
			if (method == http.MethodGet || method == http.MethodPost) && path == "/api/v1/public/rating/review" {
				return next(c)
			}
			if opts.DevMode {
				claims := &JWTClaims{UserID: "local-admin", Role: "admin"}
				c.Set(contextKeyUser, claims)
				c.SetRequest(c.Request().WithContext(access.WithPrincipal(c.Request().Context(), access.Principal{UserID: "local-admin", Role: "admin"})))
				return next(c)
			}
			if anonymousAccountWrite(method, path) && c.Request().Header.Get("X-Qraft-Client") != "1" {
				return sessionAuthError(c, http.StatusForbidden, "CLIENT_HEADER_REQUIRED", "Qraft client header is required")
			}
			public := IsPublicSessionRoute(method, path)
			cookie, err := c.Cookie(identity.SessionCookieName)
			if err == nil && cookie.Value != "" && auth != nil {
				session, authErr := auth.Authenticate(c.Request().Context(), cookie.Value)
				if authErr != nil && !errors.Is(authErr, identity.ErrUnauthorized) {
					return sessionAuthError(c, http.StatusServiceUnavailable, "AUTH_UNAVAILABLE", "account service unavailable")
				}
				if authErr == nil {
					c.Set(contextKeySession, &session)
					claims := &JWTClaims{UserID: session.User.ID.String(), Email: session.User.Email, Role: session.User.Role}
					claims.Subject = claims.UserID
					c.Set(contextKeyUser, claims)
					c.SetRequest(c.Request().WithContext(access.WithPrincipal(c.Request().Context(), access.Principal{UserID: claims.UserID, Role: claims.Role})))
				}
			}
			if public {
				return next(c)
			}
			session := GetSession(c)
			if session == nil {
				return sessionAuthError(c, http.StatusUnauthorized, "UNAUTHORIZED", "sign in to this service")
			}
			if method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions {
				submitted := c.Request().Header.Get("X-CSRF-Token")
				if submitted == "" || subtle.ConstantTimeCompare([]byte(submitted), []byte(session.CSRFToken)) != 1 {
					return sessionAuthError(c, http.StatusForbidden, "CSRF_INVALID", "refresh your session before retrying")
				}
			}
			if method == http.MethodGet && (c.Path() == "/api/v1/workflows/:id/events" || c.Path() == "/api/v1/generation/jobs/:id/events") {
				interval := opts.SessionCheckInterval
				if interval <= 0 {
					interval = 15 * time.Second
				}
				streamCtx, cancel := context.WithCancel(c.Request().Context())
				c.SetRequest(c.Request().WithContext(streamCtx))
				done := make(chan struct{})
				token := cookie.Value
				original := *session
				go func() {
					defer close(done)
					ticker := time.NewTicker(interval)
					defer ticker.Stop()
					for {
						select {
						case <-streamCtx.Done():
							return
						case <-ticker.C:
							checkCtx, checkCancel := context.WithTimeout(streamCtx, 5*time.Second)
							current, checkErr := auth.Authenticate(checkCtx, token)
							checkCancel()
							if checkErr != nil || current.ID != original.ID || current.User.ID != original.User.ID || current.User.Role != original.User.Role || current.User.Disabled {
								cancel()
								return
							}
						}
					}
				}()
				defer func() { cancel(); <-done }()
				return next(c)
			}
			return next(c)
		}
	}
}
