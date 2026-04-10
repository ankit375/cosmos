package protocol

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHeaderEncodeDecode(t *testing.T) {
	original := Header{
		Version:   ProtocolVersion,
		Channel:   ChannelControl,
		MsgType:   MsgHeartbeat,
		Flags:     FlagACKRequired,
		MessageID: 42,
		Length:    128,
	}

	encoded := original.Encode()
	assert.Equal(t, HeaderSize, len(encoded))

	decoded, err := DecodeHeader(encoded)
	require.NoError(t, err)

	assert.Equal(t, original.Version, decoded.Version)
	assert.Equal(t, original.Channel, decoded.Channel)
	assert.Equal(t, original.MsgType, decoded.MsgType)
	assert.Equal(t, original.Flags, decoded.Flags)
	assert.Equal(t, original.MessageID, decoded.MessageID)
	assert.Equal(t, original.Length, decoded.Length)
}

func TestDecodeHeaderInvalidVersion(t *testing.T) {
	buf := make([]byte, HeaderSize)
	buf[0] = 99 // Invalid version

	_, err := DecodeHeader(buf)
	assert.ErrorContains(t, err, "unsupported protocol version")
}

func TestDecodeHeaderTooShort(t *testing.T) {
	buf := make([]byte, 5) // Too short
	_, err := DecodeHeader(buf)
	assert.ErrorContains(t, err, "buffer too small")
}

func TestDecodeHeaderPayloadTooLarge(t *testing.T) {
	h := Header{
		Version: ProtocolVersion,
		Length:  MaxPayloadSize + 1,
	}
	buf := h.Encode()
	_, err := DecodeHeader(buf)
	assert.ErrorContains(t, err, "payload too large")
}

func TestEncodeDecodeMessage(t *testing.T) {
	type TestPayload struct {
		Name  string `json:"name"`
		Value int    `json:"value"`
	}

	payload := TestPayload{Name: "test", Value: 42}

	encoded, msgID, err := EncodeMessage(ChannelControl, MsgHeartbeat, 0, payload)
	require.NoError(t, err)
	assert.True(t, msgID > 0)

	decoded, err := DecodeMessage(encoded)
	require.NoError(t, err)

	assert.Equal(t, ProtocolVersion, decoded.Header.Version)
	assert.Equal(t, ChannelControl, decoded.Header.Channel)
	assert.Equal(t, MsgHeartbeat, decoded.Header.MsgType)
	assert.Equal(t, msgID, decoded.Header.MessageID)

	var result TestPayload
	err = json.Unmarshal(decoded.Payload, &result)
	require.NoError(t, err)
	assert.Equal(t, "test", result.Name)
	assert.Equal(t, 42, result.Value)
}

func TestEncodeDecodeNilPayload(t *testing.T) {
	encoded, _, err := EncodeMessage(ChannelControl, MsgPing, 0, nil)
	require.NoError(t, err)

	decoded, err := DecodeMessage(encoded)
	require.NoError(t, err)

	assert.Equal(t, uint32(0), decoded.Header.Length)
	assert.Nil(t, decoded.Payload)
}

func TestMessageIDIncrementing(t *testing.T) {
	id1 := NextMessageID()
	id2 := NextMessageID()
	id3 := NextMessageID()

	assert.True(t, id2 > id1)
	assert.True(t, id3 > id2)
}

func TestMsgTypeName(t *testing.T) {
	assert.Equal(t, "Heartbeat", MsgTypeName(MsgHeartbeat))
	assert.Equal(t, "ConfigPush", MsgTypeName(MsgConfigPush))
	assert.Contains(t, MsgTypeName(0x9999), "Unknown")
}

func TestChannelName(t *testing.T) {
	assert.Equal(t, "Control", ChannelName(ChannelControl))
	assert.Equal(t, "Telemetry", ChannelName(ChannelTelemetry))
	assert.Equal(t, "Bulk", ChannelName(ChannelBulk))
	assert.Contains(t, ChannelName(99), "Unknown")
}
