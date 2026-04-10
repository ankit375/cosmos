//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yourorg/cloudctrl/internal/configmgr"
	"github.com/yourorg/cloudctrl/internal/model"
	"github.com/yourorg/cloudctrl/internal/protocol"
)

// ============================================================
// CONFIG TEST HELPERS
// ============================================================

func validTestConfig() json.RawMessage {
	return json.RawMessage(`{
		"system": {
			"hostname": "{{site.name}}-{{device.name}}",
			"timezone": "{{site.timezone}}"
		},
		"wireless": [{
			"band": "5g",
			"channel": 36,
			"channel_width": 80,
			"country": "US",
			"ssids": [{
				"name": "TestCorp",
				"enabled": true,
				"security": {"mode": "wpa2-psk", "passphrase": "testpass123"}
			}]
		}],
		"network": {
			"management_vlan": 1,
			"interfaces": [{"name": "mgmt", "proto": "dhcp"}],
			"dns": ["8.8.8.8"]
		}
	}`)
}

func invalidTestConfig_NoManagement() json.RawMessage {
	return json.RawMessage(`{
		"wireless": [{"band": "5g", "ssids": [
			{"name": "Test", "security": {"mode": "open"}}
		]}],
		"network": {
			"interfaces": [{"name": "guest", "proto": "dhcp"}]
		}
	}`)
}

func invalidTestConfig_BadChannel() json.RawMessage {
	return json.RawMessage(`{
		"wireless": [{"band": "2g", "channel": 15, "ssids": [
			{"name": "Test", "security": {"mode": "open"}}
		]}],
		"network": {"management_vlan": 1}
	}`)
}

func overrideTestConfig() json.RawMessage {
	return json.RawMessage(`{
		"wireless": [{
			"band": "5g",
			"channel": 44,
			"ssids": [{"name": "TestCorp", "hidden": true}]
		}]
	}`)
}

// mockDeviceSender implements configmgr.DeviceSender for tests.
type mockDeviceSender struct {
	connected map[uuid.UUID]bool
	messages  []mockSentMessage
}

type mockSentMessage struct {
	DeviceID uuid.UUID
	Channel  uint8
	MsgType  uint16
	Flags    uint8
	Payload  interface{}
}

func newMockSender() *mockDeviceSender {
	return &mockDeviceSender{
		connected: make(map[uuid.UUID]bool),
	}
}

func (m *mockDeviceSender) SendMessage(deviceID uuid.UUID, channel uint8, msgType uint16, flags uint8, payload interface{}) (uint32, error) {
	m.messages = append(m.messages, mockSentMessage{
		DeviceID: deviceID,
		Channel:  channel,
		MsgType:  msgType,
		Flags:    flags,
		Payload:  payload,
	})
	return uint32(len(m.messages)), nil
}

func (m *mockDeviceSender) IsConnected(deviceID uuid.UUID) bool {
	return m.connected[deviceID]
}

func (m *mockDeviceSender) setConnected(deviceID uuid.UUID, val bool) {
	m.connected[deviceID] = val
}

func (m *mockDeviceSender) messagesForDevice(deviceID uuid.UUID) []mockSentMessage {
	var result []mockSentMessage
	for _, msg := range m.messages {
		if msg.DeviceID == deviceID {
			result = append(result, msg)
		}
	}
	return result
}

func (m *mockDeviceSender) reset() {
	m.messages = nil
}

// ============================================================
// TEST 1: Template creation and versioning
// ============================================================

func TestConfig_TemplateCreationAndVersioning(t *testing.T) {
	tenant := seedTenant(t, "cfg-tmpl")
	defer cleanupTenant(t, tenant.ID)

	site := seedSite(t, tenant.ID, "Config Test Site")
	ctx := context.Background()

	// Create first template
	t1, err := testPG.Configs.CreateTemplate(ctx, tenant.ID, site.ID, validTestConfig(), "Initial config", nil)
	if err != nil {
		t.Fatalf("create template v1: %v", err)
	}
	if t1.Version != 1 {
		t.Errorf("expected version 1, got %d", t1.Version)
	}

	// Create second template — auto-increments
	cfg2 := json.RawMessage(`{"system":{"hostname":"updated"}, "network":{"management_vlan":1,"interfaces":[{"name":"mgmt","proto":"dhcp"}]}}`)
	t2, err := testPG.Configs.CreateTemplate(ctx, tenant.ID, site.ID, cfg2, "Updated config", nil)
	if err != nil {
		t.Fatalf("create template v2: %v", err)
	}
	if t2.Version != 2 {
		t.Errorf("expected version 2, got %d", t2.Version)
	}

	// Get latest should return v2
	latest, err := testPG.Configs.GetLatestTemplate(ctx, tenant.ID, site.ID)
	if err != nil {
		t.Fatalf("get latest: %v", err)
	}
	if latest.Version != 2 {
		t.Errorf("latest should be v2, got %d", latest.Version)
	}

	// Get by version
	v1, err := testPG.Configs.GetTemplateByVersion(ctx, tenant.ID, site.ID, 1)
	if err != nil || v1 == nil {
		t.Fatalf("get v1: %v", err)
	}
	if v1.Description != "Initial config" {
		t.Errorf("v1 description mismatch: %s", v1.Description)
	}

	// History
	history, total, err := testPG.Configs.ListTemplateHistory(ctx, tenant.ID, site.ID, 10, 0)
	if err != nil {
		t.Fatalf("list history: %v", err)
	}
	if total != 2 {
		t.Errorf("expected 2 total, got %d", total)
	}
	if len(history) != 2 || history[0].Version != 2 {
		t.Error("history should be ordered desc by version")
	}
}

// ============================================================
// TEST 2: Config merge correctness via manager
// ============================================================

func TestConfig_DeviceOverrideMergeCorrectness(t *testing.T) {
	tenant := seedTenant(t, "cfg-merge")
	defer cleanupTenant(t, tenant.ID)

	site := seedSite(t, tenant.ID, "Merge Test Site")
	device := seedAdoptedDevice(t, tenant.ID, site.ID, "merge-ap")
	ctx := context.Background()

	sender := newMockSender()
	sender.setConnected(device.ID, true)

	mgr := configmgr.NewManager(testPG, sender, configmgr.DefaultManagerConfig(), newTestLogger())

	// Create site template
	_, err := testPG.Configs.CreateTemplate(ctx, tenant.ID, site.ID, validTestConfig(), "base", nil)
	if err != nil {
		t.Fatalf("create template: %v", err)
	}

	// Set device overrides
	dc, validationResult, err := mgr.UpdateDeviceOverrides(ctx, tenant.ID, device.ID, overrideTestConfig(), nil)
	if err != nil {
		t.Fatalf("update overrides: %v", err)
	}
	if validationResult != nil && validationResult.HasErrors() {
		t.Fatalf("validation errors: %+v", validationResult.Errors)
	}

	// Verify merged config
	var merged map[string]interface{}
	if err := json.Unmarshal(dc.Config, &merged); err != nil {
		t.Fatalf("unmarshal merged: %v", err)
	}

	wireless := merged["wireless"].([]interface{})
	radio := wireless[0].(map[string]interface{})
	if radio["channel"] != float64(44) {
		t.Errorf("channel should be overridden to 44, got %v", radio["channel"])
	}

	ssids := radio["ssids"].([]interface{})
	corp := ssids[0].(map[string]interface{})
	if corp["hidden"] != true {
		t.Error("SSID should have hidden=true from override")
	}
	security := corp["security"].(map[string]interface{})
	if security["mode"] != "wpa2-psk" {
		t.Error("security mode should be preserved from template")
	}
}

// ============================================================
// TEST 3: Validation catches invalid configs
// ============================================================

func TestConfig_ValidationCatchesInvalidConfigs(t *testing.T) {
	tenant := seedTenant(t, "cfg-validate")
	defer cleanupTenant(t, tenant.ID)

	site := seedSite(t, tenant.ID, "Validate Test Site")
	ctx := context.Background()

	sender := newMockSender()
	mgr := configmgr.NewManager(testPG, sender, configmgr.DefaultManagerConfig(), newTestLogger())

	// Missing management interface
	template, result, err := mgr.UpdateSiteConfig(ctx, tenant.ID, site.ID,
		invalidTestConfig_NoManagement(), "bad config", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if template != nil {
		t.Error("template should be nil when validation fails")
	}
	if result == nil || !result.HasErrors() {
		t.Error("expected validation errors")
	}

	// Invalid channel
	template2, result2, err := mgr.UpdateSiteConfig(ctx, tenant.ID, site.ID,
		invalidTestConfig_BadChannel(), "bad channel", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if template2 != nil {
		t.Error("template should be nil when validation fails")
	}
	if result2 == nil || !result2.HasErrors() {
		t.Error("expected validation errors for bad channel")
	}

	// Verify nothing stored
	latest, _ := testPG.Configs.GetLatestTemplate(ctx, tenant.ID, site.ID)
	if latest != nil {
		t.Error("no template should be stored when validation fails")
	}
}

// ============================================================
// TEST 4: Safe-apply success flow
// ============================================================

func TestConfig_SafeApply_SuccessFlow(t *testing.T) {
	tenant := seedTenant(t, "cfg-safeapply")
	defer cleanupTenant(t, tenant.ID)

	site := seedSite(t, tenant.ID, "SafeApply Test Site")
	device := seedAdoptedDevice(t, tenant.ID, site.ID, "safeapply-ap")
	ctx := context.Background()

	sender := newMockSender()
	sender.setConnected(device.ID, true)

	cfg := configmgr.ManagerConfig{
		SafeApplyTimeout:  10 * time.Second,
		StabilityDelay:    100 * time.Millisecond,
		ReconcileInterval: 1 * time.Hour,
	}
	mgr := configmgr.NewManager(testPG, sender, cfg, newTestLogger())
	mgr.Start()
	defer mgr.Stop()

	// Push config
	_, _, err := mgr.UpdateSiteConfig(ctx, tenant.ID, site.ID, validTestConfig(), "test", nil)
	if err != nil {
		t.Fatalf("update site config: %v", err)
	}

	time.Sleep(300 * time.Millisecond)

	// Verify ConfigPush was sent
	msgs := sender.messagesForDevice(device.ID)
	if len(msgs) == 0 {
		t.Fatal("expected ConfigPush message")
	}
	if msgs[0].MsgType != protocol.MsgConfigPush {
		t.Errorf("expected MsgConfigPush, got %d", msgs[0].MsgType)
	}

	// Simulate ACK success
	mgr.HandleConfigAck(device.ID, tenant.ID, &protocol.ConfigAckPayload{
		Version: 1,
		Success: true,
	})

	// Wait for stability + confirm
	time.Sleep(500 * time.Millisecond)

	// Verify ConfigConfirm sent
	allMsgs := sender.messagesForDevice(device.ID)
	foundConfirm := false
	for _, msg := range allMsgs {
		if msg.MsgType == protocol.MsgConfigConfirm {
			foundConfirm = true
		}
	}
	if !foundConfirm {
		t.Error("expected ConfigConfirm after stability delay")
	}

	// Verify DB status
	dc, _ := testPG.Configs.GetLatestDeviceConfig(ctx, tenant.ID, device.ID)
	if dc == nil {
		t.Fatal("expected device config in DB")
	}
	if dc.Status != model.ConfigStatusApplied {
		t.Errorf("expected status 'applied', got '%s'", dc.Status)
	}
}

// ============================================================
// TEST 5: Safe-apply ACK failure
// ============================================================

func TestConfig_SafeApply_AckFailure(t *testing.T) {
	tenant := seedTenant(t, "cfg-ackfail")
	defer cleanupTenant(t, tenant.ID)

	site := seedSite(t, tenant.ID, "AckFail Test Site")
	device := seedAdoptedDevice(t, tenant.ID, site.ID, "ackfail-ap")
	ctx := context.Background()

	sender := newMockSender()
	sender.setConnected(device.ID, true)

	cfg := configmgr.ManagerConfig{
		SafeApplyTimeout:  10 * time.Second,
		StabilityDelay:    100 * time.Millisecond,
		ReconcileInterval: 1 * time.Hour,
	}
	mgr := configmgr.NewManager(testPG, sender, cfg, newTestLogger())
	mgr.Start()
	defer mgr.Stop()

	_, _, err := mgr.UpdateSiteConfig(ctx, tenant.ID, site.ID, validTestConfig(), "test", nil)
	if err != nil {
		t.Fatalf("update site config: %v", err)
	}

	time.Sleep(300 * time.Millisecond)

	// Simulate ACK failure
	mgr.HandleConfigAck(device.ID, tenant.ID, &protocol.ConfigAckPayload{
		Version: 1,
		Success: false,
		Error:   "UCI validation failed",
	})

	time.Sleep(200 * time.Millisecond)

	dc, _ := testPG.Configs.GetLatestDeviceConfig(ctx, tenant.ID, device.ID)
	if dc == nil {
		t.Fatal("expected device config in DB")
	}
	if dc.Status != model.ConfigStatusFailed {
		t.Errorf("expected status 'failed', got '%s'", dc.Status)
	}

	// No ConfigConfirm should be sent
	for _, msg := range sender.messagesForDevice(device.ID) {
		if msg.MsgType == protocol.MsgConfigConfirm {
			t.Error("ConfigConfirm should NOT be sent on failure")
		}
	}
}

// ============================================================
// TEST 6: Safe-apply timeout
// ============================================================
func TestConfig_SafeApply_Timeout(t *testing.T) {
	tenant := seedTenant(t, "cfg-timeout")
	defer cleanupTenant(t, tenant.ID)

	site := seedSite(t, tenant.ID, "Timeout Test Site")
	device := seedAdoptedDevice(t, tenant.ID, site.ID, "timeout-ap")
	ctx := context.Background()

	sender := newMockSender()
	sender.setConnected(device.ID, true)

	cfg := configmgr.ManagerConfig{
		SafeApplyTimeout:  500 * time.Millisecond,
		StabilityDelay:    100 * time.Millisecond,
		ReconcileInterval: 1 * time.Hour,
	}
	mgr := configmgr.NewManager(testPG, sender, cfg, newTestLogger())
	mgr.Start()
	defer mgr.Stop()

	_, _, err := mgr.UpdateSiteConfig(ctx, tenant.ID, site.ID, validTestConfig(), "test", nil)
	if err != nil {
		t.Fatalf("update site config: %v", err)
	}

	// Don't send ACK — wait for timeout checker to run (runs every 5s)
	time.Sleep(6 * time.Second)

	dc, _ := testPG.Configs.GetLatestDeviceConfig(ctx, tenant.ID, device.ID)
	if dc == nil {
		t.Fatal("expected device config in DB")
	}
	if dc.Status != model.ConfigStatusFailed {
		t.Errorf("expected status 'failed' after timeout, got '%s'", dc.Status)
	}
}


// ============================================================
// TEST 7: Offline device — pushed on reconnect
// ============================================================

func TestConfig_OfflineDevice_PushedOnReconnect(t *testing.T) {
	tenant := seedTenant(t, "cfg-reconnect")
	defer cleanupTenant(t, tenant.ID)

	site := seedSite(t, tenant.ID, "Reconnect Test Site")
	device := seedAdoptedDevice(t, tenant.ID, site.ID, "reconnect-ap")
	ctx := context.Background()

	sender := newMockSender()
	sender.setConnected(device.ID, false) // offline

	cfg := configmgr.ManagerConfig{
		SafeApplyTimeout:  10 * time.Second,
		StabilityDelay:    100 * time.Millisecond,
		ReconcileInterval: 1 * time.Hour,
	}
	mgr := configmgr.NewManager(testPG, sender, cfg, newTestLogger())

	_, _, err := mgr.UpdateSiteConfig(ctx, tenant.ID, site.ID, validTestConfig(), "test", nil)
	if err != nil {
		t.Fatalf("update site config: %v", err)
	}

	time.Sleep(300 * time.Millisecond)

	// No messages (device offline)
	if len(sender.messagesForDevice(device.ID)) > 0 {
		t.Error("should not send messages to offline device")
	}

	// Config should be stored in DB
	dc, _ := testPG.Configs.GetLatestDeviceConfig(ctx, tenant.ID, device.ID)
	if dc == nil {
		t.Fatal("expected device config stored even when offline")
	}

	// Simulate reconnect
	sender.setConnected(device.ID, true)
	mgr.PushPendingConfigOnReconnect(tenant.ID, device.ID)

	time.Sleep(300 * time.Millisecond)

	msgs := sender.messagesForDevice(device.ID)
	if len(msgs) == 0 {
		t.Fatal("expected ConfigPush on reconnect")
	}
	if msgs[0].MsgType != protocol.MsgConfigPush {
		t.Errorf("expected ConfigPush, got %d", msgs[0].MsgType)
	}
}

// ============================================================
// TEST 8: Rollback to previous version
// ============================================================

func TestConfig_RollbackToPreviousVersion(t *testing.T) {
	tenant := seedTenant(t, "cfg-rollback")
	defer cleanupTenant(t, tenant.ID)

	site := seedSite(t, tenant.ID, "Rollback Test Site")
	ctx := context.Background()

	sender := newMockSender()
	mgr := configmgr.NewManager(testPG, sender, configmgr.DefaultManagerConfig(), newTestLogger())

	// Create v1
	_, _, err := mgr.UpdateSiteConfig(ctx, tenant.ID, site.ID, validTestConfig(), "v1", nil)
	if err != nil {
		t.Fatalf("create v1: %v", err)
	}

	// Create v2
	v2Config := json.RawMessage(`{
		"system": {"hostname": "updated", "timezone": "UTC"},
		"wireless": [{"band": "5g", "channel": 44, "channel_width": 80, "country": "US",
			"ssids": [{"name": "NewSSID", "enabled": true, "security": {"mode": "wpa2-psk", "passphrase": "newpass123"}}]
		}],
		"network": {"management_vlan": 1, "interfaces": [{"name": "mgmt", "proto": "dhcp"}], "dns": ["1.1.1.1"]}
	}`)
	_, _, err = mgr.UpdateSiteConfig(ctx, tenant.ID, site.ID, v2Config, "v2", nil)
	if err != nil {
		t.Fatalf("create v2: %v", err)
	}

	// Rollback to v1
	rolled, err := mgr.RollbackSiteConfig(ctx, tenant.ID, site.ID, 1, nil)
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if rolled.Version != 3 {
		t.Errorf("expected rollback to create version 3, got %d", rolled.Version)
	}

	v1, _ := testPG.Configs.GetTemplateByVersion(ctx, tenant.ID, site.ID, 1)
	if string(rolled.Config) != string(v1.Config) {
		t.Error("rolled back config should match v1 content")
	}

	latest, _ := testPG.Configs.GetLatestTemplate(ctx, tenant.ID, site.ID)
	if latest.Version != 3 {
		t.Errorf("latest should be v3, got %d", latest.Version)
	}
}

// ============================================================
// TEST 9: Site-wide push to all devices
// ============================================================

func TestConfig_SiteWidePush(t *testing.T) {
	tenant := seedTenant(t, "cfg-sitewide")
	defer cleanupTenant(t, tenant.ID)

	site := seedSite(t, tenant.ID, "SiteWide Test Site")
	d1 := seedAdoptedDevice(t, tenant.ID, site.ID, "ap-1")
	d2 := seedAdoptedDevice(t, tenant.ID, site.ID, "ap-2")
	d3 := seedAdoptedDevice(t, tenant.ID, site.ID, "ap-3")
	ctx := context.Background()

	sender := newMockSender()
	sender.setConnected(d1.ID, true)
	sender.setConnected(d2.ID, true)
	sender.setConnected(d3.ID, false) // offline

	cfg := configmgr.ManagerConfig{
		SafeApplyTimeout:  10 * time.Second,
		StabilityDelay:    100 * time.Millisecond,
		ReconcileInterval: 1 * time.Hour,
	}
	mgr := configmgr.NewManager(testPG, sender, cfg, newTestLogger())

	_, _, err := mgr.UpdateSiteConfig(ctx, tenant.ID, site.ID, validTestConfig(), "site push", nil)
	if err != nil {
		t.Fatalf("update site config: %v", err)
	}

	time.Sleep(500 * time.Millisecond)

	if len(sender.messagesForDevice(d1.ID)) == 0 {
		t.Error("device 1 (online) should have received ConfigPush")
	}
	if len(sender.messagesForDevice(d2.ID)) == 0 {
		t.Error("device 2 (online) should have received ConfigPush")
	}
	if len(sender.messagesForDevice(d3.ID)) > 0 {
		t.Error("device 3 (offline) should NOT have received ConfigPush")
	}

	// All 3 should have configs in DB
	for _, d := range []*model.Device{d1, d2, d3} {
		dc, _ := testPG.Configs.GetLatestDeviceConfig(ctx, tenant.ID, d.ID)
		if dc == nil {
			t.Errorf("device %s should have config in DB", d.ID)
		}
	}
}

// ============================================================
// TEST 10: Device config rollback
// ============================================================

func TestConfig_DeviceConfigRollback(t *testing.T) {
	tenant := seedTenant(t, "cfg-devroll")
	defer cleanupTenant(t, tenant.ID)

	site := seedSite(t, tenant.ID, "DevRollback Test Site")
	device := seedAdoptedDevice(t, tenant.ID, site.ID, "devroll-ap")
	ctx := context.Background()

	sender := newMockSender()
	sender.setConnected(device.ID, true)

	mgr := configmgr.NewManager(testPG, sender, configmgr.DefaultManagerConfig(), newTestLogger())

	// Create template
	_, err := testPG.Configs.CreateTemplate(ctx, tenant.ID, site.ID, validTestConfig(), "base", nil)
	if err != nil {
		t.Fatalf("create template: %v", err)
	}

	// Generate v1
	dc1, _, err := mgr.GenerateAndPushDeviceConfig(ctx, tenant.ID, device.ID, nil)
	if err != nil {
		t.Fatalf("generate v1: %v", err)
	}
	if dc1.Version != 1 {
		t.Errorf("expected version 1, got %d", dc1.Version)
	}

	// Apply overrides → v2
	_, _, err = mgr.UpdateDeviceOverrides(ctx, tenant.ID, device.ID, overrideTestConfig(), nil)
	if err != nil {
		t.Fatalf("update overrides: %v", err)
	}

	// Rollback to v1 → creates v3
	rolled, err := mgr.RollbackDeviceConfig(ctx, tenant.ID, device.ID, 1, nil)
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if rolled.Version != 3 {
		t.Errorf("expected version 3, got %d", rolled.Version)
	}
	if rolled.Source != model.ConfigSourceRollback {
		t.Errorf("expected source 'rollback', got '%s'", rolled.Source)
	}
}

// ============================================================
// TEST 11-15: API-level integration tests
// ============================================================

func TestConfigAPI_GetSiteConfig_Empty(t *testing.T) {
	tenant := seedTenant(t, "api-cfg-get")
	defer cleanupTenant(t, tenant.ID)

	site := seedSite(t, tenant.ID, "API Config Site")
	user, password := seedUser(t, tenant.ID, model.RoleAdmin)
	login := loginUser(t, user.Email, password)

	w := performRequest(testRouter, "GET",
		fmt.Sprintf("/api/v1/sites/%s/config", site.ID), "", login.AccessToken)

	assertSuccess(t, w, http.StatusOK)
}

func TestConfigAPI_UpdateAndGetSiteConfig(t *testing.T) {
	tenant := seedTenant(t, "api-cfg-update")
	defer cleanupTenant(t, tenant.ID)

	site := seedSite(t, tenant.ID, "API Update Config Site")
	user, password := seedUser(t, tenant.ID, model.RoleAdmin)
	login := loginUser(t, user.Email, password)

	body := fmt.Sprintf(`{"config":%s,"description":"API test"}`, string(validTestConfig()))
	w := performRequest(testRouter, "PUT",
		fmt.Sprintf("/api/v1/sites/%s/config", site.ID), body, login.AccessToken)

	assertSuccess(t, w, http.StatusOK)

	// Verify we can get it back
	w2 := performRequest(testRouter, "GET",
		fmt.Sprintf("/api/v1/sites/%s/config", site.ID), "", login.AccessToken)
	assertSuccess(t, w2, http.StatusOK)
}

func TestConfigAPI_ValidateInvalidConfig(t *testing.T) {
	tenant := seedTenant(t, "api-cfg-val")
	defer cleanupTenant(t, tenant.ID)

	site := seedSite(t, tenant.ID, "API Validate Site")
	user, password := seedUser(t, tenant.ID, model.RoleAdmin)
	login := loginUser(t, user.Email, password)

	body := fmt.Sprintf(`{"config":%s}`, string(invalidTestConfig_NoManagement()))
	w := performRequest(testRouter, "POST",
		fmt.Sprintf("/api/v1/sites/%s/config/validate", site.ID), body, login.AccessToken)

	assertSuccess(t, w, http.StatusOK)

	var data map[string]interface{}
	resp := parseAPIResponse(t, w)
	json.Unmarshal(resp.Data, &data)
	if data["valid"] != false {
		t.Error("expected valid=false for invalid config")
	}
}

func TestConfigAPI_GetDeviceConfig(t *testing.T) {
	tenant := seedTenant(t, "api-cfg-dev")
	defer cleanupTenant(t, tenant.ID)

	site := seedSite(t, tenant.ID, "API Device Config Site")
	device := seedAdoptedDevice(t, tenant.ID, site.ID, "api-dev-cfg")
	user, password := seedUser(t, tenant.ID, model.RoleAdmin)
	login := loginUser(t, user.Email, password)

	w := performRequest(testRouter, "GET",
		fmt.Sprintf("/api/v1/devices/%s/config", device.ID), "", login.AccessToken)

	assertSuccess(t, w, http.StatusOK)
}

func TestConfigAPI_DeviceOverrides_CRUD(t *testing.T) {
	tenant := seedTenant(t, "api-cfg-override")
	defer cleanupTenant(t, tenant.ID)

	site := seedSite(t, tenant.ID, "API Override Site")
	device := seedAdoptedDevice(t, tenant.ID, site.ID, "api-override-ap")
	user, password := seedUser(t, tenant.ID, model.RoleAdmin)
	login := loginUser(t, user.Email, password)

	// Need a template first for overrides to merge against
	_, err := testPG.Configs.CreateTemplate(context.Background(), tenant.ID, site.ID, validTestConfig(), "base", nil)
	if err != nil {
		t.Fatalf("create template: %v", err)
	}

	// PUT overrides
	body := fmt.Sprintf(`{"overrides":%s}`, string(overrideTestConfig()))
	w := performRequest(testRouter, "PUT",
		fmt.Sprintf("/api/v1/devices/%s/config/overrides", device.ID), body, login.AccessToken)
	assertSuccess(t, w, http.StatusOK)

	// GET overrides
	w2 := performRequest(testRouter, "GET",
		fmt.Sprintf("/api/v1/devices/%s/config/overrides", device.ID), "", login.AccessToken)
	assertSuccess(t, w2, http.StatusOK)

	// DELETE overrides
	w3 := performRequest(testRouter, "DELETE",
		fmt.Sprintf("/api/v1/devices/%s/config/overrides", device.ID), "", login.AccessToken)
	assertSuccess(t, w3, http.StatusOK)
}
