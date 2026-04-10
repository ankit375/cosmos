//go:build integration

package integration

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/yourorg/cloudctrl/internal/model"
)

// ════════════════════════════════════════════════════════════════
//  HELPERS
// ════════════════════════════════════════════════════════════════

// seedSuperAdmin creates a super_admin user and returns it with login credentials.
func seedSuperAdmin(t *testing.T, tenantID uuid.UUID) (*model.User, string) {
	t.Helper()
	return seedUser(t, tenantID, model.RoleSuperAdmin)
}

// ════════════════════════════════════════════════════════════════
//  TENANT CRUD (super_admin only)
// ════════════════════════════════════════════════════════════════

func TestTenantAPI_CreateSuccess(t *testing.T) {
	platformTenant := seedTenant(t, "platform-create")
	defer cleanupTenant(t, platformTenant.ID)

	sa, saPass := seedSuperAdmin(t, platformTenant.ID)
	login := loginUser(t, sa.Email, saPass)

	body := `{
		"name": "Acme Corporation",
		"slug": "acme-corp",
		"subscription": "enterprise",
		"max_devices": 500,
		"max_sites": 50,
		"max_users": 100
	}`

	w := performRequest(testRouter, "POST", "/api/v1/tenants", body, login.AccessToken)
	resp := assertSuccess(t, w, http.StatusCreated)

	var tenant model.Tenant
	dataAs(t, resp, &tenant)

	assert.NotEqual(t, uuid.Nil, tenant.ID)
	assert.Equal(t, "Acme Corporation", tenant.Name)
	assert.Equal(t, "acme-corp", tenant.Slug)
	assert.Equal(t, "enterprise", tenant.Subscription)
	assert.Equal(t, 500, tenant.MaxDevices)
	assert.Equal(t, 50, tenant.MaxSites)
	assert.Equal(t, 100, tenant.MaxUsers)
	assert.True(t, tenant.Active)

	// Cleanup created tenant
	defer func() {
    	ctx := context.Background()
    	_ = testPG.Tenants.Delete(ctx, tenant.ID)
	}()
}

func TestTenantAPI_CreateDefaults(t *testing.T) {
	platformTenant := seedTenant(t, "platform-defaults")
	defer cleanupTenant(t, platformTenant.ID)

	sa, saPass := seedSuperAdmin(t, platformTenant.ID)
	login := loginUser(t, sa.Email, saPass)

	slug := fmt.Sprintf("default-test-%s", uuid.New().String()[:8])
	body := fmt.Sprintf(`{"name": "Default Tenant", "slug": "%s"}`, slug)

	w := performRequest(testRouter, "POST", "/api/v1/tenants", body, login.AccessToken)
	resp := assertSuccess(t, w, http.StatusCreated)

	var tenant model.Tenant
	dataAs(t, resp, &tenant)

	assert.Equal(t, "standard", tenant.Subscription)
	assert.Equal(t, 100, tenant.MaxDevices)
	assert.Equal(t, 15, tenant.MaxSites)
	assert.Equal(t, 50, tenant.MaxUsers)

	// Cleanup
	defer func() {
    	ctx := context.Background()
    	_ = testPG.Tenants.Delete(ctx, tenant.ID)
	}()
}

func TestTenantAPI_CreateDuplicateSlug(t *testing.T) {
	platformTenant := seedTenant(t, "platform-dup")
	defer cleanupTenant(t, platformTenant.ID)

	sa, saPass := seedSuperAdmin(t, platformTenant.ID)
	login := loginUser(t, sa.Email, saPass)

	slug := fmt.Sprintf("dup-test-%s", uuid.New().String()[:8])
	body := fmt.Sprintf(`{"name": "First", "slug": "%s"}`, slug)

	w := performRequest(testRouter, "POST", "/api/v1/tenants", body, login.AccessToken)
	assertSuccess(t, w, http.StatusCreated)

	// Same slug again
	body = fmt.Sprintf(`{"name": "Second", "slug": "%s"}`, slug)
	w = performRequest(testRouter, "POST", "/api/v1/tenants", body, login.AccessToken)
	assertError(t, w, http.StatusConflict, "RESOURCE_ALREADY_EXISTS")
}

func TestTenantAPI_GetWithLimits(t *testing.T) {
	platformTenant := seedTenant(t, "platform-get")
	defer cleanupTenant(t, platformTenant.ID)

	sa, saPass := seedSuperAdmin(t, platformTenant.ID)
	login := loginUser(t, sa.Email, saPass)

	// Create a target tenant with some data
	target := seedTenant(t, "target-get")
	defer cleanupTenant(t, target.ID)
	seedSite(t, target.ID, "Site 1")
	seedSite(t, target.ID, "Site 2")
	seedUser(t, target.ID, model.RoleAdmin)

	path := fmt.Sprintf("/api/v1/tenants/%s", target.ID)
	w := performRequest(testRouter, "GET", path, "", login.AccessToken)
	resp := assertSuccess(t, w, http.StatusOK)

	// Response contains tenant + limits
	var result map[string]interface{}
	dataAs(t, resp, &result)

	assert.NotNil(t, result["tenant"])
	assert.NotNil(t, result["limits"])
}

func TestTenantAPI_List(t *testing.T) {
	platformTenant := seedTenant(t, "platform-list")
	defer cleanupTenant(t, platformTenant.ID)

	sa, saPass := seedSuperAdmin(t, platformTenant.ID)
	login := loginUser(t, sa.Email, saPass)

	w := performRequest(testRouter, "GET", "/api/v1/tenants", "", login.AccessToken)
	resp := assertSuccess(t, w, http.StatusOK)

	var tenants []model.Tenant
	dataAs(t, resp, &tenants)

	// Should have at least the platform tenant
	assert.True(t, len(tenants) >= 1)
}

func TestTenantAPI_Update(t *testing.T) {
	platformTenant := seedTenant(t, "platform-update")
	defer cleanupTenant(t, platformTenant.ID)

	sa, saPass := seedSuperAdmin(t, platformTenant.ID)
	login := loginUser(t, sa.Email, saPass)

	target := seedTenant(t, "target-update")
	defer cleanupTenant(t, target.ID)

	body := `{"name": "Updated Corp", "subscription": "enterprise", "max_devices": 999}`
	path := fmt.Sprintf("/api/v1/tenants/%s", target.ID)
	w := performRequest(testRouter, "PUT", path, body, login.AccessToken)
	resp := assertSuccess(t, w, http.StatusOK)

	var updated model.Tenant
	dataAs(t, resp, &updated)

	assert.Equal(t, "Updated Corp", updated.Name)
	assert.Equal(t, "enterprise", updated.Subscription)
	assert.Equal(t, 999, updated.MaxDevices)
}

func TestTenantAPI_Delete(t *testing.T) {
	platformTenant := seedTenant(t, "platform-delete")
	defer cleanupTenant(t, platformTenant.ID)

	sa, saPass := seedSuperAdmin(t, platformTenant.ID)
	login := loginUser(t, sa.Email, saPass)

	target := seedTenant(t, "target-delete")
	// No defer cleanup — we're deleting it

	path := fmt.Sprintf("/api/v1/tenants/%s", target.ID)
	w := performRequest(testRouter, "DELETE", path, "", login.AccessToken)
	assertSuccess(t, w, http.StatusOK)

	// Verify gone
	w = performRequest(testRouter, "GET", path, "", login.AccessToken)
	assertError(t, w, http.StatusNotFound, "RESOURCE_NOT_FOUND")
}

func TestTenantAPI_DeleteNotFound(t *testing.T) {
	platformTenant := seedTenant(t, "platform-del-nf")
	defer cleanupTenant(t, platformTenant.ID)

	sa, saPass := seedSuperAdmin(t, platformTenant.ID)
	login := loginUser(t, sa.Email, saPass)

	path := fmt.Sprintf("/api/v1/tenants/%s", uuid.New())
	w := performRequest(testRouter, "DELETE", path, "", login.AccessToken)
	assertError(t, w, http.StatusNotFound, "RESOURCE_NOT_FOUND")
}

// ════════════════════════════════════════════════════════════════
//  ACCESS CONTROL
// ════════════════════════════════════════════════════════════════

func TestTenantAPI_AdminForbidden(t *testing.T) {
	tenant := seedTenant(t, "tenant-admin-forbidden")
	defer cleanupTenant(t, tenant.ID)

	admin, adminPass := seedUser(t, tenant.ID, model.RoleAdmin)
	login := loginUser(t, admin.Email, adminPass)

	// Admin should NOT be able to access tenant endpoints
	endpoints := []struct {
		method string
		path   string
		body   string
	}{
		{"GET", "/api/v1/tenants", ""},
		{"POST", "/api/v1/tenants", `{"name":"Test","slug":"test"}`},
		{"GET", fmt.Sprintf("/api/v1/tenants/%s", tenant.ID), ""},
		{"PUT", fmt.Sprintf("/api/v1/tenants/%s", tenant.ID), `{"name":"X"}`},
		{"DELETE", fmt.Sprintf("/api/v1/tenants/%s", tenant.ID), ""},
	}

	for _, ep := range endpoints {
		t.Run(fmt.Sprintf("%s %s", ep.method, ep.path), func(t *testing.T) {
			w := performRequest(testRouter, ep.method, ep.path, ep.body, login.AccessToken)
			assertError(t, w, http.StatusForbidden, "AUTH_FORBIDDEN")
		})
	}
}

func TestTenantAPI_OperatorForbidden(t *testing.T) {
	tenant := seedTenant(t, "tenant-op-forbidden")
	defer cleanupTenant(t, tenant.ID)

	op, opPass := seedUser(t, tenant.ID, model.RoleOperator)
	login := loginUser(t, op.Email, opPass)

	w := performRequest(testRouter, "GET", "/api/v1/tenants", "", login.AccessToken)
	assertError(t, w, http.StatusForbidden, "AUTH_FORBIDDEN")
}

func TestTenantAPI_ViewerForbidden(t *testing.T) {
	tenant := seedTenant(t, "tenant-viewer-forbidden")
	defer cleanupTenant(t, tenant.ID)

	viewer, viewerPass := seedUser(t, tenant.ID, model.RoleViewer)
	login := loginUser(t, viewer.Email, viewerPass)

	w := performRequest(testRouter, "GET", "/api/v1/tenants", "", login.AccessToken)
	assertError(t, w, http.StatusForbidden, "AUTH_FORBIDDEN")
}

func TestTenantAPI_UnauthenticatedForbidden(t *testing.T) {
	w := performRequest(testRouter, "GET", "/api/v1/tenants", "", "")
	assertError(t, w, http.StatusUnauthorized, "AUTH_UNAUTHORIZED")
}

// ════════════════════════════════════════════════════════════════
//  TENANT LIMIT ENFORCEMENT
// ════════════════════════════════════════════════════════════════

func TestTenantAPI_CannotSetLimitBelowUsage(t *testing.T) {
	platformTenant := seedTenant(t, "platform-limit-check")
	defer cleanupTenant(t, platformTenant.ID)

	sa, saPass := seedSuperAdmin(t, platformTenant.ID)
	login := loginUser(t, sa.Email, saPass)

	// Create target tenant with data
	target := seedTenant(t, "target-limit-check")
	defer cleanupTenant(t, target.ID)

	seedSite(t, target.ID, "Limit Site 1")
	seedSite(t, target.ID, "Limit Site 2")
	seedSite(t, target.ID, "Limit Site 3")

	// Try to set max_sites to 2 (below current 3)
	body := `{"max_sites": 2}`
	path := fmt.Sprintf("/api/v1/tenants/%s", target.ID)
	w := performRequest(testRouter, "PUT", path, body, login.AccessToken)
	assertError(t, w, http.StatusBadRequest, "LIMIT_BELOW_USAGE")
}

func TestTenantAPI_GetLimits(t *testing.T) {
	platformTenant := seedTenant(t, "platform-getlimits")
	defer cleanupTenant(t, platformTenant.ID)

	sa, saPass := seedSuperAdmin(t, platformTenant.ID)
	login := loginUser(t, sa.Email, saPass)

	target := seedTenantWithLimits(t, "target-getlimits", 10, 50, 25)
	defer cleanupTenant(t, target.ID)

	seedSite(t, target.ID, "Limits Site A")
	seedSite(t, target.ID, "Limits Site B")
	seedUser(t, target.ID, model.RoleAdmin)
	seedUser(t, target.ID, model.RoleViewer)

	path := fmt.Sprintf("/api/v1/tenants/%s/limits", target.ID)
	w := performRequest(testRouter, "GET", path, "", login.AccessToken)
	resp := assertSuccess(t, w, http.StatusOK)

	var limits model.TenantLimits
	dataAs(t, resp, &limits)

	assert.Equal(t, 10, limits.MaxSites)
	assert.Equal(t, 50, limits.MaxDevices)
	assert.Equal(t, 25, limits.MaxUsers)
	assert.Equal(t, 2, limits.CurrentSites)
	assert.Equal(t, 0, limits.CurrentDevices)
	assert.Equal(t, 2, limits.CurrentUsers)
}

// ════════════════════════════════════════════════════════════════
//  TENANT DELETION SAFETY
// ════════════════════════════════════════════════════════════════

func TestTenantAPI_CannotDeleteOwnTenant(t *testing.T) {
	platformTenant := seedTenant(t, "platform-self-del")
	defer cleanupTenant(t, platformTenant.ID)

	sa, saPass := seedSuperAdmin(t, platformTenant.ID)
	login := loginUser(t, sa.Email, saPass)

	path := fmt.Sprintf("/api/v1/tenants/%s", platformTenant.ID)
	w := performRequest(testRouter, "DELETE", path, "", login.AccessToken)
	assertError(t, w, http.StatusBadRequest, "SELF_TENANT_DELETION")
}

// ════════════════════════════════════════════════════════════════
//  SLUG VALIDATION
// ════════════════════════════════════════════════════════════════

func TestTenantAPI_InvalidSlug(t *testing.T) {
	platformTenant := seedTenant(t, "platform-slug")
	defer cleanupTenant(t, platformTenant.ID)

	sa, saPass := seedSuperAdmin(t, platformTenant.ID)
	login := loginUser(t, sa.Email, saPass)

	cases := []struct {
		name string
		slug string
	}{
		{"too short", "a"},
		{"spaces", "has spaces"},
		{"special chars", "slug@invalid!"},
		{"starts with hyphen", "-bad-slug"},
		{"ends with hyphen", "bad-slug-"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := fmt.Sprintf(`{"name":"Test","slug":"%s"}`, tc.slug)
			w := performRequest(testRouter, "POST", "/api/v1/tenants", body, login.AccessToken)
			assert.True(t, w.Code == http.StatusUnprocessableEntity || w.Code == http.StatusBadRequest,
				"slug %q should be rejected, got status %d: %s", tc.slug, w.Code, w.Body.String())
		})
	}
}
