package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yourorg/cloudctrl/internal/model"
)

// CommandStore handles command_queue database operations.
type CommandStore struct {
	pool pooler
}

// NewCommandStore creates a new command store.
func NewCommandStore(pool pooler) *CommandStore {
	return &CommandStore{pool: pool}
}

// Create inserts a new command into the queue.
func (s *CommandStore) Create(ctx context.Context, cmd *model.QueuedCommand) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO command_queue (
			id, tenant_id, device_id, command_type, payload, status,
			priority, max_retries, retry_count, correlation_id,
			expires_at, created_by, created_at
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, $8, $9, $10,
			$11, $12, $13
		)`,
		cmd.ID, cmd.TenantID, cmd.DeviceID, cmd.CommandType, cmd.Payload, cmd.Status,
		cmd.Priority, cmd.MaxRetries, cmd.RetryCount, cmd.CorrelationID,
		cmd.ExpiresAt, cmd.CreatedBy, cmd.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert command: %w", err)
	}
	return nil
}

// GetByID retrieves a single command by ID.
func (s *CommandStore) GetByID(ctx context.Context, cmdID uuid.UUID) (*model.QueuedCommand, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id, tenant_id, device_id, command_type, payload, status,
		       priority, max_retries, retry_count, correlation_id, error_message,
		       expires_at, sent_at, acked_at, completed_at, created_by, created_at
		FROM command_queue WHERE id = $1`, cmdID)

	return s.scanCommand(row)
}

// UpdateStatus updates the status and sets the correlation_id and sent_at.
func (s *CommandStore) UpdateStatus(ctx context.Context, cmdID uuid.UUID, status model.CommandStatus, correlationID string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE command_queue SET
			status = $1,
			correlation_id = $2,
			sent_at = NOW()
		WHERE id = $3`,
		status, correlationID, cmdID)
	if err != nil {
		return fmt.Errorf("update command status: %w", err)
	}
	return nil
}

// Complete marks a command as completed with optional result data.
func (s *CommandStore) Complete(ctx context.Context, cmdID uuid.UUID, result json.RawMessage) error {
	// Store result in payload field (overwrite) or we can use error_message for result summary
	_, err := s.pool.Exec(ctx, `
		UPDATE command_queue SET
			status = 'completed',
			completed_at = NOW()
		WHERE id = $1`,
		cmdID)
	if err != nil {
		return fmt.Errorf("complete command: %w", err)
	}
	return nil
}

// Fail marks a command as failed with an error message.
func (s *CommandStore) Fail(ctx context.Context, cmdID uuid.UUID, errMsg string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE command_queue SET
			status = 'failed',
			error_message = $1,
			completed_at = NOW()
		WHERE id = $2`,
		errMsg, cmdID)
	if err != nil {
		return fmt.Errorf("fail command: %w", err)
	}
	return nil
}

// Expire marks a command as expired.
func (s *CommandStore) Expire(ctx context.Context, cmdID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE command_queue SET
			status = 'expired',
			completed_at = NOW()
		WHERE id = $1`,
		cmdID)
	if err != nil {
		return fmt.Errorf("expire command: %w", err)
	}
	return nil
}

// ResetToQueued resets a command to queued status for retry.
func (s *CommandStore) ResetToQueued(ctx context.Context, cmdID uuid.UUID, retryCount int) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE command_queue SET
			status = 'queued',
			retry_count = $1,
			sent_at = NULL,
			correlation_id = NULL
		WHERE id = $2`,
		retryCount, cmdID)
	if err != nil {
		return fmt.Errorf("reset command to queued: %w", err)
	}
	return nil
}

// GetPendingByDevice returns all queued commands for a device, ordered by priority.
func (s *CommandStore) GetPendingByDevice(ctx context.Context, deviceID uuid.UUID) ([]*model.QueuedCommand, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, tenant_id, device_id, command_type, payload, status,
		       priority, max_retries, retry_count, correlation_id, error_message,
		       expires_at, sent_at, acked_at, completed_at, created_by, created_at
		FROM command_queue
		WHERE device_id = $1 AND status = 'queued'
		ORDER BY priority ASC, created_at ASC`, deviceID)
	if err != nil {
		return nil, fmt.Errorf("query pending commands: %w", err)
	}
	defer rows.Close()

	return s.scanCommands(rows)
}

// GetAllPending returns all queued and sent commands across all devices.
// Used for startup recovery.
func (s *CommandStore) GetAllPending(ctx context.Context) ([]*model.QueuedCommand, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, tenant_id, device_id, command_type, payload, status,
		       priority, max_retries, retry_count, correlation_id, error_message,
		       expires_at, sent_at, acked_at, completed_at, created_by, created_at
		FROM command_queue
		WHERE status IN ('queued', 'sent')
		ORDER BY priority ASC, created_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("query all pending commands: %w", err)
	}
	defer rows.Close()

	return s.scanCommands(rows)
}

// ListByDevice returns commands for a device with pagination (for API).
func (s *CommandStore) ListByDevice(ctx context.Context, tenantID, deviceID uuid.UUID, limit, offset int) ([]*model.QueuedCommand, int, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	// Count
	var total int
	err := s.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM command_queue
		WHERE tenant_id = $1 AND device_id = $2`,
		tenantID, deviceID).Scan(&total)
	if err != nil {
		return nil, 0, fmt.Errorf("count commands: %w", err)
	}

	// Query
	rows, err := s.pool.Query(ctx, `
		SELECT id, tenant_id, device_id, command_type, payload, status,
		       priority, max_retries, retry_count, correlation_id, error_message,
		       expires_at, sent_at, acked_at, completed_at, created_by, created_at
		FROM command_queue
		WHERE tenant_id = $1 AND device_id = $2
		ORDER BY created_at DESC
		LIMIT $3 OFFSET $4`,
		tenantID, deviceID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("query commands: %w", err)
	}
	defer rows.Close()

	cmds, err := s.scanCommands(rows)
	if err != nil {
		return nil, 0, err
	}

	return cmds, total, nil
}

// CountPendingByDevice returns the number of queued commands for a device.
func (s *CommandStore) CountPendingByDevice(ctx context.Context, deviceID uuid.UUID) (int, error) {
	var count int
	err := s.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM command_queue
		WHERE device_id = $1 AND status = 'queued'`,
		deviceID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count pending commands: %w", err)
	}
	return count, nil
}

// ============================================================
// SCAN HELPERS
// ============================================================

func (s *CommandStore) scanCommand(row pgx.Row) (*model.QueuedCommand, error) {
	var cmd model.QueuedCommand
	err := row.Scan(
		&cmd.ID, &cmd.TenantID, &cmd.DeviceID, &cmd.CommandType, &cmd.Payload, &cmd.Status,
		&cmd.Priority, &cmd.MaxRetries, &cmd.RetryCount, &cmd.CorrelationID, &cmd.ErrorMessage,
		&cmd.ExpiresAt, &cmd.SentAt, &cmd.AckedAt, &cmd.CompletedAt, &cmd.CreatedBy, &cmd.CreatedAt,
	)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan command: %w", err)
	}
	return &cmd, nil
}

func (s *CommandStore) scanCommands(rows pgx.Rows) ([]*model.QueuedCommand, error) {
	var cmds []*model.QueuedCommand
	for rows.Next() {
		var cmd model.QueuedCommand
		if err := rows.Scan(
			&cmd.ID, &cmd.TenantID, &cmd.DeviceID, &cmd.CommandType, &cmd.Payload, &cmd.Status,
			&cmd.Priority, &cmd.MaxRetries, &cmd.RetryCount, &cmd.CorrelationID, &cmd.ErrorMessage,
			&cmd.ExpiresAt, &cmd.SentAt, &cmd.AckedAt, &cmd.CompletedAt, &cmd.CreatedBy, &cmd.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan command row: %w", err)
		}
		cmds = append(cmds, &cmd)
	}
	return cmds, rows.Err()
}
