package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/yourorg/cloudctrl/internal/config"
	"go.uber.org/zap"
)

// Store holds the PostgreSQL connection pool and all sub-stores.
type Store struct {
	Pool    *pgxpool.Pool
	logger  *zap.Logger

	// Sub-stores
	Tenants *TenantStore
	Users   *UserStore
	Sites   *SiteStore
	Devices *DeviceStore
	Events  *EventStore
	Audit   *AuditStore
	Configs *ConfigStore  // ← ADD THIS
	Commands *CommandStore // ← ADD THIS
}

// New creates a new PostgreSQL store with a configured connection pool.
func New(ctx context.Context, cfg config.DatabaseConfig, logger *zap.Logger) (*Store, error) {
	poolConfig, err := pgxpool.ParseConfig(cfg.DSN())
	if err != nil {
		return nil, fmt.Errorf("parse database DSN: %w", err)
	}

	poolConfig.MaxConns = int32(cfg.MaxOpenConns)
	poolConfig.MinConns = int32(cfg.MaxIdleConns)
	poolConfig.MaxConnLifetime = cfg.ConnMaxLifetime
	poolConfig.MaxConnIdleTime = 30 * time.Minute
	poolConfig.HealthCheckPeriod = 30 * time.Second

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("create connection pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	var version string
	if err := pool.QueryRow(ctx, "SELECT version()").Scan(&version); err == nil {
		logger.Info("connected to database", zap.String("version", version))
	}

	var tsVersion string
	err = pool.QueryRow(ctx,
		"SELECT extversion FROM pg_extension WHERE extname = 'timescaledb'").Scan(&tsVersion)
	if err != nil {
		logger.Warn("timescaledb extension not found")
	} else {
		logger.Info("timescaledb available", zap.String("version", tsVersion))
	}

	s := &Store{
		Pool:   pool,
		logger: logger,
	}

	// Initialize sub-stores
	s.Tenants = NewTenantStore(pool)
	s.Users = NewUserStore(pool)
	s.Sites = NewSiteStore(pool)
	s.Devices = NewDeviceStore(pool)
	s.Events = NewEventStore(pool)
	s.Audit = NewAuditStore(pool)
	s.Configs = NewConfigStore(pool)
	s.Commands = NewCommandStore(pool)

	return s, nil
}

// Close closes the database connection pool.
func (s *Store) Close() {
	s.Pool.Close()
	s.logger.Info("database connection pool closed")
}

// Health checks the database connectivity.
func (s *Store) Health(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return s.Pool.Ping(ctx)
}

// Stats returns the current pool statistics.
func (s *Store) Stats() *pgxpool.Stat {
	return s.Pool.Stat()
}
