package websocket

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/google/uuid"
	"github.com/yourorg/cloudctrl/internal/config"
	"github.com/yourorg/cloudctrl/internal/configmgr"
	"github.com/yourorg/cloudctrl/internal/device"
	"github.com/yourorg/cloudctrl/internal/model"
	"github.com/yourorg/cloudctrl/internal/protocol"
	pgstore "github.com/yourorg/cloudctrl/internal/store/postgres"
	"go.uber.org/zap"
)

// Hub manages all device WebSocket connections.
type Hub struct {
	// Configuration
	cfg config.WebSocketConfig

	// Connections: deviceID → *DeviceConnection
	mu          sync.RWMutex
	connections map[uuid.UUID]*DeviceConnection

	// Secondary indexes
	byTenant map[uuid.UUID]map[uuid.UUID]struct{} // tenantID → set of deviceIDs
	bySite   map[uuid.UUID]map[uuid.UUID]struct{} // siteID → set of deviceIDs
	byMAC    map[string]uuid.UUID                 // MAC → deviceID

	// Channels
	register   chan *DeviceConnection
	unregister chan *DeviceConnection

	// State
	stateStore *StateStore

	// Rate limiting
	connLimiter *ConnectionRateLimiter

	// Dependencies
	pgStore      *pgstore.Store
	eventEmitter *device.EventEmitter
	logger       *zap.Logger

	// Message handlers
	handlers map[uint16]MessageHandler

	// Config manager
	configManager *configmgr.Manager

	// Command manager (interface to avoid circular imports)
	commandManager interface {
		HandleCommandResponse(deviceID uuid.UUID, msgID uint32, success bool, result json.RawMessage, errMsg string)
		DeliverQueuedCommands(deviceID uuid.UUID)
		GetPendingCount(deviceID uuid.UUID) int
	}

	// Lifecycle
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// MessageHandler processes a decoded message from a device.
type MessageHandler func(conn *DeviceConnection, msg *protocol.Message)

// NewHub creates a new WebSocket hub.
func NewHub(
	cfg config.WebSocketConfig,
	pgStore *pgstore.Store,
	logger *zap.Logger,
) *Hub {
	ctx, cancel := context.WithCancel(context.Background())

	h := &Hub{
		cfg:          cfg,
		connections:  make(map[uuid.UUID]*DeviceConnection),
		byTenant:     make(map[uuid.UUID]map[uuid.UUID]struct{}),
		bySite:       make(map[uuid.UUID]map[uuid.UUID]struct{}),
		byMAC:        make(map[string]uuid.UUID),
		register:     make(chan *DeviceConnection, 64),
		unregister:   make(chan *DeviceConnection, 64),
		stateStore:   NewStateStore(),
		connLimiter:  NewConnectionRateLimiter(cfg.ConnectionRateLimit, cfg.MaxConnectionsPerIP),
		pgStore:      pgStore,
		eventEmitter: device.NewEventEmitter(pgStore.Events, logger),
		logger:       logger.Named("hub"),
		handlers:     make(map[uint16]MessageHandler),
		ctx:          ctx,
		cancel:       cancel,
	}

	h.registerHandlers()

	return h
}

// registerHandlers sets up all message type handlers.
func (h *Hub) registerHandlers() {
	h.handlers[protocol.MsgHeartbeat] = h.handleHeartbeat
	h.handlers[protocol.MsgConfigAck] = h.handleConfigAck
	h.handlers[protocol.MsgCommandResponse] = h.handleCommandResponse
	h.handlers[protocol.MsgEvent] = h.handleEvent
	h.handlers[protocol.MsgMetricsReport] = h.handleMetricsReport
	h.handlers[protocol.MsgClientEvent] = h.handleClientEvent
	h.handlers[protocol.MsgFirmwareProgress] = h.handleFirmwareProgress
	h.handlers[protocol.MsgPing] = h.handlePing
}

// SetCommandManager wires the command manager into the hub.
func (h *Hub) SetCommandManager(cm interface {
	HandleCommandResponse(deviceID uuid.UUID, msgID uint32, success bool, result json.RawMessage, errMsg string)
	DeliverQueuedCommands(deviceID uuid.UUID)
	GetPendingCount(deviceID uuid.UUID) int
}) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.commandManager = cm
}

// Run starts the hub's main loop and background workers.
func (h *Hub) Run() {
	h.logger.Info("starting WebSocket hub",
		zap.Int("max_connections", h.cfg.MaxConnections),
	)

	h.wg.Add(1)
	go h.runLoop()

	h.wg.Add(1)
	go h.runStatePersister()

	h.wg.Add(1)
	go h.runOfflineDetector()
}

// runLoop processes register/unregister events.
func (h *Hub) runLoop() {
	defer h.wg.Done()

	for {
		select {
		case conn := <-h.register:
			h.addConnection(conn)

		case conn := <-h.unregister:
			h.removeConnection(conn)

		case <-h.ctx.Done():
			h.logger.Info("hub loop stopping")
			return
		}
	}
}

// addConnection registers a device connection in the hub.
func (h *Hub) addConnection(conn *DeviceConnection) {
	h.mu.Lock()
	defer h.mu.Unlock()

	// If device already connected, close old connection
	if old, exists := h.connections[conn.DeviceID]; exists {
		h.logger.Warn("duplicate connection, closing old",
			zap.String("device_id", conn.DeviceID.String()),
			zap.String("old_addr", old.RemoteAddr),
			zap.String("new_addr", conn.RemoteAddr),
		)
		old.Close()
		h.removeConnectionLocked(old)
	}

	h.connections[conn.DeviceID] = conn

	// Update indexes
	if _, ok := h.byTenant[conn.TenantID]; !ok {
		h.byTenant[conn.TenantID] = make(map[uuid.UUID]struct{})
	}
	h.byTenant[conn.TenantID][conn.DeviceID] = struct{}{}

	if conn.SiteID != nil {
		if _, ok := h.bySite[*conn.SiteID]; !ok {
			h.bySite[*conn.SiteID] = make(map[uuid.UUID]struct{})
		}
		h.bySite[*conn.SiteID][conn.DeviceID] = struct{}{}
	}

	if conn.MAC != "" {
		h.byMAC[conn.MAC] = conn.DeviceID
	}

	wsConnectionsActive.Set(float64(len(h.connections)))
	wsConnectionsTotal.Inc()

	h.logger.Info("device connected",
		zap.String("device_id", conn.DeviceID.String()),
		zap.String("mac", conn.MAC),
		zap.String("remote_addr", conn.RemoteAddr),
		zap.Int("total_connections", len(h.connections)),
	)
}

// removeConnection unregisters a device connection from the hub.
func (h *Hub) removeConnection(conn *DeviceConnection) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.removeConnectionLocked(conn)
}

// removeConnectionLocked removes a connection (must be called with mu held).
func (h *Hub) removeConnectionLocked(conn *DeviceConnection) {
	current, exists := h.connections[conn.DeviceID]
	if !exists {
		return
	}

	// Only remove if this is the CURRENT connection for this device.
	if current != conn {
		return
	}

	delete(h.connections, conn.DeviceID)

	// Update indexes
	if tenantSet, ok := h.byTenant[conn.TenantID]; ok {
		delete(tenantSet, conn.DeviceID)
		if len(tenantSet) == 0 {
			delete(h.byTenant, conn.TenantID)
		}
	}

	if conn.SiteID != nil {
		if siteSet, ok := h.bySite[*conn.SiteID]; ok {
			delete(siteSet, conn.DeviceID)
			if len(siteSet) == 0 {
				delete(h.bySite, *conn.SiteID)
			}
		}
	}

	if conn.MAC != "" {
		delete(h.byMAC, conn.MAC)
	}

	wsConnectionsActive.Set(float64(len(h.connections)))

	// Track previous status for metrics
	prevStatus := model.DeviceStatusOnline
	h.stateStore.Update(conn.DeviceID, func(state *DeviceState) {
		prevStatus = state.Status
		state.Status = model.DeviceStatusOffline
		state.Dirty = true
	})

	// Emit disconnection event
	if conn.authenticated {
		wsDeviceStateTransitions.WithLabelValues(string(prevStatus), string(model.DeviceStatusOffline)).Inc()
		go h.eventEmitter.DeviceDisconnected(h.ctx, conn.TenantID, conn.DeviceID,
			"connection_closed", conn.ConnectionDuration())
	}

	h.logger.Info("device disconnected",
		zap.String("device_id", conn.DeviceID.String()),
		zap.String("mac", conn.MAC),
		zap.Int("total_connections", len(h.connections)),
	)
}

// GetConnection returns the connection for a device.
func (h *Hub) GetConnection(deviceID uuid.UUID) *DeviceConnection {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.connections[deviceID]
}

// GetByTenant returns all connections for a tenant.
func (h *Hub) GetByTenant(tenantID uuid.UUID) []*DeviceConnection {
	h.mu.RLock()
	defer h.mu.RUnlock()

	deviceIDs, ok := h.byTenant[tenantID]
	if !ok {
		return nil
	}

	conns := make([]*DeviceConnection, 0, len(deviceIDs))
	for id := range deviceIDs {
		if conn, exists := h.connections[id]; exists {
			conns = append(conns, conn)
		}
	}
	return conns
}

// GetBySite returns all connections for a site.
func (h *Hub) GetBySite(siteID uuid.UUID) []*DeviceConnection {
	h.mu.RLock()
	defer h.mu.RUnlock()

	deviceIDs, ok := h.bySite[siteID]
	if !ok {
		return nil
	}

	conns := make([]*DeviceConnection, 0, len(deviceIDs))
	for id := range deviceIDs {
		if conn, exists := h.connections[id]; exists {
			conns = append(conns, conn)
		}
	}
	return conns
}

// Send sends a message to a specific device.
func (h *Hub) Send(deviceID uuid.UUID, data []byte) bool {
	h.mu.RLock()
	conn, exists := h.connections[deviceID]
	h.mu.RUnlock()

	if !exists {
		return false
	}
	return conn.Send(data)
}

// SendMessage encodes and sends a protocol message to a device.
func (h *Hub) SendMessage(deviceID uuid.UUID, channel uint8, msgType uint16, flags uint8, payload interface{}) (uint32, error) {
	h.mu.RLock()
	conn, exists := h.connections[deviceID]
	h.mu.RUnlock()

	if !exists {
		return 0, fmt.Errorf("device %s not connected", deviceID)
	}
	return conn.SendMessage(channel, msgType, flags, payload)
}

// BroadcastToSite sends a message to all devices in a site.
func (h *Hub) BroadcastToSite(siteID uuid.UUID, data []byte) int {
	conns := h.GetBySite(siteID)
	sent := 0
	for _, conn := range conns {
		if conn.Send(data) {
			sent++
		}
	}
	return sent
}

// Count returns the number of active connections.
func (h *Hub) Count() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.connections)
}

// IsConnected checks if a device is connected.
func (h *Hub) IsConnected(deviceID uuid.UUID) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	_, exists := h.connections[deviceID]
	return exists
}

// StateStore returns the hub's in-memory state store.
func (h *Hub) StateStore() *StateStore {
	return h.stateStore
}

// EventEmitter returns the hub's event emitter (for API handler access).
func (h *Hub) EventEmitter() *device.EventEmitter {
	return h.eventEmitter
}

// AllowConnection checks rate limits for a new connection.
func (h *Hub) AllowConnection(ip string) bool {
	if h.Count() >= h.cfg.MaxConnections {
		wsRateLimitRejects.WithLabelValues("max_connections").Inc()
		return false
	}
	return h.connLimiter.Allow(ip)
}

// Stop gracefully shuts down the hub.
func (h *Hub) Stop(ctx context.Context) {
	h.logger.Info("stopping hub, draining connections...")

	h.cancel()

	h.mu.Lock()
	for _, conn := range h.connections {
		conn.Close()
	}
	h.mu.Unlock()

	h.connLimiter.Stop()

	done := make(chan struct{})
	go func() {
		h.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		h.logger.Info("hub stopped gracefully")
	case <-ctx.Done():
		h.logger.Warn("hub stop timed out")
	}
}
