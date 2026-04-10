package middleware

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/yourorg/cloudctrl/internal/api/response"
	"github.com/yourorg/cloudctrl/pkg/crypto"
	"github.com/yourorg/cloudctrl/internal/auth"
	"github.com/yourorg/cloudctrl/internal/model"
	pgstore "github.com/yourorg/cloudctrl/internal/store/postgres"
	redisstore "github.com/yourorg/cloudctrl/internal/store/redis"
	"go.uber.org/zap"
)

// Auth returns a middleware that validates JWT tokens and injects claims into context.
func Auth(jwtService *auth.JWTService, redisStore *redisstore.Store, logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Extract token from Authorization header
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			response.RespondUnauthorized(c, "Missing authorization header")
			c.Abort()
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
			response.RespondUnauthorized(c, "Invalid authorization header format")
			c.Abort()
			return
		}

		tokenString := parts[1]

		// Validate JWT
		claims, err := jwtService.ValidateAccessToken(tokenString)
		if err != nil {
			logger.Debug("invalid access token", zap.Error(err))
			response.RespondUnauthorized(c, "Invalid or expired token")
			c.Abort()
			return
		}

		// Check if token is blacklisted (logout)
		ctx := c.Request.Context()
		blacklisted, err := redisStore.IsTokenBlacklisted(ctx, tokenString)
		if err != nil {
			logger.Error("failed to check token blacklist", zap.Error(err))
			// Fail open — allow request but log error
			// In production you might want to fail closed instead
		}
		if blacklisted {
			response.RespondUnauthorized(c, "Token has been revoked")
			c.Abort()
			return
		}

		// Parse user ID from subject
		userID, err := uuid.Parse(claims.Subject)
		if err != nil {
			response.RespondUnauthorized(c, "Invalid token claims")
			c.Abort()
			return
		}

		// Inject claims into gin context
		authClaims := &model.AuthClaims{
			UserID:   userID,
			TenantID: claims.TenantID,
			Email:    claims.Email,
			Role:     claims.Role,
		}

		c.Set(string(model.ContextKeyUserID), userID)
		c.Set(string(model.ContextKeyTenantID), claims.TenantID)
		c.Set(string(model.ContextKeyRole), claims.Role)
		c.Set(string(model.ContextKeyEmail), claims.Email)
		c.Set("auth_claims", authClaims)
		c.Set("access_token", tokenString) // For logout blacklisting

		c.Next()
	}
}

// APIKeyAuth returns a middleware that validates API keys via X-API-Key header.
func APIKeyAuth(pgStore *pgstore.Store, logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		apiKey := c.GetHeader("X-API-Key")
		if apiKey == "" {
			response.RespondUnauthorized(c, "Missing API key")
			c.Abort()
			return
		}

		apiKeyHash := crypto.HashToken(apiKey)

		ctx := c.Request.Context()
		user, err := pgStore.Users.GetByAPIKey(ctx, apiKeyHash)
		if err != nil {
			logger.Error("api key auth: database error", zap.Error(err))
			response.RespondInternalError(c)
			c.Abort()
			return
		}
		if user == nil {
			response.RespondUnauthorized(c, "Invalid API key")
			c.Abort()
			return
		}

		if !user.Active {
			response.RespondUnauthorized(c, "Account is disabled")
			c.Abort()
			return
		}

		authClaims := &model.AuthClaims{
			UserID:   user.ID,
			TenantID: user.TenantID,
			Email:    user.Email,
			Role:     user.Role,
		}

		c.Set(string(model.ContextKeyUserID), user.ID)
		c.Set(string(model.ContextKeyTenantID), user.TenantID)
		c.Set(string(model.ContextKeyRole), user.Role)
		c.Set(string(model.ContextKeyEmail), user.Email)
		c.Set("auth_claims", authClaims)
		c.Set("auth_method", "api_key")

		c.Next()
	}
}

// AuthOrAPIKey returns middleware that accepts either JWT Bearer token or X-API-Key.
func AuthOrAPIKey(jwtService *auth.JWTService, pgStore *pgstore.Store, redisStore *redisstore.Store, logger *zap.Logger) gin.HandlerFunc {
	jwtAuth := Auth(jwtService, redisStore, logger)
	apiKeyAuth := APIKeyAuth(pgStore, logger)

	return func(c *gin.Context) {
		if c.GetHeader("X-API-Key") != "" {
			apiKeyAuth(c)
			return
		}

		if c.GetHeader("Authorization") != "" {
			jwtAuth(c)
			return
		}

		response.RespondUnauthorized(c, "Missing authentication. Provide Authorization or X-API-Key header")
		c.Abort()
	}
}

// GetAuthClaims extracts auth claims from gin context.
func GetAuthClaims(c *gin.Context) *model.AuthClaims {
	val, exists := c.Get("auth_claims")
	if !exists {
		return nil
	}
	claims, ok := val.(*model.AuthClaims)
	if !ok {
		return nil
	}
	return claims
}

// GetTenantID extracts tenant ID from gin context.
func GetTenantID(c *gin.Context) uuid.UUID {
	val, exists := c.Get(string(model.ContextKeyTenantID))
	if !exists {
		return uuid.Nil
	}
	id, ok := val.(uuid.UUID)
	if !ok {
		return uuid.Nil
	}
	return id
}

// GetUserID extracts user ID from gin context.
func GetUserID(c *gin.Context) uuid.UUID {
	val, exists := c.Get(string(model.ContextKeyUserID))
	if !exists {
		return uuid.Nil
	}
	id, ok := val.(uuid.UUID)
	if !ok {
		return uuid.Nil
	}
	return id
}

// GetUserRole extracts user role from gin context.
func GetUserRole(c *gin.Context) model.UserRole {
	val, exists := c.Get(string(model.ContextKeyRole))
	if !exists {
		return ""
	}
	role, ok := val.(model.UserRole)
	if !ok {
		return ""
	}
	return role
}
