package middleware

import (
	"net/http"

	"github.com/labstack/echo/v4"
	echomw "github.com/labstack/echo/v4/middleware"
)

// CORSConfig holds the configurable parameters for CORS.
type CORSConfig struct {
	// AllowOrigins is the list of origins that are allowed to access the API.
	// Use ["*"] during development; restrict to specific domains in production.
	AllowOrigins []string
}

// DefaultCORSConfig returns a permissive CORS configuration suitable for
// local development.
func DefaultCORSConfig() CORSConfig {
	return CORSConfig{
		AllowOrigins: []string{"*"},
	}
}

// CORS returns an Echo middleware function that sets Cross-Origin Resource
// Sharing headers. It allows the configured origins to make requests with
// the standard set of methods and headers used by the AlgoForge frontend.
func CORS(cfg CORSConfig) echo.MiddlewareFunc {
	allowOrigins := cfg.AllowOrigins
	if len(allowOrigins) == 0 {
		allowOrigins = []string{"*"}
	}

	return echomw.CORSWithConfig(echomw.CORSConfig{
		AllowOrigins: allowOrigins,
		AllowMethods: []string{
			http.MethodGet,
			http.MethodHead,
			http.MethodPost,
			http.MethodPut,
			http.MethodPatch,
			http.MethodDelete,
			http.MethodOptions,
		},
		AllowHeaders: []string{
			echo.HeaderOrigin,
			echo.HeaderContentType,
			echo.HeaderAccept,
			echo.HeaderAuthorization,
			echo.HeaderXRequestID,
			"X-Requested-With",
		},
		ExposeHeaders: []string{
			echo.HeaderContentLength,
			echo.HeaderContentType,
			"X-Request-Id",
		},
		AllowCredentials: true,
		MaxAge:           86400, // 24 hours
	})
}
