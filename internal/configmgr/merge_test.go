package configmgr

import (
	"encoding/json"
	"testing"

	"github.com/yourorg/cloudctrl/internal/model"
)

func TestDeepMerge_ObjectsMerge(t *testing.T) {
	base := json.RawMessage(`{"system":{"hostname":"ap","timezone":"UTC"},"network":{"dns":["8.8.8.8"]}}`)
	override := json.RawMessage(`{"system":{"hostname":"ap-lobby-1"}}`)

	result, err := DeepMerge(base, override)
	if err != nil {
		t.Fatalf("merge failed: %v", err)
	}

	var m map[string]interface{}
	if err := json.Unmarshal(result, &m); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	system := m["system"].(map[string]interface{})
	if system["hostname"] != "ap-lobby-1" {
		t.Errorf("expected hostname 'ap-lobby-1', got '%v'", system["hostname"])
	}
	if system["timezone"] != "UTC" {
		t.Errorf("expected timezone 'UTC', got '%v'", system["timezone"])
	}

	// network should be preserved
	if _, ok := m["network"]; !ok {
		t.Error("network key should be preserved from base")
	}
}

func TestDeepMerge_ArrayReplacesEntirely(t *testing.T) {
	base := json.RawMessage(`{"dns":["8.8.8.8"]}`)
	override := json.RawMessage(`{"dns":["1.1.1.1","8.8.4.4"]}`)

	result, err := DeepMerge(base, override)
	if err != nil {
		t.Fatalf("merge failed: %v", err)
	}

	var m map[string]interface{}
	json.Unmarshal(result, &m)

	dns := m["dns"].([]interface{})
	if len(dns) != 2 {
		t.Fatalf("expected 2 DNS entries, got %d", len(dns))
	}
	if dns[0] != "1.1.1.1" {
		t.Errorf("expected first DNS 1.1.1.1, got %v", dns[0])
	}
	if dns[1] != "8.8.4.4" {
		t.Errorf("expected second DNS 8.8.4.4, got %v", dns[1])
	}
}

func TestDeepMerge_ScalarReplace(t *testing.T) {
	base := json.RawMessage(`{"channel":"auto"}`)
	override := json.RawMessage(`{"channel":36}`)

	result, err := DeepMerge(base, override)
	if err != nil {
		t.Fatalf("merge failed: %v", err)
	}

	var m map[string]interface{}
	json.Unmarshal(result, &m)

	// JSON numbers unmarshal as float64
	if m["channel"] != float64(36) {
		t.Errorf("expected channel 36, got %v", m["channel"])
	}
}

func TestDeepMerge_NullRemovesField(t *testing.T) {
	base := json.RawMessage(`{"rate_limit":{"down":50000},"name":"test"}`)
	override := json.RawMessage(`{"rate_limit":null}`)

	result, err := DeepMerge(base, override)
	if err != nil {
		t.Fatalf("merge failed: %v", err)
	}

	var m map[string]interface{}
	json.Unmarshal(result, &m)

	if _, exists := m["rate_limit"]; exists {
		t.Error("rate_limit should be removed by null override")
	}
	if m["name"] != "test" {
		t.Error("name should be preserved")
	}
}

func TestDeepMerge_SSIDMergeByName(t *testing.T) {
	base := json.RawMessage(`{
		"wireless": [{
			"band": "5g",
			"ssids": [
				{"name": "Corp", "vlan": 100, "enabled": true},
				{"name": "Guest", "vlan": 200, "enabled": true}
			]
		}]
	}`)
	// Override changes Corp SSID channel, doesn't touch Guest
	override := json.RawMessage(`{
		"wireless": [{
			"band": "5g",
			"ssids": [
				{"name": "Corp", "hidden": true}
			]
		}]
	}`)

	result, err := DeepMerge(base, override)
	if err != nil {
		t.Fatalf("merge failed: %v", err)
	}

	var m map[string]interface{}
	json.Unmarshal(result, &m)

	wireless := m["wireless"].([]interface{})
	radio := wireless[0].(map[string]interface{})
	ssids := radio["ssids"].([]interface{})

	if len(ssids) != 2 {
		t.Fatalf("expected 2 SSIDs, got %d", len(ssids))
	}

	// Corp should be merged
	corp := ssids[0].(map[string]interface{})
	if corp["name"] != "Corp" {
		t.Errorf("expected first SSID 'Corp', got '%v'", corp["name"])
	}
	if corp["hidden"] != true {
		t.Error("Corp should have hidden=true from override")
	}
	if corp["vlan"] != float64(100) {
		t.Error("Corp should retain vlan=100 from base")
	}
	if corp["enabled"] != true {
		t.Error("Corp should retain enabled=true from base")
	}

	// Guest should be unchanged
	guest := ssids[1].(map[string]interface{})
	if guest["name"] != "Guest" {
		t.Errorf("expected second SSID 'Guest', got '%v'", guest["name"])
	}
	if guest["vlan"] != float64(200) {
		t.Error("Guest should retain vlan=200")
	}
}

func TestDeepMerge_EmptyOverride(t *testing.T) {
	base := json.RawMessage(`{"system":{"hostname":"test"}}`)

	result, err := DeepMerge(base, nil)
	if err != nil {
		t.Fatalf("merge failed: %v", err)
	}

	if string(result) != string(base) {
		t.Error("nil override should return base unchanged")
	}

	result2, err := DeepMerge(base, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("merge failed: %v", err)
	}

	var m map[string]interface{}
	json.Unmarshal(result2, &m)
	system := m["system"].(map[string]interface{})
	if system["hostname"] != "test" {
		t.Error("empty override should not change base")
	}
}

func TestDeepMerge_NewKeysAdded(t *testing.T) {
	base := json.RawMessage(`{"existing":"value"}`)
	override := json.RawMessage(`{"new_key":"new_value"}`)

	result, err := DeepMerge(base, override)
	if err != nil {
		t.Fatalf("merge failed: %v", err)
	}

	var m map[string]interface{}
	json.Unmarshal(result, &m)

	if m["existing"] != "value" {
		t.Error("existing key should be preserved")
	}
	if m["new_key"] != "new_value" {
		t.Error("new key should be added from override")
	}
}

func TestDeepMerge_DeepNested(t *testing.T) {
	base := json.RawMessage(`{
		"a": {
			"b": {
				"c": "original",
				"d": "keep"
			},
			"e": "keep"
		}
	}`)
	override := json.RawMessage(`{
		"a": {
			"b": {
				"c": "changed"
			}
		}
	}`)

	result, err := DeepMerge(base, override)
	if err != nil {
		t.Fatalf("merge failed: %v", err)
	}

	var m map[string]interface{}
	json.Unmarshal(result, &m)

	a := m["a"].(map[string]interface{})
	b := a["b"].(map[string]interface{})

	if b["c"] != "changed" {
		t.Errorf("expected c='changed', got '%v'", b["c"])
	}
	if b["d"] != "keep" {
		t.Errorf("expected d='keep', got '%v'", b["d"])
	}
	if a["e"] != "keep" {
		t.Errorf("expected e='keep', got '%v'", a["e"])
	}
}

func TestSubstituteVariables(t *testing.T) {
	config := json.RawMessage(`{
		"system": {
			"hostname": "{{site.name}}-{{device.name}}",
			"country": "{{site.country}}"
		}
	}`)

	vc := &VariableContext{
		DeviceName:  "ap-lobby-1",
		DeviceMAC:   "AA:BB:CC:DD:EE:FF",
		SiteName:    "HQ-Building-A",
		SiteCountry: "US",
	}

	result, err := SubstituteVariables(config, vc)
	if err != nil {
		t.Fatalf("substitution failed: %v", err)
	}

	var m map[string]interface{}
	json.Unmarshal(result, &m)

	system := m["system"].(map[string]interface{})
	if system["hostname"] != "HQ-Building-A-ap-lobby-1" {
		t.Errorf("expected 'HQ-Building-A-ap-lobby-1', got '%v'", system["hostname"])
	}
	if system["country"] != "US" {
		t.Errorf("expected 'US', got '%v'", system["country"])
	}
}

func TestSubstituteVariables_SpecialChars(t *testing.T) {
	config := json.RawMessage(`{"name":"{{device.name}}"}`)

	vc := &VariableContext{
		DeviceName: `ap "lobby" 1`,
	}

	result, err := SubstituteVariables(config, vc)
	if err != nil {
		t.Fatalf("substitution failed: %v", err)
	}

	// Should be valid JSON with properly escaped quotes
	if !json.Valid(result) {
		t.Errorf("result is not valid JSON: %s", string(result))
	}

	var m map[string]interface{}
	if err := json.Unmarshal(result, &m); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if m["name"] != `ap "lobby" 1` {
		t.Errorf("unexpected name: %v", m["name"])
	}
}

func TestSubstituteVariables_NoVariables(t *testing.T) {
	config := json.RawMessage(`{"hostname":"static-name"}`)
	vc := &VariableContext{DeviceName: "ap-1"}

	result, err := SubstituteVariables(config, vc)
	if err != nil {
		t.Fatalf("substitution failed: %v", err)
	}

	var m map[string]interface{}
	json.Unmarshal(result, &m)
	if m["hostname"] != "static-name" {
		t.Error("static values should not be changed")
	}
}

func TestSubstituteVariables_NilContext(t *testing.T) {
	config := json.RawMessage(`{"hostname":"{{device.name}}"}`)

	result, err := SubstituteVariables(config, nil)
	if err != nil {
		t.Fatalf("substitution failed: %v", err)
	}

	// Should return unchanged
	if string(result) != string(config) {
		t.Error("nil context should return config unchanged")
	}
}

func TestGenerateDeviceConfig_Full(t *testing.T) {
	template := json.RawMessage(`{
		"system": {
			"hostname": "{{site.name}}-{{device.name}}",
			"timezone": "{{site.timezone}}"
		},
		"wireless": [{
			"band": "5g",
			"channel": "auto",
			"ssids": [
				{"name": "Corp", "vlan": 100, "enabled": true},
				{"name": "Guest", "vlan": 200, "enabled": true}
			]
		}],
		"network": {
			"dns": ["8.8.8.8"]
		}
	}`)

	overrides := json.RawMessage(`{
		"wireless": [{
			"band": "5g",
			"channel": 36,
			"ssids": [
				{"name": "Corp", "hidden": true}
			]
		}],
		"network": {
			"dns": ["1.1.1.1", "8.8.4.4"]
		}
	}`)

	device := &model.Device{
		Name:   "ap-lobby",
		MAC:    "AA:BB:CC:DD:EE:FF",
		Serial: "SN-001",
	}
	site := &model.Site{
		Name:        "HQ",
		CountryCode: "US",
		Timezone:    "America/New_York",
	}

	result, err := GenerateDeviceConfig(template, overrides, device, site)
	if err != nil {
		t.Fatalf("generate failed: %v", err)
	}

	var m map[string]interface{}
	if err := json.Unmarshal(result, &m); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	// Check variable substitution
	system := m["system"].(map[string]interface{})
	if system["hostname"] != "HQ-ap-lobby" {
		t.Errorf("hostname: expected 'HQ-ap-lobby', got '%v'", system["hostname"])
	}
	if system["timezone"] != "America/New_York" {
		t.Errorf("timezone: expected 'America/New_York', got '%v'", system["timezone"])
	}

	// Check channel override
	wireless := m["wireless"].([]interface{})
	radio := wireless[0].(map[string]interface{})
	if radio["channel"] != float64(36) {
		t.Errorf("channel: expected 36, got %v", radio["channel"])
	}

	// Check SSID merge
	ssids := radio["ssids"].([]interface{})
	if len(ssids) != 2 {
		t.Fatalf("expected 2 SSIDs, got %d", len(ssids))
	}
	corp := ssids[0].(map[string]interface{})
	if corp["hidden"] != true {
		t.Error("Corp should have hidden=true")
	}
	if corp["vlan"] != float64(100) {
		t.Error("Corp should retain vlan=100")
	}

	// Check DNS override (array replace)
	network := m["network"].(map[string]interface{})
	dns := network["dns"].([]interface{})
	if len(dns) != 2 || dns[0] != "1.1.1.1" {
		t.Errorf("DNS should be overridden to [1.1.1.1, 8.8.4.4], got %v", dns)
	}
}

func TestGenerateDeviceConfig_NoOverrides(t *testing.T) {
	template := json.RawMessage(`{"system":{"hostname":"{{device.name}}"}}`)

	device := &model.Device{Name: "ap-1"}
	site := &model.Site{Name: "HQ", CountryCode: "US", Timezone: "UTC"}

	result, err := GenerateDeviceConfig(template, nil, device, site)
	if err != nil {
		t.Fatalf("generate failed: %v", err)
	}

	var m map[string]interface{}
	json.Unmarshal(result, &m)
	system := m["system"].(map[string]interface{})
	if system["hostname"] != "ap-1" {
		t.Errorf("expected 'ap-1', got '%v'", system["hostname"])
	}
}
