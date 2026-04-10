package configmgr

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ============================================================
// VALIDATION ERRORS
// ============================================================

// ValidationSeverity indicates how critical a validation issue is.
type ValidationSeverity string

const (
	SeverityError   ValidationSeverity = "error"
	SeverityWarning ValidationSeverity = "warning"
)

// ConfigValidationError represents a single validation issue.
type ConfigValidationError struct {
	Field    string             `json:"field"`
	Message  string             `json:"message"`
	Severity ValidationSeverity `json:"severity"`
	Layer    string             `json:"layer"` // "schema", "semantic", "safety"
}

// ConfigValidationResult holds all validation results.
type ConfigValidationResult struct {
	Valid    bool                    `json:"valid"`
	Errors   []ConfigValidationError `json:"errors,omitempty"`
	Warnings []ConfigValidationError `json:"warnings,omitempty"`
}

// HasErrors returns true if there are any error-severity issues.
func (r *ConfigValidationResult) HasErrors() bool {
	return len(r.Errors) > 0
}

// AddError adds an error-severity issue.
func (r *ConfigValidationResult) AddError(layer, field, message string) {
	r.Errors = append(r.Errors, ConfigValidationError{
		Field:    field,
		Message:  message,
		Severity: SeverityError,
		Layer:    layer,
	})
	r.Valid = false
}

// AddWarning adds a warning-severity issue.
func (r *ConfigValidationResult) AddWarning(layer, field, message string) {
	r.Warnings = append(r.Warnings, ConfigValidationError{
		Field:    field,
		Message:  message,
		Severity: SeverityWarning,
		Layer:    layer,
	})
}

// ============================================================
// DEVICE CAPABILITIES (for capability validation)
// ============================================================

// DeviceCapabilities represents what a device supports.
type DeviceCapabilities struct {
	Bands       []string `json:"bands"`
	MaxSSIDs    int      `json:"max_ssids"`
	MeshSupport bool     `json:"mesh_support"`
	WPA3        bool     `json:"wpa3"`
	VLAN        bool     `json:"vlan"`
	MaxClients  int      `json:"max_clients"`
}

// ParseCapabilities parses device capabilities from JSON.
func ParseCapabilities(raw json.RawMessage) *DeviceCapabilities {
	if len(raw) == 0 || string(raw) == "{}" || string(raw) == "null" {
		return nil
	}
	var caps DeviceCapabilities
	if err := json.Unmarshal(raw, &caps); err != nil {
		return nil
	}
	return &caps
}

// ============================================================
// MAIN VALIDATION FUNCTION
// ============================================================

// ValidateConfig runs all three validation layers on a config.
// capabilities may be nil (validation will skip capability checks).
func ValidateConfig(config json.RawMessage, capabilities *DeviceCapabilities) *ConfigValidationResult {
	result := &ConfigValidationResult{Valid: true}

	var parsed map[string]interface{}
	if err := json.Unmarshal(config, &parsed); err != nil {
		result.AddError("schema", "config", fmt.Sprintf("invalid JSON: %v", err))
		return result
	}

	// Layer 1: Schema validation
	validateSchema(parsed, result)

	// Layer 2: Semantic validation
	validateSemantics(parsed, capabilities, result)

	// Layer 3: Safety validation
	validateSafety(parsed, result)

	return result
}

// ============================================================
// LAYER 1: SCHEMA VALIDATION
// ============================================================

// Valid security modes
var validSecurityModes = map[string]bool{
	"open":           true,
	"wpa2-psk":       true,
	"wpa3-sae":       true,
	"wpa2-enterprise": true,
	"wpa3-enterprise": true,
	"wpa2/wpa3-psk":  true,
}

// Valid bands
var validBands = map[string]bool{
	"2g": true,
	"5g": true,
	"6g": true,
}

// Valid interface protos
var validProtos = map[string]bool{
	"dhcp":   true,
	"static": true,
	"pppoe":  true,
}

func validateSchema(config map[string]interface{}, result *ConfigValidationResult) {
	// Validate system section
	if system, ok := getMap(config, "system"); ok {
		validateSystemSchema(system, result)
	}

	// Validate wireless section
	if wireless, ok := getSlice(config, "wireless"); ok {
		for i, item := range wireless {
			if radio, ok := item.(map[string]interface{}); ok {
				validateWirelessSchema(radio, i, result)
			} else {
				result.AddError("schema", fmt.Sprintf("wireless[%d]", i), "must be an object")
			}
		}
	}

	// Validate network section
	if network, ok := getMap(config, "network"); ok {
		validateNetworkSchema(network, result)
	}
}

func validateSystemSchema(system map[string]interface{}, result *ConfigValidationResult) {
	// hostname
	if hostname, ok := system["hostname"]; ok {
		if s, ok := hostname.(string); ok {
			if len(s) > 255 {
				result.AddError("schema", "system.hostname", "must be at most 255 characters")
			}
			if len(s) == 0 {
				result.AddError("schema", "system.hostname", "must not be empty")
			}
		}
		// Allow variable placeholders like {{device.name}}
	}

	// timezone
	if tz, ok := system["timezone"]; ok {
		if s, ok := tz.(string); ok {
			if len(s) == 0 {
				result.AddError("schema", "system.timezone", "must not be empty")
			}
		}
	}

	// ntp_servers
	if ntpServers, ok := system["ntp_servers"]; ok {
		if arr, ok := ntpServers.([]interface{}); ok {
			for i, server := range arr {
				if _, ok := server.(string); !ok {
					result.AddError("schema", fmt.Sprintf("system.ntp_servers[%d]", i), "must be a string")
				}
			}
		}
	}
}

func validateWirelessSchema(radio map[string]interface{}, radioIdx int, result *ConfigValidationResult) {
	prefix := fmt.Sprintf("wireless[%d]", radioIdx)

	// band (required)
	band, hasBand := getStringField(radio, "band")
	if !hasBand {
		result.AddError("schema", prefix+".band", "is required")
	} else if !validBands[band] {
		result.AddError("schema", prefix+".band", fmt.Sprintf("must be one of: 2g, 5g, 6g (got '%s')", band))
	}

	// channel_width
	if cw, ok := getNumberField(radio, "channel_width"); ok {
		validWidths := map[float64]bool{20: true, 40: true, 80: true, 160: true}
		if !validWidths[cw] {
			result.AddError("schema", prefix+".channel_width", fmt.Sprintf("must be 20, 40, 80, or 160 (got %v)", cw))
		}
	}

	// country
	if country, ok := getStringField(radio, "country"); ok {
		if len(country) != 2 {
			result.AddError("schema", prefix+".country", "must be a 2-letter country code")
		}
	}

	// SSIDs
	if ssids, ok := getSlice(radio, "ssids"); ok {
		for i, item := range ssids {
			if ssid, ok := item.(map[string]interface{}); ok {
				validateSSIDSchema(ssid, radioIdx, i, band, result)
			} else {
				result.AddError("schema", fmt.Sprintf("%s.ssids[%d]", prefix, i), "must be an object")
			}
		}
	}
}

func validateSSIDSchema(ssid map[string]interface{}, radioIdx, ssidIdx int, band string, result *ConfigValidationResult) {
	prefix := fmt.Sprintf("wireless[%d].ssids[%d]", radioIdx, ssidIdx)

	// name (required)
	name, hasName := getStringField(ssid, "name")
	if !hasName {
		result.AddError("schema", prefix+".name", "is required")
	} else {
		if len(name) == 0 {
			result.AddError("schema", prefix+".name", "must not be empty")
		}
		if len(name) > 32 {
			result.AddError("schema", prefix+".name", "must be at most 32 characters")
		}
	}

	// security
	if security, ok := getMap(ssid, "security"); ok {
		validateSecuritySchema(security, prefix, result)
	}

	// vlan
	if vlan, ok := getNumberField(ssid, "vlan"); ok {
		if vlan < 1 || vlan > 4094 {
			result.AddError("schema", prefix+".vlan", fmt.Sprintf("must be between 1 and 4094 (got %v)", vlan))
		}
	}

	// rate_limit
	if rl, ok := getMap(ssid, "rate_limit"); ok {
		if down, ok := getNumberField(rl, "down_kbps"); ok {
			if down < 0 {
				result.AddError("schema", prefix+".rate_limit.down_kbps", "must be non-negative")
			}
		}
		if up, ok := getNumberField(rl, "up_kbps"); ok {
			if up < 0 {
				result.AddError("schema", prefix+".rate_limit.up_kbps", "must be non-negative")
			}
		}
	}
}

func validateSecuritySchema(security map[string]interface{}, prefix string, result *ConfigValidationResult) {
	mode, hasMode := getStringField(security, "mode")
	if !hasMode {
		result.AddError("schema", prefix+".security.mode", "is required")
		return
	}

	if !validSecurityModes[mode] {
		result.AddError("schema", prefix+".security.mode",
			fmt.Sprintf("must be one of: %s (got '%s')", joinKeys(validSecurityModes), mode))
		return
	}

	// PSK modes require passphrase
	if strings.Contains(mode, "psk") || mode == "wpa3-sae" || mode == "wpa2/wpa3-psk" {
		passphrase, hasPass := getStringField(security, "passphrase")
		if !hasPass || passphrase == "" {
			result.AddError("schema", prefix+".security.passphrase", "is required for PSK/SAE modes")
		} else {
			if len(passphrase) < 8 {
				result.AddError("schema", prefix+".security.passphrase", "must be at least 8 characters")
			}
			if len(passphrase) > 63 {
				result.AddError("schema", prefix+".security.passphrase", "must be at most 63 characters")
			}
		}
	}

	// Enterprise modes require RADIUS config
	if strings.Contains(mode, "enterprise") {
		radius, hasRadius := getMap(security, "radius")
		if !hasRadius {
			result.AddError("schema", prefix+".security.radius", "is required for enterprise modes")
		} else {
			if _, ok := getStringField(radius, "auth_server"); !ok {
				result.AddError("schema", prefix+".security.radius.auth_server", "is required")
			}
			if _, ok := getNumberField(radius, "auth_port"); !ok {
				result.AddError("schema", prefix+".security.radius.auth_port", "is required")
			}
			if _, ok := getStringField(radius, "auth_secret"); !ok {
				result.AddError("schema", prefix+".security.radius.auth_secret", "is required")
			}
		}
	}
}

func validateNetworkSchema(network map[string]interface{}, result *ConfigValidationResult) {
	// management_vlan
	if vlan, ok := getNumberField(network, "management_vlan"); ok {
		if vlan < 1 || vlan > 4094 {
			result.AddError("schema", "network.management_vlan", fmt.Sprintf("must be between 1 and 4094 (got %v)", vlan))
		}
	}

	// interfaces
	if interfaces, ok := getSlice(network, "interfaces"); ok {
		for i, item := range interfaces {
			if iface, ok := item.(map[string]interface{}); ok {
				prefix := fmt.Sprintf("network.interfaces[%d]", i)

				if _, ok := getStringField(iface, "name"); !ok {
					result.AddError("schema", prefix+".name", "is required")
				}
				if proto, ok := getStringField(iface, "proto"); ok {
					if !validProtos[proto] {
						result.AddError("schema", prefix+".proto",
							fmt.Sprintf("must be one of: dhcp, static, pppoe (got '%s')", proto))
					}
					// Static requires address + netmask
					if proto == "static" {
						if _, ok := getStringField(iface, "address"); !ok {
							result.AddError("schema", prefix+".address", "is required for static proto")
						}
						if _, ok := getStringField(iface, "netmask"); !ok {
							result.AddError("schema", prefix+".netmask", "is required for static proto")
						}
					}
				}
			}
		}
	}

	// dns
	if dns, ok := getSlice(network, "dns"); ok {
		for i, entry := range dns {
			if _, ok := entry.(string); !ok {
				result.AddError("schema", fmt.Sprintf("network.dns[%d]", i), "must be a string")
			}
		}
	}
}

// ============================================================
// LAYER 2: SEMANTIC VALIDATION
// ============================================================

// Channel validity per band and country (simplified — US rules)
var validChannels2G = map[int]bool{1: true, 2: true, 3: true, 4: true, 5: true, 6: true, 7: true, 8: true, 9: true, 10: true, 11: true}
var validChannels5G = map[int]bool{
	36: true, 40: true, 44: true, 48: true,       // UNII-1
	52: true, 56: true, 60: true, 64: true,       // UNII-2 (DFS)
	100: true, 104: true, 108: true, 112: true,   // UNII-2 Extended (DFS)
	116: true, 120: true, 124: true, 128: true,
	132: true, 136: true, 140: true, 144: true,
	149: true, 153: true, 157: true, 161: true, 165: true, // UNII-3
}

func validateSemantics(config map[string]interface{}, caps *DeviceCapabilities, result *ConfigValidationResult) {
	// Collect all SSID names per band for duplicate detection
	ssidNamesByBand := make(map[string]map[string]bool)
	totalSSIDs := 0

	if wireless, ok := getSlice(config, "wireless"); ok {
		for radioIdx, item := range wireless {
			radio, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			prefix := fmt.Sprintf("wireless[%d]", radioIdx)
			band, _ := getStringField(radio, "band")

			// Channel validation
			if channel, ok := getNumberField(radio, "channel"); ok {
				ch := int(channel)
				validateChannel(ch, band, prefix, result)
			}
			// "auto" channel is a string — always valid
			if channelStr, ok := getStringField(radio, "channel"); ok && channelStr != "auto" {
				result.AddWarning("semantic", prefix+".channel",
					fmt.Sprintf("non-standard channel string value: '%s'", channelStr))
			}

			// Channel width validation per band
			if cw, ok := getNumberField(radio, "channel_width"); ok {
				validateChannelWidth(int(cw), band, prefix, result)
			}

			// SSID validation
			if ssids, ok := getSlice(radio, "ssids"); ok {
				if _, exists := ssidNamesByBand[band]; !exists {
					ssidNamesByBand[band] = make(map[string]bool)
				}

				for ssidIdx, ssidItem := range ssids {
					ssid, ok := ssidItem.(map[string]interface{})
					if !ok {
						continue
					}
					ssidPrefix := fmt.Sprintf("%s.ssids[%d]", prefix, ssidIdx)
					totalSSIDs++

					name, _ := getStringField(ssid, "name")
					if name != "" {
						if ssidNamesByBand[band][name] {
							result.AddError("semantic", ssidPrefix+".name",
								fmt.Sprintf("duplicate SSID '%s' on band '%s'", name, band))
						}
						ssidNamesByBand[band][name] = true
					}

					// WPA3 capability check
					if security, ok := getMap(ssid, "security"); ok {
						mode, _ := getStringField(security, "mode")
						if (mode == "wpa3-sae" || mode == "wpa3-enterprise") && caps != nil && !caps.WPA3 {
							result.AddError("semantic", ssidPrefix+".security.mode",
								"device does not support WPA3")
						}
					}
				}
			}

			// Band capability check
			if caps != nil && band != "" {
				bandSupported := false
				for _, b := range caps.Bands {
					if b == band {
						bandSupported = true
						break
					}
				}
				if !bandSupported {
					result.AddError("semantic", prefix+".band",
						fmt.Sprintf("device does not support band '%s' (supports: %v)", band, caps.Bands))
				}
			}
		}
	}

	// Max SSIDs capability check
	if caps != nil && caps.MaxSSIDs > 0 && totalSSIDs > caps.MaxSSIDs {
		result.AddError("semantic", "wireless.ssids",
			fmt.Sprintf("total SSIDs (%d) exceeds device maximum (%d)", totalSSIDs, caps.MaxSSIDs))
	}
}

func validateChannel(ch int, band, prefix string, result *ConfigValidationResult) {
	switch band {
	case "2g":
		if !validChannels2G[ch] {
			result.AddError("semantic", prefix+".channel",
				fmt.Sprintf("channel %d is not valid for 2.4GHz", ch))
		}
	case "5g":
		if !validChannels5G[ch] {
			result.AddError("semantic", prefix+".channel",
				fmt.Sprintf("channel %d is not valid for 5GHz", ch))
		}
		// DFS warning
		if ch >= 52 && ch <= 144 {
			result.AddWarning("semantic", prefix+".channel",
				fmt.Sprintf("channel %d is in DFS range, may switch on radar detection", ch))
		}
	case "6g":
		// 6GHz channels: 1-233 (simplified)
		if ch < 1 || ch > 233 {
			result.AddError("semantic", prefix+".channel",
				fmt.Sprintf("channel %d is not valid for 6GHz", ch))
		}
	}
}

func validateChannelWidth(cw int, band, prefix string, result *ConfigValidationResult) {
	switch band {
	case "2g":
		if cw > 40 {
			result.AddError("semantic", prefix+".channel_width",
				fmt.Sprintf("2.4GHz band supports max 40MHz width (got %dMHz)", cw))
		}
	case "5g":
		// 80 and 160 are valid for 5GHz
	case "6g":
		// 80, 160, and 320 are valid for 6GHz
	}
}

// ============================================================
// LAYER 3: SAFETY VALIDATION
// ============================================================

func validateSafety(config map[string]interface{}, result *ConfigValidationResult) {
	// Check management interface is present
	hasManagementInterface := false

	if network, ok := getMap(config, "network"); ok {
		if interfaces, ok := getSlice(network, "interfaces"); ok {
			for _, item := range interfaces {
				if iface, ok := item.(map[string]interface{}); ok {
					name, _ := getStringField(iface, "name")
					if name == "mgmt" || name == "management" || name == "lan" {
						hasManagementInterface = true
						break
					}
				}
			}
		}

		// Also check if management_vlan is set (indicates management path exists)
		if _, ok := getNumberField(network, "management_vlan"); ok {
			hasManagementInterface = true
		}
	}

	if !hasManagementInterface {
		result.AddError("safety", "network",
			"configuration must include a management interface (mgmt, management, or lan) "+
				"or management_vlan to ensure the AP remains reachable")
	}

	// Check at least one way to reach the device
	hasReachability := false
	if network, ok := getMap(config, "network"); ok {
		if interfaces, ok := getSlice(network, "interfaces"); ok {
			for _, item := range interfaces {
				if iface, ok := item.(map[string]interface{}); ok {
					proto, _ := getStringField(iface, "proto")
					if proto == "dhcp" || proto == "static" {
						hasReachability = true
						break
					}
				}
			}
		}
	}

	if !hasReachability {
		result.AddWarning("safety", "network.interfaces",
			"no interface with dhcp or static proto found — AP may not be reachable")
	}

	// Warn if no DNS configured
	if network, ok := getMap(config, "network"); ok {
		if dns, ok := getSlice(network, "dns"); ok {
			if len(dns) == 0 {
				result.AddWarning("safety", "network.dns",
					"no DNS servers configured")
			}
		} else {
			result.AddWarning("safety", "network.dns",
				"no DNS configuration found")
		}
	}
}

// ============================================================
// FIELD EXTRACTION HELPERS
// ============================================================

func getMap(obj map[string]interface{}, key string) (map[string]interface{}, bool) {
	val, ok := obj[key]
	if !ok {
		return nil, false
	}
	m, ok := val.(map[string]interface{})
	return m, ok
}

func getSlice(obj map[string]interface{}, key string) ([]interface{}, bool) {
	val, ok := obj[key]
	if !ok {
		return nil, false
	}
	s, ok := val.([]interface{})
	return s, ok
}

func getStringField(obj map[string]interface{}, key string) (string, bool) {
	val, ok := obj[key]
	if !ok {
		return "", false
	}
	s, ok := val.(string)
	return s, ok
}

func getNumberField(obj map[string]interface{}, key string) (float64, bool) {
	val, ok := obj[key]
	if !ok {
		return 0, false
	}
	n, ok := val.(float64)
	return n, ok
}

func joinKeys(m map[string]bool) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return strings.Join(keys, ", ")
}
