package model

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type EventSeverity string

const (
	SeverityDebug    EventSeverity = "debug"
	SeverityInfo     EventSeverity = "info"
	SeverityWarning  EventSeverity = "warning"
	SeverityError    EventSeverity = "error"
	SeverityCritical EventSeverity = "critical"
)

type DeviceEvent struct {
	ID        uuid.UUID       `json:"id" db:"id"`
	TenantID  uuid.UUID       `json:"tenant_id" db:"tenant_id"`
	DeviceID  uuid.UUID       `json:"device_id" db:"device_id"`
	EventType string          `json:"event_type" db:"event_type"`
	Severity  EventSeverity   `json:"severity" db:"severity"`
	Message   string          `json:"message" db:"message"`
	Details   json.RawMessage `json:"details,omitempty" db:"details"`
	Timestamp time.Time       `json:"timestamp" db:"timestamp"`
}

type EventListParams struct {
	TenantID  uuid.UUID      `form:"-"`
	DeviceID  *uuid.UUID     `form:"device_id"`
	SiteID    *uuid.UUID     `form:"site_id"`
	EventType string         `form:"event_type"`
	Severity  *EventSeverity `form:"severity"`
	Start     *time.Time     `form:"start"`
	End       *time.Time     `form:"end"`
	Offset    int            `form:"offset" binding:"min=0"`
	Limit     int            `form:"limit" binding:"min=0,max=200"`
}

type AuditLogEntry struct {
	ID           uuid.UUID       `json:"id" db:"id"`
	TenantID     uuid.UUID       `json:"tenant_id" db:"tenant_id"`
	UserID       *uuid.UUID      `json:"user_id" db:"user_id"`
	Action       string          `json:"action" db:"action"`
	ResourceType string          `json:"resource_type" db:"resource_type"`
	ResourceID   *uuid.UUID      `json:"resource_id" db:"resource_id"`
	Details      json.RawMessage `json:"details,omitempty" db:"details"`
	IPAddress    *string         `json:"ip_address" db:"ip_address"`
	Timestamp    time.Time       `json:"timestamp" db:"timestamp"`
}

type AuditListParams struct {
	TenantID     uuid.UUID  `form:"-"`
	UserID       *uuid.UUID `form:"user_id"`
	Action       string     `form:"action"`
	ResourceType string     `form:"resource_type"`
	ResourceID   *uuid.UUID `form:"resource_id"`
	Start        *time.Time `form:"start"`
	End          *time.Time `form:"end"`
	Offset       int        `form:"offset" binding:"min=0"`
	Limit        int        `form:"limit" binding:"min=0,max=200"`
}
