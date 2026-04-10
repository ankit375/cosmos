package websocket

import (
	"encoding/json"

	"github.com/yourorg/cloudctrl/internal/configmgr"
	"github.com/yourorg/cloudctrl/internal/protocol"
	"go.uber.org/zap"
)

// SetConfigManager sets the config manager on the hub.
// Called during application startup after both hub and config manager are created.
func (h *Hub) SetConfigManager(cm *configmgr.Manager) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.configManager = cm

	// Replace the ConfigAck handler with the config-manager-aware version
	h.handlers[protocol.MsgConfigAck] = h.handleConfigAckWithManager
}

// ConfigManager returns the hub's config manager (for API handler access).
func (h *Hub) ConfigManager() *configmgr.Manager {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.configManager
}

// handleConfigAckWithManager processes a ConfigAck using the config manager's safe-apply flow.
func (h *Hub) handleConfigAckWithManager(conn *DeviceConnection, msg *protocol.Message) {
	var payload protocol.ConfigAckPayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		wsMessageErrors.WithLabelValues("ConfigAck", "decode_error").Inc()
		conn.logger.Warn("invalid ConfigAck payload",
			zap.String("device_id", conn.DeviceID.String()),
			zap.Error(err),
		)
		return
	}

	conn.logger.Info("config ack received",
		zap.String("device_id", conn.DeviceID.String()),
		zap.Int64("version", payload.Version),
		zap.Bool("success", payload.Success),
		zap.String("error", payload.Error),
		zap.Strings("warnings", payload.Warnings),
	)

	// Update in-memory state
	if payload.Success {
		h.stateStore.Update(conn.DeviceID, func(state *DeviceState) {
			state.AppliedConfigVersion = payload.Version
			state.Dirty = true
		})
	}

	// Delegate to config manager for safe-apply protocol
	h.mu.RLock()
	cm := h.configManager
	h.mu.RUnlock()

	if cm != nil {
		cm.HandleConfigAck(conn.DeviceID, conn.TenantID, &payload)
	} else {
		// Fallback: no config manager (shouldn't happen in production)
		conn.logger.Warn("config manager not set, falling back to basic ConfigAck handling",
			zap.String("device_id", conn.DeviceID.String()),
		)
		h.handleConfigAckBasic(conn, &payload)
	}

	if len(payload.Warnings) > 0 {
		conn.logger.Info("config apply warnings",
			zap.String("device_id", conn.DeviceID.String()),
			zap.Strings("warnings", payload.Warnings),
		)
	}
}

// handleConfigAckBasic is the original simple handler (fallback).
func (h *Hub) handleConfigAckBasic(conn *DeviceConnection, payload *protocol.ConfigAckPayload) {
	if payload.Success {
		conn.logger.Info("config applied successfully (basic handler)",
			zap.String("device_id", conn.DeviceID.String()),
			zap.Int64("version", payload.Version),
		)
	} else {
		conn.logger.Warn("config apply failed (basic handler)",
			zap.String("device_id", conn.DeviceID.String()),
			zap.Int64("version", payload.Version),
			zap.String("error", payload.Error),
		)
	}
}
