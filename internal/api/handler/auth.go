package handler

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/yourorg/cloudctrl/internal/api/middleware"
	"github.com/yourorg/cloudctrl/internal/auth"
	"github.com/yourorg/cloudctrl/internal/model"
	"github.com/yourorg/cloudctrl/pkg/crypto"
	pgstore "github.com/yourorg/cloudctrl/internal/store/postgres"
	redisstore "github.com/yourorg/cloudctrl/internal/store/redis"
	"go.uber.org/zap"
)

// AuthHandler handles authentication endpoints.
type AuthHandler struct {
	pg     *pgstore.Store
	redis  *redisstore.Store
	jwt    *auth.JWTService
	logger *zap.Logger
}

// NewAuthHandler creates a new AuthHandler.
func NewAuthHandler(
	pg *pgstore.Store,
	redis *redisstore.Store,
	jwt *auth.JWTService,
	logger *zap.Logger,
) *AuthHandler {
	return &AuthHandler{
		pg:     pg,
		redis:  redis,
		jwt:    jwt,
		logger: logger,
	}
}

// Login handles POST /api/v1/auth/login
func (h *AuthHandler) Login(c *gin.Context) {
	var input model.LoginInput
	if !BindAndValidate(c, &input) {
		return
	}

	ctx := c.Request.Context()

	user, err := h.pg.Users.GetByEmailGlobal(ctx, input.Email)
	if err != nil {
		h.logger.Error("login: database error", zap.Error(err))
		RespondInternalError(c, h.logger)
		return
	}
	if user == nil {
		crypto.VerifyPassword(input.Password, "$2a$10$dummyhashfortimingattackpreventiononly")
		RespondUnauthorized(c, "Invalid email or password")
		return
	}

	if !user.Active {
		RespondUnauthorized(c, "Account is disabled")
		return
	}

	if !crypto.VerifyPassword(input.Password, user.PasswordHash) {
		RespondUnauthorized(c, "Invalid email or password")
		return
	}

	accessToken, err := h.jwt.GenerateAccessToken(user)
	if err != nil {
		h.logger.Error("login: generate access token", zap.Error(err))
		RespondInternalError(c, h.logger)
		return
	}

	refreshToken, err := h.jwt.GenerateRefreshToken(user.ID)
	if err != nil {
		h.logger.Error("login: generate refresh token", zap.Error(err))
		RespondInternalError(c, h.logger)
		return
	}

	refreshClaims, err := h.jwt.ValidateRefreshToken(refreshToken)
	if err != nil {
		h.logger.Error("login: parse refresh token", zap.Error(err))
		RespondInternalError(c, h.logger)
		return
	}

	if err := h.redis.StoreRefreshToken(ctx, refreshClaims.ID, user.ID.String(), h.jwt.RefreshExpiry()); err != nil {
		h.logger.Error("login: store refresh token", zap.Error(err))
		RespondInternalError(c, h.logger)
		return
	}

	if err := h.pg.Users.UpdateLastLogin(ctx, user.ID); err != nil {
		h.logger.Warn("login: update last login", zap.Error(err))
	}

	h.logger.Info("user logged in",
		zap.String("user_id", user.ID.String()),
		zap.String("email", user.Email),
		zap.String("tenant_id", user.TenantID.String()),
	)

	RespondOK(c, model.LoginResponse{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresIn:    h.jwt.AccessExpiry(),
		User:         user,
	})
}

// Refresh handles POST /api/v1/auth/refresh
func (h *AuthHandler) Refresh(c *gin.Context) {
	var input model.RefreshInput
	if !BindAndValidate(c, &input) {
		return
	}

	ctx := c.Request.Context()

	claims, err := h.jwt.ValidateRefreshToken(input.RefreshToken)
	if err != nil {
		RespondUnauthorized(c, "Invalid or expired refresh token")
		return
	}

	storedUserID, err := h.redis.GetRefreshToken(ctx, claims.ID)
	if err != nil {
		h.logger.Error("refresh: check redis", zap.Error(err))
		RespondInternalError(c, h.logger)
		return
	}
	if storedUserID == "" {
		RespondUnauthorized(c, "Refresh token has been revoked")
		return
	}

	if storedUserID != claims.Subject {
		h.logger.Warn("refresh: user ID mismatch",
			zap.String("stored", storedUserID),
			zap.String("claimed", claims.Subject),
		)
		RespondUnauthorized(c, "Invalid refresh token")
		return
	}

	userID, err := uuid.Parse(claims.Subject)
	if err != nil {
		RespondUnauthorized(c, "Invalid refresh token")
		return
	}

	user, err := h.pg.Users.GetByIDGlobal(ctx, userID)
	if err != nil {
		h.logger.Error("refresh: lookup user", zap.Error(err))
		RespondInternalError(c, h.logger)
		return
	}
	if user == nil || !user.Active {
		_ = h.redis.RevokeRefreshToken(ctx, claims.ID)
		RespondUnauthorized(c, "Account not found or disabled")
		return
	}

	// Token rotation: revoke old, issue new
	_ = h.redis.RevokeRefreshToken(ctx, claims.ID)

	accessToken, err := h.jwt.GenerateAccessToken(user)
	if err != nil {
		h.logger.Error("refresh: generate access token", zap.Error(err))
		RespondInternalError(c, h.logger)
		return
	}

	newRefreshToken, err := h.jwt.GenerateRefreshToken(user.ID)
	if err != nil {
		h.logger.Error("refresh: generate refresh token", zap.Error(err))
		RespondInternalError(c, h.logger)
		return
	}

	newRefreshClaims, _ := h.jwt.ValidateRefreshToken(newRefreshToken)
	if err := h.redis.StoreRefreshToken(ctx, newRefreshClaims.ID, user.ID.String(), h.jwt.RefreshExpiry()); err != nil {
		h.logger.Error("refresh: store new refresh token", zap.Error(err))
		RespondInternalError(c, h.logger)
		return
	}

	RespondOK(c, model.LoginResponse{
		AccessToken:  accessToken,
		RefreshToken: newRefreshToken,
		ExpiresIn:    h.jwt.AccessExpiry(),
		User:         user,
	})
}

// Logout handles POST /api/v1/auth/logout
func (h *AuthHandler) Logout(c *gin.Context) {
	ctx := c.Request.Context()

	// Blacklist current access token
	if tokenStr, exists := c.Get("access_token"); exists {
		if token, ok := tokenStr.(string); ok {
			if accessClaims, err := h.jwt.ValidateAccessToken(token); err == nil {
				ttl := time.Until(accessClaims.ExpiresAt.Time)
				if ttl > 0 {
					if err := h.redis.BlacklistToken(ctx, token, ttl); err != nil {
						h.logger.Error("logout: blacklist token", zap.Error(err))
					}
				}
			}
		}
	}

	// Revoke refresh token if provided
	var input struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := c.ShouldBindJSON(&input); err == nil && input.RefreshToken != "" {
		if refreshClaims, err := h.jwt.ValidateRefreshToken(input.RefreshToken); err == nil {
			_ = h.redis.RevokeRefreshToken(ctx, refreshClaims.ID)
		}
	}

	h.logger.Info("user logged out",
		zap.String("user_id", middleware.GetUserID(c).String()),
	)

	RespondOK(c, gin.H{"message": "Successfully logged out"})
}

// Me handles GET /api/v1/auth/me
func (h *AuthHandler) Me(c *gin.Context) {
	claims := middleware.GetAuthClaims(c)
	if claims == nil {
		RespondUnauthorized(c, "Not authenticated")
		return
	}

	ctx := c.Request.Context()

	user, err := h.pg.Users.GetByID(ctx, claims.TenantID, claims.UserID)
	if err != nil {
		h.logger.Error("me: lookup user", zap.Error(err))
		RespondInternalError(c, h.logger)
		return
	}
	if user == nil {
		RespondNotFound(c, "User")
		return
	}

	RespondOK(c, user)
}
