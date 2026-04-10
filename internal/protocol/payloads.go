package protocol

import "encoding/json"

// ============================================================
// Device → Controller payloads
// ============================================================

type DeviceAuthPayload struct {
	Token           string          `json:"token"`
	MAC             string          `json:"mac"`
	Serial          string          `json:"serial"`
	Model           string          `json:"model"`
	FirmwareVersion string          `json:"firmware_version"`
	ConfigVersion   int64           `json:"config_version"`
	AgentVersion    string          `json:"agent_version"`
	Capabilities    json.RawMessage `json:"capabilities,omitempty"`
	SystemInfo      json.RawMessage `json:"system_info,omitempty"`
}

type HeartbeatPayload struct {
	Uptime          int64    `json:"uptime"`
	ConfigVersion   int64    `json:"config_version"`
	FirmwareVersion string   `json:"firmware_version"`
	ClientCount     int      `json:"client_count"`
	CPUUsage        float64  `json:"cpu_usage"`
	MemoryUsed      uint64   `json:"memory_used"`
	MemoryTotal     uint64   `json:"memory_total"`
	IPAddress       string   `json:"ip_address"`
	LoadAvg         [3]float64 `json:"load_avg"`
}

type ConfigAckPayload struct {
	Version   int64    `json:"version"`
	Success   bool     `json:"success"`
	AppliedAt int64    `json:"applied_at,omitempty"`
	Error     string   `json:"error,omitempty"`
	Warnings  []string `json:"warnings,omitempty"`
}

type CommandResponsePayload struct {
	Success bool            `json:"success"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   string          `json:"error,omitempty"`
}

type EventPayload struct {
	EventType string          `json:"event_type"`
	Severity  string          `json:"severity"`
	Message   string          `json:"message"`
	Details   json.RawMessage `json:"details,omitempty"`
}

type ClientEventPayload struct {
	Event     string `json:"event"`
	MAC       string `json:"mac"`
	IP        string `json:"ip,omitempty"`
	Hostname  string `json:"hostname,omitempty"`
	SSID      string `json:"ssid"`
	Band      string `json:"band"`
	RSSI      int    `json:"rssi,omitempty"`
	Reason    string `json:"reason,omitempty"`
	Timestamp int64  `json:"timestamp"`
}

type FirmwareProgressPayload struct {
	FirmwareID string `json:"firmware_id"`
	Status     string `json:"status"`
	Progress   int    `json:"progress"`
	Error      string `json:"error,omitempty"`
}

// ============================================================
// Controller → Device payloads
// ============================================================

type AuthResultPayload struct {
	Success           bool   `json:"success"`
	DeviceID          string `json:"device_id,omitempty"`
	DeviceToken       string `json:"device_token,omitempty"`
	ConfigRequired    bool   `json:"config_required"`
	ServerTime        int64  `json:"server_time"`
	HeartbeatInterval int    `json:"heartbeat_interval"`
	MetricsInterval   int    `json:"metrics_interval"`
	Error             string `json:"error,omitempty"`
	Status            string `json:"status,omitempty"`
}

type HeartbeatAckPayload struct {
	ServerTime      int64  `json:"server_time"`
	ConfigVersion   int64  `json:"config_version"`
	CommandsPending int    `json:"commands_pending"`
	FirmwareTarget  string `json:"firmware_target,omitempty"`
}

type ConfigPushPayload struct {
	Version        int64           `json:"version"`
	Config         json.RawMessage `json:"config"`
	SafeApply      bool            `json:"safe_apply"`
	ConfirmTimeout int             `json:"confirm_timeout"`
}

type ConfigConfirmPayload struct {
	Version   int64 `json:"version"`
	Confirmed bool  `json:"confirmed"`
}

type CommandPayload struct {
	Command string          `json:"command"`
	Params  json.RawMessage `json:"params,omitempty"`
	Timeout int             `json:"timeout"`
}

type FirmwareUpgradePayload struct {
	FirmwareID    string `json:"firmware_id"`
	Version       string `json:"version"`
	URL           string `json:"url"`
	SHA256        string `json:"sha256"`
	Size          int64  `json:"size"`
	Force         bool   `json:"force"`
	DownloadToken string `json:"download_token"`
}

// ============================================================
// Bidirectional payloads
// ============================================================

type ErrorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ============================================================
// Telemetry payloads (Phase 7)
// ============================================================

// MetricsReportPayload is the full metrics report sent by the AP every 60s.
// Wire type: 0x0010 MetricsReport (Device → Controller)
type MetricsReportPayload struct {
	Timestamp  int64                       `json:"timestamp"`
	System     MetricsSystemPayload        `json:"system"`
	Radios     []MetricsRadioPayload       `json:"radios"`
	Clients    []MetricsClientPayload      `json:"clients"`
	Interfaces []MetricsInterfacePayload   `json:"interfaces,omitempty"`
}

type MetricsSystemPayload struct {
	CPUUsage    float64 `json:"cpu_usage"`
	MemoryUsed  uint64  `json:"memory_used"`
	MemoryTotal uint64  `json:"memory_total"`
	LoadAvg1    float64 `json:"load_avg_1"`
	LoadAvg5    float64 `json:"load_avg_5"`
	LoadAvg15   float64 `json:"load_avg_15"`
	Uptime      int64   `json:"uptime"`
	Temperature float64 `json:"temperature,omitempty"`
}

type MetricsRadioPayload struct {
	Band         string  `json:"band"`
	Channel      int     `json:"channel"`
	ChannelWidth int     `json:"channel_width"`
	TxPower      int     `json:"tx_power"`
	NoiseFloor   int     `json:"noise_floor"`
	Utilization  float64 `json:"utilization"`
	ClientCount  int     `json:"client_count"`
	TxBytes      uint64  `json:"tx_bytes"`
	RxBytes      uint64  `json:"rx_bytes"`
	TxPackets    uint64  `json:"tx_packets"`
	RxPackets    uint64  `json:"rx_packets"`
	TxErrors     uint64  `json:"tx_errors"`
	RxErrors     uint64  `json:"rx_errors"`
	TxRetries    uint64  `json:"tx_retries"`
}

type MetricsClientPayload struct {
	MAC            string `json:"mac"`
	IP             string `json:"ip,omitempty"`
	Hostname       string `json:"hostname,omitempty"`
	SSID           string `json:"ssid"`
	Band           string `json:"band"`
	RSSI           int    `json:"rssi"`
	SNR            int    `json:"snr,omitempty"`
	TxRate         int    `json:"tx_rate"`
	RxRate         int    `json:"rx_rate"`
	TxBytes        uint64 `json:"tx_bytes"`
	RxBytes        uint64 `json:"rx_bytes"`
	ConnectedSince int64  `json:"connected_since"`
}

type MetricsInterfacePayload struct {
	Name       string `json:"name"`
	TxBytes    uint64 `json:"tx_bytes"`
	RxBytes    uint64 `json:"rx_bytes"`
	Speed      int    `json:"speed"`
	FullDuplex bool   `json:"full_duplex"`
	Up         bool   `json:"up"`
}
