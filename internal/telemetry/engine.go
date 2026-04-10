package telemetry

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/yourorg/cloudctrl/internal/model"
	"github.com/yourorg/cloudctrl/internal/protocol"
	pgstore "github.com/yourorg/cloudctrl/internal/store/postgres"
	redisstore "github.com/yourorg/cloudctrl/internal/store/redis"
	"go.uber.org/zap"
)

// EngineConfig holds telemetry engine settings.
type EngineConfig struct {
	FlushInterval        time.Duration
	BufferCapacity       int
	SessionUpdateInterval time.Duration
	ClientSnapshotTTL    time.Duration
}

// DefaultEngineConfig returns production defaults.
func DefaultEngineConfig() EngineConfig {
	return EngineConfig{
		FlushInterval:        10 * time.Second,
		BufferCapacity:       2000,
		SessionUpdateInterval: 120 * time.Second,
		ClientSnapshotTTL:    5 * time.Minute,
	}
}

// Engine is the telemetry pipeline orchestrator.
// It receives MetricsReport messages, buffers them, and periodically
// flushes to TimescaleDB via COPY protocol.
type Engine struct {
	cfg            EngineConfig
	buffer         *DoubleBuffer
	diffEngine     *ClientDiffEngine
	flusher        *Flusher
	sessionManager *ClientSessionManager
	pgStore        *pgstore.Store
	redisStore     *redisstore.Store
	logger         *zap.Logger

	// stateUpdater is called to update the in-memory device state.
	// Set by the hub via SetStateUpdater to avoid circular imports.
	StateUpdateFn func(deviceID uuid.UUID, cpu float64, memUsed uint64, memTotal uint64, clientCount int, lastMetrics time.Time)

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewEngine creates a new telemetry engine.
func NewEngine(
	cfg EngineConfig,
	pgStore *pgstore.Store,
	redisStore *redisstore.Store,
	logger *zap.Logger,
) *Engine {
	ctx, cancel := context.WithCancel(context.Background())

	sessionMgr := NewClientSessionManager(pgStore.Metrics, logger)
	flusher := NewFlusher(pgStore.Metrics, sessionMgr, logger)

	return &Engine{
		cfg:            cfg,
		buffer:         NewDoubleBuffer(cfg.BufferCapacity),
		diffEngine:     NewClientDiffEngine(),
		flusher:        flusher,
		sessionManager: sessionMgr,
		pgStore:        pgStore,
		redisStore:     redisStore,
		logger:         logger.Named("telemetry"),
		ctx:            ctx,
		cancel:         cancel,
	}
}

// SetStateUpdater sets the callback used to update in-memory device state.
// Called by the hub during wiring.
// SetStateUpdateFn sets the callback for updating in-memory device state.
func (e *Engine) SetStateUpdateFn(fn func(deviceID uuid.UUID, cpu float64, memUsed uint64, memTotal uint64, clientCount int, lastMetrics time.Time)) {
	e.StateUpdateFn = fn
}

// Start launches background workers (flusher, session updater).
func (e *Engine) Start() {
	e.logger.Info("starting telemetry engine",
		zap.Duration("flush_interval", e.cfg.FlushInterval),
		zap.Int("buffer_capacity", e.cfg.BufferCapacity),
	)

	e.wg.Add(1)
	go e.runFlusher()

	e.wg.Add(1)
	go e.runSessionUpdater()
}

// Stop gracefully stops the engine. Performs a final flush.
func (e *Engine) Stop() {
	e.logger.Info("stopping telemetry engine...")
	e.cancel()

	// Final flush
	batch := e.buffer.Swap()
	if batch.Size() > 0 {
		flushCtx, flushCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer flushCancel()
		if err := e.flusher.Flush(flushCtx, batch); err != nil {
			e.logger.Error("final flush failed", zap.Error(err))
		} else {
			e.logger.Info("final flush complete", zap.Int("items", batch.Size()))
		}
	}

	e.wg.Wait()
	e.logger.Info("telemetry engine stopped")
}

// Ingest processes a MetricsReport from a device.
// Called from the WebSocket message handler — must be fast.
// Ingest processes a MetricsReport from a device.
// Called from the WebSocket message handler — must be fast.
func (e *Engine) Ingest(deviceID, tenantID uuid.UUID, siteID *uuid.UUID, payload *protocol.MetricsReportPayload) {
	start := time.Now()
	metricsReportsReceived.Inc()

	// Validate
	if !validateMetricsReport(payload) {
		metricsReportsInvalid.Inc()
		e.logger.Debug("invalid metrics report rejected",
			zap.String("device_id", deviceID.String()),
		)
		return
	}

	// Convert to typed rows + diff clients
	dm, radioRows, clientEvents := convertMetricsReport(deviceID, tenantID, siteID, payload, e.diffEngine)

	// Append to buffer (very fast — just a slice append under a lock)
	e.buffer.Append(dm, radioRows, clientEvents)

	// Update in-memory device state
	e.updateDeviceState(deviceID, payload)

	// Update Redis client snapshot (async)
	go e.updateRedisSnapshot(deviceID, payload.Clients)

	// Update active clients gauge
	activeClientsGauge.Set(float64(e.diffEngine.TotalClients()))

	metricsIngestDuration.Observe(time.Since(start).Seconds())
}

// updateDeviceState updates the in-memory state via the state updater callback.
func (e *Engine) updateDeviceState(deviceID uuid.UUID, payload *protocol.MetricsReportPayload) {
	if e.StateUpdateFn == nil {
		return
	}
	e.StateUpdateFn(
		deviceID,
		payload.System.CPUUsage,
		payload.System.MemoryUsed,
		payload.System.MemoryTotal,
		len(payload.Clients),
		time.Now(),
	)
}


// HandleClientEvent processes a real-time ClientEvent from the wire protocol (0x0011).
func (e *Engine) HandleClientEvent(deviceID, tenantID uuid.UUID, siteID *uuid.UUID, payload *protocol.ClientEventPayload) {
	now := time.Now()

	connSince := time.Unix(payload.Timestamp, 0)
	if connSince.IsZero() || connSince.After(now) {
		connSince = now
	}

	clientInfo := model.ClientInfo{
		MAC:            payload.MAC,
		IP:             payload.IP,
		Hostname:       payload.Hostname,
		SSID:           payload.SSID,
		Band:           payload.Band,
		RSSI:           payload.RSSI,
		ConnectedSince: connSince,
	}

	deviceIDBytes := [16]byte(deviceID)
	tenantIDBytes := [16]byte(tenantID)
	var siteIDBytes *[16]byte
	if siteID != nil {
		b := [16]byte(*siteID)
		siteIDBytes = &b
	}

	event := ClientEvent{
		ClientInfo: clientInfo,
		DeviceID:   deviceIDBytes,
		TenantID:   tenantIDBytes,
		SiteID:     siteIDBytes,
		EventType:  payload.Event,
	}

	// Append to buffer for batch processing
	e.buffer.Append(nil, nil, []ClientEvent{event})
}

// OnDeviceDisconnect handles cleanup when a device disconnects.
func (e *Engine) OnDeviceDisconnect(deviceID uuid.UUID) {
	// Remove client snapshot
	disconnectedClients := e.diffEngine.RemoveDevice(deviceID)

	// Close all active sessions
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	e.sessionManager.CloseAllForDevice(ctx, deviceID, "device_offline")

	// Clean up Redis
	go func() {
		rctx, rcancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer rcancel()
		_ = e.redisStore.DeleteClientSnapshot(rctx, deviceID.String())
	}()

	activeClientsGauge.Sub(float64(len(disconnectedClients)))

	e.logger.Debug("device disconnect cleanup",
		zap.String("device_id", deviceID.String()),
		zap.Int("clients_closed", len(disconnectedClients)),
	)
}

// GetLiveClients returns the current client list for a device from the diff engine.
func (e *Engine) GetLiveClients(deviceID uuid.UUID) []model.ClientInfo {
	return e.diffEngine.GetSnapshot(deviceID)
}

// GetLiveClientsFromRedis returns the cached client list from Redis (for API queries).
func (e *Engine) GetLiveClientsFromRedis(ctx context.Context, deviceID uuid.UUID) ([]model.ClientInfo, error) {
	data, err := e.redisStore.GetClientSnapshot(ctx, deviceID.String())
	if err != nil {
		return nil, err
	}
	if data == nil {
		// Fall back to in-memory
		return e.diffEngine.GetSnapshot(deviceID), nil
	}

	var clients []model.ClientInfo
	if err := json.Unmarshal(data, &clients); err != nil {
		return nil, err
	}
	return clients, nil
}

// GetSiteClients returns all live clients for a site.
func (e *Engine) GetSiteClients(ctx context.Context, deviceIDs []uuid.UUID) ([]model.ClientInfo, error) {
	ids := make([]string, len(deviceIDs))
	for i, id := range deviceIDs {
		ids[i] = id.String()
	}

	dataMap, err := e.redisStore.GetSiteClients(ctx, ids)
	if err != nil {
		return nil, err
	}

	var allClients []model.ClientInfo
	for _, data := range dataMap {
		var clients []model.ClientInfo
		if err := json.Unmarshal(data, &clients); err != nil {
			continue
		}
		allClients = append(allClients, clients...)
	}
	return allClients, nil
}

// DiffEngine returns the client diff engine (for testing).
func (e *Engine) DiffEngine() *ClientDiffEngine {
	return e.diffEngine
}

// ============================================================
// Background workers
// ============================================================

// runFlusher swaps the double buffer every FlushInterval and flushes to DB.
func (e *Engine) runFlusher() {
	defer e.wg.Done()

	ticker := time.NewTicker(e.cfg.FlushInterval)
	defer ticker.Stop()

	e.logger.Info("metrics flusher started",
		zap.Duration("interval", e.cfg.FlushInterval),
	)

	for {
		select {
		case <-ticker.C:
			batch := e.buffer.Swap()
			if batch.Size() == 0 {
				continue
			}

			flushCtx, flushCancel := context.WithTimeout(e.ctx, 30*time.Second)
			if err := e.flusher.Flush(flushCtx, batch); err != nil {
				e.logger.Error("flush failed", zap.Error(err))
			}
			flushCancel()

		case <-e.ctx.Done():
			e.logger.Info("metrics flusher stopped")
			return
		}
	}
}

// runSessionUpdater periodically updates active session stats.
func (e *Engine) runSessionUpdater() {
	defer e.wg.Done()

	ticker := time.NewTicker(e.cfg.SessionUpdateInterval)
	defer ticker.Stop()

	e.logger.Info("session updater started",
		zap.Duration("interval", e.cfg.SessionUpdateInterval),
	)

	for {
		select {
		case <-ticker.C:
			e.updateAllActiveSessions()

		case <-e.ctx.Done():
			e.logger.Info("session updater stopped")
			return
		}
	}
}

func (e *Engine) updateAllActiveSessions() {
	snapshots := e.diffEngine.AllSnapshots()

	ctx, cancel := context.WithTimeout(e.ctx, 30*time.Second)
	defer cancel()

	for deviceID, clients := range snapshots {
		e.sessionManager.UpdateActiveSessions(ctx, deviceID, clients)
	}
}

// updateRedisSnapshot stores the client list in Redis for API queries.
func (e *Engine) updateRedisSnapshot(deviceID uuid.UUID, clients []protocol.MetricsClientPayload) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Convert to model.ClientInfo for consistent JSON
	infos := make([]model.ClientInfo, 0, len(clients))
	now := time.Now()
	for _, c := range clients {
		connSince := time.Unix(c.ConnectedSince, 0)
		if connSince.IsZero() || connSince.After(now) {
			connSince = now
		}
		infos = append(infos, model.ClientInfo{
			MAC:            c.MAC,
			IP:             c.IP,
			Hostname:       c.Hostname,
			SSID:           c.SSID,
			Band:           c.Band,
			RSSI:           c.RSSI,
			SNR:            c.SNR,
			TxRate:         c.TxRate,
			RxRate:         c.RxRate,
			TxBytes:        c.TxBytes,
			RxBytes:        c.RxBytes,
			ConnectedSince: connSince,
		})
	}

	data, err := json.Marshal(infos)
	if err != nil {
		e.logger.Error("failed to marshal client snapshot", zap.Error(err))
		return
	}

	if err := e.redisStore.SetClientSnapshot(ctx, deviceID.String(), data, e.cfg.ClientSnapshotTTL); err != nil {
		e.logger.Error("failed to store client snapshot in Redis",
			zap.String("device_id", deviceID.String()),
			zap.Error(err),
		)
	}
}

// TestSwapBuffer exposes the buffer swap for testing. Only use in tests.
func (e *Engine) TestSwapBuffer() *MetricsBatch {
	return e.buffer.Swap()
}
