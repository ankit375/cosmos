package websocket

import (
	"context"
	"encoding/json"
	"time"

	"github.com/yourorg/cloudctrl/internal/model"
	"github.com/yourorg/cloudctrl/internal/protocol"
	"go.uber.org/zap"
	"github.com/google/uuid"
)

// handleMessage dispatches a message to the appropriate handler.
func (h *Hub) handleMessage(conn *DeviceConnection, msg *protocol.Message) {
	handler, ok := h.handlers[msg.Header.MsgType]
	if !ok {
		conn.logger.Debug("unhandled message type",
			zap.String("msg_type", protocol.MsgTypeName(msg.Header.MsgType)),
			zap.String("device_id", conn.DeviceID.String()),
		)
		wsMessageErrors.WithLabelValues(protocol.MsgTypeName(msg.Header.MsgType), "unhandled").Inc()
		return
	}
	handler(conn, msg)
}

// handleHeartbeat processes a heartbeat from a device.
func (h *Hub) handleHeartbeat(conn *DeviceConnection, msg *protocol.Message) {
	var payload protocol.HeartbeatPayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		conn.logger.Warn("invalid heartbeat payload",
			zap.String("device_id", conn.DeviceID.String()),
			zap.Error(err),
		)
		wsMessageErrors.WithLabelValues("Heartbeat", "decode_error").Inc()
		return
	}

	now := time.Now()
	var previousStatus model.DeviceStatus

	// Update in-memory state
	h.stateStore.Update(conn.DeviceID, func(state *DeviceState) {
		previousStatus = state.Status
		state.Status = model.DeviceStatusOnline
		state.Uptime = payload.Uptime
		state.FirmwareVersion = payload.FirmwareVersion
		state.AppliedConfigVersion = payload.ConfigVersion
		state.IPAddress = payload.IPAddress
		state.ClientCount = payload.ClientCount
		state.CPUUsage = payload.CPUUsage
		state.MemoryUsed = payload.MemoryUsed
		state.MemoryTotal = payload.MemoryTotal
		state.LastHeartbeat = now
		state.Dirty = true
	})

	wsHeartbeatsProcessed.Inc()

	// Detect state transitions
	if previousStatus != model.DeviceStatusOnline && previousStatus != "" {
		wsDeviceStateTransitions.WithLabelValues(string(previousStatus), string(model.DeviceStatusOnline)).Inc()
		wsDevicesByStatus.WithLabelValues(string(model.DeviceStatusOnline)).Inc()
		if previousStatus == model.DeviceStatusOffline {
			wsDevicesByStatus.WithLabelValues(string(model.DeviceStatusOffline)).Dec()
		}

		// Emit events based on previous state
		state := h.stateStore.Get(conn.DeviceID)
		if state != nil {
			switch previousStatus {
			case model.DeviceStatusOffline:
				go h.eventEmitter.DeviceReconnected(h.ctx, conn.TenantID, conn.DeviceID,
					now.Sub(state.LastHeartbeat))
			case model.DeviceStatusProvisioning:
				go h.eventEmitter.DeviceOnline(h.ctx, conn.TenantID, conn.DeviceID,
					payload.IPAddress, payload.FirmwareVersion)
			}
		}

		// Push pending config on reconnect
		h.mu.RLock()
		cm := h.configManager
		h.mu.RUnlock()

		if cm != nil {
			go cm.PushPendingConfigOnReconnect(conn.TenantID, conn.DeviceID)
		}

		// Deliver queued commands on reconnect
		h.mu.RLock()
		cmdMgr := h.commandManager
		h.mu.RUnlock()

		if cmdMgr != nil {
			go cmdMgr.DeliverQueuedCommands(conn.DeviceID)
		}
	}

	// Get current state for the ack
	state := h.stateStore.Get(conn.DeviceID)
	var desiredVersion int64
	var firmwareTarget string
	if state != nil {
		desiredVersion = state.DesiredConfigVersion
	}

	// Count pending commands for this device
	pendingCommands := h.getPendingCommandCount(conn.DeviceID)

	// Send HeartbeatAck
	ack := protocol.HeartbeatAckPayload{
		ServerTime:      time.Now().Unix(),
		ConfigVersion:   desiredVersion,
		CommandsPending: pendingCommands,
		FirmwareTarget:  firmwareTarget,
	}

	conn.SendMessage(protocol.ChannelControl, protocol.MsgHeartbeatAck, protocol.FlagResponse, &ack)

	conn.logger.Debug("heartbeat processed",
		zap.String("device_id", conn.DeviceID.String()),
		zap.Int64("uptime", payload.Uptime),
		zap.Int("clients", payload.ClientCount),
		zap.Float64("cpu", payload.CPUUsage),
		zap.String("prev_status", string(previousStatus)),
		zap.Int("pending_commands", pendingCommands),
	)
}

// getPendingCommandCount returns the number of pending commands for a device.
// Uses the command manager if available, falls back to direct DB query.
func (h *Hub) getPendingCommandCount(deviceID uuid.UUID) int {
	h.mu.RLock()
	cm := h.commandManager
	h.mu.RUnlock()

	if cm != nil {
		return cm.GetPendingCount(deviceID)
	}

	// Fallback: query DB directly
	ctx, cancel := context.WithTimeout(h.ctx, 2*time.Second)
	defer cancel()

	var count int
	err := h.pgStore.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM command_queue WHERE device_id = \$1 AND status = 'queued'`,
		deviceID,
	).Scan(&count)
	if err != nil {
		return 0
	}
	return count
}

// handleConfigAck processes a config acknowledgement from a device.
func (h *Hub) handleConfigAck(conn *DeviceConnection, msg *protocol.Message) {
	var payload protocol.ConfigAckPayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		wsMessageErrors.WithLabelValues("ConfigAck", "decode_error").Inc()
		return
	}

	if payload.Success {
		h.stateStore.Update(conn.DeviceID, func(state *DeviceState) {
			state.AppliedConfigVersion = payload.Version
			state.Dirty = true
		})
		conn.logger.Info("config applied successfully",
			zap.String("device_id", conn.DeviceID.String()),
			zap.Int64("version", payload.Version),
		)
	} else {
		conn.logger.Warn("config apply failed",
			zap.String("device_id", conn.DeviceID.String()),
			zap.Int64("version", payload.Version),
			zap.String("error", payload.Error),
		)
		go h.eventEmitter.StateTransition(h.ctx, conn.TenantID, conn.DeviceID,
			model.DeviceStatusOnline, model.DeviceStatusError)
	}

	if len(payload.Warnings) > 0 {
		conn.logger.Info("config apply warnings",
			zap.String("device_id", conn.DeviceID.String()),
			zap.Strings("warnings", payload.Warnings),
		)
	}
}

// handleCommandResponse processes a command response from a device.
func (h *Hub) handleCommandResponse(conn *DeviceConnection, msg *protocol.Message) {
	var payload protocol.CommandResponsePayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		wsMessageErrors.WithLabelValues("CommandResponse", "decode_error").Inc()
		return
	}

	conn.logger.Info("command response received",
		zap.String("device_id", conn.DeviceID.String()),
		zap.Uint32("msg_id", msg.Header.MessageID),
		zap.Bool("success", payload.Success),
	)

	// Delegate to command manager for proper ACK/completion tracking
	h.mu.RLock()
	cm := h.commandManager
	h.mu.RUnlock()

	if cm != nil {
		go cm.HandleCommandResponse(
			conn.DeviceID,
			msg.Header.MessageID,
			payload.Success,
			payload.Result,
			payload.Error,
		)
		return
	}

	// Fallback: update DB directly if no command manager wired
	go h.updateCommandStatusFallback(conn.DeviceID, msg.Header.MessageID, payload.Success, payload.Error)
}

// updateCommandStatusFallback is the legacy direct-DB fallback used only when
// no command manager is wired. Prefer the command manager path.
func (h *Hub) updateCommandStatusFallback(deviceID uuid.UUID, msgID uint32, success bool, errMsg string) {
	ctx, cancel := context.WithTimeout(h.ctx, 3*time.Second)
	defer cancel()

	status := model.CommandStatusCompleted
	if !success {
		status = model.CommandStatusFailed
	}

	// PostgreSQL doesn't support ORDER BY + LIMIT in UPDATE directly.
	// Use a subquery to find the oldest sent command for this device.
	_, err := h.pgStore.Pool.Exec(ctx,
		`UPDATE command_queue SET
			status = \$1,
			error_message = \$2,
			completed_at = NOW()
		WHERE id = (
			SELECT id FROM command_queue
			WHERE device_id = \$3 AND status = 'sent'
			ORDER BY created_at ASC
			LIMIT 1
		)`,
		status, errMsg, deviceID)
	if err != nil {
		h.logger.Error("failed to update command status (fallback)",
			zap.String("device_id", deviceID.String()),
			zap.Error(err),
		)
	}
}

// handleEvent processes a device event.
func (h *Hub) handleEvent(conn *DeviceConnection, msg *protocol.Message) {
	var payload protocol.EventPayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		wsMessageErrors.WithLabelValues("Event", "decode_error").Inc()
		return
	}

	conn.logger.Info("device event",
		zap.String("device_id", conn.DeviceID.String()),
		zap.String("event_type", payload.EventType),
		zap.String("severity", payload.Severity),
		zap.String("message", payload.Message),
	)

	severity := model.EventSeverity(payload.Severity)
	if severity == "" {
		severity = model.SeverityInfo
	}

	go func() {
		ctx, cancel := context.WithTimeout(h.ctx, 3*time.Second)
		defer cancel()

		if err := h.pgStore.Events.Emit(ctx, conn.TenantID, conn.DeviceID,
			payload.EventType, severity, payload.Message, payload.Details); err != nil {
			h.logger.Error("failed to persist device event",
				zap.String("device_id", conn.DeviceID.String()),
				zap.Error(err),
			)
		}
	}()
}

// handleMetricsReport processes a metrics report from a device.
// handleMetricsReport processes a metrics report from a device.
func (h *Hub) handleMetricsReport(conn *DeviceConnection, msg *protocol.Message) {
	var payload protocol.MetricsReportPayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		conn.logger.Warn("invalid metrics report payload",
			zap.String("device_id", conn.DeviceID.String()),
			zap.Error(err),
		)
		wsMessageErrors.WithLabelValues("MetricsReport", "decode_error").Inc()
		return
	}

	// Update last metrics timestamp in state
	h.stateStore.Update(conn.DeviceID, func(state *DeviceState) {
		state.LastMetrics = time.Now()
	})

	// Delegate to telemetry engine
	h.mu.RLock()
	te := h.telemetryEngine
	h.mu.RUnlock()

	if te != nil {
		te.Ingest(conn.DeviceID, conn.TenantID, conn.SiteID, &payload)
	}

	conn.logger.Debug("metrics report processed",
		zap.String("device_id", conn.DeviceID.String()),
		zap.Int("radios", len(payload.Radios)),
		zap.Int("clients", len(payload.Clients)),
		zap.Int("payload_size", len(msg.Payload)),
	)
}

// handleClientEvent processes a client connect/disconnect event.
// handleClientEvent processes a client connect/disconnect event.
func (h *Hub) handleClientEvent(conn *DeviceConnection, msg *protocol.Message) {
	var payload protocol.ClientEventPayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		wsMessageErrors.WithLabelValues("ClientEvent", "decode_error").Inc()
		return
	}

	conn.logger.Debug("client event",
		zap.String("device_id", conn.DeviceID.String()),
		zap.String("event", payload.Event),
		zap.String("client_mac", payload.MAC),
		zap.String("ssid", payload.SSID),
	)

	// Delegate to telemetry engine
	h.mu.RLock()
	te := h.telemetryEngine
	h.mu.RUnlock()

	if te != nil {
		te.HandleClientEvent(conn.DeviceID, conn.TenantID, conn.SiteID, &payload)
	}
}


// handleFirmwareProgress processes a firmware upgrade progress report.
func (h *Hub) handleFirmwareProgress(conn *DeviceConnection, msg *protocol.Message) {
	var payload protocol.FirmwareProgressPayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		wsMessageErrors.WithLabelValues("FirmwareProgress", "decode_error").Inc()
		return
	}

	conn.logger.Info("firmware progress",
		zap.String("device_id", conn.DeviceID.String()),
		zap.String("firmware_id", payload.FirmwareID),
		zap.String("status", payload.Status),
		zap.Int("progress", payload.Progress),
	)

	// TODO: Phase 7+ — update firmware_upgrade_tasks
}

// handlePing responds to an application-level ping with a pong.
func (h *Hub) handlePing(conn *DeviceConnection, msg *protocol.Message) {
	conn.SendMessage(protocol.ChannelControl, protocol.MsgPong, protocol.FlagResponse, nil)
}
