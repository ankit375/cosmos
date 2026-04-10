package model

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type DeviceStatus string

const (
	DeviceStatusPendingAdopt DeviceStatus = "pending_adopt"
	DeviceStatusAdopting     DeviceStatus = "adopting"
	DeviceStatusProvisioning DeviceStatus = "provisioning"
	DeviceStatusOnline       DeviceStatus = "online"
	DeviceStatusOffline      DeviceStatus = "offline"
	DeviceStatusUpgrading    DeviceStatus = "upgrading"
	DeviceStatusConfigPending DeviceStatus = "config_pending"
	DeviceStatusError        DeviceStatus = "error"
	DeviceStatusDecommissioned DeviceStatus = "decommissioned"
)

func (s DeviceStatus) IsValid() bool {
	switch s {
	case DeviceStatusPendingAdopt, DeviceStatusAdopting, DeviceStatusProvisioning,
		DeviceStatusOnline, DeviceStatusOffline, DeviceStatusUpgrading,
		DeviceStatusConfigPending, DeviceStatusError, DeviceStatusDecommissioned:
		return true
	}
	return false
}

type Device struct {
	ID                    uuid.UUID       `json:"id" db:"id"`
	TenantID              uuid.UUID       `json:"tenant_id" db:"tenant_id"`
	SiteID                *uuid.UUID      `json:"site_id" db:"site_id"`
	MAC                   string          `json:"mac" db:"mac"`
	Serial                string          `json:"serial" db:"serial"`
	Name                  string          `json:"name" db:"name"`
	Model                 string          `json:"model" db:"model"`
	Status                DeviceStatus    `json:"status" db:"status"`
	FirmwareVersion       string          `json:"firmware_version" db:"firmware_version"`
	TargetFirmware        *string         `json:"target_firmware" db:"target_firmware"`
	IPAddress             *string         `json:"ip_address" db:"ip_address"`
	PublicIP              *string         `json:"public_ip" db:"public_ip"`
	DesiredConfigVersion  int64           `json:"desired_config_version" db:"desired_config_version"`
	AppliedConfigVersion  int64           `json:"applied_config_version" db:"applied_config_version"`
	DeviceTokenHash       *string         `json:"-" db:"device_token_hash"`
	Uptime                int64           `json:"uptime" db:"uptime"`
	LastSeen              *time.Time      `json:"last_seen" db:"last_seen"`
	AdoptedAt             *time.Time      `json:"adopted_at" db:"adopted_at"`
	LastConfigApplied     *time.Time      `json:"last_config_applied" db:"last_config_applied"`
	Capabilities          json.RawMessage `json:"capabilities" db:"capabilities"`
	SystemInfo            json.RawMessage `json:"system_info" db:"system_info"`
	Tags                  []string        `json:"tags" db:"tags"`
	Notes                 string          `json:"notes" db:"notes"`
	CreatedAt             time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt             time.Time       `json:"updated_at" db:"updated_at"`
}

type DeviceListParams struct {
	TenantID uuid.UUID     `form:"-"`
	SiteID   *uuid.UUID    `form:"site_id"`
	Status   *DeviceStatus `form:"status"`
	Model    string        `form:"model"`
	Search   string        `form:"search"`
	Tags     []string      `form:"tags"`
	Offset   int           `form:"offset" binding:"min=0"`
	Limit    int           `form:"limit" binding:"min=0,max=200"`
	OrderBy  string        `form:"order_by" binding:"omitempty,oneof=name status last_seen created_at model"`
	OrderDir string        `form:"order_dir" binding:"omitempty,oneof=asc desc"`
}

type UpdateDeviceInput struct {
	Name   *string   `json:"name" binding:"omitempty,max=255"`
	Notes  *string   `json:"notes" binding:"omitempty,max=2000"`
	Tags   *[]string `json:"tags"`
}

type AdoptDeviceInput struct {
	SiteID uuid.UUID `json:"site_id" binding:"required"`
	Name   string    `json:"name" binding:"omitempty,max=255"`
}

type MoveDeviceInput struct {
	SiteID uuid.UUID `json:"site_id" binding:"required"`
}

type DeviceStats struct {
	TotalDevices   int `json:"total_devices"`
	OnlineCount    int `json:"online_count"`
	OfflineCount   int `json:"offline_count"`
	PendingCount   int `json:"pending_count"`
	UpgradingCount int `json:"upgrading_count"`
}
