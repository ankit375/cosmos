package telemetry

import (
	"time"

	"github.com/google/uuid"
	"github.com/yourorg/cloudctrl/internal/model"
	"github.com/yourorg/cloudctrl/internal/protocol"
)

// IngestResult is returned by Ingest to inform the caller.
type IngestResult struct {
	ClientCount int
	RadioCount  int
}

// convertMetricsReport transforms a wire protocol MetricsReportPayload
// into typed model rows and client events for the buffer.
func convertMetricsReport(
	deviceID, tenantID uuid.UUID,
	siteID *uuid.UUID,
	payload *protocol.MetricsReportPayload,
	diffEngine *ClientDiffEngine,
) (*model.DeviceMetrics, []model.RadioMetrics, []ClientEvent) {

	now := time.Now()

	// Use device timestamp if sane, otherwise use server time
	ts := now
	if payload.Timestamp > 0 {
		deviceTime := time.Unix(payload.Timestamp, 0)
		// Accept if within 5 minutes of server time
		if now.Sub(deviceTime).Abs() < 5*time.Minute {
			ts = deviceTime
		}
	}

	// ── Device metrics row ───────────────────────────────────

	cpuUsage := float32(payload.System.CPUUsage)
	memUsed := int64(payload.System.MemoryUsed)
	memTotal := int64(payload.System.MemoryTotal)
	loadAvg1 := float32(payload.System.LoadAvg1)
	loadAvg5 := float32(payload.System.LoadAvg5)
	loadAvg15 := float32(payload.System.LoadAvg15)
	uptime := payload.System.Uptime
	clientCount := int16(len(payload.Clients))

	dm := &model.DeviceMetrics{
		Time:        ts,
		DeviceID:    deviceID,
		TenantID:    tenantID,
		CPUUsage:    &cpuUsage,
		MemoryUsed:  &memUsed,
		MemoryTotal: &memTotal,
		LoadAvg1:    &loadAvg1,
		LoadAvg5:    &loadAvg5,
		LoadAvg15:   &loadAvg15,
		Uptime:      &uptime,
		ClientCount: &clientCount,
	}

	if payload.System.Temperature > 0 {
		temp := float32(payload.System.Temperature)
		dm.Temperature = &temp
	}

	// ── Radio metrics rows ───────────────────────────────────

	radioRows := make([]model.RadioMetrics, 0, len(payload.Radios))
	for _, r := range payload.Radios {
		channel := int16(r.Channel)
		chWidth := int16(r.ChannelWidth)
		txPower := int16(r.TxPower)
		noiseFloor := int16(r.NoiseFloor)
		util := float32(r.Utilization)
		radioClients := int16(r.ClientCount)
		txBytes := int64(r.TxBytes)
		rxBytes := int64(r.RxBytes)
		txPackets := int64(r.TxPackets)
		rxPackets := int64(r.RxPackets)
		txErrors := int64(r.TxErrors)
		rxErrors := int64(r.RxErrors)
		txRetries := int64(r.TxRetries)

		radioRows = append(radioRows, model.RadioMetrics{
			Time:         ts,
			DeviceID:     deviceID,
			TenantID:     tenantID,
			Band:         r.Band,
			Channel:      &channel,
			ChannelWidth: &chWidth,
			TxPower:      &txPower,
			NoiseFloor:   &noiseFloor,
			Utilization:  &util,
			ClientCount:  &radioClients,
			TxBytes:      &txBytes,
			RxBytes:      &rxBytes,
			TxPackets:    &txPackets,
			RxPackets:    &rxPackets,
			TxErrors:     &txErrors,
			RxErrors:     &rxErrors,
			TxRetries:    &txRetries,
		})
	}

	// ── Client diff → events ─────────────────────────────────

	clientInfos := make([]model.ClientInfo, 0, len(payload.Clients))
	for _, c := range payload.Clients {
		connSince := time.Unix(c.ConnectedSince, 0)
		if connSince.IsZero() || connSince.After(now) {
			connSince = now
		}
		clientInfos = append(clientInfos, model.ClientInfo{
			MAC:            c.MAC,
			IP:             c.IP,
			Hostname:       c.Hostname,
			SSID:           c.SSID,
			Band:           c.Band,
			RSSI:           c.RSSI,
			SNR:            c.SNR,
			TxRate:         c.TxRate,
			RxRate:         c.RxRate,
			TxBytes:        c.TxBytes,
			RxBytes:        c.RxBytes,
			ConnectedSince: connSince,
		})
	}

	diff := diffEngine.Diff(deviceID, clientInfos)

	// Convert diff result to ClientEvent entries for the buffer
	var clientEvents []ClientEvent

	deviceIDBytes := [16]byte(deviceID)
	tenantIDBytes := [16]byte(tenantID)
	var siteIDBytes *[16]byte
	if siteID != nil {
		b := [16]byte(*siteID)
		siteIDBytes = &b
	}

	for _, c := range diff.Connected {
		clientEvents = append(clientEvents, ClientEvent{
			ClientInfo: c,
			DeviceID:   deviceIDBytes,
			TenantID:   tenantIDBytes,
			SiteID:     siteIDBytes,
			EventType:  "connect",
		})
	}
	for _, c := range diff.Disconnected {
		clientEvents = append(clientEvents, ClientEvent{
			ClientInfo: c,
			DeviceID:   deviceIDBytes,
			TenantID:   tenantIDBytes,
			SiteID:     siteIDBytes,
			EventType:  "disconnect",
		})
	}
	for _, r := range diff.Roamed {
		clientEvents = append(clientEvents, ClientEvent{
			ClientInfo: r.Client,
			DeviceID:   deviceIDBytes,
			TenantID:   tenantIDBytes,
			SiteID:     siteIDBytes,
			EventType:  "roam",
			OldBand:    r.OldBand,
		})
	}

	return dm, radioRows, clientEvents
}

// validateMetricsReport performs basic sanity checks on the payload.
func validateMetricsReport(payload *protocol.MetricsReportPayload) bool {
	if payload == nil {
		return false
	}
	// CPU usage should be 0-100
	if payload.System.CPUUsage < 0 || payload.System.CPUUsage > 100 {
		return false
	}
	// Memory values should be non-negative
	if payload.System.MemoryTotal == 0 {
		return false
	}
	if payload.System.MemoryUsed > payload.System.MemoryTotal {
		return false
	}
	// Each radio must have a valid band
	for _, r := range payload.Radios {
		if r.Band == "" {
			return false
		}
		if r.Utilization < 0 || r.Utilization > 100 {
			return false
		}
	}
	// Each client must have a MAC
	for _, c := range payload.Clients {
		if c.MAC == "" {
			return false
		}
	}
	return true
}
