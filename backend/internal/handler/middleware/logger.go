package middleware

import (
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// Logger returns an Echo middleware function that logs every HTTP request
// using zerolog structured logging. It records the method, path, status
// code, latency, client IP, and user agent.
func Logger() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			start := time.Now()

			// Process the request.
			err := next(c)

			// If the handler returned an error, let Echo's error handler
			// convert it into the appropriate HTTP response.
			if err != nil {
				c.Error(err)
			}

			req := c.Request()
			res := c.Response()
			latency := time.Since(start)

			var event *zerolog.Event

			status := res.Status
			switch {
			case status >= 500:
				event = log.Error()
			case status >= 400:
				event = log.Warn()
			default:
				event = log.Info()
			}

			event.
				Str("method", req.Method).
				Str("path", requestLogPath(req.URL.Path)).
				Str("query", req.URL.RawQuery).
				Int("status", status).
				Dur("latency", latency).
				Str("ip", c.RealIP()).
				Str("user_agent", req.UserAgent()).
				Int64("bytes_out", res.Size).
				Msg("request")

			return nil
		}
	}
}

func requestLogPath(path string) string {
	const annotationPrefix = "/api/v1/annotate/"
	if !strings.HasPrefix(path, annotationPrefix) {
		return path
	}

	remainder := strings.TrimPrefix(path, annotationPrefix)
	if remainder == "" {
		return path
	}
	if separator := strings.IndexByte(remainder, '/'); separator >= 0 {
		return annotationPrefix + "[redacted]" + remainder[separator:]
	}
	return annotationPrefix + "[redacted]"
}
