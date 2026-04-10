package model

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// ConfigStatus tracks the lifecycle of a config push.
type ConfigStatus string

const (
	ConfigStatusPending    ConfigStatus = "pending"
	ConfigStatusPushed     ConfigStatus = "pushed"
	ConfigStatusApplied    ConfigStatus = "applied"
	ConfigStatusFailed     ConfigStatus = "failed"
	ConfigStatusRolledBack ConfigStatus = "rolled_back"
)

// ConfigSource indicates how a config was generated.
type ConfigSource string

const (
	ConfigSourceTemplate ConfigSource = "template"
	ConfigSourceOverride ConfigSource = "override"
	ConfigSourceManual   ConfigSource = "manual"
	ConfigSourceRollback ConfigSource = "rollback"
)

// ConfigTemplate is a site-level config applied to all APs.
type ConfigTemplate struct {
	ID          uuid.UUID       `json:"id" db:"id"`
	TenantID    uuid.UUID       `json:"tenant_id" db:"tenant_id"`
	SiteID      uuid.UUID       `json:"site_id" db:"site_id"`
	Version     int64           `json:"version" db:"version"`
	Config      json.RawMessage `json:"config" db:"config"`
	Description string          `json:"description" db:"description"`
	CreatedBy   *uuid.UUID      `json:"created_by" db:"created_by"`
	CreatedAt   time.Time       `json:"created_at" db:"created_at"`
}

// DeviceConfig is the per-device config history.
type DeviceConfig struct {
	ID               uuid.UUID       `json:"id" db:"id"`
	TenantID         uuid.UUID       `json:"tenant_id" db:"tenant_id"`
	DeviceID         uuid.UUID       `json:"device_id" db:"device_id"`
	Version          int64           `json:"version" db:"version"`
	Config           json.RawMessage `json:"config" db:"config"`
	Source           ConfigSource    `json:"source" db:"source"`
	TemplateVersion  *int64          `json:"template_version" db:"template_version"`
	DeviceOverrides  json.RawMessage `json:"device_overrides" db:"device_overrides"`
	Status           ConfigStatus    `json:"status" db:"status"`
	ErrorMessage     *string         `json:"error_message" db:"error_message"`
	CreatedBy        *uuid.UUID      `json:"created_by" db:"created_by"`
	PushedAt         *time.Time      `json:"pushed_at" db:"pushed_at"`
	AppliedAt        *time.Time      `json:"applied_at" db:"applied_at"`
	CreatedAt        time.Time       `json:"created_at" db:"created_at"`
}

// DeviceOverride holds per-device config overrides.
type DeviceOverride struct {
	DeviceID  uuid.UUID       `json:"device_id" db:"device_id"`
	TenantID  uuid.UUID       `json:"tenant_id" db:"tenant_id"`
	Overrides json.RawMessage `json:"overrides" db:"overrides"`
	UpdatedBy *uuid.UUID      `json:"updated_by" db:"updated_by"`
	UpdatedAt time.Time       `json:"updated_at" db:"updated_at"`
}

// UpdateConfigTemplateInput is the input for creating/updating a site config.
type UpdateConfigTemplateInput struct {
	Config      json.RawMessage `json:"config" binding:"required"`
	Description string          `json:"description" binding:"omitempty,max=500"`
}

// UpdateDeviceOverrideInput is the input for setting device overrides.
type UpdateDeviceOverrideInput struct {
	Overrides json.RawMessage `json:"overrides" binding:"required"`
}

// APConfig is the structured configuration sent to APs.
type APConfig struct {
	System   APSystemConfig    `json:"system"`
	Wireless []APWirelessConfig `json:"wireless"`
	Network  APNetworkConfig   `json:"network"`
}

type APSystemConfig struct {
	Hostname   string   `json:"hostname"`
	Timezone   string   `json:"timezone"`
	LEDEnabled bool     `json:"led_enabled"`
	NTPServers []string `json:"ntp_servers,omitempty"`
	SyslogServer string `json:"syslog_server,omitempty"`
}

type APWirelessConfig struct {
	Band         string         `json:"band"`
	Channel      interface{}    `json:"channel"`
	ChannelWidth int            `json:"channel_width"`
	TxPower      interface{}    `json:"tx_power"`
	Country      string         `json:"country"`
	SSIDs        []APSSIDConfig `json:"ssids"`
}

type APSSIDConfig struct {
	Name            string           `json:"name"`
	Enabled         bool             `json:"enabled"`
	Hidden          bool             `json:"hidden"`
	Security        APSecurityConfig `json:"security"`
	VLAN            int              `json:"vlan,omitempty"`
	MaxClients      int              `json:"max_clients,omitempty"`
	ClientIsolation bool             `json:"client_isolation"`
	BandSteering    bool             `json:"band_steering,omitempty"`
	FastRoaming     bool             `json:"fast_roaming,omitempty"`
	RateLimit       *APRateLimit     `json:"rate_limit,omitempty"`
}

type APSecurityConfig struct {
	Mode       string          `json:"mode"`
	Passphrase string          `json:"passphrase,omitempty"`
	Radius     *APRadiusConfig `json:"radius,omitempty"`
}

type APRadiusConfig struct {
	AuthServer string `json:"auth_server"`
	AuthPort   int    `json:"auth_port"`
	AuthSecret string `json:"auth_secret"`
	AcctServer string `json:"acct_server,omitempty"`
	AcctPort   int    `json:"acct_port,omitempty"`
	AcctSecret string `json:"acct_secret,omitempty"`
}

type APRateLimit struct {
	DownKbps int `json:"down_kbps"`
	UpKbps   int `json:"up_kbps"`
}

type APNetworkConfig struct {
	ManagementVLAN int                  `json:"management_vlan"`
	Interfaces     []APInterfaceConfig  `json:"interfaces"`
	DNS            []string             `json:"dns,omitempty"`
}

type APInterfaceConfig struct {
	Name    string `json:"name"`
	Proto   string `json:"proto"`
	Address string `json:"address,omitempty"`
	Netmask string `json:"netmask,omitempty"`
	Gateway string `json:"gateway,omitempty"`
	VLAN    int    `json:"vlan,omitempty"`
	MTU     int    `json:"mtu,omitempty"`
}
