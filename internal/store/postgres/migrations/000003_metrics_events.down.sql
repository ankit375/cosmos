-- ============================================================
-- Migration 000003: Rollback Metrics and Events
-- ============================================================

-- Drop continuous aggregates first
DO 
$$
BEGIN
    DROP MATERIALIZED VIEW IF EXISTS radio_metrics_hourly CASCADE;
    DROP MATERIALIZED VIEW IF EXISTS device_metrics_hourly CASCADE;
EXCEPTION
    WHEN OTHERS THEN NULL;
END
$$
;

DROP TABLE IF EXISTS audit_log CASCADE;
DROP TABLE IF EXISTS client_sessions CASCADE;
DROP TABLE IF EXISTS radio_metrics CASCADE;
DROP TABLE IF EXISTS device_metrics CASCADE;
DROP TABLE IF EXISTS device_events CASCADE;
