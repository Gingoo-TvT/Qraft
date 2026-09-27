package handler

import (
	"github.com/Gingoo-TvT/Qraft/backend/internal/handler/middleware"
	"github.com/Gingoo-TvT/Qraft/backend/internal/service"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"net/http"
	"strings"
)

// SharedProblemAccess also guards artifact endpoints whose service methods
// predate user identities and access the object store directly.
func SharedProblemAccess(problems *service.ProblemService) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			claims := middleware.GetClaims(c)
			if claims == nil || claims.Role == "admin" || !strings.HasPrefix(c.Path(), "/api/v1/problems/:id") {
				return next(c)
			}
			id, err := uuid.Parse(c.Param("id"))
			if err != nil {
				return c.NoContent(http.StatusNotFound)
			}
			if _, err := problems.GetProblem(c.Request().Context(), id); err != nil {
				return notFound(c, "problem not found")
			}
			return next(c)
		}
	}
}
