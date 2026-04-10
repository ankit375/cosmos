-- ============================================================
-- Rollback Migration 000005
-- ============================================================

-- Drop daily aggregates
DROP MATERIALIZED VIEW IF EXISTS radio_metrics_daily CASCADE;
DROP MATERIALIZED VIEW IF EXISTS device_metrics_daily CASCADE;

-- Drop additional indexes
DROP INDEX IF EXISTS idx_device_metrics_tenant_device;
DROP INDEX IF EXISTS idx_radio_metrics_tenant_device_band;
DROP INDEX IF EXISTS idx_client_sessions_site;
DROP INDEX IF EXISTS idx_client_sessions_ssid;
DROP INDEX IF EXISTS idx_client_sessions_tenant;
