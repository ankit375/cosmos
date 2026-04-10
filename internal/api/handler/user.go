package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/yourorg/cloudctrl/internal/api/middleware"
	"github.com/yourorg/cloudctrl/internal/model"
	"github.com/yourorg/cloudctrl/pkg/crypto"
	pgstore "github.com/yourorg/cloudctrl/internal/store/postgres"
	redisstore "github.com/yourorg/cloudctrl/internal/store/redis"
	"go.uber.org/zap"
)

// UserHandler handles user CRUD endpoints.
type UserHandler struct {
	pg     *pgstore.Store
	redis  *redisstore.Store
	logger *zap.Logger
}

// NewUserHandler creates a new UserHandler.
func NewUserHandler(pg *pgstore.Store, redis *redisstore.Store, logger *zap.Logger) *UserHandler {
	return &UserHandler{
		pg:     pg,
		redis:  redis,
		logger: logger,
	}
}

// List handles GET /api/v1/users
func (h *UserHandler) List(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	ctx := c.Request.Context()

	users, err := h.pg.Users.List(ctx, tenantID)
	if err != nil {
		h.logger.Error("list users: database error", zap.Error(err))
		RespondInternalError(c, h.logger)
		return
	}

	RespondList(c, users, len(users), 0, len(users))
}

// Get handles GET /api/v1/users/:id
func (h *UserHandler) Get(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	ctx := c.Request.Context()

	userID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		RespondError(c, 400, "INVALID_ID", "Invalid user ID format")
		return
	}

	user, err := h.pg.Users.GetByID(ctx, tenantID, userID)
	if err != nil {
		h.logger.Error("get user: database error", zap.Error(err))
		RespondInternalError(c, h.logger)
		return
	}
	if user == nil {
		RespondNotFound(c, "User")
		return
	}

	RespondOK(c, user)
}

// Create handles POST /api/v1/users
func (h *UserHandler) Create(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	ctx := c.Request.Context()

	var input model.CreateUserInput
	if !BindAndValidate(c, &input) {
		return
	}

	// Validate role
	if !input.Role.IsValid() {
		RespondValidationError(c, "Invalid role. Must be admin, operator, or viewer")
		return
	}

	// Check if email already exists in tenant
	existing, err := h.pg.Users.GetByEmail(ctx, tenantID, input.Email)
	if err != nil {
		h.logger.Error("create user: check existing email", zap.Error(err))
		RespondInternalError(c, h.logger)
		return
	}
	if existing != nil {
		RespondConflict(c, "A user with this email already exists")
		return
	}

	// Hash password
	passwordHash, err := crypto.HashPassword(input.Password)
	if err != nil {
		h.logger.Error("create user: hash password", zap.Error(err))
		RespondInternalError(c, h.logger)
		return
	}

	user := &model.User{
		ID:           uuid.New(),
		TenantID:     tenantID,
		Email:        input.Email,
		PasswordHash: passwordHash,
		Name:         input.Name,
		Role:         input.Role,
		Active:       true,
	}

	if err := h.pg.Users.Create(ctx, user); err != nil {
		h.logger.Error("create user: database error", zap.Error(err))
		RespondInternalError(c, h.logger)
		return
	}

	h.logger.Info("user created",
		zap.String("user_id", user.ID.String()),
		zap.String("email", user.Email),
		zap.String("tenant_id", tenantID.String()),
		zap.String("created_by", middleware.GetUserID(c).String()),
	)

	RespondCreated(c, user)
}

// Update handles PUT /api/v1/users/:id
func (h *UserHandler) Update(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	ctx := c.Request.Context()

	userID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		RespondError(c, 400, "INVALID_ID", "Invalid user ID format")
		return
	}

	var input model.UpdateUserInput
	if !BindAndValidate(c, &input) {
		return
	}

	// Validate role if provided
	if input.Role != nil && !input.Role.IsValid() {
		RespondValidationError(c, "Invalid role. Must be admin, operator, or viewer")
		return
	}

	// Check if user exists
	existing, err := h.pg.Users.GetByID(ctx, tenantID, userID)
	if err != nil {
		h.logger.Error("update user: lookup", zap.Error(err))
		RespondInternalError(c, h.logger)
		return
	}
	if existing == nil {
		RespondNotFound(c, "User")
		return
	}

	// Prevent self-demotion from admin
	currentUserID := middleware.GetUserID(c)
	if currentUserID == userID && input.Role != nil && *input.Role != model.RoleAdmin {
		currentRole := middleware.GetUserRole(c)
		if currentRole == model.RoleAdmin {
			RespondError(c, 400, "SELF_DEMOTION", "Cannot demote your own admin account")
			return
		}
	}

	// Prevent self-deactivation
	if currentUserID == userID && input.Active != nil && !*input.Active {
		RespondError(c, 400, "SELF_DEACTIVATION", "Cannot deactivate your own account")
		return
	}

	// If email is changing, check uniqueness
	if input.Email != nil && *input.Email != existing.Email {
		dup, err := h.pg.Users.GetByEmail(ctx, tenantID, *input.Email)
		if err != nil {
			h.logger.Error("update user: check duplicate email", zap.Error(err))
			RespondInternalError(c, h.logger)
			return
		}
		if dup != nil {
			RespondConflict(c, "A user with this email already exists")
			return
		}
	}

	if err := h.pg.Users.Update(ctx, tenantID, userID, &input); err != nil {
		h.logger.Error("update user: database error", zap.Error(err))
		RespondInternalError(c, h.logger)
		return
	}

	// If user was deactivated, revoke their refresh tokens
	if input.Active != nil && !*input.Active {
		_ = h.redis.RevokeAllUserRefreshTokens(ctx, userID.String())
	}

	// Fetch updated user
	updated, err := h.pg.Users.GetByID(ctx, tenantID, userID)
	if err != nil {
		h.logger.Error("update user: fetch updated", zap.Error(err))
		RespondInternalError(c, h.logger)
		return
	}

	h.logger.Info("user updated",
		zap.String("user_id", userID.String()),
		zap.String("updated_by", currentUserID.String()),
	)

	RespondOK(c, updated)
}

// Delete handles DELETE /api/v1/users/:id
func (h *UserHandler) Delete(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	ctx := c.Request.Context()

	userID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		RespondError(c, 400, "INVALID_ID", "Invalid user ID format")
		return
	}

	// Prevent self-deletion
	if middleware.GetUserID(c) == userID {
		RespondError(c, 400, "SELF_DELETION", "Cannot delete your own account")
		return
	}

	// Check user exists
	existing, err := h.pg.Users.GetByID(ctx, tenantID, userID)
	if err != nil {
		h.logger.Error("delete user: lookup", zap.Error(err))
		RespondInternalError(c, h.logger)
		return
	}
	if existing == nil {
		RespondNotFound(c, "User")
		return
	}

	if err := h.pg.Users.Delete(ctx, tenantID, userID); err != nil {
		h.logger.Error("delete user: database error", zap.Error(err))
		RespondInternalError(c, h.logger)
		return
	}

	// Revoke all their tokens
	_ = h.redis.RevokeAllUserRefreshTokens(ctx, userID.String())

	h.logger.Info("user deleted",
		zap.String("user_id", userID.String()),
		zap.String("deleted_by", middleware.GetUserID(c).String()),
	)

	RespondOK(c, gin.H{"message": "User deleted"})
}

// ChangePassword handles PUT /api/v1/users/:id/password
func (h *UserHandler) ChangePassword(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	currentUserID := middleware.GetUserID(c)
	ctx := c.Request.Context()

	userID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		RespondError(c, 400, "INVALID_ID", "Invalid user ID format")
		return
	}

	var input model.ChangePasswordInput
	if !BindAndValidate(c, &input) {
		return
	}

	// Non-admin users can only change their own password
	currentRole := middleware.GetUserRole(c)
	if currentUserID != userID && currentRole != model.RoleAdmin {
		RespondForbidden(c, "Can only change your own password")
		return
	}

	user, err := h.pg.Users.GetByID(ctx, tenantID, userID)
	if err != nil {
		h.logger.Error("change password: lookup user", zap.Error(err))
		RespondInternalError(c, h.logger)
		return
	}
	if user == nil {
		RespondNotFound(c, "User")
		return
	}

	// Verify old password (always required, even for admins changing others)
	if currentUserID == userID {
		if !crypto.VerifyPassword(input.OldPassword, user.PasswordHash) {
			RespondError(c, 400, "WRONG_PASSWORD", "Current password is incorrect")
			return
		}
	}

	newHash, err := crypto.HashPassword(input.NewPassword)
		if err != nil {
		h.logger.Error("change password: hash", zap.Error(err))
		RespondInternalError(c, h.logger)
		return
	}

	if err := h.pg.Users.UpdatePassword(ctx, tenantID, userID, newHash); err != nil {
		h.logger.Error("change password: update", zap.Error(err))
		RespondInternalError(c, h.logger)
		return
	}

	// Revoke all refresh tokens for this user (force re-login)
	_ = h.redis.RevokeAllUserRefreshTokens(ctx, userID.String())

	h.logger.Info("password changed",
		zap.String("user_id", userID.String()),
		zap.String("changed_by", currentUserID.String()),
	)

	RespondOK(c, gin.H{"message": "Password changed successfully"})
}

// GenerateAPIKey handles POST /api/v1/users/:id/api-key
func (h *UserHandler) GenerateAPIKey(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	ctx := c.Request.Context()

	userID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		RespondError(c, 400, "INVALID_ID", "Invalid user ID format")
		return
	}

	// Check user exists
	user, err := h.pg.Users.GetByID(ctx, tenantID, userID)
	if err != nil {
		h.logger.Error("generate api key: lookup user", zap.Error(err))
		RespondInternalError(c, h.logger)
		return
	}
	if user == nil {
		RespondNotFound(c, "User")
		return
	}

	// Generate a new API key
	apiKey, err := crypto.GenerateToken(32)
	if err != nil {
		h.logger.Error("generate api key: generate token", zap.Error(err))
		RespondInternalError(c, h.logger)
		return
	}

	// Store the hash
	apiKeyHash := crypto.HashToken(apiKey)
	if err := h.pg.Users.UpdateAPIKey(ctx, tenantID, userID, apiKeyHash); err != nil {
		h.logger.Error("generate api key: store hash", zap.Error(err))
		RespondInternalError(c, h.logger)
		return
	}

	h.logger.Info("api key generated",
		zap.String("user_id", userID.String()),
		zap.String("generated_by", middleware.GetUserID(c).String()),
	)

	// Return the plaintext key — this is the only time it's visible
	RespondCreated(c, gin.H{
		"api_key": apiKey,
		"message": "Store this key securely. It cannot be retrieved again.",
	})
}
