package websocket

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/yourorg/cloudctrl/internal/model"
	pgstore "github.com/yourorg/cloudctrl/internal/store/postgres"
	"go.uber.org/zap"
)

// runStatePersister periodically flushes dirty device state to PostgreSQL.
func (h *Hub) runStatePersister() {
	defer h.wg.Done()

	ticker := time.NewTicker(h.cfg.StatePersistInterval)
	defer ticker.Stop()

	h.logger.Info("state persister started",
		zap.Duration("interval", h.cfg.StatePersistInterval),
	)

	for {
		select {
		case <-ticker.C:
			h.persistDirtyState()
		case <-h.ctx.Done():
			h.persistDirtyStateFinal()
			h.logger.Info("state persister stopped")
			return
		}
	}
}

func (h *Hub) persistDirtyStateFinal() {
	dirty := h.stateStore.CollectDirty()
	if len(dirty) == 0 {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	updates := h.buildStateUpdates(dirty)

	if err := h.pgStore.Devices.BatchUpdateState(ctx, updates); err != nil {
		h.logger.Warn("failed to persist device state on shutdown",
			zap.Int("batch_size", len(updates)),
			zap.Error(err),
		)
		return
	}

	h.logger.Info("final state flush completed", zap.Int("count", len(updates)))
}

func (h *Hub) persistDirtyState() {
	dirty := h.stateStore.CollectDirty()
	if len(dirty) == 0 {
		return
	}

	start := time.Now()

	updates := h.buildStateUpdates(dirty)

	ctx, cancel := context.WithTimeout(h.ctx, 10*time.Second)
	defer cancel()

	if err := h.pgStore.Devices.BatchUpdateState(ctx, updates); err != nil {
		h.logger.Error("failed to persist device state",
			zap.Int("batch_size", len(updates)),
			zap.Error(err),
		)
		// Mark them dirty again so we retry next cycle
		for _, state := range dirty {
			h.stateStore.Update(state.DeviceID, func(s *DeviceState) {
				s.Dirty = true
			})
		}
		return
	}

	duration := time.Since(start)
	wsStatePersistDuration.Observe(duration.Seconds())
	wsStatePersistBatchSize.Observe(float64(len(updates)))

	h.logger.Debug("persisted device state",
		zap.Int("count", len(updates)),
		zap.Duration("duration", duration),
	)
}

// buildStateUpdates converts dirty DeviceState entries to DB update structs.
func (h *Hub) buildStateUpdates(dirty []*DeviceState) []pgstore.DeviceStateUpdate {
	updates := make([]pgstore.DeviceStateUpdate, 0, len(dirty))
	for _, state := range dirty {
		var lastSeen interface{}
		if !state.LastHeartbeat.IsZero() {
			lastSeen = state.LastHeartbeat
		}

		ip := state.IPAddress
		var ipPtr *string
		if ip != "" {
			ipPtr = &ip
		}

		updates = append(updates, pgstore.DeviceStateUpdate{
			DeviceID:             state.DeviceID,
			Status:               state.Status,
			LastSeen:             lastSeen,
			IPAddress:            ipPtr,
			Uptime:               state.Uptime,
			AppliedConfigVersion: state.AppliedConfigVersion,
		})
	}
	return updates
}

// runOfflineDetector checks for devices that have missed heartbeats.
func (h *Hub) runOfflineDetector() {
	defer h.wg.Done()

	ticker := time.NewTicker(h.cfg.OfflineCheckInterval)
	defer ticker.Stop()

	h.logger.Info("offline detector started",
		zap.Duration("interval", h.cfg.OfflineCheckInterval),
		zap.Duration("timeout", h.cfg.HeartbeatTimeout),
	)

	for {
		select {
		case <-ticker.C:
			h.detectOfflineDevices()
		case <-h.ctx.Done():
			h.logger.Info("offline detector stopped")
			return
		}
	}
}

func (h *Hub) detectOfflineDevices() {
	offlined := h.stateStore.SetOffline(h.cfg.HeartbeatTimeout)

	for _, deviceID := range offlined {
		h.logger.Warn("device marked offline (heartbeat timeout)",
			zap.String("device_id", deviceID.String()),
			zap.Duration("timeout", h.cfg.HeartbeatTimeout),
		)

		wsOfflineDetections.Inc()
		wsDevicesByStatus.WithLabelValues(string(model.DeviceStatusOffline)).Inc()
		wsDevicesByStatus.WithLabelValues(string(model.DeviceStatusOnline)).Dec()
		wsDeviceStateTransitions.WithLabelValues(string(model.DeviceStatusOnline), string(model.DeviceStatusOffline)).Inc()

		// Emit offline event (NEW)
		state := h.stateStore.Get(deviceID)
		if state != nil {
			go h.eventEmitter.DeviceOffline(h.ctx, state.TenantID, deviceID, state.LastHeartbeat)
		}

		// Close the WebSocket connection if still open (NEW)
		if conn := h.GetConnection(deviceID); conn != nil {
			conn.Close()
		}
	}
}

// LoadStateFromDB loads all non-decommissioned devices into the state store on startup.
func (h *Hub) LoadStateFromDB(ctx context.Context) error {
	h.logger.Info("loading device state from database...")

	rows, err := h.pgStore.Pool.Query(ctx, `
		SELECT id, tenant_id, site_id, status, firmware_version,
		       desired_config_version, applied_config_version,
		       ip_address, uptime, last_seen
		FROM devices
		WHERE status != 'decommissioned'`)
	if err != nil {
		return err
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var (
			deviceID             uuid.UUID
			tenantID             uuid.UUID
			siteID               *uuid.UUID
			status               model.DeviceStatus
			firmwareVersion      string
			desiredConfigVersion int64
			appliedConfigVersion int64
			ipAddress            *string
			uptime               int64
			lastSeen             *time.Time
		)

		if err := rows.Scan(
			&deviceID, &tenantID, &siteID, &status, &firmwareVersion,
			&desiredConfigVersion, &appliedConfigVersion,
			&ipAddress, &uptime, &lastSeen,
		); err != nil {
			return err
		}

		state := &DeviceState{
			DeviceID:             deviceID,
			TenantID:             tenantID,
			SiteID:               siteID,
			Status:               model.DeviceStatusOffline, // Conservative: all offline until reconnect
			FirmwareVersion:      firmwareVersion,
			DesiredConfigVersion: desiredConfigVersion,
			AppliedConfigVersion: appliedConfigVersion,
			Uptime:               uptime,
			Dirty:                true, // Will persist the offline status
		}

		if ipAddress != nil {
			state.IPAddress = *ipAddress
		}
		if lastSeen != nil {
			state.LastHeartbeat = *lastSeen
		}

		h.stateStore.Set(state)
		count++
	}

	if err := rows.Err(); err != nil {
		return err
	}

	h.logger.Info("loaded device state from database",
		zap.Int("device_count", count),
	)

	return nil
}
