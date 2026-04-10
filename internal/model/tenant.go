package model

import (
	"time"

	"github.com/google/uuid"
)

type Tenant struct {
	ID           uuid.UUID         `json:"id" db:"id"`
	Name         string            `json:"name" db:"name"`
	Slug         string            `json:"slug" db:"slug"`
	Subscription string            `json:"subscription" db:"subscription"`
	MaxDevices   int               `json:"max_devices" db:"max_devices"`
	MaxSites     int               `json:"max_sites" db:"max_sites"`
	MaxUsers     int                    `json:"max_users" db:"max_users"` 
	Settings     map[string]interface{} `json:"settings" db:"settings"`
	Active       bool              `json:"active" db:"active"`
	CreatedAt    time.Time         `json:"created_at" db:"created_at"`
	UpdatedAt    time.Time         `json:"updated_at" db:"updated_at"`
}

// TenantLimits holds current usage against limits.
type TenantLimits struct {
	MaxSites       int `json:"max_sites"`
	MaxDevices     int `json:"max_devices"`
	MaxUsers       int `json:"max_users"`
	CurrentSites   int `json:"current_sites"`
	CurrentDevices int `json:"current_devices"`
	CurrentUsers   int `json:"current_users"`
}

type CreateTenantInput struct {
	Name         string `json:"name" binding:"required,min=2,max=255"`
	Slug         string `json:"slug" binding:"required,min=2,max=63"`
	Subscription string `json:"subscription" binding:"omitempty,oneof=standard professional enterprise"`
	MaxDevices   int    `json:"max_devices" binding:"omitempty,min=1,max=100000"`
	MaxSites     int    `json:"max_sites" binding:"omitempty,min=1,max=1000"`
	MaxUsers     int    `json:"max_users" binding:"omitempty,min=1,max=10000"`
}

type UpdateTenantInput struct {
	Name         *string `json:"name" binding:"omitempty,min=2,max=255"`
	Subscription *string `json:"subscription" binding:"omitempty,oneof=standard professional enterprise"`
	MaxDevices   *int    `json:"max_devices" binding:"omitempty,min=1,max=100000"`
	MaxSites     *int    `json:"max_sites" binding:"omitempty,min=1,max=1000"`
	MaxUsers     *int    `json:"max_users" binding:"omitempty,min=1,max=10000"`
	Active       *bool   `json:"active"`
}
