package middleware

import (
	"net/http"
	"strings"

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
