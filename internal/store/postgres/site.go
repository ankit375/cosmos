package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yourorg/cloudctrl/internal/model"
)

type SiteStore struct {
	pool pooler
}

func NewSiteStore(pool pooler) *SiteStore {
	return &SiteStore{pool: pool}
}

func (s *SiteStore) Create(ctx context.Context, site *model.Site) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO sites (id, tenant_id, name, description, address, timezone, country_code,
		                   latitude, longitude, auto_adopt, auto_upgrade, settings)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		site.ID, site.TenantID, site.Name, site.Description, site.Address,
		site.Timezone, site.CountryCode, site.Latitude, site.Longitude,
		site.AutoAdopt, site.AutoUpgrade, site.Settings,
	)
	if err != nil {
		return fmt.Errorf("insert site: %w", err)
	}
	return nil
}

func (s *SiteStore) GetByID(ctx context.Context, tenantID, siteID uuid.UUID) (*model.Site, error) {
	var site model.Site
	err := s.pool.QueryRow(ctx, `
		SELECT id, tenant_id, name, description, address, timezone, country_code,
		       latitude, longitude, auto_adopt, auto_upgrade, settings,
		       created_at, updated_at
		FROM sites WHERE id = $1 AND tenant_id = $2`, siteID, tenantID,
	).Scan(
		&site.ID, &site.TenantID, &site.Name, &site.Description, &site.Address,
		&site.Timezone, &site.CountryCode, &site.Latitude, &site.Longitude,
		&site.AutoAdopt, &site.AutoUpgrade, &site.Settings,
		&site.CreatedAt, &site.UpdatedAt,
	)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get site: %w", err)
	}
	return &site, nil
}

func (s *SiteStore) List(ctx context.Context, tenantID uuid.UUID) ([]*model.Site, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, tenant_id, name, description, address, timezone, country_code,
		       latitude, longitude, auto_adopt, auto_upgrade, settings,
		       created_at, updated_at
		FROM sites WHERE tenant_id = $1
		ORDER BY name ASC`, tenantID,
	)
	if err != nil {
		return nil, fmt.Errorf("list sites: %w", err)
	}
	defer rows.Close()

	var sites []*model.Site
	for rows.Next() {
		var site model.Site
		if err := rows.Scan(
			&site.ID, &site.TenantID, &site.Name, &site.Description, &site.Address,
			&site.Timezone, &site.CountryCode, &site.Latitude, &site.Longitude,
			&site.AutoAdopt, &site.AutoUpgrade, &site.Settings,
			&site.CreatedAt, &site.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan site: %w", err)
		}
		sites = append(sites, &site)
	}
	return sites, rows.Err()
}

func (s *SiteStore) Update(ctx context.Context, tenantID, siteID uuid.UUID, input *model.UpdateSiteInput) error {
	setClauses := []string{}
	args := []interface{}{}
	argIdx := 1

	if input.Name != nil {
		setClauses = append(setClauses, fmt.Sprintf("name = $%d", argIdx))
		args = append(args, *input.Name)
		argIdx++
	}
	if input.Description != nil {
		setClauses = append(setClauses, fmt.Sprintf("description = $%d", argIdx))
		args = append(args, *input.Description)
		argIdx++
	}
	if input.Address != nil {
		setClauses = append(setClauses, fmt.Sprintf("address = $%d", argIdx))
		args = append(args, *input.Address)
		argIdx++
	}
	if input.Timezone != nil {
		setClauses = append(setClauses, fmt.Sprintf("timezone = $%d", argIdx))
		args = append(args, *input.Timezone)
		argIdx++
	}
	if input.CountryCode != nil {
		setClauses = append(setClauses, fmt.Sprintf("country_code = $%d", argIdx))
		args = append(args, *input.CountryCode)
		argIdx++
	}
	if input.Latitude != nil {
		setClauses = append(setClauses, fmt.Sprintf("latitude = $%d", argIdx))
		args = append(args, *input.Latitude)
		argIdx++
	}
	if input.Longitude != nil {
		setClauses = append(setClauses, fmt.Sprintf("longitude = $%d", argIdx))
		args = append(args, *input.Longitude)
		argIdx++
	}
	if input.AutoAdopt != nil {
		setClauses = append(setClauses, fmt.Sprintf("auto_adopt = $%d", argIdx))
		args = append(args, *input.AutoAdopt)
		argIdx++
	}
	if input.AutoUpgrade != nil {
				setClauses = append(setClauses, fmt.Sprintf("auto_upgrade = $%d", argIdx))
		args = append(args, *input.AutoUpgrade)
		argIdx++
	}

	if len(setClauses) == 0 {
		return nil
	}

	query := fmt.Sprintf("UPDATE sites SET %s WHERE id = $%d AND tenant_id = $%d",
		joinStrings(setClauses, ", "), argIdx, argIdx+1)
	args = append(args, siteID, tenantID)

	result, err := s.pool.Exec(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("update site: %w", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("site not found")
	}
	return nil
}

func (s *SiteStore) Delete(ctx context.Context, tenantID, siteID uuid.UUID) error {
	result, err := s.pool.Exec(ctx, `
		DELETE FROM sites WHERE id = $1 AND tenant_id = $2`, siteID, tenantID)
	if err != nil {
		return fmt.Errorf("delete site: %w", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("site not found")
	}
	return nil
}

func (s *SiteStore) CountByTenant(ctx context.Context, tenantID uuid.UUID) (int, error) {
	var count int
	err := s.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM sites WHERE tenant_id = $1`, tenantID,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count sites: %w", err)
	}
	return count, nil
}

func (s *SiteStore) GetStats(ctx context.Context, tenantID, siteID uuid.UUID) (*model.SiteStats, error) {
	var stats model.SiteStats
	stats.SiteID = siteID

	err := s.pool.QueryRow(ctx, `
		SELECT 
			COUNT(*) as total,
			COUNT(*) FILTER (WHERE status = 'online') as online,
			COUNT(*) FILTER (WHERE status = 'offline') as offline
		FROM devices 
		WHERE tenant_id = $1 AND site_id = $2 AND status != 'decommissioned'`,
		tenantID, siteID,
	).Scan(&stats.TotalDevices, &stats.OnlineDevices, &stats.OfflineDevices)

	if err != nil {
		return nil, fmt.Errorf("get site stats: %w", err)
	}

	return &stats, nil
}
