//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yourorg/cloudctrl/internal/config"
	"github.com/yourorg/cloudctrl/internal/model"
	"github.com/yourorg/cloudctrl/internal/protocol"
	"github.com/yourorg/cloudctrl/pkg/crypto"
	ws "github.com/yourorg/cloudctrl/internal/websocket"
)

// ── Test Helpers ─────────────────────────────────────────────

func newTestHub(t *testing.T) *ws.Hub {
	t.Helper()

	cfg := config.WebSocketConfig{
		MaxConnections:       100,
		MaxConnectionsPerIP:  10,
		ConnectionRateLimit:  50,
		WriteWait:            10 * time.Second,
		PongWait:             30 * time.Second,
		PingInterval:         15 * time.Second,
		AuthTimeout:          5 * time.Second,
		ReadBufferSize:       4096,
		WriteBufferSize:      4096,
		SendChannelSize:      64,
		ControlRateLimit:     10,
		TelemetryRateLimit:   5,
		BulkRateLimit:        2,
		StatePersistInterval: 1 * time.Second, // fast for tests
		OfflineCheckInterval: 1 * time.Second,
		HeartbeatTimeout:     3 * time.Second,  // short for tests
	}

	hub := ws.NewHub(cfg, testPG, testLogger)
	hub.Run()
	return hub
}

func newTestWSServer(t *testing.T, hub *ws.Hub) *httptest.Server {
	t.Helper()

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/ws/device", hub.HandleWebSocket)

	server := httptest.NewServer(router)
	return server
}

func dialWS(t *testing.T, server *httptest.Server) *websocket.Conn {
	t.Helper()

	wsURL := "ws" + server.URL[4:] + "/ws/device" // http → ws
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err, "failed to dial websocket")
	return conn
}

func sendAuthMessage(t *testing.T, conn *websocket.Conn, token, mac, serial, mdl string) {
	t.Helper()

	payload := protocol.DeviceAuthPayload{
		Token:           token,
		MAC:             mac,
		Serial:          serial,
		Model:           mdl,
		FirmwareVersion: "1.0.0",
		ConfigVersion:   0,
		AgentVersion:    "0.1.0",
		Capabilities:    json.RawMessage(`{"bands":["2g","5g"]}`),
		SystemInfo:      json.RawMessage(`{"board":"test"}`),
	}

	data, _, err := protocol.EncodeMessage(
		protocol.ChannelControl,
		protocol.MsgDeviceAuth,
		0,
		&payload,
	)
	require.NoError(t, err)

	err = conn.WriteMessage(websocket.BinaryMessage, data)
	require.NoError(t, err)
}

func readAuthResult(t *testing.T, conn *websocket.Conn) *protocol.AuthResultPayload {
	t.Helper()

	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, data, err := conn.ReadMessage()
	require.NoError(t, err)

	msg, err := protocol.DecodeMessage(data)
	require.NoError(t, err)
	require.Equal(t, protocol.MsgAuthResult, msg.Header.MsgType)

	var result protocol.AuthResultPayload
	err = json.Unmarshal(msg.Payload, &result)
	require.NoError(t, err)

	return &result
}

func seedDevice(t *testing.T, tenantID uuid.UUID, siteID *uuid.UUID, mac, serial string, status model.DeviceStatus, tokenHash *string) *model.Device {
	t.Helper()
	ctx := context.Background()

	device := &model.Device{
		ID:              uuid.New(),
		TenantID:        tenantID,
		SiteID:          siteID,
		MAC:             mac,
		Serial:          serial,
		Name:            "Test AP",
		Model:           "AP-TEST",
		Status:          status,
		FirmwareVersion: "1.0.0",
		DeviceTokenHash: tokenHash,
		Capabilities:    json.RawMessage(`{}`),
		SystemInfo:      json.RawMessage(`{}`),
		Tags:            []string{},
	}

	err := testPG.Devices.Create(ctx, device)
	require.NoError(t, err)
	return device
}

// ── Tests ────────────────────────────────────────────────────

func TestWebSocket_NewDevicePendingAdoption(t *testing.T) {
	tenant := seedTenant(t, "ws-new")
	defer cleanupTenant(t, tenant.ID)

	hub := newTestHub(t)
	defer hub.Stop(context.Background())
	server := newTestWSServer(t, hub)
	defer server.Close()

	conn := dialWS(t, server)
	defer conn.Close()

	// Send auth with no token — new device
	sendAuthMessage(t, conn, "", "AA:BB:CC:DD:EE:01", "SN-TEST-001", "AP-TEST")

	result := readAuthResult(t, conn)
	assert.False(t, result.Success, "new device should not be authenticated")
	assert.Equal(t, "pending_adoption", result.Status)
	assert.NotEmpty(t, result.DeviceID, "device should be created and ID returned")
}

func TestWebSocket_AuthenticatedDevice(t *testing.T) {
	tenant := seedTenant(t, "ws-auth")
	site := seedSite(t, tenant.ID, "WS Test Site")
	defer cleanupTenant(t, tenant.ID)

	// Create device with token
	token := "test-device-token-abcdef123456"
	tokenHash := crypto.HashToken(token)
	device := seedDevice(t, tenant.ID, &site.ID, "AA:BB:CC:DD:EE:02", "SN-TEST-002",
		model.DeviceStatusOnline, &tokenHash)

	hub := newTestHub(t)
	defer hub.Stop(context.Background())
	server := newTestWSServer(t, hub)
	defer server.Close()

	conn := dialWS(t, server)
	defer conn.Close()

	sendAuthMessage(t, conn, token, device.MAC, device.Serial, device.Model)

	result := readAuthResult(t, conn)
	assert.True(t, result.Success, "device with valid token should authenticate")
	assert.Equal(t, device.ID.String(), result.DeviceID)
	assert.Equal(t, 30, result.HeartbeatInterval)
	assert.Equal(t, 60, result.MetricsInterval)

	// Verify hub has the connection
	time.Sleep(100 * time.Millisecond)
	assert.True(t, hub.IsConnected(device.ID))
	assert.Equal(t, 1, hub.Count())
}

func TestWebSocket_InvalidToken(t *testing.T) {
	tenant := seedTenant(t, "ws-badtoken")
	site := seedSite(t, tenant.ID, "WS Bad Token Site")
	defer cleanupTenant(t, tenant.ID)

	tokenHash := crypto.HashToken("real-token")
	device := seedDevice(t, tenant.ID, &site.ID, "AA:BB:CC:DD:EE:03", "SN-TEST-003",
		model.DeviceStatusOnline, &tokenHash)

	hub := newTestHub(t)
	defer hub.Stop(context.Background())
	server := newTestWSServer(t, hub)
	defer server.Close()

	conn := dialWS(t, server)
	defer conn.Close()

	// Send wrong token
	sendAuthMessage(t, conn, "wrong-token", device.MAC, device.Serial, device.Model)

	result := readAuthResult(t, conn)
	assert.False(t, result.Success)
}

func TestWebSocket_Heartbeat(t *testing.T) {
	tenant := seedTenant(t, "ws-hb")
	site := seedSite(t, tenant.ID, "WS Heartbeat Site")
	defer cleanupTenant(t, tenant.ID)

	token := "heartbeat-test-token"
	tokenHash := crypto.HashToken(token)
	device := seedDevice(t, tenant.ID, &site.ID, "AA:BB:CC:DD:EE:04", "SN-TEST-004",
		model.DeviceStatusOnline, &tokenHash)

	hub := newTestHub(t)
	defer hub.Stop(context.Background())
	server := newTestWSServer(t, hub)
	defer server.Close()

	conn := dialWS(t, server)
	defer conn.Close()

	// Authenticate
	sendAuthMessage(t, conn, token, device.MAC, device.Serial, device.Model)
	authResult := readAuthResult(t, conn)
	require.True(t, authResult.Success)

	// Send heartbeat
	hbPayload := protocol.HeartbeatPayload{
		Uptime:          3600,
		ConfigVersion:   1,
		FirmwareVersion: "1.0.0",
		ClientCount:     5,
		CPUUsage:        15.5,
		MemoryUsed:      134217728,
		MemoryTotal:     268435456,
		IPAddress:       "192.168.1.10",
		LoadAvg:         [3]float64{0.5, 0.3, 0.2},
	}

	hbData, _, err := protocol.EncodeMessage(
		protocol.ChannelControl,
		protocol.MsgHeartbeat,
		0,
		&hbPayload,
	)
	require.NoError(t, err)
	err = conn.WriteMessage(websocket.BinaryMessage, hbData)
	require.NoError(t, err)

	// Read HeartbeatAck
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, ackData, err := conn.ReadMessage()
	require.NoError(t, err)

	ackMsg, err := protocol.DecodeMessage(ackData)
	require.NoError(t, err)
	assert.Equal(t, protocol.MsgHeartbeatAck, ackMsg.Header.MsgType)

	var ack protocol.HeartbeatAckPayload
	err = json.Unmarshal(ackMsg.Payload, &ack)
	require.NoError(t, err)
	assert.True(t, ack.ServerTime > 0)

	// Verify state was updated
	state := hub.StateStore().Get(device.ID)
	require.NotNil(t, state)
	assert.Equal(t, int64(3600), state.Uptime)
	assert.Equal(t, 5, state.ClientCount)
	assert.Equal(t, 15.5, state.CPUUsage)
}

func TestWebSocket_DuplicateConnection(t *testing.T) {
	tenant := seedTenant(t, "ws-dup")
	site := seedSite(t, tenant.ID, "WS Dup Site")
	defer cleanupTenant(t, tenant.ID)

	token := "dup-test-token"
	tokenHash := crypto.HashToken(token)
	device := seedDevice(t, tenant.ID, &site.ID, "AA:BB:CC:DD:EE:05", "SN-TEST-005",
		model.DeviceStatusOnline, &tokenHash)

	hub := newTestHub(t)
	defer hub.Stop(context.Background())
	server := newTestWSServer(t, hub)
	defer server.Close()

	// First connection
	conn1 := dialWS(t, server)
	sendAuthMessage(t, conn1, token, device.MAC, device.Serial, device.Model)
	result1 := readAuthResult(t, conn1)
	require.True(t, result1.Success)

	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, 1, hub.Count())

	// Second connection (should replace first)
	conn2 := dialWS(t, server)
	sendAuthMessage(t, conn2, token, device.MAC, device.Serial, device.Model)
	result2 := readAuthResult(t, conn2)
	require.True(t, result2.Success)

	time.Sleep(200 * time.Millisecond)
	assert.Equal(t, 1, hub.Count(), "should still have exactly 1 connection")

	// Old connection should be closed
	conn1.SetReadDeadline(time.Now().Add(1 * time.Second))
	_, _, err := conn1.ReadMessage()
	assert.Error(t, err, "old connection should be closed")

	conn1.Close()
	conn2.Close()
}

func TestWebSocket_OfflineDetection(t *testing.T) {
	tenant := seedTenant(t, "ws-offline")
	site := seedSite(t, tenant.ID, "WS Offline Site")
	defer cleanupTenant(t, tenant.ID)

	token := "offline-test-token"
	tokenHash := crypto.HashToken(token)
	device := seedDevice(t, tenant.ID, &site.ID, "AA:BB:CC:DD:EE:06", "SN-TEST-006",
		model.DeviceStatusOnline, &tokenHash)

	hub := newTestHub(t) // heartbeat_timeout=3s, offline_check=1s
	defer hub.Stop(context.Background())
	server := newTestWSServer(t, hub)
	defer server.Close()

	// Connect and authenticate
	conn := dialWS(t, server)
	sendAuthMessage(t, conn, token, device.MAC, device.Serial, device.Model)
	result := readAuthResult(t, conn)
	require.True(t, result.Success)

	time.Sleep(100 * time.Millisecond)
	state := hub.StateStore().Get(device.ID)
	require.NotNil(t, state)
	assert.Equal(t, model.DeviceStatusOnline, state.Status)

	// Close connection and wait for offline detection
	conn.Close()
	time.Sleep(200 * time.Millisecond) // Wait for unregister

	state = hub.StateStore().Get(device.ID)
	require.NotNil(t, state)
	assert.Equal(t, model.DeviceStatusOffline, state.Status)
}

func TestWebSocket_DecommissionedDeviceRejected(t *testing.T) {
	tenant := seedTenant(t, "ws-decomm")
	site := seedSite(t, tenant.ID, "WS Decomm Site")
	defer cleanupTenant(t, tenant.ID)

	token := "decomm-test-token"
	tokenHash := crypto.HashToken(token)
	_ = seedDevice(t, tenant.ID, &site.ID, "AA:BB:CC:DD:EE:07", "SN-TEST-007",
		model.DeviceStatusDecommissioned, &tokenHash)

	hub := newTestHub(t)
	defer hub.Stop(context.Background())
	server := newTestWSServer(t, hub)
	defer server.Close()

	conn := dialWS(t, server)
	defer conn.Close()

	sendAuthMessage(t, conn, token, "AA:BB:CC:DD:EE:07", "SN-TEST-007", "AP-TEST")
	result := readAuthResult(t, conn)
	assert.False(t, result.Success)
}

func TestWebSocket_AdoptionFlow(t *testing.T) {
	tenant := seedTenant(t, "ws-adopt")
	site := seedSite(t, tenant.ID, "WS Adopt Site")
	defer cleanupTenant(t, tenant.ID)

	hub := newTestHub(t)
	defer hub.Stop(context.Background())
	server := newTestWSServer(t, hub)
	defer server.Close()

	mac := "AA:BB:CC:DD:EE:08"

	// Step 1: New device connects — gets pending_adoption
	conn1 := dialWS(t, server)
	sendAuthMessage(t, conn1, "", mac, "SN-TEST-008", "AP-TEST")
	result1 := readAuthResult(t, conn1)
	assert.False(t, result1.Success)
	assert.Equal(t, "pending_adoption", result1.Status)
	deviceID, err := uuid.Parse(result1.DeviceID)
	require.NoError(t, err)
	conn1.Close()

	// Step 2: Admin adopts the device
	token, err := hub.HandleAdoption(context.Background(), tenant.ID, deviceID, site.ID, "Lobby AP")
	require.NoError(t, err)
	assert.NotEmpty(t, token)

	// Step 3: Device reconnects with the new token
	conn2 := dialWS(t, server)
	defer conn2.Close()

	sendAuthMessage(t, conn2, token, mac, "SN-TEST-008", "AP-TEST")
	result2 := readAuthResult(t, conn2)
	assert.True(t, result2.Success, "device should authenticate with new token")
	assert.Equal(t, deviceID.String(), result2.DeviceID)
}

func TestWebSocket_ApplicationPing(t *testing.T) {
	tenant := seedTenant(t, "ws-ping")
	site := seedSite(t, tenant.ID, "WS Ping Site")
	defer cleanupTenant(t, tenant.ID)

	token := "ping-test-token"
	tokenHash := crypto.HashToken(token)
	device := seedDevice(t, tenant.ID, &site.ID, "AA:BB:CC:DD:EE:09", "SN-TEST-009",
		model.DeviceStatusOnline, &tokenHash)

	hub := newTestHub(t)
	defer hub.Stop(context.Background())
	server := newTestWSServer(t, hub)
	defer server.Close()

	conn := dialWS(t, server)
	defer conn.Close()

	// Authenticate
	sendAuthMessage(t, conn, token, device.MAC, device.Serial, device.Model)
	authResult := readAuthResult(t, conn)
	require.True(t, authResult.Success)

	// Send application-level ping
	pingData, _, err := protocol.EncodeMessage(protocol.ChannelControl, protocol.MsgPing, 0, nil)
	require.NoError(t, err)
	err = conn.WriteMessage(websocket.BinaryMessage, pingData)
	require.NoError(t, err)

	// Read pong
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, pongData, err := conn.ReadMessage()
	require.NoError(t, err)

	pongMsg, err := protocol.DecodeMessage(pongData)
	require.NoError(t, err)
	assert.Equal(t, protocol.MsgPong, pongMsg.Header.MsgType)
}

func TestWebSocket_SendToDevice(t *testing.T) {
	tenant := seedTenant(t, "ws-send")
	site := seedSite(t, tenant.ID, "WS Send Site")
	defer cleanupTenant(t, tenant.ID)

	token := "send-test-token"
	tokenHash := crypto.HashToken(token)
	device := seedDevice(t, tenant.ID, &site.ID, "AA:BB:CC:DD:EE:0A", "SN-TEST-010",
		model.DeviceStatusOnline, &tokenHash)

	hub := newTestHub(t)
	defer hub.Stop(context.Background())
	server := newTestWSServer(t, hub)
	defer server.Close()

	conn := dialWS(t, server)
	defer conn.Close()

	sendAuthMessage(t, conn, token, device.MAC, device.Serial, device.Model)
	authResult := readAuthResult(t, conn)
	require.True(t, authResult.Success)

	time.Sleep(100 * time.Millisecond)

	// Send a command from hub to device
	cmdPayload := protocol.CommandPayload{
		Command: "reboot",
		Params:  json.RawMessage(`{"delay":5}`),
		Timeout: 30,
	}
	_, err := hub.SendMessage(device.ID, protocol.ChannelControl, protocol.MsgCommand, 0, &cmdPayload)
	require.NoError(t, err)

	// Read command on device side
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, cmdData, err := conn.ReadMessage()
	require.NoError(t, err)

	cmdMsg, err := protocol.DecodeMessage(cmdData)
	require.NoError(t, err)
	assert.Equal(t, protocol.MsgCommand, cmdMsg.Header.MsgType)

	var received protocol.CommandPayload
	err = json.Unmarshal(cmdMsg.Payload, &received)
	require.NoError(t, err)
	assert.Equal(t, "reboot", received.Command)
	assert.Equal(t, 30, received.Timeout)
}

func TestWebSocket_BroadcastToSite(t *testing.T) {
	tenant := seedTenant(t, "ws-broadcast")
	site := seedSite(t, tenant.ID, "WS Broadcast Site")
	defer cleanupTenant(t, tenant.ID)

	hub := newTestHub(t)
	defer hub.Stop(context.Background())
	server := newTestWSServer(t, hub)
	defer server.Close()

	// Connect 3 devices to same site
	var conns []*websocket.Conn
	for i := 0; i < 3; i++ {
		mac := fmt.Sprintf("AA:BB:CC:DD:B0:%02X", i+1)
		serial := fmt.Sprintf("SN-BCAST-%03d", i+1)
		token := fmt.Sprintf("broadcast-token-%d", i+1)
		tokenHash := crypto.HashToken(token)

		seedDevice(t, tenant.ID, &site.ID, mac, serial,
			model.DeviceStatusOnline, &tokenHash)

		conn := dialWS(t, server)
		sendAuthMessage(t, conn, token, mac, serial, "AP-TEST")
		result := readAuthResult(t, conn)
		require.True(t, result.Success, "device %d should authenticate", i+1)
		conns = append(conns, conn)
	}
	defer func() {
		for _, c := range conns {
			c.Close()
		}
	}()

	time.Sleep(200 * time.Millisecond)
	assert.Equal(t, 3, hub.Count())

	// Broadcast a config push to all devices in the site
	configPayload := protocol.ConfigPushPayload{
		Version:        2,
		Config:         json.RawMessage(`{"system":{"hostname":"test"}}`),
		SafeApply:      true,
		ConfirmTimeout: 60,
	}
	data, _, err := protocol.EncodeMessage(
		protocol.ChannelControl,
		protocol.MsgConfigPush,
		0,
		&configPayload,
	)
	require.NoError(t, err)

	sent := hub.BroadcastToSite(site.ID, data)
	assert.Equal(t, 3, sent, "should send to all 3 devices")

	// All 3 should receive it
	for i, conn := range conns {
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		_, msgData, err := conn.ReadMessage()
		require.NoError(t, err, "device %d should receive broadcast", i+1)

		msg, err := protocol.DecodeMessage(msgData)
		require.NoError(t, err)
		assert.Equal(t, protocol.MsgConfigPush, msg.Header.MsgType)

		var cfg protocol.ConfigPushPayload
		err = json.Unmarshal(msg.Payload, &cfg)
		require.NoError(t, err)
		assert.Equal(t, int64(2), cfg.Version)
		assert.True(t, cfg.SafeApply)
	}
}

func TestWebSocket_SendToDisconnectedDevice(t *testing.T) {
	hub := newTestHub(t)
	defer hub.Stop(context.Background())

	// Try to send to a device that doesn't exist
	fakeDeviceID := uuid.New()
	_, err := hub.SendMessage(fakeDeviceID, protocol.ChannelControl, protocol.MsgCommand, 0, &protocol.CommandPayload{
		Command: "reboot",
		Timeout: 30,
	})
	assert.Error(t, err, "should error when device not connected")
	assert.Contains(t, err.Error(), "not connected")
}

func TestWebSocket_TenantIsolation(t *testing.T) {
	tenant1 := seedTenant(t, "ws-iso1")
	tenant2 := seedTenant(t, "ws-iso2")
	site1 := seedSite(t, tenant1.ID, "WS Iso Site 1")
	site2 := seedSite(t, tenant2.ID, "WS Iso Site 2")
	defer cleanupTenant(t, tenant1.ID)
	defer cleanupTenant(t, tenant2.ID)

	token1 := "iso-token-tenant1"
	token2 := "iso-token-tenant2"
	tokenHash1 := crypto.HashToken(token1)
	tokenHash2 := crypto.HashToken(token2)

	device1 := seedDevice(t, tenant1.ID, &site1.ID, "AA:BB:CC:DD:C0:01", "SN-ISO-001",
		model.DeviceStatusOnline, &tokenHash1)
	device2 := seedDevice(t, tenant2.ID, &site2.ID, "AA:BB:CC:DD:C0:02", "SN-ISO-002",
		model.DeviceStatusOnline, &tokenHash2)

	hub := newTestHub(t)
	defer hub.Stop(context.Background())
	server := newTestWSServer(t, hub)
	defer server.Close()

	// Connect both devices
	conn1 := dialWS(t, server)
	defer conn1.Close()
	sendAuthMessage(t, conn1, token1, device1.MAC, device1.Serial, device1.Model)
	result1 := readAuthResult(t, conn1)
	require.True(t, result1.Success)

	conn2 := dialWS(t, server)
	defer conn2.Close()
	sendAuthMessage(t, conn2, token2, device2.MAC, device2.Serial, device2.Model)
	result2 := readAuthResult(t, conn2)
	require.True(t, result2.Success)

	time.Sleep(200 * time.Millisecond)
	assert.Equal(t, 2, hub.Count())

	// GetByTenant should return only the correct device
	tenant1Conns := hub.GetByTenant(tenant1.ID)
	assert.Len(t, tenant1Conns, 1)
	assert.Equal(t, device1.ID, tenant1Conns[0].DeviceID)

	tenant2Conns := hub.GetByTenant(tenant2.ID)
	assert.Len(t, tenant2Conns, 1)
	assert.Equal(t, device2.ID, tenant2Conns[0].DeviceID)

	// GetBySite should return only the correct device
	site1Conns := hub.GetBySite(site1.ID)
	assert.Len(t, site1Conns, 1)

	site2Conns := hub.GetBySite(site2.ID)
	assert.Len(t, site2Conns, 1)

	// Broadcast to site1 should NOT reach device2
	pingData, _, err := protocol.EncodeMessage(protocol.ChannelControl, protocol.MsgPing, 0, nil)
	require.NoError(t, err)

	sent := hub.BroadcastToSite(site1.ID, pingData)
	assert.Equal(t, 1, sent, "broadcast to site1 should only reach 1 device")

	// Device1 gets the message
	conn1.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _, err = conn1.ReadMessage()
	assert.NoError(t, err, "device1 should receive the broadcast")

	// Device2 should NOT get the message — set short deadline to confirm
	conn2.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	_, _, err = conn2.ReadMessage()
	assert.Error(t, err, "device2 should NOT receive site1 broadcast")
}

func TestWebSocket_StatePersistence(t *testing.T) {
	tenant := seedTenant(t, "ws-persist")
	site := seedSite(t, tenant.ID, "WS Persist Site")
	defer cleanupTenant(t, tenant.ID)

	token := "persist-test-token"
	tokenHash := crypto.HashToken(token)
	device := seedDevice(t, tenant.ID, &site.ID, "AA:BB:CC:DD:D0:01", "SN-PERSIST-001",
		model.DeviceStatusOffline, &tokenHash)

	hub := newTestHub(t) // state_persist_interval=1s
	defer hub.Stop(context.Background())
	server := newTestWSServer(t, hub)
	defer server.Close()

	// Connect device
	conn := dialWS(t, server)
	defer conn.Close()
	sendAuthMessage(t, conn, token, device.MAC, device.Serial, device.Model)
	result := readAuthResult(t, conn)
	require.True(t, result.Success)

	// Send heartbeat to make state dirty
	hbPayload := protocol.HeartbeatPayload{
		Uptime:          7200,
		ConfigVersion:   0,
		FirmwareVersion: "1.0.0",
		ClientCount:     10,
		CPUUsage:        25.0,
		MemoryUsed:      100000000,
		MemoryTotal:     256000000,
		IPAddress:       "10.0.0.50",
		LoadAvg:         [3]float64{1.0, 0.8, 0.5},
	}
	hbData, _, err := protocol.EncodeMessage(protocol.ChannelControl, protocol.MsgHeartbeat, 0, &hbPayload)
	require.NoError(t, err)
	err = conn.WriteMessage(websocket.BinaryMessage, hbData)
	require.NoError(t, err)

	// Read the HeartbeatAck
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, _, err = conn.ReadMessage()
	require.NoError(t, err)

	// Wait for state persister to flush (interval=1s, give it 2.5s)
	time.Sleep(2500 * time.Millisecond)

	// Verify database was updated
	ctx := context.Background()
	dbDevice, err := testPG.Devices.GetByID(ctx, tenant.ID, device.ID)
	require.NoError(t, err)
	require.NotNil(t, dbDevice)

	assert.Equal(t, model.DeviceStatusOnline, dbDevice.Status)
	assert.NotNil(t, dbDevice.LastSeen)
	assert.Equal(t, int64(7200), dbDevice.Uptime)
}

func TestWebSocket_ConnectionCount(t *testing.T) {
	tenant := seedTenant(t, "ws-count")
	site := seedSite(t, tenant.ID, "WS Count Site")
	defer cleanupTenant(t, tenant.ID)

	hub := newTestHub(t)
	defer hub.Stop(context.Background())
	server := newTestWSServer(t, hub)
	defer server.Close()

	assert.Equal(t, 0, hub.Count())

	// Connect 5 devices
	var conns []*websocket.Conn
	for i := 0; i < 5; i++ {
		mac := fmt.Sprintf("AA:BB:CC:DD:E0:%02X", i+1)
		serial := fmt.Sprintf("SN-COUNT-%03d", i+1)
		token := fmt.Sprintf("count-token-%d", i+1)
		tokenHash := crypto.HashToken(token)

		seedDevice(t, tenant.ID, &site.ID, mac, serial,
			model.DeviceStatusOnline, &tokenHash)

		conn := dialWS(t, server)
		sendAuthMessage(t, conn, token, mac, serial, "AP-TEST")
		result := readAuthResult(t, conn)
		require.True(t, result.Success, "device %d should authenticate", i+1)
		conns = append(conns, conn)
	}

	time.Sleep(200 * time.Millisecond)
	assert.Equal(t, 5, hub.Count())

	// Disconnect 2
	conns[0].Close()
	conns[1].Close()
	time.Sleep(300 * time.Millisecond)
	assert.Equal(t, 3, hub.Count())

	// Clean up remaining
	for i := 2; i < len(conns); i++ {
		conns[i].Close()
	}
	time.Sleep(300 * time.Millisecond)
	assert.Equal(t, 0, hub.Count())
}

func TestWebSocket_AuthTimeout(t *testing.T) {
	tenant := seedTenant(t, "ws-timeout")
	defer cleanupTenant(t, tenant.ID)

	cfg := config.WebSocketConfig{
		MaxConnections:       100,
		MaxConnectionsPerIP:  10,
		ConnectionRateLimit:  50,
		WriteWait:            10 * time.Second,
		PongWait:             30 * time.Second,
		PingInterval:         15 * time.Second,
		AuthTimeout:          1 * time.Second, // Very short for test
		ReadBufferSize:       4096,
		WriteBufferSize:      4096,
		SendChannelSize:      64,
		ControlRateLimit:     10,
		TelemetryRateLimit:   5,
		BulkRateLimit:        2,
		StatePersistInterval: 30 * time.Second,
		OfflineCheckInterval: 30 * time.Second,
		HeartbeatTimeout:     90 * time.Second,
	}

	hub := ws.NewHub(cfg, testPG, testLogger)
	hub.Run()
	defer hub.Stop(context.Background())

	server := newTestWSServer(t, hub)
	defer server.Close()

	// Connect but DON'T send auth message
	wsURL := "ws" + server.URL[4:] + "/ws/device"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	defer conn.Close()

	// Wait for auth timeout
	time.Sleep(1500 * time.Millisecond)

	// Connection should be closed by server
	conn.SetReadDeadline(time.Now().Add(1 * time.Second))
	_, _, err = conn.ReadMessage()
	assert.Error(t, err, "connection should be closed after auth timeout")

	assert.Equal(t, 0, hub.Count())
}

func TestWebSocket_WrongFirstMessage(t *testing.T) {
	tenant := seedTenant(t, "ws-wrongmsg")
	defer cleanupTenant(t, tenant.ID)

	hub := newTestHub(t)
	defer hub.Stop(context.Background())
	server := newTestWSServer(t, hub)
	defer server.Close()

	conn := dialWS(t, server)
	defer conn.Close()

	// Send a Heartbeat as the first message instead of DeviceAuth
	hbPayload := protocol.HeartbeatPayload{
		Uptime:      100,
		ClientCount: 0,
	}
	data, _, err := protocol.EncodeMessage(protocol.ChannelControl, protocol.MsgHeartbeat, 0, &hbPayload)
	require.NoError(t, err)
	err = conn.WriteMessage(websocket.BinaryMessage, data)
	require.NoError(t, err)

	// Server sends error then closes — we may get the error or just a close
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, respData, err := conn.ReadMessage()
	if err != nil {
		// Connection closed before we could read — acceptable
		t.Logf("connection closed before reading error response: %v", err)
		return
	}

	// If we got a response, verify it's an error
	msg, err := protocol.DecodeMessage(respData)
	require.NoError(t, err)
	assert.Equal(t, protocol.MsgError, msg.Header.MsgType)

	var errPayload protocol.ErrorPayload
	err = json.Unmarshal(msg.Payload, &errPayload)
	require.NoError(t, err)
	assert.Equal(t, "AUTH_REQUIRED", errPayload.Code)
}

func TestWebSocket_LiveStatusAPI(t *testing.T) {
	tenant := seedTenant(t, "ws-livestatus")
	site := seedSite(t, tenant.ID, "WS LiveStatus Site")
	defer cleanupTenant(t, tenant.ID)

	token := "livestatus-test-token"
	tokenHash := crypto.HashToken(token)
	device := seedDevice(t, tenant.ID, &site.ID, "AA:BB:CC:DD:F0:01", "SN-LIVE-001",
		model.DeviceStatusOnline, &tokenHash)

	hub := newTestHub(t)
	defer hub.Stop(context.Background())
	server := newTestWSServer(t, hub)
	defer server.Close()

	// Connect and authenticate
	conn := dialWS(t, server)
	defer conn.Close()
	sendAuthMessage(t, conn, token, device.MAC, device.Serial, device.Model)
	result := readAuthResult(t, conn)
	require.True(t, result.Success)

	// Send heartbeat
	hbPayload := protocol.HeartbeatPayload{
		Uptime:      1800,
		ClientCount: 7,
		CPUUsage:    30.0,
		MemoryUsed:  150000000,
		MemoryTotal: 256000000,
		IPAddress:   "10.0.0.100",
	}
	hbData, _, err := protocol.EncodeMessage(protocol.ChannelControl, protocol.MsgHeartbeat, 0, &hbPayload)
	require.NoError(t, err)
	conn.WriteMessage(websocket.BinaryMessage, hbData)

	// Read HeartbeatAck
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, _, err = conn.ReadMessage()
	require.NoError(t, err)

	time.Sleep(100 * time.Millisecond)

	// Now check state store directly (API handler reads from this)
	state := hub.StateStore().Get(device.ID)
	require.NotNil(t, state)
	assert.Equal(t, model.DeviceStatusOnline, state.Status)
	assert.Equal(t, int64(1800), state.Uptime)
	assert.Equal(t, 7, state.ClientCount)
	assert.Equal(t, 30.0, state.CPUUsage)
	assert.Equal(t, "10.0.0.100", state.IPAddress)
	assert.True(t, hub.IsConnected(device.ID))
}

// ── Cleanup helper for devices ───────────────────────────────

func cleanupDevices(t *testing.T, tenantID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	_, err := testPG.Pool.Exec(ctx,
		`DELETE FROM devices WHERE tenant_id = $1`, tenantID)
	if err != nil {
		t.Logf("warning: failed to cleanup devices: %v", err)
	}
}
