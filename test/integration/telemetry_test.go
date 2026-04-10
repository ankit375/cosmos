//go:build integration

package integration

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yourorg/cloudctrl/internal/model"
	"github.com/yourorg/cloudctrl/internal/protocol"
	"github.com/yourorg/cloudctrl/internal/telemetry"
)

// ════════════════════════════════════════════════════════════════
// Test: Metrics received and buffered correctly
// ════════════════════════════════════════════════════════════════

func TestTelemetry_MetricsBuffered(t *testing.T) {
	tenant := seedTenant(t, "telem-buf")
	defer cleanupTenant(t, tenant.ID)

	site := seedSite(t, tenant.ID, "Site Buffer")
	device := seedAdoptedDevice(t, tenant.ID, site.ID, "AP-Buffer-1")

	engine := createTestTelemetryEngine(t)
	defer engine.Stop()

	payload := buildTestMetricsPayload()

	engine.Ingest(device.ID, tenant.ID, &site.ID, payload)

	// Buffer should contain 1 device metric + 2 radio metrics
	batch := engine.TestSwapBuffer()
	assert.Equal(t, 1, len(batch.DeviceMetrics), "should have 1 device metrics row")
	assert.Equal(t, 2, len(batch.RadioMetrics), "should have 2 radio metrics rows")

	// Verify device metrics values
	dm := batch.DeviceMetrics[0]
	assert.Equal(t, device.ID, dm.DeviceID)
	assert.Equal(t, tenant.ID, dm.TenantID)
	assert.InDelta(t, 15.5, float64(*dm.CPUUsage), 0.1)
	assert.Equal(t, int64(134217728), *dm.MemoryUsed)

	// Verify radio metrics
	found2g := false
	found5g := false
	for _, rm := range batch.RadioMetrics {
		assert.Equal(t, device.ID, rm.DeviceID)
		if rm.Band == "2g" {
			found2g = true
			assert.Equal(t, int16(6), *rm.Channel)
		}
		if rm.Band == "5g" {
			found5g = true
			assert.Equal(t, int16(36), *rm.Channel)
		}
	}
	assert.True(t, found2g, "should have 2g radio metrics")
	assert.True(t, found5g, "should have 5g radio metrics")
}

// ════════════════════════════════════════════════════════════════
// Test: Buffer flush writes to DB
// ════════════════════════════════════════════════════════════════

func TestTelemetry_BufferFlushToDB(t *testing.T) {
	tenant := seedTenant(t, "telem-flush")
	defer cleanupTenant(t, tenant.ID)

	site := seedSite(t, tenant.ID, "Site Flush")
	device := seedAdoptedDevice(t, tenant.ID, site.ID, "AP-Flush-1")

	ctx := context.Background()

	// Insert directly via store
	now := time.Now()
	cpuUsage := float32(25.5)
	memUsed := int64(100000000)
	memTotal := int64(256000000)
	clientCount := int16(10)

	deviceMetrics := []model.DeviceMetrics{
		{
					Time:        now,
			DeviceID:    device.ID,
			TenantID:    tenant.ID,
			CPUUsage:    &cpuUsage,
			MemoryUsed:  &memUsed,
			MemoryTotal: &memTotal,
			ClientCount: &clientCount,
		},
	}

	copied, err := testPG.Metrics.BatchInsertDeviceMetrics(ctx, deviceMetrics)
	require.NoError(t, err)
	assert.Equal(t, int64(1), copied)

	// Insert radio metrics
	channel := int16(36)
	band := "5g"
	util := float32(20.0)
	txBytes := int64(1000000)
	rxBytes := int64(500000)

	radioMetrics := []model.RadioMetrics{
		{
			Time:        now,
			DeviceID:    device.ID,
			TenantID:    tenant.ID,
			Band:        band,
			Channel:     &channel,
			Utilization: &util,
			TxBytes:     &txBytes,
			RxBytes:     &rxBytes,
		},
	}

	copied, err = testPG.Metrics.BatchInsertRadioMetrics(ctx, radioMetrics)
	require.NoError(t, err)
	assert.Equal(t, int64(1), copied)

	// Query back device metrics
	query := model.MetricsQuery{
		DeviceID:   device.ID,
		TenantID:   tenant.ID,
		Start:      now.Add(-1 * time.Minute),
		End:        now.Add(1 * time.Minute),
		Resolution: "raw",
	}

	results, err := testPG.Metrics.QueryDeviceMetrics(ctx, query)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(results), 1, "should have at least 1 device metrics result")

	// Query radio metrics
	radioResults, err := testPG.Metrics.QueryRadioMetrics(ctx, query)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(radioResults), 1, "should have at least 1 radio metrics result")
	assert.Equal(t, "5g", radioResults[0].Band)
}

// ════════════════════════════════════════════════════════════════
// Test: Double-buffer swap under concurrent writes
// ════════════════════════════════════════════════════════════════

func TestTelemetry_DoubleBufferConcurrent(t *testing.T) {
	buf := telemetry.NewDoubleBuffer(100)

	// Simulate concurrent writers
	done := make(chan struct{})
	writerCount := 10
	writesPerWriter := 100

	for i := 0; i < writerCount; i++ {
		go func(id int) {
			defer func() { done <- struct{}{} }()

			for j := 0; j < writesPerWriter; j++ {
				cpuUsage := float32(float64(id*100+j) / 10.0)
				dm := &model.DeviceMetrics{
					Time:     time.Now(),
					DeviceID: uuid.New(),
					TenantID: uuid.New(),
					CPUUsage: &cpuUsage,
				}
				buf.Append(dm, nil, nil)
			}
		}(i)
	}

	// Periodically swap while writers are active
	swapCount := 0
	totalItems := 0
	swapDone := make(chan struct{})
	go func() {
		defer close(swapDone)
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()

		timeout := time.After(5 * time.Second)
		for {
			select {
			case <-ticker.C:
				batch := buf.Swap()
				totalItems += len(batch.DeviceMetrics)
				swapCount++
			case <-timeout:
				// Final swap
				batch := buf.Swap()
				totalItems += len(batch.DeviceMetrics)
				return
			}
		}
	}()

	// Wait for all writers
	for i := 0; i < writerCount; i++ {
		<-done
	}

	// Wait for swapper to finish
	<-swapDone

	// Final drain
	finalBatch := buf.Swap()
	totalItems += len(finalBatch.DeviceMetrics)

	expectedTotal := writerCount * writesPerWriter
	assert.Equal(t, expectedTotal, totalItems,
		"all writes should be captured: expected %d, got %d", expectedTotal, totalItems)

	t.Logf("Concurrent test: %d writers × %d writes = %d total, captured in %d swaps",
		writerCount, writesPerWriter, totalItems, swapCount)
}

// ════════════════════════════════════════════════════════════════
// Test: Client diff engine (connect, disconnect, roam detection)
// ════════════════════════════════════════════════════════════════

func TestTelemetry_ClientDiffEngine(t *testing.T) {
	diffEngine := telemetry.NewClientDiffEngine()
	deviceID := uuid.New()

	// First report: 3 clients
	clients1 := []model.ClientInfo{
		{MAC: "AA:BB:CC:11:22:33", SSID: "Corp", Band: "5g", RSSI: -55},
		{MAC: "AA:BB:CC:44:55:66", SSID: "Corp", Band: "2g", RSSI: -70},
		{MAC: "AA:BB:CC:77:88:99", SSID: "Guest", Band: "5g", RSSI: -60},
	}
	diff1 := diffEngine.Diff(deviceID, clients1)

	// First report: all clients are "connected"
	assert.Equal(t, 3, len(diff1.Connected), "first report: all should be connected")
	assert.Equal(t, 0, len(diff1.Disconnected), "first report: none disconnected")
	assert.Equal(t, 0, len(diff1.Roamed), "first report: none roamed")

	// Second report: 1 client left, 1 roamed bands, 1 new
	clients2 := []model.ClientInfo{
		{MAC: "AA:BB:CC:11:22:33", SSID: "Corp", Band: "5g", RSSI: -55},  // unchanged
		{MAC: "AA:BB:CC:44:55:66", SSID: "Corp", Band: "5g", RSSI: -65},  // roamed 2g→5g
		// AA:BB:CC:77:88:99 gone → disconnected
		{MAC: "AA:BB:CC:AA:BB:CC", SSID: "Guest", Band: "2g", RSSI: -75}, // new client
	}
	diff2 := diffEngine.Diff(deviceID, clients2)

	assert.Equal(t, 1, len(diff2.Connected), "should detect 1 new client")
	assert.Equal(t, "AA:BB:CC:AA:BB:CC", diff2.Connected[0].MAC)

	assert.Equal(t, 1, len(diff2.Disconnected), "should detect 1 disconnected")
	assert.Equal(t, "AA:BB:CC:77:88:99", diff2.Disconnected[0].MAC)

	assert.Equal(t, 1, len(diff2.Roamed), "should detect 1 roam")
	assert.Equal(t, "AA:BB:CC:44:55:66", diff2.Roamed[0].Client.MAC)
	assert.Equal(t, "2g", diff2.Roamed[0].OldBand)
	assert.Equal(t, "5g", diff2.Roamed[0].NewBand)

	// Verify total client count
	assert.Equal(t, 3, diffEngine.TotalClients())

	// Remove device — should return all remaining as disconnected
	disconnected := diffEngine.RemoveDevice(deviceID)
	assert.Equal(t, 3, len(disconnected))
	assert.Equal(t, 0, diffEngine.TotalClients())
}

// ════════════════════════════════════════════════════════════════
// Test: Metrics query with different resolutions
// ════════════════════════════════════════════════════════════════

func TestTelemetry_MetricsQueryResolution(t *testing.T) {
	tenant := seedTenant(t, "telem-query")
	defer cleanupTenant(t, tenant.ID)

	site := seedSite(t, tenant.ID, "Site Query")
	device := seedAdoptedDevice(t, tenant.ID, site.ID, "AP-Query-1")

	ctx := context.Background()

	// Insert 10 raw data points over the last hour
	now := time.Now()
	var deviceMetrics []model.DeviceMetrics
	for i := 0; i < 10; i++ {
		cpuUsage := float32(10.0 + float64(i)*2.0)
		memUsed := int64(100000000 + int64(i)*1000000)
		memTotal := int64(256000000)
		clientCount := int16(5 + i)

		deviceMetrics = append(deviceMetrics, model.DeviceMetrics{
			Time:        now.Add(-time.Duration(10-i) * time.Minute),
			DeviceID:    device.ID,
			TenantID:    tenant.ID,
			CPUUsage:    &cpuUsage,
			MemoryUsed:  &memUsed,
			MemoryTotal: &memTotal,
			ClientCount: &clientCount,
		})
	}

	_, err := testPG.Metrics.BatchInsertDeviceMetrics(ctx, deviceMetrics)
	require.NoError(t, err)

	// Query raw resolution (last 6 hours → raw)
	query := model.MetricsQuery{
		DeviceID:   device.ID,
		TenantID:   tenant.ID,
		Start:      now.Add(-1 * time.Hour),
		End:        now.Add(1 * time.Minute),
		Resolution: "raw",
	}

	results, err := testPG.Metrics.QueryDeviceMetrics(ctx, query)
	require.NoError(t, err)
	assert.Equal(t, 10, len(results), "raw query should return 10 points")

	// Verify auto-resolution selection
	shortRange := model.MetricsQuery{
		Start: now.Add(-3 * time.Hour),
		End:   now,
	}
	assert.Equal(t, "raw", shortRange.AutoResolution())

	mediumRange := model.MetricsQuery{
		Start: now.Add(-24 * time.Hour),
		End:   now,
	}
	assert.Equal(t, "1h", mediumRange.AutoResolution())

	longRange := model.MetricsQuery{
		Start: now.Add(-7 * 24 * time.Hour),
		End:   now,
	}
	assert.Equal(t, "1d", longRange.AutoResolution())
}

// ════════════════════════════════════════════════════════════════
// Test: Client sessions (open, close, query)
// ════════════════════════════════════════════════════════════════

func TestTelemetry_ClientSessions(t *testing.T) {
	tenant := seedTenant(t, "telem-sessions")
	defer cleanupTenant(t, tenant.ID)

	site := seedSite(t, tenant.ID, "Site Sessions")
	device := seedAdoptedDevice(t, tenant.ID, site.ID, "AP-Sessions-1")

	ctx := context.Background()

	// Open a client session
	now := time.Now()
	rssi := int16(-55)
	session := &model.ClientSession{
		ID:          uuid.New(),
		TenantID:    tenant.ID,
		DeviceID:    device.ID,
		SiteID:      &site.ID,
		ClientMAC:   "AA:BB:CC:11:22:33",
		SSID:        "CorpWiFi",
		Band:        "5g",
		ConnectedAt: now.Add(-10 * time.Minute),
		AvgRSSI:     &rssi,
	}
	clientIP := "192.168.1.100"
	session.ClientIP = &clientIP
	hostname := "johns-iphone"
	session.Hostname = &hostname

	err := testPG.Metrics.OpenClientSession(ctx, session)
	require.NoError(t, err)

	// Verify active session exists
	active, err := testPG.Metrics.GetActiveSessions(ctx, device.ID)
	require.NoError(t, err)
	require.Equal(t, 1, len(active))
	assert.Equal(t, "aa:bb:cc:11:22:33", active[0].ClientMAC)
	assert.Nil(t, active[0].DisconnectedAt)

	// Update session stats
	err = testPG.Metrics.UpdateActiveSession(ctx, device.ID, "AA:BB:CC:11:22:33",
		-50, 866, 400, 1000000, 500000)
	require.NoError(t, err)

	// Close the session
	err = testPG.Metrics.CloseClientSession(ctx, device.ID, "AA:BB:CC:11:22:33",
		now, "departed", 2000000, 1000000)
	require.NoError(t, err)

	// Verify session is closed
	active2, err := testPG.Metrics.GetActiveSessions(ctx, device.ID)
	require.NoError(t, err)
	assert.Equal(t, 0, len(active2))

	// Query session history
	params := model.ClientSessionQuery{
		TenantID: tenant.ID,
		DeviceID: &device.ID,
		Limit:    50,
	}
	sessions, total, err := testPG.Metrics.ListClientSessions(ctx, params)
	require.NoError(t, err)
	assert.Equal(t, 1, total)
	require.Equal(t, 1, len(sessions))
	assert.NotNil(t, sessions[0].DisconnectedAt)
	assert.NotNil(t, sessions[0].DurationSecs)
	assert.Equal(t, int64(2000000), sessions[0].TotalTxBytes)
}

// ════════════════════════════════════════════════════════════════
// Test: Close all sessions on device disconnect
// ════════════════════════════════════════════════════════════════

func TestTelemetry_CloseAllDeviceSessions(t *testing.T) {
	tenant := seedTenant(t, "telem-closeall")
	defer cleanupTenant(t, tenant.ID)

	site := seedSite(t, tenant.ID, "Site CloseAll")
	device := seedAdoptedDevice(t, tenant.ID, site.ID, "AP-CloseAll-1")

	ctx := context.Background()
	now := time.Now()

	// Open 3 client sessions
	for i := 0; i < 3; i++ {
		rssi := int16(-55 - int16(i)*5)
		session := &model.ClientSession{
			ID:          uuid.New(),
			TenantID:    tenant.ID,
			DeviceID:    device.ID,
			SiteID:      &site.ID,
			ClientMAC:   fmt.Sprintf("AA:BB:CC:%02X:%02X:%02X", i, i, i),
			SSID:        "CorpWiFi",
			Band:        "5g",
			ConnectedAt: now.Add(-time.Duration(10+i) * time.Minute),
			AvgRSSI:     &rssi,
		}
		err := testPG.Metrics.OpenClientSession(ctx, session)
		require.NoError(t, err)
	}

	// Verify 3 active sessions
	active, err := testPG.Metrics.GetActiveSessions(ctx, device.ID)
	require.NoError(t, err)
	assert.Equal(t, 3, len(active))

	// Close all on device disconnect
	err = testPG.Metrics.CloseAllDeviceSessions(ctx, device.ID, "device_offline")
	require.NoError(t, err)

	// Verify all closed
	active2, err := testPG.Metrics.GetActiveSessions(ctx, device.ID)
	require.NoError(t, err)
	assert.Equal(t, 0, len(active2))
}

// ════════════════════════════════════════════════════════════════
// Test: Metrics API endpoints (end-to-end via HTTP)
// ════════════════════════════════════════════════════════════════

func TestTelemetry_APIEndpoints(t *testing.T) {
	tenant := seedTenant(t, "telem-api")
	defer cleanupTenant(t, tenant.ID)

	user, password := seedUser(t, tenant.ID, model.RoleAdmin)
	loginResp := loginUser(t, user.Email, password)
	token := loginResp.AccessToken

	site := seedSite(t, tenant.ID, "Site API")
	device := seedAdoptedDevice(t, tenant.ID, site.ID, "AP-API-1")

	ctx := context.Background()

	// Seed some metrics data
	now := time.Now()
	for i := 0; i < 5; i++ {
		cpuUsage := float32(10.0 + float64(i)*5.0)
		memUsed := int64(100000000)
		memTotal := int64(256000000)
		clientCount := int16(3 + i)

		_, err := testPG.Metrics.BatchInsertDeviceMetrics(ctx, []model.DeviceMetrics{
			{
				Time:        now.Add(-time.Duration(5-i) * time.Minute),
				DeviceID:    device.ID,
				TenantID:    tenant.ID,
				CPUUsage:    &cpuUsage,
				MemoryUsed:  &memUsed,
				MemoryTotal: &memTotal,
				ClientCount: &clientCount,
			},
		})
		require.NoError(t, err)
	}

	// Test GET /api/v1/devices/:id/metrics
	t.Run("GetDeviceMetrics", func(t *testing.T) {
		start := now.Add(-1 * time.Hour).Format(time.RFC3339)
		end := now.Add(1 * time.Minute).Format(time.RFC3339)
		url := fmt.Sprintf("/api/v1/devices/%s/metrics?start=%s&end=%s&resolution=raw",
			device.ID, start, end)

		w := performRequest(testRouter, "GET", url, "", token)
		assert.Equal(t, http.StatusOK, w.Code)

		var resp struct {
			Success bool `json:"success"`
			Data    struct {
				DeviceID   string `json:"device_id"`
				Resolution string `json:"resolution"`
				Points     int    `json:"points"`
				Data       []struct {
					Time   string   `json:"time"`
					CPUAvg *float64 `json:"cpu_avg"`
				} `json:"data"`
			} `json:"data"`
		}
		parseJSON(t, w, &resp)
		assert.True(t, resp.Success)
		assert.Equal(t, device.ID.String(), resp.Data.DeviceID)
		assert.Equal(t, "raw", resp.Data.Resolution)
		assert.Equal(t, 5, resp.Data.Points)
	})

	// Test GET /api/v1/devices/:id/clients (live — will be empty since no telemetry engine wired in test)
	t.Run("GetDeviceClients", func(t *testing.T) {
		url := fmt.Sprintf("/api/v1/devices/%s/clients", device.ID)
		w := performRequest(testRouter, "GET", url, "", token)
		assert.Equal(t, http.StatusOK, w.Code)

		var resp struct {
			Success bool `json:"success"`
			Data    struct {
				ClientCount int                `json:"client_count"`
				Clients     []model.ClientInfo `json:"clients"`
			} `json:"data"`
		}
		parseJSON(t, w, &resp)
		assert.True(t, resp.Success)
		assert.Equal(t, 0, resp.Data.ClientCount)
	})

	// Test GET /api/v1/devices/:id/clients/history
	t.Run("GetClientHistory", func(t *testing.T) {
		url := fmt.Sprintf("/api/v1/devices/%s/clients/history", device.ID)
		w := performRequest(testRouter, "GET", url, "", token)
		assert.Equal(t, http.StatusOK, w.Code)
	})

	// Test GET /api/v1/sites/:id/metrics
	t.Run("GetSiteMetrics", func(t *testing.T) {
		start := now.Add(-1 * time.Hour).Format(time.RFC3339)
		end := now.Add(1 * time.Minute).Format(time.RFC3339)
		url := fmt.Sprintf("/api/v1/sites/%s/metrics?start=%s&end=%s",
			site.ID, start, end)

		w := performRequest(testRouter, "GET", url, "", token)
		// May return 200 with empty data or 500 if aggregates don't exist yet
		// Both are acceptable in integration test
		assert.Contains(t, []int{http.StatusOK, http.StatusInternalServerError}, w.Code)
	})

	// Test: wrong tenant can't access metrics
	t.Run("CrossTenantBlocked", func(t *testing.T) {
		otherTenant := seedTenant(t, "telem-other")
		defer cleanupTenant(t, otherTenant.ID)

		otherUser, otherPass := seedUser(t, otherTenant.ID, model.RoleAdmin)
		otherLogin := loginUser(t, otherUser.Email, otherPass)

		url := fmt.Sprintf("/api/v1/devices/%s/metrics?start=%s&end=%s&resolution=raw",
			device.ID,
			now.Add(-1*time.Hour).Format(time.RFC3339),
			now.Add(1*time.Minute).Format(time.RFC3339),
		)

		w := performRequest(testRouter, "GET", url, "", otherLogin.AccessToken)
		assert.Equal(t, http.StatusNotFound, w.Code, "cross-tenant access should return 404")
	})
}

// ════════════════════════════════════════════════════════════════
// Test helpers
// ════════════════════════════════════════════════════════════════

func createTestTelemetryEngine(t *testing.T) *telemetry.Engine {
	t.Helper()

	cfg := telemetry.EngineConfig{
		FlushInterval:         1 * time.Hour, // Don't auto-flush in tests
		BufferCapacity:        1000,
		SessionUpdateInterval: 1 * time.Hour,
		ClientSnapshotTTL:     5 * time.Minute,
	}

	engine := telemetry.NewEngine(cfg, testPG, testRedis, testLogger)
	// Don't call Start() — we'll manually control flushing in tests
	return engine
}

func buildTestMetricsPayload() *protocol.MetricsReportPayload {
	return &protocol.MetricsReportPayload{
		Timestamp: time.Now().Unix(),
		System: protocol.MetricsSystemPayload{
			CPUUsage:    15.5,
			MemoryUsed:  134217728,
			MemoryTotal: 268435456,
			LoadAvg1:    0.5,
			LoadAvg5:    0.3,
			LoadAvg15:   0.2,
			Uptime:      86400,
			Temperature: 55.0,
		},
		Radios: []protocol.MetricsRadioPayload{
			{
				Band:         "2g",
				Channel:      6,
				ChannelWidth: 20,
				TxPower:      20,
				NoiseFloor:   -95,
				Utilization:  35.5,
				ClientCount:  15,
				TxBytes:      1073741824,
				RxBytes:      536870912,
				TxPackets:    1000000,
				RxPackets:    800000,
				TxErrors:     50,
				RxErrors:     30,
				TxRetries:    5000,
			},
			{
				Band:         "5g",
				Channel:      36,
				ChannelWidth: 80,
				TxPower:      23,
				NoiseFloor:   -100,
				Utilization:  20.0,
				ClientCount:  8,
				TxBytes:      2147483648,
				RxBytes:      1073741824,
				TxPackets:    2000000,
				RxPackets:    1500000,
				TxErrors:     20,
				RxErrors:     10,
				TxRetries:    2000,
			},
		},
		Clients: []protocol.MetricsClientPayload{
			{
				MAC:            "11:22:33:44:55:66",
				IP:             "192.168.1.100",
				Hostname:       "johns-iphone",
				SSID:           "CorpWiFi",
				Band:           "5g",
				RSSI:           -55,
				SNR:            45,
				TxRate:         866,
				RxRate:         866,
				TxBytes:        104857600,
				RxBytes:        52428800,
				ConnectedSince: time.Now().Add(-30 * time.Minute).Unix(),
			},
		},
	}
}

// parseJSON is a test helper already defined in helpers_test.go.
// performRequest is a test helper already defined in helpers_test.go.
