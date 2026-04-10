
-- ============================================================
-- Migration 000002: Devices and Config Tables
-- ============================================================

-- ============================================================
-- DEVICES
-- ============================================================
CREATE TABLE devices (
    id                      UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id               UUID         NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    site_id                 UUID         REFERENCES sites(id) ON DELETE SET NULL,
    mac                     VARCHAR(17)  NOT NULL,
    serial                  VARCHAR(64)  NOT NULL DEFAULT '',
    name                    VARCHAR(255) NOT NULL DEFAULT '',
    model                   VARCHAR(100) NOT NULL DEFAULT '',
    status                  VARCHAR(20)  NOT NULL DEFAULT 'pending_adopt'
                            CHECK (status IN (
                                'pending_adopt', 'adopting', 'provisioning',
                                'online', 'offline', 'upgrading',
                                'config_pending', 'error', 'decommissioned'
                            )),
    firmware_version        VARCHAR(50)  NOT NULL DEFAULT '',
    target_firmware         VARCHAR(50),
    ip_address              INET,
    public_ip               INET,

    -- Config tracking
    desired_config_version  BIGINT       NOT NULL DEFAULT 0,
    applied_config_version  BIGINT       NOT NULL DEFAULT 0,

    -- Auth
    device_token_hash       VARCHAR(64),

    -- Runtime info
    uptime                  BIGINT       NOT NULL DEFAULT 0,
    last_seen               TIMESTAMPTZ,
    adopted_at              TIMESTAMPTZ,
    last_config_applied     TIMESTAMPTZ,

    -- Device info
    capabilities            JSONB        NOT NULL DEFAULT '{}',
    system_info             JSONB        NOT NULL DEFAULT '{}',
    tags                    TEXT[]        DEFAULT '{}',
    notes                   TEXT         NOT NULL DEFAULT '',

    created_at              TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at              TIMESTAMPTZ  NOT NULL DEFAULT NOW(),

    UNIQUE(mac)
);

CREATE INDEX idx_devices_tenant ON devices(tenant_id);
CREATE INDEX idx_devices_site ON devices(site_id) WHERE site_id IS NOT NULL;
CREATE INDEX idx_devices_status ON devices(tenant_id, status);
CREATE INDEX idx_devices_mac ON devices(mac);
CREATE INDEX idx_devices_token ON devices(device_token_hash)
    WHERE device_token_hash IS NOT NULL;
CREATE INDEX idx_devices_pending ON devices(tenant_id)
    WHERE status = 'pending_adopt';
CREATE INDEX idx_devices_config_drift ON devices(tenant_id)
    WHERE desired_config_version > applied_config_version;

CREATE TRIGGER update_devices_updated_at
    BEFORE UPDATE ON devices
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

-- ============================================================
-- CONFIG TEMPLATES (per site)
-- ============================================================
CREATE TABLE config_templates (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id       UUID         NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    site_id         UUID         NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    version         BIGINT       NOT NULL DEFAULT 1,
    config          JSONB        NOT NULL,
    description     TEXT         NOT NULL DEFAULT '',
    created_by      UUID         REFERENCES users(id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),

    UNIQUE(site_id, version)
);

CREATE INDEX idx_config_templates_site ON config_templates(site_id, version DESC);

-- ============================================================
-- DEVICE CONFIGS (per device config history)
-- ============================================================
CREATE TABLE device_configs (
    id               UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id        UUID         NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    device_id        UUID         NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    version          BIGINT       NOT NULL,
    config           JSONB        NOT NULL,
    source           VARCHAR(20)  NOT NULL DEFAULT 'template'
                     CHECK (source IN ('template', 'override', 'manual', 'rollback')),
    template_version BIGINT,
    device_overrides JSONB,
    status           VARCHAR(20)  NOT NULL DEFAULT 'pending'
                     CHECK (status IN ('pending', 'pushed', 'applied', 'failed', 'rolled_back')),
    error_message    TEXT,
    created_by       UUID         REFERENCES users(id) ON DELETE SET NULL,
    pushed_at        TIMESTAMPTZ,
    applied_at       TIMESTAMPTZ,
    created_at       TIMESTAMPTZ  NOT NULL DEFAULT NOW(),

    UNIQUE(device_id, version)
);

CREATE INDEX idx_device_configs_device ON device_configs(device_id, version DESC);
CREATE INDEX idx_device_configs_status ON device_configs(device_id, status);

-- ============================================================
-- DEVICE OVERRIDES (per device config overrides)
-- ============================================================
CREATE TABLE device_overrides (
    device_id       UUID PRIMARY KEY REFERENCES devices(id) ON DELETE CASCADE,
    tenant_id       UUID         NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    overrides       JSONB        NOT NULL DEFAULT '{}',
    updated_by      UUID         REFERENCES users(id) ON DELETE SET NULL,
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE TRIGGER update_device_overrides_updated_at
    BEFORE UPDATE ON device_overrides
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

-- ============================================================
-- FIRMWARE
-- ============================================================
CREATE TABLE firmware (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    version         VARCHAR(50)  NOT NULL,
    model           VARCHAR(100) NOT NULL,
    filename        VARCHAR(255) NOT NULL,
    size            BIGINT       NOT NULL,
    sha256          VARCHAR(64)  NOT NULL,
    storage_path    VARCHAR(512) NOT NULL,
    release_notes   TEXT         NOT NULL DEFAULT '',
    channel         VARCHAR(20)  NOT NULL DEFAULT 'stable'
                    CHECK (channel IN ('stable', 'beta', 'nightly')),
    min_version     VARCHAR(50),
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),

    UNIQUE(version, model)
);

CREATE INDEX idx_firmware_model ON firmware(model, channel, version DESC);

-- ============================================================
-- FIRMWARE UPGRADE TASKS
-- ============================================================
CREATE TABLE firmware_upgrade_tasks (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id       UUID         NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    device_id       UUID         NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    firmware_id     UUID         NOT NULL REFERENCES firmware(id),
    status          VARCHAR(20)  NOT NULL DEFAULT 'pending'
                    CHECK (status IN (
                        'pending', 'queued', 'downloading',
                        'installing', 'rebooting', 'complete', 'failed'
                    )),
    progress        INTEGER      NOT NULL DEFAULT 0,
    error_message   TEXT,
    scheduled_at    TIMESTAMPTZ,
    started_at      TIMESTAMPTZ,
    completed_at    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_fw_tasks_device ON firmware_upgrade_tasks(device_id, created_at DESC);
CREATE INDEX idx_fw_tasks_active ON firmware_upgrade_tasks(status)
    WHERE status NOT IN ('complete', 'failed');

-- ============================================================
-- COMMAND QUEUE
-- ============================================================
CREATE TABLE command_queue (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id       UUID         NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    device_id       UUID         NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    command_type    VARCHAR(50)  NOT NULL,
    payload         JSONB        NOT NULL DEFAULT '{}',
    status          VARCHAR(20)  NOT NULL DEFAULT 'queued'
                    CHECK (status IN ('queued', 'sent', 'acked', 'completed', 'failed', 'expired')),
    priority        INTEGER      NOT NULL DEFAULT 5,
    max_retries     INTEGER      NOT NULL DEFAULT 3,
    retry_count     INTEGER      NOT NULL DEFAULT 0,
    correlation_id  VARCHAR(64),
    error_message   TEXT,
    expires_at      TIMESTAMPTZ,
    sent_at         TIMESTAMPTZ,
    acked_at        TIMESTAMPTZ,
    completed_at    TIMESTAMPTZ,
    created_by      UUID         REFERENCES users(id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_cmd_queue_device_pending ON command_queue(device_id, priority, created_at)
    WHERE status = 'queued';
CREATE INDEX idx_cmd_queue_inflight ON command_queue(correlation_id)
    WHERE status = 'sent';
CREATE INDEX idx_cmd_queue_cleanup ON command_queue(status, created_at)
    WHERE status IN ('completed', 'failed', 'expired');
