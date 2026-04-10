package model

import (
	"time"

	"github.com/google/uuid"
)

type FirmwareChannel string

const (
	FirmwareChannelStable  FirmwareChannel = "stable"
	FirmwareChannelBeta    FirmwareChannel = "beta"
	FirmwareChannelNightly FirmwareChannel = "nightly"
)

type Firmware struct {
	ID           uuid.UUID       `json:"id" db:"id"`
	Version      string          `json:"version" db:"version"`
	Model        string          `json:"model" db:"model"`
	Filename     string          `json:"filename" db:"filename"`
	Size         int64           `json:"size" db:"size"`
	SHA256       string          `json:"sha256" db:"sha256"`
	StoragePath  string          `json:"-" db:"storage_path"`
	ReleaseNotes string          `json:"release_notes" db:"release_notes"`
	Channel      FirmwareChannel `json:"channel" db:"channel"`
	MinVersion   *string         `json:"min_version" db:"min_version"`
	CreatedAt    time.Time       `json:"created_at" db:"created_at"`
}

type FirmwareUpgradeStatus string

const (
	UpgradeStatusPending     FirmwareUpgradeStatus = "pending"
	UpgradeStatusQueued      FirmwareUpgradeStatus = "queued"
	UpgradeStatusDownloading FirmwareUpgradeStatus = "downloading"
	UpgradeStatusInstalling  FirmwareUpgradeStatus = "installing"
	UpgradeStatusRebooting   FirmwareUpgradeStatus = "rebooting"
	UpgradeStatusComplete    FirmwareUpgradeStatus = "complete"
	UpgradeStatusFailed      FirmwareUpgradeStatus = "failed"
)

type FirmwareUpgradeTask struct {
	ID          uuid.UUID             `json:"id" db:"id"`
	TenantID    uuid.UUID             `json:"tenant_id" db:"tenant_id"`
	DeviceID    uuid.UUID             `json:"device_id" db:"device_id"`
	FirmwareID  uuid.UUID             `json:"firmware_id" db:"firmware_id"`
	Status      FirmwareUpgradeStatus `json:"status" db:"status"`
	Progress    int                   `json:"progress" db:"progress"`
	ErrorMessage *string              `json:"error_message" db:"error_message"`
	ScheduledAt *time.Time            `json:"scheduled_at" db:"scheduled_at"`
	StartedAt   *time.Time            `json:"started_at" db:"started_at"`
	CompletedAt *time.Time            `json:"completed_at" db:"completed_at"`
	CreatedAt   time.Time             `json:"created_at" db:"created_at"`
}
