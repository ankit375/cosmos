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
