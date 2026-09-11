package middleware

import (
	"fmt"
	"net/http"
	"runtime/debug"

	"github.com/labstack/echo/v4"
	"github.com/rs/zerolog/log"
)

// Recovery returns an Echo middleware function that recovers from panics
// anywhere in the handler chain. When a panic is caught, it logs the full
// stack trace at error level and returns a generic 500 JSON error to the
// client so that the server stays running.
func Recovery() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			defer func() {
				if r := recover(); r != nil {
					// Capture the stack trace.
					stack := debug.Stack()

					// Determine a useful error message from the recovered value.
					var errMsg string
					switch v := r.(type) {
					case error:
						errMsg = v.Error()
					case string:
						errMsg = v
					default:
						errMsg = fmt.Sprintf("%v", v)
					}

					log.Error().
						Str("error", errMsg).
						Str("method", c.Request().Method).
						Str("path", c.Request().URL.Path).
						Str("stack", string(stack)).
						Msg("panic recovered")

					// Return a generic internal server error. Never expose
					// internal details to the client.
					_ = c.JSON(http.StatusInternalServerError, map[string]interface{}{
						"success": false,
						"error": map[string]string{
							"code":    "INTERNAL_ERROR",
							"message": "an unexpected error occurred",
						},
					})
				}
			}()

			return next(c)
		}
	}
}
