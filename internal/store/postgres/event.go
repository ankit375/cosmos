package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/yourorg/cloudctrl/internal/model"
)

type EventStore struct {
	pool pooler
}

func NewEventStore(pool pooler) *EventStore {
	return &EventStore{pool: pool}
}

func (s *EventStore) Create(ctx context.Context, event *model.DeviceEvent) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO device_events (id, tenant_id, device_id, event_type, severity, message, details, timestamp)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		uuid.New(), event.TenantID, event.DeviceID, event.EventType,
		event.Severity, event.Message, event.Details, event.Timestamp,
	)
	if err != nil {
		return fmt.Errorf("insert device event: %w", err)
	}
	return nil
}

// BatchCreate inserts multiple events in one transaction.
func (s *EventStore) BatchCreate(ctx context.Context, events []*model.DeviceEvent) error {
	if len(events) == 0 {
		return nil
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	for _, event := range events {
		_, err := tx.Exec(ctx, `
			INSERT INTO device_events (id, tenant_id, device_id, event_type, severity, message, details, timestamp)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			uuid.New(), event.TenantID, event.DeviceID, event.EventType,
			event.Severity, event.Message, event.Details, event.Timestamp,
		)
		if err != nil {
			return fmt.Errorf("insert event: %w", err)
		}
	}

	return tx.Commit(ctx)
}

// Emit is a convenience method that creates a DeviceEvent and persists it.
func (s *EventStore) Emit(ctx context.Context, tenantID, deviceID uuid.UUID,
	eventType string, severity model.EventSeverity, message string, details interface{}) error {

	var detailsJSON json.RawMessage
	if details != nil {
		data, err := json.Marshal(details)
		if err != nil {
			return fmt.Errorf("marshal event details: %w", err)
		}
		detailsJSON = data
	}

	event := &model.DeviceEvent{
		TenantID:  tenantID,
		DeviceID:  deviceID,
		EventType: eventType,
		Severity:  severity,
		Message:   message,
		Details:   detailsJSON,
	}

	return s.Create(ctx, event)
}

func (s *EventStore) List(ctx context.Context, params model.EventListParams) ([]*model.DeviceEvent, int, error) {
	var conditions []string
	var args []interface{}
	argIdx := 1

	conditions = append(conditions, fmt.Sprintf("tenant_id = $%d", argIdx))
	args = append(args, params.TenantID)
	argIdx++

	if params.DeviceID != nil {
		conditions = append(conditions, fmt.Sprintf("device_id = $%d", argIdx))
		args = append(args, *params.DeviceID)
		argIdx++
	}
	if params.EventType != "" {
		conditions = append(conditions, fmt.Sprintf("event_type = $%d", argIdx))
		args = append(args, params.EventType)
		argIdx++
	}
	if params.Severity != nil {
		conditions = append(conditions, fmt.Sprintf("severity = $%d", argIdx))
		args = append(args, *params.Severity)
		argIdx++
	}
	if params.Start != nil {
		conditions = append(conditions, fmt.Sprintf("timestamp >= $%d", argIdx))
		args = append(args, *params.Start)
		argIdx++
	}
	if params.End != nil {
		conditions = append(conditions, fmt.Sprintf("timestamp <= $%d", argIdx))
		args = append(args, *params.End)
		argIdx++
	}

	whereClause := "WHERE " + joinStrings(conditions, " AND ")

	var total int
	if err := s.pool.QueryRow(ctx,
		fmt.Sprintf("SELECT COUNT(*) FROM device_events %s", whereClause), args...,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count events: %w", err)
	}

	if params.Limit <= 0 {
		params.Limit = 50
	}
	if params.Limit > 200 {
		params.Limit = 200
	}

	query := fmt.Sprintf(`
		SELECT id, tenant_id, device_id, event_type, severity, message, details, timestamp
		FROM device_events %s
		ORDER BY timestamp DESC
		LIMIT $%d OFFSET $%d`,
		whereClause, argIdx, argIdx+1)
	args = append(args, params.Limit, params.Offset)

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("query events: %w", err)
	}
	defer rows.Close()

	var events []*model.DeviceEvent
	for rows.Next() {
		var e model.DeviceEvent
		if err := rows.Scan(
			&e.ID, &e.TenantID, &e.DeviceID, &e.EventType,
			&e.Severity, &e.Message, &e.Details, &e.Timestamp,
		); err != nil {
			return nil, 0, fmt.Errorf("scan event: %w", err)
		}
		events = append(events, &e)
	}
	return events, total, rows.Err()
}
