package telemetry

import (
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/yourorg/cloudctrl/internal/model"
)

// ClientSnapshot holds the last known client list for a device.
type ClientSnapshot struct {
	Clients   map[string]model.ClientInfo // MAC → ClientInfo
	UpdatedAt time.Time
}

// ClientDiffEngine compares client snapshots to detect connect/disconnect/roam events.
type ClientDiffEngine struct {
	mu        sync.RWMutex
	snapshots map[uuid.UUID]*ClientSnapshot // deviceID → snapshot
}

// NewClientDiffEngine creates a new client diff engine.
func NewClientDiffEngine() *ClientDiffEngine {
	return &ClientDiffEngine{
		snapshots: make(map[uuid.UUID]*ClientSnapshot),
	}
}

// Diff compares the new client list with the previous snapshot.
// Returns connect/disconnect/roam events and updates the stored snapshot.
func (e *ClientDiffEngine) Diff(deviceID uuid.UUID, newClients []model.ClientInfo) model.ClientDiffResult {
	e.mu.Lock()
	defer e.mu.Unlock()

	result := model.ClientDiffResult{}

	// Build new lookup
	newMap := make(map[string]model.ClientInfo, len(newClients))
	for _, c := range newClients {
		newMap[c.MAC] = c
	}

	// Get previous snapshot
	prev := e.snapshots[deviceID]

	if prev == nil {
		// First time seeing this device — all clients are "connected"
		result.Connected = newClients
	} else {
		// Find disconnected clients (in old, not in new)
		for mac, oldClient := range prev.Clients {
			if _, exists := newMap[mac]; !exists {
				result.Disconnected = append(result.Disconnected, oldClient)
			}
		}

		// Find connected and roamed clients
		for mac, newClient := range newMap {
			oldClient, existed := prev.Clients[mac]
			if !existed {
				// New client
				result.Connected = append(result.Connected, newClient)
			} else if oldClient.Band != newClient.Band {
				// Band changed → roam
				result.Roamed = append(result.Roamed, model.ClientRoamInfo{
					Client:  newClient,
					OldBand: oldClient.Band,
					NewBand: newClient.Band,
				})
			}
		}
	}

	// Store new snapshot
	e.snapshots[deviceID] = &ClientSnapshot{
		Clients:   newMap,
		UpdatedAt: time.Now(),
	}

	return result
}

// GetSnapshot returns the current client snapshot for a device.
func (e *ClientDiffEngine) GetSnapshot(deviceID uuid.UUID) []model.ClientInfo {
	e.mu.RLock()
	defer e.mu.RUnlock()

	snap, ok := e.snapshots[deviceID]
	if !ok {
		return nil
	}

	clients := make([]model.ClientInfo, 0, len(snap.Clients))
	for _, c := range snap.Clients {
		clients = append(clients, c)
	}
	return clients
}

// RemoveDevice clears the snapshot for a device (on disconnect).
func (e *ClientDiffEngine) RemoveDevice(deviceID uuid.UUID) []model.ClientInfo {
	e.mu.Lock()
	defer e.mu.Unlock()

	snap, ok := e.snapshots[deviceID]
	if !ok {
		return nil
	}

	// Return all clients as "disconnected"
	clients := make([]model.ClientInfo, 0, len(snap.Clients))
	for _, c := range snap.Clients {
		clients = append(clients, c)
	}
	delete(e.snapshots, deviceID)
	return clients
}

// TotalClients returns the total number of active clients across all devices.
func (e *ClientDiffEngine) TotalClients() int {
	e.mu.RLock()
	defer e.mu.RUnlock()

	total := 0
	for _, snap := range e.snapshots {
		total += len(snap.Clients)
	}
	return total
}

// AllSnapshots returns a copy of all device client snapshots.
func (e *ClientDiffEngine) AllSnapshots() map[uuid.UUID][]model.ClientInfo {
	e.mu.RLock()
	defer e.mu.RUnlock()

	result := make(map[uuid.UUID][]model.ClientInfo, len(e.snapshots))
	for deviceID, snap := range e.snapshots {
		clients := make([]model.ClientInfo, 0, len(snap.Clients))
		for _, c := range snap.Clients {
			clients = append(clients, c)
		}
		result[deviceID] = clients
	}
	return result
}
