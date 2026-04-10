package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yourorg/cloudctrl/internal/model"
)

type TenantStore struct {
	pool pooler
}

func NewTenantStore(pool pooler) *TenantStore {
	return &TenantStore{pool: pool}
}

func (s *TenantStore) Create(ctx context.Context, t *model.Tenant) error {
	query := `INSERT INTO tenants (id, name, slug, subscription, max_devices, max_sites, max_users, settings, active)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`
	_, err := s.pool.Exec(ctx, query,
		t.ID, t.Name, t.Slug, t.Subscription, t.MaxDevices, t.MaxSites, t.MaxUsers, t.Settings, t.Active,
	)
	if err != nil {
		return fmt.Errorf("insert tenant: %w", err)
	}
	return nil
}

func (s *TenantStore) GetByID(ctx context.Context, id uuid.UUID) (*model.Tenant, error) {
	var t model.Tenant
	err := s.pool.QueryRow(ctx, `
		SELECT id, name, slug, subscription, max_devices, max_sites, max_users, settings, active,
		       created_at, updated_at
		FROM tenants WHERE id = $1`, id,
	).Scan(
		&t.ID, &t.Name, &t.Slug, &t.Subscription, &t.MaxDevices, &t.MaxSites, &t.MaxUsers,
		&t.Settings, &t.Active, &t.CreatedAt, &t.UpdatedAt,
	)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get tenant: %w", err)
	}
	return &t, nil
}

func (s *TenantStore) GetBySlug(ctx context.Context, slug string) (*model.Tenant, error) {
	var t model.Tenant
	err := s.pool.QueryRow(ctx, `
		SELECT id, name, slug, subscription, max_devices, max_sites, max_users, settings, active,
		       created_at, updated_at
		FROM tenants WHERE slug = $1`, slug,
	).Scan(
		&t.ID, &t.Name, &t.Slug, &t.Subscription, &t.MaxDevices, &t.MaxSites, &t.MaxUsers,
		&t.Settings, &t.Active, &t.CreatedAt, &t.UpdatedAt,
	)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get tenant by slug: %w", err)
	}
	return &t, nil
}

func (s *TenantStore) List(ctx context.Context) ([]*model.Tenant, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, slug, subscription, max_devices, max_sites, max_users, settings, active,
		       created_at, updated_at
		FROM tenants ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list tenants: %w", err)
	}
	defer rows.Close()

	var tenants []*model.Tenant
	for rows.Next() {
		var t model.Tenant
		if err := rows.Scan(
			&t.ID, &t.Name, &t.Slug, &t.Subscription, &t.MaxDevices, &t.MaxSites, &t.MaxUsers,
			&t.Settings, &t.Active, &t.CreatedAt, &t.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan tenant: %w", err)
		}
		tenants = append(tenants, &t)
	}
	return tenants, rows.Err()
}

func (s *TenantStore) Update(ctx context.Context, id uuid.UUID, input *model.UpdateTenantInput) error {
	setClauses := []string{}
	args := []interface{}{}
	argIdx := 1

	if input.Name != nil {
		setClauses = append(setClauses, fmt.Sprintf("name = $%d", argIdx))
		args = append(args, *input.Name)
		argIdx++
	}
	if input.Subscription != nil {
		setClauses = append(setClauses, fmt.Sprintf("subscription = $%d", argIdx))
		args = append(args, *input.Subscription)
		argIdx++
	}
	if input.MaxDevices != nil {
		setClauses = append(setClauses, fmt.Sprintf("max_devices = $%d", argIdx))
		args = append(args, *input.MaxDevices)
		argIdx++
	}
	if input.MaxSites != nil {
		setClauses = append(setClauses, fmt.Sprintf("max_sites = $%d", argIdx))
		args = append(args, *input.MaxSites)
		argIdx++
	}
	if input.MaxUsers != nil {
		setClauses = append(setClauses, fmt.Sprintf("max_users = $%d", argIdx))
		args = append(args, *input.MaxUsers)
		argIdx++
	}
	if input.Active != nil {
		setClauses = append(setClauses, fmt.Sprintf("active = $%d", argIdx))
		args = append(args, *input.Active)
		argIdx++
	}

	if len(setClauses) == 0 {
		return nil
	}

	query := fmt.Sprintf("UPDATE tenants SET %s WHERE id = $%d",
		joinStrings(setClauses, ", "), argIdx)
	args = append(args, id)

	result, err := s.pool.Exec(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("update tenant: %w", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("tenant not found")
	}
	return nil
}

func (s *TenantStore) Delete(ctx context.Context, id uuid.UUID) error {
	result, err := s.pool.Exec(ctx, `DELETE FROM tenants WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete tenant: %w", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("tenant not found")
	}
	return nil
}

// GetLimits returns the current usage vs limits for a tenant.
func (s *TenantStore) GetLimits(ctx context.Context, tenantID uuid.UUID) (*model.TenantLimits, error) {
	var limits model.TenantLimits

	err := s.pool.QueryRow(ctx, `
		SELECT max_sites, max_devices, max_users
		FROM tenants WHERE id = $1`, tenantID,
	).Scan(&limits.MaxSites, &limits.MaxDevices, &limits.MaxUsers)
	if err == pgx.ErrNoRows {
		return nil, fmt.Errorf("tenant not found")
	}
	if err != nil {
		return nil, fmt.Errorf("get tenant limits: %w", err)
	}

	// Count current sites
	err = s.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM sites WHERE tenant_id = $1`, tenantID,
	).Scan(&limits.CurrentSites)
	if err != nil {
		return nil, fmt.Errorf("count sites: %w", err)
	}

	// Count current devices (non-decommissioned)
	err = s.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM devices WHERE tenant_id = $1 AND status != 'decommissioned'`, tenantID,
	).Scan(&limits.CurrentDevices)
	if err != nil {
		return nil, fmt.Errorf("count devices: %w", err)
	}

	// Count current active users
	err = s.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM users WHERE tenant_id = $1 AND active = true`, tenantID,
	).Scan(&limits.CurrentUsers)
	if err != nil {
		return nil, fmt.Errorf("count users: %w", err)
	}

	return &limits, nil
}

// CheckSiteLimit returns an error if tenant has reached max sites.
func (s *TenantStore) CheckSiteLimit(ctx context.Context, tenantID uuid.UUID) error {
	var maxSites, currentSites int
	err := s.pool.QueryRow(ctx, `
		SELECT t.max_sites, COUNT(s.id)
		FROM tenants t
		LEFT JOIN sites s ON s.tenant_id = t.id
		WHERE t.id = $1
		GROUP BY t.max_sites`, tenantID,
	).Scan(&maxSites, &currentSites)
	if err != nil {
		return fmt.Errorf("check site limit: %w", err)
	}
	if currentSites >= maxSites {
		return fmt.Errorf("tenant site limit reached (%d/%d)", currentSites, maxSites)
	}
	return nil
}

// CheckDeviceLimit returns an error if tenant has reached max devices.
func (s *TenantStore) CheckDeviceLimit(ctx context.Context, tenantID uuid.UUID) error {
	var maxDevices, currentDevices int
	err := s.pool.QueryRow(ctx, `
		SELECT t.max_devices, COUNT(d.id)
		FROM tenants t
		LEFT JOIN devices d ON d.tenant_id = t.id AND d.status != 'decommissioned'
		WHERE t.id = $1
		GROUP BY t.max_devices`, tenantID,
	).Scan(&maxDevices, &currentDevices)
	if err != nil {
		return fmt.Errorf("check device limit: %w", err)
	}
	if currentDevices >= maxDevices {
		return fmt.Errorf("tenant device limit reached (%d/%d)", currentDevices, maxDevices)
	}
	return nil
}

// CheckUserLimit returns an error if tenant has reached max users.
func (s *TenantStore) CheckUserLimit(ctx context.Context, tenantID uuid.UUID) error {
	var maxUsers, currentUsers int
	err := s.pool.QueryRow(ctx, `
		SELECT t.max_users, COUNT(u.id)
		FROM tenants t
		LEFT JOIN users u ON u.tenant_id = t.id AND u.active = true
		WHERE t.id = $1
		GROUP BY t.max_users`, tenantID,
	).Scan(&maxUsers, &currentUsers)
	if err != nil {
		return fmt.Errorf("check user limit: %w", err)
	}
	if currentUsers >= maxUsers {
		return fmt.Errorf("tenant user limit reached (%d/%d)", currentUsers, maxUsers)
	}
	return nil
}
