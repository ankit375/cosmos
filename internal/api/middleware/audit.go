package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	pgstore "github.com/yourorg/cloudctrl/internal/store/postgres"
	"go.uber.org/zap"
)

// AuditContextKey is the key for storing extra audit details in context.
const AuditContextKey = "audit_details"

// SetAuditDetails allows handlers to add extra details to the audit log entry.
func SetAuditDetails(c *gin.Context, details interface{}) {
	c.Set(AuditContextKey, details)
}

// Audit returns middleware that automatically logs all mutation (POST/PUT/PATCH/DELETE)
// requests to the audit_log table.
func Audit(pgStore *pgstore.Store, logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Only audit mutations
		method := c.Request.Method
		if method != http.MethodPost && method != http.MethodPut &&
			method != http.MethodPatch && method != http.MethodDelete {
			c.Next()
			return
		}

		// Skip non-API paths
		path := c.Request.URL.Path
		if !strings.HasPrefix(path, "/api/v1/") {
			c.Next()
			return
		}

		// Skip auth endpoints (login/refresh/logout are noisy)
		if strings.HasPrefix(path, "/api/v1/auth/") {
			c.Next()
			return
		}

		// Capture request body before handler consumes it
		var requestBody json.RawMessage
		if c.Request.Body != nil {
			bodyBytes, err := io.ReadAll(c.Request.Body)
			if err == nil && len(bodyBytes) > 0 {
				c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
				requestBody = sanitizeAuditBody(bodyBytes)
			}
		}

		// ── Execute handler ──────────────────────────────────
		c.Next()

		// ── Capture ALL values from gin.Context SYNCHRONOUSLY ──
		// (gin.Context is NOT safe to access from a goroutine after return)
		tenantID := GetTenantID(c)
		userID := GetUserID(c)
		statusCode := c.Writer.Status()
		clientIP := c.ClientIP()

		if tenantID == uuid.Nil {
			return // No tenant context = skip
		}

		action := resolveAction(method, path)
		resourceType, resourceID := resolveResource(path)

		// Build details map
		details := map[string]interface{}{
			"method":      method,
			"path":        path,
			"status_code": statusCode,
		}
		if requestBody != nil {
			details["request_body"] = json.RawMessage(requestBody)
		}

		// Get handler-provided extra details
		if extra, exists := c.Get(AuditContextKey); exists {
			details["extra"] = extra
		}

		// Prepare pointer values for store
		var userIDPtr *uuid.UUID
		if userID != uuid.Nil {
			uid := userID
			userIDPtr = &uid
		}
		var resourceIDPtr *uuid.UUID
		if resourceID != uuid.Nil {
			rid := resourceID
			resourceIDPtr = &rid
		}
		ip := clientIP

		// ── Fire async DB write with fresh context ───────────
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			if err := pgStore.Audit.LogAction(
				ctx, tenantID, userIDPtr, action, resourceType,
				resourceIDPtr, details, &ip,
			); err != nil {
				logger.Error("failed to write audit log",
					zap.Error(err),
					zap.String("action", action),
					zap.String("resource_type", resourceType),
					zap.String("path", path),
				)
			}
		}()
	}
}

// resolveAction maps HTTP method to an audit action string.
func resolveAction(method, path string) string {
	// Check for special sub-resource actions
	if strings.HasSuffix(path, "/password") {
		return "change_password"
	}
	if strings.HasSuffix(path, "/api-key") {
		return "generate_api_key"
	}
	if strings.HasSuffix(path, "/stats") {
		return "get_stats"
	}

	// Config-specific actions (NEW)
	if strings.Contains(path, "/config/rollback") {
		return "config_rollback"
	}
	if strings.Contains(path, "/config/validate") {
		return "config_validate"
	}
	if strings.Contains(path, "/config/push") {
		return "config_force_push"
	}
	if strings.Contains(path, "/config/overrides") {
		switch method {
		case http.MethodPut:
			return "config_override_update"
		case http.MethodDelete:
			return "config_override_delete"
		}
	}
	if strings.Contains(path, "/config") && method == http.MethodPut {
		return "config_update"
	}

	switch method {
	case http.MethodPost:
		return "create"
	case http.MethodPut, http.MethodPatch:
		return "update"
	case http.MethodDelete:
		return "delete"
	default:
		return strings.ToLower(method)
	}
}

// resolveResource extracts resource type and ID from the URL path.
func resolveResource(path string) (string, uuid.UUID) {
	trimmed := strings.TrimPrefix(path, "/api/v1/")
	parts := strings.Split(trimmed, "/")

	if len(parts) == 0 {
		return "unknown", uuid.Nil
	}

	resourceType := singularize(parts[0])

	if len(parts) >= 3 && parts[2] == "config" {
		resourceType = singularize(parts[0]) + "_config"
	}

	var resourceID uuid.UUID
	if len(parts) >= 2 {
		if id, err := uuid.Parse(parts[1]); err == nil {
			resourceID = id
		}
	}

	return resourceType, resourceID
}

// singularize converts plural resource names to singular.
func singularize(s string) string {
	mapping := map[string]string{
		"tenants": "tenant",
		"sites":   "site",
		"users":   "user",
		"devices": "device",
		"audit":   "audit",
		"config":   "config",    
		"firmware": "firmware",
	}
	if singular, ok := mapping[s]; ok {
		return singular
	}
	if strings.HasSuffix(s, "s") {
		return s[:len(s)-1]
	}
	return s
}

// sanitizeAuditBody removes sensitive fields before logging.
func sanitizeAuditBody(body []byte) json.RawMessage {
	var data map[string]interface{}
	if err := json.Unmarshal(body, &data); err != nil {
		return nil
	}

	sensitiveFields := []string{
		"password", "new_password", "old_password",
		"password_hash", "api_key", "api_key_hash",
		"refresh_token", "access_token", "token",
		"secret", "jwt_secret",
	}

	for _, field := range sensitiveFields {
		if _, exists := data[field]; exists {
			data[field] = "[REDACTED]"
		}
	}

	sanitized, err := json.Marshal(data)
	if err != nil {
		return nil
	}
	return sanitized
}
