package middleware

import (
	"net/http"

	"github.com/labstack/echo/v4"
	echomw "github.com/labstack/echo/v4/middleware"
)

// CORSConfig holds the configurable parameters for CORS.
type CORSConfig struct {
	// AllowOrigins is the list of origins that are allowed to access the API.
	// Empty means same-origin only. Wildcards must not be used with sessions.
	AllowOrigins []string
}

// DefaultCORSConfig keeps browser sessions on the service origin.
func DefaultCORSConfig() CORSConfig {
	return CORSConfig{
		AllowOrigins: nil,
	}
}

// CORS returns an Echo middleware function that sets Cross-Origin Resource
// Sharing headers. It allows the configured origins to make requests with
// the standard set of methods and headers used by the AlgoForge frontend.
func CORS(cfg CORSConfig) echo.MiddlewareFunc {
	allowOrigins := cfg.AllowOrigins
	if len(allowOrigins) == 0 {
		return func(next echo.HandlerFunc) echo.HandlerFunc { return next }
	}
	for _, origin := range allowOrigins {
		if origin == "*" {
			return func(next echo.HandlerFunc) echo.HandlerFunc { return next }
		}
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
			"X-Qraft-Review-Token",
			"X-Qraft-Invitation-Token",
			"X-Qraft-Client",
			"X-CSRF-Token",
			"Idempotency-Key",
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
