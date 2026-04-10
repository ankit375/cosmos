package websocket

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/yourorg/cloudctrl/internal/model"
	"github.com/yourorg/cloudctrl/internal/protocol"
	"go.uber.org/zap"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

// HandleWebSocket is the gin handler for WebSocket device connections.
func (h *Hub) HandleWebSocket(c *gin.Context) {
	upgrader.ReadBufferSize = h.cfg.ReadBufferSize
	upgrader.WriteBufferSize = h.cfg.WriteBufferSize

	clientIP := c.ClientIP()
	if !h.AllowConnection(clientIP) {
		h.logger.Warn("connection rate limited",
			zap.String("ip", clientIP),
		)
		wsConnectionErrors.WithLabelValues("rate_limited").Inc()
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "rate limited"})
		return
	}

	wsConn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		h.logger.Error("websocket upgrade failed",
			zap.String("ip", clientIP),
			zap.Error(err),
		)
		wsConnectionErrors.WithLabelValues("upgrade_failed").Inc()
		return
	}

	handshakeStart := time.Now()

	conn := NewDeviceConnection(wsConn, h, h.logger.Named("conn"))

	// Wait for DeviceAuth message
	wsConn.SetReadDeadline(time.Now().Add(h.cfg.AuthTimeout))
	_, data, err := wsConn.ReadMessage()
	if err != nil {
		h.logger.Warn("failed to read auth message",
			zap.String("ip", clientIP),
			zap.Error(err),
		)
		wsConnectionErrors.WithLabelValues("auth_timeout").Inc()
		wsConn.Close()
		return
	}

	msg, err := protocol.DecodeMessage(data)
	if err != nil {
		h.logger.Warn("failed to decode auth message",
			zap.String("ip", clientIP),
			zap.Error(err),
		)
		wsConnectionErrors.WithLabelValues("auth_decode_error").Inc()
		wsConn.Close()
		return
	}

	if msg.Header.MsgType != protocol.MsgDeviceAuth {
		h.logger.Warn("first message is not DeviceAuth",
			zap.String("ip", clientIP),
			zap.String("msg_type", protocol.MsgTypeName(msg.Header.MsgType)),
		)
		wsConnectionErrors.WithLabelValues("auth_wrong_type").Inc()
		conn.respondError(msg.Header.MessageID, "AUTH_REQUIRED", "First message must be DeviceAuth")
		wsConn.Close()
		return
	}

	result, err := h.authenticateDevice(conn, msg)
	if err != nil {
		h.logger.Error("device authentication error",
			zap.String("ip", clientIP),
			zap.Error(err),
		)
		wsConnectionErrors.WithLabelValues("auth_error").Inc()
		conn.respondError(msg.Header.MessageID, "AUTH_ERROR", "Internal authentication error")
		wsConn.Close()
		return
	}

	authResp := h.buildAuthResult(result)
	respData, _, err := protocol.EncodeMessage(
		protocol.ChannelControl,
		protocol.MsgAuthResult,
		protocol.FlagResponse,
		authResp,
	)
	if err != nil {
		h.logger.Error("failed to encode auth result", zap.Error(err))
		wsConn.Close()
		return
	}

	wsConn.SetWriteDeadline(time.Now().Add(h.cfg.WriteWait))
	if err := wsConn.WriteMessage(websocket.BinaryMessage, respData); err != nil {
		h.logger.Error("failed to send auth result", zap.Error(err))
		wsConn.Close()
		return
	}

	wsHandshakeDuration.Observe(time.Since(handshakeStart).Seconds())

	// If not authenticated, close after sending the response
	if !result.Authenticated {
		h.logger.Info("device auth rejected",
			zap.String("ip", clientIP),
			zap.String("status", authResp.Status),
		)

		if result.Device != nil {
			h.stateStore.Set(&DeviceState{
				DeviceID:        result.Device.ID,
				TenantID:        result.Device.TenantID,
				SiteID:          result.Device.SiteID,
				Status:          result.Device.Status,
				FirmwareVersion: result.Device.FirmwareVersion,
				LastHeartbeat:   time.Now(),
				Dirty:           true,
			})
		}

		wsConn.Close()
		return
	}

	// ── Authenticated! Set up the persistent connection ──────

	device := result.Device

	conn.DeviceID = device.ID
	conn.TenantID = device.TenantID
	conn.SiteID = device.SiteID
	conn.MAC = device.MAC
	conn.authenticated = true

	// Check if this device was previously offline (for reconnect event) (NEW)
	previousState := h.stateStore.Get(device.ID)
	wasOffline := previousState != nil && previousState.Status == model.DeviceStatusOffline
	var offlineSince time.Time
	if wasOffline && !previousState.LastHeartbeat.IsZero() {
		offlineSince = previousState.LastHeartbeat
	}

	// Initialize state
	h.stateStore.Set(&DeviceState{
		DeviceID:             device.ID,
		TenantID:             device.TenantID,
		SiteID:               device.SiteID,
		Status:               model.DeviceStatusOnline,
		FirmwareVersion:      device.FirmwareVersion,
		DesiredConfigVersion: device.DesiredConfigVersion,
		AppliedConfigVersion: device.AppliedConfigVersion,
		IPAddress:            clientIP,
		LastHeartbeat:        time.Now(),
		Dirty:                true,
	})

	// Register with hub
	h.register <- conn

	// Reset read deadline for normal operation
	wsConn.SetReadDeadline(time.Time{})

	// Start the per-connection goroutines
	go conn.writePump()
	go conn.readPump()

	wsDevicesByStatus.WithLabelValues(string(model.DeviceStatusOnline)).Inc()

	// Emit reconnect/online events (NEW)
	if wasOffline {
		offlineDuration := time.Since(offlineSince)
		go h.eventEmitter.DeviceReconnected(h.ctx, device.TenantID, device.ID, offlineDuration)
		wsDevicesByStatus.WithLabelValues(string(model.DeviceStatusOffline)).Dec()
		wsDeviceStateTransitions.WithLabelValues(string(model.DeviceStatusOffline), string(model.DeviceStatusOnline)).Inc()
	} else if previousState == nil || previousState.Status == model.DeviceStatusProvisioning {
		go h.eventEmitter.DeviceOnline(h.ctx, device.TenantID, device.ID, clientIP, device.FirmwareVersion)
		wsDeviceStateTransitions.WithLabelValues(string(model.DeviceStatusProvisioning), string(model.DeviceStatusOnline)).Inc()
	}

	h.logger.Info("device authenticated and connected",
		zap.String("device_id", device.ID.String()),
		zap.String("mac", device.MAC),
		zap.String("model", device.Model),
		zap.String("firmware", device.FirmwareVersion),
		zap.String("ip", clientIP),
		zap.Bool("was_offline", wasOffline),
		zap.Duration("handshake", time.Since(handshakeStart)),
	)
}
