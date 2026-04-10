package websocket

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/yourorg/cloudctrl/internal/protocol"
	"go.uber.org/zap"
)

// DeviceConnection represents a single WebSocket connection to a device.
type DeviceConnection struct {
	// Identity
	DeviceID   uuid.UUID
	TenantID   uuid.UUID
	SiteID     *uuid.UUID
	MAC        string
	RemoteAddr string

	// WebSocket
	conn *websocket.Conn

	// Channels
	sendCh chan []byte
	done   chan struct{}

	// State
	connectedAt   time.Time
	lastPingSent  time.Time
	lastPongRecv  time.Time
	authenticated bool

	// Rate limiting
	msgLimiter *MessageRateLimiter

	// References
	hub    *Hub
	logger *zap.Logger

	// Close once
	closeOnce sync.Once
}

// NewDeviceConnection creates a new device connection wrapper.
func NewDeviceConnection(
	conn *websocket.Conn,
	hub *Hub,
	logger *zap.Logger,
) *DeviceConnection {
	return &DeviceConnection{
		conn:        conn,
		sendCh:      make(chan []byte, hub.cfg.SendChannelSize),
		done:        make(chan struct{}),
		connectedAt: time.Now(),
		hub:         hub,
		logger:      logger,
		RemoteAddr:  conn.RemoteAddr().String(),
		msgLimiter: NewMessageRateLimiter(
			hub.cfg.ControlRateLimit,
			hub.cfg.TelemetryRateLimit,
			hub.cfg.BulkRateLimit,
		),
	}
}

// ConnectionDuration returns how long this connection has been alive.
func (dc *DeviceConnection) ConnectionDuration() time.Duration {
	return time.Since(dc.connectedAt)
}

// Send queues a raw wire message for sending to the device.
// Returns false if the send channel is full (message dropped).
func (dc *DeviceConnection) Send(data []byte) bool {
	select {
	case dc.sendCh <- data:
		wsMessageBytesSent.Add(float64(len(data)))
		return true
	default:
		wsMessagesDropped.WithLabelValues("channel_full").Inc()
		dc.logger.Warn("send channel full, dropping message",
			zap.String("device_id", dc.DeviceID.String()),
		)
		return false
	}
}

// SendMessage encodes and sends a protocol message.
func (dc *DeviceConnection) SendMessage(channel uint8, msgType uint16, flags uint8, payload interface{}) (uint32, error) {
	data, msgID, err := protocol.EncodeMessage(channel, msgType, flags, payload)
	if err != nil {
		return 0, fmt.Errorf("encode message: %w", err)
	}
	if !dc.Send(data) {
		return 0, fmt.Errorf("send channel full")
	}
	wsMessagesSent.WithLabelValues(
		protocol.ChannelName(channel),
		protocol.MsgTypeName(msgType),
	).Inc()
	return msgID, nil
}

// Close closes the connection and signals goroutines to stop.
func (dc *DeviceConnection) Close() {
	dc.closeOnce.Do(func() {
		close(dc.done)
		dc.conn.Close()
		dc.logger.Info("device connection closed",
			zap.String("device_id", dc.DeviceID.String()),
			zap.String("mac", dc.MAC),
			zap.Duration("duration", dc.ConnectionDuration()),
		)
	})
}

// readPump reads messages from the WebSocket connection.
// Runs in its own goroutine. On read error, closes the connection.
func (dc *DeviceConnection) readPump() {
	defer func() {
		dc.hub.unregister <- dc
		dc.Close()
	}()

	dc.conn.SetReadLimit(protocol.MaxPayloadSize + protocol.HeaderSize)
	dc.conn.SetReadDeadline(time.Now().Add(dc.hub.cfg.PongWait * 3))

	dc.conn.SetPongHandler(func(string) error {
		dc.lastPongRecv = time.Now()
		dc.conn.SetReadDeadline(time.Now().Add(dc.hub.cfg.PongWait * 3))
		return nil
	})

	for {
		_, data, err := dc.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err,
				websocket.CloseGoingAway,
				websocket.CloseNormalClosure,
				websocket.CloseNoStatusReceived,
			) {
				dc.logger.Warn("unexpected websocket close",
					zap.String("device_id", dc.DeviceID.String()),
					zap.Error(err),
				)
				wsConnectionErrors.WithLabelValues("read_error").Inc()
			}
			return
		}

		wsMessageBytesReceived.Add(float64(len(data)))

		// Decode message
		msg, err := protocol.DecodeMessage(data)
		if err != nil {
			dc.logger.Warn("failed to decode message",
				zap.String("device_id", dc.DeviceID.String()),
				zap.Error(err),
			)
			wsMessageErrors.WithLabelValues("unknown", "decode_error").Inc()
			continue
		}

		// Rate limit by channel
		if !dc.msgLimiter.Allow(msg.Header.Channel) {
			dc.logger.Debug("message rate limited",
				zap.String("device_id", dc.DeviceID.String()),
				zap.String("channel", protocol.ChannelName(msg.Header.Channel)),
				zap.String("msg_type", protocol.MsgTypeName(msg.Header.MsgType)),
			)
			wsMessagesDropped.WithLabelValues("rate_limited").Inc()
			continue
		}

		wsMessagesReceived.WithLabelValues(
			protocol.ChannelName(msg.Header.Channel),
			protocol.MsgTypeName(msg.Header.MsgType),
		).Inc()

		// Route to handler
		dc.hub.handleMessage(dc, msg)
	}
}

// writePump writes messages to the WebSocket connection.
// Runs in its own goroutine. Handles WS ping/pong.
func (dc *DeviceConnection) writePump() {
	ticker := time.NewTicker(dc.hub.cfg.PingInterval)
	defer func() {
		ticker.Stop()
		dc.Close()
	}()

	for {
		select {
		case data, ok := <-dc.sendCh:
			dc.conn.SetWriteDeadline(time.Now().Add(dc.hub.cfg.WriteWait))
			if !ok {
				// Channel closed
				dc.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			if err := dc.conn.WriteMessage(websocket.BinaryMessage, data); err != nil {
				dc.logger.Debug("write error",
					zap.String("device_id", dc.DeviceID.String()),
					zap.Error(err),
				)
				wsConnectionErrors.WithLabelValues("write_error").Inc()
				return
			}

			// Coalesce: write any other queued messages without blocking
			n := len(dc.sendCh)
			for i := 0; i < n; i++ {
				msg := <-dc.sendCh
				dc.conn.SetWriteDeadline(time.Now().Add(dc.hub.cfg.WriteWait))
				if err := dc.conn.WriteMessage(websocket.BinaryMessage, msg); err != nil {
					wsConnectionErrors.WithLabelValues("write_error").Inc()
					return
				}
			}

		case <-ticker.C:
			dc.conn.SetWriteDeadline(time.Now().Add(dc.hub.cfg.WriteWait))
			dc.lastPingSent = time.Now()
			if err := dc.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				dc.logger.Debug("ping write error",
					zap.String("device_id", dc.DeviceID.String()),
					zap.Error(err),
				)
				return
			}

		case <-dc.done:
			return
		}
	}
}

// respondError sends an error message back to the device.
func (dc *DeviceConnection) respondError(msgID uint32, code string, message string) {
	payload := protocol.ErrorPayload{
		Code:    code,
		Message: message,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}

	header := protocol.Header{
		Version:   protocol.ProtocolVersion,
		Channel:   protocol.ChannelControl,
		MsgType:   protocol.MsgError,
		Flags:     protocol.FlagResponse,
		MessageID: msgID,
		Length:    uint32(len(data)),
	}

	msg := make([]byte, protocol.HeaderSize+len(data))
	copy(msg[:protocol.HeaderSize], header.Encode())
	copy(msg[protocol.HeaderSize:], data)

	dc.Send(msg)
}
