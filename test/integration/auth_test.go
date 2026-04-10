package integration

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yourorg/cloudctrl/internal/auth"
	"github.com/yourorg/cloudctrl/internal/config"
	"github.com/yourorg/cloudctrl/internal/model"
)

func ctx() context.Context {
	return context.Background()
}
// ════════════════════════════════════════════════════════════════
//  LOGIN
// ════════════════════════════════════════════════════════════════

func TestLogin_Success(t *testing.T) {
	tenant := seedTenant(t, "login")
	defer cleanupTenant(t, tenant.ID)

	user, password := seedUser(t, tenant.ID, model.RoleAdmin)

	body := fmt.Sprintf(`{"email":"%s","password":"%s"}`, user.Email, password)
	w := performRequest(testRouter, "POST", "/api/v1/auth/login", body, "")

	resp := assertSuccess(t, w, http.StatusOK)

	var loginResp model.LoginResponse
	dataAs(t, resp, &loginResp)

	assert.NotEmpty(t, loginResp.AccessToken)
	assert.NotEmpty(t, loginResp.RefreshToken)
	assert.Greater(t, loginResp.ExpiresIn, int64(0))
	assert.Equal(t, user.ID, loginResp.User.ID)
	assert.Equal(t, user.Email, loginResp.User.Email)
	assert.Equal(t, model.RoleAdmin, loginResp.User.Role)
	assert.Empty(t, loginResp.User.PasswordHash) // Must not leak
}

func TestLogin_InvalidEmail(t *testing.T) {
	body := `{"email":"nonexistent@test.com","password":"whatever"}`
	w := performRequest(testRouter, "POST", "/api/v1/auth/login", body, "")

	assertError(t, w, http.StatusUnauthorized, "AUTH_UNAUTHORIZED")
}

func TestLogin_WrongPassword(t *testing.T) {
	tenant := seedTenant(t, "login-wrong")
	defer cleanupTenant(t, tenant.ID)

	user, _ := seedUser(t, tenant.ID, model.RoleAdmin)

	body := fmt.Sprintf(`{"email":"%s","password":"wrongpassword"}`, user.Email)
	w := performRequest(testRouter, "POST", "/api/v1/auth/login", body, "")

	assertError(t, w, http.StatusUnauthorized, "AUTH_UNAUTHORIZED")
}

func TestLogin_InactiveUser(t *testing.T) {
	tenant := seedTenant(t, "login-inactive")
	defer cleanupTenant(t, tenant.ID)

	user, password := seedUser(t, tenant.ID, model.RoleAdmin)

	// Deactivate user
	active := false
	err := testPG.Users.Update(ctx(), tenant.ID, user.ID, &model.UpdateUserInput{Active: &active})
	require.NoError(t, err)

	body := fmt.Sprintf(`{"email":"%s","password":"%s"}`, user.Email, password)
	w := performRequest(testRouter, "POST", "/api/v1/auth/login", body, "")

	assertError(t, w, http.StatusUnauthorized, "AUTH_UNAUTHORIZED")
}

func TestLogin_MissingFields(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"missing email", `{"password":"test123"}`},
		{"missing password", `{"email":"test@test.com"}`},
		{"empty body", `{}`},
		{"invalid json", `not json`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := performRequest(testRouter, "POST", "/api/v1/auth/login", tc.body, "")
			assert.True(t, w.Code == http.StatusUnprocessableEntity || w.Code == http.StatusBadRequest,
				"expected 422 or 400, got %d: %s", w.Code, w.Body.String())
		})
	}
}

// ════════════════════════════════════════════════════════════════
//  TOKEN VALIDATION
// ════════════════════════════════════════════════════════════════

func TestAuthenticatedEndpoint_ValidToken(t *testing.T) {
	tenant := seedTenant(t, "token-valid")
	defer cleanupTenant(t, tenant.ID)

	user, password := seedUser(t, tenant.ID, model.RoleAdmin)
	login := loginUser(t, user.Email, password)

	w := performRequest(testRouter, "GET", "/api/v1/auth/me", "", login.AccessToken)

	resp := assertSuccess(t, w, http.StatusOK)

	var me model.User
	dataAs(t, resp, &me)
	assert.Equal(t, user.ID, me.ID)
	assert.Equal(t, user.Email, me.Email)
}

func TestAuthenticatedEndpoint_NoToken(t *testing.T) {
	w := performRequest(testRouter, "GET", "/api/v1/auth/me", "", "")
	assertError(t, w, http.StatusUnauthorized, "AUTH_UNAUTHORIZED")
}

func TestAuthenticatedEndpoint_InvalidToken(t *testing.T) {
	w := performRequest(testRouter, "GET", "/api/v1/auth/me", "", "invalid-token")
	assertError(t, w, http.StatusUnauthorized, "AUTH_UNAUTHORIZED")
}

func TestAuthenticatedEndpoint_MalformedHeader(t *testing.T) {
	tests := []struct {
		name   string
		header string
	}{
		{"no bearer prefix", "just-a-token"},
		{"basic auth", "Basic dXNlcjpwYXNz"},
		{"empty bearer", "Bearer "},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/api/v1/auth/me", nil)
			req.Header.Set("Authorization", tc.header)
			w := httptest.NewRecorder()
			testRouter.ServeHTTP(w, req)

			assert.Equal(t, http.StatusUnauthorized, w.Code, "body: %s", w.Body.String())
		})
	}
}

// ════════════════════════════════════════════════════════════════
//  REFRESH TOKEN
// ════════════════════════════════════════════════════════════════

func TestRefresh_Success(t *testing.T) {
	tenant := seedTenant(t, "refresh")
	defer cleanupTenant(t, tenant.ID)

	user, password := seedUser(t, tenant.ID, model.RoleAdmin)
	login := loginUser(t, user.Email, password)

	// Wait a tiny bit so new tokens have different iat
	time.Sleep(1100 * time.Millisecond)

	body := fmt.Sprintf(`{"refresh_token":"%s"}`, login.RefreshToken)
	w := performRequest(testRouter, "POST", "/api/v1/auth/refresh", body, "")

	resp := assertSuccess(t, w, http.StatusOK)

	var refreshResp model.LoginResponse
	dataAs(t, resp, &refreshResp)

	assert.NotEmpty(t, refreshResp.AccessToken)
	assert.NotEmpty(t, refreshResp.RefreshToken)
	assert.NotEqual(t, login.AccessToken, refreshResp.AccessToken, "should get new access token")
	assert.NotEqual(t, login.RefreshToken, refreshResp.RefreshToken, "should get new refresh token (rotation)")
	assert.Greater(t, refreshResp.ExpiresIn, int64(0))
}

func TestRefresh_InvalidToken(t *testing.T) {
	body := `{"refresh_token":"invalid-refresh-token"}`
	w := performRequest(testRouter, "POST", "/api/v1/auth/refresh", body, "")

	assertError(t, w, http.StatusUnauthorized, "AUTH_UNAUTHORIZED")
}

func TestRefresh_RevokedToken(t *testing.T) {
	tenant := seedTenant(t, "refresh-revoked")
	defer cleanupTenant(t, tenant.ID)

	user, password := seedUser(t, tenant.ID, model.RoleAdmin)
	login := loginUser(t, user.Email, password)

	// Use refresh token once (this revokes it and issues a new one)
	body := fmt.Sprintf(`{"refresh_token":"%s"}`, login.RefreshToken)
	w := performRequest(testRouter, "POST", "/api/v1/auth/refresh", body, "")
	assertSuccess(t, w, http.StatusOK)

	// Try to use the old refresh token again — should fail (token rotation)
	w = performRequest(testRouter, "POST", "/api/v1/auth/refresh", body, "")
	assertError(t, w, http.StatusUnauthorized, "AUTH_UNAUTHORIZED")
}

func TestRefresh_DeactivatedUser(t *testing.T) {
	tenant := seedTenant(t, "refresh-deactivated")
	defer cleanupTenant(t, tenant.ID)

	user, password := seedUser(t, tenant.ID, model.RoleAdmin)
	login := loginUser(t, user.Email, password)

	// Deactivate user
	active := false
	err := testPG.Users.Update(ctx(), tenant.ID, user.ID, &model.UpdateUserInput{Active: &active})
	require.NoError(t, err)

	// Try to refresh — should fail
	body := fmt.Sprintf(`{"refresh_token":"%s"}`, login.RefreshToken)
	w := performRequest(testRouter, "POST", "/api/v1/auth/refresh", body, "")

	assertError(t, w, http.StatusUnauthorized, "AUTH_UNAUTHORIZED")
}

// ════════════════════════════════════════════════════════════════
//  LOGOUT
// ════════════════════════════════════════════════════════════════

func TestLogout_Success(t *testing.T) {
	tenant := seedTenant(t, "logout")
	defer cleanupTenant(t, tenant.ID)

	user, password := seedUser(t, tenant.ID, model.RoleAdmin)
	login := loginUser(t, user.Email, password)

	// Logout with refresh token
	body := fmt.Sprintf(`{"refresh_token":"%s"}`, login.RefreshToken)
	w := performRequest(testRouter, "POST", "/api/v1/auth/logout", body, login.AccessToken)

	assertSuccess(t, w, http.StatusOK)

	// Access token should now be blacklisted
	w = performRequest(testRouter, "GET", "/api/v1/auth/me", "", login.AccessToken)
	assertError(t, w, http.StatusUnauthorized, "AUTH_UNAUTHORIZED")

	// Refresh token should be revoked
	time.Sleep(1100 * time.Millisecond)
	refreshBody := fmt.Sprintf(`{"refresh_token":"%s"}`, login.RefreshToken)
	w = performRequest(testRouter, "POST", "/api/v1/auth/refresh", refreshBody, "")
	assertError(t, w, http.StatusUnauthorized, "AUTH_UNAUTHORIZED")
}

func TestLogout_WithoutRefreshToken(t *testing.T) {
	tenant := seedTenant(t, "logout-no-refresh")
	defer cleanupTenant(t, tenant.ID)

	user, password := seedUser(t, tenant.ID, model.RoleAdmin)
	login := loginUser(t, user.Email, password)

	// Logout without providing refresh token — should still blacklist access token
	w := performRequest(testRouter, "POST", "/api/v1/auth/logout", `{}`, login.AccessToken)
	assertSuccess(t, w, http.StatusOK)

	// Access token should be blacklisted
	w = performRequest(testRouter, "GET", "/api/v1/auth/me", "", login.AccessToken)
	assertError(t, w, http.StatusUnauthorized, "AUTH_UNAUTHORIZED")
}

// ════════════════════════════════════════════════════════════════
//  CROSS-TENANT ISOLATION
// ════════════════════════════════════════════════════════════════

func TestCrossTenant_CannotAccessOtherTenantUsers(t *testing.T) {
	tenant1 := seedTenant(t, "cross-1")
	defer cleanupTenant(t, tenant1.ID)

	tenant2 := seedTenant(t, "cross-2")
	defer cleanupTenant(t, tenant2.ID)

	user1, password1 := seedUser(t, tenant1.ID, model.RoleAdmin)
	user2, _ := seedUser(t, tenant2.ID, model.RoleAdmin)

	login1 := loginUser(t, user1.Email, password1)

	// Try to access user from tenant2 using tenant1's token
	path := fmt.Sprintf("/api/v1/users/%s", user2.ID)
	w := performRequest(testRouter, "GET", path, "", login1.AccessToken)

	// Should get 404 (not 403) — tenant2 user is invisible to tenant1
	assertError(t, w, http.StatusNotFound, "RESOURCE_NOT_FOUND")
}

func TestCrossTenant_UserListOnlyShowsOwnTenant(t *testing.T) {
	tenant1 := seedTenant(t, "list-1")
	defer cleanupTenant(t, tenant1.ID)

	tenant2 := seedTenant(t, "list-2")
	defer cleanupTenant(t, tenant2.ID)

	user1, password1 := seedUser(t, tenant1.ID, model.RoleAdmin)
	seedUser(t, tenant2.ID, model.RoleAdmin) // Create user in other tenant

	login1 := loginUser(t, user1.Email, password1)

	w := performRequest(testRouter, "GET", "/api/v1/users", "", login1.AccessToken)
	resp := assertSuccess(t, w, http.StatusOK)

	var users []model.User
	dataAs(t, resp, &users)

	// Should only see tenant1's users
	for _, u := range users {
		assert.Equal(t, tenant1.ID, u.TenantID, "user %s belongs to wrong tenant", u.Email)
	}
}

// ════════════════════════════════════════════════════════════════
//  ROLE-BASED ACCESS CONTROL
// ════════════════════════════════════════════════════════════════

func TestRBAC_ViewerCannotCreateUser(t *testing.T) {
	tenant := seedTenant(t, "rbac-viewer")
	defer cleanupTenant(t, tenant.ID)

	viewer, viewerPass := seedUser(t, tenant.ID, model.RoleViewer)
	login := loginUser(t, viewer.Email, viewerPass)

	body := `{"email":"new@test.com","password":"testpass123","name":"New User","role":"viewer"}`
	w := performRequest(testRouter, "POST", "/api/v1/users", body, login.AccessToken)

	assertError(t, w, http.StatusForbidden, "AUTH_FORBIDDEN")
}

func TestRBAC_OperatorCannotCreateUser(t *testing.T) {
	tenant := seedTenant(t, "rbac-operator")
	defer cleanupTenant(t, tenant.ID)

	operator, operatorPass := seedUser(t, tenant.ID, model.RoleOperator)
	login := loginUser(t, operator.Email, operatorPass)

	body := `{"email":"new@test.com","password":"testpass123","name":"New User","role":"viewer"}`
	w := performRequest(testRouter, "POST", "/api/v1/users", body, login.AccessToken)

	assertError(t, w, http.StatusForbidden, "AUTH_FORBIDDEN")
}

func TestRBAC_AdminCanCreateUser(t *testing.T) {
	tenant := seedTenant(t, "rbac-admin")
	defer cleanupTenant(t, tenant.ID)

	admin, adminPass := seedUser(t, tenant.ID, model.RoleAdmin)
	login := loginUser(t, admin.Email, adminPass)

	body := `{"email":"newuser@test.com","password":"testpass123","name":"New User","role":"viewer"}`
	w := performRequest(testRouter, "POST", "/api/v1/users", body, login.AccessToken)

	assertSuccess(t, w, http.StatusCreated)
}

func TestRBAC_ViewerCanListUsers(t *testing.T) {
	tenant := seedTenant(t, "rbac-viewer-list")
	defer cleanupTenant(t, tenant.ID)

	viewer, viewerPass := seedUser(t, tenant.ID, model.RoleViewer)
	login := loginUser(t, viewer.Email, viewerPass)

	w := performRequest(testRouter, "GET", "/api/v1/users", "", login.AccessToken)
	assertSuccess(t, w, http.StatusOK)
}

func TestRBAC_ViewerCannotDeleteUser(t *testing.T) {
	tenant := seedTenant(t, "rbac-viewer-delete")
	defer cleanupTenant(t, tenant.ID)

	viewer, viewerPass := seedUser(t, tenant.ID, model.RoleViewer)
	otherUser, _ := seedUser(t, tenant.ID, model.RoleViewer)
	login := loginUser(t, viewer.Email, viewerPass)

	path := fmt.Sprintf("/api/v1/users/%s", otherUser.ID)
	w := performRequest(testRouter, "DELETE", path, "", login.AccessToken)

	assertError(t, w, http.StatusForbidden, "AUTH_FORBIDDEN")
}

// ════════════════════════════════════════════════════════════════
//  FULL AUTH LIFECYCLE
// ════════════════════════════════════════════════════════════════

func TestFullAuthLifecycle(t *testing.T) {
	tenant := seedTenant(t, "lifecycle")
	defer cleanupTenant(t, tenant.ID)

	user, password := seedUser(t, tenant.ID, model.RoleAdmin)

	// 1. Login
	login := loginUser(t, user.Email, password)
	assert.NotEmpty(t, login.AccessToken)
	assert.NotEmpty(t, login.RefreshToken)

	// 2. Use access token
	w := performRequest(testRouter, "GET", "/api/v1/auth/me", "", login.AccessToken)
	assertSuccess(t, w, http.StatusOK)

	// 3. Refresh
	time.Sleep(1100 * time.Millisecond)
	refreshBody := fmt.Sprintf(`{"refresh_token":"%s"}`, login.RefreshToken)
	w = performRequest(testRouter, "POST", "/api/v1/auth/refresh", refreshBody, "")
	resp := assertSuccess(t, w, http.StatusOK)

	var refreshed model.LoginResponse
	dataAs(t, resp, &refreshed)
	assert.NotEqual(t, login.AccessToken, refreshed.AccessToken)

	// 4. Old refresh token should be revoked (rotation)
	w = performRequest(testRouter, "POST", "/api/v1/auth/refresh", refreshBody, "")
	assertError(t, w, http.StatusUnauthorized, "AUTH_UNAUTHORIZED")

	// 5. New access token works
	w = performRequest(testRouter, "GET", "/api/v1/auth/me", "", refreshed.AccessToken)
	assertSuccess(t, w, http.StatusOK)

	// 6. Logout with new tokens
	logoutBody := fmt.Sprintf(`{"refresh_token":"%s"}`, refreshed.RefreshToken)
	w = performRequest(testRouter, "POST", "/api/v1/auth/logout", logoutBody, refreshed.AccessToken)
	assertSuccess(t, w, http.StatusOK)

	// 7. Access token is blacklisted
	w = performRequest(testRouter, "GET", "/api/v1/auth/me", "", refreshed.AccessToken)
	assertError(t, w, http.StatusUnauthorized, "AUTH_UNAUTHORIZED")

	// 8. Refresh token is revoked
	newRefreshBody := fmt.Sprintf(`{"refresh_token":"%s"}`, refreshed.RefreshToken)
	w = performRequest(testRouter, "POST", "/api/v1/auth/refresh", newRefreshBody, "")
	assertError(t, w, http.StatusUnauthorized, "AUTH_UNAUTHORIZED")

	// 9. Can login again
	login2 := loginUser(t, user.Email, password)
	assert.NotEmpty(t, login2.AccessToken)
}

// ════════════════════════════════════════════════════════════════
//  EXPIRED TOKEN
// ════════════════════════════════════════════════════════════════

func TestExpiredToken_Rejected(t *testing.T) {
	expiredJWT := auth.NewJWTService(config.AuthConfig{
		JWTSecret:     "integration-test-secret-key-minimum-32-characters!!",
		JWTExpiry:     -1 * time.Hour,
		RefreshExpiry: 24 * time.Hour,
	})

	tenant := seedTenant(t, "expired-token")
	defer cleanupTenant(t, tenant.ID)

	user, _ := seedUser(t, tenant.ID, model.RoleAdmin)

	expiredToken, err := expiredJWT.GenerateAccessToken(user)
	require.NoError(t, err)

	w := performRequest(testRouter, "GET", "/api/v1/auth/me", "", expiredToken)
	assertError(t, w, http.StatusUnauthorized, "AUTH_UNAUTHORIZED")
}
