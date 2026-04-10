package model

import (
	"time"

	"github.com/google/uuid"
)

type UserRole string

const (
	RoleSuperAdmin UserRole = "super_admin"
	RoleAdmin    UserRole = "admin"
	RoleOperator UserRole = "operator"
	RoleViewer   UserRole = "viewer"
)

func (r UserRole) IsValid() bool {
	switch r {
	case RoleSuperAdmin, RoleAdmin, RoleOperator, RoleViewer: // ← CHANGE: add RoleSuperAdmin
		return true
	}
	return false
}

// HasPermission checks if role has at least the given permission level.
func (r UserRole) HasPermission(required UserRole) bool {
	hierarchy := map[UserRole]int{
		RoleSuperAdmin: 4, // ← ADD
		RoleAdmin:    3,
		RoleOperator: 2,
		RoleViewer:   1,
	}
	return hierarchy[r] >= hierarchy[required]
}

// IsSuperAdmin returns true if the role is super_admin.
func (r UserRole) IsSuperAdmin() bool { // ← ADD entire method
	return r == RoleSuperAdmin
}

type User struct {
	ID           uuid.UUID  `json:"id" db:"id"`
	TenantID     uuid.UUID  `json:"tenant_id" db:"tenant_id"`
	Email        string     `json:"email" db:"email"`
	PasswordHash string     `json:"-" db:"password_hash"`
	Name         string     `json:"name" db:"name"`
	Role         UserRole   `json:"role" db:"role"`
	Active       bool       `json:"active" db:"active"`
	APIKeyHash   *string    `json:"-" db:"api_key_hash"`
	LastLoginAt  *time.Time `json:"last_login_at" db:"last_login_at"`
	CreatedAt    time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at" db:"updated_at"`
}

type CreateUserInput struct {
	Email    string   `json:"email" binding:"required,email,max=255"`
	Password string   `json:"password" binding:"required,min=8,max=128"`
	Name     string   `json:"name" binding:"required,min=1,max=255"`
	Role     UserRole `json:"role" binding:"required,oneof=super_admin admin operator viewer"` 
}

type UpdateUserInput struct {
	Email  *string   `json:"email" binding:"omitempty,email,max=255"`
	Name   *string   `json:"name" binding:"omitempty,min=1,max=255"`
	Role   *UserRole `json:"role" binding:"omitempty,oneof=super_admin admin operator viewer"` 
	Active *bool     `json:"active"`
}

type ChangePasswordInput struct {
	OldPassword string `json:"old_password" binding:"required"`
	NewPassword string `json:"new_password" binding:"required,min=8,max=128"`
}

type LoginInput struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required"`
}

type LoginResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	User         *User  `json:"user"`
}

type RefreshInput struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

// ── Context keys for auth middleware ──────────────────────────

type contextKey string

const (
	ContextKeyUserID   contextKey = "user_id"
	ContextKeyTenantID contextKey = "tenant_id"
	ContextKeyRole     contextKey = "user_role"
	ContextKeyEmail    contextKey = "user_email"
)

// AuthClaims is a lightweight struct passed through gin.Context for handlers.
type AuthClaims struct {
	UserID   uuid.UUID `json:"user_id"`
	TenantID uuid.UUID `json:"tenant_id"`
	Email    string    `json:"email"`
	Role     UserRole  `json:"role"`
}
