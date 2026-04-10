//go:build integration

package integration

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yourorg/cloudctrl/internal/model"
	pgstore "github.com/yourorg/cloudctrl/internal/store/postgres"
	"github.com/yourorg/cloudctrl/internal/websocket"
	"github.com/yourorg/cloudctrl/pkg/crypto"
)

// TestFullAdoptionFlow tests: pending → adopted → provisioning → online
func TestFullAdoptionFlow(t *testing.T) {
	tenant := seedTenant(t, "adopt-flow")
	defer cleanupTenant(t, tenant.ID)

	admin, password := seedUser(t, tenant.ID, model.RoleAdmin)
	login := loginUser(t, admin.Email, password)
	token := login.AccessToken

	site := seedSite(t, tenant.ID, "adoption-site")
	device := seedPendingDevice(t, tenant.ID)

	// 1. Verify device in pending list
	w := performRequest(testRouter, "GET", "/api/v1/devices/pending", "", token)
	resp := assertSuccess(t, w, 200)

	var pendingDevices []*model.Device
	dataAs(t, resp, &pendingDevices)

	found := false
	for _, d := range pendingDevices {
		if d.ID == device.ID {
			found = true
			if d.Status != model.DeviceStatusPendingAdopt {
				t.Errorf("expected pending_adopt, got %s", d.Status)
			}
		}
	}
	if !found {
		t.Fatal("device not found in pending list")
	}

	// 2. Adopt the device
	adoptBody := fmt.Sprintf(`{"site_id":"%s","name":"lobby-ap-1"}`, site.ID)
	w = performRequest(testRouter, "POST", fmt.Sprintf("/api/v1/devices/%s/adopt", device.ID), adoptBody, token)
	resp = assertSuccess(t, w, 200)

	var adoptResult map[string]interface{}
	dataAs(t, resp, &adoptResult)

	deviceToken, ok := adoptResult["device_token"].(string)
	if !ok || deviceToken == "" {
		t.Fatal("expected device_token in adoption response")
	}

	// 3. Verify device is provisioning
	w = performRequest(testRouter, "GET", fmt.Sprintf("/api/v1/devices/%s", device.ID), "", token)
	resp = assertSuccess(t, w, 200)

	var adoptedDevice model.Device
	dataAs(t, resp, &adoptedDevice)

	if adoptedDevice.Status != model.DeviceStatusProvisioning {
		t.Errorf("expected provisioning, got %s", adoptedDevice.Status)
	}
	if adoptedDevice.SiteID == nil || *adoptedDevice.SiteID != site.ID {
		t.Error("device not assigned to correct site")
	}
	if adoptedDevice.AdoptedAt == nil {
		t.Error("adopted_at should be set")
	}
	if adoptedDevice.Name != "lobby-ap-1" {
		t.Errorf("expected name lobby-ap-1, got %s", adoptedDevice.Name)
	}

	// 4. Verify token hash lookup works
	tokenHash := crypto.HashToken(deviceToken)
	ctx := context.Background()
	deviceByToken, err := testPG.Devices.GetByTokenHash(ctx, tokenHash)
	if err != nil || deviceByToken == nil {
		t.Fatal("device not found by token hash")
	}
	if deviceByToken.ID != device.ID {
		t.Error("token resolved to wrong device")
	}

	// 5. Simulate online via state store → verify live status
	testHub.StateStore().Set(&websocket.DeviceState{
		DeviceID:        device.ID,
		TenantID:        tenant.ID,
		SiteID:          &site.ID,
		Status:          model.DeviceStatusOnline,
		FirmwareVersion: "1.0.0",
		IPAddress:       "192.168.1.10",
		ClientCount:     5,
		CPUUsage:        12.5,
		LastHeartbeat:   time.Now(),
		Dirty:           true,
	})

	w = performRequest(testRouter, "GET", fmt.Sprintf("/api/v1/devices/%s/status", device.ID), "", token)
	resp = assertSuccess(t, w, 200)

	var statusResult map[string]interface{}
	dataAs(t, resp, &statusResult)

	if statusResult["status"] != "online" {
		t.Errorf("expected online, got %v", statusResult["status"])
	}
}

// TestAutoAdoptWhenSiteSettingEnabled tests auto-adoption via hub
func TestAutoAdoptWhenSiteSettingEnabled(t *testing.T) {
	tenant := seedTenant(t, "auto-adopt")
	defer cleanupTenant(t, tenant.ID)

	site := seedSiteWithAutoAdopt(t, tenant.ID, "auto-adopt-site")
	device := seedPendingDevice(t, tenant.ID)

	ctx := context.Background()

	// Verify site has auto_adopt
	siteFromDB, err := testPG.Sites.GetByID(ctx, tenant.ID, site.ID)
	if err != nil || siteFromDB == nil {
		t.Fatal("site not found")
	}
	if !siteFromDB.AutoAdopt {
		t.Fatal("site should have auto_adopt enabled")
	}

	// Adopt via hub (simulating what auto-adopt does)
	token, err := testHub.HandleAdoption(ctx, tenant.ID, device.ID, site.ID, "auto-adopted-ap")
	if err != nil {
		t.Fatalf("adoption failed: %v", err)
	}
	if token == "" {
		t.Fatal("expected device token")
	}

	// Verify device state in DB
	adoptedDevice, err := testPG.Devices.GetByID(ctx, tenant.ID, device.ID)
	if err != nil || adoptedDevice == nil {
		t.Fatal("device not found after adoption")
	}
	if adoptedDevice.Status != model.DeviceStatusProvisioning {
		t.Errorf("expected provisioning, got %s", adoptedDevice.Status)
	}
	if adoptedDevice.SiteID == nil || *adoptedDevice.SiteID != site.ID {
		t.Error("device not assigned to auto-adopt site")
	}

	// Verify token hash stored correctly
	tokenHash := crypto.HashToken(token)
	found, err := testPG.Devices.GetByTokenHash(ctx, tokenHash)
	if err != nil || found == nil {
		t.Fatal("device not found by token hash")
	}
	if found.ID != device.ID {
		t.Error("wrong device found by token")
	}
}

// TestDeviceGoesOfflineAfterHeartbeatTimeout tests offline detection
func TestDeviceGoesOfflineAfterHeartbeatTimeout(t *testing.T) {
	tenant := seedTenant(t, "offline-detect")
	defer cleanupTenant(t, tenant.ID)

	deviceID := uuid.New()

	// Set device online with old heartbeat (2min ago > 90s timeout)
	testHub.StateStore().Set(&websocket.DeviceState{
		DeviceID:        deviceID,
		TenantID:        tenant.ID,
		Status:          model.DeviceStatusOnline,
		FirmwareVersion: "1.0.0",
		LastHeartbeat:   time.Now().Add(-2 * time.Minute),
		Dirty:           false,
	})

	// Run offline detection
	offlined := testHub.StateStore().SetOffline(90 * time.Second)

	found := false
	for _, id := range offlined {
		if id == deviceID {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("device should have been marked offline")
	}

	// Verify state
	state := testHub.StateStore().Get(deviceID)
	if state == nil {
		t.Fatal("state should exist")
	}
	if state.Status != model.DeviceStatusOffline {
		t.Errorf("expected offline, got %s", state.Status)
	}
	if !state.Dirty {
		t.Error("should be dirty for DB sync")
	}

	// Verify recent device NOT marked offline
	recentID := uuid.New()
	testHub.StateStore().Set(&websocket.DeviceState{
		DeviceID:      recentID,
		TenantID:      tenant.ID,
		Status:        model.DeviceStatusOnline,
		LastHeartbeat: time.Now().Add(-30 * time.Second), // 30s < 90s
		Dirty:         false,
	})

	offlined2 := testHub.StateStore().SetOffline(90 * time.Second)
	for _, id := range offlined2 {
		if id == recentID {
			t.Fatal("recent device should NOT be marked offline")
		}
	}

	// Cleanup state store
	testHub.StateStore().Delete(deviceID)
	testHub.StateStore().Delete(recentID)
}

// TestDeviceComesBackOnlineAfterReconnect tests offline → online
func TestDeviceComesBackOnlineAfterReconnect(t *testing.T) {
	tenant := seedTenant(t, "reconnect")
	defer cleanupTenant(t, tenant.ID)

	deviceID := uuid.New()
	siteID := uuid.New()

	// Set device as offline
	testHub.StateStore().Set(&websocket.DeviceState{
		DeviceID:      deviceID,
		TenantID:      tenant.ID,
		SiteID:        &siteID,
		Status:        model.DeviceStatusOffline,
		LastHeartbeat: time.Now().Add(-5 * time.Minute),
		Dirty:         false,
	})

	// Verify offline
	state := testHub.StateStore().Get(deviceID)
	if state.Status != model.DeviceStatusOffline {
		t.Fatalf("expected offline, got %s", state.Status)
	}

	// Simulate reconnect
	testHub.StateStore().Set(&websocket.DeviceState{
		DeviceID:        deviceID,
		TenantID:        tenant.ID,
		SiteID:          &siteID,
		Status:          model.DeviceStatusOnline,
		FirmwareVersion: "1.0.0",
		IPAddress:       "192.168.1.10",
		LastHeartbeat:   time.Now(),
		Dirty:           true,
	})

	// Verify online
	state = testHub.StateStore().Get(deviceID)
	if state.Status != model.DeviceStatusOnline {
		t.Errorf("expected online, got %s", state.Status)
	}
	if !state.Dirty {
		t.Error("should be dirty for DB persistence")
	}
	if state.IPAddress != "192.168.1.10" {
		t.Errorf("expected IP 192.168.1.10, got %s", state.IPAddress)
	}

	// Cleanup
	testHub.StateStore().Delete(deviceID)
}

// TestStatePersistedToDBCorrectly tests dirty state → DB batch update
func TestStatePersistedToDBCorrectly(t *testing.T) {
	tenant := seedTenant(t, "persist")
	defer cleanupTenant(t, tenant.ID)

	device := seedPendingDevice(t, tenant.ID)
	ctx := context.Background()

	// Set state as online + dirty
	testHub.StateStore().Set(&websocket.DeviceState{
		DeviceID:             device.ID,
		TenantID:             tenant.ID,
		Status:               model.DeviceStatusOnline,
		FirmwareVersion:      "1.0.0",
		IPAddress:            "10.0.0.1",
		Uptime:               3600,
		AppliedConfigVersion: 5,
		LastHeartbeat:        time.Now(),
		Dirty:                true,
	})

	// Collect dirty
	dirty := testHub.StateStore().CollectDirty()
	if len(dirty) == 0 {
		t.Fatal("expected dirty state entries")
	}

	// Find our device in dirty list
	var found *websocket.DeviceState
	for _, s := range dirty {
		if s.DeviceID == device.ID {
			found = s
			break
		}
	}
	if found == nil {
		t.Fatal("device not in dirty list")
	}

	// Persist to DB
	ip := found.IPAddress
	updates := []pgstore.DeviceStateUpdate{
		{
			DeviceID:             found.DeviceID,
			Status:               found.Status,
			LastSeen:             found.LastHeartbeat,
			IPAddress:            &ip,
			Uptime:               found.Uptime,
			AppliedConfigVersion: found.AppliedConfigVersion,
		},
	}

	if err := testPG.Devices.BatchUpdateState(ctx, updates); err != nil {
		t.Fatalf("batch update failed: %v", err)
	}

	// Verify DB
	dbDevice, err := testPG.Devices.GetByID(ctx, tenant.ID, device.ID)
	if err != nil || dbDevice == nil {
		t.Fatal("device not found in DB")
	}
	if dbDevice.Status != model.DeviceStatusOnline {
		t.Errorf("DB status: expected online, got %s", dbDevice.Status)
	}
	if dbDevice.Uptime != 3600 {
		t.Errorf("DB uptime: expected 3600, got %d", dbDevice.Uptime)
	}
	if dbDevice.AppliedConfigVersion != 5 {
		t.Errorf("DB applied_config_version: expected 5, got %d", dbDevice.AppliedConfigVersion)
	}
	if dbDevice.IPAddress == nil {
		t.Error("DB ip_address not updated correctly")
	}
	if dbDevice.LastSeen == nil {
		t.Error("DB last_seen should be set")
	}
}

// TestDeviceDecommissionFlow tests full decommission lifecycle
func TestDeviceDecommissionFlow(t *testing.T) {
	tenant := seedTenant(t, "decommission")
	defer cleanupTenant(t, tenant.ID)

	admin, password := seedUser(t, tenant.ID, model.RoleAdmin)
	login := loginUser(t, admin.Email, password)
	token := login.AccessToken

	site := seedSite(t, tenant.ID, "decommission-site")
	device := seedPendingDevice(t, tenant.ID)

	ctx := context.Background()

	// Adopt device first
	adoptToken, err := testHub.HandleAdoption(ctx, tenant.ID, device.ID, site.ID, "to-decommission")
	if err != nil {
		t.Fatalf("adoption failed: %v", err)
	}
	_ = adoptToken

	// Set device online in state store
	testHub.StateStore().Set(&websocket.DeviceState{
		DeviceID:        device.ID,
		TenantID:        tenant.ID,
		SiteID:          &site.ID,
		Status:          model.DeviceStatusOnline,
		FirmwareVersion: "1.0.0",
		LastHeartbeat:   time.Now(),
	})

	// Decommission via API
	w := performRequest(testRouter, "DELETE", fmt.Sprintf("/api/v1/devices/%s", device.ID), "", token)
	assertSuccess(t, w, 200)

	// Verify DB: device is decommissioned (soft delete)
	var status string
	var tokenHash *string
	err = testPG.Pool.QueryRow(ctx,
		`SELECT status, device_token_hash FROM devices WHERE id = $1`, device.ID,
	).Scan(&status, &tokenHash)
	if err != nil {
		t.Fatalf("query device: %v", err)
	}
	if status != "decommissioned" {
		t.Errorf("expected decommissioned, got %s", status)
	}
	if tokenHash != nil {
		t.Error("token hash should be cleared on decommission")
	}

	// Verify state store cleaned up
	state := testHub.StateStore().Get(device.ID)
	if state != nil {
		t.Error("state store should be cleaned up after decommission")
	}

	// Verify cannot adopt decommissioned device
	adoptBody := fmt.Sprintf(`{"site_id":"%s"}`, site.ID)
	w = performRequest(testRouter, "POST", fmt.Sprintf("/api/v1/devices/%s/adopt", device.ID), adoptBody, token)
	if w.Code == 200 {
		t.Error("should not be able to adopt decommissioned device")
	}

	// Verify cannot update decommissioned device
	w = performRequest(testRouter, "PUT", fmt.Sprintf("/api/v1/devices/%s", device.ID), `{"name":"new"}`, token)
	if w.Code == 200 {
		t.Error("should not be able to update decommissioned device")
	}

	// Verify device not in normal list
	w = performRequest(testRouter, "GET", "/api/v1/devices", "", token)
	resp := assertSuccess(t, w, 200)

	var deviceList []*model.Device
	dataAs(t, resp, &deviceList)
	for _, d := range deviceList {
		if d.ID == device.ID {
			t.Error("decommissioned device should not appear in list")
		}
	}
}
