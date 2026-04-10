package websocket

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/yourorg/cloudctrl/internal/model"
	"github.com/yourorg/cloudctrl/internal/protocol"
	"go.uber.org/zap"
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

		// >>> NEW: Push pending config on reconnect <<<
		h.mu.RLock()
		cm := h.configManager
		h.mu.RUnlock()

		if cm != nil {
			go cm.PushPendingConfigOnReconnect(conn.TenantID, conn.DeviceID)
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
	)
}

// getPendingCommandCount returns the number of pending commands for a device.
// Queries the database command_queue table.
func (h *Hub) getPendingCommandCount(deviceID uuid.UUID) int {
	ctx, cancel := context.WithTimeout(h.ctx, 2*time.Second)
	defer cancel()

	var count int
	err := h.pgStore.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM command_queue
		 WHERE device_id = \$1 AND status = 'queued'`,
		deviceID,
	).Scan(&count)
	if err != nil {
		// Non-fatal — return 0 if we can't query
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
		// Emit config failure event (NEW)
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

	// Update command status in DB (NEW)
	go h.updateCommandStatus(conn.DeviceID, msg.Header.MessageID, payload.Success, payload.Error)
}

// updateCommandStatus marks a command as completed or failed in the database.
func (h *Hub) updateCommandStatus(deviceID uuid.UUID, msgID uint32, success bool, errMsg string) {
	ctx, cancel := context.WithTimeout(h.ctx, 3*time.Second)
	defer cancel()

	status := "completed"
	if !success {
		status = "failed"
	}

	_, err := h.pgStore.Pool.Exec(ctx,
		`UPDATE command_queue SET
			status = \$1,
			error_message = \$2,
			completed_at = NOW()
		WHERE device_id = \$3 AND status = 'sent'
		ORDER BY created_at ASC
		LIMIT 1`,
		status, errMsg, deviceID)
	if err != nil {
		h.logger.Error("failed to update command status",
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

	// Persist event to database (NEW)
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
func (h *Hub) handleMetricsReport(conn *DeviceConnection, msg *protocol.Message) {
	// Update last metrics time in state
	h.stateStore.Update(conn.DeviceID, func(state *DeviceState) {
		state.LastMetrics = time.Now()
	})

	conn.logger.Debug("metrics report received",
		zap.String("device_id", conn.DeviceID.String()),
		zap.Int("payload_size", len(msg.Payload)),
	)

	// TODO: Phase 5+ — parse full metrics, buffer, and batch flush to TimescaleDB
}

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

	// TODO: Phase 5+ — update client session tracking
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

	// TODO: Phase 5+ — update firmware_upgrade_tasks
}

// handlePing responds to an application-level ping with a pong.
func (h *Hub) handlePing(conn *DeviceConnection, msg *protocol.Message) {
	conn.SendMessage(protocol.ChannelControl, protocol.MsgPong, protocol.FlagResponse, nil)
}
