-- ============================================================
-- Migration 000004: Row-Level Security + Super Admin Role
-- Phase 2: Multi-tenant enforcement layer
-- ============================================================

-- ============================================================
-- 1. ADD SUPER_ADMIN ROLE
-- ============================================================

-- Drop and recreate the role check constraint on users
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_role_check;
ALTER TABLE users ADD CONSTRAINT users_role_check
    CHECK (role IN ('super_admin', 'admin', 'operator', 'viewer'));

-- ============================================================
-- 2. ADD MAX_USERS LIMIT TO TENANTS
-- ============================================================
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS max_users INTEGER NOT NULL DEFAULT 50;

-- ============================================================
-- 3. ROW-LEVEL SECURITY POLICIES
-- ============================================================

-- Enable RLS on all tenant-scoped tables
ALTER TABLE tenants ENABLE ROW LEVEL SECURITY;
ALTER TABLE users ENABLE ROW LEVEL SECURITY;
ALTER TABLE sites ENABLE ROW LEVEL SECURITY;
ALTER TABLE devices ENABLE ROW LEVEL SECURITY;
ALTER TABLE device_configs ENABLE ROW LEVEL SECURITY;
ALTER TABLE device_overrides ENABLE ROW LEVEL SECURITY;
ALTER TABLE config_templates ENABLE ROW LEVEL SECURITY;
ALTER TABLE command_queue ENABLE ROW LEVEL SECURITY;
ALTER TABLE firmware_upgrade_tasks ENABLE ROW LEVEL SECURITY;

-- Note: device_events, device_metrics, radio_metrics, client_sessions, audit_log
-- are hypertables — RLS on hypertables requires TimescaleDB 2.9+.
-- We enforce tenant isolation at the application layer for these.

-- ────────────────────────────────────────────────────────────
-- RLS POLICIES
-- Uses current_setting('app.current_tenant') set per-transaction
-- ────────────────────────────────────────────────────────────

-- Helper: superusers (e.g., migration runner) bypass RLS automatically.
-- The application user connects with a non-superuser role.

-- TENANTS: super_admin sees all, others see only their own
CREATE POLICY tenant_isolation ON tenants
    USING (
        current_setting('app.current_tenant', true) = 'super_admin'
        OR id = current_setting('app.current_tenant', true)::uuid
    );

-- USERS: scoped to tenant_id
CREATE POLICY user_tenant_isolation ON users
    USING (
        current_setting('app.current_tenant', true) = 'super_admin'
        OR tenant_id = current_setting('app.current_tenant', true)::uuid
    );

-- SITES: scoped to tenant_id
CREATE POLICY site_tenant_isolation ON sites
    USING (
        current_setting('app.current_tenant', true) = 'super_admin'
        OR tenant_id = current_setting('app.current_tenant', true)::uuid
    );

-- DEVICES: scoped to tenant_id
CREATE POLICY device_tenant_isolation ON devices
    USING (
        current_setting('app.current_tenant', true) = 'super_admin'
        OR tenant_id = current_setting('app.current_tenant', true)::uuid
    );

-- DEVICE_CONFIGS: scoped to tenant_id
CREATE POLICY device_config_tenant_isolation ON device_configs
    USING (
        current_setting('app.current_tenant', true) = 'super_admin'
        OR tenant_id = current_setting('app.current_tenant', true)::uuid
    );

-- DEVICE_OVERRIDES: scoped to tenant_id
CREATE POLICY device_override_tenant_isolation ON device_overrides
    USING (
        current_setting('app.current_tenant', true) = 'super_admin'
        OR tenant_id = current_setting('app.current_tenant', true)::uuid
    );

-- CONFIG_TEMPLATES: scoped to tenant_id
CREATE POLICY config_template_tenant_isolation ON config_templates
    USING (
        current_setting('app.current_tenant', true) = 'super_admin'
        OR tenant_id = current_setting('app.current_tenant', true)::uuid
    );

-- COMMAND_QUEUE: scoped to tenant_id
CREATE POLICY command_queue_tenant_isolation ON command_queue
    USING (
        current_setting('app.current_tenant', true) = 'super_admin'
        OR tenant_id = current_setting('app.current_tenant', true)::uuid
    );

-- FIRMWARE_UPGRADE_TASKS: scoped to tenant_id
CREATE POLICY firmware_task_tenant_isolation ON firmware_upgrade_tasks
    USING (
        current_setting('app.current_tenant', true) = 'super_admin'
        OR tenant_id = current_setting('app.current_tenant', true)::uuid
    );

-- ============================================================
-- 4. INDEX FOR TENANT LIMIT QUERIES
-- ============================================================
CREATE INDEX IF NOT EXISTS idx_users_tenant_count ON users(tenant_id) WHERE active = true;
CREATE INDEX IF NOT EXISTS idx_devices_tenant_count ON devices(tenant_id) WHERE status != 'decommissioned';
