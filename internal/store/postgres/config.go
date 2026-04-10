package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yourorg/cloudctrl/internal/model"
)

// ConfigStore handles all config-related database operations.
type ConfigStore struct {
	pool pooler
}

func NewConfigStore(pool pooler) *ConfigStore {
	return &ConfigStore{pool: pool}
}

// ============================================================
// CONFIG TEMPLATES
// ============================================================

const configTemplateSelectColumns = `id, tenant_id, site_id, version, config, description, created_by, created_at`

func (s *ConfigStore) scanConfigTemplate(row pgx.Row) (*model.ConfigTemplate, error) {
	var ct model.ConfigTemplate
	err := row.Scan(
		&ct.ID, &ct.TenantID, &ct.SiteID, &ct.Version,
		&ct.Config, &ct.Description, &ct.CreatedBy, &ct.CreatedAt,
	)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan config template: %w", err)
	}
	return &ct, nil
}

// GetLatestTemplate returns the latest config template for a site.
func (s *ConfigStore) GetLatestTemplate(ctx context.Context, tenantID, siteID uuid.UUID) (*model.ConfigTemplate, error) {
	return s.scanConfigTemplate(s.pool.QueryRow(ctx,
		`SELECT `+configTemplateSelectColumns+`
		FROM config_templates
		WHERE tenant_id = $1 AND site_id = $2
		ORDER BY version DESC LIMIT 1`,
		tenantID, siteID))
}

// GetTemplateByVersion returns a specific version of a config template.
func (s *ConfigStore) GetTemplateByVersion(ctx context.Context, tenantID, siteID uuid.UUID, version int64) (*model.ConfigTemplate, error) {
	return s.scanConfigTemplate(s.pool.QueryRow(ctx,
		`SELECT `+configTemplateSelectColumns+`
		FROM config_templates
		WHERE tenant_id = $1 AND site_id = $2 AND version = $3`,
		tenantID, siteID, version))
}

// ListTemplateHistory returns the version history for a site config template.
func (s *ConfigStore) ListTemplateHistory(ctx context.Context, tenantID, siteID uuid.UUID, limit, offset int) ([]*model.ConfigTemplate, int, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	var total int
	err := s.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM config_templates WHERE tenant_id = $1 AND site_id = $2`,
		tenantID, siteID).Scan(&total)
	if err != nil {
		return nil, 0, fmt.Errorf("count config templates: %w", err)
	}

	rows, err := s.pool.Query(ctx,
		`SELECT `+configTemplateSelectColumns+`
		FROM config_templates
		WHERE tenant_id = $1 AND site_id = $2
		ORDER BY version DESC
		LIMIT $3 OFFSET $4`,
		tenantID, siteID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("query config templates: %w", err)
	}
	defer rows.Close()

	var templates []*model.ConfigTemplate
	for rows.Next() {
		var ct model.ConfigTemplate
		if err := rows.Scan(
			&ct.ID, &ct.TenantID, &ct.SiteID, &ct.Version,
			&ct.Config, &ct.Description, &ct.CreatedBy, &ct.CreatedAt,
		); err != nil {
			return nil, 0, fmt.Errorf("scan config template row: %w", err)
		}
		templates = append(templates, &ct)
	}

	return templates, total, rows.Err()
}

// CreateTemplate creates a new config template version. Returns the created template.
// Auto-increments the version number.
func (s *ConfigStore) CreateTemplate(ctx context.Context, tenantID, siteID uuid.UUID, config json.RawMessage, description string, createdBy *uuid.UUID) (*model.ConfigTemplate, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	// Get next version number
	var nextVersion int64
	err = tx.QueryRow(ctx,
		`SELECT COALESCE(MAX(version), 0) + 1 FROM config_templates
		WHERE site_id = $1`,
		siteID).Scan(&nextVersion)
	if err != nil {
		return nil, fmt.Errorf("get next version: %w", err)
	}

	ct := &model.ConfigTemplate{
		ID:          uuid.New(),
		TenantID:    tenantID,
		SiteID:      siteID,
		Version:     nextVersion,
		Config:      config,
		Description: description,
		CreatedBy:   createdBy,
	}

	err = tx.QueryRow(ctx,
		`INSERT INTO config_templates (id, tenant_id, site_id, version, config, description, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING created_at`,
		ct.ID, ct.TenantID, ct.SiteID, ct.Version, ct.Config, ct.Description, ct.CreatedBy,
	).Scan(&ct.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("insert config template: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}

	return ct, nil
}

// ============================================================
// DEVICE CONFIGS
// ============================================================

const deviceConfigSelectColumns = `id, tenant_id, device_id, version, config, source,
	template_version, device_overrides, status, error_message,
	created_by, pushed_at, applied_at, created_at`

func (s *ConfigStore) scanDeviceConfig(row pgx.Row) (*model.DeviceConfig, error) {
	var dc model.DeviceConfig
	err := row.Scan(
		&dc.ID, &dc.TenantID, &dc.DeviceID, &dc.Version, &dc.Config, &dc.Source,
		&dc.TemplateVersion, &dc.DeviceOverrides, &dc.Status, &dc.ErrorMessage,
		&dc.CreatedBy, &dc.PushedAt, &dc.AppliedAt, &dc.CreatedAt,
	)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan device config: %w", err)
	}
	return &dc, nil
}

// GetLatestDeviceConfig returns the latest config for a device.
func (s *ConfigStore) GetLatestDeviceConfig(ctx context.Context, tenantID, deviceID uuid.UUID) (*model.DeviceConfig, error) {
	return s.scanDeviceConfig(s.pool.QueryRow(ctx,
		`SELECT `+deviceConfigSelectColumns+`
		FROM device_configs
		WHERE tenant_id = $1 AND device_id = $2
		ORDER BY version DESC LIMIT 1`,
		tenantID, deviceID))
}

// GetDeviceConfigByVersion returns a specific version of a device config.
func (s *ConfigStore) GetDeviceConfigByVersion(ctx context.Context, tenantID, deviceID uuid.UUID, version int64) (*model.DeviceConfig, error) {
	return s.scanDeviceConfig(s.pool.QueryRow(ctx,
		`SELECT `+deviceConfigSelectColumns+`
		FROM device_configs
		WHERE tenant_id = $1 AND device_id = $2 AND version = $3`,
		tenantID, deviceID, version))
}

// ListDeviceConfigHistory returns the config history for a device.
func (s *ConfigStore) ListDeviceConfigHistory(ctx context.Context, tenantID, deviceID uuid.UUID, limit, offset int) ([]*model.DeviceConfig, int, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	var total int
	err := s.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM device_configs WHERE tenant_id = $1 AND device_id = $2`,
		tenantID, deviceID).Scan(&total)
	if err != nil {
		return nil, 0, fmt.Errorf("count device configs: %w", err)
	}

	rows, err := s.pool.Query(ctx,
		`SELECT `+deviceConfigSelectColumns+`
		FROM device_configs
		WHERE tenant_id = $1 AND device_id = $2
		ORDER BY version DESC
		LIMIT $3 OFFSET $4`,
		tenantID, deviceID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("query device configs: %w", err)
	}
	defer rows.Close()

	var configs []*model.DeviceConfig
	for rows.Next() {
		var dc model.DeviceConfig
		if err := rows.Scan(
			&dc.ID, &dc.TenantID, &dc.DeviceID, &dc.Version, &dc.Config, &dc.Source,
			&dc.TemplateVersion, &dc.DeviceOverrides, &dc.Status, &dc.ErrorMessage,
			&dc.CreatedBy, &dc.PushedAt, &dc.AppliedAt, &dc.CreatedAt,
		); err != nil {
			return nil, 0, fmt.Errorf("scan device config row: %w", err)
		}
		configs = append(configs, &dc)
	}

	return configs, total, rows.Err()
}

// CreateDeviceConfig creates a new device config version.
func (s *ConfigStore) CreateDeviceConfig(ctx context.Context, dc *model.DeviceConfig) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	// Get next version
	var nextVersion int64
	err = tx.QueryRow(ctx,
		`SELECT COALESCE(MAX(version), 0) + 1 FROM device_configs WHERE device_id = $1`,
		dc.DeviceID).Scan(&nextVersion)
	if err != nil {
		return fmt.Errorf("get next device config version: %w", err)
	}

	dc.ID = uuid.New()
	dc.Version = nextVersion

	_, err = tx.Exec(ctx,
		`INSERT INTO device_configs (
			id, tenant_id, device_id, version, config, source,
			template_version, device_overrides, status, created_by
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		dc.ID, dc.TenantID, dc.DeviceID, dc.Version, dc.Config, dc.Source,
		dc.TemplateVersion, dc.DeviceOverrides, dc.Status, dc.CreatedBy,
	)
	if err != nil {
		return fmt.Errorf("insert device config: %w", err)
	}

	// Update device desired_config_version
	_, err = tx.Exec(ctx,
		`UPDATE devices SET desired_config_version = $1, updated_at = NOW()
		WHERE id = $2 AND tenant_id = $3`,
		dc.Version, dc.DeviceID, dc.TenantID)
	if err != nil {
		return fmt.Errorf("update device desired version: %w", err)
	}

	return tx.Commit(ctx)
}

// UpdateDeviceConfigStatus updates the status of a device config entry.
func (s *ConfigStore) UpdateDeviceConfigStatus(ctx context.Context, tenantID, deviceID uuid.UUID, version int64, status model.ConfigStatus, errMsg string) error {
	now := time.Now()
	var pushedAt, appliedAt *time.Time

	switch status {
	case model.ConfigStatusPushed:
		pushedAt = &now
	case model.ConfigStatusApplied:
		appliedAt = &now
	}

	var errMsgPtr *string
	if errMsg != "" {
		errMsgPtr = &errMsg
	}

	_, err := s.pool.Exec(ctx,
		`UPDATE device_configs SET
			status = $1,
			error_message = $2,
			pushed_at = COALESCE($3, pushed_at),
			applied_at = COALESCE($4, applied_at)
		WHERE tenant_id = $5 AND device_id = $6 AND version = $7`,
		status, errMsgPtr, pushedAt, appliedAt, tenantID, deviceID, version)
	if err != nil {
		return fmt.Errorf("update device config status: %w", err)
	}

	// If applied, update device's applied_config_version and last_config_applied
	if status == model.ConfigStatusApplied {
		_, err = s.pool.Exec(ctx,
			`UPDATE devices SET
				applied_config_version = $1,
				last_config_applied = NOW(),
				updated_at = NOW()
			WHERE id = $2 AND tenant_id = $3`,
			version, deviceID, tenantID)
		if err != nil {
			return fmt.Errorf("update device applied version: %w", err)
		}
	}

	return nil
}

// GetDevicesWithConfigDrift returns devices where desired != applied config version.
func (s *ConfigStore) GetDevicesWithConfigDrift(ctx context.Context, tenantID uuid.UUID) ([]*model.Device, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+deviceSelectColumns+`
		FROM devices
		WHERE tenant_id = $1
		  AND status IN ('online', 'config_pending')
		  AND desired_config_version > applied_config_version
		  AND status != 'decommissioned'
		ORDER BY updated_at ASC`,
		tenantID)
	if err != nil {
		return nil, fmt.Errorf("query config drift devices: %w", err)
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
			return nil, fmt.Errorf("scan drift device: %w", err)
		}
		devices = append(devices, &d)
	}

	return devices, rows.Err()
}

// GetDevicesBySite returns all active devices for a site.
func (s *ConfigStore) GetDevicesBySite(ctx context.Context, tenantID, siteID uuid.UUID) ([]*model.Device, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+deviceSelectColumns+`
		FROM devices
		WHERE tenant_id = $1 AND site_id = $2
		  AND status NOT IN ('decommissioned', 'pending_adopt')
		ORDER BY name ASC`,
		tenantID, siteID)
	if err != nil {
		return nil, fmt.Errorf("query site devices: %w", err)
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
			return nil, fmt.Errorf("scan site device: %w", err)
		}
		devices = append(devices, &d)
	}

	return devices, rows.Err()
}

// ============================================================
// DEVICE OVERRIDES
// ============================================================

// GetDeviceOverrides returns the overrides for a device.
func (s *ConfigStore) GetDeviceOverrides(ctx context.Context, tenantID, deviceID uuid.UUID) (*model.DeviceOverride, error) {
	var do model.DeviceOverride
	err := s.pool.QueryRow(ctx,
		`SELECT device_id, tenant_id, overrides, updated_by, updated_at
		FROM device_overrides
		WHERE device_id = $1 AND tenant_id = $2`,
		deviceID, tenantID,
	).Scan(&do.DeviceID, &do.TenantID, &do.Overrides, &do.UpdatedBy, &do.UpdatedAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get device overrides: %w", err)
	}
	return &do, nil
}

// UpsertDeviceOverrides creates or updates device overrides.
func (s *ConfigStore) UpsertDeviceOverrides(ctx context.Context, tenantID, deviceID uuid.UUID, overrides json.RawMessage, updatedBy *uuid.UUID) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO device_overrides (device_id, tenant_id, overrides, updated_by, updated_at)
		VALUES ($1, $2, $3, $4, NOW())
		ON CONFLICT (device_id) DO UPDATE SET
			overrides = EXCLUDED.overrides,
			updated_by = EXCLUDED.updated_by,
			updated_at = NOW()`,
		deviceID, tenantID, overrides, updatedBy)
	if err != nil {
		return fmt.Errorf("upsert device overrides: %w", err)
	}
	return nil
}

// DeleteDeviceOverrides removes all overrides for a device.
func (s *ConfigStore) DeleteDeviceOverrides(ctx context.Context, tenantID, deviceID uuid.UUID) error {
	_, err := s.pool.Exec(ctx,
		`DELETE FROM device_overrides WHERE device_id = $1 AND tenant_id = $2`,
		deviceID, tenantID)
	if err != nil {
		return fmt.Errorf("delete device overrides: %w", err)
	}
	return nil
}
