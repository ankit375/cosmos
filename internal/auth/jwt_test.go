package auth

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yourorg/cloudctrl/internal/config"
	"github.com/yourorg/cloudctrl/internal/model"
)

func newTestJWTService() *JWTService {
	return NewJWTService(config.AuthConfig{
		JWTSecret:     "test-secret-key-that-is-at-least-32-chars-long!!",
		JWTExpiry:     15 * time.Minute,
		RefreshExpiry: 24 * time.Hour,
	})
}

func testUser() *model.User {
	return &model.User{
		ID:       uuid.New(),
		TenantID: uuid.New(),
		Email:    "admin@test.com",
		Name:     "Test Admin",
		Role:     model.RoleAdmin,
		Active:   true,
	}
}

func TestGenerateAndValidateAccessToken(t *testing.T) {
	svc := newTestJWTService()
	user := testUser()

	token, err := svc.GenerateAccessToken(user)
	require.NoError(t, err)
	assert.NotEmpty(t, token)

	claims, err := svc.ValidateAccessToken(token)
	require.NoError(t, err)
	assert.Equal(t, user.ID.String(), claims.Subject)
	assert.Equal(t, user.TenantID, claims.TenantID)
	assert.Equal(t, user.Email, claims.Email)
	assert.Equal(t, user.Role, claims.Role)
	assert.Equal(t, "cloudctrl", claims.Issuer)
}

func TestGenerateAndValidateRefreshToken(t *testing.T) {
	svc := newTestJWTService()
	userID := uuid.New()

	token, err := svc.GenerateRefreshToken(userID)
	require.NoError(t, err)
	assert.NotEmpty(t, token)

	claims, err := svc.ValidateRefreshToken(token)
	require.NoError(t, err)
	assert.Equal(t, userID.String(), claims.Subject)
	assert.NotEmpty(t, claims.ID)
}

func TestExpiredAccessToken(t *testing.T) {
	svc := NewJWTService(config.AuthConfig{
		JWTSecret:     "test-secret-key-that-is-at-least-32-chars-long!!",
		JWTExpiry:     -1 * time.Hour, // Already expired
		RefreshExpiry: 24 * time.Hour,
	})

	user := testUser()
	token, err := svc.GenerateAccessToken(user)
	require.NoError(t, err)

	_, err = svc.ValidateAccessToken(token)
	assert.Error(t, err)
}

func TestInvalidSignature(t *testing.T) {
	svc1 := newTestJWTService()
	svc2 := NewJWTService(config.AuthConfig{
		JWTSecret:     "different-secret-key-that-is-at-least-32-chars!!",
		JWTExpiry:     15 * time.Minute,
		RefreshExpiry: 24 * time.Hour,
	})

	user := testUser()
	token, err := svc1.GenerateAccessToken(user)
	require.NoError(t, err)

	_, err = svc2.ValidateAccessToken(token)
	assert.Error(t, err)
}

func TestInvalidTokenString(t *testing.T) {
	svc := newTestJWTService()

	_, err := svc.ValidateAccessToken("not-a-valid-token")
	assert.Error(t, err)

	_, err = svc.ValidateRefreshToken("")
	assert.Error(t, err)
}
