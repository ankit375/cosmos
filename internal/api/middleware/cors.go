package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// CORS adds Cross-Origin Resource Sharing headers.
func CORS(allowAll bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		if allowAll {
			c.Header("Access-Control-Allow-Origin", "*")
		} else {
			origin := c.GetHeader("Origin")
			// In production, validate origin against allowed list
			c.Header("Access-Control-Allow-Origin", origin)
		}

		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Origin, Content-Type, Accept, Authorization, X-Request-ID")
		c.Header("Access-Control-Expose-Headers", "X-Request-ID")
		c.Header("Access-Control-Max-Age", "86400")
		c.Header("Access-Control-Allow-Credentials", "true")

		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}