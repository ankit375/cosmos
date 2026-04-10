package middleware

import (
	"github.com/gin-gonic/gin"
	"github.com/yourorg/cloudctrl/internal/api/response"
	"github.com/yourorg/cloudctrl/internal/model"
)

// RequireRole returns middleware that checks if the authenticated user
// has at least the required role level.
func RequireRole(required model.UserRole) gin.HandlerFunc {
	return func(c *gin.Context) {
		role := GetUserRole(c)
		if role == "" {
			response.RespondUnauthorized(c, "Authentication required")
			c.Abort()
			return
		}

		if !role.HasPermission(required) {
			response.RespondForbidden(c, "Insufficient permissions")
			c.Abort()
			return
		}

		c.Next()
	}
}

// RequireSuperAdmin is middleware that requires super_admin role.
// Used for platform-level operations like tenant management.
func RequireSuperAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		role := GetUserRole(c)
		if role == "" {
			response.RespondUnauthorized(c, "Authentication required")
			c.Abort()
			return
		}

		if !role.IsSuperAdmin() {
			response.RespondForbidden(c, "Super admin access required")
			c.Abort()
			return
		}

		c.Next()
	}
}

// RequireAdmin is a convenience middleware that requires admin role.
func RequireAdmin() gin.HandlerFunc {
	return RequireRole(model.RoleAdmin)
}

// RequireOperator is a convenience middleware that requires at least operator role.
func RequireOperator() gin.HandlerFunc {
	return RequireRole(model.RoleOperator)
}

// RequireViewer is a convenience middleware that requires at least viewer role.
func RequireViewer() gin.HandlerFunc {
	return RequireRole(model.RoleViewer)
}
