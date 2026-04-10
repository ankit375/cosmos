package model

import (
	"time"

	"github.com/google/uuid"
)

// ============================================================
// Core metric rows (map to TimescaleDB hypertables)
// ============================================================

type DeviceMetrics struct {
	Time        time.Time `json:"time" db:"time"`
	DeviceID    uuid.UUID `json:"device_id" db:"device_id"`
	TenantID    uuid.UUID `json:"tenant_id" db:"tenant_id"`
	CPUUsage    *float32  `json:"cpu_usage" db:"cpu_usage"`
	MemoryUsed  *int64    `json:"memory_used" db:"memory_used"`
	MemoryTotal *int64    `json:"memory_total" db:"memory_total"`
	LoadAvg1    *float32  `json:"load_avg_1" db:"load_avg_1"`
	LoadAvg5    *float32  `json:"load_avg_5" db:"load_avg_5"`
	LoadAvg15   *float32  `json:"load_avg_15" db:"load_avg_15"`
	Uptime      *int64    `json:"uptime" db:"uptime"`
	ClientCount *int16    `json:"client_count" db:"client_count"`
	Temperature *float32  `json:"temperature,omitempty" db:"temperature"`
}

type RadioMetrics struct {
	Time         time.Time `json:"time" db:"time"`
	DeviceID     uuid.UUID `json:"device_id" db:"device_id"`
	TenantID     uuid.UUID `json:"tenant_id" db:"tenant_id"`
	Band         string    `json:"band" db:"band"`
	Channel      *int16    `json:"channel" db:"channel"`
	ChannelWidth *int16    `json:"channel_width" db:"channel_width"`
	TxPower      *int16    `json:"tx_power" db:"tx_power"`
	NoiseFloor   *int16    `json:"noise_floor" db:"noise_floor"`
	Utilization  *float32  `json:"utilization" db:"utilization"`
	ClientCount  *int16    `json:"client_count" db:"client_count"`
	TxBytes      *int64    `json:"tx_bytes" db:"tx_bytes"`
	RxBytes      *int64    `json:"rx_bytes" db:"rx_bytes"`
	TxPackets    *int64    `json:"tx_packets" db:"tx_packets"`
	RxPackets    *int64    `json:"rx_packets" db:"rx_packets"`
	TxErrors     *int64    `json:"tx_errors" db:"tx_errors"`
	RxErrors     *int64    `json:"rx_errors" db:"rx_errors"`
	TxRetries    *int64    `json:"tx_retries" db:"tx_retries"`
}

// ============================================================
// Client sessions
// ============================================================

type ClientSession struct {
	ID               uuid.UUID  `json:"id" db:"id"`
	TenantID         uuid.UUID  `json:"tenant_id" db:"tenant_id"`
	DeviceID         uuid.UUID  `json:"device_id" db:"device_id"`
	SiteID           *uuid.UUID `json:"site_id" db:"site_id"`
	ClientMAC        string     `json:"client_mac" db:"client_mac"`
	ClientIP         *string    `json:"client_ip" db:"client_ip"`
	Hostname         *string    `json:"hostname" db:"hostname"`
	SSID             string     `json:"ssid" db:"ssid"`
	Band             string     `json:"band" db:"band"`
	ConnectedAt      time.Time  `json:"connected_at" db:"connected_at"`
	DisconnectedAt   *time.Time `json:"disconnected_at" db:"disconnected_at"`
	DurationSecs     *int       `json:"duration_secs" db:"duration_secs"`
	TotalTxBytes     int64      `json:"total_tx_bytes" db:"total_tx_bytes"`
	TotalRxBytes     int64      `json:"total_rx_bytes" db:"total_rx_bytes"`
	AvgRSSI          *int16     `json:"avg_rssi" db:"avg_rssi"`
	MinRSSI          *int16     `json:"min_rssi" db:"min_rssi"`
	MaxRSSI          *int16     `json:"max_rssi" db:"max_rssi"`
	AvgTxRate        *int       `json:"avg_tx_rate" db:"avg_tx_rate"`
	AvgRxRate        *int       `json:"avg_rx_rate" db:"avg_rx_rate"`
	DisconnectReason *string    `json:"disconnect_reason" db:"disconnect_reason"`
	Is11r            bool       `json:"is_11r" db:"is_11r"`
}

// ============================================================
// Live client info (in-memory + Redis snapshot)
// ============================================================

type ClientInfo struct {
	MAC            string    `json:"mac"`
	IP             string    `json:"ip,omitempty"`
	Hostname       string    `json:"hostname,omitempty"`
	SSID           string    `json:"ssid"`
	Band           string    `json:"band"`
	RSSI           int       `json:"rssi"`
	SNR            int       `json:"snr,omitempty"`
	TxRate         int       `json:"tx_rate"`
	RxRate         int       `json:"rx_rate"`
	TxBytes        uint64    `json:"tx_bytes"`
	RxBytes        uint64    `json:"rx_bytes"`
	ConnectedSince time.Time `json:"connected_since"`
}

// ClientDiffResult captures the result of comparing two client snapshots.
type ClientDiffResult struct {
	Connected    []ClientInfo // new clients
	Disconnected []ClientInfo // clients that disappeared
	Roamed       []ClientRoamInfo // changed device/band
}

type ClientRoamInfo struct {
	Client  ClientInfo
	OldBand string
	NewBand string
}

// ============================================================
// Query types
// ============================================================

type MetricsQuery struct {
	DeviceID   uuid.UUID `form:"device_id"`
	TenantID   uuid.UUID `form:"-"`
	Band       string    `form:"band"`
	Start      time.Time `form:"start" binding:"required"`
	End        time.Time `form:"end" binding:"required"`
	Resolution string    `form:"resolution" binding:"omitempty,oneof=raw 1m 5m 1h 1d"`
}

// AutoResolution selects the best resolution based on the time range.
func (q *MetricsQuery) AutoResolution() string {
	if q.Resolution != "" && q.Resolution != "raw" {
		return q.Resolution
	}

	duration := q.End.Sub(q.Start)
	switch {
	case duration <= 6*time.Hour:
		return "raw" // 1-minute raw data
	case duration <= 48*time.Hour:
		return "1h" // hourly aggregates
	default:
		return "1d" // daily aggregates
	}
}

type ClientSessionQuery struct {
	TenantID  uuid.UUID  `form:"-"`
	DeviceID  *uuid.UUID `form:"device_id"`
	SiteID    *uuid.UUID `form:"site_id"`
	ClientMAC string     `form:"client_mac"`
	SSID      string     `form:"ssid"`
	Band      string     `form:"band"`
	Start     *time.Time `form:"start"`
	End       *time.Time `form:"end"`
	Active    *bool      `form:"active"` // nil=all, true=connected, false=disconnected
	Offset    int        `form:"offset" binding:"min=0"`
	Limit     int        `form:"limit" binding:"min=0,max=200"`
}

// ============================================================
// Aggregate response types (for API)
// ============================================================

type DeviceMetricsResponse struct {
	Time       time.Time `json:"time"`
	CPUAvg     *float64  `json:"cpu_avg"`
	CPUMax     *float64  `json:"cpu_max"`
	MemPctAvg  *float64  `json:"mem_pct_avg"`
	ClientsMax *int      `json:"clients_max"`
	ClientsAvg *float64  `json:"clients_avg"`
}

type RadioMetricsResponse struct {
	Time           time.Time `json:"time"`
	Band           string    `json:"band"`
	UtilAvg        *float64  `json:"utilization_avg"`
	UtilMax        *float64  `json:"utilization_max"`
	ClientsMax     *int      `json:"clients_max"`
	TotalTxBytes   *int64    `json:"total_tx_bytes"`
	TotalRxBytes   *int64    `json:"total_rx_bytes"`
	TotalRetries   *int64    `json:"total_retries"`
	NoiseFloorAvg  *float64  `json:"noise_floor_avg"`
}

type SiteMetricsResponse struct {
	Time          time.Time `json:"time"`
	TotalDevices  int       `json:"total_devices"`
	OnlineDevices int       `json:"online_devices"`
	TotalClients  int       `json:"total_clients"`
	AvgCPU        *float64  `json:"avg_cpu"`
	MaxCPU        *float64  `json:"max_cpu"`
	AvgMemPct     *float64  `json:"avg_mem_pct"`
}
