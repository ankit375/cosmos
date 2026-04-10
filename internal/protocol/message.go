package protocol

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"time"
)

// Protocol constants
const (
	ProtocolVersion uint8 = 1
	HeaderSize            = 13
	MaxPayloadSize        = 1 << 20 // 1MB
)

// Channels
const (
	ChannelControl   uint8 = 0x00
	ChannelTelemetry uint8 = 0x01
	ChannelBulk      uint8 = 0x02
)

// Flags
const (
	FlagCompressed  uint8 = 0x01
	FlagACKRequired uint8 = 0x02
	FlagResponse    uint8 = 0x04
)

// Message types: Device → Controller (0x0001 - 0x00FF)
const (
	MsgDeviceAuth      uint16 = 0x0001
	MsgHeartbeat       uint16 = 0x0002
	MsgConfigAck       uint16 = 0x0003
	MsgCommandResponse uint16 = 0x0004
	MsgEvent           uint16 = 0x0005
	MsgMetricsReport   uint16 = 0x0010
	MsgClientEvent     uint16 = 0x0011
	MsgLogBatch        uint16 = 0x0020
	MsgScanResult      uint16 = 0x0021
	MsgDiagnosticDump  uint16 = 0x0022
	MsgFirmwareProgress uint16 = 0x0023
)

// Message types: Controller → Device (0x8001 - 0x80FF)
const (
	MsgAuthResult      uint16 = 0x8001
	MsgHeartbeatAck    uint16 = 0x8002
	MsgConfigPush      uint16 = 0x8003
	MsgConfigConfirm   uint16 = 0x8004
	MsgCommand         uint16 = 0x8005
	MsgFirmwareUpgrade uint16 = 0x8010
	MsgReboot          uint16 = 0x8011
	MsgLEDLocate       uint16 = 0x8012
	MsgKickClient      uint16 = 0x8013
	MsgScanRequest     uint16 = 0x8014
	MsgSpeedTest       uint16 = 0x8015
)

// Bidirectional messages (0xFF01 - 0xFFFF)
const (
	MsgPing  uint16 = 0xFF01
	MsgPong  uint16 = 0xFF02
	MsgError uint16 = 0xFF03
)

// Global message ID counter
var messageIDCounter uint32

// NextMessageID returns a unique message ID.
func NextMessageID() uint32 {
	return atomic.AddUint32(&messageIDCounter, 1)
}

// Header is the binary message header.
type Header struct {
	Version   uint8
	Channel   uint8
	MsgType   uint16
	Flags     uint8
	MessageID uint32
	Length    uint32
}

// Message represents a complete protocol message.
type Message struct {
	Header  Header
	Payload json.RawMessage
}

// EncodeHeader serializes a header to bytes (big-endian).
func (h *Header) Encode() []byte {
	buf := make([]byte, HeaderSize)
	buf[0] = h.Version
	buf[1] = h.Channel
	binary.BigEndian.PutUint16(buf[2:4], h.MsgType)
	buf[4] = h.Flags
	binary.BigEndian.PutUint32(buf[5:9], h.MessageID)
	binary.BigEndian.PutUint32(buf[9:13], h.Length)
	return buf
}

// DecodeHeader deserializes a header from bytes.
func DecodeHeader(buf []byte) (*Header, error) {
	if len(buf) < HeaderSize {
		return nil, fmt.Errorf("buffer too small: need %d, got %d", HeaderSize, len(buf))
	}

	h := &Header{
		Version:   buf[0],
		Channel:   buf[1],
		MsgType:   binary.BigEndian.Uint16(buf[2:4]),
		Flags:     buf[4],
		MessageID: binary.BigEndian.Uint32(buf[5:9]),
		Length:    binary.BigEndian.Uint32(buf[9:13]),
	}

	if h.Version != ProtocolVersion {
		return nil, fmt.Errorf("unsupported protocol version: %d (expected %d)", h.Version, ProtocolVersion)
	}

	if h.Length > MaxPayloadSize {
		return nil, fmt.Errorf("payload too large: %d > %d", h.Length, MaxPayloadSize)
	}

	return h, nil
}

// EncodeMessage creates a complete wire message.
func EncodeMessage(channel uint8, msgType uint16, flags uint8, payload interface{}) ([]byte, uint32, error) {
	var data []byte
	var err error

	if payload != nil {
		data, err = json.Marshal(payload)
		if err != nil {
			return nil, 0, fmt.Errorf("marshal payload: %w", err)
		}
	}

	msgID := NextMessageID()

	header := Header{
		Version:   ProtocolVersion,
		Channel:   channel,
		MsgType:   msgType,
		Flags:     flags,
		MessageID: msgID,
		Length:    uint32(len(data)),
	}

	headerBytes := header.Encode()
	msg := make([]byte, HeaderSize+len(data))
	copy(msg[:HeaderSize], headerBytes)
	if len(data) > 0 {
		copy(msg[HeaderSize:], data)
	}

	return msg, msgID, nil
}

// DecodeMessage parses a complete wire message.
func DecodeMessage(data []byte) (*Message, error) {
	if len(data) < HeaderSize {
		return nil, fmt.Errorf("message too short: %d < %d", len(data), HeaderSize)
	}

	header, err := DecodeHeader(data[:HeaderSize])
	if err != nil {
		return nil, err
	}

	expectedLen := HeaderSize + int(header.Length)
	if len(data) < expectedLen {
		return nil, fmt.Errorf("incomplete message: expected %d bytes, got %d", expectedLen, len(data))
	}

	msg := &Message{
		Header: *header,
	}

	if header.Length > 0 {
		msg.Payload = make(json.RawMessage, header.Length)
		copy(msg.Payload, data[HeaderSize:expectedLen])
	}

	return msg, nil
}

// MsgTypeName returns the human-readable name for a message type.
func MsgTypeName(t uint16) string {
	names := map[uint16]string{
		MsgDeviceAuth:       "DeviceAuth",
		MsgHeartbeat:        "Heartbeat",
		MsgConfigAck:        "ConfigAck",
		MsgCommandResponse:  "CommandResponse",
		MsgEvent:            "Event",
		MsgMetricsReport:    "MetricsReport",
		MsgClientEvent:      "ClientEvent",
		MsgLogBatch:         "LogBatch",
		MsgScanResult:       "ScanResult",
		MsgDiagnosticDump:   "DiagnosticDump",
		MsgFirmwareProgress: "FirmwareProgress",
		MsgAuthResult:       "AuthResult",
		MsgHeartbeatAck:     "HeartbeatAck",
		MsgConfigPush:       "ConfigPush",
		MsgConfigConfirm:    "ConfigConfirm",
		MsgCommand:          "Command",
		MsgFirmwareUpgrade:  "FirmwareUpgrade",
		MsgReboot:           "Reboot",
		MsgLEDLocate:        "LEDLocate",
		MsgKickClient:       "KickClient",
		MsgScanRequest:      "ScanRequest",
		MsgSpeedTest:        "SpeedTest",
		MsgPing:             "Ping",
		MsgPong:             "Pong",
		MsgError:            "Error",
	}
	if name, ok := names[t]; ok {
		return name
	}
	return fmt.Sprintf("Unknown(0x%04X)", t)
}

// ChannelName returns the human-readable name for a channel.
func ChannelName(c uint8) string {
	switch c {
	case ChannelControl:
		return "Control"
	case ChannelTelemetry:
		return "Telemetry"
	case ChannelBulk:
		return "Bulk"
	default:
		return fmt.Sprintf("Unknown(%d)", c)
	}
}

// Timestamp helper
func NowUnixMilli() int64 {
	return time.Now().UnixMilli()
}
