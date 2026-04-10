package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/yourorg/cloudctrl/internal/model"
)

type AuditStore struct {
	pool pooler
}

func NewAuditStore(pool pooler) *AuditStore {
	return &AuditStore{pool: pool}
}

func (s *AuditStore) Log(ctx context.Context, entry *model.AuditLogEntry) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO audit_log (id, tenant_id, user_id, action, resource_type, resource_id, details, ip_address)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		uuid.New(), entry.TenantID, entry.UserID, entry.Action,
		entry.ResourceType, entry.ResourceID, entry.Details, entry.IPAddress,
	)
	if err != nil {
		return fmt.Errorf("insert audit log: %w", err)
	}
	return nil
}

// LogAction is a convenience method for common audit logging.
func (s *AuditStore) LogAction(ctx context.Context, tenantID uuid.UUID, userID *uuid.UUID,
	action, resourceType string, resourceID *uuid.UUID, details interface{}, ipAddress *string) error {

	var detailsJSON json.RawMessage
	if details != nil {
		data, err := json.Marshal(details)
		if err != nil {
			return fmt.Errorf("marshal audit details: %w", err)
		}
		detailsJSON = data
	}

	entry := &model.AuditLogEntry{
		TenantID:     tenantID,
		UserID:       userID,
		Action:       action,
		ResourceType: resourceType,
		ResourceID:   resourceID,
		Details:      detailsJSON,
		IPAddress:    ipAddress,
	}

	return s.Log(ctx, entry)
}

func (s *AuditStore) List(ctx context.Context, params model.AuditListParams) ([]*model.AuditLogEntry, int, error) {
	var conditions []string
	var args []interface{}
	argIdx := 1

	conditions = append(conditions, fmt.Sprintf("tenant_id = $%d", argIdx))
	args = append(args, params.TenantID)
	argIdx++

	if params.UserID != nil {
		conditions = append(conditions, fmt.Sprintf("user_id = $%d", argIdx))
		args = append(args, *params.UserID)
		argIdx++
	}
	if params.Action != "" {
		conditions = append(conditions, fmt.Sprintf("action = $%d", argIdx))
		args = append(args, params.Action)
		argIdx++
	}
	if params.ResourceType != "" {
		conditions = append(conditions, fmt.Sprintf("resource_type = $%d", argIdx))
		args = append(args, params.ResourceType)
		argIdx++
	}
	if params.ResourceID != nil {
		conditions = append(conditions, fmt.Sprintf("resource_id = $%d", argIdx))
		args = append(args, *params.ResourceID)
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

	// Count
	var total int
	if err := s.pool.QueryRow(ctx,
		fmt.Sprintf("SELECT COUNT(*) FROM audit_log %s", whereClause), args...,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count audit log: %w", err)
	}

	// Pagination defaults
	if params.Limit <= 0 {
		params.Limit = 50
	}
	if params.Limit > 200 {
		params.Limit = 200
	}

	query := fmt.Sprintf(`
		SELECT id, tenant_id, user_id, action, resource_type, resource_id,
		       details, ip_address, timestamp
		FROM audit_log %s
		ORDER BY timestamp DESC
		LIMIT $%d OFFSET $%d`,
		whereClause, argIdx, argIdx+1)
	args = append(args, params.Limit, params.Offset)

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("query audit log: %w", err)
	}
	defer rows.Close()

	var entries []*model.AuditLogEntry
	for rows.Next() {
		var e model.AuditLogEntry
		if err := rows.Scan(
			&e.ID, &e.TenantID, &e.UserID, &e.Action, &e.ResourceType,
			&e.ResourceID, &e.Details, &e.IPAddress, &e.Timestamp,
		); err != nil {
			return nil, 0, fmt.Errorf("scan audit entry: %w", err)
		}
		entries = append(entries, &e)
	}
	return entries, total, rows.Err()
}
