package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yourorg/cloudctrl/internal/model"
)

// ════════════════════════════════════════════════════════════════
//  CREATE USER
// ════════════════════════════════════════════════════════════════

func TestCreateUser_Success(t *testing.T) {
	tenant := seedTenant(t, "create-user")
	defer cleanupTenant(t, tenant.ID)

	admin, adminPass := seedUser(t, tenant.ID, model.RoleAdmin)
	login := loginUser(t, admin.Email, adminPass)

	body := `{
		"email": "newuser@test.com",
		"password": "securepass123",
		"name": "New User",
		"role": "operator"
	}`
	w := performRequest(testRouter, "POST", "/api/v1/users", body, login.AccessToken)

	resp := assertSuccess(t, w, http.StatusCreated)

	var created model.User
	dataAs(t, resp, &created)

	assert.NotEqual(t, uuid.Nil, created.ID)
	assert.Equal(t, "newuser@test.com", created.Email)
	assert.Equal(t, "New User", created.Name)
	assert.Equal(t, model.RoleOperator, created.Role)
	assert.True(t, created.Active)
	assert.Equal(t, tenant.ID, created.TenantID)
	assert.Empty(t, created.PasswordHash, "password hash must not be in response")
}

func TestCreateUser_DuplicateEmail(t *testing.T) {
	tenant := seedTenant(t, "create-dup")
	defer cleanupTenant(t, tenant.ID)

	admin, adminPass := seedUser(t, tenant.ID, model.RoleAdmin)
	login := loginUser(t, admin.Email, adminPass)

	body := fmt.Sprintf(`{
		"email": "%s",
		"password": "testpass123",
		"name": "Duplicate",
		"role": "viewer"
	}`, admin.Email)
	w := performRequest(testRouter, "POST", "/api/v1/users", body, login.AccessToken)

	assertError(t, w, http.StatusConflict, "RESOURCE_ALREADY_EXISTS")
}

func TestCreateUser_InvalidEmail(t *testing.T) {
	tenant := seedTenant(t, "create-invalid")
	defer cleanupTenant(t, tenant.ID)

	admin, adminPass := seedUser(t, tenant.ID, model.RoleAdmin)
	login := loginUser(t, admin.Email, adminPass)

	body := `{
		"email": "not-an-email",
		"password": "testpass123",
		"name": "Bad Email",
		"role": "viewer"
	}`
	w := performRequest(testRouter, "POST", "/api/v1/users", body, login.AccessToken)

	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
}

func TestCreateUser_WeakPassword(t *testing.T) {
	tenant := seedTenant(t, "create-weak")
	defer cleanupTenant(t, tenant.ID)

	admin, adminPass := seedUser(t, tenant.ID, model.RoleAdmin)
	login := loginUser(t, admin.Email, adminPass)

	body := `{
		"email": "weakpass@test.com",
		"password": "short",
		"name": "Weak Pass",
		"role": "viewer"
	}`
	w := performRequest(testRouter, "POST", "/api/v1/users", body, login.AccessToken)

	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
}

func TestCreateUser_InvalidRole(t *testing.T) {
	tenant := seedTenant(t, "create-badrole")
	defer cleanupTenant(t, tenant.ID)

	admin, adminPass := seedUser(t, tenant.ID, model.RoleAdmin)
	login := loginUser(t, admin.Email, adminPass)

	body := `{
		"email": "badrole@test.com",
		"password": "testpass123",
		"name": "Bad Role",
		"role": "superadmin"
	}`
	w := performRequest(testRouter, "POST", "/api/v1/users", body, login.AccessToken)

	assert.True(t, w.Code == http.StatusUnprocessableEntity || w.Code == http.StatusBadRequest,
		"expected 422 or 400, got %d", w.Code)
}

// ════════════════════════════════════════════════════════════════
//  GET USER
// ════════════════════════════════════════════════════════════════

func TestGetUser_Success(t *testing.T) {
	tenant := seedTenant(t, "get-user")
	defer cleanupTenant(t, tenant.ID)

	admin, adminPass := seedUser(t, tenant.ID, model.RoleAdmin)
	target, _ := seedUser(t, tenant.ID, model.RoleViewer)
	login := loginUser(t, admin.Email, adminPass)

	path := fmt.Sprintf("/api/v1/users/%s", target.ID)
	w := performRequest(testRouter, "GET", path, "", login.AccessToken)

	resp := assertSuccess(t, w, http.StatusOK)

	var user model.User
	dataAs(t, resp, &user)

	assert.Equal(t, target.ID, user.ID)
	assert.Equal(t, target.Email, user.Email)
	assert.Empty(t, user.PasswordHash)
}

func TestGetUser_NotFound(t *testing.T) {
	tenant := seedTenant(t, "get-notfound")
	defer cleanupTenant(t, tenant.ID)

	admin, adminPass := seedUser(t, tenant.ID, model.RoleAdmin)
	login := loginUser(t, admin.Email, adminPass)

	path := fmt.Sprintf("/api/v1/users/%s", uuid.New())
	w := performRequest(testRouter, "GET", path, "", login.AccessToken)

	assertError(t, w, http.StatusNotFound, "RESOURCE_NOT_FOUND")
}

func TestGetUser_InvalidUUID(t *testing.T) {
	tenant := seedTenant(t, "get-invalid-uuid")
	defer cleanupTenant(t, tenant.ID)

	admin, adminPass := seedUser(t, tenant.ID, model.RoleAdmin)
	login := loginUser(t, admin.Email, adminPass)

	w := performRequest(testRouter, "GET", "/api/v1/users/not-a-uuid", "", login.AccessToken)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// ════════════════════════════════════════════════════════════════
//  LIST USERS
// ════════════════════════════════════════════════════════════════

func TestListUsers_Success(t *testing.T) {
	tenant := seedTenant(t, "list-users")
	defer cleanupTenant(t, tenant.ID)

	admin, adminPass := seedUser(t, tenant.ID, model.RoleAdmin)
	seedUser(t, tenant.ID, model.RoleOperator)
	seedUser(t, tenant.ID, model.RoleViewer)

	login := loginUser(t, admin.Email, adminPass)

	w := performRequest(testRouter, "GET", "/api/v1/users", "", login.AccessToken)
	resp := assertSuccess(t, w, http.StatusOK)

	var users []model.User
	dataAs(t, resp, &users)

	assert.Len(t, users, 3) // admin + operator + viewer

	// Check meta
	assert.NotNil(t, resp.Meta)
	assert.Equal(t, 3, resp.Meta.Total)

	// Verify no password hashes leaked
	for _, u := range users {
		assert.Empty(t, u.PasswordHash, "password hash leaked for user %s", u.Email)
	}
}

// ════════════════════════════════════════════════════════════════
//  UPDATE USER
// ════════════════════════════════════════════════════════════════

func TestUpdateUser_Success(t *testing.T) {
	tenant := seedTenant(t, "update-user")
	defer cleanupTenant(t, tenant.ID)

	admin, adminPass := seedUser(t, tenant.ID, model.RoleAdmin)
	target, _ := seedUser(t, tenant.ID, model.RoleViewer)
	login := loginUser(t, admin.Email, adminPass)

	body := `{"name": "Updated Name", "role": "operator"}`
	path := fmt.Sprintf("/api/v1/users/%s", target.ID)
	w := performRequest(testRouter, "PUT", path, body, login.AccessToken)

	resp := assertSuccess(t, w, http.StatusOK)

	var updated model.User
	dataAs(t, resp, &updated)

	assert.Equal(t, "Updated Name", updated.Name)
	assert.Equal(t, model.RoleOperator, updated.Role)
}

func TestUpdateUser_CannotSelfDemoteFromAdmin(t *testing.T) {
	tenant := seedTenant(t, "self-demote")
	defer cleanupTenant(t, tenant.ID)

	admin, adminPass := seedUser(t, tenant.ID, model.RoleAdmin)
	login := loginUser(t, admin.Email, adminPass)

	body := `{"role": "viewer"}`
	path := fmt.Sprintf("/api/v1/users/%s", admin.ID)
	w := performRequest(testRouter, "PUT", path, body, login.AccessToken)

	assertError(t, w, http.StatusBadRequest, "SELF_DEMOTION")
}

func TestUpdateUser_CannotSelfDeactivate(t *testing.T) {
	tenant := seedTenant(t, "self-deactivate")
	defer cleanupTenant(t, tenant.ID)

	admin, adminPass := seedUser(t, tenant.ID, model.RoleAdmin)
	login := loginUser(t, admin.Email, adminPass)

	body := `{"active": false}`
	path := fmt.Sprintf("/api/v1/users/%s", admin.ID)
	w := performRequest(testRouter, "PUT", path, body, login.AccessToken)

	assertError(t, w, http.StatusBadRequest, "SELF_DEACTIVATION")
}

func TestUpdateUser_DeactivateRevokesTokens(t *testing.T) {
	tenant := seedTenant(t, "deactivate-revoke")
	defer cleanupTenant(t, tenant.ID)

	admin, adminPass := seedUser(t, tenant.ID, model.RoleAdmin)
	target, targetPass := seedUser(t, tenant.ID, model.RoleViewer)

	adminLogin := loginUser(t, admin.Email, adminPass)
	targetLogin := loginUser(t, target.Email, targetPass)

	// Verify target can access API
	w := performRequest(testRouter, "GET", "/api/v1/auth/me", "", targetLogin.AccessToken)
	assertSuccess(t, w, http.StatusOK)

	// Admin deactivates target
	body := `{"active": false}`
	path := fmt.Sprintf("/api/v1/users/%s", target.ID)
	w = performRequest(testRouter, "PUT", path, body, adminLogin.AccessToken)
	assertSuccess(t, w, http.StatusOK)

	// Target's refresh token should be revoked
	refreshBody := fmt.Sprintf(`{"refresh_token":"%s"}`, targetLogin.RefreshToken)
	w = performRequest(testRouter, "POST", "/api/v1/auth/refresh", refreshBody, "")
	assertError(t, w, http.StatusUnauthorized, "AUTH_UNAUTHORIZED")
}

// ════════════════════════════════════════════════════════════════
//  DELETE USER
// ════════════════════════════════════════════════════════════════

func TestDeleteUser_Success(t *testing.T) {
	tenant := seedTenant(t, "delete-user")
	defer cleanupTenant(t, tenant.ID)

	admin, adminPass := seedUser(t, tenant.ID, model.RoleAdmin)
	target, _ := seedUser(t, tenant.ID, model.RoleViewer)
	login := loginUser(t, admin.Email, adminPass)

	path := fmt.Sprintf("/api/v1/users/%s", target.ID)
	w := performRequest(testRouter, "DELETE", path, "", login.AccessToken)
	assertSuccess(t, w, http.StatusOK)

	// Verify user is gone
	w = performRequest(testRouter, "GET", path, "", login.AccessToken)
	assertError(t, w, http.StatusNotFound, "RESOURCE_NOT_FOUND")
}

func TestDeleteUser_CannotSelfDelete(t *testing.T) {
	tenant := seedTenant(t, "self-delete")
	defer cleanupTenant(t, tenant.ID)

	admin, adminPass := seedUser(t, tenant.ID, model.RoleAdmin)
	login := loginUser(t, admin.Email, adminPass)

	path := fmt.Sprintf("/api/v1/users/%s", admin.ID)
	w := performRequest(testRouter, "DELETE", path, "", login.AccessToken)

	assertError(t, w, http.StatusBadRequest, "SELF_DELETION")
}

func TestDeleteUser_NotFound(t *testing.T) {
	tenant := seedTenant(t, "delete-notfound")
	defer cleanupTenant(t, tenant.ID)

	admin, adminPass := seedUser(t, tenant.ID, model.RoleAdmin)
	login := loginUser(t, admin.Email, adminPass)

	path := fmt.Sprintf("/api/v1/users/%s", uuid.New())
	w := performRequest(testRouter, "DELETE", path, "", login.AccessToken)

	assertError(t, w, http.StatusNotFound, "RESOURCE_NOT_FOUND")
}

// ════════════════════════════════════════════════════════════════
//  CHANGE PASSWORD
// ════════════════════════════════════════════════════════════════

func TestChangePassword_OwnPassword(t *testing.T) {
	tenant := seedTenant(t, "change-pass")
	defer cleanupTenant(t, tenant.ID)

	user, password := seedUser(t, tenant.ID, model.RoleOperator)
	login := loginUser(t, user.Email, password)

	body := fmt.Sprintf(`{"old_password":"%s","new_password":"newpass123456"}`, password)
	path := fmt.Sprintf("/api/v1/users/%s/password", user.ID)
	w := performRequest(testRouter, "PUT", path, body, login.AccessToken)

	assertSuccess(t, w, http.StatusOK)

	// Old password should no longer work
	loginBody := fmt.Sprintf(`{"email":"%s","password":"%s"}`, user.Email, password)
	w = performRequest(testRouter, "POST", "/api/v1/auth/login", loginBody, "")
	assertError(t, w, http.StatusUnauthorized, "AUTH_UNAUTHORIZED")

	// New password should work
	loginBody = fmt.Sprintf(`{"email":"%s","password":"newpass123456"}`, user.Email)
	w = performRequest(testRouter, "POST", "/api/v1/auth/login", loginBody, "")
	assertSuccess(t, w, http.StatusOK)
}

func TestChangePassword_WrongOldPassword(t *testing.T) {
	tenant := seedTenant(t, "change-wrong")
	defer cleanupTenant(t, tenant.ID)

	user, password := seedUser(t, tenant.ID, model.RoleOperator)
	login := loginUser(t, user.Email, password)

	body := `{"old_password":"wrongpass","new_password":"newpass123456"}`
	path := fmt.Sprintf("/api/v1/users/%s/password", user.ID)
	w := performRequest(testRouter, "PUT", path, body, login.AccessToken)

	assertError(t, w, http.StatusBadRequest, "WRONG_PASSWORD")
}

func TestChangePassword_NonAdminCannotChangeOthers(t *testing.T) {
	tenant := seedTenant(t, "change-others")
	defer cleanupTenant(t, tenant.ID)

	operator, operatorPass := seedUser(t, tenant.ID, model.RoleOperator)
	other, _ := seedUser(t, tenant.ID, model.RoleViewer)
	login := loginUser(t, operator.Email, operatorPass)

	body := `{"old_password":"doesntmatter","new_password":"newpass123456"}`
	path := fmt.Sprintf("/api/v1/users/%s/password", other.ID)
	w := performRequest(testRouter, "PUT", path, body, login.AccessToken)

	assertError(t, w, http.StatusForbidden, "AUTH_FORBIDDEN")
}

func TestChangePassword_RevokesAllRefreshTokens(t *testing.T) {
	tenant := seedTenant(t, "change-revoke")
	defer cleanupTenant(t, tenant.ID)

	user, password := seedUser(t, tenant.ID, model.RoleOperator)

	// Login from two "devices"
	login1 := loginUser(t, user.Email, password)
	login2 := loginUser(t, user.Email, password)

	// Change password using login1
	body := fmt.Sprintf(`{"old_password":"%s","new_password":"newpass123456"}`, password)
	path := fmt.Sprintf("/api/v1/users/%s/password", user.ID)
	w := performRequest(testRouter, "PUT", path, body, login1.AccessToken)
	assertSuccess(t, w, http.StatusOK)

	// Both refresh tokens should be revoked
	refreshBody1 := fmt.Sprintf(`{"refresh_token":"%s"}`, login1.RefreshToken)
	w = performRequest(testRouter, "POST", "/api/v1/auth/refresh", refreshBody1, "")
	assertError(t, w, http.StatusUnauthorized, "AUTH_UNAUTHORIZED")

	refreshBody2 := fmt.Sprintf(`{"refresh_token":"%s"}`, login2.RefreshToken)
	w = performRequest(testRouter, "POST", "/api/v1/auth/refresh", refreshBody2, "")
	assertError(t, w, http.StatusUnauthorized, "AUTH_UNAUTHORIZED")
}

// ════════════════════════════════════════════════════════════════
//  GENERATE API KEY
// ════════════════════════════════════════════════════════════════

func TestGenerateAPIKey_Success(t *testing.T) {
	tenant := seedTenant(t, "api-key")
	defer cleanupTenant(t, tenant.ID)

	admin, adminPass := seedUser(t, tenant.ID, model.RoleAdmin)
	target, _ := seedUser(t, tenant.ID, model.RoleOperator)
	login := loginUser(t, admin.Email, adminPass)

	path := fmt.Sprintf("/api/v1/users/%s/api-key", target.ID)
	w := performRequest(testRouter, "POST", path, "", login.AccessToken)

	resp := assertSuccess(t, w, http.StatusCreated)

	var result map[string]string
	dataAs(t, resp, &result)

	assert.NotEmpty(t, result["api_key"])
	assert.Contains(t, result["message"], "Store this key")
}

func TestGenerateAPIKey_NonAdminForbidden(t *testing.T) {
	tenant := seedTenant(t, "api-key-forbidden")
	defer cleanupTenant(t, tenant.ID)

	operator, operatorPass := seedUser(t, tenant.ID, model.RoleOperator)
	login := loginUser(t, operator.Email, operatorPass)

	path := fmt.Sprintf("/api/v1/users/%s/api-key", operator.ID)
	w := performRequest(testRouter, "POST", path, "", login.AccessToken)

	assertError(t, w, http.StatusForbidden, "AUTH_FORBIDDEN")
}

// ════════════════════════════════════════════════════════════════
//  EDGE CASES
// ════════════════════════════════════════════════════════════════

func TestPasswordNotInAnyResponse(t *testing.T) {
	tenant := seedTenant(t, "no-password-leak")
	defer cleanupTenant(t, tenant.ID)

	admin, adminPass := seedUser(t, tenant.ID, model.RoleAdmin)
	login := loginUser(t, admin.Email, adminPass)

	endpoints := []struct {
		method string
		path   string
		body   string
	}{
		{"GET", "/api/v1/auth/me", ""},
		{"GET", "/api/v1/users", ""},
		{"GET", fmt.Sprintf("/api/v1/users/%s", admin.ID), ""},
	}

	for _, ep := range endpoints {
		t.Run(fmt.Sprintf("%s %s", ep.method, ep.path), func(t *testing.T) {
			w := performRequest(testRouter, ep.method, ep.path, ep.body, login.AccessToken)
			require.Equal(t, http.StatusOK, w.Code)

			// Check raw response doesn't contain password_hash
			body := w.Body.String()
			assert.NotContains(t, body, "password_hash")
			assert.NotContains(t, body, "$2a$") // bcrypt prefix
		})
	}
}

func TestLoginResponse_ContainsUserWithoutSensitiveFields(t *testing.T) {
	tenant := seedTenant(t, "login-sensitive")
	defer cleanupTenant(t, tenant.ID)

	user, password := seedUser(t, tenant.ID, model.RoleAdmin)

	body := fmt.Sprintf(`{"email":"%s","password":"%s"}`, user.Email, password)
	w := performRequest(testRouter, "POST", "/api/v1/auth/login", body, "")

	require.Equal(t, http.StatusOK, w.Code)

	// Parse raw JSON to check field presence
	var raw map[string]json.RawMessage
	err := json.Unmarshal(w.Body.Bytes(), &raw)
	require.NoError(t, err)

	var data map[string]json.RawMessage
	err = json.Unmarshal(raw["data"], &data)
	require.NoError(t, err)

	var userData map[string]json.RawMessage
	err = json.Unmarshal(data["user"], &userData)
	require.NoError(t, err)

	// Must NOT have these fields
	assert.Nil(t, userData["password_hash"], "password_hash should not be in response")
	assert.Nil(t, userData["api_key_hash"], "api_key_hash should not be in response")
}

// ════════════════════════════════════════════════════════════════
//  API KEY AUTHENTICATION
// ════════════════════════════════════════════════════════════════

func TestAPIKey_AuthenticateWithKey(t *testing.T) {
	tenant := seedTenant(t, "apikey-auth")
	defer cleanupTenant(t, tenant.ID)

	admin, adminPass := seedUser(t, tenant.ID, model.RoleAdmin)
	target, _ := seedUser(t, tenant.ID, model.RoleOperator)
	login := loginUser(t, admin.Email, adminPass)

	path := fmt.Sprintf("/api/v1/users/%s/api-key", target.ID)
	w := performRequest(testRouter, "POST", path, "", login.AccessToken)
	resp := assertSuccess(t, w, http.StatusCreated)

	var keyResult map[string]string
	dataAs(t, resp, &keyResult)
	apiKey := keyResult["api_key"]
	assert.NotEmpty(t, apiKey)

	req := httptest.NewRequest("GET", "/api/v1/users", nil)
	req.Header.Set("X-API-Key", apiKey)
	w2 := httptest.NewRecorder()
	testRouter.ServeHTTP(w2, req)

	assertSuccess(t, w2, http.StatusOK)
}

func TestAPIKey_InvalidKeyRejected(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/v1/users", nil)
	req.Header.Set("X-API-Key", "invalid-api-key-that-doesnt-exist")
	w := httptest.NewRecorder()
	testRouter.ServeHTTP(w, req)

	assertError(t, w, http.StatusUnauthorized, "AUTH_UNAUTHORIZED")
}
