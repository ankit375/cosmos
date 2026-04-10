package postgres

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yourorg/cloudctrl/internal/model"
)

type DeviceStore struct {
	pool pooler
}

func NewDeviceStore(pool pooler) *DeviceStore {
	return &DeviceStore{pool: pool}
}

func (s *DeviceStore) Create(ctx context.Context, d *model.Device) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO devices (
			id, tenant_id, site_id, mac, serial, name, model, status,
			firmware_version, ip_address, capabilities, system_info, tags, notes,
			device_token_hash
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8,
			$9, $10, $11, $12, $13, $14, $15
		)`,
		d.ID, d.TenantID, d.SiteID, d.MAC, d.Serial, d.Name, d.Model, d.Status,
		d.FirmwareVersion, d.IPAddress, d.Capabilities, d.SystemInfo, d.Tags, d.Notes,
		d.DeviceTokenHash,
	)
	if err != nil {
		return fmt.Errorf("insert device: %w", err)
	}
	return nil
}

const deviceSelectColumns = `id, tenant_id, site_id, mac, serial, name, model, status,
	firmware_version, target_firmware, ip_address::text, public_ip::text,
	desired_config_version, applied_config_version,
	device_token_hash, uptime, last_seen, adopted_at, last_config_applied,
	capabilities, system_info, tags, notes,
	created_at, updated_at`

func (s *DeviceStore) GetByID(ctx context.Context, tenantID, deviceID uuid.UUID) (*model.Device, error) {
	return s.scanDevice(s.pool.QueryRow(ctx,
		`SELECT `+deviceSelectColumns+`
		FROM devices WHERE id = $1 AND tenant_id = $2`, deviceID, tenantID))
}

func (s *DeviceStore) GetByMAC(ctx context.Context, mac string) (*model.Device, error) {
	return s.scanDevice(s.pool.QueryRow(ctx,
		`SELECT `+deviceSelectColumns+`
		FROM devices WHERE mac = $1`, mac))
}

func (s *DeviceStore) GetByTokenHash(ctx context.Context, tokenHash string) (*model.Device, error) {
	return s.scanDevice(s.pool.QueryRow(ctx,
		`SELECT `+deviceSelectColumns+`
		FROM devices WHERE device_token_hash = $1`, tokenHash))
}

func (s *DeviceStore) scanDevice(row pgx.Row) (*model.Device, error) {
	var d model.Device
	err := row.Scan(
		&d.ID, &d.TenantID, &d.SiteID, &d.MAC, &d.Serial, &d.Name, &d.Model, &d.Status,
		&d.FirmwareVersion, &d.TargetFirmware, &d.IPAddress, &d.PublicIP,
		&d.DesiredConfigVersion, &d.AppliedConfigVersion,
		&d.DeviceTokenHash, &d.Uptime, &d.LastSeen, &d.AdoptedAt, &d.LastConfigApplied,
		&d.Capabilities, &d.SystemInfo, &d.Tags, &d.Notes,
		&d.CreatedAt, &d.UpdatedAt,
	)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan device: %w", err)
	}
	return &d, nil
}

func (s *DeviceStore) List(ctx context.Context, params model.DeviceListParams) ([]*model.Device, int, error) {
	var conditions []string
	var args []interface{}
	argIdx := 1

	// Always scope by tenant
	conditions = append(conditions, fmt.Sprintf("tenant_id = $%d", argIdx))
	args = append(args, params.TenantID)
	argIdx++

	// Exclude decommissioned by default
	conditions = append(conditions, "status != 'decommissioned'")

	if params.SiteID != nil {
		conditions = append(conditions, fmt.Sprintf("site_id = $%d", argIdx))
		args = append(args, *params.SiteID)
		argIdx++
	}
	if params.Status != nil {
		conditions = append(conditions, fmt.Sprintf("status = $%d", argIdx))
		args = append(args, *params.Status)
		argIdx++
	}
	if params.Model != "" {
		conditions = append(conditions, fmt.Sprintf("model = $%d", argIdx))
		args = append(args, params.Model)
		argIdx++
	}
	if params.Search != "" {
		conditions = append(conditions, fmt.Sprintf(
			"(name ILIKE $%d OR mac ILIKE $%d OR serial ILIKE $%d)",
			argIdx, argIdx, argIdx))
		args = append(args, "%"+params.Search+"%")
		argIdx++
	}

	whereClause := "WHERE " + strings.Join(conditions, " AND ")

	// Count
	var total int
	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM devices %s", whereClause)
	if err := s.pool.QueryRow(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count devices: %w", err)
	}

	// Order
	orderBy := "created_at"
	allowed := map[string]bool{"name": true, "status": true, "last_seen": true, "created_at": true, "model": true}
	if allowed[params.OrderBy] {
		orderBy = params.OrderBy
	}
	orderDir := "DESC"
	if params.OrderDir == "asc" {
		orderDir = "ASC"
	}

	// Pagination
	if params.Limit <= 0 {
		params.Limit = 50
	}
	if params.Limit > 200 {
		params.Limit = 200
	}

	query := fmt.Sprintf(`
		SELECT `+deviceSelectColumns+`
		FROM devices %s
		ORDER BY %s %s
		LIMIT $%d OFFSET $%d`,
		whereClause, orderBy, orderDir, argIdx, argIdx+1)
	args = append(args, params.Limit, params.Offset)

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("query devices: %w", err)
	}
	defer rows.Close()

	var devices []*model.Device
	for rows.Next() {
		var d model.Device
		if err := rows.Scan(
			&d.ID, &d.TenantID, &d.SiteID, &d.MAC, &d.Serial, &d.Name, &d.Model, &d.Status,
			&d.FirmwareVersion, &d.TargetFirmware, &d.IPAddress, &d.PublicIP,
			&d.DesiredConfigVersion, &d.AppliedConfigVersion,
			&d.DeviceTokenHash, &d.Uptime, &d.LastSeen, &d.AdoptedAt, &d.LastConfigApplied,
			&d.Capabilities, &d.SystemInfo, &d.Tags, &d.Notes,
			&d.CreatedAt, &d.UpdatedAt,
		); err != nil {
			return nil, 0, fmt.Errorf("scan device row: %w", err)
		}
		devices = append(devices, &d)
	}

	return devices, total, rows.Err()
}

func (s *DeviceStore) UpdateStatus(ctx context.Context, tenantID, deviceID uuid.UUID, status model.DeviceStatus) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE devices SET status = $1 WHERE id = $2 AND tenant_id = $3`,
		status, deviceID, tenantID)
	return err
}

func (s *DeviceStore) UpdateHeartbeat(ctx context.Context, deviceID uuid.UUID, ip string, uptime int64, appliedVersion int64) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE devices SET 
			last_seen = NOW(),
			ip_address = $1,
			uptime = $2,
			applied_config_version = $3,
			status = 'online'
		WHERE id = $4`,
		ip, uptime, appliedVersion, deviceID)
	return err
}

func (s *DeviceStore) UpdateConfigVersion(ctx context.Context, tenantID, deviceID uuid.UUID, version int64) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE devices SET desired_config_version = $1 WHERE id = $2 AND tenant_id = $3`,
		version, deviceID, tenantID)
	return err
}

func (s *DeviceStore) SetAdopted(ctx context.Context, deviceID uuid.UUID, siteID uuid.UUID, tokenHash string, name string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE devices SET
			status = 'provisioning',
			site_id = $1,
			device_token_hash = $2,
			name = $3,
			adopted_at = NOW()
		WHERE id = $4`,
		siteID, tokenHash, name, deviceID)
	return err
}

func (s *DeviceStore) Update(ctx context.Context, tenantID, deviceID uuid.UUID, input *model.UpdateDeviceInput) error {
	setClauses := []string{}
	args := []interface{}{}
	argIdx := 1

	if input.Name != nil {
		setClauses = append(setClauses, fmt.Sprintf("name = $%d", argIdx))
		args = append(args, *input.Name)
		argIdx++
	}
	if input.Notes != nil {
		setClauses = append(setClauses, fmt.Sprintf("notes = $%d", argIdx))
		args = append(args, *input.Notes)
		argIdx++
	}
	if input.Tags != nil {
		setClauses = append(setClauses, fmt.Sprintf("tags = $%d", argIdx))
		args = append(args, *input.Tags)
		argIdx++
	}

	if len(setClauses) == 0 {
		return nil
	}

	query := fmt.Sprintf("UPDATE devices SET %s WHERE id = $%d AND tenant_id = $%d",
		joinStrings(setClauses, ", "), argIdx, argIdx+1)
	args = append(args, deviceID, tenantID)

	result, err := s.pool.Exec(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("update device: %w", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("device not found")
	}
	return nil
}

func (s *DeviceStore) Delete(ctx context.Context, tenantID, deviceID uuid.UUID) error {
	// Soft delete — set status to decommissioned
	result, err := s.pool.Exec(ctx, `
		UPDATE devices SET status = 'decommissioned', device_token_hash = NULL
		WHERE id = $1 AND tenant_id = $2`,
		deviceID, tenantID)
	if err != nil {
		return fmt.Errorf("decommission device: %w", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("device not found")
	}
	return nil
}

func (s *DeviceStore) GetStats(ctx context.Context, tenantID uuid.UUID, siteID *uuid.UUID) (*model.DeviceStats, error) {
	var stats model.DeviceStats
	var query string
	var args []interface{}

	if siteID != nil {
		query = `
			SELECT
				COUNT(*) FILTER (WHERE status != 'decommissioned') as total,
				COUNT(*) FILTER (WHERE status = 'online') as online,
				COUNT(*) FILTER (WHERE status = 'offline') as offline,
				COUNT(*) FILTER (WHERE status = 'pending_adopt') as pending,
				COUNT(*) FILTER (WHERE status = 'upgrading') as upgrading
			FROM devices WHERE tenant_id = $1 AND site_id = $2`
		args = []interface{}{tenantID, *siteID}
	} else {
		query = `
			SELECT
				COUNT(*) FILTER (WHERE status != 'decommissioned') as total,
				COUNT(*) FILTER (WHERE status = 'online') as online,
				COUNT(*) FILTER (WHERE status = 'offline') as offline,
				COUNT(*) FILTER (WHERE status = 'pending_adopt') as pending,
				COUNT(*) FILTER (WHERE status = 'upgrading') as upgrading
			FROM devices WHERE tenant_id = $1`
		args = []interface{}{tenantID}
	}

	err := s.pool.QueryRow(ctx, query, args...).Scan(
		&stats.TotalDevices, &stats.OnlineCount, &stats.OfflineCount,
		&stats.PendingCount, &stats.UpgradingCount)
	if err != nil {
		return nil, fmt.Errorf("get device stats: %w", err)
	}

	return &stats, nil
}

func (s *DeviceStore) GetPendingAdopt(ctx context.Context, tenantID uuid.UUID) ([]*model.Device, error) {
	rows, err := s.pool.Query(ctx, 
		`SELECT `+deviceSelectColumns+`
		FROM devices
		WHERE tenant_id = $1 AND status = 'pending_adopt'
		ORDER BY created_at DESC`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("query pending devices: %w", err)
	}
	defer rows.Close()

	var devices []*model.Device
	for rows.Next() {
		var d model.Device
		if err := rows.Scan(
			&d.ID, &d.TenantID, &d.SiteID, &d.MAC, &d.Serial, &d.Name, &d.Model, &d.Status,
			&d.FirmwareVersion, &d.TargetFirmware, &d.IPAddress, &d.PublicIP,
			&d.DesiredConfigVersion, &d.AppliedConfigVersion,
			&d.DeviceTokenHash, &d.Uptime, &d.LastSeen, &d.AdoptedAt, &d.LastConfigApplied,
			&d.Capabilities, &d.SystemInfo, &d.Tags, &d.Notes,
			&d.CreatedAt, &d.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan pending device: %w", err)
		}
		devices = append(devices, &d)
	}
	return devices, rows.Err()
}

// BatchUpdateState bulk-updates device state fields (used by state persister worker).
func (s *DeviceStore) BatchUpdateState(ctx context.Context, updates []DeviceStateUpdate) error {
	if len(updates) == 0 {
		return nil
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	for _, u := range updates {
		_, err := tx.Exec(ctx,
			`UPDATE devices SET
				status = $1,
				last_seen = $2,
				ip_address = CASE WHEN $3::text = '' THEN NULL ELSE $3::inet END,
				uptime = $4,
				applied_config_version = $5,
				updated_at = NOW()
			WHERE id = $6`,
			u.Status, u.LastSeen, u.IPAddress, u.Uptime, u.AppliedConfigVersion, u.DeviceID)
		if err != nil {
			return fmt.Errorf("batch update device %s: %w", u.DeviceID, err)
		}
	}

	return tx.Commit(ctx)
}

// DeviceStateUpdate holds the fields updated by the state persistence worker.
type DeviceStateUpdate struct {
	DeviceID             uuid.UUID
	Status               model.DeviceStatus
	LastSeen             interface{} // time.Time or nil
	IPAddress            *string
	Uptime               int64
	AppliedConfigVersion int64
}

func (s *DeviceStore) CountByTenant(ctx context.Context, tenantID uuid.UUID) (int, error) {
	var count int
	err := s.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM devices
		WHERE tenant_id = $1 AND status != 'decommissioned'`,
		tenantID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count devices: %w", err)
	}
	return count, nil
}
