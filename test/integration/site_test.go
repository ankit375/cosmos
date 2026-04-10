//go:build integration

package integration

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yourorg/cloudctrl/internal/model"
)

// ════════════════════════════════════════════════════════════════
//  CREATE SITE
// ════════════════════════════════════════════════════════════════

func TestCreateSite_Success(t *testing.T) {
	tenant := seedTenant(t, "create-site")
	defer cleanupTenant(t, tenant.ID)

	admin, adminPass := seedUser(t, tenant.ID, model.RoleAdmin)
	login := loginUser(t, admin.Email, adminPass)

	body := `{
		"name": "Main Office",
		"description": "Primary office location",
		"address": "123 Main St",
		"timezone": "America/New_York",
		"country_code": "US",
		"latitude": 40.7128,
		"longitude": -74.006,
		"auto_adopt": true,
		"auto_upgrade": false
	}`

	w := performRequest(testRouter, "POST", "/api/v1/sites", body, login.AccessToken)
	resp := assertSuccess(t, w, http.StatusCreated)

	var site model.Site
	dataAs(t, resp, &site)

	assert.NotEqual(t, uuid.Nil, site.ID)
	assert.Equal(t, "Main Office", site.Name)
	assert.Equal(t, "Primary office location", site.Description)
	assert.Equal(t, "America/New_York", site.Timezone)
	assert.Equal(t, "US", site.CountryCode)
	assert.Equal(t, tenant.ID, site.TenantID)
	assert.True(t, site.AutoAdopt)
	assert.False(t, site.AutoUpgrade)
	assert.NotNil(t, site.Latitude)
	assert.InDelta(t, 40.7128, *site.Latitude, 0.001)
}

func TestCreateSite_MinimalFields(t *testing.T) {
	tenant := seedTenant(t, "create-site-min")
	defer cleanupTenant(t, tenant.ID)

	admin, adminPass := seedUser(t, tenant.ID, model.RoleAdmin)
	login := loginUser(t, admin.Email, adminPass)

	body := `{"name": "Minimal Site"}`
	w := performRequest(testRouter, "POST", "/api/v1/sites", body, login.AccessToken)
	resp := assertSuccess(t, w, http.StatusCreated)

	var site model.Site
	dataAs(t, resp, &site)

	assert.Equal(t, "Minimal Site", site.Name)
	assert.Equal(t, "UTC", site.Timezone)
	assert.Equal(t, "US", site.CountryCode)
	assert.False(t, site.AutoAdopt)
}

func TestCreateSite_DuplicateName(t *testing.T) {
	tenant := seedTenant(t, "create-site-dup")
	defer cleanupTenant(t, tenant.ID)

	admin, adminPass := seedUser(t, tenant.ID, model.RoleAdmin)
	login := loginUser(t, admin.Email, adminPass)

	body := `{"name": "Duplicate Site"}`
	w := performRequest(testRouter, "POST", "/api/v1/sites", body, login.AccessToken)
	assertSuccess(t, w, http.StatusCreated)

	// Same name again
	w = performRequest(testRouter, "POST", "/api/v1/sites", body, login.AccessToken)
	assertError(t, w, http.StatusConflict, "RESOURCE_ALREADY_EXISTS")
}

func TestCreateSite_MissingName(t *testing.T) {
	tenant := seedTenant(t, "create-site-noname")
	defer cleanupTenant(t, tenant.ID)

	admin, adminPass := seedUser(t, tenant.ID, model.RoleAdmin)
	login := loginUser(t, admin.Email, adminPass)

	body := `{"description": "No name"}`
	w := performRequest(testRouter, "POST", "/api/v1/sites", body, login.AccessToken)
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
}

func TestCreateSite_LimitExceeded(t *testing.T) {
	tenant := seedTenantWithLimits(t, "site-limit", 2, 100, 50)
	defer cleanupTenant(t, tenant.ID)

	admin, adminPass := seedUser(t, tenant.ID, model.RoleAdmin)
	login := loginUser(t, admin.Email, adminPass)

	// Create 2 sites (at limit)
	for i := 0; i < 2; i++ {
		body := fmt.Sprintf(`{"name": "Site %d"}`, i)
		w := performRequest(testRouter, "POST", "/api/v1/sites", body, login.AccessToken)
		assertSuccess(t, w, http.StatusCreated)
	}

	// Third should fail
	body := `{"name": "Over Limit Site"}`
	w := performRequest(testRouter, "POST", "/api/v1/sites", body, login.AccessToken)
	assertError(t, w, http.StatusForbidden, "LIMIT_EXCEEDED")
}

func TestCreateSite_ViewerForbidden(t *testing.T) {
	tenant := seedTenant(t, "create-site-viewer")
	defer cleanupTenant(t, tenant.ID)

	viewer, viewerPass := seedUser(t, tenant.ID, model.RoleViewer)
	login := loginUser(t, viewer.Email, viewerPass)

	body := `{"name": "Viewer Site"}`
	w := performRequest(testRouter, "POST", "/api/v1/sites", body, login.AccessToken)
	assertError(t, w, http.StatusForbidden, "AUTH_FORBIDDEN")
}

func TestCreateSite_OperatorForbidden(t *testing.T) {
	tenant := seedTenant(t, "create-site-op")
	defer cleanupTenant(t, tenant.ID)

	op, opPass := seedUser(t, tenant.ID, model.RoleOperator)
	login := loginUser(t, op.Email, opPass)

	body := `{"name": "Operator Site"}`
	w := performRequest(testRouter, "POST", "/api/v1/sites", body, login.AccessToken)
	assertError(t, w, http.StatusForbidden, "AUTH_FORBIDDEN")
}

// ════════════════════════════════════════════════════════════════
//  GET SITE
// ════════════════════════════════════════════════════════════════

func TestGetSite_Success(t *testing.T) {
	tenant := seedTenant(t, "get-site")
	defer cleanupTenant(t, tenant.ID)

	admin, adminPass := seedUser(t, tenant.ID, model.RoleAdmin)
	site := seedSite(t, tenant.ID, "Get Test Site")
	login := loginUser(t, admin.Email, adminPass)

	path := fmt.Sprintf("/api/v1/sites/%s", site.ID)
	w := performRequest(testRouter, "GET", path, "", login.AccessToken)
	resp := assertSuccess(t, w, http.StatusOK)

	var got model.Site
	dataAs(t, resp, &got)

	assert.Equal(t, site.ID, got.ID)
	assert.Equal(t, site.Name, got.Name)
	assert.Equal(t, tenant.ID, got.TenantID)
}

func TestGetSite_NotFound(t *testing.T) {
	tenant := seedTenant(t, "get-site-notfound")
	defer cleanupTenant(t, tenant.ID)

	admin, adminPass := seedUser(t, tenant.ID, model.RoleAdmin)
	login := loginUser(t, admin.Email, adminPass)

	path := fmt.Sprintf("/api/v1/sites/%s", uuid.New())
	w := performRequest(testRouter, "GET", path, "", login.AccessToken)
	assertError(t, w, http.StatusNotFound, "RESOURCE_NOT_FOUND")
}

func TestGetSite_InvalidUUID(t *testing.T) {
	tenant := seedTenant(t, "get-site-baduuid")
	defer cleanupTenant(t, tenant.ID)

	admin, adminPass := seedUser(t, tenant.ID, model.RoleAdmin)
	login := loginUser(t, admin.Email, adminPass)

	w := performRequest(testRouter, "GET", "/api/v1/sites/not-a-uuid", "", login.AccessToken)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// ════════════════════════════════════════════════════════════════
//  LIST SITES
// ════════════════════════════════════════════════════════════════

func TestListSites_Success(t *testing.T) {
	tenant := seedTenant(t, "list-sites")
	defer cleanupTenant(t, tenant.ID)

	admin, adminPass := seedUser(t, tenant.ID, model.RoleAdmin)
	seedSite(t, tenant.ID, "Site A")
	seedSite(t, tenant.ID, "Site B")
	seedSite(t, tenant.ID, "Site C")
	login := loginUser(t, admin.Email, adminPass)

	w := performRequest(testRouter, "GET", "/api/v1/sites", "", login.AccessToken)
	resp := assertSuccess(t, w, http.StatusOK)

	var sites []model.Site
	dataAs(t, resp, &sites)

	assert.Len(t, sites, 3)
	assert.NotNil(t, resp.Meta)
	assert.Equal(t, 3, resp.Meta.Total)

	// Should be ordered by name ASC
	assert.Equal(t, "Site A", sites[0].Name)
	assert.Equal(t, "Site B", sites[1].Name)
	assert.Equal(t, "Site C", sites[2].Name)
}

func TestListSites_ViewerCanList(t *testing.T) {
	tenant := seedTenant(t, "list-sites-viewer")
	defer cleanupTenant(t, tenant.ID)

	viewer, viewerPass := seedUser(t, tenant.ID, model.RoleViewer)
	seedSite(t, tenant.ID, "Viewer Visible Site")
	login := loginUser(t, viewer.Email, viewerPass)

	w := performRequest(testRouter, "GET", "/api/v1/sites", "", login.AccessToken)
	assertSuccess(t, w, http.StatusOK)
}

// ════════════════════════════════════════════════════════════════
//  UPDATE SITE
// ════════════════════════════════════════════════════════════════

func TestUpdateSite_Success(t *testing.T) {
	tenant := seedTenant(t, "update-site")
	defer cleanupTenant(t, tenant.ID)

	admin, adminPass := seedUser(t, tenant.ID, model.RoleAdmin)
	site := seedSite(t, tenant.ID, "Original Name")
	login := loginUser(t, admin.Email, adminPass)

	body := `{
		"name": "Updated Name",
		"timezone": "Europe/London",
		"auto_adopt": true
	}`
	path := fmt.Sprintf("/api/v1/sites/%s", site.ID)
	w := performRequest(testRouter, "PUT", path, body, login.AccessToken)
	resp := assertSuccess(t, w, http.StatusOK)

	var updated model.Site
	dataAs(t, resp, &updated)

	assert.Equal(t, "Updated Name", updated.Name)
	assert.Equal(t, "Europe/London", updated.Timezone)
	assert.True(t, updated.AutoAdopt)
}

func TestUpdateSite_OperatorCanUpdate(t *testing.T) {
	tenant := seedTenant(t, "update-site-op")
	defer cleanupTenant(t, tenant.ID)

	seedUser(t, tenant.ID, model.RoleAdmin) // Need admin for tenant
	op, opPass := seedUser(t, tenant.ID, model.RoleOperator)
	site := seedSite(t, tenant.ID, "Op Update Site")
	login := loginUser(t, op.Email, opPass)

	body := `{"name": "Operator Updated"}`
	path := fmt.Sprintf("/api/v1/sites/%s", site.ID)
	w := performRequest(testRouter, "PUT", path, body, login.AccessToken)
	assertSuccess(t, w, http.StatusOK)
}

func TestUpdateSite_ViewerForbidden(t *testing.T) {
	tenant := seedTenant(t, "update-site-viewer")
	defer cleanupTenant(t, tenant.ID)

	viewer, viewerPass := seedUser(t, tenant.ID, model.RoleViewer)
	site := seedSite(t, tenant.ID, "Viewer Update Site")
	login := loginUser(t, viewer.Email, viewerPass)

	body := `{"name": "Should Fail"}`
	path := fmt.Sprintf("/api/v1/sites/%s", site.ID)
	w := performRequest(testRouter, "PUT", path, body, login.AccessToken)
	assertError(t, w, http.StatusForbidden, "AUTH_FORBIDDEN")
}

func TestUpdateSite_NotFound(t *testing.T) {
	tenant := seedTenant(t, "update-site-notfound")
	defer cleanupTenant(t, tenant.ID)

	admin, adminPass := seedUser(t, tenant.ID, model.RoleAdmin)
	login := loginUser(t, admin.Email, adminPass)

	body := `{"name": "Ghost"}`
	path := fmt.Sprintf("/api/v1/sites/%s", uuid.New())
	w := performRequest(testRouter, "PUT", path, body, login.AccessToken)
	assertError(t, w, http.StatusNotFound, "RESOURCE_NOT_FOUND")
}

func TestUpdateSite_DuplicateName(t *testing.T) {
	tenant := seedTenant(t, "update-site-dup")
	defer cleanupTenant(t, tenant.ID)

	admin, adminPass := seedUser(t, tenant.ID, model.RoleAdmin)
	seedSite(t, tenant.ID, "Existing Site")
	target := seedSite(t, tenant.ID, "Target Site")
	login := loginUser(t, admin.Email, adminPass)

	body := `{"name": "Existing Site"}`
	path := fmt.Sprintf("/api/v1/sites/%s", target.ID)
	w := performRequest(testRouter, "PUT", path, body, login.AccessToken)
	assertError(t, w, http.StatusConflict, "RESOURCE_ALREADY_EXISTS")
}

// ════════════════════════════════════════════════════════════════
//  DELETE SITE
// ════════════════════════════════════════════════════════════════

func TestDeleteSite_Success(t *testing.T) {
	tenant := seedTenant(t, "delete-site")
	defer cleanupTenant(t, tenant.ID)

	admin, adminPass := seedUser(t, tenant.ID, model.RoleAdmin)
	site := seedSite(t, tenant.ID, "Delete Me")
	login := loginUser(t, admin.Email, adminPass)

	path := fmt.Sprintf("/api/v1/sites/%s", site.ID)
	w := performRequest(testRouter, "DELETE", path, "", login.AccessToken)
	assertSuccess(t, w, http.StatusOK)

	// Verify gone
	w = performRequest(testRouter, "GET", path, "", login.AccessToken)
	assertError(t, w, http.StatusNotFound, "RESOURCE_NOT_FOUND")
}

func TestDeleteSite_NotFound(t *testing.T) {
	tenant := seedTenant(t, "delete-site-notfound")
	defer cleanupTenant(t, tenant.ID)

	admin, adminPass := seedUser(t, tenant.ID, model.RoleAdmin)
	login := loginUser(t, admin.Email, adminPass)

	path := fmt.Sprintf("/api/v1/sites/%s", uuid.New())
	w := performRequest(testRouter, "DELETE", path, "", login.AccessToken)
	assertError(t, w, http.StatusNotFound, "RESOURCE_NOT_FOUND")
}

func TestDeleteSite_OperatorForbidden(t *testing.T) {
	tenant := seedTenant(t, "delete-site-op")
	defer cleanupTenant(t, tenant.ID)

	op, opPass := seedUser(t, tenant.ID, model.RoleOperator)
	site := seedSite(t, tenant.ID, "Op Delete Site")
	login := loginUser(t, op.Email, opPass)

	path := fmt.Sprintf("/api/v1/sites/%s", site.ID)
	w := performRequest(testRouter, "DELETE", path, "", login.AccessToken)
	assertError(t, w, http.StatusForbidden, "AUTH_FORBIDDEN")
}

// ════════════════════════════════════════════════════════════════
//  SITE STATS
// ════════════════════════════════════════════════════════════════

func TestSiteStats_EmptySite(t *testing.T) {
	tenant := seedTenant(t, "site-stats")
	defer cleanupTenant(t, tenant.ID)

	admin, adminPass := seedUser(t, tenant.ID, model.RoleAdmin)
	site := seedSite(t, tenant.ID, "Stats Site")
	login := loginUser(t, admin.Email, adminPass)

	path := fmt.Sprintf("/api/v1/sites/%s/stats", site.ID)
	w := performRequest(testRouter, "GET", path, "", login.AccessToken)
	resp := assertSuccess(t, w, http.StatusOK)

	var stats model.SiteStats
	dataAs(t, resp, &stats)

	assert.Equal(t, site.ID, stats.SiteID)
	assert.Equal(t, 0, stats.TotalDevices)
	assert.Equal(t, 0, stats.OnlineDevices)
	assert.Equal(t, 0, stats.OfflineDevices)
}

func TestSiteStats_NotFound(t *testing.T) {
	tenant := seedTenant(t, "site-stats-notfound")
	defer cleanupTenant(t, tenant.ID)

	admin, adminPass := seedUser(t, tenant.ID, model.RoleAdmin)
	login := loginUser(t, admin.Email, adminPass)

	path := fmt.Sprintf("/api/v1/sites/%s/stats", uuid.New())
	w := performRequest(testRouter, "GET", path, "", login.AccessToken)
	assertError(t, w, http.StatusNotFound, "RESOURCE_NOT_FOUND")
}

// ════════════════════════════════════════════════════════════════
//  CROSS-TENANT ISOLATION
// ════════════════════════════════════════════════════════════════

func TestSite_CrossTenantIsolation(t *testing.T) {
	tenant1 := seedTenant(t, "site-iso1")
	tenant2 := seedTenant(t, "site-iso2")
	defer cleanupTenant(t, tenant1.ID)
	defer cleanupTenant(t, tenant2.ID)

	admin1, pass1 := seedUser(t, tenant1.ID, model.RoleAdmin)
	admin2, pass2 := seedUser(t, tenant2.ID, model.RoleAdmin)

	site1 := seedSite(t, tenant1.ID, "Tenant1 Site")
	site2 := seedSite(t, tenant2.ID, "Tenant2 Site")

	login1 := loginUser(t, admin1.Email, pass1)
	login2 := loginUser(t, admin2.Email, pass2)

	// Tenant1 cannot see Tenant2's site
	path2 := fmt.Sprintf("/api/v1/sites/%s", site2.ID)
	w := performRequest(testRouter, "GET", path2, "", login1.AccessToken)
	assertError(t, w, http.StatusNotFound, "RESOURCE_NOT_FOUND")

	// Tenant2 cannot see Tenant1's site
	path1 := fmt.Sprintf("/api/v1/sites/%s", site1.ID)
	w = performRequest(testRouter, "GET", path1, "", login2.AccessToken)
	assertError(t, w, http.StatusNotFound, "RESOURCE_NOT_FOUND")

	// Tenant1 cannot update Tenant2's site
	body := `{"name": "Hacked"}`
	w = performRequest(testRouter, "PUT", path2, body, login1.AccessToken)
	assertError(t, w, http.StatusNotFound, "RESOURCE_NOT_FOUND")

	// Tenant1 cannot delete Tenant2's site
	w = performRequest(testRouter, "DELETE", path2, "", login1.AccessToken)
	assertError(t, w, http.StatusNotFound, "RESOURCE_NOT_FOUND")

	// Each tenant lists only their own sites
	w = performRequest(testRouter, "GET", "/api/v1/sites", "", login1.AccessToken)
	resp := assertSuccess(t, w, http.StatusOK)
	var sites1 []model.Site
	dataAs(t, resp, &sites1)
	require.Len(t, sites1, 1)
	assert.Equal(t, "Tenant1 Site", sites1[0].Name)

	w = performRequest(testRouter, "GET", "/api/v1/sites", "", login2.AccessToken)
	resp = assertSuccess(t, w, http.StatusOK)
	var sites2 []model.Site
	dataAs(t, resp, &sites2)
	require.Len(t, sites2, 1)
	assert.Equal(t, "Tenant2 Site", sites2[0].Name)
}
