-- ============================================================
-- Migration 000003: Metrics and Events Tables (TimescaleDB)
-- ============================================================

-- ============================================================
-- DEVICE EVENTS
-- ============================================================
CREATE TABLE device_events (
    id              UUID         DEFAULT uuid_generate_v4(),
    tenant_id       UUID         NOT NULL,
    device_id       UUID         NOT NULL,
    event_type      VARCHAR(50)  NOT NULL,
    severity        VARCHAR(20)  NOT NULL DEFAULT 'info'
                    CHECK (severity IN ('debug', 'info', 'warning', 'error', 'critical')),
    message         TEXT         NOT NULL,
    details         JSONB,
    timestamp       TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

-- Convert to hypertable if TimescaleDB is available
DO 
$$
BEGIN
    PERFORM create_hypertable('device_events', 'timestamp',
        chunk_time_interval => INTERVAL '1 day',
        if_not_exists => TRUE
    );
EXCEPTION
    WHEN OTHERS THEN
        RAISE NOTICE 'Could not create hypertable for device_events: %', SQLERRM;
        -- Fallback: create regular index for time-based queries
        CREATE INDEX IF NOT EXISTS idx_device_events_time ON device_events(timestamp DESC);
END
$$
;

CREATE INDEX idx_device_events_device ON device_events(device_id, timestamp DESC);
CREATE INDEX idx_device_events_tenant ON device_events(tenant_id, timestamp DESC);
CREATE INDEX idx_device_events_type ON device_events(event_type, timestamp DESC);
CREATE INDEX idx_device_events_severity ON device_events(severity, timestamp DESC)
    WHERE severity IN ('error', 'critical');

-- ============================================================
-- DEVICE METRICS (system-level)
-- ============================================================
CREATE TABLE device_metrics (
    time            TIMESTAMPTZ  NOT NULL,
    device_id       UUID         NOT NULL,
    tenant_id       UUID         NOT NULL,
    cpu_usage       REAL,
    memory_used     BIGINT,
    memory_total    BIGINT,
    load_avg_1      REAL,
    load_avg_5      REAL,
    load_avg_15     REAL,
    uptime          BIGINT,
    client_count    SMALLINT,
    temperature     REAL
);

DO 
$$
BEGIN
    PERFORM create_hypertable('device_metrics', 'time',
        chunk_time_interval => INTERVAL '1 day',
        if_not_exists => TRUE
    );
EXCEPTION
    WHEN OTHERS THEN
        RAISE NOTICE 'Could not create hypertable for device_metrics: %', SQLERRM;
        CREATE INDEX IF NOT EXISTS idx_device_metrics_time ON device_metrics(time DESC);
END
$$
;

CREATE INDEX idx_device_metrics_device ON device_metrics(device_id, time DESC);
CREATE INDEX idx_device_metrics_tenant ON device_metrics(tenant_id, time DESC);

-- ============================================================
-- RADIO METRICS (per-radio)
-- ============================================================
CREATE TABLE radio_metrics (
    time            TIMESTAMPTZ  NOT NULL,
    device_id       UUID         NOT NULL,
    tenant_id       UUID         NOT NULL,
    band            VARCHAR(10)  NOT NULL,
    channel         SMALLINT,
    channel_width   SMALLINT,
    tx_power        SMALLINT,
    noise_floor     SMALLINT,
    utilization     REAL,
    client_count    SMALLINT,
    tx_bytes        BIGINT,
    rx_bytes        BIGINT,
    tx_packets      BIGINT,
    rx_packets      BIGINT,
    tx_errors       BIGINT,
    rx_errors       BIGINT,
    tx_retries      BIGINT
);

DO 
$$
BEGIN
    PERFORM create_hypertable('radio_metrics', 'time',
        chunk_time_interval => INTERVAL '1 day',
        if_not_exists => TRUE
    );
EXCEPTION
    WHEN OTHERS THEN
        RAISE NOTICE 'Could not create hypertable for radio_metrics: %', SQLERRM;
        CREATE INDEX IF NOT EXISTS idx_radio_metrics_time ON radio_metrics(time DESC);
END
$$
;

CREATE INDEX idx_radio_metrics_device ON radio_metrics(device_id, band, time DESC);
CREATE INDEX idx_radio_metrics_tenant ON radio_metrics(tenant_id, time DESC);

-- ============================================================
-- CLIENT SESSIONS
-- ============================================================
CREATE TABLE client_sessions (
    id                UUID DEFAULT uuid_generate_v4(),
    tenant_id         UUID         NOT NULL,
    device_id         UUID         NOT NULL,
    site_id           UUID,
    client_mac        MACADDR      NOT NULL,
    client_ip         INET,
    hostname          VARCHAR(255),
    ssid              VARCHAR(64)  NOT NULL,
    band              VARCHAR(10)  NOT NULL,
    connected_at      TIMESTAMPTZ  NOT NULL,
    disconnected_at   TIMESTAMPTZ,
    duration_secs     INTEGER,
    total_tx_bytes    BIGINT       DEFAULT 0,
    total_rx_bytes    BIGINT       DEFAULT 0,
    avg_rssi          SMALLINT,
    min_rssi          SMALLINT,
    max_rssi          SMALLINT,
    avg_tx_rate       INTEGER,
    avg_rx_rate       INTEGER,
    disconnect_reason VARCHAR(50),
    is_11r            BOOLEAN      DEFAULT false
);

DO 
$$
BEGIN
    PERFORM create_hypertable('client_sessions', 'connected_at',
        chunk_time_interval => INTERVAL '1 day',
        if_not_exists => TRUE
    );
EXCEPTION
    WHEN OTHERS THEN
        RAISE NOTICE 'Could not create hypertable for client_sessions: %', SQLERRM;
        CREATE INDEX IF NOT EXISTS idx_client_sessions_time ON client_sessions(connected_at DESC);
END
$$
;

CREATE INDEX idx_client_sessions_device ON client_sessions(device_id, connected_at DESC);
CREATE INDEX idx_client_sessions_client ON client_sessions(client_mac, connected_at DESC);
CREATE INDEX idx_client_sessions_active ON client_sessions(device_id)
    WHERE disconnected_at IS NULL;

-- ============================================================
-- AUDIT LOG
-- ============================================================
CREATE TABLE audit_log (
    id              UUID         DEFAULT uuid_generate_v4(),
    tenant_id       UUID         NOT NULL,
    user_id         UUID,
    action          VARCHAR(100) NOT NULL,
    resource_type   VARCHAR(50)  NOT NULL,
    resource_id     UUID,
    details         JSONB,
    ip_address      INET,
    timestamp       TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

DO 
$$
BEGIN
    PERFORM create_hypertable('audit_log', 'timestamp',
        chunk_time_interval => INTERVAL '7 days',
        if_not_exists => TRUE
    );
EXCEPTION
    WHEN OTHERS THEN
        RAISE NOTICE 'Could not create hypertable for audit_log: %', SQLERRM;
        CREATE INDEX IF NOT EXISTS idx_audit_log_time ON audit_log(timestamp DESC);
END
$$
;

CREATE INDEX idx_audit_tenant ON audit_log(tenant_id, timestamp DESC);
CREATE INDEX idx_audit_user ON audit_log(user_id, timestamp DESC)
    WHERE user_id IS NOT NULL;
CREATE INDEX idx_audit_resource ON audit_log(resource_type, resource_id, timestamp DESC);

-- ============================================================
-- RETENTION POLICIES (TimescaleDB only)
-- ============================================================
DO 
$$
BEGIN
    -- Raw device metrics: keep 7 days
    PERFORM add_retention_policy('device_metrics', INTERVAL '7 days', if_not_exists => TRUE);

    -- Raw radio metrics: keep 7 days
    PERFORM add_retention_policy('radio_metrics', INTERVAL '7 days', if_not_exists => TRUE);

    -- Device events: keep 90 days
    PERFORM add_retention_policy('device_events', INTERVAL '90 days', if_not_exists => TRUE);

    -- Client sessions: keep 90 days
    PERFORM add_retention_policy('client_sessions', INTERVAL '90 days', if_not_exists => TRUE);

    -- Audit log: keep 365 days
    PERFORM add_retention_policy('audit_log', INTERVAL '365 days', if_not_exists => TRUE);
EXCEPTION
    WHEN OTHERS THEN
        RAISE NOTICE 'Could not create retention policies (TimescaleDB required): %', SQLERRM;
END
$$
;

-- ============================================================
-- CONTINUOUS AGGREGATES (TimescaleDB only)
-- ============================================================
DO 
$$
BEGIN
    -- Hourly device metrics
    EXECUTE $agg$
        CREATE MATERIALIZED VIEW IF NOT EXISTS device_metrics_hourly
        WITH (timescaledb.continuous) AS
        SELECT
            time_bucket('1 hour', time) AS bucket,
            device_id,
            tenant_id,
            AVG(cpu_usage)                                         AS avg_cpu,
            MAX(cpu_usage)                                         AS max_cpu,
            AVG(memory_used::float / NULLIF(memory_total, 0) * 100) AS avg_mem_pct,
            MAX(client_count)                                      AS max_clients,
            AVG(client_count)                                      AS avg_clients
        FROM device_metrics
        GROUP BY bucket, device_id, tenant_id
        WITH NO DATA
    $agg$;

    PERFORM add_continuous_aggregate_policy('device_metrics_hourly',
        start_offset    => INTERVAL '3 hours',
        end_offset      => INTERVAL '1 hour',
        schedule_interval => INTERVAL '1 hour',
        if_not_exists   => TRUE
    );

    -- Hourly radio metrics
    EXECUTE $agg$
        CREATE MATERIALIZED VIEW IF NOT EXISTS radio_metrics_hourly
        WITH (timescaledb.continuous) AS
        SELECT
            time_bucket('1 hour', time) AS bucket,
            device_id,
            tenant_id,
            band,
            AVG(utilization)   AS avg_utilization,
            MAX(utilization)   AS max_utilization,
            MAX(client_count)  AS max_clients,
            SUM(tx_bytes)      AS total_tx_bytes,
            SUM(rx_bytes)      AS total_rx_bytes,
            SUM(tx_retries)    AS total_retries,
            AVG(noise_floor)   AS avg_noise_floor
        FROM radio_metrics
        GROUP BY bucket, device_id, tenant_id, band
        WITH NO DATA
    $agg$;

    PERFORM add_continuous_aggregate_policy('radio_metrics_hourly',
        start_offset    => INTERVAL '3 hours',
        end_offset      => INTERVAL '1 hour',
        schedule_interval => INTERVAL '1 hour',
        if_not_exists   => TRUE
    );

EXCEPTION
    WHEN OTHERS THEN
        RAISE NOTICE 'Could not create continuous aggregates (TimescaleDB required): %', SQLERRM;
END
$$
;
