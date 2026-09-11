package handler

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

// ---------------------------------------------------------------------------
// Standard API response envelope
// ---------------------------------------------------------------------------

// APIResponse is the uniform JSON envelope returned by every API endpoint.
type APIResponse struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   *APIError   `json:"error,omitempty"`
	Meta    *Meta       `json:"meta,omitempty"`
}

// Meta carries pagination metadata for list endpoints.
type Meta struct {
	Total int `json:"total,omitempty"`
	Page  int `json:"page,omitempty"`
	Size  int `json:"size,omitempty"`
}

// APIError describes a machine-readable error returned to the client.
type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ---------------------------------------------------------------------------
// Response helpers
// ---------------------------------------------------------------------------

// ok sends a 200 success response with data.
func ok(c echo.Context, data interface{}) error {
	return c.JSON(http.StatusOK, APIResponse{
		Success: true,
		Data:    data,
	})
}

// okWithMeta sends a 200 success response with data and pagination metadata.
func okWithMeta(c echo.Context, data interface{}, meta *Meta) error {
	return c.JSON(http.StatusOK, APIResponse{
		Success: true,
		Data:    data,
		Meta:    meta,
	})
}

// created sends a 201 success response with data.
func created(c echo.Context, data interface{}) error {
	return c.JSON(http.StatusCreated, APIResponse{
		Success: true,
		Data:    data,
	})
}

// noContent sends a 204 response with no body.
func noContent(c echo.Context) error {
	return c.NoContent(http.StatusNoContent)
}

// badRequest sends a 400 error response.
func badRequest(c echo.Context, code, message string) error {
	return c.JSON(http.StatusBadRequest, APIResponse{
		Success: false,
		Error:   &APIError{Code: code, Message: message},
	})
}

// unauthorized sends a 401 error response.
func unauthorized(c echo.Context, message string) error {
	return c.JSON(http.StatusUnauthorized, APIResponse{
		Success: false,
		Error:   &APIError{Code: "UNAUTHORIZED", Message: message},
	})
}

// forbidden sends a 403 error response.
func forbidden(c echo.Context, message string) error {
	return c.JSON(http.StatusForbidden, APIResponse{
		Success: false,
		Error:   &APIError{Code: "FORBIDDEN", Message: message},
	})
}

// notFound sends a 404 error response.
func notFound(c echo.Context, message string) error {
	return c.JSON(http.StatusNotFound, APIResponse{
		Success: false,
		Error:   &APIError{Code: "NOT_FOUND", Message: message},
	})
}

// conflict sends a 409 error response.
func conflict(c echo.Context, message string) error {
	return c.JSON(http.StatusConflict, APIResponse{
		Success: false,
		Error:   &APIError{Code: "CONFLICT", Message: message},
	})
}

// serviceUnavailable sends a 503 error response.
func serviceUnavailable(c echo.Context, message string) error {
	return c.JSON(http.StatusServiceUnavailable, APIResponse{
		Success: false,
		Error:   &APIError{Code: "SERVICE_UNAVAILABLE", Message: message},
	})
}

// gatewayTimeout sends a 504 error response.
func gatewayTimeout(c echo.Context, message string) error {
	return c.JSON(http.StatusGatewayTimeout, APIResponse{
		Success: false,
		Error:   &APIError{Code: "GATEWAY_TIMEOUT", Message: message},
	})
}

// internalError sends a 500 error response.
func internalError(c echo.Context, message string) error {
	return c.JSON(http.StatusInternalServerError, APIResponse{
		Success: false,
		Error:   &APIError{Code: "INTERNAL_ERROR", Message: message},
	})
}
