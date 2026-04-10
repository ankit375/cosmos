package model

import (
	"time"

	"github.com/google/uuid"
)

type Site struct {
	ID          uuid.UUID              `json:"id" db:"id"`
	TenantID    uuid.UUID              `json:"tenant_id" db:"tenant_id"`
	Name        string                 `json:"name" db:"name"`
	Description string                 `json:"description" db:"description"`
	Address     string                 `json:"address" db:"address"`
	Timezone    string                 `json:"timezone" db:"timezone"`
	CountryCode string                 `json:"country_code" db:"country_code"`
	Latitude    *float64               `json:"latitude" db:"latitude"`
	Longitude   *float64               `json:"longitude" db:"longitude"`
	AutoAdopt   bool                   `json:"auto_adopt" db:"auto_adopt"`
	AutoUpgrade bool                   `json:"auto_upgrade" db:"auto_upgrade"`
	Settings    map[string]interface{} `json:"settings" db:"settings"`
	CreatedAt   time.Time              `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time              `json:"updated_at" db:"updated_at"`
}

type CreateSiteInput struct {
	Name        string   `json:"name" binding:"required,min=1,max=255"`
	Description string   `json:"description" binding:"omitempty,max=1000"`
	Address     string   `json:"address" binding:"omitempty,max=500"`
	Timezone    string   `json:"timezone" binding:"omitempty,max=50"`
	CountryCode string   `json:"country_code" binding:"omitempty,len=2,alpha"`
	Latitude    *float64 `json:"latitude" binding:"omitempty,min=-90,max=90"`
	Longitude   *float64 `json:"longitude" binding:"omitempty,min=-180,max=180"`
	AutoAdopt   bool     `json:"auto_adopt"`
	AutoUpgrade bool     `json:"auto_upgrade"`
}

type UpdateSiteInput struct {
	Name        *string  `json:"name" binding:"omitempty,min=1,max=255"`
	Description *string  `json:"description" binding:"omitempty,max=1000"`
	Address     *string  `json:"address" binding:"omitempty,max=500"`
	Timezone    *string  `json:"timezone" binding:"omitempty,max=50"`
	CountryCode *string  `json:"country_code" binding:"omitempty,len=2,alpha"`
	Latitude    *float64 `json:"latitude" binding:"omitempty,min=-90,max=90"`
	Longitude   *float64 `json:"longitude" binding:"omitempty,min=-180,max=180"`
	AutoAdopt   *bool    `json:"auto_adopt"`
	AutoUpgrade *bool    `json:"auto_upgrade"`
}

type SiteStats struct {
	SiteID        uuid.UUID `json:"site_id"`
	TotalDevices  int       `json:"total_devices"`
	OnlineDevices int       `json:"online_devices"`
	OfflineDevices int      `json:"offline_devices"`
	TotalClients  int       `json:"total_clients"`
}
