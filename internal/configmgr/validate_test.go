package configmgr

import (
	"encoding/json"
	"testing"
)

func TestValidateConfig_ValidMinimal(t *testing.T) {
	config := json.RawMessage(`{
		"system": {"hostname": "test-ap", "timezone": "UTC"},
		"wireless": [{
			"band": "5g",
			"channel": 36,
			"channel_width": 80,
			"country": "US",
			"ssids": [{
				"name": "TestWiFi",
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

	result := ValidateConfig(config, nil)
	if !result.Valid {
		t.Errorf("expected valid config, got errors: %+v", result.Errors)
	}
}

func TestValidateConfig_InvalidJSON(t *testing.T) {
	config := json.RawMessage(`{not valid json}`)
	result := ValidateConfig(config, nil)
	if result.Valid {
		t.Error("expected invalid for bad JSON")
	}
	if len(result.Errors) == 0 {
		t.Error("expected error for bad JSON")
	}
}

func TestValidateConfig_InvalidBand(t *testing.T) {
	config := json.RawMessage(`{
		"wireless": [{"band": "7g", "ssids": []}],
		"network": {"management_vlan": 1}
	}`)

	result := ValidateConfig(config, nil)
	if result.Valid {
		t.Error("expected invalid for bad band")
	}
	assertHasError(t, result, "wireless[0].band")
}

func TestValidateConfig_SSIDNameTooLong(t *testing.T) {
	config := json.RawMessage(`{
		"wireless": [{"band": "5g", "ssids": [
			{"name": "ThisSSIDNameIsWayTooLongForTheStandard123", "security": {"mode": "open"}}
		]}],
		"network": {"management_vlan": 1}
	}`)

	result := ValidateConfig(config, nil)
	if result.Valid {
		t.Error("expected invalid for long SSID name")
	}
	assertHasError(t, result, "wireless[0].ssids[0].name")
}

func TestValidateConfig_PSKTooShort(t *testing.T) {
	config := json.RawMessage(`{
		"wireless": [{"band": "5g", "ssids": [
			{"name": "Test", "security": {"mode": "wpa2-psk", "passphrase": "short"}}
		]}],
		"network": {"management_vlan": 1}
	}`)

	result := ValidateConfig(config, nil)
	if result.Valid {
		t.Error("expected invalid for short passphrase")
	}
	assertHasError(t, result, "wireless[0].ssids[0].security.passphrase")
}

func TestValidateConfig_PSKMissing(t *testing.T) {
	config := json.RawMessage(`{
		"wireless": [{"band": "5g", "ssids": [
			{"name": "Test", "security": {"mode": "wpa2-psk"}}
		]}],
		"network": {"management_vlan": 1}
	}`)

	result := ValidateConfig(config, nil)
	if result.Valid {
		t.Error("expected invalid for missing passphrase")
	}
	assertHasError(t, result, "wireless[0].ssids[0].security.passphrase")
}

func TestValidateConfig_EnterpriseNoRadius(t *testing.T) {
	config := json.RawMessage(`{
		"wireless": [{"band": "5g", "ssids": [
			{"name": "Corp", "security": {"mode": "wpa2-enterprise"}}
		]}],
		"network": {"management_vlan": 1}
	}`)

	result := ValidateConfig(config, nil)
	if result.Valid {
		t.Error("expected invalid for enterprise without RADIUS")
	}
	assertHasError(t, result, "wireless[0].ssids[0].security.radius")
}

func TestValidateConfig_InvalidChannel2G(t *testing.T) {
	config := json.RawMessage(`{
		"wireless": [{"band": "2g", "channel": 15, "ssids": [
			{"name": "Test", "security": {"mode": "open"}}
		]}],
		"network": {"management_vlan": 1}
	}`)

	result := ValidateConfig(config, nil)
	if result.Valid {
		t.Error("expected invalid for channel 15 on 2.4GHz")
	}
	assertHasError(t, result, "wireless[0].channel")
}

func TestValidateConfig_InvalidChannel5G(t *testing.T) {
	config := json.RawMessage(`{
		"wireless": [{"band": "5g", "channel": 37, "ssids": [
			{"name": "Test", "security": {"mode": "open"}}
		]}],
		"network": {"management_vlan": 1}
	}`)

	result := ValidateConfig(config, nil)
	if result.Valid {
		t.Error("expected invalid for channel 37 on 5GHz")
	}
	assertHasError(t, result, "wireless[0].channel")
}

func TestValidateConfig_DFSWarning(t *testing.T) {
	config := json.RawMessage(`{
		"wireless": [{"band": "5g", "channel": 52, "ssids": [
			{"name": "Test", "security": {"mode": "open"}}
		]}],
		"network": {"management_vlan": 1}
	}`)

	result := ValidateConfig(config, nil)
	// DFS should be a warning, not error
	if !result.Valid {
		// Only fail if there are schema/semantic errors (not just DFS warning)
		for _, e := range result.Errors {
			if e.Field == "wireless[0].channel" {
				t.Error("DFS channel should be a warning, not error")
			}
		}
	}
	assertHasWarning(t, result, "wireless[0].channel")
}

func TestValidateConfig_ChannelWidthTooWideFor2G(t *testing.T) {
	config := json.RawMessage(`{
		"wireless": [{"band": "2g", "channel": 6, "channel_width": 80, "ssids": [
			{"name": "Test", "security": {"mode": "open"}}
		]}],
		"network": {"management_vlan": 1}
	}`)

	result := ValidateConfig(config, nil)
	if result.Valid {
		t.Error("expected invalid for 80MHz on 2.4GHz")
	}
	assertHasError(t, result, "wireless[0].channel_width")
}

func TestValidateConfig_DuplicateSSIDOnSameBand(t *testing.T) {
	config := json.RawMessage(`{
		"wireless": [{"band": "5g", "ssids": [
			{"name": "Corp", "security": {"mode": "open"}},
			{"name": "Corp", "security": {"mode": "open"}}
		]}],
		"network": {"management_vlan": 1}
	}`)

	result := ValidateConfig(config, nil)
	if result.Valid {
		t.Error("expected invalid for duplicate SSIDs on same band")
	}
	assertHasError(t, result, "wireless[0].ssids[1].name")
}

func TestValidateConfig_InvalidVLAN(t *testing.T) {
	config := json.RawMessage(`{
		"wireless": [{"band": "5g", "ssids": [
			{"name": "Test", "vlan": 5000, "security": {"mode": "open"}}
		]}],
		"network": {"management_vlan": 1}
	}`)

	result := ValidateConfig(config, nil)
	if result.Valid {
		t.Error("expected invalid for VLAN 5000")
	}
	assertHasError(t, result, "wireless[0].ssids[0].vlan")
}

func TestValidateConfig_NoManagementInterface(t *testing.T) {
	config := json.RawMessage(`{
		"wireless": [{"band": "5g", "ssids": [
			{"name": "Test", "security": {"mode": "open"}}
		]}],
		"network": {
			"interfaces": [{"name": "guest", "proto": "dhcp"}]
		}
	}`)

	result := ValidateConfig(config, nil)
	if result.Valid {
		t.Error("expected invalid for missing management interface")
	}
	assertHasError(t, result, "network")
}

func TestValidateConfig_ManagementVLANSatisfiesSafety(t *testing.T) {
	config := json.RawMessage(`{
		"wireless": [{"band": "5g", "ssids": [
			{"name": "Test", "security": {"mode": "open"}}
		]}],
		"network": {
			"management_vlan": 1,
			"interfaces": [{"name": "wan", "proto": "dhcp"}]
		}
	}`)

	result := ValidateConfig(config, nil)
	// Should NOT have safety error about management interface
	for _, e := range result.Errors {
		if e.Layer == "safety" && e.Field == "network" {
			t.Error("management_vlan should satisfy safety check")
		}
	}
}

func TestValidateConfig_CapabilityWPA3Unsupported(t *testing.T) {
	config := json.RawMessage(`{
		"wireless": [{"band": "5g", "ssids": [
			{"name": "Test", "security": {"mode": "wpa3-sae", "passphrase": "testpass123"}}
		]}],
		"network": {"management_vlan": 1}
	}`)

	caps := &DeviceCapabilities{
		Bands:    []string{"2g", "5g"},
		MaxSSIDs: 16,
		WPA3:     false,
	}

	result := ValidateConfig(config, caps)
	if result.Valid {
		t.Error("expected invalid for WPA3 on non-WPA3 device")
	}
	assertHasError(t, result, "wireless[0].ssids[0].security.mode")
}

func TestValidateConfig_CapabilityBandUnsupported(t *testing.T) {
	config := json.RawMessage(`{
		"wireless": [{"band": "6g", "ssids": [
			{"name": "Test", "security": {"mode": "open"}}
		]}],
		"network": {"management_vlan": 1}
	}`)

	caps := &DeviceCapabilities{
		Bands:    []string{"2g", "5g"},
		MaxSSIDs: 16,
	}

	result := ValidateConfig(config, caps)
	if result.Valid {
		t.Error("expected invalid for 6GHz on 2g/5g device")
	}
	assertHasError(t, result, "wireless[0].band")
}

func TestValidateConfig_CapabilityMaxSSIDsExceeded(t *testing.T) {
	config := json.RawMessage(`{
		"wireless": [{"band": "5g", "ssids": [
			{"name": "SSID1", "security": {"mode": "open"}},
			{"name": "SSID2", "security": {"mode": "open"}},
			{"name": "SSID3", "security": {"mode": "open"}}
		]}],
		"network": {"management_vlan": 1}
	}`)

	caps := &DeviceCapabilities{
		Bands:    []string{"5g"},
		MaxSSIDs: 2,
		WPA3:     true,
	}

	result := ValidateConfig(config, caps)
	if result.Valid {
		t.Error("expected invalid for too many SSIDs")
	}
	assertHasError(t, result, "wireless.ssids")
}

func TestValidateConfig_StaticWithoutAddress(t *testing.T) {
	config := json.RawMessage(`{
		"wireless": [{"band": "5g", "ssids": [
			{"name": "Test", "security": {"mode": "open"}}
		]}],
		"network": {
			"management_vlan": 1,
			"interfaces": [{"name": "mgmt", "proto": "static"}]
		}
	}`)

	result := ValidateConfig(config, nil)
	if result.Valid {
		t.Error("expected invalid for static without address")
	}
	assertHasError(t, result, "network.interfaces[0].address")
	assertHasError(t, result, "network.interfaces[0].netmask")
}

func TestValidateConfig_ValidEnterprise(t *testing.T) {
	config := json.RawMessage(`{
		"wireless": [{"band": "5g", "ssids": [{
			"name": "Corp",
			"security": {
				"mode": "wpa2-enterprise",
				"radius": {
					"auth_server": "10.0.0.1",
					"auth_port": 1812,
					"auth_secret": "radiussecret"
				}
			}
		}]}],
		"network": {
			"management_vlan": 1,
			"interfaces": [{"name": "mgmt", "proto": "dhcp"}],
			"dns": ["8.8.8.8"]
		}
	}`)

	result := ValidateConfig(config, nil)
	if !result.Valid {
		t.Errorf("expected valid enterprise config, got errors: %+v", result.Errors)
	}
}

// ============================================================
// TEST HELPERS
// ============================================================

func assertHasError(t *testing.T, result *ConfigValidationResult, field string) {
	t.Helper()
	for _, e := range result.Errors {
		if e.Field == field {
			return
		}
	}
	t.Errorf("expected error on field '%s', but none found. Errors: %+v", field, result.Errors)
}

func assertHasWarning(t *testing.T, result *ConfigValidationResult, field string) {
	t.Helper()
	for _, w := range result.Warnings {
		if w.Field == field {
			return
		}
	}
	t.Errorf("expected warning on field '%s', but none found. Warnings: %+v", field, result.Warnings)
}
