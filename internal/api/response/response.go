package response

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// APIResponse is the standard API response envelope.
type APIResponse struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   *APIError   `json:"error,omitempty"`
	Meta    *APIMeta    `json:"meta,omitempty"`
}

// APIError represents an error in the API response.
type APIError struct {
	Code    string      `json:"code"`
	Message string      `json:"message"`
	Details interface{} `json:"details,omitempty"`
}

// APIMeta contains pagination information.
type APIMeta struct {
	Total  int `json:"total"`
	Offset int `json:"offset"`
	Limit  int `json:"limit"`
}

// RespondOK sends a success response with data.
func RespondOK(c *gin.Context, data interface{}) {
	c.JSON(http.StatusOK, APIResponse{
		Success: true,
		Data:    data,
	})
}

// RespondCreated sends a 201 response with data.
func RespondCreated(c *gin.Context, data interface{}) {
	c.JSON(http.StatusCreated, APIResponse{
		Success: true,
		Data:    data,
	})
}

// RespondList sends a paginated list response.
func RespondList(c *gin.Context, data interface{}, total, offset, limit int) {
	c.JSON(http.StatusOK, APIResponse{
		Success: true,
		Data:    data,
		Meta: &APIMeta{
			Total:  total,
			Offset: offset,
			Limit:  limit,
		},
	})
}

// RespondError sends an error response.
func RespondError(c *gin.Context, status int, code, message string) {
	c.JSON(status, APIResponse{
		Success: false,
		Error: &APIError{
			Code:    code,
			Message: message,
		},
	})
}

// RespondValidationError sends a 422 response with validation details.
func RespondValidationError(c *gin.Context, details interface{}) {
	c.JSON(http.StatusUnprocessableEntity, APIResponse{
		Success: false,
		Error: &APIError{
			Code:    "VALIDATION_ERROR",
			Message: "Request validation failed",
			Details: details,
		},
	})
}

// Common error helpers

func RespondNotFound(c *gin.Context, resource string) {
	RespondError(c, http.StatusNotFound, "RESOURCE_NOT_FOUND",
		resource+" not found")
}

func RespondUnauthorized(c *gin.Context, message string) {
	RespondError(c, http.StatusUnauthorized, "AUTH_UNAUTHORIZED", message)
}

func RespondForbidden(c *gin.Context, message string) {
	RespondError(c, http.StatusForbidden, "AUTH_FORBIDDEN", message)
}

func RespondInternalError(c *gin.Context) {
	RespondError(c, http.StatusInternalServerError, "INTERNAL_ERROR",
		"An internal error occurred")
}

func RespondConflict(c *gin.Context, message string) {
	RespondError(c, http.StatusConflict, "RESOURCE_ALREADY_EXISTS", message)
}

func RespondRateLimit(c *gin.Context) {
	RespondError(c, http.StatusTooManyRequests, "RATE_LIMIT_EXCEEDED",
		"Too many requests, please try again later")
}

func RespondBadRequest(c *gin.Context, code, message string) {
	RespondError(c, http.StatusBadRequest, code, message)
}
