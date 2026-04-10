package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/yourorg/cloudctrl/internal/api/response"
	pgstore "github.com/yourorg/cloudctrl/internal/store/postgres"
)

// Re-export types
type APIResponse = response.APIResponse
type APIError = response.APIError
type APIMeta = response.APIMeta

// Re-export functions — handlers keep calling them the same way
var (
	RespondOK              = response.RespondOK
	RespondCreated         = response.RespondCreated
	RespondList            = response.RespondList
	RespondError           = response.RespondError
	RespondValidationError = response.RespondValidationError
	RespondNotFound        = response.RespondNotFound
	RespondUnauthorized    = response.RespondUnauthorized
	RespondForbidden       = response.RespondForbidden
	RespondConflict        = response.RespondConflict
	RespondRateLimit       = response.RespondRateLimit
	RespondBadRequest      = response.RespondBadRequest
)

// RespondInternalError keeps your original signature
func RespondInternalError(c *gin.Context, _ ...interface{}) {
	response.RespondError(c, 500, "INTERNAL_ERROR", "An internal error occurred")
}

// IsUniqueViolation wraps the postgres helper for use in handlers.
func IsUniqueViolation(err error) bool {
	return pgstore.IsUniqueViolation(err)
}

// IsForeignKeyViolation wraps the postgres helper for use in handlers.
func IsForeignKeyViolation(err error) bool {
	return pgstore.IsForeignKeyViolation(err)
}
