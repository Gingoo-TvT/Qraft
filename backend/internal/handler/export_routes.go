package handler

import "github.com/labstack/echo/v4"

// RegisterExportRoutes exposes the portable Hydro surface. Private platform
// adapters are deliberately absent from the public product.
func RegisterExportRoutes(group *echo.Group, problems *ProblemHandler, _ *QuizHandler, _ bool) {
	group.GET("/problems/hydro.zip", problems.HandleDownloadHydroBatch)
	group.POST("/problems/hydro/validate", problems.HandleValidateHydroPackage)
	group.GET("/problems/:id/hydro.zip", problems.HandleDownloadHydroPackage)
}
