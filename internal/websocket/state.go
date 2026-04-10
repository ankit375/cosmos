package websocket

import (
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/yourorg/cloudctrl/internal/model"
)

// DeviceState represents the in-memory cached state of a connected device.
type DeviceState struct {
	DeviceID             uuid.UUID          `json:"device_id"`
	TenantID             uuid.UUID          `json:"tenant_id"`
	SiteID               *uuid.UUID         `json:"site_id"`
	Status               model.DeviceStatus `json:"status"`
	FirmwareVersion      string             `json:"firmware_version"`
	DesiredConfigVersion int64              `json:"desired_config_version"`
	AppliedConfigVersion int64              `json:"applied_config_version"`
	IPAddress            string             `json:"ip_address"`
	Uptime               int64              `json:"uptime"`
	ClientCount          int                `json:"client_count"`
	CPUUsage             float64            `json:"cpu_usage"`
	MemoryUsed           uint64             `json:"memory_used"`
	MemoryTotal          uint64             `json:"memory_total"`
	LastHeartbeat        time.Time          `json:"last_heartbeat"`
	LastMetrics          time.Time          `json:"last_metrics"`
	Dirty                bool               `json:"-"` // needs DB sync
}

// StateStore manages the in-memory device state cache.
type StateStore struct {
	mu     sync.RWMutex
	states map[uuid.UUID]*DeviceState // deviceID → state
}

// NewStateStore creates a new state store.
func NewStateStore() *StateStore {
	return &StateStore{
		states: make(map[uuid.UUID]*DeviceState),
	}
}

// Get returns the state for a device, or nil if not found.
func (s *StateStore) Get(deviceID uuid.UUID) *DeviceState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.states[deviceID]
}

// Set creates or replaces the state for a device.
func (s *StateStore) Set(state *DeviceState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.states[state.DeviceID] = state
}

// Update atomically updates a device's state using a callback.
// If the device doesn't exist, the callback is not called.
func (s *StateStore) Update(deviceID uuid.UUID, fn func(state *DeviceState)) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.states[deviceID]
	if !ok {
		return false
	}
	fn(state)
	return true
}

// Delete removes a device from the state store.
func (s *StateStore) Delete(deviceID uuid.UUID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.states, deviceID)
}

// GetByTenant returns all device states for a tenant.
func (s *StateStore) GetByTenant(tenantID uuid.UUID) []*DeviceState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []*DeviceState
	for _, state := range s.states {
		if state.TenantID == tenantID {
			result = append(result, state)
		}
	}
	return result
}

// GetBySite returns all device states for a site.
func (s *StateStore) GetBySite(siteID uuid.UUID) []*DeviceState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []*DeviceState
	for _, state := range s.states {
		if state.SiteID != nil && *state.SiteID == siteID {
			result = append(result, state)
		}
	}
	return result
}

// Count returns the total number of cached device states.
func (s *StateStore) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.states)
}

// CollectDirty returns all dirty states and marks them clean.
// Used by the state persister worker.
func (s *StateStore) CollectDirty() []*DeviceState {
	s.mu.Lock()
	defer s.mu.Unlock()
	var dirty []*DeviceState
	for _, state := range s.states {
		if state.Dirty {
			// Return a copy so we can safely work with it outside the lock
			cp := *state
			dirty = append(dirty, &cp)
			state.Dirty = false
		}
	}
	return dirty
}

// SetOffline marks a device as offline if its last heartbeat exceeds the timeout.
// Returns the device IDs that were transitioned to offline.
func (s *StateStore) SetOffline(timeout time.Duration) []uuid.UUID {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	var offlined []uuid.UUID

	for _, state := range s.states {
		if state.Status == model.DeviceStatusOnline &&
			!state.LastHeartbeat.IsZero() &&
			now.Sub(state.LastHeartbeat) > timeout {

			state.Status = model.DeviceStatusOffline
			state.Dirty = true
			offlined = append(offlined, state.DeviceID)
		}
	}

	return offlined
}

// Snapshot returns a read-only copy of all device states.
func (s *StateStore) Snapshot() map[uuid.UUID]DeviceState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	snap := make(map[uuid.UUID]DeviceState, len(s.states))
	for id, state := range s.states {
		snap[id] = *state
	}
	return snap
}
