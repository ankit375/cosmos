package model

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type CommandStatus string

const (
	CommandStatusQueued    CommandStatus = "queued"
	CommandStatusSent      CommandStatus = "sent"
	CommandStatusAcked     CommandStatus = "acked"
	CommandStatusCompleted CommandStatus = "completed"
	CommandStatusFailed    CommandStatus = "failed"
	CommandStatusExpired   CommandStatus = "expired"
)

type QueuedCommand struct {
	ID            uuid.UUID       `json:"id" db:"id"`
	TenantID      uuid.UUID       `json:"tenant_id" db:"tenant_id"`
	DeviceID      uuid.UUID       `json:"device_id" db:"device_id"`
	CommandType   string          `json:"command_type" db:"command_type"`
	Payload       json.RawMessage `json:"payload" db:"payload"`
	Status        CommandStatus   `json:"status" db:"status"`
	Priority      int             `json:"priority" db:"priority"`
	MaxRetries    int             `json:"max_retries" db:"max_retries"`
	RetryCount    int             `json:"retry_count" db:"retry_count"`
	CorrelationID *string         `json:"correlation_id" db:"correlation_id"`
	ErrorMessage  *string         `json:"error_message" db:"error_message"`
	ExpiresAt     *time.Time      `json:"expires_at" db:"expires_at"`
	SentAt        *time.Time      `json:"sent_at" db:"sent_at"`
	AckedAt       *time.Time      `json:"acked_at" db:"acked_at"`
	CompletedAt   *time.Time      `json:"completed_at" db:"completed_at"`
	CreatedBy     *uuid.UUID      `json:"created_by" db:"created_by"`
	CreatedAt     time.Time       `json:"created_at" db:"created_at"`
}
