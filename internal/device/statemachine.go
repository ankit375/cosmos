package device

import (
	"fmt"

	"github.com/yourorg/cloudctrl/internal/model"
)

// ValidTransitions defines the allowed state transitions for a device.
// Key = current status, Value = set of allowed next statuses.
var ValidTransitions = map[model.DeviceStatus]map[model.DeviceStatus]bool{
	model.DeviceStatusPendingAdopt: {
		model.DeviceStatusAdopting:       true,
		model.DeviceStatusProvisioning:   true, // auto-adopt skips adopting
		model.DeviceStatusDecommissioned: true,
	},
	model.DeviceStatusAdopting: {
		model.DeviceStatusProvisioning:   true,
		model.DeviceStatusError:          true,
		model.DeviceStatusPendingAdopt:   true, // adoption failed, revert
		model.DeviceStatusDecommissioned: true,
	},
	model.DeviceStatusProvisioning: {
		model.DeviceStatusOnline:         true,
		model.DeviceStatusError:          true,
		model.DeviceStatusDecommissioned: true,
	},
	model.DeviceStatusOnline: {
		model.DeviceStatusOffline:        true,
		model.DeviceStatusUpgrading:      true,
		model.DeviceStatusConfigPending:  true,
		model.DeviceStatusError:          true,
		model.DeviceStatusDecommissioned: true,
	},
	model.DeviceStatusOffline: {
		model.DeviceStatusOnline:         true,
		model.DeviceStatusDecommissioned: true,
	},
	model.DeviceStatusUpgrading: {
		model.DeviceStatusOnline:         true,
		model.DeviceStatusError:          true,
		model.DeviceStatusOffline:        true,
		model.DeviceStatusDecommissioned: true,
	},
	model.DeviceStatusConfigPending: {
		model.DeviceStatusOnline:         true,
		model.DeviceStatusError:          true,
		model.DeviceStatusOffline:        true,
		model.DeviceStatusDecommissioned: true,
	},
	model.DeviceStatusError: {
		model.DeviceStatusOnline:         true,
		model.DeviceStatusOffline:        true,
		model.DeviceStatusPendingAdopt:   true, // reset
		model.DeviceStatusDecommissioned: true,
	},
	model.DeviceStatusDecommissioned: {
		// Terminal state — no transitions out
	},
}

// CanTransition checks if a state transition is valid.
func CanTransition(from, to model.DeviceStatus) bool {
	allowed, ok := ValidTransitions[from]
	if !ok {
		return false
	}
	return allowed[to]
}

// ValidateTransition returns an error if the transition is not allowed.
func ValidateTransition(from, to model.DeviceStatus) error {
	if from == to {
		return nil // no-op transition is always valid
	}
	if !CanTransition(from, to) {
		return fmt.Errorf("invalid state transition: %s → %s", from, to)
	}
	return nil
}

// IsTerminal returns true if the device is in a terminal state.
func IsTerminal(status model.DeviceStatus) bool {
	return status == model.DeviceStatusDecommissioned
}

// IsOperational returns true if the device is in a state where it can receive commands.
func IsOperational(status model.DeviceStatus) bool {
	switch status {
	case model.DeviceStatusOnline, model.DeviceStatusConfigPending:
		return true
	default:
		return false
	}
}

// IsAdopted returns true if the device has been adopted (past pending_adopt).
func IsAdopted(status model.DeviceStatus) bool {
	switch status {
	case model.DeviceStatusPendingAdopt:
		return false
	case model.DeviceStatusDecommissioned:
		return false
	default:
		return true
	}
}
