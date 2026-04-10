package device

import (
	"testing"

	"github.com/yourorg/cloudctrl/internal/model"
)

func TestCanTransition(t *testing.T) {
	tests := []struct {
		name     string
		from     model.DeviceStatus
		to       model.DeviceStatus
		expected bool
	}{
		// ── Valid transitions ─────────────────────────────────
		{"pending → provisioning", model.DeviceStatusPendingAdopt, model.DeviceStatusProvisioning, true},
		{"pending → adopting", model.DeviceStatusPendingAdopt, model.DeviceStatusAdopting, true},
		{"pending → decommissioned", model.DeviceStatusPendingAdopt, model.DeviceStatusDecommissioned, true},
		{"adopting → provisioning", model.DeviceStatusAdopting, model.DeviceStatusProvisioning, true},
		{"adopting → error", model.DeviceStatusAdopting, model.DeviceStatusError, true},
		{"provisioning → online", model.DeviceStatusProvisioning, model.DeviceStatusOnline, true},
		{"provisioning → error", model.DeviceStatusProvisioning, model.DeviceStatusError, true},
		{"online → offline", model.DeviceStatusOnline, model.DeviceStatusOffline, true},
		{"online → upgrading", model.DeviceStatusOnline, model.DeviceStatusUpgrading, true},
		{"online → config_pending", model.DeviceStatusOnline, model.DeviceStatusConfigPending, true},
		{"online → error", model.DeviceStatusOnline, model.DeviceStatusError, true},
		{"online → decommissioned", model.DeviceStatusOnline, model.DeviceStatusDecommissioned, true},
		{"offline → online", model.DeviceStatusOffline, model.DeviceStatusOnline, true},
		{"offline → decommissioned", model.DeviceStatusOffline, model.DeviceStatusDecommissioned, true},
		{"error → online", model.DeviceStatusError, model.DeviceStatusOnline, true},
		{"error → offline", model.DeviceStatusError, model.DeviceStatusOffline, true},
		{"error → pending (reset)", model.DeviceStatusError, model.DeviceStatusPendingAdopt, true},
		{"upgrading → online", model.DeviceStatusUpgrading, model.DeviceStatusOnline, true},
		{"upgrading → error", model.DeviceStatusUpgrading, model.DeviceStatusError, true},
		{"upgrading → offline", model.DeviceStatusUpgrading, model.DeviceStatusOffline, true},
		{"config_pending → online", model.DeviceStatusConfigPending, model.DeviceStatusOnline, true},
		{"config_pending → error", model.DeviceStatusConfigPending, model.DeviceStatusError, true},
		{"config_pending → offline", model.DeviceStatusConfigPending, model.DeviceStatusOffline, true},

		// ── Invalid transitions ──────────────────────────────
		{"pending → online (skip provisioning)", model.DeviceStatusPendingAdopt, model.DeviceStatusOnline, false},
		{"pending → offline", model.DeviceStatusPendingAdopt, model.DeviceStatusOffline, false},
		{"offline → upgrading (must be online)", model.DeviceStatusOffline, model.DeviceStatusUpgrading, false},
		{"offline → config_pending", model.DeviceStatusOffline, model.DeviceStatusConfigPending, false},
		{"decommissioned → online", model.DeviceStatusDecommissioned, model.DeviceStatusOnline, false},
		{"decommissioned → pending", model.DeviceStatusDecommissioned, model.DeviceStatusPendingAdopt, false},
		{"decommissioned → offline", model.DeviceStatusDecommissioned, model.DeviceStatusOffline, false},
		{"provisioning → offline (skip online)", model.DeviceStatusProvisioning, model.DeviceStatusOffline, false},
		{"provisioning → upgrading", model.DeviceStatusProvisioning, model.DeviceStatusUpgrading, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CanTransition(tt.from, tt.to)
			if result != tt.expected {
				t.Errorf("CanTransition(%s, %s) = %v, want %v", tt.from, tt.to, result, tt.expected)
			}
		})
	}
}

func TestValidateTransition_SameState(t *testing.T) {
	statuses := []model.DeviceStatus{
		model.DeviceStatusOnline,
		model.DeviceStatusOffline,
		model.DeviceStatusPendingAdopt,
		model.DeviceStatusDecommissioned,
	}
	for _, s := range statuses {
		err := ValidateTransition(s, s)
		if err != nil {
			t.Errorf("same-state transition %s → %s should not error: %v", s, s, err)
		}
	}
}

func TestValidateTransition_InvalidReturnsError(t *testing.T) {
	err := ValidateTransition(model.DeviceStatusDecommissioned, model.DeviceStatusOnline)
	if err == nil {
		t.Error("decommissioned → online should return error")
	}
}

func TestIsTerminal(t *testing.T) {
	if !IsTerminal(model.DeviceStatusDecommissioned) {
		t.Error("decommissioned should be terminal")
	}

	nonTerminal := []model.DeviceStatus{
		model.DeviceStatusOnline,
		model.DeviceStatusOffline,
		model.DeviceStatusPendingAdopt,
		model.DeviceStatusError,
		model.DeviceStatusUpgrading,
	}
	for _, s := range nonTerminal {
		if IsTerminal(s) {
			t.Errorf("%s should not be terminal", s)
		}
	}
}

func TestIsOperational(t *testing.T) {
	operational := []model.DeviceStatus{
		model.DeviceStatusOnline,
		model.DeviceStatusConfigPending,
	}
	for _, s := range operational {
		if !IsOperational(s) {
			t.Errorf("%s should be operational", s)
		}
	}

	notOperational := []model.DeviceStatus{
		model.DeviceStatusOffline,
		model.DeviceStatusPendingAdopt,
		model.DeviceStatusProvisioning,
		model.DeviceStatusUpgrading,
		model.DeviceStatusError,
		model.DeviceStatusDecommissioned,
	}
	for _, s := range notOperational {
		if IsOperational(s) {
			t.Errorf("%s should not be operational", s)
		}
	}
}

func TestIsAdopted(t *testing.T) {
	notAdopted := []model.DeviceStatus{
		model.DeviceStatusPendingAdopt,
		model.DeviceStatusDecommissioned,
	}
	for _, s := range notAdopted {
		if IsAdopted(s) {
			t.Errorf("%s should not be adopted", s)
		}
	}

	adopted := []model.DeviceStatus{
		model.DeviceStatusAdopting,
		model.DeviceStatusProvisioning,
		model.DeviceStatusOnline,
		model.DeviceStatusOffline,
		model.DeviceStatusUpgrading,
		model.DeviceStatusConfigPending,
		model.DeviceStatusError,
	}
	for _, s := range adopted {
		if !IsAdopted(s) {
			t.Errorf("%s should be adopted", s)
		}
	}
}
