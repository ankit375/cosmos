package model

import (
	"time"

	"github.com/google/uuid"
)

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
	Temperature *float32  `json:"temperature" db:"temperature"`
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

type MetricsQuery struct {
	DeviceID   uuid.UUID `form:"device_id"`
	TenantID   uuid.UUID `form:"-"`
	Band       string    `form:"band"`
	Start      time.Time `form:"start" binding:"required"`
	End        time.Time `form:"end" binding:"required"`
	Resolution string    `form:"resolution" binding:"omitempty,oneof=raw 1m 5m 1h 1d"`
}
