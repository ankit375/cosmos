package configmgr

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/yourorg/cloudctrl/internal/model"
)

// ============================================================
// DEEP MERGE
// ============================================================

// DeepMerge merges override into base following the documented merge rules:
//   1. Objects: deep merge (recursive)
//   2. Arrays: override replaces entirely
//   3. Scalars: override replaces
//   4. Null in override: remove field
//   5. SSIDs merge by name (special case)
func DeepMerge(base, override json.RawMessage) (json.RawMessage, error) {
	if len(override) == 0 || string(override) == "null" {
		return base, nil
	}
	if len(base) == 0 || string(base) == "null" {
		return override, nil
	}

	var baseMap map[string]interface{}
	var overrideMap map[string]interface{}

	if err := json.Unmarshal(base, &baseMap); err != nil {
		return nil, fmt.Errorf("unmarshal base config: %w", err)
	}
	if err := json.Unmarshal(override, &overrideMap); err != nil {
		return nil, fmt.Errorf("unmarshal override config: %w", err)
	}

	merged := deepMergeMap(baseMap, overrideMap)

	result, err := json.Marshal(merged)
	if err != nil {
		return nil, fmt.Errorf("marshal merged config: %w", err)
	}
	return result, nil
}

// deepMergeMap recursively merges two maps.
func deepMergeMap(base, override map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{}, len(base))

	// Copy base
	for k, v := range base {
		result[k] = v
	}

	// Apply overrides
	for k, overrideVal := range override {
		// Rule 4: null means delete
		if overrideVal == nil {
			delete(result, k)
			continue
		}

		baseVal, exists := result[k]
		if !exists {
			// New key from override
			result[k] = overrideVal
			continue
		}

		// Rule 5a: Special SSID merge by "name"
		if k == "ssids" {
			if baseArr, ok := toSliceOfMaps(baseVal); ok {
				if overrideArr, ok := toSliceOfMaps(overrideVal); ok {
					result[k] = mergeByKey(baseArr, overrideArr, "name")
					continue
				}
			}
		}

		// Rule 5b: Special wireless radio merge by "band"
		if k == "wireless" {
			if baseArr, ok := toSliceOfMaps(baseVal); ok {
				if overrideArr, ok := toSliceOfMaps(overrideVal); ok {
					result[k] = mergeByKey(baseArr, overrideArr, "band")
					continue
				}
			}
		}

		// Rule 1: Both are objects → deep merge
		baseMap, baseIsMap := toMap(baseVal)
		overrideMap, overrideIsMap := toMap(overrideVal)
		if baseIsMap && overrideIsMap {
			result[k] = deepMergeMap(baseMap, overrideMap)
			continue
		}

		// Rule 2: Arrays → override replaces entirely
		// Rule 3: Scalars → override replaces
		result[k] = overrideVal
	}

	return result
}

// mergeByKey merges two arrays of maps by matching on a key field.
// Items in override that match a base item by key are deep-merged.
// Items in override that don't match any base item are appended.
// Items in base that don't match any override are kept as-is.
func mergeByKey(base, override []map[string]interface{}, key string) interface{} {
	// Index base items by key
	baseByKey := make(map[string]int, len(base))
	for i, item := range base {
		if val, ok := item[key].(string); ok {
			baseByKey[val] = i
		}
	}

	// Deep copy base
	result := make([]map[string]interface{}, len(base))
	for i, item := range base {
		result[i] = copyMap(item)
	}

	// Apply overrides
	for _, overrideItem := range override {
		keyVal, ok := overrideItem[key].(string)
		if !ok {
			// No key → append as new item
			result = append(result, overrideItem)
			continue
		}

		if idx, exists := baseByKey[keyVal]; exists {
			// Merge into existing item
			result[idx] = deepMergeMap(result[idx], overrideItem)
		} else {
			// New item → append
			result = append(result, overrideItem)
		}
	}

	// Convert back to []interface{} for JSON compatibility
	out := make([]interface{}, len(result))
	for i, m := range result {
		out[i] = m
	}
	return out
}

// ============================================================
// VARIABLE SUBSTITUTION
// ============================================================

// VariableContext holds the data available for variable substitution.
type VariableContext struct {
	DeviceName   string
	DeviceMAC    string
	DeviceSerial string
	DeviceModel  string
	SiteName     string
	SiteCountry  string
	SiteTimezone string
}

// NewVariableContext creates a variable context from a device and site.
func NewVariableContext(device *model.Device, site *model.Site) *VariableContext {
	vc := &VariableContext{}
	if device != nil {
		vc.DeviceName = device.Name
		vc.DeviceMAC = device.MAC
		vc.DeviceSerial = device.Serial
		vc.DeviceModel = device.Model
	}
	if site != nil {
		vc.SiteName = site.Name
		vc.SiteCountry = site.CountryCode
		vc.SiteTimezone = site.Timezone
	}
	return vc
}

// variables returns a map of {{key}} → value replacements.
func (vc *VariableContext) variables() map[string]string {
	return map[string]string{
		"{{device.name}}":   vc.DeviceName,
		"{{device.mac}}":    vc.DeviceMAC,
		"{{device.serial}}": vc.DeviceSerial,
		"{{device.model}}":  vc.DeviceModel,
		"{{site.name}}":     vc.SiteName,
		"{{site.country}}":  vc.SiteCountry,
		"{{site.timezone}}": vc.SiteTimezone,
	}
}

// SubstituteVariables replaces all {{variable}} placeholders in a JSON config
// with values from the variable context.
func SubstituteVariables(config json.RawMessage, vc *VariableContext) (json.RawMessage, error) {
	if vc == nil {
		return config, nil
	}

	s := string(config)
	vars := vc.variables()

	for placeholder, value := range vars {
		// Escape the value for safe JSON embedding
		escapedValue := jsonEscapeString(value)
		s = strings.ReplaceAll(s, placeholder, escapedValue)
	}

	// Validate the result is still valid JSON
	result := json.RawMessage(s)
	if !json.Valid(result) {
		return nil, fmt.Errorf("variable substitution produced invalid JSON")
	}

	return result, nil
}

// ============================================================
// FULL CONFIG GENERATION
// ============================================================

// GenerateDeviceConfig produces the final merged config for a device.
// It merges the site template with device overrides, then substitutes variables.
func GenerateDeviceConfig(
	template json.RawMessage,
	overrides json.RawMessage,
	device *model.Device,
	site *model.Site,
) (json.RawMessage, error) {
	// Step 1: Merge template + overrides
	merged := template
	if len(overrides) > 0 && string(overrides) != "{}" && string(overrides) != "null" {
		var err error
		merged, err = DeepMerge(template, overrides)
		if err != nil {
			return nil, fmt.Errorf("merge config: %w", err)
		}
	}

	// Step 2: Substitute variables
	vc := NewVariableContext(device, site)
	result, err := SubstituteVariables(merged, vc)
	if err != nil {
		return nil, fmt.Errorf("substitute variables: %w", err)
	}

	return result, nil
}

// ============================================================
// HELPERS
// ============================================================

// toMap attempts to convert an interface{} to map[string]interface{}.
func toMap(v interface{}) (map[string]interface{}, bool) {
	m, ok := v.(map[string]interface{})
	return m, ok
}

// toSliceOfMaps attempts to convert an interface{} to a slice of maps.
func toSliceOfMaps(v interface{}) ([]map[string]interface{}, bool) {
	switch arr := v.(type) {
	case []interface{}:
		result := make([]map[string]interface{}, 0, len(arr))
		for _, item := range arr {
			if m, ok := item.(map[string]interface{}); ok {
				result = append(result, m)
			} else {
				return nil, false
			}
		}
		return result, true
	case []map[string]interface{}:
		return arr, true
	}
	return nil, false
}

// copyMap returns a shallow copy of a map.
func copyMap(m map[string]interface{}) map[string]interface{} {
	cp := make(map[string]interface{}, len(m))
	for k, v := range m {
		cp[k] = v
	}
	return cp
}

// jsonEscapeString escapes a string for safe embedding inside a JSON string value.
// This handles special chars that would break JSON if substituted raw.
func jsonEscapeString(s string) string {
	// Use json.Marshal to properly escape, then strip the surrounding quotes
	b, err := json.Marshal(s)
	if err != nil {
		return s
	}
	// b will be `"escaped string"`, strip quotes
	escaped := string(b)
	if len(escaped) >= 2 && escaped[0] == '"' && escaped[len(escaped)-1] == '"' {
		return escaped[1 : len(escaped)-1]
	}
	return escaped
}

// Ensure GenerateDeviceConfig doesn't use uuid directly (satisfy import)
var _ = uuid.Nil
