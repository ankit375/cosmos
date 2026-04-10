-- ============================================================
-- Migration 000005: Telemetry Enhancements (Phase 7)
--
-- Adds:
--   - Daily continuous aggregates (device + radio)
--   - Retention policies for aggregates
--   - Additional indexes for query performance
--   - Site-level metrics aggregation view
-- ============================================================

-- ============================================================
-- DAILY DEVICE METRICS AGGREGATE
-- ============================================================
DO

$$
BEGIN
    -- Daily device metrics (aggregated from hourly)
    EXECUTE $agg$
        CREATE MATERIALIZED VIEW IF NOT EXISTS device_metrics_daily
        WITH (timescaledb.continuous) AS
        SELECT
            time_bucket('1 day', time) AS bucket,
            device_id,
            tenant_id,
            AVG(cpu_usage)                                           AS avg_cpu,
            MAX(cpu_usage)                                           AS max_cpu,
            MIN(cpu_usage)                                           AS min_cpu,
            AVG(memory_used::float / NULLIF(memory_total, 0) * 100) AS avg_mem_pct,
            MAX(client_count)                                        AS max_clients,
            AVG(client_count)                                        AS avg_clients,
            AVG(load_avg_1)                                          AS avg_load_1,
            MAX(load_avg_1)                                          AS max_load_1
        FROM device_metrics
        GROUP BY bucket, device_id, tenant_id
        WITH NO DATA
    $agg$;

    PERFORM add_continuous_aggregate_policy('device_metrics_daily',
        start_offset    => INTERVAL '3 days',
        end_offset      => INTERVAL '1 day',
        schedule_interval => INTERVAL '1 day',
        if_not_exists   => TRUE
    );

    -- Daily radio metrics
    EXECUTE $agg$
        CREATE MATERIALIZED VIEW IF NOT EXISTS radio_metrics_daily
        WITH (timescaledb.continuous) AS
        SELECT
            time_bucket('1 day', time) AS bucket,
            device_id,
            tenant_id,
            band,
            AVG(utilization)   AS avg_utilization,
            MAX(utilization)   AS max_utilization,
            MAX(client_count)  AS max_clients,
            SUM(tx_bytes)      AS total_tx_bytes,
            SUM(rx_bytes)      AS total_rx_bytes,
            SUM(tx_retries)    AS total_retries,
            AVG(noise_floor)   AS avg_noise_floor,
            SUM(tx_errors)     AS total_tx_errors,
            SUM(rx_errors)     AS total_rx_errors
        FROM radio_metrics
        GROUP BY bucket, device_id, tenant_id, band
        WITH NO DATA
    $agg$;

    PERFORM add_continuous_aggregate_policy('radio_metrics_daily',
        start_offset    => INTERVAL '3 days',
        end_offset      => INTERVAL '1 day',
        schedule_interval => INTERVAL '1 day',
        if_not_exists   => TRUE
    );

EXCEPTION
    WHEN OTHERS THEN
        RAISE NOTICE 'Could not create daily aggregates (TimescaleDB required): %', SQLERRM;
END
$$
;

-- ============================================================
-- RETENTION for aggregates
-- ============================================================
DO

$$
BEGIN
    -- Hourly aggregates: keep 90 days
    PERFORM add_retention_policy('device_metrics_hourly', INTERVAL '90 days', if_not_exists => TRUE);
    PERFORM add_retention_policy('radio_metrics_hourly', INTERVAL '90 days', if_not_exists => TRUE);

    -- Daily aggregates: keep 365 days
    PERFORM add_retention_policy('device_metrics_daily', INTERVAL '365 days', if_not_exists => TRUE);
    PERFORM add_retention_policy('radio_metrics_daily', INTERVAL '365 days', if_not_exists => TRUE);

EXCEPTION
    WHEN OTHERS THEN
        RAISE NOTICE 'Could not set aggregate retention (TimescaleDB required): %', SQLERRM;
END
$$
;

-- ============================================================
-- Additional indexes for query performance
-- ============================================================

-- Composite index for site-level aggregation queries
CREATE INDEX IF NOT EXISTS idx_device_metrics_tenant_device
    ON device_metrics(tenant_id, device_id, time DESC);

CREATE INDEX IF NOT EXISTS idx_radio_metrics_tenant_device_band
    ON radio_metrics(tenant_id, device_id, band, time DESC);

-- Client session: site-level queries
CREATE INDEX IF NOT EXISTS idx_client_sessions_site
    ON client_sessions(site_id, connected_at DESC)
    WHERE site_id IS NOT NULL;

-- Client session: SSID-based queries
CREATE INDEX IF NOT EXISTS idx_client_sessions_ssid
    ON client_sessions(ssid, connected_at DESC);

-- Client session: tenant-level queries
CREATE INDEX IF NOT EXISTS idx_client_sessions_tenant
    ON client_sessions(tenant_id, connected_at DESC);
